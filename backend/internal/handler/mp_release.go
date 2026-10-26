package handler

// 小程序版本发布的后台接口（平台超管代租户操作）。
//
// 完整链路（上传可由 CI 或后台自己做，见下方说明）：
//
//	上传代码（CI / 后台）──► 登记版本（draft）──► 提交审核（auditing，拿 auditid）
//	  ──► 轮询审核状态（audit_passed / rejected / withdrawn）──► 发布（released）
//	  中途可撤回审核（auditing ──► withdrawn）
//
// 上传有两条路，产物都是「微信侧的体验版」：
//   - CI 上传：代码由 CI 用「代码上传密钥」经 miniprogram-ci 传，后台只登记版本号；
//   - 后台上传（uploadMPRelease）：服务器上放着小程序源码 + Node + miniprogram-ci，
//     由后台自己传。这条路让「没有微信开发者工具的人也能发版」。
// 两条路共用同一把钥匙的两种配法（租户 / 全局，见 uploadKeyOf），且都不使用
// AppSecret——微信的代码上传只认「代码上传密钥」。
//
// 登记与提审刻意分成两个动作：上传后微信要异步做代码检测，检测没结束就提审
// 会报 61039（见 wechat/release.go 头注释的硬约束）。
//
// 三条安全底线（都由本文件强制执行，不依赖调用方自觉）：
//  1. 状态机白名单（model.CanTransition）：只登记过的流向才允许，非法流转一律拒绝；
//  2. 租户开关（company_wx_app.release_enabled）：企业关掉代发后一切动作拒绝；
//  3. AppID 一致性：登记时记下用的哪个小程序，动作时重新解析凭据比对，
//     不一致就拒绝——否则企业换过小程序后，会把审核/发布打到别的小程序上去。
//
// 另有两个「微信侧不可逆」的代价在接口形状上可见：撤回审核要求显式
// confirm=true（撤回不返还审核额度），发布只允许从 audit_passed 发起。

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/mpupload"
	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 微信版本接口的间接层：与 getWXACode 同一思路——本包每个函数都是一次外网调用，
// 抽成变量后单测才能注入假实现，把「提审失败不回写状态」「撤回要二次确认」
// 这些支线在本地跑一遍。
var (
	wxSubmitAudit    = wechat.SubmitAudit
	wxGetAuditStatus = wechat.GetAuditStatus
	wxRelease        = wechat.Release
	wxUndoCodeAudit  = wechat.UndoCodeAudit
)

// releaseVersionRe 版本号形态：数字/字母开头，后面可带点、横线、下划线。
// 不搞「必须 x.y.z」那种严校验——微信侧接受 "1.0.0" / "V2.1" / "2.16.0-beta"
// 等多种形态，这里只挡「明显不是版本号」的输入（中文、空格、超长），
// 真正的裁判是微信：登记时写错只影响这条记录显示，提审时微信会认它码的版本。
var releaseVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// releaseDescMax 版本描述长度上限（与 mp_release.user_desc 列宽对齐）。
const releaseDescMax = 255

// RegisterMPRelease 挂载小程序版本发布接口（全部为平台超管专用）。
//
// 路由分三组：
//   - /api/admin/companies/:id/releases    列表 + 登记 + 上传（租户维度）
//   - /api/admin/companies/:id/releases/upload-readiness   上传前探活（只读）
//   - /api/admin/releases/:id/{submit-audit,audit-status,release,undo-audit}
//     动作（记录维度）
//
// 动作组不带 companyId：租户从记录自身取（row.CompanyID），从根上排除
// 「URL 里的企业与记录归属不一致」这类错配——比在每个 handler 里再校验一次可靠。
func RegisterMPRelease(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	plat := middleware.PlatformAdminMiddleware(db)

	r.GET("/api/admin/companies/:id/releases", plat, func(c *gin.Context) {
		listMPReleases(c, db)
	})
	r.POST("/api/admin/companies/:id/releases", plat, RequireFeature("mp_release"), func(c *gin.Context) {
		createMPRelease(c, db, cfg)
	})
	r.GET("/api/admin/companies/:id/releases/upload-readiness", plat, func(c *gin.Context) {
		syncMPReleaseUploadReadiness(c, db, cfg)
	})
	r.POST("/api/admin/companies/:id/releases/upload", plat, RequireFeature("mp_release"), func(c *gin.Context) {
		uploadMPRelease(c, db, cfg)
	})
	r.POST("/api/admin/releases/:id/submit-audit", plat, RequireFeature("mp_release"), func(c *gin.Context) {
		submitMPReleaseAudit(c, db, cfg)
	})
	r.GET("/api/admin/releases/:id/audit-status", plat, func(c *gin.Context) {
		syncMPReleaseAuditStatus(c, db, cfg)
	})
	r.POST("/api/admin/releases/:id/release", plat, RequireFeature("mp_release"), func(c *gin.Context) {
		releaseMPRelease(c, db, cfg)
	})
	r.POST("/api/admin/releases/:id/undo-audit", plat, RequireFeature("mp_release"), func(c *gin.Context) {
		undoMPReleaseAudit(c, db, cfg)
	})
}

