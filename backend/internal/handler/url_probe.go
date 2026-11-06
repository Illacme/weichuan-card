package handler

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/urlprobe"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 后台「测试地址」每分钟每个管理员最多 10 次。这是全项目唯一会**按管理员输入的
// 地址发起出向请求**的接口，是 SSRF 的第一入口，限流必须比常规写接口更紧。
// 分桶用 IP + staffId 双维度：只按 IP 会让同一 NAT 后的多管理员互相挤占；
// 只按 staffId 又挡不住脚本换账号放大调用量。
const urlProbeLimitPerMin = 10

// 出站探测抽成包级函数变量，接口测试才能在不联网的前提下覆盖 code:0 成功分支
// （与 statTrackLimitFn 同一套路）。生产代码不覆盖它。
var urlProbeFn func(context.Context, string) (urlprobe.Result, error) = urlprobe.Probe

// 上限也做成可变：runAPIFlow 会在同一分桶里连续验证多种被拦地址，
// 不能让「测试用例多写一条」就莫名其妙撞上 429。生产默认仍是 10/分钟。
var urlProbeLimitFn = func() int { return urlProbeLimitPerMin }

// RegisterUrlProbe 注册 POST /api/admin/url-probe。
//
// 场景：管理员在「公司简介 → 视频来源」里填了 CDN 地址，点开链接未必能排除问题
// （浏览器能开不等于服务端能取、更不等于小程序能播；反过来 403/404 在小程序里
// 只表现为黑屏 00:00 且无任何报错）。七牛未启用 HTTPS 时 https 回
// X-Error-Info: httpsDisabled 403、对象带目录前缀时裸域名 404，都能在这里一次看出来。
//
// 安全边界全在 internal/urlprobe：URL 形态白名单 → 自己解析 DNS 并逐个 IP 排除
// 私网/回环/链路本地/保留段 → 把已校验 IP 固定进 DialContext（防 DNS rebinding）
// → 禁重定向、关代理、超时、只发 HEAD 不读响应体。本 handler 只负责鉴权、限流、
// 审计和信封，不碰这些规则。
func RegisterUrlProbe(r *gin.Engine, db *gorm.DB) {
	limiter := middleware.NewRateLimiter(
		func() int { return urlProbeLimitFn() }, urlProbeLimitPerMin, time.Minute,
	).
		WithKey(func(c *gin.Context) string {
			return "urlprobe:" + c.ClientIP() + ":" + strconv.FormatInt(c.GetInt64("staffId"), 10)
		}).
		WithMessage("地址探测过于频繁，请稍后再试").
		WithCounter(dbRateCounter{db: db})

	// 顺序不能换：AuthMiddleware 先把 staffId 写进上下文，限流分桶才拿得到它。
	r.POST("/api/admin/url-probe",
		middleware.AuthMiddleware(db, "admin"),
		limiter.Handler(),
		func(c *gin.Context) {
			var req struct {
				URL string `json:"url"`
			}
			if c.ShouldBindJSON(&req) != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
				return
			}
			host := probeHostOf(req.URL)
			res, err := urlProbeFn(c.Request.Context(), req.URL)
			if err != nil {
				// 被拦下来的地址也要留痕：反复探测 169.254.169.254 正是攻击特征。
				audit.Record(db, c, audit.Entry{
					Action: audit.ActionURLProbe, TargetType: "url", TargetID: host,
					Result: audit.ResultFail,
					Detail: map[string]any{"error": err.Error()},
				})
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
				return
			}
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionURLProbe, TargetType: "url", TargetID: host,
				Detail: map[string]any{"status": res.Status, "reachable": res.Reachable},
			})
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": res})
		})
}

// probeHostOf 只取 host:port 作审计标识。完整 URL 的 query 常带 OSS 签名 / token，
// 写进审计表等于把凭据复制一份；path 也可能含手机号等 PII。
func probeHostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Host
}
