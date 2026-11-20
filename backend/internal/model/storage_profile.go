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
type StorageProfile struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	Name       string `gorm:"size:64;uniqueIndex" json:"name"`
	Driver     string `gorm:"size:16;default:local" json:"driver"`
	Endpoint   string `gorm:"size:255" json:"endpoint"`
	Bucket     string `gorm:"size:128" json:"bucket"`
	Region     string `gorm:"size:64" json:"region"`
	BaseURL    string `gorm:"column:base_url;size:255" json:"baseUrl"`
	PathStyle  bool   `gorm:"column:path_style" json:"pathStyle"`
	HostHeader string `gorm:"column:host_header;size:255" json:"hostHeader"`
	// UseSSL 决定 S3 / OSS 构造与连通性自检用 http 还是 https。
	// 与旧 tenant_storage_configs 保持同名字段，避免「档案说走 https、实际按 http 签」。
	UseSSL bool `gorm:"column:use_ssl;default:true" json:"useSsl"`
	// LocalDir 仅 driver=local 使用：该档案自己的根目录。
	// 不填表示沿用平台 uploads 目录（内置档案的默认值）；迁移要区分源/目标时必须填。
	LocalDir    string     `gorm:"column:local_dir;size:512" json:"localDir"`
	AccessKey   string     `gorm:"column:access_key;size:512" json:"accessKey,omitempty"`
	SecretKey   string     `gorm:"column:secret_key;size:1024" json:"secretKey,omitempty"`
	PublicRead  bool       `gorm:"column:public_read;default:true" json:"publicRead"`
	Status      string     `gorm:"size:16;default:unknown" json:"status"`
	LastProbeAt *time.Time `gorm:"column:last_probe_at" json:"lastProbeAt,omitempty"`
	LastError   string     `gorm:"column:last_error;size:512" json:"lastError,omitempty"`
	Version     int64      `gorm:"default:1" json:"version"`
	Builtin     bool       `gorm:"default:false" json:"builtin"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func (StorageProfile) TableName() string { return "storage_profiles" }

// DeliveryProfile 是一套可复用的 CDN 分发配置（迁移 0054）。
//
// 独立于存储档案：一个桶可以配多个 CDN 域名（国内 / 海外 / 逃生），
// 一个域名也可以回源到不同桶。domain 只影响出参 URL，不参与上传实例构造，
// 与既有 PublicOrigin 的语义保持一致。
type DeliveryProfile struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	Name        string     `gorm:"size:64;uniqueIndex" json:"name"`
	Provider    string     `gorm:"size:24;default:none" json:"provider"`
	Domain      string     `gorm:"size:255" json:"domain"`
	PathPrefix  string     `gorm:"column:path_prefix;size:128" json:"pathPrefix"`
	AccessKey   string     `gorm:"column:access_key;size:512" json:"accessKey,omitempty"`
	SecretKey   string     `gorm:"column:secret_key;size:1024" json:"secretKey,omitempty"`
	Endpoint    string     `gorm:"size:255" json:"endpoint"`
	ResourceID  string     `gorm:"column:resource_id;size:128" json:"resourceId"`
	Status      string     `gorm:"size:16;default:unknown" json:"status"`
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
	ID                int64     `gorm:"primaryKey" json:"id"`
	TenantID          int64     `gorm:"column:tenant_id;uniqueIndex" json:"tenantId"`
	StorageProfileID  int64     `gorm:"column:storage_profile_id" json:"storageProfileId"`
	DeliveryProfileID int64     `gorm:"column:delivery_profile_id" json:"deliveryProfileId"`
	Version           int64     `gorm:"default:1" json:"version"`
	UpdatedBy         string    `gorm:"column:updated_by;size:64" json:"updatedBy"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
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
