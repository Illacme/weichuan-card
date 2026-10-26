package license

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Entitlement 是「授权是什么」的统一表达，与部署形态无关。
// 两种来源（本地签名许可证 / 中央租户服务）都收敛成它，再交给 Enforcement 强制。
type Entitlement struct {
	TenantID  int64     `json:"tenantId"`
	Edition   string    `json:"edition"`          // private | platform | trial
	Seats     int       `json:"seats"`            // 员工席位上限；0 = 不限制
	ExpiresAt time.Time `json:"expiresAt"`        // 零值 = 不过期
	Features  []string  `json:"features"`         // 开通的功能开关；空 = 全部默认开启
	AppID     string    `json:"appId,omitempty"`  // 绑定小程序 appid（独立小程序防换壳用，Phase 3 启用）
	Domain    string    `json:"domain,omitempty"` // 绑定访问域名
}

// IsExpired 是否已过期（零值 ExpiresAt 视为永不过期）。
func (e *Entitlement) IsExpired() bool {
	return !e.ExpiresAt.IsZero() && time.Now().After(e.ExpiresAt)
}

// SeatsUnlimited 席位是否不限制。
func (e *Entitlement) SeatsUnlimited() bool { return e.Seats <= 0 }

// HasFeature 该授权是否开通某功能。
// 未声明任何 Features（nil / 空）视为全部开通；声明了则必须显式包含才放行。
// nil 接收者同样视为全部开通——「没解析出授权」时不应把功能关掉。
// feature 名由签发方与产品约定（自由字符串，如 "content_sec"、"mp_release"）。
func (e *Entitlement) HasFeature(name string) bool {
	if e == nil || len(e.Features) == 0 {
		return true
	}
	for _, f := range e.Features {
		if f == name {
			return true
		}
	}
	return false
}

// LicenseResolver 抽象「授权从哪来」：集中模式读库，私有模式读本地签名文件。
// 上层 Enforcement 不关心来源，只消费 Entitlement。
type LicenseResolver interface {
	Resolve(ctx context.Context, tenantID int64) (*Entitlement, error)
}

var (
	curMu   sync.RWMutex
	current *Entitlement
)

// SetCurrent 缓存本次启动解析出的授权（供 Enforcement / 接口读取）。
func SetCurrent(e *Entitlement) {
	curMu.Lock()
	defer curMu.Unlock()
	current = e
}

// Current 返回当前生效的授权；未解析过时为 nil。
func Current() *Entitlement {
	curMu.RLock()
	defer curMu.RUnlock()
	return current
}

// CurrentTenantID 返回当前生效授权绑定的租户（企业）；未绑定则为 0。
// 私有 / 独立部署的许可证通常会把 TenantID 写死为唯一企业，
// 供 EnforceLicense 在独立模式下把席位上限收敛到该单一租户。
func CurrentTenantID() int64 {
	e := Current()
	if e == nil {
		return 0
	}
	return e.TenantID
}

var (
	ErrInvalidFormat = errors.New("license: invalid format")
	ErrBadSignature  = errors.New("license: bad signature")
	ErrExpired       = errors.New("license: expired")
	ErrNotFound      = errors.New("license: file not found")
)
