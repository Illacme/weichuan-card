package license

import (
	"context"
	"time"

	"weichuan/internal/setting"
)

// DBResolver 从系统设置读取**服务商自己**的平台授权，集中 / 平台模式用。
//
// 它与 TenantDBResolver 不是同一份数据：后者按 companyId 读 tenant_license 表，
// 管的是「客户企业的席位与到期」；这里管的是服务商自己的授权到期提醒。
// 四个 license_* 键已正式登记进 setting.registry，界面可改、提醒也读得到。
//
// 全留空时返回宽松授权（不过期、不限席位）：平台模式不强制，只做提醒。
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
