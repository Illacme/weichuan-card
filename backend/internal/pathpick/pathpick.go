// Package pathpick 服务端目录浏览：让管理员在「系统设置」里挑 Node / 模块目录 / 源码目录，
// 而不用手敲绝对路径（也避开了「浏览器文件框选的是客户端机器」这个根本误区——
// 这些路径指向的是**服务器**文件系统，后端要拿它们去 exec，客户端的框选毫无意义）。
//
// 安全是这层的核心约束：本接口等于把服务器文件系统的一部分开放给后台枚举，所以：
//   - 用**白名单**而非黑名单：只放行列举的常规部署位置（/opt /srv /usr/local ...）。
//     用黑名单定义放行范围等于在玩「你猜我漏了哪个敏感目录」——漏一个就是一次泄漏。
//   - 解析符号链接后再判白名单：否则 /home 里的软链指到 /etc，白名单就被绕过去了。
//     列举时遇到软链条目直接跳过，既不展示也不跟随——软链既能越权也能藏东西。
//   - 敏感点目录（.ssh/.aws/.gnupg/.docker/.kube/.config）连名字都不回传，更别提内容。
//   - 只回目录；只有 node_bin 这一类「目标是文件」才额外回可执行文件，绝不暴露其它文件名。
//   - 条目数封顶（maxEntries），避免一个巨型目录把响应撑爆或把遍历拖死。
package pathpick

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"weichuan/internal/mpupload"
)

// defaultRoots 默认允许浏览的根目录白名单：部署时放代码 / 依赖 / 运行时的常规位置。
// 这些是「经验上的安全默认」——既覆盖绝大多数部署，又不把 /etc / /proc 这类要害敞开。
// 具体部署若把东西放在别处，由调用方通过 extraRoots 把「当前已配置路径及其父目录」补进来，
// 这样管理员即便一开始把路径配在了白名单外，也能围着那个路径继续浏览。
var defaultRoots = []string{
	"/opt", "/srv", "/usr/local", "/usr/src", "/var/www",
	"/home", "/root", "/data", "/app", "/www", "/mnt",
}

// deniedDotDirs 敏感点目录：里面是凭据 / 密钥 / 客户端配置。
// 即便落在允许根目录内，也绝不回传其**名称**（更别提内容）——回传名字本身就泄漏了部署形态。
var deniedDotDirs = map[string]bool{
	".ssh": true, ".aws": true, ".gnupg": true, ".docker": true,
	".kube": true, ".config": true,
}

const (
	// maxEntries 单次列举返回条目上限，超出则截断并置 Truncated。
	maxEntries = 300
	// maxSuggestions 自动探测候选的上限。
	maxSuggestions = 20
	// scanMaxDepth Suggest 扫描的最大深度（根算 0）：只做浅扫，不递归整棵文件系统。
	scanMaxDepth = 3
)

// Entry 目录列表里的一项。
type Entry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Type   string `json:"type"`   // "dir" | "file"
	Marker string `json:"marker"` // mpupload.Marker 的结果，空串表示没有徽标
}

// Suggestion 自动探测到的候选路径。
type Suggestion struct {
	Path   string `json:"path"`
	Label  string `json:"label"`
	Reason string `json:"reason"` // 为什么它像正确答案
}

// Result 一次浏览的结果。
//
// Entries / Suggestions 必须是**非 nil** 切片：Go 的 nil slice 会序列化成 JSON 的 null，
// 而界面按数组用（`v-if="suggestions.length"`）。一旦下发 null，渲染函数直接抛 TypeError，
// 弹窗整块渲染失败——表现就是「点浏览按钮没反应」（2026-10-25 用户实测：node_bin 因为
// 总有 PATH 候选所以正常，另外两项没扫到候选就变成 null，窗口弹不出来）。
type Result struct {
	Kind          string       `json:"kind"`          // 本次浏览的路径类型（node_bin/module_dir/source_dir）
	Path          string       `json:"path"`          // 实际列举的目录（已解析符号链接）
	Parent        string       `json:"parent"`        // 上一级目录，供 UI 回退
	Home          string       `json:"home"`          // 当前用户家目录，供 UI 一键回家
	Selected      string       `json:"selected"`      // 当前「待选中」的路径（node_bin 下是文件本身，其余是目录）
	SelectedIsDir bool         `json:"selectedIsDir"` // Selected 是否目录（node_bin 借此区分「还没选到文件」）
	Entries       []Entry      `json:"entries"`       // 本目录下的子项
	Truncated     bool         `json:"truncated"`     // 是否因超过上限被截断
	Current       PathStatus   `json:"current"`       // Selected 作为「该类目标」的校验结果
	Suggestions   []Suggestion `json:"suggestions"`   // 自动探测到的候选
}

