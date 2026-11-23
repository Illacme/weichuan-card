package storageprofile

import (
	"strconv"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/stretchr/testify/require"
)

func platformDeliveryTestConfig() config.Config {
	return config.Config{
		CDNEnabled:         true,
		CDNProvider:        "tencent",
		CDNAccessKeyID:     "AK-ENV",
		CDNAccessKeySecret: "SK-ENV",
		CDNEndpoint:        "cdn.tencentcloudapi.com",
		CDNResourceID:      "",
	}
}

// 平台自身 CDN 档案是刷新凭据的唯一真值源：首次从部署配置播种，
// 之后管理员在档案里改过的值绝不能被每次启动的 ensure 覆盖回去。
func TestEnsureBuiltinPlatformDelivery(t *testing.T) {
	db := platformTestDB(t)
	cfg := platformDeliveryTestConfig()

	first, err := EnsureBuiltinPlatformDelivery(db, cfg)
	require.NoError(t, err)
	require.True(t, first.Builtin)
	require.Equal(t, model.DeliveryPurposePlatformPrimary, first.Purpose)
	require.Equal(t, "tencent", first.Provider)
	require.Equal(t, "AK-ENV", first.AccessKey)
	require.Equal(t, "SK-ENV", first.SecretKey)
	require.Equal(t, "cdn.tencentcloudapi.com", first.Endpoint)
	require.Empty(t, first.Domain, "平台域名唯一真值源是存储档案的访问基址，分发档案不存域名")

	// 管理员改成 Cloudflare 后重启：档案值生效，环境变量不得回滚。
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", first.ID).
		Updates(map[string]any{"provider": "cloudflare", "secret_key": "TOKEN-UI", "resource_id": "zone-1"}).Error)
	cfg.CDNProvider = "aliyun"
	cfg.CDNAccessKeyID = "AK-ENV-STILL-OLD"
	again, err := EnsureBuiltinPlatformDelivery(db, cfg)
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID, "重复启动不能新建第二套平台 CDN 档案")
	require.Equal(t, "cloudflare", again.Provider)
	require.Equal(t, "TOKEN-UI", again.SecretKey)
	require.Equal(t, "zone-1", again.ResourceID)
	require.Equal(t, "AK-ENV", again.AccessKey, "没改的字段保持档案里的值，不被 env 覆盖")

	require.Equal(t, strconv.FormatInt(first.ID, 10),
		setting.GetString(setting.KeyPlatformDeliveryProfileID, ""),
		"sys_setting 只留一个指向档案的 id")
}

// 首次播种：CDN_ENABLED 关 → provider=none（凭据仍保留在档案里，日后开启不用重填）；
// 开启但没选厂商 → 与 normalizeCDNProvider 同口径回落阿里云。
func TestEnsureBuiltinPlatformDeliverySeedSemantics(t *testing.T) {
	db := platformTestDB(t)

	off := config.Config{CDNEnabled: false, CDNProvider: "qiniu", CDNAccessKeyID: "AK"}
	row, err := EnsureBuiltinPlatformDelivery(db, off)
	require.NoError(t, err)
	require.Equal(t, storage.CDNProviderNone, row.Provider, "关闭状态要落成明确的「不刷新」")
	require.Equal(t, "AK", row.AccessKey, "凭据仍要保留，开启时不用重填")

	// 换一个库模拟「开启但没选厂商」的老部署
	db2 := platformTestDB(t)
	on := config.Config{CDNEnabled: true, CDNProvider: "", CDNAccessKeySecret: "SK"}
	row2, err := EnsureBuiltinPlatformDelivery(db2, on)
	require.NoError(t, err)
	require.Equal(t, storage.CDNProviderAliyun, row2.Provider)
}

func TestBuildPlatformRefresher(t *testing.T) {
	// 明确「不刷新」不是错误
	rf, err := BuildPlatformRefresher(&model.DeliveryProfile{Provider: storage.CDNProviderNone})
	require.NoError(t, err)
	require.Nil(t, rf)

	rf, err = BuildPlatformRefresher(&model.DeliveryProfile{Provider: ""})
	require.NoError(t, err)
	require.Nil(t, rf)

	// 凭据不全 → 报错（启动方记日志并继续，刷新是增强）
	_, err = BuildPlatformRefresher(&model.DeliveryProfile{Provider: "aliyun"})
	require.Error(t, err)

	rf, err = BuildPlatformRefresher(&model.DeliveryProfile{
		Provider: "aliyun", AccessKey: "AK", SecretKey: "SK",
	})
	require.NoError(t, err)
	require.NotNil(t, rf)
}

func TestApplyPlatformDeliveryConfig(t *testing.T) {
	cfg := config.Config{CDNProvider: "old", CDNEnabled: true}
	ApplyPlatformDeliveryConfig(&cfg, &model.DeliveryProfile{
		Provider: storage.CDNProviderNone, AccessKey: "AK",
	})
	require.False(t, cfg.CDNEnabled, "provider=none 就是关闭，不再需要第二个开关")
	require.Equal(t, storage.CDNProviderNone, cfg.CDNProvider)

	ApplyPlatformDeliveryConfig(&cfg, &model.DeliveryProfile{
		Provider: "cloudflare", AccessKey: "AK", SecretKey: "TOKEN", ResourceID: "zone-1",
	})
	require.True(t, cfg.CDNEnabled)
	require.Equal(t, "cloudflare", cfg.CDNProvider)
	require.Equal(t, "TOKEN", cfg.CDNAccessKeySecret)
	require.Equal(t, "zone-1", cfg.CDNResourceID)
}
