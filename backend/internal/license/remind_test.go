package license

import (
	"testing"
	"time"

	"weichuan/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// remindDB 造一个只有 company + tenant_license + audit_log 的临时库。
// 不跑 AutoMigrate(model.All())：那会把 22 张表全建一遍，测试慢且与本文件无关。
func remindDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Company{}, &model.TenantLicense{}, &model.AuditLog{}))
	return db
}

func seedCompany(t *testing.T, db *gorm.DB, id int64, name, status string) {
	t.Helper()
	require.NoError(t, db.Create(&model.Company{ID: id, Name: name, Status: status}).Error)
}

func seedLicense(t *testing.T, db *gorm.DB, companyID int64, edition string, seats int, expiresAt time.Time) {
	t.Helper()
	require.NoError(t, db.Create(&model.TenantLicense{
		CompanyID: companyID, Edition: edition, Seats: seats, ExpiresAt: expiresAt,
	}).Error)
}

func auditCount(t *testing.T, db *gorm.DB, action string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&model.AuditLog{}).Where("action = ?", action).Count(&n).Error)
	return n
}

// 扫描口径：只挑出 warn/urgent/expired，ok 与「不过期」不打扰。
func TestCollectExpiringFilters(t *testing.T) {
	db := remindDB(t)
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	at := func(days int) time.Time {
		return time.Date(2026, 11, 2+days, 23, 59, 59, 0, time.Local)
	}

	seedCompany(t, db, 1, "还剩半年", "enabled")
	seedLicense(t, db, 1, "platform", 0, at(180))
	seedCompany(t, db, 2, "还剩 20 天", "enabled")
	seedLicense(t, db, 2, "trial", 5, at(20))
	seedCompany(t, db, 3, "还剩 3 天", "enabled")
	seedLicense(t, db, 3, "trial", 5, at(3))
	seedCompany(t, db, 4, "已过期", "enabled")
	seedLicense(t, db, 4, "trial", 5, at(-10))
	// 停用企业不提醒：它本来就不提供服务
	seedCompany(t, db, 5, "已停用且已过期", "disabled")
	seedLicense(t, db, 5, "trial", 5, at(-10))
	// 不过期的授权（正式客户）不该出现在提醒里
	seedCompany(t, db, 6, "正式客户", "enabled")
	seedLicense(t, db, 6, "platform", 0, time.Time{})

	// 平台全局那份：本用例里 Current() 为 nil，等价于「平台模式没配全局授权」
	SetCurrent(nil)
	t.Cleanup(func() { SetCurrent(nil) })

	got := CollectExpiring(db, now)
	require.Len(t, got, 3, "应只提醒 2/3/4 三家：%+v", got)

	byID := map[int64]Reminder{}
	for _, r := range got {
		byID[r.CompanyID] = r
	}
	require.Equal(t, ExpiryLevelWarn, byID[2].Status.Level)
	require.Equal(t, 20, byID[2].Status.DaysLeft)
	require.Equal(t, "还剩 20 天", byID[2].CompanyName, "企业名要带出来，否则运营不知道是谁")
	require.Equal(t, ExpiryLevelUrgent, byID[3].Status.Level)
	require.Equal(t, ExpiryLevelExpired, byID[4].Status.Level)
	require.True(t, byID[4].Status.Expired)
	_, has1 := byID[1]
	require.False(t, has1, "还剩半年的不该打扰")
	_, has5 := byID[5]
	require.False(t, has5, "停用企业不该提醒")
	_, has6 := byID[6]
	require.False(t, has6, "不过期的授权不该提醒")
}

// 平台全局那份（系统设置里的 license_expires_at）也要提醒——
// 平台模式漏了它，等于服务商自己的授权到期了没人管。
func TestCollectExpiringIncludesGlobal(t *testing.T) {
	db := remindDB(t)
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	SetCurrent(&Entitlement{
		Edition: "platform", ExpiresAt: time.Date(2026, 11, 5, 23, 59, 59, 0, time.Local),
	})
	t.Cleanup(func() { SetCurrent(nil) })

	got := CollectExpiring(db, now)
	require.Len(t, got, 1)
	require.Equal(t, "platform", got[0].Scope)
	require.Equal(t, ExpiryLevelUrgent, got[0].Status.Level)
	require.Equal(t, "platform", got[0].Key(), "全局那份的 key 固定为 platform")
}

