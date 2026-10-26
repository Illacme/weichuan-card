package handler

// 本文件守两条「不补就等于没做」的商用就绪缺口：
//
//  1. A1-b：匿名上传端点（/api/apply/open/avatar）原先无鉴权、无限流，
//     等于把对象存储开成免费网盘，也能批量把违规图刷到公开 URL
//     （匿名上传拿不到 openid 时送不了内容安全检测）。现补按 IP 的固定窗口限流。
//  2. B1-a：席位上限原先只卡「管理员直接新增员工」一条路径，
//     「审批开通申请」这条同样会建号却不被卡——席位上限形同虚设。
//
// 两条都用**完整链路**测（走 buildEngine 的真实路由与鉴权），
// 因为这两处的漏洞都在「哪个路径没被卡」上——只测中间件单元函数
// 恰恰会漏掉「另一条建号路径」这种问题。

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"weichuan/internal/license"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---- A1-b：匿名上传限流 ----

// postAnonymousAvatar 发一次匿名头像上传（multipart，无鉴权）。
// 返回 HTTP 状态码与解析后的响应体。
func postAnonymousAvatar(t *testing.T, r *gin.Engine, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "a.png")
	require.NoError(t, err)
	// 最小 PNG 头（魔数嗅探用）
	_, err = part.Write([]byte("\x89PNG\r\n\x1a\n"))
	require.NoError(t, err)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/apply/open/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestAnonymousUploadRateLimited 匿名上传端点必须限流：
// 超过阈值后返回 code=429。
//
// 这是 A1-b 的核心断言：公开无鉴权端点不限流 = 存储当免费网盘用。
func TestAnonymousUploadRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := setupEngineWithDB(t)

	// 把阈值压到 2：便于在测试里跑出超限
	orig := uploadLimitFn
	uploadLimitFn = func() int { return 2 }
	defer func() { uploadLimitFn = orig }()

	// 前两次应放行（真的写进去了）
	for i := 1; i <= 2; i++ {
		code, body := postAnonymousAvatar(t, r, nil)
		require.Equal(t, http.StatusOK, code)
		require.EqualValues(t, 0, body["code"], "第 %d 次上传应在限流内放行", i)
	}

	// 第 3 次必须被限流
	code, body := postAnonymousAvatar(t, r, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 429, body["code"], "第 3 次必须被限流（阈值 2）")
}

// TestAnonymousUploadRateLimitNotAffectOtherEndpoints 匿名上传的限流
// 不能误伤其它接口：桶键带 upload: 前缀，与登录/刷新/埋点各自独立。
//
// 若裸 IP 当键（去掉前缀），用上传额度就能把同 IP 的登录打成 429——
// 办公网 NAT 共享出口 IP 时完全可达。
func TestAnonymousUploadRateLimitNotAffectOtherEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := setupEngineWithDB(t)
	c := newTestClient(r, t)

	orig := uploadLimitFn
	uploadLimitFn = func() int { return 2 }
	defer func() { uploadLimitFn = orig }()

	// 刷满匿名上传额度
	for i := 0; i < 3; i++ {
		postAnonymousAvatar(t, r, nil)
	}

	// 登录（另一个限流器）必须照常可用
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"], "上传限流不得牵连登录限流（桶键须带动作前缀）")
}

// TestAnonymousUploadAcceptsOpenIDForMediaCheck 匿名上传请求带上 openid 时，
// 服务端能取到它（图片内容安全 v2 的必填项）。
//
// 这条链路修的是「违规图在人工审核之前就已经挂在公开 URL 上」：
// 图上传成功即返回永久 URL，不送检就等于先公开、后审核。
func TestAnonymousUploadAcceptsOpenIDForMediaCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := setupEngineWithDB(t)

	// 带上 openid 的上传应正常成功（openid 只是送检用，不影响上传本身）
	code, body := postAnonymousAvatar(t, r, map[string]string{"openid": "ox-abc123"})
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])

	// formValue 本身的行为：取得到原值、取不到返回空串。
	//
	// 两个必踩的坑：
	//  1. 必须设 Content-Type: multipart/form-data（带 boundary）——gin 的
	//     MultipartForm() 靠这个头判断怎么解析，没设就直接报错返回 nil。
	//  2. 每个断言要用**独立的 body**：httptest.NewRequest 会消费掉 reader，
	//     同一个 buffer 读两次只会拿到空。
	mkReq := func(field, val string) *http.Request {
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		require.NoError(t, m.WriteField(field, val))
		require.NoError(t, m.Close())
		req := httptest.NewRequest("POST", "/x", &b)
		req.Header.Set("Content-Type", m.FormDataContentType())
		return req
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = mkReq("openid", "ox-xyz")
	require.Equal(t, "ox-xyz", formValue(c, "openid"))

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = mkReq("other", "v")
	require.Equal(t, "", formValue(c2, "openid"), "取不到的字段必须返回空串，不能报错也不能 panic")
}

