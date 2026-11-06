package urlprobe

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 假网络：把 DNS 结果与拨号目标都换成可控值，测试不联网也能走完整 HTTP 路径。
// 用 t.Cleanup 还原，避免一个用例污染后面的（包级变量是共享的）。
func fakeNetwork(t *testing.T, ips []net.IP, dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	t.Helper()
	oldLookup, oldDial := lookupIPFn, dialFn
	lookupIPFn = func(_ context.Context, _ string) ([]net.IP, error) { return ips, nil }
	if dial != nil {
		dialFn = dial
	}
	t.Cleanup(func() { lookupIPFn, dialFn = oldLookup, oldDial })
}

func publicIP() net.IP { return net.ParseIP("93.184.216.34") }

func TestValidateRejectsUnsafeURLs(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want error
	}{
		{"空地址", "   ", ErrEmptyURL},
		{"超长", "http://cdn.example.com/" + strings.Repeat("a", maxURLLen), ErrTooLong},
		{"javascript", "javascript:alert(1)", ErrInvalidScheme},
		{"file 协议", "file:///etc/passwd", ErrInvalidScheme},
		{"ftp", "ftp://cdn.example.com/a.mp4", ErrInvalidScheme},
		{"无 scheme", "cdn.example.com/a.mp4", ErrInvalidScheme},
		{"带用户名密码", "http://user:pass@cdn.example.com/a.mp4", ErrCredentials},
		{"localhost", "http://localhost/a.mp4", ErrBlockedHost},
		{"大写 LOCALHOST", "http://LOCALHOST/a.mp4", ErrBlockedHost},
		{"子域 .localhost", "http://x.localhost/a.mp4", ErrBlockedHost},
		{"mDNS .local", "http://nas.local/a.mp4", ErrBlockedHost},
		{"云元数据域名", "http://metadata.google.internal/computeMetadata/v1/", ErrBlockedHost},
		{"保留 TLD .test", "http://cdn.test/a.mp4", ErrBlockedHost},
		{"单标签主机走搜索域", "http://intranet/a.mp4", ErrBlockedHost},
		{"十进制整数 IP", "http://2130706433/", ErrBlockedHost},
		{"十六进制整数 IP", "http://0x7f000001/", ErrBlockedHost},
		{"端口越界", "http://cdn.example.com:99999/a.mp4", ErrInvalidPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := validate(tc.url)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestValidateAcceptsNormalCDNURLs(t *testing.T) {
	for _, raw := range []string{
		"http://cdn.example.com/aitel/video.mp4",
		"https://qiniu.ziyuanxingqiu.cn/aitel/a.mp4?x=1",
		"https://cdn.example.com:8443/a.mp4",
		"http://8.8.8.8/a.mp4",
		"/uploads/2026/a.mp4", // 站内相对路径：不是绝对地址，交给界面提示，不该被当 SSRF 拦
	} {
		u, port, err := validate(raw)
		// 相对路径没有 scheme，按设计应被拒（界面会提示填绝对地址）
		if strings.HasPrefix(raw, "/") {
			require.ErrorIs(t, err, ErrInvalidScheme)
			continue
		}
		require.NoError(t, err, raw)
		require.NotEmpty(t, u.Hostname(), raw)
		require.NotEmpty(t, port, raw)
	}
	// 「notlocalhost」不能被当成 localhost 的后缀误杀
	_, _, err := validate("https://notlocalhost.example.com/a.mp4")
	require.NoError(t, err)
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "192.0.2.1",
		"192.88.99.1", "198.18.0.1", "203.0.113.1", "224.0.0.1", "240.0.0.1",
		"::1", "::", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "64:ff9b::1",
		"::ffff:127.0.0.1", // IPv4-mapped 写法也得挡
	}
	for _, s := range blocked {
		t.Run("blocked/"+s, func(t *testing.T) {
			require.True(t, blockedIP(net.ParseIP(s)), s)
		})
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700::1111"}
	for _, s := range allowed {
		t.Run("allowed/"+s, func(t *testing.T) {
			require.False(t, blockedIP(net.ParseIP(s)), s)
		})
	}
}

func TestProbeRejectsLiteralPrivateIPWithoutDialing(t *testing.T) {
	var dialed atomic.Int32
	oldDial := dialFn
	dialFn = func(_ context.Context, _, _ string) (net.Conn, error) {
		dialed.Add(1)
		return nil, errors.New("不该拨号")
	}
	t.Cleanup(func() { dialFn = oldDial })

	// 故意不覆盖 lookupIPFn：真实解析器对 IP 字面量会直接返回该 IP，
	// 正好验证「字面量也过不了 blockedIP」这条链。
	for _, raw := range []string{
		"http://127.0.0.1:8080/admin", "http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/", "http://[::1]/",
	} {
		_, err := Probe(context.Background(), raw)
		require.ErrorIs(t, err, ErrBlockedHost, raw)
	}
	require.Zero(t, dialed.Load(), "私网地址绝不该走到拨号")
}

func TestProbeRejectsDNSRebinding(t *testing.T) {
	var dialed atomic.Int32
	// 一个公网 + 一个私网：必须整体拒绝，不能「挑能用的那个」。
	fakeNetwork(t,
		[]net.IP{publicIP(), net.ParseIP("127.0.0.1")},
		func(_ context.Context, _, _ string) (net.Conn, error) {
			dialed.Add(1)
			return nil, errors.New("不该拨号")
		})
	_, err := Probe(context.Background(), "http://rebind.example.com/a.mp4")
	require.ErrorIs(t, err, ErrBlockedHost)
	require.Zero(t, dialed.Load())
}

func TestProbeDNSFailure(t *testing.T) {
	oldLookup := lookupIPFn
	lookupIPFn = func(_ context.Context, _ string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIPFn = oldLookup })
	_, err := Probe(context.Background(), "http://nope.example.com/a.mp4")
	require.ErrorIs(t, err, ErrNoPublicIP)

	lookupIPFn = func(_ context.Context, _ string) ([]net.IP, error) { return nil, nil }
	_, err = Probe(context.Background(), "http://nope.example.com/a.mp4")
	require.ErrorIs(t, err, ErrNoPublicIP)
}

// startFakeCDN 起一个本地 httptest，但让 URL 用「公网域名」：
// lookup 返回公网 IP（过校验），dial 忽略 pin 出来的地址、改连 httptest。
func startFakeCDN(t *testing.T, h http.HandlerFunc) (string, *atomic.Int32) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	target := srv.Listener.Addr().String()
	var dialed atomic.Int32
	fakeNetwork(t, []net.IP{publicIP()},
		func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			dialed.Add(1)
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", target)
		})
	return "http://cdn.example.com/aitel/demo.mp4", &dialed
}

