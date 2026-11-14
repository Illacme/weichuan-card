package main

import (
	"fmt"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/refmigrate"
	"weichuan/internal/setting"

	"gorm.io/gorm"
)

// 本文件负责把「平台自身存储」的配置解析成 S3 源适配器需要的那一组值。
//
// 为什么不能只 config.Load()：生产上运维大概率是在后台「系统设置」里改的
// 存储端点 / 桶 / 凭据（sys_setting），环境变量里那几项还是安装时的旧值。
// 只看 env，回填会连到一个完全不同的桶，表现为「所有源对象都不存在」，
// 而 nobody 会想到去查配置来源。
//
// 为什么不能调 setting.Init(db) + cfg.OverlayFromSettings()：Init 会
// seedDefaults()，把 registry 里缺失的项 INSERT 进业务库。回填命令只读
// 配置却顺手写了库，既越权又会在只读从库上直接失败。这里改成**只读查询**
// sys_setting，再按与 OverlayFromSettings 完全相同的语义覆盖：
// 空串 = 界面没设 → 保留 env / CONFIG_FILE 的值；语法与真值表都复用
// setting.ParseBool，不另写一份。
//
// 凭据不进命令行参数：命令行会留在 shell history / ps / 工单里。它们只能来自
// env / CONFIG_FILE / sys_setting，与生产上传侧同一份，且错误文本绝不回显。

var platformSourceKeys = []string{
	"storage_driver", "storage_endpoint", "storage_bucket",
	"storage_access_key", "storage_secret_key",
	"storage_use_ssl", "storage_region",
}

// platformSourceConfig 读平台自身存储配置。db 可以为 nil（离线演练），
// 此时只认 env / CONFIG_FILE。表不存在时同样只认 env——SQLite 单测与
// 尚未跑过 setting.Init 的库都走这条路，不报错也不悄悄当空配置。
func platformSourceConfig(db *gorm.DB) (refmigrate.PlatformS3Config, error) {
	cfg := config.Load()
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

func readPlatformSettings(db *gorm.DB) (map[string]string, error) {
	out := map[string]string{}
	if db == nil || !db.Migrator().HasTable(&setting.Setting{}) {
		return out, nil
	}
	var rows []setting.Setting
	// key 是 MySQL 保留字：用 map 条件让 gorm 按方言加引号，
	// 裸字符串 "key IN ?" 在 MySQL 会直接 1064。
	if err := db.Where(map[string]any{"key": platformSourceKeys}).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 sys_setting 里的平台存储配置失败：%w", err)
	}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}
