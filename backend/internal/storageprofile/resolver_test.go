package storageprofile

import (
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{}))
	return db
}

// newTestResolver 关掉指纹节流：这些用例直接写库后立刻断言「下一次解析能看到」，
// 生产默认的 700ms 节流会让它们读回旧快照（跨进程一致性靠 Touch / 下一轮查询）。
func newTestResolver(t *testing.T, db *gorm.DB) *Resolver {
	t.Helper()
	r := New(db)
	r.SetFingerprintTTL(0)
	return r
}

func TestResolveInheritanceAndImmediateVersion(t *testing.T) {
	db := testDB(t)
	sp1 := model.StorageProfile{Name: "default", Driver: "minio", Endpoint: "s1.example.com", Bucket: "b1", BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	sp2 := model.StorageProfile{Name: "company", Driver: "minio", Endpoint: "s2.example.com", Bucket: "b2", BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	dp := model.DeliveryProfile{Name: "cdn", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}
	require.NoError(t, db.Create(&sp1).Error)
	require.NoError(t, db.Create(&sp2).Error)
	require.NoError(t, db.Create(&dp).Error)

	r := newTestResolver(t, db)
	// 无绑定：返回空生效配置，调用方走平台实例，不是错误。
	eff, err := r.Resolve(7)
	require.NoError(t, err)
	require.Nil(t, eff.Storage)

	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 0, StorageProfileID: sp1.ID, Version: 1}).Error)
	eff, err = r.Resolve(7)
	require.NoError(t, err)
	require.Equal(t, sp1.ID, eff.Storage.ID, "企业无绑定行时继承全局")

	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 7, StorageProfileID: sp2.ID, DeliveryProfileID: dp.ID, Version: 1}).Error)
	eff, err = r.Resolve(7)
	require.NoError(t, err)
	require.Equal(t, sp2.ID, eff.Storage.ID, "企业自己的绑定优先")
	require.Equal(t, dp.ID, eff.Delivery.ID)
	origin, ok := r.OriginFor(7)
	require.True(t, ok)
	require.Equal(t, "https://cdn.example.com", origin.Base)

	// 改档案版本后不需要 Touch：下一次解析按 MAX(version) 自动重载。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", sp2.ID).Update("version", 2).Error)
	eff, err = r.Resolve(7)
	require.NoError(t, err)
	require.EqualValues(t, 2, eff.Storage.Version, "全局默认切换必须即时生效，不依赖重启")

	// 绑定挂到不存在的档案必须报错，不能静默退回平台。
	require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = 7").Update("storage_profile_id", 9999).Error)
	require.NoError(t, r.Touch())
	_, err = r.Resolve(7)
	require.Error(t, err)
	require.Contains(t, err.Error(), "不存在")
}

func TestTenantFromURLsAndDynamicCDN(t *testing.T) {
	require.EqualValues(t, 12, TenantFromURLs("https://x.com/uploads/t/12/a.png"))
	require.EqualValues(t, 0, TenantFromURLs("https://x.com/uploads/p/a.png"))

	db := testDB(t)
	r := newTestResolver(t, db)
	cdn := NewDynamicCDN(nil, r)
	require.False(t, cdn.(*DynamicCDN).Configured(), "没配 CDN 时动态壳不能冒充已配置")
	require.NoError(t, cdn.Refresh([]string{"https://x.com/uploads/t/12/a.png"}))
}

// 平台对象（p/ 前缀）必须始终写平台实例，不能被全局档案接管：
// 字节进了客户桶、URL 仍是平台域名，取图必然 404。
func TestDynamicPlatformNeverUsesProfile(t *testing.T) {
	db := testDB(t)
	sp := model.StorageProfile{Name: "global", Driver: "minio", Endpoint: "s.example.com", Bucket: "b", BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 0, StorageProfileID: sp.ID, Version: 1}).Error)

	plat := &storage.LocalUploader{Dir: t.TempDir()}
	r := newTestResolver(t, db)
	r.SetPlatform(plat, "local")
	require.Same(t, plat, r.Platform(), "Platform() 不能被档案接管")
}

// OriginFor 的判定必须按「谁在写对象」分叉，而不是按「谁配了域名」分叉。
//
// 这是第七十四轮合并双口径的核心：以前只要分发档案有合法域名就返回，
// 于是一家绑了本地档案（对象在平台盘）、分发档案又填了域名的企业，
// 出参会被指到那个域名上——全站裂图且没有任何一边报错。
func TestOriginForFollowsWriteOwnership(t *testing.T) {
	db := testDB(t)
	dp := model.DeliveryProfile{Name: "cdn", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}
	require.NoError(t, db.Create(&dp).Error)

	cloud := model.StorageProfile{Name: "cloud", Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	local := model.StorageProfile{Name: "local", Driver: "local", Version: 1}
	private := model.StorageProfile{Name: "private", Driver: "minio", Endpoint: "s2.example.com", Bucket: "b2",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: false, Version: 1, UseSSL: true}
	require.NoError(t, db.Create(&cloud).Error)
	require.NoError(t, db.Create(&local).Error)
	require.NoError(t, db.Create(&private).Error)

	r := newTestResolver(t, db)

	// 没有任何绑定：档案不接管，出参用平台统一入口。
	origin, handled := r.OriginFor(7)
	require.False(t, handled, "无绑定必须判为「没有接管」")
	require.Empty(t, origin.Base)

	// 云档案 + 分发域名：接管，且用分发域名。
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 7, StorageProfileID: cloud.ID, DeliveryProfileID: dp.ID, Version: 1}).Error)
	origin, handled = r.OriginFor(7)
	require.True(t, handled)
	require.Equal(t, "https://cdn.example.com", origin.Base)
	require.EqualValues(t, 7, origin.CompanyID)

	// 本地档案 + 分发域名：接管，但域名不能用（对象在平台盘）。
	require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = 7").Updates(map[string]any{"storage_profile_id": local.ID}).Error)
	require.NoError(t, r.Touch())
	origin, handled = r.OriginFor(7)
	require.True(t, handled, "本地档案也是接管：它明确表达了「对象在平台盘」")
	require.Empty(t, origin.Base, "本地档案没有桶可回源，配了域名也只能回平台入口")

	// 私有桶：接管，但直连会 403。
	require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = 7").Updates(map[string]any{"storage_profile_id": private.ID}).Error)
	require.NoError(t, r.Touch())
	origin, handled = r.OriginFor(7)
	require.True(t, handled)
	require.Empty(t, origin.Base, "私有桶直连 403，出参必须回平台入口")

	// cid 未知：不能凭全局绑定猜域名（Origin 的不变式要求 CompanyID>0）。
	origin, handled = r.OriginFor(0)
	require.False(t, handled)
	require.Empty(t, origin.Base)
}
