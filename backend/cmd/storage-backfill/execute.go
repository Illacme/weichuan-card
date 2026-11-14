package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"weichuan/internal/dbopen"
	"weichuan/internal/pii"
	"weichuan/internal/refmigrate"

	"gorm.io/gorm"
)

// 本文件是 P3 执行器的命令行层。所有「判断」都在 internal/refmigrate 里，
// 这里只做参数、打印与退出码——否则状态机的单测覆盖不到命令行为。

// localSource 是「平台侧现有对象」的本地盘源。本地盘与真桶两种目标共用它。
// 目录必须存在且是目录：不存在时 NewLocalStore 仍会返回实例，直到 preflight
// 才报「源对象不存在」——那时错误看上去像数据问题，其实是参数写错了。
func localSource(srcDir string) (*refmigrate.LocalStore, error) {
	if strings.TrimSpace(srcDir) == "" {
		return nil, fmt.Errorf("apply 必须给 --source-dir（平台侧现有对象所在的本地目录）")
	}
	fi, err := os.Stat(srcDir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("源目录 %q 不存在或不是目录：%v", srcDir, err)
	}
	return refmigrate.NewLocalStore(srcDir)
}

// sourceFor 是**唯一的源选择口**。目标端（local / tenant）与源端（local / s3）
// 正交：四种组合都合法，例如「平台 S3 桶 → 本地盘」可用于离线演练核对字节。
func sourceFor(sourceStore, srcDir string, db *gorm.DB) (refmigrate.ObjectStore, error) {
	switch sourceStore {
	case "local":
		return localSource(srcDir)
	case "s3":
		// 两个源同时给出时无法判断哪个生效：--source-dir 里可能根本没有该对象，
		// 但命令看起来像「读过了」，这种歧义必须在参数层就挡掉。
		if strings.TrimSpace(srcDir) != "" {
			return nil, fmt.Errorf("--source-store=s3 时不要给 --source-dir：" +
				"两者同时出现无法判断哪个生效，S3 源绝不读本地源目录")
		}
		cfg, err := platformSourceConfig(db)
		if err != nil {
			return nil, err
		}
		src, err := refmigrate.NewPlatformS3Source(cfg)
		if err != nil {
			// 不回退本地盘：那会拿一个空目录当源，把所有对象报成「不存在」。
			return nil, fmt.Errorf("平台 S3 源不可用（不回退本地盘）：%w", err)
		}
		return src, nil
	default:
		return nil, fmt.Errorf("未知 --source-store=%q（可用：local / s3）", sourceStore)
	}
}

// localStores 是离线演练用的本地盘目标。
func localStores(src refmigrate.ObjectStore, dstDir string) (*refmigrate.Stores, error) {
	if strings.TrimSpace(dstDir) == "" {
		return nil, fmt.Errorf("--target-store=local 必须给 --target-dir")
	}
	dst, err := refmigrate.NewLocalStore(dstDir)
	if err != nil {
		return nil, err
	}
	return &refmigrate.Stores{
		Source: src,
		TargetFor: func(cid int64) (refmigrate.ObjectStore, error) {
			// 本地适配器：各租户共用同一根目录，键自带 t/<cid>/ 前缀故不串号。
			_ = cid
			return dst, nil
		},
	}, nil
}

// tenantStores 是真桶目标：按 company_id 解密配置得到的客户桶。
//
// 刻意不提供「目标没配就回退到平台盘」这种便利：那会复制出一堆看似成功的对象，
// 库也改成 t/<cid>/...，但真正的客户桶里一个都没有。配置缺失必须显式报错。
func tenantStores(src refmigrate.ObjectStore, db *gorm.DB, dstDir string) (*refmigrate.Stores, error) {
	if strings.TrimSpace(dstDir) != "" {
		return nil, fmt.Errorf("--target-store=tenant 时不要给 --target-dir：" +
			"两者同时出现无法判断哪个生效，真桶模式绝不读本地目标目录")
	}
	// 凭据解密依赖与写配置行时同一把钥匙。用内置 dev 密钥去解生产库里的密文
	// 会以 GCM 认证失败告终，但报错像「数据被篡改」；这里先把真正的原因说清楚。
	if pii.UsingPlaceholderKey() {
		return nil, fmt.Errorf("--target-store=tenant 必须显式设置 %s（与写 tenant_storage_configs 时同一把钥匙），"+
			"当前在用内置 dev 密钥", pii.KeyEnvVar)
	}
	return &refmigrate.Stores{
		Source:    src,
		TargetFor: refmigrate.TenantTargetFor(db, ""),
	}, nil
}

