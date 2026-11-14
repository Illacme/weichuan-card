package refmigrate

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 第六十七轮「平台自有 S3 桶当源」的守门单测。
//
// 这轮最容易被「看起来测过了」掩盖的，是三件事：
//   - 源端只读（不能因为 ObjectStore 接口有 Put 就顺手委托给内部 S3Store）；
//   - 桶不存在必须报桶，不能退化成「源对象不存在」；
//   - 源键是历史相对键，绝不能加 p/ 或 t/<cid>/ 前缀。
// 三条都用「换回最偷懒但合理的实现会不会变红」来验，见各用例注释。

func testPlatformSource(t *testing.T, f *fakeS3) *PlatformS3Source {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	st, err := NewPlatformS3Source(PlatformS3Config{
		Driver:    "minio",
		Endpoint:  strings.TrimPrefix(srv.URL, "http://"),
		Bucket:    fakeBucket,
		Region:    "us-east-1", // 显式给区域，避免 GetBucketLocation 探测干扰断言
		AccessKey: "AK-TEST",
		SecretKey: "SK-TEST",
	})
	require.NoError(t, err)
	return st
}

func TestNewPlatformS3SourceRejectsUnusableConfig(t *testing.T) {
	base := PlatformS3Config{
		Driver: "minio", Endpoint: "127.0.0.1:19000", Bucket: "wc-platform",
		Region: "us-east-1", AccessKey: "AK-TEST", SecretKey: "SK-TEST",
	}
	cases := []struct {
		name, want string
		mutate     func(*PlatformS3Config)
	}{
		{"driver=local 不该有 S3 源", "本地盘", func(c *PlatformS3Config) { c.Driver = "local" }},
		{"driver 未配置", "未配置", func(c *PlatformS3Config) { c.Driver = "" }},
		{"driver=oss 需另接 SDK", "oss", func(c *PlatformS3Config) { c.Driver = "oss" }},
		{"未知 driver", "不在枚举内", func(c *PlatformS3Config) { c.Driver = "swift" }},
		{"缺 endpoint", "MINIO_ENDPOINT", func(c *PlatformS3Config) { c.Endpoint = "" }},
		{"缺 bucket", "MINIO_BUCKET", func(c *PlatformS3Config) { c.Bucket = "" }},
		{"缺 access key", "MINIO_ACCESS_KEY", func(c *PlatformS3Config) { c.AccessKey = "" }},
		{"缺 secret key", "MINIO_SECRET_KEY", func(c *PlatformS3Config) { c.SecretKey = "" }},
		{"credential 只有空白", "MINIO_SECRET_KEY", func(c *PlatformS3Config) { c.SecretKey = "   " }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			c.mutate(&cfg)
			st, err := NewPlatformS3Source(cfg)
			require.Error(t, err, "配置不可用时必须显式拒绝，绝不构造一个会读到空桶的源")
			require.Nil(t, st)
			require.Contains(t, err.Error(), c.want)
			require.Contains(t, err.Error(), "不回退本地盘")
		})
	}
	// 反向验证锚点：把上面任意一条 mutate 去掉，对应子例必须变红。
}

func TestNewPlatformS3SourceAcceptsBothS3Drivers(t *testing.T) {
	for _, drv := range []string{"minio", "s3"} {
		st, err := NewPlatformS3Source(PlatformS3Config{
			Driver: drv, Endpoint: "127.0.0.1:19000", Bucket: "wc-platform",
			Region: "us-east-1", AccessKey: "AK", SecretKey: "SK",
		})
		require.NoError(t, err)
		require.NotNil(t, st)
	}
}

