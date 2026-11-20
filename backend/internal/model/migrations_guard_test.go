package model_test

// migrations 表名 vs gorm 模型表名守门测试。
//
// 背景（2026-10-21 修）：migrations 建的是单数表名（employee / admin /
// company_info…），而 gorm 模型没有 TableName、命名策略默认复数化
// （employees / admins / company_infos…）——两边从未对齐。SQLite 路径
// 全靠 AutoMigrate 建表所以毫无察觉；MySQL parity 测试因 TEST_MYSQL_DSN
// 门控从未在 CI 启用，于是 compose 的 MySQL 形态「migrations 建一堆
// 没人用的单数表 + 安装向导 AutoMigrate 再建一套复数空表」，系统装出来
// 是残的（sys_setting 因有显式 TableName 唯一对得上，还撞了 gorm 的
// MigrateColumnUnique 假阳性直接 1091）。
//
// 本测试不需要数据库：静态比对两边表名集合。新增模型忘记写迁移、
// 或迁移用了和模型不一致的表名，都会在这里红。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

// extraMigrationTables：migrations 里有、但不在 model.All() 的表。
// 这些表的模型带显式 TableName() 且定义在本包之外（避免反向依赖），
// 新增此类表时在此登记，别让守门测试变成漏报。
var extraMigrationTables = map[string]string{
	"sys_setting": "internal/setting.Setting（显式 TableName）",
	// 0044 建、0055 删的历史表：模型已随第七十五轮（存储配置单模型化）移除，
	// 但迁移文件保留（已应用过 0044 的库与全新安装需要走到同一终态）。
	"tenant_storage_configs": "历史表，migrations/0055 已 DROP",
}

// tableRefRe 匹配迁移脚本里出现表名的语句：CREATE TABLE [IF NOT EXISTS] x /
// ALTER TABLE x / INSERT INTO x / UPDATE x / CREATE [UNIQUE] INDEX i ON x。
var tableRefRe = regexp.MustCompile(
	`(?i)(?:CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		`|ALTER\s+TABLE\s+|INSERT\s+INTO\s+|UPDATE\s+` +
		`|CREATE\s+(?:UNIQUE\s+)?INDEX\s+` + "`?" + `[a-z0-9_]+` + "`?" + `\s+ON\s+)` +
		"`?" + `([a-z_][a-z0-9_]*)` + "`?",
)

func TestMigrationsTableNamesMatchModels(t *testing.T) {
	// 1) 模型侧表名（gorm 命名策略，含复数化；显式 TableName 优先）
	cache := &sync.Map{}
	modelTables := map[string]string{} // 表名 → 模型类型（报错用）
	for _, m := range model.All() {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		require.NoError(t, err)
		modelTables[s.Table] = fmt.Sprintf("%T", m)
	}
	require.NotEmpty(t, modelTables, "model.All() 为空？")

	// 2) 迁移侧表名
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)
	migrationTables := map[string]string{} // 表名 → 首次出现的文件
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		require.NoError(t, err)
		for _, m := range tableRefRe.FindAllStringSubmatch(string(raw), -1) {
			tbl := m[1]
			if _, seen := migrationTables[tbl]; !seen {
				migrationTables[tbl] = e.Name()
			}
		}
	}
	require.NotEmpty(t, migrationTables, "migrations 目录没有 .sql？")

	// 3) 双向比对
	var missingInMigrations []string // 模型有、迁移没有 → 新模型忘了写迁移
	for tbl, typ := range modelTables {
		if _, ok := migrationTables[tbl]; !ok {
			missingInMigrations = append(missingInMigrations, fmt.Sprintf("%s（%s）", tbl, typ))
		}
	}
	sort.Strings(missingInMigrations)

	var mismatched []string // 迁移有、模型没有（且未登记）→ 表名不一致或漏加模型
	for tbl, file := range migrationTables {
		if _, ok := modelTables[tbl]; ok {
			continue
		}
		if _, ok := extraMigrationTables[tbl]; ok {
			continue
		}
		mismatched = append(mismatched, fmt.Sprintf("%s（%s）", tbl, file))
	}
	sort.Strings(mismatched)

	if len(missingInMigrations) > 0 {
		t.Errorf("以下模型表在 migrations 里没有对应语句（新模型要同时写迁移）：%v", missingInMigrations)
	}
	if len(mismatched) > 0 {
		t.Errorf("以下迁移表名与任何模型都对不上（表名不一致？或新表忘了加模型/登记 extraMigrationTables）：%v", mismatched)
	}
}
