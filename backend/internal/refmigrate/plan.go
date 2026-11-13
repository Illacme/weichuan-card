// Package refmigrate 是 P3 存量回填的**盘点与计划**层：只读，不写库、不搬对象。
//
// 为什么先只做「计划」而不是直接搬：搬迁把一个对象从平台盘复制到客户桶、再把
// 库里的引用改成新键，两个动作**都不是事务性的**（对象存储与 MySQL 无法同事务）。
// 在不知道分布的情况下开跑，等于把不可回滚的操作压在一次运行上。所以先回答三个问题：
//
//  1. 库里到底有多少条历史形态引用，分布在哪些表 / 列；
//  2. 每一条搬完**应该**写成什么键（由 ObjectKeyFor 单点产出，不由本包拼）；
//  3. 有哪些是**异常**（伪协议、路径穿越、A 租户的键出现在 B 字段）——
//     这些必须先人工确认，脚本不该自作主张。
//
// 计划本身可重复执行、零副作用；下一轮的执行器（复制 + 改库 + 台账 + rollback）
// 消费同一份计划，不允许自己再扫一遍库——否则「计划看到的」和「执行改的」会分叉。
package refmigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"weichuan/internal/model"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// ColKind 是列的形态。图集（`project_case.album`）存的是 JSON 数组字符串，
// 按 json tag 扫字段的守门必然漏掉它——它是本包唯一的数组列，单独建模。
type ColKind int

const (
	Single ColKind = iota
	Array
)

// Column 描述一个存资源引用的列。
//
// Model 传模型值而不是表名字符串：表名由 gorm 解析（Employee 的物理表是
// employees、MediaCheck 覆盖成 media_check），在这里抄一遍表名迟早与模型漂移，
// 而漂移的症状是「盘点少了一张表」（表名写错直接 SQL 报错，还算好的；
// 更坏的是写成了一个存在但没人用的表）。
type Column struct {
	Label string // 给人看的名字：报告里用它，不用表名
	Model any
	PK    string // 主键列名（Employee 是 staff_id，其余是 id）
	Col   string // 资源列名
	Kind  ColKind
}

// Inventory 是**全部**存资源引用的列，与 docs/tenant-storage-url-boundaries.md §2
// 及 model/refs.go 的 ResolveRefs 字段集合三方对齐。
//
// 新增资源字段时必须同时改三处（模型 / 出参解析 / 这里），否则这一步的盘点会漏掉它，
// 而漏掉的后果是「那批对象永远留在平台盘上」——P3 之后关掉平台存储就裂图。
func Inventory() []Column {
	return []Column{
		{"员工头像", &model.Employee{}, "staff_id", "avatar", Single},
		{"开通申请头像", &model.OpenApply{}, "id", "avatar", Single},
		{"企业 LOGO", &model.CompanyInfo{}, "id", "logo", Single},
		{"公司简介主视频", &model.CompanyProfile{}, "id", "video_url", Single},
		{"公司简介备用视频", &model.CompanyProfile{}, "id", "video_backup_url", Single},
		{"公司简介封面", &model.CompanyProfile{}, "id", "cover", Single},
		{"业绩分类图标", &model.AchievementCategory{}, "id", "icon", Single},
		{"案例封面", &model.ProjectCase{}, "id", "cover", Single},
		// Album 是 json:"-" 的 JSON 数组字符串，出参侧要靠两处展开点手工解析
		// （TestAlbumExpansionSitesResolved 钉住），搬迁侧同样要单独解。
		{"案例图集", &model.ProjectCase{}, "id", "album", Array},
		{"服务图标", &model.BizService{}, "id", "icon", Single},
		{"首页背景", &model.HomeLayout{}, "id", "bg_image", Single},
		// 送检记录：url 是绝对地址、object_key 是从它反推的对象键。下架时按
		// object_key 覆盖写占位图，所以两者必须一起搬，只搬一个会让下架写到旧位置。
		{"送检原图地址", &model.MediaCheck{}, "id", "url", Single},
		{"送检对象键", &model.MediaCheck{}, "id", "object_key", Single},
	}
}

// Class 是单条引用的归类。**每一种的处置都不同**，所以不是一个 bool 能表达的。
type Class string

