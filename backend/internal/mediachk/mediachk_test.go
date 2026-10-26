package mediachk

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 图片送检策略层的测试。
//
// 这一层的价值全在「不该发的请求别发」上——图片已经存好了，送检是增强项，
// 但**发出去却收不到结论**是最坏的一种失败：记录显示「已送检」，
// 运营以为在管，实际一条结论都不会回来。所以每条跳过分支都要有测试。

var testCred = wechat.Config{AppID: "wx1", AppSecret: "s1"}

func baseParams() Params {
	return Params{
		Enabled:   true,
		Cred:      testCred,
		OpenID:    "openid-abc",
		PushReady: true,
		BaseURL:   "https://card.example.com",
	}
}

func TestPlanSendsForPublicURL(t *testing.T) {
	d := Plan(baseParams(), "/uploads/2026/10/abc.jpg")
	require.True(t, d.Send)
	require.Equal(t, "https://card.example.com/uploads/2026/10/abc.jpg", d.URL)
	require.Equal(t, "2026/10/abc.jpg", d.Key)
	require.Empty(t, d.Skip)
}

func TestPlanSkipReasons(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Params)
		url  string
		skip string
	}{
		{"空地址", func(*Params) {}, "  ", "no_url"},
		{"总开关关闭", func(p *Params) { p.Enabled = false }, "/uploads/a.jpg", "disabled"},
		{"没配凭据", func(p *Params) { p.Cred = wechat.Config{} }, "/uploads/a.jpg", "no_credential"},
		{"只配了 appid", func(p *Params) { p.Cred = wechat.Config{AppID: "wx1"} }, "/uploads/a.jpg", "no_credential"},
		{"没有 openid", func(p *Params) { p.OpenID = "" }, "/uploads/a.jpg", "no_openid"},
		// 没配推送 = 送出去也拿不回结论，只能制造一条永久 pending 的假记录
		{"没配推送地址", func(p *Params) { p.PushReady = false }, "/uploads/a.jpg", "no_push_config"},
		{"内网地址", func(*Params) {}, "/uploads/a.jpg", "not_public"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := baseParams()
			c.mut(&p)
			if c.skip == "not_public" {
				p.BaseURL = "http://127.0.0.1:8080"
			}
			d := Plan(p, c.url)
			require.False(t, d.Send)
			require.Equal(t, c.skip, d.Skip)
		})
	}
}

func TestAbsURL(t *testing.T) {
	require.Equal(t,
		"https://cdn.example.com/a.jpg",
		AbsURL("https://cdn.example.com/a.jpg", "https://ignored.example.com"),
		"已经是绝对地址就原样返回（不拼基址）")
	require.Equal(t,
		"https://card.example.com/uploads/a.jpg",
		AbsURL("/uploads/a.jpg", "https://card.example.com/"),
		"相对路径拼基址，尾斜杠要吃掉")
	require.Equal(t, "https://card.example.com/uploads/a.jpg",
		AbsURL("uploads/a.jpg", "https://card.example.com"), "缺前导斜杠要补")
	// UPLOAD_BASE_URL 的缺省值本身带 /uploads（http://host:8080/uploads），
	// 而本地驱动返回的又是 /uploads/<name>——不摘掉就会拼成 /uploads/uploads/<name>
	require.Equal(t, "https://card.example.com/uploads/a.jpg",
		AbsURL("/uploads/a.jpg", "https://card.example.com/uploads"))
}

func TestAbsURLRejectsUnreachable(t *testing.T) {
	// 本地开发形态：微信服务器访问不到 127.0.0.1，送过去只会得到参数错误
	require.Equal(t, "", AbsURL("/uploads/a.jpg", "http://127.0.0.1:8080"))
	require.Equal(t, "", AbsURL("/uploads/a.jpg", "http://localhost:8080"))
	require.Equal(t, "", AbsURL("/uploads/a.jpg", "http://192.168.1.10"))
	require.Equal(t, "", AbsURL("/uploads/a.jpg", ""), "没有基址就拼不出绝对地址")
}

