package model

import "time"

// 图片 / 音频内容安全检测记录（media_check）。
//
// 为什么需要一张表：微信的异步检测**只回一个 trace_id**，结论由「消息推送」
// 在几秒到几十秒之后推回来。中间这段时间必须有个地方存住「这张图是哪次送检、
// 属于哪个租户、对象键是什么」——否则回调到达时既对不上图，也没法下架它。
//
// 状态流转（见 MediaCheckStatus）：
//
//	pending ──回调(pass)──→ pass
//	   │  └──回调(review)──→ review
//	   │  └──回调(risky)──→ risky ──后台下架──→ risky + action=takedown
//	   └──送检失败────────→ failed
//
// 刻意**没有**「pending 超时自动标失败」：微信的推送可能晚到很久（重试机制下
// 最长可跨小时），提前标失败会让后来到达的真实结论写不进去（状态机不允许
// 终态回退）。列表页按 created_at 显示「待结论 N 分钟」，由人判断是否异常。
type MediaCheck struct {
	ID        int64 `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CompanyID int64 `gorm:"column:company_id;index" json:"companyId"`

	// URL 是**送检时提交给微信的那个绝对地址**。回调里只有 trace_id，
	// 靠它才能反查到原图；同时它也是「下架」要覆盖的对象定位依据。
	URL string `gorm:"column:url" json:"url"`
	// ObjectKey 存储对象键（如 2026/10/abc.jpg）。在送检时就推导并存下来，
	// 而不是下架时再算——下架是人工触发的，那时候存储驱动/基址可能已经改过，
	// 现场推导有推错的风险，而对象键一旦推错就会把别人的图覆盖掉。
	ObjectKey string `gorm:"column:object_key" json:"objectKey"`

	MediaType int    `gorm:"column:media_type" json:"mediaType"` // 1 音频 / 2 图片
	Scene     int    `gorm:"column:scene" json:"scene"`
	OpenID    string `gorm:"column:openid" json:"-"` // 提交人 openid（不回传后台列表）
	// Source 上传来源：employee / admin / apply。仅用于排查「谁传的」，
	// 不做权限判断——权限仍以 company_id 为准。
	Source string `gorm:"column:source" json:"source"`

	// TraceID 微信侧追踪号。**不建唯一索引**：微信在没收到 success 回包时会
	// 重推同一条推送（最多 3 次），而我们的更新是幂等的（同值覆盖），
	// 不需要靠唯一约束去重；建了唯一索引反而会让「送检失败」这类 trace_id
	// 为空的记录互相冲突（第二条就插不进去）。
	TraceID string `gorm:"column:trace_id;index" json:"traceId"`

	Status  MediaCheckStatus `gorm:"column:status;index" json:"status"`
	Suggest string           `gorm:"column:suggest" json:"suggest"` // 微信原始 suggest（pass/review/risky）
	Label   int              `gorm:"column:label" json:"label"`     // 违规标签
	// Raw 微信回调里的 result / detail 原文。申诉时微信要的就是它，
	// 只存归一化后的 suggest 等于把证据丢了。
	Raw string `gorm:"column:raw;type:text" json:"raw"`
	// Note 跳过 / 失败原因（如 no_push_config / api_error: ...）。
	Note string `gorm:"column:note" json:"note"`

	// Action 处置动作：空 = 未处置，takedown = 已下架（对象已覆盖成占位图）。
	Action    string     `gorm:"column:action" json:"action"`
	HandledAt *time.Time `gorm:"column:handled_at" json:"handledAt"`
	HandlerID int64      `gorm:"column:handler_id" json:"handlerId"` // 操作人 admin.id

	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 固定表名（GORM 默认会推成 media_checks，与迁移脚本不一致）。
func (MediaCheck) TableName() string { return "media_check" }

// MediaCheckStatus 检测状态。
//
// 用 string 而不是 int：这张表是要给人看、要进 SQL 手工排查的，
// 存 "risky" 比存 3 省掉一层「3 是什么来着」的对照。
type MediaCheckStatus string

const (
	MediaCheckPending MediaCheckStatus = "pending" // 已送检，等微信回调
	MediaCheckPass    MediaCheckStatus = "pass"    // 通过
	MediaCheckReview  MediaCheckStatus = "review"  // 疑似违规，微信建议人工复核
	MediaCheckRisky   MediaCheckStatus = "risky"   // 命中违规
	MediaCheckFailed  MediaCheckStatus = "failed"  // 送检失败 / 微信侧检测失败
)

// 处置动作。
const (
	MediaActionNone     = ""
	MediaActionTakedown = "takedown"
)

// Takedownable 这条记录是否还能下架。
//
// 只有「命中违规且尚未处置」的记录才允许下架：
//   - pass / review 下架等于用机器结论误伤正常内容，误判成本由用户承担；
//   - 已下架的不允许重复操作（覆盖是不可逆的，重复点没有意义，只会多一条审计）。
func (m MediaCheck) Takedownable() bool {
	return m.Status == MediaCheckRisky && m.Action != MediaActionTakedown && m.ObjectKey != ""
}

// Settled 是否已有终态结论（用于列表页区分「待结论」）。
func (m MediaCheck) Settled() bool {
	switch m.Status {
	case MediaCheckPass, MediaCheckReview, MediaCheckRisky, MediaCheckFailed:
		return true
	}
	return false
}
