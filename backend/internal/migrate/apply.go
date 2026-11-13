// Package migrate 按序应用 backend/migrations/*.sql（MySQL 方言）。
//
// 三个使用方：
//   - 安装向导（internal/setup/install.go）的 MySQL 分支：表结构归迁移脚本管，
//     与 cmd/server 对 MySQL 不跑 AutoMigrate 的策略一致；
//   - cmd/server 启动：MySQL 部署「升级」时自动补齐待应用迁移（见 AutoMigrate 说明）；
//   - 测试（handler_test 的 MySQL parity）：与生产同一条代码路径。
//
// 版本记账：已应用的迁移文件名记在 schema_migrations 表，重复启动不会重放。
// 该表是**本工具的元数据**，不占 migrations/ 目录的业务序号——否则「手工 apply
// 目录里的 .sql」与「程序记账」会互相干扰，且它的建表语句要早于 0001 才有用。
//
// 幂等性：建表用 IF NOT EXISTS；对已存在的对象，ALTER/INSERT 会报
// "Duplicate column" / "already exists" / "Duplicate entry"（1060/1050/1062），
// 删一个已不存在的列会报 "Can't DROP"（1091）——这些错误一律忽略——与
// docs/deploy-runbook.md「存量数据库应用迁移」的容错说明一致。因此首次接管的
// 存量库（记账表为空、业务表已存在）重放全部迁移也安全：命中即跳过，随后补记版本。
//
// 注意 MySQL 8.0 与 MariaDB 的 DDL 方言差异：DROP COLUMN IF EXISTS /
// ADD COLUMN IF NOT EXISTS 只有 MariaDB 认，MySQL 8.0 在解析阶段就报 1064。
// 迁移 SQL 一律写裸 DDL，幂等靠上面的错误忽略 + 版本记账，不靠 IF EXISTS。
//
// 为什么切分语句只按 ";"：migrations 里只有 DDL/DML，无存储过程 / 触发器 /
// 含分号的字符串字面量（企业名等中文数据不含分号）。若将来加入这类语句，
// 必须换成真正的 SQL 解析器，不能继续 naive split。
package migrate

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 记账表与并发锁：多副本同时启动时若都跑迁移，会互相撞 DDL 与记账 INSERT，
// 故 MySQL 侧先取一个命名锁（GET_LOCK），拿不到的副本直接跳过——另一个副本
// 正在做，跳过不会丢迁移（下次启动仍会补）。SQLite 没有 GET_LOCK，也本就
// 不支持多副本（见 cmd/server 启动日志提示），跳过加锁直接执行。
// 记账与并发锁用到的常量（见包注释）。
// nowFunc 抽成变量是为了单测可断言 applied_at，也避免依赖真实时钟。
var nowFunc = time.Now

const (
	migrationsTable = "schema_migrations"
	lockName        = "weichuan_migrate"
	lockTimeoutSec  = 10
	// envMigrationsDir 运维显式覆盖迁移目录的逃生门（见 migrationsDir）。
	// 设置后跳过「清单完整性」守门：目录内容由调用方负责（测试也用它跑记账逻辑）。
	envMigrationsDir = "WEICHUAN_MIGRATIONS_DIR"
)

// ignoredErrs 已应用过的迁移语句报的错（见包注释）：
//
//	1060 Duplicate column name  —— ALTER ADD COLUMN 列已存在
//	1050 Table already exists   —— CREATE TABLE（有 IF NOT EXISTS，双保险）
//	1061 Duplicate key name     —— ALTER ADD INDEX 索引已存在
//	1062 Duplicate entry        —— 固定主键的种子 INSERT 重复执行
//	1091 Can't DROP 'x'         —— DROP COLUMN 目标已不存在
//
// 1091 是 2026-11-13 迁移 0043 事故后补的：MySQL 8.0 不支持
// DROP COLUMN IF EXISTS，只能用裸 DROP + 忽略 1091，才能同时覆盖
// 「列还在」与「列已被手工删但版本没记账」两种库状态。
var ignoredErrs = []string{
	"Duplicate column", "already exists", "Duplicate key name", "Duplicate entry",
	"Can't DROP",
}