// releaseAllowed 该企业是否允许平台代发版本。
// 返回 (allowed, msg)：msg 是拒绝时给界面的原因。
//
// 语义分两种：
//   - companyID > 0（企业独立版）：查 company_wx_app 的 release_enabled。
//     没有凭据行 = 走平台全局小程序，该企业没有关过代发 → 允许；
//   - companyID = 0（平台自有小程序）：不落 company_wx_app，天然允许——
//     平台推自己的版本不需要谁来授权。
//
// 注意与「凭据是否配对」是两件事：没配凭据的企业仍可用全局小程序代发，
// 那条路在下面 mpReleaseCreds 里统一校验。
func releaseAllowed(db *gorm.DB, companyID int64) (bool, string) {
	if companyID <= 0 {
		return true, ""
	}
	var row model.CompanyWXApp
	if err := db.Where("company_id = ?", companyID).First(&row).Error; err != nil {
		// 查不到（未配凭据）视同允许：见函数注释
		return true, ""
	}
	if !row.ReleaseEnabled {
		return false, "该企业已关闭平台代发小程序版本（企业小程序凭据配置里的「允许平台代发」已关闭）"
	}
	return true, ""
}

// mpReleaseCreds 解析代发用的小程序凭据，并校验与登记时的一致性。
// 返回 (凭据, 是否因租户开关被拒, 原因)：凭据.AppID 为空即被拒，
// 调用方用 denyMPRelease 回写（403 还是 1 由 forbidden 决定）。
//
// AppID 一致性是关键：登记时把当时用的 appid 存进记录，动作时重新解析
// （租户凭据 → 全局设置 → 环境变量）。若企业中途换过小程序，新旧 appid
// 必然不同——此时提审/发布打到的是新小程序，而代码是 CI 传到旧小程序上的，
// auditid 也属于旧小程序。这种错配在微信侧只会得到一句莫名其妙的报错，
// 所以在这里就拦住，把原因说明白。
func mpReleaseCreds(db *gorm.DB, cfg config.Config, row *model.MPRelease) (wechat.Config, bool, string) {
	if ok, msg := releaseAllowed(db, row.CompanyID); !ok {
		return wechat.Config{}, true, msg
	}
	wc := wxAppOf(db, row.CompanyID, cfg)
	if wc.AppID == "" || wc.AppSecret == "" {
		return wechat.Config{}, false, "未配置小程序 AppID / AppSecret，无法代发版本（请先在企业小程序凭据里配置，或配置平台全局凭据）"
	}
	if row.AppID != "" && row.AppID != wc.AppID {
		return wechat.Config{}, false, "当前小程序凭据（AppID " + wc.AppID + "）与登记该版本时使用的（" + row.AppID + "）不一致，已拒绝操作——请确认企业没有更换小程序"
	}
	return wc, false, ""
}

// denyMPRelease 代发被拒的统一回写 + 审计。
// code 的选取有语义：租户主动关掉代发是「无权操作」（403，与 writeLocked 一致），
// 没配凭据 / AppID 不一致是「配置有问题」（1）——前端要能区分这两类，
// 前者提示去找企业开开关，后者提示去补凭据。
func denyMPRelease(c *gin.Context, db *gorm.DB, action string, row *model.MPRelease, forbidden bool, msg string) {
	code := 1
	if forbidden {
		code = 403
	}
	recordMPReleaseFail(c, db, action, row, msg)
	c.JSON(http.StatusOK, gin.H{"code": code, "msg": msg})
}

