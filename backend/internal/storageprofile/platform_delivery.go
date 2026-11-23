package storageprofile

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// 平台自身 CDN 的内置分发档案（迁移 0060）。
//
// 与 0059 的 platform_primary 存储档案同构，只是换了一个被配置的对象：
//   - 平台对象（p/ 前缀）与**没有绑定分发档案**的企业的覆盖写，由这份档案的
//     凭据刷新边缘缓存；
//   - 已绑定分发档案的企业仍走自己那一套（storage_bindings.delivery_profile_id），
//     逐请求解析、免重启；
//   - sys_setting 只留一个指向本档案的 id，那组 cdn_* 只作为首次播种的种子。
//
// 平台刷新器由 Resolver 按这份档案的 version 现建、整份替换（见 platform_live.go），
// 保存即生效；并发请求要么全用旧凭据、要么全用新凭据，不会一次刷新用新凭据、
// 下一次又用旧凭据。凭据写错时暂停刷新并记日志，不静默借用别家配置。
//
// 这份档案刻意不承载域名：平台对象的出参域名唯一真值源是 storage_profiles 里
// platform_primary 的 BaseURL（OriginFor 对 cid<=0 直接回平台入口）。让分发档案
// 也存一份域名，就会出现「存储档案改了基址、分发档案还指着旧域名」的第二真值源，
// 而那正是这一轮要消灭的东西。

const builtinPlatformDeliveryName = "平台自身 CDN"

// EnsureBuiltinPlatformDelivery 保证平台自身 CDN 档案存在，并把 sys_setting 指针
// 指过去。首次生成时从 cfg（已叠加历史 sys_setting 值）播种厂商与凭据；
// 之后**绝不覆盖管理员在档案里改过的值**——否则每次重启都把配置改回去。
func EnsureBuiltinPlatformDelivery(db *gorm.DB, cfg config.Config) (*model.DeliveryProfile, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库为空")
	}
	var row model.DeliveryProfile
	res := db.Where("builtin = ? AND purpose = ?", true, model.DeliveryPurposePlatformPrimary).
		Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("读取平台自身的 CDN 分发配置失败：%w", res.Error)
	}
	if res.RowsAffected == 0 {
		name, err := availableBuiltinDeliveryName(db, builtinPlatformDeliveryName)
		if err != nil {
			return nil, err
		}
		row = model.DeliveryProfile{
			Name: name, Provider: seedCDNProvider(cfg),
			AccessKey:  strings.TrimSpace(cfg.CDNAccessKeyID),
			SecretKey:  strings.TrimSpace(cfg.CDNAccessKeySecret),
			Endpoint:   strings.TrimSpace(cfg.CDNEndpoint),
			ResourceID: strings.TrimSpace(cfg.CDNResourceID),
			Status:     "unknown", Builtin: true,
			Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
		}
		if err := db.Create(&row).Error; err != nil {
			return nil, fmt.Errorf("创建平台自身的 CDN 分发配置失败：%w", err)
		}
		log.Printf("[CDN] 已生成系统自带的 CDN 分发「%s」(id=%d, provider=%s)，平台 CDN 刷新凭据以它为准",
			row.Name, row.ID, row.Provider)
	}

	// 指针写入失败不影响启动：下面用的是刚查到的这一行，不依赖 sys_setting 回读。
	if err := setting.Upsert(setting.KeyPlatformDeliveryProfileID, strconv.FormatInt(row.ID, 10)); err != nil {
		log.Printf("[CDN][WARN] 写入平台 CDN 分发配置指针失败（不影响本次启动）：%v", err)
	}
	return &row, nil
}

// seedCDNProvider 决定首次生成档案时填哪个厂商。
//
// 历史语义是「CDN_ENABLED 关 → 不刷新」，而分发档案用 provider=none 表达同一件事。
// 所以关闭时播 none（凭据仍保留在档案里，日后开启不用重填）；开启但没选厂商时
// 与 storage.normalizeCDNProvider 同口径回落阿里云——旧版本只有阿里云。
func seedCDNProvider(cfg config.Config) string {
	if !cfg.CDNEnabled {
		return storage.CDNProviderNone
	}
	p := strings.ToLower(strings.TrimSpace(cfg.CDNProvider))
	if p == "" {
		return storage.CDNProviderAliyun
	}
	return p
}

func availableBuiltinDeliveryName(db *gorm.DB, base string) (string, error) {
	candidates := []string{base, base + "（内置）"}
	for i := 2; i <= 20; i++ {
		candidates = append(candidates, fmt.Sprintf("%s（内置%d）", base, i))
	}
	for _, name := range candidates {
		var n int64
		if err := db.Model(&model.DeliveryProfile{}).Where("name = ?", name).Count(&n).Error; err != nil {
			return "", fmt.Errorf("检查 CDN 分发名称冲突失败：%w", err)
		}
		if n == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("无法为系统自带的 CDN 分发分配不冲突的名称")
}

// ApplyPlatformDeliveryConfig 把档案里的刷新配置同步到 cfg 快照。
//
// 与 ApplyPlatformConfig 同理：cfg.CDN* 仍有读者（日志、诊断、旧调用方），
// 让它们各自去读档案既重复又容易漂移，所以启动期一次性同步，此后 cfg 只读。
func ApplyPlatformDeliveryConfig(cfg *config.Config, dp *model.DeliveryProfile) {
	if cfg == nil || dp == nil {
		return
	}
	p := strings.ToLower(strings.TrimSpace(dp.Provider))
	cfg.CDNEnabled = p != "" && p != storage.CDNProviderNone
	cfg.CDNProvider = p
	cfg.CDNAccessKeyID = dp.AccessKey
	cfg.CDNAccessKeySecret = dp.SecretKey
	cfg.CDNEndpoint = dp.Endpoint
	cfg.CDNResourceID = dp.ResourceID
}

// BuildPlatformRefresher 按内置分发档案构造平台刷新器。
//
// provider=none / 空 → (nil, nil)：明确「不刷新」不是错误，调用方据此不注册刷新。
// 凭据不全 → 返回错误，由启动方记一行日志并继续启动：刷新是增强，配错不能
// 让服务起不来（与 0059 之前 cdn_* 的处理一致）。
func BuildPlatformRefresher(dp *model.DeliveryProfile) (storage.CDNRefresher, error) {
	if dp == nil {
		return nil, nil
	}
	p := strings.ToLower(strings.TrimSpace(dp.Provider))
	if p == "" || p == storage.CDNProviderNone {
		return nil, nil
	}
	return storage.NewCDNRefresher(storage.CDNConfig{
		Provider: p, AccessKeyID: dp.AccessKey, AccessKeySecret: dp.SecretKey,
		Endpoint: dp.Endpoint, ResourceID: dp.ResourceID,
	})
}
