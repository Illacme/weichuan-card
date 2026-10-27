package setup

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/dbopen"
	"weichuan/internal/model"
	"weichuan/internal/pii"
)

// resetInstallState 清掉进程内一次性锁：每个安装用例都应是「全新进程」视角。
// installDone 是包级变量，同包测试可以直接重置（生产路径由进程重启天然重置）。
func resetInstallState(t *testing.T) {
	t.Helper()
	installMu.Lock()
	installDone = false
	installMu.Unlock()
}

// sqliteEnv 准备一套干净的未配置环境：CONFIG_FILE 与 SQLite 数据文件都在临时目录。
// 数据目录先建好——modernc 对「目录不存在」报的是 unable to open database file:
// out of memory，错误信息有误导性，测试里不踩这个坑。
func sqliteEnv(t *testing.T) (cfgPath, dbPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "data", "runtime.env")
	dbPath = filepath.Join(dir, "data", "weichuan.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("DB_DSN", "")
	t.Setenv("JWT_KEY", "")
	t.Setenv("ADMIN_PASS", "")
	t.Setenv("MYSQL_ROOT_PASSWORD", "")
	t.Setenv(pii.KeyEnvVar, "")
	return cfgPath, dbPath
}

// 端到端：SQLite 全流程安装。这是「填好检测正常保存，后面就能顺利登录」的保底用例——
// 库文件、表、超管、配置文件四样都必须真实落地。
func TestInstallSQLiteEndToEnd(t *testing.T) {
	resetInstallState(t)
	cfgPath, dbPath := sqliteEnv(t)

	res, err := Install(InstallReq{
		Engine:     "sqlite",
		SQLitePath: dbPath,
		AdminUser:  "admin",
		AdminPass:  "installer-pass-123",
		AdminPass2: "installer-pass-123",
	})
	if err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	if !res.OK || res.Driver != dbopen.DriverSQLite || !res.Restarted {
		t.Fatalf("安装结果异常：%+v", res)
	}
	if res.DSN == "" || strings.Contains(res.DSN, "installer-pass") {
		t.Fatalf("回显的 DSN 不应含口令：%q", res.DSN)
	}

	// 1) 库文件与表结构
	db, _, err := dbopen.Open("sqlite://" + dbPath)
	if err != nil {
		t.Fatalf("打开装好的库失败：%v", err)
	}
	defer closeDB(db)
	var admins int64
	if err := db.Model(&model.Admin{}).Where("username = ?", "admin").Count(&admins).Error; err != nil {
		t.Fatalf("查询管理员失败：%v", err)
	}
	if admins != 1 {
		t.Fatalf("应种子出 1 个管理员，实际 %d", admins)
	}
	// 登录能校验哈希：SeedAdmin 用的是 HashPassword，这里反查 hash 存在且非明文
	var a model.Admin
	if err := db.Where("username = ?", "admin").First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.PasswordHash == "" || a.PasswordHash == "installer-pass-123" {
		t.Fatal("管理员密码必须存哈希，不能是明文")
	}
	// sys_setting 已 seed（后台系统设置的缺省值）
	var settings int64
	if err := db.Table("sys_setting").Count(&settings).Error; err != nil {
		t.Fatalf("sys_setting 应已建表并 seed：%v", err)
	}
	if settings == 0 {
		t.Fatal("sys_setting 不应为空")
	}

	// 2) 配置文件：内容、权限、以及「重启后已配置」的闭环
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("配置文件应已写入：%v", err)
	}
	text := string(data)
	for _, want := range []string{"DB_DSN=sqlite://" + dbPath, "JWT_KEY=", "ADMIN_USER=admin", "ADMIN_PASS=installer-pass-123", "PII_ENC_KEY="} {
		if !strings.Contains(text, want) {
			t.Fatalf("配置文件应包含 %q，实际：\n%s", want, text)
		}
	}
	// PII 密钥必须随安装一起签发，走的是 cmd/server 读取它的同一条路径
	// （config.Resolved）。漏了它，装完重启会直接被启动门槛拒绝——
	// 表现是「刚装好就起不来」，而原因却写在完全不同的地方，最难排查。
	if k := config.Resolved(pii.KeyEnvVar); k == "" {
		t.Fatal("安装应自动生成并写入 PII_ENC_KEY")
	}
	if fi, err := os.Stat(cfgPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限应 0600，实际 %v (%v)", fi.Mode().Perm(), err)
	}
	if Unconfigured() {
		t.Fatal("安装完成后必须判定为已配置（否则重启后又回到引导页）")
	}
	cfg := config.Load()
	if cfg.DBDSN != "sqlite://"+dbPath || cfg.AdminPass != "installer-pass-123" {
		t.Fatalf("重启后的配置应来自文件：dsn=%q pass=%q", cfg.DBDSN, cfg.AdminPass)
	}
	if len(cfg.InsecureGates()) != 0 {
		t.Fatalf("安装向导产出的配置应通过安全门槛：%v", cfg.InsecureGates())
	}

	// 3) 一次性锁：同进程再装一次应被拒
	if _, err := Install(InstallReq{Engine: "sqlite", SQLitePath: dbPath, AdminPass: "x", AdminPass2: "x"}); err == nil {
		t.Fatal("同进程第二次安装应被拒绝")
	}
}

