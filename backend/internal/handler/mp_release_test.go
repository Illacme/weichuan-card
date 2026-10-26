package handler

// 小程序版本发布接口的测试。
//
// 微信四个接口（提审 / 查状态 / 发布 / 撤回）全部用间接层注入假实现——
// 与 getWXACode 同一套路，理由也一样：真调微信要外网 + 真凭据，
// 而这里要断言的是「状态机守住没有」「微信失败时本地状态回滚没有」。
//
// 覆盖重点（按坑的深浅排序）：
//  1. 完整链路：draft → auditing → audit_passed → released，每步的 auditid / 时间戳落库；
//  2. 状态机白名单：重复提审、未审过就发布、终态再动，一律拒绝；
//  3. 微信调用失败**不留下半截状态**（提审失败还得是 draft，能重试）；
//  4. 租户开关：企业关掉代发后，登记 / 提审 / 发布 / 撤回 / 轮询全部 403；
//  5. AppID 一致性：企业换过小程序后拒绝操作，不把审核打到别的小程序上；
//  6. 撤回要二次确认（confirm=true），因为撤回不返还审核额度。

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mpReleaseEnv 一套发布测试环境：假微信 + 引擎 + 已登录超管。
type mpReleaseEnv struct {
	db          *gorm.DB
	c           *testClient
	token       string
	cfg         config.Config
	auditStatus wechat.AuditStatus // 假微信的 get_auditstatus 返回值
	releaseErr  error              // 假微信 release / undo 的返回值
	release     struct {
		calls   []string // 依次记录被调用的假微信动作
		gotCfg  wechat.Config
		auditID int64
	}
}

func newMPReleaseEnv(t *testing.T, companyID int64) *mpReleaseEnv {
	return newMPReleaseEnvWith(t, companyID, "wx-global-appid", "wx-global-secret")
}

// newMPReleaseEnvWith 同 newMPReleaseEnv，但平台全局凭据可指定（传空串 = 没配，
// 用于验证「没凭据时早失败」）。
func newMPReleaseEnvWith(t *testing.T, companyID int64, appID, appSecret string) *mpReleaseEnv {
	t.Helper()
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.Company{ID: companyID, Name: "乙公司", ShortName: "乙", Status: "enabled"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-mprel-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	env := &mpReleaseEnv{db: db, auditStatus: wechat.AuditStatus{Status: 2}}
	env.cfg = config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
		// 平台全局凭据：企业没配自己的时用它
		WXAppID:     appID,
		WXAppSecret: appSecret,
	}
	r := buildEngine(t, db, env.cfg, fakeUploader{}, &fakeRefresher{})
	env.c = newTestClient(r, t)
	env.token = loginAdmin(t, env.c)

	// 假微信：记录调用顺序与传给它的凭据，返回可断言的固定值
	origSubmit, origStatus, origRelease, origUndo := wxSubmitAudit, wxGetAuditStatus, wxRelease, wxUndoCodeAudit
	t.Cleanup(func() {
		wxSubmitAudit, wxGetAuditStatus, wxRelease, wxUndoCodeAudit = origSubmit, origStatus, origRelease, origUndo
	})
	wxSubmitAudit = func(wc wechat.Config, items []map[string]any) (int64, error) {
		env.release.calls = append(env.release.calls, "submit")
		env.release.gotCfg = wc
		require.Nil(t, items, "MVP 不传 item_list，类目取后台已配的")
		if env.releaseErr != nil {
			return 0, env.releaseErr
		}
		if env.release.auditID == 0 {
			return 0, fmt.Errorf("假微信：未预设 auditid")
		}
		return env.release.auditID, nil
	}
	wxGetAuditStatus = func(wc wechat.Config, id int64) (wechat.AuditStatus, error) {
		env.release.calls = append(env.release.calls, "status")
		env.release.gotCfg = wc
		return env.auditStatus, nil
	}
	wxRelease = func(wc wechat.Config) error {
		env.release.calls = append(env.release.calls, "release")
		env.release.gotCfg = wc
		return env.releaseErr
	}
	wxUndoCodeAudit = func(wc wechat.Config) error {
		env.release.calls = append(env.release.calls, "undo")
		env.release.gotCfg = wc
		return env.releaseErr
	}

	// 凭据缓存是进程级全局 map（key = companyID），跨用例会串扰
	t.Cleanup(func() {
		wxAppMu.Lock()
		delete(wxAppCache, companyID)
		wxAppMu.Unlock()
	})
	return env
}

