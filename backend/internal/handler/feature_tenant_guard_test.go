package handler

// Feature 闸门按「记录自身的企业」判授权，而不是按请求的 query/默认租户。
//
// mp_release 是付费能力：可以给 A 企业开、B 企业不开。动作路由
// /api/admin/releases/:id/{submit-audit,release,undo-audit} 只有记录 id，
// 平台超管请求里没有 ?companyId=。旧的 RequireFeature 走 resolveTenantForLicense，
// 这种请求会回落到默认租户 1 —— 于是「5 号没买、1 号买了，照样能给 5 号发布」，
// 按租户收费直接失效。
//
// 本用例同时钉住三件事：
//  1. 记录属于 A（已购）→ 放行；属于 B（未购）→ 403；
//  2. 同一条 B 记录，旧口径（默认租户）会误放行——证明这个守门不是恒绿；
//  3. 查不到记录 → 403，绝不退化成默认租户放行。

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"weichuan/internal/license"
	"weichuan/internal/model"
)

func TestRequireFeatureForUsesRecordOwner(t *testing.T) {
	_, db := setupEngineWithDB(t)
	defer license.SetTenantDB(nil)
	defer license.SetCurrent(nil)

	const bought, notBought int64 = 5, 9
	recBought := &model.MPRelease{CompanyID: bought, UserVersion: "1.0", Status: "draft"}
	recNot := &model.MPRelease{CompanyID: notBought, UserVersion: "1.0", Status: "draft"}
	require.NoError(t, db.Create(recBought).Error)
	require.NoError(t, db.Create(recNot).Error)

	// 平台授权不含该 feature；5 号（已购）和默认租户 1 开了，9 号没开。
	// 给默认租户 1 开是有意为之的「诱饵」：旧口径对没有 ?companyId= 的超管请求
	// 一律按 1 号判，于是会给未购的 9 号也放行——这正是要钉住的漏洞。
	license.SetCurrent(&license.Entitlement{Features: []string{"content_sec"}})
	require.NoError(t, license.SetTenantLicense(db, bought, "trial", 10, nil, "mp_release"))
	require.NoError(t, license.SetTenantLicense(db, notBought, "trial", 10, nil, "content_sec"))
	require.NoError(t, license.SetTenantLicense(db, 1, "trial", 10, nil, "mp_release"))

	gin.SetMode(gin.TestMode)
	makeRouter := func(mw gin.HandlerFunc) *gin.Engine {
		r := gin.New()
		// 模拟平台超管身份：不挂鉴权中间件，直接注入与 PlatformAdminMiddleware
		// 相同的 context 标记，让 resolveTenantForLicense 走 platform 分支。
		r.Use(func(c *gin.Context) {
			c.Set("role", "admin")
			c.Set("companyId", int64(0))
			c.Next()
		})
		r.POST("/api/admin/releases/:id/release", mw, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
		})
		return r
	}
	newRouter := makeRouter(RequireFeatureFor("mp_release", mpReleaseRecordTenant(db)))
	w := httptest.NewRecorder()
	newRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/releases/"+strconv.FormatInt(recBought.ID, 10)+"/release", nil))
	require.Contains(t, w.Body.String(), `"code":0`, "已购企业的发布应放行：%s", w.Body.String())

	w = httptest.NewRecorder()
	newRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/releases/"+strconv.FormatInt(recNot.ID, 10)+"/release", nil))
	require.Contains(t, w.Body.String(), `"code":403`, "未购企业的发布必须被拒：%s", w.Body.String())

	// 记录不存在：判不出归属就拒绝，不能退回默认租户。
	w = httptest.NewRecorder()
	newRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/releases/999999/release", nil))
	require.Contains(t, w.Body.String(), `"code":403`, "查不到记录必须拒绝：%s", w.Body.String())

	// 反向验证证明旧守门确实会漏：同一条未购记录，用默认 RequireFeature
	// （平台超管无 query → 回落默认租户 1）会放行。若这里不红，说明用例没测到点子上。
	oldRouter := makeRouter(RequireFeature("mp_release"))
	w = httptest.NewRecorder()
	oldRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/releases/"+strconv.FormatInt(recNot.ID, 10)+"/release", nil))
	require.Contains(t, w.Body.String(), `"code":0`, "旧口径应误放行（证明新守门不是恒绿）：%s", w.Body.String())
}
