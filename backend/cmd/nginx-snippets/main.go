// Command nginx-snippets 把租户存储配置渲染成 nginx snippet，并做两道前置检查。
//
// 用法：
//
//	nginx-snippets render --dsn 'root:pw@tcp(127.0.0.1:3306)/weichuan' [--out FILE] [--dns-precheck]
//	nginx-snippets render --targets tenants.json [--out FILE] [--dns-precheck]
//	nginx-snippets check  --dsn '...' --snippet FILE [--dns-precheck]
//	nginx-snippets check  --targets tenants.json --snippet FILE [--dns-precheck]
//
// 数据源两个二选一，**必须显式给**（详见 resolveTargets 注释）：`--dsn` 从
// tenant_storage_configs 读真实配置，`--targets` 读本地 JSON（演练 / 应急手改）。
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
	"strings"

	"weichuan/internal/dbopen"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/repository"
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

  render --dsn DSN     [--out FILE] [--dns-precheck] [--allow-empty]
  render --targets FILE [--out FILE] [--dns-precheck] [--allow-empty]
      渲染 snippet。不带 --out 时写 stdout（便于管道/比对）。

  check  --dsn DSN      --snippet FILE [--dns-precheck]
  check  --targets FILE --snippet FILE [--dns-precheck]
      只对账，不改任何文件。配置表与 snippet 不一致即非零退出。

数据源二选一，必须显式给：
  --dsn     从 tenant_storage_configs 读（生产用这个，MySQL 或 SQLite 路径）
  --targets 读本地 JSON {"tenants":[{companyId,driver,endpoint,bucket,useSsl,hostHeader,enabled}]}

--allow-empty：本次渲染结果不含任何 location 时，允许覆盖已存在的非空 --out
              文件（等于把所有租户撕回平台盘，默认拒绝）。
`)
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	targetsPath := fs.String("targets", "", "租户配置 JSON（与 --dsn 互斥）")
	dsn := fs.String("dsn", "", "数据库 DSN（与 --targets 互斥）")
	outPath := fs.String("out", "", "输出文件；留空写 stdout")
	dns := fs.Bool("dns-precheck", false, "落盘前先解析每个桶域名（推荐开启）")
	allowEmpty := fs.Bool("allow-empty", false, "允许用「零 location」的结果覆盖已存在的非空输出文件")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets, err := resolveTargets(*targetsPath, *dsn)
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
	snippetPath := fs.String("snippet", "", "已生成的 snippet（必填）")
	dns := fs.Bool("dns-precheck", false, "顺带解析每个桶域名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *snippetPath == "" {
		return fmt.Errorf("check 需要 --snippet")
	}

	targets, err := resolveTargets(*targetsPath, *dsn)
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
func resolveTargets(targetsPath, dsn string) ([]nginxsnippet.Target, error) {
	switch {
	case targetsPath == "" && dsn == "":
		return nil, fmt.Errorf("必须指定数据源：--dsn（从 tenant_storage_configs 读）或 --targets（本地 JSON）")
	case targetsPath != "" && dsn != "":
		return nil, fmt.Errorf("--targets 与 --dsn 互斥：两份配置同时给，无法回答「这次生成用的是哪一份」")
	case targetsPath != "":
		return loadTargets(targetsPath)
	default:
		return loadTargetsFromDB(dsn)
	}
}

// loadTargetsFromDB 从 tenant_storage_configs 读真实配置。
//
// 三条纪律：
//
//  1. **失败一律返回错误，绝不退化成空列表**。空列表 + nil 会让 render 产出一份
//     「只有注释」的 snippet 并落盘，等于把所有租户撕回平台盘——而 logs 里
//     只有一条 warning 级别的「表不存在」（见 repository.ListStorageTargets）。
//  2. 表缺失的错误原样上抛（repository.ErrStorageConfigMissing 自带处置步骤），
//     不在这里改写文案：处置步骤写在一处，改的时候不会漏。
//  3. 0 行是**合法状态**，但必须把后果说到明处：它和「查询失败」的返回形状
//     必须能被人分辨出来（一个 err=nil、一个 err!=nil）。
func loadTargetsFromDB(dsn string) ([]nginxsnippet.Target, error) {
	db, driver, err := dbopen.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败（driver=%s）：%w", driver, err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer func() { _ = sqlDB.Close() }() // 命令行工具，读一次就退出；不判关闭错误
	}

	bindings, err := repository.ListStorageTargets(db)
	if err != nil {
		// ErrStorageConfigMissing 也不加前缀：它的文案已经是完整处置步骤，
		// 再包一层只会把「先跑 ./server migrate」埋进长句子中间。
		if errors.Is(err, repository.ErrStorageConfigMissing) {
			return nil, err
		}
		return nil, err
	}

	out := make([]nginxsnippet.Target, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.NginxTarget())
	}
	fmt.Fprintf(os.Stderr, "数据源：%s（tenant_storage_configs 共 %d 行）\n", driver, len(out))
	reportUnhealthy(bindings)
	return out, nil
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
