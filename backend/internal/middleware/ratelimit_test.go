package middleware

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLoginRateLimiter(t *testing.T) {
	lim := NewLoginRateLimiter(func() int { return 3 }, 3, time.Minute)

	// 同一 IP 前 3 次放行，第 4 次被拒
	require.True(t, lim.allow("10.0.0.1"))
	require.True(t, lim.allow("10.0.0.1"))
	require.True(t, lim.allow("10.0.0.1"))
	require.False(t, lim.allow("10.0.0.1"), "超过上限应被拒绝")

	// 不同 IP 互不影响
	require.True(t, lim.allow("10.0.0.2"))
}

func TestLoginRateLimiterDefaults(t *testing.T) {
	// 非法参数应回退到安全默认值，不 panic 且能正常工作
	lim := NewLoginRateLimiter(nil, 0, 0)
	require.True(t, lim.allow("ip"))
	require.NotNil(t, lim)
	require.Equal(t, 10, lim.limit(), "fallback 非法时应回退到内建缺省 10")
}

// TestLoginRateLimiterLimitFollowsProvider 上限由 limitFn 每次判定前取，
// 而不是构造时定死——否则后台改了限流阈值必须重启才生效。
func TestLoginRateLimiterLimitFollowsProvider(t *testing.T) {
	cur := 2
	lim := NewLoginRateLimiter(func() int { return cur }, 10, time.Minute)

	// 上限 2：第三次被拒
	require.True(t, lim.allow("ip"))
	require.True(t, lim.allow("ip"))
	require.False(t, lim.allow("ip"))

	// 后台把阈值调到 5：无需重建限流器，立刻放行
	cur = 5
	require.True(t, lim.allow("ip"))
}

// TestLoginRateLimiterInvalidLimitFallsBack 界面把阈值填成 0 / 负数时，
// 必须回落兜底值而不是「限流失效」（把防爆破关掉）或「全部拒绝」（把自己锁在外面）。
func TestLoginRateLimiterInvalidLimitFallsBack(t *testing.T) {
	lim := NewLoginRateLimiter(func() int { return 0 }, 7, time.Minute)
	require.Equal(t, 7, lim.limit())
	require.True(t, lim.allow("ip"))

	lim2 := NewLoginRateLimiter(func() int { return -3 }, 7, time.Minute)
	require.Equal(t, 7, lim2.limit())
}

// TestRateLimiterSweepsExpiredBuckets 过期桶必须被回收，否则 map 单调增长。
//
// 桶只在「同一个 key 再次访问」时才会被覆盖，一个 IP 只要不再来、它的过期桶就
// 永久留在表里。公网服务每天遇到成百上千个新 IP，几周就能攒出一个可观的常驻内存，
// 而且没有任何报错——只有 OOM 那天才发现。
func TestRateLimiterSweepsExpiredBuckets(t *testing.T) {
	lim := NewLoginRateLimiter(func() int { return 100 }, 100, 30*time.Millisecond)

	// 50 个「来过一次就不再出现」的 key：正是真实流量里最常见的那一类
	for i := 0; i < 50; i++ {
		lim.allow("192.0.2." + string(rune('0'+i%10)) + string(rune('a'+i%26)))
	}
	lim.mu.Lock()
	before := len(lim.buckets)
	lim.mu.Unlock()
	require.Equal(t, 50, before, "50 个 key 各占一个桶")

	// 等窗口过期，再触发一次任意 key 的判定 → 应顺带清理
	time.Sleep(40 * time.Millisecond)
	lim.allow("trigger-key")

	lim.mu.Lock()
	after := len(lim.buckets)
	lim.mu.Unlock()
	require.Equal(t, 1, after, "过期桶应被清掉，只剩刚写入的那一个")
}

// TestRateLimiterKeepsLiveBuckets 清理不能顺手把还在窗口内的桶删了，
// 否则限流会被绕过（计数归零 = 又能再试满一轮）。
func TestRateLimiterKeepsLiveBuckets(t *testing.T) {
	lim := NewLoginRateLimiter(func() int { return 2 }, 2, 50*time.Millisecond)

	require.True(t, lim.allow("10.0.0.9"))  // count=1
	require.True(t, lim.allow("10.0.0.9"))  // count=2，撞上限
	require.False(t, lim.allow("10.0.0.9")) // 窗口内，第 3 次仍应被拒

	// 触发一次清理（用另一个 key，且它自己的窗口足够长不会过期）
	lim.sweepEvery = 30 * time.Millisecond
	lim.allow("other-key")

	// 原 key 的计数必须还在：仍被拒，而不是「清理后归零」
	require.False(t, lim.allow("10.0.0.9"), "清理不得删掉窗口内的桶，否则限流被绕过")
}

// TestRateLimiterSweepDoesNotResetCount 直接压 sweep：过期与未过期混在一起时，
// 只删过期的。上面两个用例已覆盖主路径，这条守住「边界条件」这一半。
func TestRateLimiterSweepDoesNotResetCount(t *testing.T) {
	lim := NewLoginRateLimiter(func() int { return 100 }, 100, 20*time.Millisecond)
	lim.allow("keep")
	lim.allow("drop")
	time.Sleep(30 * time.Millisecond)
	lim.allow("keep") // 同一 key 再次访问 → 计数重置为 1，窗口也刷新，仍算「活着」

	lim.mu.Lock()
	lim.sweep(time.Now())
	keys := make([]string, 0, len(lim.buckets))
	for k := range lim.buckets {
		keys = append(keys, k)
	}
	lim.mu.Unlock()

	require.Len(t, keys, 1, "只该剩刚刷新过窗口的那个桶")
	require.Equal(t, "keep", keys[0])
}

