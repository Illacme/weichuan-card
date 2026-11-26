package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHomeLayoutVersionProbe 钉死版本探针的两条关键契约：
//
//  1. 显式 no-store——版本号若像 /api/home-layout 那样被 CDN 缓存 60 秒，
//     端上读到的永远是旧号，自动刷新就永远不触发。这个 bug 不会报错、
//     只是「后台保存后端上还是没变」，极难排查，必须用测试钉死。
//  2. 缺省落朗汇、新租户（无配置行）也回 1——与模型 / 迁移默认值一致，
//     端上首次记录后不会因升级本身误触发一次刷新。
//
// 版本号「保存后 +1」的行为在 runAPIFlow（SQLite + MySQL parity）里覆盖，
// 那里有真实的 admin PUT 写库链路，比这里的探针测试更接近生产。
func TestHomeLayoutVersionProbe(t *testing.T) {
	r := setupSQLiteEngine(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/home-layout/version", nil))

	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"版本探针必须 no-store，否则 CDN 缓存会让端上永远读不到新版本")
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["configVersion"], "新租户（无配置行）版本应为缺省 1")
}
