package license

import (
	"context"
	"time"

	"weichuan/internal/setting"
)

// DBResolver 从系统设置读取授权（集中 / 平台模式用）。
//
// Phase 1 用 setting 作为中央来源的占位：真正的多租户 license 表在 Phase 4 落地，
// 那时把这里换成读 tenant_license 表即可，接口不变。
//
// 未配置任何 license_* 设置时返回「宽松」授权（不过期、不限席位），
// 等价于今天的行为——Phase 1 只铺地基，不做强制（强制在 Phase 2 的 Enforcement）。
type DBResolver struct{}

// Resolve 读取本次租户的授权设置。
func (DBResolver) Resolve(_ context.Context, _ int64) (*Entitlement, error) {
	e := &Entitlement{
		Edition:  setting.GetString("license_edition", "platform"),
		Seats:    setting.GetInt("license_seats", 0),
		Features: setting.GetStringSlice("license_features", nil),
	}
	if exp := setting.GetString("license_expires_at", ""); exp != "" {
		if t, err := time.Parse(time.RFC3339, exp); err == nil {
			e.ExpiresAt = t
		}
	}
	return e, nil
}

// DefaultDBResolver 构造中央来源解析器。
func DefaultDBResolver() *DBResolver { return &DBResolver{} }
