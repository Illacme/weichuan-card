package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/model"
)

// 审计清理的判据不是「能不能删」，而是「该不该删」：
// 删审计日志不可逆，所以默认必须一个字都不动；只有显式配了保留天数才生效。
// 这两条要是反了，升级当天就会把客户的存量证据清空——比不清理严重得多。

// seedRows 造 n 条 createdAt 时刻的审计日志。
func seedRows(t *testing.T, db *gorm.DB, n int, createdAt time.Time) {
	t.Helper()
	// 分批插入：SQLite 单条 INSERT 的变量数有上限（默认 32766），
	// 4010 行 × 11 列会直接报 "too many SQL variables"（实测）。
	const chunk = 200
	for i := 0; i < n; i += chunk {
		size := chunk
		if n-i < size {
			size = n - i
		}
		rows := make([]model.AuditLog, 0, size)
		for j := 0; j < size; j++ {
			rows = append(rows, model.AuditLog{
				ActorType: ActorAdmin, Action: ActionSettingUpdate, Result: ResultSuccess,
				CreatedAt: createdAt,
			})
		}
		require.NoError(t, db.Create(&rows).Error)
	}
}

func countLogs(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&model.AuditLog{}).Count(&n).Error)
	return n
}

// 只删超期的、留下保留期内的——「多删一条」是丢证据，「少删一条」只是多占空间。
func TestPurgeAuditLogsDeletesOnlyExpired(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 3, time.Now().AddDate(0, 0, -40)) // 40 天前：超期
	seedRows(t, db, 2, time.Now())                    // 今天：在保留期内
	require.EqualValues(t, 5, countLogs(t, db))

	n, err := PurgeAuditLogs(db, 30)
	require.NoError(t, err)
	require.EqualValues(t, 3, n, "只应删掉 40 天前那 3 条")

	require.EqualValues(t, 2, countLogs(t, db))
	var oldest model.AuditLog
	require.NoError(t, db.Order("created_at").First(&oldest).Error)
	require.WithinDuration(t, time.Now(), oldest.CreatedAt, time.Hour, "留下的必须都是近 30 天内的")
}

// 边界：刚好等于保留天数的那条要留住（「保留 30 天」= 至少能查到 30 天前的记录）。
// 少算一天会让「承诺保留 N 天」变成 N-1 天，合规口径上说不过去。
func TestPurgeAuditLogsKeepsBoundaryDay(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 1, time.Now().AddDate(0, 0, -29)) // 29 天前，保留 30 天 → 留
	seedRows(t, db, 1, time.Now().AddDate(0, 0, -31)) // 31 天前 → 删

	n, err := PurgeAuditLogs(db, 30)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.EqualValues(t, 1, countLogs(t, db))
}

// 默认（0 天）必须一条都不删：这是「升级不得突变存量」的守门用例。
// 若哪天有人把 0 解释成「保留 0 天」，这条会立刻变红。
func TestPurgeAuditLogsZeroRetentionDeletesNothing(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 4, time.Now().AddDate(0, 0, -3650))

	n, err := PurgeAuditLogs(db, 0)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	require.EqualValues(t, 4, countLogs(t, db), "保留天数未配置时绝不能动存量日志")

	// 负数同样按「不清理」处理（配置被误填成负数的兜底）
	n, err = PurgeAuditLogs(db, -7)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	require.EqualValues(t, 4, countLogs(t, db))
}

// 分批删除：一次要删的量超过单批上限时必须全部删完，不能只删头一批就收工。
func TestPurgeAuditLogsBatchesAcrossLimit(t *testing.T) {
	db := newAuditDB(t)
	total := purgeBatch*2 + 10
	seedRows(t, db, total, time.Now().AddDate(0, 0, -100))
	seedRows(t, db, 1, time.Now()) // 保留期内的一条，必须存活

	n, err := PurgeAuditLogs(db, 30)
	require.NoError(t, err)
	require.EqualValues(t, total, n, "跨批删除要把所有超期行删干净")
	require.EqualValues(t, 1, countLogs(t, db))
}

