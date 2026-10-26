// Package assets 内嵌「常用开源素材库」：PC 后台所有需要图片的位置
// （分类图标 / 服务图标 / 项目封面 / 相册 / 公司简介封面 / 公司 LOGO /
// 员工头像 / 小程序首页背景）都可以直接选内置素材，免去四处找图、按尺寸处理图。
//
// 素材由同目录 gen_library.py 生成（Lucide ISC 图标 + 程序绘制封面/LOGO/背景），
// 按「构图 motif + 配色」解耦——同款换色即可派生新款式，扩充成本低。
// 通过 go:embed 编译进二进制——单文件部署（Dockerfile 只 COPY 二进制）无需额外挂载。
// 重新生成/扩充素材：改 gen_library.py 后运行，再提交 library/ 目录。
package assets

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// libraryFS 内嵌整个 library/ 目录（manifest.json + CREDITS.md + 各分类素材）。
// all: 前缀保证以 . / _ 开头的文件也纳入（当前没有，防御性写法）。
//
//go:embed all:library
var libraryFS embed.FS

// Category 素材分类（选择器顶部 tab）
type Category struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Item 单个素材项
type Item struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"` // icon / cover / logo / avatar / background / poster
	Style    string   `json:"style"`    // 视觉调性：clean / nature / luxury / soft / vivid
	File     string   `json:"file"`     // library 内相对路径，如 icons/icon-house.png
	Thumb    string   `json:"thumb"`    // 预览图路径（缩略版，图标/LOGO/头像与 File 相同）
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Tags     []string `json:"tags"` // 搜索标签（中文名/别名/调性名）
}

// StyleCount 某调性在当前筛选条件下的素材数（前端据此隐藏 0 结果的调性）
type StyleCount struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Query 素材分页查询条件（全部可选，缺省给出安全默认值）
type Query struct {
	Page    int    // 1-based，<1 回落 1
	Size    int    // 每页条数，<1 回落 DefaultPageSize，>MaxPageSize 截断
	Type    string // 分类过滤："" 全部；icon / cover / logo / avatar / background / poster
	Style   string // 调性过滤："" 全部；clean / nature / luxury / soft / vivid
	Keyword string // 关键字：匹配名称或标签（大小写不敏感，中文直接子串匹配）
}

// Result 分页结果
type Result struct {
	Items       []Item       `json:"items"`
	Total       int          `json:"total"` // 过滤后的总条数（非本页条数）
	Page        int          `json:"page"`
	Size        int          `json:"size"`
	Pages       int          `json:"pages"` // 总页数
	StyleCounts []StyleCount `json:"styleCounts"` // 当前 分类+关键字 下各调性的素材数
}

const (
	// DefaultPageSize 选择器默认每页条数：15 = 5 列 × 3 排，弹窗内一次看全、
	// 无需滚动。选择器网格宽度约 830px、卡片最小 150px，正好排满 5 列。
	DefaultPageSize = 15
	// MaxPageSize 单页上限，防止一次拉全量
	MaxPageSize = 60
)

// Manifest 素材清单（gen_library.py 生成）
type Manifest struct {
	Version    int        `json:"version"`
	Styles     []Category `json:"styles"`     // 视觉调性（第二筛选维度）
	Categories []Category `json:"categories"` // 用途分类（第一筛选维度）
	Items      []Item     `json:"items"`
}

var (
	loadOnce sync.Once
	manifest *Manifest
	loadErr  error
)

// Load 读取并缓存内嵌 manifest.json（首次调用解析，之后零成本）
func Load() (*Manifest, error) {
	loadOnce.Do(func() {
		data, err := libraryFS.ReadFile("library/manifest.json")
		if err != nil {
			loadErr = fmt.Errorf("读取素材清单失败: %w", err)
			return
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			loadErr = fmt.Errorf("解析素材清单失败: %w", err)
			return
		}
		if len(m.Items) == 0 {
			loadErr = fmt.Errorf("素材清单为空")
			return
		}
		manifest = &m
	})
	return manifest, loadErr
}

