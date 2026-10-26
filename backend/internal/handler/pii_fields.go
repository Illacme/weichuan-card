package handler

import "weichuan/internal/pii"

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
func decPII(s string) string {
	v, err := pii.Decrypt(s)
	if err != nil {
		return ""
	}
	return v
}
