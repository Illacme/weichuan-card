package refmigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---- 测试夹具 --------------------------------------------------------------
//
// seedLegacy 造最小但覆盖关键形态的一批历史引用：两个单值列、一个数组列
// （含空槽）、以及一条被两个字段共用的源对象（去重用）。
// 合计 5 条待搬引用 → 4 个对象（a / b / c / shared）。
func seedLegacy(t *testing.T, db *gorm.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (1, 1, 'a.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (2, 1, 'b.jpg')`)
	mustExec(t, db, `INSERT INTO project_cases (id, company_id, cover, album) VALUES (1, 1, '', '["c.jpg",""]')`)
	mustExec(t, db, `INSERT INTO company_infos (id, company_id, logo) VALUES (1, 1, 'shared.jpg')`)
	mustExec(t, db, `INSERT INTO biz_services (id, company_id, icon) VALUES (1, 1, 'shared.jpg')`)
}

func mustPlanFile(t *testing.T, db *gorm.DB) *PlanFile {
	t.Helper()
	p, err := Scan(db, Options{})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "plan.json")
	b, err := json.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
	pf, err := ReadPlan(path)
	require.NoError(t, err)
	return pf
}

// mutatePlan 解析并改一份计划再写回磁盘，返回新的 PlanFile（hash 随之变化）。
func mutatePlan(t *testing.T, pf *PlanFile, fn func(*Plan)) *PlanFile {
	t.Helper()
	p, err := ReadPlan(pf.Path)
	require.NoError(t, err)
	fn(p.Plan)
	b, err := json.Marshal(p.Plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pf.Path, b, 0o600))
	out, err := ReadPlan(pf.Path)
	require.NoError(t, err)
	return out
}

func makeStores(t *testing.T, keys ...string) (*Stores, string, string) {
	t.Helper()
	root := t.TempDir()
	srcDir := filepath.Join(root, "platform")
	dstDir := filepath.Join(root, "tenant")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	for _, k := range keys {
		p := filepath.Join(srcDir, filepath.FromSlash(k))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("bytes-of-"+k), 0o644))
	}
	src, err := NewLocalStore(srcDir)
	require.NoError(t, err)
	dst, err := NewLocalStore(dstDir)
	require.NoError(t, err)
	return &Stores{
		Source: src,
		TargetFor: func(cid int64) (ObjectStore, error) {
			if cid != 1 {
				return nil, fmt.Errorf("意外的 company_id=%d", cid)
			}
			return dst, nil
		},
	}, srcDir, dstDir
}

// openFailStore 让 Stat 正常、Open 失败，用来制造「复制阶段」而不是「预检阶段」的失败。
type openFailStore struct{ ObjectStore }

func (s openFailStore) Open(string) (io.ReadCloser, error) {
	return nil, errors.New("模拟读对象失败")
}

// snapshot 把全部资源列读成一行字符串，用于「一字节未变」的断言。
func snapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var b strings.Builder
	for _, c := range Inventory() {
		table, err := tableOf(c)
		require.NoError(t, err)
		var rows []struct {
			PK  int64  `gorm:"column:pk"`
			Val string `gorm:"column:val"`
		}
		require.NoError(t, db.Table(table).
			Select(c.PK+" AS pk, COALESCE("+c.Col+", '') AS val").
			Order(c.PK).Scan(&rows).Error, table)
		for _, r := range rows {
			fmt.Fprintf(&b, "%s.%s#%d=%q\n", table, c.Col, r.PK, r.Val)
		}
	}
	return b.String()
}

func ledgerRows(t *testing.T, db *gorm.DB) []model.StorageBackfillRef {
	t.Helper()
	var rows []model.StorageBackfillRef
	require.NoError(t, db.Order("id asc").Find(&rows).Error)
	return rows
}

func valueOf(t *testing.T, db *gorm.DB, table, col, pk string, id int64) string {
	t.Helper()
	var out struct {
		Val string `gorm:"column:val"`
	}
	require.NoError(t, db.Table(table).
		Select("COALESCE("+col+", '') AS val").
		Where(pk+" = ?", id).Scan(&out).Error)
	return out.Val
}

// ---- 预检 / 幂等 / 回滚 ----------------------------------------------------

