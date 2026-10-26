package handler

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ===== 埋点统计（stat_events）的共享件 =====
// 数据流：小程序埋点（visit/call/save/project_view/service_view/video_play/
// share/poster_save）→ POST /api/stat/track（公开，按 IP 限流）→ stat_events
// 原始表 → /api/admin/stats（汇总 + 按员工 + 按天趋势 + 内容意向 TOP 榜，
// 日期范围可调）→ PC 后台「数据统计」页 / CSV 导出。
//
// 两类事件的分工：visit/call/save 是「转化」——有没有人找上门；
// project_view/service_view/video_play/share/poster_save 是「意向」——
// 访客对什么内容感兴趣。后者带 target_id（案例 / 服务 id），后台才能回答
// 「哪个案例热」，这是统计对企业客户的核心价值。

// statEventTypes 埋点类型白名单。
// 为什么不「非空即可」：event_type 是自由字符串列，而查询侧只认这几类做
// SUM(CASE WHEN)。脏值不报错、不影响别的事件，只会让「总量」与「分类」
// 悄悄对不上——典型的静默数据污染。新增事件类型在此登记，查询侧与端上同步跟上。
//
// target_id 约定（列由 migrations/0021 增加）：
//
//	visit        访问名片（首页，按页面实例去重）      targetId=0
//	call         点击拨号                              targetId=0
//	save         点击保存（存通讯录）                   targetId=0
//	project_view 浏览案例详情                          targetId=案例ID（强制，须同租户）
//	service_view 浏览服务详情                          targetId=服务ID（强制，须同租户）
//	video_play   播放公司视频（组件级去重）              targetId=0
//	share        转发（名片/案例/服务）                 targetId=0 或对象ID（不强制存在）
//	poster_save  保存海报                              targetId=0
//
// project_view/service_view 为什么强制校验 target：这两个事件的 target_id
// 直接进后台「内容意向 TOP 榜」的对象名 JOIN，伪造一个别家 targetId 会把
// 别家的案例名泄进本家榜单——必须在进水口挡掉（见 validateStatTarget）。
var statEventTypes = map[string]bool{
	"visit":        true, // 访问名片（小程序首页，按页面实例去重）
	"call":         true, // 点击拨号
	"save":         true, // 点击保存
	"project_view": true, // 浏览案例详情（targetId=案例ID）
	"service_view": true, // 浏览服务详情（targetId=服务ID）
	"video_play":   true, // 播放公司视频
	"share":        true, // 转发（targetId=0 名片 / 案例ID / 服务ID）
	"poster_save":  true, // 保存海报
}

// statSourceTypes 流量来源白名单（与 stat_events.source 列的取值一一对应）。
//
// 为什么需要白名单归一：source 来自客户端（小程序按微信 scene / 海报标记上报），
// 永不可信——任何脚本都能在 track 请求里塞任意 source 字符串。若直接落库，
// 脏值会悄悄混进「来源分布」把图玩坏（静默数据污染）。normalizeSource 把
// 取值钳进这五个已知类别 + 两个兜底（unknown/other），分布图永远干净。
//
// 重要：source 只作「统计维度」，绝不参与权限 / 租户 / 计费任何判断——
// 即使有人伪造 source='vip' 也只会在统计里多一行 'other'，不会改变任何业务结果。
//
//	scan        扫码（二维码 / 小程序码，含海报上的码）
//	share_card  转发卡片（聊天/群聊/App/小程序互跳，scene 1007/1008/1036/...）
//	search      微信搜索 / 发现 / 我的小程序等入口
//	poster      扫海报上的小程序码（poster 页生成码时带 &from=poster 标记）
//	unknown     缺省（老客户端 / 历史数据未带 source，落库即空串）
//	other       带了的、但不在白名单里的脏值
var statSourceTypes = map[string]bool{
	"scan":       true,
	"share_card": true,
	"search":     true,
	"poster":     true,
	"unknown":    true,
	"other":      true,
}

// normalizeSource 把客户端上报的 source 钳进白名单。
//   - 空串 → "unknown"（老客户端 / 历史数据兜底，落库即空串也归到这里统计）
//   - 白名单值原样返回
//   - 其余（任何脏值）→ "other"
//
// 抽成独立函数便于单测：verify 纪律要求「摘掉归一逻辑测试应 FAIL」，
// 把归一集中在这里、调用点只剩一行，反向验证才干净。
func normalizeSource(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	if statSourceTypes[s] {
		return s
	}
	return "other"
}

