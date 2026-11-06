package handler

// 小程序端企业管理员身份通路。
//
// 为什么只需要「登录」而不用改审核接口：六个 /api/admin/apply/* 端点挂的都是
// middleware.AuthMiddleware(db, "admin")，而 ExtractToken 优先读 Authorization:
// Bearer，其次才读 Cookie。PC 后台的 /api/admin/login 刻意只下发 httpOnly Cookie
// （JS 读不到令牌，防 XSS 窃取），小程序没有 Cookie 容器，所以这里另开三条端点，
// 把 role=admin 的 JWT 放进响应体。审核端点、租户隔离、平台超管拦截全部零改动。
//
// 三条端点：
//   - POST /api/admin/login/mobile   账号密码登录，成功顺带绑定微信（见页面逻辑）
//   - POST /api/admin/wechat/bind    已登录时绑定 / 换绑当前微信
//   - POST /api/admin/login/wechat   微信免密登录（code → openid → 找绑定的管理员）
//
// 安全性与 PC 登录同口径：统一失败文案 + 空账号烧等价 bcrypt 时间 + 登录限流器 +
// 企业停用校验 + 审计留痕。openid 一律由服务端用 jscode2session 换取（不信任入参），
// 绑定时用管理员自己的企业取凭据，免密登录时用客户端带来的租户取凭据——与
// /api/employee/login/wechat 完全同构，见 login_wechat.go 头部注释。

import (
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// registerAdminMobile 挂在 RegisterAuth 内部，复用同一个登录限流器。
// 刻意不导出成 handler.RegisterXxx：三份装配清单（server / devserver / buildEngine）
// 已经都调了 RegisterAuth，少一个装配点就少一类「联调静默 404」。
func registerAdminMobile(r *gin.Engine, db *gorm.DB, cfg config.Config, loginLimiter gin.HandlerFunc) {
	r.POST("/api/admin/login/mobile", loginLimiter, adminMobileLoginHandler(db))
	// 绑定必须先证明「我是这个管理员」：否则任何人都能把别人的微信号绑到自己账号上。
	r.POST("/api/admin/wechat/bind", middleware.AuthMiddleware(db, "admin"), adminWXBindHandler(db, cfg))
	// 免密登录同样是登录入口，走登录限流器（每次都要打一次有配额的 jscode2session）。
	r.POST("/api/admin/login/wechat", loginLimiter, adminWechatLoginHandler(db, cfg))
}

// adminLoginOK 签发管理员令牌并回显身份。
// PC 端 /api/admin/login 只写 Cookie；这里把 access / refresh 放进响应体，
// 小程序存进 Storage 后用 Bearer 调用 /api/admin/*，/api/refresh 则按 Body 流续期。
func adminLoginOK(c *gin.Context, db *gorm.DB, a model.Admin) {
	access, refresh, expiresIn, err := auth.IssueTokens(a.ID, "admin", a.TokenVersion, a.CompanyID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
		return
	}
	companyName, companyShortName := "", ""
	if a.CompanyID > 0 {
		var comp model.Company
		if db.First(&comp, "id = ?", a.CompanyID).Error == nil {
			companyName, companyShortName = comp.Name, comp.ShortName
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "token": access, "refreshToken": refresh, "expiresIn": expiresIn,
		"companyId": a.CompanyID, "companyName": companyName,
		"companyShortName": companyShortName, "isPlatform": a.CompanyID == 0,
		"username": a.Username, "wechatBound": a.WXOpenID != "",
	})
}

// noteAdminActor 把刚签发的身份写回 gin 上下文，让 audit.Record 记出真实的
// actor / company，而不是「anonymous / 0」。登录接口本身没有鉴权中间件，
// 不这么做审计里就查不出是谁在手机上拿了管理员令牌。
func noteAdminActor(c *gin.Context, a model.Admin) {
	c.Set("staffId", a.ID)
	c.Set("role", "admin")
	c.Set("companyId", a.CompanyID)
}

func adminMobileLoginHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Username) == "" || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var a model.Admin
		if err := db.First(&a, "username = ?", req.Username).Error; err != nil {
			// 与 PC 登录同一条守门：账号不存在也要烧掉一次 bcrypt 时间，
			// 否则「快 = 不存在」就是个账号枚举接口。
			auth.CheckPasswordDummy(req.Password)
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionLoginFail, TargetType: "admin", TargetID: req.Username,
				Result: audit.ResultFail, ActorName: req.Username,
				Detail: map[string]any{"reason": "账号不存在", "via": "mobile"},
			})
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名或密码错误"})
			return
		}
		if !auth.CheckPassword(a.PasswordHash, req.Password) {
			audit.Record(db, c, audit.Entry{
				Action: audit.ActionLoginFail, TargetType: "admin", TargetID: req.Username,
				Result: audit.ResultFail, ActorName: req.Username,
				Detail: map[string]any{"reason": "密码错误", "via": "mobile"},
			})
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名或密码错误"})
			return
		}
		if a.CompanyID > 0 && !companyEnabled(db, a.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "所属企业已停用"})
			return
		}
		noteAdminActor(c, a)
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminMobileLogin, TargetType: "admin",
			TargetID: strconv.FormatInt(a.ID, 10), ActorName: a.Username,
			Detail: map[string]any{"via": "password"},
		})
		adminLoginOK(c, db, a)
	}
}

