package model

import "time"

// StorageProfile 是一套可复用的文件存储连接信息（迁移 0054）。
//
// 与 tenant_storage_configs 的区别：那张表是「一家租户一整份配置」，
// 同一份 endpoint / AK / SK 会被复制到每个租户；本表是「连接信息只存一份，
// 谁要用由 storage_bindings 引用」。改一次桶地址只需改档案，所有绑定它的企业
// 在下一个请求就生效（见 internal/storageprofile 的版本快照）。
//
// 凭据按当前产品决策明文入库：回显掩码、SecretKey 不回传。数据库备份与访问控制
// 必须受控；项目没有 re-key 机制，将来改加密是一次专门迁移，不要在这里半做。
//
// Purpose 只标记**内置档案的用途**（迁移 0056 / 0059）：
//   - platform_primary：平台自身那份存储的连接信息（驱动 / 桶 / 凭据 / 基址），
//     启动期由它构造平台实例并 SetPlatform；sys_setting 只留一个指向它的 id；
//   - tenant_fallback：租户退路，dir 与 cfg.UploadDir 同源；
//   - platform_mirror：备份存储落点，独立目录，私有，不参与任何绑定。
//
// 普通档案为空串。内置档案都不可删，但它们**不是**可互换的：把镜像档案绑给租户
// 会让备份副本变成主写入目标，把平台档案绑给租户会让 p/ 对象写进客户桶。
// platform_primary 的连接信息可在界面改、保存即生效，目录仍跟随 cfg.UploadDir。
// 私有读（public_read=false）只对绑定到企业的档案开放：p/ 对象没有逐租户签名链路。
const (
	ProfilePurposePlatformPrimary = "platform_primary"
	ProfilePurposeTenantFallback  = "tenant_fallback"
	ProfilePurposePlatformMirror  = "platform_mirror"
)

