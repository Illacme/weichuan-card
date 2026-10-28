package config

import (
	"strconv"
	"strings"
	"time"
)

// 安全相关常量：默认密钥 / 默认超管口令与其判定下限。
//
// 默认口令之所以能被「硬门槛」拦住，是因为它连同演示账号一起写进了 README 与联调
// 文档——它已经不是秘密了。带它上线等于任何人都能进后台，所以必须是致命项而非提示项。
const (
	DefaultJWTKey    = "change-me"   // JWT_KEY 内置缺省值
	MinJWTKeyLen     = 16            // 低于此长度视为可暴力伪造
	DefaultAdminPass = "admin123456" // ADMIN_PASS 内置缺省值（首启动 seed 用）
	MinAdminPassLen  = 8             // 低于此长度仅告警：口令是给人记的，不宜卡太死
)

// Config 通过环境变量注入，不写死（保证可平滑迁云托管）
type Config struct {
	DBDSN              string
	Port               string
	JWTKey             string
	UploadDir          string // 本地上传目录（local 驱动用，小程序码缓存也落这里）
	UploadBaseURL      string // 上传文件公开访问基址（小程序/前端取此拼 URL）
	StorageDriver      string // 文件存储驱动：local(默认) | minio | oss | s3
	StoragePublic      bool   // 是否为公开桶；false=私有桶，需限时签名访问（STORAGE_PUBLIC）
	StorageRegion      string // S3 兼容存储区域（STORAGE_REGION）；留空=自动探测，oss 驱动用不到
	MinioEndpoint      string // MinIO / S3 兼容端点（不含协议头）
	MinioAccessKey     string
	MinioSecretKey     string
	MinioBucket        string
	MinioUseSSL        bool
	OssEndpoint        string // 阿里云 OSS 端点，如 oss-cn-hangzhou.aliyuncs.com
	OssAccessKeyID     string
	OssAccessKeySecret string
	OssBucket          string
	CDNEnabled         bool // 是否启用 CDN 刷新（覆盖写后使边缘缓存失效）
	// CDNProvider 厂商：aliyun / tencent / qiniu / cloudflare / cloudfront；
	// 留空按 aliyun（既有部署没有这一项）
	CDNProvider string
	// CDNAccessKeyID / Secret 是通用凭据位，按厂商含义不同：
	// 阿里云 AccessKeyId/Secret、腾讯云 SecretId/SecretKey、七牛 AccessKey/SecretKey、
	// CloudFront AccessKeyId/SecretAccessKey；Cloudflare 只用 Secret 位放 API Token。
	CDNAccessKeyID     string
	CDNAccessKeySecret string
	// CDNEndpoint 刷新接口端点：**留空按厂商取默认值**（各厂商域名不同，
	// 写死一个默认值会让换厂商后拿到错的域名）
	CDNEndpoint string
	// CDNResourceID 资源标识：Cloudflare 的 Zone ID / CloudFront 的 Distribution ID；
	// AK/SK 三家不需要（它们按提交 URL 的域名自己定位加速域名）
	CDNResourceID string
	WXAppID       string // 小程序 AppID（小程序码用）
	WXAppSecret   string // 小程序 AppSecret（小程序码用）
	// 微信「消息推送」凭据：图片内容安全的结论靠它推回来。
	// 与上面两项不同，它们**只影响回调**——Token 用于验签（必填），
	// EncodingAESKey 只在安全模式下用来解密（明文模式可留空）。
	// 留空时图片检测不送检：送出去也拿不回结论，只会造出一堆永久 pending 的假记录。
	WXMsgToken  string
	WXMsgAESKey string
	// 小程序代码上传（后台代传，见 internal/mpupload）。这三项都是**服务器路径**，
	// 只能来自环境变量 / 配置文件——界面里可配的只有那把密钥（多行 PEM 放不进
	// 一行一项的 runtime.env，且密钥不该跟着部署配置到处复制）。
	MPNodeBin       string        // node 可执行文件名或绝对路径，默认 "node"
	MPCIModuleDir   string        // 装了 miniprogram-ci 的目录；留空则试 MPSourceDir
	MPSourceDir     string        // 小程序源码目录（含 project.config.json）
	MPUploadTimeout time.Duration // 单次上传超时，默认 5 分钟
	AdminUser       string        // 初始超管用户名（首启动 seed 用）
	AdminPass       string        // 初始超管密码（首启动 seed 用，上线务必修改）
	CookieSecure    bool          // 鉴权 Cookie 是否加 Secure 标记（生产 HTTPS 设 true；本地 http 需 false）
	CookieDomain    string        // 鉴权 Cookie 作用域（默认空=仅当前主机；跨子域共享登录态时才设）
	MetricsEnabled  bool          // 是否开放 GET /metrics（Prometheus 文本格式；默认关，避免公网暴露流量规模）
	WxacodeTTLHours int           // 小程序码缓存有效期（小时）
	WxacodeTTL      time.Duration
	// TencentVideo 是否开放「腾讯视频」视频来源档（MP_TENCENT_VIDEO，默认关）。
	//
	// 为什么是部署级开关而不是界面开关：该档依赖腾讯视频小程序插件
	// （provider wxa75efa648b60994b），而插件必须在 app.json 里**静态声明**——
	// 声明了却没在 MP 后台「设置 → 第三方服务 → 插件管理」授权的插件，会让整个
	// 小程序在编译期就失败，连预览都传不上去。这不是「某个客户视频播不出来」，
	// 是「所有客户的整个小程序发不了版」。界面上点一下开关改不了这个事实，
	// 所以只能由部署方确认「插件已授权 + app.json 已声明」后显式打开。
	//
	// 默认关还兼一层：存量私有部署升级后行为不变（升级不得突变存量）。
	TencentVideo bool
	// AutoMigrate 启动时自动补齐未应用的数据库迁移（仅 MySQL；SQLite 走 AutoMigrate 建表）。
	// 默认开——「升级后忘了跑迁移」是本项目升级事故的主要形态，而迁移幂等、
	// 每次启动都只做增量。要完全自己掌控（如纳入发布流水线单独一步）设 AUTO_MIGRATE=0。
	AutoMigrate bool
	// RevokedPurgeInterval 过期会话黑名单（revoked_sessions）的定期清理间隔。
	// 该表只按行存在判定吊销，过期行（refresh 令牌已自然失效）留着只会无限增长。
	RevokedPurgeInterval time.Duration
	// RatePurgeInterval 限流计数表（rate_limit_hits）的定期清理间隔。
	// 与上面同理：每行只在一个时间窗口内有意义，过期后不会自己消失。
	RatePurgeInterval time.Duration
	// AuditPurgeInterval 审计日志按保留期清理的检查间隔（AUDIT_PURGE_MINUTES，默认 12 小时）。
	// 保留期以「天」计，检查频率高低没有业务差别，取低频即可。
	AuditPurgeInterval time.Duration
	// AuditRetentionDays 审计日志保留天数（AUDIT_RETENTION_DAYS，默认 0 = 不清理）。
	// 这是**界面未设置时的回落值**：后台「审计保留天数」一旦填了就以界面为准。
	// 默认 0 的理由见 audit/purge.go——删审计日志不可逆，不能靠升级顺手清掉存量。
	AuditRetentionDays int
	// LogFormat 日志形态（LOG_FORMAT：text / json，默认 text）。
	// json = 每行一个 JSON 对象，供 Loki / ELK / 云日志服务按字段检索（C8 集中日志）；
	// text 保留 [WARN] 等既有前缀，本地开发与已有 grep 规则不受影响。
	LogFormat string
	// MetricsAllowCIDR 允许访问 /metrics 的来源网段（METRICS_ALLOW_CIDR，逗号分隔）。
	// 留空 = 不额外限制（/metrics 仍由 metrics_enabled 开关默认关闭）。
	// 这是**第二道门**：即便有人在界面上把开关打开了，公网也抓不到流量规模。
	MetricsAllowCIDR []string
	// TrustedProxies 可信反代的地址/CIDR 列表，决定 c.ClientIP() 该不该相信
	// X-Forwarded-For。留空时用 DefaultTrustedProxies（回环 + 私有网段）。
	// 设成 TRUSTED_PROXIES=none 表示「前面没有反代」，一律用 TCP 对端地址。
	TrustedProxies []string
}

