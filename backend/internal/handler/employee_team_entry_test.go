package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmployeeShowTeamEntry 员工级开关「小程序『我的』页是否显示『团队成员』入口」。
//
// 这个开关的产品口径有两条，缺一条就会出错：
//  1. **默认关闭**：看全员名单是额外开放的能力，不勾就不出现。默认放开等于升级当天
//     给所有员工凭空多一项管理员没同意过的菜单。
//  2. **能关掉**：编辑接口用 map 更新，若把字段当可选项（omitempty / 不传就跳过），
//     取消勾选就永远写不回 false——「开了关不掉」是这类开关最常见的缺陷。
//
// 另外它只影响员工端菜单，不影响名片墙本身（/api/team 不受约束），也不进员工
// 名片修改审核流（它属于管理侧配置，不是员工自填的名片内容）。
func TestEmployeeShowTeamEntry(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 新建员工不带 showTeamEntry：默认 false
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "开关测试", "phone": "13911110001",
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)

	// 列表里字段存在且为 false（前端 el-switch 直接绑它，缺字段会渲染成 undefined）
	require.Equal(t, false, employeeField(t, c, staffID, "showTeamEntry"))

	// 员工登录，/api/employee/me 根级回 false
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13911110001", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	empToken := body["token"].(string)
	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, body["showTeamEntry"], "默认不显示团队成员入口")

	// 管理员开启
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", staffID), map[string]interface{}{
		"name": "开关测试", "phone": "13911110001", "showTeamEntry": true,
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, employeeField(t, c, staffID, "showTeamEntry"))

	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["showTeamEntry"], "开启后员工端应拿到 true")

	// 关掉：必须真的写回 false（map 更新里不能省略这个键）
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", staffID), map[string]interface{}{
		"name": "开关测试", "phone": "13911110001", "showTeamEntry": false,
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, employeeField(t, c, staffID, "showTeamEntry"), "关掉必须真正写回 false")
	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, body["showTeamEntry"])

	// 开关不影响名片墙本身：/api/team 仍照常回全员
	_, body = c.do("GET", "/api/team?companyId=1", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.GreaterOrEqual(t, len(body["data"].([]interface{})), 1, "名片墙不受员工级开关影响")

	// 新建时就能勾选
	_, body = c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "新建就开", "phone": "13911110002", "showTeamEntry": true,
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, employeeField(t, c, int64(body["staffId"].(float64)), "showTeamEntry"))
}

// employeeField 从员工列表里取某个员工的单个字段。
// 走列表接口而不是直接查库：这几个字段的出参整形（employeeBindingRows）也是被测对象，
// 直接查库会漏掉「字段在模型上有、但出参没带出去」这种缺陷。
func employeeField(t *testing.T, c *testClient, staffID int64, key string) any {
	t.Helper()
	_, body := c.do("GET", fmt.Sprintf("/api/admin/employees?staffIds=%d", staffID), nil, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	rows := body["data"].([]interface{})
	require.Len(t, rows, 1, "按 staffIds 精确取应恰好回一条")
	v, ok := rows[0].(map[string]interface{})[key]
	require.True(t, ok, "列表出参缺少字段 %s", key)
	return v
}
