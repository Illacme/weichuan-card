// Package mpupload 小程序代码上传（后台代传）。
//
// 背景：版本发布链路里「把代码送到微信」这一步，微信只开了两条路——
// 开发者工具（GUI，要装那个大块头）和 miniprogram-ci（Node 模块）。自营小程序
// 没有纯 HTTP 上传接口（wxa/commit 是第三方平台代开发专用，且授权后商家自己在
// 公众平台就不能发版了）。所以后台要做上传，只能调 Node。
//
// 部署前置条件（缺一项 Problems() 就会点名，别让运维猜）：
//   - 服务器上有 node（MP_NODE_BIN 可指定绝对路径）
//   - 某目录下装过 miniprogram-ci（MP_CI_MODULE_DIR 指向它，留空则试源码目录）
//   - 小程序源码在服务器上（MP_SOURCE_DIR），含 project.config.json
//   - 用了 TS 的项目还要在该目录装 typescript（project.config.json 的
//     useCompilerPlugins 里声明了 typescript，编译器插件要能 require 到）
//   - 服务器出口 IP 加进微信的「小程序代码上传 IP 白名单」（与 API IP 白名单
//     是两套，见 migrations/0024 的说明）；没加会报 40164
package mpupload

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed upload.js
var uploadJS []byte

const (
	// DefaultNodeBin 找不到 node 时的命令名：PATH 里没有就由 Problems() 报出来，
	// 不在这里猜常见安装路径（猜错的路径比没有路径更难排查）。
	DefaultNodeBin = "node"
	// DefaultTimeout 单次上传超时。编译 + 打包 + 上传，分包大的项目能到分钟级；
	// 超时后 Go 会杀掉 node，此时微信侧可能已收到代码——所以失败后要让调用方
	// 去微信公众平台确认一下，别当成一定没传上去。
	DefaultTimeout = 5 * time.Minute
	// 结果行前缀。miniprogram-ci 会往 stdout 打编译进度，不能假设 stdout 只有我们的 JSON。
	resultSentinel = "__WEICHUAN_MP_UPLOAD__"
)

// Config 上传所需的服务端环境。全部可由环境变量 / 配置文件注入。
type Config struct {
	// NodeBin node 可执行文件名或绝对路径，默认 "node"
	NodeBin string
	// ModuleDir 装了 miniprogram-ci 的目录（其下应有 node_modules/miniprogram-ci）。
	// 留空时回落到 SourceDir——把依赖直接装进源码目录也是常见做法。
	ModuleDir string
	// SourceDir 小程序源码目录（含 project.config.json）
	SourceDir string
	// Timeout 单次上传超时，默认 5 分钟
	Timeout time.Duration
}

// Request 一次上传的参数。
type Request struct {
	AppID   string // 传到哪个小程序
	Version string // 版本号，1-64 位，之后提审/发布都认它
	Desc    string // 版本说明（微信侧展示）
	Key     string // 代码上传密钥原文（private.key 的内容，PEM）
	Robot   int    // CI 机器人编号 1-30，可空
}

