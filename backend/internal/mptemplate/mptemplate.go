// Package mptemplate 定义小程序「整体风格模板」（配色 + 版式）的白名单。
//
// 这是模板 id 的唯一真值来源：后台可选清单、写接口校验、公开接口下发都读这里；
// 小程序端 utils/theme.ts 只负责把 id 翻译成 CSS 变量值，两端 id 集合由
// miniprogram/tests/theme.spec.ts 读本文件对账，防止一端加款另一端不认。
//
// 清单分两个轴（Series）：style 是「整体风格」= 布局族，按布局特色命名，决定首页
// 四个模块的列数 / 间距 / 圆角 / 封面高度；brand 是「品牌配色预设色板」= 纯颜色层，
// 只覆盖 11 个颜色 token，不改任何版式 token，用于对齐客户自有 VI 主色。
// P5 起两轴**全叉乘**：任意风格 × 任意色系（含 nil = 用该风格默认色）都合法，
// 4 × (1 + 8) = 36 种。色系不再隶属于某个风格：app.wxss 的 .wc-style-* 与
// .wc-brand-* 两层同时挂在根节点，颜色层后定义因而覆盖风格自带的默认色。
// **仍然走白名单，不是运行时自由色值注入**；后台按 series 分组渲染，不猜 id 前缀。
//
// 两轴 (style_id + brand_color_id) 是存储真值，任意风格 × 任意色系（含 nil）都合法，
// 见 IsValidCombo。0043 已删除 0041 引入的 template_id 兼容派生列，端上旧缓存
// （miniprogram/utils/theme.ts 的 splitTheme）仅做零迁移读取，不再回写单列 id。
//
// 为什么模板定义在代码里而不让后台上传 WXML/CSS：小程序代码是编译后上传审核的，
// 端上不能动态加载 WXSS；自由 CSS 还会把内部类名变成对客户的长期承诺，以后改版
// 就是破坏定制，并附带注入与「深底 + 深字」对比度失控的风险。加一款模板 = 改代码
// + 发版，这是刻意的成本，不是限制。
package mptemplate

import "strings"

const (
	// DefaultID 是升级零突变的缺省款：存量租户 style_id 为空时按它渲染，
	// 视觉与模板功能上线前逐字一致。
	DefaultID = "default"

	// FeatureAdvanced 是按租户授权的功能开关名（tenant_license.features 逗号分隔）。
	// 缺省放行：Features 为空 = 全部开通，因此未配授权的部署照样能选高级款。
	FeatureAdvanced = "mp_template_advanced"

	// SeriesStyle：整体风格（布局族），按布局特色命名；风格自带一套默认色，
	// 选中品牌色系时该默认色被颜色层覆盖，布局规则不变。
	SeriesStyle = "style"
	// SeriesBrand：品牌配色预设色板，8 款色系在 4 个风格上的取值逐字一致，
	// 可叠加在任意风格之上，只换颜色不改版式。
	SeriesBrand = "brand"
	// BrandPrefix 是品牌配色系列 id 的统一前缀。它与 Series 是两份独立声明，
	// 由 mptemplate_test.go 与 theme.spec.ts 双向对账，防止「分组显示一组、
	// 端上守门认另一组」。
	BrandPrefix = "brand-"
)

// Template 是后台模板页渲染一张模板卡所需的全部信息。
//
// PreviewPrimary / PreviewSoft 只用于后台色块预览；端上完整的 CSS 变量表在
// miniprogram/app.wxss（utils/theme.ts 只做 id → class 映射，不含色值）。
// 后台预览主色与端上 --wc-primary 由 miniprogram/tests/theme.spec.ts 逐款对账。
type Template struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Series         string `json:"series"`
	Advanced       bool   `json:"advanced"`
	PreviewPrimary string `json:"previewPrimary"`
	PreviewSoft    string `json:"previewSoft"`
}

