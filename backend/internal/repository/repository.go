package repository

import (
	"strings"
	"time"

	"weichuan/internal/model"

	"gorm.io/gorm"
)

// GetCard 聚合员工 + 企业信息 + 公司简介（首页一次拉齐）。
// 企业信息按员工归属租户取（employee.company_id），天然支持多租户。
func GetCard(db *gorm.DB, staffID int64) (model.Employee, model.CompanyInfo, model.CompanyProfile, error) {
	var e model.Employee
	var c model.CompanyInfo
	var p model.CompanyProfile
	if err := db.First(&e, "staff_id = ?", staffID).Error; err != nil {
		return e, c, p, err
	}
	db.First(&c, "company_id = ?", e.CompanyID) // 企业信息按租户
	db.First(&p, "company_id = ?", e.CompanyID) // 公司简介按租户
	return e, c, p, nil
}

// C 端列表的条数上限与列裁剪（2026-10-19）。
//
// 这些接口都是「一次全量展示」：小程序端没有分页 UI，靠 Limit 给天花板防止
// payload 随内容增长无上限膨胀。列裁剪的收益比条数上限更大——案例的 detail 是
// 富文本、album 是图集 JSON，服务的 content 是富文本，单条就可能几十 KB；
// 而列表页只画封面 + 标题 + 摘要（详情由 /xxx/:id 单独取，不复用列表数据）。
const (
	MaxProjectsList = 200 // 案例列表（含分类筛选）
	MaxFeaturedList = 20  // 首页精选：首页只画得下这么多个
	MaxServicesList = 50  // 业务服务：实际业务量级在个位数到十几个

	// MaxTeamList 名片墙单页条数上限（2026-10-30 起改为**分页**，不再是一次全量）。
	//
	// 历史：这个常量原来是「一次全量展示」的天花板——企业超过 300 人时访客只能
	// 看到前 300 位，且没有任何入口看到剩下的人（team.wxml 只有一句「仅显示前
	// 300 位」）。500 人以上的企业等于不能用，这是产品上的硬伤。
	//
	// 现在：服务端分页，默认一页 TeamPageDefault；这个常量退化为「单页硬上限」，
	// 防止有人传 size=100000 把响应体拉爆。
	MaxTeamList = 300

	// TeamPageDefault 名片墙默认每页条数。
	// 取 60：三列宫格一屏约 12 个，60 是 5 屏，滚动加载的节奏刚好；
	// 再大首屏就白等了，再小则一次滚动要连发好几个请求。
	TeamPageDefault = 60

	// MaxTeamSearchScan 带关键字搜索时最多扫描多少人。
	//
	// 姓名是 PII 密文列（model.Employee.NameEnc），SQL 层只有盲索引能做**等值**
	// 匹配，做不了 LIKE '张%'——而名片墙的搜索框就是要能搜「张」。
	// 所以带关键字时改为「取该企业全员 → AfterFind 解密 → Go 侧模糊过滤 →
	// 分页切片」。这要求一次把全员读进内存，必须给上限：
	// 5000 人 × 约 200B ≈ 1MB，可接受；再大的企业只扫前 5000（见 ListTeamPage 注释）。
	MaxTeamSearchScan = 5000
)

// 列表只取展示所需列；未选中的列为零值（detail 会是空串而不是富文本正文）。
const (
	projectBriefCols = "id, category_id, name, summary, cover"
	serviceBriefCols = "id, title, icon, sort"
	// teamBriefCols 名片墙的列裁剪。**name 必须在里面**：员工姓名是 PII 密文列
	// （见 model.Employee.NameEnc），AfterFind 钩子要靠它解密出明文。裁掉 name
	// 不会报错，只会让宫格上每个人都没有名字——一个 200 却全空白的接口最难查。
	//
	// 这里裁的是**读**的列，写出去的字段由 handler 另行白名单拼装：model.Employee
	// 上还挂着 Phone / Email / Landline / OpenID，直接把结构体序列化给公开接口
	// 等于把全员手机号泄给任何扫过码的人。
	teamBriefCols = "staff_id, company_id, name, title, avatar, status"
)