// migrationsDir 定位 migrations 目录：按优先级逐个候选，第一个「存在且有 .sql」的胜出。
//  1. 环境变量 WEICHUAN_MIGRATIONS_DIR（运维显式覆盖的逃生门）；
//  2. 可执行文件同级的 migrations/（容器布局：/app/server + /app/migrations；
//     -trimpath 构建时 runtime.Caller 只剩相对路径，也只有这条能找到）；
//  3. runtime.Caller 推导的源码树路径（dev / go test：源码树在，编译期绝对路径可用）；
//  4. CWD 下的 migrations/（在 backend/ 目录里直接跑二进制）。
//
// 背景：曾只用 runtime.Caller，容器里它返回**编译期**路径 /src/migrations（运行期不存在），
// MySQL 形态安装直接报「读取 migrations 目录失败」——「从未跑起来」的又一形态。
func migrationsDir() (string, error) {
	var cands []string
	if v := strings.TrimSpace(os.Getenv(envMigrationsDir)); v != "" {
		cands = append(cands, v)
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "migrations"))
	}
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		cands = append(cands, filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations"))
	}
	cands = append(cands, "migrations")
	for _, d := range cands {
		if hasSQLFiles(d) {
			return d, nil
		}
	}
	return "", fmt.Errorf("找不到 migrations 目录（候选过：%s）", strings.Join(cands, " | "))
}

func hasSQLFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			return true
		}
	}
	return false
}

// Apply 按文件名顺序执行 migrations 目录下尚未记录的 *.sql，并记录版本。
func Apply(db *gorm.DB) error {
	_, err := ApplyWithReport(db)
	return err
}

// ApplyWithReport 与 Apply 相同，但返回**本次真正应用**的迁移文件名
// （已应用过的不会出现在列表里），供启动日志与 `server migrate` 子命令回显。
func ApplyWithReport(db *gorm.DB) ([]string, error) {
	dir, err := migrationsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取 migrations 目录失败：%v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("migrations 目录 %s 下没有 .sql 文件", dir)
	}
	sort.Strings(files) // 0001 → 0019 按序应用

	done, err := appliedVersions(db)
	if err != nil {
		return nil, err
	}

	// 守门 1（2026-11-03 事故的正面防线）：本版本**应该**带的迁移文件是否都在目录里。
	//
	// 清单 expectedMigrations 由 scripts/gen_migrations_manifest.py 从 migrations/
	// 生成、编译进二进制。只有它才能识别「目录整个停留在旧版本」这种形态：
	// 此时记账表与目录是**自洽**的（都是 0001..0034），任何基于「两边对比」的检查
	// 都会得出「已是最新」——事故就是这样静默发生的（缺 0035 的 subscribe_tmpl_id，
	// 运行期才报 Unknown column）。
	//
	// 跳过 WEICHUAN_MIGRATIONS_DIR 场景：那是运维/测试显式指定的目录，内容由调用方
	// 负责（测试用 0001_a.sql 这类假文件跑记账逻辑，天然与清单不符）。
	if os.Getenv(envMigrationsDir) == "" {
		if missing := missingFromManifest(files); len(missing) > 0 {
			return nil, fmt.Errorf(
				"部署包不完整：本版本应带 %d 个迁移文件，migrations 目录（%s）缺 %d 个：%s。"+
					"极可能升级时只替换了二进制、没同步 migrations/ 目录——"+
					"把本版本的 migrations/ 与二进制一起部署后重启即可（已应用的不重放）",
				len(expectedMigrations), dir, len(missing), strings.Join(missing, ", "))
		}
		// 反向：目录里有二进制不认识的迁移（旧二进制 + 新目录）。不阻断——
		// 这一版可能仍能跑，但意味着二进制落后于目录，值得在日志里点出来。
		if extra := unknownInDir(files); len(extra) > 0 {
			log.Printf("[MIGRATE][WARN] migrations 目录里 %d 个文件不在本版本清单内：%s"+
				"（二进制比目录旧？旧版本可能不认识这些迁移的内容）",
				len(extra), strings.Join(extra, ", "))
		}
	}

	// 守门 2：记账表里有、但 migrations 目录里已经没有的版本。
	// 抓的是另一种形态——目录被换成了更旧的包（应用过的文件消失了）。
	// 这不是「迁移失败」而是「部署包不对」，同样走 [MIGRATE][FATAL] 通道。
	// 判据纯加法、不需要理解 SQL：两个集合对不上就是包不对。
	//
	// 放在 run 之前返回错误：此时目录已经不可信，继续应用「看起来待执行」的文件
	// 多半是拿旧包去改新库。
	if missing := missingVersions(done, files); len(missing) > 0 {
		return nil, fmt.Errorf(
			"部署包不完整：记账表里 %d 个已应用版本在 migrations 目录（%s）中不存在：%s。"+
				"极可能升级时只替换了二进制、没同步 migrations/ 目录——"+
				"请把本版本的 migrations/ 与二进制一起部署后重启（已应用的不会重放）",
			len(missing), dir, strings.Join(missing, ", "))
	}

	var applied []string
	run := func() error {
		for _, name := range files {
			if done[name] {
				continue
			}
			if err := applyFile(db, dir, name); err != nil {
				return err
			}
			if err := recordVersion(db, name); err != nil {
				return err
			}
			applied = append(applied, name)
		}
		return nil
	}
	// MySQL 多副本并发保护；SQLite（无 GET_LOCK、也不支持多副本）直接执行
	if err := withMySQLLock(db, run); err != nil {
		return applied, err
	}
	return applied, nil
}

