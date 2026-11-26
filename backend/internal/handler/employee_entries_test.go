package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmployeeMenuEntries 员工级「小程序我的页菜单功能授权」四项开关。
//
// 产品口径（四项分成两组，默认值相反）：
//   - 生成海报 / 数据统计 = true：这是「我的」页的本职功能，绝大多数员工都要用；
//     默认关等于升级当天全员菜单凭空少两块，管理员还得逐个补开。
//   - 团队成员 / 管理员登录 = false：看全员名单、企业管理员入口都是额外能力，
//     默认放开会让每个员工一升级就多两项管理员没同意过的菜单。
//
// 实现上要防三类缺陷（用例逐条覆盖）：
//  1. 默认值的两组差异——别把四项一刀切成全 true 或全 false；
//  2. 关得掉——独立授权接口用 map 更新，省略键等于保持原值；
//  3. 授权独立——编辑资料接口不得碰这四列，否则改个头像就把授权清零。
//
// 四项只影响员工端菜单，不影响名片墙本身，也不进员工名片修改审核流。
func TestEmployeeMenuEntries(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 新建员工不带任何授权字段：落产品默认（海报 / 统计开，团队 / 管理员登录关）
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "授权测试", "phone": "13911110001",
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)

	require.Equal(t, true, employeeField(t, c, staffID, "showPosterEntry"), "海报默认开")
	require.Equal(t, false, employeeField(t, c, staffID, "showTeamEntry"), "团队成员默认关")
	require.Equal(t, true, employeeField(t, c, staffID, "showStatEntry"), "统计默认开")
	require.Equal(t, false, employeeField(t, c, staffID, "showAdminEntry"), "管理员登录默认关")

	// 员工登录，/api/employee/me 根级四项都要回（不是塞进 data）
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13911110001", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	empToken := body["token"].(string)
	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["showPosterEntry"])
	require.Equal(t, false, body["showTeamEntry"])
	require.Equal(t, true, body["showStatEntry"])
	require.Equal(t, false, body["showAdminEntry"])

	// 管理员通过独立授权接口开「团队成员」+「管理员登录」
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d/entries", staffID), map[string]interface{}{
		"showTeamEntry": true, "showAdminEntry": true,
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, employeeField(t, c, staffID, "showTeamEntry"))
	require.Equal(t, true, employeeField(t, c, staffID, "showAdminEntry"))
	// 只传两项，另两项必须保持原值——局部更新不能被清零
	require.Equal(t, true, employeeField(t, c, staffID, "showPosterEntry"), "局部更新不得清掉海报")
	require.Equal(t, true, employeeField(t, c, staffID, "showStatEntry"), "局部更新不得清掉统计")

	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["showTeamEntry"], "开启后员工端应拿到 true")
	require.Equal(t, true, body["showAdminEntry"])

	// 关掉：必须真的写回 false（map 更新里不能省略这个键）
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d/entries", staffID), map[string]interface{}{
		"showTeamEntry": false, "showPosterEntry": false,
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, employeeField(t, c, staffID, "showTeamEntry"), "关掉必须真正写回 false")
	require.Equal(t, false, employeeField(t, c, staffID, "showPosterEntry"), "海报也能关掉")
	_, body = c.do("GET", "/api/employee/me", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, body["showTeamEntry"])
	require.Equal(t, false, body["showPosterEntry"])

	// 编辑资料接口不得碰授权：只传资料字段，四项必须原样保留
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", staffID), map[string]interface{}{
		"name": "授权测试-改名", "phone": "13911110001", "title": "总监",
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, false, employeeField(t, c, staffID, "showPosterEntry"), "编辑资料不得重置海报授权")
	require.Equal(t, false, employeeField(t, c, staffID, "showTeamEntry"), "编辑资料不得重置团队授权")
	require.Equal(t, true, employeeField(t, c, staffID, "showStatEntry"), "编辑资料不得重置统计授权")
	require.Equal(t, true, employeeField(t, c, staffID, "showAdminEntry"), "编辑资料不得重置管理员授权")

	// 空授权体必须报错，不能静默成功（否则前端漏传参数时管理员以为保存了）
	_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d/entries", staffID), map[string]interface{}{}, c.cookies["access_token"])
	require.EqualValues(t, 1, body["code"])

	// 授权开关不影响名片墙本身：/api/team 仍照常回全员
	_, body = c.do("GET", "/api/team?companyId=1", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.GreaterOrEqual(t, len(body["data"].([]interface{})), 1, "名片墙不受员工级授权影响")
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
