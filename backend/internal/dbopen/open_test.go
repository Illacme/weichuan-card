package dbopen

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/model"
)

// TestIsSQLiteDSN 钉死路由判据：判错比不判更糟——把生产 MySQL 读成空 SQLite 库，
// 或把 SQLite 路径丢给 MySQL 驱动，两者都是启动期就炸的事故。
func TestIsSQLiteDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"默认 MySQL DSN", "root:root@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true", false},
		{"compose 里的 MySQL DSN", "root:pw@tcp(mysql:3306)/weichuan?charset=utf8mb4&parseTime=true", false},
		{"sqlite:// 前缀", "sqlite:///data/weichuan.db", true},
		{"sqlite: 前缀", "sqlite:/data/weichuan.db", true},
		{"file: URI", "file:/data/weichuan.db", true},
		{"裸路径 .db", "/data/weichuan.db", true},
		{"裸路径带查询串", "/data/weichuan.db?_pragma=busy_timeout(1000)", true},
		{".sqlite3 结尾", "/data/weichuan.sqlite3", true},
		{"扩展名不在白名单的裸路径不算 SQLite", "/data/weichuan", false},
		{"空串", "", false},
		{"空白串", "   ", false},
	}
	for _, c := range cases {
		if got := IsSQLiteDSN(c.dsn); got != c.want {
			t.Errorf("%s：IsSQLiteDSN(%q) = %v，want %v", c.name, c.dsn, got, c.want)
		}
	}
}

// TestNormalizeSQLiteDSN 三项默认 pragma 必须补上，但用户显式指定的同名项优先。
// WAL + busy_timeout 是 SQLite 上生产可用的底线：缺了会出现「并发读被写挡住」
// 与「并发写直接报 database is locked」。
func TestNormalizeSQLiteDSN(t *testing.T) {
	got := NormalizeSQLiteDSN("sqlite:///data/weichuan.db")
	for _, want := range []string{
		"/data/weichuan.db",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q，实际：%s", want, got)
		}
	}
	if strings.HasPrefix(got, "sqlite:") {
		t.Errorf("前缀应被剥离，实际：%s", got)
	}

	// 用户指定的 busy_timeout 优先，其余两项仍要补
	got = NormalizeSQLiteDSN("sqlite:/data/weichuan.db?_pragma=busy_timeout(1000)")
	if !strings.Contains(got, "busy_timeout(1000)") {
		t.Errorf("应保留用户值，实际：%s", got)
	}
	if strings.Contains(got, "busy_timeout(5000)") {
		t.Errorf("不应覆盖用户值，实际：%s", got)
	}
	if !strings.Contains(got, "_pragma=journal_mode(WAL)") {
		t.Errorf("未指定的项仍应补默认值，实际：%s", got)
	}

	// 已有查询串时用 & 连接，不能出现两个 ?
	got = NormalizeSQLiteDSN("file:/data/weichuan.db?mode=rwc")
	if n := strings.Count(got, "?"); n != 1 {
		t.Errorf("查询串分隔符拼接错误（? 出现 %d 次）：%s", n, got)
	}
}

