package migrate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/dbopen"
)

// 记账逻辑用 SQLite + 临时 migrations 目录覆盖：把 WEICHUAN_MIGRATIONS_DIR 指向
// 一个自制目录，就不必依赖真实 MySQL 也能验证「只应用未记录的 / 失败不记录 /
// 幂等重放」这三条——生产（MySQL）走的是同一段代码，锁与方言差异只在
// isMySQL 两个分支里，由 CI 的 MySQL parity 用例（TestMySQLMigrations）兜住。

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open(filepath.Join(t.TempDir(), "migrate.db"))
	require.NoError(t, err)
	return db
}

// writeMigrations 在临时目录里写入若干迁移文件，并把环境变量指过去。
func writeMigrations(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, sql := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600))
	}
	t.Setenv("WEICHUAN_MIGRATIONS_DIR", dir)
	return dir
}

func recordedVersions(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var vs []string
	require.NoError(t, db.Raw("SELECT version FROM "+migrationsTable+" ORDER BY version").Scan(&vs).Error)
	return vs
}

// 记账的核心承诺：已应用过的迁移不重放；新增文件只应用增量。
func TestApplyRecordsVersionsAndAppliesOnlyPending(t *testing.T) {
	db := newTestDB(t)
	dir := writeMigrations(t, map[string]string{
		"0001_a.sql": "CREATE TABLE t1 (id INTEGER PRIMARY KEY);",
		"0002_b.sql": "CREATE TABLE t2 (id INTEGER PRIMARY KEY);",
	})

	applied, err := ApplyWithReport(db)
	require.NoError(t, err)
	require.Equal(t, []string{"0001_a.sql", "0002_b.sql"}, applied, "首次应应用全部且按文件顺序")
	require.Equal(t, []string{"0001_a.sql", "0002_b.sql"}, recordedVersions(t, db))
	require.True(t, db.Migrator().HasTable("t1"))
	require.True(t, db.Migrator().HasTable("t2"))

	// 第二次：无待应用迁移，applied 为空且不再执行（若重放，CREATE TABLE 会报重复）
	applied2, err := ApplyWithReport(db)
	require.NoError(t, err)
	require.Empty(t, applied2, "已记录的迁移不应被重放")

	// 增量：只应用新文件
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0003_c.sql"),
		[]byte("CREATE TABLE t3 (id INTEGER PRIMARY KEY);"), 0o600))
	applied3, err := ApplyWithReport(db)
	require.NoError(t, err)
	require.Equal(t, []string{"0003_c.sql"}, applied3, "只应应用新增的那一个")
	require.True(t, db.Migrator().HasTable("t3"))
}

// 迁移失败时绝不能把版本记成已应用：否则下次启动会跳过它，留下一个
// 「表结构对不上、却没人知道」的静默缺口（与 P0「不报错的失败更贵」同源）。
func TestApplyFailureIsNotRecorded(t *testing.T) {
	db := newTestDB(t)
	dir := writeMigrations(t, map[string]string{
		"0001_ok.sql":    "CREATE TABLE t1 (id INTEGER PRIMARY KEY);",
		"0002_bad.sql":   "CREATE TABLE t2 (id INTEGER PRIMARY KEY);\nTHIS IS NOT SQL;",
		"0003_after.sql": "CREATE TABLE t3 (id INTEGER PRIMARY KEY);",
	})

	_, err := ApplyWithReport(db)
	require.Error(t, err, "非法 SQL 必须报错")
	require.Contains(t, err.Error(), "0002_bad.sql")
	require.Equal(t, []string{"0001_ok.sql"}, recordedVersions(t, db),
		"失败的迁移不得记账，且后续文件应中止（避免跳过顺序依赖）")
	require.False(t, db.Migrator().HasTable("t3"), "失败后不应继续应用后面的迁移")

	// 修好后重跑：从失败处继续，而不是当作已应用跳过去
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0002_bad.sql"),
		[]byte("CREATE TABLE t2 (id INTEGER PRIMARY KEY);"), 0o600))
	applied, err := ApplyWithReport(db)
	require.NoError(t, err)
	require.Equal(t, []string{"0002_bad.sql", "0003_after.sql"}, applied)
	require.True(t, db.Migrator().HasTable("t2"))
	require.True(t, db.Migrator().HasTable("t3"))
}

// 存量库接管：表已存在（如 compose initdb.d 已建好）时重放会报重复，
// 这类错误必须忽略并补记版本——否则每次启动都失败，升级就永远推不动。
func TestApplyToleratesPreexistingSchema(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Exec("CREATE TABLE t1 (id INTEGER PRIMARY KEY)").Error)

	writeMigrations(t, map[string]string{
		"0001_a.sql": "CREATE TABLE t1 (id INTEGER PRIMARY KEY);", // 无 IF NOT EXISTS
	})

	applied, err := ApplyWithReport(db)
	require.NoError(t, err, "已存在的对象应被忽略（Duplicate / already exists）")
	require.Equal(t, []string{"0001_a.sql"}, applied)
	require.Equal(t, []string{"0001_a.sql"}, recordedVersions(t, db))
}

