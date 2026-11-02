package license

import (
	"fmt"
	"log"
	"sync"
	"time"

	"weichuan/internal/audit"

	"gorm.io/gorm"
)

// 授权到期提醒（P0-3）。
//
// 要解决的问题：授权到期会**自动**降级只读（甚至私有部署直接起不来），但没有任何
// 东西会**主动**告诉任何人。客户是「某天上午突然改不了内容」才知道的，运营是
// 「客户打电话来问」才知道的。自动降权 + 无人知晓 = 客户以为系统坏了。
//
// 三层提醒，本文件负责最底层的那一层（服务端扫描 → 日志 + 审计）：
//  1. 客户：登录后台即见顶部横幅（handler 的 /api/admin/license 提供数据）；
//  2. 运营：平台后台的到期汇总（同上，平台超管视角）；
//  3. 运维：本文件每天把 `[LICENSE][EXPIRING]` 写进日志，由
//     `scripts/alert_check.sh` 的 R5 规则抓取并推送 webhook——它是唯一
//     「不需要有人登录」的通道，也是私有部署（没有平台后台）唯一的指望。
//
// 为什么按「档位变化」记审计而不是每天记一条：审计表是只追加的证据链，不是
// 闹钟。同一个授权连续 20 天「还剩 30 天」记 20 条只会把真正的变更淹掉；
// 而 warn→urgent→expired 每一次跳档都对应一个该做的动作（催款 → 最后通牒 →
// 停服告知），值得留痕。

// DefaultRemindInterval 默认扫描间隔（一天一次）。
// 到期是「天」级事件，扫得更勤没有任何收益，只是每天多几条日志。
const DefaultRemindInterval = 24 * time.Hour

// Reminder 一条「该提醒」的记录。
type Reminder struct {
	// Scope：tenant（某企业）/ platform（平台全局那份授权）。
	Scope       string       `json:"scope"`
	CompanyID   int64        `json:"companyId"`
	CompanyName string       `json:"companyName"`
	Edition     string       `json:"edition"`
	Seats       int          `json:"seats"`
	Status      ExpiryStatus `json:"status"`
}

// Key 用于「档位变化」判定的稳定标识。
func (r Reminder) Key() string {
	if r.Scope == "platform" {
		return "platform"
	}
	return fmt.Sprintf("tenant:%d", r.CompanyID)
}

// CollectExpiring 扫出所有需要提醒的授权（warn / urgent / expired）。
//
// 私有 / 独立部署只有一份授权（data/license.key），且到期即拒绝启动——
// 那种部署下能跑到这个函数，说明还没到期（或刚过期还没重启），提醒仍然有意义。
func CollectExpiring(db *gorm.DB, now time.Time) []Reminder {
	out := make([]Reminder, 0, 4)

	// 1) 全局那份（平台模式来自系统设置，私有模式来自许可证文件）
	if g := Current(); g != nil {
		if st := StatusOf(g, now); st.NeedsAttention() {
			out = append(out, Reminder{
				Scope: "platform", CompanyID: g.TenantID,
				Edition: g.Edition, Seats: g.Seats, Status: st,
			})
		}
	}

	// 私有模式到此为止：单租户，租户那份就是全局那份，重复提醒只会变成两条一模一样的日志。
	if PrivateMode() || db == nil {
		return out
	}

	// 2) 各租户的授权行。LEFT JOIN 是为了不漏「授权行还在、企业被删了」的脏数据
	//    （那种行同样需要有人去清，但不能让它把整个扫描打断）。
	var rows []struct {
		CompanyID int64
		Name      string
		Status    string
		Edition   string
		Seats     int
		ExpiresAt time.Time
	}
	err := db.Table("tenant_license").
		Select("tenant_license.company_id AS company_id, company.name AS name, " +
			"company.status AS status, tenant_license.edition AS edition, " +
			"tenant_license.seats AS seats, tenant_license.expires_at AS expires_at").
		Joins("LEFT JOIN company ON company.id = tenant_license.company_id").
		Scan(&rows).Error
	if err != nil {
		// 扫描失败不该让整个后台任务挂掉：下一轮会重试，日志留痕即可。
		log.Printf("[WARN] 授权到期扫描失败（下轮重试）：%v", err)
		return out
	}

	for _, r := range rows {
		// 停用企业不提醒：它本来就不提供服务，混进提醒列表只会稀释真正的提醒
		// （运营看到 10 条里 6 条是早已停用的企业，就会开始无视这个列表）。
		if r.Status != "" && r.Status != "enabled" {
			continue
		}
		st := StatusOf(&Entitlement{ExpiresAt: r.ExpiresAt}, now)
		if !st.NeedsAttention() {
			continue
		}
		out = append(out, Reminder{
			Scope: "tenant", CompanyID: r.CompanyID, CompanyName: r.Name,
			Edition: r.Edition, Seats: r.Seats, Status: st,
		})
	}
	return out
}

