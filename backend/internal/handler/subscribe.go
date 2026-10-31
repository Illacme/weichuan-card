package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 审核结果的订阅消息通知。
//
// 为什么需要：员工在小程序里提交开通申请后就关掉了小程序，审批往往几小时后才发生。
// 没有这条通知，他永远不会知道自己已经能用了——「功能做完了但没人用」的典型形态，
// 而且管理员还得另外用微信/电话告诉他初始密码。
//
// 为什么只做「审核结果」这一类：微信的一次性订阅是**用户同意一次只能下发一条**，
// 长期订阅又只对政务/医疗等类目开放。所以「名片被访问就通知员工」这类高频事件
// 在这里做不出来——用户同意一次只收到第一条，之后再也不响，看起来就是坏的。
// 与其做一个会让人误解的功能，不如不做（README 里已把这条限制写清）。
//
// 审核结果是低频、用户强预期的事件，正好落在一次性订阅的适用区间里。

// auditNotice 一条审核结果通知的素材。
//
// 刻意只放「事实」，不放模板字段名：字段名由运营在公众平台选模板时决定，
// 存在 sys_setting 里（wx_tmpl_audit_fields），这里按顺序填三个值。
type auditNotice struct {
	CompanyID int64
	// OpenID 接收者。空串表示这个人没绑微信，没有可送达的通道，直接跳过。
	OpenID string
	// Subject 事项名称（模板第一项，thing 类型，≤20 字符）
	Subject string
	// Approved 审核是否通过，决定模板第二项（phrase 类型，≤5 个汉字）
	Approved bool
	// Remark 备注说明（模板第三项，thing 类型，≤20 字符）
	Remark string
	// Page 点开卡片后跳转的小程序页面（不带前导斜杠）
	Page string
}

// dispatch 异步执行 fn。
//
// 抽成变量不是为了「可替换」，而是为了**可测**：通知必须在 goroutine 里发
// （postWX 用的 http.Post 没有超时，微信侧卡住时会把审批接口一起拖死），
// 但测试需要同步等待，否则断言与 goroutine 竞争、时快时慢。
var dispatch = func(fn func()) { go fn() }

// sendSubscribe 是 wechat.SendSubscribe 的间接层，唯一目的是可测（同 code2Session）。
var sendSubscribe = wechat.SendSubscribe

// auditTemplate 解析「审核结果通知」的模板 ID 与字段名。
//
// 字段名可配而不是写死：模板是运营在公众平台**选**的，不同模板的字段名不同
// （thing1 / phrase2 / character_string3 …）。写死会让「运营换个模板，代码就
// 静默发不出去」——现象只是「审批成功了但员工没收到」，极难往配置上想。
//
// 取值链（两级），与 wxAppOf 的凭据解析保持一致：
//  1. 企业独立版小程序：company_wx_app 里配了 own AppID **且** AppSecret（即
//     真正走自己凭据的），用自家账号下的 SubscribeTmplID。模板与 access_token
//     同属一个 AppID，必须一致，否则微信回「模板不存在」静默发不出去。
//     若自家配了凭据却没配模板 → 视为「该企业不发」（不回落平台模板，那是另一个 AppID 的）。
//  2. 平台模式（companyID=0，或本企业无 own 凭据）→ 用全局 sys_setting.wx_tmpl_audit。
func auditTemplate(db *gorm.DB, companyID int64) (tplID string, fields []string) {
	if companyID != 0 && db != nil {
		var row model.CompanyWXApp
		// 与 wxAppOf 同口径：AppID 与 AppSecret 都非空才算「走自己凭据」。
		if err := db.Where("company_id = ?", companyID).First(&row).Error; err == nil &&
			strings.TrimSpace(row.AppID) != "" && strings.TrimSpace(row.AppSecret) != "" {
			if tpl := strings.TrimSpace(row.SubscribeTmplID); tpl != "" {
				return tpl, parseTemplateFields(row.SubscribeTmplFields)
			}
			// 自有凭据却没配模板：不回落平台模板（AppID 不同必失败），直接视为关闭
			return "", nil
		}
	}
	// 平台模式 / 回落：全局模板
	tplID = strings.TrimSpace(setting.GetString("wx_tmpl_audit", ""))
	raw := setting.GetString("wx_tmpl_audit_fields", "thing1,phrase2,thing3")
	return tplID, parseTemplateFields(raw)
}

