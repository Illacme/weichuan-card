package wechat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// 微信消息推送（图片检测结论回调）的测试。
//
// 这条链路是**公网无鉴权入口**，错一处就是安全洞或永久失效：
//  1. 验签写成「顺序拼接」——换一组 token/nonce 就全错，只在线上暴露；
//  2. 空签名被当成通过——任何人都能伪造回调把违规图刷成 pass；
//  3. 解密按 16 字节去填充——微信是 32，明文尾部多几个字节，XML 解析失败，
//     现象是「回调收到了但一条结论都没落库」；
//  4. 只认 result 字段——灰度期一半回调读不出结论，记录显示「已检测」却是空的。

// 微信的签名算法是「字典序排序后拼接再 sha1」。
// 期望值用独立实现（Python hashlib）算好后硬编码，避免「用被测函数验被测函数」。
func TestSignatureMatchesWeChatAlgorithm(t *testing.T) {
	require.Equal(t, "94ffbd20c84766e871799062cb1bc3e3f40c5e13", Signature("abc", "123", "xyz"))
}

// 参数顺序不影响结果（排序后再拼），这与微信一致。
func TestSignatureIsOrderIndependent(t *testing.T) {
	a := Signature("tok", "1700000000", "nonce1")
	b := Signature("nonce1", "tok", "1700000000")
	require.Equal(t, a, b)
}

// 安全模式多带一个密文参与签名：msg_signature 与 signature 必须不同，
// 否则两种模式的校验会互相放行。
func TestSignatureWithExtraDiffers(t *testing.T) {
	plain := Signature("abc", "123", "nonce")
	enc := Signature("abc", "123", "nonce", "CIPHER")
	require.NotEqual(t, plain, enc)
	require.Equal(t, "eb742ed88cdc09e3b68010c3d90f27082e8194a4", enc)
}

func TestVerifySignature(t *testing.T) {
	sig := Signature("tok", "1700000000", "nonce1")
	require.Equal(t, "cb1280606383b6651d19b1fe525f375d316683d5", sig)
	require.True(t, VerifySignature("tok", "1700000000", "nonce1", sig))
	// 微信有时把签名传成大写
	require.True(t, VerifySignature("tok", "1700000000", "nonce1", "CB1280606383B6651D19B1FE525F375D316683D5"))
	require.False(t, VerifySignature("tok", "1700000000", "nonce2", sig), "nonce 变了签名就该失效")
	require.False(t, VerifySignature("other", "1700000000", "nonce1", sig), "token 不对必须拒绝")
}

// 空 token / 空签名一律拒绝：空签名绝不能等于「不用校验」，
// 否则把 token 清空就等于把回调端点开放给任何人。
func TestVerifySignatureRejectsEmpty(t *testing.T) {
	require.False(t, VerifySignature("", "1", "n", "whatever"))
	require.False(t, VerifySignature("tok", "1", "n", ""))
}

// ---- 解密 ----

// 测试用 EncodingAESKey：43 位合法 base64，解出 32 字节。
const testAESKey = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

// encryptForTest 按微信的报文结构造密文（随机串 + 长度 + 消息 + appid），
// 填充到 32 字节边界。与 DecryptMsg 互为逆运算。
func encryptForTest(t *testing.T, encodingAESKey, msg, appID string) string {
	t.Helper()
	key, err := aesKey(encodingAESKey)
	require.NoError(t, err)

	plain := bytes.Repeat([]byte("R"), 16)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(msg)))
	plain = append(plain, l[:]...)
	plain = append(plain, []byte(msg)...)
	plain = append(plain, []byte(appID)...)

	// 微信的块大小是 32
	pad := 32 - len(plain)%32
	if pad == 0 {
		pad = 32
	}
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out)
}

func TestDecryptMsgRoundTrip(t *testing.T) {
	body := "<xml><Event><![CDATA[wxa_media_check]]></Event></xml>"
	enc := encryptForTest(t, testAESKey, body, "wxappid0123456789")
	got, err := DecryptMsg(testAESKey, enc, "wxappid0123456789")
	require.NoError(t, err)
	require.Equal(t, body, got)
}

// appid 不一致必须报错：同一个推送 URL 被多个小程序复用时，
// 不校验就会把别家的结论写进自己的记录。
func TestDecryptMsgAppIDMismatch(t *testing.T) {
	enc := encryptForTest(t, testAESKey, "<xml/>", "wxother")
	_, err := DecryptMsg(testAESKey, enc, "wxappid0123456789")
	require.Error(t, err)
	require.Contains(t, err.Error(), "appid")
}

