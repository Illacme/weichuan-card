package model

import (
	"time"

	"gorm.io/gorm"
)

// Employee 员工即名片主体；staff_id 为对外唯一编号
// Landline（座机）为员工个人办公电话，与 company_info.tel（公司总机）是两个层级：
// 小程序名片「座机」行优先展示本人的，为空时回退到公司总机。
// CompanyID 为租户外键：员工归属企业；登录令牌携带该值实现后台按租户隔离。
type Employee struct {
	StaffID      int64     `gorm:"primaryKey;autoIncrement;column:staff_id" json:"staffId"`
	CompanyID    int64     `gorm:"column:company_id;index" json:"companyId"`
	Title        string    `gorm:"column:title" json:"title"`       // 职位（对外展示）
	Avatar       string    `gorm:"column:avatar" json:"avatar"`     // 头像
	Landline     string    `gorm:"column:landline" json:"landline"` // 座机（办公号，按业务电话处理，不加密）
	OpenID       string    `gorm:"column:openid" json:"openid"`     // 微信 openid（登录时采集，供后台核实身份）
	PasswordHash string    `gorm:"column:password_hash" json:"-"`
	Status       string    `gorm:"column:status;default:enabled" json:"status"` // enabled / disabled
	TokenVersion int       `gorm:"column:token_version;default:0" json:"-"`     // 安全戳：改密/禁用自增，令旧令牌失效
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`

	// ---- PII 明文：只存在内存，不落库（gorm:"-"）。读写由 pii 钩子在边界加解密，
	//      因此 handler 仍按普通字符串使用这些字段，无感。JSON 名保持不变（对外契约不变）。
	Name     string `gorm:"-" json:"name"`
	Phone    string `gorm:"-" json:"phone"`    // 手机（主号，同时是登录账号）
	Phone2   string `gorm:"-" json:"phone2"`   // 手机 2（选填）：名片「手机」行下方并列展示，不参与登录
	Email    string `gorm:"-" json:"email"`    // 邮箱
	Nickname string `gorm:"-" json:"nickname"` // 微信昵称（登录时授权采集）

	// ---- PII 密文：落库到**原列名**（保证存量列/迁移列一致）+ 盲索引（等值检索用）。
	//      盲索引是 HMAC 派生值，不可逆、泄露「相等」这一点信息，用于登录/后台检索。
	NameEnc      string `gorm:"column:name" json:"-"`
	PhoneEnc     string `gorm:"column:phone" json:"-"`
	Phone2Enc    string `gorm:"column:phone2" json:"-"`
	EmailEnc     string `gorm:"column:email" json:"-"`
	NicknameEnc  string `gorm:"column:nickname" json:"-"`
	NameBidx     string `gorm:"column:name_bidx" json:"-"`
	PhoneBidx    string `gorm:"column:phone_bidx" json:"-"`
	Phone2Bidx   string `gorm:"column:phone2_bidx" json:"-"`
	EmailBidx    string `gorm:"column:email_bidx" json:"-"`
	NicknameBidx string `gorm:"column:nickname_bidx" json:"-"`
}

// Company 租户（企业）。多租户第一期：朗汇机电为 1 号租户，存量数据全部归属。
// 与 CompanyInfo 是 1:1——CompanyInfo.company_id 指向本表主键，企业基础信息
// （全称/简称/地址/电话/LOGO）按租户隔离。
// status: enabled / disabled；停用后该企业的管理员与员工均不能登录（见 handler/auth.go）。
type Company struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name      string    `gorm:"column:name" json:"name"`
	ShortName string    `gorm:"column:short_name" json:"shortName"` // 企业简称：PC 后台侧边栏品牌名 + 公司信息简称的初始值；公司信息里的简称仅用于小程序展示，两者相互独立
	Status    string    `gorm:"column:status;default:enabled" json:"status"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName 显式指定单数表名：GORM 默认会把 Company 复数化为 companies，
// 与 migrations/0011 的 company 表不一致（开发/生产两张名）。
func (Company) TableName() string { return "company" }

// CompanyInfo 企业基础信息（首页固定展示）。
// company_id 为租户外键（唯一）：一个企业一行；历史 id=1 单行迁移为 company_id=1（朗汇）。
type CompanyInfo struct {
	ID        int64  `gorm:"primaryKey;column:id" json:"id"`
	CompanyID int64  `gorm:"column:company_id;uniqueIndex" json:"companyId"`
	FullName  string `gorm:"column:full_name" json:"fullName"`
	ShortName string `gorm:"column:short_name" json:"shortName"` // 企业简称（小程序顶栏居中展示）
	Address   string `gorm:"column:address" json:"address"`
	Tel       string `gorm:"column:tel" json:"tel"`
	Logo      string `gorm:"column:logo" json:"logo"`
}

// CompanyProfile 公司简介 + 视频（按租户隔离，company_id 唯一）
//
// videoSource 决定小程序端渲染哪一档（取值见下方 VideoSource* 常量，空串等同 none）：
//
//	none     不展示视频，显示封面占位（默认）
//	channels 微信视频号：<channel-video feed-id>；存储与流量全由微信承担，且官方声明「组件调用无资质要求」
//	         同主体填 videoFeedId（可自动播放）；跨主体改填 videoFeedToken——
//	         feed-id 官方明确「仅同主体时生效」，跨主体必须走 feed-token（基础库 2.31.1+，
//	         非个人主体小程序，且强制 autoplay=false）。两者二选一，feedId 优先。
//	upload   云存储上传：文件落在已配置的对象存储（OSS/COS/MinIO），直传不经应用服务器
//	url      外部 MP4 直链：客户自备 URL，流量与视听类目资质由客户自行承担
//	tencent  腾讯视频插件（AppID wxa75efa648b60994b）：预留档——插件必须在 app.json 静态声明，
//	         故默认不可选，需客户小程序后台申请通过后再由部署方接线，避免未授权拖垮整个小程序
//
// videoUrl 由 upload / url 两档共用，同时充当 channels / tencent 开启 videoFallbackUrl 时的兜底源。
type CompanyProfile struct {
	ID             int64  `gorm:"primaryKey;column:id" json:"id"`
	CompanyID      int64  `gorm:"column:company_id;uniqueIndex" json:"companyId"`
	IntroText      string `gorm:"column:intro_text;type:text" json:"introText"`
	VideoSource    string `gorm:"column:video_source" json:"videoSource"`
	VideoChannelID string `gorm:"column:video_channel_id" json:"videoChannelId"`
	VideoFeedID    string `gorm:"column:video_feed_id" json:"videoFeedId"`
	// 跨主体兜底：视频号与我们小程序不同主体时，feed-id 无效，必须改用 feed-token。
	// 二者二选一，feedId 优先（同主体可自动播放，体验更好）。
	VideoFeedToken   string `gorm:"column:video_feed_token" json:"videoFeedToken"`
	VideoVid         string `gorm:"column:video_vid" json:"videoVid"`
	VideoURL         string `gorm:"column:video_url" json:"videoUrl"`
	VideoFallbackURL bool   `gorm:"column:video_fallback_url;default:false" json:"videoFallbackUrl"`
	Cover            string `gorm:"column:cover" json:"cover"`
}

// 视频来源枚举（company_profile.video_source）。
// 新增档位必须同步：handler/content.go 的 PUT 入参、小程序 company-video 组件、后台 CompanyProfile.vue。
const (
	VideoSourceNone     = "none"     // 不展示视频，显示封面占位
	VideoSourceChannels = "channels" // 微信视频号（推荐：零存储零流量零资质）
	VideoSourceUpload   = "upload"   // 云存储上传（对象存储，客户自有账号）
	VideoSourceURL      = "url"      // 外部 MP4 直链
	VideoSourceTencent  = "tencent"  // 腾讯视频插件（预留，默认不可选）
)

// IsValidVideoSource 校验 video_source 取值。空串按 none 处理：
// 该列是后加的，历史行没有值，不能因为缺省就把保存整个拒掉。
func IsValidVideoSource(s string) bool {
	switch s {
	case "", VideoSourceNone, VideoSourceChannels, VideoSourceUpload, VideoSourceURL, VideoSourceTencent:
		return true
	}
	return false
}

// AchievementCategory 经典业绩分类（种子 8 类，按租户隔离）
type AchievementCategory struct {
	ID        int64          `gorm:"primaryKey;column:id" json:"id"`
	CompanyID int64          `gorm:"column:company_id;index" json:"companyId"`
	Name      string         `gorm:"column:name" json:"name"`
	Icon      string         `gorm:"column:icon" json:"icon"`
	Sort      int            `gorm:"column:sort" json:"sort"`
	IsLocked  bool           `gorm:"column:is_locked;default:false" json:"isLocked"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at" json:"-"`
}

// ProjectCase 项目案例（按租户隔离）
type ProjectCase struct {
	ID             int64          `gorm:"primaryKey;column:id" json:"id"`
	CompanyID      int64          `gorm:"column:company_id;index" json:"companyId"`
	CategoryID     int64          `gorm:"column:category_id;index" json:"categoryId"`
	Name           string         `gorm:"column:name" json:"name"`
	Summary        string         `gorm:"column:summary" json:"summary"`
	Detail         string         `gorm:"column:detail;type:text" json:"detail"`
	Cover          string         `gorm:"column:cover" json:"cover"`
	Album          string         `gorm:"column:album;type:json" json:"-"` // 存 JSON 数组字符串
	IsHomeFeatured bool           `gorm:"column:is_home_featured;default:false" json:"isHomeFeatured"`
	Enabled        bool           `gorm:"column:enabled;default:true" json:"enabled"`
	Sort           int            `gorm:"column:sort" json:"sort"`
	CreatedAt      time.Time      `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at" json:"-"`

	// CategoryName 非持久化：列表/详情接口回填分类名，避免前端二次查询
	// （见《跳转页组件与接口字段映射》§4）。gorm:"-" 让 GORM 忽略它。
	CategoryName string `gorm:"-" json:"categoryName,omitempty"`
}

// BizService 业务服务（富文本含视频号块，按租户隔离）
type BizService struct {
	ID        int64          `gorm:"primaryKey;column:id" json:"id"`
	CompanyID int64          `gorm:"column:company_id;index" json:"companyId"`
	Title     string         `gorm:"column:title" json:"title"`
	Icon      string         `gorm:"column:icon" json:"icon"` // 业务服务图标（首页服务区卡片展示）
	Content   string         `gorm:"column:content;type:text" json:"content"`
	Sort      int            `gorm:"column:sort" json:"sort"`
	CreatedAt time.Time      `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at" json:"-"`
}

// HomeLayout 首页板块顺序 + 首页背景（JSON 数组，按租户隔离，company_id 唯一）
type HomeLayout struct {
	ID        int64  `gorm:"primaryKey;column:id" json:"id"`
	CompanyID int64  `gorm:"column:company_id;uniqueIndex" json:"companyId"`
	Order     string `gorm:"column:order_col;type:json" json:"order"`
	// BgImage 小程序首页全屏背景图 URL（可取自内置素材库「首页背景」分类）。
	// 空串表示不设置背景——保持原有灰底布局（背景是可选增强，不是必填项）。
	BgImage string `gorm:"column:bg_image" json:"bgImage"`
	// BgMask 背景遮罩浓度 0~100：背景图上叠加的黑色蒙层不透明度，
	// 用于压暗花哨的背景、突出白色内容卡片与文字（0=不压暗，默认）。
	BgMask int `gorm:"column:bg_mask;default:0" json:"bgMask"`
}

// StatEvent 埋点原始事件
type StatEvent struct {
	ID        int64  `gorm:"primaryKey;column:id;autoIncrement" json:"id"`
	StaffID   int64  `gorm:"column:staff_id;index" json:"staffId"`
	EventType string `gorm:"column:event_type;index" json:"eventType"`
	// TargetID 关联业务对象的 id（案例 / 服务），无关联对象的事件记 0。
	// 事件类型清单与各类型的 target 约定见 stat.go 的 statEventTypes 注释；
	// 列由 migrations/0021 增加（MySQL），SQLite 走 AutoMigrate。
	TargetID int64 `gorm:"column:target_id;default:0" json:"targetId"`
	// Source 流量来源（扫码 / 转发 / 搜索 / 海报 / 兜底）。仅作统计维度，
	// 永不参与权限 / 租户 / 计费判断。取值由后端 normalizeSource 白名单归一，
	// 列由 migrations/0022 增加（MySQL），SQLite 走 AutoMigrate。
	Source    string    `gorm:"column:source;default:''" json:"source"`
	StatDate  string    `gorm:"column:stat_date;index" json:"statDate"` // YYYY-MM-DD
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
}

// Admin 后台管理员（账号 + 密码登录，非微信 openid）。
// 两级角色（多租户第一期）：
//   - company_id = 0：平台超级管理员，可见/管理全部租户（企业管理入口仅对其开放）；
//   - company_id > 0：企业管理员，仅可见/管理本企业的数据（登录时校验企业未停用）。
type Admin struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CompanyID    int64     `gorm:"column:company_id;index" json:"companyId"` // 0=平台超管
	Username     string    `gorm:"column:username" json:"username"`
	PasswordHash string    `gorm:"column:password_hash" json:"-"`
	TokenVersion int       `gorm:"column:token_version;default:0" json:"-"` // 安全戳：改密自增，令旧令牌失效
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
}

