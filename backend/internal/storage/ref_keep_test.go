package storage

// NormalizeOrKeep 的行为契约（第五十九轮：写入侧落库前宽松归一）。
//
// 这条函数是「写入侧收敛」的唯一闸门。它要同时挡住两种反向的错：
//   - 收得太紧：外链与历史对象也被强行改写 → 正常保存失败，或对象指向一个
//     不存在的键（裂图）；
//   - 收得太松：不安全输入与「同源却推不出键」被放过 → 前者是注入面，
//     后者让库里的形态静默退回绝对 URL，出参侧看不出任何异常。
//
// 所以下面每个用例都标注了它守的是哪一条分界。

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 本平台对象一律归一成带前缀裸键（第 1 类）。
//
// 其中 /uploads/t/5/abcd.jpg 是最关键的形态：本地驱动第五十四轮起就返回它，
// 剥掉 /uploads/ 后**本身就是完整对象键**。早期版本的 NormalizeRef 在这里会撞
// 「双重加前缀」而报错——那会让本地存储下所有表单保存全部失败。
func TestNormalizeOrKeepNormalizesPlatformObjects(t *testing.T) {
	cases := []struct {
		in     string
		base   string
		scope  Scope
		cid    int64
		want   string
		reason string
	}{
		{"/uploads/t/5/abcd.jpg", "", ScopeTenant, 5, "t/5/abcd.jpg", "本地驱动相对路径（最常见）"},
		{"/uploads/p/library/cat-1.png", "", ScopePlatform, 0, "p/library/cat-1.png", "平台素材"},
		{"t/5/abcd.jpg", "", ScopeTenant, 5, "t/5/abcd.jpg", "已是裸键要幂等"},
		{"https://bucket.example.com/t/5/abcd.jpg", "https://bucket.example.com", ScopeTenant, 5,
			"t/5/abcd.jpg", "云驱动绝对 URL 按基址剥"},
		{"https://bucket.example.com/t/5/abcd.jpg?X-Amz-Signature=deadbeef",
			"https://bucket.example.com", ScopeTenant, 5,
			"t/5/abcd.jpg", "签名查询串必须丢掉，否则连签名一起写进键名"},
	}
	for _, c := range cases {
		key, kind, err := NormalizeOrKeep(c.in, c.base, c.scope, c.cid)
		require.NoError(t, err, c.reason)
		require.Equal(t, c.want, key, c.reason)
		require.Equal(t, RefNormalized, kind, c.reason)
	}
}

// 合法外链原样保留（第 2 类）。
//
// 这不是妥协：company_profile.video_url 有一档就是任意外域 MP4 直链，
// 硬归一会让「填外链」这个正常操作永远保存失败。
func TestNormalizeOrKeepKeepsForeignURL(t *testing.T) {
	for _, in := range []string{
		"https://cdn.other.com/video/demo.mp4",
		"http://media.example.org/a/b.jpg",
		"https://weixin.qq.com/x/y.png",
	} {
		key, kind, err := NormalizeOrKeep(in, "https://our.example.com", ScopeTenant, 5)
		require.NoError(t, err, "合法外链不该报错：%s", in)
		require.Equal(t, RefForeignURL, kind, "外链要标出来，否则调用方无从记日志：%s", in)
		require.Equal(t, in, key, "外链必须原样保留，改写它等于替用户改数据：%s", in)
	}
}

// 无归属前缀的历史对象原样保留（第 3 类，本轮最重要的判断）。
//
// 第五十四轮之前上传的文件键是 "x.jpg"，对象真的就在 /uploads/x.jpg。
// 给它补前缀不会报错，但出参解析会得到 /uploads/t/5/x.jpg——一个不存在的对象。
// 这类数据归 P3 搬迁处理（搬对象 + 改库），写入侧绝不能替它猜。
func TestNormalizeOrKeepKeepsLegacyObjects(t *testing.T) {
	cases := []struct {
		in   string
		base string
	}{
		{"/uploads/x.jpg", ""},
		{"/uploads/legacy/old-name.png", ""},
		{"https://our.example.com/x.jpg", "https://our.example.com"},
	}
	for _, c := range cases {
		key, kind, err := NormalizeOrKeep(c.in, c.base, ScopeTenant, 5)
		require.NoError(t, err)
		require.Equal(t, RefLegacyPath, kind,
			"无前缀历史对象必须标 legacy-path 而不是假装归一了：%s", c.in)
		require.Equal(t, c.in, key,
			"历史对象必须原样保留——补前缀会让它指向一个不存在的键：%s", c.in)
	}
}

