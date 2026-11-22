package main

import (
	"fmt"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/refmigrate"
	"weichuan/internal/setting"

	"gorm.io/gorm"
)

// 本文件负责把「平台自身存储」的配置解析成 S3 源适配器需要的那一组值。
//
// 唯一真值源是 storage_profiles 里 purpose=platform_primary 的内置档案
// （迁移 0059）——与生产上传侧同一份连接信息、同一处自检。回填命令只读配置，
// 不能调 setting.Init（会 seedDefaults 写库），所以这里直接查表。
//
// 表不存在 / 档案还没生成时（离线演练、尚未跑过启动流程的库）才退回
// env / CONFIG_FILE / 历史 sys_setting。后者只在 bootstrap 阶段有意义：
// 正常启动会把那批 key 清掉，档案建成后不会再走这条分支。
//
// 凭据不进命令行参数：命令行会留在 shell history / ps / 工单里。

// legacyPlatformSettingKeys 与 storageprofile 包的播种清单同口径。
var legacyPlatformSettingKeys = []string{
	"storage_driver", "storage_endpoint", "storage_bucket",
	"storage_access_key", "storage_secret_key", "storage_use_ssl",
	"storage_region",
}

func platformSourceConfig(db *gorm.DB) (refmigrate.PlatformS3Config, error) {
	cfg := config.Load()
	if db != nil && db.Migrator().HasTable(&model.StorageProfile{}) {
		var sp model.StorageProfile
		err := db.Where("builtin = ? AND purpose = ?", true, model.ProfilePurposePlatformPrimary).
			Limit(1).Find(&sp).Error
		if err != nil {
			return refmigrate.PlatformS3Config{}, fmt.Errorf("读取平台自身存储配置失败：%w", err)
		}
		if sp.ID > 0 {
			return platformSourceFromProfile(&sp), nil
		}
	}

	values, err := readPlatformSettings(db)
	if err != nil {
		return refmigrate.PlatformS3Config{}, err
	}
	// 与 config.overlay.str 一字不差：空串 / 纯空白视为「未设置」。
	str := func(key, def string) string {
		if v := strings.TrimSpace(values[key]); v != "" {
			return v
		}
		return def
	}
	out := refmigrate.PlatformS3Config{
		Driver:    str("storage_driver", cfg.StorageDriver),
		Endpoint:  str("storage_endpoint", cfg.MinioEndpoint),
		Bucket:    str("storage_bucket", cfg.MinioBucket),
		Region:    str("storage_region", cfg.StorageRegion),
		AccessKey: str("storage_access_key", cfg.MinioAccessKey),
		SecretKey: str("storage_secret_key", cfg.MinioSecretKey),
		UseSSL:    cfg.MinioUseSSL,
	}
	if v := strings.TrimSpace(values["storage_use_ssl"]); v != "" {
		out.UseSSL = setting.ParseBool(v, cfg.MinioUseSSL)
	}
	return out, nil
}

// platformSourceFromProfile 档案里的 endpoint 是 host[:port]（与 nginx / 自检同口径），
// 正好是 PlatformS3Config 要的形态；不需要再补 scheme。
func platformSourceFromProfile(sp *model.StorageProfile) refmigrate.PlatformS3Config {
	ep, useSSL := splitProfileEndpoint(sp.Endpoint, sp.UseSSL)
	// OSS 档案也存 host[:port]，但 platform_source 的 SDK 要完整 URL；
	// 这里只在 scheme 缺失时补，和 buildUploader 的 OSS 分支同一口径。
	if sp.Driver == "oss" && ep != "" && !strings.Contains(ep, "://") {
		if useSSL {
			ep = "https://" + ep
		} else {
			ep = "http://" + ep
		}
	}
	return refmigrate.PlatformS3Config{
		Driver: sp.Driver, Endpoint: ep, Bucket: sp.Bucket, Region: sp.Region,
		AccessKey: sp.AccessKey, SecretKey: sp.SecretKey, UseSSL: useSSL,
	}
}

func splitProfileEndpoint(endpoint string, useSSL bool) (string, bool) {
	ep := strings.TrimSpace(endpoint)
	low := strings.ToLower(ep)
	switch {
	case strings.HasPrefix(low, "https://"):
		return ep[len("https://"):], true
	case strings.HasPrefix(low, "http://"):
		return ep[len("http://"):], false
	}
	return ep, useSSL
}

func readPlatformSettings(db *gorm.DB) (map[string]string, error) {
	out := map[string]string{}
	if db == nil || !db.Migrator().HasTable(&setting.Setting{}) {
		return out, nil
	}
	var rows []setting.Setting
	// key 是 MySQL 保留字：用 map 条件让 gorm 按方言加引号，
	// 裸字符串 "key IN ?" 在 MySQL 会直接 1064。
	if err := db.Where(map[string]any{"key": legacyPlatformSettingKeys}).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 sys_setting 里的平台存储配置失败：%w", err)
	}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}
