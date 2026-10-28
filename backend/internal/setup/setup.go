// Package setup 首次部署体验：未配置模式下的引导与安装向导。
//
// 背景：生产版 cmd/server 启动即连库，库地址没配就直接 log.Fatal——运维看到的是
// 一行冷冰冰的日志，不知道下一步该做什么。本包在「确实没配置」时让进程起来，
// 在 /setup 提供完整的首次部署体验：
//
//   - 安装向导（默认）：表单填写数据库连接与管理员账号 → 测试连接 → 一键安装
//     （建库/建表/建超管/写配置文件）→ 自重启 → 直接进登录页；
//   - 手动配置（高级）：生成一份可复制的 .env 文本，运维自己抄。
//
// 安装器安全边界（安装页是未鉴权页面，这些是底线，逐条有测试钉死）：
//   - 仅在「DB_DSN 未提供」时可安装（Unconfigured），正常模式不挂写接口，
//     结构上不可重放；进程内还有一次性锁（installDone）双保险；
//   - 拒绝覆盖已初始化的数据库（admins 表有数据即拒）；
//   - 写接口带同源检查（sameOrigin），挡跨源 CSRF 与 DNS rebinding；
//   - 响应与日志不出现明文口令 / 完整 DSN（回显一律掩码）；
//   - 配置文件 0600、原子写（临时文件 + rename）。
//
// 配置去向：安装向导写的是 CONFIG_FILE（默认 ./data/runtime.env），不是环境变量。
// 优先级只有一条规则——环境变量 > 配置文件 > 内置缺省——不存在双源歧义；
// 已在环境变量里显式给的部署（老 .env 用户）行为完全不变。
package setup

import (
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"weichuan/internal/config"
)

// required 首次启动必须具备的配置项。
// 判据是「环境变量与配置文件里是否都没有值」，不能用 config.Load() 之后的值：
// DB_DSN 与 JWT_KEY 都有内置默认值，用默认值判断会把「没配」误判成「配了」。
var required = []struct{ env, why string }{
	{"DB_DSN", "数据库连接串；不填则服务无法加载任何业务功能"},
	{"JWT_KEY", "登录令牌签名密钥；沿用内置缺省值等于任何人都能伪造身份"},
	{"ADMIN_PASS", "初始超管密码；首次启动用于创建超管账号"},
}

// MySQLHint 检测到编排环境（compose 内置 MySQL）时给表单的预填值。
// 刻意不含密码：响应是未鉴权的，密码只能由运维自己从 .env 抄进来。
type MySQLHint struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
	Note     string `json:"note"`
}

// Missing 一条待配项
type Missing struct {
	Env   string `json:"env"`
	Why   string `json:"why"`
	Value string `json:"value"`
}

// Status 引导页 / 状态接口所需信息
type Status struct {
	Configured  bool       `json:"configured"`
	Missing     []Missing  `json:"missing"`
	EnvText     string     `json:"envText"`
	Hint        string     `json:"hint"`
	Installable bool       `json:"installable"` // 是否可用安装向导（未配置即可用）
	Engines     []string   `json:"engines"`     // 可选的数据库引擎
	SQLitePath  string     `json:"sqlitePath"`  // 建议的 SQLite 文件路径
	ConfigFile  string     `json:"configFile"`  // 安装后配置的写入位置
	AdminUser   string     `json:"adminUser"`   // 建议的管理员用户名
	MySQLHint   *MySQLHint `json:"mysqlHint,omitempty"`
}

// Unconfigured 判定是否为「首次运行、尚未配置」。
// 看环境变量与配置文件两层（config.Resolved）：安装向导会把 DB_DSN 写进
// 配置文件，重启后必须据此判定为「已配置」，否则装完又回到引导页。
// 只看 DB_DSN：它是唯一「没它就完全起不来、且无法存进库里」的自举依赖。
func Unconfigured() bool {
	return config.Resolved("DB_DSN") == ""
}

// StatusPayload 当前引导/安装状态。Configured 一律以 DB_DSN 为准（与 Unconfigured
// 一致），Missing 则列出所有缺失项，便于运维一次补齐而不是改一次重启一次。
func StatusPayload() Status {
	missing := make([]Missing, 0, len(required))
	for _, r := range required {
		if config.Resolved(r.env) == "" {
			missing = append(missing, Missing{Env: r.env, Why: r.why, Value: suggest(r.env)})
		}
	}
	cfgFile := config.ConfigFilePath()
	return Status{
		Configured: !Unconfigured(),
		Missing:    missing,
		EnvText:    EnvText(),
		Hint: "推荐直接使用本页下方的安装向导；也可以把配置写进 .env 后重启。" +
			"优先级：环境变量 > " + cfgFile + " > 内置缺省值。",
		Installable: Unconfigured(),
		Engines:     []string{"mysql", "sqlite"},
		SQLitePath:  filepath.Join(filepath.Dir(cfgFile), "weichuan.db"),
		ConfigFile:  cfgFile,
		AdminUser:   "admin",
		MySQLHint:   composeMySQLHint(),
	}
}

