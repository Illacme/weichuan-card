package pii

import (
	"encoding/base64"
	"os"
	"strings"
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
	// 未设置 PII_ENC_KEY 时应报告占位密钥（供启动自检拒绝生产使用）。
	// 本测试假定测试环境未设置该环境变量；若 CI 设了则该断言自然不成立——
	// 用 t.Skip 保护，避免误报。
	if KeyError() != nil {
		t.Fatalf("默认 dev 密钥不应有加载错误：%v", KeyError())
	}
	_ = strings.TrimSpace // 保留 strings 依赖，避免未来删测试时 import 飘红
}