// TestPlatformS3SourceIsReadOnly 测的是「回填绝不改动源桶」这个承诺本身。
// 只断言 Put 返回错误还不够（一个空实现也能过），所以同时断言：
// ① 假端点没收到任何 PUT；② 源桶里的对象字节没变；③ 真的经 Stores.Copy
//
//	搬到另一个目标时，源桶仍原样、目标文件字节正确。
func TestPlatformS3SourceIsReadOnly(t *testing.T) {
	f := newFakeS3()
	f.objects["shared-logo.png"] = []byte("0123456789abcdefghij")
	st := testPlatformSource(t, f)

	err := st.Put("shared-logo.png", bytes.NewReader([]byte("OVERWRITE")), 9, "image/png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "只读")
	require.False(t, f.sawPath("PUT "), "源端绝不发出任何 PUT 请求")
	got, ok := f.object("shared-logo.png")
	require.True(t, ok)
	require.Equal(t, []byte("0123456789abcdefghij"), got, "源桶对象必须原样")

	// 真正的搬迁路径：Source 是平台桶，Target 是一个临时本地目录。
	dst, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	stores := &Stores{Source: st, TargetFor: func(int64) (ObjectStore, error) { return dst, nil }}
	require.NoError(t, stores.Copy("shared-logo.png", "t/7/shared-logo.png", 7))
	rc, err := dst.Open("t/7/shared-logo.png")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	copied, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("0123456789abcdefghij"), copied)
	require.False(t, f.sawPath("PUT "), "搬到目标也不许反过来写源桶")
}

// TestPlatformS3SourceReadsHistoricalKeyWithoutPrefix 钉住键的口径。
// SourceKey 是 legacyRel 推出的历史相对键；若有人「顺手」用 ObjectKeyFor
// 补平台前缀，桶里全部 404，而这个错误看上去像「存量对象丢了」。
func TestPlatformS3SourceReadsHistoricalKeyWithoutPrefix(t *testing.T) {
	f := newFakeS3()
	payload := []byte("legacy-platform-object")
	f.objects["shared-logo.png"] = payload
	st := testPlatformSource(t, f)

	info, err := st.Stat("shared-logo.png")
	require.NoError(t, err)
	require.True(t, info.Exists)
	require.Equal(t, int64(len(payload)), info.Size)
	rc, err := st.Open("shared-logo.png")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	require.True(t, f.sawPath("/"+fakeBucket+"/shared-logo.png"), "请求路径必须是 /<bucket>/<历史键>")
	require.False(t, f.sawPath("/"+fakeBucket+"/p/shared-logo.png"), "不许给源键加 p/ 前缀")
}

