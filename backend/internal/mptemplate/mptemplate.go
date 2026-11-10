// Package mptemplate 定义小程序「整体风格模板」（配色 + 版式）的白名单。
//
// 这是模板 id 的唯一真值来源：后台可选清单、写接口校验、公开接口下发都读这里；
// 小程序端 utils/theme.ts 只负责把 id 翻译成 CSS 变量值，两端 id 集合由
// miniprogram/tests/theme.spec.ts 读本文件对账，防止一端加款另一端不认。
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
)

// Template 是后台「整体风格」页渲染一张模板卡所需的全部信息。
//
// PreviewPrimary / PreviewSoft 只用于后台色块预览；端上完整的 CSS 变量表在
// miniprogram/app.wxss（utils/theme.ts 只做 id → class 映射，不含色值）。
// 后台预览主色与端上 --wc-primary 由 miniprogram/tests/theme.spec.ts 逐款对账。
type Template struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Advanced       bool   `json:"advanced"`
	PreviewPrimary string `json:"previewPrimary"`
	PreviewSoft    string `json:"previewSoft"`
}

// templates 顺序即后台展示顺序；default 必须排第一（免费款 / 缺省款）。
var templates = []Template{
	{
		ID:             DefaultID,
		Name:           "经典蓝",
		Description:    "默认配色与圆角，清爽稳重，适合大多数行业",
		Advanced:       false,
		PreviewPrimary: "#1a8cff",
		PreviewSoft:    "#e8f1fb",
	},
	{
		ID:             "ink",
		Name:           "沉稳墨",
		Description:    "低饱和深灰蓝 + 近直角细描边，适合工程、制造、法律等专业形象",
		Advanced:       true,
		PreviewPrimary: "#3d4b5c",
		PreviewSoft:    "#e8edf2",
	},
	{
		ID:             "warm",
		Name:           "暖金棕",
		Description:    "金棕主色 + 大圆角胶囊按钮，适合餐饮、文旅、家居等品牌",
		Advanced:       true,
		// 与 miniprogram/app.wxss 的 .wc-theme-warm --wc-primary 保持一致；
		// theme.spec.ts 会逐款对账，端上调深后后台预览不能还显示旧的浅金色。
		PreviewPrimary: "#8f6320",
		PreviewSoft:    "#f7efdf",
	},
	{
		ID:             "teal",
		Name:           "松石青",
		Description:    "清透青绿 + 中圆角轻投影，适合健康、教育、环保等品牌",
		Advanced:       true,
		PreviewPrimary: "#0f766e",
		PreviewSoft:    "#dff2ef",
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

// IsAdvanced 判断某款是否需要 FeatureAdvanced 授权。
func IsAdvanced(id string) bool {
	for _, t := range templates {
		if t.ID == id && t.Advanced {
			return true
		}
	}
	return false
}
