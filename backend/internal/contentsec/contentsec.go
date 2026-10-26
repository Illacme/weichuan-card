// Package contentsec 是内容安全的「策略层」：决定要不要检测、检测失败怎么办、
// 命中之后怎么回。
//
// 与 wechat.MsgSecCheck（只负责调接口）分开，是因为策略分支比接口调用更容易出错，
// 而它们都不该真打外网才能验证：「未配凭据」「接口超时」「微信建议复核」「内容超长」
// 这几条在本地必须跑得起来。
package contentsec

import (
	"strings"

	"weichuan/internal/wechat"
)

// maxContentRunes 微信 v2 单次检测的内容上限（2500 字）。
//
// 按 rune 截断而不是按字节：中文一个字三字节，按字节切会把最后一个字切成半个
// UTF-8 序列，微信直接回参数错误——而这条错误又会走 fail-open 放行，等于
// 「内容越长越不检测」。
const maxContentRunes = 2500

// checkText 供测试替换（生产就是 wechat.MsgSecCheck）。
// 与 wechat 包里 apiBase 抽成变量的理由相同：不注入就只能「不测」或「打真接口」。
var checkText = wechat.MsgSecCheck

// Params 一次检测的参数。
type Params struct {
	Enabled bool          // 总开关（系统设置 content_sec_enabled）
	Cred    wechat.Config // 小程序凭据（租户 → 全局 → 环境变量，由调用方解析）
	OpenID  string        // 用户 openid（v2 必填）
	Scene   int           // 场景，缺省按「资料」
}

// Decision 一次检测的最终决策。
type Decision struct {
	Allowed bool              // 是否放行——调用方照它走即可，不必再看 Skipped
	Reason  string            // 拒绝原因（可直接回给用户）
	Skipped bool              // 未实际检测（无内容 / 开关关 / 无凭据 / 无 openid / 接口失败）
	Note    string            // 跳过或复核的原因（写日志用，不回给用户）
	Verdict wechat.SecVerdict // 微信原始结论（Skipped 时为零值）
}

// CheckText 检测若干段文本（拼接后一次提交）。
//
// 失败一律放行（fail-open）。理由：微信接口抖动、凭据没配、限流打满都会让
// 「提交开通申请」这种低频但关键的操作变成 100% 失败，而内容安全是增强项、
// 不是业务前提。代价是漏检，所以每次跳过都带 Note，调用方应记 WARN 并纳入巡检。
//
// 唯一的例外是**命中违规**：那是确定的结论，必须拦。
func CheckText(p Params, texts ...string) Decision {
	content := joinTexts(texts...)
	if content == "" {
		return Decision{Allowed: true, Skipped: true, Note: "no_content"}
	}
	if !p.Enabled {
		return Decision{Allowed: true, Skipped: true, Note: "disabled"}
	}
	if p.Cred.AppID == "" || p.Cred.AppSecret == "" {
		return Decision{Allowed: true, Skipped: true, Note: "no_credential"}
	}
	if p.OpenID == "" {
		// 匿名提交（访客还没登录）拿不到 openid，v2 接口用不了。
		// 这类提交靠后台人工审核兜底，不在这里拦。
		return Decision{Allowed: true, Skipped: true, Note: "no_openid"}
	}
	v, err := checkText(p.Cred, p.OpenID, content, p.Scene)
	if err != nil {
		return Decision{Allowed: true, Skipped: true, Note: "api_error: " + err.Error()}
	}
	switch {
	case v.Risky():
		return Decision{Allowed: false, Reason: "内容含违规信息，请修改后重新提交", Verdict: v}
	case v.Review():
		// 微信建议人工复核：放行，但让调用方记一笔，便于事后抽查。
		return Decision{Allowed: true, Note: "pending_review", Verdict: v}
	}
	return Decision{Allowed: true, Verdict: v}
}

// joinTexts 拼接非空文本（各段去首尾空白，换行分隔），再按 rune 截断到上限。
//
// 分段拼接而不是只测最长那个字段：违规词可能被拆在两个字段里（如标题写前半句、
// 职位写后半句），单字段检测会双双放行。
func joinTexts(texts ...string) string {
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t)
	}
	r := []rune(b.String())
	if len(r) > maxContentRunes {
		r = r[:maxContentRunes]
	}
	return string(r)
}