// DefaultTrustedProxies 可信反代的缺省网段：回环 + 三类私有网段。
//
// 为什么必须有这个缺省值：gin 的内置缺省是 0.0.0.0/0（信任一切），启动日志会打印
// 「You trusted all proxies, this is NOT safe」。在那种设置下 c.ClientIP() 直接取
// X-Forwarded-For 的**最左**一项，而这一项完全由客户端决定——于是所有按 IP 分桶的
// 限流（登录 / 刷新 / 小程序码）都能靠每次换一个伪造头绕过，锁定与防爆破形同虚设。
//
// 反过来若收得比实际更紧（例如错设成 none 但前面确有反代），所有用户会被算成
// 同一个 IP、共用一个桶——一个人就能把整站限流打满。所以缺省取「私有网段」：
// 容器网络、k8s 集群网络、同机 nginx、云 LB 的内网地址都落在里面，
// 而公网来源永远不在里面（伪造头因此不被采信，取到的是 TCP 对端真实地址）。
var DefaultTrustedProxies = []string{
	"127.0.0.1/32", "::1/128", // 同机反代（宿主机 nginx / 裸机部署）
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC1918：docker bridge、k8s、内网 LB
	"fc00::/7", // IPv6 唯一本地地址
}

// parseTrustedProxies 解析 TRUSTED_PROXIES（逗号分隔的 IP 或 CIDR）。
// 空值 → 缺省网段；"none"（不区分大小写）→ 空列表（gin 此时完全不读转发头）。
func parseTrustedProxies(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultTrustedProxies
	}
	if strings.EqualFold(raw, "none") {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return DefaultTrustedProxies
	}
	return out
}