// 已配置（DB_DSN 由环境变量给出）时安装器必须拒绝：这是「不被重放成重置」的底线。
func TestInstallRefusesWhenAlreadyConfigured(t *testing.T) {
	resetInstallState(t)
	noConfigFile(t)
	t.Setenv("DB_DSN", "root:root@tcp(127.0.0.1:3306)/weichuan")
	_, err := Install(InstallReq{
		Engine: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "x.db"),
		AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123",
	})
	if err == nil {
		t.Fatal("已配置时安装应被拒绝")
	}
	if !strings.Contains(err.Error(), "已完成配置") {
		t.Fatalf("拒绝原因应说清「已完成配置」，实际：%v", err)
	}
}

// 目标库已有数据（admins 非空）时拒绝：安装向导只初始化空库。
func TestInstallRefusesExistingData(t *testing.T) {
	resetInstallState(t)
	_, dbPath := sqliteEnv(t)

	// 预置一个「已初始化」的库
	pre, _, err := dbopen.Open("sqlite://" + dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.AutoMigrate(&model.Admin{}); err != nil {
		t.Fatal(err)
	}
	if err := pre.Create(&model.Admin{CompanyID: 0, Username: "old-admin", PasswordHash: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	closeDB(pre)

	_, err = Install(InstallReq{
		Engine: "sqlite", SQLitePath: dbPath,
		AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123",
	})
	if err == nil {
		t.Fatal("已有数据的库应被拒绝")
	}
	if !strings.Contains(err.Error(), "已初始化") {
		t.Fatalf("拒绝原因应说明库已初始化，实际：%v", err)
	}
	// 被拒时不能写配置文件（否则半配置状态起不来）
	if _, err := os.Stat(config.ConfigFilePath()); err == nil {
		t.Fatal("安装失败时不应留下配置文件")
	}
}

func TestInstallValidationErrors(t *testing.T) {
	resetInstallState(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runtime.env")
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("DB_DSN", "")
	t.Setenv("JWT_KEY", "")
	t.Setenv("ADMIN_PASS", "")

	cases := []struct {
		name    string
		req     InstallReq
		wantSub string
	}{
		{
			"MySQL 字段不全",
			InstallReq{Engine: "mysql", DBHost: "127.0.0.1", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123"},
			"完整的 MySQL",
		},
		{
			"库名含非法字符",
			InstallReq{Engine: "mysql", DBHost: "h", DBUser: "u", DBName: "lang-hui;drop", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123"},
			"库名",
		},
		{
			"端口非法",
			InstallReq{Engine: "mysql", DBHost: "h", DBPort: "abc", DBUser: "u", DBName: "weichuan", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123"},
			"端口",
		},
		{
			"未知引擎",
			InstallReq{Engine: "oracle", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123"},
			"不支持的数据库类型",
		},
		{
			"SQLite 路径为空",
			InstallReq{Engine: "sqlite", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123"},
			"SQLite 数据文件路径",
		},
		{
			"密码太短",
			InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "a.db"), AdminPass: "short", AdminPass2: "short"},
			"至少 8 位",
		},
		{
			"两次密码不一致",
			InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "b.db"), AdminPass: "installer-pass-123", AdminPass2: "installer-pass-124"},
			"不一致",
		},
		{
			"使用内置缺省口令",
			InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "c.db"), AdminPass: config.DefaultAdminPass, AdminPass2: config.DefaultAdminPass},
			"内置缺省口令",
		},
		{
			"自定义 JWT 密钥过短",
			InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "d.db"), AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123", JWTKey: "short"},
			"JWT 密钥至少",
		},
		{
			"使用内置缺省 JWT 密钥",
			InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "e.db"), AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123", JWTKey: config.DefaultJWTKey},
			"内置缺省密钥",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Install(c.req)
			if err == nil {
				t.Fatalf("应报错：%s", c.name)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("错误信息应包含 %q，实际：%v", c.wantSub, err)
			}
			if _, err := os.Stat(cfgPath); err == nil {
				t.Fatal("校验失败的安装不应留下配置文件")
			}
		})
	}
}

