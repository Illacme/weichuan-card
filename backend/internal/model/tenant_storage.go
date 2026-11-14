package model

import "time"

// TenantStorageConfig 是按企业登记的「自有对象桶」配置（设计稿 §3.2，迁移 0044）。
//
// 为什么要有这张表：目前所有上传都落在平台那一套存储上（sys_setting 的
// storage_driver 等全局单值）。客户说「文件存进我自己公司的桶」时，全局单值
// 装不下第二个租户——与 CompanyWXApp 是同一类问题，只是这次带上数据主权性质：
// 客户要的不是一个开关，而是「我的文件不落在你机器上」。
//
// 读写边界（三条，任何一条破了都会出事）：
//
//  1. **只有平台超管可写**。企业管理员侧不暴露写接口、菜单隐藏、连读接口都不给。
//     能改自己的桶地址 = 能把全企业的图指到任意主机，且出参 URL 与对象键都跟着变，
//     事后无从追溯是谁改的。接口层挂 PlatformAdminMiddleware，不只是前端隐藏。
//  2. **凭据列只写不读**：AccessKeyEnc / SecretKeyEnc 是 pii.Encrypt 的产物，
//     写入必须经 pii.Encrypt（空串表示「没配」，不是「配了空」）。两列 json:"-"，
//     永远不参与序列化——与 CompanyWXApp.AppSecret 的处理一致，区别是本表的凭据
//     要参与真实 S3 请求签名，漏一次明文就会随报错/日志带出去。
//  3. **给生成器读的字段与非密文列严格分开**：cmd/nginx-snippets 只需要
//     driver/endpoint/bucket/use_ssl/host_header/enabled，不该 SELECT 凭据列。
//     它不是「顺手多取两列也没关系」——生成器跑在能写 nginx 配置的机器上，
//     把明文凭据读进内存没有收益，只有风险。
//
// 为什么 host_header 是一列而不是从 endpoint 推：virtual-hosted-style 的桶名在
// Host 里，且这个 Host 同时是 TLS SNI 的取值（第五十五轮 R1 真桶实测：漏 SNI 被
// 421 拒绝）。而桶名拼在 endpoint 前面还是后面逐厂商不同（MinIO path-style，
// OSS/S3 virtual-hosted-style），推导等于猜。给一列让超管照控制台抄。
type TenantStorageConfig struct {
	ID int64 `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	// CompanyID 租户（company.id）。0 不作为合法值——平台自身那份走 sys_setting。
	CompanyID int64 `gorm:"column:company_id;uniqueIndex" json:"companyId"`
	// Driver 取值见 nginxsnippet.DriverLocal / DriverMinio / DriverOSS / DriverS3，
	// 与 config.StorageDriver 枚举一致。校验在 nginxsnippet.Target.Validate 单点做，
	// 不在模型上做（模型层校验拦不住直接写库的 SQL，反而不如出参时显式报错）。
	Driver string `gorm:"column:driver;size:16" json:"driver"`
	// Endpoint 是 host[:port]，**不带 scheme**。带 scheme 是最常见的填错方式
	// （把控制台里的 URL 原样粘过来），后果是 snippet 里拼出 http://https://xxx。
	Endpoint string `gorm:"column:endpoint;size:255" json:"endpoint"`
	Bucket   string `gorm:"column:bucket;size:128" json:"bucket"`
	// Region OSS/S3 需要，MinIO 可空。仅参与 Go 侧客户端构造，不进 nginx snippet。
	Region string `gorm:"column:region;size:64" json:"region"`
	// UseSSL 为 true 时 snippet 必须同时给 proxy_ssl_server_name + proxy_ssl_name，
	// 否则真桶按 SNI 判不出该用哪张证书，直接 421。
	UseSSL bool `gorm:"column:use_ssl" json:"useSsl"`
	// HostHeader 发给桶的 Host（也是 TLS SNI 取值）。空则退化成 Endpoint。
	HostHeader string `gorm:"column:host_header;size:255" json:"hostHeader"`

	// AccessKeyEnc / SecretKeyEnc 是 pii.Encrypt 的密文（带 v1: 前缀）。
	// json:"-" 是硬要求：一旦被序列化进任何接口出参，等于把租户凭据交给全网。
	AccessKeyEnc string `gorm:"column:access_key_enc;size:512" json:"-"`
	SecretKeyEnc string `gorm:"column:secret_key_enc;size:512" json:"-"`
	// KeyID 加密密钥版本标识。换钥匙时据此判断哪些行还需要重加密，
	// 现在不填（空串 = 当前密钥），留口子而已。
	KeyID string `gorm:"column:key_id;size:32" json:"keyId"`

	// PublicBaseURL 客户自有访问域名，纯 origin（http(s)://host[:port]）。
	// 第六十八轮订正：不再是「恒为空」。它现在可由超管登记、经
	// storage.ValidatePublicOrigin 归一化，并原样回显到配置界面；
	// 但**仍不参与实例构造**：uploadroute.DBLoader 把它放进 Config.PublicOrigin，
	// 绝不是 Config.BaseURL（那个是上传返回基址，写入侧要靠它剥回裸键）。
	// 出参侧按租户选 origin 尚未接入，故 publicBaseUrlActive 恒为 false。
	PublicBaseURL string `gorm:"column:public_base_url;size:255" json:"publicBaseUrl"`
	// PublicRead 桶是否公开读。第一阶段试点要求为 true：私有桶要走预签名，
	// 而出参契约（存裸键 / 存 URL）会跟着变，不在本轮范围。
	// 第六十八轮起它是**运行时消费项**：写入侧 validateTenantStorageRow 与上传侧
	// uploadroute.build 都会显式拒绝 false（回填侧 TenantTargetFor 不拦）。
	PublicRead bool `gorm:"column:public_read" json:"publicRead"`
	// Enabled 开关。关掉即回平台存储。注意 driver=local 时同样不生成 snippet，
	// 两者的判定**只允许在 Target.NeedsSnippet 一处**（见该函数注释：
	// 只判 Enabled 会留下一条指向不存在桶的 location，那家租户全站图 404 而后台显示正常）。
	Enabled bool `gorm:"column:enabled" json:"enabled"`
	// Status 连通性自检结果：unknown / ok / error。后台列表靠它标红。
	Status string `gorm:"column:status;size:16" json:"status"`
	// LastCheckedAt 最近一次自检时间。
	LastCheckedAt *time.Time `gorm:"column:last_checked_at" json:"lastCheckedAt"`
	// LastError 最近一次自检的脱敏摘要：不含凭据、不含完整响应体
	// （S3 的错误 XML 里会回显 AccessKey 的部分片段与桶名，原样落库再展示给
	// 企业管理员就是泄露）。
	LastError string    `gorm:"column:last_error;size:512" json:"lastError"`
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 显式指定表名，与 migrations/0044 对齐（见 migrations_guard_test.go）。
//
// 这里 gorm 默认复数化也会得到同一个名字，显式写是为了不依赖命名策略：
// 将来若有人给模型加 gorm 标签改命名、或升 gorm 大版本调整策略，
// 靠默认值的表名会静默变掉，而守门测试比对的是「模型表名 vs 迁移表名」，
// 默认值一变它就红——但那时已经很难看出是被谁改的。
func (TenantStorageConfig) TableName() string { return "tenant_storage_configs" }
