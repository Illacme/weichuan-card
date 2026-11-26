package migrate

// 纯净基线守门：顶层 0001_init.sql 必须是「纯净终态 schema」——
// 只有 CREATE TABLE，不含任何 ALTER / 数据 UPDATE / DROP / INSERT。
//
// 背景：商用把当前版本固化为产品初始版，全新安装走纯净 DDL，升级安装走
// history/ 的 62 个增量。基线与 history 的结构等价由真实 MySQL 对账测试
// （clean_schema_parity_test.go，CI backend-mysql job）保证——那是最强验证。
// 本测试是零依赖的静态防线，挡住「有人把历史拼接版、或夹带数据搬迁的 SQL
// 重新写回基线」这类回退：拼接版能在空库跑通，但会让交付物重新背上历史包袱。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBaselineIsCleanSchema(t *testing.T) {
	dir, err := migrationsDir()
	require.NoError(t, err)

	baseline, err := os.ReadFile(filepath.Join(dir, "0001_init.sql"))
	require.NoError(t, err, "顶层基线文件 0001_init.sql 必须存在")
	// 剥掉行注释再判：注释里出现 "setting.update" 之类字样说明不了脚本行为。
	var codeLines []string
	for _, ln := range strings.Split(string(baseline), "\n") {
		s := strings.TrimSpace(ln)
		if s != "" && !strings.HasPrefix(s, "--") {
			codeLines = append(codeLines, s)
		}
	}
	code := strings.ToUpper(strings.Join(codeLines, "\n"))

	// 出现下列任一关键字就说明历史 DDL/DML 被混进了全新安装脚本。
	for _, banned := range []string{
		"ALTER TABLE", "DROP TABLE", "RENAME TABLE",
		"INSERT INTO", "UPDATE ", "DELETE FROM", "REPLACE INTO",
		"CREATE INDEX", "CREATE UNIQUE INDEX",
	} {
		require.NotContains(t, code, banned,
			"纯净基线里不该出现 %q：全新安装只要最终态建表，历史演进归 history/", banned)
	}

	// 正向：必须是若干 CREATE TABLE IF NOT EXISTS，且数量与 history 推演一致。
	createRe := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+[a-z_][a-z0-9_]*`)
	tables := createRe.FindAllString(string(baseline), -1)
	require.Len(t, tables, 28, "纯净基线应恰好 28 张表（history 建 29 张，0055 删 1 张）")

	// 归档目录必须仍在：它既是升级路径的真值源，也是老库逃生门。
	entries, err := os.ReadDir(filepath.Join(dir, "history"))
	require.NoError(t, err, "history 归档目录必须存在（升级路径与老库逃生门依赖它）")
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			n++
		}
	}
	require.Equal(t, 64, n, "history 应保留全部 64 个历史增量")
}
