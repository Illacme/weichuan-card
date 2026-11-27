package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 公开接口缓存可配置 + 立即刷新（第一百一十四轮）。
//
// 守三件事：
//  1. TTL 从 setting 动态读取——默认 60；改成 120 后响应头跟着变；0 = no-store；
//  2. 「立即刷新」接口原子递增缓存版本号，且公开配置下发新版本号；
//  3. 刷新接口需平台超管——未登录进不来。
//
// 这里直接改 setting（内存缓存即时刷新），与生产 PUT /api/admin/settings 的写入
// 路径一致（都经 Store.Set），不重复测那条通用 PUT 通路。

// cacheHeader 发一个 GET，返回 Cache-Control 响应头。
func cacheHeader(t *testing.T, r *gin.Engine, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Header().Get("Cache-Control")
}

func TestPublicCacheConfigurable(t *testing.T) {
	r, db := setupPublicEngine(t)
	// setupPublicEngine 只建引擎，没初始化 setting；这里补上（与生产启动链一致）。
	require.NoError(t, setting.Init(db))

	// 默认 60：被缓存的公开 GET 带 max-age=60 / s-maxage=60。
	require.Contains(t, cacheHeader(t, r, "/api/projects"), "max-age=60")
	require.Contains(t, cacheHeader(t, r, "/api/home-layout"), "s-maxage=60")

	// 改成 120：响应头立即跟着变（无需重启 / 无需重建引擎）。
	require.NoError(t, setting.Upsert(setting.KeyPublicCacheTTL, "120"))
	cc := cacheHeader(t, r, "/api/projects")
	require.Contains(t, cc, "max-age=120")
	require.Contains(t, cc, "s-maxage=120")
	require.NotContains(t, cc, "max-age=60")

	// 0 = 关闭缓存：发 no-store，避免 max-age=0 在部分 CDN 上仍有缓存歧义。
	require.NoError(t, setting.Upsert(setting.KeyPublicCacheTTL, "0"))
	require.Equal(t, "no-store", cacheHeader(t, r, "/api/projects"))

	// 非法值（超上限）被拒，内存值保持 0。
	require.Error(t, setting.Upsert(setting.KeyPublicCacheTTL, "3601"))
	require.Equal(t, "no-store", cacheHeader(t, r, "/api/projects"))

	// 复位默认，避免污染同包其它用例（setting 是进程级全局单例）。
	require.NoError(t, setting.Upsert(setting.KeyPublicCacheTTL, "60"))
}

// publicCacheVersionOf 从 /api/public/settings 信封里读缓存版本号。
// Public() 下发的全是字符串（setting.Public 返回 map[string]string），这里转 int64。
func publicCacheVersionOf(t *testing.T, body map[string]interface{}) int64 {
	t.Helper()
	raw := body["data"].(map[string]interface{})[setting.KeyPublicCacheVersion]
	v, err := strconv.ParseInt(raw.(string), 10, 64)
	require.NoError(t, err)
	return v
}

func TestCacheRefreshBumpsVersion(t *testing.T) {
	plat := setupSQLiteEngine(t)
	client := newTestClient(plat, t)

	// 平台超管登录（cookie 鉴权）。
	_, body := client.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])

	// 公开配置里的版本号起点（seed 默认 1）。
	_, body = client.do("GET", "/api/public/settings", nil, "")
	require.EqualValues(t, 0, body["code"])
	before := publicCacheVersionOf(t, body)
	require.EqualValues(t, 1, before)

	// 点一次「立即刷新缓存」：版本号 +1，回包带回新值。
	_, body = client.doWithCookie("POST", "/api/admin/cache/refresh", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, before+1, body["data"].(map[string]interface{})["cacheVersion"])

	// 再点一次：继续递增（不是原地踏步）。
	_, body = client.doWithCookie("POST", "/api/admin/cache/refresh", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, before+2, body["data"].(map[string]interface{})["cacheVersion"])

	// 公开配置立刻反映新版本号（Bump 同步了内存缓存，无需重启）。
	_, body = client.do("GET", "/api/public/settings", nil, "")
	require.EqualValues(t, before+2, publicCacheVersionOf(t, body))

	// 复位，避免污染同包其它用例对这个全局值的预期。
	require.NoError(t, setting.Upsert(setting.KeyPublicCacheVersion, "1"))
}

func TestCacheRefreshRequiresPlatformAdmin(t *testing.T) {
	r := setupSQLiteEngine(t)

	// 未登录：POST /api/admin/cache/refresh 被中间件挡下（HTTP 200 + code 401/403）。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/cache/refresh", nil))
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotEqual(t, float64(0), body["code"], "未登录不应能刷新缓存")

	// 版本号没有被改动（刷新是特权操作，失败也要留白）。
	require.EqualValues(t, 1, setting.GetInt(setting.KeyPublicCacheVersion, 1))
}