// TestPlatformS3SourceReportsMissingBucketNotMissingObject 是本轮最关键的一条。
// HEAD 响应按 HTTP 规范不带 body，Stat 的 404 分不出「对象没有」与「桶没有」；
// 第六十六轮目标端靠 Put 的错误码兜底，源端没有 Put。把 ensureBucket 删掉，
// 这条会拿到 Exists=false / err=nil —— 正是「换回坏实现必红」的反向验证。
func TestPlatformS3SourceReportsMissingBucketNotMissingObject(t *testing.T) {
	f := newFakeS3()
	f.bucketMissing = true
	st := testPlatformSource(t, f)

	info, err := st.Stat("shared-logo.png")
	require.Error(t, err, "桶不存在时必须报错，不能当成「源对象不存在」")
	require.False(t, info.Exists)
	require.Contains(t, err.Error(), "平台源桶")
	require.Contains(t, err.Error(), "不存在")
	require.Contains(t, err.Error(), "MINIO_BUCKET")
	require.Equal(t, 1, f.probeCount(), "探测一次就够")

	_, err = st.Open("shared-logo.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "平台源桶")
}

func TestPlatformS3SourceDistinguishesMissingObjectFromMissingBucket(t *testing.T) {
	f := newFakeS3() // 桶在，对象不在
	st := testPlatformSource(t, f)
	info, err := st.Stat("gone.png")
	require.NoError(t, err, "桶在时，404 就是「源对象不存在」，交给上层按数据问题处理")
	require.False(t, info.Exists)
}

func TestPlatformS3SourceBucketProbeIsOnceAndDeniedIsNotSwallowed(t *testing.T) {
	t.Run("桶探测只做一次", func(t *testing.T) {
		f := newFakeS3()
		f.objects["a.png"] = []byte("a")
		st := testPlatformSource(t, f)
		for i := 0; i < 3; i++ {
			_, err := st.Stat("a.png")
			require.NoError(t, err)
		}
		_, err := st.Open("a.png")
		require.NoError(t, err)
		require.Equal(t, 1, f.probeCount(), "sync.Once：不逐对象重复发 HEAD /<bucket>")
	})
	t.Run("凭据错绝不吞成桶不存在", func(t *testing.T) {
		f := newFakeS3()
		f.bucketStatus = http.StatusForbidden
		f.bucketBody = `<Error><Code>AccessDenied</Code><Message>` + leakCanary +
			`</Message><RequestId>x</RequestId></Error>`
		st := testPlatformSource(t, f)
		_, err := st.Stat("a.png")
		require.Error(t, err)
		require.Contains(t, err.Error(), "平台源桶")
		require.Contains(t, err.Error(), "403")
		require.NotContains(t, err.Error(), leakCanary, "桶方 Message 不能进错误文本")
		require.NotContains(t, err.Error(), "AK-TEST")
		require.NotContains(t, err.Error(), "SK-TEST")
	})
}

// TestPlatformS3SourceRejectsUnsafeKeysWithoutRequests 与本地盘 / 目标桶共用
// validateObjectKey：源键同样来自计划文件，不能只在目标侧挡穿越。
func TestPlatformS3SourceRejectsUnsafeKeysWithoutRequests(t *testing.T) {
	f := newFakeS3()
	st := testPlatformSource(t, f)
	for _, key := range []string{"", "/etc/passwd", "../secret.png", "a/../b.png", "a//b.png", "a\\b.png", "a\x00b.png"} {
		_, err := st.Stat(key)
		require.Error(t, err, "键 %q 必须在发请求前被挡住", key)
	}
}

// TestPlatformSourceUsesUploaderConstructor 是源码级守门：平台源必须复用
// 上传侧同一个客户端构造函数（省略 lookup 变参 = BucketLookupAuto）。
// 自己 minio.New 或调 S3BucketLookup 都会让「写进去的桶」与「读出来的桶」
// 分叉，而两边日志都不报错——第六十三轮真桶实测正是这类问题。
//
// 抓取型守门自带兜底：至少要求文件里有 3 个 func 声明且能在 2 个文件里
// 抓到锚点，否则「文件改名 / 正则失效 → 一条没抓到 → 全部通过」。
func TestPlatformSourceUsesUploaderConstructor(t *testing.T) {
	raw, err := os.ReadFile("platform_source.go")
	require.NoError(t, err, "读不到源文件时守门必须失败，不能静默放行")
	code := stripLineComments(string(raw))
	require.GreaterOrEqual(t, strings.Count(code, "func "), 3, "抓到的函数太少，守门可能已失效")
	// 「至少抓到 N 个」兜底：这些锚点必须真的出现在代码里，否则说明文件被
	// 改名 / 重写，上面的 NotContains 会一条也抓不到、于是「全部通过」。
	anchors := []string{
		"func NewPlatformS3Source(", "func (s *PlatformS3Source) Stat(",
		"func (s *PlatformS3Source) Open(", "func (s *PlatformS3Source) Put(",
		"func (s *PlatformS3Source) ensureBucket(",
	}
	found := 0
	for _, a := range anchors {
		if strings.Contains(code, a) {
			found++
		}
	}
	require.GreaterOrEqual(t, found, 5, "只抓到 %d 个函数锚点，守门可能已失效", found)
	require.Contains(t, code, "storage.NewMinioUploader(",
		"平台源必须复用上传侧客户端构造，保证路径风格同源")
	require.Contains(t, code, "NewMinioUploader(cfg.Endpoint", "必须传平台自己的配置")
	require.NotContains(t, code, "minio.New(", "不许自己建客户端（会丢掉 Auto 契约）")
	require.NotContains(t, code, "BucketLookupAuto", "不许在源侧另写路径风格")
	require.NotContains(t, code, "S3BucketLookup", "那是租户语义，平台没有 host_header")
	require.NotContains(t, code, "ObjectKeyFor(", "源键是历史相对键，不许重写前缀")
	require.Contains(t, code, "BucketExists(", "源端没有 Put 兜底，必须探桶")
	require.Contains(t, code, "拒绝写入", "Put 必须显式拒绝")
}

// stripLineComments 去掉整行注释。扫代码别扫注释——文件头注释里写了
// 「不许自己 minio.New」，朴素 Contains 会把它判成违规（第六十六轮踩过）。
func stripLineComments(src string) string {
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
