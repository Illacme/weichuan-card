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

// P0b 出参侧接入 + 写入侧接线守门：
//
// 第五十三轮接了公开出参（9 条公开 GET + signHandler），第五十四轮接写入侧
// （W1 上传 / W2 素材 / W3 小程序码 / W5 预签名改用带前缀键），第五十七轮把后台出参
// （R11–R15：content.go 的公司信息/简介/分类/案例/服务、apply.go 的「我的」与导出）也收口。
// 守门现在守四件事：
//  1. 写入侧 API（NormalizeRef / ObjectKeyFor / ParseRef / IsNormalizedRef）只允许在
//     sanctioned 写侧 / 出参侧文件出现，防止有人把键生成散落到后台接口；
//  2. storage.ResolveRef 只允许在 model/refs.go 内调用（集中口径，handler 走 model 封装）；
//  3. model.ResolveResourceRef / .ResolveRefs() 只许在出参侧 sanctioned 文件调用，
//     防止有人顺手把解析接到写入路径（那会让解析值被 Save 回库，形态静默退回历史形态）；
//  4. album 这类「模型方法解不开、只能在展开点手工解析」的字段，展开点必须跟着解析
//     （见 TestAlbumExpansionSitesResolved）。
//
// 若将来要扩展接入点，先更新本守门的 sanctioned 集合与 docs/tenant-storage-url-boundaries.md，
// 而不是把断言删掉。
func TestRefWiringGate(t *testing.T) {
	const root = ".." // internal/storage → internal

	// 允许出现 ref API 的文件（出参侧 + 写入侧 + 签名修正 + 各自单测）。
	sanctioned := map[string]bool{
		filepath.Join(root, "model", "refs.go"):              true, // 解析口径集中地
		filepath.Join(root, "model", "refs_test.go"):         true, // model 解析单测
		filepath.Join(root, "handler", "handler.go"):         true, // 9 条公开只读接口出参
		filepath.Join(root, "handler", "storage.go"):         true, // signHandler 用 ObjectKeyFromURL / W5 预签名用 ObjectKeyFor
		filepath.Join(root, "handler", "ref_wiring_test.go"): true, // signHandler 回归
		filepath.Join(root, "handler", "upload.go"):          true, // W1 上传主入口用 ObjectKeyFor 生成带前缀键
		filepath.Join(root, "handler", "assets.go"):          true, // W2 素材采用用 ObjectKeyFor 落 p/ 前缀
		filepath.Join(root, "handler", "wechat.go"):          true, // W3 小程序码用 ObjectKeyFor 落 p/ 前缀
		filepath.Join(root, "handler", "content.go"):         true, // 后台内容类出参（R11–R13）+ projectToMap 的 cover/album
		filepath.Join(root, "handler", "apply.go"):           true, // 小程序「我的」与后台员工导出（avatar）
		// 第五十九轮：写入侧归一的唯一落地点。所有业务表单都经它调 storage.NormalizeOrKeep，
		// 不允许各自直接调纯函数——否则「读写从哪进」这件事就散在十几个 handler 里了。
		filepath.Join(root, "handler", "resourceref.go"):      true,
		filepath.Join(root, "handler", "resourceref_test.go"): true,
	}

	writeAPI := regexp.MustCompile(`storage\.(NormalizeRef|NormalizeOrKeep|ObjectKeyFor|ParseRef|IsNormalizedRef)\s*\(`)
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
			if writeAPI.MatchString(line) && !sanctioned[p] {
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
		"写入侧 API 只允许在 sanctioned 写侧 / 出参侧文件内调用；违规：%v", writeOffenders)
	require.Empty(t, resolveRefOffenders,
		"storage.ResolveRef 只允许在 model/refs.go 内调用；违规：%v", resolveRefOffenders)
	require.Empty(t, resolveOffenders,
		"ResolveResourceRef / ResolveRefs 只允许在出参侧 sanctioned 文件调用；违规：%v", resolveOffenders)
}

// album 是多租户存储里最特殊的资源字段：`json:"-"`、列类型 json、存的是 URL 数组字符串，
// 模型方法（ResolveRefs）解不开它，只能在两处展开点手工解析。
//
// 这条守门盯两件事：
//  1. 展开点数量必须还是 2（公开 /api/projects/:id、后台 projectToMap）。新增第三处、
//     或者某处被删，都要在这里显式确认——否则新展开点忘了解析，表现只是「后台图集裂图」；
//  2. 有展开点的文件必须真的调了 model.ResolveResourceRefs。
func TestAlbumExpansionSitesResolved(t *testing.T) {
	const root = ".."
	const wantSites = 2
	albumUnmarshal := regexp.MustCompile(`json\.Unmarshal\(\[\]byte\(\w+\.Album\)`)

	sites := 0
	var unparsed []string
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
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(b)
		n := len(albumUnmarshal.FindAllString(src, -1))
		if n == 0 {
			return nil
		}
		sites += n
		if !strings.Contains(src, "model.ResolveResourceRefs(") {
			unparsed = append(unparsed, p)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, wantSites, sites,
		"album 展开点数量变了（%d→%d）：新增/删除展开点都必须同步解析与 docs/tenant-storage-url-boundaries.md", wantSites, sites)
	require.Empty(t, unparsed, "这些文件展开了 album 却没调 model.ResolveResourceRefs：%v", unparsed)
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
		// 第五十九轮：写入侧宽松归一的闸门。它同样必须是纯函数——
		// 一旦它开始读 env / 写日志，handler 那条「回滚开关只有一个」的承诺就散了。
		"func NormalizeOrKeep(",
	} {
		require.Contains(t, src, want, "ref.go 缺少设计稿约定的 API %q", want)
	}
}
