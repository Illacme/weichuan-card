package migrate

// 基线保真守门：单一基线 0001_init.sql 必须与 history/ 里归档的全部历史
// 增量脚本按文件名顺序拼接后逐条语句一致。
//
// 背景：商用按全新部署交付，顶层只留一个基线文件；62 个历史增量移入
// history/ 归档（执行器与清单只扫顶层 *.sql，归档不参与部署）。
// 拼接保真意味着「跑一条基线」与「从头逐个跑 62 条历史」终态逐字相同——
// 不需要手工推导最终表结构，也不会漏掉 0062 这类纯数据修正迁移。
//
// 本测试盯住两件事：
//   1. 有人改了基线却没同步 history（或反之）——语句序列一比对就红；
//   2. history 目录或基线文件被整个删掉——保真关系无从验证，也算红。
// 未来新增迁移写顶层增量文件（0063_xxx.sql），不要改基线，也不要动归档。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// splitStatements 与 applyFile 同语义：按分号切分，丢空段与纯注释段。
// 必须和 apply.go 保持一致，否则「保真」验证的就不是执行器真正看到的东西。
func splitStatements(raw string) []string {
	var out []string
	for _, stmt := range strings.Split(raw, ";") {
		s := strings.TrimSpace(stmt)
		if s == "" || isCommentOnly(s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestBaselineMatchesHistory(t *testing.T) {
	dir, err := migrationsDir()
	require.NoError(t, err)

	historyDir := filepath.Join(dir, "history")
	entries, err := os.ReadDir(historyDir)
	require.NoError(t, err, "history 归档目录必须存在；基线是从它生成的")

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	require.NotEmpty(t, names, "history 下没有归档的 .sql，保真关系无从验证")

	sort.Strings(names)
	var expected []string
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(historyDir, name))
		require.NoError(t, err)
		expected = append(expected, splitStatements(string(raw))...)
	}
	require.NotEmpty(t, expected, "归档历史里没有可执行语句")

	baseline, err := os.ReadFile(filepath.Join(dir, "0001_init.sql"))
	require.NoError(t, err, "顶层基线文件 0001_init.sql 必须存在")
	actual := splitStatements(string(baseline))

	require.Equal(t, expected, actual,
		"基线语句序列与 history 归档按序拼接不一致：改基线必须同步 history（或反过来）")
}
