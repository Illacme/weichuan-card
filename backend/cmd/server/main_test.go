package main

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/pii"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// PII 密钥门槛的四条边界必须钉死。方向错在两头都很贵：
// 漏拦 = 生产用源码里公开的 dev 密钥加密、且没人会察觉；
// 误拦 = 连走向导的新装都起不来，所有人被挡在门外。
func TestPIIKeyGate(t *testing.T) {
	// pii 的密钥是包级状态，改完必须还原，否则会污染同包其它用例
	t.Cleanup(func() { pii.Reload() })
	valid := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	t.Run("给了合法密钥则放行", func(t *testing.T) {
		t.Setenv(pii.KeyEnvVar, valid)
		t.Setenv("ALLOW_INSECURE", "")
		pii.Reload()
		if err := piiKeyGate(); err != nil {
			t.Fatalf("合法密钥应放行，实际被拦：%v", err)
		}
	})
	t.Run("占位密钥必须拦下", func(t *testing.T) {
		t.Setenv(pii.KeyEnvVar, "")
		t.Setenv("ALLOW_INSECURE", "")
		pii.Reload()
		err := piiKeyGate()
		if err == nil {
			t.Fatal("未设置 PII_ENC_KEY 时生产必须拒绝启动")
		}
		if !strings.Contains(err.Error(), pii.KeyEnvVar) {
			t.Fatalf("错误信息应指明该改哪个环境变量，实际：%v", err)
		}
	})
	t.Run("ALLOW_INSECURE 是本地联调的逃生门", func(t *testing.T) {
		t.Setenv(pii.KeyEnvVar, "")
		t.Setenv("ALLOW_INSECURE", "1")
		pii.Reload()
		if err := piiKeyGate(); err != nil {
			t.Fatalf("ALLOW_INSECURE=1 应放行本地联调：%v", err)
		}
	})
	t.Run("密钥格式错误不受逃生门影响", func(t *testing.T) {
		t.Setenv(pii.KeyEnvVar, "not-a-valid-base64!!")
		t.Setenv("ALLOW_INSECURE", "1")
		pii.Reload()
		if err := piiKeyGate(); err == nil {
			t.Fatal("配了格式错误的密钥必须拦下：放行会让此后每一次加解密都失败")
		}
	})
}