func (c Config) nodeBin() string {
	if b := strings.TrimSpace(c.NodeBin); b != "" {
		return b
	}
	return DefaultNodeBin
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

func (c Config) moduleDir() string {
	if d := strings.TrimSpace(c.ModuleDir); d != "" {
		return d
	}
	return strings.TrimSpace(c.SourceDir)
}

// Kind 路径配置项的种类：决定「选什么」（文件还是目录）与「怎么算对」。
// 与 setting 注册项里的 PathKind 字段一一对应，是服务端路径选择器的「选什么」契约。
type Kind string

const (
	// KindNodeBin Node 可执行文件（可能是命令名 node，也可能是绝对路径）。
	KindNodeBin Kind = "node_bin"
	// KindModuleDir 装了 miniprogram-ci 的目录（其下应有 node_modules/miniprogram-ci）。
	KindModuleDir Kind = "module_dir"
	// KindSourceDir 小程序源码目录（含 project.config.json）。
	KindSourceDir Kind = "source_dir"
)

// CheckPath 校验某项路径配置当前填的值是否可用。
// 返回 (是否可用, 说明)。说明在可用时是肯定句（如「该目录下有 project.config.json」），
// 不可用时必须具体到「缺什么、怎么补」——运维登服务器照着改，含糊一句会让他无从下手。
//
// 与 Config.Problems() 共用同一套规则：本章是唯一实现，Problems() 直接调它，
// 保证「界面提示」「上传前的探活」「路径选择器里的实时校验」三处口径完全一致，
// 不会出现「这里说缺 A、那里说缺 B」让管理员对不上的情况。因此本函数的否定文案
// 必须与历史文案逐字节相同（已有测试钉死），只动肯定文案。
func CheckPath(kind Kind, value string) (bool, string) {
	switch kind {
	case KindNodeBin:
		// node 是个命令名也可能绝对路径：绝对路径交给 LookPath 直接判定可执行性，
		// 含斜杠时它只查这个文件本身（不会当 PATH 相对项去猜），正好符合「校验一个具体文件」。
		if _, err := exec.LookPath(value); err != nil {
			return false, "找不到 node 可执行文件（" + value + "）：上传代码需要 Node 环境，可用 MP_NODE_BIN 指定绝对路径"
		}
		return true, "node 可执行文件可用（" + value + "）"
	case KindModuleDir:
		value = strings.TrimSpace(value)
		if value == "" {
			return false, "未配置 miniprogram-ci 安装目录（MP_CI_MODULE_DIR）"
		}
		if !isDir(filepath.Join(value, "node_modules", "miniprogram-ci")) {
			return false, value + " 下没有 node_modules/miniprogram-ci：请在该目录执行 npm install miniprogram-ci"
		}
		return true, value + " 下已有 node_modules/miniprogram-ci"
	case KindSourceDir:
		value = strings.TrimSpace(value)
		if value == "" {
			return false, "未配置小程序源码目录（MP_SOURCE_DIR）"
		}
		if !isDir(value) {
			return false, "小程序源码目录不存在：" + value
		}
		if !isFile(filepath.Join(value, "project.config.json")) {
			return false, value + " 下没有 project.config.json，不像一个小程序工程目录"
		}
		return true, value + " 下有 project.config.json，是一个小程序工程目录"
	}
	return false, "未知的路径类型：" + string(kind)
}

// Marker 描述一个目录/文件「像不像」这一类路径配置项的正确答案。
// 返回空串表示不像（界面不显示徽标）；非空时作为目录/文件上的小徽标，
// 让管理员在一堆候选里一眼认出「这个就是我要的」。只做存在性嗅探，不深究内容。
//
// 为什么单独抽出来而不是在列举时顺手写：选择器每列一个条目都要算一次徽标，
// 规则若散落两处极易漂移——比如某天改了「判定工程目录」的标准却忘了徽标，
// 界面就会对真正正确的目录沉默。集中到这里，列举逻辑只管调用。
func Marker(kind Kind, path string) string {
	switch kind {
	case KindSourceDir:
		// 工程目录的硬标志就是 project.config.json：缺它微信那边也认不出这是个工程。
		if isFile(filepath.Join(path, "project.config.json")) {
			return "小程序工程目录"
		}
	case KindModuleDir:
		// 装了 miniprogram-ci 的目录：依赖恰好叫 node_modules/miniprogram-ci。
		if isDir(filepath.Join(path, "node_modules", "miniprogram-ci")) {
			return "已装 miniprogram-ci"
		}
	case KindNodeBin:
		// 命令名 node 或带版本号的 node20 / node-18 这类文件名都算像；
		// 但 node_modules / nodectl 之类不能误判为 node 可执行文件，
		// 故紧跟 "node" 的字符必须是版本/变体分隔符（数字 / - / _ / .）。
		if looksLikeNodeName(filepath.Base(path)) {
			return "node 可执行文件"
		}
	}
	return ""
}

// Problems 返回上传功能当前不可用的原因列表（空 = 可用）。
//
// 刻意做成「返回全部问题」而不是「遇到第一个就返回」：运维改完一项再点一次
// 才知道还有下一项，是这套功能最难用的形态。一次性列全，照着改一遍就好。
// 规则实现下放到 CheckPath，本函数只负责把三类配置的结果汇总。
func (c Config) Problems() []string {
	var ps []string
	if ok, msg := CheckPath(KindNodeBin, c.nodeBin()); !ok {
		ps = append(ps, msg)
	}
	if ok, msg := CheckPath(KindModuleDir, c.moduleDir()); !ok {
		ps = append(ps, msg)
	}
	if ok, msg := CheckPath(KindSourceDir, c.SourceDir); !ok {
		ps = append(ps, msg)
	}
	return ps
}

// KeyProblems 校验代码上传密钥的格式。密钥是用户在界面上粘贴进来的，
// 常见错误是贴成了 AppSecret、或者只贴了一半——在跑编译前拦住，
// 比让 miniprogram-ci 报一句 "error: error" 友好得多。
func KeyProblems(key string) []string {
	k := normalizeKey(key)
	if k == "" {
		return []string{"未填写代码上传密钥"}
	}
	var ps []string
	if !strings.Contains(k, "PRIVATE KEY") {
		ps = append(ps, "这不像一份代码上传密钥：内容里没有 PRIVATE KEY 标记。应在微信公众平台 → 开发管理 → 开发设置 → 小程序代码上传 里下载 .key 文件，把文件内容整个粘贴进来")
	}
	if !strings.HasSuffix(k, "-----") {
		ps = append(ps, "密钥内容不完整（结尾不是 -----），可能是粘贴时被截断了")
	}
	return ps
}

// Upload 执行一次上传。返回 nil 表示微信侧已接收该版本代码（成为体验版）。
func Upload(ctx context.Context, c Config, req Request) error {
	if ps := c.Problems(); len(ps) > 0 {
		return fmt.Errorf("上传环境未就绪：%s", strings.Join(ps, "；"))
	}
	if ps := KeyProblems(req.Key); len(ps) > 0 {
		return fmt.Errorf("%s", strings.Join(ps, "；"))
	}
	if strings.TrimSpace(req.AppID) == "" || strings.TrimSpace(req.Version) == "" {
		return fmt.Errorf("缺少 AppID 或版本号")
	}

	keyPath, removeKey, err := writeTempKey(normalizeKey(req.Key))
	if err != nil {
		return fmt.Errorf("写入上传密钥临时文件失败：%w", err)
	}
	defer removeKey()

	scriptPath, err := ensureScript()
	if err != nil {
		return fmt.Errorf("准备上传脚本失败：%w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"appid":          req.AppID,
		"projectPath":    strings.TrimSpace(c.SourceDir),
		"privateKeyPath": keyPath,
		"version":        strings.TrimSpace(req.Version),
		"desc":           req.Desc,
		"robot":          req.Robot,
	})
	if err != nil {
		return fmt.Errorf("构造上传参数失败：%w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, c.nodeBin(), scriptPath)
	cmd.Dir = strings.TrimSpace(c.SourceDir)
	cmd.Stdin = bytes.NewReader(payload)
	// NODE_PATH 让脚本里的 require('miniprogram-ci') 能解析到安装目录。
	// 不能用 cwd 代替：require 的解析起点是**脚本所在目录**，不是进程工作目录。
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(c.moduleDir(), "node_modules"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// 优先用脚本回传的结构化错误；拿不到再退回 stderr 尾部。
		if res, ok := parseResult(stdout.Bytes()); ok && !res.OK {
			return fmt.Errorf("上传失败：%s", res.Error)
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("上传超时（%s）：node 进程已被终止。微信侧可能已收到代码，请到公众平台确认后再重试", c.timeout())
		}
		return fmt.Errorf("上传失败：%s", tailForError(stderr.String(), stdout.String(), err))
	}
	res, ok := parseResult(stdout.Bytes())
	if !ok {
		// node 正常退出却没打结果行：脚本被换掉 / node 版本异常之类，不能当成功
		return fmt.Errorf("上传结果无法解析（stdout 未见结果标记），stdout 尾部：%s", tailForError(stdout.String(), "", nil))
	}
	if !res.OK {
		return fmt.Errorf("上传失败：%s", res.Error)
	}
	return nil
}

type uploadResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func parseResult(out []byte) (uploadResult, bool) {
	var last string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, resultSentinel) {
			last = strings.TrimPrefix(line, resultSentinel)
		}
	}
	if last == "" {
		return uploadResult{}, false
	}
	var r uploadResult
	if err := json.Unmarshal([]byte(last), &r); err != nil {
		return uploadResult{}, false
	}
	return r, true
}

// tailForError 从 stderr/stdout 尾部截一段作为错误详情。
// 只截尾部：编译错误的结论通常在最后几行，全文会把日志撑爆。
func tailForError(stderr, stdout string, runErr error) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		s = strings.TrimSpace(stdout)
	}
	const max = 600
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	if s == "" {
		if runErr != nil {
			return runErr.Error()
		}
		return "未知错误"
	}
	return s
}

