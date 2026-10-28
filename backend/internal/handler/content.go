package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterContent 注册「企业内容管理」写接口（后台登录用户），覆盖：
// 公司信息 / 公司简介 / 业绩分类 / 项目案例 / 业务服务 / 首页布局。
// 多租户：全部按 targetCompanyID 收窄到单一企业——企业管理员固定本企业，
// 平台超管可用 ?companyId= 指定（缺省 1 号租户朗汇，保持既有行为）。
// tencentVideoEnabled 腾讯视频档是否可选：系统设置优先，未设置（空串）回退部署
// 环境变量 MP_TENCENT_VIDEO——与 metrics_enabled 同一套路（见 setting registry）。
//
// 保留环境变量这一路：自动化部署 / 安装向导可以在无人值守时把默认形态一次设好；
// 界面一旦显式设过就以界面为准，运维不用为了开一个功能去登服务器改配置再重启。
func tencentVideoEnabled(cfg config.Config) bool {
	return setting.GetBool("video_tencent_enabled", cfg.TencentVideo)
}

// tencentDisabledReason 腾讯视频档不可选时给界面看的理由。区分「界面显式关掉」与
// 「从未设置过」：前者再说「去系统设置打开」就够了，后者还要补一条部署侧的改法，
// 否则只看界面的运维不知道还能从环境变量那里设默认值。
func tencentDisabledReason(cfg config.Config) string {
	raw := strings.TrimSpace(setting.GetString("video_tencent_enabled", ""))
	if raw != "" {
		return "腾讯视频档已在「系统设置 → 功能开关」关闭，开启后本档才可选"
	}
	return "腾讯视频档未启用：请在「系统设置 → 功能开关」开启「腾讯视频档」，或在部署环境设环境变量 MP_TENCENT_VIDEO=true（还需先在小程序后台添加并授权腾讯视频插件、在 app.json 声明）"
}

