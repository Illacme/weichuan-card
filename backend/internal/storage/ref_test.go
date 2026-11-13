package storage

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 本文件钉住 P0a 纯函数的行为契约。设计稿 §4 的 P0 验收判据里有两条
// （TestResolveKeepsLegacyAbsoluteURL / TestLegacyUploadsRelativeURL），
// 这里用实际落地的名字覆盖，并补上归属、幂等、路径穿越、端上契约的断言。

func TestObjectKeyForPlatformAndTenant(t *testing.T) {
	pk, err := ObjectKeyFor(ScopePlatform, 0, "library/cat-1.png")
	require.NoError(t, err)
	require.Equal(t, "p/library/cat-1.png", pk)

	tk, err := ObjectKeyFor(ScopeTenant, 5, "abcd1234.jpg")
	require.NoError(t, err)
	require.Equal(t, "t/5/abcd1234.jpg", tk)

	// 缩略图仍在同一租户目录下；_thumb 派生规则不能把对象甩到平台前缀下。
	tk2, err := ObjectKeyFor(ScopeTenant, 5, "abcd1234_thumb.jpg")
	require.NoError(t, err)
	require.Equal(t, "t/5/abcd1234_thumb.jpg", tk2)
}

func TestObjectKeyForRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		scope Scope
		cid   int64
		rel   string
	}{
		{"空 rel", ScopeTenant, 5, ""},
		{"空白 rel", ScopeTenant, 5, "   "},
		{"绝对路径", ScopeTenant, 5, "/etc/passwd"},
		{"路径穿越", ScopeTenant, 5, "../secret.jpg"},
		{"中段路径穿越", ScopeTenant, 5, "library/../../secret.jpg"},
		{"空段", ScopeTenant, 5, "library//x.png"},
		{"点段", ScopeTenant, 5, "library/./x.png"},
		{"反斜杠", ScopeTenant, 5, `library\x.png`},
		{"查询串", ScopeTenant, 5, "x.jpg?v=1"},
		{"片段", ScopeTenant, 5, "x.jpg#frag"},
		{"绝对 URL", ScopeTenant, 5, "https://cdn.example/x.jpg"},
		{"协议相对 URL", ScopeTenant, 5, "//evil.example/x.jpg"},
		{"双重平台前缀", ScopeTenant, 5, "p/x.jpg"},
		{"双重租户前缀", ScopeTenant, 5, "t/5/x.jpg"},
		{"租户 cid=0", ScopeTenant, 0, "x.jpg"},
		{"租户 cid 负数", ScopeTenant, -1, "x.jpg"},
		{"平台 cid 非 0", ScopePlatform, 5, "x.jpg"},
		{"未知 scope", Scope("other"), 5, "x.jpg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ObjectKeyFor(c.scope, c.cid, c.rel)
			require.Error(t, err, "非法输入必须显式报错，不能静默产出对象键")
		})
	}
}

func TestParseRefRoundTrip(t *testing.T) {
	for _, raw := range []string{
		"p/library/cat-1.png",
		"p/wxacode/42.jpg",
		"t/5/abcd1234.jpg",
		"t/12/library/x_thumb.png",
	} {
		ref, err := ParseRef(raw)
		require.NoError(t, err, raw)
		require.Equal(t, raw, ref.String(), "解析后必须逐字还原，不能改变对象键")
		require.True(t, IsNormalizedRef(ref.String()))

		// 从键反推归属再重建，结果必须一致（下架 / 搬迁要靠这个选实例）。
		rebuilt, err := ObjectKeyFor(ref.Scope, ref.CompanyID, relOf(ref))
		require.NoError(t, err)
		require.Equal(t, raw, rebuilt)
	}
	ref, err := ParseRef("t/5/abcd1234.jpg")
	require.NoError(t, err)
	require.Equal(t, ScopeTenant, ref.Scope)
	require.Equal(t, int64(5), ref.CompanyID)

	pref, err := ParseRef("p/library/cat-1.png")
	require.NoError(t, err)
	require.Equal(t, ScopePlatform, pref.Scope)
	require.Equal(t, int64(0), pref.CompanyID)
}

