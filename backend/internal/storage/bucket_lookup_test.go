package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/nginxsnippet"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
)

func TestS3BucketLookupFollowsHostHeader(t *testing.T) {
	cases := []struct {
		name string
		t    nginxsnippet.Target
		want minio.BucketLookupType
	}{
		{
			name: "hostHeader 留空且 endpoint 是 IP：桶名进 path",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverMinio,
				Endpoint: "127.0.0.1:19000", Bucket: "wc-t7", Enabled: true,
			},
			want: minio.BucketLookupPath,
		},
		{
			name: "hostHeader 等于 endpoint：仍是路径风格",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverMinio,
				Endpoint: "s3.example.com", HostHeader: "s3.example.com",
				Bucket: "wc-t7", Enabled: true,
			},
			want: minio.BucketLookupPath,
		},
		{
			name: "hostHeader 以 <bucket>. 开头：桶名进 Host",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverS3,
				Endpoint: "s3.example.com", HostHeader: "wc-t7.s3.example.com",
				Bucket: "wc-t7", Enabled: true,
			},
			want: minio.BucketLookupDNS,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := S3BucketLookup(c.t)
			require.NoError(t, err)
			require.Equal(t, c.want, got)
		})
	}
}

func TestS3BucketLookupRejectsUndecidableConfig(t *testing.T) {
	cases := []struct {
		name string
		t    nginxsnippet.Target
	}{
		{
			name: "hostHeader 既不是 endpoint 也不带桶名前缀",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverMinio,
				Endpoint: "127.0.0.1:19000", HostHeader: "cdn.other.example.com",
				Bucket: "wc-t7", Enabled: true,
			},
		},
		{
			name: "company_id=0（平台自身没有自有桶）",
			t: nginxsnippet.Target{
				CompanyID: 0, Driver: nginxsnippet.DriverMinio,
				Endpoint: "127.0.0.1:19000", Bucket: "wc-t7", Enabled: true,
			},
		},
		{
			name: "endpoint 带了 scheme",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverMinio,
				Endpoint: "https://127.0.0.1:19000", Bucket: "wc-t7", Enabled: true,
			},
		},
		{
			name: "bucket 含路径分隔符",
			t: nginxsnippet.Target{
				CompanyID: 7, Driver: nginxsnippet.DriverMinio,
				Endpoint: "127.0.0.1:19000", Bucket: "wc-t7/../other", Enabled: true,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := S3BucketLookup(c.t)
			require.Error(t, err, "判断不出风格时必须显式报错，不许交给 SDK 猜")
		})
	}
}

// TestS3BucketLookupIsTheOnlyStyleDecision 是源码级守门。
//
// 上传侧与回填侧如果各写一份路径风格判定（或任一侧退回 BucketLookupAuto），
// 症状是「对象写进去了、nginx 取不回来」：两边各自的日志都是绿的，
// 第六十三轮真桶实测正是这样才拖到很晚才发现。
//
// 守门自带「至少抓到 2 处」兜底：抓取型守门最坏的坏法是改个写法就一条也抓不到，
// 于是「全部通过」（项目里已踩过一次）。
func TestS3BucketLookupIsTheOnlyStyleDecision(t *testing.T) {
	files := []string{
		filepath.Join("..", "uploadroute", "resolver.go"),
		filepath.Join("..", "refmigrate", "s3store.go"),
	}
	found := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err, "读不到 %s：守门不能因为文件改名就静默放行", f)
		s := codeOnly(string(raw))
		if !strings.Contains(s, "S3BucketLookup(") {
			t.Errorf("%s 没有调用 S3BucketLookup：路径风格不许在这一侧另立判定", f)
			continue
		}
		if strings.Contains(s, "BucketLookupAuto") {
			t.Errorf("%s 出现了 BucketLookupAuto：这一侧必须显式传判定结果", f)
		}
		found++
	}
	require.GreaterOrEqual(t, found, 2, "只抓到 %d 处调用，守门可能已失效", found)

	// 上传侧算了 bl 还得真的传进客户端构造，否则算了个寂寞。
	b, err := os.ReadFile(files[0])
	require.NoError(t, err)
	require.Contains(t, string(b), ", bl)",
		"uploadroute.build 必须把 S3BucketLookup 的结果传给 NewMinioUploader")
}

// codeOnly 去掉整行注释后剩下的代码。
//
// 守门扫的是代码，不是注释文本：注释里解释「为什么不能用 BucketLookupAuto」
// 不该被判成违规。第一版守门就是用朴素 Contains，把 s3store.go 的这段注释
// 扫成了失败——这正是抓取型守门最典型的假阳性。
func codeOnly(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// 反向验证钩子：把 NewMinioUploader 的默认风格改成 Path 时，
// 平台存储（sys_setting，没有 host_header）的行为会被悄悄改变。
// 这条断言钉住「变参省略 = Auto」这条兼容契约。
func TestNewMinioUploaderDefaultsToAutoLookup(t *testing.T) {
	b, err := os.ReadFile("minio.go")
	require.NoError(t, err)
	require.Contains(t, string(b), "bl := minio.BucketLookupAuto",
		"省略 lookup 变参时必须是 SDK 默认风格，不要顺手改掉平台存储的行为")
}
