package handler

import (
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/mediachk"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 图片内容安全（异步检测）的接线层。
//
// 整条链路是「三段式」，任何一段断开都表现为**同一件事**——违规图一直在线：
//
//	① 上传成功后调 /wxa/media_check_async，拿到 trace_id 并落一条 pending 记录；
//	② 微信通过「消息推送」把结论推回来，按 trace_id 更新记录；
//	③ 后台列表里看到 risky 的记录，人工确认后一键下架（覆盖成占位图）。
//
// 为什么处置不做成自动的：微信误判率不低，自动覆盖是不可逆的（原图当场丢失），
// 一次误判就等于把用户的头像/相册永久毁掉。所以这里只把结论摆出来，
// 让人点一下——代价是处置有延迟，换的是「不会误伤」。
//
// 为什么①②之间必须有一条数据库记录而不是内存 map：进程重启、多副本部署、
// 微信重推这几种情况都会让内存态失效，而 trace_id 是唯一能把回调对回图的东西。

// mediaCheckAsync 送检接口的间接层：生产就是 wechat.MediaCheckAsync。
// 抽出来是为了让「送检成功 / 送检失败」两条支线都能在本地跑通，不必真打微信。
var mediaCheckAsync = wechat.MediaCheckAsync

// RegisterMediaCheck 注册图片内容安全相关路由：
//   - GET/POST /api/wechat/msgpush     微信消息推送（公网无鉴权，靠签名自证）
//   - GET  /api/admin/media-checks     检测记录列表
//   - GET  /api/admin/media-checks/config  推送配置状态（给后台显示该填什么）
//   - POST /api/admin/media-checks/:id/takedown  一键下架
func RegisterMediaCheck(r *gin.Engine, db *gorm.DB, cfg config.Config, up storage.Uploader, refresher storage.CDNRefresher) {
	r.GET("/api/wechat/msgpush", msgPushEcho(cfg))
	r.POST("/api/wechat/msgpush", msgPushReceive(db, cfg))

	admin := middleware.AuthMiddleware(db, "admin")
	r.GET("/api/admin/media-checks", admin, listMediaChecks(db))
	r.GET("/api/admin/media-checks/config", admin, mediaCheckConfig(db, cfg))
	r.POST("/api/admin/media-checks/:id/takedown", admin, takedownMediaCheck(db, up, refresher))
}

// ---- ① 送检 ----

// mediaCheckParams 组装送检参数。
//
// PushReady 是这里最关键的一项：没配消息推送 Token 时**不送检**。
// 送检本身能成功（拿到 trace_id、落一条 pending），但结论永远不会回来——
// 后台会出现一堆永远「待结论」的记录，运营以为在管，实际一条结论都没收到。
// 与其制造这种假象，不如不送，并在日志里明说原因。
func mediaCheckParams(db *gorm.DB, cfg config.Config, companyID int64, openid string) mediachk.Params {
	return mediachk.Params{
		Enabled:   setting.GetBool("content_sec_enabled", true),
		Cred:      wxAppOf(db, companyID, cfg),
		OpenID:    openid,
		PushReady: resolveWXMsgToken(cfg) != "",
		BaseURL:   cfg.UploadBaseURL,
		Scene:     wechat.SceneProfile,
	}
}

// submitMediaCheck 上传成功后触发送检（不阻塞响应）。
//
// 放在 goroutine 里是因为 media_check_async 要先取 access_token——首次调用是
// 一次真实的外网往返，放在上传响应路径上会让每次上传都多几百毫秒。
// 代价是「上传成功但送检失败」只留在日志里，这正是 recordMediaCheck 要把
// 失败也写成一条 failed 记录的原因。
func submitMediaCheck(db *gorm.DB, cfg config.Config, c *gin.Context, rawURL string) {
	if db == nil || !isImageURL(rawURL) {
		// mp4 不送检：/wxa/media_check_async 只支持音频与图片，
		// 视频检测不在小程序接口范围内（见 wechat/media.go 的说明）。
		return
	}
	role := c.GetString("role")
	staffID := c.GetInt64("staffId")
	companyID := c.GetInt64("companyId")
	openid := ""
	source := "admin"
	if role == "employee" {
		source = "employee"
		openid = employeeOpenID(db, staffID)
	} else if role == "" {
		// 匿名端点（/api/apply/open/avatar）：访客还没提交申请，没有登录态，
		// 但客户端能拿到自己的 openid（wx.login 静默换取，无需授权），
		// 并随上传请求一起带上来——有这个 openid 就能送内容安全检测。
		//
		// 为什么必须送检：这张图上传成功即返回永久 URL，是**立刻公开可访问**的，
		// 而它最终会写进员工头像（审批通过时 apply.go 把申请单的 avatar 落到
		// 员工记录）。不送检就等于「违规图在人工审核之前已经挂在网上」。
		// 拿不到 openid 时（老版本客户端）仍按人工审核兜底，见下。
		source = "apply"
		openid = formValue(c, "openid")
	}

	p := mediaCheckParams(db, cfg, companyID, openid)
	d := mediachk.Plan(p, rawURL)
	if !d.Send {
		// disabled / no_url 太常见（每次非图片上传都会命中前者），不值得刷日志；
		// 其余原因都是「配置没到位」，必须留痕，否则事后查不出为什么没检。
		if d.Skip != "disabled" && d.Skip != "no_url" {
			log.Printf("[MEDIA-CHECK] skipped (%s) url=%s", d.Skip, rawURL)
		}
		return
	}
	go recordMediaCheck(db, p, d, source, openid, companyID)
}

// recordMediaCheck 同步送检并落库，返回写入的记录（失败时也返回，便于测试断言）。
//
// 为什么失败也要落一条：这张图确实被上传了、也确实没被检测过。只留日志的话，
// 后台看不到「有一批图没检」，而日志会被轮转掉。
func recordMediaCheck(db *gorm.DB, p mediachk.Params, d mediachk.Decision, source, openid string, companyID int64) *model.MediaCheck {
	row := &model.MediaCheck{
		CompanyID: companyID,
		URL:       d.URL,
		ObjectKey: d.Key,
		MediaType: wechat.MediaImage,
		Scene:     p.Scene,
		OpenID:    openid,
		Source:    source,
		Status:    model.MediaCheckPending,
	}
	trace, err := mediaCheckAsync(p.Cred, wechat.MediaCheckReq{
		MediaURL:  d.URL,
		MediaType: wechat.MediaImage,
		OpenID:    openid,
		Scene:     p.Scene,
	})
	if err != nil {
		row.Status = model.MediaCheckFailed
		row.Note = "send_failed: " + err.Error()
		log.Printf("[MEDIA-CHECK] send_failed url=%s err=%v", d.URL, err)
	} else {
		row.TraceID = trace
		log.Printf("[MEDIA-CHECK] submitted trace=%s url=%s", trace, d.URL)
	}
	if db != nil {
		if err := db.Create(row).Error; err != nil {
			log.Printf("[MEDIA-CHECK] save_failed trace=%s err=%v", row.TraceID, err)
			return row
		}
	}
	return row
}

// employeeOpenID 取员工的微信 openid（图片检测 v2 的必填项）。
// 查不到就返回空串，由 mediachk.Plan 按 no_openid 跳过——绝不因此让上传失败。
func employeeOpenID(db *gorm.DB, staffID int64) string {
	if db == nil || staffID <= 0 {
		return ""
	}
	var openid string
	if err := db.Model(&model.Employee{}).Where("staff_id = ?", staffID).
		Select("openid").Scan(&openid).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(openid)
}

// formValue 从 multipart 表单里取一个普通字段的值。
//
// 用于匿名上传端点取客户端带上来的 openid：wx.uploadFile 的 formData 会
// 作为 multipart 的普通字段提交，与文件字段并存。
//
// 取不到 / 解析失败一律返回空串——调用方据此跳过送检（人工审核兜底），
// 绝不因为读不到 openid 就让上传失败。
func formValue(c *gin.Context, key string) string {
	form, err := c.MultipartForm()
	if err != nil || form == nil {
		return ""
	}
	vals := form.Value[key]
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

// isImageURL 按扩展名判断是否图片。mp4 明确排除（见 submitMediaCheck 注释）。
func isImageURL(u string) bool {
	switch strings.ToLower(filepath.Ext(u)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// ---- ② 消息推送回调 ----

// msgPushEcho 微信配置推送 URL 时的校验：验签通过就原样回 echostr。
//
// 不验签会怎样：任何人都能拿这个端点做「回显探测器」，而微信那边的配置
// 一旦被别人的 URL 抢先验过，后续推送就全推到别人服务器上去了。
func msgPushEcho(cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := resolveWXMsgToken(cfg)
		if token == "" {
			c.String(http.StatusServiceUnavailable, "msg push not configured")
			return
		}
		if !wechat.VerifySignature(token, c.Query("timestamp"), c.Query("nonce"), c.Query("signature")) {
			c.String(http.StatusForbidden, "invalid signature")
			return
		}
		c.String(http.StatusOK, c.Query("echostr"))
	}
}

// pushEnvelope 安全模式推送的外层：body 只有一个 Encrypt 字段。
type pushEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}

// msgPushReceive 接收微信推送，更新检测结论。
//
// 回包必须是纯文本 "success"（200）：微信只认这个，回别的会触发重推，
// 连续失败后会把整条推送通道停掉——那时图片检测就永久收不到结论了。
// 所以「不认识的事件」「trace_id 对不上」都算处理成功：重推也不会有不同结果。
func msgPushReceive(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := resolveWXMsgToken(cfg)
		if token == "" {
			c.String(http.StatusServiceUnavailable, "msg push not configured")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if err != nil {
			c.String(http.StatusBadRequest, "read body failed")
			return
		}
		body, ok := decryptPush(c, cfg, token, string(raw))
		if !ok {
			return // decryptPush 已写响应
		}
		ev, isMedia := wechat.ParseMediaCheckEvent(body)
		if !isMedia {
			// 推送 URL 是独占的：关注、扫码等事件也会推过来，一律回 success
			c.String(http.StatusOK, "success")
			return
		}
		if !ev.Finished() {
			// status_code=0/1 表示微信还在检，此时写库会把「检测中」当成结论
			c.String(http.StatusOK, "success")
			return
		}
		if err := applyMediaCheckEvent(db, ev); err != nil {
			// 落库失败回 500 让微信重推：结论丢了就再也找不回来了
			log.Printf("[MEDIA-CHECK] apply_failed trace=%s err=%v", ev.TraceID, err)
			c.String(http.StatusInternalServerError, "retry")
			return
		}
		c.String(http.StatusOK, "success")
	}
}

// decryptPush 验签 + （安全模式）解密，返回明文 XML。
// 第二个返回值为 false 时已写响应，调用方直接返回。
func decryptPush(c *gin.Context, cfg config.Config, token, raw string) (string, bool) {
	// encrypt_type=aes 是安全模式的标志；同时兼容只带 msg_signature 的实现
	encrypted := c.Query("encrypt_type") == "aes" || c.Query("msg_signature") != ""
	if !encrypted {
		if !wechat.VerifySignature(token, c.Query("timestamp"), c.Query("nonce"), c.Query("signature")) {
			c.String(http.StatusForbidden, "invalid signature")
			return "", false
		}
		return raw, true
	}

	var env pushEnvelope
	if err := xml.Unmarshal([]byte(raw), &env); err != nil || env.Encrypt == "" {
		c.String(http.StatusBadRequest, "bad encrypted body")
		return "", false
	}
	// 安全模式的签名参数是 msg_signature，且密文本身也参与签名
	if !wechat.VerifySignature(token, c.Query("timestamp"), c.Query("nonce"),
		c.Query("msg_signature"), env.Encrypt) {
		c.String(http.StatusForbidden, "invalid signature")
		return "", false
	}
	key := resolveWXMsgAESKey(cfg)
	if key == "" {
		log.Printf("[MEDIA-CHECK] 收到安全模式推送但未配置 EncodingAESKey，无法解密")
		c.String(http.StatusServiceUnavailable, "aes key not configured")
		return "", false
	}
	// appID 传空 = 不校验：企业独立版下同一个推送 URL 会收到多个小程序的推送，
	// 写死一个 appID 会把其它租户的结论全部拒掉。
	plain, err := wechat.DecryptMsg(key, env.Encrypt, "")
	if err != nil {
		log.Printf("[MEDIA-CHECK] 解密推送失败: %v", err)
		c.String(http.StatusBadRequest, "decrypt failed")
		return "", false
	}
	return plain, true
}

// applyMediaCheckEvent 按 trace_id 更新检测结论。
//
// 规则：**不允许从 risky 降级**。微信重推时带的是同一条结论（幂等），
// 但若某次推送里少了 result 字段、归一成 failed，直接覆盖就会把一条已确认的
// 违规结论冲掉——而那条结论正是下架的依据。宁可保留原判，记一行日志。
func applyMediaCheckEvent(db *gorm.DB, ev wechat.MediaCheckEvent) error {
	if db == nil {
		return nil
	}
	var row model.MediaCheck
	err := db.Where("trace_id = ?", ev.TraceID).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 对不上通常是环境串了（测试/预发的推送打到了生产），重推也没用
			log.Printf("[MEDIA-CHECK] 未知 trace_id=%s（可能来自其它环境）", ev.TraceID)
			return nil
		}
		return err
	}
	status := mediachk.Normalize(ev.Suggest)
	if row.Status == model.MediaCheckRisky && status != model.MediaCheckRisky {
		log.Printf("[MEDIA-CHECK] 拒绝降级 trace=%s %s -> %s", ev.TraceID, row.Status, status)
		return nil
	}
	updates := map[string]any{
		"status":  string(status),
		"suggest": ev.Suggest,
		"label":   ev.Label,
		"raw":     ev.Raw,
	}
	if ev.ErrCode != 0 || status == model.MediaCheckFailed {
		note := ev.ErrMsg
		if note == "" {
			note = "微信侧检测失败"
		}
		updates["note"] = note
	}
	log.Printf("[MEDIA-CHECK] settled trace=%s status=%s label=%d", ev.TraceID, status, ev.Label)
	return db.Model(&model.MediaCheck{}).Where("id = ?", row.ID).Updates(updates).Error
}

