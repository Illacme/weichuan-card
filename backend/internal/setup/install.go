package setup

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"weichuan/internal/config"
	"weichuan/internal/dbopen"
	"weichuan/internal/handler"
	"weichuan/internal/migrate"
	"weichuan/internal/model"
	"weichuan/internal/setting"
)

// 完整安装向导（第二步）：在「未配置模式」下提供表单页 + 测试连接 + 一键安装。
//
// 与第一步「只读引导版」的分界：引导版只生成文本让人抄进 .env；本版直接把配置
// 写进 CONFIG_FILE（默认 ./data/runtime.env）、建库建表、建初始超管，然后自重启。
// 写入位置从「环境变量」变成「配置文件」是安全的，因为优先级规则
// （环境变量 > 配置文件 > 内置缺省）只有一条，不存在双源歧义。
//
// 安全边界（安装页是公网可见的未鉴权页面，这些是底线）：
//   - 仅在 Unconfigured() 为真时可安装，且进程内一次性（installDone）；
//   - 拒绝覆盖已初始化的数据库（admins 表有数据即拒），不会被重放成「重置系统」；
//   - 写接口带同源检查（sameOrigin），挡住跨源 CSRF 与 DNS rebinding；
//   - 响应与日志都不出现明文口令 / 完整 DSN（回显一律掩码）；
//   - 配置文件 0600、原子写。

// InstallReq 安装请求（扁平 JSON，与页面表单一一对应）。
type InstallReq struct {
	Engine         string `json:"engine"` // mysql | sqlite（空按 mysql）
	DBHost         string `json:"dbHost"`
	DBPort         string `json:"dbPort"`
	DBUser         string `json:"dbUser"`
	DBPassword     string `json:"dbPassword"`
	DBName         string `json:"dbName"`
	CreateDatabase bool   `json:"createDatabase"` // 仅 MySQL：库不存在时自动创建
	SQLitePath     string `json:"sqlitePath"`

	AdminUser  string `json:"adminUser"`
	AdminPass  string `json:"adminPass"`
	AdminPass2 string `json:"adminPass2"`
	JWTKey     string `json:"jwtKey"` // 留空 = 服务端随机生成

	CookieSecure bool `json:"cookieSecure"`
}

// TestResult 连接测试结果。WillCreate 表示「库不存在但勾了自动建库」——
// 这时测试仍然算通过（装的时候会建），不然勾了建库选项的人反而测不过。
type TestResult struct {
	Driver     string `json:"driver"`
	DSN        string `json:"dsn"` // 掩码后的连接串，仅供回显确认
	WillCreate bool   `json:"willCreate"`
	Note       string `json:"note"`
}

// InstallResult 安装结果。Restarted 表示已安排自重启（而非「已经重启完」——
// 重启是异步的，页面靠轮询 /api/setup/status 感知就绪）。
type InstallResult struct {
	OK         bool   `json:"ok"`
	Driver     string `json:"driver"`
	DSN        string `json:"dsn"`
	ConfigFile string `json:"configFile"`
	Restarted  bool   `json:"restarted"`
	Message    string `json:"message"`
}

var (
	installMu   sync.Mutex
	installDone bool
)

// TestConnection 只做连通性测试，不建库、不建表、不写任何东西。
// 例外：勾了「自动创建数据库」时测的是服务器本身（不选库），
// 库不存在会以 WillCreate=true 通过——这正是安装时的行为。
func TestConnection(req InstallReq) (TestResult, error) {
	dsn, driver, err := composeDSN(req)
	if err != nil {
		return TestResult{}, err
	}
	if driver == dbopen.DriverMySQL && req.CreateDatabase {
		serverDSN, name, err := mysqlServerDSN(dsn)
		if err != nil {
			return TestResult{}, err
		}
		sdb, _, err := dbopen.Open(serverDSN)
		if err != nil {
			return TestResult{}, friendlyErr(driver, dsn, err)
		}
		defer closeDB(sdb)
		var n int64
		if err := sdb.Raw(
			"SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name,
		).Scan(&n).Error; err != nil {
			return TestResult{}, fmt.Errorf("查询数据库列表失败：%v", scrubDSN(err.Error(), dsn))
		}
		if n > 0 {
			return TestResult{Driver: driver, DSN: maskDSN(dsn), Note: "数据库已存在，安装时将直接使用"}, nil
		}
		return TestResult{
			Driver: driver, DSN: maskDSN(dsn), WillCreate: true,
			Note: "数据库不存在，安装时将自动创建",
		}, nil
	}
	db, drv, err := dbopen.Open(dsn)
	if err != nil {
		return TestResult{}, friendlyErr(driver, dsn, err)
	}
	defer closeDB(db)
	return TestResult{Driver: drv, DSN: maskDSN(dsn), Note: "连接正常"}, nil
}

