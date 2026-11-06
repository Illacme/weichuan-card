package wechat

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWXHTTPClientTimeout 守门：微信外呼必须带超时。
//
// 此前直接调 http.Get/http.Post（http.DefaultClient，Timeout=0），jscode2session /
// 取 token 卡住时会无限期占着 tokenMu，把依赖 token 的所有请求（订阅通知、小程序码、
// 内容安全、手机号验证）一起拖死——微信偶发抖动就能让整站冻结。
//
// 这里让 server 故意长时间不响应，函数应在 wxHTTPTimeout 内返回 err 而非永久挂起；
// 一旦有人把 wxHTTPClient 改回裸 http.Get/Post（Timeout=0），这条用例会从「~10s 返回」
// 退化成「永久阻塞、测试超时失败」，从而挡住退化。
func TestWXHTTPClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意长时间不响应以触发 client 超时。监听 request context：client 超时断开后
		// r.Context() 被取消，handler 立即返回、连接关闭，避免 srv.Close() 干等整段 sleep。
		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()
	restore := SetAPIBase(srv.URL)
	defer restore()

	start := time.Now()
	_, _, err := Code2Session(Config{AppID: "a", AppSecret: "b"}, "code")
	elapsed := time.Since(start)

	require.Error(t, err)
	require.True(t, elapsed < 15*time.Second,
		"无超时会永久阻塞；实际耗时 %s 应远小于 server 的 30s（超时=%s）", elapsed, wxHTTPTimeout)
}
