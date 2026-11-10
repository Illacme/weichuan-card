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
