// Package mediachk 是图片内容安全的「策略层」：决定一张图要不要送检、
// 送检地址怎么拼、结论怎么归一、下架时用什么样的占位图。
//
// 与 wechat.MediaCheckAsync（只负责调接口）分开，理由和 contentsec 一样：
// 策略分支比接口调用更容易出错，而它们都不该真打外网才能验证——
// 「没配推送地址」「URL 是内网地址」「微信还在检」「结论读不出来」这几条
// 在本地必须跑得起来。
package mediachk

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"weichuan/internal/model"
	"weichuan/internal/wechat"
)

// Params 一次送检判定的输入。
type Params struct {
	Enabled bool          // 总开关（系统设置 content_sec_enabled）
	Cred    wechat.Config // 小程序凭据（租户 → 全局 → 环境变量，由调用方解析）
	OpenID  string        // 提交人 openid（v2 必填）
	// PushReady 微信消息推送是否配置就绪（Token 已填）。
	//
	// 这一项是本包里最容易被忽略、后果最隐蔽的判断：没配推送时送检**能成功**
	// （拿到 trace_id、落一条 pending），但结论永远不会回来——记录永久停在
	// 「待结论」，运营看到一堆 pending 却不知道图到底有没有问题。
	// 与其制造这种假象，不如不送：日志里明说 skipped (no_push_config)。
	PushReady bool
	BaseURL   string // 站点 / 存储公开基址，用于把相对路径绝对化成微信能访问的 URL
	Scene     int    // 场景，缺省按「资料」
}

// Decision 送检判定结果。
type Decision struct {
	Send bool   // 是否真的送检
	URL  string // 送检用的绝对 URL（Send 为真时有效）
	Key  string // 存储对象键（下架时覆盖用；推导不出时为空）
	Skip string // 跳过原因（写日志，不回给用户）
}

// Plan 判定一张图要不要送检，并把 URL 绝对化、对象键推导出来。
//
// 跳过一律**不影响上传**：图片已经存好了，内容安全是增强项、不是上传的前提。
// 与文本检测的 fail-open 一致——区别只在于图片这条链路即使「送检成功」也
// 拿不到同步结论，所以跳过的原因要更详细地记下来，否则事后根本说不清
// 「这张违规图当时为什么没被检出」。
//
// 跳过的记录**不落库**，只进日志：后台超管上传、匿名申请都拿不到 openid，
// 若每次跳过都写一行，「没配 openid」会把列表淹掉，反而看不见真正要处置的违规图。
func Plan(p Params, rawURL string) Decision {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Decision{Skip: "no_url"}
	}
	if !p.Enabled {
		return Decision{Skip: "disabled"}
	}
	if p.Cred.AppID == "" || p.Cred.AppSecret == "" {
		return Decision{Skip: "no_credential"}
	}
	if p.OpenID == "" {
		// 后台超管上传、访客匿名申请都拿不到 openid，v2 接口用不了。
		// 这类上传靠后台人工审核兜底，不在这里拦。
		return Decision{Skip: "no_openid"}
	}
	if !p.PushReady {
		return Decision{Skip: "no_push_config"}
	}
	abs := AbsURL(rawURL, p.BaseURL)
	if abs == "" {
		return Decision{Skip: "not_public"}
	}
	return Decision{Send: true, URL: abs, Key: ObjectKey(rawURL, p.BaseURL)}
}

// AbsURL 把存储返回的地址变成「微信服务器能访问到」的绝对地址。
//
// 返回空串表示**不可达**，调用方应跳过送检。本地驱动返回的是 /uploads/<name>
// 这种相对路径，拼上 BaseURL 后如果仍然是 127.0.0.1 / 内网地址，微信那边
// 只会回参数错误——那属于「必然失败的调用」，不该发。
func AbsURL(raw, baseURL string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if base == "" {
			return ""
		}
		// 本地驱动返回 /uploads/<name>，而 UPLOAD_BASE_URL 的**缺省值本身就带
		// /uploads**（http://127.0.0.1:8080/uploads）。不摘掉就会拼成
		// /uploads/uploads/<name>——微信拿到的是一条 404，而报错只会说
		// 「media_url 无效」，排查时完全看不出是这里多拼了一层。
		if strings.HasPrefix(u, "/uploads/") {
			base = strings.TrimSuffix(base, "/uploads")
		}
		if !strings.HasPrefix(u, "/") {
			u = "/" + u
		}
		u = base + u
	}
	if !Reachable(u) {
		return ""
	}
	return u
}

