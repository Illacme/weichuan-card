package handler

import (
	"fmt"
	"testing"

	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 审核结果订阅消息的测试。
//
// 这条链路的失败模式都很安静——审批照样成功、页面照样返回 code:0，
// 只有员工手机上少了那条通知。所以测试必须钉住四件事：
//  1. 审批/驳回**真的会**触发通知（少接一个 dispatch 就静默失效）；
//  2. 模板字段按配置的顺序填，不是写死的 thing1/phrase2/thing3；
//  3. 没 openid / 没配模板 / 字段数不对时**不发**，且不影响审批结果；
//  4. 用户没订阅（43101）时审批仍然是成功——通知失败绝不能回滚业务。

// sentMsg 捕获到的一次发送。
type sentMsg struct {
	cfg wechat.Config
	msg wechat.SubscribeMsg
}

// stubSubscribe 把发送替换成同步记录器。
//
// 同时把 dispatch 改成同步执行：线上是 goroutine（http.Post 没有超时，
// 微信卡住会把审批接口一起拖死），但测试必须能同步断言，
// 否则断言与 goroutine 竞争，时绿时红。
func stubSubscribe(t *testing.T, err error) *[]sentMsg {
	t.Helper()
	got := []sentMsg{}
	oldSend, oldDispatch := sendSubscribe, dispatch
	sendSubscribe = func(cfg wechat.Config, m wechat.SubscribeMsg) error {
		got = append(got, sentMsg{cfg: cfg, msg: m})
		return err
	}
	dispatch = func(fn func()) { fn() }
	t.Cleanup(func() { sendSubscribe, dispatch = oldSend, oldDispatch })
	return &got
}

// useSetting 临时改一个系统设置项，用例结束还原（setting 是进程内单例，
// 不还原会污染后续用例——同 hardening_test.go 的做法）。
func useSetting(t *testing.T, key, val string) {
	t.Helper()
	old := setting.GetString(key, "")
	require.NoError(t, setting.Upsert(key, val))
	t.Cleanup(func() { _ = setting.Upsert(key, old) })
}

// enableAuditTemplate 把「审核结果通知」配成可用状态。
func enableAuditTemplate(t *testing.T, fields string) {
	t.Helper()
	useSetting(t, "wx_tmpl_audit", "TPL-AUDIT")
	if fields != "" {
		useSetting(t, "wx_tmpl_audit_fields", fields)
	}
}

// TestSubscribeConfigEndpoint 小程序在调 wx.requestSubscribeMessage 之前要知道模板 ID。
// 未配置时必须是**空串**而不是报错：小程序据此跳过授权请求，
// 否则会弹一个「没有任何模板」的窗（微信对空 tmplIds 直接 fail，用户看到点了没反应）。
func TestSubscribeConfigEndpoint(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	_, body := c.do("GET", "/api/subscribe/config", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "", body["data"].(map[string]interface{})["auditTemplateId"],
		"未配置时应回空串，不是报错")

	enableAuditTemplate(t, "")
	_, body = c.do("GET", "/api/subscribe/config", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "TPL-AUDIT", body["data"].(map[string]interface{})["auditTemplateId"])
}

// TestApproveSendsAuditNotification 开通申请通过 → 给申请人发一条。
func TestApproveSendsAuditNotification(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "")

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅甲", "phone": "13800002001", "title": "销售", "openid": "o-sub-1",
	}, "")
	require.EqualValues(t, 0, body["code"])

	id := findApplyIDByPhone(t, c, adminToken, "13800002001")
	require.NotZero(t, id)
	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"], "审批本身必须成功")

	require.Len(t, *sent, 1, "审批通过必须发出一条通知")
	m := (*sent)[0].msg
	require.Equal(t, "o-sub-1", m.ToUser, "接收者是申请人（申请时带的 openid 已写进员工记录）")
	require.Equal(t, "TPL-AUDIT", m.TemplateID)
	require.Equal(t, "pages/employee/login/login", m.Page)
	require.Equal(t, "名片开通申请", m.Data["thing1"])
	require.Equal(t, "审核通过", m.Data["phrase2"])
	// 备注里绝不能出现初始密码：订阅消息会进微信服务通知，可能直接显示在锁屏上。
	// 而且他不需要密码——点开小程序就能微信一键登录。
	require.NotContains(t, m.Data["thing3"], body["initialPassword"], "通知里不能带初始密码")
	require.Contains(t, m.Data["thing3"], "微信一键登录")
}

