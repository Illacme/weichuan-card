package refmigrate

import (
	"encoding/json"
	"fmt"

	"weichuan/internal/model"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

type RefAction string

const (
	RefUpdate RefAction = "update" // 库里还是 Raw，要改
	RefSkip   RefAction = "skip"   // 已搬完（值 = TargetKey 且台账有记录）
)

type ObjectAction string

const (
	ObjCopy      ObjectAction = "copy"
	ObjSkip      ObjectAction = "skip"
	ObjOverwrite ObjectAction = "overwrite"
)

type RefStep struct {
	R        lookup
	Action   RefAction
	Current  string
	LedgerID int64 // 非 0 = 台账里已有这一行（回滚后重跑时复用，不重复插入）
}

type ObjectStep struct {
	Group     ObjectGroup
	CompanyID int64
	Src       ObjectInfo
	Dst       ObjectInfo
	Action    ObjectAction
}

type Preflight struct {
	PlanHash string
	Refs     []RefStep
	Objects  []ObjectStep
}

type ApplyOptions struct {
	DryRun bool
	// Force 只放宽一件事：目标对象已存在但大小与源不一致时用源覆盖它
	// （上次复制到一半留下半截对象）。它**绝不放宽**库值的过期检测：
	// 「库被别人改过」要么停下、要么人工修数据，没有 force 选项。
	Force bool
}

type ApplyResult struct {
	PlanHash       string
	ObjectsCopied  int
	ObjectsSkipped int
	RefsUpdated    int
	RefsSkipped    int
	Preflight      *Preflight
}

// loadLedger 按计划 hash 读台账。台账是「这批计划改过哪些格」的唯一真相，
// 与计划文件本身互为交叉校验（Rollback 里会检查两者对得上）。
func loadLedger(db *gorm.DB, hash string) (map[string]model.StorageBackfillRef, error) {
	var rows []model.StorageBackfillRef
	if err := db.Where("plan_hash = ?", hash).Order("id asc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取回填台账失败（是不是没跑 migrate / 缺 0045？）：%w", err)
	}
	out := make(map[string]model.StorageBackfillRef, len(rows))
	for _, r := range rows {
		out[r.RefID] = r
	}
	return out, nil
}

// readWhole 读一格的完整原文。COALESCE 把 NULL 变空串：业务列可空，
// 直接扫进 string 在部分驱动上会报「converting NULL to string」。
func readWhole(q *gorm.DB, r lookup) (string, error) {
	var out struct {
		Val string `gorm:"column:val"`
	}
	tx := q.Table(r.Table).
		Select("COALESCE("+r.Col.Col+", '') AS val").
		Where(r.Col.PK+" = ?", r.Item.PKValue).
		Scan(&out)
	if tx.Error != nil {
		return "", fmt.Errorf("读 %s.%s#%d 失败：%w", r.Table, r.Col.Col, r.Item.PKValue, tx.Error)
	}
	if tx.RowsAffected == 0 {
		return "", fmt.Errorf("%w：%s.%s#%d 这行不存在了", ErrStalePlan, r.Table, r.Col.Col, r.Item.PKValue)
	}
	return out.Val, nil
}

func elementAt(whole string, idx int) (string, error) {
	var list []string
	if err := json.Unmarshal([]byte(whole), &list); err != nil {
		return "", fmt.Errorf("JSON 数组解析失败：%w", err)
	}
	if idx < 0 || idx >= len(list) {
		return "", fmt.Errorf("数组下标 %d 越界（当前 %d 个元素）", idx, len(list))
	}
	return list[idx], nil
}

func readRef(q *gorm.DB, r lookup) (string, error) {
	whole, err := readWhole(q, r)
	if err != nil {
		return "", err
	}
	if r.Item.Index < 0 {
		return whole, nil
	}
	v, err := elementAt(whole, r.Item.Index)
	if err != nil {
		return "", fmt.Errorf("%s[%d]：%w", r.Col.Label, r.Item.Index, err)
	}
	return v, nil
}

