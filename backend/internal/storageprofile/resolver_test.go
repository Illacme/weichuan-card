package storageprofile

import (
	"testing"

	"weichuan/internal/model"

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

func TestResolveInheritanceAndImmediateVersion(t *testing.T) {
	db := testDB(t)
	sp1 := model.StorageProfile{Name: "default", Driver: "minio", Endpoint: "s1.example.com", Bucket: "b1", BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	sp2 := model.StorageProfile{Name: "company", Driver: "minio", Endpoint: "s2.example.com", Bucket: "b2", BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, Version: 1, UseSSL: true}
	dp := model.DeliveryProfile{Name: "cdn", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}
	require.NoError(t, db.Create(&sp1).Error)
	require.NoError(t, db.Create(&sp2).Error)
	require.NoError(t, db.Create(&dp).Error)

	r := New(db)
	// 无绑定：返回空生效配置，调用方按旧口径走平台，不是错误。
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
	r := New(db)
	cdn := NewDynamicCDN(nil, r)
	require.False(t, cdn.(*DynamicCDN).Configured(), "没配 CDN 时动态壳不能冒充已配置")
	require.NoError(t, cdn.Refresh([]string{"https://x.com/uploads/t/12/a.png"}))
}
