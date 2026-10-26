package config

import (
	"strings"
	"time"

	"weichuan/internal/setting"
)

// str 取界面设置值；**空串一律视同「未设置」**，保留环境变量值。
// 不能直接用 setting.GetString(key, def)：seed 会把这些 key 以空串写进库，
// GetString 读到的是「有这条记录、值为空」，会把环境变量值覆盖成空——
// 这正是「只配了环境变量的老部署升级后凭空失效」的成因。
func str(key, def string) string {
	if v := strings.TrimSpace(setting.GetString(key, "")); v != "" {
		return v
	}
	return def
}

// OverlayFromSettings 用系统设置（sys_setting）覆盖环境变量加载出的配置。
//
// 语义：**空串 = 未在界面设置 → 保留环境变量值**。
// 这样同一份部署既能纯环境变量跑（界面全空），也能在界面里逐项覆盖（界面优先），
// 且把界面上的值清空后会自动退回环境变量，不会出现「改坏了退不回去」的死角。
//
// 调用时机：setting.Init(db) 之后、构造上传器 / CDN 刷新器之前（见 cmd/server）。
// 覆盖结果只影响本次进程：存储与 CDN 客户端在启动时构造，因此这些项标记为
// 「需重启生效」（setting 的 Restart 元数据），界面会明确提示。
func (c *Config) OverlayFromSettings() {
	// 文件存储（MinIO / S3 兼容 与 阿里云 OSS 共用同一组界面字段，按驱动二选一）
	c.StorageDriver = str("storage_driver", c.StorageDriver)
	c.StoragePublic = setting.GetBool("storage_public", c.StoragePublic)
	c.MinioEndpoint = str("storage_endpoint", c.MinioEndpoint)
	c.MinioBucket = str("storage_bucket", c.MinioBucket)
	c.MinioAccessKey = str("storage_access_key", c.MinioAccessKey)
	c.MinioSecretKey = str("storage_secret_key", c.MinioSecretKey)
	c.MinioUseSSL = setting.GetBool("storage_use_ssl", c.MinioUseSSL)
	// storage_region 对 minio / s3 两个 S3 兼容驱动生效（oss 客户端没有区域参数）
	c.StorageRegion = str("storage_region", c.StorageRegion)
	c.OssEndpoint = str("storage_endpoint", c.OssEndpoint)
	c.OssBucket = str("storage_bucket", c.OssBucket)
	c.OssAccessKeyID = str("storage_access_key", c.OssAccessKeyID)
	c.OssAccessKeySecret = str("storage_secret_key", c.OssAccessKeySecret)

	c.UploadDir = str("upload_dir", c.UploadDir)
	c.UploadBaseURL = str("upload_base_url", c.UploadBaseURL)

	// CDN 缓存刷新：厂商 + 通用凭据位 + 资源标识
	c.CDNEnabled = setting.GetBool("cdn_enabled", c.CDNEnabled)
	c.CDNProvider = str("cdn_provider", c.CDNProvider)
	c.CDNAccessKeyID = str("cdn_access_key_id", c.CDNAccessKeyID)
	c.CDNAccessKeySecret = str("cdn_access_key_secret", c.CDNAccessKeySecret)
	// 端点留空时由各厂商实现按 provider 取自己的默认域名（写死一个会串厂商）
	c.CDNEndpoint = str("cdn_endpoint", c.CDNEndpoint)
	c.CDNResourceID = str("cdn_resource_id", c.CDNResourceID)

	// 微信：AppID / AppSecret 另有请求期解析（resolveWXAppID / resolveWXAppSecret，改完即时生效），
	// 此处同步一份到 cfg，保证启动日志与其它读取点看到的值一致。
	c.WXAppID = str("wx_appid", c.WXAppID)
	c.WXAppSecret = str("wx_appsecret", c.WXAppSecret)
	// 消息推送凭据同样在请求期解析（resolveWXMsgToken / resolveWXMsgAESKey），
	// 这里同步一份只为让启动日志与其它读取点看到一致的值。
	c.WXMsgToken = str("wx_msg_token", c.WXMsgToken)
	c.WXMsgAESKey = str("wx_msg_aeskey", c.WXMsgAESKey)

	// 后台上传小程序代码：三项「服务器上有什么」，界面优先、空串回落环境变量。
	// 上传时按请求期再叠一次（见 handler.mpUploadCfg），这里同步是为了让
	// 启动日志与其它读取点看到的值一致。
	c.MPNodeBin = str("mp_node_bin", c.MPNodeBin)
	c.MPCIModuleDir = str("mp_ci_module_dir", c.MPCIModuleDir)
	c.MPSourceDir = str("mp_source_dir", c.MPSourceDir)
	// 超时不挂环境变量（见 setting 注册处的说明），只在界面配了值时覆盖；
	// 填 0 / 负数视为「没配」，保留进程内取值。
	if v := setting.GetInt("mp_upload_timeout_sec", 0); v > 0 {
		c.MPUploadTimeout = time.Duration(v) * time.Second
	}
}
