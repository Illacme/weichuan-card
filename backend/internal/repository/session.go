package repository

import (
	"log"
	"sync"
	"time"

	"weichuan/internal/model"
	"weichuan/internal/safego"

	"gorm.io/gorm"
)

// IsSessionRevoked 判断会话 sid 是否已被吊销（登出 / 踢下线时写入黑名单）。
// sid 为空（历史令牌，签发时无 sid）时按未吊销处理，由调用方结合其他校验兜底。
func IsSessionRevoked(db *gorm.DB, sid string) (bool, error) {
	if sid == "" {
		return false, nil
	}
	var cnt int64
	if err := db.Model(&model.RevokedSession{}).Where("sid = ?", sid).Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// RevokeSession 把会话 sid 写入黑名单（幂等：重复登出不会重复生效）。
// expiresAt 传该会话 refresh 令牌的过期时间，供后台按时间清理过期记录；
// 吊销判定只看 sid 是否存在，与 expiresAt 无关。
//
// 可移植性说明：刻意不用 clause.OnConflict{DoNothing}——它生成
// 「ON CONFLICT DO NOTHING」，MySQL 不支持该语法（仅 SQLite / PG 支持）。
// 这里用「先查后插」：同一会话的并发登出只会有一个 Insert 成功，
// 且两者写入的是同一个 sid，无论谁成功吊销判定都生效；失败方的
// duplicate-key 错误由调用方忽略（登出接口本身始终返回成功）。
func RevokeSession(db *gorm.DB, sid, userType string, userID int64, expiresAt time.Time) error {
	if sid == "" {
		return nil
	}
	exists, err := IsSessionRevoked(db, sid)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	rs := model.RevokedSession{
		SID:       sid,
		UserType:  userType,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	return db.Create(&rs).Error
}

// PurgeExpiredSessions 清理已过期的黑名单记录（expires_at 早于当前时间），
// 返回删除行数。吊销判定不看时间：行存在即视为已吊销。而 expires_at 是
// 该会话 refresh 令牌的过期时间——过了这个时点，会话自身的全部令牌都已因
// JWT exp 自然失效（access 15 分钟 / refresh 30 天，均早于或等于该时点），
// 黑名单行不再有存在价值，删除不会削弱任何吊销语义，只是防止表无限增长。
// 触发方式：服务启动时机会性执行一次（main.go），生产可另配定时任务：
//
//	DELETE FROM revoked_sessions WHERE expires_at < NOW();
func PurgeExpiredSessions(db *gorm.DB) (int64, error) {
	res := db.Where("expires_at < ?", time.Now()).Delete(&model.RevokedSession{})
	return res.RowsAffected, res.Error
}

// DefaultPurgeInterval 定期清理的默认间隔（6 小时）。见 StartExpiredSessionPurge。
const DefaultPurgeInterval = 6 * time.Hour

// StartExpiredSessionPurge 启动后台定期清理过期会话黑名单，返回停止函数。
//
// 为什么需要它：只靠启动时清一次的话，长期运行的实例（几个月不重启）里
// revoked_sessions 会一直堆积——每次登出写一行，且要等该会话 refresh 令牌
// 过期（最长 30 天）才可删。表增长本身不致命，但会让「登出后黑名单查询」
// 与备份体积无谓变慢，属于典型的「上线时看不出来、半年后才烦人」的问题。
//
// 清理失败只记日志、不中断循环：下一次 tick 会重试，且失败不影响任何吊销语义
// （黑名单行留着只会更保守，不会误放行）。interval <= 0 时回退默认间隔。
//
// stop() 返回时清理协程必然已退出（内部等待其退出信号），因此
// 「停止之后不会再有任何一次 DELETE」是硬保证，而不是时序上的运气：
// 若只 close(done) 就返回，协程可能正停在 select 上且 tick 与 done 同时就绪
// ——Go 的 select 在多个 case 就绪时随机选，于是 stop() 已返回却仍执行一次清理。
// 这在 macOS 上测不出来（调度恰好绕开），在 Linux CI 上稳定复现。
func StartExpiredSessionPurge(db *gorm.DB, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultPurgeInterval
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	safego.Run("session.purge", func() {
		defer ticker.Stop()
		defer close(stopped)
		for {
			// 先非阻塞地看一眼停止信号：已停止就别再等下一次 tick，
			// 免得多做一次没有意义的 DELETE（关停/热重载后不该再动数据库）。
			select {
			case <-done:
				return
			default:
			}
			select {
			case <-ticker.C:
				n, err := PurgeExpiredSessions(db)
				if err != nil {
					log.Printf("[WARN] 定期清理过期会话黑名单失败（下次 tick 会重试，不影响吊销判定）：%v", err)
					continue
				}
				if n > 0 {
					log.Printf("[INFO] 定期清理过期会话黑名单 %d 行", n)
				}
			case <-done:
				return
			}
		}
	})
	return func() {
		once.Do(func() {
			close(done)
			<-stopped // 等到协程真正退出，stop() 之后不可能再有清理动作
		})
	}
}