// DSN 组装：端口缺省、库名白名单、sqlite 建目录。
func TestComposeDSN(t *testing.T) {
	dsn, driver, err := composeDSN(InstallReq{
		Engine: "mysql", DBHost: "db.internal", DBUser: "u", DBPassword: "p@ss/w:rd", DBName: "weichuan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if driver != dbopen.DriverMySQL {
		t.Fatalf("driver=%q", driver)
	}
	want := "u:p@ss/w:rd@tcp(db.internal:3306)/weichuan?charset=utf8mb4&parseTime=true"
	if dsn != want {
		t.Fatalf("DSN 组装错误：\n got %q\nwant %q", dsn, want)
	}

	// 空 engine 按 mysql 兜底
	if _, driver, _ = composeDSN(InstallReq{DBHost: "h", DBUser: "u", DBName: "weichuan"}); driver != dbopen.DriverMySQL {
		t.Fatalf("空 engine 应按 mysql 处理，实际 %q", driver)
	}

	// sqlite：路径落盘目录不存在时应自动创建
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	dsn, driver, err = composeDSN(InstallReq{Engine: "sqlite", SQLitePath: filepath.Join(dir, "weichuan.db")})
	if err != nil {
		t.Fatal(err)
	}
	if driver != dbopen.DriverSQLite || dsn != "sqlite://"+filepath.Join(dir, "weichuan.db") {
		t.Fatalf("sqlite DSN 异常：%q %q", dsn, driver)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("数据目录应被自动创建：%v", err)
	}
}

// 服务器连接串拆分：切分规则必须与 go-sql-driver 一致（最后一个 @、最后一个 /），
// 否则密码里含这两个字符时 CREATE DATABASE 会连错库。
func TestMysqlServerDSN(t *testing.T) {
	cases := []struct {
		name     string
		dsn      string
		wantSrv  string
		wantName string
		wantErr  bool
	}{
		{
			"常规",
			"root:root@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true",
			"root:root@tcp(127.0.0.1:3306)/?charset=utf8mb4&parseTime=true",
			"weichuan", false,
		},
		{
			"密码含@和/",
			"root:p@ss/w@rd@tcp(h:3307)/db1",
			"root:p@ss/w@rd@tcp(h:3307)/",
			"db1", false,
		},
		{"缺@", "rootroottcp(h:3306)/db", "", "", true},
		{"缺库名", "root:p@tcp(h:3306)/", "", "", true},
		{"库名非法", "root:p@tcp(h:3306)/db-1", "", "", true},
		{"无参数串", "root:p@tcp(h:3306)/db2", "root:p@tcp(h:3306)/", "db2", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, name, err := mysqlServerDSN(c.dsn)
			if c.wantErr {
				if err == nil {
					t.Fatalf("应报错，实际 srv=%q name=%q", srv, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错：%v", err)
			}
			if srv != c.wantSrv || name != c.wantName {
				t.Fatalf("srv=%q name=%q, want srv=%q name=%q", srv, name, c.wantSrv, c.wantName)
			}
		})
	}
}

func TestMaskDSN(t *testing.T) {
	if got := maskDSN("root:s3cret@tcp(h:3306)/db?x=1"); got != "root:******@tcp(h:3306)/db?x=1" {
		t.Fatalf("MySQL DSN 密码应被掩码：%q", got)
	}
	if got := maskDSN("sqlite:///data/weichuan.db"); got != "sqlite:///data/weichuan.db" {
		t.Fatalf("SQLite 无凭据不应改动：%q", got)
	}
	if got := maskDSN("garbage"); got != "garbage" {
		t.Fatalf("无法解析时应原样返回：%q", got)
	}
}

// 连接测试：SQLite 一路应直接通过并回显 driver。
func TestTestConnectionSQLite(t *testing.T) {
	resetInstallState(t)
	sqliteEnv(t)
	dbPath := filepath.Join(t.TempDir(), "weichuan.db")
	res, err := TestConnection(InstallReq{Engine: "sqlite", SQLitePath: dbPath})
	if err != nil {
		t.Fatalf("SQLite 连接测试应通过：%v", err)
	}
	if res.Driver != dbopen.DriverSQLite {
		t.Fatalf("driver=%q", res.Driver)
	}
	if res.DSN != "sqlite://"+dbPath {
		t.Fatalf("回显 DSN 异常：%q", res.DSN)
	}
}

// HTTP 层：安装接口信封形状 + 一次性锁 + 重启可被测试替换。
func TestInstallEndpointEnvelope(t *testing.T) {
	resetInstallState(t)
	cfgPath, dbPath := sqliteEnv(t)

	// 替换掉真实自重启：单测绝不能把测试进程 exec 掉
	orig := ScheduleRestart
	restarted := 0
	ScheduleRestart = func() { restarted++ }
	defer func() { ScheduleRestart = orig }()

	r := Engine()
	body := `{"engine":"sqlite","sqlitePath":"` + dbPath +
		`","adminUser":"admin","adminPass":"installer-pass-123","adminPass2":"installer-pass-123"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OK         bool   `json:"ok"`
			Driver     string `json:"driver"`
			ConfigFile string `json:"configFile"`
			Restarted  bool   `json:"restarted"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, w.Body.String())
	}
	if resp.Code != 0 || !resp.Data.OK || resp.Data.Driver != dbopen.DriverSQLite {
		t.Fatalf("安装接口响应异常：%+v", resp)
	}
	if resp.Data.ConfigFile != cfgPath {
		t.Fatalf("应回显配置文件路径 %q，实际 %q", cfgPath, resp.Data.ConfigFile)
	}
	if restarted != 1 {
		t.Fatalf("安装成功后应安排一次自重启，实际 %d 次", restarted)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("配置文件应已写入：%v", err)
	}
}

