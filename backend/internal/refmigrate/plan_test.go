package refmigrate

import (
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newDB 建一个独立的 SQLite 库。刻意用临时文件而不是 :memory:——
// glebarez/sqlite 的连接池会让 each-connection 各有一个内存库，多一条查询就看不到表。
func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "plan.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Employee{}, &model.OpenApply{}, &model.CompanyInfo{}, &model.CompanyProfile{},
		&model.AchievementCategory{}, &model.ProjectCase{}, &model.BizService{},
		&model.HomeLayout{}, &model.MediaCheck{}, &model.StorageBackfillRef{},
	))
	return db
}

// TestInventoryTablesExist 钉住清单里的每个模型都能解析出真实表名。
//
// 这条不是形式主义：Column.PK 与 Column.Col 是字符串，写错一个字母不会编译失败，
// 只会在运行期得到「no such column」——而盘点命令如果因为一个笔误整天不出报告，
// 运维会以为「库里没有需要搬的东西」。所以让它在单测里先红。
func TestInventoryTablesExist(t *testing.T) {
	db := newDB(t)
	for _, col := range Inventory() {
		table, err := tableName(db, col.Model)
		require.NoError(t, err, col.Label)
		require.True(t, db.Migrator().HasTable(table), "%s 的表 %s 不存在", col.Label, table)
		require.True(t, db.Migrator().HasColumn(col.Model, col.Col), "%s 的列 %s 不存在", col.Label, col.Col)
		require.True(t, db.Migrator().HasColumn(col.Model, col.PK), "%s 的主键 %s 不存在", col.Label, col.PK)
	}
}

// TestScanClassifiesAllForms 用一张真实形态的库覆盖每一种归类。
// 少一种归类 = 执行器少一种处置，而「没归类的那批」会静默留在平台盘上。
func TestScanClassifiesAllForms(t *testing.T) {
	db := newDB(t)
	const base = "https://card.example.com/uploads"

	// company 1：三种历史形态 + 平台对象 + 外链
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (11, 1, 'old-avatar.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (12, 1, 't/1/new-avatar.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (13, 1, '')`)
	mustExec(t, db, `INSERT INTO company_infos (id, company_id, logo) VALUES (1, 1, 'p/library/cat-1.png')`)
	mustExec(t, db, `INSERT INTO company_profiles (id, company_id, video_url, video_backup_url, cover)
		VALUES (1, 1, 'https://cdn.other.com/a.mp4', '/uploads/cover.jpg', 'https://card.example.com/uploads/cover2.jpg')`)
	// 图集：数组列，两个元素分别是历史形态与已就绪
	mustExec(t, db, `INSERT INTO project_cases (id, company_id, cover, album)
		VALUES (1, 1, '', '["/uploads/p1.jpg","t/1/p2.jpg"]')`)
	// 危险值：伪协议 + 别人的租户键
	mustExec(t, db, `INSERT INTO biz_services (id, company_id, icon) VALUES (1, 1, 'javascript:alert(1)')`)
	mustExec(t, db, `INSERT INTO open_applies (id, company_id, avatar) VALUES (1, 1, 't/9/stolen.jpg')`)
	// 送检记录：url 与 object_key 必须一起搬
	mustExec(t, db, `INSERT INTO media_check (id, company_id, url, object_key)
		VALUES (1, 1, 'https://card.example.com/uploads/2026/10/abc.jpg', '2026/10/abc.jpg')`)
	// 另一家租户，用来验证 --company 过滤
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (21, 2, 'other.jpg')`)

	plan, err := Scan(db, Options{BaseURL: base})
	require.NoError(t, err)

	byRaw := map[string]Item{}
	for _, it := range plan.Items {
		byRaw[it.Column+"|"+it.Raw] = it
	}

	cases := []struct {
		name, column, raw string
		class             Class
		source, target    string
	}{
		{"历史裸键", "员工头像", "old-avatar.jpg", ClassNeedsMigrate, "old-avatar.jpg", "t/1/old-avatar.jpg"},
		{"已就绪的租户键", "员工头像", "t/1/new-avatar.jpg", ClassTenantReady, "", "t/1/new-avatar.jpg"},
		{"平台对象（允许）", "企业 LOGO", "p/library/cat-1.png", ClassPlatform, "", "p/library/cat-1.png"},
		{"外域直链", "公司简介主视频", "https://cdn.other.com/a.mp4", ClassForeignURL, "", ""},
		{"历史相对路径", "公司简介备用视频", "/uploads/cover.jpg", ClassNeedsMigrate, "cover.jpg", "t/1/cover.jpg"},
		{"历史绝对 URL", "公司简介封面", "https://card.example.com/uploads/cover2.jpg", ClassNeedsMigrate, "cover2.jpg", "t/1/cover2.jpg"},
		{"图集元素（历史）", "案例图集", "/uploads/p1.jpg", ClassNeedsMigrate, "p1.jpg", "t/1/p1.jpg"},
		{"图集元素（就绪）", "案例图集", "t/1/p2.jpg", ClassTenantReady, "", "t/1/p2.jpg"},
		{"伪协议", "服务图标", "javascript:alert(1)", ClassInvalid, "", ""},
		{"别人的租户键", "开通申请头像", "t/9/stolen.jpg", ClassForeignTenant, "", ""},
		{"送检对象键", "送检对象键", "2026/10/abc.jpg", ClassNeedsMigrate, "2026/10/abc.jpg", "t/1/2026/10/abc.jpg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it, ok := byRaw[c.column+"|"+c.raw]
			require.True(t, ok, "盘点里没有这一条：%s=%q", c.column, c.raw)
			require.Equal(t, c.class, it.Class, it.Note)
			require.Equal(t, c.source, it.SourceKey)
			require.Equal(t, c.target, it.TargetKey)
		})
	}

	// 空值不进盘点（SQL 层就已排除）：它们没有对象可搬，列进报告只会稀释真正的条目。
	for _, it := range plan.Items {
		require.NotEqual(t, ClassEmpty, it.Class, "空值不该出现在计划里：%+v", it)
	}

	// 另一家的头像也必须被扫到（默认全量）
	other, ok := byRaw["员工头像|other.jpg"]
	require.True(t, ok)
	require.Equal(t, "t/2/other.jpg", other.TargetKey, "目标键必须按行自己的 company_id 算")
}

