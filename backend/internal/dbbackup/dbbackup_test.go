package dbbackup

import (
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
)

// 造一个真实的 SQLite 库（AutoMigrate 全部模型 + 塞几行数据），
// 用来验证「快照产物能打开、表齐、行数一致」。
func seedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, driver, err := dbopen.Open(path)
	require.NoError(t, err)
	require.Equal(t, dbopen.DriverSQLite, driver)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "朗汇", ShortName: "朗汇", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "第二家", ShortName: "二", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Employee{CompanyID: 1, Name: "张三", Phone: "13800000001", Status: "enabled"}).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return path
}

func TestSnapshotProducesUsableCopy(t *testing.T) {
	src := seedDB(t)
	out := filepath.Join(t.TempDir(), "out.db")

	rep, err := Snapshot(src, out)
	require.NoError(t, err)
	require.Equal(t, "ok", rep.Integrity)
	require.Greater(t, rep.Tables, 10, "表数量明显偏少说明快照不完整")
	require.Greater(t, rep.Bytes, int64(0))
	require.EqualValues(t, 2, rep.Rows["company"])
	require.EqualValues(t, 1, rep.Rows["employees"])

	// 产物必须能独立打开（不依赖源库 / 不依赖 -wal）
	again, err := Inspect(out)
	require.NoError(t, err)
	require.Equal(t, rep.Tables, again.Tables)
	require.Equal(t, rep.Rows["company"], again.Rows["company"])
}

// 源库里的行在快照之后继续变化，不能影响已生成的快照——
// 这是「快照」而不是「软链」的基本要求。
func TestSnapshotIsIsolatedFromLaterWrites(t *testing.T) {
	src := seedDB(t)
	out := filepath.Join(t.TempDir(), "out.db")
	_, err := Snapshot(src, out)
	require.NoError(t, err)

	db, _, err := dbopen.Open(src)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "第三家", ShortName: "三", Status: "enabled"}).Error)

	after, err := Inspect(out)
	require.NoError(t, err)
	require.EqualValues(t, 2, after.Rows["company"], "快照不该被后续写入影响")
}

func TestSnapshotRejectsMySQLDSN(t *testing.T) {
	_, err := Snapshot("root:pw@tcp(127.0.0.1:3306)/weichuan", filepath.Join(t.TempDir(), "x.db"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "MySQL")
}

func TestSnapshotRejectsEmptyOut(t *testing.T) {
	_, err := Snapshot(seedDB(t), "")
	require.Error(t, err)
}

// 覆盖已存在的输出文件：备份目录里的一次性产物，留着旧的只会让
// 「这份备份是什么时候的」变模糊。
func TestSnapshotOverwritesExistingOutput(t *testing.T) {
	src := seedDB(t)
	out := filepath.Join(t.TempDir(), "out.db")
	require.NoError(t, os.WriteFile(out, []byte("stale"), 0o644))

	rep, err := Snapshot(src, out)
	require.NoError(t, err)
	require.Equal(t, "ok", rep.Integrity)
	require.Greater(t, rep.Bytes, int64(5))
}

func TestInspectMissingFile(t *testing.T) {
	_, err := Inspect(filepath.Join(t.TempDir(), "nope.db"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "不存在")
}

// 关键回归：体检一个不存在的路径绝不能把文件「创建」出来。
// gorm 默认打开不存在的 SQLite 路径会建一个空库，那样体检会返回
// 「0 张表、integrity ok」——看起来一切正常，实际什么都没检查。
func TestInspectDoesNotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.db")
	_, err := Inspect(path)
	require.Error(t, err)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "体检不该创建文件")
}

func TestInspectEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	_, err := Inspect(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "空文件")
}

func TestInspectNonSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.db")
	require.NoError(t, os.WriteFile(path, []byte("this is definitely not a database, just text padding to be non-empty"), 0o644))
	_, err := Inspect(path)
	require.Error(t, err)
}

func TestInspectDirectory(t *testing.T) {
	_, err := Inspect(t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "目录")
}

func TestDiffTables(t *testing.T) {
	base := Report{Rows: map[string]int64{"company": 1, "employees": 2}}
	full := Report{Rows: map[string]int64{"company": 1, "employees": 2, "audit_log": 0}}
	missing := Report{Rows: map[string]int64{"company": 1}}

	require.Empty(t, DiffTables(base, full), "对比库表更多不算缺失")
	require.Equal(t, []string{"employees"}, DiffTables(base, missing))
}

func TestSortedRowNamesIsStable(t *testing.T) {
	r := Report{Rows: map[string]int64{"b": 1, "a": 2, "c": 3}}
	require.Equal(t, []string{"a", "b", "c"}, r.SortedRowNames())
}
