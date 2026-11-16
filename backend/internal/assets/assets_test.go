package assets

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 开源素材库是编译期内嵌的（go:embed），随二进制分发，部署时不需要额外挂载目录。
// 这组测试守的是三件事：
// 1) 清单与文件真的对得上（gen_library.py 生成后忘拷文件，线上就是一片裂图）；
// 2) 分页/过滤的边界不会把越界下标喂给切片（panic 在这里比 500 更难查）；
// 3) 路径读取不会被 ".." 穿越到 embed 根之外。

// TestManifestFilesExist 清单里每一条的 File/Thumb 都必须真能读到。
// 这条比任何字段断言都值钱：素材是脚本生成的，清单先落盘、文件后拷贝，
// 中间任何一步漏了，前端拿到的就是 404。
func TestManifestFilesExist(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	require.NotEmpty(t, m.Items)

	for _, it := range m.Items {
		require.NotEmpty(t, it.ID)
		require.NotEmpty(t, it.File, "素材 %s 缺 file", it.ID)

		data, err := ReadFile(it.File)
		require.NoError(t, err, "素材 %s 的文件读不到：%s", it.ID, it.File)
		require.NotEmpty(t, data, "素材 %s 的文件是空的：%s", it.ID, it.File)

		if it.Thumb != "" {
			_, err = ReadFile(it.Thumb)
			require.NoError(t, err, "素材 %s 的缩略图读不到：%s", it.ID, it.Thumb)
		}
	}
}

// 分类与调性都在清单里声明过：前端 tab 是按这两个列表渲染的，
// 出现清单外的 category/style 会让素材「消失在所有筛选里」。
func TestItemsReferenceDeclaredTaxonomy(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)

	cats := map[string]bool{}
	for _, c := range m.Categories {
		cats[c.Key] = true
	}
	styles := map[string]bool{}
	for _, s := range m.Styles {
		styles[s.Key] = true
	}
	require.NotEmpty(t, cats)
	require.NotEmpty(t, styles)

	for _, it := range m.Items {
		require.True(t, cats[it.Category], "素材 %s 的分类 %q 未在 categories 中声明", it.ID, it.Category)
		require.True(t, styles[it.Style], "素材 %s 的调性 %q 未在 styles 中声明", it.ID, it.Style)
	}
}

// 清单只解析一次并缓存：素材选择器会被频繁打开，重复解析 JSON 纯属浪费。
func TestLoadCachesManifest(t *testing.T) {
	m1, err := Load()
	require.NoError(t, err)
	m2, err := Load()
	require.NoError(t, err)
	require.Same(t, m1, m2)
}

func TestFindHitAndMiss(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)

	got, err := Find(m.Items[0].ID)
	require.NoError(t, err)
	require.Equal(t, m.Items[0].ID, got.ID)

	_, err = Find("no-such-asset")
	require.Error(t, err)
	require.Contains(t, err.Error(), "素材不存在")
}

// ---- 分页边界 ----

// Page<1 / Size<1 走安全缺省，而不是把负下标喂给切片（那是 panic，不是 500）
func TestListAppliesDefaultPagingForInvalidInput(t *testing.T) {
	r := List(Query{Page: 0, Size: 0})

	require.Equal(t, 1, r.Page)
	require.Equal(t, DefaultPageSize, r.Size)
	require.LessOrEqual(t, len(r.Items), DefaultPageSize)
	require.NotNil(t, r.Items, "空结果也要是空切片，nil 会被序列化成 null")
}

func TestListCapsPageSize(t *testing.T) {
	r := List(Query{Page: 1, Size: 9999})

	require.Equal(t, MaxPageSize, r.Size)
	require.LessOrEqual(t, len(r.Items), MaxPageSize)
}

// 页码越界要夹到最后一页，而不是返回空列表（前端会以为「没有素材」）
func TestListClampsOutOfRangePage(t *testing.T) {
	r := List(Query{Page: 100000, Size: 1})

	require.Equal(t, r.Pages, r.Page)
	require.NotEmpty(t, r.Items)
}

func TestListPageCount(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)

	const size = 5
	r := List(Query{Page: 1, Size: size})
	require.Equal(t, len(m.Items), r.Total)

	want := len(m.Items) / size
	if len(m.Items)%size != 0 {
		want++
	}
	require.Equal(t, want, r.Pages)

	// 最后一页的条数
	last := List(Query{Page: r.Pages, Size: size})
	require.Equal(t, r.Total-(r.Pages-1)*size, len(last.Items))
}

// ---- 过滤 ----

func TestListFiltersByCategory(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	cat := m.Items[0].Category

	r := List(Query{Page: 1, Size: MaxPageSize, Type: cat})
	require.NotEmpty(t, r.Items)
	for _, it := range r.Items {
		require.Equal(t, cat, it.Category)
	}

	// 换个不存在的分类：空结果但结构完整
	empty := List(Query{Type: "no-such-category"})
	require.Equal(t, 0, empty.Total)
	require.Equal(t, 0, empty.Pages)
	require.Empty(t, empty.Items)
	require.NotNil(t, empty.Items)
}