// 记账表的 applied_at 应写入实际时间（用注入的 nowFunc 断言，不依赖真实时钟）。
func TestRecordVersionStoresAppliedAt(t *testing.T) {
	db := newTestDB(t)
	writeMigrations(t, map[string]string{
		"0001_a.sql": "CREATE TABLE t1 (id INTEGER PRIMARY KEY);",
	})
	want := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	orig := nowFunc
	nowFunc = func() time.Time { return want }
	t.Cleanup(func() { nowFunc = orig })

	_, err := ApplyWithReport(db)
	require.NoError(t, err)

	var got time.Time
	require.NoError(t, db.Raw(
		"SELECT applied_at FROM "+migrationsTable+" WHERE version = ?", "0001_a.sql").Scan(&got).Error)
	require.WithinDuration(t, want, got, time.Second)
}

// Apply 是无返回值的包装：行为与 ApplyWithReport 一致（现有调用方不必改）。
func TestApplyWrapsReport(t *testing.T) {
	db := newTestDB(t)
	writeMigrations(t, map[string]string{
		"0001_a.sql": "CREATE TABLE t1 (id INTEGER PRIMARY KEY);",
	})
	require.NoError(t, Apply(db))
	require.NoError(t, Apply(db), "重复 Apply 必须幂等")
}

// 部署包完整性守门（2026-11-03 线上事故）：记账表里有、目录里没有 → 报错。
//
// 事故形态：升级只替换了二进制、没同步 migrations/ 目录。程序对着旧目录
// （全是已记账的文件）得出「数据库已是最新」，运行期才撞 Unknown column，
// 且启动日志没有任何异常——这是「静默失败」的典型。
//
// 两层语义都要钉住：
//  1. 必须报错，且错误里点名缺失的文件与处置办法（否则运维仍然不知道要同步目录）；
//  2. 报错发生在应用任何文件之前——目录不可信时不能拿旧包去改新库。
func TestApplyRejectsIncompleteDeploymentPackage(t *testing.T) {
	db := newTestDB(t)
	dir := writeMigrations(t, map[string]string{
		"0001_a.sql": "CREATE TABLE t1 (id INTEGER PRIMARY KEY);",
	})
	_, err := ApplyWithReport(db)
	require.NoError(t, err)

	// 模拟「新二进制带着新目录来了，但旧目录里的文件丢了」：
	// 换成本版本目录（只有新文件），记账表里仍是 0001。
	require.NoError(t, os.Remove(filepath.Join(dir, "0001_a.sql")))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "0002_b.sql"), []byte("CREATE TABLE t2 (id INTEGER PRIMARY KEY);"), 0o600))

	_, err = ApplyWithReport(db)
	require.Error(t, err, "记账表与目录对不上时必须报错，不能静默「已是最新」")
	require.Contains(t, err.Error(), "部署包不完整")
	require.Contains(t, err.Error(), "0001_a.sql", "错误里要点名缺失的文件")
	require.Contains(t, err.Error(), "migrations/ 目录", "错误里要给出处置办法")

	// 守门先于执行：可疑时不能顺手把「看起来待执行」的 0002 应用到库里
	require.False(t, db.Migrator().HasTable("t2"), "守门失败时不应继续应用任何迁移")
	require.Equal(t, []string{"0001_a.sql"}, recordedVersions(t, db), "不得新增记账")

	// 目录补齐后恢复正常：0002 得到应用，0001 不重放
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "0001_a.sql"), []byte("CREATE TABLE t1 (id INTEGER PRIMARY KEY);"), 0o600))
	applied, err := ApplyWithReport(db)
	require.NoError(t, err)
	require.Equal(t, []string{"0002_b.sql"}, applied)
}

// missingVersions 是纯函数：目录多出文件不算缺失（那是正常的待应用增量）。
func TestMissingVersions(t *testing.T) {
	done := map[string]bool{"0001_a.sql": true, "0002_b.sql": true}
	require.Empty(t, missingVersions(done, []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"}),
		"目录多出文件是正常的待应用增量，不算缺失")
	require.Equal(t, []string{"0002_b.sql"}, missingVersions(done, []string{"0001_a.sql"}))
	require.Empty(t, missingVersions(map[string]bool{}, []string{"0001_a.sql"}),
		"空记账（首次接管存量库）不算缺失")
}

// TestApplyRejectsDirMissingManifestFile 守门 1 的正面验证：目录缺清单里的文件时
// 必须在**执行任何 SQL 之前**返回错误。
//
// 不用伪造目录，直接把清单临时加一个幽灵文件名——这正是线上形态的反向模拟
// （二进制带着 0035 的清单，磁盘目录里没有它）。注意本用例不设
// WEICHUAN_MIGRATIONS_DIR，因此走真实仓库目录；守门在 run 之前返回，
// 不会把 MySQL 方言迁移应用到 SQLite 上（否则本用例会在别处炸）。
func TestApplyRejectsDirMissingManifestFile(t *testing.T) {
	orig := expectedMigrations
	expectedMigrations = append(append([]string{}, orig...), "9999_ghost.sql")
	t.Cleanup(func() { expectedMigrations = orig })

	_, err := ApplyWithReport(newTestDB(t))
	require.Error(t, err, "目录缺本版本应有的文件时必须报错，不能静默「已是最新」")
	require.Contains(t, err.Error(), "部署包不完整")
	require.Contains(t, err.Error(), "9999_ghost.sql", "错误里要点名缺失的文件")
	require.Contains(t, err.Error(), "migrations/ 目录", "错误里要给出处置办法")
}
