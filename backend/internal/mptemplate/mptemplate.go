// Package mptemplate 定义小程序「整体风格模板」（配色 + 版式）的白名单。
//
// 这是模板 id 的唯一真值来源：后台可选清单、写接口校验、公开接口下发都读这里；
// 小程序端 utils/theme.ts 只负责把 id 翻译成 CSS 变量值，两端 id 集合由
// miniprogram/tests/theme.spec.ts 读本文件对账，防止一端加款另一端不认。
//
// 清单分两个系列（Series）：style 是「整体风格」（配色 + 版式一起换），
// brand 是「品牌配色预设色板」（只换配色，7 个版式 token 逐字沿用 default）。
// 后者是客户对齐自有 VI 主色的增值选项：**仍然走白名单，不是运行时自由色值注入**。
// 后台按 series 分组渲染，不硬编码 id 前缀；两端分组一致性由测试对账。
//
// 为什么模板定义在代码里而不让后台上传 WXML/CSS：小程序代码是编译后上传审核的，
// 端上不能动态加载 WXSS；自由 CSS 还会把内部类名变成对客户的长期承诺，以后改版
// 就是破坏定制，并附带注入与「深底 + 深字」对比度失控的风险。加一款模板 = 改代码
// + 发版，这是刻意的成本，不是限制。
package mptemplate

import "strings"