// TestScanCompanyFilter 钉住试点模式：只扫一家时，别家的条目一条都不该出现。
// 试点只开放一家企业时，误扫另一家会生成一批「不该执行」的计划。
func TestScanCompanyFilter(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (11, 1, 'a.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (21, 2, 'b.jpg')`)

	plan, err := Scan(db, Options{CompanyID: 2})
	require.NoError(t, err)
	require.Equal(t, 1, len(plan.Items))
	require.Equal(t, int64(21), plan.Items[0].PKValue)
	require.Equal(t, "t/2/b.jpg", plan.Items[0].TargetKey)
}

// TestScanIsReadOnlyAndIdempotent 是「只读」这件事的证据：
// 扫两遍结果必须完全一致，且库里的值一个字节都不许变。
//
// 为什么值得单独测：盘点命令最容易在「顺手归一一下」的诱惑下变成写操作，
// 而一旦它开始写库，P3 的 dry-run 就失去了全部意义——你看到的不是现状。
func TestScanIsReadOnlyAndIdempotent(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (11, 1, '/uploads/a.jpg')`)
	mustExec(t, db, `INSERT INTO project_cases (id, company_id, album) VALUES (1, 1, '["/uploads/x.jpg"]')`)

	first, err := Scan(db, Options{})
	require.NoError(t, err)
	second, err := Scan(db, Options{})
	require.NoError(t, err)
	require.Equal(t, first.Items, second.Items, "两次盘点必须完全一致")
	require.Equal(t, first.Counts, second.Counts)

	var avatar string
	require.NoError(t, db.Raw(`SELECT avatar FROM employees WHERE staff_id = 11`).Scan(&avatar).Error)
	require.Equal(t, "/uploads/a.jpg", avatar, "盘点不许改写库里的值")

	var album string
	require.NoError(t, db.Raw(`SELECT album FROM project_cases WHERE id = 1`).Scan(&album).Error)
	require.Equal(t, `["/uploads/x.jpg"]`, album)
}

// TestObjectsDedupesBySource 钉住「同一张图只复制一次」。
// 库里的改写仍是逐条（一行一条 UPDATE），但对象复制必须去重：
// 重复复制不只是白花流量，还会让「复制失败」被重复计数，掩盖真实失败数。
func TestObjectsDedupesBySource(t *testing.T) {
	db := newDB(t)
	// 同一张 /uploads/shared.jpg 被三个字段引用
	mustExec(t, db, `INSERT INTO company_infos (id, company_id, logo) VALUES (1, 1, 'shared.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (11, 1, '/uploads/shared.jpg')`)
	mustExec(t, db, `INSERT INTO company_profiles (id, company_id, cover) VALUES (1, 1, 'https://card.example.com/uploads/shared.jpg')`)

	plan, err := Scan(db, Options{BaseURL: "https://card.example.com/uploads"})
	require.NoError(t, err)
	groups := plan.Objects()
	require.Equal(t, 3, plan.Counts[ClassNeedsMigrate], "三条引用")
	require.Equal(t, 1, len(groups), "但只有一个对象要复制")
	require.Equal(t, "shared.jpg", groups[0].SourceKey)
	require.Equal(t, "t/1/shared.jpg", groups[0].TargetKey)
	require.Equal(t, 3, groups[0].Refs)
}

// TestProblemsMustBeHumanDecided 钉住「异常必须浮出来」：
// 串号与非法值不能混在「跳过」里——跳过一条串号键等于把一个安全隐患留在库里。
func TestProblemsMustBeHumanDecided(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `INSERT INTO biz_services (id, company_id, icon) VALUES (1, 1, 'data:image/png;base64,AA')`)
	mustExec(t, db, `INSERT INTO open_applies (id, company_id, avatar) VALUES (1, 1, 't/9/x.jpg')`)
	mustExec(t, db, `INSERT INTO employees (staff_id, company_id, avatar) VALUES (11, 1, 'ok.jpg')`)

	plan, err := Scan(db, Options{})
	require.NoError(t, err)
	problems := plan.Problems()
	require.Len(t, problems, 2)
	for _, p := range problems {
		require.NotEmpty(t, p.Note, "异常条目必须写明原因：%+v", p)
	}
}

// TestAlbumBrokenJSONIsReported 钉住「坏图集不能从盘点里消失」。
// 若把解析失败的 JSON 当空值处理，那条数据里的所有图片引用就永远不会被搬迁，
// 也不会出现在任何报告里——典型的三无状态：没人报错、没人知道、到时候裂图。
func TestAlbumBrokenJSONIsReported(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `INSERT INTO project_cases (id, company_id, album) VALUES (1, 1, 'not-json')`)

	plan, err := Scan(db, Options{})
	require.NoError(t, err)
	require.Equal(t, 1, plan.Counts[ClassInvalid])
	require.Len(t, plan.Items, 1)
	require.Contains(t, plan.Items[0].Note, "JSON")
	require.True(t, strings.Contains(plan.Items[0].Note, "解析失败"))
}

func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	require.NoError(t, db.Exec(sql, args...).Error)
}
