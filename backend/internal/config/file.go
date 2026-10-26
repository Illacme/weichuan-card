package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// 配置文件（runtime.env）支持：让「安装向导」有地方落盘，同时不引入双源歧义。
//
// 优先级只有一条规则，且对所有配置项一致：
//
//	环境变量 > 配置文件 > 内置缺省值
//
// 为什么这样排：
//   - 环境变量赢：容器编排 / systemd 注入了就是显式意图，配置文件是安装向导的产物，
//     不该反过来覆盖运维在编排里显式写的东西；
//   - 配置文件赢内置缺省：安装向导把 DB_DSN / JWT_KEY / 超管口令写进文件，
//     进程重启后必须读到，否则「装完重启又回到引导页」；
//   - 文件路径自己只能来自 CONFIG_FILE 环境变量——让文件指定自己的位置是先有鸡
//     还是先有蛋，不绕这个圈子。
//
// 空串 / 纯空白一律视为「未设置」（与既有 getEnv 对环境变量的语义一致），
// 这样「界面上清空某项」和「环境变量里给空串」都是同一个意思：回退下一级。

// ConfigFileDefault 配置文件默认路径（相对进程工作目录）。
// 与 uploads / SQLite 数据一样收敛在 data/ 下，备份和挂载都只照顾一个目录。
const ConfigFileDefault = "./data/runtime.env"

var (
	fileMu    sync.Mutex
	fileCache = map[string]map[string]string{}
)

// ConfigFilePath 返回配置文件路径：CONFIG_FILE 环境变量优先，缺省 ./data/runtime.env。
func ConfigFilePath() string {
	if p := strings.TrimSpace(os.Getenv("CONFIG_FILE")); p != "" {
		return p
	}
	return ConfigFileDefault
}

// Resolved 返回某个配置项的实际来源值（环境变量 > 配置文件），两者都没有则空串。
// 空串 = 未配置。供 setup.Unconfigured 等「判断有没有配」的场景使用——
// 不能用 Load() 之后的 Config 值判断，那里面混入了内置缺省值。
func Resolved(key string) string {
	v, _ := lookup(key)
	return v
}

// lookup 按优先级取值。空串/纯空白一律算未设置（见包注释）。
func lookup(key string) (string, bool) {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v, true
	}
	if v := fileValues(ConfigFilePath())[key]; strings.TrimSpace(v) != "" {
		return v, true
	}
	return "", false
}

// fileValues 读配置文件并缓存（按路径）。文件不存在 = 无配置值，不是错误：
// 首次部署时这个文件本来就还不存在。
func fileValues(path string) map[string]string {
	fileMu.Lock()
	defer fileMu.Unlock()
	if v, ok := fileCache[path]; ok {
		return v
	}
	v := parseFile(path)
	fileCache[path] = v
	return v
}

// parseFile 解析 KEY=VALUE 行式配置。
// 规则刻意与 .env 保持一致（运维已经把 .env 的直觉带过来了）：
//   - # 开头为注释，空行忽略；
//   - 成对的单/双引号会被剥掉（引号内的空格保留）；
//   - 解析不了的行忽略而不是报错——半份配置好过半份错误。
func parseFile(path string) map[string]string {
	vals := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return vals
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		if k == "" {
			continue
		}
		v := strings.TrimSpace(line[i+1:])
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		vals[k] = v
	}
	return vals
}

// WriteFileValues 合并式原子写配置文件。
//
//   - 合并而非覆盖：安装向导只写自己负责的几个键（DB_DSN / JWT_KEY / ADMIN_*），
//     运维手工加的其他键不能被一次安装抹掉；
//   - 原子：先写同目录临时文件再 rename，写到一半断电不会留下半份配置
//     （半份配置比没有配置更危险：DB_DSN 有值但 JWT_KEY 缺失会让进程起不来）；
//   - 0600：文件里是数据库口令和签名密钥，不能组/全局可读。
func WriteFileValues(path string, kv map[string]string) error {
	if len(kv) == 0 {
		return fmt.Errorf("没有可写入的配置项")
	}
	for k := range kv {
		if strings.ContainsAny(k, "=\n") {
			return fmt.Errorf("配置项名 %q 含非法字符", k)
		}
	}
	for k, v := range kv {
		// 换行会破坏「一行一项」的格式（解析按行切），必须在写盘前拦住，
		// 而不是写出一份读回来会静默变形的配置。
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("配置项 %s 的值含换行符，无法写入配置文件", k)
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("创建配置目录 %s 失败：%w", dir, err)
	}
	merged := parseFile(path)
	for k, v := range kv {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 稳定顺序：便于 diff、审阅与排障

	var b strings.Builder
	b.WriteString("# weichuan 运行配置。优先级：环境变量 > 本文件 > 内置缺省值。\n")
	b.WriteString("# 由安装向导 / 运维维护；含敏感值，权限 0600，请勿提交进版本库。\n")
	for _, k := range keys {
		b.WriteString(k + "=" + quoteValue(merged[k]) + "\n")
	}

	tmp, err := os.CreateTemp(dir, ".runtime.env-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	// 清理兜底：rename 成功后这一步只是「删一个已不存在的路径」的 no-op，
	// 失败（文件不存在）正是预期结果，无需判错。
	// 必须是 defer：写成同步调用会在 rename 之前就把临时文件删掉。
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(b.String()); err != nil {
		// 已经要返回写错误了，关闭失败不改变结论（后面还有一步显式 Close 兜着）
		_ = tmp.Close()
		return fmt.Errorf("写临时文件失败：%w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置文件权限失败：%w", err)
	}
	// 这一步的 Close 错误**必须**判：写成功而 Close 失败意味着数据未必落盘，
	// 此时 rename 上去的就是一个残缺的配置文件——而配置文件坏了，连改回来的入口都没了。
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败：%w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换配置文件失败：%w", err)
	}
	// 兜底：rename 保留的是临时文件的权限，而临时文件上面已经 Chmod(0o600) 了，
	// 这里再设一次只是双保险；失败不影响结果（权限已由上面的 Chmod 保证）。
	_ = os.Chmod(path, 0o600)

	fileMu.Lock()
	delete(fileCache, path)
	fileMu.Unlock()
	return nil
}

// quoteValue 值含首尾空白或引号时加双引号包裹，避免解析时被 TrimSpace 吃掉。
// 刻意不做转义：parseFile 只剥最外层一对引号、不动内容，
// 因此 `has"quote` 写成 `"has"quote"` 读回来仍是 `has"quote`。
// 引号内的值不含换行（换行值在 WriteFileValues 入口就被拒绝）。
func quoteValue(v string) string {
	if v == "" {
		return `""`
	}
	if v != strings.TrimSpace(v) || strings.ContainsAny(v, `"'`) {
		return `"` + v + `"`
	}
	return v
}