// validateStatTarget 校验埋点的 targetId：project_view / service_view 必须
// 指向一个真实存在、且与员工同租户的业务对象；其余事件只要求非负。
//
// 为什么只有这两类强制：它们的 target_id 直接进后台「内容意向 TOP 榜」的
// 对象名 JOIN——放过一个别家 targetId，别家的案例名就会出现在本家榜单里
// （跨租户信息泄露的静默形态）。visit/video_play/poster_save 天然没有关联
// 对象；share 的 targetId 语义是「转发的名片(0)/案例/服务」，但分享不按
// target 聚合计入 TOP 榜、转发的对象也可能刚被删，故不强制存在性。
//
// 返回值即回包 msg（HTTP 恒 200、code:1）：
//   - 「参数错误」targetId 为负，或强制类事件没带 target；
//   - 「对象不存在」target 查不到，或与员工不同租户（两种都回同一句，
//     不向调用方泄露「别家存在这个 id」）；
//   - 「上报失败」查询本身出错。
func validateStatTarget(db *gorm.DB, eventType string, targetID, companyID int64) error {
	if targetID < 0 {
		return errors.New("参数错误")
	}
	if eventType != "project_view" && eventType != "service_view" {
		return nil
	}
	if targetID <= 0 {
		return errors.New("参数错误")
	}
	var cnt int64
	var err error
	if eventType == "project_view" {
		err = db.Model(&model.ProjectCase{}).
			Where("id = ? AND company_id = ?", targetID, companyID).Count(&cnt).Error
	} else {
		err = db.Model(&model.BizService{}).
			Where("id = ? AND company_id = ?", targetID, companyID).Count(&cnt).Error
	}
	if err != nil {
		return errors.New("上报失败")
	}
	if cnt == 0 {
		return errors.New("对象不存在")
	}
	return nil
}

// statTrackLimitFn 埋点上报限流阈值（每 IP 每分钟事件数）。
//
// 取 60 的考量：一次真实到访最多产生个位数事件（访问 1 + 点击若干），
// 办公网 NAT 下整层人同时扫也远到不了这个量；对脚本刷量（每分钟上千次）
// 则是硬顶。故意不进设置界面——运营没有「放开伪造刷量」的诉求，
// 少一个可调项少一个误操作面。
//
// 抽成包级变量是为了测试能把阈值压低（同 randRead 的间接层手法）。
var statTrackLimitFn = func() int { return 60 }

// statTrackLimiter 埋点接口的限流器：按客户端 IP 分桶 + 数据库共享计数。
// 不按 staffId 分桶——扫号脚本正是靠换 ID 放大调用量，按 ID 分桶等于没限。
//
// 桶键带 track: 前缀：与 login:/refresh: 共用同一张 rate_limit_hits 表，
// 裸 IP 当键会挤进同一行——访客埋点刷够 10 条/分就能把同 IP 的登录打到
// 429（办公网 NAT 共享出口 IP 时可达，2026-10-23 实测抓过）。
func statTrackLimiter(db *gorm.DB) *middleware.RateLimiter {
	return middleware.NewRateLimiter(statTrackLimitFn, 60, time.Minute).
		WithKey(func(c *gin.Context) string { return "track:" + c.ClientIP() }).
		WithMessage("上报过于频繁，请稍后再试").
		WithCounter(dbRateCounter{db: db})
}

// statDateRange 解析统计查询的日期范围（YYYY-MM-DD，闭区间）。
// from/to 均可缺省：缺省表示不设下界 / 上界（「全部时间」口径，保持兼容）。
// ok=false 表示参数非法（格式错、或 2026-02-31 这类不存在的日期、或 from > to），
// 调用方应回 code:1——静默忽略坏参数会让「筛了却没生效」比不筛更难排查。
func statDateRange(c *gin.Context) (from, to string, ok bool) {
	from = c.Query("from")
	to = c.Query("to")
	if from != "" && !validStatDate(from) {
		return "", "", false
	}
	if to != "" && !validStatDate(to) {
		return "", "", false
	}
	// YYYY-MM-DD 的字典序即时间序，字符串比较即可（无需再解析一次）
	if from != "" && to != "" && from > to {
		return "", "", false
	}
	return from, to, true
}