// RevokedSession 已吊销会话黑名单（单设备精确吊销）。
// 一次登录对应一个 sid（见 auth.Claims.SID，access/refresh 共享）；登出时把 sid 写入本表，
// 之后该会话签发的所有令牌立即失效。与 token_version 互补：
//   - token_version：账号级，改密/禁用后该用户所有设备全部失效；
//   - 本表：会话级，只失效指定设备，同一账号的其他登录不受影响。
//
// expires_at 仅用于后台按时间清理过期记录，不参与吊销判定（判定只看 sid 是否存在）。
type RevokedSession struct {
	SID       string    `gorm:"primaryKey;column:sid" json:"sid"`
	UserType  string    `gorm:"column:user_type" json:"userType"` // admin / employee
	UserID    int64     `gorm:"column:user_id" json:"userId"`     // admin.id 或 employee.staff_id
	ExpiresAt time.Time `gorm:"column:expires_at;index" json:"expiresAt"`
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
}

// OpenApply 员工自助开通申请（审核流，按租户隔离：申请归属企业，审核后员工落到同企业）
type OpenApply struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CompanyID    int64     `gorm:"column:company_id;index" json:"companyId"`
	Title        string    `gorm:"column:title" json:"title"`
	Landline     string    `gorm:"column:landline" json:"landline"` // 座机（选填）
	Avatar       string    `gorm:"column:avatar" json:"avatar"`
	OpenID       string    `gorm:"column:openid" json:"openid"`                 // 提交申请时的微信 openid
	Status       string    `gorm:"column:status;default:pending" json:"status"` // pending/approved/rejected
	RejectReason string    `gorm:"column:reject_reason" json:"rejectReason"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`

	// ---- PII 明文（gorm:"-" 不落库）+ 密文（原列名）+ 盲索引：与 Employee 同一套方案 ----
	Name     string `gorm:"-" json:"name"`
	Phone    string `gorm:"-" json:"phone"`
	Phone2   string `gorm:"-" json:"phone2"` // 手机 2（选填），审核通过后一并写入员工资料
	Email    string `gorm:"-" json:"email"`
	Nickname string `gorm:"-" json:"nickname"` // 提交申请时的微信昵称

	NameEnc      string `gorm:"column:name" json:"-"`
	PhoneEnc     string `gorm:"column:phone" json:"-"`
	Phone2Enc    string `gorm:"column:phone2" json:"-"`
	EmailEnc     string `gorm:"column:email" json:"-"`
	NicknameEnc  string `gorm:"column:nickname" json:"-"`
	NameBidx     string `gorm:"column:name_bidx" json:"-"`
	PhoneBidx    string `gorm:"column:phone_bidx" json:"-"`
	Phone2Bidx   string `gorm:"column:phone2_bidx" json:"-"`
	EmailBidx    string `gorm:"column:email_bidx" json:"-"`
	NicknameBidx string `gorm:"column:nickname_bidx" json:"-"`
}