type StorageProfile struct {
	ID       int64  `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"size:64;uniqueIndex" json:"name"`
	Driver   string `gorm:"size:16;default:local" json:"driver"`
	Endpoint string `gorm:"size:255" json:"endpoint"`
	Bucket   string `gorm:"size:128" json:"bucket"`
	Region   string `gorm:"size:64" json:"region"`
	BaseURL  string `gorm:"column:base_url;size:255" json:"baseUrl"`
	// S3 路径风格由 HostHeader 单点推导（nginxsnippet.Target.PathStyle），
	// 不设独立字段：曾经有 path_style 列，但运行时零消费，改它不改变任何行为。
	// 0058 已删列，别再加回来。
	HostHeader string `gorm:"column:host_header;size:255" json:"hostHeader"`
	// UseSSL 决定 S3 / OSS 构造与连通性自检用 http 还是 https。
	// 与旧 tenant_storage_configs 保持同名字段，避免「档案说走 https、实际按 http 签」。
	//
	// 刻意**不加** gorm 的 default 标签：带 default 时 GORM 会把零值从 INSERT 里省略、
	// 交给库默认值，于是「新建时选 false」被翻回 true。use_ssl 与 public_read 都踩过
	// 这个坑（public_read 被翻回 true 等于把私有桶当公开桶用，出参会按匿名可读拼 URL）。
	// 默认值一律由创建方显式给出：handler 的 create 与 EnsureBuiltinLocal。
	UseSSL bool `gorm:"column:use_ssl" json:"useSsl"`
	// LocalDir 仅 driver=local 使用：该档案自己的根目录。
	// 不填表示沿用平台 uploads 目录（内置档案的默认值）；迁移要区分源/目标时必须填。
	LocalDir    string     `gorm:"column:local_dir;size:512" json:"localDir"`
	AccessKey   string     `gorm:"column:access_key;size:512" json:"accessKey,omitempty"`
	SecretKey   string     `gorm:"column:secret_key;size:1024" json:"secretKey,omitempty"`
	PublicRead  bool       `gorm:"column:public_read" json:"publicRead"`
	Status      string     `gorm:"size:16;default:unknown" json:"status"`
	LastProbeAt *time.Time `gorm:"column:last_probe_at" json:"lastProbeAt,omitempty"`
	LastError   string     `gorm:"column:last_error;size:512" json:"lastError,omitempty"`
	Version     int64      `gorm:"default:1" json:"version"`
	Builtin     bool       `gorm:"default:false" json:"builtin"`
	Purpose     string     `gorm:"column:purpose;size:24" json:"purpose"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func (StorageProfile) TableName() string { return "storage_profiles" }

// DeliveryProfile 是一套可复用的 CDN 分发配置（迁移 0054）。
//
// 独立于存储档案：一个桶可以配多个 CDN 域名（国内 / 海外 / 逃生），
// 一个域名也可以回源到不同桶。domain 只影响出参 URL，不参与上传实例构造，
// 与既有 PublicOrigin 的语义保持一致。
//
// Purpose 只标记**内置档案的用途**（迁移 0060）：
//   - platform_primary：平台自身那份 CDN 刷新凭据。平台对象（p/ 前缀）与未绑定
//     企业的覆盖写由它刷新；它的 Domain 不参与出参（平台域名唯一真值源是
//     storage_profiles 里 platform_primary 的 BaseURL），只承载 provider / 凭据 /
//     端点 / 资源标识。
//
// 普通档案为空串。内置档案不可删、不可绑定给企业。
const (
	DeliveryPurposePlatformPrimary = "platform_primary"
)

type DeliveryProfile struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	Name       string `gorm:"size:64;uniqueIndex" json:"name"`
	Provider   string `gorm:"size:24;default:none" json:"provider"`
	Domain     string `gorm:"size:255" json:"domain"`
	AccessKey  string `gorm:"column:access_key;size:512" json:"accessKey,omitempty"`
	SecretKey  string `gorm:"column:secret_key;size:1024" json:"secretKey,omitempty"`
	Endpoint   string `gorm:"size:255" json:"endpoint"`
	ResourceID string `gorm:"column:resource_id;size:128" json:"resourceId"`
	Status     string `gorm:"size:16;default:unknown" json:"status"`
	// Builtin / Purpose 与 StorageProfile 同口径：内置行由启动逻辑 ensure 创建，
	// 不可删、不可绑定；平台自身那行的凭据按 version 现建刷新器，保存即生效。
	Builtin     bool       `gorm:"default:false" json:"builtin"`
	Purpose     string     `gorm:"column:purpose;size:24" json:"purpose"`
	LastProbeAt *time.Time `gorm:"column:last_probe_at" json:"lastProbeAt,omitempty"`
	LastError   string     `gorm:"column:last_error;size:512" json:"lastError,omitempty"`
	Version     int64      `gorm:"default:1" json:"version"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func (DeliveryProfile) TableName() string { return "delivery_profiles" }

// StorageBinding 决定谁用哪套档案（迁移 0054）。
//
// TenantID=0 是全局默认；企业没有绑定行时继承全局。Version 每次变更递增，
// 运行时解析器按下一次请求比对版本，秒级最终一致（多副本各进程独立缓存）。
type StorageBinding struct {
	ID                int64 `gorm:"primaryKey" json:"id"`
	TenantID          int64 `gorm:"column:tenant_id;uniqueIndex" json:"tenantId"`
	StorageProfileID  int64 `gorm:"column:storage_profile_id" json:"storageProfileId"`
	DeliveryProfileID int64 `gorm:"column:delivery_profile_id" json:"deliveryProfileId"`
	// PreviousStorageProfileID / PreviousDeliveryProfileID 记录「本次切换前的那一套」，
	// 供一键回滚使用。语义与边界见迁移 0057：
	//   - 前进切换时把旧档案写进来；回滚时与当前值交换（可反复来回切）；
	//   - 0 表示「绑定首次建立之前没有档案」，回滚接口据此拒绝，退路是删绑定回落全局。
	// 它们只是回滚游标，**不参与运行时档案解析**——Resolve 只读当前档案，
	// 绝不能因为这里留了旧值就把对象写回旧桶。
	PreviousStorageProfileID  int64     `gorm:"column:previous_storage_profile_id" json:"previousStorageProfileId"`
	PreviousDeliveryProfileID int64     `gorm:"column:previous_delivery_profile_id" json:"previousDeliveryProfileId"`
	Version                   int64     `gorm:"default:1" json:"version"`
	UpdatedBy                 string    `gorm:"column:updated_by;size:64" json:"updatedBy"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

func (StorageBinding) TableName() string { return "storage_bindings" }

// StorageMigrationJob 是一次「把对象从源档案复制到目标档案」的任务（迁移 0054）。
//
// 只复制不删除：源桶对象保留，确认无误后由运维自行清理，避免不可逆操作。
type StorageMigrationJob struct {
	ID              int64      `gorm:"primaryKey" json:"id"`
	TenantID        int64      `gorm:"column:tenant_id" json:"tenantId"`
	SourceProfileID int64      `gorm:"column:source_profile_id" json:"sourceProfileId"`
	TargetProfileID int64      `gorm:"column:target_profile_id" json:"targetProfileId"`
	Status          string     `gorm:"size:16;default:created" json:"status"`
	Total           int64      `gorm:"default:0" json:"total"`
	Done            int64      `gorm:"default:0" json:"done"`
	Failed          int64      `gorm:"default:0" json:"failed"`
	LastKey         string     `gorm:"column:last_key;size:512" json:"lastKey,omitempty"`
	DryRun          bool       `gorm:"column:dry_run" json:"dryRun"`
	AutoSwitch      bool       `gorm:"column:auto_switch" json:"autoSwitch"`
	CreatedBy       string     `gorm:"column:created_by;size:64" json:"createdBy"`
	Error           string     `gorm:"size:512" json:"error,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	FinishedAt      *time.Time `gorm:"column:finished_at" json:"finishedAt,omitempty"`
}

func (StorageMigrationJob) TableName() string { return "storage_migration_jobs" }

// StorageMigrationError 是迁移失败对象的一条记录，可导出后逐个重试。
type StorageMigrationError struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	JobID     int64     `gorm:"column:job_id;index" json:"jobId"`
	ObjectKey string    `gorm:"column:object_key;size:512" json:"objectKey"`
	Error     string    `gorm:"size:512" json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

func (StorageMigrationError) TableName() string { return "storage_migration_errors" }
