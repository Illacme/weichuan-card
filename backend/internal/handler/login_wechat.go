package handler

import (
	"log"
	"net/http"
	"strings"

	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 微信一键登录：wx.login 的 code → openid → 找到已绑定该 openid 的员工 → 签发令牌。
//
// 为什么值得做：一线员工忘密码是 B 端最高频的工单，而每次都要管理员去后台重置。
// 微信一键登录把「记一个密码」这件事整个省掉，且不降低安全性——openid 由微信签发，
// 用户不需要记住任何东西，也不存在被撞库的密码。
//
// 前提是「员工账号已绑定 openid」，绑定有两条来源：
//  1. 小程序内自助申请开通（/api/apply/open 提交了 openid，审批通过时写进员工记录，见 apply.go）；
//  2. 已用手机号密码登录后，在「我的」页主动绑定（本文件的 bind 接口）。
// 所以一键登录对「自己申请开通」的员工开箱可用，对「管理员后台建号」的员工需要先绑一次。
//
// 刻意不做「首次用微信登录自动建号」：员工账号是审核出来的（谁是哪家公司的人必须有人确认），
// 自动建号等于任何人点一下就变成某企业的员工，名片墙会立刻被陌生人占满。

// code2Session 是 wechat.Code2Session 的间接层：唯一目的是可测。
// 微信接口要外网 + 真凭据，注入后才能在单测里覆盖「已绑定 / 未绑定 / 多账号歧义」这些分支
// （同 getWXACode 的做法）。
var code2Session = wechat.Code2Session

// resolveOpenID 用 code 换 openid，失败时返回可直接回给客户端的业务码信封。
//
// companyID 决定用哪套小程序凭据：企业独立版各配自己的 appid，同一自然人在不同小程序里
// 的 openid 并不相同（openid 是「用户 × 小程序」维度的）。用错凭据换出来的 openid
// 属于另一个小程序，拿去匹配员工必然对不上——所以绑定场景一律用员工自己的企业，
// 不信任客户端传来的值（见 wechatBindHandler）。
func resolveOpenID(db *gorm.DB, cfg config.Config, companyID int64, code string) (string, gin.H) {
	wc := wxAppOf(db, companyID, cfg)
	if wc.AppID == "" || wc.AppSecret == "" {
		return "", gin.H{"code": 1, "msg": "未配置小程序 AppID / WX_APPSECRET，暂不能使用微信登录"}
	}
	openid, _, err := code2Session(wc, code)
	if err != nil {
		// 不把微信的原始错误码直接甩给用户（对用户没有意义），但必须留在服务端日志里，
		// 否则「一键登录一直失败」只能靠猜是凭据错、code 过期还是网络问题。
		log.Printf("[WECHAT-LOGIN] code2session 失败 company=%d err=%v", companyID, err)
		return "", gin.H{"code": 1, "msg": "微信登录失败，请重试"}
	}
	return openid, nil
}

// wechatLoginHandler 一键登录。
//
// 业务码约定（与密码登录保持一致，另加 3）：
//
//	0  登录成功，返回 token / refreshToken / expiresIn
//	1  参数或微信侧错误（code 过期、未配置凭据……）
//	2  账号或企业已停用
//	3  该微信尚未绑定员工账号 / 绑定了多个账号（需要走密码登录）
func wechatLoginHandler(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code string `json:"code"`
			// 客户端按自己所属租户填（企业独立版固定、平台模式粘滞，见 miniprogram/utils/tenant.ts）。
			// 与 /api/wechat/openid 同口径：可以被伪造，但伪造只会让 code2session 失败
			// ——微信会校验 appid 与 code 是否同属一个小程序，拼不出「能用的错凭据」。
			CompanyID int64 `json:"companyId"`
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

		// 按 openid 找员工。
		//
		// 平台模式下所有租户共用一个小程序，openid 跨租户相同——同一微信在两家企业都任职时
		// 会有两条记录。此时用客户端带来的 companyId 收窄；收窄后仍多于一条说明数据本身
		// 有歧义，宁可让用户走密码登录，也不能随便挑一条签发令牌（登错公司=看到别家的名片墙）。
		q := db.Where("openid = ?", openid)
		if req.CompanyID > 0 {
			q = q.Where("company_id = ?", req.CompanyID)
		}
		var list []model.Employee
		if err := q.Limit(2).Find(&list).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
			return
		}
		if len(list) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"code": 3,
				"msg":  "该微信还没有绑定员工账号，请先用手机号密码登录，登录后在「我的」页绑定微信",
			})
			return
		}
		if len(list) > 1 {
			c.JSON(http.StatusOK, gin.H{"code": 3, "msg": "该微信绑定了多个员工账号，请用手机号密码登录"})
			return
		}
		e := list[0]
		// 状态校验：openid 只能证明「你是同一个微信」，证明不了「你还在这家公司」，
		// 停用账号 / 停用企业的闸门必须和密码登录一样拦。
		if e.Status == "disabled" {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "账号已停用"})
			return
		}
		if !companyEnabled(db, e.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "所属企业已停用"})
			return
		}
		access, refresh, expiresIn, err := auth.IssueTokens(e.StaffID, "employee", e.TokenVersion, e.CompanyID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "token": access, "refreshToken": refresh, "expiresIn": expiresIn,
		})
	}
}

