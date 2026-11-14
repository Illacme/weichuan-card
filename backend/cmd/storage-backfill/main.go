// Command storage-backfill 是 P3 存量回填的**盘点 + 执行**命令。
//
// 用法：
//
//	storage-backfill plan --dsn DSN --out plan.json
//	storage-backfill apply --plan plan.json --dsn DSN --source-dir SRC --target-dir DST [--dry-run] [--yes]
//	storage-backfill apply --plan plan.json --dsn DSN --source-store s3 --target-store tenant [--dry-run] [--yes]
//	storage-backfill rollback --plan plan.json --dsn DSN --yes
//	storage-backfill status --plan plan.json --dsn DSN
//
// 源（local / s3）与目标（local / tenant）正交，四种组合都合法；
// 两个默认值都是 local，第六十五轮的既有命令一字不改照常跑。
//
// plan 只读；apply / rollback 会改库，二者**消费同一份计划 JSON，不重扫库**。
// 计划是某一时刻看到的库，重扫一次看到的是另一个时刻的库——两者相等时看不出
// 差别，一旦中间有人改了数据，重扫会把新值当成计划的一部分搬走，而回滚依据
// 仍是手里那份旧计划。所以执行器对每条引用做过期检测，值对不上就整批停下。
//
// 执行器的三步分工（为什么这样切）见 internal/refmigrate/apply.go 的包注释：
// 复制对象（不可回滚，只增不删）与「改库 + 记台账」（同一个 MySQL 事务）分开，
// 任一处失败都能原样重跑。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"weichuan/internal/dbopen"
	"weichuan/internal/refmigrate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "plan":
		err = runPlan(os.Args[2:])
	case "apply":
		err = runApply(os.Args[2:])
	case "rollback":
		err = runRollback(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("未知子命令 %q（可用：plan / apply / rollback / status）", os.Args[1])
	}
	if err != nil {
		// 「有异常数据」不是「命令失败」：CI 里要能区分「盘点跑挂了（1，去看日志）」
		// 与「盘点跑通了但有串号（2，去看数据）」。混成一个码，红灯亮起时
		// 第一反应永远是重跑命令，而不是去处理数据。
		if exit := exitFor(err); exit == 2 {
			fmt.Fprintf(os.Stderr, "⚠ %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		}
		os.Exit(exitFor(err))
	}
}

// errProblems 表示「命令本身跑通了，但库里有需要人工处理的数据」。
// 单独成一个哨兵，是为了让退出码 2 只在**这一种**情况下出现。
var errProblems = errors.New("有需要人工处理的引用（串号 / 非法）")

// exitFor 把错误映射成退出码。单独抽出来是为了能单测——
// 这段逻辑写错过一次（文档写 2、实现走 1），而它只影响 CI 判读，
// 平时手跑完全看不出来。
func exitFor(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errProblems), errors.Is(err, refmigrate.ErrNeedsHuman):
		return 2
	default:
		return 1
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `storage-backfill —— P3 存量资源引用的盘点 / 搬迁 / 回滚

  plan     --dsn DSN [--base-url URL] [--company N] [--out FILE] [--json] [--fail-on-problem]
  apply    --plan FILE --dsn DSN [--source-store local|s3] [--source-dir DIR]
           [--target-store local|tenant] [--target-dir DIR] [--dry-run] [--yes] [--force]
  rollback --plan FILE --dsn DSN [--dry-run] [--yes]
  status   --plan FILE --dsn DSN

plan（只读）:
  --dsn             数据库 DSN（MySQL 或 SQLite，按形态自动选驱动）
  --base-url        平台统一入口基址（cfg.UploadBaseURL，如 https://host/uploads）
  --company         只盘点这一家（试点用）；默认全量
  --out             把完整计划写成 JSON（apply / rollback 读它，不再自己扫库）
  --json            把计划打到 stdout
  --fail-on-problem 发现串号 / 非法值时以退出码 2 结束（CI 用）

apply / rollback（会改数据）:
  --plan            必填，plan --out 产出的那份 JSON；执行器不重扫库
  --dsn             必填，无默认值（避免误连生产）
  --source-store    local（默认，平台本地盘，需 --source-dir）/ s3（平台自有
                    MinIO / S3 兼容桶，直接从桶里读历史对象）。与 --target-store 正交。
  --source-dir      仅 source-store=local 时必填：平台侧现有对象的本地目录。
                    s3 模式给了这个参数会直接报错（避免误以为两边都生效）。
  --target-store    local（默认，离线演练）/ tenant（按 company_id 解密
                    tenant_storage_configs，对象真的写进客户自己的桶）
  --target-dir      仅 target-store=local 时的目标对象根目录。本地适配器把各租户
                    映射到同一根目录，键自带 t/<cid>/ 前缀不会串号；
                    tenant 模式给了这个参数会直接报错（避免误以为两边都生效）
  --dry-run         只做预检，零写入
  --yes             确认真的执行。不加时自动按 dry-run 处理
  --force           仅允许「目标对象已存在但大小与源不一致」时用源覆盖；
                    绝不放宽库值的过期检测

source-store=s3 的配置来源（优先级：系统设置 > 环境变量/CONFIG_FILE > 内置缺省）：
  storage_driver / storage_endpoint / storage_bucket / storage_access_key /
  storage_secret_key / storage_use_ssl / storage_region
  从 sys_setting 只读加载，与生产上传侧是同一份配置；命令不会写这张表。
  凭据不接受命令行参数（会留在 history / ps / 工单里），也不出现在错误文本中。
  driver=local 或 oss 会显式拒绝，绝不拿一个空桶冒充源，也绝不回退本地盘。
  桶不存在 / 凭据错 / 网络不可达会在第一次 Stat 就报「平台源桶」，
  不会退化成「源对象不存在」那种让人去查文件的误导性错误。

退出码：0 正常 / 需要人工确认时加 --yes；1 命令出错；2 数据需人工处理。
`)
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	dsn := fs.String("dsn", "", "数据库 DSN")
	baseURL := fs.String("base-url", "", "平台统一入口基址")
	company := fs.Int64("company", 0, "只盘点这一家（0 = 全量）")
	out := fs.String("out", "", "把完整计划写成 JSON")
	asJSON := fs.Bool("json", false, "把计划打到 stdout")
	failOnProblem := fs.Bool("fail-on-problem", false, "发现异常时退出码 2")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dsn) == "" {
		return fmt.Errorf("必须给 --dsn（盘点要读库；没有默认值是刻意的，避免误连生产）")
	}

	db, driver, err := dbopen.Open(*dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败：%w", err)
	}
	plan, err := refmigrate.Scan(db, refmigrate.Options{BaseURL: *baseURL, CompanyID: *company})
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			return err
		}
	} else {
		printReport(plan, driver, *company)
	}

	if *out != "" {
		if err := writeJSON(*out, plan); err != nil {
			return err
		}
		fmt.Printf("\n计划已写出：%s（执行器下一轮读它，不要重新扫库）\n", *out)
	}

	if *failOnProblem && len(plan.Problems()) > 0 {
		return fmt.Errorf("%w：共 %d 条", errProblems, len(plan.Problems()))
	}
	return nil
}