func TestProbeSuccessIsHEADOnlyAndKeepsHost(t *testing.T) {
	var methods, hosts []string
	url, dialed := startFakeCDN(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		hosts = append(hosts, r.Host)
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "1234")
		w.WriteHeader(http.StatusOK)
	})
	res, err := Probe(context.Background(), url)
	require.NoError(t, err)
	require.True(t, res.Reachable)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "video/mp4", res.ContentType)
	require.Equal(t, int64(1234), res.ContentLength)
	require.Contains(t, res.Message, "地址可访问")
	require.Equal(t, []string{"HEAD"}, methods, "只能发 HEAD，绝不拉响应体")
	require.Equal(t, []string{"cdn.example.com"}, hosts, "Host 头必须还是原域名")
	require.Equal(t, int32(1), dialed.Load())
}

func TestProbeStatusHints(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		contentTyp string
		location   string
		wantMsg    string
		wantOK     bool
	}{
		{"404 提示目录前缀", 404, "application/xml", "", "目录前缀", false},
		{"403 提示 HTTPS 未启用", 403, "text/plain", "", "httpsDisabled", false},
		{"200 但返回网页", 200, "text/html; charset=utf-8", "", "不是视频文件", false},
		{"405 提示浏览器确认", 405, "", "", "浏览器里确认", false},
		{"500 通用失败", 500, "", "", "不可用", false},
		{"302 不跟随", 302, "", "http://127.0.0.1/admin", "不跟随跳转", false},
		{"206 也算可访问", 206, "video/mp4", "", "地址可访问", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := startFakeCDN(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentTyp != "" {
					w.Header().Set("Content-Type", tc.contentTyp)
				}
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
			})
			res, err := Probe(context.Background(), url)
			require.NoError(t, err)
			require.Equal(t, tc.status, res.Status)
			require.Equal(t, tc.wantOK, res.Reachable)
			require.Contains(t, res.Message, tc.wantMsg)
		})
	}
}

func TestProbeDoesNotFollowRedirectToIntranet(t *testing.T) {
	var hits atomic.Int32
	url, _ := startFakeCDN(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Location", "http://127.0.0.1:8080/admin")
		w.WriteHeader(http.StatusFound)
	})
	res, err := Probe(context.Background(), url)
	require.NoError(t, err)
	require.Equal(t, 302, res.Status)
	require.False(t, res.Reachable)
	require.Equal(t, int32(1), hits.Load(), "重定向只能记一次，绝不发起第二次请求")
}

func TestProbeRequestFailureIsGeneric(t *testing.T) {
	fakeNetwork(t, []net.IP{publicIP()},
		func(_ context.Context, _, _ string) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		})
	_, err := Probe(context.Background(), "http://cdn.example.com/a.mp4")
	require.ErrorIs(t, err, ErrRequest)
	require.NotContains(t, err.Error(), "93.184.216.34", "报错不能泄漏内部解析细节")
}

func TestPinnedClientHardening(t *testing.T) {
	cli := newPinnedClient(publicIP(), "80")
	tr, ok := cli.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, tr.Proxy, "必须显式关代理，否则会绕过固定 IP")
	require.NotNil(t, tr.DialContext)
	require.NotNil(t, cli.CheckRedirect)
	require.LessOrEqual(t, cli.Timeout, requestTimeout)

	// DialContext 拿到的必须是「已校验 IP:端口」，不是 URL 里的主机名。
	var got string
	oldDial := dialFn
	dialFn = func(_ context.Context, _ string, addr string) (net.Conn, error) {
		got = addr
		return nil, errors.New("stop")
	}
	t.Cleanup(func() { dialFn = oldDial })
	_, _ = tr.DialContext(context.Background(), "tcp", "attacker.example.com:80")
	require.Equal(t, "93.184.216.34:80", got)

	// HTTPS 默认端口、IPv6 也要拼对。
	tr6 := newPinnedClient(net.ParseIP("2606:4700::1111"), "443").Transport.(*http.Transport)
	_, _ = tr6.DialContext(context.Background(), "tcp", "x:443")
	require.Equal(t, "[2606:4700::1111]:443", got)
}
