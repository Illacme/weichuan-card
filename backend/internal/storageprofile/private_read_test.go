package storageprofile

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
)

// 私有桶是「出参不变、读侧变」：OriginFor 必须继续回平台统一入口，
// PrivateRead 则为 true，让 /uploads/t/<cid>/ 走后端签名跳转。
func TestPrivateReadAndOriginFallback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))

	sp := model.StorageProfile{
		Name: "private", Driver: nginxsnippet.DriverMinio, Endpoint: "s.example.com",
		Bucket: "b", BaseURL: "https://card.example.com/uploads",
		AccessKey: "AK", SecretKey: "SK", PublicRead: false, Version: 1,
	}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&model.DeliveryProfile{Name: "cdn", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 7, StorageProfileID: sp.ID, DeliveryProfileID: 1, Version: 1}).Error)

	r := New(db)
	r.SetFingerprintTTL(0)

	require.True(t, r.PrivateRead(7), "私有桶租户必须走签名跳转")
	require.False(t, r.PrivateRead(0), "平台 cid 不在此列")
	require.False(t, r.PrivateRead(99), "未绑定企业不在此列")

	origin, handled := r.OriginFor(7)
	require.True(t, handled)
	require.Equal(t, model.PlatformOrigin(), origin, "出参仍是稳定的 /uploads/ 路径，不能换成客户域名")

	target := NginxTarget(7, &sp)
	require.True(t, target.Private)
	require.True(t, target.NeedsSnippet(), "私有桶也要有 location，只是目标换成后端")

	// 构造上传器必须成功：写入侧仍用凭据直传桶，私有读不影响写入。
	up, err := r.For(7)
	require.NoError(t, err)
	require.NotNil(t, up)
}

// 平台自身档案即使被手工改成 public_read=0，也必须构造失败：
// p/ 对象没有逐租户签名链路，放过去只会得到「配置保存了、图全裂」。
func TestPlatformPrimaryCannotBePrivate(t *testing.T) {
	sp := &model.StorageProfile{
		Name: "platform", Driver: nginxsnippet.DriverOSS, Endpoint: "oss-cn.example.com",
		Bucket: "private-bucket", BaseURL: "https://card.example.com/uploads",
		AccessKey: "AK", SecretKey: "SK", PublicRead: false, Version: 1,
		Builtin: true, Purpose: model.ProfilePurposePlatformPrimary,
	}
	cfg := uploaderConfigFor(sp, 0)
	_, err := buildUploader(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "公开读")

	// 去掉内置标记后同一份配置就能构造，证明拦的是用途而不是字段本身。
	sp.Builtin = false
	sp.Purpose = ""
	_, err = buildUploader(uploaderConfigFor(sp, 7))
	require.NoError(t, err)
}
