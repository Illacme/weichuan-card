package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 微信「消息推送」回调（图片内容安全的结论从这里回来）。
//
// 这条链路是**公网无鉴权入口**：微信服务器不会带我们的 token，能证明
// 「这条请求真的来自微信」的只有签名。所以顺序必须是
// 「先验签 → 再解密 → 再解析」，任何一步都不能因为「反正后面还要校验」而省掉。
//
// 两种模式都要支持（后台可配）：
//   - 明文模式：body 就是明文 XML，签名参数是 signature；
//   - 安全模式：body 是 <xml><Encrypt>…</Encrypt></xml>，签名参数是 msg_signature，
//     需要 EncodingAESKey 才能解出明文。
// 只支持明文是行不通的——很多账号默认开着安全模式，那样回调会一条都收不到，
// 而现象只是「图片检测永远没有结论」，极难往「推送模式配错了」上想。

// ---- 签名 ----

// Signature 计算微信消息签名：token / timestamp / nonce（安全模式再加密文）
// 按字典序排序后拼接，取 sha1 十六进制。
//
// 排序而不是按固定顺序拼接：微信的算法就是「字典序」，写死顺序在
// token 恰好排在最前或最后时能通过，换一组参数就全错——这种错只在线上暴露。
func Signature(token, timestamp, nonce string, extra ...string) string {
	parts := append([]string{token, timestamp, nonce}, extra...)
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

// VerifySignature 校验签名。签名缺失一律判失败——空签名不能等于「没配签名」。
// 用常量时间比较：签名是可枚举的短字符串，逐字节比较的耗时差足以被利用。
func VerifySignature(token, timestamp, nonce, signature string, extra ...string) bool {
	if token == "" || signature == "" {
		return false
	}
	want := Signature(token, timestamp, nonce, extra...)
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(signature))) == 1
}

// ---- 解密（安全模式） ----

// DecryptMsg 解开安全模式推送的密文，返回明文 XML。
//
// appID 只做**一致性校验**：明文尾部带着 appid，若与本地配置的不符，说明
// 这条推送属于另一个小程序（同一个推送 URL 被多个小程序复用时的典型情形）。
// 不校验就会把别家的检测结论写进自己的记录——trace_id 撞不上倒是无害，
// 但一旦撞上就是张冠李戴。
func DecryptMsg(encodingAESKey, encryptB64, appID string) (string, error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptB64))
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	if len(raw) < aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("密文长度非法: %d", len(raw))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(raw))
	// IV 取 key 前 16 字节——微信的约定，不是我们选的
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(out, raw)

	plain, err := pkcs7Unpad(out)
	if err != nil {
		return "", err
	}
	// 明文结构：16 字节随机串 + 4 字节网络序长度 + 消息体 + appid
	if len(plain) < 20 {
		return "", errors.New("解密结果过短，不像微信推送报文")
	}
	n := binary.BigEndian.Uint32(plain[16:20])
	if int(n) > len(plain)-20 {
		return "", fmt.Errorf("报文长度字段越界: %d", n)
	}
	msg := string(plain[20 : 20+int(n)])
	if appID != "" {
		if got := string(plain[20+int(n):]); got != appID {
			return "", fmt.Errorf("推送 appid 不匹配：报文 %q，本地配置 %q", got, appID)
		}
	}
	return msg, nil
}