// 传空 appID 表示「不校验」——有些部署只有一个小程序，不必为此配一项。
func TestDecryptMsgSkipAppIDCheckWhenEmpty(t *testing.T) {
	enc := encryptForTest(t, testAESKey, "<xml/>", "wxother")
	got, err := DecryptMsg(testAESKey, enc, "")
	require.NoError(t, err)
	require.Equal(t, "<xml/>", got)
}

// key 长度不对必须明确报错：短 key 会被 base64 解成 16/24 字节，
// AES 仍能建 cipher，只是解出乱码——报错要能直接指向「key 少打了几位」。
func TestDecryptMsgBadKeyLength(t *testing.T) {
	_, err := DecryptMsg("tooshort", "AAAA", "wx")
	require.Error(t, err)
	require.Contains(t, err.Error(), "43")
}

func TestDecryptMsgEmptyKey(t *testing.T) {
	_, err := DecryptMsg("", "AAAA", "wx")
	require.Error(t, err)
	require.Contains(t, err.Error(), "EncodingAESKey")
}

func TestDecryptMsgBadCiphertext(t *testing.T) {
	_, err := DecryptMsg(testAESKey, "not-base64!!!", "wx")
	require.Error(t, err)
}

// ---- 事件解析 ----

func TestParseMediaCheckV2Result(t *testing.T) {
	body := `<xml>
<ToUserName><![CDATA[gh_1]]></ToUserName>
<FromUserName><![CDATA[o_user_1]]></FromUserName>
<MsgType><![CDATA[event]]></MsgType>
<Event><![CDATA[wxa_media_check]]></Event>
<appid><![CDATA[wxappid0123456789]]></appid>
<trace_id><![CDATA[trace-9]]></trace_id>
<version>2</version>
<result><![CDATA[{"suggest":"risky","label":20001}]]></result>
<errcode>0</errcode>
<errmsg><![CDATA[ok]]></errmsg>
<status_code>2</status_code>
</xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.Equal(t, "trace-9", ev.TraceID)
	require.Equal(t, "o_user_1", ev.OpenID)
	require.Equal(t, "risky", ev.Suggest)
	require.Equal(t, 20001, ev.Label)
	require.True(t, ev.Finished())
}

// detail 数组：任一策略判违规即违规（取第一条会让违规被后面的 pass 盖掉）。
func TestParseMediaCheckDetailArrayTakesWorst(t *testing.T) {
	body := `<xml>
<Event>wxa_media_check</Event>
<trace_id>t1</trace_id>
<detail><![CDATA[[{"strategy":"content_model","suggest":"pass","label":100},{"strategy":"ocr","suggest":"risky","label":20002}]]]></detail>
<status_code>2</status_code>
</xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.Equal(t, "risky", ev.Suggest)
	require.Equal(t, 20002, ev.Label)
}

// v1 形态只有 isrisky。
func TestParseMediaCheckIsRisky(t *testing.T) {
	body := `<xml><Event>wxa_media_check</Event><trace_id>t2</trace_id><isrisky>1</isrisky></xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.Equal(t, "risky", ev.Suggest)
	require.True(t, ev.Finished(), "v1 报文没有 status_code，应视为终态")
}

// 只有 errcode=87014 也必须读出「违规」，否则会被当成「没结论」而放行。
func TestParseMediaCheckErrCode87014(t *testing.T) {
	body := `<xml><Event>wxa_media_check</Event><trace_id>t3</trace_id><errcode>87014</errcode></xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.Equal(t, "risky", ev.Suggest)
	require.Equal(t, 87014, ev.Label)
}

// status_code=1（检测中）不是终态：此时把记录标成 pass 会让违规图永远逃过处置。
func TestParseMediaCheckRunningIsNotFinished(t *testing.T) {
	body := `<xml><Event>wxa_media_check</Event><trace_id>t4</trace_id><status_code>1</status_code></xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.False(t, ev.Finished())
}

// status_code=3（检测失败）且无结论 → 标成失败，并给一句能看的兜底原因。
func TestParseMediaCheckFailed(t *testing.T) {
	body := `<xml><Event>wxa_media_check</Event><trace_id>t5</trace_id><status_code>3</status_code></xml>`
	ev, ok := ParseMediaCheckEvent(body)
	require.True(t, ok)
	require.True(t, ev.Finished())
	require.Equal(t, PushStatusFailed, ev.StatusCode)
	require.Empty(t, ev.Suggest)
	require.NotEmpty(t, ev.ErrMsg)
}

// 推送 URL 是独占的：关注、扫码等其它事件也会推过来，必须能识别并「不当回事」。
func TestParseIgnoresOtherEvents(t *testing.T) {
	_, ok := ParseMediaCheckEvent(`<xml><Event>subscribe</Event></xml>`)
	require.False(t, ok)
}

func TestParseIgnoresMalformedXML(t *testing.T) {
	_, ok := ParseMediaCheckEvent(`not xml at all`)
	require.False(t, ok)
}
