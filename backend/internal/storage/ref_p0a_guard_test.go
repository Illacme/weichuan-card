package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// P0a 的承诺是「零行为变更、可逆」。只靠人记住「先别接线」不够，
// 这里把它变成一条会红的断言：除了 storage 包自己，任何生产包都不许
// 调用 ref.go 新导出的 API。
//
// P0b 真正开始接入时，本守门**必然变红**——那是有意的信号：它要求接入者
// 先交付「业务 URL 边界清单」，并把本守门替换成逐点接入守门，
// 而不是把这条断言直接删掉蒙混过去。
func TestP0aRefIsNotWiredIntoProduction(t *testing.T) {
	const root = ".." // internal/storage → internal
	call := regexp.MustCompile(`storage\.(NormalizeRef|ResolveRef|ParseRef|ObjectKeyFor|IsNormalizedRef)\s*\(`)

	var offenders []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if p == filepath.Join(root, "storage") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".go" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if call.MatchString(line) {
				offenders = append(offenders,
					fmt.Sprintf("%s:%d: %s", p, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	require.NoError(t, err, "扫描 internal/ 失败（工作目录不是 backend/internal/storage？）")
	require.Empty(t, offenders,
		"P0a 阶段 ref.go 不得接进生产包；先完成 P0b 的边界清单，再替换本守门")
}

// ref.go 必须是纯函数集合：不注册 init、不碰文件系统 / 数据库 / 全局配置，
// 只依赖标准库。否则「整体删掉即可回滚」这个承诺就不成立。
func TestP0aRefIsPureStdlibOnly(t *testing.T) {
	b, err := os.ReadFile("ref.go")
	require.NoError(t, err)
	src := string(b)

	require.NotContains(t, src, "func init(", "ref.go 不得有 init 副作用")
	for _, banned := range []string{
		"github.com/", "gorm.io/", "os.", "ioutil.", "log.", "time.Now", "config.",
	} {
		require.NotContains(t, src, banned,
			"ref.go 只能依赖标准库纯函数，不得出现 %q", banned)
	}
	// 导出的 API 面必须与设计稿 §3.1 对得上，防止悄悄多出一个有副作用的函数。
	for _, want := range []string{
		"type Scope string", "type Ref struct",
		"func ObjectKeyFor(", "func ParseRef(", "func IsNormalizedRef(",
		"func NormalizeRef(", "func ResolveRef(",
	} {
		require.Contains(t, src, want, "ref.go 缺少设计稿约定的 API %q", want)
	}
}
