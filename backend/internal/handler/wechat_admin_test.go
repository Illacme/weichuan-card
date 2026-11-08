package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 微信绑定「可维护」层（P1）守门。
//
// 重点不是 happy path，而是三条边界：
//   - 企业管理员只能看/解绑本企业（scopeCond 收窄），跨租户反查只给平台超管；
//   - openid 只进不出：清单与反查的整包 JSON 里都不许出现原文，审计 Detail 里连掩码都不写；
//   - 解绑必须真的断掉入口：清列 + 安全戳自增，已签发的令牌立刻 401。
//
// 复用 bindingEngine（wechat_binding_test.go）：独立临时库 + 全量路由 + 关内容安全。
const (
	wxaPlatOpenID = "oFULL-plat-wx-9876543210"
	wxaEntOpenID  = "oFULL-ent-wx-1234567890"
	wxaEnt2OpenID = "oFULL-ent2-wx-5555555555"
	wxaEmpOpenID  = "oFULL-emp-openid-abcdef"
)

type wechatAdminFixture struct {
	c           *testClient
	db          *gorm.DB
	platToken   string
	entToken    string
	platAdminID int64
	entAdminID  int64
	ent2AdminID int64
	empID       int64
}

// wechatAdminFixture 造四类账号：平台超管（已绑）、企业 1 管理员（已绑 + 可登录）、
// 企业 2 管理员（已绑，用于验证越权）、企业 1 员工（已绑）。
func newWechatAdminFixture(t *testing.T) wechatAdminFixture {
	t.Helper()
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	platToken := loginAdmin(t, c)

	var plat model.Admin
	require.NoError(t, db.Where("company_id = ?", 0).First(&plat).Error)
	require.NoError(t, db.Model(&model.Admin{}).Where("id = ?", plat.ID).
		Update("wx_openid", wxaPlatOpenID).Error)

	hash, err := auth.HashPassword("entpass123")
	require.NoError(t, err)
	ent := model.Admin{CompanyID: 1, Username: "entboss", PasswordHash: hash, WXOpenID: wxaEntOpenID}
	require.NoError(t, db.Create(&ent).Error)

	require.NoError(t, db.Create(&model.Company{
		ID: 2, Name: "第二家企业", ShortName: "第二家", Status: "enabled", LoginCode: "second",
	}).Error)
	ent2 := model.Admin{CompanyID: 2, Username: "entboss2", PasswordHash: hash, WXOpenID: wxaEnt2OpenID}
	require.NoError(t, db.Create(&ent2).Error)

	empID := applyAndApprove(t, c, platToken, "绑定员工", "13500000051", wxaEmpOpenID, 0)

	_, body := c.do("POST", "/api/admin/login/mobile", map[string]interface{}{
		"username": "entboss", "password": "entpass123",
	}, "")
	require.EqualValues(t, 0, body["code"], "企业管理员登录应成功：%v", body["msg"])

	return wechatAdminFixture{
		c: c, db: db, platToken: platToken, entToken: body["token"].(string),
		platAdminID: plat.ID, entAdminID: ent.ID, ent2AdminID: ent2.ID, empID: empID,
	}
}

// TestWechatBindingListScope 清单按身份收窄，且只回掩码。
func TestWechatBindingListScope(t *testing.T) {
	f := newWechatAdminFixture(t)

	// 平台超管：四类账号全见（company_id=0 的平台超管 + 两家企业管理员 + 一名员工）
	_, body := f.c.do("GET", "/api/admin/wechat/bindings", nil, f.platToken)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	rows := body["data"].([]interface{})
	require.Len(t, rows, 4)
	full := bindingJSON(t, body)
	for _, raw := range []string{wxaPlatOpenID, wxaEntOpenID, wxaEnt2OpenID, wxaEmpOpenID} {
		require.NotContains(t, full, raw, "清单绝不能回 openid 原文")
	}
	for _, it := range rows {
		row := it.(map[string]interface{})
		require.Equal(t, true, row["wechatBound"])
		require.Contains(t, row, "wxOpenidMasked")
		require.Contains(t, row, "companyName")
		require.Contains(t, []string{"admin", "employee"}, row["kind"])
	}
	// 企业名回填：平台超管的 companyName 应是「平台」，企业 1 应是「测试」
	for _, it := range rows {
		row := it.(map[string]interface{})
		if row["companyId"] == float64(0) {
			require.Equal(t, "平台", row["companyName"])
		}
	}

	// 企业 1 管理员：只看得到本企业管理员 + 本企业员工，共 2 条
	_, body = f.c.do("GET", "/api/admin/wechat/bindings", nil, f.entToken)
	require.EqualValues(t, 0, body["code"])
	entRows := body["data"].([]interface{})
	require.Len(t, entRows, 2, "scopeCond 必须把清单收窄到本企业")
	for _, it := range entRows {
		require.Equal(t, float64(1), it.(map[string]interface{})["companyId"])
	}
	require.NotContains(t, bindingJSON(t, body), wxaEnt2OpenID)
	require.NotContains(t, bindingJSON(t, body), wxaPlatOpenID)

	// 关键字与分页：服务端信封 {code,data,total,page,size}
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?keyword=entboss", nil, f.platToken)
	require.EqualValues(t, 2, body["total"])
	// 类型筛选在分页之前生效：total 必须是筛后的真实条数，否则翻页会翻出空白页
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?kind=employee", nil, f.platToken)
	require.EqualValues(t, 1, body["total"])
	require.Len(t, body["data"].([]interface{}), 1)
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?kind=admin", nil, f.platToken)
	require.EqualValues(t, 3, body["total"])
	// 非法 kind 按「全部」处理，不让手拼的查询串把清单弄成空列表
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?kind=hacker", nil, f.platToken)
	require.EqualValues(t, 4, body["total"])
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?page=1&size=2", nil, f.platToken)
	require.EqualValues(t, 4, body["total"])
	require.Len(t, body["data"].([]interface{}), 2)
	require.EqualValues(t, 1, body["page"])
	// 越界页码夹到末页（删完数据后停在第 99 页不该显示空白）
	_, body = f.c.do("GET", "/api/admin/wechat/bindings?page=99&size=2", nil, f.platToken)
	require.EqualValues(t, 2, body["page"])
	require.NotEmpty(t, body["data"].([]interface{}))
}

