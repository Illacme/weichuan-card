package logx

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 日志形态切换是全局状态（改的是标准 log 的输出目标与 flags），
// 每个用例结束必须还原，否则会污染同包后续用例、也把别的包测试带的日志弄成 JSON。
func withCapture(t *testing.T, format string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldOut := SetOutput(&buf)
	old := SetFormat(format)
	require.Equal(t, format, old, "SetFormat 应返回实际生效的格式")
	t.Cleanup(func() {
		SetFormat(FormatText)
		SetOutput(oldOut)
	})
	return &buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		require.NoErrorf(t, json.Unmarshal([]byte(ln), &m), "每行都必须是合法 JSON：%q", ln)
		out = append(out, m)
	}
	return out
}

// JSON 模式的核心价值：既有代码一行都不用改，打出来的日志就成了可检索的字段。
// 级别从既有的 [WARN] 标签推断——运维已经把 [WARN] 写进告警规则了，别让他们改规则。
func TestJSONModeWrapsExistingLogLines(t *testing.T) {
	buf := withCapture(t, FormatJSON)
	log.Printf("[WARN] 磁盘剩余空间不足")

	got := lines(t, buf)
	require.Len(t, got, 1)
	require.Equal(t, "warn", got[0]["level"])
	require.Equal(t, "WARN", got[0]["tag"])
	require.Equal(t, "磁盘剩余空间不足", got[0]["msg"])
	require.NotEmpty(t, got[0]["ts"], "时间戳不能丢：JSON 模式下标准 log 的 flags 已被清掉")
}

// 连续标签里最严重的那个说了算：[MIGRATE][FATAL] 若只认第一个标签，
// 数据库迁移失败这种致命错误会被记成普通 info，告警规则直接漏掉。
func TestJSONModeTakesMostSevereTag(t *testing.T) {
	buf := withCapture(t, FormatJSON)
	log.Printf("[MIGRATE][FATAL] 迁移失败，已停止启动")
	log.Printf("[INFO] 普通信息")
	log.Printf("没有标签的一行")

	got := lines(t, buf)
	require.Len(t, got, 3)
	require.Equal(t, "fatal", got[0]["level"])
	require.Equal(t, "MIGRATE.FATAL", got[0]["tag"])
	require.Equal(t, "迁移失败，已停止启动", got[0]["msg"])

	require.Equal(t, "info", got[1]["level"])
	require.Equal(t, "INFO", got[1]["tag"])

	require.Equal(t, "info", got[2]["level"], "认不出的标签按 info，绝不能丢行")
	require.Nil(t, got[2]["tag"])
	require.Equal(t, "没有标签的一行", got[2]["msg"])
}

// 文本模式必须与历史格式一致：升级后运维既有的 grep 规则不能失效。
func TestTextModeKeepsBracketPrefix(t *testing.T) {
	buf := withCapture(t, FormatText)
	log.Printf("[WARN] 磁盘剩余空间不足")
	require.Contains(t, buf.String(), "[WARN] 磁盘剩余空间不足")
	require.NotContains(t, buf.String(), `"level"`, "文本模式不能变成 JSON")
}

// 配错格式值按 text 处理：最多是「没结构化」，绝不能变成「不打日志」。
func TestUnknownFormatFallsBackToText(t *testing.T) {
	withCapture(t, FormatText)
	require.Equal(t, FormatText, SetFormat("xml"))
	require.Equal(t, FormatText, Format())
	require.Equal(t, FormatJSON, SetFormat("JSON"), "大小写与空白应容忍")
	require.Equal(t, FormatJSON, Format())
}

