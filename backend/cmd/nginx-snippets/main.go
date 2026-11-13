// Command nginx-snippets 把租户存储配置渲染成 nginx snippet，并做两道前置检查。
//
// 用法：
//
//	nginx-snippets render --targets tenants.json [--out FILE] [--dns-precheck]
//	nginx-snippets check  --targets tenants.json --snippet FILE [--dns-precheck]
//
// 两个子命令都**不碰库、不写 nginx、不 reload**：只算、只写文件、只报错。
// 真正落盘与 reload 由运维（或后续的配置接口）决定，这条边界是刻意的——
// 一个会自动 reload 线上接入层的工具，出问题时的爆炸半径是整站。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"weichuan/internal/nginxsnippet"
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

  render --targets FILE [--out FILE] [--dns-precheck]
      渲染 snippet。不带 --out 时写 stdout（便于管道/比对）。

  check  --targets FILE --snippet FILE [--dns-precheck]
      只对账，不改任何文件。配置表与 snippet 不一致即非零退出。

输入文件：{"tenants":[{companyId,driver,endpoint,bucket,useSsl,hostHeader,enabled}]}
`)
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	targetsPath := fs.String("targets", "", "租户配置 JSON（必填）")
	outPath := fs.String("out", "", "输出文件；留空写 stdout")
	dns := fs.Bool("dns-precheck", false, "落盘前先解析每个桶域名（推荐开启）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetsPath == "" {
		return fmt.Errorf("缺少 --targets")
	}

	targets, err := loadTargets(*targetsPath)
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

	if *outPath == "" {
		fmt.Print(out)
	} else if err := os.WriteFile(*outPath, []byte(out), 0o644); err != nil {
		return err
	}

	// 摘要走 stderr：stdout 要保持可管道（生产上有人会 `render > x && diff`）。
	var taken, skipped []int64
	for _, t := range targets {
		if t.NeedsSnippet() {
			taken = append(taken, t.CompanyID)
		} else {
			skipped = append(skipped, t.CompanyID)
		}
	}
	fmt.Fprintf(os.Stderr, "已接管（生成 location）：%v\n", taken)
	fmt.Fprintf(os.Stderr, "未接管（回落平台盘）：%v\n", skipped)
	if *dns {
		fmt.Fprintf(os.Stderr, "DNS 预检：%d 个域名全部可解析\n", len(taken))
	}
	return nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	targetsPath := fs.String("targets", "", "租户配置 JSON（必填）")
	snippetPath := fs.String("snippet", "", "已生成的 snippet（必填）")
	dns := fs.Bool("dns-precheck", false, "顺带解析每个桶域名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetsPath == "" || *snippetPath == "" {
		return fmt.Errorf("check 需要 --targets 与 --snippet")
	}

	targets, err := loadTargets(*targetsPath)
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
