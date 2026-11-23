package main

import (
	"fmt"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/refmigrate"

	"gorm.io/gorm"
)

// 本文件负责把「主存储」的配置解析成 S3 源适配器需要的那一组值。
//
// 唯一真值源是 storage_profiles 里 purpose=platform_primary 的内置档案
// （迁移 0059）——与生产上传侧同一份连接信息、同一处自检。回填命令只读配置，
// 不能调 setting.Init（会 seedDefaults 写库），所以这里直接查表。
//
// 表不存在 / 档案还没生成时（离线演练、尚未跑过启动流程的库）退回
// env / CONFIG_FILE。驱动 / 端点 / 桶 / 凭据不再从 sys_setting 读：那批 key
// 早已从 registry 移除，留着这条分支等于给同一件事留第二处入口。
//
// 凭据不进命令行参数：命令行会留在 shell history / ps / 工单里。

func platformSourceConfig(db *gorm.DB) (refmigrate.PlatformS3Config, error) {
	cfg := config.Load()
	if db != nil && db.Migrator().HasTable(&model.StorageProfile{}) {
		var sp model.StorageProfile
		err := db.Where("builtin = ? AND purpose = ?", true, model.ProfilePurposePlatformPrimary).
			Limit(1).Find(&sp).Error
		if err != nil {
			return refmigrate.PlatformS3Config{}, fmt.Errorf("读取主存储配置失败：%w", err)
		}
		if sp.ID > 0 {
			return platformSourceFromProfile(&sp), nil
		}
	}

	return refmigrate.PlatformS3Config{
		Driver:    cfg.StorageDriver,
		Endpoint:  cfg.MinioEndpoint,
		Bucket:    cfg.MinioBucket,
		Region:    cfg.StorageRegion,
		AccessKey: cfg.MinioAccessKey,
		SecretKey: cfg.MinioSecretKey,
		UseSSL:    cfg.MinioUseSSL,
	}, nil
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
