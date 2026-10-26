package repository

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
)

// 限流计数表的多副本共享语义。用 SQLite 临时库覆盖：UPSERT 语法虽按方言分流，
// 但「同 key 同窗口自增、换窗口重新计」的语义两边一致。

func newRateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open(filepath.Join(t.TempDir(), "rate.db"))
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.RateLimitHit{}))
	return db
}

// 同一 key 在同一窗口内必须累加，而不是各自记一份——这正是多副本下
// 「阈值不翻倍」的保证。
func TestRateHitAccumulatesWithinWindow(t *testing.T) {
	db := newRateTestDB(t)
	w := time.Minute

	n1, err := RateHit(db, "login:1.2.3.4", w)
	require.NoError(t, err)
	require.Equal(t, 1, n1)

	n2, err := RateHit(db, "login:1.2.3.4", w)
	require.NoError(t, err)
	require.Equal(t, 2, n2, "同一 key 同一窗口应累加，否则多副本下阈值翻倍")

	n3, err := RateHit(db, "login:1.2.3.4", w)
	require.NoError(t, err)
	require.Equal(t, 3, n3)
}

// 不同 key 互不影响：限流是按 key 分桶的，串桶会让无辜用户被拒。
func TestRateHitIsolatesKeys(t *testing.T) {
	db := newRateTestDB(t)
	for i := 0; i < 3; i++ {
		_, err := RateHit(db, "login:5.6.7.8", time.Minute)
		require.NoError(t, err)
	}
	n, err := RateHit(db, "login:9.9.9.9", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, n, "新 key 应从 1 开始，不能继承别的 key 的计数")
}

// 窗口翻页后必须重新计数：否则一次撞限就永久锁死该 key
// （也正是固定窗口限流的本意——惩罚只持续一个窗口）。
//
// 翻页靠**推进可控时钟**而不是 sleep：sleep 的时长必须大于窗口，
// 负载高时「睡够了但调度没跟上」同样会偶发失败；推进时钟没有这个不确定性，
// 还顺带把这一条从测试总时长里省掉 1.3 秒。
func TestRateHitResetsOnNewWindow(t *testing.T) {
	db := newRateTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)
	restore := SetNowFunc(func() time.Time { return base })
	defer restore()

	for i := 0; i < 3; i++ {
		_, err := RateHit(db, "login:same", time.Minute)
		require.NoError(t, err)
	}
	n, err := RateHit(db, "login:same", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 4, n, "同一窗口内应持续累加")

	base = base.Add(2 * time.Minute) // 推进到下一个窗口
	n, err = RateHit(db, "login:same", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, n, "新窗口应从 1 重新计")
}

// 时间源本身要可还原：否则一个测试固定了时钟，后续测试就活在 2026 年，
// 表现为「单独跑全绿、整包跑偶发失败」这种最难查的形态。
func TestSetNowFuncRestoresPreviousClock(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)

	restore := SetNowFunc(func() time.Time { return fixed })
	require.Equal(t, fixed.Unix()/60*60, windowStart(time.Minute), "固定时钟后窗口起始应随之确定")
	restore()

	// 还原后应回到墙上时钟：与 time.Now 算出的窗口一致
	require.Equal(t, time.Now().Unix()/60*60, windowStart(time.Minute), "还原后应回到真实时钟")

	// nil 表示显式还原为 time.Now
	restore = SetNowFunc(nil)
	require.Equal(t, time.Now().Unix()/60*60, windowStart(time.Minute))
	restore()
}

// 窗口边界必须对齐：同一窗口内的任意时刻都应落到同一行。
// 若不对齐，同一个窗口会被拆成多行，计数偏小、限流被绕开。
func TestWindowStartAlignsToWindow(t *testing.T) {
	got := windowStart(time.Minute)
	require.EqualValues(t, 0, got%60, "窗口应按整分钟对齐，实际 %d", got)

	got2 := windowStart(2 * time.Second)
	require.EqualValues(t, 0, got2%2, "窗口应对齐到自身边界，实际 %d", got2)

	// 非法窗口不能除零
	require.NotZero(t, windowStart(0))
	require.NotZero(t, windowStart(-time.Second))
}

// 清理只删过期窗口，且保留量可控。
func TestPurgeOldRateHitsKeepsRecent(t *testing.T) {
	db := newRateTestDB(t)
	// 必须用对齐后的窗口起始，否则 RateHitsOf（按当前窗口查）对不上——
	// 这正是 windowStart 对齐的意义：同一窗口内任意时刻都落在同一行。
	curWindow := windowStart(time.Minute)

	// 一行很旧（应删），一行当前（应留），一行来自未来一点的窗口（应留）
	require.NoError(t, db.Create(&model.RateLimitHit{BucketKey: "old", WindowStart: curWindow - 3600, Hits: 5}).Error)
	require.NoError(t, db.Create(&model.RateLimitHit{BucketKey: "now", WindowStart: curWindow, Hits: 5}).Error)
	require.NoError(t, db.Create(&model.RateLimitHit{BucketKey: "future", WindowStart: curWindow + 60, Hits: 5}).Error)

	n, err := PurgeOldRateHits(db, 10*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "只该删掉那一行很旧的")

	left, err := RateHitsOf(db, "now", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 5, left, "当前窗口的行必须留着，否则限流计数被清空")

	gone, err := RateHitsOf(db, "old", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 0, gone)
}

// keep <= 0 时回落默认值，不能把整张表清空（那等于限流整体失效）。
func TestPurgeOldRateHitsFallsBackOnBadKeep(t *testing.T) {
	db := newRateTestDB(t)
	require.NoError(t, db.Create(&model.RateLimitHit{
		BucketKey: "k", WindowStart: windowStart(time.Minute), Hits: 1,
	}).Error)

	n, err := PurgeOldRateHits(db, 0)
	require.NoError(t, err)
	require.EqualValues(t, 0, n, "keep 非法时应回落默认保留量，而不是清表")

	var total int64
	require.NoError(t, db.Model(&model.RateLimitHit{}).Count(&total).Error)
	require.EqualValues(t, 1, total, "当前窗口的行不该被误删")
}

// stop() 的时序保证与 session purge 相同：返回时协程必然已退出，
// 之后不可能再有任何一次 DELETE。重复 stop 不 panic。
func TestStartRateLimitPurgeStopIdempotent(t *testing.T) {
	db := newRateTestDB(t)
	stop := StartRateLimitPurge(db, 20*time.Millisecond)
	stop()
	stop() // 重复调用不应 panic 或阻塞
}
