// Command nginx-snippets 把租户存储配置渲染成 nginx snippet，并做两道前置检查。
//
// 用法：
//
//	nginx-snippets render --dsn 'root:pw@tcp(127.0.0.1:3306)/weichuan' [--source auto|legacy|profiles] [--out FILE] [--dns-precheck]
//	nginx-snippets render --targets tenants.json [--out FILE] [--dns-precheck]
//	nginx-snippets check  --dsn '...' [--source auto|legacy|profiles] --snippet FILE [--dns-precheck]
//	nginx-snippets check  --targets tenants.json --snippet FILE [--dns-precheck]
//
// 数据源两个二选一，**必须显式给**（详见 resolveTargets 注释）：`--dsn` 从数据库读，
// `--targets` 读本地 JSON（演练 / 应急手改）。
//
// `--dsn` 下还有第三层选择 `--source`（第七十四轮）：
//
//	auto     默认。存储档案（storage_profiles + storage_bindings）优先，
//	         旧表 tenant_storage_configs 给没有档案绑定的企业补位。
//	legacy   只读旧表，行为与本轮之前完全一致（回滚用）。
//	profiles 只读档案。档案表缺失直接报错，不降级。
//
// 为什么 auto 是默认：接入层必须跟着「谁在写对象」走。档案绑定改了之后，
// 上传会写进新桶，而 snippet 若还指着旧桶，那家租户的图会全部 404——
// 这是本轮之前真实存在的断层（见 docs/tenant-storage-design.md §0.1 阶段二）。
//
// 两个子命令都**不写 nginx、不 reload**：只算、只写文件、只报错。
// 真正 reload 由运维（或后续的配置接口）决定，这条边界是刻意的——
// 一个会自动 reload 线上接入层的工具，出问题时的爆炸半径是整站。
//
// 唯一的写动作是 `--out` 指定文件的原子替换（writeFileAtomic），
// 且在任何校验失败时都不发生。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"weichuan/internal/dbopen"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/repository"
)

// 数据源枚举。默认 auto：接入层必须跟着对象实际落在哪走，见文件头。
const (
	sourceAuto     = "auto"
	sourceLegacy   = "legacy"
	sourceProfiles = "profiles"
)

