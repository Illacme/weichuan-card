package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// rawBody 发起请求并返回原始响应体（CSV 等非 JSON 响应用）。
func rawBody(r *gin.Engine, method, path, token string) (int, []byte) {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// TestEmployeeExportCSV 钉死批量导出：
//   - 返回 text/csv 且带 UTF-8 BOM（Excel 双击不乱码）；
//   - 内容含表头与本租户员工明文（PII 经 AfterFind 解密）；
//   - 高风险动作记审计（employee.export_batch）。
func TestEmployeeExportCSV(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	_, body := plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "导出甲", "phone": "13800000051", "title": "工程师", "email": "a@a.com",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "导出乙", "phone": "13900000052", "title": "销售",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	code, raw := rawBody(r, "GET", "/api/admin/employees/export", adminToken)
	require.Equal(t, http.StatusOK, code)
	require.True(t, bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), "CSV 必须有 UTF-8 BOM")
	s := string(raw)
	require.Contains(t, s, "姓名")
	require.Contains(t, s, "手机")
	require.Contains(t, s, "导出甲")
	require.Contains(t, s, "13800000051")
	require.Contains(t, s, "导出乙")
	require.Contains(t, s, "13900000052")

	// 审计：高风险导出必须留痕
	_, body = plat.do("GET", "/api/admin/audit-logs?action=employee.export_batch", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	logs := body["data"].([]interface{})
	require.GreaterOrEqual(t, len(logs), 1, "应记入一条批量导出审计")
}

// TestEmployeeExportTenantIsolation 钉死导出的租户隔离：
// 企业管理员只能导出自己企业的员工，不能带走别家数据。
func TestEmployeeExportTenantIsolation(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	// 平台超管在朗汇（公司 1）建一名员工
	_, body := plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "朗汇独占", "phone": "13800000061", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 开乙公司 + 企业管理员
	_, body = plat.do("POST", "/api/admin/companies", map[string]interface{}{
		"name": "导出隔离企", "shortName": "导出企", "adminUsername": "expo", "adminPassword": "expopass",
		"seedCategories": false,
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	comp2 := newTestClient(r, t)
	_, body = comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "expo", "password": "expopass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	comp2Token := comp2.cookies["access_token"]
	require.NotEmpty(t, comp2Token)

	// 乙企业管理员的员工落到乙公司
	_, body = comp2.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
		"name": "乙独占", "phone": "13700000062", "title": "销售",
	})
	require.EqualValues(t, 0, body["code"])

	// 乙企业管理员导出：应只含乙公司员工，不含朗汇的
	code, raw := rawBody(r, "GET", "/api/admin/employees/export", comp2Token)
	require.Equal(t, http.StatusOK, code)
	s := string(raw)
	require.Contains(t, s, "13700000062", "应导出本企业的员工")
	require.NotContains(t, s, "13800000061", "不得带走别家企业的员工")
	require.NotContains(t, s, "朗汇独占", "不得带走别家企业的员工")

	// 反向：平台超管（all 范围）缺省导出应含**所有租户**的员工（与列表页同口径）
	code, raw = rawBody(r, "GET", "/api/admin/employees/export", adminToken)
	require.Equal(t, http.StatusOK, code)
	s = string(raw)
	require.Contains(t, s, "13800000061", "平台超管缺省可见本租户员工")
	require.Contains(t, s, "乙独占", "平台超管缺省可见全部租户员工")

	// 平台超管按 ?companyId=2 收窄：只含乙公司员工
	code, raw = rawBody(r, "GET", "/api/admin/employees/export?companyId=2", adminToken)
	require.Equal(t, http.StatusOK, code)
	s = string(raw)
	require.Contains(t, s, "乙独占")
	require.NotContains(t, s, "13800000061", "按 companyId 收窄后不带别家")
	require.NotContains(t, s, "朗汇独占")
}

// TestEmployeeExportFilter 钉死导出与列表页同口径的筛选：
// 带 keyword / status 时，导出的就是当前筛选结果。
func TestEmployeeExportFilter(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	_, body := plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "筛选甲", "phone": "13800000071", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "筛选乙", "phone": "13900000072", "title": "销售",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 按姓名关键字筛选导出
	code, raw := rawBody(r, "GET", "/api/admin/employees/export?keyword=筛选甲", adminToken)
	require.Equal(t, http.StatusOK, code)
	s := string(raw)
	require.Contains(t, s, "13800000071")
	require.NotContains(t, s, "13900000072")

	// 按状态筛选：先禁用一个再导出 disabled
	_, body = plat.do("GET", "/api/admin/employees", nil, adminToken)
	var targetID int64
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["phone"] == "13900000072" {
			targetID = int64(m["staffId"].(float64))
		}
	}
	require.NotZero(t, targetID)
	_, body = plat.do("POST", fmt.Sprintf("/api/admin/employees/%d/disable", targetID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	code, raw = rawBody(r, "GET", "/api/admin/employees/export?status=enabled", adminToken)
	require.Equal(t, http.StatusOK, code)
	s = string(raw)
	require.Contains(t, s, "13800000071")
	require.NotContains(t, s, "13900000072")
}
