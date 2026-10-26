package handler

// 海报个性化设置的归一逻辑测试。
//
// 只测纯函数、不碰数据库：这几个函数是**唯一**的准入闸口——自定义背景的
// file 会进 canvas 的 img.src、bgId 会进本地存储键、版式的数值会直接当
// 坐标用。归一漏了就是脏数据落库 + 客户端渲染错位，而这类问题在真机上
// 只表现为「背景没了 / 文字跑到画布外」，很难从现象反推原因。
//
// （带 DB 的接口测试见 handler_test.go 的 setup，但本沙箱禁止删除文件，
// SQLite 提交事务删不掉 -journal 会报 SQLITE_IOERR_DELETE，故此处只覆盖
// 不依赖数据库的部分。）

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPosterLayoutPatchDefaults(t *testing.T) {
	// 空补丁（客户端没传 / 老版本不认 layout 字段）→ 全套默认值。
	// 新员工 / 从没调过设置的人必须拿到与改造前完全一致的海报，
	// 否则升级当天所有人发现自己的海报变了样。
	got := posterLayoutPatch{}.resolve()
	require.Equal(t, DefaultPosterLayout(), got)
	require.Equal(t, PosterTemplateClassic, got.Template)
	require.Equal(t, 72, got.AvatarSize)
	require.Equal(t, 0, got.OffsetY)
	// 姓名默认显示：老版本存的 layout 里没有 showName，不能因此把名字藏起来
	require.True(t, got.ShowName)
	require.Equal(t, "", got.CustomText)
	// 二维码尺寸默认自动：老版本没有 qrSize，不能突然把码放大或缩小
	require.Equal(t, 0, got.QrSize)
	// 座机 / 邮箱默认关：这两行以前从不出现在海报上，默认开等于让所有人的
	// 海报凭空多两行。码默认开——它是海报唯一的落地入口。
	require.False(t, got.ShowLandline)
	require.False(t, got.ShowEmail)
	require.True(t, got.ShowQr)
}

func TestPosterLayoutPatchExplicitFalse(t *testing.T) {
	// 关键区别：明确传 false 才是「关掉」。若用 bool 而非指针接请求，
	// 空对象 {} 会被解析成「五个元素全关」——用户发出去是一张只剩背景的白板。
	no := false
	got := posterLayoutPatch{ShowPhone: &no, ShowName: &no}.resolve()
	require.False(t, got.ShowPhone)
	require.False(t, got.ShowName)
	require.True(t, got.ShowAvatar, "没传的字段必须保持默认开")
	require.True(t, got.ShowCompany)
}

func TestPosterLayoutPatchNewToggles(t *testing.T) {
	// 座机 / 邮箱默认关、显式传 true 才开；码默认开、显式传 false 才关。
	// 两个方向的默认值刻意相反：前两者是「新出现的信息」（默认塞给用户会
	// 改掉他已经调好的版式），后者是「海报唯一的落地入口」（默认藏起来会
	// 让用户发出去一张没人扫得到的图）。
	yes, no := true, false
	on := posterLayoutPatch{ShowLandline: &yes, ShowEmail: &yes}.resolve()
	require.True(t, on.ShowLandline)
	require.True(t, on.ShowEmail)
	require.True(t, on.ShowQr)

	off := posterLayoutPatch{ShowQr: &no}.resolve()
	require.False(t, off.ShowQr)
	// 关码不该连带关掉别的元素
	require.True(t, off.ShowPhone)
	require.True(t, off.ShowName)
}

func TestNormalizePosterText(t *testing.T) {
	// 换行必须压平：canvas 的 fillText 不认 \n，会画出缺字或方块，
	// 而粘贴进来的文案里很自然会带换行。折成空格而不是直接删——
	// 英文 "hello\nworld" 直接删会粘成 helloworld。
	require.Equal(t, "24 小时 上门维修", normalizePosterText("24 小时\n上门维修"))
	require.Equal(t, "a b", normalizePosterText("  a\t\t b  "))
	require.Equal(t, "", normalizePosterText("   \n\t "))
	require.Equal(t, "", normalizePosterText(""))
	// 全角空格也当空白处理，否则「一句话」里会留一个看起来像空白的怪字符
	require.Equal(t, "A B", normalizePosterText("A\u3000B"))
	// 控制字符（比如粘贴进来的 \u0000）变空格，不能让它们进 canvas
	require.Equal(t, "A B", normalizePosterText("A\u0000B"))
	// 超长按 rune 截断，不会把中文或 emoji 劈成半个
	long := ""
	for i := 0; i < maxPosterTextLen+10; i++ {
		long += "字"
	}
	require.Equal(t, maxPosterTextLen, len([]rune(normalizePosterText(long))))
	require.Equal(t, maxPosterTextLen, len([]rune(normalizePosterText("🎉"+long))))
}

