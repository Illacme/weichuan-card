package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/refmigrate"
	"weichuan/internal/setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 第六十七轮 CLI 侧守门：配置来源必须与生产上传侧同源、只读、不写库，
// 且 --source-store 的分派真的生效。

// envPlatformS3 把测试用的平台存储配置装进环境变量，并让 CONFIG_FILE 指向
// 一个空的临时文件，避免开发机上真实的 data/runtime.env 干扰取值。
func envPlatformS3(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty.env")
	require.NoError(t, os.WriteFile(empty, []byte("# empty\n"), 0o600))
	t.Setenv("CONFIG_FILE", empty)
	t.Setenv("STORAGE_DRIVER", "s3")
	t.Setenv("MINIO_ENDPOINT", "127.0.0.1:19000")
	t.Setenv("MINIO_BUCKET", "wc-platform-env")
	t.Setenv("MINIO_ACCESS_KEY", "AK-ENV")
	t.Setenv("MINIO_SECRET_KEY", "SK-ENV")
	t.Setenv("MINIO_USE_SSL", "false")
	t.Setenv("STORAGE_REGION", "ap-guangzhou")
}

func TestPlatformSourceConfigReadsEnv(t *testing.T) {
	envPlatformS3(t)
	cfg, err := platformSourceConfig(nil)
	require.NoError(t, err)
	require.Equal(t, "s3", cfg.Driver)
	require.Equal(t, "127.0.0.1:19000", cfg.Endpoint)
	require.Equal(t, "wc-platform-env", cfg.Bucket)
	require.Equal(t, "AK-ENV", cfg.AccessKey)
	require.Equal(t, "SK-ENV", cfg.SecretKey)
	require.Equal(t, "ap-guangzhou", cfg.Region)
	require.False(t, cfg.UseSSL)
}

// TestPlatformSourceConfigSettingsOverride 钉住与 config.OverlayFromSettings
// 一字不差的语义：非空界面值覆盖 env，空串 / 纯空白视为「未设置」保留 env。
// 尤其 storage_use_ssl 用 setting.ParseBool 同一真值表，"on" 必须算 true。
func TestPlatformSourceConfigSettingsOverride(t *testing.T) {
	envPlatformS3(t)
	db := settingsDB(t)
	rows := []setting.Setting{
		{Key: "storage_bucket", Value: "wc-platform-ui"},
		{Key: "storage_secret_key", Value: "SK-UI"},
		{Key: "storage_use_ssl", Value: "on"},
		{Key: "storage_endpoint", Value: "   "}, // 空白 = 未设置，应回落 env
		{Key: "storage_region", Value: "ap-shanghai"},
	}
	require.NoError(t, db.Create(&rows).Error)

	cfg, err := platformSourceConfig(db)
	require.NoError(t, err)
	require.Equal(t, "wc-platform-ui", cfg.Bucket)
	require.Equal(t, "SK-UI", cfg.SecretKey)
	require.True(t, cfg.UseSSL, "on/yes/1 必须按 setting.ParseBool 的真值表算 true")
	require.Equal(t, "127.0.0.1:19000", cfg.Endpoint, "空白界面值不许把 env 覆盖成空")
	require.Equal(t, "ap-shanghai", cfg.Region)
	require.Equal(t, "AK-ENV", cfg.AccessKey, "界面没设的项保留 env")
}

// TestPlatformSourceConfigNeverWritesSettings：回填命令只读配置，绝不能调
// setting.Init（会 seedDefaults 往业务库 INSERT 一批默认项）。这里把行数与
// 内容都拍下来再比，反向验证时把 Init 加回来必红。
func TestPlatformSourceConfigNeverWritesSettings(t *testing.T) {
	envPlatformS3(t)
	db := settingsDB(t)
	require.NoError(t, db.Create(&setting.Setting{Key: "storage_bucket", Value: "wc-ui"}).Error)
	before := settingRows(t, db)

	_, err := platformSourceConfig(db)
	require.NoError(t, err)
	require.Equal(t, before, settingRows(t, db), "读配置不许写 sys_setting（含 seedDefaults）")
}

func TestSourceForS3BuildsRealSourceNotLocalDisk(t *testing.T) {
	envPlatformS3(t)
	src, err := sourceFor("s3", "", nil)
	require.NoError(t, err)
	require.IsType(t, &refmigrate.PlatformS3Source{}, src,
		"必须是真 S3 源，绝不能拿本地盘顶上")
}

