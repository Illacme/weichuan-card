package nginxsnippet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 私有桶的 location 绝不能长成「匿名回源桶」那样：
// 那个方向没有签名，真桶必 403，而上传侧一切正常，属于最难察觉的裂图。
func TestPrivateTargetRendersBackendRedirectLocation(t *testing.T) {
	tg := Target{
		CompanyID: 7, Driver: DriverOSS, Endpoint: "oss.example.com",
		Bucket: "private-bucket", UseSSL: true, HostHeader: "private-bucket.oss.example.com",
		Enabled: true, Private: true,
	}
	out, err := Render([]Target{tg})
	require.NoError(t, err)

	require.Contains(t, out, "location ^~ /uploads/t/7/ {")
	require.Contains(t, out, "proxy_pass http://127.0.0.1:8080;")
	require.Contains(t, out, `add_header Cache-Control "no-store" always;`)
	require.Contains(t, out, "add_header X-Content-Type-Options nosniff always;")
	// 反向断言：私有桶配置里不许出现任何直连桶的痕迹。
	require.NotContains(t, out, "proxy_pass https://oss.example.com;")
	require.NotContains(t, out, "rewrite ^/uploads/")
	require.NotContains(t, out, "proxy_ssl_name")
	require.NotContains(t, out, "add_header Cache-Control $uploads_cache")

	// 同一批 targets 必须参与对账：漏生成私有桶 location 也会让请求落回静态盘。
	diff := Reconcile([]Target{tg}, out)
	require.True(t, diff.OK(), diff.String())
}

// 切换公开/私有后忘记重新生成，是这条链路最容易漏的一步：
// cid 还在、location 也在，只比 cid 的对账会报「一致」，而线上是匿名回源私有桶。
func TestReconcileDetectsPublicPrivateModeMismatch(t *testing.T) {
	public := Target{
		CompanyID: 7, Driver: DriverMinio, Endpoint: "s.example.com",
		Bucket: "assets", Enabled: true,
	}
	private := public
	private.Private = true

	publicSnippet, err := Render([]Target{public})
	require.NoError(t, err)
	privateSnippet, err := Render([]Target{private})
	require.NoError(t, err)

	d := Reconcile([]Target{private}, publicSnippet)
	require.False(t, d.OK())
	require.Equal(t, []int64{7}, d.ModeMismatch)
	require.Contains(t, d.String(), "未重新生成")

	d = Reconcile([]Target{public}, privateSnippet)
	require.False(t, d.OK())
	require.Equal(t, []int64{7}, d.ModeMismatch)
}