// TestRejectSendsAuditNotification 驳回也要发——「没通过」比「通过了」更需要通知，
// 否则申请人只会一直等，而管理员以为已经告诉他了。
func TestRejectSendsAuditNotification(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "")

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅乙", "phone": "13800002002", "openid": "o-sub-2",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002002")
	require.NotZero(t, id)

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/reject", id),
		map[string]interface{}{"reason": "手机号不属于本公司"}, adminToken)
	require.EqualValues(t, 0, body["code"])

	require.Len(t, *sent, 1)
	m := (*sent)[0].msg
	require.Equal(t, "o-sub-2", m.ToUser)
	require.Equal(t, "未通过", m.Data["phrase2"])
	require.Equal(t, "手机号不属于本公司", m.Data["thing3"], "驳回理由就是用户最想知道的东西")
}

// 驳回没填理由：给一句中性的说明，而不是把空串塞进模板（空值会被微信回 47003）。
func TestRejectWithoutReasonUsesFallback(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "")

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅丙", "phone": "13800002003", "openid": "o-sub-3",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002003")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/reject", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Len(t, *sent, 1)
	require.NotEmpty(t, (*sent)[0].msg.Data["thing3"], "空备注会被微信回 47003")
}

// 申请时没带 openid（老客户端 / 未同意隐私）：没有可送达的通道，静默跳过。
// 不能因此让审批失败——没绑微信的员工照样要能开通。
func TestApproveSkipsWhenNoOpenID(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "")

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅丁", "phone": "13800002004",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002004")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, *sent, "没有 openid 就不该尝试发送")
}

// 未配置模板 ID：功能未启用，静默跳过（不是错误）。
func TestApproveSkipsWhenTemplateUnset(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	useSetting(t, "wx_tmpl_audit", "") // 显式置空

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅戊", "phone": "13800002005", "openid": "o-sub-5",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002005")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, *sent)
}

// 字段数不是 3：配置写错了。宁可不发也不发一条字段错位的通知
// （字段错位会被微信回 47003，而 47003 的原始 errmsg 完全看不出是「少填了一项」）。
func TestAuditNotifySkipsOnBadFieldConfig(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "thing1,phrase2") // 少一项

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅己", "phone": "13800002006", "openid": "o-sub-6",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002006")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"], "配置错误不能让审批失败")
	require.Empty(t, *sent, "字段数不对时不发")
}

// 字段名按配置走，不写死：运营换个模板（thing2/thing3/phrase1）时不该改代码。
func TestAuditNotifyUsesConfiguredFields(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, " thing4 , phrase9 , thing7 ") // 带空格，应被 trim

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅庚", "phone": "13800002007", "openid": "o-sub-7",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002007")
	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	require.Len(t, *sent, 1)
	m := (*sent)[0].msg
	require.Equal(t, "名片开通申请", m.Data["thing4"])
	require.Equal(t, "审核通过", m.Data["phrase9"])
	require.Contains(t, m.Data, "thing7")
}

// 用户没订阅（43101）：审批必须照样成功。一次性订阅「同意一次只能发一条」，
// 所以这个失败每天都会发生很多次，它绝不能回滚业务，也不能让审批接口报错。
func TestAuditNotifyNotGrantedStillApproves(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	stubSubscribe(t, &wechat.APIError{Code: wechat.ErrSubscribeNoPermission, Msg: "user refuse"})
	enableAuditTemplate(t, "")

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "订阅辛", "phone": "13800002008", "openid": "o-sub-8",
	}, "")
	require.EqualValues(t, 0, body["code"])
	id := findApplyIDByPhone(t, c, adminToken, "13800002008")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", id), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.NotZero(t, body["staffId"], "员工照建，账号照发")

	// 员工确实建出来了：用返回的初始密码能登录
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13800002008", "password": body["initialPassword"],
	}, "")
	require.EqualValues(t, 0, body["code"])
}

