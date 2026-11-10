package safego

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunRecoversPanic(t *testing.T) {
	done := make(chan struct{})
	var gotName string
	var gotValue any
	var gotStack []byte
	orig := reportPanic
	reportPanic = func(name string, recovered any, stack []byte) {
		gotName, gotValue, gotStack = name, recovered, stack
		close(done)
	}
	t.Cleanup(func() { reportPanic = orig })

	Run("test.panicking", func() { panic("boom") })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("子协程 panic 没有被 recover 上报（进程本该直接崩掉）")
	}
	require.Equal(t, "test.panicking", gotName, "日志必须带任务名，否则一堆 goroutine 分不清谁炸了")
	require.Equal(t, "boom", gotValue)
	require.Contains(t, string(gotStack), "goroutine", "必须带完整堆栈，只打 panic 值排不了障")
}

func TestRunExecutesNormally(t *testing.T) {
	done := make(chan struct{})
	Run("test.normal", func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("任务没有执行")
	}
}

// TestAllAsyncTasksUseSafeGo 静态守门：以后新加的 go fn() 必须经 safego.Run。
//
// gin.Recovery() 只保护请求协程，这个约束靠 code review 很容易漏——尤其
// 「临时加个后台任务」的时候。扫 backend 全仓（排除测试与本包实现本身），
// 剥掉整行注释后不允许出现裸 goroutine 启动。
func TestAllAsyncTasksUseSafeGo(t *testing.T) {
	root := filepath.Join("..", "..")
	goStmt := regexp.MustCompile(`\bgo\s+(func|\w+\()`)
	commentLine := regexp.MustCompile(`(?m)^[ \t]*//.*$`)
	var bad []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "safego.go") {
			return nil
		}
		src, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		clean := commentLine.ReplaceAllString(string(src), "")
		for i, line := range strings.Split(clean, "\n") {
			if goStmt.MatchString(line) {
				bad = append(bad, path+":"+strconv.Itoa(i+1)+" "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, bad, "发现绕过 safego 的裸 goroutine：%v", bad)
}