// Find 按素材 ID 查找（不存在返回 error）
func Find(id string) (*Item, error) {
	m, err := Load()
	if err != nil {
		return nil, err
	}
	for i := range m.Items {
		if m.Items[i].ID == id {
			return &m.Items[i], nil
		}
	}
	return nil, fmt.Errorf("素材不存在: %s", id)
}

// List 按条件分页查询素材（纯内存过滤：素材静态内嵌且量级百级，无需索引）。
// 参数非法时不报错，按安全默认处理（Page<1 → 1；Size<1 → 24；Size>60 → 60）。
func List(q Query) Result {
	m, err := Load()
	if err != nil {
		return Result{Items: []Item{}, Page: 1, Size: DefaultPageSize}
	}
	kw := strings.ToLower(strings.TrimSpace(q.Keyword))
	typ := strings.TrimSpace(q.Type)
	sty := strings.TrimSpace(q.Style)

	// 第一遍：按 分类 + 关键字 过滤，得到 styleCounts 的统计基数。
	// 调性计数必须排除 style 本身的影响，否则前端选中某调性后其余调性全变 0。
	base := make([]Item, 0, len(m.Items))
	for _, it := range m.Items {
		if typ != "" && it.Category != typ {
			continue
		}
		if kw != "" && !matchKeyword(it, kw) {
			continue
		}
		base = append(base, it)
	}
	counts := styleCounts(m, base)

	matched := base
	if sty != "" {
		matched = make([]Item, 0, len(base))
		for _, it := range base {
			if it.Style == sty {
				matched = append(matched, it)
			}
		}
	}
	total := len(matched)

	size := q.Size
	if size < 1 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	pages := total / size
	if total%size != 0 {
		pages++
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	if pages > 0 && page > pages {
		page = pages
	}
	start := (page - 1) * size
	end := start + size
	if start > total {
		start, end = total, total
	}
	if end > total {
		end = total
	}
	return Result{
		Items:       matched[start:end],
		Total:       total,
		Page:        page,
		Size:        size,
		Pages:       pages,
		StyleCounts: counts,
	}
}

// styleCounts 统计 base 中各调性的素材数，顺序与 manifest.styles 一致。
// 前端据此隐藏当前条件下 0 结果的调性（例如图标分类下只有「简约清晰」）。
func styleCounts(m *Manifest, base []Item) []StyleCount {
	if len(m.Styles) == 0 {
		return nil
	}
	idx := make(map[string]int, len(m.Styles))
	for _, it := range base {
		if it.Style != "" {
			idx[it.Style]++
		}
	}
	out := make([]StyleCount, 0, len(m.Styles))
	for _, s := range m.Styles {
		out = append(out, StyleCount{Key: s.Key, Name: s.Name, Count: idx[s.Key]})
	}
	return out
}

// matchKeyword 关键字匹配：名称或任一标签含关键字即命中（kw 已小写）
func matchKeyword(it Item, kw string) bool {
	if strings.Contains(strings.ToLower(it.Name), kw) {
		return true
	}
	for _, t := range it.Tags {
		if strings.Contains(strings.ToLower(t), kw) {
			return true
		}
	}
	return false
}

// ContentType 按扩展名推断素材 MIME（采用时写入存储用）
func ContentType(rel string) string {
	switch strings.ToLower(rel[strings.LastIndex(rel, ".")+1:]) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// ReadFile 读取 library 内素材字节（rel 为 Item.File / Item.Thumb）。
// 安全：io/fs 语义拒绝含 ".." 的路径，embed 根外的文件不可达。
func ReadFile(rel string) ([]byte, error) {
	if rel == "" || strings.Contains(rel, "..") {
		return nil, os.ErrNotExist
	}
	return libraryFS.ReadFile("library/" + strings.TrimPrefix(rel, "/"))
}
