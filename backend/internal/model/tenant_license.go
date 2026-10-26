package model

import "time"

// TenantLicense 按租户（企业）的授权记录（Phase 4 多租户 license 表）。
//
// 取值链：本表该租户的行 → 全局系统设置（license_*）→ 无（不过期 / 不限席位）。
// 平台模式下不给每个租户单独配，自动落到全局那一份（行为与 Phase 1~3 完全一致）；
// 需要给某个企业单独配席位 / 到期时间 / 功能开关时，插一行即可。
//
// 私有 / 独立部署不走本表——那种形态只有单一租户，授权来自 data/license.key
// 的 Ed25519 签名文件（见 internal/license），启动时验签、过期即拒绝启动。
//
// 为什么独立成表而不是加在 company 上：授权是「商业属性」，与租户主数据的
// 变更频率、权限粒度都不同；独立表将来加「签发批次 / 试用转正式 / 续费历史」
// 不必动 company。company_id 唯一：一个企业一条授权。
type TenantLicense struct {
	ID int64 `gorm:"primaryKey;autoIncrement;column:id" json:"-"`
	// CompanyID 租户（company.id）。0 不作为合法值——平台全局那份走 sys_setting。
	CompanyID int64 `gorm:"column:company_id;uniqueIndex" json:"companyId"`
	// Edition 版本标识：private | platform | trial（业务展示用，不参与强制判定）。
	Edition string `gorm:"column:edition;size:32" json:"edition"`
	// Seats 员工席位上限；0 = 不限制。
	Seats int `gorm:"column:seats" json:"seats"`
	// ExpiresAt 到期时间；零值 = 不过期。过期后该租户写操作被拦（降级只读）。
	ExpiresAt time.Time `gorm:"column:expires_at" json:"expiresAt"`
	// Features 功能开关，逗号分隔；空 = 全部开通（缺省放行）。
	Features  string    `gorm:"column:features;size:512" json:"features"`
	CreatedAt time.Time `gorm:"column:created_at" json:"-"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"-"`
}

// TableName 显式指定单数表名，与 migrations/0029 对齐（见 migrations_guard_test.go）。
func (TenantLicense) TableName() string { return "tenant_license" }