func TestReachable(t *testing.T) {
	ok := []string{
		"https://cdn.example.com/a.jpg",
		"http://1.2.3.4/a.jpg",
		"https://card.company.cn/uploads/a.jpg",
	}
	for _, u := range ok {
		require.True(t, Reachable(u), u)
	}
	bad := []string{
		"http://127.0.0.1:8080/a.jpg",
		"http://localhost/a.jpg",
		"http://10.1.2.3/a.jpg",
		"http://172.16.0.9/a.jpg",
		"http://192.168.0.2/a.jpg",
		"http://169.254.1.1/a.jpg",
		"http://0.0.0.0/a.jpg",
		"http://[::1]/a.jpg",
		"http://nas.local/a.jpg",
		"ftp://example.com/a.jpg",
		"not-a-url",
		"",
	}
	for _, u := range bad {
		require.False(t, Reachable(u), u)
	}
}

func TestObjectKey(t *testing.T) {
	require.Equal(t, "2026/10/abc.jpg", ObjectKey("/uploads/2026/10/abc.jpg", "https://card.example.com"))
	require.Equal(t, "2026/10/abc.jpg", ObjectKey("https://cdn.example.com/2026/10/abc.jpg", "https://cdn.example.com"))
	// 签名 URL 的 query 必须切掉，否则键名会带上一整串 ?X-Amz-Signature=...
	require.Equal(t, "2026/10/abc.jpg",
		ObjectKey("https://cdn.example.com/2026/10/abc.jpg?X-Amz-Signature=deadbeef", "https://cdn.example.com"))
	// BaseURL 为空时相对路径仍要推对（老实现会推成 uploads/2026/...）
	require.Equal(t, "2026/10/abc.jpg", ObjectKey("/uploads/2026/10/abc.jpg", ""))
	// 推不出来就返回空——宁可不下架，也不能拿猜出来的键覆盖别人的对象
	require.Equal(t, "", ObjectKey("https://other.example.com/x.jpg", "https://cdn.example.com"))
	require.Equal(t, "", ObjectKey("", "https://cdn.example.com"))
}

func TestNormalize(t *testing.T) {
	require.Equal(t, model.MediaCheckPass, Normalize("pass"))
	require.Equal(t, model.MediaCheckPass, Normalize(" PASS "))
	require.Equal(t, model.MediaCheckReview, Normalize("review"))
	require.Equal(t, model.MediaCheckRisky, Normalize("risky"))
	// 读不出结论必须落 failed：当 pass 等于「检测了但违规图照样在线」
	require.Equal(t, model.MediaCheckFailed, Normalize(""))
	require.Equal(t, model.MediaCheckFailed, Normalize("unknown"))
}

// 占位图必须是一张能被解码的 PNG，且尺寸稳定（前端可能按原尺寸展示）。
func TestPlaceholderIsValidPNG(t *testing.T) {
	b := Placeholder()
	require.NotEmpty(t, b)
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	require.Equal(t, 480, img.Bounds().Dx())
	require.Equal(t, 480, img.Bounds().Dy())

	// 多次调用返回同一份（sync.Once 缓存），避免每次下架都重新画 23 万像素
	require.Equal(t, b, Placeholder())
}

// 下架是覆盖同名对象，扩展名改不了——所以内容必须迁就扩展名，
// 否则静态托管按 .jpg 给出的 Content-Type 与真实内容不符。
func TestPlaceholderForMatchesExtension(t *testing.T) {
	pngBytes, pngType := PlaceholderFor("2026/10/abc.png")
	require.Equal(t, "image/png", pngType)
	_, err := png.Decode(bytes.NewReader(pngBytes))
	require.NoError(t, err)

	for _, key := range []string{"2026/10/abc.jpg", "2026/10/ABC.JPEG"} {
		jpgBytes, jpgType := PlaceholderFor(key)
		require.Equal(t, "image/jpeg", jpgType, key)
		_, err := jpeg.Decode(bytes.NewReader(jpgBytes))
		require.NoError(t, err, key)
	}

	// gif / webp 没有标准库编码器，退化成 PNG 字节（浏览器会嗅探真实格式）
	gifBytes, gifType := PlaceholderFor("2026/10/abc.gif")
	require.Equal(t, "image/png", gifType)
	require.Equal(t, pngBytes, gifBytes)
}
