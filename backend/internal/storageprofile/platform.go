package storageprofile

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// 主存储的内置档案（迁移 0059）。
//
// 三件事必须分清：
//   - 连接信息（驱动 / 端点 / 桶 / 凭据 / 基址 / 公开读）在 storage_profiles，
//     由档案页统一编辑、统一自检；
//   - 目录事实（upload_dir / mirror_dir）留在 sys_setting，改完必须重启；
//   - sys_setting 里只多一个指向档案的 id，便于排障时确认「平台实际用哪一套」。
//
// 平台实例仍是启动期构造、运行期不可变：改档案只改库、不换进程里的实例，
// 避免一个请求写新桶、下一个请求又写旧桶。要确保生效必须重启。

const builtinPlatformName = "主存储"

// EnsureBuiltinPlatformPrimary 保证主存储档案存在，并把 sys_setting 指针
// 指过去。首次生成时从 cfg（环境变量 + 部署配置）播种连接信息；
// 之后**绝不覆盖管理员在档案里改过的值**——否则每次重启都把配置改回去。
func EnsureBuiltinPlatformPrimary(db *gorm.DB, cfg config.Config) (*model.StorageProfile, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库为空")
	}
	var row model.StorageProfile
	res := db.Where("builtin = ? AND purpose = ?", true, model.ProfilePurposePlatformPrimary).
		Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("读取主存储配置失败：%w", res.Error)
	}
	if res.RowsAffected == 0 {
		name, err := availableBuiltinName(db, builtinPlatformName)
		if err != nil {
			return nil, err
		}
		row = model.StorageProfile{
			Name: name, Driver: cfg.StorageDriver, LocalDir: strings.TrimSpace(cfg.UploadDir),
			BaseURL: strings.TrimSpace(cfg.UploadBaseURL), PublicRead: cfg.StoragePublic,
			Status: "unknown", Builtin: true,
			Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
		}
		switch row.Driver {
		case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
			row.Endpoint, row.Bucket = cfg.MinioEndpoint, cfg.MinioBucket
			row.AccessKey, row.SecretKey = cfg.MinioAccessKey, cfg.MinioSecretKey
			row.UseSSL, row.Region = cfg.MinioUseSSL, cfg.StorageRegion
		case nginxsnippet.DriverOSS:
			row.Endpoint, row.Bucket = cfg.OssEndpoint, cfg.OssBucket
			row.AccessKey, row.SecretKey = cfg.OssAccessKeyID, cfg.OssAccessKeySecret
			row.UseSSL = true // OSS SDK 固定 https，与 storage.ProbeBucket 同口径
			row.Region = cfg.StorageRegion
		default:
			row.Driver = nginxsnippet.DriverLocal
			row.UseSSL = true
		}
		if err := db.Create(&row).Error; err != nil {
			return nil, fmt.Errorf("创建主存储配置失败：%w", err)
		}
		log.Printf("[STORAGE] 已生成系统自带的文件存储「%s」(id=%d, driver=%s)，平台连接信息以它为准",
			row.Name, row.ID, row.Driver)
	} else {
		// local_dir 是部署期事实：换 UPLOAD_DIR 后静态路由 / 本地实例都指向新目录，
		// 档案留着旧目录会造成「换目录不生效」的错觉。只在值不同时更新，避免
		// 每次启动都写库、惊动档案版本。
		want := strings.TrimSpace(cfg.UploadDir)
		if want != "" && strings.TrimSpace(row.LocalDir) != want {
			if err := db.Model(&model.StorageProfile{}).Where("id = ?", row.ID).
				Update("local_dir", want).Error; err != nil {
				return nil, fmt.Errorf("同步主存储配置目录失败：%w", err)
			}
			row.LocalDir = want
		}
	}

	// 指针写入失败不影响启动：下面用的是刚查到的这一行，不依赖 sys_setting 回读。
	// 但必须留痕，否则「设置里看不到指向」会让人以为平台在用别的档案。
	if err := setting.Upsert(setting.KeyPlatformStorageProfileID, strconv.FormatInt(row.ID, 10)); err != nil {
		log.Printf("[STORAGE][WARN] 写入主存储配置指针失败（不影响本次启动）：%v", err)
	}
	return &row, nil
}

// ApplyPlatformConfig 把档案里的连接信息同步到 cfg 快照。
//
// 业务代码里仍有大量读取 cfg.StorageDriver / cfg.StoragePublic / cfg.UploadBaseURL
// 的地方（出参归一化、签名、日志、安装向导）。让它们各自去读档案既重复又容易漂移，
// 所以在启动期一次性同步：此后 cfg 只读，档案改动必须重启才生效——与内置档案的
// 语义一致，也保持平台实例运行期不可变。
func ApplyPlatformConfig(cfg *config.Config, sp *model.StorageProfile) {
	if cfg == nil || sp == nil {
		return
	}
	cfg.StorageDriver = sp.Driver
	cfg.StoragePublic = sp.PublicRead
	cfg.UploadBaseURL = sp.BaseURL
	cfg.MinioEndpoint, cfg.MinioBucket = sp.Endpoint, sp.Bucket
	cfg.MinioAccessKey, cfg.MinioSecretKey = sp.AccessKey, sp.SecretKey
	cfg.MinioUseSSL, cfg.StorageRegion = sp.UseSSL, sp.Region
	cfg.OssEndpoint, cfg.OssBucket = sp.Endpoint, sp.Bucket
	cfg.OssAccessKeyID, cfg.OssAccessKeySecret = sp.AccessKey, sp.SecretKey
}

// BuildPlatformUploader 按内置档案构造平台实例。
// local 用 upload_dir（与 /uploads 静态路由同源）；云驱动复用档案侧的
// buildUploader，自检与真正上传共用同一套构造口径。
func BuildPlatformUploader(sp *model.StorageProfile, cfg config.Config) (storage.Uploader, error) {
	if sp == nil {
		return nil, fmt.Errorf("主存储配置不存在")
	}
	if sp.Driver == nginxsnippet.DriverLocal {
		base := strings.TrimSpace(sp.BaseURL)
		if base == "" {
			base = cfg.UploadBaseURL
		}
		return &storage.LocalUploader{
			Dir: cfg.UploadDir, BaseURL: base,
			MaxBytes: maxBytes, AllowExt: storage.DefaultAllowExt,
		}, nil
	}
	return buildUploader(uploaderConfigFor(sp, 0))
}

// LocalPlatformFallback 构造失败时的逃生实例：必须是本地盘，保证管理员还能
// 进后台把档案改回来。与 cfg.UploadDir 同源，不会写到镜像或别家桶里。
func LocalPlatformFallback(cfg config.Config) storage.Uploader {
	return &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: maxBytes, AllowExt: storage.DefaultAllowExt,
	}
}
