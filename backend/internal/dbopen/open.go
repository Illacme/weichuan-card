// Package dbopen 按 DSN 形态选择数据库驱动，让同一个生产二进制同时支持 MySQL 与 SQLite。
//
// 为什么需要这个包：业务层是方言无关的（`TestAPIFlow` 在 SQLite 上跑完整流程，
// `TestAPIFlowMySQL` 用真实 MySQL 跑完全相同断言做 parity），唯一写死 MySQL 的地方
// 是 `cmd/server` 的 `gorm.Open(mysql.Open(...))`。把驱动选择收敛到这里，
// 部署时就能只靠一个 `DB_DSN` 环境变量决定用哪个库，不必换二进制。
//
// 调用方还需要按返回的驱动名决定建表方式：`migrations/*.sql` 是 MySQL 方言
// （ENGINE=InnoDB / AUTO_INCREMENT / 反引号），SQLite 侧只能走 GORM AutoMigrate。
package dbopen

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 驱动名常量，供调用方分支（建表方式、日志、运维提示）。
const (
	DriverMySQL  = "mysql"
	DriverSQLite = "sqlite"
)

// gormLogger 返回统一的 GORM 日志配置。三道防线：
//
//  1. ParameterizedQueries=true：callbacks 路径（Create/Updates/Find…）的 SQL
//     只打 `?` 占位，**绑定参数不进日志**（gorm 通过 ParamsFilter 丢弃 vars）。
//  2. redactingLogger 在 Trace 出口把字符串字面量再打码一遍：
//     gorm v1.25 的 DB.Scan（finisher_api.go）有一条独立日志路径，用 logger.Recorder
//     记录**已 Explain（参数已内联）**的 SQL 再交给 Trace，绕过 ParamsFilter——
//     而项目里 db.Raw(...).Scan(...) 用得很多，只靠第 1 条会漏。
//  3. 输出用 log.Default()：logx 已接管标准 log 的输出目标与格式
//     （LOG_FORMAT=json 时逐行包成 JSONL）。自己 new 一个 os.Stderr 的 logger
//     会绕开它，出现「文本里套 JSON」且丢失统一时间戳。
//
// 2026-11-03 事故背景：company_wx_app 的 UPDATE 把 app_secret 明文写进日志
// （用户又在工单里贴了日志 → 密钥只能去微信后台重置）。排查需要的是
// 「谁在什么时候动了哪张表、报了什么错、影响几行」——语句 + 错误码 + rows 够用，
// 值本身反而是负债：app_secret / upload_key / PII 密文 / 口令 hash 都经过这里。
// 需要看参数时请在测试或本地用 db.Debug()，生产日志不该有这条路。
//
// 级别保持 gorm 默认的 Warn（慢查询 + 错误），不顺手改成 Info：
// 全量 SQL 日志在并发下会淹没业务日志。慢查询阈值同默认 200ms。
func gormLogger() logger.Interface {
	base := logger.New(log.Default(), logger.Config{
		SlowThreshold:        200 * time.Millisecond,
		LogLevel:             logger.Warn,
		ParameterizedQueries: true,
		Colorful:             false,
	})
	return redactingLogger{Interface: base}
}

// redactingLogger 在 Trace 出口对 SQL 做字面量打码，并强制实现 ParamsFilter。
//
// 为什么要实现 ParamsFilter：gorm 用类型断言 `db.Logger.(ParamsFilter)` 决定是否
// 丢弃绑定参数（callbacks.go）。只嵌入 logger.Interface 的话方法集里没有它，
// 断言失败 → 参数照旧内联。这里显式表态「一律丢弃参数」。
type redactingLogger struct {
	logger.Interface
}

// ParamsFilter 永远丢弃绑定参数（见类型注释）。返回 nil 而非 params，
// 让 Dialector.Explain 无值可替换，SQL 保持 `?`。
func (redactingLogger) ParamsFilter(_ context.Context, sql string, _ ...interface{}) (string, []interface{}) {
	return sql, nil
}

// Trace 包一层：内层拿到 fc() 的结果后先打码再打印。
func (l redactingLogger) Trace(
	ctx context.Context,
	begin time.Time,
	fc func() (string, int64),
	err error,
) {
	l.Interface.Trace(ctx, begin, func() (string, int64) {
		sql, rows := fc()
		return redactSQL(sql), rows
	}, err)
}

