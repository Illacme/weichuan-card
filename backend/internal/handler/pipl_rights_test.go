package handler

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestEmployeeCancelDeletesSelf 员工本人注销：删除本人员工记录及关联 PII，且只删自己。
//
// 这是 PIPL 删除权在「数据主体自己操作」场景下的落地——此前员工没有任何自助删除入口，
// 全靠人工找 DBA 执行。验证点：
//  1. 注销后本人 /api/employee/me 立即查不到（账号没了）；
//  2. 同租户的另一个员工不受影响（不能「注销一个连累别人」）；
//  3. 关联的访客统计（StatEvent）一并清掉，不留半成品；
//  4. 操作记审计（ActionEmployeeCancel）。
func TestEmployeeCancelDeletesSelf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 显式造两个同租户员工（已知 staffId，避免反查歧义）
	emp1ID := makeEmployee(t, db, "13900000111", "注销测试甲")
	emp1Token := loginAsEmployee(t, c, "13900000111")
	emp2ID := makeEmployee(t, db, "13900000222", "注销测试乙")
	require.NotEqual(t, emp1ID, emp2ID)

	// 给 emp1 塞一条访客统计，验证注销会把它一起清掉
	db.Create(&model.StatEvent{StaffID: emp1ID, EventType: "visit", StatDate: "2026-10-26"})

	// 注销 emp1
	code, body := c.do("POST", "/api/employee/me/cancel", nil, emp1Token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])

	// 本人已查不到：员工记录被删后，令牌版本校验（TokenVersion 按主键查员工）失败，
	// 中间件直接以 401 信封码锁死会话——这是删除权的「顺带吊销」副作用，比单纯返回 1 更彻底。
	_, me := c.do("GET", "/api/employee/me", nil, emp1Token)
	require.NotEqualValues(t, 0, me["code"], "注销后本人信息应查不到（会话被立即吊销）")

	// emp1 主记录没了
	var n1 int64
	db.Model(&model.Employee{}).Where("staff_id = ?", emp1ID).Count(&n1)
	require.EqualValues(t, 0, n1, "注销后员工主记录应被删除")

	// emp2 还在
	var n2 int64
	db.Model(&model.Employee{}).Where("staff_id = ?", emp2ID).Count(&n2)
	require.EqualValues(t, 1, n2, "同租户其他员工不应被连累")

	// emp1 的访客统计一并清掉
	var ns int64
	db.Model(&model.StatEvent{}).Where("staff_id = ?", emp1ID).Count(&ns)
	require.EqualValues(t, 0, ns, "注销应连带清除本人的访客统计")

	// 审计留痕
	var na int64
	db.Model(&model.AuditLog{}).Where("action = ?", audit.ActionEmployeeCancel).Count(&na)
	require.EqualValues(t, 1, na)
}

// TestEmployeeCancelRequiresOwnSession 注销必须走已登录的本人身份，不能凭空删。
func TestEmployeeCancelRequiresOwnSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	// 未登录直接调 → 被鉴权拦（本项目约定 HTTP 恒 200，成败看信封 code）
	code, body := c.do("POST", "/api/employee/me/cancel", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.NotEqualValues(t, 0, body["code"], "未登录必须被拒")
	// 关键：被拒时绝不能真删——审计里不应出现任何注销记录
	var na int64
	db.Model(&model.AuditLog{}).Where("action = ?", audit.ActionEmployeeCancel).Count(&na)
	require.EqualValues(t, 0, na, "未登录的注销请求绝不能落库")
}

// TestAdminExportEmployee 后台导出某员工个人信息（PIPL 查阅复制权协助义务）。
//
// 验证点：
//  1. 同租户员工能被导出，data 含该员工 PII；
//  2. 导出记审计（ActionEmployeeExport，虽是只读但属高风险提取，特意留痕）；
//  3. 跨租户的导出应被 scopeCond 挡下（返回 code:1，不在本租户）；
//  4. 不存在的员工返回 code:1。
func TestAdminExportEmployee(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 同租户员工（company 1）
	empID := makeEmployee(t, db, "13900000333", "导出测试")
	_ = loginAsEmployee(t, c, "13900000333")

	code, body := c.do("GET", "/api/admin/employees/"+strconv.FormatInt(empID, 10)+"/export", nil, adminToken)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.EqualValues(t, empID, data["staffId"])
	require.NotEmpty(t, data["name"], "导出应含姓名等 PII")

	// 审计
	var na int64
	db.Model(&model.AuditLog{}).Where("action = ?", audit.ActionEmployeeExport).Count(&na)
	require.EqualValues(t, 1, na)

	// 不存在的员工
	_, body = c.do("GET", "/api/admin/employees/99999999/export", nil, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 跨租户：建公司 2 + 公司 2 的管理员，用公司 2 管理员导不出公司 1 的员工
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "他企", ShortName: "他企", Status: "enabled"}).Error)
	hash, _ := auth.HashPassword("other-pass-123")
	require.NoError(t, db.Create(&model.Admin{CompanyID: 2, Username: "admin2", PasswordHash: hash}).Error)
	_, loginBody := c.do("POST", "/api/admin/login", map[string]interface{}{"username": "admin2", "password": "other-pass-123"}, "")
	require.EqualValues(t, 0, loginBody["code"])
	otherAdminToken := c.cookies["access_token"]

	// 公司 2 管理员试图导出公司 1 的员工 → 应被 scopeCond 挡下
	_, body = c.do("GET", "/api/admin/employees/"+strconv.FormatInt(empID, 10)+"/export", nil, otherAdminToken)
	require.EqualValues(t, 1, body["code"], "跨租户导出必须被拒")
}

// makeEmployee 直接落一条员工记录并返回其 staff_id（测试隔离下可控）。
func makeEmployee(t *testing.T, db *gorm.DB, phone, name string) int64 {
	t.Helper()
	hash, err := auth.HashPassword("init-pwd-123")
	require.NoError(t, err)
	e := model.Employee{
		CompanyID:    1,
		Name:         name,
		Phone:        phone,
		PasswordHash: hash,
		Status:       "enabled",
	}
	require.NoError(t, db.Create(&e).Error)
	return e.StaffID
}

// loginAsEmployee 用手机号 + 初始密码登录，返回员工令牌。
func loginAsEmployee(t *testing.T, c *testClient, phone string) string {
	t.Helper()
	_, body := c.do("POST", "/api/employee/login", map[string]interface{}{"phone": phone, "password": "init-pwd-123"}, "")
	require.EqualValues(t, 0, body["code"])
	tok, ok := body["token"].(string)
	require.True(t, ok, "员工登录应返回 token")
	return tok
}
