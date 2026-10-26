package handler

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 前端报错上报：把浏览器侧的白屏 / 运行时异常 / 未捕获 Promise 拒绝对接到服务端日志，
// 带上 requestId（前端从最近一次响应头里取到的 X-Request-Id）即可和后端同一次请求的日志串起来。
//
// 为什么是个无鉴权公开端点：报错可能发生在登录之前、匿名访客页、或令牌已失效的瞬间——
// 这些场景下前端根本拿不到有效会话，一旦要求鉴权，报错反而上报不出去，等于没接。
// 代价是它成了潜在的刷日志入口，所以用「每 IP 固定窗口限流 + 载荷体积上限 + 字段裁剪」三道闸兜住。
//
// 为什么只是写日志而不是落库：本端点的唯一目的就是「和后端日志关联」，存库反而把一条线索
// 拆成两处（日志在 Loki/文件、错误在表），排查时要两边跳。C1 的诉求是「出事服务端能收到信号」，
// 日志流已经能满足；真要聚合查询再说。
func RegisterClientError(r *gin.Engine) {
	r.POST("/api/client-error", clientErrorLimiter(), receiveClientError)
}

type clientErrorPayload struct {
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	URL       string `json:"url"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Level     string `json:"level"` // error / warning / unhandledrejection
	Env       string `json:"env"`   // dev / production（前端按 import.meta.env.MODE 填）
	RequestID string `json:"requestId"`
	UserAgent string `json:"userAgent"`
	Extra     string `json:"extra"`
}

const maxClientErrorBody = 1 << 16 // 64KB 上限，挡住超大体

// clientErrorLimiter 每 IP 固定窗口限流：默认 1 分钟内最多 60 条。
// 报错本就低频，这个额度足以覆盖「一个页面崩了连发几条」而挡住恶意刷日志。
// 窗口到期整体重置（不区分 IP 单独过期）——实现最简，且一分钟的窗口里 map 体积有界。
func clientErrorLimiter() gin.HandlerFunc {
	const window = time.Minute
	const max = 60
	var mu sync.Mutex
	hits := map[string]int{}
	var resetAt time.Time
	return func(c *gin.Context) {
		ip := clientIP(c)
		mu.Lock()
		if time.Now().After(resetAt) {
			hits = map[string]int{}
			resetAt = time.Now().Add(window)
		}
		n := hits[ip]
		if n >= max {
			mu.Unlock()
			c.JSON(http.StatusOK, gin.H{"code": 429, "msg": "too many requests"})
			c.Abort()
			return
		}
		hits[ip]++
		mu.Unlock()
		c.Next()
	}
}

func receiveClientError(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxClientErrorBody))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "read failed"})
		return
	}
	var p clientErrorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "bad json"})
		return
	}
	if strings.TrimSpace(p.Message) == "" && strings.TrimSpace(p.Stack) == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "empty"})
		return
	}
	level := p.Level
	if level == "" {
		level = "error"
	}
	log.Printf("[CLIENT-ERROR] level=%s reqId=%q env=%s url=%s line=%d col=%d ua=%s msg=%q stack=%q extra=%q",
		level,
		clip(p.RequestID, 64),
		clip(p.Env, 24),
		clip(p.URL, 1000),
		p.Line, p.Column,
		clip(p.UserAgent, 300),
		clip(p.Message, 2000),
		clip(p.Stack, 4000),
		clip(p.Extra, 1024),
	)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// clientIP 取原始客户端 IP：优先 X-Forwarded-For 首段（反代已在可信网段，见 launch-checklist
// TRUSTED_PROXIES），否则取 RemoteAddr 的 host 部分。
func clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if ip, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return ip
	}
	return c.Request.RemoteAddr
}

// clip 把字符串裁到 n 字节（不切 UTF-8 边界也暂忽略——日志里多/少半个字符无所谓）。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
