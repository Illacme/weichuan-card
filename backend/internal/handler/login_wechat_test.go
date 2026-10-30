package handler

import (
	"fmt"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 微信一键登录 / 绑定。
//
// 这条链路的价值是「员工不用记密码」，所以测试要钉住的不是 happy path 一条，
// 而是所有会让「点了没反应 / 登进别人账号」的分支：
//   - 未绑定时必须明确告诉用户去绑定，而不是笼统的「登录失败」；
//   - 停用账号 / 停用企业照样拦住（openid 只证明「你是同一个微信」，
//     证明不了「你还在这家公司」）；
//   - 同一微信在多家企业任职时不能随便挑一条签发令牌（会登进别家公司）；
//   - 绑定必须登录态 + 同企业内一个微信只能绑一个账号。

// stubCode2Session 把 code → openid 固定下来：真接口要外网 + 真凭据，测试里换掉。
// 映射故意做得「一个 code 对一个 openid」，因为真实 code 也是一次性的。
func stubCode2Session(t *testing.T) {
	t.Helper()
	orig := code2Session
	t.Cleanup(func() { code2Session = orig })
	code2Session = func(_ wechat.Config, code string) (string, string, error) {
		switch code {
		case "code-a":
			return "openid-a", "sk-a", nil
		case "code-b":
			return "openid-b", "sk-b", nil
		}
		// 真实微信对过期/重复使用的 code 也是报错，这里同构
		return "", "", fmt.Errorf("jscode2session err 40163: code been used")
	}
}

// withWXCreds 给测试配置小程序凭据。走 config 而不是 sys_setting：
// wxAppOf 的回落链最后一环就是 cfg，不依赖任何缓存状态，跨用例不会互相污染。
func withWXCreds(c *config.Config) {
	c.WXAppID = "wx-test-appid"
	c.WXAppSecret = "wx-test-secret"
}

// prepWechatTest 建库 + 调高登录限流 + 关掉内容安全。
//
// 关内容安全是因为开通申请里带 openid，配了凭据后 contentsec 会真的去打微信接口；
// 测试不该依赖外网，也不该因为外网抖动变红。
func prepWechatTest(t *testing.T) *testClient {
	t.Helper()
	r := setupSQLiteEngine(t, withWXCreds)
	require.NoError(t, setting.Upsert("login_rate_limit_per_min", "200"))
	require.NoError(t, setting.Upsert("content_sec_enabled", "false"))
	stubCode2Session(t)
	return newTestClient(r, t)
}

// applyAndApprove 走「自助申请 → 审批通过」把员工建出来。
// 这条路径会把申请里的 openid 写进员工记录（apply.go 的审批分支），
// 正是一键登录「开箱可用」的那批人。
func applyAndApprove(t *testing.T, c *testClient, adminToken, name, phone, openid string, companyID int64) int64 {
	t.Helper()
	req := map[string]interface{}{"name": name, "phone": phone, "title": "销售", "openid": openid}
	if companyID > 0 {
		req["companyId"] = companyID
	}
	_, body := c.do("POST", "/api/apply/open", req, "")
	require.EqualValues(t, 0, body["code"], "提交开通申请应成功：%v", body["msg"])

	applyID := findApplyIDByPhone(t, c, adminToken, phone)
	require.NotZero(t, applyID, "应能找到刚提交的申请")
	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", applyID), nil, adminToken)
	require.EqualValues(t, 0, body["code"], "审批通过应建出员工：%v", body["msg"])
	staffID := int64(body["staffId"].(float64))
	require.NotZero(t, staffID)
	return staffID
}

// TestWechatLoginWithoutCredentials 没配小程序凭据时给明确原因，
// 而不是笼统的「微信登录失败」——部署方看到 AppID 三个字就知道该去填什么。
func TestWechatLoginWithoutCredentials(t *testing.T) {
	r := setupSQLiteEngine(t) // 不注入 WX 凭据
	stubCode2Session(t)
	c := newTestClient(r, t)

	_, body := c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "AppID")
}

// TestWechatLogin 一键登录的完整判定矩阵。
func TestWechatLogin(t *testing.T) {
	c := prepWechatTest(t)
	adminToken := loginAdmin(t, c)

	// ===== 1. 参数与微信侧错误 =====
	_, body := c.do("POST", "/api/employee/login/wechat", map[string]interface{}{}, "")
	require.EqualValues(t, 1, body["code"], "缺 code 应拒绝")

	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-used"}, "")
	require.EqualValues(t, 1, body["code"], "微信侧报错应回 code:1")
	// 不把微信原始错误码甩给用户
	require.NotContains(t, body["msg"], "40163")

	// ===== 2. 未绑定：明确引导去绑定，而不是「登录失败」=====
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 3, body["code"], "未绑定应是 code:3（需要走密码登录）")
	require.Contains(t, body["msg"], "绑定")

	// ===== 3. 自助申请 → 审批，openid 随员工记录落库，开箱即可一键登录 =====
	staffID := applyAndApprove(t, c, adminToken, "一键甲", "13500000001", "openid-a", 0)

	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 0, body["code"], "已绑定应登录成功：%v", body["msg"])
	token := body["token"].(string)
	require.NotEmpty(t, token)
	require.NotEmpty(t, body["refreshToken"], "一键登录同样要下发 refreshToken，否则 15 分钟后被迫重登")

	// 拿到的令牌是真能用的，且 wechatBound 已置位
	_, body = c.do("GET", "/api/employee/me", nil, token)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["wechatBound"], "「我的」页靠它显示「已绑定」")
	require.EqualValues(t, staffID, int64(body["data"].(map[string]interface{})["staffId"].(float64)))

	// ===== 4. 停用账号：openid 证明不了「你还在这家公司」，照样拦 =====
	_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/disable", staffID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 2, body["code"], "停用账号应回 code:2")
	require.Contains(t, body["msg"], "停用")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/enable", staffID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	// ===== 5. 停用企业：员工自己没被停，但所属企业停了同样不能进 =====
	_, body = c.do("POST", "/api/admin/companies/1/status", map[string]interface{}{"status": "disabled"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 2, body["code"], "企业停用应回 code:2")
	require.Contains(t, body["msg"], "企业")

	_, body = c.do("POST", "/api/admin/companies/1/status", map[string]interface{}{"status": "enabled"}, adminToken)
	require.EqualValues(t, 0, body["code"])
}