// ── 跨副本共享计数（WithCounter）──

// fakeCounter 可控的共享计数后端：calls 记录被调用次数，n 是返回值，err 是错误。
type fakeCounter struct {
	calls int
	n     int
	err   error
	// seen 记录每次收到的 key，用来验证「key 传对了」
	seen []string
}

func (f *fakeCounter) Hit(key string, _ time.Duration) (int, error) {
	f.calls++
	f.seen = append(f.seen, key)
	return f.n, f.err
}

// 挂了共享计数时，判定必须以它为准——这才是「多副本下阈值不翻倍」的那一半。
func TestRateLimiterUsesSharedCounter(t *testing.T) {
	fc := &fakeCounter{n: 3}
	lim := NewLoginRateLimiter(func() int { return 10 }, 10, time.Minute).WithCounter(fc)

	require.True(t, lim.allow("1.2.3.4"))
	require.Equal(t, 1, fc.calls, "判定应走共享计数")
	require.Equal(t, []string{"1.2.3.4"}, fc.seen, "key 要原样传给共享计数")

	// 共享计数超过上限 → 拒绝（即使进程内一次都没记过）
	fc.n = 11
	require.False(t, lim.allow("1.2.3.4"), "应以共享计数为准，而不是进程内那份")
}

// counter 出错时必须降级到进程内，**绝不能直接放行**。
//
// 这条守的是最坏情况：共享存储故障时若放行，限流整体消失，而那正是最可能被盯上的
// 时刻（攻击者不需要打垮存储，只要等到它抖动）。进程内计数此时充当兜底，
// 至少还能挡住来自同一个副本的高频尝试。
func TestRateLimiterFallsBackToLocalOnCounterError(t *testing.T) {
	fc := &fakeCounter{err: errors.New("db down")}
	lim := NewLoginRateLimiter(func() int { return 2 }, 2, time.Minute).WithCounter(fc)

	require.True(t, lim.allow("1.2.3.4"), "降级后第一次仍应放行")
	require.True(t, lim.allow("1.2.3.4"), "第二次放行")
	require.False(t, lim.allow("1.2.3.4"), "存储故障不该让限流失效：第三次应被进程内兜底拦住")
	require.Equal(t, 3, fc.calls, "每次仍应尝试共享计数（故障恢复后自动回到共享口径）")
}

// 共享计数生效期间，进程内也要记账——为的是存储故障、降级到进程内的那一刻
// 计数不会从零开始，否则一次存储抖动就能换来一整轮免费尝试。
func TestRateLimiterKeepsLocalCountAlongsideShared(t *testing.T) {
	fc := &fakeCounter{n: 5} // 共享口径：已经 5 次了
	lim := NewLoginRateLimiter(func() int { return 10 }, 10, time.Minute).WithCounter(fc)

	for i := 0; i < 5; i++ {
		lim.allow("1.2.3.4")
	}

	// 存储此刻故障：降级到进程内。若期间没记账，这里会从 0 开始
	// （等于白送 10 次）；记了账则应剩 10-5=5 次
	fc.err = errors.New("db down")
	for i := 0; i < 5; i++ {
		require.True(t, lim.allow("1.2.3.4"), "前 5 次放行（进程内已有 5 次）")
	}
	require.False(t, lim.allow("1.2.3.4"), "第 6 次应被拦：进程内计数接着共享口径，而不是重新开始")
}

// WithCounter(nil) 不应把已有 counter 冲掉（链式调用的常见写法）。
func TestRateLimiterWithCounterIgnoresNil(t *testing.T) {
	fc := &fakeCounter{n: 1}
	lim := NewLoginRateLimiter(func() int { return 10 }, 10, time.Minute).WithCounter(fc).WithCounter(nil)
	lim.allow("k")
	require.Equal(t, 1, fc.calls, "传 nil 不该覆盖已挂上的 counter")
}

// 共用同一张计数表（rate_limit_hits）的多个限流器，桶键必须带动作前缀各自分家。
// 生产上 login/refresh/track 三个限流器都挂 DB 计数：若键都退化成裸 IP，
// 它们会挤进同一行互相挤占额度——访客埋点刷够 10 条/分就能把同 IP 的登录
// 打到 429（办公网 NAT 共享出口 IP 时完全可达，2026-10-23 实测抓过：
// 现象是「登录莫名被限流」，而代码里两个限流器看起来毫不相干）。
func TestRateLimiterKeyPrefixSeparatesBuckets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeCounter{n: 1}

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Request.RemoteAddr = "1.2.3.4:5678"
		return c
	}
	login := NewLoginRateLimiter(func() int { return 10 }, 10, time.Minute).
		WithKey(func(c *gin.Context) string { return "login:" + c.ClientIP() }).WithCounter(fc)
	track := NewRateLimiter(func() int { return 60 }, 60, time.Minute).
		WithKey(func(c *gin.Context) string { return "track:" + c.ClientIP() }).WithCounter(fc)

	login.Handler()(newCtx())
	track.Handler()(newCtx())

	require.Equal(t, []string{"login:1.2.3.4", "track:1.2.3.4"}, fc.seen,
		"键必须带各自的动作前缀，否则在共享计数表里挤同一行、互相挤占额度")
}