// ListTeamPage 名片墙：某企业**启用中**的员工，服务端分页 + 关键字搜索。
//
// 存在的理由：小程序裸启动（扫体验版二维码 / 直接打开）时没有任何 staffId 参数，
// 原先会弹「输入员工编号」——那是开发期兜底，访客看不懂。名片墙给访客一个
// 「这家公司有谁」的落点，点谁看谁的名片。
//
// 只统计/返回 enabled：停用员工的名片在 /api/card 里本来就是 code=2，
// 挂在墙上点进去只会得到「该员工名片已停用」，不如不列。
//
// 两条查询路径（为什么不是一条）：
//
//   - 无关键字 → 纯 SQL 分页（Count + Offset/Limit）。快，且走 staff_id 索引。
//   - 有关键字 → 全员拉取（上限 MaxTeamSearchScan）→ 解密 → Go 侧模糊过滤 → 切片。
//     因为姓名落库是密文，SQL 里没法 LIKE；而名片墙的搜索必须支持「张」这种
//     前缀。企业规模在几千人以内时这条路径是毫秒级，换来的是搜索体验正常。
//
// 越界页码夹到末页（与后台分页 pagedQuery 同一语义）：删完数据后停在原页码
// 不该看到空列表，让人以为没数据。
func ListTeamPage(db *gorm.DB, companyID int64, keyword string, page, size int) ([]model.Employee, int64, int, error) {
	const enabledOnly = "company_id = ? AND status = ?"

	if size <= 0 || size > MaxTeamList {
		size = MaxTeamList
	}
	if page < 1 {
		page = 1
	}

	kw := strings.TrimSpace(keyword)
	if kw == "" {
		var total int64
		if err := db.Model(&model.Employee{}).Where(enabledOnly, companyID, "enabled").
			Count(&total).Error; err != nil {
			return nil, 0, 0, err
		}
		pages := int((total + int64(size) - 1) / int64(size))
		if pages < 1 {
			pages = 1
		}
		if page > pages {
			page = pages
		}
		var list []model.Employee
		err := db.Where(enabledOnly, companyID, "enabled").
			Select(teamBriefCols).
			Order("staff_id asc").
			Offset((page - 1) * size).Limit(size).
			Find(&list).Error
		if err != nil {
			return nil, 0, 0, err
		}
		return list, total, page, nil
	}

	var all []model.Employee
	if err := db.Where(enabledOnly, companyID, "enabled").
		Select(teamBriefCols).
		Order("staff_id asc").
		Limit(MaxTeamSearchScan).
		Find(&all).Error; err != nil {
		return nil, 0, 0, err
	}
	// 统一小写做不区分大小写的包含匹配：姓名/职位里有英文（"Amy" / "Sales"）时，
	// 用户不会注意大小写；中文不受 ToLower 影响。
	needle := strings.ToLower(kw)
	matched := make([]model.Employee, 0, 16)
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Name), needle) ||
			strings.Contains(strings.ToLower(e.Title), needle) {
			matched = append(matched, e)
		}
	}

	total := int64(len(matched))
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * size
	if start > len(matched) {
		start = len(matched)
	}
	end := start + size
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], total, page, nil
}

// GetAchievements 分类（按 sort）+ 首页精选项目（按租户隔离）
func GetAchievements(db *gorm.DB, companyID int64) ([]model.AchievementCategory, []model.ProjectCase, error) {
	var cats []model.AchievementCategory
	if err := db.Where("company_id = ?", companyID).Order("sort asc").Find(&cats).Error; err != nil {
		return nil, nil, err
	}
	var featured []model.ProjectCase
	if err := db.Where("company_id = ? AND is_home_featured = ? AND enabled = ?", companyID, true, true).
		Select(projectBriefCols).
		Order("sort asc").Limit(MaxFeaturedList).Find(&featured).Error; err != nil {
		return nil, nil, err
	}
	return cats, featured, nil
}