// 连接测试接口：坏请求给可读错误，不崩不挂。
func TestTestEndpointRejectsBadRequest(t *testing.T) {
	resetInstallState(t)
	sqliteEnv(t)
	r := Engine()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/test", strings.NewReader(`{"engine":"mysql"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := jsonUnmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if resp.Code == 0 || resp.Msg == "" {
		t.Fatalf("字段不全时应返回可读错误：%+v", resp)
	}
}

// MySQL 全流程（含 CREATE DATABASE）：需要真实 MySQL，门控见 TEST_MYSQL_DSN。
// 库名带 _inst 后缀，避免碰到开发机上已有的库。
func TestInstallMySQLEndToEnd(t *testing.T) {
	base := os.Getenv("TEST_MYSQL_DSN")
	if base == "" {
		t.Skip("set TEST_MYSQL_DSN (e.g. root:root@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true) to run MySQL install path")
	}
	resetInstallState(t)
	cfgPath, _ := sqliteEnv(t)
	t.Setenv("DB_DSN", "")

	user, pass, host, port, baseName, err := splitMySQLDSN(base)
	if err != nil {
		t.Fatalf("TEST_MYSQL_DSN 无法解析：%v", err)
	}
	dbName := baseName + "_inst"
	if len(dbName) > 64 {
		dbName = dbName[:64]
	}

	// 先连服务器确认可用，并确保目标库不存在（模拟全新安装）
	serverDSN, _, err := mysqlServerDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	sdb, _, err := dbopen.Open(serverDSN)
	if err != nil {
		t.Skipf("MySQL 不可达，跳过：%v", err)
	}
	defer closeDB(sdb)
	if err := sdb.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error; err != nil {
		t.Fatalf("清理测试库失败：%v", err)
	}

	req := InstallReq{
		Engine: "mysql", DBHost: host, DBPort: port, DBUser: user, DBPassword: pass,
		DBName: dbName, CreateDatabase: true,
		AdminUser: "admin", AdminPass: "installer-pass-123", AdminPass2: "installer-pass-123",
	}
	// 测试连接：库不存在 + 勾了自动创建 → 应通过并提示「将创建」
	testRes, err := TestConnection(req)
	if err != nil {
		t.Fatalf("勾了自动建库时连接测试应通过：%v", err)
	}
	if !testRes.WillCreate {
		t.Fatalf("库不存在时应提示将创建，实际 %+v", testRes)
	}

	res, err := Install(req)
	if err != nil {
		t.Fatalf("MySQL 安装失败（请确认 TEST_MYSQL_DSN 的账号有建库权限）：%v", err)
	}
	if !res.OK || res.Driver != dbopen.DriverMySQL {
		t.Fatalf("安装结果异常：%+v", res)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("配置文件应已写入：%v", err)
	}
	// 验证库与管理员真实存在
	dsn, _, err := composeDSN(req)
	if err != nil {
		t.Fatal(err)
	}
	db, _, err := dbopen.Open(dsn)
	if err != nil {
		t.Fatalf("打开装好的 MySQL 库失败：%v", err)
	}
	defer closeDB(db)
	var admins int64
	if err := db.Model(&model.Admin{}).Count(&admins).Error; err != nil {
		t.Fatalf("查询管理员失败：%v", err)
	}
	if admins != 1 {
		t.Fatalf("应种子出 1 个管理员，实际 %d", admins)
	}
	// 清理
	if err := sdb.Exec("DROP DATABASE IF EXISTS `" + dbName + "`").Error; err != nil {
		t.Logf("清理测试库失败（可手动删）：%v", err)
	}
}

