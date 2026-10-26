package handler

import (
	"net/http"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/repository"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// dbRateCounter 把限流计数落到数据库，使多副本部署下各副本共享同一份计数。
//
// 为什么不放在 middleware 包里：那个包只依赖 Counter 接口，不需要知道计数存在
// 哪里（数据库 / Redis / 别的）。反过来让 middleware 去认 repository 会把依赖
// 方向弄反，也会把 ORM 拖进一个本来很轻的包。
//
// 为什么只给登录 / 刷新挂，小程序码（RegisterWechat）不挂：小程序码限流前面还有
// 进程内缓存挡着（命中缓存的请求根本到不了限流器），多副本下阈值翻倍的影响面小；
// 而登录限流是防爆破的唯一闸门，翻倍等于直接削弱安全边界，优先级最高。
// 小程序码那边要挂的话，得先给 RegisterWechat 传 db（改 4 个调用点）。
type dbRateCounter struct{ db *gorm.DB }

func (c dbRateCounter) Hit(key string, window time.Duration) (int, error) {
	return repository.RateHit(c.db, key, window)
}

// RegisterAuth 注册登录 / 刷新 / 登出接口。
//   - 员工登录（小程序端）：Bearer 头鉴权，token 走响应体；
//   - 超管登录（PC 后台）：httpOnly Cookie 鉴权，token 不下发到响应体；
//   - /api/refresh：Cookie 流（读 refresh_token Cookie）或 Body 流（传 refreshToken）二选一；
//   - /api/logout：吊销当前会话 sid（单设备精确吊销）并清除 httpOnly Cookie。
//
// 两个登录接口均叠加登录限流中间件（按客户端 IP 固定窗口计数，防爆破），
// 且计数走数据库共享——多副本下每个副本各算一份会让实际阈值翻倍。
func RegisterAuth(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	// 限流阈值取自系统设置（后台「系统设置 → 登录与令牌」可改，即时生效）；
	// 缺省值同时是兜底：界面填 0 / 负数时仍按 10 / 60 次每分钟限流，不会把防爆破关掉。
	//
	// 桶键必须带动作前缀（login:/refresh:）：login/refresh/track 三个限流器
	// 共用同一张 rate_limit_hits 表，若都用裸 IP 当键就会挤进同一行计数——
	// 访客埋点刷够登录阈值（10/分）就能把同 IP 的登录打到 429，办公网
	// NAT 共享出口 IP 时完全可达（2026-10-23 全量测试跑挂实测抓过：
	// 表现是「登录莫名被限流」，排查时代码里两个限流器看起来毫不相干）。
	loginLimiter := middleware.NewLoginRateLimiter(func() int {
		return setting.GetInt("login_rate_limit_per_min", 10)
	}, 10, time.Minute).
		WithKey(func(c *gin.Context) string { return "login:" + c.ClientIP() }).
		// 跨副本共享计数：否则每个副本各算一份，实际阈值 = 单副本阈值 × 副本数。
		// 存储故障时中间件会自动降级到进程内计数，不会把登录接口打挂。
		WithCounter(dbRateCounter{db: db})
	refreshLimiter := middleware.NewLoginRateLimiter(func() int {
		return setting.GetInt("refresh_rate_limit_per_min", 60)
	}, 60, time.Minute).
		WithKey(func(c *gin.Context) string { return "refresh:" + c.ClientIP() }).
		WithCounter(dbRateCounter{db: db})

	// 员工登录（按手机号）——小程序端，Bearer 头鉴权
	r.POST("/api/employee/login", loginLimiter.Handler(), func(c *gin.Context) {
		var req struct {
			Phone    string `json:"phone"`
			Password string `json:"password"`
			OpenID   string `json:"openid"`   // 微信 openid（wx.login 换取，供后台核实身份）
			Nickname string `json:"nickname"` // 微信昵称（用户授权后采集）
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Phone == "" || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var e model.Employee
		// 手机号已静态加密：按盲索引等值查找（库里看不到明文，但仍能精确命中）。
		if err := db.First(&e, "phone_bidx = ?", pii.BlindIndex(req.Phone)).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在"})
			return
		}
		if e.Status == "disabled" {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "账号已停用"})
			return
		}
		// 租户校验：企业停用后其员工一并不能登录
		if !companyEnabled(db, e.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "所属企业已停用"})
			return
		}
		if !auth.CheckPassword(e.PasswordHash, req.Password) {
			// 登录失败必须留痕：连续失败是爆破信号，没有日志就只能靠猜
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionLoginFail, TargetType: "employee", TargetID: req.Phone,
				Result: audit.ResultFail, ActorName: e.Name,
			})
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "密码错误"})
			return
		}
		// 记录本次登录的微信身份，供后台核实；逐字段判空，空值不覆盖已有值
		// （例如登录只静默拿到 openid 时，不能把申请阶段填好的昵称清空）。
		loginUpdates := map[string]interface{}{}
		if req.OpenID != "" {
			loginUpdates["openid"] = req.OpenID
		}
		if req.Nickname != "" {
			// 昵称属 PII：此处是 map 更新（不触发模型钩子），必须显式加密 + 补盲索引。
			enc, err := pii.Encrypt(req.Nickname)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
				return
			}
			loginUpdates["nickname"] = enc
			loginUpdates["nickname_bidx"] = pii.BlindIndex(req.Nickname)
		}
		if len(loginUpdates) > 0 {
			db.Model(&e).Updates(loginUpdates)
		}
		access, refresh, expiresIn, err := auth.IssueTokens(e.StaffID, "employee", e.TokenVersion, e.CompanyID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "token": access, "refreshToken": refresh, "expiresIn": expiresIn})
	})

	// 超管登录（按用户名）——PC 后台，httpOnly Cookie 鉴权（token 不入响应体）
	r.POST("/api/admin/login", loginLimiter.Handler(), func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var a model.Admin
		if err := db.First(&a, "username = ?", req.Username).Error; err != nil {
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionLoginFail, TargetType: "admin", TargetID: req.Username,
				Result: audit.ResultFail, ActorName: req.Username,
				Detail: map[string]any{"reason": "账号不存在"},
			})
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在"})
			return
		}
		if !auth.CheckPassword(a.PasswordHash, req.Password) {
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionLoginFail, TargetType: "admin", TargetID: req.Username,
				Result: audit.ResultFail, ActorName: req.Username,
				Detail: map[string]any{"reason": "密码错误"},
			})
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "密码错误"})
			return
		}
		// 租户校验：企业管理员所属企业停用后不能登录（平台超管 company_id=0 不受限）
		if a.CompanyID > 0 && !companyEnabled(db, a.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "所属企业已停用"})
			return
		}
		access, refresh, expiresIn, err := auth.IssueTokens(a.ID, "admin", a.TokenVersion, a.CompanyID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
			return
		}
		setAuthCookies(c, access, refresh, auth.AccessTokenTTL(), auth.RefreshTokenTTL(), cfg)
		// 回显身份（非敏感）：PC 后台据此区分平台超管 / 企业管理员并展示当前企业
		companyName := ""
		companyShortName := ""
		if a.CompanyID > 0 {
			var comp model.Company
			if db.First(&comp, "id = ?", a.CompanyID).Error == nil {
				companyName = comp.Name
				companyShortName = comp.ShortName
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "expiresIn": expiresIn,
			"companyId": a.CompanyID, "companyName": companyName,
			"companyShortName": companyShortName,
			"isPlatform":       a.CompanyID == 0,
		})
	})

	// 刷新 access 令牌：Cookie 流（PC 后台，读 refresh_token Cookie）或 Body 流（小程序，传 refreshToken）。
	r.POST("/api/refresh", refreshLimiter.Handler(), func(c *gin.Context) {
		var req struct {
			RefreshToken string `json:"refreshToken"`
		}
		_ = c.ShouldBindJSON(&req)
		refreshTok := req.RefreshToken
		fromCookie := false
		if refreshTok == "" {
			if ck, err := c.Cookie(refreshCookieName); err == nil && ck != "" {
				refreshTok = ck
				fromCookie = true
			}
		}
		if refreshTok == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		claims, err := auth.ParseToken(refreshTok)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "刷新令牌无效"})
			return
		}
		if claims.Typ != "refresh" {
			c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "刷新令牌类型错误"})
			return
		}
		// 安全戳校验：refresh 同样受 token_version 约束，改密/禁用后立即失效
		currentVersion, err := middleware.TokenVersion(db, claims.Role, claims.StaffID)
		if err != nil || currentVersion != claims.VID {
			c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "登录已失效，请重新登录"})
			return
		}
		// 会话黑名单校验：该会话已登出 / 被踢下线时，refresh 同样失效
		if revoked, err := repository.IsSessionRevoked(db, claims.SID); err != nil || revoked {
			c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "登录已失效，请重新登录"})
			return
		}
		// 刷新不换 sid：一次登录始终同一个会话，登出吊销该 sid 即终结整条令牌链
		access, refresh, expiresIn, err := auth.IssueTokensWithSID(claims.StaffID, claims.Role, currentVersion, claims.CompanyID, claims.SID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "签发失败"})
			return
		}
		if fromCookie {
			// Cookie 流：轮换下发新的 access / refresh Cookie
			setAuthCookies(c, access, refresh, auth.AccessTokenTTL(), auth.RefreshTokenTTL(), cfg)
			c.JSON(http.StatusOK, gin.H{"code": 0, "expiresIn": expiresIn})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "token": access, "expiresIn": expiresIn})
	})

	// 登出：吊销当前会话 sid（单设备精确吊销），并清除 httpOnly 鉴权 Cookie（PC 后台）。
	//   - PC 后台：Cookie 流，浏览器自动携带 access_token，本接口解析出 sid 写入黑名单；
	//   - 小程序端：Bearer 流，客户端丢弃 token 即可；携带有效 Bearer 调用时同样吊销该 sid。
	// 只失效当前设备：同一账号的其他登录不受影响（那需要改密 / 禁用，走 token_version 账号级吊销）。
	// 不挂鉴权中间件：access 过期后客户端仍需能调本接口清 Cookie / 收尾会话。
	r.POST("/api/logout", func(c *gin.Context) {
		// 吊销失败必须让调用方知道：从前吞掉错误照样回 ok，
		// 用户以为已登出，refresh token 却仍能续期——整套「登出吊销 sid」的保证形同虚设。
		if err := revokeCurrentSession(c, db); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登出失败，请重试：" + err.Error()})
			return
		}
		clearAuthCookies(c, cfg)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}

