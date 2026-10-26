package model

import (
	"log"

	"gorm.io/gorm"

	"weichuan/internal/pii"
)

// Employee 的 PII 加解密钩子与存量回填。
//
// 只在 BeforeCreate 加密、AfterFind 解密：
//   - 插入路径（审批建号、后台新增员工、devserver 种子、测试）统一走 db.Create → 自动加密；
//   - 更新路径全部是显式 map（见 handler，映射到具体列名），由调用方用 pii.Encrypt +
//     pii.BlindIndex 显式写密文与索引——不挂 BeforeUpdate，避免「零值明文覆盖既有数据」
//     （GORM 的 struct Updates 只写非零字段，若此时 Name 恰好为空就会被当成清空）。
func (e *Employee) BeforeCreate(tx *gorm.DB) error {
	var err error
	if e.NameEnc, err = pii.Encrypt(e.Name); err != nil {
		return err
	}
	if e.PhoneEnc, err = pii.Encrypt(e.Phone); err != nil {
		return err
	}
	if e.Phone2Enc, err = pii.Encrypt(e.Phone2); err != nil {
		return err
	}
	if e.EmailEnc, err = pii.Encrypt(e.Email); err != nil {
		return err
	}
	if e.NicknameEnc, err = pii.Encrypt(e.Nickname); err != nil {
		return err
	}
	e.NameBidx = pii.BlindIndex(e.Name)
	e.PhoneBidx = pii.BlindIndex(e.Phone)
	e.Phone2Bidx = pii.BlindIndex(e.Phone2)
	e.EmailBidx = pii.BlindIndex(e.Email)
	e.NicknameBidx = pii.BlindIndex(e.Nickname)
	return nil
}

// AfterFind 把库里的密文解密回内存明文字段。
//
// 解密失败（密钥不符 / 数据被篡改）时不把密文当明文回传：留空 + 告警，
// 避免「界面看着正常、其实是密文乱码」这种最难排查的静默错。
func (e *Employee) AfterFind(tx *gorm.DB) error {
	dec := func(col, enc string) string {
		v, err := pii.Decrypt(enc)
		if err != nil {
			log.Printf("[PII][WARN] 员工 %d 的 %s 解密失败：%v", e.StaffID, col, err)
			return ""
		}
		return v
	}
	e.Name = dec("name", e.NameEnc)
	e.Phone = dec("phone", e.PhoneEnc)
	e.Phone2 = dec("phone2", e.Phone2Enc)
	e.Email = dec("email", e.EmailEnc)
	e.Nickname = dec("nickname", e.NicknameEnc)
	return nil
}

// BackfillEmployees 把存量员工的 PII 明文加密入库并补盲索引（幂等）。
//
// 触发时机：服务启动、表结构就绪之后（SQLite 在 AutoMigrate 后，MySQL 在迁移后）。
// 只处理「非空且尚未加密」或「已加密但缺盲索引」的字段，已就绪的行不再改写，
// 因此重复执行不会反复重加密（避免每次启动都换 nonce、白白搅动数据）。
//
// 用 db.Table("employees") 直接读原始列（绕过模型钩子），从而能看见真实存储形态。
// 任何加密误差都向上返回——宁可启动时报错，也不能悄悄留着明文。
func BackfillEmployees(db *gorm.DB) error {
	if !db.Migrator().HasTable("employees") {
		return nil // 全新库（还没建表）：无需回填
	}
	var rows []struct {
		StaffID      int64
		Name         string
		Phone        string
		Phone2       string
		Email        string
		Nickname     string
		NameBidx     string
		PhoneBidx    string
		Phone2Bidx   string
		EmailBidx    string
		NicknameBidx string
	}
	if err := db.Table("employees").
		Select("staff_id, name, phone, phone2, email, nickname, " +
			"name_bidx, phone_bidx, phone2_bidx, email_bidx, nickname_bidx").
		Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		upd := map[string]any{}
		if err := backfillField(upd, "name", r.Name, r.NameBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "phone", r.Phone, r.PhoneBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "phone2", r.Phone2, r.Phone2Bidx); err != nil {
			return err
		}
		if err := backfillField(upd, "email", r.Email, r.EmailBidx); err != nil {
			return err
		}
		if err := backfillField(upd, "nickname", r.Nickname, r.NicknameBidx); err != nil {
			return err
		}
		if len(upd) == 0 {
			continue
		}
		if err := db.Table("employees").Where("staff_id = ?", r.StaffID).Updates(upd).Error; err != nil {
			return err
		}
	}
	return nil
}

// backfillField 决定单个字段是否需要回填，并按需写入 upd。
//   - 空值：跳过（不产生密文/索引）。
//   - 明文：加密 + 计算盲索引。
//   - 已是密文但缺盲索引：解密后补索引（老版本可能只加密未建索引）。
func backfillField(upd map[string]any, col, raw, bidx string) error {
	if raw == "" {
		return nil
	}
	if pii.IsEncrypted(raw) {
		if bidx != "" {
			return nil // 已就绪
		}
		plain, err := pii.Decrypt(raw)
		if err != nil {
			return err
		}
		upd[col+"_bidx"] = pii.BlindIndex(plain)
		return nil
	}
	enc, err := pii.Encrypt(raw)
	if err != nil {
		return err
	}
	upd[col] = enc
	upd[col+"_bidx"] = pii.BlindIndex(raw)
	return nil
}