// CardModifyApply 员工名片信息修改申请（审核流，对外仍显示旧信息直到通过）。
// company_id 冗余自员工归属租户：后台按企业隔离列表时免 join。
type CardModifyApply struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CompanyID    int64     `gorm:"column:company_id;index" json:"companyId"`
	StaffID      int64     `gorm:"column:staff_id;index" json:"staffId"`
	Status       string    `gorm:"column:status;default:pending" json:"status"`
	RejectReason string    `gorm:"column:reject_reason" json:"rejectReason"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`

	// Snapshot 明文（gorm:"-"）：变更字段 JSON {avatar,name,title,phone,landline,email}。
	// 整段作为单个 PII 密文落库（列改 LONGTEXT：密文不是合法 JSON，不能存 JSON 列）。
	Snapshot    string `gorm:"-" json:"snapshot"`
	SnapshotEnc string `gorm:"column:snapshot;type:text" json:"-"`
}

// AuditLog 管理操作审计日志。
//
// 定位：回答「谁在什么时候、对什么对象、做了什么、成功没有」。
// 覆盖后台的**写操作**（含失败/被拒的尝试）与登录失败——只读查询不记，否则日志量会淹没有用信息。
//
// 设计取舍：
//   - actor_name 冗余存一份用户名：账号被删除后仍能追溯是谁干的（只存 id 就成了悬空引用）。
//   - detail 只记**变更摘要**，且敏感值（密钥 / 密码 / 连接串）一律不落库，只记「已修改」。
//     否则审计表本身会变成新的泄密面——这与其存在的初衷相悖。
//   - 本表只追加不修改：审计记录被篡改就等于没有审计。
type AuditLog struct {
	ID         int64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	ActorType  string    `gorm:"column:actor_type;size:16;index" json:"actorType"` // admin / employee / anonymous / system
	ActorID    int64     `gorm:"column:actor_id;index" json:"actorId"`             // admin.id 或 employee.staff_id
	ActorName  string    `gorm:"column:actor_name;size:64" json:"actorName"`       // 冗余用户名（账号删除后仍可追溯）
	CompanyID  int64     `gorm:"column:company_id;index" json:"companyId"`         // 操作者所属租户（0=平台）
	Action     string    `gorm:"column:action;size:32;index" json:"action"`        // 见 internal/audit 的动作常量
	TargetType string    `gorm:"column:target_type;size:32" json:"targetType"`     // setting / admin / company / employee ...
	TargetID   string    `gorm:"column:target_id;size:64" json:"targetId"`         // 目标标识（字符串以便覆盖非数字主键）
	Detail     string    `gorm:"column:detail;type:text" json:"detail"`            // JSON 变更摘要（敏感值只记「已修改」）
	IP         string    `gorm:"column:ip;size:64" json:"ip"`
	Result     string    `gorm:"column:result;size:16" json:"result"` // success / fail
	CreatedAt  time.Time `gorm:"column:created_at;index" json:"createdAt"`
}

