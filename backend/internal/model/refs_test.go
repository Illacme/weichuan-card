package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 现行生产数据（云绝对 URL / 本地 /uploads/ 相对路径 / 空值）经 ResolveResourceRef 必须零行为变更。
func TestResolveResourceRefCurrentDataNoOp(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"空", "", ""},
		{"云绝对URL", "https://cdn.example.com/abcd.jpg", "https://cdn.example.com/abcd.jpg"},
		{"本地相对", "/uploads/abcd.jpg", "/uploads/abcd.jpg"},
		{"本地相对带缓存戳", "/uploads/abcd.jpg?v=2", "/uploads/abcd.jpg?v=2"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, ResolveResourceRef(c.in), c.name)
	}
}

// 将来写入侧收敛成裸键 / 前缀键后，ResolveResourceRef 必须自动补回 /uploads/ 前缀。
func TestResolveResourceRefFutureKeyForms(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"裸键", "abcd.jpg", "/uploads/abcd.jpg"},
		{"平台键", "p/library/cat-1.png", "/uploads/p/library/cat-1.png"},
		{"租户键", "t/5/abcd.jpg", "/uploads/t/5/abcd.jpg"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, ResolveResourceRef(c.in), c.name)
	}
}

// 不安全引用解析失败时保持原值，不静默降级成猜测 URL。
func TestResolveResourceRefUnsafeReturnsRaw(t *testing.T) {
	require.Equal(t, "data:image/png;base64,xxx", ResolveResourceRef("data:image/png;base64,xxx"))
}

// 六个模型的 ResolveRefs 各自解析其资源字段，零行为变更 / 未来键补前缀。
func TestModelResolveRefs(t *testing.T) {
	e := Employee{Avatar: "https://cdn.x/av.jpg"}
	e.ResolveRefs()
	require.Equal(t, "https://cdn.x/av.jpg", e.Avatar)

	ci := CompanyInfo{Logo: "/uploads/logo.png"}
	ci.ResolveRefs()
	require.Equal(t, "/uploads/logo.png", ci.Logo)

	cp := CompanyProfile{Cover: "t/5/cover.jpg", VideoURL: "https://cdn.x/v.mp4", VideoBackupURL: ""}
	cp.ResolveRefs()
	require.Equal(t, "/uploads/t/5/cover.jpg", cp.Cover)
	require.Equal(t, "https://cdn.x/v.mp4", cp.VideoURL)
	require.Equal(t, "", cp.VideoBackupURL)

	c := AchievementCategory{Icon: "/uploads/icon.png"}
	c.ResolveRefs()
	require.Equal(t, "/uploads/icon.png", c.Icon)

	p := ProjectCase{Cover: "p/c.png"}
	p.ResolveRefs()
	require.Equal(t, "/uploads/p/c.png", p.Cover)

	s := BizService{Icon: "t/9/i.png"}
	s.ResolveRefs()
	require.Equal(t, "/uploads/t/9/i.png", s.Icon)
}
