package storageprofile

// 平台实例热更新守门（A1）：改主存储 / 平台 CDN 的内置配置后，不重启也必须换实例。
//
// 历史实现把平台上传器 / 刷新器在启动期构造一次、作为固定指针注入各 handler，
// 于是凭据/驱动/厂商改完必须重启，与「绑定改完下一个请求生效」形成两套语义。
// 这组用例证明 Platform()/DriverFor()/平台刷新器都按 version 现建，
// 且失败时保留旧实例、p/ 对象永不借用全局绑定。

import (
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/model"
	"weichuan/internal/storage"
)

func TestPlatformUploaderHotReloadOnCredentialChange(t *testing.T) {
	db := testDB(t)
	// OSS 构造只建 SDK 客户端、不发网络请求，且 cid=0 不走 S3BucketLookup（那是 minio/s3 的限制）。
	sp := &model.StorageProfile{
		Name: "主存储", Driver: "oss", Endpoint: "old.example.com", Bucket: "old-bucket",
		BaseURL: "https://old.example.com/uploads", AccessKey: "OLD_AK", SecretKey: "OLD_SK",
		PublicRead: true, UseSSL: true, Status: "ok", Builtin: true,
		Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(sp).Error)

	r := newTestResolver(t, db)
	// 启动期兜底实例（local），只在档案读不到时才用。
	r.SetPlatform(&storage.LocalUploader{Dir: t.TempDir()}, "local")
	r.SetFingerprintTTL(0) // 每次解析都查库，测试不必 sleep

	up1 := r.Platform()
	m1, ok := up1.(*storage.OssUploader)
	require.True(t, ok, "主存储是 oss 时应构造 OssUploader")
	require.Equal(t, "old-bucket", m1.Bucket.BucketName)

	// 改凭据 + 桶 + 基址，version++，保存（不重启）——下一次 Platform() 必须换实例。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"endpoint": "new.example.com", "bucket": "new-bucket", "base_url": "https://new.example.com/uploads",
		"access_key": "NEW_AK", "secret_key": "NEW_SK", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())

	up2 := r.Platform()
	require.NotSame(t, up1, up2, "配置变了必须返回新实例，而不是启动期固定指针")
	m2, ok := up2.(*storage.OssUploader)
	require.True(t, ok)
	require.Equal(t, "new-bucket", m2.Bucket.BucketName, "桶必须跟着变")
	require.Equal(t, "https://new.example.com/uploads", m2.BaseURL, "访问基址必须跟着变")
}

func TestPlatformDriverHotReload(t *testing.T) {
	db := testDB(t)
	sp := &model.StorageProfile{
		Name: "主存储", Driver: "local", BaseURL: "/uploads", PublicRead: true,
		Status: "ok", Builtin: true, Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(sp).Error)

	r := newTestResolver(t, db)
	r.SetPlatform(&storage.LocalUploader{Dir: t.TempDir()}, "local")
	r.SetFingerprintTTL(0)
	require.Equal(t, "local", r.DriverFor(0))

	// 改成 oss 云驱动：DriverFor 必须立即反映新落点，与 Platform() 同一份快照。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"driver": "oss", "endpoint": "oss-cn-hangzhou.aliyuncs.com", "bucket": "valid-bucket", "base_url": "https://x/uploads",
		"access_key": "AK", "secret_key": "SK", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())
	require.Equal(t, "oss", r.DriverFor(0), "换驱动后能力探测必须立即反映新落点")
	_, ok := r.Platform().(*storage.OssUploader)
	require.True(t, ok, "Platform() 必须与 DriverFor 一致地换成新驱动实例")
}

