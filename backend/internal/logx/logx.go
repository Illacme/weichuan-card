// Package logx 统一日志的输出形态：本地看人话（text），进日志系统看字段（json）。
//
// 为什么要有这个包（C8 集中日志）：
// 生产上一出事，最先被问的是「把当时的日志拉出来看看」。如果日志是 `[WARN] xxx`
// 这样的自由文本，那在 Kibana / Loki 里只能靠正则去捞，「把 5xx 按路径聚合」
// 「按 request_id 串起一次请求」都得靠人肉解析。集中日志的前提是**结构化**。
//
// 为什么不直接换成某个日志库：全站已有上百处 log.Printf，换库要改所有调用点，
// 且会把 `[WARN]` 这类已被运维写进告警规则的前缀一起改掉。这里采用的办法是
// **包装标准 log 的输出目标**——JSON 模式下把已经打出来的每一行原样包成 JSONL，
// 既有调用点一行都不用改，级别从既有的方括号标签里推断（见 levelOfTag）。
// 代价是「新代码想加字段」仍然只能写进文本；真正需要字段化的访问日志走 Access。
package logx

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// 日志格式。
const (
	// FormatText 人读的文本（默认）：保留 [WARN] 等既有前缀，运维已有告警规则不受影响。
	FormatText = "text"
	// FormatJSON 每行一个 JSON 对象（JSONL），供 Loki / ELK / 云日志服务按字段检索。
	FormatJSON = "json"
)

// Fields 附加字段（JSON 模式下展开成顶层键）。
type Fields map[string]any

var (
	mu     sync.RWMutex
	format           = FormatText
	out    io.Writer = os.Stderr
)

// SetFormat 切换全局日志格式，返回实际生效的格式。
// 未知值按 text 处理：配错值最多是「没结构化」，绝不能变成「不打日志」。
func SetFormat(f string) string {
	f = strings.ToLower(strings.TrimSpace(f))
	mu.Lock()
	switch f {
	case FormatJSON:
		format = FormatJSON
		// 时间戳由 JSON 自己的 ts 字段带（RFC3339，含纳秒与时区），
		// 标准 log 再加一个前缀就变成「文本里套 JSON」了，故清掉 flags。
		log.SetFlags(0)
		log.SetOutput(lineSink{})
	case FormatText:
		format = FormatText
		log.SetFlags(log.LstdFlags)
		log.SetOutput(out)
	default:
		// 未知值：保持原格式不动（见函数注释——配错最多是「没结构化」），
		// 下面统一返回实际生效的那个，调用方能据此提示运维。
	}
	got := format
	mu.Unlock()
	return got
}

// Format 当前日志格式。
func Format() string {
	mu.RLock()
	defer mu.RUnlock()
	return format
}

// SetOutput 替换日志输出目标（默认 os.Stderr），返回旧目标。
// 主要给测试捕获输出用；生产不需要调（12-factor：直接写 stderr，由采集器接管）。
func SetOutput(w io.Writer) io.Writer {
	if w == nil {
		w = io.Discard
	}
	mu.Lock()
	old := out
	out = w
	if format == FormatText {
		log.SetOutput(w)
	}
	mu.Unlock()
	return old
}

// currentOut 取当前输出目标。
func currentOut() io.Writer {
	mu.RLock()
	defer mu.RUnlock()
	return out
}

// Writer 返回一个「按当前格式写出」的目标，供标准 log 之外的写入方接入
// （例如 gin.DefaultWriter / gin.DefaultErrorWriter——gin 的启动日志与 panic 栈
// 不走本包的 log.Printf，不接进来的话 JSON 模式下会混进纯文本行）。
// 返回的是动态包装：格式中途切换也能跟上，不需要调用方重新取值。
func Writer() io.Writer { return lineSink{} }

// lineSink 把写入的每一行按当前格式落到 out：JSON 模式包成 JSONL，text 模式原样转发。
// 它同时被注册为标准 log 的输出目标（见 SetFormat）。
type lineSink struct{}

func (lineSink) Write(p []byte) (int, error) {
	w := currentOut()
	if w == nil {
		return len(p), nil
	}
	if Format() != FormatJSON {
		_, err := w.Write(p)
		return len(p), err
	}
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tags, msg := leadingTags(line)
		f := Fields{}
		if len(tags) > 0 {
			f["tag"] = strings.Join(tags, ".")
		}
		writeJSON(w, levelOfTags(tags), msg, f)
	}
	// 必须返回 len(p)：标准 log 会据此判断是否「写入不完整」。
	return len(p), nil
}