// create 登记一个版本，返回记录 id。
func (e *mpReleaseEnv) create(t *testing.T, companyID int64, version string) int64 {
	t.Helper()
	_, body := e.c.do("POST", fmt.Sprintf("/api/admin/companies/%d/releases", companyID),
		map[string]interface{}{"userVersion": version, "userDesc": "测试版本 " + version}, e.token)
	require.EqualValues(t, 0, body["code"], "登记响应：%v", body)
	return int64(body["data"].(map[string]interface{})["id"].(float64))
}

// dataOf 取回包 data。
func dataOf(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	require.NotNil(t, body["data"], "响应：%v", body)
	return body["data"].(map[string]interface{})
}

// TestMPReleaseFullFlow 正常链路一条龙：登记 → 提审 → 轮询审过 → 发布，
// 以及「每一步的非法操作都被状态机挡住」。
func TestMPReleaseFullFlow(t *testing.T) {
	e := newMPReleaseEnv(t, 2)

	// 1) 登记 → draft，凭据用全局那份（企业没配自己的），appid 固化进记录
	id := e.create(t, 2, "1.2.0")
	_, body := e.c.do("GET", "/api/admin/companies/2/releases", nil, e.token)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["total"])
	row := body["data"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "draft", row["status"])
	require.Equal(t, "待提审", row["statusName"])
	require.Equal(t, "wx-global-appid", row["appId"], "登记时应把当时用的小程序固化进记录")
	require.Equal(t, "1.2.0", row["userVersion"])

	// 2) 同一企业同一版本号重复登记 → 拒绝（否则「线上是哪一版」会有两条记录）
	_, body = e.c.do("POST", "/api/admin/companies/2/releases",
		map[string]interface{}{"userVersion": "1.2.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "已登记过")

	// 3) 没审过就发布 → 拒绝，且假微信一次都不该被调
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不能发布")
	require.NotContains(t, e.release.calls, "release")

	// 4) 提审 → auditing，auditid 落库
	e.release.auditID = 777888
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "提审响应：%v", body)
	d := dataOf(t, body)
	require.Equal(t, "auditing", d["status"])
	require.EqualValues(t, 777888, d["auditId"])
	require.Equal(t, "wx-global-appid", e.release.gotCfg.AppID, "提审必须用该企业的小程序凭据")
	require.NotNil(t, d["submittedAt"])

	// 5) 重复提审 → 状态机拒绝（微信不会收到第二次调用）
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不能提交审核")
	require.Equal(t, []string{"submit"}, e.release.calls)

	// 6) 撤回不带 confirm → 拒绝（撤回不返还额度，二次确认是硬要求）
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/undo-audit", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "confirm")

	// 7) 轮询：仍是审核中 → 状态不动、不落库
	e.auditStatus = wechat.AuditStatus{Status: 2}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "轮询响应：%v", body)
	d = dataOf(t, body)
	require.Equal(t, "auditing", d["status"])
	require.Equal(t, false, d["changed"])
	require.EqualValues(t, 2, d["wxStatus"])

	// 8) 轮询：审核通过 → audit_passed（终局才落库）
	e.auditStatus = wechat.AuditStatus{Status: 0}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	d = dataOf(t, body)
	require.Equal(t, "audit_passed", d["status"])
	require.Equal(t, "审过待发布", d["statusName"])
	require.Equal(t, true, d["changed"])

	// 9) 发布 → released（终态）
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "发布响应：%v", body)
	d = dataOf(t, body)
	require.Equal(t, "released", d["status"])
	require.NotNil(t, d["releasedAt"])

	// 10) 终态不能再动：再发布、再轮询都不改状态、不再多调微信
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	d = dataOf(t, body)
	require.Equal(t, "released", d["status"])
	require.Equal(t, false, d["changed"])
	require.Equal(t, []string{"submit", "status", "status", "release", "status"}, e.release.calls,
		"微信调用顺序应与操作一一对应，终态后不得再多一次 release")
}