// missingFromManifest 返回「本版本清单里有、目录里没有」的文件（按清单顺序）。
// 用于识别「目录整体停留在旧版本」（见 ApplyWithReport 守门 1）。
func missingFromManifest(files []string) []string {
	inDir := make(map[string]bool, len(files))
	for _, f := range files {
		inDir[f] = true
	}
	var missing []string
	for _, name := range expectedMigrations {
		if !inDir[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// unknownInDir 返回「目录里有、清单里没有」的文件（按字典序）。
// 语义是「二进制落后于目录」，只告警不阻断（见 ApplyWithReport）。
func unknownInDir(files []string) []string {
	known := make(map[string]bool, len(expectedMigrations))
	for _, name := range expectedMigrations {
		known[name] = true
	}
	var extra []string
	for _, f := range files {
		if !known[f] {
			extra = append(extra, f)
		}
	}
	sort.Strings(extra)
	return extra
}

// missingVersions 返回「记账表里有、目录里没有」的版本（按字典序）。
// 用于部署包完整性守门（见 ApplyWithReport）。目录里多出的文件是正常的
// （待应用的增量），只有反向缺失才说明包不对。
func missingVersions(done map[string]bool, files []string) []string {
	inDir := make(map[string]bool, len(files))
	for _, f := range files {
		inDir[f] = true
	}
	var missing []string
	for v := range done {
		if !inDir[v] {
			missing = append(missing, v)
		}
	}
	sort.Strings(missing)
	return missing
}

// applyFile 执行单个迁移文件的全部语句；任一句失败（非可忽略错误）即中止，
// 调用方据此不记录该版本——下次启动会重试，而不是把半个迁移记成已完成。
func applyFile(db *gorm.DB, dir, name string) error {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("读取 %s 失败：%v", name, err)
	}
	for _, stmt := range strings.Split(string(raw), ";") {
		s := strings.TrimSpace(stmt)
		if s == "" || isCommentOnly(s) {
			continue
		}
		if err := db.Exec(s).Error; err != nil {
			if isIgnored(err) {
				continue
			}
			return fmt.Errorf("应用 %s 失败：%v", name, err)
		}
	}
	return nil
}

// appliedVersions 确保记账表存在并返回已应用的版本集合。
// 表不存在时先建（IF NOT EXISTS）——这是「存量库首次被本工具接管」的常态，
// 返回空集合，随后按幂等重放并补记版本。
func appliedVersions(db *gorm.DB) (map[string]bool, error) {
	create := "CREATE TABLE IF NOT EXISTS " + migrationsTable + " (" +
		"version VARCHAR(64) NOT NULL PRIMARY KEY, " +
		"applied_at DATETIME NOT NULL" +
		")"
	// ENGINE / CHARSET 是 MySQL 方言，SQLite 不认；记账表建表要两种形态都能执行
	// （生产只有 MySQL，但单测用 WEICHUAN_MIGRATIONS_DIR + SQLite 就能覆盖记账逻辑，
	//  不必依赖真实 MySQL）
	if isMySQL(db) {
		create += " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	}
	if err := db.Exec(create).Error; err != nil {
		return nil, fmt.Errorf("创建迁移记账表失败：%v", err)
	}
	type row struct {
		Version string
	}
	var rows []row
	if err := db.Raw("SELECT version FROM " + migrationsTable).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取迁移记账表失败：%v", err)
	}
	done := make(map[string]bool, len(rows))
	for _, r := range rows {
		done[r.Version] = true
	}
	return done, nil
}

// recordVersion 把一个迁移文件记为已应用（重复执行靠 Duplicate entry 忽略）。
func recordVersion(db *gorm.DB, version string) error {
	err := db.Exec("INSERT INTO "+migrationsTable+" (version, applied_at) VALUES (?, ?)",
		version, nowFunc()).Error
	if err != nil && !isIgnored(err) {
		return fmt.Errorf("记录迁移版本 %s 失败：%v", version, err)
	}
	return nil
}

// isMySQL 是否 MySQL 方言（决定记账表 DDL 形态与是否加迁移锁）。
func isMySQL(db *gorm.DB) bool {
	return db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql"
}

// withMySQLLock 在 MySQL 上用 GET_LOCK 串行化迁移；非 MySQL（SQLite）直接执行 fn。
// 拿不到锁时返回错误：另一个副本正在迁移，本副本跳过即可（下次启动仍会补齐）。
func withMySQLLock(db *gorm.DB, fn func() error) error {
	if !isMySQL(db) {
		return fn()
	}
	var got int
	if err := db.Raw("SELECT GET_LOCK(?, ?)", lockName, lockTimeoutSec).Scan(&got).Error; err != nil {
		return fmt.Errorf("获取迁移锁失败：%v", err)
	}
	if got != 1 {
		return fmt.Errorf("获取迁移锁超时（%ds）：另一实例正在应用迁移", lockTimeoutSec)
	}
	defer func() {
		// 释放失败不致命：GET_LOCK 是连接级的，连接关闭时自动释放；
		// 但连接池可能复用这条连接，所以仍然显式释放，失败要留痕
		if err := db.Exec("SELECT RELEASE_LOCK(?)", lockName).Error; err != nil {
			log.Printf("[WARN] 释放迁移锁失败（连接关闭后会自动释放）：%v", err)
		}
	}()
	return fn()
}

// isCommentOnly 整段都是注释行（-- 开头）时没有可执行语句。
// 注意：naive 按 ";" 切分后，注释可能和半条语句在同一段里，
// 那种情况直接交给 MySQL 报错（不会发生——迁移文件的分号只在语句末尾）。
func isCommentOnly(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return false
		}
	}
	return true
}

func isIgnored(err error) bool {
	msg := err.Error()
	for _, want := range ignoredErrs {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}