// Access 输出一行访问日志。
//
// JSON 模式下 method / path / status 等是真字段（能直接按 status>=500 聚合、
// 按 request_id 串联）；text 模式下与历史格式完全一致（`[ACCESS] GET /x 200 3ms
// req=abc ip=1.2.3.4`），运维既有的 grep 规则不会因为升级而失效。
func Access(method, path string, status int, latency time.Duration, reqID, ip string) {
	ms := latency.Round(time.Millisecond).Milliseconds()
	if Format() == FormatJSON {
		level := "info"
		if status >= 500 {
			level = "warn"
		}
		writeJSON(currentOut(), level, "http_access", Fields{
			"method": method, "path": path, "status": status,
			"latency_ms": ms, "request_id": reqID, "ip": ip,
		})
		return
	}
	line := "%s %s %d %dms req=%s ip=%s"
	if status >= 500 {
		log.Printf("[ACCESS-5XX] "+line, method, path, status, ms, reqID, ip)
		return
	}
	log.Printf("[ACCESS] "+line, method, path, status, ms, reqID, ip)
}

// writeJSON 写一行 JSONL。ts / level / msg 是固定字段，同名输入字段被忽略
// （避免调用方把 level 覆盖成「业务字段」，那样告警规则会失效）。
func writeJSON(w io.Writer, level, msg string, f Fields) {
	if w == nil {
		return
	}
	m := map[string]any{
		"ts":    time.Now().Format(time.RFC3339Nano),
		"level": level,
		"msg":   msg,
	}
	for k, v := range f {
		if k == "ts" || k == "level" || k == "msg" {
			continue
		}
		m[k] = v
	}
	b, err := json.Marshal(m) // map 的键由 encoding/json 排序，输出稳定
	if err != nil {
		// 字段里有 json 编不了的值（chan / func 等）时兜底：宁可只留 msg，
		// 也不能因为记不下来就整行丢失（那正是排障时最需要的那行）。
		b, _ = json.Marshal(map[string]any{
			"ts": time.Now().Format(time.RFC3339Nano), "level": "error",
			"msg": "log fields marshal failed: " + msg,
		})
	}
	_, _ = w.Write(append(b, '\n'))
}

// leadingTags 取出行首连续的方括号标签（最多 4 个），返回标签与剩余正文。
// 处理连续标签是因为既有日志里有 `[MIGRATE][FATAL]` 这种：只认第一个会把
// FATAL 当成普通 INFO，告警规则直接漏掉最严重的那类。
func leadingTags(line string) ([]string, string) {
	rest := line
	var tags []string
	for len(tags) < 4 {
		if !strings.HasPrefix(rest, "[") {
			break
		}
		i := strings.Index(rest, "]")
		if i <= 1 {
			break
		}
		tags = append(tags, rest[1:i])
		rest = strings.TrimSpace(rest[i+1:])
	}
	if len(tags) == 0 {
		return nil, line
	}
	if rest == "" {
		// 整行只有标签（如 `[WARN]`），正文不能是空串，否则日志里看不出发生了什么
		return tags, line
	}
	return tags, rest
}

// levelOfTags 由标签推断级别：取其中最严重的一个。
// 标签是既有约定（[WARN] / [ERROR] / [FATAL] / [ACCESS] …），认不出的按 info，
// 绝不因为认不出就丢掉这一行。
func levelOfTags(tags []string) string {
	level := "info"
	rank := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3, "fatal": 4}
	cur := rank[level]
	for _, t := range tags {
		l := levelOfOneTag(t)
		if rank[l] > cur {
			level, cur = l, rank[l]
		}
	}
	return level
}

func levelOfOneTag(tag string) string {
	switch strings.ToUpper(tag) {
	case "FATAL", "PANIC":
		return "fatal"
	case "ERROR", "ERR":
		return "error"
	case "WARN", "WARNING", "ACCESS-5XX":
		return "warn"
	case "DEBUG":
		return "debug"
	case "INFO", "ACCESS":
		return "info"
	}
	// 未知标签（如 [MIGRATE] / [LICENSE] / [CDN]）：按 info，
	// 标签本身仍在 tag 字段里，检索时照样能筛。
	return "info"
}