// redactSQL 把 SQL 里的**字符串字面量**替换为 `?`，保留语句结构与标识符：
//
//	SELECT * FROM `company_wx_app` WHERE app_secret = 'd54c…'  →  … = '?'
//	WHERE name = "张三"  （SQLite 用双引号表示字符串）          →  WHERE name = "?"
//
// 只处理引号内内容：表名/列名是裸标识符或反引号（MySQL）包起来的，不受影响，
// SQL 形状仍然一眼能看懂。数字字面量（LIMIT 10、id = 1）保留——它们不含敏感信息，
// 打码反而影响排查（分页、行 id 是最常看的东西）。
//
// 转义形态：SQL 标准用双写引号（'' 或 ""），MySQL 还支持反斜杠转义（\'）。
func redactSQL(sql string) string {
	if !strings.ContainsAny(sql, `'"`) {
		return sql
	}
	var b strings.Builder
	b.Grow(len(sql))
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if c != '\'' && c != '"' {
			b.WriteByte(c)
			continue
		}
		// 进入字面量：输出开引号 + ? + 闭引号，中间的原始内容一律丢弃
		b.WriteByte(c)
		b.WriteByte('?')
		for i++; i < len(sql); i++ {
			if sql[i] == '\\' && c == '\'' && i+1 < len(sql) {
				i++ // 跳过 \" 里的被转义字符
				continue
			}
			if sql[i] == c {
				// 双写引号是转义，不是结束
				if i+1 < len(sql) && sql[i+1] == c {
					i++
					continue
				}
				break
			}
		}
		if i < len(sql) {
			b.WriteByte(c) // 补上闭引号
		}
	}
	return b.String()
}

// Open 按 DSN 形态打开数据库连接，第二个返回值是实际使用的驱动名。
//
//	err = db 为 nil 时的错误（调用方直接 log.Fatal 即可）。
func Open(dsn string) (*gorm.DB, string, error) {
	if IsSQLiteDSN(dsn) {
		db, err := gorm.Open(sqlite.Open(NormalizeSQLiteDSN(dsn)), &gorm.Config{
			Logger: gormLogger(),
		})
		if err != nil {
			return nil, DriverSQLite, err
		}
		return db, DriverSQLite, nil
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: gormLogger(),
	})
	if err != nil {
		return nil, DriverMySQL, err
	}
	return db, DriverMySQL, nil
}

// IsSQLiteDSN 判定 DSN 是否指向 SQLite。
//
// 识别三种写法：`sqlite://<路径>`、`sqlite:<路径>`、`file:<URI>`，
// 以及裸路径兜底（`.db` / `.sqlite` / `.sqlite3` 结尾，可带 `?_pragma=` 查询串）。
// 兜底只看扩展名而不「非 MySQL 即 SQLite」：猜错会把生产库读成空库，
// 宁可让老实的 MySQL DSN 继续走 MySQL。
func IsSQLiteDSN(dsn string) bool {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range []string{"sqlite://", "sqlite:", "file:"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	path := lower
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// NormalizeSQLiteDSN 把用户写的 DSN 规整成驱动可直接消费的形式，并补上生产必需的 pragma。
//
// 三项默认值（用户显式写了同名 pragma 时以其为准，不被覆盖）：
//   - journal_mode(WAL)：读写不再互斥。默认的 rollback journal 下，一次写会挡住所有读，
//     名片页与后台管理并发时表现为大面积卡顿。
//   - busy_timeout(5000)：拿不到写锁时最多等 5 秒再报 `database is locked`。
//     SQLite 是单写者，没有这个等待时间，并发写会直接失败。
//   - foreign_keys(1)：SQLite 出于兼容默认**关闭**外键约束，不打开则约束形同虚设。
//
// 已实测：`github.com/glebarez/sqlite`（modernc，SQLite 3.41.2）支持 `_pragma=` 查询参数。
func NormalizeSQLiteDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "sqlite://"):
		s = s[len("sqlite://"):]
	case strings.HasPrefix(lower, "sqlite:"):
		s = s[len("sqlite:"):]
	}
	if s == "" {
		return s
	}
	sep := "?"
	if strings.Contains(s, "?") {
		sep = "&"
	}
	for _, p := range []struct{ key, val string }{
		{"journal_mode", "WAL"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
	} {
		if strings.Contains(s, "_pragma="+p.key) {
			continue
		}
		s += sep + "_pragma=" + p.key + "(" + p.val + ")"
		sep = "&"
	}
	return s
}