func TestListFiltersByStyle(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	style := m.Items[0].Style

	r := List(Query{Page: 1, Size: MaxPageSize, Style: style})
	require.NotEmpty(t, r.Items)
	for _, it := range r.Items {
		require.Equal(t, style, it.Style)
	}
}

// 关键字同时匹配名称与标签，大小写不敏感，前后空格要吃掉
func TestListKeywordMatchesNameAndTags(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	target := m.Items[0]

	byName := List(Query{Page: 1, Size: MaxPageSize, Keyword: strings.ToUpper(target.Name)})
	require.NotEmpty(t, byName.Items, "关键字应按大小写不敏感命中名称")

	if len(target.Tags) > 0 {
		byTag := List(Query{Page: 1, Size: MaxPageSize, Keyword: "  " + target.Tags[0] + "  "})
		require.NotEmpty(t, byTag.Items, "关键字应命中标签，且忽略首尾空格")
	}

	none := List(Query{Keyword: "zzz-不存在的关键字-zzz"})
	require.Equal(t, 0, none.Total)
}

// styleCounts 必须先排除 style 自身的影响，否则用户选中「简约」后，
// 其它调性的计数全变 0，前端会把它们全部隐藏——等于选一次就锁死在一个调性里。
func TestStyleCountsIgnoreSelectedStyle(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	require.NotEmpty(t, m.Styles)

	base := List(Query{Page: 1, Size: MaxPageSize})
	require.Len(t, base.StyleCounts, len(m.Styles))

	style := m.Styles[0].Key
	filtered := List(Query{Page: 1, Size: MaxPageSize, Style: style})

	require.Equal(t, base.StyleCounts, filtered.StyleCounts, "选中调性不应改变各调性的计数")

	// 顺序与 manifest.styles 一致（前端按这个顺序渲染 tab）
	for i, s := range m.Styles {
		require.Equal(t, s.Key, filtered.StyleCounts[i].Key)
		require.Equal(t, s.Name, filtered.StyleCounts[i].Name)
	}
}

// styleCounts 的计数要和该调性实际能查到的条数对得上
func TestStyleCountsMatchActualTotals(t *testing.T) {
	all := List(Query{Page: 1, Size: 1})
	for _, sc := range all.StyleCounts {
		r := List(Query{Page: 1, Size: 1, Style: sc.Key})
		require.Equal(t, sc.Count, r.Total, "调性 %s 的计数与实际条数不一致", sc.Key)
	}
}

// ---- ContentType ----

func TestContentType(t *testing.T) {
	cases := map[string]string{
		"a/b/icon.png":   "image/png",
		"cover.jpg":      "image/jpeg",
		"cover.JPEG":     "image/jpeg",
		"x.webp":         "image/webp",
		"x.gif":          "image/gif",
		"weird.pdf":      "application/octet-stream",
		"no-extension":   "application/octet-stream",
		"dir.d/name.svg": "application/octet-stream",
		"upper/case.PNG": "image/png",
		"trailing.dot.":  "application/octet-stream",
		"":               "application/octet-stream",
	}
	for rel, want := range cases {
		require.Equal(t, want, ContentType(rel), "ContentType(%q)", rel)
	}
}

// ---- ReadFile 的路径安全 ----

// 这里锁的是**行为契约**：embed 根之外的文件一律读不到，且返回的是
// fs.ErrNotExist（调用方据此回 404，而不是 500）。
//
// 实测结论（故意摘掉 ReadFile 里那行 `strings.Contains(rel, "..")` 后本测试仍通过）：
// io/fs 本身就拒绝含 ".." 的路径，那行显式判断只是「快速失败 + 意图明确」，
// 不是安全边界。别把它当成唯一的防线，也别以为删掉它测试会红。
func TestReadFileRejectsTraversal(t *testing.T) {
	for _, rel := range []string{"", "../assets.go", "icons/../../etc/passwd", "..", "a/../../b"} {
		_, err := ReadFile(rel)
		require.ErrorIs(t, err, os.ErrNotExist, "路径 %q 必须被拒绝", rel)
	}
}

// 前导斜杠要归一：调用方可能直接透传以 / 开头的 URL 路径
func TestReadFileAcceptsLeadingSlash(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	target := m.Items[0]

	withSlash, err := ReadFile("/" + target.File)
	require.NoError(t, err)
	plain, err := ReadFile(target.File)
	require.NoError(t, err)
	require.Equal(t, plain, withSlash)
}

// 清单文件本身也在 embed 里（Load 依赖它，这里额外确认版本号有效）
func TestManifestVersion(t *testing.T) {
	m, err := Load()
	require.NoError(t, err)
	require.Greater(t, m.Version, 0)
	require.NotEmpty(t, m.Categories)
}