// ---- ③ 后台：列表 / 配置状态 / 下架 ----

func listMediaChecks(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, size := pageParams(c)
		q := db.Model(&model.MediaCheck{})
		// 租户隔离：平台超管（company_id=0）看全部，企业管理员只看自己企业
		if cid := middleware.CurrentCompanyID(c); cid != 0 {
			q = q.Where("company_id = ?", cid)
		}
		if cond, arg := statusCond(c.Query("status"),
			string(model.MediaCheckPending), string(model.MediaCheckPass),
			string(model.MediaCheckReview), string(model.MediaCheckRisky),
			string(model.MediaCheckFailed)); cond != "" {
			q = q.Where(cond, arg)
		}
		if c.Query("onlyTakedownable") == "1" {
			q = q.Where("status = ? AND action <> ?", string(model.MediaCheckRisky), model.MediaActionTakedown)
		}
		var rows []model.MediaCheck
		total, page := pagedQuery(q, "id desc", &rows, page, size)
		pagedOK(c, rows, total, page, size)
	}
}

// mediaCheckConfig 把「要让图片检测跑起来还缺什么」直接回给后台。
//
// 这三项都是运营在微信公众平台那边要填/要抄的值，散在文档里没人看，
// 放在界面上才能真的配起来。
func mediaCheckConfig(db *gorm.DB, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid := middleware.CurrentCompanyID(c)
		cred := wxAppOf(db, cid, cfg)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"enabled":    setting.GetBool("content_sec_enabled", true),
			"tokenReady": resolveWXMsgToken(cfg) != "",
			"aesReady":   resolveWXMsgAESKey(cfg) != "",
			"credReady":  cred.AppID != "" && cred.AppSecret != "",
			"pushUrl":    msgPushURL(cfg),
			"pending":    countMediaChecks(db, cid, model.MediaCheckPending, false),
			// 违规数只算「还没处置」的：已下架的也计入会让运营以为还有一批没处理，
			// 而这个数字的唯一用途就是提示「有几张图等着你点下架」。
			"risky":         countMediaChecks(db, cid, model.MediaCheckRisky, true),
			"takedownReady": true,
		}})
	}
}