func TestApplyDryRunWritesNothing(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, dstDir := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	before := snapshot(t, db)

	res, err := Apply(db, pf, stores, ApplyOptions{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, 4, len(res.Preflight.Objects))
	require.Equal(t, 5, len(res.Preflight.Refs))
	require.Equal(t, 0, res.ObjectsCopied)
	require.Equal(t, before, snapshot(t, db), "dry-run 不许改库")
	require.Empty(t, ledgerRows(t, db), "dry-run 不许写台账")
	_, err = os.Stat(dstDir)
	require.True(t, os.IsNotExist(err), "dry-run 不许创建目标目录")
}

func TestApplyIsIdempotentAndResumable(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, dstDir := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	first, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	require.Equal(t, 4, first.ObjectsCopied, "shared.jpg 被两个字段引用，只复制一次")
	require.Equal(t, 5, first.RefsUpdated)

	require.Equal(t, "t/1/a.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1))
	require.Equal(t, `["t/1/c.jpg",""]`, valueOf(t, db, "project_cases", "album", "id", 1),
		"数组列只改目标下标，空槽必须原样留着")
	require.Equal(t, "t/1/shared.jpg", valueOf(t, db, "biz_services", "icon", "id", 1))
	for _, k := range []string{"t/1/a.jpg", "t/1/b.jpg", "t/1/c.jpg", "t/1/shared.jpg"} {
		b, err := os.ReadFile(filepath.Join(dstDir, filepath.FromSlash(k)))
		require.NoError(t, err, k)
		require.Equal(t, "bytes-of-"+strings.TrimPrefix(k, "t/1/"), string(b))
	}
	afterFirst := snapshot(t, db)
	require.Len(t, ledgerRows(t, db), 5)

	// 重跑：对象靠 Stat 跳过、引用靠台账跳过，一行不该再动。
	second, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, second.ObjectsCopied)
	require.Equal(t, 0, second.RefsUpdated)
	require.Equal(t, 4, second.ObjectsSkipped)
	require.Equal(t, 5, second.RefsSkipped)
	require.Equal(t, afterFirst, snapshot(t, db), "重跑不许改库")
	require.Len(t, ledgerRows(t, db), 5, "重跑不许新增台账行")

	counts, total, err := Status(db, pf.Hash)
	require.NoError(t, err)
	require.Equal(t, map[string]int{StatusApplied: 5}, counts)
	require.Equal(t, 5, total)
}

func TestRollbackRestoresValuesAndKeepsObjects(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, dstDir := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	before := snapshot(t, db)

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	res, err := Rollback(db, pf, false)
	require.NoError(t, err)
	require.Equal(t, 5, res.Reverted)
	require.Equal(t, before, snapshot(t, db), "回滚必须把库一字节不差地还原（含数组空槽）")

	for _, row := range ledgerRows(t, db) {
		require.Equal(t, StatusRolledBack, row.Status)
	}
	// R5：对象只复制不删除。
	for _, k := range []string{"t/1/a.jpg", "t/1/b.jpg", "t/1/c.jpg", "t/1/shared.jpg"} {
		_, err := os.Stat(filepath.Join(dstDir, filepath.FromSlash(k)))
		require.NoError(t, err, "回滚不许删对象：%s", k)
	}
}

func TestRollbackThenReapplyReusesLedger(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	_, err = Rollback(db, pf, false)
	require.NoError(t, err)
	res, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	require.Equal(t, 5, res.RefsUpdated)
	require.Equal(t, 0, res.ObjectsCopied, "目标对象还在，不该重复复制")
	require.Len(t, ledgerRows(t, db), 5, "回滚后重跑复用台账行，不插第二条")
	require.Equal(t, "t/1/a.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1))
	for _, row := range ledgerRows(t, db) {
		require.Equal(t, StatusApplied, row.Status)
	}
}

func TestRollbackDryRunWritesNothing(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	after := snapshot(t, db)

	res, err := Rollback(db, pf, true)
	require.NoError(t, err)
	require.Equal(t, 5, res.Total)
	require.Equal(t, 0, res.Reverted)
	require.Equal(t, after, snapshot(t, db))
}

// ---- 必须停下的场景 --------------------------------------------------------

func TestApplyStopsOnStalePlan(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	// 计划生成后有人在后台改了头像。
	mustExec(t, db, `UPDATE employees SET avatar = 'brand-new.jpg' WHERE staff_id = 1`)
	before := snapshot(t, db)
	stores, _, dstDir := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrStalePlan)
	require.Equal(t, before, snapshot(t, db), "整批停下，任何一行都不许改")
	require.Empty(t, ledgerRows(t, db))
	_, statErr := os.Stat(dstDir)
	require.True(t, os.IsNotExist(statErr), "预检失败不许复制对象")
}

func TestRollbackStopsWhenRowWasEditedAfterApply(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	mustExec(t, db, `UPDATE employees SET avatar = 'edited-after-migrate.jpg' WHERE staff_id = 1`)
	after := snapshot(t, db)

	_, err = Rollback(db, pf, false)
	require.ErrorIs(t, err, ErrStalePlan)
	require.Equal(t, after, snapshot(t, db), "有一条不匹配就整批不回滚")
	require.Equal(t, "edited-after-migrate.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1),
		"绝不覆盖用户搬迁后的新值")
}

func TestApplyRefusesPlanWithProblems(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	mustExec(t, db, `INSERT INTO home_layouts (id, company_id, bg_image) VALUES (1, 1, 'javascript:alert(1)')`)
	pf := mustPlanFile(t, db)
	before := snapshot(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrPlanHasProblems)
	require.ErrorIs(t, err, ErrNeedsHuman)
	require.Equal(t, before, snapshot(t, db))
	require.Empty(t, ledgerRows(t, db))
}

func TestApplyRefusesTamperedTargetKey(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	tampered := mutatePlan(t, pf, func(p *Plan) {
		for i := range p.Items {
			if p.Items[i].Class == ClassNeedsMigrate {
				p.Items[i].TargetKey = "t/9/" + p.Items[i].SourceKey // 串到别人租户
				break
			}
		}
	})
	before := snapshot(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, tampered, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrPlanTampered)
	require.ErrorIs(t, err, ErrNeedsHuman)
	require.Equal(t, before, snapshot(t, db))
	require.Empty(t, ledgerRows(t, db))
}

func TestApplyRefusesTamperedTable(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	tampered := mutatePlan(t, pf, func(p *Plan) {
		for i := range p.Items {
			if p.Items[i].Class == ClassNeedsMigrate {
				p.Items[i].Table = "admins"
				break
			}
		}
	})
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, tampered, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrPlanTampered, "计划里的表名不许用来拼 SQL")
	require.Empty(t, ledgerRows(t, db))
}