const (
	// DefaultID 是升级零突变的缺省款：存量租户 template_id 为空时按它渲染，
	// 视觉与模板功能上线前逐字一致。
	DefaultID = "default"

	// FeatureAdvanced 是按租户授权的功能开关名（tenant_license.features 逗号分隔）。
	// 缺省放行：Features 为空 = 全部开通，因此未配授权的部署照样能选高级款。
	FeatureAdvanced = "mp_template_advanced"

	// SeriesStyle：整体风格，配色与版式一起换（default / ink / warm / teal）。
	SeriesStyle = "style"
	// SeriesBrand：品牌配色预设色板，只换配色，版式沿用 default。
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
var templates = []Template{
	{
		ID:             DefaultID,
		Name:           "经典蓝",
		Description:    "默认配色与圆角，清爽稳重，适合大多数行业",
		Series:         SeriesStyle,
		Advanced:       false,
		PreviewPrimary: "#1a8cff",
		PreviewSoft:    "#e8f1fb",
	},
	{
		ID:             "ink",
		Name:           "沉稳墨",
		Description:    "低饱和深灰蓝 + 近直角细描边，适合工程、制造、法律等专业形象",
		Series:         SeriesStyle,
		Advanced:       true,
		PreviewPrimary: "#3d4b5c",
		PreviewSoft:    "#e8edf2",
	},
	{
		ID:          "warm",
		Name:        "暖金棕",
		Description: "金棕主色 + 大圆角胶囊按钮，适合餐饮、文旅、家居等品牌",
		Series:      SeriesStyle,
		Advanced:    true,
		// 与 miniprogram/app.wxss 的 .wc-theme-warm --wc-primary 保持一致；
		// theme.spec.ts 会逐款对账，端上调深后后台预览不能还显示旧的浅金色。
		PreviewPrimary: "#8f6320",
		PreviewSoft:    "#f7efdf",
	},
	{
		ID:             "teal",
		Name:           "松石青",
		Description:    "清透青绿 + 中圆角轻投影，适合健康、教育、环保等品牌",
		Series:         SeriesStyle,
		Advanced:       true,
		PreviewPrimary: "#0f766e",
		PreviewSoft:    "#dff2ef",
	},
	// 品牌配色预设色板：只换主色与配套浅色档，7 个版式 token 逐字沿用 default。
	// 8 款主色都按 WCAG AA 实测过 12 组真实搭配（最低 4.95，见 theme.spec.ts）。
	{
		ID:             "brand-crimson",
		Name:           "绛红",
		Description:    "绛红主色 + 经典圆角版式，适合节庆、餐饮、国潮与零售品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#a01f2e",
		PreviewSoft:    "#f3e2e4",
	},
	{
		ID:             "brand-coral",
		Name:           "朱橘",
		Description:    "朱橘主色 + 经典圆角版式，适合食品、家居与生活方式品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#a33d16",
		PreviewSoft:    "#f3e6e1",
	},
	{
		ID:             "brand-amber",
		Name:           "琥珀",
		Description:    "琥珀金主色 + 经典圆角版式，适合酒旅、文创与高端制造品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#8a5a08",
		PreviewSoft:    "#f0eadf",
	},
	{
		ID:             "brand-forest",
		Name:           "森绿",
		Description:    "森绿主色 + 经典圆角版式，适合农林、健康与户外品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#12603a",
		PreviewSoft:    "#e0eae5",
	},
	{
		ID:             "brand-ocean",
		Name:           "海蓝",
		Description:    "深海蓝主色 + 经典圆角版式，适合物流、科技与专业服务品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#0b5585",
		PreviewSoft:    "#dfe9ef",
	},
	{
		ID:             "brand-indigo",
		Name:           "靛蓝",
		Description:    "靛青蓝主色 + 经典圆角版式，适合金融、咨询与教育机构",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#38429c",
		PreviewSoft:    "#e5e6f2",
	},
	{
		ID:             "brand-violet",
		Name:           "堇紫",
		Description:    "堇紫主色 + 经典圆角版式，适合美业、设计与文化创意品牌",
		Series:         SeriesBrand,
		Advanced:       true,
		PreviewPrimary: "#612a96",
		PreviewSoft:    "#eae3f1",
	},
	{
		ID:             "brand-magenta",
		Name:           "玫紫",
		Description:    "玫紫主色 + 经典圆角版式，适合时尚、母婴与女性消费品牌",
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

// IsAdvanced 判断某款（单列 template_id 形态）是否需要 FeatureAdvanced 授权。
// 两轴化后新代码应改用 IsAdvancedCombo(styleID, brandColorID)；本函数保留以
// 兼容旧调用与既有测试，内部按 Split 拆两轴再判定。
func IsAdvanced(id string) bool {
	s, b := Split(id)
	return IsAdvancedCombo(s, b)
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

// StyleOfBrand 返回某品牌色系所属的整体风格；非品牌色系返回空串。
// P1 全部 brand-* 归 classic（用户确认的迁移映射，存量视觉零突变）；
// 其余风格各自的调校色系是后续阶段的增强，不在 P1 范围。
func StyleOfBrand(id string) string {
	if IsKnownBrandColor(id) {
		return DefaultID
	}
	return ""
}

// BelongsToStyle 校验 brandColorID（若非 nil）是否属于 styleID 这棵子树。
// 这是两轴「树形」模型的核心不变式：色系不能跨风格乱配。
func BelongsToStyle(styleID string, brandColorID *string) bool {
	if brandColorID == nil {
		return true
	}
	return StyleOfBrand(*brandColorID) == styleID
}

// Split 把旧的单列 template_id 拆成 (styleID, brandColorID)：
// 品牌款 brand-* → (classic, brand-*)；风格款 → (id, nil)；缺省/空 → (classic, nil)。
// 反向由 DeriveTemplateID 完成；两者保证新旧两形态可无损互转。
func Split(id string) (string, *string) {
	id = strings.TrimSpace(id)
	if id == "" || id == DefaultID {
		return DefaultID, nil
	}
	if IsBrand(id) {
		b := id
		return DefaultID, &b
	}
	if NormalizeStyle(id) != "" {
		return id, nil
	}
	return "", nil
}

// DeriveTemplateID 由两轴值反推兼容用的单列 template_id：
// 选了品牌色系 → 该色系 id；否则 → 风格 id；风格为空兜底 classic。
func DeriveTemplateID(styleID string, brandColorID *string) string {
	if brandColorID != nil {
		return *brandColorID
	}
	if styleID == "" {
		return DefaultID
	}
	return styleID
}

// IsAdvancedCombo 判断 (styleID, brandColorID) 组合是否属高级款：
// 风格本身高级，或所选品牌色系高级。两轴任一高级即高级。
func IsAdvancedCombo(styleID string, brandColorID *string) bool {
	if brandColorID != nil {
		for _, t := range templates {
			if t.ID == *brandColorID && t.Series == SeriesBrand {
				return t.Advanced
			}
		}
		return false
	}
	for _, t := range templates {
		if t.ID == styleID && t.Series == SeriesStyle {
			return t.Advanced
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
