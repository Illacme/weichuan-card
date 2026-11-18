package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 盲索引列必须真的落到数据库索引上。0030 / 0031 只 ADD COLUMN，
// 登录（WHERE phone_bidx = ?）与判重（company_id + phone_bidx）因此全表扫描。
//
// 这张表是「源码守门」与「MySQL 真库断言」共用的唯一真值源：
//   - TestPIIBlindIndexMigrationDeclaresAllIndexes 证明迁移文件写全了；
//   - TestMySQLMigrations 里的 assertPIIBlindIndexes 证明迁移执行后索引真的存在。
//
// 只做前者会重蹈「manifest 对上但列没建」的覆辙，只做后者则本地无 DSN 时零覆盖。
var wantPIIBlindIndexes = []struct {
	table string
	index string
	cols  []string
}{
	{"employees", "idx_emp_name_bidx", []string{"name_bidx", "company_id"}},
	{"employees", "idx_emp_phone_bidx", []string{"phone_bidx", "company_id"}},
	{"employees", "idx_emp_phone2_bidx", []string{"phone2_bidx", "company_id"}},
	{"employees", "idx_emp_email_bidx", []string{"email_bidx", "company_id"}},
	{"employees", "idx_emp_nickname_bidx", []string{"nickname_bidx", "company_id"}},
	{"open_applies", "idx_open_apply_name_bidx", []string{"name_bidx", "company_id"}},
	{"open_applies", "idx_open_apply_phone_bidx", []string{"phone_bidx", "company_id"}},
	{"open_applies", "idx_open_apply_phone2_bidx", []string{"phone2_bidx", "company_id"}},
	{"open_applies", "idx_open_apply_email_bidx", []string{"email_bidx", "company_id"}},
	{"open_applies", "idx_open_apply_nickname_bidx", []string{"nickname_bidx", "company_id"}},
}

// TestPIIBlindIndexMigrationDeclaresAllIndexes 是零依赖的源码守门：
// 迁移文件改名、漏写某列、列顺序写反、manifest 漏登记，本地 go test 即刻红。
func TestPIIBlindIndexMigrationDeclaresAllIndexes(t *testing.T) {
	path := filepath.Join("..", "..", "migrations", "0051_pii_blind_index.sql")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "读取 %s 失败；迁移文件改名会同时打断线上升级", path)

	// 剥整行注释：注释里解释了为什么列顺序是 (bidx, company_id)，
	// 朴素正则会把说明文字里的关键词也当成语句抓进来。
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	code := strings.Join(lines, "\n")

	re := regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(\w+)\s+ADD\s+INDEX\s+(\w+)\s*\(([^)]+)\)`)
	got := re.FindAllStringSubmatch(code, -1)
	require.Len(t, got, len(wantPIIBlindIndexes),
		"量尺自检：0051 应精确声明 %d 条索引，实际抓到 %d 条（正则失配会让守门空转）",
		len(wantPIIBlindIndexes), len(got))

	declared := make(map[string][]string, len(got))
	for _, m := range got {
		cols := strings.Split(m[3], ",")
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		declared[m[1]+"."+m[2]] = cols
	}
	for _, w := range wantPIIBlindIndexes {
		key := w.table + "." + w.index
		require.Equal(t, w.cols, declared[key],
			"%s 的列顺序不对：登录用 phone_bidx 单列命中，bidx 必须在最左；company_id 只能排第二", key)
	}
}

// assertPIIBlindIndexes 直接查 information_schema，验证迁移**执行后**索引真的建出来了。
// 由 TestMySQLMigrations 调用，CI 的 MySQL service job 每次都会跑到（该用例有 PASS 断言，
// 改名/跳过都会红）。
func assertPIIBlindIndexes(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, w := range wantPIIBlindIndexes {
		var cols []string
		require.NoError(t, db.Raw(`
			SELECT COLUMN_NAME FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
			ORDER BY SEQ_IN_INDEX`, w.table, w.index).Scan(&cols).Error,
			"查询 %s.%s 的索引元数据失败", w.table, w.index)
		require.Equal(t, w.cols, cols,
			"%s.%s 索引不存在或列顺序不对：登录/判重会退化为全表扫描", w.table, w.index)
	}
}
