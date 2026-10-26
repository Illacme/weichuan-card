package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/stretchr/testify/require"
)

// fakeOSS 记录 OSS 请求的方法 / 路径 / 体，按 200 + x-oss-request-id 应答，
// 使 aliyun-oss-go-sdk 的 PutObject 走完整网络路径而无需真实 OSS。
type fakeOSS struct {
	gotMethod string
	gotPath   string
	gotBody   []byte
	hits      int
}

func (f *fakeOSS) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		f.gotBody = body
		w.Header().Set("x-oss-request-id", "fake-request-id")
		w.WriteHeader(http.StatusOK)
	}))
}

// newTestOssUploader 指向本地假 OSS 的 OssUploader（白盒直装，免真实服务）。
// 关键：oss.ForcePathStyle(true) 让 URL 形如 http://<endpoint>/<bucket>/<key>，
// 这样才能打到 127.0.0.1:PORT 的测试服务器（默认虚拟主机风格会拼 bucket 子域名）。
func newTestOssUploader(t *testing.T, srv *httptest.Server) *OssUploader {
	t.Helper()
	client, err := oss.New(strings.TrimPrefix(srv.URL, "http://"), "ak", "sk", oss.ForcePathStyle(true))
	require.NoError(t, err)
	b, err := client.Bucket("weichuan")
	require.NoError(t, err)
	return &OssUploader{
		Client:   client,
		Bucket:   b,
		BaseURL:  "http://cdn.example.com",
		MaxBytes: 10 << 20,
		AllowExt: DefaultAllowExt,
	}
}

func TestOssSave(t *testing.T) {
	fake := &fakeOSS{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestOssUploader(t, srv)

	fh := makeFileHeader(t, "logo.jpg", jpgWithExif())
	url, err := up.Save(fh)
	require.NoError(t, err)

	require.Equal(t, 1, fake.hits, "Save 应真正发起一次 PUT")
	require.Equal(t, "PUT", fake.gotMethod)
	require.True(t, strings.HasPrefix(fake.gotPath, "/weichuan/"), "对象路径应为 /<bucket>/<name>，实际 %s", fake.gotPath)
	require.True(t, strings.HasSuffix(fake.gotPath, ".jpg"))
	require.Equal(t, "http://cdn.example.com"+strings.TrimPrefix(fake.gotPath, "/weichuan"), url)
	// 经 ProcessImage 重新编码后，落到 OSS 的字节必须已剥离 EXIF/GPS 标记
	require.NotContains(t, string(fake.gotBody), "Exif\x00\x00", "上传到 OSS 的应是剥离后的字节，而非带 EXIF 的原图")
}

func TestOssSaveRejectsBadExtBeforeHTTP(t *testing.T) {
	fake := &fakeOSS{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestOssUploader(t, srv)

	_, err := up.Save(makeFileHeader(t, "evil.exe", []byte("MZ")))
	require.Error(t, err)
	require.Equal(t, 0, fake.hits, "扩展名不合法应在发请求前就被拒绝")
}

func TestOssSaveBytes(t *testing.T) {
	fake := &fakeOSS{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestOssUploader(t, srv)

	url, err := up.SaveBytes("wxacode/1.jpg", []byte("jpgbytes"), "image/jpeg")
	require.NoError(t, err)
	require.Equal(t, 1, fake.hits)
	require.Equal(t, "PUT", fake.gotMethod)
	require.Equal(t, "/weichuan/wxacode/1.jpg", fake.gotPath)
	require.Equal(t, "http://cdn.example.com/wxacode/1.jpg", url)
}

func TestOssPresignAndSign(t *testing.T) {
	fake := &fakeOSS{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestOssUploader(t, srv)

	// 预签名 PUT：本地签名，URL 带 OSS V1 签名参数与过期时间
	putURL, finalURL, err := up.PresignPut("direct/a.png", "image/png", 3600)
	require.NoError(t, err)
	require.Contains(t, putURL, "OSSAccessKeyId=ak")
	require.Contains(t, putURL, "Signature=")
	require.Contains(t, putURL, "Expires=")
	require.Equal(t, "http://cdn.example.com/direct/a.png", finalURL)

	// 私有桶限时 GET
	getURL, err := up.SignGetURL("private/b.png", 3600)
	require.NoError(t, err)
	require.Contains(t, getURL, "Signature=")

	// 签名是本地计算，不应打网络
	require.Equal(t, 0, fake.hits, "签名是本地计算，不应发起请求")
}

func TestNewOssUploaderInvalidConfig(t *testing.T) {
	// 空 endpoint / 空 bucket 应在构造期即报错（SDK 对空 endpoint 不校验，会拖到上传才失败）
	_, err := NewOssUploader("", "ak", "sk", "weichuan", "http://cdn.example.com", 10<<20, DefaultAllowExt)
	require.Error(t, err)
	_, err = NewOssUploader("oss-cn-hangzhou.aliyuncs.com", "ak", "sk", "", "http://cdn.example.com", 10<<20, DefaultAllowExt)
	require.Error(t, err)
}
