// Package dbbackup 提供 SQLite 的一致性快照与体检能力，供 `server backup` /
// `server dbcheck` 两个运维子命令与恢复演练脚本复用。
//
// 为什么需要它：
//   - 生产默认形态就是 SQLite，而此前 `scripts/backup.sh` 只做 MySQL 的
//     mysqldump——SQLite 部署实际上**没有任何备份手段**。直接 cp 一个正在写的
//     库会拿到撕裂的快照（WAL 里还有未合并的页），恢复回来是坏库。
//   - `VACUUM INTO` 是 SQLite 官方的在线快照方式：读事务内完成，不阻塞写，
//     产物是干净的单文件（不含 -wal/-shm）。但它要求 sqlite3 命令行工具，
//     而运行镜像只装了 tzdata；用二进制自己干，非容器部署也能用。
//
// 体检（Inspect）独立成函数，是因为「备份文件存在」和「备份文件能用」是两件事：
// 前者 `ls` 就能看，后者要打开它、跑 integrity_check、数表。恢复演练的价值
// 全在后者。
package dbbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"weichuan/internal/dbopen"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Report 一次快照或体检的结果。
type Report struct {
	Path      string           `json:"path"`
	Bytes     int64            `json:"bytes"`
	Tables    int              `json:"tables"`
	Integrity string           `json:"integrity"`
	Rows      map[string]int64 `json:"rows"` // 每张表的行数（按表名排序输出）
}

// openReadOnly 以只读方式打开一个已存在的 SQLite 文件。
//
// mode=ro 不只是「保险」：gorm 默认打开一个不存在的路径会**创建**一个空库，
// 于是「体检一个写错的路径」会得到「0 张表、integrity ok」这种看起来正常的结论——
// 比直接报错危险得多。
func openReadOnly(path string) (*gorm.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// 静默日志：体检是运维命令，不需要 gorm 把每条 SELECT 都打出来
	dsn := "file:" + abs + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	return gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

// Snapshot 对 SQLite 库做一致性快照（VACUUM INTO），并立即体检产物。
//
// out 若已存在会被覆盖——这是备份目录里的一次性产物，保留旧文件只会让
// 「这份备份是什么时候的」变模糊。
func Snapshot(dsn, out string) (Report, error) {
	if !dbopen.IsSQLiteDSN(dsn) {
		return Report{}, fmt.Errorf("SQLite 快照只适用于 SQLite 库；MySQL 请用 scripts/backup.sh（mysqldump --single-transaction）")
	}
	if strings.TrimSpace(out) == "" {
		return Report{}, fmt.Errorf("缺少输出路径")
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Report{}, fmt.Errorf("创建输出目录失败：%w", err)
	}
	// VACUUM INTO 要求目标文件不存在
	if _, err := os.Stat(abs); err == nil {
		if err := os.Remove(abs); err != nil {
			return Report{}, fmt.Errorf("清理已存在的输出文件失败：%w", err)
		}
	}

	db, _, err := dbopen.Open(dsn)
	if err != nil {
		return Report{}, fmt.Errorf("连接数据库失败：%w", err)
	}
	// 参数绑定优先；个别驱动版本不接受 VACUUM INTO 的位置参数，退回字面量
	// （单引号转义两遍，路径里带引号也不会被拼坏）。
	if err := db.Exec("VACUUM INTO ?", abs).Error; err != nil {
		lit := strings.ReplaceAll(abs, "'", "''")
		if err2 := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", lit)).Error; err2 != nil {
			return Report{}, fmt.Errorf("VACUUM INTO 失败：%v（绑定参数方式报错：%v）", err2, err)
		}
	}
	if sqlDB, e := db.DB(); e == nil {
		_ = sqlDB.Close()
	}
	return Inspect(abs)
}

// Inspect 打开一个 SQLite 文件做只读体检：完整性、表数、各表行数。
//
// 只读打开 + 只跑 SELECT，绝不改动被体检的文件——它可能正是生产库本身。
func Inspect(path string) (Report, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Report{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Report{}, fmt.Errorf("备份文件不存在：%w", err)
	}
	if fi.IsDir() {
		return Report{}, fmt.Errorf("%s 是目录，不是数据库文件", abs)
	}
	if fi.Size() == 0 {
		return Report{}, fmt.Errorf("%s 是空文件（备份中断或被截断）", abs)
	}

	db, err := openReadOnly(abs)
	if err != nil {
		return Report{}, fmt.Errorf("打开失败（文件可能不是 SQLite 库或已损坏）：%w", err)
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()

	rep := Report{Path: abs, Bytes: fi.Size(), Rows: map[string]int64{}}

	var integrity string
	if err := db.Raw("PRAGMA integrity_check").Scan(&integrity).Error; err != nil {
		return Report{}, fmt.Errorf("integrity_check 执行失败：%w", err)
	}
	rep.Integrity = integrity
	if !strings.EqualFold(strings.TrimSpace(integrity), "ok") {
		// 损坏的库不该被当成「备份可用」，直接失败比返回一份带警告的报告更有力
		return rep, fmt.Errorf("完整性检查未通过：%s", integrity)
	}

	var tables []string
	if err := db.Raw(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
	).Scan(&tables).Error; err != nil {
		return Report{}, fmt.Errorf("读取表清单失败：%w", err)
	}
	rep.Tables = len(tables)
	for _, t := range tables {
		var n int64
		// 表名来自 sqlite_master，不是外部输入；仍用双引号包裹以兼容保留字
		if err := db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, strings.ReplaceAll(t, `"`, `""`))).Scan(&n).Error; err != nil {
			return Report{}, fmt.Errorf("统计表 %s 行数失败：%w", t, err)
		}
		rep.Rows[t] = n
	}
	return rep, nil
}

// SortedRowNames 返回按表名排序的行数键，供报告稳定输出（map 遍历顺序随机，
// 每次跑出来的报告都不一样就没法 diff 了）。
func (r Report) SortedRowNames() []string {
	names := make([]string, 0, len(r.Rows))
	for k := range r.Rows {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// DiffTables 比较两份报告的表集合，返回「基准有、对比缺失」的表名。
// 备份不完整（少表）是硬故障，必须能被自动发现。
func DiffTables(base, other Report) []string {
	var missing []string
	for _, t := range base.SortedRowNames() {
		if _, ok := other.Rows[t]; !ok {
			missing = append(missing, t)
		}
	}
	return missing
}
