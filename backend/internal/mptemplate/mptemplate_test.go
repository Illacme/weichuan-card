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
	for _, tpl := range all {
		require.NotEmpty(t, tpl.ID)
		require.False(t, seen[tpl.ID], "模板 id 重复：%s", tpl.ID)
		seen[tpl.ID] = true
		require.NotEmpty(t, tpl.Name)
		require.NotEmpty(t, tpl.Description)
		require.True(t, strings.HasPrefix(tpl.PreviewPrimary, "#"))
		require.True(t, strings.HasPrefix(tpl.PreviewSoft, "#"))
	}
	require.False(t, IsAdvanced(DefaultID), "缺省款必须免费，否则升级后客户会被降级")
	require.True(t, IsAdvanced("ink"))
	require.True(t, IsAdvanced("warm"))
}

func TestNormalize(t *testing.T) {
	require.Equal(t, DefaultID, Normalize(""))
	require.Equal(t, DefaultID, Normalize("  "))
	require.Equal(t, "ink", Normalize("ink"))
	require.Equal(t, "warm", Normalize(" warm "))
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