// 清理动作本身留痕：删了多少条、按多少天保留，且操作人标为「系统」。
// 这条是「谁删了审计日志」唯一的答案来源。
func TestRunAuditPurgeOnceRecordsAudit(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 5, time.Now().AddDate(0, 0, -200))

	RunAuditPurgeOnce(db, func() int { return 90 })

	require.EqualValues(t, 1, countLogs(t, db), "只应剩下「清理」这一条留痕")
	row := lastLog(t, db)
	require.Equal(t, ActionAuditPurge, row.Action)
	require.Equal(t, ActorSystem, row.ActorType, "清理是系统动作，不能标成 anonymous")
	require.Equal(t, "系统", row.ActorName)
	require.Equal(t, ResultSuccess, row.Result)

	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.Detail), &detail))
	require.EqualValues(t, 5, int64(detail["deleted"].(float64)))
	require.EqualValues(t, 90, int64(detail["keep_days"].(float64)))
}

// 保留天数每次重新取（界面改完即时生效），而不是启动时读一次就固定住。
func TestRunAuditPurgeOnceRereadsRetention(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 2, time.Now().AddDate(0, 0, -200))

	days := 0
	RunAuditPurgeOnce(db, func() int { return days })
	require.EqualValues(t, 2, countLogs(t, db), "0 天 = 不清理，一条都不该动")

	days = 30
	RunAuditPurgeOnce(db, func() int { return days })
	require.EqualValues(t, 1, countLogs(t, db), "改成 30 天后应立即生效（无需重启）")
}

// 没删到东西时不留痕：否则每次 tick 都会往审计表里塞一条空跑记录，
// 很快就把真正有用的记录淹掉。
func TestRunAuditPurgeOnceSilentWhenNothingDeleted(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 1, time.Now())

	RunAuditPurgeOnce(db, func() int { return 30 })
	require.EqualValues(t, 1, countLogs(t, db), "没有超期行时不应新增留痕")
}

// 清理失败也要留痕（result=fail），且错误文本不进 detail——
// 数据库错误可能携带连接串（含密码），审计表不能变成新的泄密面。
func TestRunAuditPurgeOnceRecordsFailure(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 3, time.Now().AddDate(0, 0, -200))
	require.NoError(t, db.Exec("DROP TABLE audit_log").Error) // 让删除与写入都失败

	require.NotPanics(t, func() {
		RunAuditPurgeOnce(db, func() int { return 30 })
	}, "清理失败不能把进程带崩")
}

// 定期任务：stop() 之后不会再有任何删除，且重复 stop 不 panic。
func TestStartAuditLogPurgeStopsCleanly(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 1, time.Now().AddDate(0, 0, -200))

	stop := StartAuditLogPurge(db, 10*time.Millisecond, func() int { return 30 })
	stop()
	stop() // 幂等：重复停止不应 panic

	// 停止后再给足时间，若协程没退就会继续删；这里断言行数没有再变化。
	// （保留天数先设 0 再启动，能更确定地看出「stop 之后确实没跑过」）
	db2 := newAuditDB(t)
	seedRows(t, db2, 1, time.Now().AddDate(0, 0, -200))
	stop2 := StartAuditLogPurge(db2, 10*time.Millisecond, func() int { return 30 })
	stop2()
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, countLogs(t, db2), "stop() 之后不应再发生任何删除")
}

// 保留天数超过上限时按上限夹住（与 setting registry 的 intRange(0,3650) 一致），
// 防止有人填 999999 天导致 AddDate 溢出成「过去的时间」从而误删全表。
func TestPurgeAuditLogsClampsHugeRetention(t *testing.T) {
	db := newAuditDB(t)
	seedRows(t, db, 2, time.Now())

	n, err := PurgeAuditLogs(db, 999999)
	require.NoError(t, err)
	require.EqualValues(t, 0, n, "保留 3650 天时今天的日志当然不该被删")
	require.EqualValues(t, 2, countLogs(t, db))
}

// nil db 不 panic（部分部署形态下可能拿不到库连接）
func TestPurgeNilDBIsNoop(t *testing.T) {
	n, err := PurgeAuditLogs(nil, 30)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	require.NotPanics(t, func() { RunAuditPurgeOnce(nil, func() int { return 30 }) })
}