// TestWechatLoginMultiTenant 同一微信在两家企业都任职时：
// 带 companyId 要登进对应那家；不带就得拒掉，绝不能随便挑一条签发令牌
// ——挑错等于让 A 公司的人拿到的令牌能看 B 公司的名片墙。
func TestWechatLoginMultiTenant(t *testing.T) {
	c := prepWechatTest(t)
	adminToken := loginAdmin(t, c)

	// 第二家企业（平台超管建）
	_, body := c.do("POST", "/api/admin/companies", map[string]interface{}{
		"name": "第二家公司", "shortName": "第二家", "adminUsername": "second", "adminPassword": "secondpass",
		"seedCategories": false,
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	comp2 := int64(body["data"].(map[string]interface{})["id"].(float64))

	// 同一个 openid 在两家企业各建一个员工（模拟同一个人在两家兼职）
	applyAndApprove(t, c, adminToken, "兼职甲", "13500000011", "openid-a", 0)
	applyAndApprove(t, c, adminToken, "兼职甲", "13500000011", "openid-a", comp2)

	// 带 companyId：登进对应的那家
	_, body = c.do("POST", "/api/employee/login/wechat",
		map[string]interface{}{"code": "code-a", "companyId": comp2}, "")
	require.EqualValues(t, 0, body["code"], "带 companyId 应能登录：%v", body["msg"])
	_, me := c.do("GET", "/api/employee/me", nil, body["token"].(string))
	require.Equal(t, "第二家公司", me["companyName"])

	// 不带 companyId：两条记录，无法判断登哪家 → 让用户走密码登录
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 3, body["code"], "跨租户歧义必须拒绝，不能挑一条签发")
	require.Contains(t, body["msg"], "多个")
}

// TestWechatBind 绑定流程：未登录不能绑、绑完能一键登录、同企业内一个微信只能绑一个账号。
func TestWechatBind(t *testing.T) {
	c := prepWechatTest(t)
	adminToken := loginAdmin(t, c)

	// 管理员后台建号（这条路径没有 openid，是「需要手动绑一次」的那批人）
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "绑定甲", "phone": "13500000021", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)

	// ===== 1. 未登录不能绑定（否则任何人都能把别人的微信号绑到自己账号上）=====
	_, body = c.do("POST", "/api/employee/wechat/bind", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 401, body["code"])

	// 密码登录拿令牌
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13500000021", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	token := body["token"].(string)

	// 绑之前 wechatBound=false，一键登录也进不去
	_, me := c.do("GET", "/api/employee/me", nil, token)
	require.Equal(t, false, me["wechatBound"])
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 3, body["code"])

	// ===== 2. 绑定成功，之后可一键登录 =====
	_, body = c.do("POST", "/api/employee/wechat/bind", map[string]interface{}{"code": "code-a"}, token)
	require.EqualValues(t, 0, body["code"], "绑定应成功：%v", body["msg"])

	_, me = c.do("GET", "/api/employee/me", nil, token)
	require.Equal(t, true, me["wechatBound"])

	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 0, body["code"], "绑定后应能一键登录：%v", body["msg"])
	_, me = c.do("GET", "/api/employee/me", nil, body["token"].(string))
	require.EqualValues(t, staffID, int64(me["data"].(map[string]interface{})["staffId"].(float64)))

	// ===== 3. 同一微信绑第二个账号 → 拒绝 =====
	// 否则两个人共用一个微信号时，谁点「一键登录」都会进到先绑的那个人，
	// 另一人永远登不进自己的名片。
	_, body = c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "绑定乙", "phone": "13500000022", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	initPwd2 := body["initialPassword"].(string)
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13500000022", "password": initPwd2,
	}, "")
	require.EqualValues(t, 0, body["code"])
	token2 := body["token"].(string)

	_, body = c.do("POST", "/api/employee/wechat/bind", map[string]interface{}{"code": "code-a"}, token2)
	require.EqualValues(t, 1, body["code"], "同一微信绑第二个账号必须被拒")
	require.Contains(t, body["msg"], "其他员工")

	// ===== 4. 换微信号：新微信覆盖旧的，不需要单独的「解绑」接口 =====
	_, body = c.do("POST", "/api/employee/wechat/bind", map[string]interface{}{"code": "code-b"}, token)
	require.EqualValues(t, 0, body["code"], "换微信应覆盖绑定：%v", body["msg"])

	// 旧微信已经匹配不到任何员工
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-a"}, "")
	require.EqualValues(t, 3, body["code"], "换绑后旧微信不应再能登录")
	// 新微信可以
	_, body = c.do("POST", "/api/employee/login/wechat", map[string]interface{}{"code": "code-b"}, "")
	require.EqualValues(t, 0, body["code"], "换绑后新微信应能一键登录：%v", body["msg"])
}