// ListProjects 案例列表（按租户；categoryID=0 表示全部上架案例）。
// limit<=0 或超过上限时夹到上限：调用方（小程序）不传即用默认量级。
func ListProjects(db *gorm.DB, companyID int64, categoryID int64, limit int) ([]model.ProjectCase, error) {
	var ps []model.ProjectCase
	if limit <= 0 || limit > MaxProjectsList {
		limit = MaxProjectsList
	}
	q := db.Where("company_id = ?", companyID).
		Select(projectBriefCols).
		Order("sort asc").Limit(limit)
	if categoryID > 0 {
		q = q.Where("category_id = ? AND enabled = ?", categoryID, true)
	} else {
		// 未传分类：返回全部上架案例（供公开接口 /api/projects 无 categoryId 时列出全部）
		q = q.Where("enabled = ?", true)
	}
	if err := q.Find(&ps).Error; err != nil {
		return nil, err
	}
	fillCategoryNames(db, ps)
	return ps, nil
}

func GetProject(db *gorm.DB, companyID int64, id int64) (model.ProjectCase, error) {
	var p model.ProjectCase
	err := db.First(&p, "company_id = ? AND id = ? AND enabled = ?", companyID, id, true).Error
	if err != nil {
		return p, err
	}
	// 注意：p 是值，直接传切片再回填改的是切片元素——必须把回填后的元素取回，
	// 否则 handler 里 p.CategoryName 仍是空值（Go 值语义，非指针接收）。
	ps := []model.ProjectCase{p}
	fillCategoryNames(db, ps)
	return ps[0], nil
}

// fillCategoryNames 批量回填 ProjectCase.CategoryName（非持久化字段）：
// 一次 IN 查询取全部分类名，避免 N+1；分类被删/查不到时留空，由前端兜底。
func fillCategoryNames(db *gorm.DB, ps []model.ProjectCase) {
	if len(ps) == 0 {
		return
	}
	ids := make([]int64, 0, len(ps))
	seen := map[int64]bool{}
	for _, p := range ps {
		if p.CategoryID != 0 && !seen[p.CategoryID] {
			ids = append(ids, p.CategoryID)
			seen[p.CategoryID] = true
		}
	}
	if len(ids) == 0 {
		return
	}
	var cats []model.AchievementCategory
	if err := db.Where("id IN ?", ids).Find(&cats).Error; err != nil {
		return // 回填失败不阻断主查询：分类名降级为空
	}
	nameOf := make(map[int64]string, len(cats))
	for _, c := range cats {
		nameOf[c.ID] = c.Name
	}
	for i := range ps {
		ps[i].CategoryName = nameOf[ps[i].CategoryID]
	}
}

// ListServices 业务服务列表（按租户）。列表只取卡片字段：
// content 是富文本正文，只有详情页需要（/api/services/:id 单取）。
func ListServices(db *gorm.DB, companyID int64, limit int) ([]model.BizService, error) {
	var s []model.BizService
	if limit <= 0 || limit > MaxServicesList {
		limit = MaxServicesList
	}
	err := db.Where("company_id = ?", companyID).
		Select(serviceBriefCols).
		Order("sort asc").Limit(limit).Find(&s).Error
	return s, err
}

func GetService(db *gorm.DB, companyID int64, id int64) (model.BizService, error) {
	var s model.BizService
	err := db.First(&s, "company_id = ? AND id = ?", companyID, id).Error
	return s, err
}

func GetHomeLayout(db *gorm.DB, companyID int64) (model.HomeLayout, error) {
	var l model.HomeLayout
	err := db.First(&l, "company_id = ?", companyID).Error
	return l, err
}

// TrackStat 写入埋点事件（仅记点击，不校验真实结果）。
// targetID 为关联业务对象 id（案例 / 服务），无关联对象的事件传 0；
// source 为流量来源（扫码 / 转发 / 搜索 / 海报 / 兜底），仅作统计维度，
// 应由 handler.normalizeSource 归一后传入；进水口的取值校验见
// handler.validateStatTarget。
func TrackStat(db *gorm.DB, staffID int64, eventType string, targetID int64, source string) error {
	ev := model.StatEvent{
		StaffID:   staffID,
		EventType: eventType,
		TargetID:  targetID,
		Source:    source,
		StatDate:  time.Now().Format("2006-01-02"),
	}
	return db.Create(&ev).Error
}