// printReport 是人看的部分：先给「哪一列有多少条待搬」，再给结论。
// 顺序刻意按「待搬迁」降序——运维第一眼要知道的是工作量，不是全量分布。
func printReport(plan *refmigrate.Plan, driver string, company int64) {
	scope := "全量"
	if company > 0 {
		scope = fmt.Sprintf("仅企业 %d", company)
	}
	fmt.Printf("== 存量资源引用盘点（只读，未改动任何数据）==\n")
	fmt.Printf("库：%s · 范围：%s · 基址：%s\n\n", driver, scope, orDash(plan.BaseURL))

	// 表格自己算宽度，不用 text/tabwriter：tabwriter 按 rune 个数补空格，
	// 而中文是双宽字符（一个汉字占两列），用它排出来的表中文列全部错位——
	// 不报错、只是难看，于是很容易一直留着。见 renderTable。
	total := map[refmigrate.Class]int{}
	rows := make([][]string, 0, len(plan.Columns))
	for _, c := range plan.Columns {
		rows = append(rows, []string{
			c.Column, c.Table, strconv.Itoa(c.Total),
			strconv.Itoa(c.Counts[refmigrate.ClassNeedsMigrate]),
			strconv.Itoa(c.Counts[refmigrate.ClassTenantReady]),
			strconv.Itoa(c.Counts[refmigrate.ClassPlatform]),
			strconv.Itoa(c.Counts[refmigrate.ClassForeignURL]),
			strconv.Itoa(c.Counts[refmigrate.ClassForeignTenant]),
			strconv.Itoa(c.Counts[refmigrate.ClassInvalid]),
		})
		for k, v := range c.Counts {
			total[k] += v
		}
	}
	fmt.Print(renderTable([]string{"列", "表", "总计", "待搬迁", "已就绪", "平台", "外链", "串号", "非法"}, rows))

	groups := plan.Objects()
	fmt.Printf("\n合计：%d 条引用\n", sum(total))
	fmt.Printf("  待搬迁 %d 条（去重后 %d 个对象要复制）\n", total[refmigrate.ClassNeedsMigrate], len(groups))
	fmt.Printf("  已就绪 %d 条 · 平台对象 %d 条（永不搬）· 外域直链 %d 条（原样保留）\n",
		total[refmigrate.ClassTenantReady], total[refmigrate.ClassPlatform], total[refmigrate.ClassForeignURL])

	problems := plan.Problems()
	if len(problems) > 0 {
		fmt.Printf("\n⚠ 需要人工处理 %d 条（脚本不猜）：\n", len(problems))
		shown := 0
		for _, p := range problems {
			if shown >= 10 {
				fmt.Printf("  …（其余 %d 条见 --json 明细）\n", len(problems)-shown)
				break
			}
			fmt.Printf("  · %s %s.%s#%d company=%d 值=%q —— %s\n",
				p.Column, p.Table, p.PK, p.PKValue, p.CompanyID, p.Raw, p.Note)
			shown++
		}
	}

	if len(groups) > 0 {
		fmt.Printf("\n搬迁预览（前 5 个对象）：\n")
		for i, g := range groups {
			if i >= 5 {
				fmt.Printf("  …（其余 %d 个见 --json 明细）\n", len(groups)-i)
				break
			}
			fmt.Printf("  %s → %s（%d 条引用）\n", g.SourceKey, g.TargetKey, g.Refs)
		}
	}

	fmt.Printf("\n下一步：apply --plan <这份 JSON> --dsn … " +
		"[--source-store local --source-dir DIR | --source-store s3] " +
		"[--target-store local --target-dir DIR | --target-store tenant] --dry-run（先预检）。\n")
}