func TestSourceForS3RejectsAmbiguousSourceDir(t *testing.T) {
	envPlatformS3(t)
	_, err := sourceFor("s3", t.TempDir(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source-dir")
	require.Contains(t, err.Error(), "无法判断哪个生效")
}

// TestSourceForS3NeverFallsBackToLocalDisk：把配置弄坏（driver=local）、
// 或只留半截凭据，都必须显式失败。反向验证：把 sourceFor 里 s3 分支改成
// 直接 return localSource("")，这条会拿到「必须给 --source-dir」而不是
// 「不回退本地盘」——如期变红。
func TestSourceForS3NeverFallsBackToLocalDisk(t *testing.T) {
	envPlatformS3(t)
	cases := []struct {
		name, key, val, want string
	}{
		{"平台是本地盘", "STORAGE_DRIVER", "local", "本地盘"},
		{"平台是 oss", "STORAGE_DRIVER", "oss", "oss"},
		{"桶名为空", "MINIO_BUCKET", "", "MINIO_BUCKET"},
		{"凭据缺一", "MINIO_SECRET_KEY", "", "MINIO_SECRET_KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(c.key, c.val)
			src, err := sourceFor("s3", "", nil)
			require.Error(t, err)
			require.Nil(t, src)
			require.Contains(t, err.Error(), "不回退本地盘")
			require.Contains(t, err.Error(), c.want)
		})
	}
}

func TestBuildStoresS3SourceWithTenantTarget(t *testing.T) {
	envPlatformS3(t)
	testPiiKey(t)
	db := settingsDB(t) // 故意不建 tenant_storage_configs：目标应整批停下
	stores, err := buildStores("s3", "tenant", "", "", db)
	require.NoError(t, err, "源可用、目标配置在 TargetFor 时才解析")
	require.IsType(t, &refmigrate.PlatformS3Source{}, stores.Source)
	_, err = stores.TargetFor(7)
	require.Error(t, err)
	require.Contains(t, err.Error(), "不回退平台盘")
}

func TestBuildStoresS3SourceWithLocalTarget(t *testing.T) {
	envPlatformS3(t)
	root := t.TempDir()
	stores, err := buildStores("s3", "local", "", filepath.Join(root, "dst"), nil)
	require.NoError(t, err, "S3 源 → 本地盘目标是合法组合（离线核对字节用）")
	require.IsType(t, &refmigrate.PlatformS3Source{}, stores.Source)
	target, err := stores.TargetFor(7)
	require.NoError(t, err)
	require.IsType(t, &refmigrate.LocalStore{}, target,
		"本地目标不需要 PII_ENC_KEY，也不该读 tenant_storage_configs")
}

// TestSourceConfigIsReadOnlyAndSingleTruthTable 是源码级守门：
// 不许引入第二份配置加载路径或第二份 bool 真值表；抓取型守门带兜底。
func TestSourceConfigIsReadOnlyAndSingleTruthTable(t *testing.T) {
	raw, err := os.ReadFile("source_config.go")
	require.NoError(t, err)
	code := stripCLIComments(string(raw))
	requires := []string{
		"config.Load()", "setting.ParseBool(", "readPlatformSettings(",
		"db.Migrator().HasTable(", "map[string]any{\"key\":",
	}
	found := 0
	for _, a := range requires {
		require.Contains(t, code, a)
		found++
	}
	require.GreaterOrEqual(t, found, 5, "抓到的锚点太少，守门可能已失效")
	require.NotContains(t, code, "setting.Init(", "Init 会 seedDefaults 写库")
	require.NotContains(t, code, "OverlayFromSettings(", "该函数依赖已初始化的全局 Store")
	require.NotContains(t, code, "Upsert(", "回填命令不许写系统设置")
	require.NotContains(t, code, "os.Getenv(\"MINIO_SECRET_KEY\")", "凭据解析必须走 config.Load")
}

func stripCLIComments(src string) string {
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

func settingsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")),
		&gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&setting.Setting{}))
	return db
}

func settingRows(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var rows []setting.Setting
	require.NoError(t, db.Order("`key`").Find(&rows).Error)
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.Key + "=" + r.Value + ";")
	}
	return b.String()
}
