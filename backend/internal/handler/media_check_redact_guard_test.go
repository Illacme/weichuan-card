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
	require.Regexp(t, `err=%s",\s*redactURLForLog\(d\.URL\),\s*redactWXSecretText\(err\.Error\(\)\)\)`, s,
		"服务端日志的错误要打 token；URL 也要剥 query（私有桶签名在 query 里）")
	require.Regexp(t, `submitted trace=%s url=%s",\s*trace,\s*redactURLForLog\(d\.URL\)`, s,
		"成功日志同样不能把签名 URL 原样打出来")
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
			// P0：公开 /api/wechat/openid 的失败路径就是这条 URL
			name: "code2session 的错误同时带 secret 与 js_code",
			in:   `Get "https://api.weixin.qq.com/sns/jscode2session?appid=wx1&secret=APPSECRET-123&js_code=CODE-456&grant_type=authorization_code": dial tcp: i/o timeout`,
			want: `Get "https://api.weixin.qq.com/sns/jscode2session?appid=wx1&secret=***&js_code=***&grant_type=authorization_code": dial tcp: i/o timeout`,
		},
		{
			// P0：GetAccessToken 失败时会走到这里，且 /api/wechat/wxacode 会把它回显给访客
			name: "获取 access_token 的 URL 里 secret 也要打掉",
			in:   `Get "https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=wx1&secret=TOPSECRET": EOF`,
			want: `Get "https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=wx1&secret=***": EOF`,
		},
		{
			name: "secret 大小写不敏感",
			in:   `SECRET=AbCdE`,
			want: `SECRET=***`,
		},
		{
			name: "JSON 形态的 secret 也打码",
			in:   `body={"app_secret":"tk-json"} err=x`,
			want: `body={"app_secret":"***"} err=x`,
		},
		{
			// 量尺自检：appid 不是凭据（公开值），打掉它只会妨碍排障
			name: "appid 保留不误伤",
			in:   `appid=wx123&secret=s1&js_code=c1`,
			want: `appid=wx123&secret=***&js_code=***`,
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

func TestRedactURLForLog(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"签名 URL 去掉整段 query", "https://bucket.oss.cn/t/5/a.png?X-Amz-Signature=deadbeef&X-Amz-Credential=AK", "https://bucket.oss.cn/t/5/a.png"},
		{"微信 access_token 也一并去掉", "https://api.weixin.qq.com/x?access_token=tk123&scene=2", "https://api.weixin.qq.com/x"},
		{"裸对象键的签名参数也要截掉", "t/5/a.png?X-Amz-Signature=abc", "t/5/a.png"},
		{"fragment 同样去掉", "https://x.com/a.png#sig=xyz", "https://x.com/a.png"},
		{"普通 URL 原样保留", "https://x.com/a.png", "https://x.com/a.png"},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, redactURLForLog(tc.in)) })
	}
}
