package handler

import (
	"encoding/json"
	"log"
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
			if err := db.Create(&ci).Error; err != nil {
				dbWriteFailed(c, "保存公司基础信息（新建）", "保存失败，请稍后重试", err)
				return
			}
		} else {
			if err := db.Model(&ci).Updates(map[string]interface{}{"full_name": req.FullName, "short_name": req.ShortName, "address": req.Address, "tel": req.Tel, "logo": req.Logo}).Error; err != nil {
				dbWriteFailed(c, "保存公司基础信息（更新）", "保存失败，请稍后重试", err)
				return
			}
			// GORM 的 map 更新不回写结构体：手工同步，否则回包还是改动前的旧值，
			// 界面「保存后立刻刷新」看起来像没生效（与 company-profile 同一处理）。
			ci.FullName = req.FullName
			ci.ShortName = req.ShortName
			ci.Address = req.Address
			ci.Tel = req.Tel
			ci.Logo = req.Logo
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
			// 自托管 MP4 档的备用地址（主源失败时切换）。channels / tencent 档不用它，
			// 那两档仍把 videoUrl 当兜底源——存量行为保持不变。
			VideoBackupURL string `json:"videoBackupUrl"`
			// 是否自动播放。腾讯视频插件的 autoplay 默认 true，必须显式下传，
			// 否则「后台关掉」在小程序端不生效（见 model.CompanyProfile.VideoAutoplay）。
			VideoAutoplay bool   `json:"videoAutoplay"`
			Cover         string `json:"cover"`
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
		// 先判断有没有行，再把 req 落到结构体上。
		//
		// 为什么不沿用「Updates(fields) 后直接回 cp」：GORM 不会把 map 里的值写回
		// 结构体，客户端拿到的是**改动前**的旧值。自动播放开关这种「保存后立刻看
		// 开关状态」的场景，会被当成「保存没生效」而反复点。
		var cp model.CompanyProfile
		exists := db.First(&cp, "company_id = ?", cid).Error == nil
		cp.CompanyID = cid
		cp.IntroText = req.IntroText
		cp.VideoSource = req.VideoSource
		cp.VideoChannelID = req.VideoChannelID
		cp.VideoFeedID = req.VideoFeedID
		cp.VideoFeedToken = req.VideoFeedToken
		cp.VideoVid = req.VideoVid
		cp.VideoURL = req.VideoURL
		cp.VideoFallbackURL = req.VideoFallbackURL
		cp.VideoBackupURL = req.VideoBackupURL
		cp.VideoAutoplay = req.VideoAutoplay
		cp.Cover = req.Cover

		fields := map[string]interface{}{
			"intro_text":         req.IntroText,
			"video_source":       req.VideoSource,
			"video_channel_id":   req.VideoChannelID,
			"video_feed_id":      req.VideoFeedID,
			"video_feed_token":   req.VideoFeedToken,
			"video_vid":          req.VideoVid,
			"video_url":          req.VideoURL,
			"video_fallback_url": req.VideoFallbackURL,
			"video_backup_url":   req.VideoBackupURL,
			"video_autoplay":     req.VideoAutoplay,
			"cover":              req.Cover,
		}
		if exists {
			// 不判 RowsAffected==0：重复点保存、值没变时 MySQL 返回 0 行受影响，
			// 那不是「没找到」，当成失败会把一次幂等保存报成错误。
			// 但 Error 必须判——否则写库失败也回 code:0，用户以为存上了。
			//
			// err 必须落日志：前端只能看到一句「保存失败」，而最常见的真实原因是
			// **升级时漏同步 migrations/ 目录导致新列不存在**（2026-11-03 的 0035、
			// 2026-11-05 的 0038 都栽在这），MySQL 报 Unknown column。不打出来，
			// 用户和运维只能对着红条猜；msg 保持不回显原始 SQL 错误（不泄露表结构）。
			if err := db.Model(&cp).Updates(fields).Error; err != nil {
				log.Printf("[CONTENT][ERROR] 保存公司简介失败（更新）company=%d err=%v", cid, err)
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
				return
			}
		} else if err := db.Create(&cp).Error; err != nil {
			log.Printf("[CONTENT][ERROR] 保存公司简介失败（新建）company=%d err=%v", cid, err)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
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
		if err := db.Create(&req).Error; err != nil {
			dbWriteFailed(c, "新建业绩分类", "保存失败，请稍后重试", err)
			return
		}
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
		// 「未找到」用带租户条件的查询判定，不用 RowsAffected：重复保存相同值时
		// MySQL 原样 UPDATE 的 RowsAffected=0，据此判「未找到」会把一次幂等保存报成错误。
		// 先查出记录再更新，同时把租户越权拦在这里（查不到即无权或不存在）。
		var cat model.AchievementCategory
		if db.First(&cat, "id = ? AND company_id = ?", id, targetCompanyID(c)).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		if err := db.Model(&cat).Updates(map[string]interface{}{"name": req.Name, "icon": req.Icon, "sort": req.Sort, "is_locked": req.IsLocked}).Error; err != nil {
			dbWriteFailed(c, "更新业绩分类", "保存失败，请稍后重试", err)
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
		res := db.Delete(&cat)
		if res.Error != nil {
			dbWriteFailed(c, "删除业绩分类", "删除失败，请稍后重试", res.Error)
			return
		}
		if res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
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
		if err := db.Create(&p).Error; err != nil {
			dbWriteFailed(c, "新建项目案例", "保存失败，请稍后重试", err)
			return
		}
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
		if err := db.Model(&p).Updates(map[string]interface{}{
			"category_id": in.CategoryID, "name": in.Name, "summary": in.Summary,
			"detail": in.Detail, "cover": in.Cover, "album": string(alb),
			"is_home_featured": in.IsHomeFeatured, "enabled": in.Enabled, "sort": in.Sort,
		}).Error; err != nil {
			dbWriteFailed(c, "更新项目案例", "保存失败，请稍后重试", err)
			return
		}
		// 二次查库不是多余的：GORM 的 map 更新不会把值写回结构体，回包要给出
		// 改动后的新值（含数据库刚刷新的 updated_at），必须重读这一行。
		if err := db.First(&p, "id = ?", id).Error; err != nil {
			dbWriteFailed(c, "重读项目案例", "保存失败，请稍后重试", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": projectToMap(p)})
	})
	r.DELETE("/api/admin/projects/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		res := db.Delete(&model.ProjectCase{}, "id = ? AND company_id = ?", id, targetCompanyID(c))
		if res.Error != nil {
			dbWriteFailed(c, "删除项目案例", "删除失败，请稍后重试", res.Error)
			return
		}
		if res.RowsAffected == 0 {
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
		if err := db.Create(&s).Error; err != nil {
			dbWriteFailed(c, "新建业务服务", "保存失败，请稍后重试", err)
			return
		}
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
		// 与 categories 同理：先按租户查存在性，不用 RowsAffected 判「未找到」——
		// MySQL 原样 UPDATE 的 RowsAffected=0 会把一次幂等保存报成错误。
		var svc model.BizService
		if db.First(&svc, "id = ? AND company_id = ?", id, targetCompanyID(c)).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		if err := db.Model(&svc).Updates(map[string]interface{}{"title": req.Title, "icon": req.Icon, "content": req.Content, "sort": req.Sort}).Error; err != nil {
			dbWriteFailed(c, "更新业务服务", "保存失败，请稍后重试", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
	r.DELETE("/api/admin/services/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		res := db.Delete(&model.BizService{}, "id = ? AND company_id = ?", id, targetCompanyID(c))
		if res.Error != nil {
			dbWriteFailed(c, "删除业务服务", "删除失败，请稍后重试", res.Error)
			return
		}
		if res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// ===== 小程序展示配置（首页板块顺序 + 首页背景 + 详情页时间档位，按租户） =====
	r.GET("/api/admin/home-layout", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var hl model.HomeLayout
		order := []string{}
		var bgImage string
		var bgMask int
		// 没有配置行（新租户 / 从未保存过）时回缺省档位，而不是空串——
		// 前端 radio 拿到空串会一个都不选中，看起来像配置丢了。
		projectMode, serviceMode := model.DefaultTimeMode, model.DefaultTimeMode
		if db.First(&hl, "company_id = ?", targetCompanyID(c)).Error == nil {
			if hl.Order != "" {
				_ = json.Unmarshal([]byte(hl.Order), &order)
			}
			bgImage, bgMask = hl.BgImage, hl.BgMask
			// 存量行（0040 之前建的）列值由迁移默认值补上，理论上不会是空串；
			// 仍过一遍归一，避免脏值让前端渲染出未知档位。
			projectMode = model.NormalizeTimeMode(hl.ProjectTimeMode)
			serviceMode = model.NormalizeTimeMode(hl.ServiceTimeMode)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"order": order, "bgImage": bgImage, "bgMask": bgMask,
			"projectTimeMode": projectMode, "serviceTimeMode": serviceMode,
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
			// 两个时间档位同理用指针：后台可能只改案例那一栏，不能把服务刷成缺省。
			ProjectTimeMode *string `json:"projectTimeMode"`
			ServiceTimeMode *string `json:"serviceTimeMode"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 枚举值非法一律拒绝，不做「静默兜底成 date」：后台是唯一写入方，
		// 存进一个不认识的档位只会让端上渲染出不显示也不报错的空白。
		projectMode, serviceMode := "", ""
		if req.ProjectTimeMode != nil {
			if projectMode = model.NormalizeTimeMode(*req.ProjectTimeMode); projectMode == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "案例时间显示档位无效"})
				return
			}
		}
		if req.ServiceTimeMode != nil {
			if serviceMode = model.NormalizeTimeMode(*req.ServiceTimeMode); serviceMode == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "服务时间显示档位无效"})
				return
			}
		}
		b, _ := json.Marshal(req.Order)
		var hl model.HomeLayout
		if db.First(&hl, "company_id = ?", cid).Error != nil {
			created := model.HomeLayout{
				CompanyID:       cid,
				Order:           string(b),
				ProjectTimeMode: model.DefaultTimeMode,
				ServiceTimeMode: model.DefaultTimeMode,
			}
			if req.BgImage != nil {
				created.BgImage = *req.BgImage
			}
			if req.BgMask != nil {
				created.BgMask = clampMask(*req.BgMask)
			}
			if projectMode != "" {
				created.ProjectTimeMode = projectMode
			}
			if serviceMode != "" {
				created.ServiceTimeMode = serviceMode
			}
			if err := db.Create(&created).Error; err != nil {
				dbWriteFailed(c, "新建首页布局", "保存失败，请稍后重试", err)
				return
			}
		} else {
			upd := map[string]interface{}{"order_col": string(b)}
			if req.BgImage != nil {
				upd["bg_image"] = *req.BgImage
			}
			if req.BgMask != nil {
				upd["bg_mask"] = clampMask(*req.BgMask)
			}
			if projectMode != "" {
				upd["project_time_mode"] = projectMode
			}
			if serviceMode != "" {
				upd["service_time_mode"] = serviceMode
			}
			if err := db.Model(&hl).Updates(upd).Error; err != nil {
				dbWriteFailed(c, "更新首页布局", "保存失败，请稍后重试", err)
				return
			}
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
