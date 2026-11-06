package handler

import (
	"os"
	"testing"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// statPurgeDB 用临时文件库而非 :memory:，避免 gorm 连接池在多测试并发下
// 把 :memory: 拆成多个独立库，导致 Pluck 查到 id 但 Delete 落到另一空库（删 0 行）。
func statPurgeDB(t *testing.T) *gorm.DB {
	t.Helper()
	tmpDB, err := os.CreateTemp("", "weichuan-statpurge-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StatEvent{}, &model.AuditLog{}))
	return db
}

func addStat(t *testing.T, db *gorm.DB, daysAgo int) {
	ts := time.Now().AddDate(0, 0, -daysAgo)
	ev := model.StatEvent{
		StaffID:   1,
		EventType: "visit",
		StatDate:  ts.Format("2006-01-02"),
		CreatedAt: ts,
	}
	require.NoError(t, db.Create(&ev).Error)
}

func TestPurgeStatEventsDeletesOnlyExpired(t *testing.T) {
	db := statPurgeDB(t)
	// 60 天前：超出 30 天保留窗口，应被删。
	for i := 0; i < 5; i++ {
		addStat(t, db, 60)
	}
	// 1 天前：落在保留窗口内，应保留。
	for i := 0; i < 3; i++ {
		addStat(t, db, 1)
	}
	n, err := PurgeStatEvents(db, 30)
	require.NoError(t, err)
	require.Equal(t, int64(5), n)
	var remaining int64
	db.Model(&model.StatEvent{}).Count(&remaining)
	require.Equal(t, int64(3), remaining)
}

func TestPurgeStatEventsZeroRetentionDeletesNothing(t *testing.T) {
	db := statPurgeDB(t)
	addStat(t, db, 100)
	n, err := PurgeStatEvents(db, 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
	var c int64
	db.Model(&model.StatEvent{}).Count(&c)
	require.Equal(t, int64(1), c)
}

func TestPurgeStatEventsNegativeRetentionDeletesNothing(t *testing.T) {
	db := statPurgeDB(t)
	addStat(t, db, 100)
	n, err := PurgeStatEvents(db, -5)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestPurgeStatEventsBatchesAcrossLimit(t *testing.T) {
	db := statPurgeDB(t)
	for i := 0; i < statPurgeBatch*2+10; i++ {
		addStat(t, db, 60)
	}
	n, err := PurgeStatEvents(db, 30)
	require.NoError(t, err)
	require.Equal(t, int64(statPurgeBatch*2+10), n)
}

func TestRunStatPurgeOnceSilentWhenNothingDeleted(t *testing.T) {
	db := statPurgeDB(t)
	RunStatPurgeOnce(db, func() int { return 30 })
	var c int64
	db.Model(&model.AuditLog{}).Count(&c)
	require.Equal(t, int64(0), c)
}

func TestRunStatPurgeOnceRecordsAudit(t *testing.T) {
	db := statPurgeDB(t)
	for i := 0; i < 3; i++ {
		addStat(t, db, 60)
	}
	RunStatPurgeOnce(db, func() int { return 30 })
	var rows []model.AuditLog
	db.Where("action = ?", audit.ActionStatPurge).Find(&rows)
	require.Len(t, rows, 1)
	require.Equal(t, "stat_events", rows[0].TargetType)
}

func TestRunStatPurgeOnceSkipsWhenZeroRetention(t *testing.T) {
	db := statPurgeDB(t)
	addStat(t, db, 100)
	RunStatPurgeOnce(db, func() int { return 0 })
	var c int64
	db.Model(&model.StatEvent{}).Count(&c)
	require.Equal(t, int64(1), c)
	var ac int64
	db.Model(&model.AuditLog{}).Count(&ac)
	require.Equal(t, int64(0), ac)
}

func TestStartStatEventsPurgeStopsCleanly(t *testing.T) {
	db := statPurgeDB(t)
	stop := StartStatEventsPurge(db, 10*time.Millisecond, func() int { return 0 })
	time.Sleep(30 * time.Millisecond)
	require.NotPanics(t, stop)
}