// parseTemplateFields 把「thing1,phrase2,thing3」拆成去空格的非空切片。
// 留空时给默认 thing1,phrase2,thing3（企业独立版模板大多也是这个布局）。
func parseTemplateFields(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = "thing1,phrase2,thing3"
	}
	var fields []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// clipRunes 按**字符数**截断（微信的 thing ≤20 字符、phrase ≤5 字符都是按字符算）。
// 按字节切会把一个汉字劈成半个，微信收到非法 UTF-8 会回 47003，
// 而现象是「明明配好了却发不出去」。
func clipRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

// notifyAudit 组装并投递一条审核结果通知，返回待执行的函数。
//
// 返回函数而不是直接起 goroutine：调用方决定时机（`dispatch(notifyAudit(...))`），
// 测试也能同步跑完。所有失败路径都只记日志——**通知失败绝不能影响审批本身**，
// 审批成功与否是业务事实，通知只是锦上添花。
func notifyAudit(db *gorm.DB, cfg config.Config, n auditNotice) func() {
	return func() {
		openid := strings.TrimSpace(n.OpenID)
		if openid == "" {
			return // 没绑微信：没有可送达的通道，静默跳过
		}
		tplID, fields := auditTemplate(db, n.CompanyID)
		if tplID == "" {
			return // 未配置模板 = 功能未启用，静默跳过（不是错误）
		}
		if len(fields) != 3 {
			log.Printf("[SUBSCRIBE] 模板字段配置有误：需要 3 项（事项/结果/备注），实际 %d 项 %v", len(fields), fields)
			return
		}
		result := "审核通过"
		if !n.Approved {
			result = "未通过"
		}
		data := map[string]string{
			fields[0]: clipRunes(n.Subject, 20),
			fields[1]: clipRunes(result, 5),
			fields[2]: clipRunes(n.Remark, 20),
		}
		err := sendSubscribe(wxAppOf(db, n.CompanyID, cfg), wechat.SubscribeMsg{
			ToUser:     openid,
			TemplateID: tplID,
			Page:       n.Page,
			Data:       data,
		})
		switch {
		case err == nil:
			// 送达成功，无需日志（高频路径，日志只留异常）
		case wechat.IsSubscribeNotGranted(err):
			// 43101：用户没同意 / 一次性订阅的配额已用完。这是**正常现象**，
			// 不是故障——记 warn 会让人以为出错了，而它每天都会发生很多次。
			log.Printf("[SUBSCRIBE] 未送达（用户未订阅或配额已用尽）company=%d", n.CompanyID)
		default:
			log.Printf("[SUBSCRIBE] 发送失败 company=%d err=%v；%s", n.CompanyID, err, wechat.SubscribeErrHint(err))
		}
	}
}

// rejectRemark 驳回通知的备注。
//
// 管理员填了理由就用理由（那才是用户想知道的东西），没填给一句中性的说明。
// 不追加「请联系管理员」之类的套话：备注是 thing 类型（≤20 字符），
// 套话会把真正有用的理由挤掉——理由一长，用户就只看到「请联系管理员」。
func rejectRemark(reason string) string {
	if r := strings.TrimSpace(reason); r != "" {
		return r
	}
	return "未填写理由，请联系管理员"
}

// RegisterSubscribe 注册订阅消息相关接口。
func RegisterSubscribe(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	// 小程序在调 wx.requestSubscribeMessage 之前要知道模板 ID。
	//
	// 公开接口，理由：模板 ID 不是密钥——用户在微信授权弹窗里本来就能看到模板名称，
	// 而访客在提交开通申请**之前**还没有任何身份，挂鉴权等于这个功能对最需要它的人
	// （还没开通的员工）不可用。响应里只有模板 ID，不含任何凭据。
	//
	// 未配置时回空串：小程序据此跳过授权请求，不弹一个「没有任何模板」的窗
	// （微信对空 tmplIds 会直接 fail，用户看到的是点了没反应）。
	r.GET("/api/subscribe/config", func(c *gin.Context) {
		// companyId 来自小程序端（getCompanyId）：企业独立版用它拿到自己账号下的模板；
		// 平台模式传 0 / 不传则回落全局模板。该接口公开——模板 ID 不是密钥
		// （用户授权弹窗里本就可见），泄露无妨，且访客提交申请前尚无身份不能挂鉴权。
		companyID, _ := strconv.ParseInt(c.Query("companyId"), 10, 64)
		if companyID < 0 {
			companyID = 0
		}
		tplID, _ := auditTemplate(db, companyID)
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{"auditTemplateId": tplID},
		})
	})
}
