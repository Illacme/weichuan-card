package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/dbopen"
	"weichuan/internal/handler"
	"weichuan/internal/license"
	"weichuan/internal/logx"
	"weichuan/internal/middleware"
	"weichuan/internal/migrate"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/repository"
	"weichuan/internal/setting"
	"weichuan/internal/setup"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
)

// usage 子命令说明。`server` 不带参数即正常起服务，带参数则是运维子命令。
const usage = `用法：
  server            正常启动服务（默认）
  server migrate    应用待执行的数据库迁移后退出（不启服务）
  server backup     对 SQLite 库做在线一致性快照（--out <文件>）
  server dbcheck    只读体检一个 SQLite 备份文件（--file <文件>）
  server help       显示本说明

迁移默认在启动时自动补齐（仅 MySQL；AUTO_MIGRATE=0 关闭），
migrate 子命令用于把它拆成发布流水线里的独立一步。
backup / dbcheck 的详细参数见 server help 之后的子命令说明（server backup 无参即打印）。
`

func main() {
	initGinMode()
	cfg := config.Load()
	initLogFormat(cfg)

	// 运维子命令：先于「未配置模式」判定处理——migrate 需要在没有服务监听的
	// 情况下连库，也被 InsecureGates 之外的场景使用（如升级脚本）。
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			runMigrate(cfg)
			return
		case "backup":
			runBackup(cfg)
			return
		case "dbcheck":
			runDBCheck()
			return
		case "help", "-h", "--help":
			log.Print(usage)
			return
		default:
			log.Printf("[FATAL] 未知子命令 %q", os.Args[1])
			log.Print(usage)
			os.Exit(2)
		}
	}

	// 首次部署引导：连库必需的 DB_DSN 根本没提供时，不再直接 Fatal
	// （运维只看到一行日志、不知道下一步做什么），而是以免数据库方式起来，
	// 只提供只读引导页：说清缺什么、生成可复制的配置、提示改完重启。
	// 判据刻意是「DB_DSN 未提供」而不是「连不上库」：后者是线上真故障，
	// 若也进引导页，一次数据库抖动就会让终端用户看到安装页。
	if setup.Unconfigured() {
		log.Printf("[SETUP] 未检测到 DB_DSN，进入初始配置模式：不加载业务功能，仅提供引导页")
		log.Printf("[SETUP] 请访问 http://<服务器IP>:%s/setup 查看并复制需要配置的内容", cfg.Port)
		if err := setup.Engine().Run(":" + cfg.Port); err != nil {
			log.Fatal(err)
		}
		return
	}

	// PII 加密密钥：先让「配置文件形态」也生效，再过安全门槛。
	// pii 包在包级 init 里只读环境变量，而包级 init 早于本函数的配置加载——
	// 安装向导把密钥写进的是 runtime.env，进程起来时 init 早已跑完、看不到，
	// 所以必须由这里补一次（见 syncPIIKeyFromConfig）。
	syncPIIKeyFromConfig()

	// PII 加密密钥硬门槛：未设置 PII_ENC_KEY 时 pii 回落到源码里公开的内置
	// dev 密钥，「加密」在已知密钥下进行、形同虚设。此前只打 WARN 不拦，
	// 生产漏设无人察觉；现在与 JWT 缺省密钥同等处理——拒绝启动。
	//
	// 为什么现在敢拦了：安装向导会自动签发随机密钥写进配置（见 setup.Install），
	// 所以走向导的新装不会被拦；被拦住的正是手工部署漏设的场景，
	// 而那恰恰是最需要有人喊一声的地方。逃生门同是 ALLOW_INSECURE=1。
	if err := piiKeyGate(); err != nil {
		log.Fatalf("[SECURITY][FATAL] %v", err)
	}
	if pii.UsingPlaceholderKey() {
		// 能走到这里只可能是 ALLOW_INSECURE=1 放行的：留痕，避免运维忘了自己开过逃生门。
		log.Printf("[SECURITY][ALLOW_INSECURE] 已放行：%s 未设置，PII 正用内置 dev 密钥加密", pii.KeyEnvVar)
	}

	// 安全硬门槛：带内置缺省的 JWT 密钥 / 超管口令上线，等于把后台开放给所有人——
	// 这两个默认值都原样写在公开文档里，属于已知凭据，因此这里拒绝启动而不是只告警。
	// 自愈方式很简单（改一个环境变量重启），不会像 db_dsn 那样把「改回来的入口」弄丢。
	// 本地联调确需放行：ALLOW_INSECURE=1。
	if gates := cfg.InsecureGates(); len(gates) > 0 {
		if os.Getenv("ALLOW_INSECURE") != "1" {
			for _, g := range gates {
				log.Printf("[SECURITY][FATAL] %s", g)
			}
			log.Fatal("拒绝以不安全的默认凭据启动：请设置 JWT_KEY / ADMIN_PASS 后重启" +
				"（本地联调确需放行请加 ALLOW_INSECURE=1，切勿用于生产）")
		}
		for _, g := range gates {
			log.Printf("[SECURITY][ALLOW_INSECURE] 已放行：%s", g)
		}
	}

	// 其余不安全配置只提示不拦截（如超管口令偏弱），由运维自行判断。
	for _, w := range cfg.InsecureWarnings() {
		log.Printf("[SECURITY] %s", w)
	}

	// 驱动由 DB_DSN 形态决定：默认是 MySQL；写成 sqlite://<路径> / file:<URI> /
	// *.db 则走 SQLite（详见 docs/deploy-runbook.md「路径 D：SQLite 单机部署」）。
	// 注意 db_dsn 是系统设置里的只读项——改错会连不上库、把「改回来的入口」一起弄丢，
	// 所以库地址只能在启动时用环境变量定好，上线后改不动。
	db, driver, err := dbopen.Open(cfg.DBDSN)
	if err != nil {
		// 走到这里说明 DB_DSN 给了但连不上——是配置错误 / 库不可用，不是「没配置」，
		// 所以仍然 Fatal（不能让终端用户对着一个半成品服务）。但要把排查路径写明，
		// 而不是留一句 "db connect: ..." 让人自己去猜。
		// 注意：**不打印 DSN 原文**，里面含数据库口令，打印即泄密。
		log.Printf("[FATAL] 数据库连接失败（driver=%s）：%v", driver, err)
		log.Printf("[FATAL] 排查顺序：1) 数据库是否已启动、地址/端口是否可达；" +
			"2) DB_DSN 的账号口令与库名是否正确（日志不打印 DSN，避免泄露口令）；" +
			"3) 先跑 scripts/preflight.sh 做静态自检；4) 详见 docs/deploy-runbook.md")
		log.Fatal("db connect failed")
	}

	// SQLite 没有可执行的 migrations：`migrations/*.sql` 是 MySQL 方言
	// （ENGINE=InnoDB / AUTO_INCREMENT / 反引号），这里改为按模型建表。
	// MySQL 侧不动——生产库的表结构由迁移脚本管，AutoMigrate 擅自改列类型风险更大。
	if driver == dbopen.DriverSQLite {
		if err := db.AutoMigrate(model.All()...); err != nil {
			log.Fatalf("auto migrate: %v", err)
		}
	}
	log.Printf("database driver=%s", driver)
	if driver == dbopen.DriverSQLite {
		log.Printf("[INFO] SQLite 为单写者且不支持多副本：请勿将库文件放在 NFS 等网络盘，" +
			"备份请用 VACUUM INTO 取在线一致性快照（详见 docs/deploy-runbook.md）")
	}

	// MySQL 升级自动补齐迁移：compose 的 initdb.d 只在卷为空时执行，存量库
	// 升级后新增的迁移必须有人应用——历史上这一步靠人工跑 SQL（runbook §4），
	// 漏了的表现是「表结构落后一个版本、报错却看不出原因」。这里按记账表只做
	// 增量，幂等且带多副本锁；AUTO_MIGRATE=0 可交回发布流水线（配 `server migrate`）。
	//
	// 失败不 Fatal 是刻意的：迁移失败时服务仍能让管理员进后台查日志、也保留了
	// 「改完重启自愈」的入口；而另一种常见失败——多副本抢锁超时——本就该跳过
	// （另一个副本正在做），为此直接退出反而会把整站打挂。代价是必须靠日志
	// 暴露，故这里用 [MIGRATE][FATAL] 前缀并写进 runbook 的排查清单。
	if driver == dbopen.DriverMySQL && cfg.AutoMigrate {
		applied, err := migrate.ApplyWithReport(db)
		switch {
		case err == nil && len(applied) > 0:
			log.Printf("[MIGRATE] 已应用 %d 个迁移：%s", len(applied), strings.Join(applied, ", "))
		case err == nil:
			log.Printf("[MIGRATE] 数据库已是最新（无待应用迁移）")
		default:
			log.Printf("[MIGRATE][FATAL] 应用数据库迁移失败：%v", err)
			log.Printf("[MIGRATE][FATAL] 服务继续启动，但表结构可能与本版本不一致："+
				"请按上面的错误处理；或设 AUTO_MIGRATE=0 后手工执行 `%s migrate`", os.Args[0])
		}
	} else if driver == dbopen.DriverMySQL {
		log.Printf("[MIGRATE] AUTO_MIGRATE=0：跳过自动迁移，请自行确认表结构与本版本匹配")
	}

	// 存量 PII 回填：把历史明文员工 PII 加密入库并补盲索引（幂等，仅处理未加密/缺索引的行）。
	// 放在表结构就绪之后、业务路由装配之前；失败只告警不终止——读路径对未加密的
	// 历史行仍兼容（Decrypt 对非密文原样返回），但必须靠这条日志暴露出来排查。
	if err := model.BackfillPII(db); err != nil {
		log.Printf("[PII][ERROR] PII 存量回填失败：%v（历史明文行尚未加密，请排查密钥/DB 后重启）", err)
	}

	handler.SeedAdmin(db, cfg) // 首启动若 admin 表为空则创建初始超管

	// 系统设置：建表 + seed 缺省值 + 载入内存缓存（后台「系统设置」界面维护运行参数）
	if err := setting.Init(db); err != nil {
		log.Fatalf("init settings: %v", err)
	}
	// 系统设置覆盖环境变量（空串=界面未设置→保留环境变量），让云存储 / CDN / 微信凭据
	// 也能在后台维护；这些客户端在启动时构造，故界面标记为「需重启生效」。
	cfg.OverlayFromSettings()

	// 按租户解析授权用的库连接（Phase 4）：注入后 EnforceLicense 会先查
	// tenant_license 表该租户的行，查不到才回退全局设置。
	license.SetTenantDB(db)

	// 授权公钥自检：私有 / 独立部署用的是**对外收费交付**的形态，
	// 此时验签公钥必须是发行方自己的——不能用仓库里那把占位演示密钥。
	//
	// 为什么这是硬门槛而不是告警：占位密钥的配对私钥写在签发工具的注释里，
	// 任何拿到二进制的人都能自己签一张「永久有效 / 无限席位」的许可证，
	// 授权机制直接形同虚设。
	//
	// 为什么只在私有模式拦：集中 / 平台模式（SaaS）不验本地许可证文件，
	// 授权来自系统设置，用不用占位公钥都不影响；而演示 / 开源仓库需要
	// 「clone 下来就能跑」，一刀切拦会让本地开发无从下手。
	//
	// 为什么排在 license.Verify **之前**：私钥模式下 Verify 会先验签，
	// 用占位公钥验真实许可证必然失败，会先一步 Fatalf 退出——
	// 那样用户看到的只会是「授权校验失败」，而真正的原因是「公钥没换」。
	// 先说原因，才不会让人往错的方向排查。
	//
	// 自愈方式：把发行方自己的公钥放进环境变量 LICENSE_PUB（base64），
	// 无需改代码重新编译；改完重启即生效。
	if license.PrivateMode() && license.IsPlaceholderPublicKey() {
		log.Fatalf("[LICENSE][FATAL] 私有部署仍在使用仓库内置的占位演示公钥，拒绝启动："+
			"该公钥的配对私钥是公开的，任何人都能伪造许可证。"+
			"请用 license keygen 生成密钥对，把公钥填入环境变量 %s 后重启"+
			"（私钥只留在签发环境，绝不能进仓库或二进制）",
			license.PubKeyEnvVar)
	}

	// 授权校验（Phase 1 地基）：私有 / 独立部署验本地签名许可证，集中模式读系统设置。
	// 私有模式下验签失败或过期 = 拒绝启动（与 InsecureGates 同一类硬门槛逻辑）。
	if _, err := license.Verify(context.Background()); err != nil {
		log.Fatalf("[LICENSE][FATAL] 授权校验失败，拒绝启动：%v", err)
	}
	// 上传限制从系统设置读取（后台改上限即时生效、无需重启）；未配置则回落 10MB / 默认白名单
	storage.UploadLimitBytes = func() int64 {
		return int64(setting.GetInt("upload_max_mb", 10)) * 1024 * 1024
	}
	storage.AllowExtProvider = func() map[string]bool {
		sl := setting.GetStringSlice("upload_allow_ext", nil)
		if len(sl) == 0 {
			return storage.DefaultAllowExt
		}
		m := make(map[string]bool, len(sl))
		for _, e := range sl {
			m[e] = true
		}
		return m
	}

	// 清理过期的会话黑名单行（expires_at 已过的吊销记录，令牌自身也已过期）：
	// 启动时先清一次（把上次停机期间到期的行补掉），再挂后台定期清理——
	// 只清一次的话，长期不重启的实例里这张表会一直堆积（详见该函数注释）。
	// 间隔由 REVOKED_PURGE_MINUTES 控制，默认 6 小时。
	if n, err := repository.PurgeExpiredSessions(db); err != nil {
		log.Printf("[WARN] purge expired revoked sessions: %v", err)
	} else if n > 0 {
		log.Printf("[INFO] purged %d expired revoked sessions", n)
	}
	stopPurge := repository.StartExpiredSessionPurge(db, cfg.RevokedPurgeInterval)
	defer stopPurge()

	// 限流计数表同样只增不减：每一行只在一个窗口内有意义，但不会自己消失。
	// 不清理的话长期运行下按「每分钟一批新行」的速度增长（详见该函数注释）。
	stopRatePurge := repository.StartRateLimitPurge(db, cfg.RatePurgeInterval)
	defer stopRatePurge()

	// 审计日志按保留期清理（C3）：audit_log 只追加，长期运行会一直涨。
	// 保留天数取自系统设置（默认 0 = 不清理，理由见 audit/purge.go），所以传的是
	// 取值函数——后台改完即时生效，不必重启。启动时先跑一次，把上次停机期间
	// 超期的行补掉；清理动作本身会记一条 audit.purge 审计。
	auditKeepDays := func() int {
		return setting.GetInt("audit_retention_days", cfg.AuditRetentionDays)
	}
	audit.RunAuditPurgeOnce(db, auditKeepDays)
	stopAuditPurge := audit.StartAuditLogPurge(db, cfg.AuditPurgeInterval, auditKeepDays)
	defer stopAuditPurge()

	r := handler.New(db)
	// 可信反代：gin 的内置缺省是「信任所有代理」（启动日志自己会打一行
	// "You trusted all proxies, this is NOT safe"）。那样 c.ClientIP() 直接采信
	// 客户端自带 X-Forwarded-For 的**最左**一项，而这一项由客户端随意填写——
	// 登录 / refresh / 小程序码三处限流全按这个 IP 分桶，于是「每次换一个伪造头」
	// 就等于完全没有限流（超管口令可被无限次爆破，实测 15/15 放行）。
	// 这里收窄到真实反代所在网段，见 config.DefaultTrustedProxies 的取舍说明。
	applyTrustedProxies(r, cfg)
	// 可观测性：请求 ID + 访问日志（方法/路径/状态/耗时/IP，5xx 单独前缀便于告警匹配）
	r.Use(middleware.RequestID(), middleware.AccessLog())
	// 指标端点：采集器始终注册，开关交给系统设置 metrics_enabled（留空回退 METRICS_ENABLED）——
	// 后台改完即时生效、无需重启；关闭时 /metrics 返回 404，避免公网暴露流量规模。
	metrics := middleware.NewMetrics()
	metrics.Enabled = func() bool {
		return setting.GetBool("metrics_enabled", cfg.MetricsEnabled)
	}
	// /metrics 的第二道门：即便开关在界面上被打开，也只允许 METRICS_ALLOW_CIDR
	// 里的来源（内网采集器）访问，公网一律 404——流量规模不该被外人看到。
	if len(cfg.MetricsAllowCIDR) > 0 {
		allow := middleware.NewIPAllowlist(cfg.MetricsAllowCIDR)
		metrics.Allow = func(c *gin.Context) bool { return allow(c.ClientIP()) }
	}
	r.Use(metrics.Middleware())
	r.GET("/metrics", metrics.Handler())
	if metrics.Enabled() {
		log.Printf("[INFO] metrics enabled: GET /metrics")
	}
	handler.RegisterAuth(r, db, cfg)   // 员工/超管登录（JWT 签发）+ 刷新 + 登出
	handler.RegisterApply(r, db, cfg)  // 开通申请 / 名片修改审核 / 员工管理 / 统计
	handler.RegisterStatsExport(r, db) // 统计 CSV 导出（与 /api/admin/stats 同口径，按租户隔离）
	handler.RegisterStatsDetail(r, db) // 埋点明细列表（聚合的下钻：逐条事件 + 筛选 + 分页）
	handler.RegisterContent(r, db)     // 企业内容管理（公司信息/简介/分类/项目/服务/布局，按租户隔离）
	handler.RegisterCompanies(r, db)
	handler.RegisterCompanyLicense(r, db) // 按企业查看/设置授权（席位 / 到期 / 功能开关）
	handler.RegisterStorageQuota(r, db)   // 按租户存储用量与配额（GET /api/admin/storage + PUT quota）
	handler.RegisterCompanyWXApp(r, db)   // 租户小程序凭据：GET/PUT /api/admin/companies/:id/wechat
	handler.RegisterMPRelease(r, db, cfg) // 小程序版本发布：登记 / 提审 / 查审核状态 / 发布 / 撤回（平台超管代租户）
	handler.RegisterAdmins(r, db)         // 平台超管账号管理：列表/新增/改用户名/重置密码/删除
	handler.RegisterAuditLogs(r, db)      // 操作审计日志（只读查询：平台超管）

	// 文件上传：存储驱动可配（local 默认 / minio / oss），业务代码不变。
	// 驱动初始化失败时降级本地（而不是 Fatal）：凭据在界面里改错的情况下，
	// 进程必须还能起来，管理员才进得去后台把它改回来（「改错可自愈」的底线）。
	up := newUploaderWithFallback(cfg)
	// CDN 刷新器（可选）：启用后覆盖写会触发边缘缓存失效。厂商由 cdn_provider 决定，
	// 各厂商要求的凭据不同（Cloudflare 只要 Token+Zone ID，AK/SK 三家不需要资源标识），
	// 校验放在 storage.NewCDNRefresher 里——配不全只记一行日志、不启用刷新，
	// 绝不能因为 CDN 配错让服务起不来（刷新是增强，主流程不依赖它）。
	var refresher storage.CDNRefresher
	if cfg.CDNEnabled {
		rf, err := storage.NewCDNRefresher(storage.CDNConfig{
			Provider:        cfg.CDNProvider,
			AccessKeyID:     cfg.CDNAccessKeyID,
			AccessKeySecret: cfg.CDNAccessKeySecret,
			Endpoint:        cfg.CDNEndpoint,
			ResourceID:      cfg.CDNResourceID,
		})
		if err != nil {
			log.Printf("[WARN] cdn refresh disabled: %v", err)
		} else {
			refresher = rf
			log.Printf("cdn refresh enabled (provider=%s, endpoint=%s)",
				storage.CDNProviderLabel(cfg.CDNProvider), storage.CDNDefaultEndpoint(cfg.CDNProvider))
		}
	}
	// /uploads 静态托管：local 驱动下用户文件与小程序码均落本地并由本路由托管；
	// minio/oss 驱动下用户文件/小程序码走对象存储，此路由仅作空目录兜底，无副作用。
	r.Static("/uploads", cfg.UploadDir)
	handler.RegisterUpload(r, db, cfg, up, refresher)  // POST /api/admin/upload + /api/employee/upload（图片异步送检）
	handler.RegisterStorage(r, db, up, refresher, cfg) // 直传预签名 / 私有桶签名 / CDN 刷新 / 本地私有文件流
	// 图片内容安全：微信消息推送回调 + 后台检测记录列表 + 一键下架
	handler.RegisterMediaCheck(r, db, cfg, up, refresher)
	// 刷新器往下传给「会覆盖同名对象」的两条路径（素材采用 / 小程序码重生成），
	// 普通上传的对象名是随机的、永不覆盖，刷新对它本就无意义（见 handler.refreshCDN 注释）
	handler.RegisterAssets(r, db, up, refresher)      // 常用素材库（内嵌图标/封面/LOGO）：清单 / 采用 / 预览图
	handler.RegisterWechat(r, db, cfg, up, refresher) // GET /api/wechat/wxacode（小程序码，按 staffId 归属租户取凭据）
	handler.RegisterSettings(r, db, cfg)              // 系统设置：GET/PUT /api/admin/settings + GET /api/public/settings
	handler.RegisterClientError(r)                    // 前端报错上报（无鉴权公开端点，限流 + 日志关联 X-Request-Id）
	handler.RegisterPoster(r, db)                     // 海报个性化设置：GET/PUT /api/employee/poster-prefs（自定义背景 + 版式）
	// 首次配置状态查询（只读）：已配置时返回 configured=true；未配置时进程根本进不到这里
	// （见开头 setup.Unconfigured 分支）。前端据此决定是否显示配置引导。
	// /setup 别名：安装完成后还有人手动敲 /setup 时 302 回首页，不给裸 404。
	setup.RegisterStatus(r)
	setup.RegisterSetupAlias(r)

	log.Printf("listen :%s (storage driver=%s)", cfg.Port, cfg.StorageDriver)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal(err)
	}
}

