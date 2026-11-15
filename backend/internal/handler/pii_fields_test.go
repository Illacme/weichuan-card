package handler

// decPII 的静默失败回归：换钥 / 密钥配错时统计页姓名会整列变空，
// 此前一条日志都没有，运营只会以为「这批人没填名字」。
//
// 用 EncryptWithKey 造「另一把密钥加密的密文」来模拟换钥后的存量数据，
// 比手改密文字节更贴近真实事故形态；同时验证日志不泄漏密文 / 明文。

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"weichuan/internal/pii"

	"github.com/stretchr/testify/require"
)

func capturePIILog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(prev)
		log.SetFlags(log.Flags())
	}()
	fn()
	return buf.String()
}

func TestDecPIILogsFailureWithoutLeakingValue(t *testing.T) {
	// 用第三把密钥加密：当前密钥必然解不开（GCM 认证失败），等价于换钥后读到旧密文。
	other := b64Rep(0x7f)
	enc, err := pii.EncryptWithKey(other, "张三")
	require.NoError(t, err)

	var got string
	out := capturePIILog(t, func() {
		got = decPII("admin/stats.detail", enc)
	})
	require.Equal(t, "", got, "解不开时仍只留空，不把密文乱码展示给运营")

	// 量尺自己有读数：日志必须真的写了，且带上链路名，否则「不留空」也可能来自别的分支。
	require.Contains(t, out, "[PII][WARN]", "解密失败必须留痕：%q", out)
	require.Contains(t, out, "admin/stats.detail", "日志要能定位是哪条统计链路：%q", out)
	require.NotContains(t, out, "张三", "日志不得写明文")
	require.NotContains(t, out, enc, "日志不得写密文")
	require.True(t, strings.Contains(out, "失败") || strings.Contains(out, "authentication"),
		"日志应带失败原因，便于判断是不是换钥：%q", out)
}

func TestDecPIIKeepsLegacyPlaintextAndValidCipherQuiet(t *testing.T) {
	enc, err := pii.Encrypt("李四")
	require.NoError(t, err)

	out := capturePIILog(t, func() {
		require.Equal(t, "已删除", decPII("admin/stats.source-top", "已删除"),
			"COALESCE 回落的历史明文不是密文，必须原样返回")
		require.Equal(t, "李四", decPII("admin/stats.detail", enc))
		require.Equal(t, "", decPII("admin/stats.detail", ""), "空值不解密、不报错")
	})
	require.NotContains(t, out, "[PII][WARN]",
		"正常路径不该刷 WARN，否则真出事时会被噪音淹没：%q", out)
}