// splitMySQLDSN 测试专用：把 go-sql-driver DSN 拆成表单字段，
// 与 composeDSN 互为逆运算（MySQL 用例要靠它把 TEST_MYSQL_DSN 搬进请求体）。
func splitMySQLDSN(dsn string) (user, pass, host, port, name string, err error) {
	query := ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		query = dsn[i:]
		dsn = dsn[:i]
	}
	at := strings.LastIndexByte(dsn, '@')
	if at < 0 {
		return "", "", "", "", "", errors.New("缺少 @")
	}
	cred := dsn[:at]
	colon := strings.IndexByte(cred, ':')
	if colon < 0 {
		return "", "", "", "", "", errors.New("凭据段缺少 :")
	}
	user, pass = cred[:colon], cred[colon+1:]
	rest := dsn[at+1:]
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", "", "", "", errors.New("缺少 /库名")
	}
	name = rest[slash+1:]
	addr := rest[:slash]
	if i := strings.IndexByte(addr, '('); i >= 0 {
		addr = addr[i+1:]
		if j := strings.IndexByte(addr, ')'); j >= 0 {
			addr = addr[:j]
		}
	}
	host, port = addr, "3306"
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		host, port = addr[:i], addr[i+1:]
	}
	_ = query
	return user, pass, host, port, name, nil
}

// jsonUnmarshal 包一层，避免每个用例都 import encoding/json。
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