// composeMySQLHint 检测 docker-compose 编排环境：MYSQL_ROOT_PASSWORD 由 compose
// 必填注入，命中即按内置 MySQL 服务预填表单（密码仍由运维手填，不进响应）。
func composeMySQLHint() *MySQLHint {
	if strings.TrimSpace(os.Getenv("MYSQL_ROOT_PASSWORD")) == "" {
		return nil
	}
	return &MySQLHint{
		Host:     "mysql",
		Port:     "3306",
		User:     "root",
		Database: "weichuan",
		Note: "检测到编排环境（MYSQL_ROOT_PASSWORD 已设置），已按 docker-compose 内置 MySQL 预填；" +
			"密码请填 .env 里的 MYSQL_ROOT_PASSWORD。",
	}
}

// EnvText 生成一份可直接写进 .env 的配置。密钥一律现场生成：
// 绝不能给「已知默认值」，那等于把公开文档里的凭据写进客户配置。
func EnvText() string {
	var b strings.Builder
	b.WriteString("# weichuan 初始配置（由安装引导生成）\n")
	b.WriteString("# 方式一（推荐）：打开 /setup 使用安装向导，它会把这些值写进 " +
		config.ConfigFilePath() + " 并自动重启。\n")
	b.WriteString("# 方式二：把下面内容写进 .env（与 docker-compose.yml 同级）后重启；" +
		"环境变量优先级高于配置文件。\n\n")
	if config.Resolved("DB_DSN") == "" {
		b.WriteString("DB_DSN=root:请改成数据库密码@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true\n")
		b.WriteString("# 单机想免 MySQL：把上面一行换成 DB_DSN=sqlite:///data/weichuan.db\n\n")
	}
	if config.Resolved("JWT_KEY") == "" {
		b.WriteString("JWT_KEY=" + secret(32) + "\n\n")
	}
	if config.Resolved("ADMIN_PASS") == "" {
		b.WriteString("ADMIN_USER=admin\n")
		b.WriteString("ADMIN_PASS=" + secret(12) + "\n\n")
	}
	b.WriteString("# 生产 HTTPS 环境务必改成 true，否则登录 Cookie 可能走明文\n")
	b.WriteString("COOKIE_SECURE=false\n")
	return b.String()
}

func suggest(env string) string {
	switch env {
	case "DB_DSN":
		return "root:请改成数据库密码@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true"
	case "JWT_KEY":
		return secret(32)
	case "ADMIN_PASS":
		return secret(12)
	}
	return ""
}

// secret 生成 n 字节强度的随机串（base64url）。
// crypto/rand 失败时返回空串而不是回退常量：宁可让运维看到「缺值」，
// 也不能把可预测的凭据塞进配置里。
func secret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// piiKey 生成 PII 静态加密密钥：32 个随机字节的**标准** base64（44 字符）。
//
// 刻意不复用 secret()：那个用的是 RawURLEncoding，而 pii 包按
// base64.StdEncoding 解码——RawURL 的 '-' / '_' 会被判为非法字符，
// 拿 secret(32) 的返回值当 PII_ENC_KEY 会直接「密钥格式错误」起不来，
// 且这种错误只在换了随机值之后才偶发出现，极难排查。
func piiKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Engine 构造「未配置模式」的引擎：与主流程一致的 Recovery，只挂引导/安装路由。
func Engine() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	Register(r)
	return r
}

// Register 注册引导/安装模式路由。
// 写接口（test/install）只在本模式挂：正常模式的进程根本进不到安装分支，
// 挂上反而给「重放安装页重置系统」留了门。
func Register(r *gin.Engine) {
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/setup") })
	r.GET("/setup", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, Page())
	})
	api := r.Group("/api/setup", sameOrigin())
	{
		api.POST("/test", handleTest)
		api.POST("/install", handleInstall)
	}
	RegisterStatus(r)
	// 就绪探针：配置都没给，业务不可用，必须如实返回 503。
	// 与主流程 /healthz 同口径——「进程在但不可用」不能报 200，
	// 否则编排的 depends_on/service_healthy 与外部探活都会误判。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unconfigured",
			"db":     "unknown",
			"hint":   "DB_DSN 未提供，服务处于初始配置模式；请访问 /setup 完成安装",
		})
	})
}

