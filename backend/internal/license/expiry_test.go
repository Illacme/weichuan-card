package license

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 到期状态是「提醒」的唯一判据，算错一天就会让客户在最后一天才发现——
// 或者反过来，还剩半年就开始天天弹提示。这里把边界全部钉死。
func TestStatusOfLevels(t *testing.T) {
	// 固定「今天」为 2026-11-02 09:00（本地时区），避免用 time.Now() 让断言随日期漂移
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	day := func(n int) time.Time {
		return time.Date(2026, 11, 2+n, 23, 59, 59, 0, time.Local)
	}

	cases := []struct {
		name      string
		expires   time.Time
		level     string
		daysLeft  int
		expired   bool
		attention bool
	}{
		{"不过期（零值）", time.Time{}, ExpiryLevelNone, 0, false, false},
		{"还剩半年", day(180), ExpiryLevelOK, 180, false, false},
		{"31 天：刚好在提醒窗口外", day(31), ExpiryLevelOK, 31, false, false},
		{"30 天：进入提醒窗口", day(30), ExpiryLevelWarn, 30, false, true},
		{"8 天：仍在 warn", day(8), ExpiryLevelWarn, 8, false, true},
		{"7 天：转 urgent", day(7), ExpiryLevelUrgent, 7, false, true},
		{"1 天：明天到期", day(1), ExpiryLevelUrgent, 1, false, true},
		{"0 天：今天 23:59:59 到期，此刻还没过期", day(0), ExpiryLevelUrgent, 0, false, true},
		{"昨天 23:59:59 已过", day(-1), ExpiryLevelExpired, -1, true, true},
		{"一个月前", day(-30), ExpiryLevelExpired, -30, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := StatusOf(&Entitlement{ExpiresAt: c.expires}, now)
			require.Equal(t, c.level, st.Level, "level")
			require.Equal(t, c.daysLeft, st.DaysLeft, "daysLeft")
			require.Equal(t, c.expired, st.Expired, "expired")
			require.Equal(t, c.attention, st.NeedsAttention(), "needsAttention")
		})
	}
}

// nil 授权 = 未配置 = 不限制，绝不能算成「已过期」（那会让全站只读）。
func TestStatusOfNilIsNotExpired(t *testing.T) {
	st := StatusOf(nil, time.Now())
	require.Equal(t, ExpiryLevelNone, st.Level)
	require.False(t, st.Expired)
	require.False(t, st.NeedsAttention())
	require.Equal(t, "", st.ExpiresAtDate(), "不过期时不该出现 0001-01-01")
}

// DaysBetween 必须按日历天算：到期时间统一是 23:59:59，若用 Hours()/24，
// 上午看会得到 0.6 天——四舍五入成 1，客户理解成「还有一天」实际当天就停服。
func TestDaysBetweenUsesCalendarDays(t *testing.T) {
	exp := time.Date(2026, 11, 9, 23, 59, 59, 0, time.Local)

	// 同一天上午：0 天
	require.Equal(t, 0, DaysBetween(time.Date(2026, 11, 9, 9, 0, 0, 0, time.Local), exp))
	// 前一天深夜：1 天（哪怕只差 1 小时零 1 秒）
	require.Equal(t, 1, DaysBetween(time.Date(2026, 11, 8, 22, 58, 58, 0, time.Local), exp))
	// 一周前：7 天
	require.Equal(t, 7, DaysBetween(time.Date(2026, 11, 2, 0, 0, 1, 0, time.Local), exp))
	// 已过期一天：-1
	require.Equal(t, -1, DaysBetween(time.Date(2026, 11, 10, 0, 0, 1, 0, time.Local), exp))
}

// 文案要说清「哪天到期、还剩几天」，而不是让用户自己算。
func TestStatusText(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	at := func(n int) *Entitlement {
		return &Entitlement{ExpiresAt: time.Date(2026, 11, 2+n, 23, 59, 59, 0, time.Local)}
	}

	require.Contains(t, StatusOf(at(7), now).Text(), "2026-11-09")
	require.Contains(t, StatusOf(at(7), now).Text(), "仅剩 7 天")
	require.Contains(t, StatusOf(at(30), now).Text(), "还剩 30 天")
	// 最后一天：必须说「今天到期」，说「还剩 0 天」等于没说
	require.Contains(t, StatusOf(at(0), now).Text(), "今天")
	require.Contains(t, StatusOf(at(-3), now).Text(), "已于")
	require.Contains(t, StatusOf(at(-3), now).Text(), "3 天前")
	require.Equal(t, "授权不过期", StatusOf(&Entitlement{}, now).Text())
}

// 两种部署形态的后果不同：私有部署到期是「服务起不来」，比只读严重得多，
// 文案不能混用——混用的结果是客户以为还能登录看看。
func TestConsequenceDiffersByMode(t *testing.T) {
	require.Contains(t, Consequence(true), "拒绝启动")
	require.Contains(t, Consequence(false), "只读")
	require.NotContains(t, Consequence(false), "拒绝启动")
}