// 不安全输入一律显式失败，不静默降级成某个猜测值（第 4 类）。
func TestNormalizeOrKeepRejectsUnsafe(t *testing.T) {
	cases := []struct{ in, why string }{
		{"javascript:alert(1)", "伪协议进 <img src> 就是执行"},
		{"data:image/svg+xml;base64,AAAA", "data URI 可夹带脚本"},
		{"//evil.com/x.jpg", "协议相对 URL 会指向外站"},
		{"t/5/../../etc/passwd", "路径穿越"},
		{`t/5/a\b.jpg`, "反斜杠在部分环境被当成路径分隔符"},
		{"/etc/passwd", "不是 /uploads/ 开头的绝对路径"},
	}
	for _, c := range cases {
		key, kind, err := NormalizeOrKeep(c.in, "https://our.example.com", ScopeTenant, 5)
		require.Error(t, err, "应拒绝：%s（%s）", c.in, c.why)
		require.True(t, errors.Is(err, ErrInvalidRef), "错误要可判定，实际：%v", err)
		require.Empty(t, key)
		require.Empty(t, kind)
	}
}

// 同源却推不出键 → 报错，绝不按外链放过（第 5 类，最隐蔽的一条）。
//
// 构造方式：基址是 https://our.example.com/uploads，而输入是
// https://our.example.com/other/x.jpg —— host 相同、基址前缀不匹配、路径里也没有
// /uploads/ 段，于是 legacyRel 推不出键。若按外链放行，库里的形态就静默退回
// 绝对 URL，而第五十三/五十七轮接的出参解析看不出任何异常。
func TestNormalizeOrKeepRejectsSameHostUnparsable(t *testing.T) {
	key, kind, err := NormalizeOrKeep(
		"https://our.example.com/other/x.jpg",
		"https://our.example.com/uploads", ScopeTenant, 5)
	require.Error(t, err, "同源但推不出键必须报错，不能当外链")
	require.Contains(t, err.Error(), "同源",
		"错误要指出这是同源问题，否则运维会去查外链白名单")
	require.Empty(t, key)
	require.Empty(t, kind)
}

// 同 host、路径不在基址下、且是 http 明文直链 → 按历史外链放行（2026-11-19 线上报障）。
//
// 真实场景：客户一直在用 `http://qiniu.../video-langhui.mp4` 这条第三方视频直链，
// 平台基址是 `https://qiniu.../aitel`（七牛 S3 的对象名带空间目录前缀）。
// host 相同、路径不同、协议是 http。《42d08ee》只放过「同 host + 同路径 + 仅协议
// 不同」，这一种仍被上面那条同源守门拒掉，于是客户什么都没改、点保存就报
// 「视频地址不合法」。http 明文不可能是本平台对象，放行不破坏「同源必拒」的初衷
// （那条守的是 https 基址配错、本平台对象退回绝对 URL 的情形）。
func TestNormalizeOrKeepAllowsSameHostHTTPForeignLink(t *testing.T) {
	const raw = "http://qiniu.zhiyuanxingqiu.cn/video-langhui.mp4"
	cases := []struct{ name, base string }{
		{"基址带七牛空间目录", "https://qiniu.zhiyuanxingqiu.cn/aitel"},
		{"基址是域名根", "https://qiniu.zhiyuanxingqiu.cn"},
		{"基址连末尾斜杠", "https://qiniu.zhiyuanxingqiu.cn/aitel/"},
	}
	require.GreaterOrEqual(t, len(cases), 3, "量尺要有读数")
	for _, c := range cases {
		key, kind, err := NormalizeOrKeep(raw, c.base, ScopeTenant, 5)
		require.NoError(t, err, "%s：http 历史直链不该被拒", c.name)
		// 两种合法归类都接受：基址是域名根时 swapScheme 能剥出相对部分 → legacy-path；
		// 基址带 /aitel 时走 http 例外 → foreign-url。两者都原样保留、不猜键改写，
		// 对用户结果一致（保存成功、地址不变），不该因归类不同判失败。
		require.Contains(t, []RefKeepKind{RefForeignURL, RefLegacyPath}, kind, c.name)
		require.Equal(t, raw, key, "%s：外链原样保留，不许猜键改写", c.name)
	}
}