func TestPosterLayoutPatchWhitelist(t *testing.T) {
	// 白名单外的模板 / 配色一律落默认，不能原样透传（否则前端画不出东西）
	bad := "evil"
	rainbow := "rainbow"
	got := posterLayoutPatch{Template: &bad, TextColor: &rainbow}.resolve()
	require.Equal(t, PosterTemplateClassic, got.Template)
	require.Equal(t, PosterTextAuto, got.TextColor)

	for _, tpl := range []string{
		PosterTemplateClassic, PosterTemplateCenter, PosterTemplateMinimal, PosterTemplateCard,
		PosterTemplateSplit, PosterTemplateLeft, PosterTemplateStack, PosterTemplatePoster,
	} {
		v := tpl
		require.Equal(t, tpl, posterLayoutPatch{Template: &v}.resolve().Template)
	}
	for _, c := range []string{PosterTextAuto, PosterTextLight, PosterTextDark} {
		v := c
		require.Equal(t, c, posterLayoutPatch{TextColor: &v}.resolve().TextColor)
	}
}

func TestPosterLayoutPatchClampsOffset(t *testing.T) {
	// 位移上限只挡「离谱值」：真正的可拖区间由客户端按内容包围盒算，
	// 服务端算不出文字多宽，能做的只是保证不出现画布量级的脏数据。
	big, small := 99999, -99999
	require.Equal(t, posterOffsetLimit, posterLayoutPatch{OffsetY: &big}.resolve().OffsetY)
	require.Equal(t, -posterOffsetLimit, posterLayoutPatch{OffsetY: &small}.resolve().OffsetY)
	require.Equal(t, posterOffsetLimit, posterLayoutPatch{OffsetX: &big}.resolve().OffsetX)
	require.Equal(t, -posterOffsetLimit, posterLayoutPatch{OffsetX: &small}.resolve().OffsetX)
	require.Equal(t, posterOffsetLimit, posterLayoutPatch{QrOffsetX: &big}.resolve().QrOffsetX)
	require.Equal(t, -posterOffsetLimit, posterLayoutPatch{QrOffsetY: &small}.resolve().QrOffsetY)

	// 上限必须大于画布长边，否则「拖到画布另一头」会被后端截断
	require.Greater(t, posterOffsetLimit, 460, "上限不能小于画布长边，否则贴边拖不过去")

	// 区间内的值原样保留：以前横向只有 ±16，拖一下就被截断，手感像卡住
	for _, v := range []int{0, 10, 30, 60, 120, 200} {
		x := v
		require.Equal(t, v, posterLayoutPatch{OffsetX: &x}.resolve().OffsetX)
		require.Equal(t, v, posterLayoutPatch{OffsetY: &x}.resolve().OffsetY)
		require.Equal(t, v, posterLayoutPatch{QrOffsetX: &x}.resolve().QrOffsetX)
		require.Equal(t, v, posterLayoutPatch{QrOffsetY: &x}.resolve().QrOffsetY)
	}

	// 没传就必须是 0，不能继承上次的偏移
	d := posterLayoutPatch{}.resolve()
	require.Equal(t, 0, d.OffsetX)
	require.Equal(t, 0, d.OffsetY)
	require.Equal(t, 0, d.QrOffsetX)
	require.Equal(t, 0, d.QrOffsetY)
}

func TestNearestPosterQrSize(t *testing.T) {
	// 二维码尺寸吸附到档位：库里不留界面表达不出来的值，
	// 否则下次进页面「选中态」会凭空消失。
	cases := map[int]int{
		0: 0, -5: 0, // 0 / 负数 = 自动
		30: 76, 56: 76, 76: 76, // 小于中档的都给「小」
		90: 76,                      // 与 76 / 104 等距 → 取较小档（与头像同一规则）
		95: 104, 104: 104, 120: 104, // 靠近中档给「中」
		130: 140, 140: 140, 999: 140, // 超过上限给「大」
	}
	for in, want := range cases {
		require.Equal(t, want, nearestPosterQrSize(in), "输入 %d", in)
	}
	// 每个档位自己必须稳定（吸附后不再变）
	for _, s := range posterQrSizes {
		require.Equal(t, s, nearestPosterQrSize(s))
	}
}

func TestNearestAvatarSize(t *testing.T) {
	// 取最近档位而不是夹紧：传 70 应得 72（最近），传 200 应得 88（上限档）。
	// 等距（80）取较小档 72。
	cases := map[int]int{0: 56, 40: 56, 56: 56, 60: 56, 70: 72, 72: 72, 80: 72, 84: 88, 200: 88, -50: 56}
	for in, want := range cases {
		require.Equal(t, want, nearestAvatarSize(in), "输入 %d", in)
	}
}

