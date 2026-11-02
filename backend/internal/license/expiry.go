package license

import (
	"fmt"
	"math"
	"time"
)

// 授权到期提醒（P0-3）：把「还剩多久、该不该报警」收敛成一处计算。
//
// 为什么值得单独一个文件：到期天数此前散落在三处各写一遍（企业管理列表的
// `now.After(lic.ExpiresAt)`、授权弹窗的 `expired`、`Entitlement.IsExpired()`），
// 每处都只回答「过期了没有」，回答不了「还有几天」——而「还有几天」才是能
// 驱动人去做事的信息（续费、催款、扩容）。三处各算一遍的另一个后果是口径漂移：
// 一处用 UTC、一处用本地时区，同一份授权在两个页面上显示相差一天。
//
// 阈值取 30 / 7 天：
//   - 30 天：给运营留出「联系客户 → 客户走付款流程 → 财务到账」的时间。
//     少于一个月才发现，基本等于到期那天才谈续费。
//   - 7 天：进入催办节奏（此时客户还没被停服，说话还管用）。
const (
	ExpiryWarnDays   = 30
	ExpiryUrgentDays = 7
)

// 到期档位。空授权（不过期）也有自己的档位，不能与 ok 混在一起——
// 界面上「永久有效」和「还剩 300 天」是两件事，前者不该出现在提醒列表里。
const (
	ExpiryLevelNone    = "none"    // 未配置授权 / 不过期
	ExpiryLevelOK      = "ok"      // 距今 > 30 天
	ExpiryLevelWarn    = "warn"    // <= 30 天
	ExpiryLevelUrgent  = "urgent"  // <= 7 天
	ExpiryLevelExpired = "expired" // 已过期
)

// ExpiryStatus 是「这份授权还剩多久」的结构化表达，直接进 JSON 给前端用。
type ExpiryStatus struct {
	ExpiresAt time.Time `json:"expiresAt"`
	// DaysLeft 按**日历天**计（见 DaysBetween），今天到期 = 0，明天到期 = 1。
	// 已过期为负数（或 0，若过期不到一天）。
	DaysLeft int    `json:"daysLeft"`
	Expired  bool   `json:"expired"`
	Level    string `json:"level"`
}

// DaysBetween 返回两个时间相差几个**日历天**（按各自本地日期取整）。
//
// 不能用 `Hours()/24`：到期时间在 23:59:59，此刻是上午 9 点，真实差 14.99 小时，
// 除下来是 0.6 天——四舍五入成 1 天，而客户理解的「明天到期」才是 1 天。
// 直接取日期再相减，得到的才是人能对上号的那个数。
//
// 用 Round 而不是整除：夏令时切换那天只有 23 小时（或多 25 小时），
// 整除会少算一天，Round 能吸收这 1 小时的偏差。
func DaysBetween(now, t time.Time) int {
	n := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	e := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return int(math.Round(e.Sub(n).Hours() / 24))
}

// StatusOf 计算一份授权的到期状态。nil（未解析出授权）返回「不过期」档位，
// 与 Entitlement 的零值语义一致：没配授权 = 不限制，不能当成「已过期」。
func StatusOf(e *Entitlement, now time.Time) ExpiryStatus {
	if e == nil || e.ExpiresAt.IsZero() {
		return ExpiryStatus{Level: ExpiryLevelNone}
	}
	st := ExpiryStatus{
		ExpiresAt: e.ExpiresAt,
		DaysLeft:  DaysBetween(now, e.ExpiresAt),
		Expired:   e.IsExpired(),
	}
	switch {
	case st.Expired:
		st.Level = ExpiryLevelExpired
	case st.DaysLeft <= ExpiryUrgentDays:
		st.Level = ExpiryLevelUrgent
	case st.DaysLeft <= ExpiryWarnDays:
		st.Level = ExpiryLevelWarn
	default:
		st.Level = ExpiryLevelOK
	}
	return st
}

// NeedsAttention 是否属于「该提醒人」的档位（warn / urgent / expired）。
// ok 与 none 都不打扰：一个还剩半年的授权天天弹提示，只会把人训练成无视提示。
func (s ExpiryStatus) NeedsAttention() bool {
	switch s.Level {
	case ExpiryLevelWarn, ExpiryLevelUrgent, ExpiryLevelExpired:
		return true
	}
	return false
}

// ExpiresAtDate 到期日期（YYYY-MM-DD，本地时区）。不过期时返回空串——
// 调用方据此显示「不过期」而不是 0001-01-01。
func (s ExpiryStatus) ExpiresAtDate() string {
	if s.ExpiresAt.IsZero() {
		return ""
	}
	return s.ExpiresAt.Format("2006-01-02")
}

// Text 一句能直接摆在界面上的话（不含「找谁续费」——那取决于是私有部署还是 SaaS，
// 由 handler 层按部署形态追加）。
func (s ExpiryStatus) Text() string {
	switch s.Level {
	case ExpiryLevelExpired:
		if s.DaysLeft == 0 {
			return fmt.Sprintf("授权已于今天（%s）到期", s.ExpiresAtDate())
		}
		return fmt.Sprintf("授权已于 %s 到期（%d 天前）", s.ExpiresAtDate(), -s.DaysLeft)
	case ExpiryLevelUrgent:
		if s.DaysLeft <= 0 {
			// 今天 23:59:59 到期，此刻还没过期——这一天里最该说清「就是今天」
			return fmt.Sprintf("授权今天（%s）到期", s.ExpiresAtDate())
		}
		return fmt.Sprintf("授权 %s 到期，仅剩 %d 天", s.ExpiresAtDate(), s.DaysLeft)
	case ExpiryLevelWarn:
		return fmt.Sprintf("授权 %s 到期，还剩 %d 天", s.ExpiresAtDate(), s.DaysLeft)
	case ExpiryLevelOK:
		return fmt.Sprintf("授权 %s 到期", s.ExpiresAtDate())
	}
	return "授权不过期"
}

// Consequence 到期后会发生什么。分开写是因为两种部署形态的后果**严重程度不同**：
// 平台 / SaaS 是「后台变只读」，私有 / 独立部署是「服务起不来」——后者必须
// 提前说清，否则客户会在某个早晨发现网站打不开。
func Consequence(private bool) string {
	if private {
		return "到期后服务将拒绝启动（授权校验是启动硬门槛），网站与小程序会同时不可用"
	}
	return "到期后后台自动切换为只读：可以登录查看，但无法再修改内容、新增员工、发布小程序"
}
