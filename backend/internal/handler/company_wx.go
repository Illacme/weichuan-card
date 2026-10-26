package handler

import (
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 租户小程序凭据的后台配置接口（平台超管）。
//
// 背景：小程序有两种形态，平台模式（一个 appid 服务所有企业）用
// sys_setting 的全局那一份就够；企业独立版（每企业一个 appid，共用同一套
// 后端）必须用该企业自己那份。本文件提供「给某个企业配它自己那份」的入口，
// 取值链见 wechat.go 的 wxAppOf。
//
// 由平台超管代填而不是企业管理员自填：现有后台权限模型里企业管理员的写接口
// 都限本租户，但没有「企业端设置页」这个入口；凭据又是阻塞性配置（不配就
// 出不了码），先让平台能代填把链路打通。

// RegisterCompanyWXApp 挂载租户小程序凭据的读写接口。
// 与 RegisterCompanies 分开注册，调用方需在两者都注册后路由才完整。
func RegisterCompanyWXApp(r *gin.Engine, db *gorm.DB) {
	plat := middleware.PlatformAdminMiddleware(db)
	// 读取：只回 appId 与「是否已配」。appSecret 永不回传——
	// 与 sys_setting.wx_appsecret 同策略，界面据此显示「已配置」而不是明文。
	r.GET("/api/admin/companies/:id/wechat", plat, func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var row model.CompanyWXApp
		err := db.Where("company_id = ?", id).First(&row).Error
		if err != nil {
			// 没配过 = 走全局凭据，不是错误
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
				"appId": "", "configured": false,
				// 没配凭据时也没有「关代发」这一说——代发走平台全局小程序，
				// 企业没表达过意见，默认允许（与 releaseEnabled 缺省值一致）。
				"releaseEnabled": true,
				// 上传密钥同理：没配过时回落平台全局那份（sys_setting.wx_upload_key）
				"uploadKeyConfigured": strings.TrimSpace(setting.GetString("wx_upload_key", "")) != "",
			}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"appId":          row.AppID,
			"configured":     strings.TrimSpace(row.AppID) != "" && strings.TrimSpace(row.AppSecret) != "",
			"releaseEnabled": row.ReleaseEnabled,
			// 与 appSecret 同策略：只回「配没配」，永不明文回传。
			// 上传密钥是能把代码推到这个小程序上的凭证，泄漏了比 AppSecret 更难收场。
			"uploadKeyConfigured": strings.TrimSpace(row.UploadKey) != "",
		}})
	})

	// 写入：appId + appSecret 成对保存。
	// appSecret 传空串 = 清空（落回全局凭据）；不传该字段 = 保留原值不动。
	// 与 sys_setting 的掩码语义保持一致：界面看到的 "******" 原样提交即「不改」。
	r.PUT("/api/admin/companies/:id/wechat", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			AppID          string `json:"appId"`
			AppSecret      string `json:"appSecret"`
			UploadKey      string `json:"uploadKey"`
			ReleaseEnabled *bool  `json:"releaseEnabled"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		appID := strings.TrimSpace(req.AppID)
		if appID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "AppID 不能为空，直接提交空串可清空该企业的凭据"})
			return
		}
		// 只校验前缀不校验长度：微信侧是最终裁判，这里拦太严会让
		// 「换了个新形态的 appid」时后台先拒一次，反而耽误事。
		if !strings.HasPrefix(appID, "wx") || len(appID) < 8 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "AppID 格式不对，应以 wx 开头"})
			return
		}
		var row model.CompanyWXApp
		err := db.Where("company_id = ?", id).First(&row).Error
		if err != nil {
			row = model.CompanyWXApp{CompanyID: id}
			// 新建时开关缺省 true（允许代发）：与 migration 0026 的 DEFAULT 1 一致。
			// 指向针为 nil 表示前端没提这档事，不能因为「没传」就默认关掉——
			// 那会让所有只改 AppID 的既有调用静默关掉企业的代发权限。
			row.ReleaseEnabled = true
		}
		row.AppID = appID
		// releaseEnabled 用指针区分「没传（不动）」与「传了 false（要关）」。
		// 布尔字段的零值就是 false，不用指针的话「只改 AppID 不带这个字段」
		// 会被解释成「关闭代发」——一次普通的凭据更新就把企业权限关了。
		if req.ReleaseEnabled != nil {
			row.ReleaseEnabled = *req.ReleaseEnabled
		}
		// 空串 = 清空 secret（明确要落回全局）；非空 = 覆盖。
		// 掩码 "******" 由 setting.Mask 定义，界面原样提交即命中这里被当作空值，
		// 所以「只看不改点保存」会清掉 secret——这是有意的：见下方审计脱敏说明。
		if secret := strings.TrimSpace(req.AppSecret); secret != "" {
			row.AppSecret = secret
		} else {
			row.AppSecret = ""
		}
		// 上传密钥同 appSecret 的语义：空串 = 清空（落回平台全局那把）。
		// 不校验内容格式——格式校验放在真正上传前做（mpupload.KeyProblems），
		// 那里能给出「缺 PRIVATE KEY 标记」这类具体到原因的提示；
		// 在这里拦只会得到一句「格式不对」，反而要让运维猜。
		if key := strings.TrimSpace(req.UploadKey); key != "" {
			row.UploadKey = key
		} else {
			row.UploadKey = ""
		}
		if err := db.Save(&row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		}
		// 改完立刻生效：wxacode 的凭据缓存是 30s TTL，不等它自然过期
		clearWXAppCache(id)
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyUpdate, TargetType: "company_wx_app", TargetID: strconv.FormatInt(id, 10),
			// appSecret / uploadKey 都不进审计明细：脱敏规则按 key 名匹配（见 audit 包），
			// 这里连 key 都不出现，从源头杜绝泄漏
			Detail: map[string]any{
				"appId":               appID,
				"releaseEnabled":      row.ReleaseEnabled,
				"uploadKeyConfigured": row.UploadKey != "",
			},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}