func Load() Config {
	ttlHours := atoi(getEnv("WXACODE_TTL_HOURS", "720"), 720) // 默认 30 天
	return Config{
		DBDSN:              getEnv("DB_DSN", "root:root@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true"),
		Port:               getEnv("PORT", "8080"),
		JWTKey:             getEnv("JWT_KEY", DefaultJWTKey),
		UploadDir:          getEnv("UPLOAD_DIR", "./uploads"),
		UploadBaseURL:      getEnv("UPLOAD_BASE_URL", "http://127.0.0.1:8080/uploads"),
		StorageDriver:      getEnv("STORAGE_DRIVER", "local"),
		StoragePublic:      parseBool(getEnv("STORAGE_PUBLIC", "true")),
		StorageRegion:      getEnv("STORAGE_REGION", ""),
		MinioEndpoint:      getEnv("MINIO_ENDPOINT", ""),
		MinioAccessKey:     getEnv("MINIO_ACCESS_KEY", ""),
		MinioSecretKey:     getEnv("MINIO_SECRET_KEY", ""),
		MinioBucket:        getEnv("MINIO_BUCKET", ""),
		MinioUseSSL:        parseBool(getEnv("MINIO_USE_SSL", "false")),
		OssEndpoint:        getEnv("OSS_ENDPOINT", ""),
		OssAccessKeyID:     getEnv("OSS_ACCESS_KEY_ID", ""),
		OssAccessKeySecret: getEnv("OSS_ACCESS_KEY_SECRET", ""),
		OssBucket:          getEnv("OSS_BUCKET", ""),
		CDNEnabled:         parseBool(getEnv("CDN_ENABLED", "false")),
		CDNProvider:        getEnv("CDN_PROVIDER", ""),
		CDNAccessKeyID:     getEnv("CDN_ACCESS_KEY_ID", ""),
		CDNAccessKeySecret: getEnv("CDN_ACCESS_KEY_SECRET", ""),
		CDNEndpoint:        getEnv("CDN_ENDPOINT", ""),
		CDNResourceID:      getEnv("CDN_RESOURCE_ID", ""),
		WXAppID:            getEnv("WX_APPID", ""),
		WXAppSecret:        getEnv("WX_APPSECRET", ""),
		WXMsgToken:         getEnv("WX_MSG_TOKEN", ""),
		WXMsgAESKey:        getEnv("WX_MSG_AESKEY", ""),
		MPNodeBin:          getEnv("MP_NODE_BIN", ""),
		MPCIModuleDir:      getEnv("MP_CI_MODULE_DIR", ""),
		MPSourceDir:        getEnv("MP_SOURCE_DIR", ""),
		MPUploadTimeout:    time.Duration(atoi(getEnv("MP_UPLOAD_TIMEOUT_SEC", "300"), 300)) * time.Second,
		AdminUser:          getEnv("ADMIN_USER", "admin"),
		AdminPass:          getEnv("ADMIN_PASS", DefaultAdminPass),
		CookieSecure:       parseBool(getEnv("COOKIE_SECURE", "false")),
		CookieDomain:       getEnv("COOKIE_DOMAIN", ""),
		MetricsEnabled:     parseBool(getEnv("METRICS_ENABLED", "false")),
		WxacodeTTLHours:    ttlHours,
		WxacodeTTL:         time.Duration(ttlHours) * time.Hour,
		TencentVideo:       parseBool(getEnv("MP_TENCENT_VIDEO", "false")),
		AutoMigrate:        parseBool(getEnv("AUTO_MIGRATE", "true")),
		// 默认 6 小时：refresh token 最长 30 天，黑名单行要到那时才「过期」，
		// 故清理频率无需高，只要保证长期运行的表不会无限增长即可（启动还会跑一次）
		RevokedPurgeInterval: time.Duration(atoi(getEnv("REVOKED_PURGE_MINUTES", "360"), 360)) * time.Minute,
		RatePurgeInterval:    time.Duration(atoi(getEnv("RATE_PURGE_MINUTES", "10"), 10)) * time.Minute,
		AuditPurgeInterval:   time.Duration(atoi(getEnv("AUDIT_PURGE_MINUTES", "720"), 720)) * time.Minute,
		AuditRetentionDays:   atoi(getEnv("AUDIT_RETENTION_DAYS", "0"), 0),
		LogFormat:            getEnv("LOG_FORMAT", "text"),
		MetricsAllowCIDR:     splitList(getEnv("METRICS_ALLOW_CIDR", "")),
		TrustedProxies:       parseTrustedProxies(getEnv("TRUSTED_PROXIES", "")),
	}
}

