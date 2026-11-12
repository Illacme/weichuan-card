package mptemplate

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/model"
)

func TestAllReturnsCopy(t *testing.T) {
	a := All()
	require.NotEmpty(t, a)
	a[0].ID = "tampered"
	require.Equal(t, DefaultID, All()[0].ID, "All() 必须返回副本，调用方不能改坏包内清单")
}

func TestIDsAreUniqueAndDefaultFirst(t *testing.T) {
	all := All()
	require.Equal(t, DefaultID, all[0].ID, "缺省款必须排第一，后台按此顺序展示")
	seen := map[string]bool{}
	brands := 0
	for _, tpl := range all {
		require.NotEmpty(t, tpl.ID)
		require.False(t, seen[tpl.ID], "模板 id 重复：%s", tpl.ID)
		seen[tpl.ID] = true
		require.NotEmpty(t, tpl.Name)
		require.NotEmpty(t, tpl.Description)
		require.Contains(t, []string{SeriesStyle, SeriesBrand}, tpl.Series, "%s 的系列非法", tpl.ID)
		require.True(t, strings.HasPrefix(tpl.PreviewPrimary, "#"))
		require.True(t, strings.HasPrefix(tpl.PreviewSoft, "#"))
		if IsBrand(tpl.ID) {
			brands++
			require.True(t, tpl.Advanced, "品牌配色是增值能力，%s 必须受授权 gate 约束", tpl.ID)
			require.Equal(t, SeriesBrand, tpl.Series)
		} else {
			require.Equal(t, SeriesStyle, tpl.Series)
		}
	}
	require.Equal(t, 8, brands, "品牌配色预设色板固定 8 款；加款要同步改这个断言与 openapi enum")
	require.False(t, IsAdvanced(DefaultID), "缺省款必须免费，否则升级后客户会被降级")
	require.True(t, IsAdvanced("ink"))
	require.True(t, IsAdvanced("warm"))
	require.True(t, IsAdvanced("teal"))
}

// Series 与 brand- 前缀是两份独立声明：后台按前者分组，端上守门按后者分组。
// 任一侧漂移都不会报错（客户只是看到卡片出现在错误的分组里），只能靠断言钉住。
func TestSeriesMatchesBrandPrefix(t *testing.T) {
	for _, tpl := range All() {
		expected := SeriesStyle
		if IsBrand(tpl.ID) {
			expected = SeriesBrand
		}
		require.Equal(t, expected, tpl.Series, "%s 的系列与 id 前缀不一致", tpl.ID)
		require.Equal(t, expected, SeriesOf(tpl.ID))
	}
	require.Empty(t, SeriesOf("neon"), "未知 id 不该被归进任何系列")
	require.False(t, IsBrand("ink"))
	require.True(t, IsBrand("brand-ocean"))
}

func TestNormalize(t *testing.T) {
	require.Equal(t, DefaultID, Normalize(""))
	require.Equal(t, DefaultID, Normalize("  "))
	require.Equal(t, "ink", Normalize("ink"))
	require.Equal(t, "warm", Normalize(" warm "))
	require.Equal(t, "teal", Normalize("teal"))
	require.Equal(t, "brand-crimson", Normalize("brand-crimson"))
	require.Equal(t, "brand-magenta", Normalize(" brand-magenta "), "品牌配色款同样走白名单归一")
	require.Equal(t, "", Normalize("brand-neon"), "前缀像品牌款但不在白名单里，也必须拒绝")
	require.Equal(t, "", Normalize("neon"), "未知 id 必须返回空串由调用方拒绝，不能静默兜底")
	require.Equal(t, "", Normalize("INK"), "大小写不敏感会让端上 class 名对不上")
}

func TestIsAdvancedUnknownIsFalse(t *testing.T) {
	require.False(t, IsAdvanced("neon"))
	require.False(t, IsAdvanced(""))
}

// 三处默认值必须一致：包常量、GORM 模型 tag、MySQL 迁移。
// 任一侧漏改都会让「新租户 / 存量行」在某个环境里读到空 id 或别的款。
func TestDefaultIDMatchesModelAndMigration(t *testing.T) {
	f, ok := reflect.TypeOf(model.HomeLayout{}).FieldByName("TemplateID")
	require.True(t, ok, "HomeLayout 必须有 TemplateID 列")
	require.Contains(t, string(f.Tag.Get("gorm")), "default:"+DefaultID)

	sql, err := os.ReadFile("../../migrations/0041_miniprogram_theme.sql")
	require.NoError(t, err)
	require.Contains(t, string(sql), "DEFAULT '"+DefaultID+"'")
}

