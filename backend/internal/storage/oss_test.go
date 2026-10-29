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
	// paths/bodies 按请求顺序记录：Save 现在会 PUT 两次（原图 + 缩略图），
	// 只留最后一次会让「返回 URL 对不对」这类断言悄悄指向缩略图。
	paths  []string
	bodies [][]byte
	hits   int
}

func (f *fakeOSS) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		f.paths = append(f.paths, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		f.gotBody = body
		f.bodies = append(f.bodies, body)
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

	// 图片上传会 PUT 两次：原图 + <name>_thumb.<ext> 缩略图（E4）。
	require.Equal(t, 2, fake.hits, "Save 应 PUT 原图并附带一张缩略图")
	require.Equal(t, "PUT", fake.gotMethod)
	require.Len(t, fake.paths, 2)
	mainObj, thumbObj := fake.paths[0], fake.paths[1]
	require.True(t, strings.HasPrefix(mainObj, "/weichuan/"), "对象路径应为 /<bucket>/<name>，实际 %s", mainObj)
	require.True(t, strings.HasSuffix(mainObj, ".jpg"))
	require.Equal(t, strings.TrimSuffix(mainObj, ".jpg")+"_thumb.jpg", thumbObj,
		"缩略图对象名必须是原对象名末位扩展名前插 _thumb——端上按此规则派生，错一个字符就是 404")
	require.Equal(t, "http://cdn.example.com"+strings.TrimPrefix(mainObj, "/weichuan"), url)
	// 经 ProcessUpload 重新编码后，落到 OSS 的原图字节必须已剥离 EXIF/GPS 标记
	require.NotContains(t, string(fake.bodies[0]), "Exif\x00\x00", "上传到 OSS 的应是剥离后的字节，而非带 EXIF 的原图")
	require.Less(t, len(fake.bodies[1]), len(fake.bodies[0]), "缩略图字节数应小于原图")
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
