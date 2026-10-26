package license

// 授权验签公钥的来源与自检（2026-10-26 补）。
//
// 要守住的语义：
//  1. 环境变量 LICENSE_PUB 能覆盖内嵌公钥——发布时注入真公钥，不必改代码重编译；
//  2. 占位演示公钥能被识别出来（发布自检的依据）；
//  3. 环境变量写错时**不能**静默退回内嵌公钥——
//     以为用的是环境变量那把、实际用的是内嵌那把，是最难查的一类错。

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPublicKeyIsPlaceholderByDefault(t *testing.T) {
	// 未设置环境变量时：用的是内嵌值，且它就是那把占位演示密钥
	os.Unsetenv(PubKeyEnvVar)
	require.True(t, IsPlaceholderPublicKey(),
		"仓库内置的必须是占位演示密钥（这样自检才能识别出来）")
}

func TestEnvPubOverridesEmbedded(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	require.NoError(t, err)
	t.Setenv(PubKeyEnvVar, pub)

	got := DefaultPublicKey()
	require.NotNil(t, got, "环境变量里的公钥应生效")
	require.False(t, IsPlaceholderPublicKey(),
		"注入了自己的公钥后不应再被判定为占位密钥")

	// 注入的公钥必须真的能验签（覆盖的是「验签用的那把」，不是摆设）
	privKey := mustDecodePriv(t, priv)
	sig := ed25519.Sign(privKey, []byte("payload"))
	require.True(t, ed25519.Verify(got, []byte("payload"), sig))
}

func TestEnvPubInvalidIsDetected(t *testing.T) {
	// 写错的公钥必须被当成「有问题」，而不是静默退回内嵌那把
	t.Setenv(PubKeyEnvVar, "not-a-valid-base64!!")
	require.True(t, IsPlaceholderPublicKey(),
		"非法公钥应被自检发现（解析不出来 = 验签必然失败）")
	require.Nil(t, DefaultPublicKey(),
		"环境变量非法时不应拿内嵌公钥顶替——那会让人以为用的是环境变量那把")
}

func TestEnvPubWrongLengthIsDetected(t *testing.T) {
	// base64 合法但长度不对（Ed25519 公钥是 32 字节）
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	t.Setenv(PubKeyEnvVar, short)
	require.True(t, IsPlaceholderPublicKey())
	require.Nil(t, DefaultPublicKey())
}

// SetPublicKey（测试 / 签发工具注入）优先级最高：它是显式调用，比环境变量更具体。
func TestSetPublicKeyWinsOverEnv(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	require.NoError(t, err)
	t.Setenv(PubKeyEnvVar, base64.StdEncoding.EncodeToString(make([]byte, 32)))

	SetPublicKey(mustDecodePub(t, pub))
	defer SetPublicKey(nil)

	require.False(t, IsPlaceholderPublicKey(),
		"显式注入的公钥应优先于环境变量")
}

// mustDecodePub 解 base64 公钥为 ed25519 公钥。
func mustDecodePub(t *testing.T, b64 string) ed25519.PublicKey {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	require.Len(t, b, ed25519.PublicKeySize)
	return ed25519.PublicKey(b)
}

// mustDecodePriv 解 base64 私钥为 ed25519 私钥。
func mustDecodePriv(t *testing.T, b64 string) ed25519.PrivateKey {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	require.Len(t, b, ed25519.PrivateKeySize)
	return ed25519.PrivateKey(b)
}