// TestWechatLookupPlatformOnly 反查是「输入完整 openid → 输出账号」，方向不能反过来。
func TestWechatLookupPlatformOnly(t *testing.T) {
	f := newWechatAdminFixture(t)

	// 企业管理员连查都不许查（跨租户关联能力只给平台超管）
	_, body := f.c.do("POST", "/api/admin/wechat/lookup",
		map[string]interface{}{"openid": wxaEmpOpenID}, f.entToken)
	require.EqualValues(t, 403, body["code"])

	// 参数：空值 / 超长都得挡在查询之前
	for _, bad := range []string{"", "  ", strings.Repeat("x", 129)} {
		_, b := f.c.do("POST", "/api/admin/wechat/lookup",
			map[string]interface{}{"openid": bad}, f.platToken)
		require.EqualValues(t, 1, b["code"], "openid=%q 应被拒", bad)
	}

	// 命中员工
	_, body = f.c.do("POST", "/api/admin/wechat/lookup",
		map[string]interface{}{"openid": wxaEmpOpenID}, f.platToken)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	rows := body["data"].([]interface{})
	require.Len(t, rows, 1)
	require.Equal(t, "employee", rows[0].(map[string]interface{})["kind"])
	require.EqualValues(t, f.empID, int64(rows[0].(map[string]interface{})["id"].(float64)))
	require.Equal(t, maskOpenIDForDisplay(wxaEmpOpenID), rows[0].(map[string]interface{})["wxOpenidMasked"])
	require.NotContains(t, bindingJSON(t, body), wxaEmpOpenID, "反查输出绝不回 openid 原文")

	// 命中管理员 + 查无此号
	_, body = f.c.do("POST", "/api/admin/wechat/lookup",
		map[string]interface{}{"openid": wxaPlatOpenID}, f.platToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "admin", body["data"].([]interface{})[0].(map[string]interface{})["kind"])
	_, body = f.c.do("POST", "/api/admin/wechat/lookup",
		map[string]interface{}{"openid": "o-nobody-0000"}, f.platToken)
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, body["data"].([]interface{}))

	// 只读但破例留痕，且 Detail 里连掩码都不能有
	_, logs := f.c.do("GET", "/api/admin/audit-logs?action="+audit.ActionWechatLookup, nil, f.platToken)
	require.EqualValues(t, 0, logs["code"])
	auditRows := logs["data"].([]interface{})
	require.Len(t, auditRows, 3, "三次成功反查各记一条")
	for _, it := range auditRows {
		d := it.(map[string]interface{})["detail"].(string)
		require.Contains(t, d, "admin_count")
		require.Contains(t, d, "employee_count")
		for _, raw := range []string{wxaEmpOpenID, wxaPlatOpenID, maskOpenIDForDisplay(wxaEmpOpenID)} {
			require.NotContains(t, d, raw, "审计 Detail 不许写 openid / 掩码")
		}
	}
}

