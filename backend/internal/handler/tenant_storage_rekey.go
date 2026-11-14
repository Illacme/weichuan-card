package handler

import (
	"fmt"
	"log"

	"weichuan/internal/audit"
	"weichuan/internal/model"
	"weichuan/internal/pii"

	"gorm.io/gorm"
)

// RekeyTenantStorage 换 PII_ENC_KEY 后，对 tenant_storage_configs 的凭据做「旧密钥解密 →
// 新密钥重加密」回填，并把 key_id 刷新为当前密钥指纹（停机式 re-key 的核心步骤）。
//
// 调用方约定（见 cmd/server 的 tenant-storage-rekey 子命令）：
//   - 当前进程必须以**新密钥**装载（pii 当前密钥 = 目标密钥）；
//   - oldKeyB64 是**旧密钥**（base64 32 字节），用于解密换钥前写入的行；不提供时，
//     凡是当前密钥解不开的行都会被视为致命错误返回（避免静默丢凭据）。
//
// 处理范围：全表遍历（不仅是 key_id != 当前指纹 的行）。理由：key_id 只是标记，
// 可能标错；全量尝试解密能自愈「标记与实际不符」的行。空凭据列（已清空）直接跳过。
//
// 失败语义：单行若既不能被当前密钥解、也不能用旧密钥解，说明它属于第三个未知密钥，
// 这类行绝不能静默跳过（跳过后凭据将永久不可恢复）。函数返回已成功重加密的行数，
// 并把第一个无法处理的致命错误带出，由 CLI 中止并提示运维。
func RekeyTenantStorage(db *gorm.DB, oldKeyB64 string) (processed int, err error) {
	target := pii.CurrentKeyID()
	if target == "" {
		return 0, fmt.Errorf("当前 PII_ENC_KEY 不可用（未设置或格式错误），无法重加密；请先正确设置新密钥")
	}

	var rows []model.TenantStorageConfig
	if e := db.Find(&rows).Error; e != nil {
		return 0, fmt.Errorf("读取租户存储配置失败：%w", e)
	}

	var fatal error
	rekeyed := 0
	skipped := 0
	for i := range rows {
		row := &rows[i]
		newAK, akChanged, e := rekeyColumn(oldKeyB64, row.AccessKeyEnc)
		if e != nil {
			fatal = fmt.Errorf("企业 %d 的 AccessKey 无法解密（既非当前密钥也非旧密钥）：%w", row.CompanyID, e)
			break
		}
		newSK, skChanged, e := rekeyColumn(oldKeyB64, row.SecretKeyEnc)
		if e != nil {
			fatal = fmt.Errorf("企业 %d 的 SecretKey 无法解密（既非当前密钥也非旧密钥）：%w", row.CompanyID, e)
			break
		}
		if !akChanged && !skChanged {
			// 两列都没动：凭据为空或已是用当前密钥加密且 key_id 已正确——跳过，
			// 但仍把 key_id 校正为 target，避免「密文对、标记错」的脏数据残留。
			if row.KeyID != target {
				if u := db.Model(&model.TenantStorageConfig{}).
					Where("id = ?", row.ID).Update("key_id", target).Error; u != nil {
					log.Printf("[REKEY][WARN] 企业 %d 的 key_id 校正失败：%v", row.CompanyID, u)
				}
			}
			skipped++
			continue
		}
		if u := db.Model(&model.TenantStorageConfig{}).Where("id = ?", row.ID).
			Updates(map[string]any{
				"access_key_enc": newAK,
				"secret_key_enc": newSK,
				"key_id":         target,
			}).Error; u != nil {
			fatal = fmt.Errorf("企业 %d 重加密后写回失败：%w", row.CompanyID, u)
			break
		}
		rekeyed++
	}

	audit.Record(db, nil, audit.Entry{
		Action:     audit.ActionTenantStorageRekey,
		ActorType:  audit.ActorSystem,
		TargetType: "company_storage",
		TargetID:   "all",
		Detail: map[string]any{
			"targetKeyId": target,
			"rekeyed":     rekeyed,
			"skipped":     skipped,
			"hadOldKey":   oldKeyB64 != "",
			"fatal":       fatal != nil,
		},
	})
	if fatal != nil {
		return rekeyed, fatal
	}
	return rekeyed, nil
}

// rekeyColumn 对单列凭据密文做重加密：能解开就重新用当前密钥加密；空串原样返回、不计数。
// 返回 (新密文, 是否发生变化, 错误)。
func rekeyColumn(oldKeyB64, enc string) (string, bool, error) {
	if enc == "" {
		return "", false, nil // 空凭据：没东西可重加密
	}
	// 先试当前密钥（目标密钥）。新写入的行本就能解开，无需旧密钥。
	plain, e := pii.Decrypt(enc)
	if e == nil {
		// 当前密钥能解开：若已经是 target 指纹加密的，密文不变；
		// 但保险起见一律重新加密（随机 nonce 会让密文不同，密钥指纹一致）。
		// 是否「真的变化」由调用方通过 key_id 变化判定，这里只要保证写回的是当前密钥密文。
		re, e2 := pii.Encrypt(plain)
		return re, e2 == nil, e2
	}
	// 当前密钥解不开：若提供了旧密钥，用它再试一次。
	if oldKeyB64 != "" {
		plain, e2 := pii.DecryptWithKey(oldKeyB64, enc)
		if e2 == nil {
			re, e3 := pii.Encrypt(plain)
			return re, e3 == nil, e3
		}
		return "", false, fmt.Errorf("旧密钥也解不开：%w", e2)
	}
	return "", false, fmt.Errorf("未提供旧密钥且当前密钥解不开：%w", e)
}