// writeRef 是**比较并交换**：只有当前值仍等于 expectedOld 才写。
// 预检到提交之间可能有人改了库（哪怕只改了一个数组元素），不 CAS 就会把
// 别人的新值覆盖掉——而且库里会显示成「搬迁成功」，没人察觉。
func writeRef(q *gorm.DB, r lookup, newVal, expectedOld string) error {
	whole, err := readWhole(q, r)
	if err != nil {
		return err
	}
	if r.Item.Index < 0 && whole != expectedOld {
		return fmt.Errorf("%w：%s.%s#%d 预检后又被改动（期望 %q，实际 %q）",
			ErrStalePlan, r.Table, r.Col.Col, r.Item.PKValue, expectedOld, whole)
	}
	toWrite := newVal
	if r.Item.Index >= 0 {
		cur, err := elementAt(whole, r.Item.Index)
		if err != nil {
			return fmt.Errorf("%s[%d]：%w", r.Col.Label, r.Item.Index, err)
		}
		if cur != expectedOld {
			return fmt.Errorf("%w：%s[%d] 预检后又被改动（期望 %q，实际 %q）",
				ErrStalePlan, r.Col.Label, r.Item.Index, expectedOld, cur)
		}
		var list []string
		if err := json.Unmarshal([]byte(whole), &list); err != nil {
			return err
		}
		list[r.Item.Index] = newVal
		b, err := json.Marshal(list)
		if err != nil {
			return err
		}
		toWrite = string(b)
	}
	// 刻意不检查 RowsAffected：MySQL 对「原样 UPDATE」返回 0，
	// 拿 0 当「没找到行」会误报（项目里已经踩过一次）。
	upd := q.Table(r.Table).
		Where(r.Col.PK+" = ?", r.Item.PKValue).
		Update(r.Col.Col, toWrite)
	if upd.Error != nil {
		return fmt.Errorf("改写 %s.%s#%d 失败：%w", r.Table, r.Col.Col, r.Item.PKValue, upd.Error)
	}
	return nil
}

func objectCompany(g ObjectGroup) (int64, error) {
	ref, err := storage.ParseRef(g.TargetKey)
	if err != nil {
		return 0, fmt.Errorf("%w：目标键 %q 不是合法的租户键：%v", ErrPlanTampered, g.TargetKey, err)
	}
	if ref.Scope != storage.ScopeTenant {
		return 0, fmt.Errorf("%w：目标键 %q 归属平台而非租户", ErrPlanTampered, g.TargetKey)
	}
	return ref.CompanyID, nil
}

