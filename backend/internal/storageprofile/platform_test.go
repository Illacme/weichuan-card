package storageprofile

import (
	"strconv"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func platformTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StorageProfile{}, &model.DeliveryProfile{},
		&model.StorageBinding{}, &setting.Setting{}))
	require.NoError(t, setting.Init(db))
	return db
}

func platformTestConfig() config.Config {
	return config.Config{
		StorageDriver: "s3", UploadDir: "/data/uploads", UploadBaseURL: "https://card.example.com/uploads",
		StoragePublic: true, StorageRegion: "ap-guangzhou",
		MinioEndpoint: "s3.example.com", MinioBucket: "platform-old",
		MinioAccessKey: "AK-ENV", MinioSecretKey: "SK-ENV", MinioUseSSL: true,
	}
}

// 平台自身存储档案是平台连接信息的唯一真值源：首次从部署配置播种，
// 之后管理员在档案里改过的值绝不能被每次启动的 ensure 覆盖回去。
func TestEnsureBuiltinPlatformPrimary(t *testing.T) {
	db := platformTestDB(t)
	cfg := platformTestConfig()

	first, err := EnsureBuiltinPlatformPrimary(db, cfg)
	require.NoError(t, err)
	require.True(t, first.Builtin)
	require.Equal(t, model.ProfilePurposePlatformPrimary, first.Purpose)
	require.Equal(t, "s3", first.Driver)
	require.Equal(t, "s3.example.com", first.Endpoint)
	require.Equal(t, "platform-old", first.Bucket)
	require.Equal(t, "AK-ENV", first.AccessKey)
	require.Equal(t, "https://card.example.com/uploads", first.BaseURL)
	require.True(t, first.PublicRead)
	require.Equal(t, "ap-guangzhou", first.Region)
	require.Equal(t, "/data/uploads", first.LocalDir, "本地目录是部署期事实，跟随 upload_dir")

	// 管理员改连接信息（含驱动）后重启：只能是档案值生效，不能被 env 播种值回滚。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", first.ID).
		Updates(map[string]any{"driver": "minio", "bucket": "platform-new", "secret_key": "SK-UI"}).Error)
	cfg.MinioBucket = "platform-env-still-old"
	again, err := EnsureBuiltinPlatformPrimary(db, cfg)
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID, "重复启动不能新建第二套平台档案")
	require.Equal(t, "minio", again.Driver)
	require.Equal(t, "platform-new", again.Bucket)
	require.Equal(t, "SK-UI", again.SecretKey)

	// 目录仍然跟随配置纠正：静态路由与本地实例都绑在 cfg.UploadDir 上。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", first.ID).
		Update("local_dir", "/mnt/wrong").Error)
	again, err = EnsureBuiltinPlatformPrimary(db, cfg)
	require.NoError(t, err)
	require.Equal(t, "/data/uploads", again.LocalDir)

	require.Equal(t, strconv.FormatInt(first.ID, 10),
		setting.GetString(setting.KeyPlatformStorageProfileID, ""),
		"sys_setting 只留一个指向档案的 id")
}

// 第三套内置档案按 purpose 精确寻址，不能和退路 / 镜像档案互相认成自己。
func TestEnsureBuiltinPlatformPrimaryIsDistinct(t *testing.T) {
	db := platformTestDB(t)
	cfg := platformTestConfig()

	fallback, err := EnsureBuiltinLocal(db, cfg.UploadDir, cfg.UploadBaseURL)
	require.NoError(t, err)
	mirror, err := EnsureBuiltinMirror(db, "/data/mirror")
	require.NoError(t, err)
	platform, err := EnsureBuiltinPlatformPrimary(db, cfg)
	require.NoError(t, err)

	require.Len(t, distinctIDs(fallback.ID, mirror.ID, platform.ID), 3)
	require.Equal(t, model.ProfilePurposeTenantFallback, fallback.Purpose)
	require.Equal(t, model.ProfilePurposePlatformMirror, mirror.Purpose)
	require.Equal(t, model.ProfilePurposePlatformPrimary, platform.Purpose)
}

func distinctIDs(ids ...int64) map[int64]bool {
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// 业务代码仍从 cfg 读驱动 / 公开读 / 基址，启动期同步一次即可，
// 不要求每个 handler 各自查档案（那样既重复又容易漂移）。
func TestApplyPlatformConfigKeepsRuntimeSnapshotInSync(t *testing.T) {
	cfg := config.Config{}
	sp := &model.StorageProfile{
		Driver: "s3", Endpoint: "s3.example.com", Bucket: "b", Region: "ap-guangzhou",
		AccessKey: "AK", SecretKey: "SK", BaseURL: "https://cdn.example.com/uploads",
		PublicRead: true, UseSSL: false,
	}
	ApplyPlatformConfig(&cfg, sp)
	require.Equal(t, "s3", cfg.StorageDriver)
	require.True(t, cfg.StoragePublic)
	require.Equal(t, "https://cdn.example.com/uploads", cfg.UploadBaseURL)
	require.Equal(t, "ap-guangzhou", cfg.StorageRegion)
	require.Equal(t, "AK", cfg.MinioAccessKey)
	require.Equal(t, "b", cfg.OssBucket)
}

// local 驱动的基址允许留空（端上按页面源拼），但要落到 upload_dir 而不是
// 档案自己的 LocalDir：平台实例与 /uploads 静态路由必须同源。
func TestBuildPlatformUploaderLocalUsesUploadDir(t *testing.T) {
	cfg := platformTestConfig()
	sp := &model.StorageProfile{Driver: "local", BaseURL: ""}
	up, err := BuildPlatformUploader(sp, cfg)
	require.NoError(t, err)
	local, ok := up.(*storage.LocalUploader)
	require.True(t, ok)
	require.Equal(t, "/data/uploads", local.Dir)
	require.Equal(t, cfg.UploadBaseURL, local.BaseURL)
}