// 两轴拆分 / 反推：保证新旧两形态（单列 template_id ↔ (styleId, brandColorId)）
// 可无损互转，且不变量在单测层钉死。
func TestTwoAxisSplitAndDerive(t *testing.T) {
	// Split：品牌款 → (classic, brand-*)；风格款 → (id, nil)；缺省/空 → (classic, nil)
	s, b := Split("brand-ocean")
	require.Equal(t, DefaultID, s)
	require.NotNil(t, b)
	require.Equal(t, "brand-ocean", *b)

	s, b = Split("ink")
	require.Equal(t, "ink", s)
	require.Nil(t, b)

	s, b = Split("")
	require.Equal(t, DefaultID, s)
	require.Nil(t, b)

	s, b = Split("default")
	require.Equal(t, DefaultID, s)
	require.Nil(t, b)

	// 未知值拆分失败（调用方据此拒绝，不静默兜底）
	s, b = Split("neon")
	require.Equal(t, "", s)
	require.Nil(t, b)

	// DeriveTemplateID：有品牌色系取其 id，否则取风格 id，风格为空兜底 classic
	bc := "brand-crimson"
	require.Equal(t, "brand-crimson", DeriveTemplateID("classic", &bc))
	require.Equal(t, "ink", DeriveTemplateID("ink", nil))
	require.Equal(t, DefaultID, DeriveTemplateID("", nil))
}

// 全叉乘组合合法性 + 高级款判定（P5）。
func TestCrossProductComboAndAdvanced(t *testing.T) {
	bc := "brand-ocean"
	styles := []string{DefaultID, "ink", "warm", "teal"}
	brands := []string{
		"brand-crimson", "brand-coral", "brand-amber", "brand-forest",
		"brand-ocean", "brand-indigo", "brand-violet", "brand-magenta",
	}

	// P1–P4 只允许 default 挂色系；P5 起 4 风格 × (nil + 8 色系) = 36 组合全部合法。
	// 漏掉任意一格客户都只是「点不动」，不会报错，属于典型的静默失败。
	n := 0
	for _, s := range styles {
		require.True(t, IsValidCombo(s, nil), "%s 不带色系恒合法", s)
		n++
		for _, b := range brands {
			bid := b
			require.True(t, IsValidCombo(s, &bid), "%s + %s 必须合法（全叉乘）", s, b)
			n++
		}
	}
	require.Equal(t, 36, n, "4 风格 × 9 色档")

	// 放开叉乘不等于放弃白名单：未知风格 / 未知色系仍要拒绝。
	require.False(t, IsValidCombo("neon", nil))
	bad := "brand-neon"
	require.False(t, IsValidCombo(DefaultID, &bad))
	require.False(t, IsValidCombo("neon", &bad))

	require.True(t, IsKnownBrandColor("brand-magenta"))
	require.False(t, IsKnownBrandColor("neon"))
	require.Equal(t, "warm", NormalizeStyle("warm"))
	require.Equal(t, "", NormalizeStyle("neon"))

	// 高级款判定：两轴任一高级即高级。当前 8 款色系恰好全是高级款，
	// 所以「高级风格 + 色系」与旧的短路写法结果相同；改成 OR 是为了将来
	// 新增免费色系时不会漏判高级风格，不是当下可观测的行为变化。
	require.True(t, IsAdvancedCombo(DefaultID, &bc))
	require.True(t, IsAdvancedCombo("ink", nil))
	require.True(t, IsAdvancedCombo("ink", &bc))
	require.False(t, IsAdvancedCombo(DefaultID, nil))
}

// 风格分类按布局特色命名，不许再把颜色塞回风格名里（用户需求，P5）。
// 颜色由第 2 轴负责：风格名带颜色会让人误以为「换风格 = 换色」，
// 也会让「紧凑信息 + 绛红」这类组合在后台看起来自相矛盾。
func TestStyleNamesDescribeLayout(t *testing.T) {
	colorWords := []string{"蓝", "墨", "棕", "青", "红", "绿", "紫", "金", "灰", "橘", "色系"}
	layoutWords := []string{"列", "圆角", "间距", "描边", "留白", "排布", "密度", "网格"}
	for _, tpl := range All() {
		if tpl.Series != SeriesStyle {
			continue
		}
		for _, w := range colorWords {
			require.NotContains(t, tpl.Name, w, "%s 的风格名不该含颜色词 %q", tpl.ID, w)
			require.NotContains(t, tpl.Description, w, "%s 的风格说明不该含颜色词 %q", tpl.ID, w)
		}
		require.NotEmpty(t, tpl.Description)
		hit := false
		for _, w := range layoutWords {
			if strings.Contains(tpl.Description, w) {
				hit = true
				break
			}
		}
		require.True(t, hit, "%s 的说明必须描述布局特色，不能只谈颜色", tpl.ID)
	}
}