func TestPlatformUploaderKeepsLastGoodOnBadCredentials(t *testing.T) {
	db := testDB(t)
	sp := &model.StorageProfile{
		Name: "主存储", Driver: "oss", Endpoint: "s.example.com", Bucket: "good-bucket",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true,
		UseSSL: true, Status: "ok", Builtin: true,
		Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(sp).Error)

	r := newTestResolver(t, db)
	r.SetPlatform(&storage.LocalUploader{Dir: t.TempDir()}, "local")
	r.SetFingerprintTTL(0)
	good := r.Platform()
	_, ok := good.(*storage.OssUploader)
	require.True(t, ok, "前置条件：初始实例应构造成功")

	// 清空凭据（buildUploader 会拒绝），version++。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"secret_key": "", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())
	require.Same(t, good, r.Platform(), "构造失败时必须保留上一份可用实例，不能让上传整体瘫掉")

	// 修正回去：换一次 version，必须能恢复（改错可自愈）。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"secret_key": "SK", "version": 3,
	}).Error)
	require.NoError(t, r.Touch())
	recovered := r.Platform()
	require.NotSame(t, good, recovered, "修正后必须重建出可用实例")
	m, ok := recovered.(*storage.OssUploader)
	require.True(t, ok)
	require.Equal(t, "good-bucket", m.Bucket.BucketName)
}

// countingRefresher 定义在 dynamic_cdn_guard_test.go（同包），这里直接用。
// 本用例直接检查 PlatformCDNRefresher 的实例切换，不触发真实刷新请求。
func TestPlatformCDNRefresherHotReload(t *testing.T) {
	db := testDB(t)
	dp := &model.DeliveryProfile{
		Name: "平台自身 CDN", Provider: "aliyun", AccessKey: "AK1", SecretKey: "SK1",
		Status: "ok", Builtin: true, Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(dp).Error)

	r := newTestResolver(t, db)
	r.SetFingerprintTTL(0)
	rf1, found := r.PlatformCDNRefresher()
	require.True(t, found, "有平台 CDN 内置配置时应以它为真值源")
	require.NotNil(t, rf1)

	// 换凭据 + 版本：必须现建出新刷新器。
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", dp.ID).Updates(map[string]any{
		"access_key": "AK2", "secret_key": "SK2", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())
	rf2, found := r.PlatformCDNRefresher()
	require.True(t, found)
	require.NotSame(t, rf1, rf2, "凭据变了必须换刷新器，不能沿用启动期那一份")

	// 改成不刷新（none）：found 仍为 true，rf 为 nil——明确不刷，不能回落启动 base。
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", dp.ID).Updates(map[string]any{
		"provider": "none", "version": 3,
	}).Error)
	require.NoError(t, r.Touch())
	rf3, found := r.PlatformCDNRefresher()
	require.True(t, found, "档案存在就以它为唯一真值源")
	require.Nil(t, rf3, "provider=none 是明确不刷新")

	base := &countingRefresher{}
	cdn := NewDynamicCDN(base, r)
	require.NoError(t, cdn.Refresh([]string{"https://x/uploads/p/library/a.png"}))
	require.Zero(t, base.calls, "档案改成不刷新后不能偷偷回落启动 base")
}

func TestPlatformCDNNoProfileFallsBackToBase(t *testing.T) {
	db := testDB(t)
	r := newTestResolver(t, db) // 库里没有平台 CDN 内置配置
	r.SetFingerprintTTL(0)
	base := &countingRefresher{}
	cdn := NewDynamicCDN(base, r)

	require.NoError(t, cdn.Refresh([]string{"https://x/uploads/p/library/a.png"}))
	require.Equal(t, 1, base.calls, "没有平台 CDN 配置时（旧库）才回落启动期 base")
}

func TestPlatformBaseURLAndPublicAreLive(t *testing.T) {
	db := testDB(t)
	sp := &model.StorageProfile{
		Name: "主存储", Driver: "local", BaseURL: "https://old/uploads", PublicRead: true,
		Status: "ok", Builtin: true, Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(sp).Error)

	r := newTestResolver(t, db)
	r.SetFingerprintTTL(0)
	v, ok := r.PlatformBaseURL()
	require.True(t, ok)
	require.Equal(t, "https://old/uploads", v)
	p, ok := r.PlatformPublic()
	require.True(t, ok)
	require.True(t, p)

	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"base_url": "https://new/uploads", "public_read": false, "version": 2,
	}).Error)
	require.NoError(t, r.Touch())
	v, ok = r.PlatformBaseURL()
	require.True(t, ok)
	require.Equal(t, "https://new/uploads", v, "基址必须实时")
	p, ok = r.PlatformPublic()
	require.True(t, ok)
	require.False(t, p, "公开读开关必须实时")
}