// renderTable 按**显示宽度**对齐，第一列左对齐、其余右对齐。
//
// 为什么不用 text/tabwriter：它按 rune 个数算宽度，中文一列里放「员工头像」（4 个
// rune、8 列宽）与「case1-a.jpg」会算出同样的宽度，排出来的表必然错位。错位的
// 表格不报错、只是难读，所以最容易一直留着没人修。
func renderTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = dispWidth(h)
	}
	for _, r := range rows {
		for i, cell := range r {
			if i < len(widths) && dispWidth(cell) > widths[i] {
				widths[i] = dispWidth(cell)
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			pad := widths[i] - dispWidth(cell)
			if pad < 0 {
				pad = 0
			}
			if i == 0 {
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", pad))
			} else {
				b.WriteString(strings.Repeat(" ", pad))
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	writeRow(headers)
	for _, r := range rows {
		writeRow(r)
	}
	return b.String()
}

// dispWidth 是终端里的显示列宽：东亚宽字符算 2。区间取自 Unicode 的
// East Asian Width = Wide/Fullwidth 的常用部分，覆盖中日韩与全角标点。
func dispWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r < 0x1100:
		return 1
	case r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF,   // CJK 部首 … 彝文
		r >= 0xA960 && r <= 0xA97F,   // Hangul Jamo Extended-A
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul 音节
		r >= 0xF900 && r <= 0xFAFF,   // CJK 兼容汉字
		r >= 0xFE10 && r <= 0xFE19,   // 竖排标点
		r >= 0xFE30 && r <= 0xFE6F,   // CJK 兼容形式
		r >= 0xFF00 && r <= 0xFF60,   // 全角形式
		r >= 0xFFE0 && r <= 0xFFE6,   // 全角符号
		r >= 0x1F300 && r <= 0x1F64F, // emoji（⚠ 在这里）
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展 B 及以后
		return 2
	}
	return 1
}

func sum(m map[refmigrate.Class]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(未设置，按 /uploads/ 段匹配)"
	}
	return s
}

// writeJSON 原子替换：盘点结果只会被人拿去执行，写一半的文件比没有更危险。
func writeJSON(path string, plan *refmigrate.Plan) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plan-*.json")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmp.Name()) // 成功时已被 Rename 走，这里只剩失败路径
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	// 顺序稳定：Items 已经按列清单 + 主键有序，Objects 按源键排序。
	if err := enc.Encode(plan); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil { // 计划里有业务数据的位置信息，不给全局读
		return err
	}
	return os.Rename(tmp.Name(), path)
}