func TestApplyRejectsMismatchedCounts(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	tampered := mutatePlan(t, pf, func(p *Plan) {
		p.Items = p.Items[:len(p.Items)-1] // 像被截断的计划
	})
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	_, err := Apply(db, tampered, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrPlanTampered)
}

func TestPreflightStopsOnMissingSourceObject(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg") // 少 shared.jpg
	before := snapshot(t, db)

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrNeedsHuman)
	require.Equal(t, before, snapshot(t, db))
	require.Empty(t, ledgerRows(t, db))
}

func TestCopyStageFailureLeavesDBUntouched(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	stores.Source = openFailStore{stores.Source} // Stat 通过、Open 失败
	before := snapshot(t, db)

	res, err := Apply(db, pf, stores, ApplyOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "库未改动")
	require.Equal(t, 0, res.RefsUpdated)
	require.Equal(t, before, snapshot(t, db), "① 失败时 ② 一行都不该动")
	require.Empty(t, ledgerRows(t, db))
}

func TestObjectConflictRequiresForce(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, dstDir := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	// 目标已存在但内容是半截的（上次复制到一半）。
	bad := filepath.Join(dstDir, "t", "1", "a.jpg")
	require.NoError(t, os.MkdirAll(filepath.Dir(bad), 0o755))
	require.NoError(t, os.WriteFile(bad, []byte("x"), 0o644))
	before := snapshot(t, db)

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.ErrorIs(t, err, ErrObjectConflict)
	require.Equal(t, before, snapshot(t, db))

	res, err := Apply(db, pf, stores, ApplyOptions{Force: true})
	require.NoError(t, err)
	require.Equal(t, 4, res.ObjectsCopied)
	b, err := os.ReadFile(bad)
	require.NoError(t, err)
	require.Equal(t, "bytes-of-a.jpg", string(b), "--force 应当用源覆盖残件")
}

