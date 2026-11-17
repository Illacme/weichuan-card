package handler

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// 凭据去留的静态守门：只盯「备注会出参 / 错误会写日志」这两条路径。
//
// 量尺自检放在最前面：如果哪天代码换了写法导致正则一条也抓不到，
// 后面那些 NotRegexp 会「全部通过」——那是守门最危险的坏法。
func TestMediaCheckNoteSourcesAreSanitized(t *testing.T) {
	src, err := os.ReadFile("media_check.go")
	require.NoError(t, err)
	s := string(src)

	require.Regexp(t, `row\.Note\s*=\s*"send_failed:`, s,
		"量尺自检：必须抓到送检失败的落库点")
	require.NotRegexp(t, `row\.Note\s*=\s*"send_failed:"\s*\+`, s,
		"送检失败的备注禁止拼接任何动态错误（err.Error 里会带 access_token）")
	require.Regexp(t, `updates\["note"\]\s*=\s*redactWXSecretText\(`, s,
		"回调写回的备注必须经过凭据打码")
	require.Regexp(t, `err=%s",\s*d\.URL,\s*redactWXSecretText\(err\.Error\(\)\)\)`, s,
		"服务端日志允许留错误，但必须先打掉 access_token")
}

func TestRedactWXSecretText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "网络错里带完整请求 URL",
			in:   `Post "https://api.weixin.qq.com/cgi-bin/wxa/media_check_async?access_token=tk-123": dial tcp: i/o timeout`,
			want: `Post "https://api.weixin.qq.com/cgi-bin/wxa/media_check_async?access_token=***": dial tcp: i/o timeout`,
		},
		{
			name: "URL 参数后面还有别的 query",
			in:   `GET /x?access_token=abc123&scene=2 failed`,
			want: `GET /x?access_token=***&scene=2 failed`,
		},
		{
			name: "大小写不敏感",
			in:   `ACCESS_TOKEN=AbCdE`,
			want: `ACCESS_TOKEN=***`,
		},
		{
			name: "JSON 形态也打码",
			in:   `body={"access_token":"tk-json"} err=x`,
			want: `body={"access_token":"***"} err=x`,
		},
		{
			name: "同一串里多处都打码",
			in:   `a?access_token=one&b=1 body={"access_token":"two"}`,
			want: `a?access_token=***&b=1 body={"access_token":"***"}`,
		},
		{
			name: "普通错误原样保留",
			in:   `微信未返回 trace_id，无法追踪检测结论`,
			want: `微信未返回 trace_id，无法追踪检测结论`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, redactWXSecretText(tc.in))
		})
	}
}