// Install 执行完整安装：校验 → （可选）建库 → 连接 → 建表 → seed 设置 → 建超管
// → 写配置文件。任何一步失败都不写配置，进程保持未配置状态，运维可改完重试。
//
// 刻意不在本函数内重启进程：重启要放到 HTTP 响应发出之后（见 ScheduleRestart），
// 且单测绝不能把测试进程自己 exec 掉。
func Install(req InstallReq) (InstallResult, error) {
	installMu.Lock()
	defer installMu.Unlock()

	if installDone {
		return InstallResult{}, errors.New("本进程已完成过一次安装，请刷新页面查看状态")
	}
	if !Unconfigured() {
		return InstallResult{}, fmt.Errorf("服务已完成配置，安装向导不可用。"+
			"如需重新安装：停止服务后删除配置文件 %s（或清空数据库）再启动", config.ConfigFilePath())
	}

	dsn, driver, err := composeDSN(req)
	if err != nil {
		return InstallResult{}, err
	}

	adminUser, adminPass, jwtKey, err := validateAdmin(req)
	if err != nil {
		return InstallResult{}, err
	}

	// MySQL 可选建库：连「不选库」的服务器连接执行 CREATE DATABASE。
	// 名字已在 composeDSN 校验过（仅字母数字下划线），不存在注入面。
	if driver == dbopen.DriverMySQL && req.CreateDatabase {
		serverDSN, name, err := mysqlServerDSN(dsn)
		if err != nil {
			return InstallResult{}, err
		}
		sdb, _, err := dbopen.Open(serverDSN)
		if err != nil {
			return InstallResult{}, friendlyErr(driver, dsn, err)
		}
		stmt := "CREATE DATABASE IF NOT EXISTS `" + name +
			"` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
		if err := sdb.Exec(stmt).Error; err != nil {
			closeDB(sdb)
			return InstallResult{}, fmt.Errorf("创建数据库 %s 失败（该账号可能需要建库权限）：%v",
				name, scrubDSN(err.Error(), dsn))
		}
		closeDB(sdb)
		log.Printf("[SETUP] 已确保数据库存在：%s", name)
	}

	// 连接（安装场景下这就是连通性测试的实做）
	db, drv, err := dbopen.Open(dsn)
	if err != nil {
		return InstallResult{}, friendlyErr(driver, dsn, err)
	}
	defer closeDB(db)

	// 新鲜库守卫：admins 表已有数据的库绝不碰。安装向导的职责是初始化空库，
	// 不是接管在用的系统——否则「重放安装页」就成了经典的重置漏洞。
	if db.Migrator().HasTable(&model.Admin{}) {
		var cnt int64
		if err := db.Model(&model.Admin{}).Count(&cnt).Error; err != nil {
			return InstallResult{}, fmt.Errorf("检查数据库状态失败：%v", err)
		}
		if cnt > 0 {
			return InstallResult{}, fmt.Errorf(
				"目标数据库已初始化（已有 %d 个管理员账号），安装向导只初始化空库。"+
					"如需重装请先清空数据库", cnt)
		}
	}

	// 建表策略与 cmd/server 逐条对齐（见 main.go 同址注释）：
	//   SQLite —— migrations/*.sql 是 MySQL 方言执行不了，按模型 AutoMigrate；
	//   MySQL  —— 表结构归 migrations/*.sql 管，这里按序应用（幂等，已应用过
	//     的语句按 Duplicate column / already exists / Duplicate entry 忽略，
	//     与 runbook §4 手工迁移的容错一致）。
	// 曾经这里对 MySQL 也无条件 AutoMigrate(model.All())，三个后果：
	//   ① 建出一套与迁移表名不一致的复数空表（ migrations 单数 vs gorm 复数，
	//      2026-10-21 已把迁移改名对齐，并有守门测试钉死）；
	//   ② 把迁移建的表按模型定义 MODIFY 成 longtext，正是 main.go 刻意规避的
	//      「AutoMigrate 擅自改列类型」；
	//   ③ sys_setting 因唯一有显式 TableName 而撞车，触发 gorm mysql driver 的
	//      MigrateColumnUnique 假阳性直接 1091（详见 setting.Init 的注释）。
	if driver == dbopen.DriverSQLite {
		if err := db.AutoMigrate(model.All()...); err != nil {
			return InstallResult{}, fmt.Errorf("初始化表结构失败：%v", err)
		}
	} else {
		if err := migrate.Apply(db); err != nil {
			return InstallResult{}, fmt.Errorf("应用数据库迁移失败：%v", err)
		}
	}

	// 系统设置 seed（后台「系统设置」的缺省值）
	if err := setting.Init(db); err != nil {
		return InstallResult{}, fmt.Errorf("初始化系统设置失败：%v", err)
	}

	// 初始超管
	handler.SeedAdmin(db, config.Config{AdminUser: adminUser, AdminPass: adminPass})
	// SeedAdmin 失败只打日志不返回，这里必须自己验：装完登不进去比报错更糟
	var seeded int64
	if err := db.Model(&model.Admin{}).Where("username = ?", adminUser).Count(&seeded).Error; err != nil {
		return InstallResult{}, fmt.Errorf("校验管理员账号失败：%v", err)
	}
	if seeded == 0 {
		return InstallResult{}, errors.New("创建初始管理员账号失败，请查看服务端日志后重试")
	}

	// 写配置文件（最后一步：数据库全部就绪后才落盘，失败也不会留下半配置）
	path := config.ConfigFilePath()
	kv := map[string]string{
		"DB_DSN":     dsn,
		"JWT_KEY":    jwtKey,
		"ADMIN_USER": adminUser,
		"ADMIN_PASS": adminPass,
	}
	if req.CookieSecure {
		kv["COOKIE_SECURE"] = "true"
	}
	if err := config.WriteFileValues(path, kv); err != nil {
		return InstallResult{Driver: drv, ConfigFile: path}, fmt.Errorf(
			"数据库已初始化，但写入配置文件失败：%v。请手动创建 %s 并填入连接信息后重启服务", err, path)
	}

	installDone = true
	log.Printf("[SETUP] 安装完成：driver=%s config=%s admin_user=%q", drv, path, adminUser)
	return InstallResult{
		OK:         true,
		Driver:     drv,
		DSN:        maskDSN(dsn),
		ConfigFile: path,
		Restarted:  restartAvailable,
		Message:    "安装完成，服务正在重启，即将进入登录页…",
	}, nil
}