// TableName 显式指定表名（避免 GORM 复数化）。
func (AuditLog) TableName() string { return "audit_log" }

// RateLimitHit 限流计数（多副本共享用）。一行 = 一个分桶键在一个时间窗口内的累计次数。
//
// 为什么需要它：进程内限流器在多副本下每个副本各有一份桶，实际阈值会变成
// 「单副本阈值 × 副本数」——防爆破从 10 次/分悄悄变成 30 次/分，而且不报错。
// 把计数落到同一张表后，同一窗口内所有副本打的是同一行，阈值才与副本数无关。
//
// 设计取舍：
//   - window_start 用 Unix 秒（对齐到窗口边界）而不是时间戳字符串：同一窗口内
//     必然是同一个整数，天然成为主键的一部分，也不需要额外计算「窗口是否变化」。
//   - 列名用 bucket_key 而不是 key：KEY 在 MySQL 属关键字，裸用要靠反引号兜底。
//   - 只增不修：计数靠 UPSERT 自增，没有任何 UPDATE 业务字段的路径。
//   - 单机（SQLite）形态也用这张表：行为一致，免得出现「MySQL 限流准、SQLite 不准」
//     这种难以解释的差异；代价是每次登录多两次 IO，对登录接口无感。
type RateLimitHit struct {
	BucketKey   string `gorm:"primaryKey;column:bucket_key;size:191" json:"-"`
	WindowStart int64  `gorm:"primaryKey;column:window_start" json:"-"`
	Hits        int    `gorm:"column:hits" json:"-"`
}

