package repository

// 档案（storage_profiles + storage_bindings）→ nginx 回源目标的读取层。
//
// 为什么需要这一层：`cmd/nginx-snippets --dsn` 一直只读 tenant_storage_configs，
// 于是「客户自有桶的 /uploads/t/<cid>/ 回源段」只能由旧表生成。档案化之后，
// 一家企业到底该回源到哪个桶，答案在绑定里——不补这条读取路径，档案就永远
// 只能改上传、改不了接入层（见 docs/tenant-storage-design.md §0.1 阶段二）。
//
// 三条纪律与 ListStorageTargets 完全一致，别在这里放松：
//
//	表不存在  → ErrProfileTablesMissing（运维按它给的步骤处理）
//	查询出错  → 原样包装返回，**绝不**返回空切片 + nil
//	真的没绑定 → ([] , nil)，调用方据此输出「没有任何企业由档案接管」

import (
	"errors"
	"fmt"
	"sort"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storageprofile"

	"gorm.io/gorm"
)

// ErrProfileTablesMissing 表示迁移 0054 还没跑。
//
// 与 ErrStorageConfigMissing 分开成两个哨兵：它们的处置步骤不同（0044 / 0054），
// 合成一个的话运维会照着旧提示重跑一遍已经跑过的迁移。
var ErrProfileTablesMissing = errors.New(
	`存储档案表不存在（迁移 0054 未应用）：请先执行 ./server migrate；` +
		`未升级的部署请用 --source=legacy 只读 tenant_storage_configs`)

// ProfileTarget 是「按档案 + 绑定解析出的一家企业的回源目标」。
//
// 比裸 nginxsnippet.Target 多带档案信息，是为了让生成器能把「这家为什么有 location」
// 说清楚——运维排查时第一个问题永远是「这条 location 是从哪来的」。
type ProfileTarget struct {
	CompanyID   int64
	ProfileID   int64
	ProfileName string
	// ProfileStatus 是档案自检状态（ok / error / unknown），供生成器打警告用。
	// 不参与渲染：桶暂时探不通不代表 location 该消失。
	ProfileStatus string
	// Global 为 true 表示该企业没有自己的绑定行，用的是全局默认（tenant_id=0）。
	Global bool
	Target nginxsnippet.Target
}

// ListProfileTargets 读出「每家由档案接管的企业 → 该回源的桶」。
//
// 全局绑定（tenant_id=0）要展开成**全部企业**：nginx 的 location 是按
// /uploads/t/<cid>/ 逐家生成的，没有「全局 location」这种形态。展开需要企业清单，
// 所以这里会读一次 company 表；企业被停用也照样展开（多一条没人访问的 location
// 无害，漏一家才是裂图）。
func ListProfileTargets(db *gorm.DB) ([]ProfileTarget, error) {
	// 先探连接，再问表在不在：gorm 的 HasTable 把 error 吞成 false，
	// 「库连不上」会被误报成「没跑迁移」（与 ListStorageTargets 同一个坑）。
	if err := db.Exec("SELECT 1").Error; err != nil {
		return nil, fmt.Errorf("数据库不可达，无法读取存储档案：%w", err)
	}
	if !db.Migrator().HasTable(&model.StorageProfile{}) || !db.Migrator().HasTable(&model.StorageBinding{}) {
		return nil, ErrProfileTablesMissing
	}

	var bindings []model.StorageBinding
	if err := db.Find(&bindings).Error; err != nil {
		return nil, fmt.Errorf("读取 storage_bindings 失败：%w", err)
	}
	if len(bindings) == 0 {
		return nil, nil
	}

	// 候选企业：有自己绑定行的 + （存在全局绑定时）全部企业。
	cands := make(map[int64]bool, len(bindings))
	hasGlobal := false
	for _, b := range bindings {
		if b.TenantID == 0 {
			hasGlobal = true
			continue
		}
		cands[b.TenantID] = true
	}
	if hasGlobal {
		var ids []int64
		if err := db.Model(&model.Company{}).Pluck("id", &ids).Error; err != nil {
			return nil, fmt.Errorf("读取企业清单失败（全局绑定需要展开成逐家 location）：%w", err)
		}
		for _, id := range ids {
			cands[id] = true
		}
	}

	r := storageprofile.New(db)
	out := make([]ProfileTarget, 0, len(cands))
	for cid := range cands {
		eff, err := r.Resolve(cid)
		if err != nil {
			// 绑定指向不存在 / 已停用的档案时，运行时的上传也会报同一个错。
			// 这里静默跳过等于「这家悄悄回落平台盘」，是比中断生成更坏的结果。
			return nil, fmt.Errorf("企业 %d 的存储档案绑定不可用（请先修好再生成 snippet）：%w", cid, err)
		}
		if eff.Storage == nil {
			continue // 没有全局绑定、也没有自己的绑定行 → 交回旧表 / 平台
		}
		_, global := bindingFor(bindings, cid)
		out = append(out, ProfileTarget{
			CompanyID:     cid,
			ProfileID:     eff.Storage.ID,
			ProfileName:   eff.Storage.Name,
			ProfileStatus: eff.Storage.Status,
			Global:        global,
			Target:        storageprofile.NginxTarget(cid, eff.Storage),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CompanyID < out[j].CompanyID })
	return out, nil
}

// bindingFor 回报该企业是否有自己的绑定行；没有则说明生效的是全局绑定。
func bindingFor(bindings []model.StorageBinding, cid int64) (model.StorageBinding, bool) {
	for _, b := range bindings {
		if b.TenantID == cid {
			return b, false
		}
	}
	return model.StorageBinding{}, true
}
