package uploadroute

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testPlatform(t *testing.T) *storage.LocalUploader {
	t.Helper()
	return &storage.LocalUploader{Dir: t.TempDir()}
}

func minioCfg(version int64) *Config {
	return &Config{
		CompanyID: 7,
		Driver:    "minio",
		Endpoint:  "127.0.0.1:9000",
		Bucket:    "cust-bucket",
		UseSSL:    false,
		AccessKey: "AK",
		SecretKey: "SK",
		BaseURL:   "https://card.example.com/uploads",
		// 第一阶段口径就是公开读桶。夹具若留 false，build 会按「私有桶」显式拒绝，
		// 后面所有「正常配置」用例都会红——那不是回归，是夹具没跟上契约。
		PublicRead: true,
		Version:    version,
	}
}

// TestForNoConfigUsesPlatform 钉住「没配就是走平台」不是降级，而是明确配置。
// 若把它当错误，所有没配自有桶的租户上传会整体失败。
func TestForNoConfigUsesPlatform(t *testing.T) {
	plat := testPlatform(t)
	res := New(plat, "local", func(int64) (*Config, error) { return nil, nil })

	for _, cid := range []int64{-5, 0, 1, 12345} {
		up, err := res.For(cid)
		require.NoError(t, err)
		require.Same(t, plat, up, "cid=%d 未配置时必须用平台实例", cid)
		require.Equal(t, "local", res.DriverFor(cid))
	}
}

// TestForLoadErrorNeverFallsBack 是本包最重要的一条。
//
// 读库失败（表不存在 / 库抖动 / 解密失败）时**不能**换成平台实例顶上：
// 客户以为文件在自己桶里，实际躺在平台盘上，事发前没有任何人会发现。
// 所以这里断言两件事——返回错误，且返回的实例是 nil。
func TestForLoadErrorNeverFallsBack(t *testing.T) {
	plat := testPlatform(t)
	boom := errors.New("tenant_storage_configs 表不存在")
	res := New(plat, "local", func(int64) (*Config, error) { return nil, boom })

	up, err := res.For(7)
	require.Error(t, err)
	require.ErrorIs(t, err, boom, "底层原因必须能被 errors.Is 到，运维才分得清是库问题还是配置问题")
	require.Nil(t, up, "读配置失败时绝不能退回平台实例")
	require.Contains(t, err.Error(), "企业 7")
}

// TestForBrokenTenantConfigFailsLoudly 覆盖「配置行在、但构造不出来」的三种形态：
// 缺凭据、缺基址、驱动不在枚举内。三者的共同要求是报错且**声明没有回退**——
// 否则用户只会以为网络问题，反复重试。
func TestForBrokenTenantConfigFailsLoudly(t *testing.T) {
	cases := []struct {
		name string
		mu   func(*Config)
		want string
	}{
		{"缺 SecretKey", func(c *Config) { c.SecretKey = "" }, "凭据不完整"},
		{"缺 BaseURL", func(c *Config) { c.BaseURL = "" }, "公开访问基址为空"},
		{"驱动不在枚举", func(c *Config) { c.Driver = "cos" }, "不在枚举内"},
		{"缺 bucket", func(c *Config) { c.Bucket = "" }, "要求 endpoint 与 bucket 都非空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minioCfg(1)
			tc.mu(cfg)
			res := New(testPlatform(t), "local", func(int64) (*Config, error) { return cfg, nil })

			up, err := res.For(7)
			require.Error(t, err)
			require.Nil(t, up)
			require.Contains(t, err.Error(), tc.want)
			require.Contains(t, err.Error(), "不回退平台存储", "报错必须写明没有退回平台存储")
		})
	}
}

// TestForRoutesToTenantBucket 证明「配好了就真的给的是租户实例」。
// 用 *storage.MinioUploader 判类型而不是判字段：只要构造成功，类型就变了，
// 这正是「上传会写进客户桶」的最小可观测证据。
func TestForRoutesToTenantBucket(t *testing.T) {
	plat := testPlatform(t)
	cfg := minioCfg(1)
	res := New(plat, "local", func(int64) (*Config, error) { return cfg, nil })

	up, err := res.For(7)
	require.NoError(t, err)
	require.IsType(t, &storage.MinioUploader{}, up)
	require.NotSame(t, plat, up, "配了自有桶就不能再用平台实例")
	require.Equal(t, "minio", res.DriverFor(7))
	// 平台自身（cid<=0）不受租户路由影响。
	platUp, err := res.For(0)
	require.NoError(t, err)
	require.Same(t, plat, platUp)
}