// TestAnonymousUploadWithOpenIDSubmitsMediaCheck 端到端：匿名上传带上 openid 时，
// 图片**真的进入内容安全送检**并落一条 pending 记录（source=apply）。
//
// 这条链路修的是「违规图在人工审核之前就已经挂在公开 URL 上」：
// 图上传成功即返回永久 URL（立刻公开可访问），而它最终会写进员工头像。
// 改造前匿名端点因为没有 openid 直接跳过送检，等于这条路上完全无检测。
func TestAnonymousUploadWithOpenIDSubmitsMediaCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := stubMediaCheck(t, "trace-apply-1", nil)
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	configureMediaCheck(t, c, adminToken)

	// 带 openid 上传（客户端用 wx.login 静默换取后随上传一起带上）
	code, body := postAnonymousAvatar(t, r, map[string]string{"openid": "openid-apply"})
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])

	var row model.MediaCheck
	require.Eventually(t, func() bool {
		return db.First(&row).Error == nil
	}, 3*time.Second, 20*time.Millisecond, "匿名上传带 openid 时应落一条检测记录")

	require.EqualValues(t, 1, atomic.LoadInt32(calls), "应真的发起送检")
	require.Equal(t, "trace-apply-1", row.TraceID)
	require.Equal(t, model.MediaCheckPending, row.Status)
	require.Equal(t, "apply", row.Source, "来源应记为 apply（匿名申请），便于后台区分")
	require.Equal(t, "openid-apply", row.OpenID)
	require.NotEmpty(t, row.ObjectKey, "对象键必须在送检时就落下来，下架时才有依据")
}

// TestAnonymousUploadWithoutOpenIDStillUploads 拿不到 openid 时上传本身仍要成功
// （老客户端 / 网络异常），只是不送检——绝不能因为送检做不了就把上传也堵死。
func TestAnonymousUploadWithoutOpenIDStillUploads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := stubMediaCheck(t, "trace-apply-2", nil)
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	configureMediaCheck(t, c, adminToken)

	code, body := postAnonymousAvatar(t, r, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"], "没有 openid 也要能上传")

	// 不送检（没有 openid，v2 接口用不了），但也不能因此报错
	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 0, atomic.LoadInt32(calls))
	var n int64
	require.NoError(t, db.Model(&model.MediaCheck{}).Count(&n).Error)
	require.EqualValues(t, 0, n, "没有 openid 时不落检测记录（人工审核兜底）")
}

// ---- B1-a：席位上限覆盖审批路径 ----

// seedOpenApply 建一条待审的开通申请（租户 1）。
func seedOpenApply(t *testing.T, db *gorm.DB, name, phone string) {
	t.Helper()
	ap := model.OpenApply{CompanyID: 1, Name: name, Phone: phone, Status: "pending"}
	require.NoError(t, db.Create(&ap).Error)
}

// TestSeatCapAppliesToApprovalPath 审批开通申请（POST /api/admin/apply/open/:id/approve）
// 同样会建员工，必须受席位上限约束。
//
// 改造前这条路径不查席位：管理员可以反复通过申请无限建号，
// 客户超额使用却不付费——席位上限形同虚设。
func TestSeatCapAppliesToApprovalPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	// 租户 1 已有 2 名员工，授权席位上限 2 → 已达上限
	require.NoError(t, db.Create(&model.Employee{CompanyID: 1, Name: "a", Phone: "13900000001"}).Error)
	require.NoError(t, db.Create(&model.Employee{CompanyID: 1, Name: "b", Phone: "13900000002"}).Error)

	// 一条待审申请
	seedOpenApply(t, db, "c", "13900000003")

	license.SetCurrent(&license.Entitlement{Seats: 2})
	defer license.SetCurrent(nil)

	tok := loginAdmin(t, c)
	_, body := c.do("POST", "/api/admin/apply/open/1/approve", nil, tok)
	require.EqualValues(t, 403, body["code"], "席位已满时审批建号必须被拒")

	// 确认没有真的建出员工（不能只是返回了 403 却把号建了）
	var n int64
	require.NoError(t, db.Model(&model.Employee{}).Where("company_id = ?", 1).Count(&n).Error)
	require.EqualValues(t, 2, n, "被拒的审批不得留下员工记录")
}

// TestSeatCapAllowsApprovalWhenUnderLimit 席位未满时审批仍要正常通过
// （防止把闸门修得过紧：申请流程被卡死是更严重的故障）。
func TestSeatCapAllowsApprovalWhenUnderLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	seedOpenApply(t, db, "c", "13900000003")

	license.SetCurrent(&license.Entitlement{Seats: 5})
	defer license.SetCurrent(nil)

	tok := loginAdmin(t, c)
	_, body := c.do("POST", "/api/admin/apply/open/1/approve", nil, tok)
	require.EqualValues(t, 0, body["code"], "席位未满时审批应正常通过")

	var n int64
	require.NoError(t, db.Model(&model.Employee{}).Count(&n).Error)
	require.EqualValues(t, 1, n, "审批通过应真的建出员工")
}
