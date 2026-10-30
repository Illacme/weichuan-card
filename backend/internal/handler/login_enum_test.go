package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoginNoAccountEnumeration 登录失败不得泄露「账号是否存在」。
//
// 背景：改造前两条路径的回复是不同的 ——
//
//	admin:    账号不存在 → "账号不存在"，存在但密码错 → "密码错误"
//	employee: 账号不存在 → "账号不存在"，存在但密码错 → "密码错误"
//
// 于是任何人拿一份用户名字典打一遍，就能把「哪些账号真实存在」筛出来，
// 再对这批账号集中爆破（配合限流的 10 次/分，枚举阶段完全不触发阈值）。
//
// 两条硬性要求，缺一条都算没修：
//  1. 文案必须完全一致 —— 只改一半等于没改；
//  2. 账号不存在时也要走一次等价代价的 bcrypt 比较 —— 否则「回复快 = 账号不存在」
//     本身就是可测量的信号，文案统一了照样能枚举。
//
// 第 2 条用响应耗时断言不稳（CI 机器负载会抖），这里只钉住文案与状态披露顺序，
// 时间侧由 auth.CheckPasswordDummy 的实现保证（cost 与 HashPassword 一致）。
func TestLoginNoAccountEnumeration(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 建一个真实存在的员工，密码用接口回传的初始密码
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "枚举测试", "phone": "13700000099", "title": "员工",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	empID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)

	// probe 用独立 client：登录会写 cookie jar，用同一个会把主 client 的身份顶掉
	probe := newTestClient(r, t)

	// ---- 超管登录 ----
	_, noSuchUser := probe.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "__definitely_not_an_admin__", "password": "whatever123",
	}, "")
	_, wrongPwd := probe.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "definitely-wrong",
	}, "")

	require.EqualValues(t, 1, noSuchUser["code"])
	require.EqualValues(t, 1, wrongPwd["code"])
	require.Equal(t, wrongPwd["msg"], noSuchUser["msg"],
		"超管登录：账号不存在与密码错误必须返回同一文案，否则可枚举账号")

	// ---- 员工登录 ----
	_, noSuchPhone := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13700000000", "password": "whatever123",
	}, "")
	_, empWrongPwd := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13700000099", "password": "definitely-wrong",
	}, "")

	require.EqualValues(t, 1, noSuchPhone["code"])
	require.EqualValues(t, 1, empWrongPwd["code"])
	require.Equal(t, empWrongPwd["msg"], noSuchPhone["msg"],
		"员工登录：账号不存在与密码错误必须返回同一文案")

	// ---- 停用账号：状态只能在密码校验通过之后披露 ----
	// 若「账号已停用」在验密之前就返回，它等于确认了「这个手机号是员工」，
	// 上面统一文案就白做了。
	_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/disable", empID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	_, disabledWrongPwd := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13700000099", "password": "definitely-wrong",
	}, "")
	require.Equal(t, empWrongPwd["msg"], disabledWrongPwd["msg"],
		"停用账号配错密码时，不得返回「账号已停用」——那会泄露该手机号是员工账号")
	require.EqualValues(t, 1, disabledWrongPwd["code"])

	// 密码正确后才告知停用，用户体验与安全都不吃亏
	_, disabledRightPwd := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13700000099", "password": initPwd,
	}, "")
	require.EqualValues(t, 2, disabledRightPwd["code"])
	require.Contains(t, disabledRightPwd["msg"], "停用")
}