// 驳回 → 改完重新提审：reason 要落库（界面直接展示），重新提审后要清掉——
// 旧原因留着会让人以为新一轮也被同一个问题拒了。
// 顺带覆盖「在微信侧撤回」的自愈：轮询发现 status=3 时本地跟着变 withdrawn。
func TestMPReleaseRejectAndResubmit(t *testing.T) {
	e := newMPReleaseEnv(t, 2)
	id := e.create(t, 2, "2.0.0")
	e.release.auditID = 1001

	_, body := e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)

	// 驳回：原因 + 截图 id 一起回来
	e.auditStatus = wechat.AuditStatus{Status: 1, Reason: "首页按钮点击无响应", Screenshot: "media-1|media-2"}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	d := dataOf(t, body)
	require.Equal(t, "rejected", d["status"])
	require.Equal(t, "已驳回", d["statusName"])
	require.Equal(t, "首页按钮点击无响应", d["auditReason"], "驳回原因必须落库，否则界面上只能干瞪眼")

	// 驳回后不能直接发布
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])

	// 重新提审：新 auditid，旧原因清空
	e.release.auditID = 1002
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	d = dataOf(t, body)
	require.Equal(t, "auditing", d["status"])
	require.EqualValues(t, 1002, d["auditId"])
	require.Equal(t, "", d["auditReason"], "重新提审应清掉上一轮的驳回原因")

	// 在微信公众平台侧手动撤回 → 轮询自愈为 withdrawn
	e.auditStatus = wechat.AuditStatus{Status: 3}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	d = dataOf(t, body)
	require.Equal(t, "withdrawn", d["status"])
	require.Equal(t, true, d["changed"])

	// 撤回是终局性的：只能重新提审，不能再撤回一次
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/undo-audit", id),
		map[string]interface{}{"confirm": true}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不能撤回")
}

// 微信调用失败时本地状态必须回滚：提审失败还是 draft（能重试），
// 发布失败还是 audit_passed。做不到这一点，一次网络抖动就会留下
// 「本地说在审、微信根本没收到」的半截状态，之后每一步都错。
func TestMPReleaseWeChatFailureRollsBack(t *testing.T) {
	e := newMPReleaseEnv(t, 2)
	id := e.create(t, 2, "3.0.0")
	e.release.auditID = 2001

	// 提审失败 → 仍是 draft，auditid 不落库
	e.releaseErr = fmt.Errorf("假微信：提审被拒（errcode 86001）")
	_, body := e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "提审被拒")
	d := mustGet(t, e, id)
	require.Equal(t, "draft", d["status"])
	require.EqualValues(t, 0, d["auditId"])

	// 微信恢复 → 提审成功
	e.releaseErr = nil
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)

	// 撤回失败 → 仍是 auditing（没有白扣额度也没有假撤回）
	e.releaseErr = fmt.Errorf("假微信：撤回失败")
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/undo-audit", id),
		map[string]interface{}{"confirm": true}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Equal(t, "auditing", mustGet(t, e, id)["status"])

	// 撤回成功 → withdrawn
	e.releaseErr = nil
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/undo-audit", id),
		map[string]interface{}{"confirm": true}, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	require.Equal(t, "withdrawn", mustGet(t, e, id)["status"])

	// 重新提审 → 审过 → 发布失败 → 仍是 audit_passed，可以重点发布
	e.release.auditID = 2002
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	e.auditStatus = wechat.AuditStatus{Status: 0}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	e.releaseErr = fmt.Errorf("假微信：发布失败")
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Equal(t, "audit_passed", mustGet(t, e, id)["status"])

	e.releaseErr = nil
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	require.Equal(t, "released", mustGet(t, e, id)["status"])
}

// mustGet 直接取单条记录的最新状态（绕开列表，避免分页干扰）。
func mustGet(t *testing.T, e *mpReleaseEnv, id int64) map[string]interface{} {
	t.Helper()
	var row model.MPRelease
	require.NoError(t, e.db.First(&row, id).Error)
	return mpReleaseJSON(&row)
}

