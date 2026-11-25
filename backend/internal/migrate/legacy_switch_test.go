package migrate

// 老库升级自动切换到 history/ 的测试。
//
// 背景：产品初始版把顶层基线换成纯净 0001_init.sql，62 个历史增量归档到
// history/。已经按 0001..0062 记过账的老库升级到本版本时，若拿顶层纯净基线
// 直接跑，守门 2 会因「记账里有、目录里没有」拒绝启动。maybeLegacyHistoryDir
// 让这种库自动切到 history/ 继续按增量升级，无需人工设环境变量。

import (
	"fmt"
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

// 线上 2026-11-25 事故形态：增量部署时旧增量文件没被删，顶层残留 0002..0053，
// 同时又传了完整的 history/。此时「记账版本在顶层全都在」会让旧判据误判为
// 「不需要切换」，结果 0054+ 的增量没跑（storage_profiles 表缺失，启动失败）。
// 正确判据：老库（有记账）+ history 覆盖全部记账版本 → 一律用 history 升级。
func TestLegacyDBWithStaleTopLevelFilesSwitchesToHistory(t *testing.T) {
	t.Setenv(envMigrationsDir, "")
	db := newTestDB(t)
	dir := t.TempDir()

	// 顶层：旧增量残留（0001 被本版本纯净基线同名覆盖，0002..0003 是旧文件）
	writeDir(t, dir, map[string]string{
		"0001_init.sql":  "CREATE TABLE top1 (id INTEGER PRIMARY KEY);",
		"0002_apply.sql": "CREATE TABLE top2 (id INTEGER PRIMARY KEY);",
		"0003_extra.sql": "CREATE TABLE top3 (id INTEGER PRIMARY KEY);",
	})
	// history：完整增量（0001..0004），含老库已记账的全部版本
	writeDir(t, filepath.Join(dir, "history"), map[string]string{
		"0001_init.sql":  "CREATE TABLE h1 (id INTEGER PRIMARY KEY);",
		"0002_apply.sql": "CREATE TABLE h2 (id INTEGER PRIMARY KEY);",
		"0003_extra.sql": "CREATE TABLE h3 (id INTEGER PRIMARY KEY);",
		"0004_new.sql":   "CREATE TABLE h4 (id INTEGER PRIMARY KEY);",
	})

	_, err := appliedVersions(db)
	require.NoError(t, err)
	for _, v := range []string{"0001_init.sql", "0002_apply.sql", "0003_extra.sql"} {
		require.NoError(t, recordVersion(db, v))
	}

	applied, err := applyFromDir(db, dir)
	require.NoError(t, err)
	require.Equal(t, []string{"0004_new.sql"}, applied,
		"顶层残留旧增量不应阻止切换到 history，否则新表建不出来")
	require.True(t, db.Migrator().HasTable("h4"))
}

// 最接近线上事故：记账到 0053，顶层混着纯净 0001 + 旧增量 0002..0053，
// history 完整且含 0054 之后的新迁移。修复后应切 history 并补跑这些新迁移。
func TestLegacyDBAt0053WithStaleTopLevelSwitchesToHistory(t *testing.T) {
	t.Setenv(envMigrationsDir, "")
	db := newTestDB(t)
	dir := t.TempDir()

	top := map[string]string{"0001_init.sql": "SELECT 1;"}
	hist := map[string]string{"0001_init.sql": "SELECT 1;"}
	for i := 2; i <= 53; i++ {
		name := fmt.Sprintf("%04d_step.sql", i)
		hist[name] = "SELECT 1;"
		top[name] = "SELECT 1;" // 顶层残留旧增量
	}
	// 0054..0062 只在 history 里，其中 0054 建 storage_profiles
	hist["0054_storage.sql"] = "CREATE TABLE storage_profiles (id INTEGER PRIMARY KEY);"
	writeDir(t, dir, top)
	writeDir(t, filepath.Join(dir, "history"), hist)

	_, err := appliedVersions(db)
	require.NoError(t, err)
	for _, v := range []string{"0001_init.sql", "0053_step.sql"} {
		require.NoError(t, recordVersion(db, v))
	}

	applied, err := applyFromDir(db, dir)
	require.NoError(t, err)
	require.Contains(t, applied, "0054_storage.sql")
	require.True(t, db.Migrator().HasTable("storage_profiles"),
		"history 里的 storage_profiles 必须建出来，否则启动仍 FATAL")
}

// 线上事故的另一种形态：顶层残留旧增量、history 没传。旧代码静默放行，
// 结果 0054+ 没跑；新守门必须明确报错，而不是等运行期
// `Table 'weichuan.storage_profiles' doesn't exist` 才发现。
func TestLegacyDBStaleTopLevelWithoutHistoryFailsFast(t *testing.T) {
	t.Setenv(envMigrationsDir, "")
	db := newTestDB(t)
	dir := t.TempDir()
	writeDir(t, dir, map[string]string{
		"0001_init.sql":  "SELECT 1;",
		"0002_apply.sql": "SELECT 1;",
		"0003_extra.sql": "SELECT 1;",
	})
	_, err := appliedVersions(db)
	require.NoError(t, err)
	for _, v := range []string{"0001_init.sql", "0002_apply.sql", "0003_extra.sql"} {
		require.NoError(t, recordVersion(db, v))
	}

	_, err = applyFromDir(db, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "migrations/history/ 一起部署")
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
