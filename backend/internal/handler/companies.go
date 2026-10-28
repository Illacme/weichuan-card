package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/cattemplate"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// defaultShortName 企业简称缺省值：企业管理里未设置简称（留空/纯空白）时，
// 侧边栏品牌名回落为「微传名片」。
const defaultShortName = "微传名片"

// resolveShortName 规整企业简称：去首尾空白；为空则回落缺省值「微传名片」。
func resolveShortName(raw string) string {
	if s := strings.TrimSpace(raw); s != "" {
		return s
	}
	return defaultShortName
}

// validLoginCode 校验登录页白标码格式：小写字母 / 数字 / 连字符，且非空。
// 仅作格式约束；唯一性在写入时另行校验（避免空串在多企业间冲突，故不依赖 DB 唯一索引）。
func validLoginCode(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		// 写成「合法即继续」而不是把三个条件取反否定：后者 staticcheck 会提
		// QF1001（could apply De Morgan's law），而展开的否定式（r < 'a' || r > 'z'）
		// 远比白名单本身难读——白名单要一眼看出「允许什么」。
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// independentLocked 是否开启「独立版企业锁定」（后台「系统设置 → 功能开关 → independent_lock」）。
// 开启后企业管理整体只读：列表 / 管理员列表照常可查，
// 但新建企业、编辑企业、启停企业、新建企业管理员一律拒绝。
func independentLocked() bool {
	return setting.GetBool("independent_lock", false)
}

// writeLocked 企业管理「写操作」的统一门禁：锁定时返回 403 并给出明确原因。
// 放在各写接口开头而非注册期判定——锁定是运行期开关，注册期判定会导致改完还得重启。
func writeLocked(c *gin.Context) bool {
	if !independentLocked() {
		return false
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 403,
		"msg":  "已开启独立版企业锁定（系统设置 independent_lock），企业管理为只读",
	})
	return true
}

// countByCompany 批量统计「某企业下的记录数」：一次 group by 拿到所有目标企业的计数，
// 替代原先逐企业 COUNT 的 N+1 写法。ids 为空时返回空 map（不再查库）。
func countByCompany(db *gorm.DB, tableModel interface{}, ids []int64) map[int64]int64 {
	out := make(map[int64]int64, len(ids))
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		CompanyID int64 `gorm:"column:company_id"`
		Cnt       int64 `gorm:"column:cnt"`
	}
	db.Model(tableModel).
		Select("company_id, count(*) as cnt").
		Where("company_id IN ?", ids).
		Group("company_id").
		Scan(&rows)
	for _, r := range rows {
		out[r.CompanyID] = r.Cnt
	}
	return out
}

// configuredWXCompanies 批量查「已配好租户小程序凭据」的企业 id 集合：一次 IN 查询，
// 不逐行 SELECT。半配置（只填了 AppID）不算配好——取值侧 tenantWXApp 也要求成对，
// 两侧口径必须一致，否则列表显示「已配置」而生成码时仍回落全局小程序。
func configuredWXCompanies(db *gorm.DB, ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		CompanyID int64 `gorm:"column:company_id"`
	}
	// 与 countByCompany 一样不处理 Scan 错误：表未建（迁移未执行）时退化为「全部未配置」，
	// 页面照样能打开，只是没有高亮——比整页报错好。
	db.Model(&model.CompanyWXApp{}).
		Select("company_id").
		Where("company_id IN ? AND app_id <> '' AND app_secret <> ''", ids).
		Scan(&rows)
	for _, r := range rows {
		out[r.CompanyID] = true
	}
	return out
}

// seedCategoryTemplate 给新开通的企业复制一份业绩分类模板（见 internal/cattemplate）。
//
// 三个刻意的选择：
//  1. **icon 留空**：演示数据的分类图标是演示企业上传目录里的文件，干净库（未勾选
//     演示数据）里根本不存在，照抄 URL 只会得到一排裂图。图标由企业管理员在
//     「业绩分类」页自行从素材库挑。
//  2. **is_locked=false**：锁定是给演示租户保设计稿用的（分类不可删除）。客户要按
//     自己行业改名或删掉用不上的分类，锁死等于逼他们另建一家企业——模板的意义是
//     「有骨架可以改」，不是「必须用这八类」。
//  3. **失败只由调用方记日志、不中断事务**：分类缺了页面照样能打开（业绩页空列表），
//     不值得为它把已经建好的企业整体回滚——与三件套种子一致。
func seedCategoryTemplate(tx *gorm.DB, companyID int64) error {
	for _, t := range cattemplate.Default() {
		cat := model.AchievementCategory{
			CompanyID: companyID,
			Name:      t.Name,
			Sort:      t.Sort,
		}
		// 逐条 Create 而不是批量：批量时一条冲突就整批丢失，而这里每条都是独立
		// 可缺的骨架数据，坏一条不该连累其余七条。
		if err := tx.Create(&cat).Error; err != nil {
			return err
		}
	}
	return nil
}