// 租户开关：企业把「允许平台代发」关掉后，登记 / 提审 / 轮询 / 撤回 / 发布
// 一律 403——企业独立版的小程序归企业自己管，平台不该有单方面推上线的能力。
// 开关本身还要验证「只改开关不动凭据」的保存语义（布尔字段用指针区分
// 「没传」与「传了 false」，否则一次普通保存就会把企业权限关掉）。
func TestMPReleaseTenantSwitch(t *testing.T) {
	e := newMPReleaseEnv(t, 2)

	// 配租户凭据（开关缺省 true）
	_, body := e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "secret-b"}, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	_, body = e.c.do("GET", "/api/admin/companies/2/wechat", nil, e.token)
	require.Equal(t, true, dataOf(t, body)["releaseEnabled"], "新建凭据时开关应缺省允许")

	// 用租户自己的凭据登记 + 提审。两条记录：一条审过（测发布），
	// 一条在审（测撤回/轮询）——关掉开关后要覆盖的动作所处状态不同。
	idPassed := e.create(t, 2, "4.0.0")
	require.Equal(t, "wx-tenant-b", mustGet(t, e, idPassed)["appId"])
	e.release.auditID = 3001
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", idPassed), nil, e.token)
	require.EqualValues(t, 0, body["code"], "%v", body)
	require.Equal(t, "wx-tenant-b", e.release.gotCfg.AppID, "提审必须用租户自己的小程序")
	e.auditStatus = wechat.AuditStatus{Status: 0}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", idPassed), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "audit_passed", mustGet(t, e, idPassed)["status"])

	idAuditing := e.create(t, 2, "4.1.0")
	e.release.auditID = 3002
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", idAuditing), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "auditing", mustGet(t, e, idAuditing)["status"])

	// 关掉代发（凭据原样带回，secret 也重填——只看不改会清 secret 是既有约定）
	_, body = e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "secret-b", "releaseEnabled": false}, e.token)
	require.EqualValues(t, 0, body["code"])
	_, body = e.c.do("GET", "/api/admin/companies/2/wechat", nil, e.token)
	require.Equal(t, false, dataOf(t, body)["releaseEnabled"])

	// 之后一切代发动作 403（注意：状态机允许的动作才会走到开关校验，
	// 所以发布用审过那条、撤回用在审那条）
	_, body = e.c.do("POST", "/api/admin/companies/2/releases",
		map[string]interface{}{"userVersion": "4.2.0"}, e.token)
	require.EqualValues(t, 403, body["code"], "登记：%v", body)
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", idAuditing), nil, e.token)
	require.EqualValues(t, 403, body["code"], "轮询：%v", body)
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/undo-audit", idAuditing),
		map[string]interface{}{"confirm": true}, e.token)
	require.EqualValues(t, 403, body["code"], "撤回：%v", body)
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", idPassed), nil, e.token)
	require.EqualValues(t, 403, body["code"], "发布：%v", body)
	// 列表是只读的，不受开关影响（企业要能看见历史）
	_, body = e.c.do("GET", "/api/admin/companies/2/releases", nil, e.token)
	require.EqualValues(t, 0, body["code"])

	// 开关只改开关：再保存一次凭据但不带 releaseEnabled 字段，值不能被重置
	_, body = e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "secret-b"}, e.token)
	require.EqualValues(t, 0, body["code"])
	_, body = e.c.do("GET", "/api/admin/companies/2/wechat", nil, e.token)
	require.Equal(t, false, dataOf(t, body)["releaseEnabled"], "没传 releaseEnabled 时应保持原值")

	// 重新打开 → 恢复
	_, body = e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "secret-b", "releaseEnabled": true}, e.token)
	require.EqualValues(t, 0, body["code"])
	e.auditStatus = wechat.AuditStatus{Status: 2}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", idAuditing), nil, e.token)
	require.EqualValues(t, 0, body["code"], "重开后轮询应恢复：%v", body)
	require.Equal(t, "auditing", mustGet(t, e, idAuditing)["status"])
	// 重开后发布也恢复
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", idPassed), nil, e.token)
	require.EqualValues(t, 0, body["code"], "重开后发布应恢复：%v", body)
	require.Equal(t, "released", mustGet(t, e, idPassed)["status"])
}

// AppID 一致性：登记后企业换了小程序（凭据改成另一个 appid），一切动作拒绝。
// 放行的话，提审/发布会打到新小程序上——而代码是 CI 传到旧小程序的，
// auditid 也属于旧的。微信侧只会回一句莫名其妙的错，极难查。
func TestMPReleaseAppIDMismatch(t *testing.T) {
	e := newMPReleaseEnv(t, 2)
	_, body := e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-a", "appSecret": "secret-a"}, e.token)
	require.EqualValues(t, 0, body["code"])

	id := e.create(t, 2, "5.0.0")
	require.Equal(t, "wx-tenant-a", mustGet(t, e, id)["appId"])

	// 企业换成另一个小程序
	_, body = e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "secret-b"}, e.token)
	require.EqualValues(t, 0, body["code"])

	// 提审被拒，原因里两个 appid 都要出现（否则排查时还是不知道谁和谁不一致）
	e.release.auditID = 5001
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不一致")
	require.Contains(t, body["msg"], "wx-tenant-a")
	require.Contains(t, body["msg"], "wx-tenant-b")
	require.Empty(t, e.release.calls, "凭据不一致时一次微信都不该调")
	require.Equal(t, "draft", mustGet(t, e, id)["status"], "状态不应被改动")
}