// validStatDate 严格校验 YYYY-MM-DD。time.Parse 会挡掉 2026-02-31 与
// 2026-13-01——这类值若进了查询条件，在两种数据库上都是「查不到」而非报错，
// 表现出来就是「统计莫名少一块」。
func validStatDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// statStaffRow 按员工聚合的一行：员工资料 + 计数。
// 前三个（visit/call/save）是转化，后五个是意向（见文件头两类事件的分工）。
// 意向计数进 byStaff 与 CSV 导出，但 PC 表格不展示（列宽装不下）——
// 后台要看「哪个员工带来的内容意向多」时导出来看。
// 零数据员工也出现在结果里（计数 0）——「有员工但没访问」是运营要看到的事实。
type statStaffRow struct {
	StaffID     int64
	Name        string
	Title       string
	Avatar      string
	Visit       int64
	Call        int64
	Save        int64
	ProjectView int64
	ServiceView int64
	VideoPlay   int64
	Share       int64
	PosterSave  int64
}

// statRowsByStaff 取「当前后台用户可见范围」内员工的统计聚合（按租户隔离）。
//
// 租户范围用子查询（staff_id IN (SELECT staff_id FROM employees ...)）而不是
// 先查员工再拼 IN 列表：员工数随运营增长，IN 列表会越来越长，而子查询在两种
// 方言上同形态。stat_events 表本身不带 company_id（埋点只有 staffId），
// 租户隔离正是通过这个子查询建立的。
//
// 聚合别名一律 snake_case：Scan 按字段的 DBName（NamingStrategy 由 CamelCase
// 转来）匹配，多词字段写驼峰别名（projectView）会静默扫不进来——计数恒 0、
// 不报错，2026-10-23 实测抓过。
func statRowsByStaff(c *gin.Context, db *gorm.DB, from, to string) ([]statStaffRow, error) {
	var emps []model.Employee
	if err := db.Scopes(scopeCond(c)).Order("staff_id asc").Find(&emps).Error; err != nil {
		return nil, err
	}
	agg := map[int64]statStaffRow{}
	if len(emps) > 0 {
		var rows []statStaffRow
		q := db.Model(&model.StatEvent{}).
			Where("staff_id IN (?)", db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
			Select("staff_id, " +
				"SUM(CASE WHEN event_type = 'visit' THEN 1 ELSE 0 END) AS `visit`, " +
				"SUM(CASE WHEN event_type = 'call' THEN 1 ELSE 0 END) AS `call`, " +
				"SUM(CASE WHEN event_type = 'save' THEN 1 ELSE 0 END) AS `save`, " +
				"SUM(CASE WHEN event_type = 'project_view' THEN 1 ELSE 0 END) AS `project_view`, " +
				"SUM(CASE WHEN event_type = 'service_view' THEN 1 ELSE 0 END) AS `service_view`, " +
				"SUM(CASE WHEN event_type = 'video_play' THEN 1 ELSE 0 END) AS `video_play`, " +
				"SUM(CASE WHEN event_type = 'share' THEN 1 ELSE 0 END) AS `share`, " +
				"SUM(CASE WHEN event_type = 'poster_save' THEN 1 ELSE 0 END) AS `poster_save`").
			Group("staff_id")
		if from != "" {
			q = q.Where("stat_date >= ?", from)
		}
		if to != "" {
			q = q.Where("stat_date <= ?", to)
		}
		if err := q.Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			agg[r.StaffID] = r
		}
	}
	out := make([]statStaffRow, 0, len(emps))
	for _, e := range emps {
		r := agg[e.StaffID] // 查不到 = 该员工在范围内零事件，天然补 0
		out = append(out, statStaffRow{
			StaffID: e.StaffID, Name: e.Name, Title: e.Title, Avatar: e.Avatar,
			Visit: r.Visit, Call: r.Call, Save: r.Save,
			ProjectView: r.ProjectView, ServiceView: r.ServiceView,
			VideoPlay: r.VideoPlay, Share: r.Share, PosterSave: r.PosterSave,
		})
	}
	return out, nil
}

// statTrendRow 按天聚合的一行（升序返回）。
// 后五项与 statStaffRow 的意向计数同源：趋势图上「访客什么时候对什么感兴趣」
// 与按员工的分布是两个维度，两边的口径必须一致（都按同一事件类型白名单聚合）。
type statTrendRow struct {
	Day         string `gorm:"column:day"`
	Visit       int64  `gorm:"column:visit"`
	Call        int64  `gorm:"column:call"`
	Save        int64  `gorm:"column:save"`
	ProjectView int64  `gorm:"column:project_view"`
	ServiceView int64  `gorm:"column:service_view"`
	VideoPlay   int64  `gorm:"column:video_play"`
	Share       int64  `gorm:"column:share"`
	PosterSave  int64  `gorm:"column:poster_save"`
}