// TestForCachesByUpdatedAt 钉住缓存与失效：
//
//   - 同一个 version 连续问两次，必须拿到**同一个指针**——否则每次上传都重建
//     S3 客户端（等于给每个请求加一次 TLS 握手）；
//   - version 一变（第六十轮的 PUT 走 gorm Updates，会自动刷新 updated_at），
//     必须换成新实例——否则超管改完桶地址，租户还得重启服务才生效，
//     而界面已经显示"已保存"。
func TestForCachesByUpdatedAt(t *testing.T) {
	plat := testPlatform(t)
	version := int64(100)
	res := New(plat, "local", func(int64) (*Config, error) { return minioCfg(version), nil })

	first, err := res.For(7)
	require.NoError(t, err)
	second, err := res.For(7)
	require.NoError(t, err)
	require.Same(t, first, second, "同一版本必须命中缓存")

	version = 101
	third, err := res.For(7)
	require.NoError(t, err)
	require.NotSame(t, first, third, "updated_at 变了必须重建实例")
	require.IsType(t, &storage.MinioUploader{}, third)
}

// TestRouteEnabledOnlyOnExplicitValues 钉住开关的默认侧别。
//
// 拼错 / 留空 / 写成别的值都必须落到 off。反过来（拼错即打开）会让一次手误把
// 线上上传整体切到客户桶，而这件事在日志里只有一行"已启用"。
func TestRouteEnabledOnlyOnExplicitValues(t *testing.T) {
	for _, v := range []string{"on", "ON", " On ", "1", "true", "TRUE", "yes"} {
		t.Run("开:"+v, func(t *testing.T) {
			t.Setenv("TENANT_STORAGE_ROUTE", v)
			require.True(t, RouteEnabled())
		})
	}
	for _, v := range []string{"", "off", "0", "false", "no", "tru", "onn", "   "} {
		t.Run("关:"+v, func(t *testing.T) {
			t.Setenv("TENANT_STORAGE_ROUTE", v)
			require.False(t, RouteEnabled())
		})
	}
}

// ---- DBLoader：三个分支都要能被区分开 ----

func newRouteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "route.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))
	return db
}

func mustEncrypt(t *testing.T, plain string) string {
	t.Helper()
	enc, err := pii.Encrypt(plain)
	require.NoError(t, err)
	return enc
}

func seedRouteRow(t *testing.T, db *gorm.DB, row model.TenantStorageConfig) {
	t.Helper()
	if row.AccessKeyEnc == "" {
		row.AccessKeyEnc = mustEncrypt(t, "AK-PLAIN")
	}
	if row.SecretKeyEnc == "" {
		row.SecretKeyEnc = mustEncrypt(t, "SK-PLAIN")
	}
	require.NoError(t, db.Create(&row).Error)
}

// TestDBLoaderSkipsRowsThatDontNeedSnippet 钉住与 nginx 侧同一个判定：
// enabled=false 与 driver=local 都返回 (nil, nil) —— 明确走平台。
//
// 若这里改成"只要行存在就用自有桶"，nginx 不会给这两种行生成回源段，
// 结果是上传写进客户桶、取图却走平台回退段 → 全站 404，而后台配置显示正常。
func TestDBLoaderSkipsRowsThatDontNeedSnippet(t *testing.T) {
	db := newRouteDB(t)
	load := DBLoader(db, "https://plat.example.com/uploads")

	seedRouteRow(t, db, model.TenantStorageConfig{
		CompanyID: 1, Driver: "minio", Endpoint: "127.0.0.1:9000", Bucket: "b", Enabled: false,
	})
	seedRouteRow(t, db, model.TenantStorageConfig{
		CompanyID: 2, Driver: "local", Endpoint: "", Bucket: "", Enabled: true,
	})

	for _, cid := range []int64{1, 2, 3} { // 3 = 压根没有行
		cfg, err := load(cid)
		require.NoError(t, err, "cid=%d 不该报错（这是明确配置，不是失败）", cid)
		require.Nil(t, cfg, "cid=%d 应判定为走平台实例", cid)
	}
}

// TestDBLoaderDecryptsAndDefaultsBaseURL 覆盖正常路径：
// 凭据必须解密成明文（S3 签名要真凭据）。第六十八轮订正：BaseURL **恒**为平台
// 统一入口，不再「在 public_base_url 为空时退到」它——自有域名是访客侧 origin，
// 只登记进 Config.PublicOrigin，不能覆盖上传返回基址（设计稿 §3.4）。
func TestDBLoaderDecryptsAndDefaultsBaseURL(t *testing.T) {
	db := newRouteDB(t)
	load := DBLoader(db, "https://plat.example.com")

	seedRouteRow(t, db, model.TenantStorageConfig{
		CompanyID: 7, Driver: "s3", Endpoint: "s3.example.com", Bucket: "cust",
		Region: "ap-guangzhou", UseSSL: true, Enabled: true, PublicRead: true,
	})

	cfg, err := load(7)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "AK-PLAIN", cfg.AccessKey)
	require.Equal(t, "SK-PLAIN", cfg.SecretKey)
	require.Equal(t, "https://plat.example.com", cfg.BaseURL)
	require.Equal(t, "ap-guangzhou", cfg.Region)
	require.True(t, cfg.UseSSL)
	require.NotZero(t, cfg.Version, "缓存版本号必须有值，否则每次都会重建实例")
}