// PosterPref 员工的海报个性化设置（每员工一行）。
//
// 为什么单独一张表、而不是塞进 employee：
//   - employee 的写入走「提交 → 待审 → 通过才生效」的审核流（见 CardModifyApply），
//     而海报版式是纯个人偏好，让员工调个字号还要等管理员审批很荒谬；
//   - 海报设置是高频小写入（每次改版式都存一次），不该把审核链路的行锁带上。
//
// 字段存 JSON 文本而非拆列：版式可选项会随需求增删（模板/开关/配色/位移），
// 拆成列每加一项都要写迁移；JSON 只在 handler 层用白名单校验归一，
// 脏数据进不来（校验见 handler/poster.go normalizePosterLayout）。
//
// backgrounds 与 bgId 分开存：前者是「我的素材库」（可增删），
// 后者是「当前选中的是哪张」（可以是内置素材 id，也可以是自定义背景 id）。
type PosterPref struct {
	ID        int64  `gorm:"primaryKey;autoIncrement;column:id" json:"-"`
	StaffID   int64  `gorm:"column:staff_id;uniqueIndex" json:"-"`
	CompanyID int64  `gorm:"column:company_id;index" json:"-"` // 租户冗余：后台按企业清理/统计时不必回表 employee
	BgID      string `gorm:"column:bg_id;size:64" json:"bgId"` // 当前背景："" = 白底 / poster-* = 内置 / c-* = 自定义
	// Backgrounds 自定义背景图 JSON 数组：[{"id":"c-1a2b3c4d","name":"我的照片","file":"/uploads/..."}]
	Backgrounds string    `gorm:"column:backgrounds;type:text" json:"-"`
	Layout      string    `gorm:"column:layout;type:text" json:"-"` // 版式 JSON（见 handler/poster.go）
	CreatedAt   time.Time `gorm:"column:created_at" json:"-"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"-"`
}