// mpReleaseJSON 记录 → 回包。statusName 由后端给（界面不做状态字典）；
// 时间戳为指针，未发生的时间点是 null 而不是 0001-01-01。
func mpReleaseJSON(row *model.MPRelease) gin.H {
	return gin.H{
		"id":          row.ID,
		"companyId":   row.CompanyID,
		"appId":       row.AppID,
		"userVersion": row.UserVersion,
		"userDesc":    row.UserDesc,
		"status":      row.Status,
		"statusName":  model.MPReleaseStatusNames[row.Status],
		"auditId":     row.AuditID,
		"auditReason": row.AuditReason,
		"source":      row.Source,
		"operatorId":  row.OperatorID,
		"submittedAt": row.SubmittedAt,
		"releasedAt":  row.ReleasedAt,
		"createdAt":   row.CreatedAt,
		"updatedAt":   row.UpdatedAt,
	}
}

// releaseRowOf 按 id 取记录；不存在时已写回包并返回 nil。
func releaseRowOf(c *gin.Context, db *gorm.DB, id int64) *model.MPRelease {
	var row model.MPRelease
	if err := db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本记录不存在"})
		return nil
	}
	return &row
}

// transitionTo 条件状态更新：只有库里那一行**仍处于 from** 才改写。
//
// 为什么不用「先查再改」：两个超管同时点发布时，两次请求都会读到
// audit_passed，都去调微信——发布是幂等的但状态会被写两次、审计也会记两份。
// 带上 status 条件后，第二个请求 RowsAffected=0，直接失败，
// 把「同一时刻只有一个动作能生效」交给数据库保证（SQLite 写锁与 MySQL
// 行锁都能满足，不依赖 FOR UPDATE 这类方言）。
// 返回 (changed, err)：changed=false 且 err=nil 表示状态已被别人改掉。
func transitionTo(tx *gorm.DB, id int64, from, to string, extra map[string]any) (bool, error) {
	if !model.CanTransition(from, to) {
		return false, nil
	}
	updates := map[string]any{"status": to}
	for k, v := range extra {
		updates[k] = v
	}
	res := tx.Model(&model.MPRelease{}).Where("id = ? AND status = ?", id, from).Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// recordMPReleaseFail 动作失败的审计。提审/发布/撤回都是「有真实后果」的动作，
// 失败的尝试同样要留痕（被状态机拒掉的也算——那说明有人在点不该点的按钮）。
func recordMPReleaseFail(c *gin.Context, db *gorm.DB, action string, row *model.MPRelease, msg string) {
	audit.Record(db, c, audit.Entry{
		Action:     action,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   row.CompanyID,
			"appId":       row.AppID,
			"userVersion": row.UserVersion,
			"status":      row.Status,
			"error":       msg,
		},
		Result: audit.ResultFail,
	})
}

// ---- 列表 / 登记 ----

// listMPReleases 某企业的版本发布记录（按 id 倒序 = 新的在前）。
// 支持 ?status= 过滤（只接受状态机里的合法值，非法值不过滤）。
func listMPReleases(c *gin.Context, db *gorm.DB) {
	companyID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	page, size := pageParams(c)

	q := db.Model(&model.MPRelease{}).Where("company_id = ?", companyID)
	if cond, arg := statusCond(c.Query("status"), model.MPReleaseStatuses...); cond != "" {
		q = q.Where(cond, arg)
	}
	var rows []model.MPRelease
	total, effPage := pagedQuery(q, "id desc", &rows, page, size)
	// 逐条转回包形状（statusName 等派生字段不落库）
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, mpReleaseJSON(&rows[i]))
	}
	pagedOK(c, out, total, effPage, size)
}