// wechatBindHandler 把当前登录员工与微信绑定，之后即可一键登录。
//
// 绑定是**覆盖写**，不是追加：员工换了微信号时，用新微信登录 → 提示未绑定 → 密码登录 →
// 重新绑定，新 openid 覆盖旧的。因此不需要单独的「解绑」接口——旧微信从此匹配不到任何
// 员工记录，效果等同于解绑，而且不会顺手清掉后台用于核实身份的历史 openid 记录。
func wechatBindHandler(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var req struct {
			Code string `json:"code"`
		}
		if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Code) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "缺少 code"})
			return
		}
		var me model.Employee
		if err := db.First(&me, "staff_id = ?", staffID).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到员工"})
			return
		}
		// 凭据取员工自己的企业，不取客户端传的 companyId：绑定是已登录场景，
		// 身份是确定的，没有任何理由相信入参（用错凭据会把别家小程序的 openid 绑进来）。
		openid, fail := resolveOpenID(db, cfg, me.CompanyID, req.Code)
		if fail != nil {
			c.JSON(http.StatusOK, fail)
			return
		}
		// 同一企业内一个微信只能绑一个账号：否则两个人共用一个微信号，谁点「一键登录」
		// 都会进到同一个人（先建的那条）的名片，另一人永远登不进自己的。
		var dup int64
		if err := db.Model(&model.Employee{}).
			Where("company_id = ? AND openid = ? AND staff_id <> ?", me.CompanyID, openid, me.StaffID).
			Count(&dup).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "绑定失败"})
			return
		}
		if dup > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该微信已绑定本企业的其他员工账号，请联系管理员处理"})
			return
		}
		if err := db.Model(&model.Employee{}).
			Where("staff_id = ?", me.StaffID).
			Update("openid", openid).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "绑定失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已绑定，下次可直接微信一键登录"})
	}
}

// RegisterWechatLogin 注册一键登录与绑定接口。
// 单独一个函数而不是塞进 RegisterAuth：RegisterAuth 已经很长，且这两条路由
// 复用的是同一个登录限流器——由调用方传进来，避免在这里重复构造。
func RegisterWechatLogin(r *gin.Engine, db *gorm.DB, cfg config.Config, loginLimiter gin.HandlerFunc) {
	// 一键登录走登录限流器：openid 猜不出来，但 code 是一次性的、每次请求都要打一次
	// 微信接口（有配额），不限流的话一个脚本就能把 jscode2session 配额打光。
	r.POST("/api/employee/login/wechat", loginLimiter, wechatLoginHandler(db, cfg))
	// 绑定必须已登录：否则任何人都能把「别人的微信号」绑到「自己的账号」上。
	r.POST("/api/employee/wechat/bind", middleware.AuthMiddleware(db, "employee"), wechatBindHandler(db, cfg))
}