func RegisterContent(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	// ===== 视频来源能力探测 =====
	// 目前只有「腾讯视频」一档需要开关：它依赖小程序侧静态声明插件，未授权时整个
	// 小程序编译失败（见 config.TencentVideo 与 setting registry 的说明）。界面据此
	// 禁用选项并原样展示原因，别让客户配完才在真机上发现播不出来。
	r.GET("/api/admin/video-capability", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		on := tencentVideoEnabled(cfg)
		reason := ""
		if !on {
			reason = tencentDisabledReason(cfg)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"tencentVideo":  on,
			"tencentReason": reason,
		}})
	})

	// ===== 公司基础信息（固定展示，按租户） =====
	r.GET("/api/admin/company-info", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var ci model.CompanyInfo
		if db.First(&ci, "company_id = ?", cid).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": model.CompanyInfo{CompanyID: cid}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": ci})
	})
	r.PUT("/api/admin/company-info", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var req struct {
			FullName  string `json:"fullName"`
			ShortName string `json:"shortName"`
			Address   string `json:"address"`
			Tel       string `json:"tel"`
			Logo      string `json:"logo"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var ci model.CompanyInfo
		if db.First(&ci, "company_id = ?", cid).Error != nil {
			ci = model.CompanyInfo{CompanyID: cid, FullName: req.FullName, ShortName: req.ShortName, Address: req.Address, Tel: req.Tel, Logo: req.Logo}
			db.Create(&ci)
		} else {
			db.Model(&ci).Updates(map[string]interface{}{"full_name": req.FullName, "short_name": req.ShortName, "address": req.Address, "tel": req.Tel, "logo": req.Logo})
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": ci})
	})

	// ===== 公司简介 + 视频（按租户） =====
	r.GET("/api/admin/company-profile", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var cp model.CompanyProfile
		if db.First(&cp, "company_id = ?", cid).Error != nil {
			cp = model.CompanyProfile{CompanyID: cid, VideoSource: model.VideoSourceNone}
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": cp})
			return
		}
		// 该列后加，历史行是空串；归一成 none 免得前端下拉框落到未选中态
		if cp.VideoSource == "" {
			cp.VideoSource = model.VideoSourceNone
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": cp})
	})
	r.PUT("/api/admin/company-profile", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var req struct {
			IntroText        string `json:"introText"`
			VideoSource      string `json:"videoSource"`
			VideoChannelID   string `json:"videoChannelId"`
			VideoFeedID      string `json:"videoFeedId"`
			VideoFeedToken   string `json:"videoFeedToken"`
			VideoVid         string `json:"videoVid"`
			VideoURL         string `json:"videoUrl"`
			VideoFallbackURL bool   `json:"videoFallbackUrl"`
			Cover            string `json:"cover"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if !model.IsValidVideoSource(req.VideoSource) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "视频来源取值非法"})
			return
		}
		if req.VideoSource == "" {
			req.VideoSource = model.VideoSourceNone
		}
		// 腾讯视频档：插件须在 app.json 静态声明，未授权会让整个小程序编译失败，
		// 故默认关闭、由运维在系统设置（或部署环境变量 MP_TENCENT_VIDEO）显式开启。
		// 服务端必须拦——前端只是把选项置灰，直接调接口照样能绕过。
		if req.VideoSource == model.VideoSourceTencent {
			if !tencentVideoEnabled(cfg) {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "腾讯视频档未启用：请先在「系统设置 → 功能开关」开启「腾讯视频档」"})
				return
			}
			// vid 是这一档唯一的取片凭证，空着等于配了个永远播不出来的档
			if strings.TrimSpace(req.VideoVid) == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "腾讯视频档需要填写 vid"})
				return
			}
			req.VideoVid = strings.TrimSpace(req.VideoVid)
		}
		var cp model.CompanyProfile
		fields := map[string]interface{}{
			"intro_text":         req.IntroText,
			"video_source":       req.VideoSource,
			"video_channel_id":   req.VideoChannelID,
			"video_feed_id":      req.VideoFeedID,
			"video_feed_token":   req.VideoFeedToken,
			"video_vid":          req.VideoVid,
			"video_url":          req.VideoURL,
			"video_fallback_url": req.VideoFallbackURL,
			"cover":              req.Cover,
		}
		if db.First(&cp, "company_id = ?", cid).Error != nil {
			cp = model.CompanyProfile{
				CompanyID:        cid,
				IntroText:        req.IntroText,
				VideoSource:      req.VideoSource,
				VideoChannelID:   req.VideoChannelID,
				VideoFeedID:      req.VideoFeedID,
				VideoFeedToken:   req.VideoFeedToken,
				VideoVid:         req.VideoVid,
				VideoURL:         req.VideoURL,
				VideoFallbackURL: req.VideoFallbackURL,
				Cover:            req.Cover,
			}
			db.Create(&cp)
		} else {
			db.Model(&cp).Updates(fields)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": cp})
	})

	// ===== 业绩分类（按租户） =====
	r.GET("/api/admin/categories", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var list []model.AchievementCategory
		db.Where("company_id = ?", targetCompanyID(c)).Order("sort asc").Find(&list)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": list})
	})
	r.POST("/api/admin/categories", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var req model.AchievementCategory
		if c.ShouldBindJSON(&req) != nil || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		req.CompanyID = targetCompanyID(c) // 租户外键以服务端解析为准，不信客户端
		db.Create(&req)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": req})
	})
	registerSortRoute(r, db, "/api/admin/categories/sort", &model.AchievementCategory{}, targetCompanyID)
	r.PUT("/api/admin/categories/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Name     string `json:"name"`
			Icon     string `json:"icon"`
			Sort     int    `json:"sort"`
			IsLocked bool   `json:"isLocked"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 租户条件进 Where：企业管理员改不到别家分类（RowsAffected=0 即越权）
		if db.Model(&model.AchievementCategory{}).Where("id = ? AND company_id = ?", id, targetCompanyID(c)).
			Updates(map[string]interface{}{"name": req.Name, "icon": req.Icon, "sort": req.Sort, "is_locked": req.IsLocked}).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
	r.DELETE("/api/admin/categories/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		cid := targetCompanyID(c)
		var cat model.AchievementCategory
		if db.First(&cat, "id = ? AND company_id = ?", id, cid).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		// 强校验：分类下存在项目则禁止删除（避免产生孤立数据，按租户统计）
		var pcnt int64
		db.Model(&model.ProjectCase{}).Where("category_id = ? AND company_id = ?", id, cid).Count(&pcnt)
		if pcnt > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该分类下存在项目，无法删除"})
			return
		}
		if cat.IsLocked {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "锁定分类不可删除"})
			return
		}
		db.Delete(&cat)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// ===== 项目案例（按租户） =====
	r.GET("/api/admin/projects", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		catID, _ := strconv.ParseInt(c.Query("categoryId"), 10, 64)
		q := db.Where("company_id = ?", cid).Order("sort asc")
		if catID > 0 {
			q = q.Where("category_id = ?", catID)
		}
		var list []model.ProjectCase
		q.Find(&list)
		data := make([]gin.H, 0, len(list))
		for _, p := range list {
			data = append(data, projectToMap(p))
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
	})
	r.POST("/api/admin/projects", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		in, ok := bindProject(c)
		if !ok {
			return
		}
		alb, _ := json.Marshal(in.Album)
		p := model.ProjectCase{
			CompanyID: targetCompanyID(c), CategoryID: in.CategoryID, Name: in.Name, Summary: in.Summary, Detail: in.Detail,
			Cover: in.Cover, Album: string(alb), IsHomeFeatured: in.IsHomeFeatured,
			Enabled: in.Enabled, Sort: in.Sort,
		}
		db.Create(&p)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": projectToMap(p)})
	})
	registerSortRoute(r, db, "/api/admin/projects/sort", &model.ProjectCase{}, targetCompanyID)
	r.PUT("/api/admin/projects/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		in, ok := bindProject(c)
		if !ok {
			return
		}
		var p model.ProjectCase
		if db.First(&p, "id = ? AND company_id = ?", id, targetCompanyID(c)).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		alb, _ := json.Marshal(in.Album)
		db.Model(&p).Updates(map[string]interface{}{
			"category_id": in.CategoryID, "name": in.Name, "summary": in.Summary,
			"detail": in.Detail, "cover": in.Cover, "album": string(alb),
			"is_home_featured": in.IsHomeFeatured, "enabled": in.Enabled, "sort": in.Sort,
		})
		db.First(&p, "id = ?", id)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": projectToMap(p)})
	})
	r.DELETE("/api/admin/projects/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if db.Delete(&model.ProjectCase{}, "id = ? AND company_id = ?", id, targetCompanyID(c)).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// ===== 业务服务（按租户） =====
	r.GET("/api/admin/services", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var list []model.BizService
		db.Where("company_id = ?", targetCompanyID(c)).Order("sort asc").Find(&list)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": list})
	})
	r.POST("/api/admin/services", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var req struct {
			Title   string `json:"title"`
			Icon    string `json:"icon"`
			Content string `json:"content"`
			Sort    int    `json:"sort"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Title == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		s := model.BizService{CompanyID: targetCompanyID(c), Title: req.Title, Icon: req.Icon, Content: req.Content, Sort: req.Sort}
		db.Create(&s)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": s})
	})
	registerSortRoute(r, db, "/api/admin/services/sort", &model.BizService{}, targetCompanyID)
	r.PUT("/api/admin/services/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Title   string `json:"title"`
			Icon    string `json:"icon"`
			Content string `json:"content"`
			Sort    int    `json:"sort"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if db.Model(&model.BizService{}).Where("id = ? AND company_id = ?", id, targetCompanyID(c)).
			Updates(map[string]interface{}{"title": req.Title, "icon": req.Icon, "content": req.Content, "sort": req.Sort}).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
	r.DELETE("/api/admin/services/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if db.Delete(&model.BizService{}, "id = ? AND company_id = ?", id, targetCompanyID(c)).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// ===== 首页布局（板块顺序 + 首页背景，按租户） =====
	r.GET("/api/admin/home-layout", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var hl model.HomeLayout
		order := []string{}
		var bgImage string
		var bgMask int
		if db.First(&hl, "company_id = ?", targetCompanyID(c)).Error == nil {
			if hl.Order != "" {
				_ = json.Unmarshal([]byte(hl.Order), &order)
			}
			bgImage, bgMask = hl.BgImage, hl.BgMask
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"order": order, "bgImage": bgImage, "bgMask": bgMask,
		}})
	})
	r.PUT("/api/admin/home-layout", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var req struct {
			Order []string `json:"order"`
			// 指针区分「未提供」（保持原值）与「传空串」（清空背景）——
			// 前端分块保存时可能只提交板块顺序，不能把背景顺手清掉。
			BgImage *string `json:"bgImage"`
			BgMask  *int    `json:"bgMask"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		b, _ := json.Marshal(req.Order)
		var hl model.HomeLayout
		if db.First(&hl, "company_id = ?", cid).Error != nil {
			created := model.HomeLayout{CompanyID: cid, Order: string(b)}
			if req.BgImage != nil {
				created.BgImage = *req.BgImage
			}
			if req.BgMask != nil {
				created.BgMask = clampMask(*req.BgMask)
			}
			db.Create(&created)
		} else {
			upd := map[string]interface{}{"order_col": string(b)}
			if req.BgImage != nil {
				upd["bg_image"] = *req.BgImage
			}
			if req.BgMask != nil {
				upd["bg_mask"] = clampMask(*req.BgMask)
			}
			db.Model(&hl).Updates(upd)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}

// clampMask 把遮罩浓度钳制到 0~100（前端滑块范围一致，服务端兜底防越界写入）。
func clampMask(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// registerSortRoute 注册「批量排序」接口，项目案例 / 业绩分类 / 业务服务共用。
//
// 请求体为 [{id, sort}]，在单个事务内逐条更新，避免前端拖拽或上下箭头
// 调整顺序时中途失败留下「部分生效」的中间态。
//
// companyID 在请求时解析目标租户（见 targetCompanyID）：排序更新同样带
// company_id 条件，企业管理员排不到别家的数据。
//
// ⚠️ 调用位置有约束：本函数注册的静态 /sort 路由必须排在同资源的
// PUT /xxx/:id 之前注册，否则 "sort" 会被 :id 参数路由匹配吞掉。
func registerSortRoute(r *gin.Engine, db *gorm.DB, path string, dest interface{}, companyID func(*gin.Context) int64) {
	r.PUT(path, middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var items []struct {
			ID   int64 `json:"id"`
			Sort int   `json:"sort"`
		}
		if c.ShouldBindJSON(&items) != nil || len(items) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		cid := companyID(c)
		tx := db.Begin()
		if tx.Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "数据库错误"})
			return
		}
		for _, it := range items {
			if it.ID <= 0 {
				continue
			}
			if err := tx.Model(dest).Where("id = ? AND company_id = ?", it.ID, cid).
				Update("sort", it.Sort).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "更新失败"})
				return
			}
		}
		if err := tx.Commit().Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "提交失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}

