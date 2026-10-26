package license

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTenantDB 起一个临时 SQLite 库并建 tenant_license 表。
func newTenantDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open("sqlite://" + filepath.Join(t.TempDir(), "tenant.db"))
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TenantLicense{}))
	return db
}

func TestTenantDBResolverFound(t *testing.T) {
	db := newTenantDB(t)
	require.NoError(t, db.Create(&model.TenantLicense{
		CompanyID: 5, Edition: "platform", Seats: 42,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Features:  "mp_release, content_sec",
	}).Error)

	e, err := NewTenantDBResolver(db).Resolve(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, int64(5), e.TenantID)
	require.Equal(t, "platform", e.Edition)
	require.Equal(t, 42, e.Seats)
	require.False(t, e.IsExpired())
	require.Equal(t, []string{"mp_release", "content_sec"}, e.Features)
	require.True(t, e.HasFeature("mp_release"))
	require.False(t, e.HasFeature("unknown"))
}

func TestTenantDBResolverNotFound(t *testing.T) {
	db := newTenantDB(t)

	// 租户 9 没有授权行 → ErrNotFound（由 ResolveFor 决定回退，不在这里编宽松授权）
	_, err := NewTenantDBResolver(db).Resolve(context.Background(), 9)
	require.ErrorIs(t, err, ErrNotFound)

	// tenantID <= 0（平台超管未指定企业）同样视为查不到
	_, err = NewTenantDBResolver(db).Resolve(context.Background(), 0)
	require.ErrorIs(t, err, ErrNotFound)

	// 库连接为空
	_, err = NewTenantDBResolver(nil).Resolve(context.Background(), 5)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestResolveForFallbackToGlobal 没给租户单独配时，必须回退全局设置——
// 否则新增一张空表会让存量部署「突然开始按 0 席拦截」，属于上线即事故。
func TestResolveForFallbackToGlobal(t *testing.T) {
	restore := TenantDB()
	defer SetTenantDB(restore)
	defer SetCurrent(nil)

	global := &Entitlement{Seats: 7, Features: []string{"mp_release"}}
	SetCurrent(global)

	SetTenantDB(newTenantDB(t))
	// 租户 9 无行 → 回退全局
	require.Same(t, global, ResolveFor(context.Background(), 9))

	// 未注入库连接 → 同样回退全局（测试 / 未接线部署）
	SetTenantDB(nil)
	require.Same(t, global, ResolveFor(context.Background(), 5))
}

// TestResolveForPerTenant 同一进程内不同租户拿到各自的授权。
func TestResolveForPerTenant(t *testing.T) {
	restore := TenantDB()
	defer SetTenantDB(restore)
	defer SetCurrent(nil)

	global := &Entitlement{Seats: 7}
	SetCurrent(global)

	db := newTenantDB(t)
	require.NoError(t, db.Create(&model.TenantLicense{CompanyID: 1, Seats: 3, Features: "mp_release"}).Error)
	require.NoError(t, db.Create(&model.TenantLicense{CompanyID: 2, Seats: 100}).Error)
	SetTenantDB(db)

	a := ResolveFor(context.Background(), 1)
	require.Equal(t, 3, a.Seats)
	require.True(t, a.HasFeature("mp_release"))

	b := ResolveFor(context.Background(), 2)
	require.Equal(t, 100, b.Seats)
	require.True(t, b.HasFeature("anything")) // 未声明 Features = 全部开通
}

func TestSplitFeatures(t *testing.T) {
	require.Nil(t, splitFeatures(""))
	require.Nil(t, splitFeatures("  ,  "))
	require.Equal(t, []string{"a"}, splitFeatures("a"))
	require.Equal(t, []string{"a", "b", "c"}, splitFeatures("a, b ,c"))
}
