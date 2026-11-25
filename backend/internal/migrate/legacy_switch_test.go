package migrate

// 老库升级自动切换到 history/ 的测试。
//
// 背景：产品初始版把顶层基线换成纯净 0001_init.sql，62 个历史增量归档到
// history/。已经按 0001..0062 记过账的老库升级到本版本时，若拿顶层纯净基线
// 直接跑，守门 2 会因「记账里有、目录里没有」拒绝启动。maybeLegacyHistoryDir
// 让这种库自动切到 history/ 继续按增量升级，无需人工设环境变量。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeDir 在给定目录写文件（自动建父目录）。
func writeDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, sql := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(sql), 0o600))
	}
}

// 老库：记账里有 0001..0003（老版本增量），顶层只有纯净 0001_init.sql，
// history 里能补齐全部老版本。应自动切到 history，且只应用新增的 0004。
func TestLegacyDBAutoSwitchesToHistory(t *testing.T) {
	t.Setenv(envMigrationsDir, "") // 确保不显式指定，允许自动切换
	db := newTestDB(t)
	dir := t.TempDir()

	// 顶层：纯净基线（名字与老库记账里的 0001 相同）
	writeDir(t, dir, map[string]string{
		"0001_init.sql": "CREATE TABLE pure (id INTEGER PRIMARY KEY);",
	})
	// history：老版本的 0001..0003 + 本版本新增 0004
	writeDir(t, filepath.Join(dir, "history"), map[string]string{
		"0001_init.sql":  "CREATE TABLE old1 (id INTEGER PRIMARY KEY);",
		"0002_apply.sql": "CREATE TABLE old2 (id INTEGER PRIMARY KEY);",
		"0003_extra.sql": "CREATE TABLE old3 (id INTEGER PRIMARY KEY);",
		"0004_new.sql":   "CREATE TABLE new4 (id INTEGER PRIMARY KEY);",
	})

	// 预置记账：模拟老库已应用 0001..0003（但没跑过 0004）。
	// 先建记账表（appliedVersions 会 CREATE TABLE IF NOT EXISTS），再写记录。
	_, err := appliedVersions(db)
	require.NoError(t, err)
	for _, v := range []string{"0001_init.sql", "0002_apply.sql", "0003_extra.sql"} {
		require.NoError(t, recordVersion(db, v))
	}

	applied, err := applyFromDir(db, dir)
	require.NoError(t, err, "老库应自动切到 history 而非被守门 2 拒绝")
	require.Equal(t, []string{"0004_new.sql"}, applied,
		"切换后应只应用 history 里未记账的 0004（已记账的 0001..0003 跳过）")
	require.True(t, db.Migrator().HasTable("new4"))
	require.False(t, db.Migrator().HasTable("pure"), "不应执行顶层纯净基线")
}

// 全新库：记账为空，顶层纯净基线正常应用，不受 history 影响。
func TestFreshDBUsesTopLevelBaseline(t *testing.T) {
	t.Setenv(envMigrationsDir, "")
	db := newTestDB(t)
	dir := t.TempDir()
	writeDir(t, dir, map[string]string{
		"0001_init.sql": "CREATE TABLE pure (id INTEGER PRIMARY KEY);",
	})
	writeDir(t, filepath.Join(dir, "history"), map[string]string{
		"0001_init.sql": "CREATE TABLE old1 (id INTEGER PRIMARY KEY);",
	})

	applied, err := applyFromDir(db, dir)
	require.NoError(t, err)
	require.Equal(t, []string{"0001_init.sql"}, applied)
	require.True(t, db.Migrator().HasTable("pure"), "全新库应建顶层纯净基线")
	require.False(t, db.Migrator().HasTable("old1"), "全新库不应碰 history")
}

// history 补不齐时保持 fail-closed：记账里有 0005，但顶层与 history 都没有它，
// 不能瞎切，应被守门 2 拒绝（这是「库比我们认识的版本还超前」的正确处理）。
func TestLegacyIncompleteHistoryFailsClosed(t *testing.T) {
	t.Setenv(envMigrationsDir, "")
	db := newTestDB(t)
	dir := t.TempDir()
	writeDir(t, dir, map[string]string{
		"0001_init.sql": "CREATE TABLE pure (id INTEGER PRIMARY KEY);",
	})
	writeDir(t, filepath.Join(dir, "history"), map[string]string{
		"0001_init.sql": "CREATE TABLE old1 (id INTEGER PRIMARY KEY);",
	})
	_, err := appliedVersions(db)
	require.NoError(t, err)
	require.NoError(t, recordVersion(db, "0005_future.sql"))

	_, err = applyFromDir(db, dir)
	require.Error(t, err, "history 补不齐已记账版本时必须 fail-closed")
	require.Contains(t, err.Error(), "部署包不完整")
}

// 纯函数判据：显式指定 env 时不切换（尊重运维/测试指定，不被自动逻辑覆盖）。
func TestExplicitDirDisablesAutoSwitch(t *testing.T) {
	explicit := t.TempDir()
	t.Setenv(envMigrationsDir, explicit)
	dir := t.TempDir()
	files := []string{"0001_init.sql"}
	done := map[string]bool{"0005_future.sql": true}

	gotDir, gotFiles, switched := maybeLegacyHistoryDir(dir, files, done)
	require.False(t, switched)
	require.Equal(t, dir, gotDir)
	require.Equal(t, files, gotFiles)
}