// InsecureWarnings 返回上线前应修正的不安全配置提示（超管口令偏弱）。
//
// 与 InsecureGates 的分工：Warnings 只提示、不拦启动（改不改都不影响系统可用）；
// Gates 命中则 cmd/server 拒绝启动。判断依据是「这个默认值是否已经不 secret」——
// JWT 密钥与初始口令都原样写在公开文档里，属于已知凭据，必须拦；
// 口令长度只是强弱问题，交给运维判断，不卡启动。
func (c Config) InsecureWarnings() []string {
	var ws []string
	if len(c.AdminPass) < MinAdminPassLen {
		ws = append(ws, "初始超管口令长度不足 8 位，建议通过 ADMIN_PASS 设置更强口令并在首登后及时改密")
	}
	return ws
}

// InsecureGates 返回「命中即拒绝在生产启动」的安全硬门槛。
//
// 设计取舍：这里只放「带默认凭据上线等于把后台开放给所有人」的项，且每一项都能靠
// 改一个环境变量自愈（不像 db_dsn / cookie_* 那样会把「改回来的入口」一起弄丢），
// 所以「拒绝启动」是安全且不危险的。逃生门是 ALLOW_INSECURE=1（仅本地联调用）。
func (c Config) InsecureGates() []string {
	var gs []string
	if c.JWTKey == DefaultJWTKey {
		gs = append(gs, "JWT_KEY 仍为内置缺省值 change-me：任何人都能伪造任意身份的 token，请用 openssl rand -base64 32 生成后设置 JWT_KEY")
	} else if len(c.JWTKey) < MinJWTKeyLen {
		gs = append(gs, "JWT_KEY 长度不足 16 位，可被暴力破解，请用 openssl rand -base64 32 生成后设置 JWT_KEY")
	}
	if c.AdminPass == DefaultAdminPass {
		gs = append(gs, "初始超管口令仍为内置缺省值（该口令已随演示账号写入公开文档）：任何人都能登录后台，请设置 ADMIN_PASS 后启动")
	}
	return gs
}

// getEnv 取配置值：环境变量 > 配置文件（CONFIG_FILE）> 内置缺省值。
// 走 lookup 而不是直接读 os.Getenv，是为了让安装向导写进 runtime.env 的值
// 在重启后生效；空串/纯空白一律视为未设置（见 file.go 的优先级说明）。
func getEnv(k, def string) string {
	if v, ok := lookup(k); ok {
		return v
	}
	return def
}

// splitList 解析逗号分隔的列表（去空白、丢空项）。
func splitList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseBool(s string) bool {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false
	}
	return b
}
