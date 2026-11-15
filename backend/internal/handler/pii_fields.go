package handler

import (
	"log"

	"weichuan/internal/pii"
)

// setPII 往 GORM 的 Updates(map) 里写一个加密 PII 字段：密文列 + 盲索引列。
//
// 为什么需要它：GORM 的 map 更新（db.Model(x).Updates(map)）**不触发模型钩子**，
// 若直接把明文塞进 map，就会绕过 Employee 的加密钩子、把明文写进库——这是
// 「以为加密了、其实没有」的典型静默漏洞。所有 map 更新点都必须经过本函数。
//
// 空串按「清空」处理：Encrypt("")=""、BlindIndex("")=""，读写两侧一致。
func setPII(updates map[string]interface{}, col, plain string) error {
	enc, err := pii.Encrypt(plain)
	if err != nil {
		return err
	}
	updates[col] = enc
	updates[col+"_bidx"] = pii.BlindIndex(plain)
	return nil
}

// decPII 解密「裸 SQL 查出来、未走模型 AfterFind」的 PII 值（如统计 JOIN 带出的姓名）。
// 非密文（例如 COALESCE 回落的「已删除」占位）原样返回；真密文解密失败则留空，
// 避免把密文乱码当名字展示给运营。
//
// where 是链路标识，必须打进日志：解密失败的表现只是「统计页姓名变空白」，
// 没人会把它和「PII_ENC_KEY 换过 / 配错」联系起来。此前这里静默 return ""，
// 换钥后整张报表的姓名栏全空却一条日志都没有，属于最难查的那类静默失败。
//
// 日志只写链路 + 错误原因，**绝不写密文或明文**：日志的可见范围通常比数据库更宽，
// 把密文写进去等于给离线爆破留素材。模型层 apply_pii.go / employee_pii.go
// 的 [PII][WARN] 也是同一口径。
func decPII(where, s string) string {
	v, err := pii.Decrypt(s)
	if err != nil {
		log.Printf("[PII][WARN] %s 解密姓名失败（已留空）：%v", where, err)
		return ""
	}
	return v
}