// 反例：同样「同 host、路径不同」，https 地址仍必须拒——原守门语义不能丢。
func TestNormalizeOrKeepStillRejectsSameHostHTTPSUnparsable(t *testing.T) {
	_, _, err := NormalizeOrKeep(
		"https://qiniu.zhiyuanxingqiu.cn/other/x.mp4",
		"https://qiniu.zhiyuanxingqiu.cn/aitel", ScopeTenant, 5)
	require.Error(t, err, "https 同源推不出键仍要拒，不能被 http 的放宽连带放过")
	require.Contains(t, err.Error(), "同源")
}

// 空值是合法状态：avatar / cover / logo 大量为空，出参侧 ResolveRef 也约定
// 空值不报错。两侧口径必须一致，否则「清空头像」这个操作会失败。
func TestNormalizeOrKeepEmptyIsOK(t *testing.T) {
	for _, in := range []string{"", "   "} {
		key, kind, err := NormalizeOrKeep(in, "https://our.example.com", ScopeTenant, 5)
		require.NoError(t, err)
		require.Empty(t, key)
		require.Equal(t, RefNormalized, kind)
	}
}

// 归属不一致必须报错（跨租户改绑）。宽松归一只放宽「推不出键」和「平台对象」，
// 不放宽「这个键属于别人」——那正是多租户最怕的串号。
func TestNormalizeOrKeepRejectsScopeMismatch(t *testing.T) {
	key, kind, err := NormalizeOrKeep("t/9/abcd.jpg", "", ScopeTenant, 5)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrScopeMismatch))
	require.Empty(t, key)
	require.Empty(t, kind)

	// 平台字段里出现租户键也要拒：平台素材库引用某个租户的对象，
	// 等于把一个私域文件挂进所有企业都能看到的位置。
	_, _, err = NormalizeOrKeep("t/9/abcd.jpg", "", ScopePlatform, 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrScopeMismatch))
}

// 平台的键出现在租户字段里是**允许**的（有意的宽松）。
//
// 真实链路：访客在小程序申请页上传头像时没登录，请求若没带 companyId，
// 上传落在 p/ 下，open_apply.avatar 存的就是 p/xxx.jpg 而 company_id 是某租户。
// 这是既有数据形态，硬拒会让「申请提交」这条无鉴权链路直接失败。
func TestNormalizeOrKeepAllowsPlatformKeyInTenantField(t *testing.T) {
	key, kind, err := NormalizeOrKeep("p/library/cat-1.png", "", ScopeTenant, 5)
	require.NoError(t, err, "平台对象进租户字段是合法历史形态，不该阻断申请提交")
	require.Equal(t, RefNormalized, kind)
	require.Equal(t, "p/library/cat-1.png", key)

	// 本地驱动形态同理：/uploads/p/...
	key, kind, err = NormalizeOrKeep("/uploads/p/library/cat-1.png", "", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, RefNormalized, kind)
	require.Equal(t, "p/library/cat-1.png", key)
}

// 基址为空（本地驱动）时不看同源：本平台的绝对 URL 若真出现，说明数据是别处
// 搬来的。只要能剥出带前缀的键就归一，剥不出就按历史对象保留——都不报错。
// 报错会让「存量库里那些字段一改就失败」，比留着更糟。
func TestNormalizeOrKeepEmptyBase(t *testing.T) {
	// 能剥出带前缀键 → 归一
	key, kind, err := NormalizeOrKeep("https://legacy.example.com/uploads/t/5/x.jpg", "", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, RefNormalized, kind)
	require.Equal(t, "t/5/x.jpg", key)

	// 剥不出带前缀键 → 当历史对象保留
	key, kind, err = NormalizeOrKeep("https://legacy.example.com/uploads/x.jpg", "", ScopeTenant, 5)
	require.NoError(t, err)
	require.Equal(t, RefLegacyPath, kind)
	require.Equal(t, "https://legacy.example.com/uploads/x.jpg", key)
}
