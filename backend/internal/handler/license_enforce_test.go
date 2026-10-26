package handler

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"weichuan/internal/dbopen"
	"weichuan/internal/license"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestEnforceLicenseExpiredDegradesToReadOnly 授权过期后，非白名单的写请求被拒（code=2，只读降级），
// 读请求与白名单（登录）仍放行。
func TestEnforceLicenseExpiredDegradesToReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	license.SetCurrent(&license.Entitlement{ExpiresAt: time.Now().Add(-time.Hour)})
	defer license.SetCurrent(nil)

	r := gin.New()
	r.Use(EnforceLicense(nil)) // 本测试不涉及席位分支
	r.GET("/api/foo", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.POST("/api/foo", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	// 白名单内的登录路由：中间件应放行到 handlers（返回 200，而非被拦截）
	r.POST("/api/auth/login", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/foo", nil))
	require.Equal(t, 200, w.Code)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/foo", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "\"code\":2")

	// 白名单内的写请求（管理员登录）仍放行，保证能登进来看只读状态
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/login", nil))
	require.Equal(t, 200, w.Code)
}

// TestEnforceLicenseSeatCap 席位上限按「当前租户（企业）」隔离统计：
// 企业 1 已达上限 → 拒（code=403）；企业 2 未达上限 → 放行（不受企业 1 影响）。
func TestEnforceLicenseSeatCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, _, err := dbopen.Open("sqlite://" + filepath.Join(t.TempDir(), "seat.db"))
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Employee{}))
	require.NoError(t, db.Create(&model.Employee{CompanyID: 1, Name: "a", Phone: "13900000001"}).Error)
	require.NoError(t, db.Create(&model.Employee{CompanyID: 1, Name: "b", Phone: "13900000002"}).Error)
	require.NoError(t, db.Create(&model.Employee{CompanyID: 2, Name: "c", Phone: "13900000003"}).Error)

	license.SetCurrent(&license.Entitlement{Seats: 2})
	defer license.SetCurrent(nil)

	// 注入 companyId（绕过鉴权中间件，聚焦席位分支；与 apply.go 创建员工时的 cid 解析路径一致）
	routerFor := func(cid int64) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("companyId", cid)
			c.Set("role", "admin")
			c.Next()
		})
		r.Use(EnforceLicense(db))
		r.POST("/api/admin/employees", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
		return r
	}

	// 企业 1 已有 2 席（达上限）→ 拒
	w := httptest.NewRecorder()
	routerFor(1).ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/employees", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "\"code\":403")

	// 企业 2 仅 1 席（未达上限）→ 放行（证明按租户隔离，不与企业 1 混算）
	w = httptest.NewRecorder()
	routerFor(2).ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/employees", nil))
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "\"code\":403")
}

// TestRequireFeature 功能开关拦截：授权未开通时被拒，开通或缺省（nil/空 Features）时放行。
func TestRequireFeature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer license.SetCurrent(nil)

	license.SetCurrent(&license.Entitlement{Features: []string{"content_sec"}})
	r := gin.New()
	r.Use(RequireFeature("mp_release"))
	r.POST("/api/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// 未开通 → 拒
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/x", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "\"code\":403")

	// 已开通 → 放行
	license.SetCurrent(&license.Entitlement{Features: []string{"mp_release"}})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/x", nil))
	require.Equal(t, 200, w.Code)

	// 未声明 Features（缺省）→ 全部开通
	license.SetCurrent(&license.Entitlement{})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/x", nil))
	require.Equal(t, 200, w.Code)
}