// CompanyWXApp 租户的小程序凭据（一个企业一个小程序）。
//
// 为什么单独一张表而不是塞进 company / sys_setting：
//   - sys_setting 是全局 key-value，没有租户维度——企业独立版小程序
//     每家企业一个 AppID，共用一个后端时全局那一份只能服务一个小程序；
//   - company 表是租户主数据，凭据属于「集成配置」，变更频率与权限粒度都不同。
//
// 取值链三级回落：本表 → sys_setting 的 wx_appid / wx_appsecret → 环境变量。
// 平台模式（一个小程序服务所有企业）不给租户配，自动落到第二级。
type CompanyWXApp struct {
	ID        int64  `gorm:"primaryKey;autoIncrement;column:id" json:"-"`
	CompanyID int64  `gorm:"column:company_id;uniqueIndex" json:"-"`
	AppID     string `gorm:"column:app_id;size:64" json:"appId"`
	AppSecret string `gorm:"column:app_secret;size:128" json:"-"` // 只写不读：接口层只回掩码
	// UploadKey 小程序「代码上传密钥」（private.key 的 PEM 原文）。
	// 与 AppSecret 是两把不同的钥匙：AppSecret 换 access_token 调提审/发布接口，
	// UploadKey 才能把代码传到微信（miniprogram-ci 只认它，不认 AppSecret）。
	// 同样只写不读——接口层只回「是否已配置」。
	UploadKey string `gorm:"column:upload_key;size:2048" json:"-"`
	// ReleaseEnabled 该企业是否允许平台代发小程序版本（默认 true）。
	// 企业独立版的小程序归企业自己管，平台不该有单方面推送上线的能力——
	// 企业把这项关掉后，一切代发动作（提审 / 发布 / 撤回）一律拒绝。
	// 平台自有小程序（company_id=0，不落本表）没有这个开关。
	ReleaseEnabled bool      `gorm:"column:release_enabled" json:"releaseEnabled"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"-"`
	UpdatedAt      time.Time `gorm:"column:updated_at" json:"-"`
}