// relOf 只给测试用：剥掉归属前缀，还原不含前缀的相对对象键。
func relOf(ref Ref) string {
	if ref.Scope == ScopePlatform {
		return strings.TrimPrefix(ref.ObjectKey, PlatformKeyPrefix)
	}
	return strings.TrimPrefix(ref.ObjectKey,
		TenantKeyPrefix+strconv.FormatInt(ref.CompanyID, 10)+"/")
}

func TestParseRefRejectsLegacyForms(t *testing.T) {
	for _, raw := range []string{
		"https://cdn.example.com/abcd1234.jpg",
		"/uploads/abcd1234.jpg",
		"abcd1234.jpg",
		"",
	} {
		_, err := ParseRef(raw)
		if raw == "" {
			require.ErrorIs(t, err, ErrEmptyRef)
			continue
		}
		require.ErrorIs(t, err, ErrLegacyRef, "%q 属于历史形态，不能当新键解析", raw)
		require.False(t, IsNormalizedRef(raw))
	}
}

func TestParseRefRejectsMalformedTenantKey(t *testing.T) {
	for _, raw := range []string{
		"t/",
		"t/0/x.jpg",
		"t/-1/x.jpg",
		"t/abc/x.jpg",
		"t/5",
		"t/5/",
		"t/5/../x.jpg",
		"p/",
	} {
		_, err := ParseRef(raw)
		require.Error(t, err, "%q 是坏键，必须报错", raw)
	}
}

func TestNormalizeRefStripsAbsoluteURL(t *testing.T) {
	// 云驱动返回 <UploadBaseURL>/<key>，baseURL 必须能把前缀剥干净。
	ref, err := NormalizeRef("https://cdn.example.com/abcd1234.jpg",
		"https://cdn.example.com", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, "t/5/abcd1234.jpg", ref.String())

	// 带 query 的签名 / 缓存戳 URL，剥前缀后还要把 ? 后面切掉。
	ref, err = NormalizeRef("https://cdn.example.com/prefix/x.jpg?X-Amz-Signature=abc",
		"https://cdn.example.com/prefix", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, "t/5/x.jpg", ref.String())

	// 基址对不上但路径里明确有 /uploads/ 段时，按该段定位而不是猜键。
	ref, err = NormalizeRef("https://other.example.com/uploads/library/cat-1.png",
		"https://cdn.example.com", ScopePlatform, 0)
	require.NoError(t, err)
	require.Equal(t, "p/library/cat-1.png", ref.String())

	// 基址对不上且没有 /uploads/ 段 → 宁可报错也不能猜一个键。
	_, err = NormalizeRef("https://other.example.com/x.jpg",
		"https://cdn.example.com", ScopeTenant, 5)
	require.ErrorIs(t, err, ErrInvalidRef)
}

func TestNormalizeRefStripsUploadsRelativeFirst(t *testing.T) {
	// mediachk.go:153-158 记过的事故：先按基址 TrimPrefix 会把
	// "/uploads/x.jpg" 推成 "uploads/x.jpg"（多一层目录），下架覆盖写错位置。
	ref, err := NormalizeRef("/uploads/x.jpg", "https://cdn.example.com", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, "t/5/x.jpg", ref.String())

	// 基址为空（本地驱动）时同样不能多出一层 uploads/。
	ref, err = NormalizeRef("/uploads/x.jpg", "", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, "t/5/x.jpg", ref.String())

	// 历史裸键（media_check.object_key 那一类）也要能套上归属前缀。
	ref, err = NormalizeRef("library/cat-1.png", "", ScopePlatform, 0)
	require.NoError(t, err)
	require.Equal(t, "p/library/cat-1.png", ref.String())
}