func TestPlatformBaseURLFallsBackWhenNoInstance(t *testing.T) {
	db := testDB(t)
	r := newTestResolver(t, db) // 没有主存储内置行、也没注入兜底实例
	r.SetFingerprintTTL(0)
	v, ok := r.PlatformBaseURL()
	require.False(t, ok, "没有生效实例时必须回报 ok=false，让调用方回落 cfg")
	require.Empty(t, v)
	p, ok := r.PlatformPublic()
	require.False(t, ok)
	require.False(t, p, "ok=false 时零值不该被当成公开读真值")
}

// 脱钩守门：构造失败时上传器保留旧实例，baseURL/public 必须跟着是旧值。
// 若只保留上传器、基址却读新档案，写入归一会拿新基址剥不出旧上传返回的 URL，
// 这是一条没有任何报错的静默失败，必须钉死。
func TestPlatformRuntimeStaysCoherentOnBuildFailure(t *testing.T) {
	db := testDB(t)
	sp := &model.StorageProfile{
		Name: "主存储", Driver: "oss", Endpoint: "s.example.com", Bucket: "good-bucket",
		BaseURL: "https://old/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true,
		UseSSL: true, Status: "ok", Builtin: true,
		Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(sp).Error)

	r := newTestResolver(t, db)
	r.SetFingerprintTTL(0)
	up1 := r.Platform()
	base1, _ := r.PlatformBaseURL()
	require.Equal(t, "https://old/uploads", base1, "前置条件：初始基址")

	// 同时改基址 + 清空凭据（构造失败），保存。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp.ID).Updates(map[string]any{
		"base_url": "https://new/uploads", "secret_key": "", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())

	require.Same(t, up1, r.Platform(), "构造失败：上传器保留旧实例")
	base2, ok := r.PlatformBaseURL()
	require.True(t, ok)
	require.Equal(t, "https://old/uploads", base2, "构造失败时基址必须跟着上传器保留旧值，不能读新档案")
}

// CDN 凭据无效时不能对外宣称"已启用"：否则管理员以为违规图下架已刷新边缘，实际没有。
func TestPlatformCDNConfiguredReflectsUsableRefresher(t *testing.T) {
	db := testDB(t)
	dp := &model.DeliveryProfile{
		Name: "平台自身 CDN", Provider: "aliyun", AccessKey: "", SecretKey: "",
		Status: "ok", Builtin: true, Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(dp).Error)
	r := newTestResolver(t, db)
	r.SetFingerprintTTL(0)

	require.False(t, r.PlatformCDNConfigured(), "凭据为空、实际不刷新时不能报已启用")

	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", dp.ID).Updates(map[string]any{
		"access_key": "AK", "secret_key": "SK", "version": 2,
	}).Error)
	require.NoError(t, r.Touch())
	require.True(t, r.PlatformCDNConfigured(), "凭据补齐后应如实报已启用")
}

// 守门：热更新逻辑只能认 purpose=platform_primary 的内置行，不能读全局绑定。
// 若有人把 platformPrimary() 改成走 Bindings[0]，p/ 对象会写进客户桶——这是
// 第八十一轮花大力气修掉的静默失败，必须防回退。
func TestLivePlatformNeverReadsGlobalBinding(t *testing.T) {
	db := testDB(t)
	// 全局绑定指向一份客户桶，且**没有** platform_primary 内置行。
	tenant := &model.StorageProfile{
		Name: "客户桶", Driver: "oss", Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "customer",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true,
		UseSSL: true, Status: "ok", Version: 1,
	}
	require.NoError(t, db.Create(tenant).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 0, StorageProfileID: tenant.ID, Version: 1}).Error)

	r := newTestResolver(t, db)
	fallback := &storage.LocalUploader{Dir: t.TempDir()}
	r.SetPlatform(fallback, "local")
	r.SetFingerprintTTL(0)

	// 没有主存储内置行 → 回落启动兜底 local，绝不能用到客户桶。
	require.Same(t, fallback, r.Platform(), "p/ 平台对象绝不能借用全局绑定里的客户桶")
}
