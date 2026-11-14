package storage

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ValidatePublicOrigin 是「客户自有访问域名」写入校验与 ResolveRef 出参 origin
// 校验的**同一个函数**。这里的用例既测规则本身，也测「两者确实共用一份实现」。

func TestValidatePublicOriginAcceptsPureOrigins(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"https://img.cust.com", "https://img.cust.com"},
		{"  https://img.cust.com  ", "https://img.cust.com"},
		{"https://img.cust.com/", "https://img.cust.com"},
		{"https://img.cust.com///", "https://img.cust.com"},
		{"http://127.0.0.1:9000", "http://127.0.0.1:9000"},
		{"https://IMG.Cust.COM:8443/", "https://IMG.Cust.COM:8443"},
	}
	for _, c := range cases {
		got, err := ValidatePublicOrigin(c.in)
		require.NoError(t, err, "输入 %q 应被接受", c.in)
		require.Equal(t, c.want, got, "输入 %q", c.in)
	}
}

func TestValidatePublicOriginRejectsAnythingButPureOrigin(t *testing.T) {
	cases := []struct {
		in, why string
	}{
		{"img.cust.com", "没有 scheme"},
		{"//img.cust.com", "协议相对 URL"},
		{"ftp://img.cust.com", "非 http(s)"},
		{"javascript:alert(1)", "伪协议"},
		{"https://", "缺 host"},
		{"https://img.cust.com/prefix", "带路径：会拼出 /prefix/uploads/..."},
		{"https://img.cust.com/uploads", "带路径：与统一入口重复"},
		{"https://img.cust.com?a=1", "带 query"},
		{"https://img.cust.com#frag", "带 fragment"},
		{"https://user:pw@img.cust.com", "带 userinfo"},
		{`https://img.cust.com"`, "带引号：破坏 nginx 语法"},
		{"https://img.cust.com/x y", "带空格"},
		{`https://img.cust.com\x`, "带反斜杠"},
	}
	require.GreaterOrEqual(t, len(cases), 10, "负例集合本身要有读数，别让守门空跑")
	for _, c := range cases {
		got, err := ValidatePublicOrigin(c.in)
		require.Error(t, err, "输入 %q 应被拒绝（%s）", c.in, c.why)
		require.Empty(t, got, "被拒时不得返回半个可用值：%q", c.in)
	}
}

// TestResolveRefUsesTheSameOriginValidator 是「单点」的承诺本身：
// 只测 ValidatePublicOrigin 的规则是不够的——若 ResolveRef 另写一份校验，
// 规则再全也会在出参侧漂移。这里断言出参确实按归一化后的 origin 拼 URL，
// 且被 ValidatePublicOrigin 拒掉的值在 ResolveRef 里同样报错。
func TestResolveRefUsesTheSameOriginValidator(t *testing.T) {
	got, err := ResolveRef("t/5/photo.jpg", " https://img.cust.com/ ")
	require.NoError(t, err)
	require.Equal(t, "https://img.cust.com/uploads/t/5/photo.jpg", got)

	for _, bad := range []string{
		"https://img.cust.com/prefix", "https://user:pw@img.cust.com", "ftp://img.cust.com",
	} {
		_, err := ResolveRef("t/5/photo.jpg", bad)
		require.Error(t, err, "origin=%q 在写入侧会被拒，出参侧也必须拒", bad)
	}
}

// TestRefGoDoesNotCarryASecondOriginValidator 是源码级守门：
// 把 cleanOrigin 提成导出函数后，ref.go 不许再留一份私有实现。
// 「抓取型守门」必须自带「至少抓到 N 个」兜底，否则换种写法就一条也抓不到，
// 于是「全部通过」——先剥整行注释，避免把解释性文字扫成代码。
func TestRefGoDoesNotCarryASecondOriginValidator(t *testing.T) {
	b, err := os.ReadFile("ref.go")
	require.NoError(t, err)
	var code strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}
	src := code.String()
	require.NotContains(t, src, "func cleanOrigin(", "ref.go 不得再自带一份 origin 校验")
	require.Contains(t, src, "ValidatePublicOrigin(origin)",
		"ResolveRef 必须调用单点校验；改回内联实现时本用例必须红")
	require.Equal(t, 1, strings.Count(src, "ValidatePublicOrigin(origin)"),
		" ResolveRef 里的调用点应当恰好一处")
}
