package storageprofile

import (
	"fmt"
	"strings"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// 内置本地档案：每个部署都必须有且只有一套，作为「云配置配错 / 桶欠费 / 网络不通」
// 时的最后退路。它不可删除（handler 拦），驱动锁定为 local（handler 拦），
// 管理员只能改它的展示名与本地目录。
//
// 为什么不写进迁移 SQL：本地目录取决于部署时的 UPLOAD_DIR，基址取决于
// UPLOAD_BASE_URL，两者都不是迁移能知道的。放到启动时 ensure，升级与首装同一套逻辑。
const builtinLocalName = "平台本地存储"

// EnsureBuiltinLocal 保证内置本地档案存在，返回该档案（已存在则原样返回）。
//
// 三条边界：
//  1. 幂等：重复启动不会重复插入，也不会覆盖管理员改过的名称 / 目录；
//  2. 只认 builtin=true 的行；若历史手工数据里已有同名非内置行，换一个不冲突的名字；
//  3. 不自动创建全局绑定——把 local 绑成全局默认会覆盖租户已有的自有桶配置
//     （tenant_storage_configs），属于静默改变文件落点。绑不绑由管理员在界面决定。
func EnsureBuiltinLocal(db *gorm.DB, dir, baseURL string) (*model.StorageProfile, error) {
	if db == nil {
		return nil, fmt.Errorf("数据库为空")
	}
	var row model.StorageProfile
	res := db.Where("builtin = ?", true).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("读取内置本地档案失败：%w", res.Error)
	}
	if res.RowsAffected > 0 {
		// 已存在：只在字段为空时补默认值，绝不覆盖管理员改过的目录。
		updates := map[string]any{}
		if strings.TrimSpace(row.LocalDir) == "" && strings.TrimSpace(dir) != "" {
			updates["local_dir"] = strings.TrimSpace(dir)
		}
		if strings.TrimSpace(row.BaseURL) == "" && strings.TrimSpace(baseURL) != "" {
			updates["base_url"] = strings.TrimSpace(baseURL)
		}
		if len(updates) > 0 {
			if err := db.Model(&model.StorageProfile{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return nil, fmt.Errorf("补全内置本地档案失败：%w", err)
			}
			_ = db.First(&row, row.ID).Error
		}
		return &row, nil
	}

	name, err := availableBuiltinName(db)
	if err != nil {
		return nil, err
	}
	row = model.StorageProfile{
		Name: name, Driver: "local", LocalDir: strings.TrimSpace(dir),
		BaseURL: strings.TrimSpace(baseURL), PublicRead: true, UseSSL: true,
		Status: "unknown", Builtin: true, Version: 1,
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, fmt.Errorf("创建内置本地档案失败：%w", err)
	}
	return &row, nil
}

// availableBuiltinName 找一个不与现有档案冲突的名字。
// 名字有唯一索引；若「平台本地存储」被手工建过（非内置），直接插入会报唯一键冲突，
// 让整个进程起不来——这里退让成「平台本地存储（内置）」并在必要时加序号。
func availableBuiltinName(db *gorm.DB) (string, error) {
	candidates := []string{builtinLocalName, builtinLocalName + "（内置）"}
	for i := 2; i <= 20; i++ {
		candidates = append(candidates, fmt.Sprintf("%s（内置%d）", builtinLocalName, i))
	}
	for _, name := range candidates {
		var n int64
		if err := db.Model(&model.StorageProfile{}).Where("name = ?", name).Count(&n).Error; err != nil {
			return "", fmt.Errorf("检查档案名冲突失败：%w", err)
		}
		if n == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("无法为内置本地档案分配不冲突的名称")
}