// createMPRelease 登记一个「CI 已上传」的版本 → draft。
//
// 这一步不调任何微信接口：代码是 CI 用 miniprogram-ci 传的（见文件头），
// 后台只把版本号与描述记下来，让后续提审/发布有据可查、有单可查。
// 也因此**无法在此校验版本号是否真的传过**——写错只影响记录显示，
// 提审时微信会拿它自己收到的那份代码，错版本的后果由审核环节暴露。
func createMPRelease(c *gin.Context, db *gorm.DB, cfg config.Config) {
	companyID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		UserVersion string `json:"userVersion"`
		UserDesc    string `json:"userDesc"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
		return
	}
	version := strings.TrimSpace(req.UserVersion)
	if !releaseVersionRe.MatchString(version) {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本号格式不对：1-64 位字母/数字/点/横线/下划线，如 1.0.0"})
		return
	}
	desc := strings.TrimSpace(req.UserDesc)
	if len([]rune(desc)) > releaseDescMax {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本描述过长（最多 255 字）"})
		return
	}
	// 租户存在性：企业维度接口，id 打错不该静默建出一条租户不存在的记录
	if companyID > 0 {
		var cnt int64
		db.Model(&model.Company{}).Where("id = ?", companyID).Count(&cnt)
		if cnt == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
	}
	if ok, msg := releaseAllowed(db, companyID); !ok {
		c.JSON(http.StatusOK, gin.H{"code": 403, "msg": msg})
		return
	}
	// 同一企业同一版本只登记一次：重复登记会让「当前线上是哪一版」出现两条记录，
	// 而微信侧的代码只有一份。已上线的版本号重新出现，说明是新一轮上传，
	// 也应当作新记录——但那必须先走完旧记录的生命周期，这里仍按重复拒绝，
	// 把选择权交给人（改名或先归档旧记录）而不是猜。
	var dup int64
	db.Model(&model.MPRelease{}).
		Where("company_id = ? AND user_version = ?", companyID, version).
		Count(&dup)
	if dup > 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该版本已登记过（企业 + 版本号重复），请检查版本号或使用已有记录"})
		return
	}
	// 凭据解析放在登记时：一来没配凭据就根本没有后续，早失败早提示；
	// 二来把「这次代发落在哪个小程序上」固化进记录（AppID），
	// 供后面每一步做一致性比对（见 mpReleaseCreds）。
	wc := wxAppOf(db, companyID, cfg)
	if wc.AppID == "" || wc.AppSecret == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未配置小程序 AppID / AppSecret，无法登记版本"})
		return
	}
	row := model.MPRelease{
		CompanyID:   companyID,
		AppID:       wc.AppID,
		UserVersion: version,
		UserDesc:    desc,
		Status:      model.MPReleaseDraft,
		// source 记录代码从哪来：manual = 人工登记（CI 已传，手填版本号）。
		// 后台自己传的那条路用 backend（见 uploadMPRelease）。
		Source:     "manual",
		OperatorID: c.GetInt64("staffId"),
	}
	if err := db.Create(&row).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登记失败"})
		return
	}
	audit.Record(db, c, audit.Entry{
		Action:     audit.ActionMPReleaseCreate,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   companyID,
			"appId":       row.AppID,
			"userVersion": version,
			"userDesc":    desc,
		},
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": mpReleaseJSON(&row)})
}

// ---- 上传代码（后台代传）----

// mpUploadCfg 从全局配置取上传所需的服务端环境。
// mpUploadCfg 组装上传所需的环境参数。
//
// cfg 是 RegisterMPRelease 启动时**按值**收下的那份（路由闭包里捕获的是拷贝），
// 这里在请求期再叠一次系统设置：这样「系统设置 → 后台上传代码」里改的
// Node / 模块目录 / 源码目录 / 超时**立即生效，不用重启服务**。
// 思路与 resolveWXAppID 一致（那类也是请求期解析），区别是那里只叠两个字段，
// 这里直接复用 OverlayFromSettings 的同一套「界面优先、空串回落环境变量」规则，
// 免得两处各写一半、日后对不上。
func mpUploadCfg(cfg config.Config) mpupload.Config {
	cfg.OverlayFromSettings() // cfg 是值拷贝：覆盖结果不会泄出本次请求
	return mpupload.Config{
		NodeBin:   cfg.MPNodeBin,
		ModuleDir: cfg.MPCIModuleDir,
		SourceDir: cfg.MPSourceDir,
		Timeout:   cfg.MPUploadTimeout,
	}
}

// uploadKeyOf 取该企业的小程序代码上传密钥：租户 → 平台全局。
// 与 wxAppOf 同构的三级回落，只是最后一环没有环境变量——PEM 是多行文本，
// 放不进一行一项的 runtime.env（见 migrations/0027 的说明）。
func uploadKeyOf(db *gorm.DB, companyID int64) string {
	if companyID > 0 {
		var row model.CompanyWXApp
		if err := db.Where("company_id = ?", companyID).First(&row).Error; err == nil {
			if k := strings.TrimSpace(row.UploadKey); k != "" {
				return k
			}
		}
	}
	return strings.TrimSpace(setting.GetString("wx_upload_key", ""))
}

// uploadReadiness 上传功能当前能不能用。返回值按「缺什么」分组，
// 让界面能直接告诉运维去改哪一项，而不是回一句「上传失败」。
//
// 刻意把「服务器环境」与「这把钥匙」分开列：前者要运维登服务器改，
// 后者在界面上粘贴就行，两类人的动作完全不同。
func uploadReadiness(db *gorm.DB, cfg config.Config, companyID int64, key string) []string {
	problems := mpUploadCfg(cfg).Problems()
	problems = append(problems, mpupload.KeyProblems(key)...)
	return problems
}

// syncMPReleaseUploadReadiness GET /api/admin/companies/:id/releases/upload-readiness
// 上传前探活：环境缺什么、钥匙配没配，一次列全。
// 不写审计——它是纯查询，且上传前会被频繁调用（每次打开弹窗）。
func syncMPReleaseUploadReadiness(c *gin.Context, db *gorm.DB, cfg config.Config) {
	companyID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	key := uploadKeyOf(db, companyID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"ready":    len(uploadReadiness(db, cfg, companyID, key)) == 0,
		"problems": uploadReadiness(db, cfg, companyID, key),
		// 不回传密钥本身，只回「配没配」——与 appSecret 同策略
		"keyConfigured": key != "",
	}})
}

// uploadMPRelease POST /api/admin/companies/:id/releases/upload
// 上传代码到微信（成为体验版），成功后自动建一条 draft 记录。
//
// 与「登记版本」的分工：登记是「CI 已经传过了，我手填版本号」；
// 本接口是「后台自己传」，所以版本号由上传方给定、传完即落记录，
// 不存在「填的版本号跟实际传的不是一个」这种错配。
//
// 同步执行、耗时可达分钟级（编译 + 上传）。前端必须在此期间锁死按钮并提示，
// 运维侧要把反代的 proxy_read_timeout 放到 MP_UPLOAD_TIMEOUT_SEC 以上
// （见 backend/README「小程序版本发布」一节）。
func uploadMPRelease(c *gin.Context, db *gorm.DB, cfg config.Config) {
	companyID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		UserVersion string `json:"userVersion"`
		UserDesc    string `json:"userDesc"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
		return
	}
	version := strings.TrimSpace(req.UserVersion)
	if !releaseVersionRe.MatchString(version) {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本号格式不对：1-64 位字母/数字/点/横线/下划线，如 1.0.0"})
		return
	}
	desc := strings.TrimSpace(req.UserDesc)
	if len([]rune(desc)) > releaseDescMax {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本描述过长（最多 255 字）"})
		return
	}
	if companyID > 0 {
		var cnt int64
		db.Model(&model.Company{}).Where("id = ?", companyID).Count(&cnt)
		if cnt == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
	}
	if ok, msg := releaseAllowed(db, companyID); !ok {
		c.JSON(http.StatusOK, gin.H{"code": 403, "msg": msg})
		return
	}
	// 重复版本号先拦：微信侧允许重复传同一版本号，但我们自己的记录会重key，
	// 且「已登记过」大概率意味着这版已经传过了，不该再烧一次编译
	var dup int64
	db.Model(&model.MPRelease{}).
		Where("company_id = ? AND user_version = ?", companyID, version).
		Count(&dup)
	if dup > 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该版本已登记过（企业 + 版本号重复），请检查版本号或使用已有记录"})
		return
	}
	wc := wxAppOf(db, companyID, cfg)
	if wc.AppID == "" || wc.AppSecret == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未配置小程序 AppID / AppSecret，无法上传版本"})
		return
	}
	key := uploadKeyOf(db, companyID)
	// 环境/钥匙没就绪时**不要**跑 node：缺 node 的机器上 exec 只会回一句
	// "exec: node: executable file not found in $PATH"，运维看不出要装什么
	if problems := uploadReadiness(db, cfg, companyID, key); len(problems) > 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "上传环境未就绪：" + strings.Join(problems, "；")})
		return
	}

	if err := mpupload.Upload(c.Request.Context(), mpUploadCfg(cfg), mpupload.Request{
		AppID:   wc.AppID,
		Version: version,
		Desc:    desc,
		Key:     key,
	}); err != nil {
		// 上传失败不建记录：记录是「这一版已经到微信了」的凭证，
		// 传失败却留下一条 draft，会让人以为只差提审，接着白扣一次审核额度
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionMPReleaseUpload,
			TargetType: "mp_release",
			TargetID:   "",
			Detail: map[string]any{
				"companyId":   companyID,
				"appId":       wc.AppID,
				"userVersion": version,
				"error":       truncateRunes(err.Error(), 300),
			},
			Result: audit.ResultFail,
		})
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
		return
	}

	row := model.MPRelease{
		CompanyID:   companyID,
		AppID:       wc.AppID,
		UserVersion: version,
		UserDesc:    desc,
		Status:      model.MPReleaseDraft,
		// source=backend：代码是后台自己传的（版本号与代码必然一致），
		// 与 ci（CI 传的）/ manual（人工登记）区分开
		Source:     "backend",
		OperatorID: c.GetInt64("staffId"),
	}
	if err := db.Create(&row).Error; err != nil {
		// 代码已经在微信侧了，记录却没建上——必须说清楚，
		// 否则运营会再传一次（白烧一次编译）或以为没传
		c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "代码已上传成功，但版本记录保存失败，请刷新列表查看；请勿重复上传同一版本号"})
		return
	}
	audit.Record(db, c, audit.Entry{
		Action:     audit.ActionMPReleaseUpload,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   companyID,
			"appId":       row.AppID,
			"userVersion": version,
			"userDesc":    desc,
			"source":      row.Source,
		},
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": mpReleaseJSON(&row)})
}