func TestNormalizeRefIsIdempotent(t *testing.T) {
	first, err := NormalizeRef("/uploads/abcd1234.jpg", "", ScopeTenant, 5)
	require.NoError(t, err)
	second, err := NormalizeRef(first.String(), "", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, first, second, "重复归一化绝不能再加一层前缀")
	require.Equal(t, "t/5/abcd1234.jpg", second.String())
}

func TestNormalizeRefRejectsCrossTenantRebind(t *testing.T) {
	_, err := NormalizeRef("t/5/abcd1234.jpg", "", ScopeTenant, 9)
	require.ErrorIs(t, err, ErrScopeMismatch,
		"A 租户对象用 B 租户身份再存一次必须报错，不能静默改写成 t/9/...")

	_, err = NormalizeRef("p/library/cat-1.png", "", ScopeTenant, 9)
	require.ErrorIs(t, err, ErrScopeMismatch, "平台对象不能被租户身份带跑")

	_, err = NormalizeRef("", "", ScopeTenant, 5)
	require.ErrorIs(t, err, ErrEmptyRef)
}

func TestResolveRefKeepsLegacyAbsoluteURL(t *testing.T) {
	// 设计稿 P0 的第一条验收：存量绝对 URL 必须原样返回。
	// 哪怕传了平台入口 origin 也不能改写，否则老数据立刻裂图。
	const old = "https://old-cdn.example.com/abcd1234.jpg"
	for _, origin := range []string{"", "https://weichuan.ziyuanxingqiu.cn"} {
		got, err := ResolveRef(old, origin)
		require.NoError(t, err)
		require.Equal(t, old, got)
	}
	// 带 query 的存量 URL 同样原样返回（缓存戳不能被悄悄剥掉）。
	const oldQ = "https://old-cdn.example.com/x.jpg?v=3"
	got, err := ResolveRef(oldQ, "https://weichuan.ziyuanxingqiu.cn")
	require.NoError(t, err)
	require.Equal(t, oldQ, got)
}

func TestResolveRefLegacyUploadsRelativeURL(t *testing.T) {
	got, err := ResolveRef("/uploads/abcd1234.jpg", "")
	require.NoError(t, err)
	require.Equal(t, "/uploads/abcd1234.jpg", got, "origin 为空时保持现有相对路径形态")

	got, err = ResolveRef("/uploads/abcd1234.jpg", "https://weichuan.ziyuanxingqiu.cn/")
	require.NoError(t, err)
	require.Equal(t, "https://weichuan.ziyuanxingqiu.cn/uploads/abcd1234.jpg", got,
		"尾斜杠必须归一，不能拼出 //uploads/")

	// 无前缀裸键（存量 object_key）按 /uploads/ 拼，与 LocalUploader 一致。
	got, err = ResolveRef("library/cat-1.png", "https://weichuan.ziyuanxingqiu.cn")
	require.NoError(t, err)
	require.Equal(t, "https://weichuan.ziyuanxingqiu.cn/uploads/library/cat-1.png", got)
}

func TestResolveRefNormalizedKeys(t *testing.T) {
	for _, c := range []struct {
		key, want string
	}{
		{"p/library/cat-1.png", "https://weichuan.ziyuanxingqiu.cn/uploads/p/library/cat-1.png"},
		{"t/5/abcd1234.jpg", "https://weichuan.ziyuanxingqiu.cn/uploads/t/5/abcd1234.jpg"},
	} {
		got, err := ResolveRef(c.key, "https://weichuan.ziyuanxingqiu.cn")
		require.NoError(t, err, c.key)
		require.Equal(t, c.want, got)

		got, err = ResolveRef(c.key, "")
		require.NoError(t, err, c.key)
		require.Equal(t, "/uploads/"+c.key, got, "origin 为空时返回相对路径供端上拼基址")
	}
}