// TableName 显式指定单数表名，与 migrations/0024 对齐（见 migrations_guard_test.go）。
func (CompanyWXApp) TableName() string { return "company_wx_app" }

// MPRelease 小程序版本发布记录（平台代租户上传 / 提审 / 发布）。
//
// 为什么需要一张表而不是「每次都去问微信」：
//  1. 微信只认 auditid，不认我们的业务语义。提审返回的 auditid 得存下来，
//     否则查状态时不知道该查哪一单；
//  2. 提审**消耗审核额度且撤回不返还**（官方约束），所以必须留下完整痕迹：
//     谁提的、什么时候提的、为什么驳回、发布了哪一版。事后扯皮时这是唯一证据；
//  3. 「当前线上是哪一版」要能一眼答出来，不能靠人去公众平台翻。
//
// 状态机见 mp_release.go 的 CanTransition——**只有白名单内的流向才允许**，
// 非法流转一律在 service 层拒掉，不依赖调用方自觉。
type MPRelease struct {
	ID        int64  `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	CompanyID int64  `gorm:"column:company_id;index" json:"companyId"` // 租户；0 = 平台自有小程序
	AppID     string `gorm:"column:app_id;size:64" json:"appId"`       // 本次发布打到哪个小程序
	// UserVersion 上传代码时自定义的版本号（微信侧 user_version，长度 ≤ 64）。
	// 由 CI 上传后登记进来，不是我们生成的——微信拿它标识一个代码版本。
	UserVersion string `gorm:"column:user_version;size:64" json:"userVersion"`
	UserDesc    string `gorm:"column:user_desc;size:255" json:"userDesc"` // 版本说明，展示在后台
	// Status 状态机当前态，取值见 mp_release.go 的常量。
	Status string `gorm:"column:status;size:32;index" json:"status"`
	// AuditID 微信审核单 id（提审时返回）。未提审为 0。
	AuditID int64 `gorm:"column:audit_id" json:"auditId"`
	// AuditReason 驳回 / 延后原因（微信原样透出，不翻译——翻译会丢信息，
	// 而排查的人需要按原文去搜）。
	AuditReason string `gorm:"column:audit_reason;size:500" json:"auditReason"`
	// Source 登记来源：ci = 外部 CI 上传后登记，manual = 后台手工登记。
	// 留这个字段是为了将来能回答「这一版是谁推的」。
	Source     string `gorm:"column:source;size:32" json:"source"`
	OperatorID int64  `gorm:"column:operator_id" json:"operatorId"` // 最后一步操作人（admin.id）
	// 三个时间戳用指针而不是 time.Time：零值 time.Time 会被 JSON 序列化成
	// "0001-01-01T00:00:00Z"，前端得判空串；指针序列化成 null，语义直白。
	SubmittedAt *time.Time `gorm:"column:submitted_at" json:"submittedAt"`
	ReleasedAt  *time.Time `gorm:"column:released_at" json:"releasedAt"`
	CreatedAt   time.Time  `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   time.Time  `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 显式指定单数表名，与 migrations/0025 对齐。
func (MPRelease) TableName() string { return "mp_release" }