// revokeSession 吊销会话的间接层：唯一目的是可测——把「写黑名单失败」这条支线
// 放进单测（生产只有数据库故障才会走到，不注入就永远覆盖不到）。
var revokeSession = repository.RevokeSession

// revokeCurrentSession 从 Bearer / Cookie 中解析当前会话并写入黑名单，失败时回传错误。
//
// 「无 token / token 已失效」不算失败（无需吊销，静默跳过）：那种情况下本来就没有
// 有效会话，登出天然成立。只有「解析出有效 sid 却写不进黑名单」才报错——
// 那时必须让用户知道登出没成功，而不是给他一个假的 ok。
func revokeCurrentSession(c *gin.Context, db *gorm.DB) error {
	// 优先解析 access（Bearer 或 access_token Cookie）；access 过期 / 缺失时
	// 退而解析 refresh_token Cookie，保证「access 已过期但 refresh 仍有效」时登出也能吊销会话。
	raw := middleware.ExtractToken(c)
	if raw == "" {
		if ck, err := c.Cookie(refreshCookieName); err == nil && ck != "" {
			raw = ck
		}
	}
	if raw == "" {
		return nil
	}
	claims, err := auth.ParseToken(raw)
	if err != nil || claims.SID == "" {
		return nil
	}
	if claims.Typ != "access" && claims.Typ != "refresh" {
		return nil
	}
	return revokeSession(db, claims.SID, claims.Role, claims.StaffID, sessionExpiry(c, claims))
}

// sessionExpiry 估算黑名单记录的过期时间：优先取 refresh 令牌的过期时间
// （PC 后台从 refresh_token Cookie 解析），都没有时保守按 now + RefreshTokenTTL。
// 该时间仅用于后台清理过期记录，不参与吊销判定。
func sessionExpiry(c *gin.Context, claims *auth.Claims) time.Time {
	if claims.Typ == "refresh" && claims.ExpiresAt != nil {
		return claims.ExpiresAt.Time
	}
	if ck, err := c.Cookie(refreshCookieName); err == nil && ck != "" {
		if rc, err := auth.ParseToken(ck); err == nil && rc.ExpiresAt != nil {
			return rc.ExpiresAt.Time
		}
	}
	return time.Now().Add(auth.RefreshTokenTTL())
}