// 核心行为：同一档位只记一条审计，跳档才再记。
// 否则一个还剩 30 天的授权会在审计表里连续出现 23 条「还剩 30 天」。
func TestRunExpiryReminderOnceAuditsOnLevelChangeOnly(t *testing.T) {
	db := remindDB(t)
	ResetReminderMemory()
	t.Cleanup(ResetReminderMemory)

	day := func(n int) time.Time {
		return time.Date(2026, 11, 2+n, 23, 59, 59, 0, time.Local)
	}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)

	seedCompany(t, db, 1, "朗汇机电", "enabled")
	seedLicense(t, db, 1, "trial", 5, day(25))
	SetCurrent(nil)
	t.Cleanup(func() { SetCurrent(nil) })

	// 第一次：进入 warn → 记一条
	RunExpiryReminderOnce(db, now)
	require.EqualValues(t, 1, auditCount(t, db, "license.expiring"))

	// 同档位再扫两次（模拟连续两天）→ 不新增
	RunExpiryReminderOnce(db, now)
	RunExpiryReminderOnce(db, now)
	require.EqualValues(t, 1, auditCount(t, db, "license.expiring"))

	// 改成 3 天后（warn → urgent）→ 新增一条
	require.NoError(t, db.Model(&model.TenantLicense{}).Where("company_id = ?", 1).
		Update("expires_at", day(3)).Error)
	RunExpiryReminderOnce(db, now)
	require.EqualValues(t, 2, auditCount(t, db, "license.expiring"))

	// 再改成已过期（urgent → expired）→ 再新增一条
	require.NoError(t, db.Model(&model.TenantLicense{}).Where("company_id = ?", 1).
		Update("expires_at", day(-1)).Error)
	RunExpiryReminderOnce(db, now)
	require.EqualValues(t, 3, auditCount(t, db, "license.expiring"))

	// 审计详情要能回答「提醒的是谁、还剩几天」，而不是只留一个 level
	var last model.AuditLog
	require.NoError(t, db.Order("id desc").First(&last).Error)
	require.Equal(t, "system", last.ActorType)
	require.Equal(t, "license", last.TargetType)
	require.Equal(t, "1", last.TargetID)
	require.Contains(t, last.Detail, `"level":"expired"`)
	require.Contains(t, last.Detail, `"companyId":1`)
}

// 续费之后档位回到 ok：记忆必须被清掉，否则下次再临近到期就再也不提醒了。
func TestRunExpiryReminderOnceForgetsAfterRenewal(t *testing.T) {
	db := remindDB(t)
	ResetReminderMemory()
	t.Cleanup(ResetReminderMemory)

	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	day := func(n int) time.Time {
		return time.Date(2026, 11, 2+n, 23, 59, 59, 0, time.Local)
	}

	seedCompany(t, db, 1, "朗汇机电", "enabled")
	seedLicense(t, db, 1, "trial", 5, day(5))
	SetCurrent(nil)
	t.Cleanup(func() { SetCurrent(nil) })

	RunExpiryReminderOnce(db, now) // urgent → 1 条
	require.EqualValues(t, 1, auditCount(t, db, "license.expiring"))

	// 续费一年：不再提醒
	require.NoError(t, db.Model(&model.TenantLicense{}).Where("company_id = ?", 1).
		Update("expires_at", day(365)).Error)
	got := RunExpiryReminderOnce(db, now)
	require.Empty(t, got)
	require.EqualValues(t, 1, auditCount(t, db, "license.expiring"))

	// 一年后又临近到期（模拟时间前进）：必须能重新提醒
	RunExpiryReminderOnce(db, time.Date(2027, 10, 10, 9, 0, 0, 0, time.Local))
	require.EqualValues(t, 2, auditCount(t, db, "license.expiring"))
}

// 扫描出错不该 panic，也不该把已收集到的项丢掉（这里是库被关掉的极端情形）。
func TestCollectExpiringSurvivesBrokenDB(t *testing.T) {
	db := remindDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	SetCurrent(nil)
	t.Cleanup(func() { SetCurrent(nil) })

	require.NotPanics(t, func() {
		got := CollectExpiring(db, time.Now())
		require.Empty(t, got)
	})
}
