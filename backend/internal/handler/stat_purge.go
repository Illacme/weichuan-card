package handler

import (
	"log"
	"sync"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/model"
	"weichuan/internal/safego"

	"gorm.io/gorm"
)

// 埋点原始表（stat_events）定期清理。
//
// 为什么需要：TrackStat 每次点击/访问都写一条（见 repository.TrackStat），而公众号文章、
// 海报裂变会带来持续的高频写入。这张表只有「增」没有「删」，长期运行会一直涨，届时
// 后台「数据统计」的聚合查询（按 stat_date 范围扫）与 CSV 导出会越翻越慢。
//
// 与 audit_log 保留期一致的哲学：默认 0 = 永不自动删。删统计原始数据本身不可逆，且
// 「某天的数据没了」远比「表大一点」糟糕——想清理必须由运维显式填天数，不填就不动。
// 保留天数取自系统设置 stat_retention_days（后台改完即时生效，不必重启）。
//
// 删除走「查 id + 按主键删」分批：stat_events 仍被高频写入，一条 DELETE 到底会在
// MySQL 上长时间持锁；分批把单次持锁压到毫秒级（与 audit/purge.go 同理由）。

const (
	statPurgeBatch      = 2000
	statPurgeMaxBatches = 50
	// MaxStatRetentionDays 保留天数上限，与 setting registry 的 intRange(0, 3650) 对齐。
	MaxStatRetentionDays = 3650
	// DefaultStatPurgeInterval 检查间隔（12 小时）。保留期以「天」计，频率高低无业务差别。
	DefaultStatPurgeInterval = 12 * time.Hour
)

// PurgeStatEvents 删除早于 keepDays 天之前的埋点事件，返回删除行数。
// keepDays <= 0 直接返回 0 且不删——宁可不动，也不把「没配天数」误解成「保留 0 天」全清。
func PurgeStatEvents(db *gorm.DB, keepDays int) (int64, error) {
	if db == nil || keepDays <= 0 {
		return 0, nil
	}
	if keepDays > MaxStatRetentionDays {
		keepDays = MaxStatRetentionDays
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	var total int64
	for i := 0; i < statPurgeMaxBatches; i++ {
		var ids []int64
		if err := db.Model(&model.StatEvent{}).
			Where("created_at < ?", cutoff).
			Order("id").Limit(statPurgeBatch).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		res := db.Where("id IN ?", ids).Delete(&model.StatEvent{})
		total += res.RowsAffected
		if res.Error != nil {
			return total, res.Error
		}
		// 捞到 id 却没删掉（并发下极罕见）或本批没满，都退出。
		if res.RowsAffected == 0 || len(ids) < statPurgeBatch {
			break
		}
	}
	return total, nil
}

// RunStatPurgeOnce 执行一次「清理 + 留痕」，供启动时与定期任务共用。
// keepDays 传函数：后台改保留天数即时生效，每次 tick 重读。
func RunStatPurgeOnce(db *gorm.DB, keepDays func() int) {
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
	n, err := PurgeStatEvents(db, days)
	if err != nil {
		// 失败也留痕：清理失败意味着「保留期配了但没执行」，运维需要知道。
		// 错误文本不进 Detail（数据库错误可能携带连接串），排查看服务端日志即可。
		log.Printf("[WARN] 定期清理埋点数据失败（下次 tick 重试）：%v", err)
		audit.Record(db, nil, audit.Entry{
			Action:     audit.ActionStatPurge,
			ActorType:  audit.ActorSystem,
			TargetType: "stat_events",
			Result:     audit.ResultFail,
			Detail:     map[string]any{"keep_days": days},
		})
		return
	}
	if n == 0 {
		return
	}
	log.Printf("[INFO] 清理埋点数据 %d 行（保留 %d 天）", n, days)
	audit.Record(db, nil, audit.Entry{
		Action:     audit.ActionStatPurge,
		ActorType:  audit.ActorSystem,
		TargetType: "stat_events",
		Detail:     map[string]any{"keep_days": days, "deleted": n},
	})
}

// StartStatEventsPurge 启动后台定期清理，返回停止函数。
// 与 audit.StartAuditLogPurge / repository.StartExpiredSessionPurge 同构：
// stop() 返回时协程必然已退出，因此「停止之后不会再有任何一次 DELETE」是硬保证。
func StartStatEventsPurge(db *gorm.DB, interval time.Duration, keepDays func() int) (stop func()) {
	if interval <= 0 {
		interval = DefaultStatPurgeInterval
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	safego.Run("stat.purge", func() {
		defer ticker.Stop()
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-ticker.C:
				RunStatPurgeOnce(db, keepDays)
			case <-done:
				return
			}
		}
	})
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}
