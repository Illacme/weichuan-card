package storageprofile

import (
	"fmt"
	"strings"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// 内置本地档案：每个部署都有两套，且都不可删除（handler 拦）、驱动锁定为 local。
//
//   - tenant_fallback「本地应急盘」：租户退路，目录与 cfg.UploadDir 同源，
//     静态路由 /uploads 直接托管它。服务「云配置全坏时还能上传」。
//   - platform_mirror「本地备份盘」：备份落点，目录为 cfg.MirrorDir，
//     不在 /uploads 下，PublicRead=false。只接收「上传成功后从云回拉」的副本，
//     **不允许被任何绑定引用**（绑定它等于把备份副本变成主写入目标）。
//
// 第三套内置档案 platform_primary「平台主存储」在 platform.go：它承载平台
// 自己的连接信息，驱动可以是云；与上面两套「本地盘用途」不是一回事，别混用。
//
// 为什么不写进迁移 SQL：目录取决于部署时的 UPLOAD_DIR / MIRROR_DIR，基址取决于
// UPLOAD_BASE_URL，都不是迁移能知道的。放到启动时 ensure，升级与首装同一套逻辑。
const (
	builtinLocalName  = "本地应急盘"
	builtinMirrorName = "本地备份盘"
)

// 旧默认名：2026-11-23 之前种下的展示名。三个名字里两个都带「平台」、两个都带
// 「本地」，用户分不清「平台主存储 / 平台本地存储 / 平台本地镜像」谁是谁，
// 于是改成按职责命名（主存储 / 应急 / 备份）。
//
// 改名放在启动 ensure 里而不是迁移 SQL：SQLite 与 devserver 走 AutoMigrate、
// 根本不执行 migrations，只有在这里改才能让三种部署形态表现一致。
const (
	legacyBuiltinLocalName    = "平台本地存储"
	legacyBuiltinMirrorName   = "平台本地镜像"
	legacyBuiltinPlatformName = "平台自身存储"
)

// renameBuiltinIfLegacy 把「还叫旧默认名」的内置档案改成新角色名。
//
// 两条边界：
//  1. 管理员自己改过的名字不动——只认字符串完全等于旧默认名的情况；
//  2. 新名字已被别的档案占用时不改：name 有唯一索引，硬改会让整个进程起不来，
//     宁可继续显示旧名，也不能为了好看挡住启动。
func renameBuiltinIfLegacy(db *gorm.DB, row *model.StorageProfile, legacy, want string) error {
	if row == nil || strings.TrimSpace(row.Name) != legacy {
		return nil
	}
	var n int64
	if err := db.Model(&model.StorageProfile{}).Where("name = ?", want).Count(&n).Error; err != nil {
		return fmt.Errorf("检查内置存储改名冲突失败：%w", err)
	}
	if n > 0 {
		return nil
	}
	if err := db.Model(&model.StorageProfile{}).Where("id = ?", row.ID).
		Update("name", want).Error; err != nil {
		return fmt.Errorf("更新系统自带文件存储的展示名失败：%w", err)
	}
	row.Name = want
	return nil
}

// EnsureBuiltinLocal 保证「租户退路」内置档案存在并返回它。
func EnsureBuiltinLocal(db *gorm.DB, dir, baseURL string) (*model.StorageProfile, error) {
	return ensureBuiltin(db, model.ProfilePurposeTenantFallback, builtinLocalName, dir, baseURL, true)
}

// EnsureBuiltinMirror 保证「本地备份盘」内置档案存在并返回它。
// 镜像副本不对外公开，所以 baseURL 传空；目录可由部署配置修改，但启动后不可在界面改。
func EnsureBuiltinMirror(db *gorm.DB, dir string) (*model.StorageProfile, error) {
	return ensureBuiltin(db, model.ProfilePurposePlatformMirror, builtinMirrorName, dir, "", false)
}

// ensureBuiltin 保证某一用途的内置档案存在。
//
// 四条边界：
//  1. 幂等：重复启动不重复插入，也不覆盖管理员改过的展示名；
//  2. 只认 builtin=true 且 purpose 匹配的行；历史同名非内置行由 availableBuiltinName 避让；
//  3. **local_dir 必须同步为传入的 dir**：退路档案要与静态路由 / 平台实例同源
//     （否则租户写新目录、静态路由读旧目录，全站裂图且无人报错）；镜像档案要与
//     运行期 MirrorManager 的落点同源（否则备份写进 A 目录，备份脚本打的是 B 目录）；
//  4. 不自动创建全局绑定——把 local 绑成全局默认会静默改变所有租户的文件落点。
func ensureBuiltin(db *gorm.DB, purpose, name, dir, baseURL string, publicRead bool) (*model.StorageProfile, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库为空")
	}
	// 升级兼容（SQLite / devserver 的 AutoMigrate 不会执行迁移里的 UPDATE）：
	// 0056 之前的内置行 purpose 为空，只能是租户退路档案——那时还没有第二种。
	if err := db.Model(&model.StorageProfile{}).
		Where("builtin = ? AND purpose = ?", true, "").
		Update("purpose", model.ProfilePurposeTenantFallback).Error; err != nil {
		return nil, fmt.Errorf("补齐系统自带文件存储的用途失败：%w", err)
	}
	var row model.StorageProfile
	res := db.Where("builtin = ? AND purpose = ?", true, purpose).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("读取系统自带文件存储失败：%w", res.Error)
	}
	want := strings.TrimSpace(dir)
	if res.RowsAffected > 0 {
		// 先按旧默认名改成角色名，再走下面那些「保内容」的更新。
		legacy := legacyBuiltinLocalName
		if purpose == model.ProfilePurposePlatformMirror {
			legacy = legacyBuiltinMirrorName
		}
		if err := renameBuiltinIfLegacy(db, &row, legacy, name); err != nil {
			return nil, err
		}
		// 已存在：展示名保留管理员改过的值；local_dir 必须跟随启动配置，
		// 仅在值确实不同时更新，避免每次启动都无谓写库并惊动档案版本。
		updates := map[string]any{}
		if want != "" && strings.TrimSpace(row.LocalDir) != want {
			updates["local_dir"] = want
		}
		if strings.TrimSpace(row.BaseURL) == "" && strings.TrimSpace(baseURL) != "" {
			updates["base_url"] = strings.TrimSpace(baseURL)
		}
		if row.Purpose != purpose {
			updates["purpose"] = purpose
		}
		if len(updates) > 0 {
			if err := db.Model(&model.StorageProfile{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return nil, fmt.Errorf("补全系统自带文件存储失败：%w", err)
			}
			_ = db.First(&row, row.ID).Error
		}
		return &row, nil
	}

	name, err := availableBuiltinName(db, name)
	if err != nil {
		return nil, err
	}
	row = model.StorageProfile{
		Name: name, Driver: "local", LocalDir: want,
		BaseURL: strings.TrimSpace(baseURL), PublicRead: publicRead, UseSSL: true,
		Status: "unknown", Builtin: true, Purpose: purpose, Version: 1,
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, fmt.Errorf("创建系统自带文件存储失败：%w", err)
	}
	return &row, nil
}

// availableBuiltinName 找一个不与现有档案冲突的名字。
// 名字有唯一索引；若期望名被手工建过（非内置），直接插入会报唯一键冲突，
// 让整个进程起不来——这里退让成「xxx（内置）」并在必要时加序号。
func availableBuiltinName(db *gorm.DB, base string) (string, error) {
	candidates := []string{base, base + "（内置）"}
	for i := 2; i <= 20; i++ {
		candidates = append(candidates, fmt.Sprintf("%s（内置%d）", base, i))
	}
	for _, name := range candidates {
		var n int64
		if err := db.Model(&model.StorageProfile{}).Where("name = ?", name).Count(&n).Error; err != nil {
			return "", fmt.Errorf("检查文件存储名称冲突失败：%w", err)
		}
		if n == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("无法为系统自带文件存储分配不冲突的名称")
}