// Reachable 判断这个 URL 是否可能被微信服务器访问到。
//
// 只排除「确定不可达」的形态（回环 / 私有网段 / 链路本地 / 非 http(s)），
// 不做 DNS 解析也不做探活：一次上传就多一次 DNS + TCP 往返，代价远大于收益，
// 而真正的可达性由微信侧的返回码说话（不可达会回 media_url 无效，
// 那条错误会被记成送检失败并进日志）。
func Reachable(raw string) bool {
	pu, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if pu.Scheme != "http" && pu.Scheme != "https" {
		return false
	}
	host := pu.Hostname()
	if host == "" {
		return false
	}
	// 域名一律认为可达（本地开发常见的 .local / .internal 除外）
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

// ObjectKey 从公开 URL 反推存储对象键（下架时用它定位要覆盖的对象）。
//
// 与 handler/storage.go 里签名接口的推导同构，但**先处理相对路径**：
// 那里是先按绝对基址 TrimPrefix、失败再退 /uploads/，在 BaseURL 为空时
// 会把 "/uploads/x.jpg" 推成 "uploads/x.jpg"（多一层目录），覆盖时写错位置。
//
// 返回空串表示推不出来——此时这条记录不允许下架（见 model.MediaCheck.Takedownable），
// 宁可让人工去处理，也不能拿一个猜出来的键去覆盖别人的对象。
func ObjectKey(raw, baseURL string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	// 签名 URL 带 query，不切掉会连着 ?X-Amz-Signature=... 一起当键名
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if strings.HasPrefix(u, "/uploads/") {
		return strings.TrimPrefix(u, "/uploads/")
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base != "" && strings.HasPrefix(u, base+"/") {
		return strings.TrimPrefix(u, base+"/")
	}
	return ""
}

// Normalize 把微信的 suggest 归一成本系统的状态。
//
// 读不出结论（空串 / 未知取值）一律按 failed：把它当 pass 等于「检测了但
// 违规图照样在线」，而 failed 至少会在后台列表里显形，能被人看见。
func Normalize(suggest string) model.MediaCheckStatus {
	switch strings.ToLower(strings.TrimSpace(suggest)) {
	case "pass":
		return model.MediaCheckPass
	case "review":
		return model.MediaCheckReview
	case "risky":
		return model.MediaCheckRisky
	}
	return model.MediaCheckFailed
}

// ---- 占位图 ----

var (
	placeholderOnce sync.Once
	placeholderPNG  []byte
	placeholderJPEG []byte
)

// PlaceholderFor 按对象键的扩展名返回占位图与内容类型。
//
// 为什么要按扩展名分两种：下架是**覆盖同名对象**，对象键（含扩展名）不能改
// ——它已经写进了名片/相册等业务字段，改了就是一批裂图。所以只能让内容去
// 迁就扩展名：.jpg 的键里塞 JPEG，.png 的塞 PNG，静态托管按扩展名给的
// Content-Type 才和真实内容一致。
//
// .gif / .webp 没有标准库编码器，退化成 PNG 字节：浏览器与小程序 image 组件
// 都会嗅探真实格式，仍能正常显示——代价只是 Content-Type 与内容不符，
// 而「让违规图立刻下线」比「响应头完全正确」重要得多。
func PlaceholderFor(key string) ([]byte, string) {
	ensurePlaceholder()
	switch strings.ToLower(filepath.Ext(key)) {
	case ".jpg", ".jpeg":
		return placeholderJPEG, "image/jpeg"
	}
	return placeholderPNG, "image/png"
}

// Placeholder 返回 PNG 占位图（测试与不关心扩展名的场景用）。
func Placeholder() []byte {
	ensurePlaceholder()
	return placeholderPNG
}

// ensurePlaceholder 只画一次：480×480 = 23 万像素，每次下架都重画没有意义。
func ensurePlaceholder() {
	placeholderOnce.Do(func() {
		const size = 480
		bg := color.RGBA{R: 0xF2, G: 0xF3, B: 0xF5, A: 0xFF}
		fg := color.RGBA{R: 0xBC, G: 0xC0, B: 0xC8, A: 0xFF}
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				img.SetRGBA(x, y, bg)
			}
		}
		// 两条对角线画出 ×，半宽 5px（视觉上约 10px 粗，缩到 80px 头像也看得见）
		for i := 0; i < size; i++ {
			for d := -5; d <= 5; d++ {
				if x := i + d; x >= 0 && x < size {
					img.SetRGBA(x, i, fg)
				}
				if x := size - 1 - i + d; x >= 0 && x < size {
					img.SetRGBA(x, i, fg)
				}
			}
		}

		var pb, jb bytes.Buffer
		if err := png.Encode(&pb, img); err != nil {
			// png.Encode 写内存实际不会失败；真失败了也要有个能用的返回值，
			// 否则下架接口会因为没有占位图而彻底不可用——宁可覆盖成一张
			// 1×1 的灰点，也不能让「已确认违规」的图继续在线。
			placeholderPNG = fallbackPNG
		} else {
			placeholderPNG = pb.Bytes()
		}
		if err := jpeg.Encode(&jb, img, &jpeg.Options{Quality: 85}); err != nil {
			// JPEG 编不出来时退 PNG 字节：扩展名是 .jpg 但内容是 PNG，
			// 浏览器照样显示，总比不覆盖强。
			placeholderJPEG = placeholderPNG
		} else {
			placeholderJPEG = jb.Bytes()
		}
	})
}

// fallbackPNG 1×1 灰色 PNG，仅在生成占位图失败时兜底使用。
var fallbackPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xD7, 0x63, 0xF2, 0xF3, 0xF3, 0x03,
	0x00, 0x03, 0x1D, 0x01, 0x6C, 0x9E, 0x1B, 0x1B,
	0xE4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
	0x44, 0xAE, 0x42, 0x60, 0x82,
}