// bindProject 解析项目案例的写请求体（album 为字符串数组，映射至 JSON 列）
func bindProject(c *gin.Context) (projectInput, bool) {
	var in projectInput
	if c.ShouldBindJSON(&in) != nil || in.Name == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
		return in, false
	}
	return in, true
}

type projectInput struct {
	CategoryID     int64    `json:"categoryId"`
	Name           string   `json:"name"`
	Summary        string   `json:"summary"`
	Detail         string   `json:"detail"`
	Cover          string   `json:"cover"`
	Album          []string `json:"album"`
	IsHomeFeatured bool     `json:"isHomeFeatured"`
	Enabled        bool     `json:"enabled"`
	Sort           int      `json:"sort"`
}

// projectToMap 把模型转为含 album 数组的响应结构（模型 Album 为 json:"-"）
func projectToMap(p model.ProjectCase) gin.H {
	var album []string
	if p.Album != "" {
		_ = json.Unmarshal([]byte(p.Album), &album)
	}
	return gin.H{
		"id":             p.ID,
		"categoryId":     p.CategoryID,
		"name":           p.Name,
		"summary":        p.Summary,
		"detail":         p.Detail,
		"cover":          p.Cover,
		"album":          album,
		"isHomeFeatured": p.IsHomeFeatured,
		"enabled":        p.Enabled,
		"sort":           p.Sort,
		"createdAt":      p.CreatedAt,
	}
}