// 参数与存在性校验 + 鉴权：这些是「最早出错」的地方，错误信息必须指向
// 具体的下一步动作（补凭据 / 先提审 / 刷新重试），而不是干巴巴的「参数错误」。
func TestMPReleaseGuards(t *testing.T) {
	e := newMPReleaseEnv(t, 2)

	// 版本号形态
	for _, bad := range []string{"", "   ", "中文版本", "a b", "1.0 版", strings.Repeat("9", 65)} {
		_, body := e.c.do("POST", "/api/admin/companies/2/releases",
			map[string]interface{}{"userVersion": bad}, e.token)
		require.EqualValues(t, 1, body["code"], "版本号 %q 应被拒", bad)
	}

	// 不存在的企业 / 不存在的记录
	_, body := e.c.do("POST", "/api/admin/companies/999/releases",
		map[string]interface{}{"userVersion": "1.0.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "企业不存在")
	_, body = e.c.do("POST", "/api/admin/releases/999/submit-audit", nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不存在")

	// 没提审就查审核状态
	id := e.create(t, 2, "6.0.0")
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "尚未提审")

	// 未登录一律进不来
	_, body = e.c.do("GET", "/api/admin/companies/2/releases", nil, "")
	require.NotEqualValues(t, 0, body["code"])
	_, body = e.c.do("POST", "/api/admin/companies/2/releases",
		map[string]interface{}{"userVersion": "6.0.1"}, "")
	require.NotEqualValues(t, 0, body["code"])
}

// 没配任何小程序凭据时，登记阶段就明确失败——不然后面每一步都会以
// 更奇怪的方式失败（微信报 40164 之类），而运营看不懂。
func TestMPReleaseNoCredentials(t *testing.T) {
	e := newMPReleaseEnvWith(t, 2, "", "")
	_, body := e.c.do("POST", "/api/admin/companies/2/releases",
		map[string]interface{}{"userVersion": "7.0.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "未配置")
	// 库里不应留下记录
	require.EqualValues(t, 0, countRows(t, e.db, &model.MPRelease{}))
}

// 审计留痕：代发链上每个动作（含失败的尝试）都要能回答
// 「谁、在什么时候、对哪个小程序、做了什么」。
func TestMPReleaseAuditTrail(t *testing.T) {
	e := newMPReleaseEnv(t, 2)
	id := e.create(t, 2, "8.0.0")
	e.release.auditID = 8001
	_, body := e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	e.auditStatus = wechat.AuditStatus{Status: 0}
	_, body = e.c.do("GET", fmt.Sprintf("/api/admin/releases/%d/audit-status", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/release", id), nil, e.token)
	require.EqualValues(t, 0, body["code"])

	var logs []model.AuditLog
	require.NoError(t, e.db.Where("action LIKE ?", "mp_release.%").Order("id asc").Find(&logs).Error)
	actions := make([]string, 0, len(logs))
	for _, l := range logs {
		actions = append(actions, l.Action)
		require.Equal(t, "mp_release", l.TargetType)
		require.Equal(t, fmt.Sprintf("%d", id), l.TargetID)
		require.Equal(t, "success", l.Result)
		require.NotEmpty(t, l.ActorName, "审计必须能对应到人")
	}
	require.Equal(t, []string{
		"mp_release.create", "mp_release.submit_audit", "mp_release.audit_status", "mp_release.release",
	}, actions)

	// 失败的尝试同样留痕（被状态机拒掉的提交审核）
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 1, body["code"])
	var fail model.AuditLog
	require.NoError(t, e.db.Where("action = ? AND result = ?", "mp_release.submit_audit", "fail").
		Order("id desc").First(&fail).Error)
	require.Contains(t, fail.Detail, "8.0.0")
}

// countRows 数表行数（只读断言用）。
func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(model).Count(&n).Error)
	return n
}