// errReleaseStateChanged 事务内条件更新失败：状态已被并发请求改掉。
// 单独一个哨兵错误是为了把「并发撞车」与「微信报错」「数据库错误」区分开——
// 前者要回「状态刚被其他操作改变，请刷新后重试」，后两者是系统错误。
var errReleaseStateChanged = errors.New("mp release status changed concurrently")

// ---- 提交审核 ----

// submitMPReleaseAudit 提交审核：draft / rejected / withdrawn ──► auditing。
//
// 顺序很讲究：先在事务里**条件占位**（status → auditing），再调微信，
// 微信失败则整个事务回滚、状态回到原值。反过来（先调微信再改状态）会在
// 微信成功、数据库写失败的缝里留下一条「微信在审、本地还显示待提审」的记录，
// 而重试提审会产生第二个 auditid——额度白扣一次。
//
// 不传 item_list：类目取小程序后台已配的（官方 ext 文档明确自有 token 路径
// 可以只调 submit_audit）。哪个租户后台类目没配齐，微信会在这里报错，
// 把 errmsg 原样透出即可，不需要我们猜类目。
func submitMPReleaseAudit(c *gin.Context, db *gorm.DB, cfg config.Config) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	row := releaseRowOf(c, db, id)
	if row == nil {
		return
	}
	// 预检只为给出人话错误；真正的竞态保证在事务内的条件更新（见 transitionTo）
	if !model.CanTransition(row.Status, model.MPReleaseAuditing) {
		msg := "当前状态（" + model.MPReleaseStatusNames[row.Status] + "）不能提交审核"
		recordMPReleaseFail(c, db, audit.ActionMPReleaseSubmit, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	wc, forbidden, msg := mpReleaseCreds(db, cfg, row)
	if wc.AppID == "" {
		denyMPRelease(c, db, audit.ActionMPReleaseSubmit, row, forbidden, msg)
		return
	}
	from := row.Status

	err := db.Transaction(func(tx *gorm.DB) error {
		changed, err := transitionTo(tx, id, row.Status, model.MPReleaseAuditing, nil)
		if err != nil {
			return err
		}
		if !changed {
			return errReleaseStateChanged
		}
		auditID, err := wxSubmitAudit(wc, nil)
		if err != nil {
			return err
		}
		now := time.Now()
		// audit_reason 清空：它只描述「当前这一审核单」的驳回原因，
		// 重新提审就是新的一单，旧原因留着会误导排查。
		return tx.Model(&model.MPRelease{}).Where("id = ?", id).Updates(map[string]any{
			"audit_id":     auditID,
			"submitted_at": &now,
			"audit_reason": "",
		}).Error
	})
	if err != nil {
		msg := "提交审核失败：" + err.Error()
		recordMPReleaseFail(c, db, audit.ActionMPReleaseSubmit, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	db.First(row, id)
	audit.Record(db, c, audit.Entry{
		Action:     audit.ActionMPReleaseSubmit,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   row.CompanyID,
			"appId":       row.AppID,
			"userVersion": row.UserVersion,
			"auditId":     row.AuditID,
			"from":        from,
		},
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": mpReleaseJSON(row)})
}

// ---- 发布 ----

// releaseMPRelease 发布：audit_passed ──► released（终态）。
//
// 微信侧 release 发的是「当前审过的那一版」，不带任何参数——所以本地记录
// 必须停留在 audit_passed 才允许点，状态机保证这一点（见 model.CanTransition）。
// 事务内先条件占位再调微信：微信失败回滚，记录退回 audit_passed 可重试；
// 微信成功后的缝里若数据库提交失败，本地会停在 audit_passed 而线上其实已更新，
// 此时再点一次发布微信会报「没有可发布的版本」——错误信息指明了真相，
// 好过静默地把一条已上线记录再推一遍。
func releaseMPRelease(c *gin.Context, db *gorm.DB, cfg config.Config) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	row := releaseRowOf(c, db, id)
	if row == nil {
		return
	}
	if !model.CanTransition(row.Status, model.MPReleaseReleased) {
		msg := "当前状态（" + model.MPReleaseStatusNames[row.Status] + "）不能发布，需审核通过后才行"
		recordMPReleaseFail(c, db, audit.ActionMPReleaseRelease, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	wc, forbidden, msg := mpReleaseCreds(db, cfg, row)
	if wc.AppID == "" {
		denyMPRelease(c, db, audit.ActionMPReleaseRelease, row, forbidden, msg)
		return
	}
	from := row.Status

	err := db.Transaction(func(tx *gorm.DB) error {
		changed, err := transitionTo(tx, id, from, model.MPReleaseReleased, nil)
		if err != nil {
			return err
		}
		if !changed {
			return errReleaseStateChanged
		}
		if err := wxRelease(wc); err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&model.MPRelease{}).Where("id = ?", id).
			Updates(map[string]any{"released_at": &now}).Error
	})
	if err != nil {
		msg := "发布失败：" + err.Error()
		recordMPReleaseFail(c, db, audit.ActionMPReleaseRelease, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	db.First(row, id)
	audit.Record(db, c, audit.Entry{
		Action:     audit.ActionMPReleaseRelease,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   row.CompanyID,
			"appId":       row.AppID,
			"userVersion": row.UserVersion,
			"auditId":     row.AuditID,
			"from":        from,
		},
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": mpReleaseJSON(row)})
}

// ---- 撤回审核 ----

// undoMPReleaseAudit 撤回审核：auditing ──► withdrawn。
//
// **撤回不返还已消耗的审核额度**（微信官方硬约束），所以本接口要求请求体
// 显式带 confirm=true——前端必须弹二次确认，服务端这里是最后一道闸：
// 少了这个字段一律拒绝，不给「默认同意」的可能。
func undoMPReleaseAudit(c *gin.Context, db *gorm.DB, cfg config.Config) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	row := releaseRowOf(c, db, id)
	if row == nil {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.Confirm {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "撤回审核消耗的审核额度不予返还，请二次确认（confirm=true）后再提交"})
		return
	}
	if !model.CanTransition(row.Status, model.MPReleaseWithdrawn) {
		msg := "当前状态（" + model.MPReleaseStatusNames[row.Status] + "）不能撤回，仅审核中可撤回"
		recordMPReleaseFail(c, db, audit.ActionMPReleaseUndoAudit, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	wc, forbidden, msg := mpReleaseCreds(db, cfg, row)
	if wc.AppID == "" {
		denyMPRelease(c, db, audit.ActionMPReleaseUndoAudit, row, forbidden, msg)
		return
	}
	from := row.Status

	err := db.Transaction(func(tx *gorm.DB) error {
		changed, err := transitionTo(tx, id, from, model.MPReleaseWithdrawn, nil)
		if err != nil {
			return err
		}
		if !changed {
			return errReleaseStateChanged
		}
		return wxUndoCodeAudit(wc)
	})
	if err != nil {
		msg := "撤回审核失败：" + err.Error()
		recordMPReleaseFail(c, db, audit.ActionMPReleaseUndoAudit, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	db.First(row, id)
	audit.Record(db, c, audit.Entry{
		Action:     audit.ActionMPReleaseUndoAudit,
		TargetType: "mp_release",
		TargetID:   strconv.FormatInt(row.ID, 10),
		Detail: map[string]any{
			"companyId":   row.CompanyID,
			"appId":       row.AppID,
			"userVersion": row.UserVersion,
			"auditId":     row.AuditID,
			"from":        from,
			"note":        "撤回不返还审核额度",
		},
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": mpReleaseJSON(row)})
}

// ---- 查审核状态（顺带同步） ----

// syncMPReleaseAuditStatus 查微信审核状态，终局时把结果同步进本地记录。
//
// 「查」与「同步」合成一个动作是刻意的：审核结果（通过 / 驳回 + 原因）只在
// 微信侧，不同步下来本地记录的 status 就永远是 auditing，发布按钮永远不亮。
// 分开做两个按钮只会让人漏点后一个。
//
// 非终局（审核中 / 延后）不落库：本地状态本来就是 auditing，写它等于
// 每次轮询都刷 updated_at，把「这条记录最近动过」的信号冲掉。
func syncMPReleaseAuditStatus(c *gin.Context, db *gorm.DB, cfg config.Config) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	row := releaseRowOf(c, db, id)
	if row == nil {
		return
	}
	if row.AuditID <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "尚未提审，没有审核单可查（请先提交审核）"})
		return
	}
	wc, forbidden, msg := mpReleaseCreds(db, cfg, row)
	if wc.AppID == "" {
		denyMPRelease(c, db, audit.ActionMPReleaseStatus, row, forbidden, msg)
		return
	}
	st, err := wxGetAuditStatus(wc, row.AuditID)
	if err != nil {
		msg := "查询审核状态失败：" + err.Error()
		recordMPReleaseFail(c, db, audit.ActionMPReleaseStatus, row, msg)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
		return
	}
	target, terminal := model.WXAuditStatusToRelease(st.Status)
	from := row.Status
	changed := false
	if terminal {
		extra := map[string]any{}
		if target == model.MPReleaseRejected {
			// 驳回原因落库（界面直接展示）；审核截图只存 media_id 在微信侧，
			// 不引入素材下载依赖——排查时去公众平台看更直接。
			extra["audit_reason"] = truncateRunes(st.Reason, 500)
		}
		// 非白名单流向（如已 released 又查到 0）会在这里返回 changed=false，
		// 不当错误处理：本地状态比一次轮询结果更接近事实。
		changed, err = transitionTo(db, id, from, target, extra)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "同步审核状态失败：" + err.Error()})
			return
		}
	}
	db.First(row, id)
	if changed {
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionMPReleaseStatus,
			TargetType: "mp_release",
			TargetID:   strconv.FormatInt(row.ID, 10),
			Detail: map[string]any{
				"companyId":   row.CompanyID,
				"appId":       row.AppID,
				"userVersion": row.UserVersion,
				"auditId":     row.AuditID,
				"from":        from,
				"to":          target,
				"wxStatus":    st.Status,
				"reason":      truncateRunes(st.Reason, 200),
			},
		})
	}
	data := mpReleaseJSON(row)
	data["wxStatus"] = st.Status
	data["changed"] = changed
	if !terminal {
		data["note"] = "微信仍在审核中（status=2/4），已返回实时状态，本地记录未变动"
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
}

// truncateRunes 按字符截断（不是字节）：中文原因按字节切会出乱码。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
