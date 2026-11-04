package wechat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// 本包每个导出函数都是一次微信外网调用。测试把 apiBase 指向 httptest，
// 于是「成功 / errcode / access_token 失效重试」这些支线都能在本地跑一遍，
// 不必依赖真接口（也不必掏真凭据）。

// fakeWX 起一个假微信接口，按路径分发；hits 统计各路径被请求了几次。
type fakeWX struct {
	srv  *httptest.Server
	old  string
	mu   chan struct{} // 串行化（httptest 会并发回调）
	path map[string]int
}

func newFakeWX(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeWX {
	t.Helper()
	f := &fakeWX{mu: make(chan struct{}, 1), path: map[string]int{}, old: apiBase}
	wrapped := func(w http.ResponseWriter, r *http.Request) {
		f.mu <- struct{}{}
		f.path[r.URL.Path]++
		<-f.mu
		handler(w, r)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(wrapped))
	apiBase = f.srv.URL
	t.Cleanup(func() {
		apiBase = f.old
		f.srv.Close()
		resetTokenCache()
	})
	return f
}

func (f *fakeWX) hits(path string) int {
	f.mu <- struct{}{}
	n := f.path[path]
	<-f.mu
	return n
}

// resetTokenCache 清空 access_token 进程级缓存：它是包级全局变量，
// 不清会让上一个用例签出的 token 泄漏到下一个用例（表现为「该发的请求没发」）。
// 按 appid 分键后，逐个删比整体清更贴近真实——也顺带验证「键真的是 appid」。
func resetTokenCache() {
	tokenMu.Lock()
	for k := range tokenCache {
		delete(tokenCache, k)
	}
	tokenMu.Unlock()
}

var testCfg = Config{AppID: "wxappid0123456789", AppSecret: "0123456789abcdef0123456789abcdef"}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

// ---- jscode2session ----

func TestCode2SessionSuccess(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/sns/jscode2session", r.URL.Path)
		// 凭据必须真的带在请求里（漏传是最常见的接线错误）
		require.Equal(t, testCfg.AppID, r.URL.Query().Get("appid"))
		require.Equal(t, testCfg.AppSecret, r.URL.Query().Get("secret"))
		require.Equal(t, "the-code", r.URL.Query().Get("js_code"))
		writeJSON(t, w, map[string]any{"openid": "o-openid", "session_key": "sk"})
	})

	openid, sk, err := Code2Session(testCfg, "the-code")
	require.NoError(t, err)
	require.Equal(t, "o-openid", openid)
	require.Equal(t, "sk", sk)
	require.Equal(t, 1, f.hits("/sns/jscode2session"))
}

// 未配置凭据时必须在本地报错，而不是带着空 appid 打一次外网（会被微信当作无效请求，
// 排查时只会看到「微信返回错误」，看不出其实是自己没配）。
func TestCode2SessionRejectsUnconfigured(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("不应发起请求") })

	_, _, err := Code2Session(Config{}, "code")
	require.Error(t, err)
	require.Contains(t, err.Error(), "WX_APPID")

	_, _, err = Code2Session(testCfg, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "js_code")
}

func TestCode2SessionErrCode(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})
	})
	_, _, err := Code2Session(testCfg, "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "40029")
	require.Contains(t, err.Error(), "invalid code")
}

// ---- access_token ----

func TestGetAccessTokenCaches(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
	})

	tk, err := GetAccessToken(testCfg)
	require.NoError(t, err)
	require.Equal(t, "tk-1", tk)

	// 缓存命中：不再打接口（微信 access_token 有日调用限额，重复换取会拖垮配额）
	tk2, err := GetAccessToken(testCfg)
	require.NoError(t, err)
	require.Equal(t, "tk-1", tk2)
	require.Equal(t, 1, f.hits("/cgi-bin/token"))
}

// 缓存过期要重新换取，而不是一直用旧 token 直到 401。
func TestGetAccessTokenRefreshesWhenExpired(t *testing.T) {
	n := 0
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		// expires_in=0 → 缓存有效期算出来是「5 分钟前」，下次调用必然刷新
		writeJSON(t, w, map[string]any{"access_token": "tk-" + string(rune('0'+n)), "expires_in": 0})
	})

	tk1, err := GetAccessToken(testCfg)
	require.NoError(t, err)
	tk2, err := GetAccessToken(testCfg)
	require.NoError(t, err)
	require.NotEqual(t, tk1, tk2, "过期的 token 不应继续复用")
	require.Equal(t, 2, n)
}