// buildStores 是唯一的参数 → 适配器分派口。两个默认值都保持 local，
// 让第六十五轮的既有命令一字不改也照常跑。
func buildStores(sourceStore, targetStore, srcDir, dstDir string, db *gorm.DB) (*refmigrate.Stores, error) {
	src, err := sourceFor(sourceStore, srcDir, db)
	if err != nil {
		return nil, err
	}
	switch targetStore {
	case "local":
		return localStores(src, dstDir)
	case "tenant":
		return tenantStores(src, db, dstDir)
	default:
		return nil, fmt.Errorf("未知 --target-store=%q（可用：local / tenant）", targetStore)
	}
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	var planPath, dsn, srcDir, dstDir, sourceStore, targetStore string
	var dryRun, yes, force bool
	fs.StringVar(&planPath, "plan", "", "计划 JSON（必填）")
	fs.StringVar(&dsn, "dsn", "", "数据库 DSN（必填）")
	fs.StringVar(&srcDir, "source-dir", "", "source-store=local 时平台侧现有对象的本地目录")
	fs.StringVar(&dstDir, "target-dir", "", "target-store=local 时的目标对象根目录")
	fs.StringVar(&sourceStore, "source-store", "local",
		"源存储：local（平台本地盘，需 --source-dir）/ s3（平台自有 S3 桶，配置来自 env/CONFIG_FILE/sys_setting）")
	fs.StringVar(&targetStore, "target-store", "local",
		"目标存储：local（本地盘，需 --target-dir）/ tenant（按 company_id 解密配置走客户真桶）")
	fs.BoolVar(&dryRun, "dry-run", false, "只预检")
	fs.BoolVar(&yes, "yes", false, "确认真的执行")
	fs.BoolVar(&force, "force", false, "目标对象大小不符时用源覆盖")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(planPath) == "" || strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("必须同时给 --plan 与 --dsn")
	}
	pf, err := refmigrate.ReadPlan(planPath)
	if err != nil {
		return err
	}
	db, driver, err := dbopen.Open(dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败：%w", err)
	}
	stores, err := buildStores(sourceStore, targetStore, srcDir, dstDir, db)
	if err != nil {
		return err
	}
	dry := dryRun || !yes
	res, err := refmigrate.Apply(db, pf, stores, refmigrate.ApplyOptions{DryRun: dry, Force: force})
	printPreflight(pf, driver, res)
	if err != nil {
		return err
	}
	if dry {
		fmt.Printf("\n【未写入任何数据】预检通过；确认无误后加 --yes 执行，或用 --dry-run 只做预检。\n")
		return nil
	}
	fmt.Printf("\n✅ 已复制 %d 个对象、改写 %d 条引用（跳过 %d 个对象 / %d 条已搬完的引用）。\n",
		res.ObjectsCopied, res.RefsUpdated, res.ObjectsSkipped, res.RefsSkipped)
	fmt.Printf("   台账 plan_hash=%s；回滚：storage-backfill rollback --plan %s --dsn … --yes\n",
		pf.Hash[:12], planPath)
	return nil
}

func printPreflight(pf *refmigrate.PlanFile, driver string, res *refmigrate.ApplyResult) {
	fmt.Printf("== P3 搬迁预检 ==\n库：%s · 计划：%s\nplan_hash：%s\n\n", driver, pf.Path, pf.Hash)
	if res == nil || res.Preflight == nil {
		return
	}
	copyN, skipN, overwriteN := 0, 0, 0
	for _, o := range res.Preflight.Objects {
		switch o.Action {
		case refmigrate.ObjCopy:
			copyN++
		case refmigrate.ObjSkip:
			skipN++
		case refmigrate.ObjOverwrite:
			overwriteN++
		}
	}
	updateN, doneN := 0, 0
	for _, r := range res.Preflight.Refs {
		if r.Action == refmigrate.RefUpdate {
			updateN++
		} else {
			doneN++
		}
	}
	fmt.Printf("对象 %d 个：复制 %d · 已存在跳过 %d · 覆盖 %d\n",
		len(res.Preflight.Objects), copyN, skipN, overwriteN)
	fmt.Printf("引用 %d 条：待改写 %d · 已搬完 %d\n\n", len(res.Preflight.Refs), updateN, doneN)
	shown := 0
	for _, o := range res.Preflight.Objects {
		if o.Action == refmigrate.ObjSkip {
			continue
		}
		if shown >= 5 {
			fmt.Printf("  …（其余见 status / 计划 JSON）\n")
			break
		}
		fmt.Printf("  [%s] %s → %s\n", o.Action, o.Group.SourceKey, o.Group.TargetKey)
		shown++
	}
}

func runRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	var planPath, dsn string
	var dryRun, yes bool
	fs.StringVar(&planPath, "plan", "", "计划 JSON（必填）")
	fs.StringVar(&dsn, "dsn", "", "数据库 DSN（必填）")
	fs.BoolVar(&dryRun, "dry-run", false, "只预检")
	fs.BoolVar(&yes, "yes", false, "确认真的执行")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(planPath) == "" || strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("必须同时给 --plan 与 --dsn")
	}
	pf, err := refmigrate.ReadPlan(planPath)
	if err != nil {
		return err
	}
	db, driver, err := dbopen.Open(dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败：%w", err)
	}
	dry := dryRun || !yes
	res, err := refmigrate.Rollback(db, pf, dry)
	if err != nil {
		return err
	}
	fmt.Printf("== P3 回滚 ==\n库：%s · 计划：%s\nplan_hash：%s\n", driver, pf.Path, pf.Hash)
	fmt.Printf("台账里待回滚 %d 条（只改库，**不删除任何对象**）\n", res.Total)
	if dry {
		fmt.Printf("\n【未写入任何数据】确认无误后加 --yes 执行。\n")
		return nil
	}
	fmt.Printf("\n✅ 已把 %d 条引用改回原值；对象保留在目标位置（R5）。\n", res.Reverted)
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var planPath, dsn string
	fs.StringVar(&planPath, "plan", "", "计划 JSON（必填）")
	fs.StringVar(&dsn, "dsn", "", "数据库 DSN（必填）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(planPath) == "" || strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("必须同时给 --plan 与 --dsn")
	}
	pf, err := refmigrate.ReadPlan(planPath)
	if err != nil {
		return err
	}
	db, driver, err := dbopen.Open(dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败：%w", err)
	}
	counts, total, err := refmigrate.Status(db, pf.Hash)
	if err != nil {
		return err
	}
	fmt.Printf("== P3 台账 ==\n库：%s · 计划：%s\nplan_hash：%s\n合计 %d 行\n", driver, pf.Path, pf.Hash, total)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-12s %d\n", k, counts[k])
	}
	if total == 0 {
		fmt.Printf("  （没有记录：这份计划还没 apply 过）\n")
	}
	return nil
}