// statDateExpr 把 stat_date 归一成 YYYY-MM-DD 字符串的 SQL 表达式。
//
// 为什么不直接 Select("stat_date")：MySQL 的 stat_date 是 DATE 列，读回来是
// time.Time，GORM 扫进 string 字段会格式化成 RFC3339（2026-10-23T00:00:00Z，
// parity 首跑实测抓到）；而 SQLite 侧模型是 string、存的就是 'YYYY-MM-DD' 原样。
// 同一个字段两种形状，前端按日期渲染就会一半带 T 一半不带。
// CAST(... AS CHAR) 在两边都得到 'YYYY-MM-DD'（MySQL 8.0.46 / SQLite 实测），
// 故用一个表达式通吃，不引方言分支。分组与排序仍走原始列（DATE/TEXT 排序等价）。
const statDateExpr = "CAST(stat_date AS CHAR)"

// statTrend 按天趋势（同一日期口径 + 同一租户范围）。
// 只回有事件的日期，无事件日期由前端在首尾之间补零——避免两边各写一套日期推导。
//
// 八个指标一次聚合：前端图例可点选（默认只画转化三线），故这里必须全给，
// 否则「勾了案例浏览却没线」就成了静默缺数据。
func statTrend(c *gin.Context, db *gorm.DB, from, to string) ([]statTrendRow, error) {
	var rows []statTrendRow
	q := db.Model(&model.StatEvent{}).
		Where("staff_id IN (?)", db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
		Select(statDateExpr+" AS `day`, "+
			"SUM(CASE WHEN event_type = 'visit' THEN 1 ELSE 0 END) AS `visit`, "+
			"SUM(CASE WHEN event_type = 'call' THEN 1 ELSE 0 END) AS `call`, "+
			"SUM(CASE WHEN event_type = 'save' THEN 1 ELSE 0 END) AS `save`, "+
			// 注意：别名一律 snake_case。GORM Scan 按字段 DBName 匹配列名，
			// 写成驼峰（projectView）会**静默扫不进来、计数恒 0 不报错**。
			"SUM(CASE WHEN event_type = 'project_view' THEN 1 ELSE 0 END) AS `project_view`, "+
			"SUM(CASE WHEN event_type = 'service_view' THEN 1 ELSE 0 END) AS `service_view`, "+
			"SUM(CASE WHEN event_type = 'video_play' THEN 1 ELSE 0 END) AS `video_play`, "+
			"SUM(CASE WHEN event_type = 'share' THEN 1 ELSE 0 END) AS `share`, "+
			"SUM(CASE WHEN event_type = 'poster_save' THEN 1 ELSE 0 END) AS `poster_save`").
		Group("stat_date").Order("stat_date asc")
	if from != "" {
		q = q.Where("stat_date >= ?", from)
	}
	if to != "" {
		q = q.Where("stat_date <= ?", to)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ===== 内容意向 TOP 榜 =====

// statTopLimit TOP 榜默认条数。10 条一屏放得下，也够运营看出头部内容；
// 全量榜没有终端场景（要看全部走明细筛 eventType + targetId 下钻）。
const statTopLimit = 10

// statTopRow TOP 榜一行：业务对象 + 其事件数。
type statTopRow struct {
	TargetID int64  `gorm:"column:target_id"`
	Name     string `gorm:"column:name"`
	Count    int64  `gorm:"column:cnt"`
}

// statTopTargets 「内容意向 TOP 榜」：某类内容事件按 target 聚合取前 limit 个。
//
// 参数 table/nameCol 由调用方写死传入（project_cases.name /
// biz_services.title），不是用户输入——本函数只有两个调用点。
//
// 租户隔离：事件先经 staff_id IN (当前可见员工) 圈进本租户；target 与员工
// 的同租户关系在进水口已强制（validateStatTarget），故按 id JOIN 对象名
// 不会带出别家数据。
//
// 为什么 LEFT JOIN 且带 deleted_at IS NULL：对象被删后，其历史浏览仍要
// 留在榜上（名字回落「已删除」）。若用内连接把被删对象的事件剔掉，TOP 榜
// 各行数字加起来就对不上汇总里的事件总数——「汇总说 100、榜单加起来 92」
// 是最难排查的一类对账事故（同明细与汇总的口径约定）。
func statTopTargets(c *gin.Context, db *gorm.DB, from, to, eventType, table, nameCol string, limit int) ([]statTopRow, error) {
	var rows []statTopRow
	q := db.Table("stat_events").
		Joins("LEFT JOIN " + table + " ON " + table + ".id = stat_events.target_id AND " + table + ".deleted_at IS NULL").
		Where("stat_events.event_type = ?", eventType).
		Where("stat_events.staff_id IN (?)",
			db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
		Select("stat_events.target_id AS target_id, " +
			"COALESCE(" + table + "." + nameCol + ", '已删除') AS `name`, " +
			"COUNT(*) AS cnt").
		Group("stat_events.target_id, " + table + "." + nameCol).
		// 次级排序固定 target_id：同票数时榜单顺序稳定（分页之外也让测试可断言）
		Order("cnt DESC, stat_events.target_id ASC").
		Limit(limit)
	if from != "" {
		q = q.Where("stat_date >= ?", from)
	}
	if to != "" {
		q = q.Where("stat_date <= ?", to)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ===== 流量来源分布 + 裂变榜 =====

// statSourceLimit / statFissionLimit 两条榜的默认条数（与 TOP 榜同档，一屏放得下）。
const (
	statSourceLimit  = 6 // 来源类别固定 6 个（含 unknown/other 兜底），多给也无意义
	statFissionLimit = 10
)

// statSourceRow 来源分布的一行：来源类别 + 其 visit 事件数。
type statSourceRow struct {
	Source string `gorm:"column:source"`
	Count  int64  `gorm:"column:cnt"`
}

// statSourceDist 流量来源分布：对 visit（进入名片的入口事件）按 source 分组计数。
//
// 为什么只看 visit：source 的语义是「这次进入是从哪来的」，只有入口事件
// 携带有意义的来源；call/save/project_view 等是进入之后的行为，其 source
// 只是复用了进入时的上下文，聚合它们会重复计数把分布放大。
//
// 空串兜底：历史库（迁移 0022 之前）的 source 为空，按 '' 分组会多出一个
// 空桶——用 CASE WHEN 归一成 'unknown'，与 normalizeSource 的缺省值对齐，
// 历史数据自然落到 unknown 桶，分布图不出现裸空串。CASE WHEN 在 MySQL /
// SQLite 同形态，不引方言分支。
//
// 租户隔离：事件先经 staff_id IN (当前可见员工) 圈进本租户，与汇总/趋势一致。
func statSourceDist(c *gin.Context, db *gorm.DB, from, to string) ([]statSourceRow, error) {
	var rows []statSourceRow
	q := db.Model(&model.StatEvent{}).
		Where("event_type = ?", "visit").
		Where("staff_id IN (?)", db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
		Select("CASE WHEN source = '' THEN 'unknown' ELSE source END AS `source`, COUNT(*) AS cnt").
		Group("source").
		Order("cnt DESC")
	if from != "" {
		q = q.Where("stat_date >= ?", from)
	}
	if to != "" {
		q = q.Where("stat_date <= ?", to)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// statFissionRow 裂变榜的一行：被转发的员工 + 其名片靠转发带来的新访问数。
type statFissionRow struct {
	StaffID int64  `gorm:"column:staff_id"`
	Name    string `gorm:"column:name"`
	Count   int64  `gorm:"column:cnt"`
}

// statFissionTop 裂变榜：按 staff_id 聚合并发榜「谁的名片被转发带来的新访客最多」。
//
// 裂变归因复用现有分享链路，无需新增 ref_staff_id 字段：访客经由分享卡片打开时，
// 微信 scene 落入 share_card 区间（1007/1008/1036/...），小程序据此把该次 visit
// 的 source 记为 'share_card'、staff_id 仍是「被分享的那张名片的主人」。于是
// 「某员工 + source=share_card 的 visit 数」就是他名片的裂变贡献——
// 分享者本人无需单独记录，归因落在被分享的名片上（这正是运营要的排行）。
//
// 为什么 LEFT JOIN employees 取 name：staff_id 已在前置 IN 子查询里圈进本租户，
// JOIN 只是补名字；被删员工的事件仍保留计数（name 回落「已删除」），
// 与 TOP 榜「汇总与榜单对得上」的口径约定一致。
func statFissionTop(c *gin.Context, db *gorm.DB, from, to string, limit int) ([]statFissionRow, error) {
	var rows []statFissionRow
	// 注意：employees 表无 deleted_at 列（员工是硬删除），故 JOIN 不带
	// deleted_at 条件；被删员工在 employees 里已无行，LEFT JOIN 得 name=NULL，
	// COALESCE 回落「已删除」，计数不减（与 TOP 榜对账约定一致）。
	q := db.Table("stat_events").
		Joins("LEFT JOIN employees ON employees.staff_id = stat_events.staff_id").
		Where("stat_events.event_type = ?", "visit").
		Where("stat_events.source = ?", "share_card").
		Where("stat_events.staff_id IN (?)",
			db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
		Select("stat_events.staff_id AS staff_id, COALESCE(employees.name, '已删除') AS `name`, COUNT(*) AS cnt").
		Group("stat_events.staff_id, employees.name").
		Order("cnt DESC, stat_events.staff_id ASC").
		Limit(limit)
	if from != "" {
		q = q.Where("stat_date >= ?", from)
	}
	if to != "" {
		q = q.Where("stat_date <= ?", to)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	// employees.name 已加密，裸 SQL 不经过模型 AfterFind，这里显式解密；
	// COALESCE 回落的「已删除」不是密文，decPII 原样返回。
	for i := range rows {
		rows[i].Name = decPII(rows[i].Name)
	}
	return rows, nil
}

// ===== 埋点明细列表 =====

// statEventRaw 明细查询的原始行（数据库形状，供 Scan）。
// stat_date 走 CAST 归一的理由同 statTrend（MySQL DATE 列扫进 string 会变 RFC3339）。
// employees.name 由 JOIN 带出。用普通（非 LEFT）JOIN 是故意的：WHERE 子查询
// 已把范围圈死在「当前存在的员工」上，员工不存在时其事件在明细与汇总中
// 一致地不计入——明细是汇总的下钻，两边必须能对账（汇总说 100、明细列出
// 105 条是最难排查的一类事故）。
type statEventRaw struct {
	ID        int64     `gorm:"column:id"`
	StaffID   int64     `gorm:"column:staff_id"`
	Name      string    `gorm:"column:name"`
	EventType string    `gorm:"column:event_type"`
	TargetID  int64     `gorm:"column:target_id"`
	StatDate  string    `gorm:"column:stat_date"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// statDetailItem 明细回包一行。
// CreatedAt 是服务端格式化好的 "YYYY-MM-DD HH:mm:ss" 字符串：时区以服务端 TZ
// 为权威口径（stat_date 的「今天」同样按它落库，见 compose TZ 约定）。
// 前端若再按浏览器时区换算，跨时区使用时明细时间会与趋势图日期对不上。
// TargetName 是「对象」列（内容事件的案例/服务名），语义见 statTargetName。
type statDetailItem struct {
	ID         int64  `json:"id"`
	StaffID    int64  `json:"staffId"`
	Name       string `json:"name"`
	EventType  string `json:"eventType"`
	TargetID   int64  `json:"targetId"`
	TargetName string `json:"targetName"`
	StatDate   string `json:"statDate"`
	CreatedAt  string `json:"createdAt"`
}

// statTargetNames 一页内批量补查对象名：两种表各一次 IN 查询（一页最多 100 行，
// 比逐行查省 99 轮往返）。查不到的 id 不在返回的 map 里，由 statTargetName
// 回落「已删除」——对象被删后历史事件仍要出现在明细里：明细是汇总的下钻，
// 行数必须与汇总额对得上，不能因为对象没了就把事件吞掉。
//
// 补查失败（db 错误）返回 nil：名字列整体回落「已删除」，但事件数据不丢——
// 明细列表本身的价值高于名字列，不应因附带的展示查询失败而整页报错。
func statTargetNames(db *gorm.DB, events []statEventRaw) (caseNames, svcNames map[int64]string) {
	caseIDs, svcIDs := map[int64]bool{}, map[int64]bool{}
	for _, e := range events {
		if e.TargetID <= 0 {
			continue
		}
		switch e.EventType {
		case "project_view":
			caseIDs[e.TargetID] = true
		case "service_view":
			svcIDs[e.TargetID] = true
		case "share":
			// 转发的目标可能是案例也可能是服务（进水口不强制存在性），两张表都查
			caseIDs[e.TargetID] = true
			svcIDs[e.TargetID] = true
		}
	}
	lookup := func(ids map[int64]bool, dst interface{}) map[int64]string {
		if len(ids) == 0 {
			return nil
		}
		list := make([]int64, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		out := map[int64]string{}
		// 软删除作用域由 GORM 自动加（两模型都带 DeletedAt）：已删对象查不到，
		// 与 statTopTargets 的 LEFT JOIN + deleted_at IS NULL 同口径
		if err := db.Where("id IN (?)", list).Find(dst).Error; err != nil {
			return nil
		}
		switch v := dst.(type) {
		case *[]model.ProjectCase:
			for _, r := range *v {
				out[r.ID] = r.Name
			}
		case *[]model.BizService:
			for _, r := range *v {
				out[r.ID] = r.Title
			}
		}
		return out
	}
	var pcs []model.ProjectCase
	var svcs []model.BizService
	return lookup(caseIDs, &pcs), lookup(svcIDs, &svcs)
}

// statTargetName 明细行的「对象」列取值：
//   - targetId=0（visit/video_play/poster_save/转发名片）无关联对象 → 占位符；
//   - project_view/service_view 取对应表的名字；对象已删 → 「已删除」；
//   - share 的 targetId 可能是案例也可能是服务，案例优先（纯展示用途，
//     分享不按 target 聚合计入 TOP 榜，撞 id 也不会污染任何统计口径）。
func statTargetName(eventType string, targetID int64, caseNames, svcNames map[int64]string) string {
	if targetID <= 0 {
		return "—"
	}
	switch eventType {
	case "project_view":
		if n, ok := caseNames[targetID]; ok {
			return n
		}
	case "service_view":
		if n, ok := svcNames[targetID]; ok {
			return n
		}
	case "share":
		if n, ok := caseNames[targetID]; ok {
			return n
		}
		if n, ok := svcNames[targetID]; ok {
			return n
		}
	default:
		// 不带 target 的事件理论上 targetId 恒 0；真出现正值也不冒充对象名
		return "—"
	}
	return "已删除"
}

// RegisterStatsDetail 注册埋点明细列表（GET /api/admin/stats/detail，按租户隔离）。
//
// 与 /api/admin/stats（聚合）互补：汇回答「谁有多少」，明答「都有什么」——
// 排查「某个员工的访问量对不上」时必须能下钻到逐条事件，否则聚合数只是
// 一个无法验证的黑盒。分页信封沿用全站约定 {code:0,data,total,page,size}
// （pagination.go 是唯一权威实现）。
//
// 筛选参数均可缺省（缺省即不过滤）：
//   from/to   日期范围，复用 statDateRange 的严格校验（坏值 code:1，不静默忽略）
//   staffId   指定员工（别家员工的 id 查过来是空列表——租户隔离由子查询保证）
//   eventType 指定事件类型，白名单外拒收（同埋点进水口：脏值会让「筛了却没生效」）
//   targetId  指定内容对象（配 eventType=project_view/service_view 下钻 TOP 榜）
func RegisterStatsDetail(r *gin.Engine, db *gorm.DB) {
	r.GET("/api/admin/stats/detail", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		from, to, ok := statDateRange(c)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "日期参数错误"})
			return
		}
		eventType := c.Query("eventType")
		if eventType != "" && !statEventTypes[eventType] {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "事件类型参数错误"})
			return
		}
		var staffID int64
		if v := c.Query("staffId"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工参数错误"})
				return
			}
			staffID = n
		}
		var targetID int64
		if v := c.Query("targetId"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "对象参数错误"})
				return
			}
			targetID = n
		}

		page, size := pageParams(c)
		q := db.Model(&model.StatEvent{}).
			// 租户隔离：stat_events 不带 company_id，仍靠员工子查询建立边界
			Where("stat_events.staff_id IN (?)",
				db.Model(&model.Employee{}).Select("staff_id").Scopes(scopeCond(c))).
			// JOIN 只为带出姓名（一页最多 100 行，比二次查 ID 列表省一轮往返）。
			// 隔离不依赖 JOIN——WHERE 子查询已圈死范围；员工不存在时事件在
			// 汇总里同样不计（byStaff 从员工表出发），两边口径一致才能对账
			Joins("JOIN employees ON employees.staff_id = stat_events.staff_id").
			Select("stat_events.id, stat_events.staff_id, employees.name, " +
				"stat_events.event_type, stat_events.target_id, " +
				statDateExpr + " AS stat_date, stat_events.created_at")
		if from != "" {
			q = q.Where("stat_date >= ?", from)
		}
		if to != "" {
			q = q.Where("stat_date <= ?", to)
		}
		if staffID > 0 {
			q = q.Where("stat_events.staff_id = ?", staffID)
		}
		if eventType != "" {
			q = q.Where("event_type = ?", eventType)
		}
		if targetID > 0 {
			q = q.Where("stat_events.target_id = ?", targetID)
		}

		var events []statEventRaw
		total, page := pagedQuery(q, "stat_events.id desc", &events, page, size)
		// 明细里的员工姓名同样来自裸 SQL 的 JOIN，需显式解密（见 statFissionTop 同款处理）。
		for i := range events {
			events[i].Name = decPII(events[i].Name)
		}

		caseNames, svcNames := statTargetNames(db, events)
		list := make([]statDetailItem, 0, len(events))
		for _, e := range events {
			list = append(list, statDetailItem{
				ID: e.ID, StaffID: e.StaffID, Name: e.Name,
				EventType: e.EventType, TargetID: e.TargetID,
				TargetName: statTargetName(e.EventType, e.TargetID, caseNames, svcNames),
				StatDate:   e.StatDate,
				CreatedAt:  e.CreatedAt.Format("2006-01-02 15:04:05"),
			})
		}
		pagedOK(c, list, total, page, size)
	})
}

// RegisterStatsExport 注册统计 CSV 导出（超管，按租户隔离）。
// 与 GET /api/admin/stats 同一套口径（scoped 员工 + stat_date 范围 + 零数据补 0），
// 只是把表格搬进 Excel：运营做月报、或客户要求「把数据给我自己看」的场景都要这个。
func RegisterStatsExport(r *gin.Engine, db *gorm.DB) {
	r.GET("/api/admin/stats/export", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		from, to, ok := statDateRange(c)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "日期参数错误"})
			return
		}
		rows, err := statRowsByStaff(c, db, from, to)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		body, err := statsCSV(rows)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "导出失败"})
			return
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", statsDisposition(from, to))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", body)
	})
}

// statsCSV 生成 CSV 内容。
// 后五列是内容意向（案例浏览/服务浏览/视频播放/转发/海报保存）：PC 表格
// 列宽装不下 11 列，CSV 没有这个限制——「哪个员工带来的内容意向多」
// 就靠导出来看（与 byStaff 接口同源同口径）。
// BOM 不可省：没有它 Excel 双击打开时按本地代码页解析，中文全乱码——
// 而「导出来就是要用 Excel 打开」正是这个接口的存在理由。
func statsCSV(rows []statStaffRow) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"编号", "姓名", "职位", "访问", "拨号", "保存",
		"案例浏览", "服务浏览", "视频播放", "转发", "海报保存"}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		rec := []string{
			strconv.FormatInt(r.StaffID, 10),
			csvCell(r.Name),
			csvCell(r.Title),
			strconv.FormatInt(r.Visit, 10),
			strconv.FormatInt(r.Call, 10),
			strconv.FormatInt(r.Save, 10),
			strconv.FormatInt(r.ProjectView, 10),
			strconv.FormatInt(r.ServiceView, 10),
			strconv.FormatInt(r.VideoPlay, 10),
			strconv.FormatInt(r.Share, 10),
			strconv.FormatInt(r.PosterSave, 10),
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	// Write 的错要到 Flush 之后才在 Error() 上现形：不查就等于把
	// 「导出只剩半行」当成功交付，而下载方无从知道文件被截断。
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// csvCell 防公式注入：以 = + - @ 或制表符 / 回车开头的单元格，Excel / WPS
// 打开时会当公式执行（姓名 / 职位是自由文本，建一个名叫
// =cmd|'/c calc'!A1 的员工就能在运营的电脑上跑东西）。加 ' 前缀是最通用的解法。
// 逗号 / 引号 / 换行的转义由 encoding/csv 负责（自动加引号包裹）。
func csvCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// statsDisposition 下载文件名。中文名走 RFC 5987 的 filename*（百分号编码），
// 同时留一个 ASCII filename 兜底（不认 filename* 的老客户端）。
func statsDisposition(from, to string) string {
	name := "名片统计"
	switch {
	case from != "" && to != "":
		name += "_" + from + "_" + to
	case from != "":
		name += "_" + from + "_起"
	case to != "":
		name += "_至_" + to
	default:
		name += "_全部"
	}
	name += ".csv"
	return "attachment; filename=\"stats.csv\"; filename*=UTF-8''" + url.QueryEscape(name)
}