// preflight 是**全量只读预检**：任何一条不合格就整批停下，绝不「跳过坏的那条」。
// 跳过会造成「一部分搬了、一部分没搬」，比全停更难收拾：库里的形态从此混着两种，
// 而人手里那份计划既不能重跑（会撞过期检测）也不能直接回滚（台账只有一半）。
func preflight(db *gorm.DB, pf *PlanFile, stores *Stores, opt ApplyOptions) (*Preflight, error) {
	if err := ValidatePlan(pf.Plan, true); err != nil {
		return nil, err
	}
	lookups, err := MigrateLookups(pf.Plan)
	if err != nil {
		return nil, err
	}
	ledger, err := loadLedger(db, pf.Hash)
	if err != nil {
		return nil, err
	}
	pfOut := &Preflight{PlanHash: pf.Hash}

	for _, r := range lookups {
		cur, err := readRef(db, r)
		if err != nil {
			return nil, err
		}
		row, hasLedger := ledger[RefID(r)]
		step := RefStep{R: r, Current: cur}
		if hasLedger {
			step.LedgerID = row.ID
		}
		// 用 tagged switch 而不是 switch{case cur == ...}：两处比较对象恒为 cur，
		// 写成后者会被 staticcheck QF1002 点名。目标键在前、原值在后，
		// 顺序与原实现一致（二者万一相等，仍按「已搬完」处理）。
		switch cur {
		case r.Item.TargetKey:
			if !hasLedger || row.Status != StatusApplied {
				return nil, fmt.Errorf("%w：%s.%s#%d 库里已是目标键 %q，但台账没有 applied 记录（谁改的？）",
					ErrStalePlan, r.Table, r.Col.Col, r.Item.PKValue, cur)
			}
			step.Action = RefSkip
		case r.Item.Raw:
			step.Action = RefUpdate
		default:
			return nil, fmt.Errorf("%w：%s.%s#%d 现在的值是 %q，计划生成时是 %q（也不是目标键 %q）",
				ErrStalePlan, r.Table, r.Col.Col, r.Item.PKValue, cur, r.Item.Raw, r.Item.TargetKey)
		}
		pfOut.Refs = append(pfOut.Refs, step)
	}

	groups := pf.Plan.Objects()
	for _, g := range groups {
		cid, err := objectCompany(g)
		if err != nil {
			return nil, err
		}
		if stores == nil || stores.Source == nil {
			return nil, fmt.Errorf("没有配置源对象存储，无法搬迁 %q", g.SourceKey)
		}
		src, err := stores.Source.Stat(g.SourceKey)
		if err != nil {
			return nil, fmt.Errorf("检查源对象 %q 失败：%w", g.SourceKey, err)
		}
		if !src.Exists {
			return nil, fmt.Errorf("%w：源对象 %q 不存在，无法复制成 %q", ErrNeedsHuman, g.SourceKey, g.TargetKey)
		}
		dstStore, err := stores.target(cid)
		if err != nil {
			return nil, fmt.Errorf("company_id=%d 的目标存储不可用：%w", cid, err)
		}
		dst, err := dstStore.Stat(g.TargetKey)
		if err != nil {
			return nil, fmt.Errorf("检查目标对象 %q 失败：%w", g.TargetKey, err)
		}
		step := ObjectStep{Group: g, CompanyID: cid, Src: src, Dst: dst}
		switch {
		case !dst.Exists:
			step.Action = ObjCopy
		case dst.Size == src.Size:
			step.Action = ObjSkip
		case opt.Force:
			step.Action = ObjOverwrite
		default:
			return nil, fmt.Errorf("%w：目标 %q 已存在 %d 字节，源 %q 是 %d 字节；"+
				"确认是上次复制到一半的残件后加 --force 覆盖，否则人工检查",
				ErrObjectConflict, g.TargetKey, dst.Size, g.SourceKey, src.Size)
		}
		pfOut.Objects = append(pfOut.Objects, step)
	}
	return pfOut, nil
}

// Apply 执行搬迁。返回的结果里带完整预检，命令层用它打印 dry-run 报告。
func Apply(db *gorm.DB, pf *PlanFile, stores *Stores, opt ApplyOptions) (*ApplyResult, error) {
	check, err := preflight(db, pf, stores, opt)
	if err != nil {
		return nil, err
	}
	res := &ApplyResult{PlanHash: pf.Hash, Preflight: check}
	for _, st := range check.Objects {
		if st.Action == ObjSkip {
			res.ObjectsSkipped++
		}
	}
	for _, st := range check.Refs {
		if st.Action == RefSkip {
			res.RefsSkipped++
		}
	}
	if opt.DryRun {
		return res, nil
	}

	// ① 复制对象：全部成功才动库。中途失败时库一行未改，重跑会跳过已复制的对象。
	for _, st := range check.Objects {
		if st.Action == ObjSkip {
			continue
		}
		if err := stores.Copy(st.Group.SourceKey, st.Group.TargetKey, st.CompanyID); err != nil {
			return res, fmt.Errorf("复制 %q → %q 失败（库未改动）：%w",
				st.Group.SourceKey, st.Group.TargetKey, err)
		}
		res.ObjectsCopied++
	}

	// ② 改库 + 记台账放在**同一个事务**里：任何一条失败，业务行与台账一起回滚，
	//    不会留下「库改了但没台账、因而无法回滚」的半截状态。
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, st := range check.Refs {
			if st.Action == RefSkip {
				continue
			}
			r := st.R
			if err := writeRef(tx, r, r.Item.TargetKey, r.Item.Raw); err != nil {
				return err
			}
			if st.LedgerID != 0 {
				// 回滚后重跑：复用台账行，不插第二条（唯一键也不允许）。
				if err := tx.Model(&model.StorageBackfillRef{}).Where("id = ?", st.LedgerID).
					Updates(map[string]any{
						"status": StatusApplied, "old_value": r.Item.Raw,
						"new_value": r.Item.TargetKey,
					}).Error; err != nil {
					return fmt.Errorf("更新台账 #%d 失败：%w", st.LedgerID, err)
				}
			} else {
				row := model.StorageBackfillRef{
					PlanHash: pf.Hash, RefID: RefID(r),
					RefTable: r.Table, PKCol: r.Col.PK, PKValue: r.Item.PKValue,
					RefCol: r.Col.Col, ArrayIndex: r.Item.Index,
					OldValue: r.Item.Raw, NewValue: r.Item.TargetKey,
					CompanyID: r.Item.CompanyID, Status: StatusApplied,
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("记账 %s.%s#%d 失败：%w", r.Table, r.Col.Col, r.Item.PKValue, err)
				}
			}
			res.RefsUpdated++
		}
		return nil
	})
	if err != nil {
		// 事务回滚了，计数也跟着撤：否则报告会宣称「改了 N 行」而库里一行没动。
		res.RefsUpdated = 0
		return res, err
	}
	return res, nil
}

