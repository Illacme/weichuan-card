package refmigrate

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/pii"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeS3 是一个只实现 HEAD/PUT/GET 的最小 S3 端点。
//
// 它**不验签名**：这里要测的是「适配器按什么路径发请求、怎么解释桶方响应」，
// 不是 SigV4。真签名的证据在 tools/backfill-e2e 的 versitygw 灰度里。
// 故意把凭据片段写进错误 XML，用来证明我们的错误文本不会把它带出去。
const leakCanary = "AK-CANARY-MUST-NOT-LEAK"

type fakeS3 struct {
	mu               sync.Mutex
	objects          map[string][]byte
	headStatus       int
	headBody         string
	putStatus        int
	putBody          string
	statSizeOverride *int64
	paths            []string
	// BucketExists 走的是 HEAD /<bucket>（path 风格）或 HEAD /（虚拟主机风格）。
	// 桶级与对象级是两个不同的请求，必须分开模拟，否则「桶不存在」这个
	// 源端独有的兜底根本测不到。
	bucketMissing bool
	bucketStatus  int
	bucketBody    string
	bucketProbes  int
}

// decodeAWSChunked 还原 minio-go 的 SigV4 流式签名载荷。
//
// 实测（本轮调试打印）：PutObject 发的是 Content-Length=193、
// X-Amz-Decoded-Content-Length=20，body 形如
//
//	"14;chunk-signature=<hex>\r\n<20 字节>\r\n0;chunk-signature=<hex>\r\n\r\n"
//
// 真 S3 服务端会解码；假端点若不解码，HEAD 就会回 193 字节，
// 于是「写后回读 size 校验」会红——而红的其实是测试装置，不是被测代码。
func decodeAWSChunked(b []byte) ([]byte, error) {
	var out bytes.Buffer
	for {
		i := bytes.Index(b, []byte("\r\n"))
		if i < 0 {
			return nil, fmt.Errorf("aws-chunked 缺少 chunk 头")
		}
		line := string(b[:i])
		b = b[i+2:]
		n, err := strconv.ParseInt(strings.SplitN(line, ";", 2)[0], 16, 64)
		if err != nil {
			return nil, fmt.Errorf("aws-chunked 长度 %q 非法：%w", line, err)
		}
		if n == 0 {
			return out.Bytes(), nil
		}
		if int64(len(b)) < n+2 {
			return nil, fmt.Errorf("aws-chunked 数据不足")
		}
		out.Write(b[:n])
		b = b[n+2:]
	}
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

const fakeBucket = "wc-t7"

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	key := strings.TrimPrefix(r.URL.Path, "/"+fakeBucket+"/")
	status, body := f.headStatus, f.headBody
	putStatus, putBody := f.putStatus, f.putBody
	obj, exists := f.objects[key]
	override := f.statSizeOverride
	f.mu.Unlock()

	// 桶级 HEAD 没有对象键：minio-go 的 BucketExists 用它判「桶在不在」。
	// path 风格是 /<bucket> 或 /<bucket>/，虚拟主机风格是 /。
	isBucketHead := r.URL.Path == "/"+fakeBucket ||
		r.URL.Path == "/"+fakeBucket+"/" || r.URL.Path == "/"

	switch r.Method {
	case http.MethodHead:
		if isBucketHead {
			f.mu.Lock()
			f.bucketProbes++
			f.mu.Unlock()
			// 桶不存在按 S3 的真实形态回 404 且**不带 body**：minio-go 会把
			// objectName=="" 的 404 归一成 NoSuchBucket → (false, nil)。
			if f.bucketMissing {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if f.bucketStatus != 0 {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(f.bucketStatus)
				_, _ = io.WriteString(w, f.bucketBody)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if status != 0 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound) // 有些实现只给 404，不给 XML 错误体
			return
		}
		size := int64(len(obj))
		if override != nil {
			size = *override
		}
		w.Header().Set("ETag", `"fake-etag"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.WriteHeader(http.StatusOK)
	case http.MethodPut:
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if putStatus != 0 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(putStatus)
			_, _ = io.WriteString(w, putBody)
			return
		}
		if r.Header.Get("X-Amz-Decoded-Content-Length") != "" {
			raw, err = decodeAWSChunked(raw)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		f.mu.Lock()
		f.objects[key] = raw
		f.mu.Unlock()
		w.Header().Set("ETag", `"fake-etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", `"fake-etag"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(obj)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj)
	default:
		w.WriteHeader(http.StatusOK) // GetBucketLocation 之类的探测，不是本测试关注点
	}
}

func (f *fakeS3) sawPath(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

func (f *fakeS3) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.objects[key]
	return append([]byte(nil), v...), ok
}

func (f *fakeS3) probeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bucketProbes
}

// testS3Store 起一个 path-style 假桶并返回对应适配器。
// Region 显式给 us-east-1，避免 minio-go 先发 GetBucketLocation 探测。
func testS3Store(t *testing.T, f *fakeS3) (*S3Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	st, err := NewS3Store(S3StoreConfig{
		CompanyID: 7,
		Driver:    "minio",
		Endpoint:  strings.TrimPrefix(srv.URL, "http://"),
		Bucket:    fakeBucket,
		Region:    "us-east-1",
		AccessKey: "AK-TEST",
		SecretKey: "SK-TEST",
	})
	require.NoError(t, err)
	return st, srv
}

func TestS3StorePutWritesObjectAtTenantKey(t *testing.T) {
	f := newFakeS3()
	st, _ := testS3Store(t, f)
	key := "t/7/shared-logo.png"
	payload := []byte("0123456789abcdefghij") // 20 字节

	info, err := st.Stat(key)
	require.NoError(t, err)
	require.False(t, info.Exists, "写之前对象必须不存在")

	require.NoError(t, st.Put(key, bytes.NewReader(payload), int64(len(payload)), "image/png"))

	got, ok := f.object(key)
	require.True(t, ok, "对象必须真的落在 t/7/ 这个租户键下")
	require.Equal(t, payload, got)
	info, err = st.Stat(key)
	require.NoError(t, err)
	require.True(t, info.Exists)
	require.Equal(t, int64(20), info.Size)
	require.True(t, f.sawPath("/"+fakeBucket+"/"+key),
		"路径风格下请求 path 必须是 /<bucket>/<key>，不能把桶名塞进 Host")
}

func TestS3StoreOpenReadsObjectBytes(t *testing.T) {
	f := newFakeS3()
	st, _ := testS3Store(t, f)
	key := "t/7/a.bin"
	require.NoError(t, st.Put(key, bytes.NewReader([]byte("hello")), 5, "application/octet-stream"))
	rc, err := st.Open(key)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), got)
}

func TestS3StoreStatDistinguishesMissingFromDenied(t *testing.T) {
	t.Run("404 无错误体等于对象不存在", func(t *testing.T) {
		f := newFakeS3()
		st, _ := testS3Store(t, f)
		info, err := st.Stat("t/7/none.png")
		require.NoError(t, err)
		require.False(t, info.Exists)
	})
	t.Run("403 AccessDenied 绝不能被当成不存在", func(t *testing.T) {
		f := newFakeS3()
		f.headStatus = http.StatusForbidden
		f.headBody = `<Error><Code>AccessDenied</Code><Message>` + leakCanary +
			`</Message><RequestId>x</RequestId></Error>`
		st, _ := testS3Store(t, f)
		_, err := st.Stat("t/7/a.png")
		require.Error(t, err)
		require.Contains(t, err.Error(), "AccessDenied")
		require.Contains(t, err.Error(), "403")
		require.NotContains(t, err.Error(), leakCanary, "桶方 Message 不能进错误文本")
		require.NotContains(t, err.Error(), "AK-TEST")
		require.NotContains(t, err.Error(), "SK-TEST")
	})
	t.Run("404 一律视为对象不存在，但桶不存在时 Put 必须显式失败", func(t *testing.T) {
		// HEAD 响应按 HTTP 规范不带 body，所以 Stat 分不出「对象不存在」与
		// 「桶不存在」。协议限制不能靠猜来绕，兜底是 Put 的显式错误码 + 写后回读。
		f := newFakeS3()
		f.headStatus = http.StatusNotFound
		f.putStatus = http.StatusNotFound
		f.putBody = `<Error><Code>NoSuchBucket</Code><Message>` + leakCanary +
			`</Message><RequestId>x</RequestId></Error>`
		st, _ := testS3Store(t, f)
		info, err := st.Stat("t/7/a.png")
		require.NoError(t, err)
		require.False(t, info.Exists)
		err = st.Put("t/7/a.png", bytes.NewReader([]byte("x")), 1, "image/png")
		require.Error(t, err, "桶不存在绝不能变成搬迁成功")
		require.Contains(t, err.Error(), "NoSuchBucket")
		require.NotContains(t, err.Error(), leakCanary)
	})
}

func TestS3StorePutVerifiesPostWriteVisibility(t *testing.T) {
	f := newFakeS3()
	wrong := int64(5)
	f.statSizeOverride = &wrong
	st, _ := testS3Store(t, f)
	err := st.Put("t/7/a.png", bytes.NewReader([]byte("0123456789abcdefghij")), 20, "image/png")
	require.Error(t, err, "写完必须回读校验 size，否则「假成功」没有任何人报错")
	require.Contains(t, err.Error(), "大小不符")
}

func TestS3StoreRejectsUnsafeKeysWithoutSendingRequests(t *testing.T) {
	f := newFakeS3()
	st, _ := testS3Store(t, f)
	bad := []string{"", "/t/7/a.png", "t/7/../x.png", "t/7//x.png", `t\7\a.png`, "t/7/x\x00.png"}
	for _, key := range bad {
		_, err := st.Stat(key)
		require.Error(t, err, "%q 必须在发出请求前被挡住", key)
		_, err = st.Open(key)
		require.Error(t, err, "%q", key)
		err = st.Put(key, strings.NewReader("x"), 1, "")
		require.Error(t, err, "%q", key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.paths, "非法键不该产生任何 S3 请求")
}

func TestNewS3StoreRejectsUnusableConfig(t *testing.T) {
	base := S3StoreConfig{
		CompanyID: 7, Driver: "minio", Endpoint: "127.0.0.1:19000",
		Bucket: "wc-t7", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk",
	}
	cases := []struct {
		name   string
		mutate func(*S3StoreConfig)
	}{
		{"company_id=0", func(c *S3StoreConfig) { c.CompanyID = 0 }},
		{"driver=local", func(c *S3StoreConfig) { c.Driver = "local" }},
		{"driver=oss 本轮不支持", func(c *S3StoreConfig) { c.Driver = "oss" }},
		{"driver 不在枚举", func(c *S3StoreConfig) { c.Driver = "ftp" }},
		{"凭据缺一", func(c *S3StoreConfig) { c.SecretKey = "" }},
		{"hostHeader 判断不出风格", func(c *S3StoreConfig) { c.HostHeader = "cdn.other.example.com" }},
		{"虚拟主机风格但域名与 endpoint 不一致", func(c *S3StoreConfig) {
			c.Endpoint = "127.0.0.1:19000"
			c.HostHeader = "wc-t7.s3.example.com"
		}},
		{"endpoint 为空", func(c *S3StoreConfig) { c.Endpoint = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			c.mutate(&cfg)
			_, err := NewS3Store(cfg)
			require.Error(t, err)
		})
	}
}

func TestNewS3StoreAcceptsVirtualHostMatchingEndpoint(t *testing.T) {
	_, err := NewS3Store(S3StoreConfig{
		CompanyID: 7, Driver: "s3", Endpoint: "s3.example.com",
		HostHeader: "wc-t7.s3.example.com", Bucket: "wc-t7",
		Region: "ap-guangzhou", UseSSL: true, AccessKey: "ak", SecretKey: "sk",
	})
	require.NoError(t, err)
}

// ---- 按 company_id 解密组装 ------------------------------------------------

func newTenantDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := t.TempDir() + "/tenant.db"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))
	return db
}

func seedTenant(t *testing.T, db *gorm.DB, cid int64, driver string, enabled bool, badCipher bool) {
	t.Helper()
	ak, err := pii.Encrypt("AK-TEST")
	require.NoError(t, err)
	sk, err := pii.Encrypt("SK-TEST")
	require.NoError(t, err)
	if badCipher {
		ak = "v1:this-is-not-valid-ciphertext"
	}
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: cid, Driver: driver, Endpoint: "127.0.0.1:19000",
		Bucket: "wc-t7", Region: "us-east-1",
		AccessKeyEnc: ak, SecretKeyEnc: sk, Enabled: enabled,
	}).Error)
}

func TestTenantTargetForRefusesAnythingButARealBucket(t *testing.T) {
	cases := []struct {
		name      string
		seed      bool
		driver    string
		enabled   bool
		badCipher bool
		wantErr   string
	}{
		{"没有配置行", false, "minio", true, false, "没有生效的自有桶配置"},
		{"配置未启用", true, "minio", false, false, "没有生效的自有桶配置"},
		{"driver=local 明确走平台", true, "local", true, false, "没有生效的自有桶配置"},
		{"凭据解密失败", true, "minio", true, true, "读取失败"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newTenantDB(t)
			if c.seed {
				seedTenant(t, db, 7, c.driver, c.enabled, c.badCipher)
			}
			target := TenantTargetFor(db, "")
			st, err := target(7)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.wantErr)
			require.Contains(t, err.Error(), "不回退平台盘")
			require.Nil(t, st, "失败时绝不返回任何可用存储，调用方没有机会误用")
		})
	}
}

func TestTenantTargetForBuildsS3StoreNotLocalStore(t *testing.T) {
	db := newTenantDB(t)
	seedTenant(t, db, 7, "minio", true, false)
	st, err := TenantTargetFor(db, "")(7)
	require.NoError(t, err)
	require.IsType(t, &S3Store{}, st, "真桶模式绝不能悄悄拿到本地盘适配器")
	require.Equal(t, "wc-t7", st.(*S3Store).bucket)
	_, err = TenantTargetFor(db, "")(0)
	require.Error(t, err)
}