// msgPushURL 由上传基址推出推送地址：上传基址是唯一一处「本服务对外是什么地址」
// 的配置，再单开一项只为了拼一个 URL，运维必然会漏配其中一个。
func msgPushURL(cfg config.Config) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.UploadBaseURL), "/")
	base = strings.TrimSuffix(base, "/uploads")
	if base == "" {
		return ""
	}
	return base + "/api/wechat/msgpush"
}

// countMediaChecks 统计某状态的记录数；onlyUnhandled 为真时排除已下架的。
func countMediaChecks(db *gorm.DB, companyID int64, status model.MediaCheckStatus, onlyUnhandled bool) int64 {
	var n int64
	q := db.Model(&model.MediaCheck{}).Where("status = ?", string(status))
	if onlyUnhandled {
		q = q.Where("action <> ?", model.MediaActionTakedown)
	}
	if companyID != 0 {
		q = q.Where("company_id = ?", companyID)
	}
	_ = q.Count(&n).Error
	return n
}

func takedownMediaCheck(db *gorm.DB, up storage.Uploader, refresher storage.CDNRefresher) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if id <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var row model.MediaCheck
		if err := db.First(&row, id).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "记录不存在"})
			return
		}
		if cid := middleware.CurrentCompanyID(c); cid != 0 && row.CompanyID != cid {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "无权操作该记录"})
			return
		}
		if !row.Takedownable() {
			// 只有「命中违规 + 未处置 + 推得出对象键」的才允许下架：
			// 覆盖是不可逆的，不能让它变成一个通用的「随便删图」按钮。
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该记录不可下架（仅命中违规且未处置的记录可下架）"})
			return
		}
		if up == nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "存储未初始化"})
			return
		}
		data, contentType := mediachk.PlaceholderFor(row.ObjectKey)
		if _, err := up.SaveBytes(row.ObjectKey, data, contentType); err != nil {
			log.Printf("[MEDIA-CHECK] takedown_failed id=%d key=%s err=%v", row.ID, row.ObjectKey, err)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "下架失败：" + err.Error()})
			return
		}
		// 覆盖写之后必须刷 CDN：不刷的话边缘节点会继续吐旧图，
		// 表现为「后台显示已下架，但用户看到的还是那张违规图」。
		refreshCDN(refresher, row.URL)

		now := time.Now()
		handlerID := c.GetInt64("staffId")
		if err := db.Model(&model.MediaCheck{}).Where("id = ?", row.ID).Updates(map[string]any{
			"action":     model.MediaActionTakedown,
			"handled_at": &now,
			"handler_id": handlerID,
		}).Error; err != nil {
			// 对象已经覆盖了，这里失败只影响「有没有留痕」，不回滚——
			// 回滚也回不去（原图已丢），如实报错让人手工补记。
			log.Printf("[MEDIA-CHECK] takedown_mark_failed id=%d err=%v", row.ID, err)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图片已下架，但记录更新失败，请刷新后重试"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionMediaTakedown,
			TargetType: "media_check",
			TargetID:   strconv.FormatInt(row.ID, 10),
			Detail: map[string]any{
				"url":   row.URL,
				"label": row.Label,
				"trace": row.TraceID,
			},
		})
		log.Printf("[MEDIA-CHECK] takedown id=%d key=%s by=%d", row.ID, row.ObjectKey, handlerID)
		c.JSON(http.StatusOK, gin.H{"code": 0})
	}
}

// ---- 推送凭据解析 ----
//
// 与 resolveWXAppID 同构：优先系统设置（改完即时生效），留空回退环境变量。
// 不用启动时一次性读进 cfg 的方式，是因为「回调收不到」这件事排查成本很高，
// 而运维的第一反应一定是「去后台把 Token 改对」——若那需要重启才生效，
// 他改完发现还是不行，只会往更偏的方向查。

func resolveWXMsgToken(cfg config.Config) string {
	if v := strings.TrimSpace(setting.GetString("wx_msg_token", "")); v != "" {
		return v
	}
	return strings.TrimSpace(cfg.WXMsgToken)
}

func resolveWXMsgAESKey(cfg config.Config) string {
	if v := strings.TrimSpace(setting.GetString("wx_msg_aeskey", "")); v != "" {
		return v
	}
	return strings.TrimSpace(cfg.WXMsgAESKey)
}
