package model

import "time"

// StorageUsage 按租户累计的存储用量（上传字节数与文件数）。
//
// 为什么要单独一张累计表，而不是每次 SUM 一遍文件大小：
//   - 对象存储（MinIO / OSS）里没有「对象归属哪个租户」这个元数据，只能靠名字前缀
//     或者数据库登记；本地驱动的 /uploads 更是扁平目录，反向统计无从做起；
//   - 配额检查发生在**每次上传之前**，一次 LIST/SUM 就是 O(文件数) 的开销，
//     租户一多、文件一多，上传接口会被自己拖垮。
//
// 代价（务必知悉）：**统计从本功能启用起累计**，历史文件不回溯计入。
// 这是有意的取舍——历史文件早已无法可靠归属到租户，硬凑一个估算值
// （比如按 DB 里引用的路径去 stat）会比"没数字"更容易误导人。
// 后台界面必须把「统计起点」写清楚，别让人以为这是磁盘真实占用。
//
// company_id 作主键：一个租户一行，配额检查就是一次主键查询。
// company_id = 0 不作为合法值（平台自身的素材库/小程序码不归属任何租户，不计量）。
type StorageUsage struct {
	CompanyID int64 `gorm:"column:company_id;primaryKey;autoIncrement:false" json:"companyId"`
	// Bytes 累计上传字节数（按上传时的文件大小计；剥 EXIF / 压缩后的落盘大小可能更小，
	// 这里刻意取**偏大**的口径：配额是成本封顶，宁可早一点提示也不要低估）。
	Bytes int64 `gorm:"column:bytes" json:"bytes"`
	// Files 累计上传文件数（按次计，覆盖写不扣减——统计口径是"上传量"而非"存量"）。
	Files int64 `gorm:"column:files" json:"files"`
	// QuotaMB 该租户的配额覆盖（MB）。0 = 跟随全局设置 storage_quota_mb；
	// 给个别客户单独放大/收紧时填这里，不动全局。
	QuotaMB   int64     `gorm:"column:quota_mb" json:"quotaMb"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 显式指定单数表名，与 migrations/0032 对齐（见 migrations_guard_test.go）。
func (StorageUsage) TableName() string { return "storage_usage" }
