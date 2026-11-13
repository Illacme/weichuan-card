package model

import "time"

// StorageBackfillRef 是 P3 存量回填的**台账**：一行 = 一条被改写的资源引用。
//
// 它存在的唯一理由是回滚。old_value 记的是改库前那一格的真实值（数组列则是
// 该下标上的元素），没有它就无法把库搬回去。因此它与业务行的改写必须放在
// 同一个事务里——见 migrations/0045 的表注释。
//
// 刻意不记对象复制状态：目标对象在不在、大小对不对，问对象存储本身比问台账准。
// 台账只记「我们改过库里的哪一格」，这两件事的真相来源不同，别混在一起。
type StorageBackfillRef struct {
	ID       int64  `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	PlanHash string `gorm:"column:plan_hash;size:64;uniqueIndex:uk_backfill_refs_plan_ref,priority:1" json:"planHash"`
	// RefID 是引用身份的 sha256，由表 / 主键列 / 主键值 / 列名 / 数组下标算出。
	// 用哈希而不是六列联合唯一键：联合键在 utf8mb4 下要占上千字节的索引空间，
	// 而这里要的只是「同一条引用不记两次」。
	RefID string `gorm:"column:ref_id;size:64;uniqueIndex:uk_backfill_refs_plan_ref,priority:2" json:"refId"`

	// RefTable / PKCol / RefCol 只用于人排查，执行器**绝不拿它们拼 SQL**：
	// 拼 SQL 用的列名一律从 refmigrate.Inventory() 的代码常量来。
	RefTable string `gorm:"column:table_name;size:64" json:"tableName"`
	PKCol    string `gorm:"column:pk_col;size:64" json:"pkCol"`
	PKValue  int64  `gorm:"column:pk_value" json:"pkValue"`
	RefCol   string `gorm:"column:ref_col;size:64" json:"refCol"`
	// ArrayIndex -1 = 单值列；>= 0 = JSON 数组里的第几个元素。
	ArrayIndex int    `gorm:"column:array_index" json:"arrayIndex"`
	OldValue   string `gorm:"column:old_value;type:text" json:"oldValue"`
	NewValue   string `gorm:"column:new_value;size:512" json:"newValue"`
	CompanyID  int64  `gorm:"column:company_id" json:"companyId"`
	Status     string `gorm:"column:status;size:16" json:"status"`

	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 显式指定，与 migrations/0045 对齐（migrations_guard_test.go 双向对账）。
func (StorageBackfillRef) TableName() string { return "storage_backfill_refs" }