// adminWXBindHandler 把当前登录管理员与微信绑定（覆盖写，同企业内 openid 唯一）。
// 凭据取管理员自己的 company_id，不取客户端传值：绑定是已登录场景，身份确定，
// 用错凭据会把别家小程序的 openid 绑进来，等于给错误的人开了管理员入口。
func adminWXBindHandler(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		adminID := c.GetInt64("staffId")
		var req struct {
			Code string `json:"code"`
		}
		if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Code) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "缺少 code"})
			return
		}
		var a model.Admin
		if err := db.First(&a, "id = ?", adminID).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到管理员"})
			return
		}
		openid, fail := resolveOpenID(db, cfg, a.CompanyID, req.Code)
		if fail != nil {
			c.JSON(http.StatusOK, fail)
			return
		}
		var dup int64
		if err := db.Model(&model.Admin{}).
			Where("company_id = ? AND wx_openid = ? AND id <> ?", a.CompanyID, openid, a.ID).
			Count(&dup).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "绑定失败"})
			return
		}
		if dup > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该微信已绑定本企业的其他管理员账号，请联系平台管理员处理"})
			return
		}
		res := db.Model(&model.Admin{}).Where("id = ?", a.ID).Update("wx_openid", openid)
		if res.Error != nil || res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "绑定失败"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminWXBind, TargetType: "admin",
			TargetID: strconv.FormatInt(a.ID, 10),
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已绑定，下次打开小程序可直接进入审核"})
	}
}

// adminWechatLoginHandler 微信免密登录。
//
// 业务码与 /api/employee/login/wechat 对齐：0 成功 / 1 参数或微信侧错误 /
// 2 企业停用 / 3 未绑定或绑定多个账号（需走账号密码登录）。
//
// 平台模式下所有租户共用一个小程序，openid 跨租户相同——同一微信可能是两家企业的
// 管理员。先用客户端 companyId 收窄；收窄后仍多于一条就宁可让用户输密码，
// 绝不随便挑一个签发令牌（登错公司 = 看到别家的待审申请）。
func adminWechatLoginHandler(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code      string `json:"code"`
			CompanyID int64  `json:"companyId"`
		}
		if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Code) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "缺少 code"})
			return
		}
		openid, fail := resolveOpenID(db, cfg, req.CompanyID, req.Code)
		if fail != nil {
			c.JSON(http.StatusOK, fail)
			return
		}
		var list []model.Admin
		if err := db.Where("wx_openid = ? AND wx_openid <> ''", openid).Limit(3).Find(&list).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
			return
		}
		if len(list) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"code": 3,
				"msg":  "该微信还没有绑定管理员账号，请先用账号密码登录",
			})
			return
		}
		chosen := list[0]
		if len(list) > 1 {
			var matched []model.Admin
			for _, a := range list {
				if req.CompanyID > 0 && a.CompanyID == req.CompanyID {
					matched = append(matched, a)
				}
			}
			if len(matched) != 1 {
				c.JSON(http.StatusOK, gin.H{"code": 3, "msg": "该微信绑定了多个企业管理员账号，请用账号密码登录"})
				return
			}
			chosen = matched[0]
		}
		if chosen.CompanyID > 0 && !companyEnabled(db, chosen.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "所属企业已停用"})
			return
		}
		noteAdminActor(c, chosen)
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminMobileLogin, TargetType: "admin",
			TargetID: strconv.FormatInt(chosen.ID, 10), ActorName: chosen.Username,
			Detail: map[string]any{"via": "wechat"},
		})
		adminLoginOK(c, db, chosen)
	}
}