// initGinMode 设定 gin 的运行模式：运维没显式给 GIN_MODE 时固定 release。
//
// 不设就是 debug——每次启动把**全部路由表**打进日志（几十行，把真正要看的安全告警
// 与迁移日志挤到屏幕外），并且保留调试期开销。本地排查可以 GIN_MODE=debug 覆盖。
// 必须早于 handler.New / setup.Engine：路由注册时就在按模式决定要不要打印。
// syncPIIKeyFromConfig 让「写在配置文件里的 PII_ENC_KEY」也能生效。
//
// pii 包在包级 init 里读环境变量，而包级 init 早于 main 里的 config.Load()——
// 安装向导把密钥写进 runtime.env 后进程重新启动，init 早就跑完了、看不到这个值，
// 于是「装完就有真密钥」只会在环境变量形态下成立。这里把配置文件的值补进
// 环境变量再让 pii 重新装载一次，两种部署形态才一致。
// 环境变量优先：已显式注入时不覆盖，保持「env > 配置文件」的既有语义。
func syncPIIKeyFromConfig() {
	if os.Getenv(pii.KeyEnvVar) == "" {
		if v := config.Resolved(pii.KeyEnvVar); v != "" {
			// Setenv 只作用于本包的常量键（不含 '=' 与 NUL），失败仅可能是内存耗尽；
			// 即便失败也只是落回 dev 密钥，由紧随其后的 piiKeyGate 拦住。
			_ = os.Setenv(pii.KeyEnvVar, v)
		}
	}
	pii.Reload()
}

