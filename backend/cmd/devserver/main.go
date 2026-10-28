// devserver 是一个仅用于本地联调 / 演示的开发态后端：
//   - 用 SQLite（纯 Go，免 cgo）替代 MySQL，无需任何外部依赖即可 `go run` 起来；
//   - 复用与生产完全一致的路由装配（handler.New + 全部 Register*），保证联调即验证生产契约；
//   - 首次启动自动 seed 演示数据（超管 / 企业 / 企业信息与简介 / 8 分类 / 10 案例 /
//     4 项服务 / 首页布局 / 1 演示员工 / 1 企业管理员 + 27 张占位素材）。
//     种子实现在 internal/demoseed——与安装向导「加载演示企业数据」是同一份代码，
//     联调看到的内容与客户勾选演示后看到的一模一样。
//
// 生产部署仍走 cmd/server（MySQL + docker-compose），本文件不参与生产构建路径。
//
// 运行：
//
//	cd backend && go run ./cmd/devserver
//
// 然后小程序 app.ts 的 baseUrl 指向 http://<本机IP>:8080 （开发者工具需勾选"不校验合法域名"）。
// 上传/采用接口返回的是相对路径 /uploads/<name>，各端自行拼绝对——
// 浏览器/管理后台经 vite 代理或反向代理解析，小程序经 assetUrl 拼 baseUrl，
// 因此不再需要为手机端单独配 DEV_UPLOAD_BASE_URL（旧方案会把 127.0.0.1 写死进库导致裂图）。
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"weichuan/internal/config"
	"weichuan/internal/demoseed"
	"weichuan/internal/handler"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/setup"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// devModels devserver 的建表清单，直接取 model 包的权威清单（新增模型只改那里一处）。