// 访问日志字段化是集中日志最直接的收益：能按 status 聚合 5xx、按 request_id 串一次请求。
func TestAccessEmitsStructuredFields(t *testing.T) {
	buf := withCapture(t, FormatJSON)
	Access("GET", "/api/employees", 200, 3*time.Millisecond, "req-1", "1.2.3.4")
	Access("POST", "/api/login", 500, 12*time.Millisecond, "req-2", "1.2.3.5")

	got := lines(t, buf)
	require.Len(t, got, 2)
	require.Equal(t, "http_access", got[0]["msg"])
	require.Equal(t, "info", got[0]["level"])
	require.Equal(t, "GET", got[0]["method"])
	require.Equal(t, "/api/employees", got[0]["path"])
	require.EqualValues(t, 200, got[0]["status"])
	require.EqualValues(t, 3, got[0]["latency_ms"])
	require.Equal(t, "req-1", got[0]["request_id"])
	require.Equal(t, "1.2.3.4", got[0]["ip"])

	require.Equal(t, "warn", got[1]["level"], "5xx 必须能被按级别筛出来")
}

// 文本模式下访问日志的形状要一字不改（[ACCESS] / [ACCESS-5XX] 前缀是既有约定）。
func TestAccessTextFormatUnchanged(t *testing.T) {
	buf := withCapture(t, FormatText)
	Access("GET", "/ok", 200, 3*time.Millisecond, "r1", "1.2.3.4")
	Access("GET", "/oops", 500, 4*time.Millisecond, "r2", "1.2.3.4")

	out := buf.String()
	require.Contains(t, out, "[ACCESS] GET /ok 200 3ms req=r1 ip=1.2.3.4")
	require.Contains(t, out, "[ACCESS-5XX] GET /oops 500 4ms req=r2 ip=1.2.3.4")
}

// 字段里有编码不了的值（chan / func）时兜底：宁可只留 msg，
// 也不能整行丢失——排障时最需要的往往正是那一行。
func TestWriteJSONFallsBackOnMarshalError(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, "info", "关键事件", Fields{"bad": make(chan int)})
	require.Contains(t, buf.String(), "log fields marshal failed: 关键事件")
}

// ts / level / msg 是固定字段，不能被业务字段顶掉（否则告警规则会失效）。
func TestReservedKeysNotOverridable(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, "warn", "原始消息", Fields{"level": "info", "msg": "被顶掉了吗"})
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Equal(t, "warn", m["level"])
	require.Equal(t, "原始消息", m["msg"])
}

// SetOutput(nil) 要落成丢弃而不是 panic（nil writer 会让每次写日志都崩）。
func TestSetOutputNilDiscards(t *testing.T) {
	old := SetOutput(nil)
	t.Cleanup(func() { SetOutput(old) })
	require.NotPanics(t, func() {
		log.Printf("[INFO] 不该 panic")
		Access("GET", "/x", 200, time.Millisecond, "r", "1.1.1.1")
	})
	require.Equal(t, io.Discard, currentOut())
}

// Writer() 给 gin 这类「自己拿 io.Writer」的组件用：格式中途切换也要跟上，
// 否则 gin 的 panic 栈会以纯文本混进 JSONL，把那一行变成解析失败。
func TestWriterFollowsCurrentFormat(t *testing.T) {
	var buf bytes.Buffer
	old := SetOutput(&buf)
	t.Cleanup(func() { SetFormat(FormatText); SetOutput(old) })

	SetFormat(FormatJSON)
	_, err := Writer().Write([]byte("[GIN-debug] 启动完成\n"))
	require.NoError(t, err)
	got := lines(t, &buf)
	require.Len(t, got, 1)
	require.Equal(t, "GIN-debug", got[0]["tag"])
	require.Equal(t, "启动完成", got[0]["msg"])

	SetFormat(FormatText)
	_, err = Writer().Write([]byte("纯文本一行\n"))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "纯文本一行")
}

// 默认状态：未调用 SetFormat 时是文本、输出到 stderr（本地开发与既有部署不受影响）。
func TestDefaults(t *testing.T) {
	require.Equal(t, FormatText, Format())
	require.Equal(t, os.Stderr, currentOut())
}