const (
	// ClassEmpty 空值。**正常不会出现在计划里**：单值列在 SQL 层就排除了空串，
	// 数组里的空元素在扫描时直接跳过（一个空槽没有对象可搬，列进报告只会稀释真正的条目）。
	// 保留这个归类是为了让「过滤条件被改坏」时冒出来的是一条可见的「空值」，
	// 而不是一条指向空键的「已就绪」——后者会被执行器当成无需处理的正常数据。
	ClassEmpty Class = "empty"
	// ClassTenantReady 已是本人租户的键：库不用改。对象是否已在客户桶里，
	// 本包不判断（那要联网查桶），由执行器在 --probe 时确认。
	ClassTenantReady Class = "tenant-ready"
	// ClassPlatform 平台对象（p/ 前缀）落在租户字段里：既有形态，允许（访客未登录
	// 上传头像就落 p/）。**永不搬家**——它不属于任何租户。
	ClassPlatform Class = "platform"
	// ClassNeedsMigrate 历史形态（无归属前缀）：对象要复制、库要改键。
	ClassNeedsMigrate Class = "migrate"
	// ClassForeignURL 外域直链（video_source=url 那一档）：原样保留。
	ClassForeignURL Class = "foreign-url"
	// ClassForeignTenant 别人的租户键出现在这一行：**串号嫌疑**，必须人工确认。
	ClassForeignTenant Class = "foreign-tenant"
	// ClassInvalid 不安全 / 推不出键：既不搬也不改，只报出来。
	ClassInvalid Class = "invalid"
)

// Item 是一条引用（数组列会拆成多条）。
type Item struct {
	Column    string // Column.Label
	Table     string
	PK        string
	PKValue   int64
	CompanyID int64
	Index     int    // 数组下标；单值列恒 -1
	Raw       string // 库里的原值
	Class     Class
	SourceKey string // 平台侧现有对象键（migrate 时有效）
	TargetKey string // 搬迁后库里应写的键（migrate / tenant-ready 时有效）
	Note      string // 归类理由（invalid / foreign-tenant 必填）
}

// ColumnStat 是一列的盘点结果。
type ColumnStat struct {
	Column string
	Table  string
	Total  int
	Counts map[Class]int
}

// ObjectGroup 按「源键 → 目标键」去重后的搬迁单元。
//
// 同一张图可能被多个字段引用（企业 LOGO 同时出现在 company_info 与某员工头像），
// 执行器必须**只复制一次**，否则 N 倍写入量与 N 倍的失败面。库里的改写仍然是逐条的。
type ObjectGroup struct {
	SourceKey string
	TargetKey string
	Refs      int // 引用条数
}

// Plan 是一次盘点的完整结果。
type Plan struct {
	BaseURL   string
	CompanyID int64 // 0 = 全量
	Items     []Item
	Columns   []ColumnStat
	Counts    map[Class]int
}

// Scan 扫描全库，产出计划。**只读**：不做任何 UPDATE / INSERT，不访问对象存储。
func Scan(db *gorm.DB, opt Options) (*Plan, error) {
	plan := &Plan{BaseURL: opt.BaseURL, CompanyID: opt.CompanyID, Counts: map[Class]int{}}

	for _, col := range Inventory() {
		table, err := tableName(db, col.Model)
		if err != nil {
			return nil, err
		}
		items, err := scanColumn(db, col, table, opt)
		if err != nil {
			return nil, err
		}
		stat := ColumnStat{Column: col.Label, Table: table, Total: len(items), Counts: map[Class]int{}}
		for _, it := range items {
			stat.Counts[it.Class]++
			plan.Counts[it.Class]++
		}
		if stat.Total > 0 {
			plan.Columns = append(plan.Columns, stat)
		}
		plan.Items = append(plan.Items, items...)
	}
	return plan, nil
}

// Options 是扫描参数。
type Options struct {
	// BaseURL 是平台统一入口基址（cfg.UploadBaseURL，形如
	// https://host/uploads）。用来剥绝对 URL；传空串也能工作——
	// storage 的剥离逻辑对「基址不匹配」会退到 `/uploads/` 段匹配。
	BaseURL string
	// CompanyID > 0 时只扫这一家（试点用）。
	CompanyID int64
}

// row 的列名**必须显式标注**：gorm 的默认命名会把 CID 映射成 c_id，
// 于是 Scan 静默扫出 0 —— 症状是「每一条都被判成 company_id 为 0 的非法值」，
// 而 SQL 本身完全正确。
type row struct {
	PK  int64  `gorm:"column:pk"`
	CID int64  `gorm:"column:cid"`
	Val string `gorm:"column:val"`
}