// normalizeKey 归一化密钥：去首尾空白、CRLF 转 LF。
// CRLF 必须转——PEM 的 base64 按行解析，行尾多一个 \r 会让解析失败，
// 而用户从网页/编辑器里复制出来的密钥很可能带 \r\n。
func normalizeKey(key string) string {
	k := strings.TrimSpace(key)
	k = strings.ReplaceAll(k, "\r\n", "\n")
	k = strings.ReplaceAll(k, "\r", "\n")
	return strings.TrimSpace(k)
}

// writeTempKey 把密钥写进 0600 临时文件，返回路径与清理函数。
// 之所以必须落盘：miniprogram-ci 的 Project 只接受 privateKeyPath，不接受内容。
func writeTempKey(key string) (string, func(), error) {
	f, err := os.CreateTemp("", "weichuan-mp-key-*.key")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	// 清理回调：两行都是「尽力而为」——文件马上要被删，关闭/删除失败都没有
	// 可补救的动作（留着的后果只是临时目录多一个文件，由系统清理）。
	// 写成 _ = 而不是留空，是为了让「这里确实不判错」成为显式决定而非漏判。
	remove := func() {
		_ = f.Close()
		_ = os.Remove(path)
	}
	if err := f.Chmod(0o600); err != nil {
		remove()
		return "", func() {}, err
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		remove()
		return "", func() {}, err
	}
	// 这个 Close 必须判错：写成功而 Close 失败意味着私钥未必落盘，
	// 后面 miniprogram-ci 读到的是空文件——报错会更晚、更难定位。
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

var (
	scriptOnce sync.Once
	scriptPath string
	scriptErr  error
)

// ensureScript 把内嵌的上传脚本落到临时文件（进程内只做一次）。
// 用固定路径而不是每次新建：运维排障时可以直接 `node <路径>` 手工复现，
// 而临时随机名在报错信息里出现一次就再也找不到了。
func ensureScript() (string, error) {
	scriptOnce.Do(func() {
		dir, err := os.MkdirTemp("", "weichuan-mpupload-")
		if err != nil {
			scriptErr = err
			return
		}
		p := filepath.Join(dir, "upload.js")
		if err := os.WriteFile(p, uploadJS, 0o600); err != nil {
			scriptErr = err
			return
		}
		scriptPath = p
	})
	return scriptPath, scriptErr
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// looksLikeNodeName 判断文件名像不像 node 可执行文件。
//
// 只认两类：
//  1. 恰好就是 "node"；
//  2. "node" 紧跟版本号（数字，如 node20），或紧跟分隔符（-/_.）且分隔符之后是数字
//     （如 node-18 / node_16 / node.v18）——覆盖常见的带版本/变体命名。
//
// 必须收紧：裸前缀判断会把 node_modules / nodectl / 甚至纯数字目录名（如临时目录
// 的 "002"）误判成 node 可执行文件，导致目录列表里出现错误的「node 可执行文件」徽标。
// 因此：名字必须以 "node" 开头；且 "node" 之后要么直接是数字，要么是「分隔符 + 数字」
// （分隔符后跟字母的 node_modules 不算，跟数字的才算变体版本号）。
func looksLikeNodeName(base string) bool {
	if base == "node" {
		return true
	}
	if !strings.HasPrefix(base, "node") {
		return false
	}
	rest := base[4:]
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// node20 这类紧贴版本号
		return true
	case '-', '_', '.':
		// 分隔符之后必须是数字（node-18 / node_16 / node.v18 的版本号），
		// 而非字母——否则 node_modules 这种「node + 下划线 + 字母」会蒙混过关。
		return len(rest) >= 2 && rest[1] >= '0' && rest[1] <= '9'
	}
	return false
}
