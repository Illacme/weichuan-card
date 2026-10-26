package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/dbbackup"
)

// `server backup` / `server dbcheck` 两个运维子命令。
//
// 与 `server migrate` 同样的定位：不启服务、只做一件事、可进 cron 与流水线。
// 它们的存在理由见 internal/dbbackup 的包注释——生产默认形态是 SQLite，
// 而「在线一致性备份」和「备份到底能不能用」此前都没有工具。

const backupUsage = `用法：
  server backup  --out <文件> [--db <dsn>]
      对 SQLite 库做在线一致性快照（VACUUM INTO），并立即校验产物。
      MySQL 请用 scripts/backup.sh（mysqldump --single-transaction 才有一致性快照）。

  server dbcheck --file <文件> [--json | --field <字段>]
      只读体检一个 SQLite 备份文件：integrity_check / 表数 / 各表行数。
      --field 取值：path | bytes | tables | integrity | rows:<表名>
      （给 shell 脚本取单个值用，省掉 jq 依赖）
`

func runBackup(cfg config.Config) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("out", "", "输出文件路径（SQLite .db）")
	dsn := fs.String("db", "", "数据库 DSN，缺省取 DB_DSN")
	if err := fs.Parse(os.Args[2:]); err != nil {
		log.Fatalf("[BACKUP][FATAL] 参数解析失败：%v", err)
	}
	if strings.TrimSpace(*out) == "" {
		log.Print(backupUsage)
		os.Exit(2)
	}
	d := strings.TrimSpace(*dsn)
	if d == "" {
		d = cfg.DBDSN
	}
	rep, err := dbbackup.Snapshot(d, *out)
	if err != nil {
		log.Fatalf("[BACKUP][FATAL] %v", err)
	}
	log.Printf("[BACKUP] OK %s（%d 字节 / %d 张表 / integrity=%s）",
		rep.Path, rep.Bytes, rep.Tables, rep.Integrity)
}

func runDBCheck() {
	fs := flag.NewFlagSet("dbcheck", flag.ExitOnError)
	file := fs.String("file", "", "要体检的 SQLite 文件")
	asJSON := fs.Bool("json", false, "以 JSON 输出完整报告")
	field := fs.String("field", "", "只输出某个字段（path|bytes|tables|integrity|rows:<表名>）")
	if err := fs.Parse(os.Args[2:]); err != nil {
		log.Fatalf("[DBCHECK][FATAL] 参数解析失败：%v", err)
	}
	if strings.TrimSpace(*file) == "" {
		log.Print(backupUsage)
		os.Exit(2)
	}
	rep, err := dbbackup.Inspect(*file)
	if err != nil {
		// 体检失败时仍把已拿到的部分打出来（如 integrity 不通过时的文件大小），
		// 排查时这些信息比一句 FATAL 有用
		log.Printf("[DBCHECK][FATAL] %v", err)
		os.Exit(1)
	}

	switch {
	case strings.TrimSpace(*field) != "":
		v, ferr := fieldOf(rep, *field)
		if ferr != nil {
			log.Printf("[DBCHECK][FATAL] %v", ferr)
			os.Exit(2)
		}
		fmt.Println(v)
	case *asJSON:
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
	default:
		fmt.Printf("文件      : %s\n", rep.Path)
		fmt.Printf("大小      : %d 字节\n", rep.Bytes)
		fmt.Printf("完整性    : %s\n", rep.Integrity)
		fmt.Printf("表数量    : %d\n", rep.Tables)
		fmt.Println("各表行数  :")
		for _, t := range rep.SortedRowNames() {
			fmt.Printf("  %-28s %d\n", t, rep.Rows[t])
		}
	}
}

// fieldOf 取单个字段值。rows:<表名> 表不存在时返回错误而不是 0——
// 0 会被脚本读成「表是空的」，而实际是「表根本没有」，两者的处置完全不同。
// 特例：rows 不带表名时输出全部表，每行 `表名=行数`，供演练脚本直接 while read。
func fieldOf(rep dbbackup.Report, field string) (string, error) {
	switch field {
	case "path":
		return rep.Path, nil
	case "bytes":
		return fmt.Sprintf("%d", rep.Bytes), nil
	case "tables":
		return fmt.Sprintf("%d", rep.Tables), nil
	case "integrity":
		return rep.Integrity, nil
	case "rows":
		var b strings.Builder
		for _, t := range rep.SortedRowNames() {
			fmt.Fprintf(&b, "%s=%d\n", t, rep.Rows[t])
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	if name, ok := strings.CutPrefix(field, "rows:"); ok {
		n, exists := rep.Rows[name]
		if !exists {
			return "", fmt.Errorf("备份里没有表 %q", name)
		}
		return fmt.Sprintf("%d", n), nil
	}
	return "", fmt.Errorf("未知字段 %q（可选：path|bytes|tables|integrity|rows|rows:<表名>）", field)
}
