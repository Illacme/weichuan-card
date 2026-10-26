package license

import (
	"path/filepath"
	"testing"
	"time"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件守的是 B4：平台模式「新租户默认零约束」必须被一条落库的试用授权行取代。
//
// 为什么这些断言值得写：授权过期/席位超限的强制逻辑早就有（EnforceLicense），
// 但**没人给新租户写过授权行**——`ResolveFor` 查不到行就回退全局设置，
// 而全局缺省是"不限席位、不过期"，于是那套强制逻辑对新客户从来没生效过。
// 这几条用例锁的正是"行真的会被写出来、写的值是对的、关掉开关就不写"。

func newLicenseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open("sqlite://" + filepath.Join(t.TempDir(), "lic.db"))
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	return db
}

// withTrialSpec 临时替换试用口径（见 TrialSpecFromSettings 为何是包级变量）。
func withTrialSpec(t *testing.T, spec TrialSpec) {
	t.Helper()
	old := TrialSpecFromSettings
	TrialSpecFromSettings = func() TrialSpec { return spec }
	t.Cleanup(func() { TrialSpecFromSettings = old })
}

func TestTrialSpecDefaults(t *testing.T) {
	// 未初始化 setting（进程启动早期 / 单测）时必须给出可用的兜底值，
	// 不能因为读到 0 就生成「0 天 0 席位」的授权——那等于新租户一个员工都建不了。
	spec := TrialSpecFromSettings()
	require.True(t, spec.Enabled)
	require.Equal(t, 14, spec.Days)
	require.Equal(t, 5, spec.Seats)
}

func TestTrialSpecNewExpiry(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.Local)
	spec := TrialSpec{Enabled: true, Days: 14, Seats: 3}
	ent := spec.New(7, now)
	require.EqualValues(t, 7, ent.TenantID)
	require.Equal(t, "trial", ent.Edition)
	require.Equal(t, 3, ent.Seats)
	require.Equal(t, now.AddDate(0, 0, 14), ent.ExpiresAt)
	require.False(t, ent.IsExpired())
	// 到期后必须判为已过期——这是「到期自动降权（只读）」的判定基础
	ent.ExpiresAt = time.Now().Add(-time.Minute)
	require.True(t, ent.IsExpired())
}

// TestCreateTrialLicense 新开通的企业必须拿到一条试用授权行；重复调用不重复写。
func TestCreateTrialLicense(t *testing.T) {
	db := newLicenseDB(t)

	before := time.Now()
	require.NoError(t, CreateTrialLicense(db, 7))

	row, err := TenantLicenseOf(db, 7)
	require.NoError(t, err)
	require.Equal(t, "trial", row.Edition)
	require.Equal(t, 5, row.Seats)
	// 到期日 = 开通时刻 + 14 天（允许几秒误差）
	require.WithinDuration(t, before.AddDate(0, 0, 14), row.ExpiresAt, 5*time.Second)

	// 幂等：再调一次不能覆盖（否则重放会把到期日往后推，「延长试用」就白设了）
	require.NoError(t, CreateTrialLicense(db, 7))
	var cnt int64
	db.Model(&model.TenantLicense{}).Where("company_id = ?", 7).Count(&cnt)
	require.EqualValues(t, 1, cnt)
	again, err := TenantLicenseOf(db, 7)
	require.NoError(t, err)
	require.Equal(t, row.ExpiresAt.Unix(), again.ExpiresAt.Unix())
}

// TestCreateTrialLicenseRespectsSwitch 关掉「新租户自动试用」后不写行（回到不限制）。
func TestCreateTrialLicenseRespectsSwitch(t *testing.T) {
	db := newLicenseDB(t)
	withTrialSpec(t, TrialSpec{Enabled: false, Days: 14, Seats: 5})

	require.NoError(t, CreateTrialLicense(db, 8))
	_, err := TenantLicenseOf(db, 8)
	require.Error(t, err, "关闭试用后不应写授权行")
}

// TestCreateTrialLicenseSkipsPlatform companyID<=0（平台自身）不该有租户授权行。
func TestCreateTrialLicenseSkipsPlatform(t *testing.T) {
	db := newLicenseDB(t)
	require.NoError(t, CreateTrialLicense(db, 0))
	var cnt int64
	db.Model(&model.TenantLicense{}).Count(&cnt)
	require.EqualValues(t, 0, cnt)
}

// TestSetTenantLicenseUpsertAndClearExpiry 后台「授权」的写入语义：
//   - 首次写入 = 插入；
//   - 再次写入 = 覆盖（转正：席位 0 = 不限、到期清空 = 不过期）。
//
// 「清空到期」这条尤其重要：用结构体零值写库会被 GORM 序列化成
// '0000-00-00 00:00:00'，MySQL 严格模式直接报错——所以实现改用了 map + nil。
func TestSetTenantLicenseUpsertAndClearExpiry(t *testing.T) {
	db := newLicenseDB(t)

	exp := time.Date(2026, 12, 31, 23, 59, 59, 0, time.Local)
	require.NoError(t, SetTenantLicense(db, 9, "trial", 5, &exp, "content_sec"))
	row, err := TenantLicenseOf(db, 9)
	require.NoError(t, err)
	require.Equal(t, "trial", row.Edition)
	require.Equal(t, 5, row.Seats)
	require.Equal(t, exp.Unix(), row.ExpiresAt.Unix())
	require.Equal(t, "content_sec", row.Features)

	// 转正：不限席位 + 不过期
	require.NoError(t, SetTenantLicense(db, 9, "platform", 0, nil, ""))
	row, err = TenantLicenseOf(db, 9)
	require.NoError(t, err)
	require.Equal(t, "platform", row.Edition)
	require.Equal(t, 0, row.Seats)
	require.True(t, row.ExpiresAt.IsZero(), "清空到期后应为零值（库里是 NULL）")
	require.Empty(t, row.Features)

	// 不能变成两行
	var cnt int64
	db.Model(&model.TenantLicense{}).Where("company_id = ?", 9).Count(&cnt)
	require.EqualValues(t, 1, cnt)
}

// TestResolveForUsesTrialRow 走到真实取值链：有试用行时 ResolveFor 必须返回它
// （而不是回退全局设置）。这是「到期自动降权」真正生效的那一步。
func TestResolveForUsesTrialRow(t *testing.T) {
	db := newLicenseDB(t)
	SetTenantDB(db)
	t.Cleanup(func() { SetTenantDB(nil) })

	// 平台模式：本机没有许可证文件、DEPLOY_MODE 未设
	t.Setenv("DEPLOY_MODE", "platform")
	SetCurrent(&Entitlement{Edition: "platform", Seats: 0}) // 全局缺省 = 不限
	t.Cleanup(func() { SetCurrent(nil) })

	exp := time.Now().Add(-time.Hour) // 已过期
	require.NoError(t, SetTenantLicense(db, 10, "trial", 3, &exp, ""))

	ent := ResolveFor(t.Context(), 10)
	require.NotNil(t, ent)
	require.Equal(t, "trial", ent.Edition)
	require.Equal(t, 3, ent.Seats)
	require.True(t, ent.IsExpired(), "试用已到期 → 中间件据此降级只读")

	// 没有授权行的租户（存量客户）仍回退全局：升级不能让老客户突然被限
	fallback := ResolveFor(t.Context(), 11)
	require.NotNil(t, fallback)
	require.Equal(t, "platform", fallback.Edition)
	require.False(t, fallback.IsExpired())
}
