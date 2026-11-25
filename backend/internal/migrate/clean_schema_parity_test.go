package migrate_test

// 纯净 schema 对账：history 增量重放后的库结构，必须与「只跑 0001_init.sql
// 纯净基线」的库逐列逐索引一致。
//
// 为什么需要真实 MySQL：
// gen_migrations_clean_schema.py 在内存里重放 CREATE/ALTER 推演终态，
// 解析器再周密也可能漏掉 MySQL 方言的细微语义（AUTO_INCREMENT 归属、
// UNIQUE KEY 归一、列顺序等）。静态的语法比对证明不了结构等价，
// 只有让 MySQL 自己建两遍库、再查 information_schema 对比才作数。
//
// 只在设置 TEST_MYSQL_DSN 时运行（CI backend-mysql job）。两个临时库
// 用固定后缀，测试开始先 DROP IF EXISTS，结束清理；不碰被测库本体。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"weichuan/internal/migrate"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type columnInfo struct {
	Name      string
	Type      string
	Nullable  string
	Default   any
	Extra     string
	ColumnKey string
}

type indexInfo struct {
	Name      string
	NonUnique int64
	Columns   string
}

func schemaOf(t *testing.T, db *gorm.DB) map[string]any {
	t.Helper()
	var tables []string
	require.NoError(t, db.Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'",
	).Scan(&tables).Error)
	sort.Strings(tables)

	out := map[string]any{}
	for _, tbl := range tables {
		var cols []columnInfo
		require.NoError(t, db.Raw(
			"SELECT column_name AS name, column_type AS type, is_nullable AS nullable, "+
				"column_default AS `default`, extra AS extra, column_key AS column_key "+
				"FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position",
			tbl,
		).Scan(&cols).Error)
		var idxs []indexInfo
		require.NoError(t, db.Raw(
			"SELECT index_name AS name, non_unique AS non_unique, "+
				"GROUP_CONCAT(column_name ORDER BY seq_in_index) AS columns "+
				"FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? "+
				"GROUP BY index_name, non_unique ORDER BY index_name",
			tbl,
		).Scan(&idxs).Error)
		out[tbl] = struct {
			Cols []columnInfo
			Idx  []indexInfo
		}{Cols: cols, Idx: idxs}
	}
	return out
}

func dsnFor(base, dbName string) string {
	// 形如 root:root@tcp(127.0.0.1:3306)/weichuan_test?charset=...
	i := strings.Index(base, "/weichuan_test?")
	if i < 0 {
		return ""
	}
	return base[:i] + "/" + dbName + base[i+len("/weichuan_test"):]
}

func applySQLFile(t *testing.T, db *gorm.DB, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, stmt := range strings.Split(string(raw), ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		// 先剥注释再判空（与 apply.go 的 isCommentOnly 同语义）
		hasCode := false
		for _, ln := range strings.Split(s, "\n") {
			ln = strings.TrimSpace(ln)
			if ln != "" && !strings.HasPrefix(ln, "--") {
				hasCode = true
			}
		}
		if !hasCode {
			continue
		}
		require.NoError(t, db.Exec(s).Error, "应用 %s 失败：%s", filepath.Base(path), s[:min(len(s), 100)])
	}
}

func TestCleanSchemaMatchesHistoryReplay(t *testing.T) {
	base := os.Getenv("TEST_MYSQL_DSN")
	if base == "" {
		t.Skip("set TEST_MYSQL_DSN to run MySQL clean-schema parity")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	migDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	const dbHist = "weichuan_schema_hist"
	const dbClean = "weichuan_schema_clean"
	dsnH, dsnc := dsnFor(base, dbHist), dsnFor(base, dbClean)
	require.NotEmpty(t, dsnH, "TEST_MYSQL_DSN 格式不符，无法推导临时库 DSN：%s", base)

	// 用无库 DSN 连 server 建/删临时库。
	serverDSN := base[:strings.LastIndex(base, "/")] + "/"
	server, err := gorm.Open(mysql.Open(serverDSN), &gorm.Config{})
	require.NoError(t, err)
	for _, name := range []string{dbHist, dbClean} {
		require.NoError(t, server.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)).Error)
		require.NoError(t, server.Exec(fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4", name)).Error)
	}
	t.Cleanup(func() {
		for _, name := range []string{dbHist, dbClean} {
			_ = server.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)).Error
		}
	})

	dbH, err := gorm.Open(mysql.Open(dsnH), &gorm.Config{})
	require.NoError(t, err)
	dbc, err := gorm.Open(mysql.Open(dsnc), &gorm.Config{})
	require.NoError(t, err)

	// A：history/ 全量按序重放。migrate 的 migrationsDir 优先读
	// WEICHUAN_MIGRATIONS_DIR，把它指到 history/ 即可走与生产完全相同的
	// 应用路径（含幂等容错与记账）；t.Setenv 在用例结束自动恢复。
	histDir := filepath.Join(migDir, "history")
	t.Setenv("WEICHUAN_MIGRATIONS_DIR", histDir)
	_, err = migrate.ApplyWithReport(dbH)
	require.NoError(t, err, "history 增量重放失败")
	// B：纯净基线（顶层 0001_init.sql，按分号切分直接执行）
	applySQLFile(t, dbc, filepath.Join(migDir, "0001_init.sql"))

	sh, sc := schemaOf(t, dbH), schemaOf(t, dbc)
	require.Equal(t, sh, sc,
		"纯净 0001_init.sql 的库结构与 history 重放结果不一致：\n"+
			"history: %v\nclean: %v", sortedKeys(sh), sortedKeys(sc))
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
