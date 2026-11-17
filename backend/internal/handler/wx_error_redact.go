package handler

import (
	"regexp"
)

// 微信 SDK / net/http 的错误文本里常常带着完整请求 URL：
//
//	Post "https://api.weixin.qq.com/cgi-bin/xxx?access_token=SECRET": dial tcp ...
//
// 这类错误一旦原样写进数据库或接口出参，就等于把 access_token 复制了一份出去。
// 这里只做「文本打码」，不改变错误语义，也不打业务日志之外的任何输出。
var (
	wxURLTokenPattern  = regexp.MustCompile(`(?i)(access_token=)[^&\s"']+`)
	wxJSONTokenPattern = regexp.MustCompile(`(?i)("access_token"\s*:\s*")[^"]*(")`)
)

// redactWXSecretText 把错误文本里的微信访问凭据打码。
//
// 注意它只用于「人看的日志 / 备注」，绝不用于重新拼请求——打码后的串不能调用微信接口。
func redactWXSecretText(s string) string {
	if s == "" {
		return ""
	}
	out := wxURLTokenPattern.ReplaceAllString(s, "${1}***")
	return wxJSONTokenPattern.ReplaceAllString(out, "${1}***${2}")
}