// restartDelay 自重启延时：必须长到让 HTTP 响应完整 flush（本地回环是毫秒级，
// 但经 nginx 反代 + 慢客户端时要留余量），又不至于让运维觉得卡住了。
const restartDelay = 900 * time.Millisecond

// ScheduleRestart 安排一次安装后自重启。做成包级变量是为了测试能替换掉真实
// exec——安装逻辑单测绝不能把测试进程自己 exec 掉。
var ScheduleRestart = func() {
	go func() {
		time.Sleep(restartDelay)
		if err := reexecSelf(); err != nil {
			log.Printf("[SETUP] 安装后自重启失败：%v；请手动重启服务使配置生效", err)
		}
	}()
}

// validateAdmin 校验并归一化管理员与密钥输入。
// 两条硬性拒绝要特别说明：
//   - 拒绝内置缺省口令/密钥：这两个值随公开文档泄漏过，带它启动会被
//     InsecureGates 拦下——安装成功但进程起不来，比安装时直接报错更难排查；
//   - 两次密码必须一致：装完登不进去的第一大原因。
func validateAdmin(req InstallReq) (user, pass, jwtKey string, err error) {
	user = strings.TrimSpace(req.AdminUser)
	if user == "" {
		user = "admin"
	}
	if len(user) > 64 || strings.ContainsAny(user, " \t\r\n") {
		return "", "", "", errors.New("管理员用户名不能包含空格，且不超过 64 个字符")
	}
	pass = req.AdminPass
	if strings.TrimSpace(pass) == "" {
		return "", "", "", errors.New("请设置管理员密码")
	}
	// 先判「是不是已知缺省值」再判长度：缺省值本身可能不长，
	// 顺序反了会把「用了公开文档里的口令」报成「密码太短」，排查方向就错了。
	if pass == config.DefaultAdminPass {
		return "", "", "", fmt.Errorf("不能使用内置缺省口令 %s（该口令已写在公开文档里，任何人可登录）", config.DefaultAdminPass)
	}
	if len(pass) < config.MinAdminPassLen {
		return "", "", "", fmt.Errorf("管理员密码至少 %d 位", config.MinAdminPassLen)
	}
	if pass != req.AdminPass2 {
		return "", "", "", errors.New("两次输入的管理员密码不一致")
	}
	jwtKey = strings.TrimSpace(req.JWTKey)
	if jwtKey == "" {
		if jwtKey = secret(32); jwtKey == "" {
			return "", "", "", errors.New("生成 JWT 密钥失败（随机源不可用），请手动填写")
		}
	}
	if jwtKey == config.DefaultJWTKey {
		return "", "", "", fmt.Errorf("不能使用内置缺省密钥 %s（任何人都能伪造登录态）", config.DefaultJWTKey)
	}
	if len(jwtKey) < config.MinJWTKeyLen {
		return "", "", "", fmt.Errorf("JWT 密钥至少 %d 位（留空则由服务端自动生成）", config.MinJWTKeyLen)
	}
	if strings.ContainsAny(jwtKey, "\n\r") {
		return "", "", "", errors.New("JWT 密钥不能包含换行")
	}
	return user, pass, jwtKey, nil
}

