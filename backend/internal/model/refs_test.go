package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 现行生产数据（云绝对 URL / 本地 /uploads/ 相对路径 / 空值）在平台零值 origin 下
// 经 ResolveResourceRef 必须零行为变更。
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
		require.Equal(t, c.want, ResolveResourceRef(c.in, PlatformOrigin()), c.name)
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
		require.Equal(t, c.want, ResolveResourceRef(c.in, PlatformOrigin()), c.name)
	}
}

// 不安全引用解析失败时保持原值，不静默降级成猜测 URL。
func TestResolveResourceRefUnsafeReturnsRaw(t *testing.T) {
	require.Equal(t, "data:image/png;base64,xxx",
		ResolveResourceRef("data:image/png;base64,xxx", PlatformOrigin()))
}

// 六个模型的 ResolveRefs 各自解析其资源字段，零行为变更 / 未来键补前缀。
func TestModelResolveRefs(t *testing.T) {
	e := Employee{Avatar: "https://cdn.x/av.jpg"}
	e.ResolveRefs(PlatformOrigin())
	require.Equal(t, "https://cdn.x/av.jpg", e.Avatar)

	ci := CompanyInfo{Logo: "/uploads/logo.png"}
	ci.ResolveRefs(PlatformOrigin())
	require.Equal(t, "/uploads/logo.png", ci.Logo)

	cp := CompanyProfile{Cover: "t/5/cover.jpg", VideoURL: "https://cdn.x/v.mp4", VideoBackupURL: ""}
	cp.ResolveRefs(PlatformOrigin())
	require.Equal(t, "/uploads/t/5/cover.jpg", cp.Cover)
	require.Equal(t, "https://cdn.x/v.mp4", cp.VideoURL)
	require.Equal(t, "", cp.VideoBackupURL)

	c := AchievementCategory{Icon: "/uploads/icon.png"}
	c.ResolveRefs(PlatformOrigin())
	require.Equal(t, "/uploads/icon.png", c.Icon)

	p := ProjectCase{Cover: "p/c.png"}
	p.ResolveRefs(PlatformOrigin())
	require.Equal(t, "/uploads/p/c.png", p.Cover)

	s := BizService{Icon: "t/9/i.png"}
	s.ResolveRefs(PlatformOrigin())
	require.Equal(t, "/uploads/t/9/i.png", s.Icon)
}

// 第六十九轮：客户自有域名**只**作用于归属该企业的新形态对象键。
//
// 这是本轮最容易写错的一条：客户域名的回源规则只覆盖 /uploads/t/<企业id>/，
// 而租户字段里真实混着平台对象、历史裸键、外链。无差别拼域名会让它们在切量
// 当天全部 404，后台预览却因为走平台入口看起来完全正常。
func TestResolveResourceRefTenantOriginOnlyForOwnTenantKeys(t *testing.T) {
	o := Origin{Base: "https://img.cust.com", CompanyID: 5}
	cases := []struct {
		name, in, want string
	}{
		{"本租户新键", "t/5/photo.jpg", "https://img.cust.com/uploads/t/5/photo.jpg"},
		{"本租户键带 uploads 前缀", "/uploads/t/5/photo.jpg", "https://img.cust.com/uploads/t/5/photo.jpg"},
		{"平台对象", "p/library/cat-1.png", "/uploads/p/library/cat-1.png"},
		{"平台对象带 uploads 前缀", "/uploads/p/library/cat-1.png", "/uploads/p/library/cat-1.png"},
		{"下架占位图也是平台对象", "p/placeholder.png", "/uploads/p/placeholder.png"},
		{"历史无前缀裸键", "old.jpg", "/uploads/old.jpg"},
		{"历史相对路径", "/uploads/old.jpg", "/uploads/old.jpg"},
		{"别家租户键不指到这家域名", "t/9/photo.jpg", "/uploads/t/9/photo.jpg"},
		{"存量绝对 URL 原样", "https://cdn.x/photo.jpg", "https://cdn.x/photo.jpg"},
		{"合法外链原样", "https://other.example.com/v.mp4", "https://other.example.com/v.mp4"},
		{"空值", "", ""},
	}
	require.GreaterOrEqual(t, len(cases), 10, "量尺要有读数：负例集合不能只剩两三条")
	for _, c := range cases {
		require.Equal(t, c.want, ResolveResourceRef(c.in, o), c.name)
	}
}

// Base 为空（平台统一入口）时，CompanyID 单独不该产生任何效果。
// 否则「后台出口传零值」与「公开出口按租户」就分不开了。
func TestResolveResourceRefPlatformOriginIgnoresCompanyID(t *testing.T) {
	withCID := Origin{Base: "", CompanyID: 5}
	require.Equal(t, "/uploads/t/5/photo.jpg",
		ResolveResourceRef("t/5/photo.jpg", withCID))
	require.Equal(t, ResolveResourceRef("t/5/photo.jpg", PlatformOrigin()),
		ResolveResourceRef("t/5/photo.jpg", withCID))
}

// album 是 JSON 数组字符串展开出来的 []string，混合形态必须逐元素判定。
func TestResolveResourceRefsTenantOrigin(t *testing.T) {
	o := Origin{Base: "https://img.cust.com", CompanyID: 5}
	got := ResolveResourceRefs([]string{"t/5/a.jpg", "p/b.png", "old.jpg"}, o)
	require.Equal(t, []string{
		"https://img.cust.com/uploads/t/5/a.jpg",
		"/uploads/p/b.png",
		"/uploads/old.jpg",
	}, got)
	require.Nil(t, ResolveResourceRefs(nil, o), "nil 必须原样返回，别把 null 变成 []")
}

// 模型方法同样把 origin 透传到每个资源字段，不允许某一类字段偷偷绕过归属判定。
func TestModelResolveRefsTenantOrigin(t *testing.T) {
	o := Origin{Base: "https://img.cust.com", CompanyID: 5}
	e := Employee{Avatar: "t/5/av.jpg"}
	e.ResolveRefs(o)
	require.Equal(t, "https://img.cust.com/uploads/t/5/av.jpg", e.Avatar)

	cp := CompanyProfile{Cover: "p/c.jpg", VideoURL: "t/5/v.mp4"}
	cp.ResolveRefs(o)
	require.Equal(t, "/uploads/p/c.jpg", cp.Cover)
	require.Equal(t, "https://img.cust.com/uploads/t/5/v.mp4", cp.VideoURL)
}
