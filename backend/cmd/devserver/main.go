// devserver 是一个仅用于本地联调 / 演示的开发态后端：
//   - 用 SQLite（纯 Go，免 cgo）替代 MySQL，无需任何外部依赖即可 `go run` 起来；
//   - 复用与生产完全一致的路由装配（handler.New + 全部 Register*），保证联调即验证生产契约；
//   - 首次启动自动 seed 演示数据（超管 / 公司信息 / 7 分类 / 1 精选项目 / 1 服务 / 首页布局 / 1 演示员工）。
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
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/handler"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/setting"
	"weichuan/internal/setup"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// seedAssetFS 内嵌设计还原用的蓝色主题 PNG 占位素材（由 gen_seed_assets.py 生成）。
// 这些仅用于本地联调/演示占位；正式上线请在 PC 后台上传真实图片替换。
//
//go:embed seedassets/*.png seedassets/*.jpg
var seedAssetFS embed.FS

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

	seedDemoData(db, cfg)

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
	log.Printf("演示账号：平台超管 %s/%s；员工手机 13832603521 / 密码 weichuan123（高立新，业务经理）", cfg.AdminUser, cfg.AdminPass)
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

// seedDemoData 注入一套可联调的演示数据（仅开发态；生产由 migrations SQL + PC 后台录入）。
// 数据内容与小程序 V1.3 设计稿一致：廊坊市朗汇机电设备有限公司 / 员工高立新 / 7 分类 / 2 精选项目 / 4 业务服务。
// 图片素材（LOGO/头像/分类图标/项目封面/服务图标等）由 writeSeedAssets 写入上传目录，
// 并以 cfg.UploadBaseURL 为基址回写，保证本机与真机（配 DEV_UPLOAD_BASE_URL）都能取到图。
func seedDemoData(db *gorm.DB, cfg config.Config) {
	assets := writeSeedAssets(cfg.UploadDir)

	// 1 号租户：朗汇机电（存量演示数据全部归属；多租户第一期的小程序缺省也落这里）
	db.Where("id = ?", 1).FirstOrCreate(&model.Company{ID: 1, Name: "廊坊市朗汇机电设备有限公司", ShortName: "朗汇机电", Status: "enabled"})

	// 公司信息 / 简介（company_id=1）
	db.Where("company_id = ?", 1).FirstOrCreate(&model.CompanyInfo{
		CompanyID: 1, FullName: "廊坊市朗汇机电设备有限公司", ShortName: "朗汇机电", Address: "廊坊市广阳区紫金城商住楼1-2-1911",
		Tel: "0316-8023950", Logo: assets["logo.png"],
	})
	db.Where("company_id = ?", 1).FirstOrCreate(&model.CompanyProfile{
		// 视频号档（跨主体）：用 feed-token；feed-id 留空 → 组件走 channels-token 分支
		// （强制 autoplay=false，用户点一下再播）。
		CompanyID: 1, IntroText: "成立于 2015 年 11 月，专注于电梯销售、安装、维修保养与改造服务，为住宅、政务中心、医疗、商业、公共交通、生产制造与仓储物流等多场景提供机电一体化解决方案。",
		VideoSource:    model.VideoSourceChannels,
		VideoFeedToken: "AThgKivzk",
		Cover:          assets["cover-company.jpg"],
	})

	// 经典业绩 8 分类（与设计稿一致，客户已确认锁定；原「生产制造、仓储物流」按设计稿拆为两个分类）
	cats := []model.AchievementCategory{
		{ID: 1, CompanyID: 1, Name: "居住住宅", Icon: assets["cat-1.png"], Sort: 1, IsLocked: true},
		{ID: 2, CompanyID: 1, Name: "政务办公", Icon: assets["cat-2.png"], Sort: 2, IsLocked: true},
		{ID: 3, CompanyID: 1, Name: "医院、学校", Icon: assets["cat-3.png"], Sort: 3, IsLocked: true},
		{ID: 4, CompanyID: 1, Name: "商业、酒店", Icon: assets["cat-4.png"], Sort: 4, IsLocked: true},
		{ID: 5, CompanyID: 1, Name: "公共交通", Icon: assets["cat-5.png"], Sort: 5, IsLocked: true},
		{ID: 6, CompanyID: 1, Name: "生产制造", Icon: assets["cat-6.png"], Sort: 6, IsLocked: true},
		{ID: 7, CompanyID: 1, Name: "仓储物流", Icon: assets["cat-7.png"], Sort: 7, IsLocked: true},
		{ID: 8, CompanyID: 1, Name: "私人住宅", Icon: assets["cat-8.png"], Sort: 8, IsLocked: true},
	}
	for _, c := range cats {
		db.Where("id = ?", c.ID).FirstOrCreate(&c)
	}

	// 项目案例：2 个首页精选（设计稿）+ 其余分类各 1 个（让客户验收时点开任意分类都有内容）
	projects := []model.ProjectCase{
		{ID: 101, CompanyID: 1, CategoryID: 1, Name: "大兴临空服务中心", Summary: "临空经济区地标机电总包",
			Detail: "<p>大兴临空服务中心项目涵盖多栋客货梯与自动扶梯的供货、安装与维保，满足高客流交通枢纽的可靠运行需求。</p>",
			Cover:  assets["cover-daxing.jpg"], Album: `["` + assets["album-1.png"] + `","` + assets["album-2.png"] + `"]`,
			IsHomeFeatured: true, Enabled: true, Sort: 1},
		{ID: 102, CompanyID: 1, CategoryID: 3, Name: "某医院电梯项目", Summary: "医疗楼宇专用电梯解决方案",
			Detail: "<p>某医院电梯项目提供医疗专用电梯的定制、安装与运维，兼顾洁净、安静与应急响应。</p>",
			Cover:  assets["cover-hospital.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: true, Enabled: true, Sort: 2},
		{ID: 103, CompanyID: 1, CategoryID: 1, Name: "某高层住宅电梯项目", Summary: "住宅客梯批量交付",
			Detail: "<p>为高层住宅提供客梯的批量供货与安装，统一调度的群控方案提升高峰运力。</p>",
			Cover:  assets["cover-p1b.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 2},
		{ID: 104, CompanyID: 1, CategoryID: 2, Name: "市民服务中心电梯工程", Summary: "政务大厅无机房客梯",
			Detail: "<p>市民服务中心采用无机房客梯，节省机房空间，满足政务服务大厅的无障碍通行需求。</p>",
			Cover:  assets["cover-p2.jpg"], Album: `["` + assets["album-2.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
		{ID: 105, CompanyID: 1, CategoryID: 3, Name: "某学校电梯项目", Summary: "校园电梯安全改造",
			Detail: "<p>针对校园既有电梯的安全改造，升级制动与监控，保障师生乘梯安全。</p>",
			Cover:  assets["cover-p3b.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 3},
		{ID: 106, CompanyID: 1, CategoryID: 4, Name: "商业综合体扶梯项目", Summary: "商场自动扶梯群控",
			Detail: "<p>商业综合体部署自动扶梯群控系统，结合客流分析优化运行节奏，节能高效。</p>",
			Cover:  assets["cover-p4.jpg"], Album: `["` + assets["album-2.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
		{ID: 107, CompanyID: 1, CategoryID: 5, Name: "地铁站点电梯项目", Summary: "轨道交通垂直交通",
			Detail: "<p>地铁站点垂直交通方案，重载电梯与扶梯满足大客流高频率运行。</p>",
			Cover:  assets["cover-p5.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
		{ID: 108, CompanyID: 1, CategoryID: 7, Name: "智能仓储货梯项目", Summary: "物流仓储货梯定制",
			Detail: "<p>为智能仓储定制货梯，对接 AGV 与 WMS，实现物料垂直转运自动化。</p>",
			Cover:  assets["cover-p6.jpg"], Album: `["` + assets["album-2.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
		{ID: 110, CompanyID: 1, CategoryID: 6, Name: "某生产制造货梯项目", Summary: "工厂车间货梯定制",
			Detail: "<p>为生产制造车间定制载重货梯，耐粉尘、抗震动，保障产线物料垂直运输稳定可靠。</p>",
			Cover:  assets["cover-p8.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
		{ID: 109, CompanyID: 1, CategoryID: 8, Name: "别墅家用电梯项目", Summary: "私人住宅别墅电梯",
			Detail: "<p>私人别墅家用电梯，定制轿厢与静音驱动，兼顾美观与乘坐舒适。</p>",
			Cover:  assets["cover-p7.jpg"], Album: `["` + assets["album-1.png"] + `"]`,
			IsHomeFeatured: false, Enabled: true, Sort: 1},
	}
	for _, p := range projects {
		db.Where("id = ?", p.ID).FirstOrCreate(&p)
	}

	// 4 个业务服务（设计稿示例：电梯销售/安装/维修保养/改造）
	svcs := []model.BizService{
		{ID: 1, CompanyID: 1, Title: "电梯销售", Icon: assets["svc-1.png"], Content: "<p>全系列电梯设备销售与选型咨询，匹配不同楼宇场景需求。</p>", Sort: 1},
		{ID: 2, CompanyID: 1, Title: "电梯安装", Icon: assets["svc-2.png"], Content: "<p>专业安装团队，标准化施工与验收交付，保障工期与质量。</p>", Sort: 2},
		{ID: 3, CompanyID: 1, Title: "电梯维保", Icon: assets["svc-3.png"], Content: "<p>7x24 应急响应与按需定制的保养计划，延长设备寿命。</p>", Sort: 3},
		{ID: 4, CompanyID: 1, Title: "电梯改造", Icon: assets["svc-4.png"], Content: "<p>旧梯升级改造，提升安全性、乘运体验与能效表现。</p>", Sort: 4},
	}
	for _, s := range svcs {
		db.Where("id = ?", s.ID).FirstOrCreate(&s)
	}

	// 首页布局（company_id=1）
	db.Where("company_id = ?", 1).FirstOrCreate(&model.HomeLayout{CompanyID: 1, Order: `["achievement","service"]`})

	// 1 个演示员工（设计稿员工：高立新 / 业务经理；手机号登录用）
	var cnt int64
	db.Model(&model.Employee{}).Where("phone_bidx = ?", pii.BlindIndex("13832603521")).Count(&cnt)
	if cnt == 0 {
		hash, _ := auth.HashPassword("weichuan123")
		emp := model.Employee{
			CompanyID: 1,
			// Phone2（第二个手机号，选填）默认不播种：演示名片只展示主号，需副号时编辑填写
			Name: "高立新", Title: "业务经理", Phone: "13832603521", Phone2: "",
			Landline: "0316-8023950",
			Email:    "13832603521@163.com",
			Avatar:   assets["avatar-gaolixin.png"], PasswordHash: hash, Status: "enabled",
		}
		db.Create(&emp)
		var got model.Employee
		db.Where("phone_bidx = ?", pii.BlindIndex("13832603521")).First(&got)
		fmt.Printf("演示员工 staffId=%d\n", got.StaffID)
	}
}

// writeSeedAssets 把内嵌的 PNG 占位素材写入上传目录，并返回「文件名→相对 URL」映射。
// 每次启动都重写（上传目录为临时目录，重启即清空），保证素材始终可用。
// 返回相对 /uploads/<name>（与各端按源拼绝对的统一约定一致）。
func writeSeedAssets(uploadDir string) map[string]string {
	urls := map[string]string{}
	entries, err := seedAssetFS.ReadDir("seedassets")
	if err != nil {
		log.Printf("读取内嵌素材失败: %v", err)
		return urls
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := seedAssetFS.ReadFile("seedassets/" + e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(uploadDir, e.Name()), data, 0644); err != nil {
			log.Printf("写入素材 %s 失败: %v", e.Name(), err)
			continue
		}
		urls[e.Name()] = "/uploads/" + e.Name()
	}
	return urls
}