// piiKeyGate 返回 PII 密钥相关的启动硬门槛错误（nil = 放行）。
//
// 独立成函数是为了让单测能直接覆盖：启动门槛写错的两种后果都很贵——
// 漏拦等于生产裸奔且无人察觉，误拦等于所有人都起不来。
func piiKeyGate() error {
	// 配了但格式不对：Encrypt/Decrypt 会一路报错。与其等到写入时才炸，
	// 不如启动时说清楚——这种错没有「放行」的合理场景，故不受 ALLOW_INSECURE 影响。
	if err := pii.KeyError(); err != nil {
		return err
	}
	if !pii.UsingPlaceholderKey() {
		return nil
	}
	if os.Getenv("ALLOW_INSECURE") == "1" {
		return nil
	}
	return fmt.Errorf("未设置 %s（base64 32 字节）：员工 PII 正用源码里公开的内置 dev 密钥加密，形同虚设。"+
		"请用 openssl rand -base64 32 生成后设置"+
		"（警告：已用 dev 密钥写入的数据换钥后无法解密，必须在首次写入业务数据之前设定）；"+
		"本地联调确需放行请加 ALLOW_INSECURE=1，切勿用于生产", pii.KeyEnvVar)
}

// initLogFormat 切换日志形态（LOG_FORMAT=json 输出 JSONL，供日志系统按字段检索）。
//
// 必须在任何业务日志之前调用：否则前面的几行还是纯文本，混在 JSONL 里会让采集器
// 那一行解析失败（多数采集器遇到非 JSON 行是整行丢弃——恰好把启动阶段最该看的
// 那几行丢掉）。同时把 gin 自己的两个 writer 也接进来，它是把 panic 栈与启动日志
// 直接写到 DefaultWriter 的，不接就会在 JSONL 里混进纯文本块。
func initLogFormat(cfg config.Config) {
	got := logx.SetFormat(cfg.LogFormat)
	gin.DefaultWriter = logx.Writer()
	gin.DefaultErrorWriter = logx.Writer()
	if got == logx.FormatJSON {
		log.Printf("[INFO] 日志格式=json（结构化日志，供集中采集）")
	}
}