// TestGormLoggerRedactsBindParams 钉住「绑定参数不进日志」。
//
// 2026-11-03 线上事故：company_wx_app 的 UPDATE 语句把 app_secret 明文打进了
// GORM 日志（用户又在工单里贴了日志 → 密钥只能重置）。这类泄露不报错、不告警，
// 只在「日志被人看到」的那一刻变成事故——只能靠断言钉住。
//
// 做法：接管标准 log 输出，跑一条**必然报错**的语句（Warn 级只在慢查询/错误时
// 打印，普通成功查询不打），断言日志里没有密钥值、但有 ? 占位符。
func TestGormLoggerRedactsBindParams(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.db")
	db, _, err := Open("sqlite://" + p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// gormLogger 绑的是 log.Default()，换掉标准 log 的输出即可捕获它
	var buf bytes.Buffer
	origOut, origFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	const secret = "SUPERSECRET-app-secret-0123456789"
	var rows []map[string]any
	// 表不存在 → gorm 以 Error 级打印 SQL + 错误
	if err := db.Raw("SELECT * FROM definitely_missing WHERE app_secret = ?", secret).Scan(&rows).Error; err == nil {
		t.Fatal("期望查询报错（表不存在），否则 gorm 不会打日志、断言落空")
	}

	out := buf.String()
	if !strings.Contains(out, "definitely_missing") {
		t.Fatalf("日志里没有这条 SQL，捕获失败，断言无意义。输出：%s", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("绑定参数泄露进了日志（应被参数化为 ?）：%s", out)
	}
	if !strings.Contains(out, "?") {
		t.Fatalf("日志里应有 ? 占位符（ParameterizedQueries 生效的证据）：%s", out)
	}
}

// TestGormLoggerRedactsUpdate 覆盖 **callbacks 路径**（Updates/Find/Create）：
// 这条路径靠 ParamsFilter 丢弃绑定参数。与上面 Scan 路径（靠 redactSQL）是两道独立防线，
// 任一失效都该被断言抓住——2026-11-03 泄露的正是这条 UPDATE 路径。
func TestGormLoggerRedactsUpdate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log2.db")
	db, _, err := Open("sqlite://" + p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	type rec struct {
		ID     int64 `gorm:"primaryKey"`
		Secret string
	}
	if err := db.AutoMigrate(&rec{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&rec{Secret: "x"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var buf bytes.Buffer
	origOut, origFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	const secret = "SUPERSECRET-wx-app-secret-9876543210"
	// 往不存在的列写 → 必然报错 → 以 Error 级打印 SQL
	if err := db.Model(&rec{}).Where("id = ?", 1).Update("no_such_col", secret).Error; err == nil {
		t.Fatal("期望 Update 报错（列不存在），否则 gorm 不打日志、断言落空")
	}

	out := buf.String()
	if !strings.Contains(out, "no_such_col") {
		t.Fatalf("日志里没有这条 SQL，捕获失败。输出：%s", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("绑定参数泄露进了日志（应被参数化为 ?）：%s", out)
	}
}

// TestRedactSQL 钉住字面量打码的边界：该打码的打干净、不该动的（标识符/数字）保留。
func TestRedactSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		// 基本形态：单/双引号字面量都打码（SQLite 用双引号表示字符串）
		{`SELECT * FROM t WHERE a = 'x'`, `SELECT * FROM t WHERE a = '?'`},
		{`SELECT * FROM t WHERE a = "x"`, `SELECT * FROM t WHERE a = "?"`},
		// 反引号是标识符（MySQL），必须保留
		{"SELECT * FROM `company_wx_app` WHERE app_secret = 'k'", "SELECT * FROM `company_wx_app` WHERE app_secret = '?'"},
		// 数字字面量保留：分页与行 id 是最常看的排查线索
		{`SELECT * FROM t LIMIT 10 OFFSET 20`, `SELECT * FROM t LIMIT 10 OFFSET 20`},
		// SQL 标准转义：双写引号不是结束
		{`WHERE a = 'it''s here'`, `WHERE a = '?'`},
		// MySQL 反斜杠转义
		{`WHERE a = 'it\'s' AND b = 'y'`, `WHERE a = '?' AND b = '?'`},
		// 中文与空串
		{`WHERE name = "张三"`, `WHERE name = "?"`},
		{`WHERE a = ''`, `WHERE a = '?'`},
		// 无引号 SQL 原样返回（不引入任何改动）
		{`UPDATE t SET a = ? WHERE id = ?`, `UPDATE t SET a = ? WHERE id = ?`},
		// 引号不闭合（截断的 SQL）也要能终止，不能吞掉后面内容
		{`WHERE a = 'unterminated`, `WHERE a = '?`},
	}
	for _, c := range cases {
		if got := redactSQL(c.in); got != c.want {
			t.Errorf("redactSQL(%q)\n  got  %q\n  want %q", c.in, got, c.want)
		}
	}
}

// TestOpenSQLite 端到端：DSN 走完 Open 后，库真的可用且 pragma 生效。
// 这是「生产上 SQLite」这条路径唯一能自动化验证的环节——驱动选错、
// pragma 没吃进去，都在这里暴露，而不是等到线上报 database is locked。
func TestOpenSQLite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "weichuan.db")
	db, driver, err := Open("sqlite://" + p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if driver != DriverSQLite {
		t.Fatalf("driver = %q，want %q", driver, DriverSQLite)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	var jm string
	if err := db.Raw("PRAGMA journal_mode").Scan(&jm).Error; err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if jm != "wal" {
		t.Errorf("journal_mode = %q，want wal（未生效则并发读会被写锁挡住）", jm)
	}
	var bt int64
	if err := db.Raw("PRAGMA busy_timeout").Scan(&bt).Error; err != nil {
		t.Fatalf("pragma busy_timeout: %v", err)
	}
	if bt != 5000 {
		t.Errorf("busy_timeout = %d，want 5000", bt)
	}

	// 表必须真的建出来：AutoMigrate 静默跳过会让运行期才报 no such table
	var cnt int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='employees'").Scan(&cnt).Error; err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if cnt == 0 {
		t.Error("employees 表未建出——model.All() 漏了 Employee？")
	}
}

// TestOpenFileURI 保证 file: URI 写法同样可用（文档里给了这种写法）。
func TestOpenFileURI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "weichuan.db")
	db, driver, err := Open("file:" + p)
	if err != nil {
		t.Fatalf("open file: URI: %v", err)
	}
	if driver != DriverSQLite {
		t.Fatalf("driver = %q，want sqlite", driver)
	}
	if err := db.Exec("CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
}