func TestForceDoesNotBypassStaleDBValue(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	mustExec(t, db, `UPDATE employees SET avatar = 'changed.jpg' WHERE staff_id = 1`)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")

	_, err := Apply(db, pf, stores, ApplyOptions{Force: true})
	require.ErrorIs(t, err, ErrStalePlan, "--force 只放宽对象覆盖，绝不放宽库值检测")
	require.Equal(t, "changed.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1))
}

func TestRollbackRefusesForeignLedger(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.NoError(t, err)
	// 台账里塞一条与计划对不上的记录（模拟计划文件被换过）。
	require.NoError(t, db.Create(&model.StorageBackfillRef{
		PlanHash: pf.Hash, RefID: "deadbeef", OldValue: "x", NewValue: "y", Status: StatusApplied,
	}).Error)

	_, err = Rollback(db, pf, false)
	require.ErrorIs(t, err, ErrPlanTampered)
	require.Equal(t, "t/1/a.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1))
}

// ---- 纯函数 / 路径安全 -----------------------------------------------------

func TestRefIDIsStableAndIndexSensitive(t *testing.T) {
	r, err := resolve(Item{Column: "员工头像", Table: "employees", PK: "staff_id", PKValue: 7, Index: -1})
	require.NoError(t, err)
	same, err := resolve(Item{Column: "员工头像", Table: "employees", PK: "staff_id", PKValue: 7, Index: -1})
	require.NoError(t, err)
	require.Equal(t, RefID(r), RefID(same))

	arr, err := resolve(Item{Column: "案例图集", Table: "project_cases", PK: "id", PKValue: 7, Index: 0})
	require.NoError(t, err)
	arr1, err := resolve(Item{Column: "案例图集", Table: "project_cases", PK: "id", PKValue: 7, Index: 1})
	require.NoError(t, err)
	require.NotEqual(t, RefID(arr), RefID(arr1), "同一数组的不同下标是不同的引用")
}

func TestLocalStoreRejectsUnsafeKeys(t *testing.T) {
	dir := t.TempDir()
	st, err := NewLocalStore(dir)
	require.NoError(t, err)
	for _, key := range []string{"", "/etc/passwd", "../escape.jpg", "a/../../b.jpg", "a//b.jpg", `a\b.jpg`, "a/./b.jpg"} {
		_, err := st.Stat(key)
		require.Error(t, err, "对象键 %q 必须被拒", key)
		require.Error(t, st.Put(key, strings.NewReader("x"), 1, ""), "对象键 %q 必须被拒", key)
	}
	require.NoError(t, st.Put("t/1/ok.jpg", strings.NewReader("hello"), 5, "image/jpeg"))
	info, err := st.Stat("t/1/ok.jpg")
	require.NoError(t, err)
	require.True(t, info.Exists)
	require.EqualValues(t, 5, info.Size)
}

// errReader 模拟复制到一半失败：验证 Put 的原子落盘，不留半截对象。
type errReader struct{ sent bool }

func (r *errReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("模拟网络中断")
	}
	r.sent = true
	return copy(p, "abc"), nil
}

func TestLocalStorePutIsAtomic(t *testing.T) {
	dir := t.TempDir()
	st, err := NewLocalStore(dir)
	require.NoError(t, err)
	require.Error(t, st.Put("t/1/x.jpg", &errReader{}, 999, ""))
	info, err := st.Stat("t/1/x.jpg")
	require.NoError(t, err)
	require.False(t, info.Exists, "写到一半不许留下一个「存在但短一截」的对象")
	entries, err := os.ReadDir(filepath.Join(dir, "t", "1"))
	require.NoError(t, err)
	require.Empty(t, entries, "临时文件不许残留")
}

// TestWriteRefIsCompareAndSwap 直接测事务内的 CAS：预检到提交之间若有人改了库，
// 不 CAS 就会把别人的新值覆盖掉，而库里显示成「搬迁成功」。
func TestWriteRefIsCompareAndSwap(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	r, err := resolve(Item{Column: "员工头像", Table: "employees", PK: "staff_id", PKValue: 1, Index: -1,
		Raw: "a.jpg", TargetKey: "t/1/a.jpg", Class: ClassNeedsMigrate})
	require.NoError(t, err)

	mustExec(t, db, `UPDATE employees SET avatar = 'someone-else.jpg' WHERE staff_id = 1`)
	err = writeRef(db, r, r.Item.TargetKey, r.Item.Raw)
	require.ErrorIs(t, err, ErrStalePlan)
	require.Equal(t, "someone-else.jpg", valueOf(t, db, "employees", "avatar", "staff_id", 1),
		"CAS 失败时不许写")

	// 数组列的 CAS 也要按元素比，不是拿整个 JSON 字符串比。
	arr, err := resolve(Item{Column: "案例图集", Table: "project_cases", PK: "id", PKValue: 1, Index: 0,
		Raw: "c.jpg", TargetKey: "t/1/c.jpg", Class: ClassNeedsMigrate})
	require.NoError(t, err)
	mustExec(t, db, `UPDATE project_cases SET album = '["edited.jpg",""]' WHERE id = 1`)
	err = writeRef(db, arr, arr.Item.TargetKey, arr.Item.Raw)
	require.ErrorIs(t, err, ErrStalePlan)
	require.Equal(t, `["edited.jpg",""]`, valueOf(t, db, "project_cases", "album", "id", 1))
}

// TestTransactionRollsBackDBWhenLedgerInsertFails 盯住「改库 + 记台账同一事务」：
// 在第 3 条记账处注入失败，前两条业务行的改写必须一起回滚，台账也必须为空。
// 否则会留下「库改了但没有回滚依据」的半截状态——这是本轮最不能忍的失败面。
func TestTransactionRollsBackDBWhenLedgerInsertFails(t *testing.T) {
	db := newDB(t)
	seedLegacy(t, db)
	pf := mustPlanFile(t, db)
	stores, _, _ := makeStores(t, "a.jpg", "b.jpg", "c.jpg", "shared.jpg")
	before := snapshot(t, db)

	created := 0
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:fail-third-ledger", func(tx *gorm.DB) {
		if tx.Statement.Table != "storage_backfill_refs" {
			return
		}
		created++
		if created == 3 {
			tx.AddError(errors.New("模拟第 3 条记账失败"))
		}
	}))

	_, err := Apply(db, pf, stores, ApplyOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "模拟第 3 条记账失败")
	require.Equal(t, before, snapshot(t, db), "事务必须把前面已改的业务行一起回滚")
	require.Empty(t, ledgerRows(t, db), "台账也不许留下半截记录")
}