func TestResolveRefEmptyIsNoop(t *testing.T) {
	// avatar / logo / videoUrl 等业务字段经常为空，出参侧不能因空值整条报错。
	for _, origin := range []string{"", "https://weichuan.ziyuanxingqiu.cn"} {
		got, err := ResolveRef("", origin)
		require.NoError(t, err)
		require.Equal(t, "", got)
		got, err = ResolveRef("   ", origin)
		require.NoError(t, err)
		require.Equal(t, "", got)
	}
}

func TestResolveRefRejectsUnsafeInput(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"data:image/png;base64,AAAA",
		"//evil.example/x.jpg",
		"/uploads/../secret.jpg",
		"t/0/x.jpg",
		"t/abc/x.jpg",
		"/other/x.jpg",
		"/uploads/x.jpg?v=1",
	} {
		_, err := ResolveRef(raw, "https://weichuan.ziyuanxingqiu.cn")
		require.Error(t, err, "%q 必须显式报错，不能静默降级成猜测 URL", raw)
	}
}

func TestResolveRefRejectsOriginWithPath(t *testing.T) {
	for _, origin := range []string{
		"https://host.example.com/prefix",
		"https://host.example.com/prefix/",
		"https://host.example.com?x=1",
		"https://user:pwd@host.example.com",
		"ftp://host.example.com",
		"host.example.com",
	} {
		_, err := ResolveRef("t/5/x.jpg", origin)
		require.ErrorIs(t, err, ErrInvalidRef, "origin %q 不是纯 origin，必须报错", origin)
	}
	// legacy 绝对 URL 分支不经过 origin 校验，不应因为 origin 非法而裂图。
	got, err := ResolveRef("https://old.example.com/x.jpg", "https://host.example.com/prefix")
	require.NoError(t, err)
	require.Equal(t, "https://old.example.com/x.jpg", got)
}

// TestResolveRefPreservesThumbnailContract 钉住端上契约：
// 管理端 thumbnailURL 按「URL 末位扩展名前插 _thumb」派生缩略图
// （frontend/src/utils/thumb.spec.ts:6-15）。ResolveRef 的输出必须仍是
// 普通 URL 形态，且派生出来的缩略图仍落在同一租户前缀下。
func TestResolveRefPreservesThumbnailContract(t *testing.T) {
	const origin = "https://weichuan.ziyuanxingqiu.cn"
	got, err := ResolveRef("t/5/abcd1234.jpg", origin)
	require.NoError(t, err)

	thumb := deriveThumbForTest(got)
	require.Equal(t, origin+"/uploads/t/5/abcd1234_thumb.jpg", thumb)

	// 缩略图键反过来仍要能解析成同一租户，不能被判成平台对象。
	ref, err := ParseRef(strings.TrimPrefix(thumb, origin+UploadsPathPrefix))
	require.NoError(t, err)
	require.Equal(t, ScopeTenant, ref.Scope)
	require.Equal(t, int64(5), ref.CompanyID)

	// 存量绝对 URL 也走同一条派生规则。
	thumb = deriveThumbForTest("https://cdn.example.com/uploads/x.png")
	require.Equal(t, "https://cdn.example.com/uploads/x_thumb.png", thumb)
}

func deriveThumbForTest(u string) string {
	dot := strings.LastIndexByte(u, '.')
	slash := strings.LastIndexByte(u, '/')
	if dot <= slash {
		return u
	}
	return u[:dot] + "_thumb" + u[dot:]
}

func TestRefErrorsAreComparable(t *testing.T) {
	// 调用方要用 errors.Is 分流「历史形态」与「真非法」，错误必须可判。
	_, err := ParseRef("/uploads/x.jpg")
	require.True(t, errors.Is(err, ErrLegacyRef))

	_, err = ObjectKeyFor(ScopeTenant, 0, "x.jpg")
	require.True(t, errors.Is(err, ErrScopeMismatch))

	_, err = ResolveRef("javascript:alert(1)", "")
	require.True(t, errors.Is(err, ErrInvalidRef))
}