func initGinMode() {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
}

// applyTrustedProxies 把可信反代列表装到 gin 引擎上，决定 ClientIP() 是否采信
// X-Forwarded-For。设置失败（CIDR 写错）不静默：打印告警并退回「一个都不信」——
// 退回是最安全的方向（所有用户算成反代 IP，限流会偏严但不会被绕过），
// 与「宁可误拦、不可漏放」一致。
func applyTrustedProxies(r *gin.Engine, cfg config.Config) {
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Printf("[WARN] TRUSTED_PROXIES 配置无效（%v）：已退回「不信任任何代理」，"+
			"ClientIP 一律取 TCP 对端地址；若前面确有反代，请改成反代的 IP/CIDR", err)
		// 必须显式置空：gin 解析失败时会提前返回，engine 里仍留着那份**含无效项**
		// 的原始列表，字符串形态的项还会继续参与匹配——不清掉就不算真的退回了。
		if err := r.SetTrustedProxies(nil); err != nil {
			log.Printf("[FATAL] 无法将可信代理列表重置为空：%v", err)
		}
	}
}

// runMigrate `server migrate`：应用待执行迁移后退出，不启服务、不监听端口。
// 用途：把「应用迁移」从启动流程里拆出来单独跑——发布流水线希望在起新版本
// 之前先确认迁移成功，或部署设了 AUTO_MIGRATE=0、由人工择时执行。
// 与启动时自动迁移走同一条代码路径（migrate.ApplyWithReport），只是失败即
// os.Exit(1)：这里是运维显式发起的动作，退出码必须能进流水线判断。
func runMigrate(cfg config.Config) {
	// 用 Resolved 而不是 cfg.DBDSN：后者有内置缺省值，未配置的部署会拿着
	// 缺省 DSN 去连一个根本不存在的库，报出来的错会把人带偏。
	if config.Resolved("DB_DSN") == "" {
		log.Fatal("[MIGRATE] DB_DSN 未配置，无法迁移：请先完成安装向导，或用 DB_DSN=... server migrate 显式指定")
	}
	db, driver, err := dbopen.Open(cfg.DBDSN)
	if err != nil {
		// 不打印 DSN 原文（含数据库口令）
		log.Printf("[MIGRATE] 连接数据库失败（driver=%s）：%v", driver, err)
		log.Fatal("[MIGRATE] 排查顺序：库是否已启动 / 地址端口是否可达 / 账号口令与库名是否正确（日志不打印 DSN）")
	}
	if driver == dbopen.DriverSQLite {
		log.Print("[MIGRATE] SQLite 部署无需手工迁移：表由服务启动时按模型 AutoMigrate 维护")
		return
	}
	applied, err := migrate.ApplyWithReport(db)
	if err != nil {
		log.Printf("[MIGRATE] 应用数据库迁移失败：%v", err)
		log.Fatal("[MIGRATE] 迁移未完成：请按上面的错误处理后再启动服务（未应用的迁移不会记为已应用）")
	}
	if len(applied) == 0 {
		log.Print("[MIGRATE] 数据库已是最新（无待应用迁移）")
		return
	}
	log.Printf("[MIGRATE] 已应用 %d 个迁移：%s", len(applied), strings.Join(applied, ", "))
}