func scanColumn(db *gorm.DB, col Column, table string, opt Options) ([]Item, error) {
	q := db.Model(col.Model).
		Select(col.PK + " AS pk, company_id AS cid, " + col.Col + " AS val").
		Where(col.Col + " IS NOT NULL AND " + col.Col + " <> ''")
	if opt.CompanyID > 0 {
		q = q.Where("company_id = ?", opt.CompanyID)
	}
	var rows []row
	if err := q.Order(col.PK).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("扫描 %s.%s 失败：%w", table, col.Col, err)
	}

	var items []Item
	for _, r := range rows {
		values := []string{r.Val}
		if col.Kind == Array {
			// 图集是 JSON 数组字符串。解析失败**不能当成空**：那会让一条坏数据
			// 从盘点里彻底消失（执行器永远不知道它存在），所以单独报 invalid。
			var list []string
			if err := json.Unmarshal([]byte(r.Val), &list); err != nil {
				items = append(items, Item{
					Column: col.Label, Table: table, PK: col.PK, PKValue: r.PK,
					CompanyID: r.CID, Index: -1, Raw: r.Val, Class: ClassInvalid,
					Note: "JSON 数组解析失败：" + err.Error(),
				})
				continue
			}
			values = list
		}
		for i, v := range values {
			if strings.TrimSpace(v) == "" {
				continue // 图集里的空槽：没有对象可搬
			}
			idx := -1
			if col.Kind == Array {
				idx = i
			}
			items = append(items, classify(col, table, r, idx, v, opt.BaseURL))
		}
	}
	return items, nil
}

func classify(col Column, table string, r row, idx int, raw, baseURL string) Item {
	it := Item{
		Column: col.Label, Table: table, PK: col.PK, PKValue: r.PK,
		CompanyID: r.CID, Index: idx, Raw: raw,
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		it.Class = ClassEmpty
		return it
	}
	if r.CID <= 0 {
		// 这四张表都按 company_id 分租户，0 行说明数据本身有问题：
		// 没有归属就算不出目标键（t/0/...），搬到哪里都是错的。
		it.Class = ClassInvalid
		it.Note = "company_id 为 0：这一行没有租户归属，无法确定搬到哪个桶"
		return it
	}

	plan, err := storage.PlanNormalize(s, baseURL, storage.ScopeTenant, r.CID)
	if err != nil {
		it.Note = err.Error()
		if errors.Is(err, storage.ErrScopeMismatch) {
			it.Class = ClassForeignTenant
		} else {
			it.Class = ClassInvalid
		}
		return it
	}

	switch {
	case plan.Kind == storage.RefForeignURL:
		it.Class = ClassForeignURL
	case plan.Scope == storage.ScopePlatform:
		it.Class = ClassPlatform
		it.TargetKey = plan.Key // 平台键：库里的值就是它，永不改
	case plan.Kind == storage.RefLegacyPath:
		it.Class = ClassNeedsMigrate
		it.SourceKey = plan.LegacyRel
		it.TargetKey = plan.Key
	default:
		it.Class = ClassTenantReady
		it.TargetKey = plan.Key
	}
	return it
}

// Objects 把 migrate 类条目按「源 → 目标」去重，供执行器按对象复制。
// 顺序稳定（按源键排序），便于比对两次盘点的差异。
func (p *Plan) Objects() []ObjectGroup {
	by := map[string]*ObjectGroup{}
	for _, it := range p.Items {
		if it.Class != ClassNeedsMigrate {
			continue
		}
		k := it.SourceKey + "\x00" + it.TargetKey
		if g, ok := by[k]; ok {
			g.Refs++
			continue
		}
		by[k] = &ObjectGroup{SourceKey: it.SourceKey, TargetKey: it.TargetKey, Refs: 1}
	}
	out := make([]ObjectGroup, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceKey < out[j].SourceKey })
	return out
}

// Problems 返回需要人工处理的条目（串号 / 非法）。执行器遇到它们必须**停下**
// 而不是跳过：静默跳过一条串号键，等于把一个安全隐患留在库里且没人知道。
func (p *Plan) Problems() []Item {
	var out []Item
	for _, it := range p.Items {
		if it.Class == ClassForeignTenant || it.Class == ClassInvalid {
			out = append(out, it)
		}
	}
	return out
}

// tableName 用 gorm 自己解析表名，不硬编码（见 Column.Model 注释）。
func tableName(db *gorm.DB, m any) (string, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(m); err != nil {
		return "", fmt.Errorf("解析模型表名失败：%w", err)
	}
	return stmt.Schema.Table, nil
}