// blockingReader 在第一次 Read 后卡住，让测试能在「写到一半」的窗口里观察目标路径。
type blockingReader struct {
	started chan struct{}
	release chan struct{}
	sent    bool
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		close(r.started)
		return copy(p, "abc"), nil
	}
	<-r.release
	return 0, io.EOF
}

// TestLocalStorePutNeverExposesPartialObject 才真正测「原子落盘」。
// 上面那条 errorReader 用例测的是「失败后有清理」——直接写目标路径再 defer 删掉
// 也能通过，二者不是一回事。原子性要保证的是：复制途中（尤其是进程被 kill、
// 根本来不及跑 defer）Stat 目标键必须看不见对象，否则重跑会把半截对象当成
// 「已复制」而跳过，库里指向一个坏文件。
func TestLocalStorePutNeverExposesPartialObject(t *testing.T) {
	dir := t.TempDir()
	st, err := NewLocalStore(dir)
	require.NoError(t, err)
	r := &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- st.Put("t/1/slow.jpg", r, 3, "") }()

	<-r.started
	info, err := st.Stat("t/1/slow.jpg")
	require.NoError(t, err)
	require.False(t, info.Exists, "写到一半时目标对象绝不能可见")
	close(r.release)
	require.NoError(t, <-done)
	info, err = st.Stat("t/1/slow.jpg")
	require.NoError(t, err)
	require.True(t, info.Exists)
	require.EqualValues(t, 3, info.Size)
}