// aesKey 把 43 位 EncodingAESKey 还原成 32 字节 AES 密钥。
// 长度不对时明确报错：直接 base64 解码一个短串会得到 24/16 字节的密钥，
// aes.NewCipher 仍然能建出 AES-192/128——加密方式变了却不会报错，
// 只会解出一堆乱码，排查时完全看不出根因是「key 少打了几位」。
func aesKey(encodingAESKey string) ([]byte, error) {
	k := strings.TrimSpace(encodingAESKey)
	if k == "" {
		return nil, errors.New("未配置 EncodingAESKey（安全模式必填）")
	}
	if len(k) != 43 {
		return nil, fmt.Errorf("EncodingAESKey 长度应为 43，实际 %d", len(k))
	}
	key, err := base64.StdEncoding.DecodeString(k + "=")
	if err != nil {
		return nil, fmt.Errorf("EncodingAESKey 不是合法 base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("EncodingAESKey 解出 %d 字节，应为 32", len(key))
	}
	return key, nil
}

// pkcs7Unpad 去填充。注意微信的块大小是 **32** 而不是 AES 的 16：
// 按 16 去填会得到「填充字节数落在 1..16」的假设，而微信实际可能填到 32，
// 于是最后几个字节被当成正文留在消息体里，XML 解析随即失败。
func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("空报文")
	}
	pad := int(b[len(b)-1])
	if pad < 1 || pad > 32 || pad > len(b) {
		return nil, fmt.Errorf("非法填充长度 %d", pad)
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("填充字节不一致，密钥可能不对")
		}
	}
	return b[:len(b)-pad], nil
}

// ---- 事件解析 ----

// 推送里的 status_code（微信口径）。
const (
	PushStatusPending = 0 // 待检测
	PushStatusRunning = 1 // 检测中
	PushStatusDone    = 2 // 检测完成
	PushStatusFailed  = 3 // 检测失败
)

// MediaCheckEvent 一条图片检测结论。
type MediaCheckEvent struct {
	TraceID    string // 对账凭据：唯一能把结论对回某次送检
	AppID      string // 归属小程序
	OpenID     string // 提交人
	Suggest    string // pass / review / risky；空串 = 本次推送没给出结论
	Label      int    // 违规标签
	StatusCode int    // 见 PushStatus*
	ErrCode    int    // 微信侧错误码（0 = 正常）
	ErrMsg     string
	Raw        string // 原始 detail / result 文本，留档备查
}

// Finished 本次推送是否已经给出终态。
// status_code=2（完成）或 3（失败）才算终态；0/1 说明微信还在检，
// 此时把记录标成 pass 会让一张违规图永远逃过处置。
func (e MediaCheckEvent) Finished() bool {
	return e.StatusCode == PushStatusDone || e.StatusCode == PushStatusFailed
}

// 推送 XML 的原始形态。字段名大小写混排（appid / trace_id 小写，ToUserName 驼峰）
// 是微信定的，不能统一。
type pushXML struct {
	XMLName    xml.Name `xml:"xml"`
	MsgType    string   `xml:"MsgType"`
	Event      string   `xml:"Event"`
	AppID      string   `xml:"appid"`
	FromUser   string   `xml:"FromUserName"`
	TraceID    string   `xml:"trace_id"`
	Version    string   `xml:"version"`
	Detail     string   `xml:"detail"`
	Result     string   `xml:"result"`
	ErrCode    string   `xml:"errcode"`
	ErrMsg     string   `xml:"errmsg"`
	StatusCode string   `xml:"status_code"`
	IsRisky    string   `xml:"isrisky"`
}

