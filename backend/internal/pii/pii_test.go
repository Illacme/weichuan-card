package pii

import (
	"bytes"
	"encoding/base64"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	for _, plain := range []string{"张三", "13800138000", "a@b.com", "微信昵称"} {
		enc, err := Encrypt(plain)
		require.NoError(t, err)
		require.True(t, IsEncrypted(enc), "密文应带版本前缀")
		require.NotContains(t, enc, plain, "密文不应包含明文")
		got, err := Decrypt(enc)
		require.NoError(t, err)
		require.Equal(t, plain, got)
	}
}

func TestEncryptEmptyIsEmpty(t *testing.T) {
	enc, err := Encrypt("")
	require.NoError(t, err)
	require.Equal(t, "", enc)
	got, err := Decrypt("")
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestEncryptIsRandomized(t *testing.T) {
	a, err := Encrypt("张三")
	require.NoError(t, err)
	b, err := Encrypt("张三")
	require.NoError(t, err)
	require.NotEqual(t, a, b, "同一明文每次加密应产生不同密文（随机 nonce）")
}

func TestDecryptLegacyPlaintextPassthrough(t *testing.T) {
	// 未回填的历史明文（无 v1: 前缀）应原样返回，保证升级期可读。
	got, err := Decrypt("13800138000")
	require.NoError(t, err)
	require.Equal(t, "13800138000", got)
}

func TestDecryptTamperedFails(t *testing.T) {
	enc, err := Encrypt("张三")
	require.NoError(t, err)
	// 篡改最后一个字符（仍是合法 base64 字符），密文校验应失败而不是返回垃圾明文。
	tampered := enc[:len(enc)-1] + "A"
	if tampered == enc {
		tampered = enc[:len(enc)-1] + "B"
	}
	_, err = Decrypt(tampered)
	require.Error(t, err, "被篡改的密文必须解密失败")
}

func TestBlindIndexDeterministicAndNormalized(t *testing.T) {
	a := BlindIndex("张三")
	b := BlindIndex(" 张三 ")
	c := BlindIndex("张 三")
	require.NotEmpty(t, a)
	require.Equal(t, a, b, "首尾空白不应影响盲索引")
	require.Equal(t, a, c, "内部空白应被归一化")
	require.NotEqual(t, a, BlindIndex("李四"), "不同值盲索引必须不同")
}

func TestCurrentKeyIDAndWithKey(t *testing.T) {
	keyA := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	keyB := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	require.NotEqual(t, keyA, keyB)

	// 恢复内置 dev 密钥，避免改动包级状态殃及其它测试。
	defer SetKey("")

	require.NoError(t, SetKey(keyA))
	idA := CurrentKeyID()
	require.NotEmpty(t, idA, "有效密钥必须产生非空指纹")
	require.Equal(t, idA, CurrentKeyID(), "同一密钥指纹必须稳定（进程内不漂移）")

	require.NoError(t, SetKey(keyB))
	idB := CurrentKeyID()
	require.NotEqual(t, idA, idB, "不同密钥必须产生不同指纹")

	require.NoError(t, SetKey(keyA))
	require.Equal(t, idA, CurrentKeyID(), "切回同一密钥指纹必须还原")

	// EncryptWithKey/DecryptWithKey 不依赖包级当前密钥：
	// keyA 加密的密文只能被 keyA 解，被 keyB 解必须失败。
	enc, err := EncryptWithKey(keyA, "topsecret")
	require.NoError(t, err)
	require.True(t, IsEncrypted(enc))
	got, err := DecryptWithKey(keyA, enc)
	require.NoError(t, err)
	require.Equal(t, "topsecret", got)
	_, err = DecryptWithKey(keyB, enc)
	require.Error(t, err, "非对应密钥必须解不开 WithKey 密文")

	// Decrypt（当前=keyA）能解 keyA 的密文；切到 keyB 后解不开 —— 印证「换钥匙后旧密文解不开」。
	require.NoError(t, SetKey(keyA))
	got2, err := Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "topsecret", got2)
	require.NoError(t, SetKey(keyB))
	_, err = Decrypt(enc)
	require.Error(t, err, "当前密钥与密文密钥不符必须解密失败")
}

func TestBlindIndexEmailCaseInsensitive(t *testing.T) {
	require.Equal(t, BlindIndex("A@B.com"), BlindIndex("a@b.com "))
}

func TestBlindIndexEmpty(t *testing.T) {
	require.Equal(t, "", BlindIndex(""))
	require.Equal(t, "", BlindIndex("   "))
}

// Reload 的存在理由：包级 init 早于配置加载，安装向导写进**配置文件**的密钥
// 在 init 时还不存在，必须靠 Reload 才能生效。这里同时验证「Reload 真的换了钥匙」
// ——只断言状态位会在「算了新值却没装进去」的实现下假通过。
func TestReloadPicksUpNewKey(t *testing.T) {
	real := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv(KeyEnvVar, real)
	Reload()
	t.Cleanup(func() {
		os.Unsetenv(KeyEnvVar)
		Reload()
	})

	if UsingPlaceholderKey() {
		t.Fatal("Reload 后应读到环境变量里的真密钥，不应再是占位密钥")
	}
	enc, err := Encrypt("张三")
	if err != nil {
		t.Fatalf("用真密钥加密失败：%v", err)
	}

	// 换回 dev 密钥：同一串密文必须解不开。能解开说明 Reload 没真正换掉钥匙。
	os.Unsetenv(KeyEnvVar)
	Reload()
	if !UsingPlaceholderKey() {
		t.Fatal("清掉环境变量并 Reload 后应回落到占位密钥")
	}
	if _, err := Decrypt(enc); err == nil {
		t.Fatal("换钥后旧密文不应还能解开（说明 Reload 没有真正生效）")
	}
}

func TestKeyShape(t *testing.T) {
	// 用 SetKey 固定包级状态，不依赖 CI 是否设了 PII_ENC_KEY；
	// 退出时恢复 dev 密钥，避免污染同包其它用例。
	defer SetKey("")

	require.NoError(t, SetKey(""))
	require.True(t, UsingPlaceholderKey(), "空密钥应回落内置 dev 密钥并标记为占位")
	require.NoError(t, KeyError())
	require.Len(t, CurrentKeyID(), 16, "指纹是 sha256 hex 的前 16 位")

	// 非法 base64：报错、清空指纹，且不准继续加解密。
	require.Error(t, SetKey("not-a-valid-base64!"))
	require.Error(t, KeyError())
	require.Empty(t, CurrentKeyID(), "坏密钥不能留下旧指纹")
	_, err := Encrypt("张三")
	require.Error(t, err, "坏密钥下必须拒绝加密")

	// 合法 base64 但不是 32 字节（AES-256 要求）。
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	require.Error(t, SetKey(short))
	require.Error(t, KeyError())
	require.Empty(t, CurrentKeyID())

	// 合法 32 字节：装载成功，占位标记与错误位都要清掉。
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	require.NoError(t, SetKey(good))
	require.False(t, UsingPlaceholderKey())
	require.NoError(t, KeyError())
	require.Len(t, CurrentKeyID(), 16)
}
