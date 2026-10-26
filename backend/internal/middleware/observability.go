package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"weichuan/internal/logx"

	"github.com/gin-gonic/gin"
)

// ─────────────────────────────────────────────────────────────
// 可观测性三件套（零第三方依赖）：
//   1. RequestID：注入 / 透传 X-Request-Id，串联访问日志与排障；
//   2. AccessLog：每个请求一行结构化日志（方法 / 路径 / 状态 / 耗时 / IP）；
//   3. Metrics：内存计数器 + Prometheus 文本格式 /metrics（默认关闭，
//      由 METRICS_ENABLED=1 显式开启，避免公网暴露流量规模）。
// 日志走标准 log（12-factor），生产可被 journald / 日志采集直接收集；
// 形态（人读文本 / JSONL）由 logx 统一切换，见 LOG_FORMAT。
// ─────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────
// 可观测性三件套（零第三方依赖）：
//   1. RequestID：注入 / 透传 X-Request-Id，串联访问日志与排障；
//   2. AccessLog：每个请求一行结构化日志（方法 / 路径 / 状态 / 耗时 / IP）；
//   3. Metrics：内存计数器 + Prometheus 文本格式 /metrics（默认关闭，
//      由 METRICS_ENABLED=1 显式开启，避免公网暴露流量规模）。
// 日志走标准 log（12-factor），生产可被 journald / 日志采集直接收集。
// ─────────────────────────────────────────────────────────────

// RequestIDHeader 请求 ID 头（入站缺失则生成，出源与响应头均带回，便于串联排查）。
const RequestIDHeader = "X-Request-Id"

// RequestIDKey 请求 ID 在 gin.Context 中的键。
const RequestIDKey = "requestId"

// RequestID 注入请求 ID：优先沿用调用方传来的 X-Request-Id（网关 / 上游透传），
// 否则生成 8 字节随机十六进制串；同时写入响应头，便于客户端报障时引用。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(RequestIDHeader)
		if rid == "" {
			rid = newRequestID()
		}
		c.Set(RequestIDKey, rid)
		c.Writer.Header().Set(RequestIDHeader, rid)
		c.Next()
	}
}

// RequestIDOf 取出当前请求的 ID（未挂 RequestID 中间件时返回空串）。
func RequestIDOf(c *gin.Context) string {
	if v, ok := c.Get(RequestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// 熵耗尽属极端情况，退化为纳秒时间戳
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// AccessLog 访问日志：请求结束后输出一行，含状态码与耗时。
// 5xx 以 [ACCESS-5XX] 前缀标出，便于日志告警规则直接匹配。
// 注意：panic 会抛穿本中间件（c.Next() 不返回），此类 500 由链上的
// gin.Recovery 记录（含栈信息），不重复输出访问日志行。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		latency := time.Since(start).Round(time.Millisecond)
		// 走 logx 而不是直接 log.Printf：JSON 模式下 method / path / status 是
		// 可聚合的字段（「按路径统计 5xx」这条最常用），文本模式下格式与历史一致。
		logx.Access(c.Request.Method, c.Request.URL.Path, status, latency, RequestIDOf(c), c.ClientIP())
	}
}

// Metrics 轻量进程内指标收集器（无第三方依赖，单实例口径）。
// 多实例部署时各实例只反映自身流量，聚合由 Prometheus 侧完成。
type Metrics struct {
	mu       sync.Mutex
	counters map[string]int64 // key: "METHOD|statusClass"（如 "GET|2xx"）
	total    atomic.Int64
	started  time.Time
	// Enabled 运行时开关（由系统设置 metrics_enabled 驱动，见 cmd/server）。
	// 关闭时不计数、GET /metrics 返回 404——避免在公网暴露流量规模。
	// nil 表示不接入系统设置（始终启用）。
	Enabled func() bool
	// Allow 可选的进一步限制：返回 false 时 /metrics 同样按 404 处理。
	// 用于把指标端点收在内网（METRICS_ALLOW_CIDR），即便开关被误开也不至于
	// 把流量规模摊在公网上。nil 表示不额外限制。
	Allow func(c *gin.Context) bool
}