func TestGetAccessTokenErrCode(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 40125, "errmsg": "invalid appsecret"})
	})
	_, err := GetAccessToken(testCfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "40125")
}

// ---- 小程序码 ----

var jpegBytes = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

func TestGetWXACodeUnlimitSuccess(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			require.Equal(t, "tk-1", r.URL.Query().Get("access_token"))
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	b, err := GetWXACodeUnlimit(testCfg, "staff=42", "pages/index/index")
	require.NoError(t, err)
	require.Equal(t, jpegBytes, b)
	require.Equal(t, 1, f.hits("/wxa/getwxacodeunlimit"))
}

// 请求体里的 scene **必须原样**发出去：`json.Marshal` 会把 `&` 转成 `\u0026`，
// 而微信的 scene 校验按原始字符串做字符集检查 → 40129 invalid scene。
//
// 这条坑的迷惑性在于只影响海报码（scene = staffId=N&from=poster，含 &），
// 名片普通码（scene = staffId=N）一直正常，所以看日志会以为是「微信抽风」。
// 断言直接看**原始字节**，而不是解成 map 再比——那样转义会被还原，等于没测。
func TestGetWXACodeUnlimitSendsSceneUnescaped(t *testing.T) {
	var raw string
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			raw = string(b)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	_, err := GetWXACodeUnlimit(testCfg, "staffId=3&from=poster", "pages/index/index")
	require.NoError(t, err)
	require.Equal(t, 1, f.hits("/wxa/getwxacodeunlimit"))
	require.Contains(t, raw, `"scene":"staffId=3&from=poster"`)
	require.NotContains(t, raw, `\u0026`, "scene 里的 & 不能被 HTML 转义（微信会判 40129）")
}

func TestCheckScene(t *testing.T) {
	// 合法：普通名片码 / 海报码 / 满 32 字符
	require.NoError(t, checkScene("staffId=3"))
	require.NoError(t, checkScene("staffId=3&from=poster"))
	require.NoError(t, checkScene("abcdefghijklmnopqrstuvwxyz123456"))

	// 超长（33 字符）
	require.ErrorContains(t, checkScene("abcdefghijklmnopqrstuvwxyz1234567"), "超长")
	// 中文 / 空格 / % / 引号都不在官方白名单里
	require.ErrorContains(t, checkScene("员工=3"), "非法字符")
	require.ErrorContains(t, checkScene("staff id=3"), "非法字符")
	require.ErrorContains(t, checkScene("staffId%3D3"), "非法字符")
	require.ErrorContains(t, checkScene(`staffId="3"`), "非法字符")
	require.ErrorContains(t, checkScene(""), "不能为空")
}

// 非法 scene 在本地就被拦下，不浪费一次微信调用（也不让 operator 只看到
// 「invalid scene rid: xxx」这种查不到东西的文案）。
func TestGetWXACodeUnlimitRejectsBadSceneBeforeCallingWX(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
			return
		}
		t.Fatalf("非法 scene 不应该发起微信请求，却打到了 %s", r.URL.Path)
	})

	_, err := GetWXACodeUnlimit(testCfg, "员工=3", "pages/index/index")
	require.ErrorContains(t, err, "非法字符")
	require.Equal(t, 0, f.hits("/wxa/getwxacodeunlimit"))
}

// 环境版本与 check_path 必须真的进请求体：微信按 env_version 出码，
// 且 check_path 的语义是「page 是否存在于该版本」。正式版未发布时
// release + check_path=true 必然 41030（验收期出不了码的根因）。
func TestGetWXACodeSendsEnvVersionAndCheckPath(t *testing.T) {
	var raw string
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			raw = string(b)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	_, err := GetWXACode(testCfg, WXACodeParams{
		Scene: "staffId=1", Page: "pages/index/index",
		EnvVersion: "trial", CheckPath: false,
	})
	require.NoError(t, err)
	require.Contains(t, raw, `"env_version":"trial"`)
	require.Contains(t, raw, `"check_path":false`)
	require.Equal(t, 1, f.hits("/wxa/getwxacodeunlimit"))

	// 非法 / 空环境版本一律回 release（配错的表现是「扫出来打不开」，比报错更难查）
	resetTokenCache()
	_, err = GetWXACode(testCfg, WXACodeParams{Scene: "staffId=1", Page: "pages/index/index", EnvVersion: "prod"})
	require.NoError(t, err)
	require.Contains(t, raw, `"env_version":"release"`)
}