// clientIPAfter 装好可信代{配置后取一次 ClientIP，模拟「这个请求是谁发来的」。
// remoteAddr 是对端（反代）地址，xff 是请求自带的 X-Forwarded-For。
func clientIPAfter(t *testing.T, trusted []string, remoteAddr, xff string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	applyTrustedProxies(r, config.Config{TrustedProxies: trusted})
	r.GET("/ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })

	req := httptest.NewRequest("GET", "/ip", nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

// TestSpoofedXFFIgnoredOnDirectConnection 直连（对端是公网地址）时，客户端自带的
// X-Forwarded-For 必须被完全忽略。
//
// 这条守的是限流的**唯一防线**：登录 / refresh / 小程序码三处都按 c.ClientIP() 分桶。
// gin 的内置缺省信任所有代理，此时 ClientIP() 取 XFF 最左一项，客户端每次换一个值
// 就换一个桶——实测同一 IP 连打 15 次错误密码、全部放行（正常应在第 10 次拦下）。
// 也就是说「防爆破限流」在缺省配置下等于不存在。
func TestSpoofedXFFIgnoredOnDirectConnection(t *testing.T) {
	got := clientIPAfter(t, config.DefaultTrustedProxies, "198.51.100.9:1234", "1.2.3.4")
	require.Equal(t, "198.51.100.9", got,
		"对端不在可信网段时，XFF 一律不可信：否则一次伪造就换掉限流桶")
}

// TestXFFUsedBehindPrivateProxy 反代位于私有网段（同机 nginx / docker bridge /
// k8s / 内网 LB 都落在这里）时，要采信 XFF 里记录的客户端地址。
//
// 与上一条是同一枚硬币的两面：收得太紧会把所有用户算成反代的 IP、共用一个桶，
// 一个人就能把整站限流打满。真实客户端 IP 恰好是「用户在限流里被公平对待」的前提。
func TestXFFUsedBehindPrivateProxy(t *testing.T) {
	for _, proxy := range []string{
		"172.18.0.5:44321",   // docker compose 默认 bridge 网段
		"10.0.3.7:443",       // k8s pod / 内网 LB
		"192.168.1.10:51000", // 局域网反代
		"127.0.0.1:60000",    // 同机 nginx
	} {
		got := clientIPAfter(t, config.DefaultTrustedProxies, proxy, "203.0.113.7")
		require.Equal(t, "203.0.113.7", got, "反代 %s 在可信网段，应采信 XFF", proxy)
	}
}

// TestXFFChainPicksRightmostUntrusted 校验 gin 的解析语义：链上出现多项时，
// 从右往左跳过可信代理、取第一个不可信地址。
//
// 注意本仓库自带的 nginx 已经改成**覆盖** XFF（见 frontend/nginx.conf 的长注释），
// 正常部署不会出现这种多项链。这条留着是因为语义本身有安全含义：一旦有人把
// nginx 换回 $proxy_add_x_forwarded_for（或前面加了一层追加式反代），
// 「跳过可信代理」这一步就会把客户端伪造的左值当成答案——那是实测过的绕过路径。
// 它锁的是「万一回到追加模式，行为至少仍是可预期的」，不是当前部署的拓扑。
func TestXFFChainPicksRightmostUntrusted(t *testing.T) {
	got := clientIPAfter(t, config.DefaultTrustedProxies, "172.18.0.5:443", "1.2.3.4, 203.0.113.7")
	require.Equal(t, "203.0.113.7", got, "应取最右侧（反代亲手追加的）地址，而不是客户端伪造的左值")
}

// TestInvalidTrustedProxiesFallsBackToNoTrust 配置写错时不静默、且退回安全侧。
//
// 退回方向很重要：退回「谁都不信」会让限流偏严（宁可误拦），
// 而退回「信所有人」等于把防爆破关掉。gin 解析失败时会提前返回、把那份含
// 无效项的原始列表留在 engine 里，所以必须显式置空。
func TestInvalidTrustedProxiesFallsBackToNoTrust(t *testing.T) {
	got := clientIPAfter(t, []string{"not-a-cidr"}, "172.18.0.5:443", "203.0.113.7")
	require.Equal(t, "172.18.0.5", got, "配置无效时应退回「不信任任何代理」")
}

// TestTrustedProxiesNoneMeansNoProxy TRUSTED_PROXIES=none 表示前面没有反代，
// 此时即使对端恰好来自私有网段也不能采信 XFF（例如后端被直接暴露的场景）。
func TestTrustedProxiesNoneMeansNoProxy(t *testing.T) {
	got := clientIPAfter(t, nil, "172.18.0.5:443", "203.0.113.7")
	require.Equal(t, "172.18.0.5", got)
}

// TestNewUploaderFallsBackToLocalOnBadConfig 云存储凭据在后台填错时，服务必须还能起来。
//
// 这是「改错可自愈」的底线：若构造失败就 Fatal，管理员连后台都进不去，
// 也就永远改不回来——只能手工改库或改环境变量，等于把人锁在系统外。
func TestNewUploaderFallsBackToLocalOnBadConfig(t *testing.T) {
	// oss 驱动但端点为空 → NewOssUploader 必然失败
	cfg := config.Config{StorageDriver: "oss", UploadDir: t.TempDir()}
	up := newUploaderWithFallback(cfg)

	_, ok := up.(*storage.LocalUploader)
	require.True(t, ok, "构造失败应降级为本地存储，而不是让进程退出")
}

// TestNewUploaderUsesConfiguredDriver 正常配置下不走降级路径（保证降级只在出错时发生）。
func TestNewUploaderUsesConfiguredDriver(t *testing.T) {
	cfg := config.Config{StorageDriver: "local", UploadDir: t.TempDir()}
	up := newUploaderWithFallback(cfg)

	local, ok := up.(*storage.LocalUploader)
	require.True(t, ok)
	require.Equal(t, cfg.UploadDir, local.Dir)
}
