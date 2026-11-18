package handler

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 两个公开端点（无鉴权）曾把微信 SDK 的错误原文回显给调用者：
// net/url.Error.Error() 会把完整 URL 拼进文本，而 URL 里带着 AppSecret / js_code /
// access_token。这里用「让 SDK 真去拼 URL、真发生网络错误」的端到端方式钉死打码，
// 而不是只测打码函数本身——只测函数无法证明调用点接上了。
func TestPublicWechatEndpointsRedactCredentials(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	const secret = "SENSITIVE-WX-SECRET-123"
	const jscode = "JS-CODE-456"

	for _, key := range []string{"wx_appid", "wx_appsecret", "wxacode_rate_limit_per_min"} {
		old := setting.GetString(key, "")
		defer func(k, v string) { _ = setting.Upsert(k, v) }(key, old)
	}
	require.NoError(t, setting.Upsert("wx_appid", "wx-test-appid"))
	require.NoError(t, setting.Upsert("wx_appsecret", secret))
	// 放宽到 30，避免本用例的两次请求被限流挡住而走不到错误回显分支
	require.NoError(t, setting.Upsert("wxacode_rate_limit_per_min", "30"))

	// 把微信基址指向一个必然连接拒绝的地址：SDK 会真的拼出带 secret 的 URL，
	// 再返回 *url.Error。这样断言的是「真实故障窗口下用户看到什么」。
	defer wechat.SetAPIBase("http://127.0.0.1:1")()

	// 防止其它用例的注入残留（包级变量跨用例共享）
	origC2S, origWX := code2Session, getWXACode
	code2Session, getWXACode = wechat.Code2Session, wechat.GetWXACode
	defer func() { code2Session, getWXACode = origC2S, origWX }()

	// ① /api/wechat/openid：公开、无限流豁免，是最容易被扫号打爆的一个
	_, body := c.do("POST", "/api/wechat/openid", map[string]interface{}{
		"code": jscode, "companyId": 1,
	}, "")
	msg, _ := body["msg"].(string)
	require.EqualValues(t, 1, body["code"], "响应：%v", body)
	require.NotContains(t, msg, secret, "AppSecret 绝不能出现在公开端点出参")
	require.NotContains(t, msg, jscode, "js_code 绝不能出现在公开端点出参")
	require.Contains(t, msg, "secret=***", "错误文本应保留可排障的 URL 骨架，只打掉凭据")
	require.Contains(t, msg, "js_code=***")

	// ② /api/wechat/wxacode：访客高频调用，命中缓存前会真调微信取 access_token
	const staffID int64 = 880004
	wxacodeDrop(staffID)
	defer wxacodeDrop(staffID)
	_, body = c.do("GET", "/api/wechat/wxacode?staffId=880004", nil, "")
	msg, _ = body["msg"].(string)
	require.EqualValues(t, 1, body["code"], "响应：%v", body)
	require.NotContains(t, msg, secret)
	require.NotContains(t, msg, "SENSITIVE")
}

// openid 端点此前完全没有闸门：companyId 客户端自填可枚举，脚本拿随机 code
// 就能耗尽微信 jscode2session 配额，之后所有正常用户一起登不上。
func TestWechatOpenIDRateLimitedPerIP(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	for _, key := range []string{"wx_appid", "wx_appsecret", "wxacode_rate_limit_per_min"} {
		old := setting.GetString(key, "")
		defer func(k, v string) { _ = setting.Upsert(k, v) }(key, old)
	}
	require.NoError(t, setting.Upsert("wx_appid", "test-appid"))
	require.NoError(t, setting.Upsert("wx_appsecret", "test-secret"))
	require.NoError(t, setting.Upsert("wxacode_rate_limit_per_min", "2"))

	orig := code2Session
	calls := 0
	code2Session = func(_ wechat.Config, _ string) (string, string, error) {
		calls++
		return "openid-fake", "", nil
	}
	defer func() { code2Session = orig }()

	call := func() map[string]interface{} {
		_, body := c.do("POST", "/api/wechat/openid", map[string]interface{}{
			"code": "any-code", "companyId": 1,
		}, "")
		return body
	}

	require.EqualValues(t, 0, call()["code"])
	require.EqualValues(t, 0, call()["code"])
	require.Equal(t, 2, calls)

	// 第三次（换 companyId 也一样，分桶按 IP）必须被挡下，且不再打微信接口
	_, third := c.do("POST", "/api/wechat/openid", map[string]interface{}{
		"code": "any-code", "companyId": 2,
	}, "")
	require.EqualValues(t, 429, third["code"], "响应：%v", third)
	require.Equal(t, 2, calls, "超限请求不应再触发微信调用")
}

// 源码级守门：所有公开端点的错误回显都必须经过打码出口。
// 行为测试只覆盖「当前能构造出来的两种故障」，未来新增的 err.Error() 回显
// 不会被它们发现，所以再钉一层静态检查。
func TestPublicWechatErrorEchoesAreRedacted(t *testing.T) {
	src, err := os.ReadFile("wechat.go")
	require.NoError(t, err)

	// 只取真正执行的代码行：注释里解释「不要回显原文」不算违规。
	var lines []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		lines = append(lines, line)
	}
	code := strings.Join(lines, "\n")

	echo := regexp.MustCompile(`"msg":\s*([^}\n]+)`)
	matches := echo.FindAllStringSubmatch(code, -1)
	require.GreaterOrEqual(t, len(matches), 6,
		"量尺自检：必须抓到公开端点的全部消息回显点，否则下面的断言会空转")

	errorEchoes := 0
	for _, m := range matches {
		expr := m[1]
		if !strings.Contains(expr, "Error()") {
			continue
		}
		errorEchoes++
		require.Contains(t, expr, "redactWXSecretText(",
			"错误原文禁止直接回显：%s", expr)
	}
	require.GreaterOrEqual(t, errorEchoes, 6,
		"量尺自检：必须抓到所有动态错误回显，注释或改名都不能让它归零")

	// 限流必须挂在路由上，只定义 limiter 不接线等于没做
	require.Regexp(t, `r\.POST\("/api/wechat/openid",\s*openidLimiter\.Handler\(\)`, code)
	require.Regexp(t, `r\.GET\("/api/wechat/wxacode",\s*wxacodeLimiter\.Handler\(\)`, code)
}
