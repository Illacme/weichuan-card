package handler

import (
	"regexp"
)

// 微信 SDK / net/http 的错误文本里常常带着完整请求 URL：
//
//	Post "https://api.weixin.qq.com/cgi-bin/token?appid=wx1&secret=SECRET": dial tcp ...
//
// 这类错误一旦原样写进数据库或接口出参，就等于把 AppSecret / access_token
// 复制了一份出去。打码必须覆盖**所有**拼进 URL 的凭据参数：
//   - access_token：小程序码 / 内容安全等接口
//   - secret：code2session 与获取 access_token 两条链路（本轮补上）
//   - js_code：wx.login 换来的一次性凭证（本轮补上）
//
// 只做「文本打码」，不改变错误语义，也不打业务日志之外的任何输出。
var (
	wxURLTokenPattern   = regexp.MustCompile(`(?i)(access_token=)[^&\s"']+`)
	wxURLSecretPattern  = regexp.MustCompile(`(?i)(secret=)[^&\s"']+`)
	wxURLCodePattern    = regexp.MustCompile(`(?i)(js_code=)[^&\s"']+`)
	wxJSONTokenPattern  = regexp.MustCompile(`(?i)("access_token"\s*:\s*")[^"]*(")`)
	wxJSONSecretPattern = regexp.MustCompile(`(?i)("(?:app_)?secret"\s*:\s*")[^"]*(")`)
)

// redactWXSecretText 把错误文本里的微信访问凭据打码。
//
// 注意它只用于「人看的日志 / 备注 / 面向用户的错误消息」，绝不用于重新拼请求——
// 打码后的串不能调用微信接口。
func redactWXSecretText(s string) string {
	if s == "" {
		return ""
	}
	out := wxURLTokenPattern.ReplaceAllString(s, "${1}***")
	out = wxURLSecretPattern.ReplaceAllString(out, "${1}***")
	out = wxURLCodePattern.ReplaceAllString(out, "${1}***")
	out = wxJSONTokenPattern.ReplaceAllString(out, "${1}***${2}")
	return wxJSONSecretPattern.ReplaceAllString(out, "${1}***${2}")
}