// 41030 的文案必须点明「page 在该环境版本里不存在」：微信原文只有 invalid page，
// 运维照着去改 page 路径会越改越远（本项目 page 一直是 pages/index/index，没写错）。
func TestGetWXACodeHintsOnInvalidPage(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		default:
			writeJSON(t, w, map[string]any{"errcode": 41030, "errmsg": "invalid page"})
		}
	})

	_, err := GetWXACode(testCfg, WXACodeParams{
		Scene: "staffId=1", Page: "pages/index/index", EnvVersion: "release", CheckPath: true,
	})
	require.ErrorContains(t, err, "41030")
	require.ErrorContains(t, err, "正式版")
	require.ErrorContains(t, err, "体验版")
}

// access_token 失效（40001/42001）必须刷新后重试一次：这是生产上最高频的失败
// ——token 在别处被刷新（多实例 / 后台手工调用）后本地缓存就成了废票。
func TestGetWXACodeUnlimitRetriesOnExpiredToken(t *testing.T) {
	var tokenCalls, codeCalls int32
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			n := atomic.AddInt32(&tokenCalls, 1)
			writeJSON(t, w, map[string]any{"access_token": "tk-" + string(rune('0'+n)), "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			// 第一次用旧 token 被拒，第二次（刷新后）成功
			if atomic.AddInt32(&codeCalls, 1) == 1 {
				writeJSON(t, w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})
				return
			}
			require.Equal(t, "tk-2", r.URL.Query().Get("access_token"), "重试必须带上刷新后的 token")
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes)
		}
	})

	b, err := GetWXACodeUnlimit(testCfg, "staff=42", "pages/index/index")
	require.NoError(t, err)
	require.Equal(t, jpegBytes, b)
	require.EqualValues(t, 2, f.hits("/wxa/getwxacodeunlimit"), "应重试一次")
	require.EqualValues(t, 2, f.hits("/cgi-bin/token"), "重试前应先刷新 token")
}

// 只重试一次：重试也失败就报错，不能无限循环打微信接口。
func TestGetWXACodeUnlimitGivesUpAfterOneRetry(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			writeJSON(t, w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})
		}
	})

	_, err := GetWXACodeUnlimit(testCfg, "staff=42", "pages/index/index")
	require.Error(t, err)
	require.Contains(t, err.Error(), "40001")
	require.Equal(t, 2, f.hits("/wxa/getwxacodeunlimit"), "最多重试一次")
}

// 其它 errcode（如 41030 页面不存在）不该触发 token 刷新重试——刷了也没用，
// 白白多打两次接口，还会把真正的原因（页面路径错）掩盖在重试里。
func TestGetWXACodeUnlimitNoRetryOnOtherErrCode(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-1", "expires_in": 7200})
		case "/wxa/getwxacodeunlimit":
			writeJSON(t, w, map[string]any{"errcode": 41030, "errmsg": "invalid page"})
		}
	})

	_, err := GetWXACodeUnlimit(testCfg, "staff=42", "pages/nope/nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "41030")
	require.Equal(t, 1, f.hits("/wxa/getwxacodeunlimit"))
	require.Equal(t, 1, f.hits("/cgi-bin/token"), "非 token 类错误不应刷新 token")
}

// 凭据缺失时不要打外网（与 jscode2session 同一条规矩）
func TestGetWXACodeUnlimitRejectsUnconfigured(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := GetWXACodeUnlimit(Config{}, "s", "p")
	require.Error(t, err)
	require.Equal(t, 0, f.hits("/cgi-bin/token"))
}

func TestIsImage(t *testing.T) {
	require.True(t, isImage(jpegBytes))
	require.True(t, isImage([]byte("\x89PNG\r\n\x1a\nrest")))
	require.False(t, isImage([]byte(`{"errcode":40001}`))) // 微信失败时返回 JSON
	require.False(t, isImage(nil))
	require.False(t, isImage([]byte{0xff, 0xd8})) // 太短
}

// 微信偶发返回带空格/换行的 JSON，别把它当成图片
func TestIsImageRejectsNonImagePayload(t *testing.T) {
	require.False(t, isImage([]byte(strings.Repeat("x", 32))))
}
