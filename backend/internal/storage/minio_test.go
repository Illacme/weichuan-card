package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// fakeS3 记录 S3 兼容请求的方法 / 路径 / 查询，并按 200 + ETag 应答，
// 使 minio-go 的 PutObject 走完整网络路径而无需真实对象存储。
// 注意：handler 必须完整读完请求体再应答，否则客户端仍在写 body 时连接被
// 关闭，会报 "EOF"。
type fakeS3 struct {
	gotMethod string
	gotPath   string
	gotQuery  map[string]string
	gotBody   []byte
	hits      int
}

func (f *fakeS3) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		f.gotQuery = map[string]string{}
		for k := range r.URL.Query() {
			f.gotQuery[k] = r.URL.Query().Get(k)
		}
		body, _ := io.ReadAll(r.Body)
		f.gotBody = body
		w.Header().Set("ETag", `"9b71f8f1c1f0a3d4e5f6a7b8c9d0e1f2"`)
		w.WriteHeader(http.StatusOK)
	}))
}

// newTestMinioUploader 指向本地假 S3 的 MinioUploader（白盒直装，免真实服务）。
// 两个要点：
//  1. 凭据用显式静态 V4（与 NewMinioUploader 一致）——若不传 Creds，minio-go 会走
//     默认凭据链（含 EC2 IAM 查询），沙箱内必然超时失败；
//  2. 显式指定 Region——否则首次 presign / put 会先发一次 GET /<bucket>?location
//     探测区域，假服务返回空体会让客户端解析 XML 时 EOF。
func newTestMinioUploader(t *testing.T, srv *httptest.Server) *MinioUploader {
	t.Helper()
	client, err := minio.New(strings.TrimPrefix(srv.URL, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4("ak", "sk", ""),
		Region: "us-east-1",
	})
	require.NoError(t, err)
	return &MinioUploader{
		Client:   client,
		Bucket:   "weichuan",
		BaseURL:  "http://cdn.example.com",
		MaxBytes: 10 << 20,
		AllowExt: DefaultAllowExt,
	}
}

func TestMinioSave(t *testing.T) {
	fake := &fakeS3{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestMinioUploader(t, srv)

	fh := makeFileHeader(t, "logo.jpg", jpgWithExif())
	url, err := up.Save(fh)
	require.NoError(t, err)

	require.Equal(t, 1, fake.hits, "Save 应真正发起一次 PUT")
	require.Equal(t, "PUT", fake.gotMethod)
	require.True(t, strings.HasPrefix(fake.gotPath, "/weichuan/"), "对象路径应为 /<bucket>/<name>，实际 %s", fake.gotPath)
	require.True(t, strings.HasSuffix(fake.gotPath, ".jpg"))
	require.Equal(t, "http://cdn.example.com"+strings.TrimPrefix(fake.gotPath, "/weichuan"), url, "返回 BaseURL + 对象名")
	// 经 ProcessImage 重新编码后，落到 MinIO 的字节必须已剥离 EXIF/GPS 标记
	require.NotContains(t, string(fake.gotBody), "Exif\x00\x00", "上传到 MinIO 的应是剥离后的字节，而非带 EXIF 的原图")
}

func TestMinioSaveRejectsBadExtBeforeHTTP(t *testing.T) {
	fake := &fakeS3{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestMinioUploader(t, srv)

	_, err := up.Save(makeFileHeader(t, "evil.exe", []byte("MZ")))
	require.Error(t, err)
	require.Equal(t, 0, fake.hits, "扩展名不合法应在发请求前就被拒绝")
}

func TestMinioSaveBytes(t *testing.T) {
	fake := &fakeS3{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestMinioUploader(t, srv)

	url, err := up.SaveBytes("wxacode/1.jpg", []byte("jpgbytes"), "image/jpeg")
	require.NoError(t, err)
	require.Equal(t, 1, fake.hits)
	require.Equal(t, "PUT", fake.gotMethod)
	require.Equal(t, "/weichuan/wxacode/1.jpg", fake.gotPath, "relPath 应原样作为对象名")
	// bytes.Reader 场景 minio-go 走 aws-chunked 流式签名，body 带 chunk 帧，
	// 故断言「包含原始内容」而非全等。
	require.Contains(t, string(fake.gotBody), "jpgbytes")
	require.Equal(t, "http://cdn.example.com/wxacode/1.jpg", url)
}

func TestMinioPresignAndSign(t *testing.T) {
	fake := &fakeS3{}
	srv := fake.server(t)
	defer srv.Close()
	up := newTestMinioUploader(t, srv)

	// 预签名 PUT：不发起网络请求，URL 带 AWS V4 签名参数
	putURL, finalURL, err := up.PresignPut("direct/a.png", "image/png", 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, putURL, "X-Amz-Signature=", "预签名 URL 应含签名")
	require.Contains(t, putURL, "X-Amz-Algorithm=AWS4-HMAC-SHA256")
	require.Equal(t, "http://cdn.example.com/direct/a.png", finalURL)

	// 私有桶限时 GET：同样本地签名
	getURL, err := up.SignGetURL("private/b.png", 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, getURL, "X-Amz-Signature=")

	// 两个签名操作都不应打网络
	require.Equal(t, 0, fake.hits, "签名是本地计算，不应发起请求")
}

func TestNewMinioUploaderInvalidEndpoint(t *testing.T) {
	// 空 endpoint 应在构造期即报错（minio.New 对空 endpoint 返回错误）
	_, err := NewMinioUploader("", "ak", "sk", "weichuan", "http://cdn.example.com", "", false, 10<<20, DefaultAllowExt)
	require.Error(t, err)
}

// TestNewMinioUploaderRegion 区域参数必须透传到 minio 客户端：部分 S3 云
// （腾讯云 COS / 华为云 OBS 等）的 GetBucketLocation 探测不可靠或不实现，
// 显式区域是它们能正常工作的前提。region 会进入 V4 签名的 credential scope
// （X-Amz-Credential=.../<region>/s3/aws4_request，查询串里 / 编码为 %2F），
// 这是可从外部观察的透传证据；且显式区域后 presign 不再发 location 探测请求。
func TestNewMinioUploaderRegion(t *testing.T) {
	fake := &fakeS3{}
	srv := fake.server(t)
	defer srv.Close()

	up, err := NewMinioUploader(strings.TrimPrefix(srv.URL, "http://"), "ak", "sk", "weichuan",
		"http://cdn.example.com", "ap-guangzhou", false, 10<<20, DefaultAllowExt)
	require.NoError(t, err)

	putURL, _, err := up.PresignPut("direct/a.png", "image/png", 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, putURL, "ap-guangzhou%2Fs3%2Faws4_request", "区域应进入签名 credential scope")
	require.Equal(t, 0, fake.hits, "显式 region 应跳过 GetBucketLocation 探测")
}