// templates 顺序即后台展示顺序；default 必须排第一（免费款 / 缺省款）。
// 同系列内按此顺序展示，跨系列由后台按 Series 分组后再渲染。
// P5：风格名一律描述**布局特色**，不再用颜色命名（颜色由第 2 轴负责）；
// PreviewPrimary / PreviewSoft 仅代表「该风格不带色系时的默认色」，用于后台色块。
var templates = []Template{
	{
		ID:             DefaultID,
		Name:           "标准均衡",
		Description:    "标准列数与间距、中等圆角，四个模块均衡排布，适合大多数行业",
		Series:         SeriesStyle,
		Advanced:       false,
		PreviewPrimary: "#1a8cff",
		PreviewSoft:    "#e8f1fb",
	},
	{
		ID:             "ink",
		Name:           "紧凑信息",
		Description:    "近直角细描边、紧凑列宽与高信息密度，适合工程、制造、法律等专业形象",
		Series:         SeriesStyle,
		Advanced:       true,
		PreviewPrimary: "#3d4b5c",
		PreviewSoft:    "#e8edf2",
	},
	{
		ID:          "warm",
		Name:        "宽松亲和",
		Description: "大圆角胶囊按钮、单列精选大图与宽松留白，适合餐饮、文旅、家居等品牌",
		Series:      SeriesStyle,
		Advanced:    true,
		// 与 miniprogram/app.wxss 的 .wc-style-warm --wc-primary 保持一致；
		// theme.spec.ts 会逐款对账，端上调深后后台预览不能还显示旧的浅金色。
		PreviewPrimary: "#8f6320",
		PreviewSoft:    "#f7efdf",
	},
	{
		ID:             "teal",
		Name:           "疏排透气",
		Description:    "中圆角轻投影、疏排网格与大留白，适合健康、教育、环保等品牌",
		Series:         SeriesStyle,
		Advanced:       true,
		PreviewPrimary: "#0f766e",
		PreviewSoft:    "#dff2ef",
	},
	// 品牌配色预设色板：8 款主色按统一算法派生，在 4 个风格上取值逐字一致。
	// 12 套色板（4 风格默认色 + 8 品牌色）的 WCAG AA 实测最低 4.95，见 theme.spec.ts。
	{
		ID:             "brand-crimson",
		Name:           "绛红",
		Description:    "绛红主色，版式跟随所选整体风格，适合节庆、餐饮、国潮与零售品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#a01f2e",
		PreviewSoft:    "#f3e2e4",
	},
	{
		ID:             "brand-coral",
		Name:           "朱橘",
		Description:    "朱橘主色，版式跟随所选整体风格，适合食品、家居与生活方式品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#a33d16",
		PreviewSoft:    "#f3e6e1",
	},
	{
		ID:             "brand-amber",
		Name:           "琥珀",
		Description:    "琥珀金主色，版式跟随所选整体风格，适合酒旅、文创与高端制造品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#8a5a08",
		PreviewSoft:    "#f0eadf",
	},
	{
		ID:             "brand-forest",
		Name:           "森绿",
		Description:    "森绿主色，版式跟随所选整体风格，适合农林、健康与户外品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#12603a",
		PreviewSoft:    "#e0eae5",
	},
	{
		ID:             "brand-ocean",
		Name:           "海蓝",
		Description:    "深海蓝主色，版式跟随所选整体风格，适合物流、科技与专业服务品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#0b5585",
		PreviewSoft:    "#dfe9ef",
	},
	{
		ID:             "brand-indigo",
		Name:           "靛蓝",
		Description:    "靛青蓝主色，版式跟随所选整体风格，适合金融、咨询与教育机构",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#38429c",
		PreviewSoft:    "#e5e6f2",
	},
	{
		ID:             "brand-violet",
		Name:           "堇紫",
		Description:    "堇紫主色，版式跟随所选整体风格，适合美业、设计与文化创意品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#612a96",
		PreviewSoft:    "#eae3f1",
	},
	{
		ID:             "brand-magenta",
		Name:           "玫紫",
		Description:    "玫紫主色，版式跟随所选整体风格，适合时尚、母婴与女性消费品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#8c2358",
		PreviewSoft:    "#f0e2e9",
	},
}

// All 返回清单副本：调用方拿到的是自己的切片，改它不会污染包内状态
// （与 internal/cattemplate.Default() 同一约定）。
func All() []Template {
	return append([]Template(nil), templates...)
}

// Normalize 归一模板 id：空串按缺省款，未知值返回空串由调用方拒绝。
//
// 不做「未知就静默兜底成 default」：后台是唯一写入方，把一个不认识的 id
// 存进库只会让端上既不显示也不报错。
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultID
	}
	for _, t := range templates {
		if t.ID == v {
			return v
		}
	}
	return ""
}

// NormalizeStyle 校验整体风格 id（白名单 SeriesStyle），未知返回空串。
// 与 Normalize 同理：不做静默兜底，未知值由调用方拒绝。
func NormalizeStyle(id string) string {
	id = strings.TrimSpace(id)
	for _, t := range templates {
		if t.ID == id && t.Series == SeriesStyle {
			return id
		}
	}
	return ""
}

// IsKnownBrandColor 判断某 id 是否为已知品牌色系（SeriesBrand）。
func IsKnownBrandColor(id string) bool {
	id = strings.TrimSpace(id)
	for _, t := range templates {
		if t.ID == id && t.Series == SeriesBrand {
			return true
		}
	}
	return false
}

// IsValidCombo 校验 (styleID, brandColorID) 是否为合法组合。
//
// P1–P4 是树形模型（色系隶属于某个风格，只有 default 能挂 brand-*）；
// **P5 起改为全叉乘**：布局与颜色在 app.wxss 里已是两个独立的 class 层，
// 任意风格叠加任意色系都能正确渲染，再按风格限制色系只会凭空砍掉 3/4 的选项。
// 这里仍要挡住两类脏值：未知风格 id、未知色系 id；brandColorID 为 nil 表示
// 「用该风格默认色」，恒合法。
func IsValidCombo(styleID string, brandColorID *string) bool {
	if NormalizeStyle(styleID) == "" {
		return false
	}
	if brandColorID == nil {
		return true
	}
	return IsKnownBrandColor(*brandColorID)
}

// IsAdvancedCombo 判断 (styleID, brandColorID) 组合是否属高级款：
// 风格本身高级 **或** 所选品牌色系高级，任一命中即需授权。
// P5 之前这里在 brandColorID 非空时直接短路返回色系的 Advanced，
// 叉乘下会漏掉「高级风格 + 免费色系」这一类判定，必须两轴都查。
func IsAdvancedCombo(styleID string, brandColorID *string) bool {
	for _, t := range templates {
		if t.ID == styleID && t.Series == SeriesStyle && t.Advanced {
			return true
		}
	}
	if brandColorID != nil {
		for _, t := range templates {
			if t.ID == *brandColorID && t.Series == SeriesBrand && t.Advanced {
				return true
			}
		}
	}
	return false
}

// IsBrand 判断某款是否属于「品牌配色预设色板」。
// 端上守门按 brand- 前缀分组，后台按下发的 Series 分组；两者必须一致，
// 由 TestSeriesMatchesBrandPrefix 钉住，避免同一款在两处被归进不同组。
func IsBrand(id string) bool {
	return strings.HasPrefix(id, BrandPrefix)
}

// SeriesOf 返回某款所属系列；未知 id 返回空串。
func SeriesOf(id string) string {
	for _, t := range templates {
		if t.ID == id {
			return t.Series
		}
	}
	return ""
}