// composeDSN 把表单字段组装成 DSN，同时做字段级校验。
// 服务端组装（而不是页面拼好传上来）是为了让校验只有一份实现。
func composeDSN(req InstallReq) (dsn, driver string, err error) {
	switch strings.ToLower(strings.TrimSpace(req.Engine)) {
	case "sqlite":
		p := strings.TrimSpace(req.SQLitePath)
		if p == "" {
			return "", "", errors.New("请填写 SQLite 数据文件路径")
		}
		if strings.ContainsAny(p, "\n\r") {
			return "", "", errors.New("文件路径不能包含换行")
		}
		// SQLite 不会自动创建目录：先建好，否则 Open 报
		// "unable to open database file"，排查半天才发现是少一层目录。
		if dir := filepath.Dir(p); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return "", "", fmt.Errorf("创建数据目录 %s 失败：%v", dir, err)
			}
		}
		return "sqlite://" + p, dbopen.DriverSQLite, nil
	case "mysql", "":
		host := strings.TrimSpace(req.DBHost)
		port := strings.TrimSpace(req.DBPort)
		user := strings.TrimSpace(req.DBUser)
		name := strings.TrimSpace(req.DBName)
		if host == "" || user == "" || name == "" {
			return "", "", errors.New("请填写完整的 MySQL 连接信息（主机、用户名、库名）")
		}
		if port == "" {
			port = "3306"
		}
		if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("端口 %q 不是有效端口号（1-65535）", port)
		}
		if !validDBName(name) {
			return "", "", fmt.Errorf("库名 %q 只能包含字母、数字和下划线（且不超过 64 字符）", name)
		}
		if strings.ContainsAny(host+user+req.DBPassword, "\n\r") {
			return "", "", errors.New("连接信息不能包含换行")
		}
		// 密码允许为空（本地开发常见）；允许含 @ / : / / ——go-sql-driver 的
		// 解析器按「最后一个 @」「最后一个 /」切分，与本文件 mysqlServerDSN 一致。
		dsn = user + ":" + req.DBPassword + "@tcp(" + host + ":" + port + ")/" + name +
			"?charset=utf8mb4&parseTime=true"
		return dsn, dbopen.DriverMySQL, nil
	default:
		return "", "", fmt.Errorf("不支持的数据库类型 %q（可选 mysql / sqlite）", req.Engine)
	}
}

