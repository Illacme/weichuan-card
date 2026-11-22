package handler

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"weichuan/internal/model"
	"weichuan/internal/storage"
	"weichuan/internal/storageprofile"

	"gorm.io/gorm"
)

// 切换前完整性核对：防止「绑定切过去了，对象却没搬全」导致整站裂图。
//
// 为什么做在切换的入口而不是读路径：读路径（nginx 前缀反代 / 出参拼 URL）是
// 纯字符串与静态路由，没有能力探测对象在哪、更没有第二个档案可回退（见
// storageprofile.Resolver 与 nginxsnippet）。因此唯一可靠的把关点就是**切换瞬间**——
// 在覆盖绑定之前，实地比对两个档案的对象集合。
//
// fail closed 是这条链路的纪律：列举失败返回 error，调用方拒绝切换。
// 「没核对成功」绝不能当成「核对通过」，那等于把这道门拆了。
const switchMissingSample = 5

// verifySwitchObjects 比对目标档案是否已经包含源档案在 prefix 下的全部对象。
//
// 返回缺失 key 的采样（最多 switchMissingSample 个，已排序）与准确总数。
// 任一侧列举失败都返回 error，由调用方 fail closed。
//
// 用 map 求差集而不是双层循环：大桶可能有几十万对象，O(n+m) 是必须；
// 采样只作用于展示，总数仍按完整差集计，保证提示里的数字是真的。
func verifySwitchObjects(src, dst storage.ObjectLister, prefix string) ([]string, int, error) {
	present := make(map[string]struct{})
	if err := src.ListObjects(prefix, func(key string, _ int64) error {
		if strings.HasPrefix(key, prefix) {
			present[key] = struct{}{}
		}
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("列举当前文件存储的对象失败：%w", err)
	}
	if err := dst.ListObjects(prefix, func(key string, _ int64) error {
		delete(present, key)
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("列举目标文件存储的对象失败：%w", err)
	}

	total := len(present)
	all := make([]string, 0, total)
	for key := range present {
		all = append(all, key)
	}
	// 先全局排序再采样：这样示例稳定且总是字典序最小的几个，
	// 便于管理员按 key 定位、也让断言不依赖 map 遍历的随机顺序。
	sort.Strings(all)
	missing := all
	if len(all) > switchMissingSample {
		missing = all[:switchMissingSample]
	}
	return missing, total, nil
}

// listerForProfile 按档案 ID 取对象列举能力。profileID=0 表示平台实例
// （没有绑定时对象所在的那一份），其余走 UploaderForProfile 不经过当前绑定——
// 自检和迁移都走同一个口子，这里也复用，避免「核对看的是 A、切换写的是 B」。
func listerForProfile(resolver *storageprofile.Resolver, profileID int64) (storage.ObjectLister, error) {
	var up storage.Uploader
	if profileID == 0 {
		up = resolver.Platform()
		if up == nil {
			return nil, fmt.Errorf("平台存储实例尚未就绪")
		}
	} else {
		var err error
		up, err = resolver.UploaderForProfile(profileID)
		if err != nil {
			return nil, err
		}
	}
	lister, ok := up.(storage.ObjectLister)
	if !ok {
		return nil, fmt.Errorf("文件存储 %d 的驱动不支持对象列举，无法在切换前核对完整性", profileID)
	}
	return lister, nil
}

// verifyProfileSwitch 按档案 ID 核对一次切换。
// prefix 与迁移任务同源（migrationPrefix）：企业任务只搬 t/<cid>/，
// 核对也只看这一段；全局任务整桶核对。
// srcProfileID==dstProfileID 时无需核对（重复提交同一档案）。
func verifyProfileSwitch(resolver *storageprofile.Resolver, tenantID, srcProfileID, dstProfileID int64) ([]string, int, error) {
	if srcProfileID == dstProfileID {
		return nil, 0, nil
	}
	prefix := migrationPrefix(tenantID)
	src, err := listerForProfile(resolver, srcProfileID)
	if err != nil {
		return nil, 0, fmt.Errorf("读取当前文件存储：%w", err)
	}
	dst, err := listerForProfile(resolver, dstProfileID)
	if err != nil {
		return nil, 0, fmt.Errorf("读取目标文件存储：%w", err)
	}
	return verifySwitchObjects(src, dst, prefix)
}

// switchNotReadyError 把核对结果转成给管理员看的错误。
// 带上缺失 key 示例与明确处置路径——只说「对象不全」会让管理员无从下手。
func switchNotReadyError(missing []string, total int) error {
	return fmt.Errorf("目标文件存储尚未包含当前文件存储的全部对象：缺失 %d 个（示例：%s）。"+
		"请先运行迁移任务把对象复制到目标文件存储、确认零失败后再切换；"+
		"若确认无需搬迁（例如目标本就是新桶），可显式强制切换",
		total, strings.Join(missing, "、"))
}

// currentProfilePair 读出 tenantID 当前生效的 (存储档案, 分发档案)。
// 企业没有自己的绑定时继承全局；全局也没有则存储档案为 0（平台实例）。
// 只在**覆盖绑定之前**调用——覆盖之后再读就是目标值了，核对会永远通过。
func currentProfilePair(db *gorm.DB, tenantID int64) (storageID, deliveryID int64, err error) {
	var b model.StorageBinding
	q := db.Where("tenant_id = ?", tenantID)
	if tenantID > 0 {
		// 企业自己的绑定优先于全局；Order 让 tenant_id 降序，企业行排在全局行前。
		q = db.Where("tenant_id IN ?", []int64{tenantID, 0}).Order("tenant_id DESC")
	}
	res := q.Limit(1).Find(&b)
	if res.Error != nil {
		return 0, 0, fmt.Errorf("读取当前绑定失败：%w", res.Error)
	}
	if res.RowsAffected == 0 {
		return 0, 0, nil
	}
	return b.StorageProfileID, b.DeliveryProfileID, nil
}

// guardManualSwitch 供手动 PUT 绑定调用：以「当前生效档案」为源核对目标是否齐全。
// force=true 只跳过对象齐全核对，不跳过任何库读取；调用方必须在审计里记 force。
//
// 读当前绑定失败要 fail closed：此时无法知道源是谁，「当作空集放行」等于拆门。
func guardManualSwitch(db *gorm.DB, resolver *storageprofile.Resolver, tenantID, targetID int64, force bool) error {
	if force {
		return nil
	}
	srcID, _, err := currentProfilePair(db, tenantID)
	if err != nil {
		return err
	}
	missing, total, err := verifyProfileSwitch(resolver, tenantID, srcID, targetID)
	if err != nil {
		return err
	}
	if total > 0 {
		return switchNotReadyError(missing, total)
	}
	return nil
}

// guardAutoSwitch 供迁移任务 autoSwitch 调用：源/目标取迁移任务的两端，
// 不信当前绑定（它可能因为并发被手工改过）。迁移「零失败」只是复制端的结论，
// 这里是独立的读端复核——两道门各自成立，才允许覆盖绑定。
//
// 不支持 force：迁移模式本身就要求「先完整搬迁、再切换」，核对不过就是没资格切。
func guardAutoSwitch(resolver *storageprofile.Resolver, job *model.StorageMigrationJob) error {
	missing, total, err := verifyProfileSwitch(resolver, job.TenantID, job.SourceProfileID, job.TargetProfileID)
	if err != nil {
		return err
	}
	if total > 0 {
		return fmt.Errorf("迁移后核对发现目标文件存储仍缺失 %d 个对象（示例：%s），已拒绝自动切换绑定；"+
			"请重试迁移失败对象后再切", total, strings.Join(missing, "、"))
	}
	return nil
}

// rollbackBinding 把绑定切回「本次切换前的档案」，交换语义：
//
//	current, previous = previous, current
//
// 交换而不是单向置回，是为了让回滚可逆：切 A→B 后回滚到 A，此时 previous=B，
// 再回滚一次就又回到 B。管理员误点了回滚也能立刻撤销，不会丢掉「刚才那套」。
//
// 三条边界（勿回退）：
//  1. previous storage=0 表示这条绑定从没切换过（或首次建立前没有档案）——拒绝，
//     退路是 DELETE binding 回落全局/平台，不另造一套「回滚到平台」语义。
//  2. **回滚不跑对象齐全核对**：它是应急动作，目标是「立刻回到切换前能用的状态」，
//     此时要求逐对象比对只会把管理员卡在故障现场。对象是否齐全由 previous 的来历
//     保证——它就是切换前的生效档案，对象当时就在那儿。
//  3. 但目标档案本身必须有效（存在 / 非镜像 / 非停用），否则回滚会指向一个坏档案，
//     等于从一个故障跳到另一个故障。
func rollbackBinding(db *gorm.DB, tenantID int64, actor string) error {
	// 与 saveBinding 同一条纪律：校验、读旧值、交换写入必须在同一事务里。
	// 否则「读到 previous A」和「写回 previous B」之间插入一次并发切换，
	// 那次切换记下的回滚游标就会被这次回滚覆盖，且没有任何报错。
	return db.Transaction(func(tx *gorm.DB) error {
		var b model.StorageBinding
		res := tx.Where("tenant_id = ?", tenantID).Limit(1).Find(&b)
		if res.Error != nil {
			return fmt.Errorf("读取绑定失败：%w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("该范围没有绑定记录，没有可回滚的切换")
		}
		if b.PreviousStorageProfileID <= 0 {
			return fmt.Errorf("这条绑定从未切换过文件存储（或已是初始状态），没有可回滚的目标")
		}
		if msg := validateBindingTargets(tx, b.PreviousStorageProfileID, b.PreviousDeliveryProfileID); msg != "" {
			return fmt.Errorf("切换前的文件存储已不可用，无法回滚：%s", msg)
		}
		var sp model.StorageProfile
		if err := tx.First(&sp, b.PreviousStorageProfileID).Error; err != nil {
			return fmt.Errorf("切换前的文件存储已不存在，无法回滚")
		}
		if sp.Status == "deprecated" {
			return fmt.Errorf("切换前的文件存储已停用，无法回滚")
		}

		curStorage, curDelivery := b.StorageProfileID, b.DeliveryProfileID
		b.StorageProfileID = b.PreviousStorageProfileID
		b.DeliveryProfileID = b.PreviousDeliveryProfileID
		b.PreviousStorageProfileID = curStorage
		b.PreviousDeliveryProfileID = curDelivery
		b.UpdatedBy = actor
		b.Version++
		b.UpdatedAt = time.Now()
		if err := tx.Save(&b).Error; err != nil {
			return fmt.Errorf("回滚写入失败：%w", err)
		}
		return nil
	})
}