// PathStatus 某个路径作为当前项时的校验结果。
type PathStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ---- 白名单判定 ----

// mergeRoots 把默认根目录与调用方补充的根目录合并去重。
//
// 顺序有意为之：进程自身的**家目录放最前**，因为 defaultDir 取「第一个存在的根」作为
// 起点——macOS 开发机与单用户部署的项目常放在家目录下（/Users/xxx/...），若不放进来，
// 选择器会一进来就报「不在允许浏览的范围」（2026-10-25 用户实测）。
// 这不新增越权能力：管理员本来就能把 MP_* 配成任意路径、后端据此 exec/读文件，
// 浏览接口只是把「手敲路径」换成「看得见地挑」；敏感点目录仍由 deniedDotDirs 屏蔽。
func mergeRoots(extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(defaultRoots)+len(extra)+1)

	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		seen[home] = true
		out = append(out, home)
	}
	for _, r := range defaultRoots {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, r := range extra {
		r = strings.TrimSpace(r)
		// 白名单只接受绝对路径：相对路径（"." / "node"）会随进程 cwd 漂移，
		// 混进来等于把 cwd 悄悄开放出去。
		if r == "" || !filepath.IsAbs(r) || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// resolveRoot 解析根目录的符号链接；不存在则返回空串（表示它无法容纳任何已存在的路径）。
// 调用方据此跳过不存在的根——不存在的根里不可能有「存在的路径」。
func resolveRoot(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return ""
}

// isAllowed 判断已解析路径是否落在某个允许根目录内（或恰好就是根本身）。
// 入参 resolvedPath 必须是已 EvalSymlinks 的真实路径，否则一个指向 /etc 的软链会骗过前缀比较。
func isAllowed(resolvedPath string, roots []string) bool {
	for _, root := range roots {
		r := resolveRoot(root)
		if r == "" {
			continue
		}
		if resolvedPath == r {
			return true
		}
		if strings.HasPrefix(resolvedPath, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// defaultDir 当调用方没给 dir 时，挑一个「存在且允许」的起点：第一个存在的默认根，
// 都没有则回落家目录（家目录本身也必须过白名单，否则回落空串由调用方报错）。
func defaultDir(roots []string) string {
	for _, root := range roots {
		if resolveRoot(root) != "" {
			return root
		}
	}
	if home, err := os.UserHomeDir(); err == nil && isAllowed(resolveRoot(home), roots) {
		return home
	}
	return ""
}

// ---- 列举 ----

// List 列出一个目录下可供选择的内容。
//
// dir == "" 时回落到 defaultDir（第一个存在的允许根，否则家目录，再否则空串→由调用方报错）。
// kind 决定除了目录外是否还回可执行文件（仅 node_bin）。
//
// 安全拒绝（返回 error，由接口转成 code:1）：
//   - 路径不存在 / 无法解析符号链接；
//   - 路径不在白名单内（含「目录存在但被软链指到白名单外」）；
//   - dir 不是目录（node_bin 允许 dir 直接是某个可执行文件，此时改列其父目录）。
func List(dir string, kind mpupload.Kind, extraRoots []string) (Result, error) {
	// 即便走错误分支也保证数组字段是 []（不是 null）——错误分支虽然只回 code:1，
	// 但同一结构体在成功分支里也会被界面直接消费，默认值就该是「空数组」。
	res := Result{Entries: []Entry{}, Suggestions: []Suggestion{}}
	roots := mergeRoots(extraRoots)
	res.Kind = string(kind)
	if home, err := os.UserHomeDir(); err == nil {
		res.Home = home
	}

	if dir == "" {
		dir = defaultDir(roots)
	}
	if dir == "" {
		return res, fmt.Errorf("没有可浏览的起点目录（白名单内均不存在）")
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return res, fmt.Errorf("目录不存在：%s", dir)
	}
	if !isAllowed(resolved, roots) {
		return res, fmt.Errorf("该路径不在允许浏览的范围内")
	}

	// node_bin 允许直接传入一个可执行文件（管理员点选的那个 node）：这时改列其父目录，
	// 而「当前项校验」针对该文件本身——目录本身不可能是 node 可执行文件。
	listDir := resolved
	validatePath := resolved
	if kind == mpupload.KindNodeBin {
		if fi, statErr := os.Stat(resolved); statErr == nil && !fi.IsDir() {
			parent := filepath.Dir(resolved)
			if pr, perr := filepath.EvalSymlinks(parent); perr == nil && isAllowed(pr, roots) {
				listDir = pr
				validatePath = resolved
			}
		}
	}

	listResolved, err := filepath.EvalSymlinks(listDir)
	if err != nil {
		return res, fmt.Errorf("目录不存在：%s", listDir)
	}
	if !isAllowed(listResolved, roots) {
		return res, fmt.Errorf("该路径不在允许浏览的范围内")
	}

	entries, err := os.ReadDir(listResolved)
	if err != nil {
		return res, fmt.Errorf("读取目录失败：%s", listResolved)
	}

	res.Path = listResolved
	res.Parent = filepath.Dir(listResolved)

	var dirs, files []Entry
	for _, e := range entries {
		name := e.Name()
		// 软链条目：既不展示也不跟随——它能越权也能藏东西（见包注释）。
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		// 敏感点目录连名字都不回传。
		if deniedDotDirs[name] {
			continue
		}
		full := filepath.Join(listResolved, name)
		if e.IsDir() {
			dirs = append(dirs, Entry{
				Name: name, Path: full, Type: "dir",
				Marker: mpupload.Marker(kind, full),
			})
		} else if kind == mpupload.KindNodeBin {
			// 只有 node_bin 这一类「目标是文件」才回可执行文件，绝不暴露别的文件名。
			if info, ierr := e.Info(); ierr == nil && info.Mode().Perm()&0o111 != 0 {
				files = append(files, Entry{
					Name: name, Path: full, Type: "file",
					Marker: mpupload.Marker(kind, full),
				})
			}
		}
	}

	// 目录在前、文件在后，各自按名排序——UI 里目录永远先出现，找起来不乱。
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	// 用 make 而非 append 到 nil：空目录也要下发 []，不能是 null（见 Result 注释）。
	all := make([]Entry, 0, len(dirs)+len(files))
	all = append(all, dirs...)
	all = append(all, files...)
	if len(all) > maxEntries {
		all = all[:maxEntries]
		res.Truncated = true
	}
	res.Entries = all

	// 当前「待选中」的路径：node_bin 若直接传了文件，选中的就是那个文件；
	// 否则就是正在浏览的目录本身。界面据此高亮列表项、决定「选择此路径」是否可点。
	res.Selected = validatePath
	if fi, statErr := os.Stat(validatePath); statErr == nil {
		res.SelectedIsDir = fi.IsDir()
	}

	// 当前项（无论是目录还是点选的文件）作为该类目标的校验结果。
	ok, msg := mpupload.CheckPath(kind, validatePath)
	res.Current = PathStatus{OK: ok, Message: msg}

	return res, nil
}

// ---- 自动探测 ----

// maxScanDirs 自动探测最多访问多少个目录。
// 家目录可能极其庞大：把它纳入白名单后，若不加预算，一次「打开选择器」的探测会扫到
// 几十秒（2026-10-25 实测 70s+）。这里把扫描量钉死，宁可少给几个候选，也不让界面卡住。
const maxScanDirs = 2000

// scanner 一次自动探测的上下文：携带 kind、结果集、已访问目录计数，把扫描量钉死。
// 扫描口径与列举一致：不跟随软链、不进入敏感点目录、只下探到 scanMaxDepth。
type scanner struct {
	kind    mpupload.Kind
	out     []Suggestion
	seen    map[string]bool
	visited int
}

func (s *scanner) add(path, reason string) {
	if path == "" || s.seen[path] {
		return
	}
	s.seen[path] = true
	s.out = append(s.out, Suggestion{Path: path, Label: filepath.Base(path), Reason: reason})
}

func (s *scanner) full() bool { return len(s.out) >= maxSuggestions }

func (s *scanner) scan(dir string, depth int) {
	if depth > scanMaxDepth || s.visited >= maxScanDirs || s.full() {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if s.visited >= maxScanDirs || s.full() {
			return
		}
		name := e.Name()
		if deniedDotDirs[name] {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !e.IsDir() {
			continue
		}
		s.visited++
		child := filepath.Join(dir, name)
		switch s.kind {
		case mpupload.KindSourceDir:
			// 命中工程目录：含 project.config.json。
			if isFile(filepath.Join(child, "project.config.json")) {
				s.add(child, "含 project.config.json")
			}
		case mpupload.KindModuleDir:
			// 命中装了依赖的目录：含 node_modules/miniprogram-ci。
			if isDir(filepath.Join(child, "node_modules", "miniprogram-ci")) {
				s.add(child, "含 node_modules/miniprogram-ci")
			}
		}
		s.scan(child, depth+1)
	}
}

// Suggest 自动探测某类路径的候选，免去管理员一层层翻目录。
// 只做浅扫（深度 ≤ scanMaxDepth、访问目录数 ≤ maxScanDirs、不跟随软链、跳过敏感点目录），
// 因此不会把整棵文件系统拖进来。
func Suggest(kind mpupload.Kind, extraRoots []string) []Suggestion {
	if kind == mpupload.KindNodeBin {
		// node 走 LookPath + 常见绝对位置，不扫描目录。
		// 用 make 起手：没有候选时要回 []，不能回 nil（nil 会序列化成 null，界面按数组用）。
		out := make([]Suggestion, 0, 4)
		seen := map[string]bool{}
		add := func(path, reason string) {
			if path == "" || seen[path] {
				return
			}
			seen[path] = true
			out = append(out, Suggestion{Path: path, Label: filepath.Base(path), Reason: reason})
		}
		if p, err := exec.LookPath("node"); err == nil {
			if rp, rerr := filepath.EvalSymlinks(p); rerr == nil {
				p = rp
			}
			add(p, "在 PATH 中")
		}
		for _, c := range []string{
			"/usr/local/bin/node", "/usr/bin/node", "/opt/homebrew/bin/node",
		} {
			if fi, serr := os.Stat(c); serr == nil && !fi.IsDir() {
				if rp, rerr := filepath.EvalSymlinks(c); rerr == nil {
					add(rp, "常见安装位置")
				}
			}
		}
		if len(out) > maxSuggestions {
			out = out[:maxSuggestions]
		}
		return out
	}

	// 目录类：**先扫已配置的路径**（extraRoots），再扫默认根与家目录——已配过的部署能
	// 立刻命中，而不会被庞大的家目录先耗尽扫描预算。
	roots := mergeRoots(extraRoots)
	ordered := make([]string, 0, len(roots))
	seenRoot := map[string]bool{}
	for _, r := range extraRoots {
		r = strings.TrimSpace(r)
		if r != "" && filepath.IsAbs(r) && !seenRoot[r] {
			seenRoot[r] = true
			ordered = append(ordered, r)
		}
	}
	for _, r := range roots {
		if !seenRoot[r] {
			seenRoot[r] = true
			ordered = append(ordered, r)
		}
	}

	s := &scanner{kind: kind, out: make([]Suggestion, 0, 4), seen: map[string]bool{}}
	for _, root := range ordered {
		if s.visited >= maxScanDirs || s.full() {
			break
		}
		if resolveRoot(root) == "" {
			continue
		}
		s.scan(root, 0)
	}
	return s.out
}

// isDir / isFile 是 pathpick 自用的存在性判断（不导出，避免与 mpupload 的同名私有函数耦合）。
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
