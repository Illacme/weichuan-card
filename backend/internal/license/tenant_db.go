package license

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// 本文件是 Phase 4 的「多租户授权」：把授权从「全局一份」下沉到「按租户一份」。
//
// 取值链（ResolveFor）：
//   私有 / 独立部署 → Current()（单租户，data/license.key 启动时已验签）
//   否则            → tenant_license 表该租户的行 → 回退 Current()（全局设置）
//
// 关键设计：回退。没给某租户单独配就落到全局那一份，行为与 Phase 1~3 完全一致。
// 否则新增一张空表会让所有存量部署「突然开始按 0 席拦截」，属于上线即事故的改法。

var (
	tenantMu sync.RWMutex
	tenantDB *gorm.DB
)

// SetTenantDB 注入按租户解析授权用的库连接（启动序列调用；测试可不注入）。
// 未注入时 ResolveFor 退化为全局授权，等价于 Phase 1~3 行为。
func SetTenantDB(db *gorm.DB) {
	tenantMu.Lock()
	defer tenantMu.Unlock()
	tenantDB = db
}

// TenantDB 取当前注入的库连接；未注入返回 nil。
func TenantDB() *gorm.DB {
	tenantMu.RLock()
	defer tenantMu.RUnlock()
	return tenantDB
}

// TenantDBResolver 按租户读 tenant_license 表。
// 实现 LicenseResolver 接口（Resolve(ctx, tenantID)），与 FileResolver 并列。
type TenantDBResolver struct{ db *gorm.DB }

// NewTenantDBResolver 构造按租户的解析器。
func NewTenantDBResolver(db *gorm.DB) *TenantDBResolver { return &TenantDBResolver{db: db} }

// Resolve 读取该租户的授权行。
// 库连接为空 / tenantID <= 0 / 查不到该租户的行，一律返回 ErrNotFound——
// 由调用方（ResolveFor）决定回退，而不是在这里编一个「宽松授权」糊过去。
func (r *TenantDBResolver) Resolve(_ context.Context, tenantID int64) (*Entitlement, error) {
	if r == nil || r.db == nil || tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant %d", ErrNotFound, tenantID)
	}
	var row model.TenantLicense
	if err := r.db.Where("company_id = ?", tenantID).First(&row).Error; err != nil {
		return nil, fmt.Errorf("%w: tenant %d: %v", ErrNotFound, tenantID, err)
	}
	return &Entitlement{
		TenantID:  row.CompanyID,
		Edition:   row.Edition,
		Seats:     row.Seats,
		ExpiresAt: row.ExpiresAt,
		Features:  splitFeatures(row.Features),
	}, nil
}

// ResolveFor 取「本次请求」生效的授权。
//
// 这是 Phase 4 的核心：EnforceLicense 中间件改用它替代裸的 Current()，
// 于是席位上限、过期、功能开关都能按租户分别生效。
// 私有 / 独立部署仍返回全局那份（单租户形态，本来就只有一个企业）。
func ResolveFor(ctx context.Context, tenantID int64) *Entitlement {
	if PrivateMode() {
		return Current()
	}
	if db := TenantDB(); db != nil && tenantID > 0 {
		if e, err := NewTenantDBResolver(db).Resolve(ctx, tenantID); err == nil && e != nil {
			return e
		}
	}
	return Current()
}

// splitFeatures 解析「逗号分隔」的功能开关串（与 sys_setting.license_features 同格式）。
// 空串 → nil（视为全部开通，缺省放行）。
func splitFeatures(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
