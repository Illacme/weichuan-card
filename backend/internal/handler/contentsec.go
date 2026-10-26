package handler

import (
	"log"

	"weichuan/internal/config"
	"weichuan/internal/contentsec"
	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"gorm.io/gorm"
)

// 内容安全接入点。
//
// 只覆盖**用户生成内容**：员工自己提交的名片修改、访客提交的开通申请。
// 后台超管配置的内容（企业信息、业绩项目、业务服务）不在此列——那是运营方
// 自己发布的内容，责任在运营方，逐条过机器检测既无必要也会拖慢保存。
//
// 检测结论的处置原则见 contentsec.CheckText：只有「命中违规」才拦，
// 其余（未配凭据 / 接口失败 / 建议复核 / 无 openid）一律放行并记日志。
// 这里的日志不是可选项：内容安全静默失效时，只有它能证明「当时没检测」。

// contentSecText 对一组 UGC 文本做内容安全检测，返回最终决策。
// 调用方只需看 Allowed；Reason 可直接回给用户。
func contentSecText(db *gorm.DB, cfg config.Config, companyID int64, openid string, texts ...string) contentsec.Decision {
	d := contentsec.CheckText(contentsec.Params{
		// 缺省开：UGC 内容安全是小程序提审的硬性要求，不该让运维「想起来才开」。
		Enabled: setting.GetBool("content_sec_enabled", true),
		// 凭据按租户解析：企业独立版下 openid 属于该租户自己的小程序，
		// 拿平台全局凭据去查会得到「openid 不合法」。
		Cred:   wxAppOf(db, companyID, cfg),
		OpenID: openid,
		Scene:  wechat.SceneProfile,
	}, texts...)

	switch {
	case !d.Allowed:
		log.Printf("[CONTENT-SEC] blocked company=%d label=%d trace=%s openid=%s",
			companyID, d.Verdict.Label, d.Verdict.TraceID, maskOpenID(openid))
	case d.Note == "pending_review":
		log.Printf("[CONTENT-SEC] pending_review company=%d label=%d trace=%s openid=%s",
			companyID, d.Verdict.Label, d.Verdict.TraceID, maskOpenID(openid))
	case d.Skipped && d.Note != "no_content":
		log.Printf("[CONTENT-SEC] skipped (%s) company=%d openid=%s", d.Note, companyID, maskOpenID(openid))
	}
	return d
}

// maskOpenID 日志里不给完整 openid：它是用户在本小程序内的唯一标识，
// 原样落日志等于把「可关联到具体人」的标识又扩散了一份出去。
func maskOpenID(s string) string {
	if s == "" {
		return "-"
	}
	if len(s) <= 10 {
		return "***"
	}
	return s[:6] + "***" + s[len(s)-4:]
}