// newUploaderWithFallback 构造存储实现；失败时降级为本地磁盘并打印醒目告警。
// 不 Fatal 是刻意的：凭据可能是在后台「系统设置」里填错的，若进程起不来，
// 管理员就再也进不去后台改回来——降级保证「改错可自愈」。
func newUploaderWithFallback(cfg config.Config) storage.Uploader {
	up, err := newUploader(cfg)
	if err == nil {
		return up
	}
	log.Printf("[WARN] 存储驱动 %q 初始化失败：%v；已降级为本地磁盘存储。"+
		"请到「系统设置 → 文件存储」修正后重启服务。", cfg.StorageDriver, err)
	return &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
}

// newUploader 按存储驱动构造文件存储实现（local / minio / oss / s3），统一返回 storage.Uploader
func newUploader(cfg config.Config) (storage.Uploader, error) {
	const maxBytes = 10 * 1024 * 1024 // 10MB
	switch cfg.StorageDriver {
	// minio 与 s3 共用同一套 S3 客户端实现：端点 / 桶 / 凭据 / SSL / 区域全部来自
	// 同一批界面字段（storage_region 对两者都生效），枚举分开只为界面上的归属说明
	// ——s3 面向腾讯云 COS / 华为云 OBS / 七牛云 / AWS S3 / R2 等 S3 兼容云，
	// 这类云常需要显式指定区域，minio-go 的自动探测对它们并非总可靠。
	case "minio", "s3":
		return storage.NewMinioUploader(cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey,
			cfg.MinioBucket, cfg.UploadBaseURL, cfg.StorageRegion, cfg.MinioUseSSL, maxBytes, storage.DefaultAllowExt)
	case "oss":
		return storage.NewOssUploader(cfg.OssEndpoint, cfg.OssAccessKeyID, cfg.OssAccessKeySecret,
			cfg.OssBucket, cfg.UploadBaseURL, maxBytes, storage.DefaultAllowExt)
	default:
		return &storage.LocalUploader{
			Dir:      cfg.UploadDir,
			BaseURL:  cfg.UploadBaseURL,
			MaxBytes: maxBytes,
			AllowExt: storage.DefaultAllowExt,
		}, nil
	}
}
