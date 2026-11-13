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

// P0b 出参侧接入守门：把「不许接线」升级为「只允许在指定出参侧文件接」。
//
// 三件事必须守住：
//  1. 写入侧 API（NormalizeRef / ObjectKeyFor / ParseRef / IsNormalizedRef）本轮**一律**
//     不得出现在 storage 包之外——那是下一阶段（写侧收敛）的事，提前接会立刻裂图；
//  2. storage.ResolveRef 只允许在 model/refs.go 内调用（集中口径，handler 走 model 封装）；
//  3. model.ResolveResourceRef / .ResolveRefs() 只许在出参侧 sanctioned 文件调用，
//     防止有人顺手把解析接到后台接口（R11–R15）或写入路径，绕过边界清单。
//
// 若将来要扩展接入点，先更新本守门的 sanctioned 集合与 docs/tenant-storage-url-boundaries.md，
// 而不是把断言删掉。
func TestRefWiringGate(t *testing.T) {
	const root = ".." // internal/storage → internal

	// 本轮允许出现 ref API 的文件（出参侧 + 签名修正 + 各自单测）。
	sanctioned := map[string]bool{
		filepath.Join(root, "model", "refs.go"):              true, // 解析口径集中地
		filepath.Join(root, "model", "refs_test.go"):         true, // model 解析单测
		filepath.Join(root, "handler", "handler.go"):         true, // 9 条公开只读接口出参
		filepath.Join(root, "handler", "storage.go"):         true, // signHandler 改用 ObjectKeyFromURL
		filepath.Join(root, "handler", "ref_wiring_test.go"): true, // signHandler 回归
	}

	writeAPI := regexp.MustCompile(`storage\.(NormalizeRef|ObjectKeyFor|ParseRef|IsNormalizedRef)\s*\(`)
	resolveRefAPI := regexp.MustCompile(`storage\.ResolveRef\s*\(`)
	resolveResourceRefAPI := regexp.MustCompile(`model\.ResolveResourceRef\s*\(|\.ResolveRefs\(\)`)

	var writeOffenders, resolveRefOffenders, resolveOffenders []string
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
			if writeAPI.MatchString(line) {
				writeOffenders = append(writeOffenders, fmt.Sprintf("%s:%d", p, i+1))
			}
			if resolveRefAPI.MatchString(line) && !sanctioned[p] {
				resolveRefOffenders = append(resolveRefOffenders, fmt.Sprintf("%s:%d", p, i+1))
			}
			if resolveResourceRefAPI.MatchString(line) && !sanctioned[p] {
				resolveOffenders = append(resolveOffenders, fmt.Sprintf("%s:%d", p, i+1))
			}
		}
		return nil
	})
	require.NoError(t, err, "扫描 internal/ 失败（工作目录不是 backend/internal/storage？）")
	require.Empty(t, writeOffenders,
		"写入侧 API 本轮不得接出 storage 包（应在写侧收敛阶段）；违规：%v", writeOffenders)
	require.Empty(t, resolveRefOffenders,
		"storage.ResolveRef 只允许在 model/refs.go 内调用；违规：%v", resolveRefOffenders)
	require.Empty(t, resolveOffenders,
		"ResolveResourceRef / ResolveRefs 只允许在出参侧 sanctioned 文件调用；违规：%v", resolveOffenders)
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