// enabled 当前是否采集指标；未接入开关时恒为 true。
func (m *Metrics) enabled() bool {
	return m.Enabled == nil || m.Enabled()
}

// NewMetrics 创建指标收集器。
func NewMetrics() *Metrics {
	return &Metrics{counters: make(map[string]int64), started: time.Now()}
}

// Middleware 计数中间件：按「方法 × 状态类（2xx/4xx/5xx…）」累计请求数。
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if !m.enabled() {
			return
		}
		class := fmt.Sprintf("%dxx", c.Writer.Status()/100)
		key := c.Request.Method + "|" + class
		m.mu.Lock()
		m.counters[key]++
		m.mu.Unlock()
		m.total.Add(1)
	}
}

// Handler 输出 Prometheus 文本格式（text/plain; version=0.0.4）指标。
func (m *Metrics) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 指标默认关闭：开关未开时 /metrics 视为不存在，避免公网探测到流量规模
		if !m.enabled() {
			c.Status(http.StatusNotFound)
			return
		}
		// 白名单同样按「不存在」处理：不返回 403 是为了不暴露「这里有个端点」。
		if m.Allow != nil && !m.Allow(c) {
			c.Status(http.StatusNotFound)
			return
		}
		m.mu.Lock()
		keys := make([]string, 0, len(m.counters))
		for k := range m.counters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("# HELP weichuan_http_requests_total HTTP requests total, grouped by method and status class\n")
		b.WriteString("# TYPE weichuan_http_requests_total counter\n")
		for _, k := range keys {
			method, class, _ := strings.Cut(k, "|")
			fmt.Fprintf(&b, "weichuan_http_requests_total{method=%q,status=%q} %d\n", method, class, m.counters[k])
		}
		total := m.total.Load()
		uptime := int64(time.Since(m.started).Seconds())
		m.mu.Unlock()

		b.WriteString("# HELP weichuan_http_requests_received_total All received HTTP requests\n")
		b.WriteString("# TYPE weichuan_http_requests_received_total counter\n")
		fmt.Fprintf(&b, "weichuan_http_requests_received_total %d\n", total)
		b.WriteString("# HELP weichuan_uptime_seconds Process uptime in seconds\n")
		b.WriteString("# TYPE weichuan_uptime_seconds gauge\n")
		fmt.Fprintf(&b, "weichuan_uptime_seconds %d\n", uptime)

		c.Data(http.StatusOK, "text/plain; version=0.0.4", []byte(b.String()))
	}
}

// ─────────────────────────────────────────────────────────────
// 来源 IP 白名单（给 /metrics 这类「只该内网访问」的端点用）
// ─────────────────────────────────────────────────────────────

// NewIPAllowlist 按 CIDR 列表构造放行判定。
//
// 两条必须说清的取舍：
//   - 列表为空 = 放行全部。/metrics 本身默认关闭，白名单是**第二道门**，
//     没配第二道门时不该顺手把第一道门也堵死（否则配了 METRICS_ENABLED=1
//     却忘了配网段的人会得到一个永远 404 的监控端点，且不知道为什么）。
//   - 任一条 CIDR 解析失败 → **整体拒绝**（fail-closed）。配了白名单却写错，
//     最坏的结果是「以为限制了其实没限制」；宁可让监控端点先不可用，
//     也不能让流量规模被公网抓到。启动时日志会点名哪条写错了。
func NewIPAllowlist(cidrs []string) func(ip string) bool {
	nets := make([]*net.IPNet, 0, len(cidrs))
	bad := make([]string, 0)
	for _, s := range cidrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
			continue
		}
		// 也接受单个 IP（没有 /掩码 的写法很常见）
		if ip := net.ParseIP(s); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		bad = append(bad, s)
	}
	for _, s := range bad {
		log.Printf("[WARN] METRICS_ALLOW_CIDR 里有无法解析的条目 %q：白名单按「全部拒绝」处理（/metrics 将一律 404）", s)
	}
	if len(bad) > 0 {
		return func(string) bool { return false }
	}
	if len(nets) == 0 {
		return func(string) bool { return true }
	}
	return func(ip string) bool {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return false
		}
		for _, n := range nets {
			if n.Contains(parsed) {
				return true
			}
		}
		return false
	}
}