// RegisterCompanies 注册租户（企业）管理接口——仅平台超级管理员可用
// （middleware.PlatformAdminMiddleware：登录 + role=admin + company_id=0）。
// 覆盖：企业列表 / 开通企业（可一并建企业管理员）/ 停用启用 / 企业管理员管理。
// 企业管理员（company_id>0）调用这些接口一律 403，越权改不了别家租户。
func RegisterCompanies(r *gin.Engine, db *gorm.DB) {
	plat := middleware.PlatformAdminMiddleware(db)

	// 当前登录身份（前端 hydration 用：令牌在 httpOnly Cookie 中，JS 读不到 claims）
	r.GET("/api/admin/me", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		adminID := c.GetInt64("staffId")
		var a model.Admin
		if db.First(&a, "id = ?", adminID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		companyName := ""
		companyShortName := ""
		if a.CompanyID > 0 {
			var comp model.Company
			if db.First(&comp, "id = ?", a.CompanyID).Error == nil {
				companyName = comp.Name
				companyShortName = comp.ShortName
			}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"adminId":          a.ID,
			"username":         a.Username,
			"companyId":        a.CompanyID,
			"companyName":      companyName,
			"companyShortName": companyShortName,
			"isPlatform":       a.CompanyID == 0,
			// 独立版企业锁定：前端据此把「企业管理」页切为只读。
			// 后端各写接口也有 writeLocked 拦截——双保险，避免只靠前端隐藏按钮被绕过。
			"independentLock": independentLocked(),
		}})
	})

	// 企业列表（含员工数/管理员数，供平台方总览）
	//
	// 分页 + 关键字（名称/简称）+ 状态筛选，参数见 handler/pagination.go 的文件头约定。
	// 数量统计改为「按当前页 id 批量 group by」：原写法是每行 2 次 COUNT（N+1），
	// 租户一多就是 2N 次查询；现在固定 2 次，与页数无关。
	r.GET("/api/admin/companies", plat, func(c *gin.Context) {
		page, size := pageParams(c)

		q := db.Model(&model.Company{})
		if cond, args := keywordCond(c.Query("keyword"), "name", "short_name"); cond != "" {
			q = q.Where(cond, args...)
		}
		if cond, v := statusCond(c.Query("status"), "enabled", "disabled"); cond != "" {
			q = q.Where(cond, v)
		}

		var comps []model.Company
		total, page := pagedQuery(q, "id asc", &comps, page, size)

		ids := make([]int64, 0, len(comps))
		for _, comp := range comps {
			ids = append(ids, comp.ID)
		}
		empCnt := countByCompany(db, &model.Employee{}, ids)
		adminCnt := countByCompany(db, &model.Admin{}, ids)
		// 租户小程序凭据状态：让平台一眼看出哪些企业还缺 AppID/AppSecret
		// （缺了的企业员工生成海报码时会回落到平台小程序，需要提前发现而不是等客诉）
		wxOK := configuredWXCompanies(db, ids)
		// 存储用量与授权状态：前者是成本/计费口径，后者决定这家企业还能不能继续用
		// （试用到期 → 后台只读）。两者都必须批量取，否则列表一页 20 行就是 40 次查询。
		storeBytes := storageBytesByCompany(db, ids)
		lics := tenantLicensesByCompany(db, ids)
		now := time.Now()

		data := make([]gin.H, 0, len(comps))
		for _, comp := range comps {
			lic := lics[comp.ID]
			exp := ""
			expired := false
			if !lic.ExpiresAt.IsZero() {
				exp = lic.ExpiresAt.Format("2006-01-02")
				expired = now.After(lic.ExpiresAt)
			}
		data = append(data, gin.H{
			"id": comp.ID, "name": comp.Name, "shortName": comp.ShortName, "status": comp.Status,
			"loginCode":      comp.LoginCode,
			"createdAt":      comp.CreatedAt,
			"employeeCount":  empCnt[comp.ID], "adminCount": adminCnt[comp.ID],
			"wxConfigured": wxOK[comp.ID],
			// storageBytes 是「自本功能启用起累计的上传量」，不是磁盘真实占用（见 model.StorageUsage）
			"storageBytes":     storeBytes[comp.ID],
			"licenseExists":    lic.ID > 0,
			"licenseEdition":   lic.Edition,
			"licenseSeats":     lic.Seats,
			"licenseExpiresAt": exp,
			"licenseExpired":   expired,
		})
		}
		pagedOK(c, data, total, page, size)
	})

	// 开通企业：建租户行 + 种子企业内容三件套（公司信息/简介/首页布局）；
	// 可选一并创建企业管理员（adminUsername + adminPassword）。
	// shortName（企业简称）：① 未设置时默认「微传名片」，作为 PC 后台侧边栏品牌名；
	// ② 显式填写的值同时作为公司信息简称的初始值，企业管理员登录后可在
	// 「公司信息」页继续完善（此后仅影响小程序展示）。
	r.POST("/api/admin/companies", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		var req struct {
			Name          string `json:"name"`
			ShortName     string `json:"shortName"`
			LoginCode     string `json:"loginCode"`
			AdminUsername string `json:"adminUsername"`
			AdminPassword string `json:"adminPassword"`
			// SeedCategories 是否复制业绩分类模板（八类骨架）。
			// 不传（nil）= 复制：跨行业客户拿到的是「可改可删的骨架」，比面对一个
			// 全空的业绩页更容易上手；确实一家都不要的，传 false 即可。
			// 用指针而不是 bool，是为了让「没传」和「明确不要」区分开——否则老版本
			// 客户端（不带该字段）会被当成明确不要，静默少掉分类。
			SeedCategories *bool `json:"seedCategories"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 登录白标码（?company=）：填写时才校验格式与唯一性
		if req.LoginCode != "" {
			if !validLoginCode(req.LoginCode) {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录白标码仅限小写字母、数字、连字符"})
				return
			}
			var cnt int64
			db.Model(&model.Company{}).Where("login_code = ?", req.LoginCode).Count(&cnt)
			if cnt > 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录白标码已存在"})
				return
			}
		}
		// 企业管理员二选一：要么都填（建账号），要么都不填（后续再建）
		if (req.AdminUsername == "") != (req.AdminPassword == "") {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "管理员账号与密码需同时填写"})
			return
		}
		if req.AdminUsername != "" {
			var cnt int64
			db.Model(&model.Admin{}).Where("username = ?", req.AdminUsername).Count(&cnt)
			if cnt > 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "管理员用户名已存在"})
				return
			}
		}
		comp := model.Company{Name: req.Name, ShortName: resolveShortName(req.ShortName), LoginCode: req.LoginCode, Status: "enabled", CreatedAt: time.Now()}
		// 企业行 + 种子内容 + 试用授权放在**同一个事务**里。
		//
		// 为什么不各自 Create 了事：B4 的机制是「新租户靠这条试用授权行才受约束」，
		// 一旦企业建成而授权行没写成，这家企业就永远落在"不限制"那一侧——
		// 而且事后没人会知道本该有一条。宁可让这次开通整体失败、让运营重试。
		//
		// 种子内容三件套的 error 仍旧不中断：它们只是"开箱即用的空白模板"，
		// 缺了页面照样能打开（各自都有兜底），不值得为它整体回滚。
		//
		// catErr 单独接出来而不是在事务里打日志：事务一旦回滚，那条 WARN 就是假警报
		// （分类其实没留下，甚至企业也没留下），排查时会被带偏。
		var catErr error
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&comp).Error; err != nil {
				return err
			}
			// 简称种子取开通时显式填写的值，未填则留空——小程序顶栏回落展示企业全称，
			// 不跟随 company.short_name 的缺省值「微传名片」（两者相互独立）
			tx.Create(&model.CompanyInfo{CompanyID: comp.ID, FullName: req.Name, ShortName: strings.TrimSpace(req.ShortName)})
			tx.Create(&model.CompanyProfile{CompanyID: comp.ID})
			tx.Create(&model.HomeLayout{CompanyID: comp.ID, Order: `["achievement","service"]`})
			// 业绩分类模板：零数据版装完后新企业一个分类都没有，企业管理员登录后
			// 业绩页是空的、小程序业绩 Tab 也渲染不出来——必须先建分类才能录案例。
			if req.SeedCategories == nil || *req.SeedCategories {
				catErr = seedCategoryTemplate(tx, comp.ID)
			}
			// 试用授权：license_trial_enabled=false 时是 no-op（回到"不限制"的老行为）
			return license.CreateTrialLicense(tx, comp.ID)
		})
		if err != nil {
			log.Printf("[COMPANY][ERROR] 开通企业失败（已回滚）：%v", err)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建失败"})
			return
		}
		if catErr != nil {
			// 提交之后再报：企业已建成，只是分类不全，运营补建即可（业绩页能正常打开）
			log.Printf("[COMPANY][WARN] 企业 %d 业绩分类模板复制失败（已跳过）：%v", comp.ID, catErr)
		}
		if req.AdminUsername != "" {
			hash, _ := auth.HashPassword(req.AdminPassword)
			if err := db.Create(&model.Admin{CompanyID: comp.ID, Username: req.AdminUsername, PasswordHash: hash}).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业已创建，但管理员创建失败，请重试或手动添加"})
				return
			}
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyCreate, TargetType: "company", TargetID: strconv.FormatInt(comp.ID, 10),
			Detail: map[string]any{"name": comp.Name, "admin": req.AdminUsername},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"id": comp.ID, "name": comp.Name, "shortName": comp.ShortName}})
	})

	// 编辑企业（名称 / 简称）：只改 company 表本身。
	// 简称改后同步侧边栏品牌名（未设置/清空则回落缺省值「微传名片」），但不回写公司信息的简称——
	// 公司信息里的简称是企业管理员在「公司信息」页维护的小程序展示值，两者相互独立。
	r.PUT("/api/admin/companies/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Name      string `json:"name"`
			ShortName string `json:"shortName"`
			LoginCode string `json:"loginCode"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 登录白标码（?company=）：填写时校验格式与唯一性（排除自身）；
		// 留空表示清除该企业的白标码（登录页回落全局品牌）
		if req.LoginCode != "" {
			if !validLoginCode(req.LoginCode) {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录白标码仅限小写字母、数字、连字符"})
				return
			}
			var cnt int64
			db.Model(&model.Company{}).Where("login_code = ? AND id <> ?", req.LoginCode, id).Count(&cnt)
			if cnt > 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "登录白标码已存在"})
				return
			}
		}
		if db.Model(&model.Company{}).Where("id = ?", id).
			Updates(map[string]interface{}{"name": req.Name, "short_name": resolveShortName(req.ShortName), "login_code": req.LoginCode}).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyUpdate, TargetType: "company", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"name": req.Name, "shortName": req.ShortName, "loginCode": req.LoginCode},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 停用 / 启用企业（停用后该企业的管理员与员工均不能登录）
	r.POST("/api/admin/companies/:id/status", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Status string `json:"status"`
		}
		if c.ShouldBindJSON(&req) != nil || (req.Status != "enabled" && req.Status != "disabled") {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if db.Model(&model.Company{}).Where("id = ?", id).
			Update("status", req.Status).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyStatus, TargetType: "company", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"status": req.Status},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 企业管理员列表
	r.GET("/api/admin/companies/:id/admins", plat, func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var list []model.Admin
		db.Where("company_id = ?", id).Order("id asc").Find(&list)
		data := make([]gin.H, 0, len(list))
		for _, a := range list {
			data = append(data, gin.H{"id": a.ID, "username": a.Username, "createdAt": a.CreatedAt})
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
	})

	// 新增企业管理员
	r.POST("/api/admin/companies/:id/admins", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Username == "" || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var comp model.Company
		if db.First(&comp, "id = ?", id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		var cnt int64
		db.Model(&model.Admin{}).Where("username = ?", req.Username).Count(&cnt)
		if cnt > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名已存在"})
			return
		}
		hash, _ := auth.HashPassword(req.Password)
		a := model.Admin{CompanyID: id, Username: req.Username, PasswordHash: hash}
		if err := db.Create(&a).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建失败"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyAdminAdd, TargetType: "admin", TargetID: strconv.FormatInt(a.ID, 10),
			Detail: map[string]any{"username": a.Username, "companyId": id},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"id": a.ID, "username": a.Username}})
	})
}
