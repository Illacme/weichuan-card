// Command license 是「签发方」用的许可证工具，不随服务发布。
//
// 为什么单独一个二进制而不是 server 的子命令：签名私钥只应存在于签发环境，
// 把它编译进随服务分发的二进制等于把「给任何人签一张永久授权」的能力
// 发到了客户机器上。
//
// 用法：
//
//	license keygen
//	    生成 Ed25519 密钥对。公钥内嵌进服务二进制（替换 internal/license/embed.go
//	    的 embeddedPublicKeyB64），私钥留在本工具的签发环境里、绝不进仓库。
//
//	license sign --priv <base64私钥> [--priv-file <文件>] [选项]
//	    签发一份许可证文件。常用选项：
//	      --tenant N      绑定租户（企业 id）；私有部署填该企业 id，0 = 不绑定
//	      --edition S     版本标识：private | platform | trial（业务展示用）
//	      --seats N       员工席位上限；0 = 不限制
//	      --expires T     到期时间，RFC3339（如 2027-01-01T00:00:00Z）；空 = 不过期
//	      --features S    功能开关，逗号分隔（如 mp_release,content_sec）；空 = 全部开通
//	      --out PATH      输出路径，默认 data/license.key
//	    私钥也可经环境变量 LICENSE_PRIV 传入，避免出现在进程列表 / shell 历史里。
//
//	license show [--file PATH] [--pub <base64公钥>]
//	    读取一份许可证文件并验签展示内容（运维核对「这份授权到什么时候、多少席位」）。
//	    不传 --pub 时用服务内嵌的那把公钥验签。
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"weichuan/internal/license"
)

const usage = `license — 许可证签发工具（签发方专用，不随服务发布）

子命令：
  keygen   生成 Ed25519 密钥对（公钥内嵌进服务，私钥留签发环境）
  sign     签发一份许可证文件
  show     读取并验签展示一份许可证文件

用 "license <子命令> -h" 看各子命令的选项。`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = cmdKeygen(os.Args[2:])
	case "sign":
		err = cmdSign(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Println(usage)
		return
	default:
		fmt.Printf("未知子命令：%s\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}

// cmdKeygen 生成密钥对。私钥只打印到标准输出（由签发方自行保管），不落文件——
// 工具不该替用户决定私钥放哪、更不该顺手写一个权限可能过宽的文件。
func cmdKeygen(_ []string) error {
	pub, priv, err := license.GenerateKeyPair()
	if err != nil {
		return err
	}
	fmt.Println("# 把下面这行公钥填进 internal/license/embed.go 的 embeddedPublicKeyB64：")
	fmt.Println("public:", pub)
	fmt.Println("# 私钥只留签发环境，绝不进仓库 / 服务二进制：")
	fmt.Println("private:", priv)
	return nil
}

func cmdSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	var (
		privB64  = fs.String("priv", "", "base64 编码的 Ed25519 私钥（也可经 LICENSE_PRIV 传入）")
		privFile = fs.String("priv-file", "", "从文件读取 base64 私钥（与 --priv 二选一）")
		tenant   = fs.Int64("tenant", 0, "绑定租户（企业 id）；0 = 不绑定")
		edition  = fs.String("edition", "private", "版本标识：private | platform | trial")
		seats    = fs.Int("seats", 0, "员工席位上限；0 = 不限制")
		expires  = fs.String("expires", "", "到期时间 RFC3339，如 2027-01-01T00:00:00Z；空 = 不过期")
		features = fs.String("features", "", "功能开关，逗号分隔；空 = 全部开通")
		out      = fs.String("out", license.DefaultLicensePath, "输出许可证文件路径")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw := *privB64
	if raw == "" {
		raw = os.Getenv("LICENSE_PRIV")
	}
	if raw == "" && *privFile != "" {
		b, err := os.ReadFile(*privFile)
		if err != nil {
			return fmt.Errorf("读取私钥文件：%w", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" {
		return fmt.Errorf("缺少私钥：用 --priv / --priv-file，或设环境变量 LICENSE_PRIV")
	}
	privBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("私钥不是合法 base64：%w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("私钥长度应为 %d 字节，实际 %d", ed25519.PrivateKeySize, len(privBytes))
	}
	priv := ed25519.PrivateKey(privBytes)

	ent := &license.Entitlement{
		TenantID: *tenant,
		Edition:  *edition,
		Seats:    *seats,
	}
	if *expires != "" {
		t, err := time.Parse(time.RFC3339, *expires)
		if err != nil {
			return fmt.Errorf("--expires 不是合法 RFC3339 时间：%w", err)
		}
		ent.ExpiresAt = t
	}
	if *features != "" {
		for _, p := range strings.Split(*features, ",") {
			if v := strings.TrimSpace(p); v != "" {
				ent.Features = append(ent.Features, v)
			}
		}
	}

	text, err := license.SignLicense(priv, ent)
	if err != nil {
		return err
	}
	if dir := dirOf(*out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建输出目录：%w", err)
		}
	}
	if err := os.WriteFile(*out, []byte(text), 0o600); err != nil {
		return fmt.Errorf("写许可证文件：%w", err)
	}
	fmt.Printf("已签发：%s\n", *out)
	return printEntitlement(ent)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	var (
		file = fs.String("file", license.DefaultLicensePath, "许可证文件路径")
		pub  = fs.String("pub", "", "base64 公钥；空 = 用服务内嵌公钥验签")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("读取许可证：%w", err)
	}
	key := license.DefaultPublicKey()
	if *pub != "" {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*pub))
		if err != nil {
			return fmt.Errorf("公钥不是合法 base64：%w", err)
		}
		key = ed25519.PublicKey(b)
	}
	ent, err := license.ParseLicense(key, raw)
	if err != nil {
		return fmt.Errorf("验签失败（文件被篡改，或公钥与签发私钥不配对）：%w", err)
	}
	fmt.Printf("验签通过：%s\n", *file)
	return printEntitlement(ent)
}

// printEntitlement 以人可读形式回显授权内容，方便签发 / 运维核对。
func printEntitlement(e *license.Entitlement) error {
	out := map[string]any{
		"tenantId": e.TenantID,
		"edition":  e.Edition,
		"seats":    e.Seats,
		"features": e.Features,
	}
	if e.Seats <= 0 {
		out["seats"] = "不限"
	}
	if e.ExpiresAt.IsZero() {
		out["expiresAt"] = "永不过期"
	} else {
		out["expiresAt"] = e.ExpiresAt.Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(out, "  ", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// dirOf 取路径的目录部分（无目录分隔符时返回空串，避免对当前目录做 MkdirAll）。
func dirOf(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i > 0 {
		return p[:i]
	}
	return ""
}
