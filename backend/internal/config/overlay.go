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
	// 文件存储只叠「目录」这一类部署期事实。驱动 / 端点 / 桶 / 凭据 / 基址 / 公开读
	// 的唯一真值源是 storage_profiles：启动期由 ApplyPlatformConfig 回填 cfg，
	// 这里再从 sys_setting 读一遍就等于给同一件事留第二处入口。
	c.UploadDir = str("upload_dir", c.UploadDir)
	// 本地镜像目录与 UploadDir 一样属于部署期事实：界面留空就沿用 MIRROR_DIR，
	// 启动后由 ValidateMirrorDirs 统一校验两者不重叠。
	c.MirrorDir = str("mirror_dir", c.MirrorDir)

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