// 名片修改审核通过 → 通知员工本人（接收者 openid 在员工记录上，不在申请行上）。
func TestModifyApproveSendsAuditNotification(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	sent := stubSubscribe(t, nil)
	enableAuditTemplate(t, "")

	// 先走开通申请建出一个带 openid 的员工
	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "改资料甲", "phone": "13800002009", "openid": "o-mod-1",
	}, "")
	require.EqualValues(t, 0, body["code"])
	openID := findApplyIDByPhone(t, c, adminToken, "13800002009")
	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", openID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)
	*sent = (*sent)[:0] // 清掉开通那一条

	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13800002009", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	empToken := body["token"].(string)

	// 员工提交资料修改（进 pending）
	_, body = c.do("PUT", "/api/employee/me", map[string]interface{}{"title": "销售总监"}, empToken)
	require.EqualValues(t, 0, body["code"])

	// 找到这条修改申请并审批通过
	_, body = c.do("GET", "/api/admin/apply/modify?status=pending", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	var modID int64
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		if int64(m["staffId"].(float64)) == staffID {
			modID = int64(m["id"].(float64))
		}
	}
	require.NotZero(t, modID, "应能找到刚提交的修改申请")

	_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/modify/%d/approve", modID), nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	require.Len(t, *sent, 1)
	m := (*sent)[0].msg
	require.Equal(t, "o-mod-1", m.ToUser, "接收者是员工本人的 openid")
	require.Equal(t, "名片资料修改", m.Data["thing1"])
	require.Equal(t, "审核通过", m.Data["phrase2"])
	require.Equal(t, "pages/employee/me/me", m.Page)
}

// clipRunes 按字符截断：微信的 thing ≤20 字符、phrase ≤5 字符都按字符算。
// 按字节切会把汉字劈成半个，微信收到非法 UTF-8 回 47003，
// 现象是「明明配好了却发不出去」。
func TestClipRunes(t *testing.T) {
	require.Equal(t, "短", clipRunes("短", 20))
	require.Equal(t, "一二三", clipRunes("一二三四五", 3))
	require.Equal(t, "", clipRunes("   ", 20))
	// 20 个汉字正好不被截断
	long := "一二三四五六七八九十一二三四五六七八九十"
	require.Equal(t, long, clipRunes(long, 20))
	require.Equal(t, 20, len([]rune(clipRunes(long+"多", 20))))
}

// TestSubscribeConfigPerCompany 走 HTTP 验证：配置接口按 companyId 返回对应模板。
// 取值链与 wxAppOf 一致：
//   - 企业独立版（own AppID+AppSecret 且配了模板）→ 自家模板；
//   - 不传 companyId → 回落全局 sys_setting.wx_tmpl_audit；
//   - 企业独立版但没配模板 → 视为关闭（不回落平台模板，AppID 不同必失败），回空串；
//   - 凭据不完整（只有 AppID 没 AppSecret，与 wxAppOf 同口径算「走全局 token」）→ 回落全局。
func TestSubscribeConfigPerCompany(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	useSetting(t, "wx_tmpl_audit", "TPL-GLOBAL")

	// 给 2 号企业配独立凭据 + 订阅模板
	_, body := c.do("PUT", "/api/admin/companies/2/wechat", map[string]interface{}{
		"appId": "wxent222", "appSecret": "secret222",
		"subscribeTmplId": "TPL-ENT-2", "subscribeTmplFields": "thing9,thing8,thing7",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 查配置带 companyId=2 → 自家模板
	_, body = c.do("GET", "/api/subscribe/config?companyId=2", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "TPL-ENT-2", body["data"].(map[string]interface{})["auditTemplateId"])

	// 不传 companyId → 回落全局
	_, body = c.do("GET", "/api/subscribe/config", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "TPL-GLOBAL", body["data"].(map[string]interface{})["auditTemplateId"])

	// 配了独立凭据但没配模板的企业（3 号）→ 视为关闭，配置接口回空串
	_, body = c.do("PUT", "/api/admin/companies/3/wechat", map[string]interface{}{
		"appId": "wxent333", "appSecret": "secret333",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/subscribe/config?companyId=3", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "", body["data"].(map[string]interface{})["auditTemplateId"])

	// 凭据不完整（只有 AppID 没有 AppSecret）→ 与 wxAppOf 同口径走全局 token，
	// 模板也该走全局而不是用企业自己那个（那会因 AppID 不匹配而失败）
	_, body = c.do("PUT", "/api/admin/companies/4/wechat", map[string]interface{}{
		"appId": "wxent444", "subscribeTmplId": "TPL-SHOULD-NOT-USE",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/subscribe/config?companyId=4", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "TPL-GLOBAL", body["data"].(map[string]interface{})["auditTemplateId"],
		"凭据不完整时回落全局模板")
}