// targetFile 是输入文件形态。允许两种：{"tenants":[...]} 与裸数组 [...]
// （后者是为了能直接从 SQL 导出拼出来，少一步包装）。
type targetFile struct {
	Tenants []nginxsnippet.Target `json:"tenants"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = runRender(os.Args[2:])
	case "check":
		err = runCheck(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("未知子命令 %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `nginx-snippets —— 租户对象桶 → nginx snippet

  render --dsn DSN     [--source auto|legacy|profiles] [--out FILE] [--dns-precheck] [--allow-empty]
  render --targets FILE [--out FILE] [--dns-precheck] [--allow-empty]
      渲染 snippet。不带 --out 时写 stdout（便于管道/比对）。

  check  --dsn DSN      [--source auto|legacy|profiles] --snippet FILE [--dns-precheck]
  check  --targets FILE --snippet FILE [--dns-precheck]
      只对账，不改任何文件。配置表与 snippet 不一致即非零退出。

数据源二选一，必须显式给：
  --dsn     从数据库读（生产用这个，MySQL 或 SQLite 路径）
  --targets 读本地 JSON {"tenants":[{companyId,driver,endpoint,bucket,useSsl,hostHeader,enabled}]}

--source 只对 --dsn 生效，决定读哪张表：
  auto     默认。存储档案（storage_profiles + storage_bindings）优先，
           旧表 tenant_storage_configs 给没有档案绑定的企业补位。
  legacy   只读旧表，行为与本轮之前完全一致（需要回滚时用）。
  profiles 只读档案。档案表缺失直接报错，不降级。

--allow-empty：本次渲染结果不含任何 location 时，允许覆盖已存在的非空 --out
              文件（等于把所有租户撕回平台盘，默认拒绝）。
`)
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	targetsPath := fs.String("targets", "", "租户配置 JSON（与 --dsn 互斥）")
	dsn := fs.String("dsn", "", "数据库 DSN（与 --targets 互斥）")
	source := fs.String("source", sourceAuto, "数据源：auto（档案优先）/ legacy（只读旧表）/ profiles（只读档案）")
	outPath := fs.String("out", "", "输出文件；留空写 stdout")
	dns := fs.Bool("dns-precheck", false, "落盘前先解析每个桶域名（推荐开启）")
	allowEmpty := fs.Bool("allow-empty", false, "允许用「零 location」的结果覆盖已存在的非空输出文件")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets, err := resolveTargets(*targetsPath, *dsn, *source)
	if err != nil {
		return err
	}
	if *dns {
		if fails := nginxsnippet.Precheck(context.Background(), nil, targets); len(fails) > 0 {
			return precheckError(fails)
		}
	}

	out, err := nginxsnippet.Render(targets)
	if err != nil {
		return err
	}

	// 摘要先算出来，落盘守卫和收尾提示都要用。
	var taken, skipped []int64
	for _, t := range targets {
		if t.NeedsSnippet() {
			taken = append(taken, t.CompanyID)
		} else {
			skipped = append(skipped, t.CompanyID)
		}
	}

	// 「渲染结果零 location」+「目标文件已有内容」= 一次静默的线上清空。
	//
	// 这不是假想：配置表被误清、DSN 指到空库、容器里挂错卷，三种情况都会
	// 走到这里，而它们各自的报错都不明显。默认拒绝，要清空必须显式 --allow-empty。
	if *outPath != "" && len(taken) == 0 && !*allowEmpty {
		if st, err := os.Stat(*outPath); err == nil && st.Size() > 0 {
			return fmt.Errorf(
				"本次渲染没有任何 location，但 %s 已存在且非空（%d 字节）：这会把现有租户的 "+
					"location 全部抹掉，所有 /uploads/t/* 回落平台盘。确认要清空请加 --allow-empty",
				*outPath, st.Size())
		}
	}

	if *outPath == "" {
		fmt.Print(out)
	} else if err := writeFileAtomic(*outPath, []byte(out)); err != nil {
		return err
	}

	// 摘要走 stderr：stdout 要保持可管道（生产上有人会 `render > x && diff`）。
	fmt.Fprintf(os.Stderr, "已接管（生成 location）：%v\n", taken)
	fmt.Fprintf(os.Stderr, "未接管（回落平台盘）：%v\n", skipped)
	if len(taken) == 0 {
		fmt.Fprintln(os.Stderr, "⚠️  没有任何租户接管存储：整份 snippet 只有注释，所有 /uploads/ 走回退段")
	}
	if *dns {
		fmt.Fprintf(os.Stderr, "DNS 预检：%d 个域名全部可解析\n", len(taken))
	}
	return nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	targetsPath := fs.String("targets", "", "租户配置 JSON（与 --dsn 互斥）")
	dsn := fs.String("dsn", "", "数据库 DSN（与 --targets 互斥）")
	source := fs.String("source", sourceAuto, "数据源：auto（档案优先）/ legacy（只读旧表）/ profiles（只读档案）")
	snippetPath := fs.String("snippet", "", "已生成的 snippet（必填）")
	dns := fs.Bool("dns-precheck", false, "顺带解析每个桶域名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *snippetPath == "" {
		return fmt.Errorf("check 需要 --snippet")
	}

	targets, err := resolveTargets(*targetsPath, *dsn, *source)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(*snippetPath)
	if err != nil {
		return err
	}
	d := nginxsnippet.Reconcile(targets, string(body))
	if !d.OK() {
		return fmt.Errorf("%v", d)
	}
	fmt.Fprintf(os.Stderr, "✓ %v\n", d)
	if *dns {
		if fails := nginxsnippet.Precheck(context.Background(), nil, targets); len(fails) > 0 {
			return precheckError(fails)
		}
		fmt.Fprintln(os.Stderr, "✓ DNS 预检通过")
	}
	return nil
}

func precheckError(fails []nginxsnippet.PrecheckFailure) error {
	var b strings.Builder
	b.WriteString("桶域名解析失败，拒绝生成（否则 nginx -t 会 emerg，卡住整份配置更新）：")
	for _, f := range fails {
		b.WriteString("\n  · ")
		b.WriteString(f.Error())
	}
	return fmt.Errorf("%s", b.String())
}

func loadTargets(path string) ([]nginxsnippet.Target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "[") {
		var list []nginxsnippet.Target
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s 解析失败：%w", path, err)
		}
		return list, nil
	}
	var f targetFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s 解析失败：%w", path, err)
	}
	return f.Tenants, nil
}

// resolveTargets 选数据源。**必须显式给一个**，两个都不行。
//
// 为什么不允许「能连库就连库，连不上就退回 JSON」，也不允许从环境变量
// （DB_DSN / DATABASE_URL）自动取：
//
//	这份输出决定线上哪些 /uploads/t/<cid>/ 会被代理到哪个桶。数据源不确定，
//	等于「这次生成用的是哪份配置」这个问题无法回答——出事后复盘的第一步就断了。
//	所以宁可让运维多敲一次 --dsn。
//
// 两个都给的害处更具体：--targets 很容易是上一次演练留下的文件，两份同时存在时
// 工具不知道该信谁，而两种选择都会「成功」并产出一份看着正常的 snippet。
//
// --source 只对 --dsn 有意义：--targets 读的是现成 JSON，里面没有「档案」这一层。
// 显式给非 auto 时直接拒绝，而不是静默忽略——运维以为自己在读档案、实际读的是
// 演练文件，是最坏的一种「成功」。
func resolveTargets(targetsPath, dsn, source string) ([]nginxsnippet.Target, error) {
	src, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	switch {
	case targetsPath == "" && dsn == "":
		return nil, fmt.Errorf("必须指定数据源：--dsn（从数据库读）或 --targets（本地 JSON）")
	case targetsPath != "" && dsn != "":
		return nil, fmt.Errorf("--targets 与 --dsn 互斥：两份配置同时给，无法回答「这次生成用的是哪一份」")
	case targetsPath != "":
		if src != sourceAuto {
			return nil, fmt.Errorf("--source=%s 只对 --dsn 生效：--targets 读的是现成 JSON，没有档案这一层", src)
		}
		return loadTargets(targetsPath)
	default:
		return loadTargetsFromDB(dsn, src)
	}
}

// parseSource 校验并归一化 --source。空串按 auto 处理（flag 有默认值，这里兜手填）。
func parseSource(v string) (string, error) {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "", sourceAuto:
		return sourceAuto, nil
	case sourceLegacy, sourceProfiles:
		return s, nil
	default:
		return "", fmt.Errorf("--source 只接受 auto / legacy / profiles，收到 %q", v)
	}
}

// loadTargetsFromDB 从数据库读真实配置，按 source 决定读哪几张表。
//
// 三条纪律：
//
//  1. **失败一律返回错误，绝不退化成空列表**。空列表 + nil 会让 render 产出一份
//     「只有注释」的 snippet 并落盘，等于把所有租户撕回平台盘——而 logs 里
//     只有一条 warning 级别的「表不存在」（见 repository.ListStorageTargets）。
//  2. 表缺失的错误原样上抛（ErrStorageConfigMissing / ErrProfileTablesMissing
//     自带处置步骤），不在这里改写文案：处置步骤写在一处，改的时候不会漏。
//  3. 0 行是**合法状态**，但必须把后果说到明处：它和「查询失败」的返回形状
//     必须能被人分辨出来（一个 err=nil、一个 err!=nil）。
//
// auto 下唯一允许的「降级」是档案表整体缺失（迁移 0054 还没跑）：那说明这个部署
// 本来就没有档案，退到旧表不是降级而是实况。**单个绑定指向坏档案不在此列**，
// 那种情况必须报错——静默跳过等于让那家悄悄回落平台盘。
func loadTargetsFromDB(dsn, source string) ([]nginxsnippet.Target, error) {
	db, driver, err := dbopen.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败（driver=%s）：%w", driver, err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer func() { _ = sqlDB.Close() }() // 命令行工具，读一次就退出；不判关闭错误
	}

	var legacy []repository.StorageTarget
	if source != sourceProfiles {
		legacy, err = repository.ListStorageTargets(db)
		if err != nil {
			// ErrStorageConfigMissing 也不加前缀：它的文案已经是完整处置步骤，
			// 再包一层只会把「先跑 ./server migrate」埋进长句子中间。
			return nil, err
		}
	}

	var prof []repository.ProfileTarget
	if source != sourceLegacy {
		prof, err = repository.ListProfileTargets(db)
		if err != nil {
			if errors.Is(err, repository.ErrProfileTablesMissing) && source == sourceAuto {
				fmt.Fprintf(os.Stderr, "⚠️  %v\n    --source=auto 本次只读 tenant_storage_configs\n", err)
			} else {
				return nil, err
			}
		}
	}

	out := mergeTargets(legacy, prof, source)
	reportSource(driver, source, len(legacy), len(prof), len(out))
	if source != sourceProfiles {
		reportUnhealthy(legacy)
	}
	if source != sourceLegacy {
		reportProfileUnhealthy(prof)
	}
	return out, nil
}

// mergeTargets 合并两份数据源：档案优先，旧表给没有档案绑定的企业补位。
//
// 关键一条：**档案里 driver=local 的企业必须覆盖掉旧表那一行**。那家企业被明确
// 绑到了本地档案（对象在平台盘上），旧表里若还留着一条指向客户桶的启用行，
// 补位逻辑会把 location 生成出来——于是 nginx 去客户桶取一个根本不存在的对象。
// 覆盖而不是「跳过」，正是为了让 local 也能表达「别生成」。
func mergeTargets(legacy []repository.StorageTarget, prof []repository.ProfileTarget, source string) []nginxsnippet.Target {
	byCompany := make(map[int64]nginxsnippet.Target, len(legacy)+len(prof))
	if source != sourceProfiles {
		for _, l := range legacy {
			byCompany[l.CompanyID] = l.NginxTarget()
		}
	}
	if source != sourceLegacy {
		for _, p := range prof {
			byCompany[p.CompanyID] = p.Target
		}
	}

	ids := make([]int64, 0, len(byCompany))
	for cid := range byCompany {
		ids = append(ids, cid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]nginxsnippet.Target, 0, len(ids))
	for _, cid := range ids {
		out = append(out, byCompany[cid])
	}
	return out
}

// reportSource 把「这次用的是哪份配置」打到 stderr。
//
// 这不是装饰：出事后复盘的第一句话就是它。auto 下必须同时报出两边的行数，
// 否则「档案 3 家」看不出旧表补了几家，也就没法判断某条 location 的来源。
func reportSource(driver, source string, legacyRows, profileRows, merged int) {
	switch source {
	case sourceProfiles:
		fmt.Fprintf(os.Stderr, "数据源：%s（storage_bindings 解析出 %d 家）\n", driver, profileRows)
	case sourceLegacy:
		fmt.Fprintf(os.Stderr, "数据源：%s（tenant_storage_configs 共 %d 行）\n", driver, legacyRows)
	default:
		fmt.Fprintf(os.Stderr,
			"数据源：%s（auto：tenant_storage_configs %d 行 + 存储档案 %d 家 → 合并 %d 家，档案优先）\n",
			driver, legacyRows, profileRows, merged)
	}
}

// reportProfileUnhealthy 把自检失败 / 从没自检过的档案列到 stderr（按档案名去重）。
//
// 与 reportUnhealthy 同一条理由：不阻断生成（桶暂时探不通不代表 location 该消失，
// 消失反而让已缓存的图全 404），但必须让运维看见。
func reportProfileUnhealthy(targets []repository.ProfileTarget) {
	failed := map[string]bool{}
	unchecked := map[string]bool{}
	for _, t := range targets {
		switch t.ProfileStatus {
		case "error":
			failed[t.ProfileName] = true
		case "", "unknown":
			unchecked[t.ProfileName] = true
		}
	}
	if names := sortedKeys(failed); len(names) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  以下存储档案自检失败（status=error），snippet 仍会生成：%v\n", names)
	}
	if names := sortedKeys(unchecked); len(names) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  以下存储档案从未完成自检（status 为空或 unknown）：%v\n", names)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reportUnhealthy 把自检失败 / 从没自检过的租户列到 stderr。
//
// 不阻断生成：一个桶暂时探不通不代表 snippet 该消失（消失反而让已缓存的图全 404）。
// 但也不能不说——「配置写完了、忘了点自检」和「昨天开始就坏了」在后台列表上
// 长得一样，只有把它抖到运维面前才可能被处理。
func reportUnhealthy(bindings []repository.StorageTarget) {
	var failed, unchecked []int64
	for _, b := range bindings {
		if !b.Enabled {
			continue // 停用的行本来就不生成 location，状态无意义
		}
		switch b.Status {
		case "error":
			failed = append(failed, b.CompanyID)
		case "", "unknown":
			unchecked = append(unchecked, b.CompanyID)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  以下租户的存储自检失败（status=error），snippet 仍会生成：%v\n", failed)
	}
	if len(unchecked) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  以下租户从未完成自检（status 为空或 unknown）：%v\n", unchecked)
	}
}

// writeFileAtomic 先写同目录临时文件再 rename。
//
// 为什么不直接 os.WriteFile：这份文件是 nginx 的 include 目标。写到一半被打断
// （磁盘满 / Ctrl-C / OOM 被杀）会留下半截配置——最坏的形态是某段 location 的
// `{` 已写出、`}` 还没写，nginx -t 直接 emerg，下一次 reload 卡住，而运维看到的
// 是「文件明明更新过」。rename 在同一文件系统内是原子的：读方要么看到旧内容，
// 要么看到新内容，不存在中间态。
//
// 临时文件必须建在**目标同目录**：跨文件系统的 rename 会走 copy + unlink，
// 又退回非原子。用 CreateTemp 的随机后缀避免并发两个进程互相覆盖。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nginx-snippets-*.tmp")
	if err != nil {
		return fmt.Errorf("在 %s 创建临时文件失败（目录不存在或不可写？）：%w", dir, err)
	}
	tmpName := tmp.Name()
	// 成功路径下 rename 已经把名字取走，这里的 Remove 只会命中失败路径。
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写临时文件失败：%w", err)
	}
	// Sync 后再 rename：否则断电时可能 rename 已生效而内容还在页缓存里，
	// 得到一个长度正确、内容全零的 snippet。
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush 临时文件失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败：%w", err)
	}
	// CreateTemp 建的是 0600，nginx worker 读不到；恢复成原来的 0644。
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("设置文件权限失败：%w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换 %s 失败：%w", path, err)
	}
	return nil
}