// Rollback 把一批搬迁改回去。**对象只复制不删除**（R5）：搬过去的对象留着，
// 下次再切回租户桶时还能用；删掉它才是真正不可逆的一步。
func Rollback(db *gorm.DB, pf *PlanFile, dryRun bool) (*RollbackResult, error) {
	// 回滚不要求「计划里没有异常条目」：异常条目本来就没被 apply 过，
	// 台账里也不会有它们。但计划结构仍要校验，防止拿错文件。
	if err := ValidatePlan(pf.Plan, false); err != nil {
		return nil, err
	}
	lookups, err := MigrateLookups(pf.Plan)
	if err != nil {
		return nil, err
	}
	byID := map[string]lookup{}
	for _, r := range lookups {
		byID[RefID(r)] = r
	}
	var rows []model.StorageBackfillRef
	if err := db.Where("plan_hash = ? AND status = ?", pf.Hash, StatusApplied).
		Order("id desc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取台账失败：%w", err)
	}
	res := &RollbackResult{PlanHash: pf.Hash, Total: len(rows)}

	type unit struct {
		row model.StorageBackfillRef
		r   lookup
	}
	units := make([]unit, 0, len(rows))
	for _, row := range rows {
		r, ok := byID[row.RefID]
		if !ok {
			return nil, fmt.Errorf("%w：台账 #%d 的 ref_id=%s 在这份计划里找不到（计划文件换过？）",
				ErrPlanTampered, row.ID, row.RefID)
		}
		if row.OldValue != r.Item.Raw || row.NewValue != r.Item.TargetKey {
			return nil, fmt.Errorf("%w：台账 #%d 与计划不一致", ErrPlanTampered, row.ID)
		}
		cur, err := readRef(db, r)
		if err != nil {
			return nil, err
		}
		if cur != row.NewValue {
			return nil, fmt.Errorf("%w：%s.%s#%d 现在是 %q，不是搬迁后的 %q（有人在搬迁后改过库，不能覆盖）",
				ErrStalePlan, r.Table, r.Col.Col, r.Item.PKValue, cur, row.NewValue)
		}
		units = append(units, unit{row: row, r: r})
	}
	if dryRun {
		return res, nil
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, u := range units {
			if err := writeRef(tx, u.r, u.row.OldValue, u.row.NewValue); err != nil {
				return err
			}
			if err := tx.Model(&model.StorageBackfillRef{}).Where("id = ?", u.row.ID).
				Update("status", StatusRolledBack).Error; err != nil {
				return fmt.Errorf("更新台账 #%d 失败：%w", u.row.ID, err)
			}
			res.Reverted++
		}
		return nil
	})
	if err != nil {
		res.Reverted = 0
		return res, err
	}
	return res, nil
}

type RollbackResult struct {
	PlanHash string
	Total    int // 待回滚的台账行数
	Reverted int // 实际改回去的行数
}

// Status 给出一批计划的台账进度，供 status 子命令与排障用。
func Status(db *gorm.DB, hash string) (map[string]int, int, error) {
	var rows []model.StorageBackfillRef
	if err := db.Where("plan_hash = ?", hash).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("读取回填台账失败：%w", err)
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status]++
	}
	return counts, len(rows), nil
}