// ParseMediaCheckEvent 解析明文推送 XML。
//
// 第二个返回值为 false 表示「这不是图片检测事件」——微信的推送 URL 是**独占**的，
// 一旦配上，账号下的其它事件（关注、扫码等）也会推过来。对它们只能回 success，
// 回失败会让微信反复重推，最终把这条回调通道整个关掉。
func ParseMediaCheckEvent(body string) (MediaCheckEvent, bool) {
	var p pushXML
	if err := xml.Unmarshal([]byte(body), &p); err != nil {
		return MediaCheckEvent{}, false
	}
	if p.Event != "wxa_media_check" {
		return MediaCheckEvent{}, false
	}
	ev := MediaCheckEvent{
		TraceID:    strings.TrimSpace(p.TraceID),
		AppID:      strings.TrimSpace(p.AppID),
		OpenID:     strings.TrimSpace(p.FromUser),
		StatusCode: atoiSafe(p.StatusCode, PushStatusDone),
		ErrCode:    atoiSafe(p.ErrCode, 0),
		ErrMsg:     strings.TrimSpace(p.ErrMsg),
	}
	// 没给 status_code 的报文（v1 形态）视为终态：它带的是 isrisky，
	// 本身就是最终结论，若当成「还在检」会永远等不到第二条推送。
	if strings.TrimSpace(p.StatusCode) == "" {
		ev.StatusCode = PushStatusDone
	}

	suggest, label, raw := pickVerdict(p)
	ev.Suggest, ev.Label, ev.Raw = suggest, label, raw

	// errcode 非 0 且没有结论 → 检测失败。有结论时以结论为准：
	// 微信曾在「部分策略失败、整体仍出结论」时同时带上非 0 的 errcode。
	if ev.ErrCode != 0 && suggest == "" {
		ev.StatusCode = PushStatusFailed
	}
	if ev.StatusCode == PushStatusFailed && suggest == "" && ev.ErrMsg == "" {
		ev.ErrMsg = "微信侧检测失败（未给原因）"
	}
	return ev, true
}

// detailItem 是 detail 数组里的一项（每个检测策略一条）。
type detailItem struct {
	Suggest string `json:"suggest"`
	Label   int    `json:"label"`
}

// pickVerdict 从 result / detail / isrisky 三种形态里取结论。
//
// 为什么要按顺序兜三层：微信在不同版本里分别用过这三种字段，
// 只认其中一种的实现在灰度期会「一半回调读不出结论」——那比完全没接更糟，
// 因为记录会显示「已检测」，实际结论是空的。
func pickVerdict(p pushXML) (suggest string, label int, raw string) {
	// 1) v2 的 result：{"suggest":"pass","label":100}
	if s := strings.TrimSpace(p.Result); s != "" {
		var r detailItem
		if json.Unmarshal([]byte(s), &r) == nil && r.Suggest != "" {
			return r.Suggest, r.Label, s
		}
	}
	// 2) detail：可能是数组也可能是单个对象；任一策略判 risky 即 risky
	if s := strings.TrimSpace(p.Detail); s != "" {
		var arr []detailItem
		if json.Unmarshal([]byte(s), &arr) == nil && len(arr) > 0 {
			return worstOf(arr, s)
		}
		var one detailItem
		if json.Unmarshal([]byte(s), &one) == nil && one.Suggest != "" {
			return one.Suggest, one.Label, s
		}
	}
	// 3) v1 的 isrisky：1 = 违规，0 = 通过
	switch strings.TrimSpace(p.IsRisky) {
	case "1":
		return "risky", 0, "isrisky=1"
	case "0":
		return "pass", 0, "isrisky=0"
	}
	// 4) 87014 直接表达「命中」，即便没有 result 字段
	if atoiSafe(p.ErrCode, 0) == 87014 {
		return "risky", 87014, "errcode=87014"
	}
	return "", 0, ""
}

// worstOf 取一组策略结论里最严重的那个：多策略（图片模型 + OCR + 人工）
// 只要有一条判违规就必须按违规处理，取第一条会让违规结论被后面的 pass 盖掉。
func worstOf(arr []detailItem, raw string) (string, int, string) {
	suggest, label := arr[0].Suggest, arr[0].Label
	for _, it := range arr[1:] {
		if rank(it.Suggest) > rank(suggest) {
			suggest, label = it.Suggest, it.Label
		}
	}
	return suggest, label, raw
}

// rank 结论严重度：risky > review > pass > 空。
func rank(s string) int {
	switch s {
	case "risky":
		return 3
	case "review":
		return 2
	case "pass":
		return 1
	}
	return 0
}

// atoiSafe 解析可缺省的整数字段，失败回退缺省值。
// 微信的 XML 里数字字段有时是空标签，直接 Atoi 会得到 error 而不是 0。
func atoiSafe(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}