// TestWechatUnbind 解绑的租户边界、幂等拒绝、安全戳自增与审计。
func TestWechatUnbind(t *testing.T) {
	f := newWechatAdminFixture(t)

	// 企业管理员不能解绑别家企业的管理员（scopeCond 把 UPDATE 的 WHERE 也收窄了）
	_, body := f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "admin", "id": f.ent2AdminID}, f.entToken)
	require.EqualValues(t, 1, body["code"])
	var still model.Admin
	require.NoError(t, f.db.First(&still, "id = ?", f.ent2AdminID).Error)
	require.Equal(t, wxaEnt2OpenID, still.WXOpenID, "越权解绑不得落库")

	// 参数校验
	_, body = f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "other", "id": f.empID}, f.platToken)
	require.EqualValues(t, 1, body["code"])

	// ===== 解绑员工：清 openid + 安全戳自增 → 已签发令牌立即 401 =====
	empToken, _, _, err := auth.IssueTokens(f.empID, "employee", 0, 1)
	require.NoError(t, err)
	_, me := f.c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, me["code"], "解绑前员工令牌应有效")

	_, body = f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "employee", "id": f.empID}, f.entToken)
	require.EqualValues(t, 0, body["code"], "企业管理员应能解绑本企业员工：%v", body["msg"])
	require.Equal(t, false, body["self"])

	var emp model.Employee
	require.NoError(t, f.db.First(&emp, "staff_id = ?", f.empID).Error)
	require.Equal(t, "", emp.OpenID)
	require.Equal(t, 1, emp.TokenVersion, "解绑必须踢掉已签发令牌")
	_, me = f.c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 401, me["code"], "解绑后旧令牌必须失效，否则「解了绑手机上还登着」")

	// 反查现在查不到这个 openid
	_, body = f.c.do("POST", "/api/admin/wechat/lookup",
		map[string]interface{}{"openid": wxaEmpOpenID}, f.platToken)
	require.Empty(t, body["data"].([]interface{}))

	// 重复解绑明确拒绝，不制造第二条审计
	_, body = f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "employee", "id": f.empID}, f.platToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "未绑定")

	// ===== 解绑企业 1 管理员（平台超管代操作）=====
	_, body = f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "admin", "id": f.entAdminID}, f.platToken)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	var ent model.Admin
	require.NoError(t, f.db.First(&ent, "id = ?", f.entAdminID).Error)
	require.Equal(t, "", ent.WXOpenID)
	require.Equal(t, 1, ent.TokenVersion)

	// 审计两条动作，Detail 不含姓名 / openid / 掩码
	for _, act := range []string{audit.ActionEmployeeWXUnbind, audit.ActionAdminWXUnbind} {
		_, logs := f.c.do("GET", "/api/admin/audit-logs?action="+act, nil, f.platToken)
		require.EqualValues(t, 0, logs["code"])
		require.Len(t, logs["data"].([]interface{}), 1, "%s 应只记一条", act)
		d := logs["data"].([]interface{})[0].(map[string]interface{})["detail"].(string)
		for _, raw := range []string{wxaEmpOpenID, wxaEntOpenID, maskOpenIDForDisplay(wxaEmpOpenID), "绑定员工"} {
			require.NotContains(t, d, raw, "审计 Detail 不该变成第二份 PII 副本")
		}
	}

	// ===== 解绑自己：self=true 且当前登录立即失效（放最后，后面的断言别再用 platToken）=====
	_, body = f.c.do("POST", "/api/admin/wechat/unbind",
		map[string]interface{}{"kind": "admin", "id": f.platAdminID}, f.platToken)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	require.Equal(t, true, body["self"])
	_, body = f.c.do("GET", "/api/admin/wechat/bindings", nil, f.platToken)
	require.EqualValues(t, 401, body["code"], "解绑自己后旧令牌必须失效")
}

// TestUnbindIsSelf 钉住「两张表主键撞号」这个隐蔽误判：
// 员工 staff_id 与管理员 id 各自自增，数值相等纯属巧合。若拿 id 直接比 staffId，
// 解绑一个碰巧同号的员工会把 self 置 true，前端就把当前管理员踢去重新登录。
func TestUnbindIsSelf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newCtx := func(role string, staffID int64) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("role", role)
		c.Set("staffId", staffID)
		return c
	}

	admin := newCtx("admin", 7)
	require.True(t, unbindIsSelf("admin", 7, admin), "解绑当前登录的管理员才算 self")
	require.False(t, unbindIsSelf("admin", 8, admin), "同级别的另一个管理员不是自己")
	require.False(t, unbindIsSelf("employee", 7, admin), "撞号的员工不是调用者本人")
	require.False(t, unbindIsSelf("employee", 9, admin))

	// 防御：本端点目前强制 role=admin，但若哪天放宽鉴权，员工身份也不该匹配管理员 id
	emp := newCtx("employee", 7)
	require.False(t, unbindIsSelf("admin", 7, emp))
	require.False(t, unbindIsSelf("employee", 7, emp), "本端点不提供员工自助解绑，self 语义不适用")
}
