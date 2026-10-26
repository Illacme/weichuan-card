package repository

import (
	"log"
	"sync"
	"time"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// isMySQLDialect 是否 MySQL 方言。与 migrate 包的同名判断是两份：
// 那个是包私有的（迁移用），这里是仓储层自己的（UPSERT 语法分流用）。
// 判据取 Dialector.Name()，与 dbopen 的 DriverMySQL 常量保持同一套字符串。
func isMySQLDialect(db *gorm.DB) bool {
	return db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql"
}

// 限流计数的跨副本共享实现。
//
// 背景：进程内限流器（middleware.RateLimiter）按 key 分桶，多副本部署下每个副本
// 各持一份桶，实际阈值会变成「单副本阈值 × 副本数」——登录防爆破从 10 次/分
// 悄悄翻倍，而且不报错、监控看不出来。
//
// 这里把计数落到 rate_limit_hits 表：同一 (key, 窗口) 在所有副本上是同一行，
// 于是阈值与副本数无关。SQLite 单机形态也走同一路径——行为一致，
// 免得出现「MySQL 限流准、SQLite 不准」这种难以解释的差异。

// 本包取「当前时间」的时间源。
//
// 为什么不直接调 time.Now：窗口按墙上时钟对齐（见 windowStart），于是「现在落在
// 哪一分钟」决定了计数落到哪一行。测试里连打十几次请求只要跨过一次分钟边界，
// 计数就翻页重置，「第 11 次必须被拒」会变成放行——这是与被测逻辑无关的偶发
// 失败（并发跑全量包时最容易撞上）。把时间源做成可替换的，测试就能固定 / 推进
// 时钟，让「窗口翻页」成为可断言的确定事件。生产路径不受影响（默认 time.Now）。
var (
	nowMu sync.RWMutex
	nowFn = time.Now
)

// now 取当前时间（走可替换的时间源）。
func now() time.Time {
	nowMu.RLock()
	fn := nowFn
	nowMu.RUnlock()
	return fn()
}

// SetNowFunc 替换本包取当前时间的实现，返回还原函数。
// **仅测试使用**：固定时钟可让限流窗口不再自己翻页；推进时钟可断言翻页行为。
// 传 nil 表示还原为 time.Now。调用方应在测试结束时执行返回的还原函数。
func SetNowFunc(fn func() time.Time) (restore func()) {
	nowMu.Lock()
	old := nowFn
	if fn == nil {
		fn = time.Now
	}
	nowFn = fn
	nowMu.Unlock()
	return func() {
		nowMu.Lock()
		nowFn = old
		nowMu.Unlock()
	}
}

// windowStart 把时间对齐到窗口边界，返回该窗口的起始 Unix 秒。
// 同一窗口内任意时刻都得到同一个值，它因此可以作为主键的一部分，
// 也不需要额外判断「窗口是否已经翻页」。
func windowStart(window time.Duration) int64 {
	sec := int64(window / time.Second)
	if sec <= 0 {
		sec = 1
	}
	return now().Unix() / sec * sec
}

// RateHit 记录一次命中，返回该 key 在当前窗口内的累计次数（含本次）。
//
// 自增必须是原子的：两个副本同时打同一行时，若用「先查后插」会互相覆盖，
// 计数偏小等于限流被绕开。故这里用数据库自己的 UPSERT。
//
// 方言分流是必须的：MySQL 用 ON DUPLICATE KEY UPDATE，SQLite 用
// ON CONFLICT DO UPDATE，两者语法不兼容（本项目不用 gorm 的 clause.OnConflict，
// 理由见 repository/session.go 里 RevokeSession 的注释）。
func RateHit(db *gorm.DB, key string, window time.Duration) (int, error) {
	if key == "" {
		key = "unknown"
	}
	start := windowStart(window)
	var stmt string
	if isMySQLDialect(db) {
		stmt = "INSERT INTO rate_limit_hits (bucket_key, window_start, hits) VALUES (?, ?, 1) " +
			"ON DUPLICATE KEY UPDATE hits = hits + 1"
	} else {
		stmt = "INSERT INTO rate_limit_hits (bucket_key, window_start, hits) VALUES (?, ?, 1) " +
			"ON CONFLICT(bucket_key, window_start) DO UPDATE SET hits = hits + 1"
	}
	if err := db.Exec(stmt, key, start).Error; err != nil {
		return 0, err
	}
	var n int
	if err := db.Model(&model.RateLimitHit{}).
		Where("bucket_key = ? AND window_start = ?", key, start).
		Pluck("hits", &n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// RateHitsOf 只读当前窗口内的累计次数（不自增）。测试与排查用。
func RateHitsOf(db *gorm.DB, key string, window time.Duration) (int, error) {
	var n int
	err := db.Model(&model.RateLimitHit{}).
		Where("bucket_key = ? AND window_start = ?", key, windowStart(window)).
		Pluck("hits", &n).Error
	return n, err
}

// PurgeOldRateHits 删除早于 keep 之前开始的窗口行，返回删除行数。
//
// 为什么需要它：每一行只在一个窗口内有意义，但不会自己消失——按窗口聚合意味着
// 每分钟都会产生一批新行，长期运行下表只增不减。保留 keep 这么多个窗口即可
// （限流判定只看当前窗口，历史行留着没有任何用途）。
func PurgeOldRateHits(db *gorm.DB, keep time.Duration) (int64, error) {
	if keep <= 0 {
		keep = 10 * time.Minute
	}
	cutoff := now().Add(-keep).Unix()
	res := db.Where("window_start < ?", cutoff).Delete(&model.RateLimitHit{})
	return res.RowsAffected, res.Error
}

// DefaultRatePurgeInterval 限流计数表的定期清理间隔（10 分钟）。
// 窗口是分钟级，10 分钟的间隔足够及时，且每次删除的行数有界。
const DefaultRatePurgeInterval = 10 * time.Minute

// StartRateLimitPurge 启动后台定期清理过期的限流计数行，返回停止函数。
// 与 StartExpiredSessionPurge 同构：stop() 返回时协程必然已退出，
// 因此「停止之后不会再有任何一次 DELETE」是硬保证而不是时序运气
// （select 多 case 同时就绪时 Go 随机选，macOS 测不出、Linux CI 稳定复现，
// 见 StartExpiredSessionPurge 注释里的那次修复）。
func StartRateLimitPurge(db *gorm.DB, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultRatePurgeInterval
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer ticker.Stop()
		defer close(stopped)
		for {
			// 先非阻塞地看一眼停止信号：已停止就别再等下一次 tick
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-ticker.C:
				n, err := PurgeOldRateHits(db, interval)
				if err != nil {
					log.Printf("[WARN] 定期清理限流计数行失败（下次 tick 会重试，不影响限流判定）：%v", err)
					continue
				}
				if n > 0 {
					log.Printf("[INFO] 定期清理限流计数 %d 行", n)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}