// validDBName 库名白名单：字母数字下划线。白名单而不是转义——
// 库名会进 CREATE DATABASE 语句，宁可拒绝也不给注入面。
func validDBName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

// mysqlServerDSN 把 DSN 拆成「不选库的服务器连接串」和「库名」。
// 切分规则与 go-sql-driver/mysql 的 ParseDSN 一致：凭据段取最后一个 '@'，
// 路径段取最后一个 '/'（密码里可能含这两个字符）。
func mysqlServerDSN(dsn string) (serverDSN, dbName string, err error) {
	query := ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		query = dsn[i:]
		dsn = dsn[:i]
	}
	at := strings.LastIndexByte(dsn, '@')
	if at < 0 {
		return "", "", errors.New("MySQL 连接串格式不正确（缺少 @）")
	}
	rest := dsn[at+1:]
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", errors.New("MySQL 连接串格式不正确（缺少 /库名）")
	}
	name := strings.TrimSpace(rest[slash+1:])
	if !validDBName(name) {
		return "", "", fmt.Errorf("无法从连接串中解析出合法的库名（得到 %q）", name)
	}
	server := dsn[:at+1] + rest[:slash] + "/" + query
	return server, name, nil
}

// maskDSN 掩码密码部分，用于回显与日志。SQLite 无凭据，原样返回。
func maskDSN(dsn string) string {
	if dbopen.IsSQLiteDSN(dsn) {
		return dsn
	}
	at := strings.LastIndexByte(dsn, '@')
	if at < 0 {
		return dsn
	}
	colon := strings.IndexByte(dsn[:at], ':')
	if colon < 0 {
		return dsn
	}
	return dsn[:colon+1] + "******" + dsn[at:]
}

// scrubDSN 把错误文本里出现的完整 DSN 换成掩码版——驱动偶尔会把 DSN 拼进
// 错误消息，而那里面带着数据库口令，不能原样透出。
func scrubDSN(msg, dsn string) string {
	if dsn == "" {
		return msg
	}
	return strings.ReplaceAll(msg, dsn, maskDSN(dsn))
}

// friendlyErr 把常见连接错误翻译成可操作的提示。兜底返回原文（已做 DSN 脱敏）。
func friendlyErr(driver, dsn string, err error) error {
	msg := scrubDSN(err.Error(), dsn)
	switch {
	case strings.Contains(msg, "Unknown database"):
		return errors.New("数据库不存在：请勾选「自动创建数据库」，或先在 MySQL 中建好库")
	case strings.Contains(msg, "Access denied"):
		return errors.New("MySQL 拒绝访问：请检查用户名与密码")
	case strings.Contains(msg, "connection refused"):
		return errors.New("连不上数据库服务器：请确认主机与端口正确、服务已启动")
	case strings.Contains(msg, "client is required to use authentication"):
		return errors.New("MySQL 8 默认缓存认证插件不兼容：请在连接串加 auth_with_name_native=1 或改用支持 caching_sha2 的客户端")
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "lookup"):
		return errors.New("无法解析数据库主机名：请确认地址是否正确")
	case driver == dbopen.DriverSQLite && strings.Contains(msg, "unable to open database file"):
		return errors.New("无法创建/打开 SQLite 文件：请确认所在目录存在且进程有写权限")
	}
	return errors.New(msg)
}

// closeDB 关闭 gorm 连接（测试连接 / 建库用的临时连接要即时释放）。
func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		// 临时连接，关闭失败无后果（进程退出也会回收）
		_ = sqlDB.Close()
	}
}
