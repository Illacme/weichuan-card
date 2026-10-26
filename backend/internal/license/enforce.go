package license

// 本文件把「授权是什么」（Entitlement，见 entitlement.go）翻译成「运行期该怎么拦」。
// 全部以 license.Current() 为输入；未解析授权（测试 / 未配置部署）时一律放行，等价于现状。

// Expired 当前授权是否已过期。未解析授权时返回 false（视为有效）。
func Expired() bool {
	e := Current()
	return e != nil && e.IsExpired()
}

// SeatLimit 当前授权的席位上限；0 表示不限制。
func SeatLimit() int {
	e := Current()
	if e == nil {
		return 0
	}
	return e.Seats
}

// HasFeature 当前授权是否开通某功能。
// 未解析授权、或未声明任何 Features（nil/空）时视为全部开通（默认放行）；
// 声明了 Features 则必须显式包含该名才放行。
// feature 名由签发方与产品约定（自由字符串，如 "content_sec"、"mp_release"）。
func HasFeature(name string) bool {
	// 委派给 Entitlement.HasFeature，避免两处各写一遍判定逻辑（Phase 4 起
	// 中间件拿到的可能是「按租户解析」的授权，直接调方法即可，不经过 Current()）。
	return Current().HasFeature(name)
}