// 进程内的「上次提醒到哪一档」。重启会丢，于是重启后可能多记一条同档审计——
// 这是刻意接受的：多一条「还剩 25 天」比漏掉一次提醒安全得多。
var (
	remindMu   sync.Mutex
	lastLevels = map[string]string{}
)

// RunExpiryReminderOnce 执行一次扫描：打日志 + 档位变化时写审计。
//
// 返回本次需要提醒的条目（便于测试与将来的站内消息复用）。
func RunExpiryReminderOnce(db *gorm.DB, now time.Time) []Reminder {
	items := CollectExpiring(db, now)

	remindMu.Lock()
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		key := it.Key()
		seen[key] = true

		// 日志**每次**都打：alert_check 靠它推送，而「已经过期三个月」这件事
		// 仍然需要每天提醒一次——降级只读的客户每天都在流失订单。
		log.Printf("[LICENSE][EXPIRING] scope=%s company=%d name=%q edition=%s expiresAt=%s daysLeft=%d level=%s",
			it.Scope, it.CompanyID, it.CompanyName, it.Edition,
			it.Status.ExpiresAtDate(), it.Status.DaysLeft, it.Status.Level)

		if lastLevels[key] == it.Status.Level {
			continue // 同一档位已记过，不重复写审计
		}
		lastLevels[key] = it.Status.Level
		audit.Record(db, nil, audit.Entry{
			Action:     audit.ActionLicenseExpiring,
			ActorType:  audit.ActorSystem,
			TargetType: "license",
			TargetID:   targetIDOf(it),
			Detail: map[string]any{
				"scope":     it.Scope,
				"companyId": it.CompanyID,
				"edition":   it.Edition,
				"expiresAt": it.Status.ExpiresAtDate(),
				"daysLeft":  it.Status.DaysLeft,
				"level":     it.Status.Level,
			},
		})
	}
	// 续费之后档位回到 ok/none，把记忆清掉：下次再临近到期要能重新提醒。
	for k := range lastLevels {
		if !seen[k] {
			delete(lastLevels, k)
		}
	}
	remindMu.Unlock()

	return items
}

func targetIDOf(r Reminder) string {
	if r.Scope == "platform" {
		return "platform"
	}
	return fmt.Sprintf("%d", r.CompanyID)
}

// StartExpiryReminder 启动后台定期扫描，返回停止函数。
// 与 audit.StartAuditLogPurge 同构：stop() 返回时协程必然已退出。
//
// interval 来自 LICENSE_REMIND_MINUTES（默认 24h）。下限 1 分钟由 config 保证，
// 这里再兜一次底：0 或负数会让 time.NewTicker panic。
func StartExpiryReminder(db *gorm.DB, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultRemindInterval
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
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
				RunExpiryReminderOnce(db, time.Now())
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

// ResetReminderMemory 清空进程内的档位记忆（仅测试用）。
func ResetReminderMemory() {
	remindMu.Lock()
	defer remindMu.Unlock()
	lastLevels = map[string]string{}
}

// RenewHint 拼「找谁续费」。私有部署与服务商 SaaS 的续费路径完全不同：
// 前者要换 data/license.key 并重启（不换的话下次重启就起不来），
// 后者在后台改授权即时生效、无需重启——写错会让客户白重启一次生产服务。
func RenewHint(private bool) string {
	if private {
		return "请联系服务商续期，拿到新的 data/license.key 后重启服务"
	}
	return "请联系服务商续费，续费后自动恢复，无需重启"
}
