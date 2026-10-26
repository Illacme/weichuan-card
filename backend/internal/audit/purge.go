package audit

import (
	"log"
	"sync"
	"time"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// 审计日志保留期清理（C3）。
//
// 为什么需要它：audit_log 是只追加表（本包刻意不提供改/删接口——能改审计记录
// 就等于没有审计），管理员的每一次写操作都会留一行。上线一年后这张表能涨到
// 百万行，届时「留着全部」既占磁盘、也让后台分页越翻越慢。
//
// 为什么默认不清理（保留天数缺省 0 = 永不自动删）：
//   - 审计数据的价值恰恰在于「出事之后能翻到很久以前」，而**删掉是不可逆的**。
//     升级当天就把一年前的日志清掉，是把「本来就有的证据」变成「再也拿不回来」，
//     属于升级行为突变（与 B3 存储配额、B4 试用授权同一条纪律：升级不得突变存量）。
//   - 因此：想清理必须由运维**显式填一个天数**，填了才生效，不填就不动。
//
// 为什么清理动作本身也要记一条审计：
// 「谁删了审计日志」是最该被追责的动作之一——一个能删日志的人同时也能抹掉自己的
// 操作痕迹。这里把清理记成 audit.purge（操作者 = system，因为没有 gin 上下文），
// 于是后台审计页能看到「某年某月某日清掉了 N 条、保留 M 天」；至于为什么清、
// 谁配的保留天数，则由 setting.update（改 audit_retention_days）那条记录回答。

const (
	// purgeBatch 每批删除的行数。一次 DELETE 清掉几十万行会在 MySQL 上长时间
	// 持锁（审计表还在被业务持续写入），分批能把单次持锁时间压到毫秒级。
	purgeBatch = 2000
	// purgeMaxBatches 单个 tick 内最多跑多少批（= 10 万行）。
	// 有上限才有「可预期的耗时上限」：积压再多也是每 tick 追一点，不会把整个
	// 实例拖住；正常运行时根本到不了这个上限。
	purgeMaxBatches = 50
	// MaxRetentionDays 保留天数的上限，与 setting registry 的 intRange(0, 3650) 对齐。
	MaxRetentionDays = 3650
	// DefaultAuditPurgeInterval 定期清理间隔（12 小时）。
	// 审计清理不是时效性动作：保留期以「天」为单位，晚半天删没有任何业务差别；
	// 而频率高了纯粹是在给数据库找事。间隔由 AUDIT_PURGE_MINUTES 覆盖。
	DefaultAuditPurgeInterval = 12 * time.Hour
)

// PurgeAuditLogs 删除早于 keepDays 天之前的审计日志，返回实际删除行数。
//
// keepDays <= 0 表示「不清理」——直接返回 0 且不动数据。这个短路是刻意放在
// 最前面的：宁可什么都不做，也不能因为「没配天数」被解读成「保留 0 天」而全表清空。
//
// 分批删除而不是一条 DELETE 到底：见上面 purgeBatch 的说明。每批先按主键捞出
// id 再按 id 删，是因为 MySQL 不允许在 IN 子查询里直接写 LIMIT，而 SQLite 不支持
// DELETE ... LIMIT——「查 id + 按主键删」是两种库都能跑的写法。
func PurgeAuditLogs(db *gorm.DB, keepDays int) (int64, error) {
	if db == nil || keepDays <= 0 {
		return 0, nil
	}
	if keepDays > MaxRetentionDays {
		keepDays = MaxRetentionDays
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	var total int64
	for i := 0; i < purgeMaxBatches; i++ {
		var ids []int64
		if err := db.Model(&model.AuditLog{}).
			Where("created_at < ?", cutoff).
			Order("id").Limit(purgeBatch).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		res := db.Where("id IN ?", ids).Delete(&model.AuditLog{})
		total += res.RowsAffected
		if res.Error != nil {
			return total, res.Error
		}
		// 捞到 id 却一行都没删掉（并发下极罕见）：再循环下去只会空转，直接退出。
		if res.RowsAffected == 0 {
			break
		}
		// 本批没满 = 后面没有更多待删行，不必再查一次。
		if len(ids) < purgeBatch {
			break
		}
	}
	return total, nil
}

// RunAuditPurgeOnce 执行一次「清理 + 留痕」，供启动时与定期任务共用。
//
// keepDays 传函数而不是数值：保留天数在后台设置里可改（界面改完即时生效，
// 不需要重启），每次 tick 重新读一遍才能反映最新配置。
// 返回 0 时不记审计（「什么都没删」不构成一条需要追责的事件，否则审计表里
// 会多出成千上万条无意义的空跑记录）。
func RunAuditPurgeOnce(db *gorm.DB, keepDays func() int) {
	if db == nil {
		return
	}
	days := 0
	if keepDays != nil {
		days = keepDays()
	}
	if days <= 0 {
		return
	}
	n, err := PurgeAuditLogs(db, days)
	detail := map[string]any{"keep_days": days}
	if err != nil {
		// 失败也留痕：清理失败意味着「保留期配了但没执行」，运维需要知道。
		// 错误文本**不进 detail**——数据库错误可能携带连接串（含密码），
		// 而审计表一旦泄密就与它存在的初衷相悖；排查看服务端日志即可。
		log.Printf("[WARN] 定期清理审计日志失败（下次 tick 会重试）：%v", err)
		Record(db, nil, Entry{
			Action: ActionAuditPurge, ActorType: ActorSystem, TargetType: "audit_log",
			Result: ResultFail, Detail: detail,
		})
		return
	}
	if n == 0 {
		return
	}
	detail["deleted"] = n
	log.Printf("[INFO] 清理审计日志 %d 行（保留 %d 天）", n, days)
	Record(db, nil, Entry{
		Action: ActionAuditPurge, ActorType: ActorSystem, TargetType: "audit_log",
		Detail: detail,
	})
}

// StartAuditLogPurge 启动后台定期清理，返回停止函数。
// 与 repository.StartExpiredSessionPurge / StartRateLimitPurge 同构：stop() 返回时
// 协程必然已退出，因此「停止之后不会再有任何一次 DELETE」是硬保证而不是时序运气。
func StartAuditLogPurge(db *gorm.DB, interval time.Duration, keepDays func() int) (stop func()) {
	if interval <= 0 {
		interval = DefaultAuditPurgeInterval
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
				RunAuditPurgeOnce(db, keepDays)
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