// sys_setting 表由 setting.Init 自行 AutoMigrate，不在此列。
//
// 为什么曾经需要手工维护：漏加不报编译错，只在运行期查询时报「no such table」
// （曾漏加 RevokedSession 导致 devserver 下每次鉴权请求都被误判为「会话已吊销」
// 而 401，见 main_test.go 的守卫）。
var devModels = model.All()

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 临时 SQLite 文件库（比内存库更稳，避免共享缓存连接断开后数据丢失）
	tmpDB, err := os.CreateTemp("", "weichuan-dev-*.db")
	if err != nil {
		log.Fatalf("create temp db: %v", err)
	}
	dbPath := tmpDB.Name()
	// 只是借它拿一个不冲突的路径，文件本身马上由 gorm 打开；关闭失败无后果
	_ = tmpDB.Close()
	log.Printf("dev db: %s", dbPath)
	// 如实回写 DB_DSN：本进程是拿 dbPath 直连 gorm 的，**不经过 DB_DSN 配置**，
	// 但 /api/setup/status（与生产同一套 StatusPayload）只认 DB_DSN——不回写就会
	// 报 configured=false，前端 main.ts 据此去挂安装引导页而不是后台 App，
	// 本地联调直接废掉（「接口查不到 → 按已配置」的兜底反而比「查到却说没配」安全）。
	// 缺的从来是「DB_DSN 这个环境变量」而不是数据库，故这里补上真实值。
	_ = os.Setenv("DB_DSN", dbPath) // 仅影响本进程；失败只会让联调页误判，故不中断

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(devModels...); err != nil {
		log.Fatalf("auto migrate: %v", err)
	}
	// 存量 PII 回填（员工/开通申请/修改快照，加密 + 补盲索引）：幂等，仅处理未就绪的行。
	if err := model.BackfillPII(db); err != nil {
		log.Printf("[PII][ERROR] PII 存量回填失败：%v", err)
	}

	// 系统设置：seed 缺省值 + 载入内存缓存（后台「系统设置」界面维护运行参数）
	if err := setting.Init(db); err != nil {
		log.Fatalf("init settings: %v", err)
	}
	// 上传限制从系统设置读取（后台改上限即时生效、无需重启）
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

	// 复用与 cmd/server 一致的环境变量加载（ADMIN_USER / ADMIN_PASS 等可由 env 覆盖，
	// 避免把演示账号口令写死在代码里），仅本地联调相关字段（SQLite 上传目录 / 本地驱动 /
	// dev JWT 密钥）在此显式覆盖。
	cfg := config.Load()
	// 与生产一致：系统设置覆盖环境变量（界面留空则保留环境变量）。
	// 放在下面几个「开发态强制覆盖」之前，保证 devserver 仍然是本地磁盘 + 临时目录。
	cfg.OverlayFromSettings()

	// 按租户解析授权用的库连接（Phase 4）：与生产同路径，便于本地验证多租户授权。
	license.SetTenantDB(db)

	// 授权校验（Phase 1 地基）：开发态下即便私有模式也只告警不致命，避免卡住本地联调。
	if _, err := license.Verify(context.Background()); err != nil {
		log.Printf("[LICENSE][WARN] 开发态忽略授权校验失败：%v", err)
	}
	cfg.UploadDir = mustDir(filepath.Join(os.TempDir(), "weichuan-dev-uploads"))
	cfg.UploadBaseURL = uploadBaseURL(port)
	cfg.StorageDriver = "local"
	cfg.StoragePublic = true
	cfg.JWTKey = "dev-jwt-key"
	// 与上面的 os.Setenv 成对：让 cfg 里的 DSN 也是真实值（后台「系统设置」展示、
	// 以及任何直接读 cfg.DBDSN 的地方都不会看到一个空串——那是「没连库」的假象）。
	cfg.DBDSN = dbPath
	log.Printf("dev server listening on :%s (storage=local, 上传返回相对路径 /uploads/<name>，由各端按源拼绝对)", port)
	handler.SeedAdmin(db, cfg)

	// 演示数据：与安装向导「加载演示企业数据」用的是同一个包（internal/demoseed），
	// 保证联调看到的内容与客户装完勾选演示后看到的一模一样。
	// 口令在此写死：联调要反复登录，随机口令只会让每次重启都得翻日志。
	if _, err := demoseed.Load(db, demoseed.Options{
		UploadDir:            cfg.UploadDir,
		EmployeePassword:     "weichuan123",
		CompanyAdminPassword: "langhui123",
	}); err != nil {
		// 演示数据不完整不该拦住联调（缺几张图也能看主流程），但必须留痕
		log.Printf("[DEMOSEED][WARN] 演示数据未完全写入：%v", err)
	}

	r := handler.New(db)
	// 与生产一致的可观测性中间件（请求 ID + 访问日志）
	r.Use(middleware.RequestID(), middleware.AccessLog())
	// 指标端点（与 cmd/server 一致）：开关由系统设置 metrics_enabled 控制，
	// 开发态默认留空 → 回退 METRICS_ENABLED 环境变量。
	metrics := middleware.NewMetrics()
	metrics.Enabled = func() bool {
		return setting.GetBool("metrics_enabled", cfg.MetricsEnabled)
	}
	r.Use(metrics.Middleware())
	r.GET("/metrics", metrics.Handler())
	handler.RegisterAuth(r, db, cfg)
	handler.RegisterApply(r, db, cfg)
	// 统计的两个「后加」端点：CSV 导出 + 埋点明细（/api/admin/stats 汇总本身在
	// RegisterApply 里，故统计页能开，但明细抽屉与导出会 404——2026-10-24 实测抓到）。
	// 漏注册不报编译错、只在点开功能时 404，正是下面 main_test.go 守门测试要钉住的漂移。
	handler.RegisterStatsExport(r, db)
	handler.RegisterStatsDetail(r, db)
	handler.RegisterContent(r, db)
	handler.RegisterCompanies(r, db)
	handler.RegisterCompanyLicense(r, db) // 按企业查看/设置授权（席位 / 到期 / 功能开关）
	handler.RegisterStorageQuota(r, db)   // 按租户存储用量与配额
	handler.RegisterCompanyWXApp(r, db)   // 租户小程序凭据：GET/PUT /api/admin/companies/:id/wechat
	handler.RegisterMPRelease(r, db, cfg) // 小程序版本发布：登记 / 提审 / 查审核状态 / 发布 / 撤回（平台超管代租户）
	handler.RegisterAdmins(r, db)         // 平台超管账号管理（列表 / 新增 / 改用户名 / 重置密码 / 删除）
	handler.RegisterAuditLogs(r, db)      // 操作审计日志（只读查询：平台超管）

	up := &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
	var refresher storage.CDNRefresher // 开发态不接 CDN
	// 联调期 /uploads 下的文件（含种子素材）经常被同名覆盖，浏览器按 Last-Modified
	// 启发式缓存会拿到旧图（症状：小程序已更新、后台还是旧图标）→ 开发态一律 no-cache。
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/uploads/") {
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	})
	r.Static("/uploads", cfg.UploadDir)
	handler.RegisterUpload(r, db, cfg, up, refresher)
	handler.RegisterMediaCheck(r, db, cfg, up, refresher)
	handler.RegisterStorage(r, db, up, refresher, cfg)
	handler.RegisterAssets(r, db, up, refresher)      // 常用素材库（内嵌图标/封面/LOGO）
	handler.RegisterWechat(r, db, cfg, up, refresher) // 小程序码
	handler.RegisterSettings(r, db, cfg)              // 系统设置：GET/PUT /api/admin/settings + GET /api/public/settings
	handler.RegisterClientError(r)                    // 前端报错上报（无鉴权公开端点，限流 + 日志关联 X-Request-Id）
	handler.RegisterPoster(r, db)                     // 海报个性化设置：GET/PUT /api/employee/poster-prefs
	// 安装状态查询与 /setup 别名：必须与生产（cmd/server）一致。
	// 缺了前者会让前端启动探针 GET /api/setup/status 每次都吃 404——
	// 功能上无碍（前端把查不到按「已配置」处理，见 api/setup.ts 的注释），
	// 但既刷 [ACCESS] 404 噪声，也违背了本文件「复用与生产完全一致的路由装配，
	// 保证联调即验证生产契约」的承诺：联调少验证两个端点，生产才暴露差异。
	// devserver 一定有 DSN，故这里恒返回 configured=true，不会误触发安装向导。
	setup.RegisterStatus(r)
	setup.RegisterSetupAlias(r)

	log.Printf("dev server listening on :%s (storage=local, db=sqlite)", port)
	log.Printf("演示账号：\n  平台超管(PC 后台) %s/%s\n  企业管理员(PC 后台·朗汇) langhui/langhui123\n  企业员工(小程序·非 PC 后台) 手机 13832603521 / 密码 weichuan123（高立新，业务经理）", cfg.AdminUser, cfg.AdminPass)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func mustDir(name string) string {
	if err := os.MkdirAll(name, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", name, err)
	}
	abs, _ := filepath.Abs(name)
	return abs
}

// uploadBaseURL 仅作为配置字段 UploadBaseURL 的填充值（本地驱动已不依赖它生成 URL）。
// 保留以兼容生产云存储（MinIO/OSS 用 BaseURL 拼绝对 CDN）与反向代理场景；
// 本地联调因上传返回相对路径 /uploads/<name>，DEV_UPLOAD_BASE_URL 已不再影响手机端取图。
func uploadBaseURL(port string) string {
	if v := strings.TrimSpace(os.Getenv("DEV_UPLOAD_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:" + port + "/uploads"
}