// RegisterStatus 只挂状态查询接口。正常模式也会挂它：前端据此判断是否显示引导，
// 接口查不到（404）即视为已配置，不会误触发引导页。
func RegisterStatus(r *gin.Engine) {
	r.GET("/api/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": StatusPayload()})
	})
}

// RegisterSetupAlias 正常模式下的 /setup 别名：安装完成后还有人手动敲 /setup，
// 给个 302 回首页，而不是一屏裸 404。
func RegisterSetupAlias(r *gin.Engine) {
	r.GET("/setup", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
}

func handleTest(c *gin.Context) {
	var req InstallReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请求格式错误：" + err.Error()})
		return
	}
	res, err := TestConnection(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": res})
}

func handleInstall(c *gin.Context) {
	var req InstallReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请求格式错误：" + err.Error()})
		return
	}
	res, err := Install(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": res})
	// 响应写入后再安排自重启（ScheduleRestart 内部 goroutine 延时，给 flush 留时间）。
	// 放在这里而不是 Install 里：单测要能跑完整安装流程而不会把测试进程 exec 掉。
	if res.Restarted {
		ScheduleRestart()
	}
}

// sameOrigin 写接口的同源检查。安装页是未鉴权页面，挡两类攻击：
//   - 跨站 CSRF：运维浏览器里的恶意页面往 /api/setup/install 发 POST；
//   - DNS rebinding：恶意页面把域名绑到受害者 IP，浏览器请求会带上受害者 Host。
//
// 判据是 Origin / Referer 与「浏览器真正访问的 Host」是否一致（不比对 scheme：https
// 终止在反代时 Origin 是 https、到后端是 http，比对 scheme 会误杀正常部署）。
// 两个头都没有的请求（curl / 脚本 / 服务端调用）放行：安装窗口只存在于
// 「首次配置前」且一次性，不放行只会挡住运维自动化，挡不住任何攻击。
func sameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := effectiveHost(c)
		src := c.GetHeader("Origin")
		if src == "" {
			src = c.GetHeader("Referer")
		}
		if src != "" && !sameHost(src, host) {
			// 拒绝时必须把「实际收到了什么」回给调用方。2026-10-28 线上事故：
			// 宝塔站点的反代没写 proxy_set_header Host，nginx 默认把它改成上游地址
			// 127.0.0.1:8080，安装向导第一步「测试连接」就 403；而当时只回一句
			// 「跨源请求被拒绝」，前端又只显示
			// "Request failed with status code 403"，运维完全无从下手。
			c.JSON(http.StatusForbidden, gin.H{
				"code": 403,
				"msg": "跨源请求被拒绝：浏览器来源 " + hostOf(src) + "，后端收到的 Host 是 " + host +
					"（反代没透传 Host 时会这样：在反代 /api 的 location 里加 proxy_set_header Host $host; 再重载 nginx；用 CDN / 负载均衡时让对方带上 X-Forwarded-Host）",
				"origin": hostOf(src),
				"host":   host,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// effectiveHost 取浏览器真正访问的 Host（仅用于同源比较）。
//
// **反代会把 Host 变成上游地址**：nginx 的 proxy_set_header 默认值是 $proxy_host，
// 面板类工具（宝塔等）若没显式写 `proxy_set_header Host $host;`，后端收到的就是
// 127.0.0.1:8080，与浏览器 Origin 里的域名必然对不上，安装向导直接判成跨源。
// 所以先看反代注入的 X-Forwarded-Host。
//
// 只有 Host 本身是回环/私有地址时才采信它：Host 已是公网域名说明能直接比对，
// 不必再引入一个客户端可写的头（不给伪造多留一条路）。
func effectiveHost(c *gin.Context) string {
	host := c.Request.Host
	if !internalHost(host) {
		return host
	}
	// X-Forwarded-Host 可能是逗号分隔的链（多级反代），取最左的原始 Host
	if xfh := c.GetHeader("X-Forwarded-Host"); xfh != "" {
		if first := strings.TrimSpace(strings.Split(xfh, ",")[0]); first != "" {
			return first
		}
	}
	return host
}

// internalHost 判断 host（可带端口）是否指向本机或内网。
// 用途见 effectiveHost：区分「反代改写了 Host」与「浏览器真的就访问这个域名」。
func internalHost(host string) bool {
	h := host
	if strings.HasPrefix(h, "[") { // IPv6 字面量：[::1]:8080
		if i := strings.Index(h, "]"); i > 0 {
			h = h[1:i]
		}
	} else if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()
}

// hostOf 取 URL 的宿主。只用于错误信息（判定仍走 sameHost）。
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// sameHost 取 URL 的宿主与请求 Host 比较（都转小写；端口不同即不同源）。
func sameHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// Page 返回安装向导 HTML。页面是自包含的（内联 CSS/JS、无外部依赖）：
// 安装场景下前端可能还没构建部署，后端必须能独立把这事办了。
// 所有动态内容（预填值、配置文本）都在页面加载后从 /api/setup/status 取，
// 因此本模板不需要任何服务端占位符。
func Page() string {
	return pageTpl
}

const pageTpl = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>安装向导 · 电子名片</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  [hidden] { display: none !important; }
  body { margin:0; background:#f5f7fa; font-family:-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif; color:#303133; }
  .wrap { max-width:760px; margin:40px auto; padding:0 16px 48px; }
  .hd { margin-bottom:20px; }
  .brand { font-size:13px; color:#909399; letter-spacing:.5px; }
  h1 { margin:6px 0 8px; font-size:22px; }
  .lead { margin:0; color:#606266; font-size:14px; line-height:1.7; }
  .steps { list-style:none; display:flex; gap:10px; margin:0 0 16px; padding:0; }
  .step { flex:1; display:flex; align-items:center; gap:8px; padding:10px 12px; border:1px solid #ebeef5; border-radius:8px; background:#fff; color:#909399; font-size:13px; }
  .step b { width:22px; height:22px; border-radius:50%; background:#f0f2f5; color:#909399; display:inline-flex; align-items:center; justify-content:center; font-size:12px; flex:none; }
  .step.active { border-color:#409eff; color:#303133; }
  .step.active b { background:#409eff; color:#fff; }
  .block { background:#fff; border:1px solid #ebeef5; border-radius:8px; padding:20px; margin-bottom:16px; }
  .block.details { padding:0; }
  .block.details summary { cursor:pointer; padding:14px 20px; font-size:14px; font-weight:500; list-style:none; }
  .block.details summary::before { content:'▸'; margin-right:8px; color:#909399; }
  .block.details[open] summary::before { content:'▾'; }
  .block.details > *:not(summary) { margin-left:20px; margin-right:20px; }
  .block.details > pre { margin-left:20px; margin-right:20px; }
  h2 { margin:0 0 14px; font-size:15px; }
  .engines { display:grid; grid-template-columns:1fr 1fr; gap:12px; margin-bottom:16px; }
  .engine { border:1px solid #ebeef5; border-radius:8px; padding:14px 16px; cursor:pointer; background:#fff; }
  .engine:hover { border-color:#c0d9f5; }
  .engine b { display:block; font-size:15px; margin-bottom:6px; }
  .engine p { margin:0; font-size:12px; color:#909399; line-height:1.6; }
  .engine.active { border-color:#409eff; box-shadow:0 0 0 1px #409eff inset; }
  .grid { display:grid; grid-template-columns:1fr 1fr; gap:12px 16px; }
  .grid label { display:flex; flex-direction:column; gap:6px; font-size:13px; color:#606266; }
  .grid .wide { grid-column:1 / -1; }
  .grid input[type=text], .grid input[type=password] { height:36px; border:1px solid #dcdfe6; border-radius:4px; padding:0 10px; font-size:14px; width:100%; }
  .grid input:focus { outline:none; border-color:#409eff; }
  .chk { display:flex; align-items:center; gap:8px; font-size:13px; color:#606266; grid-column:1 / -1; }
  .tip { margin:10px 0 0; font-size:12px; color:#909399; line-height:1.7; }
  .dsn { margin:14px 0 0; font-size:12px; color:#606266; word-break:break-all; }
  code { background:#f5f7fa; border:1px solid #ebeef5; border-radius:4px; padding:2px 8px; font-size:12px; }
  pre { background:#f5f7fa; border:1px solid #ebeef5; border-radius:6px; padding:14px 16px; overflow-x:auto; font-size:12px; line-height:1.7; white-space:pre; }
  .banner { margin:16px 0 0; padding:10px 14px; border-radius:6px; font-size:13px; line-height:1.6; }
  .banner.ok { background:#f0f9eb; border:1px solid #e1f3d8; color:#529b2e; }
  .banner.err { background:#fef0f0; border:1px solid #fde2e2; color:#f23c3c; }
  .confirm { margin-top:16px; padding:14px 16px; border:1px solid #fde2e2; background:#fefafa; border-radius:8px; }
  .confirm p { margin:0 0 8px; font-size:13px; color:#606266; line-height:1.7; }
  .actions { display:flex; gap:12px; justify-content:flex-end; margin-top:18px; }
  .btn { border:none; border-radius:4px; padding:9px 18px; font-size:14px; cursor:pointer; }
  .btn.primary { background:#409eff; color:#fff; }
  .btn.primary:hover { background:#66b1ff; }
  .btn.ghost { background:#fff; border:1px solid #dcdfe6; color:#606266; }
  .btn.ghost:hover { color:#409eff; border-color:#c0d9f5; }
  .btn.danger { background:#f56c6c; color:#fff; }
  .btn.danger:hover { background:#f78989; }
  .btn:disabled { opacity:.6; cursor:not-allowed; }
  .psteps { list-style:none; margin:0; padding:0; }
  .psteps li { display:flex; align-items:center; padding:10px 0; font-size:14px; color:#909399; border-bottom:1px dashed #f0f2f5; }
  .psteps li:last-child { border-bottom:none; }
  .psteps li.run { color:#303133; }
  .psteps li.done { color:#303133; }
  .psteps i { display:inline-block; width:16px; height:16px; border-radius:50%; border:2px solid #dcdfe6; margin-right:10px; flex:none; position:relative; }
  .psteps li.done i { background:#67c23a; border-color:#67c23a; }
  .psteps li.done i::after { content:''; position:absolute; left:4px; top:1px; width:4px; height:8px; border:solid #fff; border-width:0 2px 2px 0; transform:rotate(45deg); }
  .psteps li.run i { border-color:#409eff; border-top-color:transparent; animation:spin .8s linear infinite; }
  .psteps li.fail i { background:#f56c6c; border-color:#f56c6c; }
  @keyframes spin { to { transform:rotate(360deg); } }
  .acc { list-style:none; margin:8px 0 0; padding:0; font-size:13px; line-height:1.6; }
  .acc li { padding:6px 0; border-bottom:1px dashed #f0f2f5; color:#303133; }
  .acc b { display:inline-block; min-width:64px; color:#606266; font-weight:500; }
  .acc i { color:#909399; font-style:normal; margin-left:8px; }
  .tip.warn { color:#e6a23c; }
  .note { margin:20px 0 0; font-size:12px; color:#909399; line-height:1.8; }
  @media (max-width: 640px) {
    .wrap { margin-top:20px; }
    .steps { flex-direction:column; }
    .engines, .grid { grid-template-columns:1fr; }
    .actions { flex-direction:column; }
    .btn { width:100%; }
  }
</style>
</head>
<body>
<div class="wrap">
  <header class="hd">
    <div class="brand">微传名片</div>
    <h1>安装向导</h1>
    <p class="lead">首次部署需要两步：连接数据库、创建管理员账号。保存后服务会自动重启，随后即可登录使用。</p>
  </header>

  <ol class="steps" id="steps">
    <li class="step active" data-s="1"><b>1</b><span>数据库</span></li>
    <li class="step" data-s="2"><b>2</b><span>管理员账号</span></li>
    <li class="step" data-s="3"><b>3</b><span>完成</span></li>
  </ol>

  <section id="form-area">
    <div class="block">
      <h2>选择数据库</h2>
      <div class="engines">
        <div class="engine active" data-engine="mysql">
          <b>MySQL</b>
          <p>适合生产与多人使用。需要一台可访问的 MySQL 服务，docker compose 部署已内置。</p>
        </div>
        <div class="engine" data-engine="sqlite">
          <b>SQLite</b>
          <p>零依赖单机版，数据即一个文件。适合试用与小规模内部使用；不支持多副本并发写，请勿放在网络盘。</p>
        </div>
      </div>

      <div id="mysql-fields" class="grid">
        <label>主机<input id="f-dbHost" type="text" value="127.0.0.1" autocomplete="off"></label>
        <label>端口<input id="f-dbPort" type="text" value="3306" inputmode="numeric" autocomplete="off"></label>
        <label>库名<input id="f-dbName" type="text" value="weichuan" autocomplete="off"></label>
        <label>用户名<input id="f-dbUser" type="text" value="root" autocomplete="off"></label>
        <label>密码<input id="f-dbPassword" type="password" autocomplete="new-password"></label>
        <div></div>
        <div class="chk"><input type="checkbox" id="f-createDatabase" checked><span>数据库不存在时自动创建</span></div>
        <p class="tip" id="compose-tip" hidden></p>
      </div>

      <div id="sqlite-fields" class="grid" hidden>
        <label class="wide">数据文件路径<input id="f-sqlitePath" type="text" autocomplete="off"></label>
        <p class="tip">相对路径相对于服务进程的工作目录；目录不存在时会自动创建。</p>
      </div>

      <p class="dsn">连接串预览：<code id="dsn-preview"></code></p>
    </div>

    <div class="block">
      <h2>管理员账号</h2>
      <div class="grid">
        <label>用户名<input id="f-adminUser" type="text" value="admin" autocomplete="off"></label>
        <label>密码<input id="f-adminPass" type="password" autocomplete="new-password"></label>
        <label>确认密码<input id="f-adminPass2" type="password" autocomplete="new-password"></label>
      </div>
      <p class="tip">平台超管账号。登录后可在后台右上角「修改密码」随时更换。</p>
    </div>

    <div class="block">
      <h2>初始数据</h2>
      <p class="tip" style="margin-top:0">默认<b>不装载任何业务数据</b>：装完是一个干净的空系统，随后由平台超管在「企业管理」里新建企业，再逐步录入信息、员工与内容。</p>
      <div class="chk"><input type="checkbox" id="f-loadDemoData"><span>同时装载演示企业数据（朗汇机电样板：企业信息、8 个业绩分类、10 个项目案例、4 项服务及配套图片素材）</span></div>
      <p class="tip">用于先看到成品效果，内容可在后台逐条修改或删除。装载后会额外生成两个演示登录账号，口令在安装完成后一次性显示。</p>
    </div>

    <details class="block details">
      <summary>高级选项</summary>
      <div class="grid">
        <label class="wide">JWT 密钥（留空由服务端自动生成）<input id="f-jwtKey" type="text" autocomplete="off"></label>
        <div class="chk"><input type="checkbox" id="f-cookieSecure"><span>Cookie 强制 HTTPS（站点启用 https 后再勾选）</span></div>
      </div>
    </details>

    <div class="banner" id="banner" hidden></div>

    <div class="actions">
      <button type="button" class="btn ghost" id="btn-test">测试连接</button>
      <button type="button" class="btn primary" id="btn-install">开始安装</button>
    </div>

    <div id="confirm-bar" class="confirm" hidden>
      <p>确认按以下配置安装？目标数据库将被初始化，管理员账号按上面填写的内容创建。此操作只需一次。</p>
      <p class="dsn"><code id="confirm-dsn"></code></p>
      <p id="confirm-demo"></p>
      <div class="actions">
        <button type="button" class="btn ghost" id="btn-cancel">取消</button>
        <button type="button" class="btn danger" id="btn-confirm">确认安装</button>
      </div>
    </div>
  </section>

  <section id="progress-area" class="block" hidden>
    <h2>安装进度</h2>
    <ul class="psteps" id="psteps">
      <li data-p="1"><i></i><span>写入配置并初始化数据库</span></li>
      <li data-p="2"><i></i><span>重启服务并加载配置</span></li>
      <li data-p="3"><i></i><span>完成，进入登录页</span></li>
    </ul>
    <p class="tip" id="progress-tip"></p>
    <div id="demo-box" hidden></div>
    <div class="actions"><button type="button" class="btn ghost" id="btn-recheck" hidden>重新检测</button></div>
  </section>

  <details class="block details">
    <summary>手动配置（不使用安装向导）</summary>
    <p class="tip">把下面内容写进 <code>.env</code>（与 docker-compose.yml 同级）后重启。优先级：环境变量 &gt; <span id="config-file-hint"></span> &gt; 内置缺省值。</p>
    <pre id="env-text"></pre>
    <button type="button" class="btn ghost" id="btn-copy">复制配置</button>
  </details>

  <p class="note">安全说明：安装向导仅在首次配置前可用，安装一次后即关闭；不会覆盖已有数据的数据库；配置以 0600 权限写入 <span id="config-file-note"></span>；响应与日志中不出现明文口令。</p>
</div>
<script>
(function () {
  var state = { engine: 'mysql' };
  function $(id) { return document.getElementById(id); }
  function val(id) { return $(id).value.trim(); }
  function raw(id) { return $(id).value; }
  function chk(id) { return $(id).checked; }

  function showBanner(kind, msg) {
    var b = $('banner');
    b.hidden = false;
    b.textContent = msg;
    b.className = 'banner ' + kind;
  }
  function hideBanner() { $('banner').hidden = true; }

  function setBusy(on) {
    $('btn-test').disabled = on;
    $('btn-install').disabled = on;
    $('btn-test').textContent = on ? '测试中…' : '测试连接';
    $('btn-install').textContent = on ? '安装中…' : '开始安装';
  }

  function setStep(n) {
    var items = document.querySelectorAll('.step');
    for (var i = 0; i < items.length; i++) {
      var s = parseInt(items[i].getAttribute('data-s'), 10);
      items[i].className = 'step' + (s <= n ? ' active' : '');
    }
  }

  function pstep(n, st) {
    var li = document.querySelector('#psteps li[data-p="' + n + '"]');
    if (li) li.className = st;
  }

  function payload() {
    var p = {
      engine: state.engine,
      adminUser: val('f-adminUser'),
      adminPass: raw('f-adminPass'),
      adminPass2: raw('f-adminPass2'),
      jwtKey: val('f-jwtKey'),
      cookieSecure: chk('f-cookieSecure'),
      loadDemoData: chk('f-loadDemoData')
    };
    if (state.engine === 'mysql') {
      p.dbHost = val('f-dbHost');
      p.dbPort = val('f-dbPort');
      p.dbUser = val('f-dbUser');
      p.dbPassword = raw('f-dbPassword');
      p.dbName = val('f-dbName');
      p.createDatabase = chk('f-createDatabase');
    } else {
      p.sqlitePath = val('f-sqlitePath');
    }
    return p;
  }

  function previewText() {
    if (state.engine === 'mysql') {
      var pw = raw('f-dbPassword') ? '******' : '';
      return val('f-dbUser') + ':' + pw + '@tcp(' + val('f-dbHost') + ':' +
        (val('f-dbPort') || '3306') + ')/' + val('f-dbName') + '?charset=utf8mb4&parseTime=true';
    }
    return 'sqlite://' + val('f-sqlitePath');
  }
  function preview() { $('dsn-preview').textContent = previewText(); }

  function post(url, data, cb) {
    fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data)
    }).then(function (r) { return r.json(); }).then(function (body) {
      cb(body);
    }).catch(function () {
      cb({ code: 1, msg: '网络请求失败，请检查服务后重试' });
    });
  }

  // 引擎切换
  var cards = document.querySelectorAll('.engine');
  for (var i = 0; i < cards.length; i++) {
    cards[i].addEventListener('click', function () {
      var e = this.getAttribute('data-engine');
      state.engine = e;
      for (var j = 0; j < cards.length; j++) {
        cards[j].className = 'engine' + (cards[j] === this ? ' active' : '');
      }
      $('mysql-fields').hidden = e !== 'mysql';
      $('sqlite-fields').hidden = e !== 'sqlite';
      hideBanner();
      preview();
    });
  }

  // 输入即时刷新连接串预览
  var inputs = document.querySelectorAll('#form-area input');
  for (var k = 0; k < inputs.length; k++) {
    inputs[k].addEventListener('input', preview);
  }

  $('btn-test').addEventListener('click', function () {
    hideBanner();
    setBusy(true);
    post('/api/setup/test', payload(), function (body) {
      setBusy(false);
      if (body && body.code === 0) {
        var d = body.data || {};
        showBanner('ok', '连接正常（' + (d.driver || '') + '）' + (d.note ? '：' + d.note : ''));
      } else {
        showBanner('err', (body && body.msg) || '连接失败');
      }
    });
  });

  $('btn-install').addEventListener('click', function () {
    hideBanner();
    // 客户端预检与服务端同规则，只为少一次往返；真正的判据在服务端
    if (state.engine === 'mysql') {
      if (!val('f-dbHost') || !val('f-dbUser') || !val('f-dbName')) {
        showBanner('err', '请填写完整的 MySQL 连接信息（主机、用户名、库名）');
        return;
      }
    } else if (!val('f-sqlitePath')) {
      showBanner('err', '请填写 SQLite 数据文件路径');
      return;
    }
    if (raw('f-adminPass').length < 8) {
      showBanner('err', '管理员密码至少 8 位');
      return;
    }
    if (raw('f-adminPass') !== raw('f-adminPass2')) {
      showBanner('err', '两次输入的管理员密码不一致');
      return;
    }
    $('confirm-dsn').textContent = previewText();
    $('confirm-demo').textContent = chk('f-loadDemoData')
      ? '初始数据：将同时装载演示企业数据（朗汇机电样板内容 + 图片素材），装完后可在后台修改或删除。'
      : '初始数据：不装载任何业务数据，装完是干净的空系统，需要先在后台新建企业并录入内容。';
    $('confirm-bar').hidden = false;
  });

  $('btn-cancel').addEventListener('click', function () { $('confirm-bar').hidden = true; });

  $('btn-confirm').addEventListener('click', function () {
    $('confirm-bar').hidden = true;
    setBusy(true);
    post('/api/setup/install', payload(), function (body) {
      setBusy(false);
      if (body && body.code === 0) {
        startProgress(body.data || {});
      } else {
        showBanner('err', (body && body.msg) || '安装失败');
      }
    });
  });

  function startProgress(res) {
    $('form-area').hidden = true;
    $('progress-area').hidden = false;
    setStep(3);
    pstep(1, 'done');
    showDemo(res);
    if (res.restarted) {
      pstep(2, 'run');
      poll();
    } else {
      pstep(2, 'fail');
      $('progress-tip').textContent = '服务未能自动重启，请手动重启服务后点击下方按钮重新检测。';
      $('btn-recheck').hidden = false;
    }
  }

  // showDemo 展示演示数据结果：账号口令 + 不影响成功的偏差提示。
  // 全程用 textContent 组装而不是拼 innerHTML——warning 里可能带数据库错误原文，
  // 拼字符串会把「库里的错误文本」变成「页面上的 HTML」。
  function showDemo(res) {
    var box = $('demo-box');
    var demo = res.demo || null;
    var warnings = res.warnings || [];
    var accounts = (demo && demo.accounts) || [];
    if (accounts.length === 0 && warnings.length === 0) return;

    if (accounts.length > 0) {
      var title = document.createElement('p');
      title.className = 'tip';
      title.style.color = '#303133';
      title.textContent = '演示登录账号（口令在库里只存哈希，离开本页后无法再次查看，请现在抄走）：';
      box.appendChild(title);

      var ul = document.createElement('ul');
      ul.className = 'acc';
      for (var i = 0; i < accounts.length; i++) {
        var a = accounts[i];
        var li = document.createElement('li');
        var where = document.createElement('b');
        where.textContent = a.where || '';
        li.appendChild(where);
        li.appendChild(document.createTextNode((a.user || '') + ' / ' + (a.pass || '') + ' '));
        var role = document.createElement('i');
        role.textContent = a.role || '';
        li.appendChild(role);
        ul.appendChild(li);
      }
      box.appendChild(ul);
    }

    for (var j = 0; j < warnings.length; j++) {
      var w = document.createElement('p');
      w.className = 'tip warn';
      w.textContent = warnings[j];
      box.appendChild(w);
    }
    box.hidden = false;
  }

  function poll() {
    var tries = 0;
    var timer = setInterval(function () {
      tries++;
      fetch('/api/setup/status').then(function (r) { return r.json(); }).then(function (body) {
        if (body && body.code === 0 && body.data && body.data.configured) {
          clearInterval(timer);
          pstep(2, 'done');
          pstep(3, 'run');
          setTimeout(function () {
            pstep(3, 'done');
            finish();
          }, 500);
        }
      }).catch(function () { /* 重启瞬间连接被拒是正常的，继续轮询 */ });
      if (tries >= 50) {
        clearInterval(timer);
        pstep(2, 'fail');
        $('progress-tip').textContent = '等待服务重启超时，请手动刷新页面或重启服务。';
        $('btn-recheck').hidden = false;
      }
    }, 1200);
  }

  function finish() {
    var n = 3;
    $('progress-tip').textContent = '安装完成，' + n + ' 秒后进入登录页…';
    var t = setInterval(function () {
      n--;
      if (n <= 0) {
        clearInterval(t);
        location.href = '/';
        return;
      }
      $('progress-tip').textContent = '安装完成，' + n + ' 秒后进入登录页…';
    }, 1000);
  }

  $('btn-recheck').addEventListener('click', function () { location.reload(); });

  $('btn-copy').addEventListener('click', function () {
    var t = $('env-text').textContent;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(t).then(function () {
        $('btn-copy').textContent = '已复制';
        setTimeout(function () { $('btn-copy').textContent = '复制配置'; }, 1500);
      }, function () { window.prompt('请手动复制：', t); });
    } else {
      window.prompt('请手动复制：', t);
    }
  });

  function init() {
    fetch('/api/setup/status').then(function (r) { return r.json(); }).then(function (body) {
      var d = body && body.data;
      if (!d) {
        showBanner('err', '无法获取安装状态，请刷新重试');
        return;
      }
      if (d.configured) {
        location.replace('/');
        return;
      }
      if (d.mysqlHint) {
        $('f-dbHost').value = d.mysqlHint.host || '127.0.0.1';
        $('f-dbPort').value = d.mysqlHint.port || '3306';
        $('f-dbUser').value = d.mysqlHint.user || 'root';
        $('f-dbName').value = d.mysqlHint.database || 'weichuan';
        var tip = $('compose-tip');
        tip.hidden = false;
        tip.textContent = d.mysqlHint.note || '';
      }
      if (d.sqlitePath) $('f-sqlitePath').value = d.sqlitePath;
      if (d.adminUser) $('f-adminUser').value = d.adminUser;
      $('env-text').textContent = d.envText || '';
      $('config-file-hint').textContent = d.configFile || '';
      $('config-file-note').textContent = d.configFile || '';
      preview();
    }).catch(function () {
      showBanner('err', '无法获取安装状态，请刷新重试');
    });
  }

  init();
})();
</script>
</body>
</html>`

// 页面历史：第一版是纯只读引导（只生成文本、不碰库）；本版把同一页升级为
// 安装向导，手动配置收进底部折叠区。__ITEMS__/__ENVTEXT__ 占位符随服务端
// 渲染一起移除——现在所有动态值都由页面 JS 从 /api/setup/status 取，
// 模板本身是常量，回归面更小。