// TestDBLoaderPublicBaseURLIsOriginNotUploadBase 是第六十八轮修正的架构错接：
// 客户自有域名是**访客侧 origin**，不是「上传接口返回的 URL 基址」。
//
// 旧实现把它覆盖到 Config.BaseURL，上传于是返回 `https://img.cust.com/t/9/x.jpg`；
// 写入侧 normalizeResourceRef 用平台 cfg.UploadBaseURL 剥不掉、URL 里也没有
// /uploads/ 段，NormalizeOrKeep 会把整条当外链**原样写库**——库里从裸键退回
// 绝对 URL，media_check 反推键失败。这条用例同时钉住「登记到了」与「没串位」。
func TestDBLoaderPublicBaseURLIsOriginNotUploadBase(t *testing.T) {
	db := newRouteDB(t)
	load := DBLoader(db, "https://plat.example.com/uploads")

	seedRouteRow(t, db, model.TenantStorageConfig{
		CompanyID: 9, Driver: "oss", Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "cust",
		Enabled: true, PublicRead: true, PublicBaseURL: "https://img.cust.com",
	})

	cfg, err := load(9)
	require.NoError(t, err)
	require.Equal(t, "https://img.cust.com", cfg.PublicOrigin, "自有域名必须被登记")
	require.Equal(t, "https://plat.example.com/uploads", cfg.BaseURL,
		"自有域名绝不能覆盖上传返回基址：写入侧靠这个基址把 URL 剥回裸键")
	require.Equal(t, "oss-cn-hangzhou.aliyuncs.com", cfg.Endpoint,
		"回源侧不受自有域名影响，两者正交")
}

// TestBuildRejectsPrivateBucket 钉住 public_read 的运行时消费。
//
// 在此之前这一列只有写入侧 validateTenantStorageRow 会看，库里若被人手工改成 0，
// 上传链路照样按公开读拼 URL、零报错。现在 build 必须显式拒绝，
// 且 Resolver.For 要带上「不回退平台存储」的载荷。
func TestBuildRejectsPrivateBucket(t *testing.T) {
	cfg := minioCfg(1)
	cfg.PublicRead = false
	res := New(testPlatform(t), "local", func(int64) (*Config, error) { return cfg, nil })

	up, err := res.For(7)
	require.Error(t, err)
	require.Nil(t, up)
	require.Contains(t, err.Error(), "公开读")
	require.Contains(t, err.Error(), "不回退平台存储")
}

// TestBuildRejectsUnvalidatedPublicOrigin 兜住「第六十八轮之前手工写进库的坏域名」：
// 写入侧已经校验过新值，但存量行没被洗过，翻开关那天必须在构造实例时报错。
func TestBuildRejectsUnvalidatedPublicOrigin(t *testing.T) {
	cfg := minioCfg(1)
	cfg.PublicOrigin = "https://img.cust.com/prefix"
	res := New(testPlatform(t), "local", func(int64) (*Config, error) { return cfg, nil })

	up, err := res.For(7)
	require.Error(t, err)
	require.Nil(t, up)
	require.Contains(t, err.Error(), "自有访问域名不合法")
	// 合法值必须能过，否则这个守门可能只是「一律报错」的假绿。
	cfg.PublicOrigin = "https://img.cust.com"
	up2, err2 := build(cfg)
	require.NoError(t, err2)
	require.NotNil(t, up2)
}

// TestDBLoaderDecryptErrorIsNotSwallowed 是「无静默降级」在数据层的落点：
// 换过 PII_ENC_KEY 之后密文解不开，必须报错。吞掉它就只能落平台盘。
func TestDBLoaderDecryptErrorIsNotSwallowed(t *testing.T) {
	db := newRouteDB(t)
	load := DBLoader(db, "https://plat.example.com")

	good := mustEncrypt(t, "AK-PLAIN")
	// 截掉尾部 8 个字符：仍是 "v1:" 前缀，但 GCM 校验必然失败，
	// 模拟的是「密钥换了」而不是「字段为空」。
	seedRouteRow(t, db, model.TenantStorageConfig{
		CompanyID: 11, Driver: "minio", Endpoint: "127.0.0.1:9000", Bucket: "b",
		Enabled: true, AccessKeyEnc: good[:len(good)-8],
	})

	cfg, err := load(11)
	require.Error(t, err)
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "解密失败")
}

// TestVerifyRequiresMigrationTable 钉住启动自检：表不存在要报错，且提示可照做。
// 路由打开后每个上传都要读这张表，缺表 = 所有租户的上传同时不可用。
func TestVerifyRequiresMigrationTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{})
	require.NoError(t, err)

	err = Verify(db)
	require.Error(t, err)
	require.Contains(t, err.Error(), "./server migrate")
	require.True(t, strings.Contains(err.Error(), "0044"), "提示里要点名迁移编号，否则不知道该补哪个文件")

	require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))
	require.NoError(t, Verify(db))
}