func TestNormalizePosterBackgroundsDropsInvalid(t *testing.T) {
	in := []PosterBackground{
		{ID: "c-1a2b3c4d", Name: "我的照片", File: "/uploads/2026/10/a.jpg"},    // 合法
		{ID: "c-5e6f7a8b", Name: "", File: "https://cdn.example.com/b.jpg"}, // 合法（名字补默认）
		{ID: "", Name: "没 id", File: "/uploads/c.jpg"},                      // 丢：id 空
		{ID: "c-9", Name: "坏协议", File: "javascript:alert(1)"},               // 丢：伪协议
		{ID: "c-10", Name: "协议相对", File: "//evil.example.com/d.jpg"},        // 丢：协议相对
		{ID: "c-11", Name: "没文件", File: ""},                                 // 丢：空路径
		{ID: "c-1a2b3c4d", Name: "重复 id", File: "/uploads/e.jpg"},           // 丢：id 重复
	}
	got := normalizePosterBackgrounds(in)
	require.Len(t, got, 2)
	require.Equal(t, "c-1a2b3c4d", got[0].ID)
	require.Equal(t, "我的照片", got[0].Name)
	require.Equal(t, "我的背景", got[1].Name, "空名字要补默认，否则选择条上是一片空白")
	require.Equal(t, "https://cdn.example.com/b.jpg", got[1].File)
}

func TestNormalizePosterBackgroundsCapsCount(t *testing.T) {
	// 超过上限截断而不是报错：上传第 10 张时不该让整次保存失败
	in := make([]PosterBackground, 0, 20)
	for i := 0; i < 20; i++ {
		in = append(in, PosterBackground{
			ID:   "c-" + string(rune('a'+i)) + "0000000",
			Name: "背景",
			File: "/uploads/x.jpg",
		})
	}
	require.Len(t, normalizePosterBackgrounds(in), maxPosterBackgrounds)
}

func TestNormalizePosterBackgroundsTruncatesName(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "字"
	}
	got := normalizePosterBackgrounds([]PosterBackground{{ID: "c-1", Name: long, File: "/uploads/a.jpg"}})
	require.Len(t, got, 1)
	require.Len(t, []rune(got[0].Name), maxPosterBgNameLen)
}

func TestNormalizePosterBackgroundsEmpty(t *testing.T) {
	// nil / 空切片 / 全是脏值 → 返回 nil（序列化成 null，读回来当空列表）
	require.Nil(t, normalizePosterBackgrounds(nil))
	require.Nil(t, normalizePosterBackgrounds([]PosterBackground{}))
	require.Nil(t, normalizePosterBackgrounds([]PosterBackground{{ID: "x", File: "javascript:1"}}))
}

func TestValidPosterFile(t *testing.T) {
	ok := []string{"/uploads/a.jpg", "https://cdn.example.com/a.jpg", "http://cdn.example.com/a.jpg"}
	for _, f := range ok {
		require.True(t, validPosterFile(f), f)
	}
	bad := []string{"", "//evil.com/a.jpg", "javascript:alert(1)", "data:image/png;base64,xx", "uploads/a.jpg"}
	for _, f := range bad {
		require.False(t, validPosterFile(f), f)
	}
}

func TestDecodePosterLayoutFallsBack(t *testing.T) {
	// 库里可能存着历史脏值（改过白名单 / 手工改库）：读取侧也要归一，
	// 保证返回给客户端的永远合法。缺字段按默认值补，而不是当成「关闭」。
	require.Equal(t, DefaultPosterLayout(), decodePosterLayout(""))
	require.Equal(t, DefaultPosterLayout(), decodePosterLayout("{not json"))
	require.Equal(t, DefaultPosterLayout(), decodePosterLayout(`{"template":"nope"}`))

	raw, _ := json.Marshal(PosterLayout{Template: PosterTemplateCard, AvatarSize: 88, ShowPhone: false})
	got := decodePosterLayout(string(raw))
	require.Equal(t, PosterTemplateCard, got.Template)
	require.Equal(t, 88, got.AvatarSize)
	require.False(t, got.ShowPhone)
}

func TestDecodePosterBackgroundsFallsBack(t *testing.T) {
	require.Nil(t, decodePosterBackgrounds(""))
	require.Nil(t, decodePosterBackgrounds("null"))
	require.Nil(t, decodePosterBackgrounds("[broken"))

	got := decodePosterBackgrounds(`[{"id":"c-1","name":"图","file":"/uploads/a.jpg"}]`)
	require.Len(t, got, 1)
	require.Equal(t, "c-1", got[0].ID)
}

func TestIsPosterBgAvailable(t *testing.T) {
	bgs := []PosterBackground{{ID: "c-1", Name: "图", File: "/uploads/a.jpg"}}
	// 内置素材交给客户端清单校验，这里不拦
	require.True(t, isPosterBgAvailable("poster-brand", bgs))
	require.True(t, isPosterBgAvailable("c-1", bgs))
	// 自定义背景被删了还留着 bgId → 下次进页面渲染一张空背景，必须拦掉
	require.False(t, isPosterBgAvailable("c-2", bgs))
	require.False(t, isPosterBgAvailable("c-1", nil))
}

func TestPosterBgIDRejectsUnsafe(t *testing.T) {
	ok := []string{"", "poster-brand", "c-1a2b3c4d", "A_b-9"}
	for _, id := range ok {
		require.True(t, posterBgIDRe.MatchString(id), id)
	}
	bad := []string{"c 1", "c-1;drop", "../etc", "c-1/../x", "c-" + string(make([]byte, 80))}
	for _, id := range bad {
		require.False(t, posterBgIDRe.MatchString(id), id)
	}
}
