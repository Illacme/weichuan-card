package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/mptemplate"
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
		// 后台出参也过解析（与公开接口同一口径）：库里是裸键时，编辑页的 logo 预览
		// 需要一个可访问的 URL。ci 是本次查询的局部变量，解析不会写回库。
		ci.ResolveRefs()
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
		// 写入侧归一（第五十九轮）：落库前把资源字段收敛成带归属前缀的裸键。
		// 失败必须放弃写入——收到的值要么属于别家企业（串号），要么是伪协议 / 穿越。
		// 归一结果写回 req，下面的新建 / 更新两条分支共用。
		if v, err := normalizeResourceRef(cfg, cid, "company-info.logo", req.Logo); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "LOGO 地址不合法，请重新上传"})
			return
		} else {
			req.Logo = v
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
		// 已经落库了，这里解析的是**副本**：直接改 ci 再 Save 会把 /uploads/t/5/x.jpg
		// 写回库，形态从裸键退回历史形态且无任何报错（见 model/refs.go 的落地纪律）。
		resp := ci
		resp.ResolveRefs()
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
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
		// cover / videoUrl / videoBackupUrl 三处都可能来自上传，后台预览要用可访问 URL
		cp.ResolveRefs()
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
		// 写入侧归一（第五十九轮）：三个资源字段一起收敛。
		//
		// videoUrl 这里**不按 videoSource 分档**：归一函数本身对「合法外域 URL」
		// 原样保留（RefForeignURL），所以「外部 MP4 直链」档填 CDN 地址照旧能存，
		// 「自托管上传」档填本平台地址则被收敛成裸键。分档判反而要多维护一份
		// 「哪一档算本平台资源」的映射，将来加档必漏。
		for _, f := range []struct {
			name string
			ptr  *string
		}{
			{"company-profile.videoUrl", &req.VideoURL},
			{"company-profile.videoBackupUrl", &req.VideoBackupURL},
			{"company-profile.cover", &req.Cover},
		} {
			v, err := normalizeResourceRef(cfg, cid, f.name, *f.ptr)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "视频或封面地址不合法，请重新上传"})
				return
			}
			*f.ptr = v
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
		resp := cp
		resp.ResolveRefs()
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
	})

	// ===== 业绩分类（按租户） =====
	r.GET("/api/admin/categories", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var list []model.AchievementCategory
		db.Where("company_id = ?", targetCompanyID(c)).Order("sort asc").Find(&list)
		for i := range list {
			list[i].ResolveRefs()
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": list})
	})
	r.POST("/api/admin/categories", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var req model.AchievementCategory
		if c.ShouldBindJSON(&req) != nil || req.Name == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		req.CompanyID = targetCompanyID(c) // 租户外键以服务端解析为准，不信客户端
		// 写入侧归一（第五十九轮）。放在 CompanyID 赋值之后：scope 由 cid 推导，
		// 而 cid 必须以服务端解析的为准，否则客户端传个 companyId 就能把键写到别家名下。
		if v, err := normalizeResourceRef(cfg, req.CompanyID, "category.icon", req.Icon); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图标地址不合法，请重新上传"})
			return
		} else {
			req.Icon = v
		}
		if err := db.Create(&req).Error; err != nil {
			dbWriteFailed(c, "新建业绩分类", "保存失败，请稍后重试", err)
			return
		}
		resp := req
		resp.ResolveRefs()
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
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
		cid := targetCompanyID(c)
		if v, err := normalizeResourceRef(cfg, cid, "category.icon", req.Icon); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图标地址不合法，请重新上传"})
			return
		} else {
			req.Icon = v
		}
		// 「未找到」用带租户条件的查询判定，不用 RowsAffected：重复保存相同值时
		// MySQL 原样 UPDATE 的 RowsAffected=0，据此判「未找到」会把一次幂等保存报成错误。
		// 先查出记录再更新，同时把租户越权拦在这里（查不到即无权或不存在）。
		var cat model.AchievementCategory
		if db.First(&cat, "id = ? AND company_id = ?", id, cid).Error != nil {
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
		cid := targetCompanyID(c)
		in, ok := bindProject(c, cfg, cid)
		if !ok {
			return
		}
		alb, _ := json.Marshal(in.Album)
		p := model.ProjectCase{
			CompanyID: cid, CategoryID: in.CategoryID, Name: in.Name, Summary: in.Summary, Detail: in.Detail,
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
		cid := targetCompanyID(c)
		in, ok := bindProject(c, cfg, cid)
		if !ok {
			return
		}
		var p model.ProjectCase
		if db.First(&p, "id = ? AND company_id = ?", id, cid).Error != nil {
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
		for i := range list {
			list[i].ResolveRefs()
		}
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
		cid := targetCompanyID(c)
		if v, err := normalizeResourceRef(cfg, cid, "service.icon", req.Icon); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图标地址不合法，请重新上传"})
			return
		} else {
			req.Icon = v
		}
		s := model.BizService{CompanyID: cid, Title: req.Title, Icon: req.Icon, Content: req.Content, Sort: req.Sort}
		if err := db.Create(&s).Error; err != nil {
			dbWriteFailed(c, "新建业务服务", "保存失败，请稍后重试", err)
			return
		}
		resp := s
		resp.ResolveRefs()
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
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
		cid := targetCompanyID(c)
		if v, err := normalizeResourceRef(cfg, cid, "service.icon", req.Icon); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图标地址不合法，请重新上传"})
			return
		} else {
			req.Icon = v
		}
		// 与 categories 同理：先按租户查存在性，不用 RowsAffected 判「未找到」——
		// MySQL 原样 UPDATE 的 RowsAffected=0 会把一次幂等保存报成错误。
		var svc model.BizService
		if db.First(&svc, "id = ? AND company_id = ?", id, cid).Error != nil {
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

	// ===== 小程序展示配置（板块顺序 + 首页背景 + 详情页时间档位 + 风格模板，按租户） =====
	r.GET("/api/admin/home-layout", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var hl model.HomeLayout
		order := []string{}
		var bgImage string
		var bgMask int
		// 没有配置行（新租户 / 从未保存过）时回缺省档位，而不是空串——
		// 前端 radio 拿到空串会一个都不选中，看起来像配置丢了。
		projectMode, serviceMode := model.DefaultTimeMode, model.DefaultTimeMode
		styleID, brandColorID := mptemplate.DefaultID, (*string)(nil)
		if db.First(&hl, "company_id = ?", cid).Error == nil {
			if hl.Order != "" {
				_ = json.Unmarshal([]byte(hl.Order), &order)
			}
			bgImage, bgMask = hl.BgImage, hl.BgMask
			// 存量行（0040 之前建的）列值由迁移默认值补上，理论上不会是空串；
			// 仍过一遍归一，避免脏值让前端渲染出未知档位。
			projectMode = model.NormalizeTimeMode(hl.ProjectTimeMode)
			serviceMode = model.NormalizeTimeMode(hl.ServiceTimeMode)
			// 两轴：优先读 style_id / brand_color_id；0042 已回填存量行，
			// style_id 在 MySQL 端与 SQLite AutoMigrate 均有 DEFAULT 'default'，
			// 空串兜底见 effectiveStyleBrand。
			styleID, brandColorID = effectiveStyleBrand(hl)
		}
		// 清单与授权态一并下发：后台不必自己硬编码模板，且能直接把「未开通」
		// 的高级款置灰并说明原因，而不是等保存时才被拒。
		entitled := license.ResolveFor(c.Request.Context(), cid).HasFeature(mptemplate.FeatureAdvanced)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"order": order, "bgImage": bgImage, "bgMask": bgMask,
			"projectTimeMode": projectMode, "serviceTimeMode": serviceMode,
			"styleId": styleID, "brandColorId": brandColorID,
			"templates":        mptemplate.All(),
			"templateEntitled": entitled,
		}})
	})
	r.PUT("/api/admin/home-layout", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		cid := targetCompanyID(c)
		var req struct {
			// Order 也必须是指针：新增「风格模板」页签后，模板页只提交风格两轴，
			// 若仍无条件写 order_col，会把客户配好的板块顺序冲成 "null"——
			// 没人报错、前台只是悄悄回到默认顺序，属于典型的静默数据丢失。
			Order *[]string `json:"order"`
			// 指针区分「未提供」（保持原值）与「传空串」（清空背景）——
			// 前端分块保存时可能只提交板块顺序，不能把背景顺手清掉。
			BgImage *string `json:"bgImage"`
			BgMask  *int    `json:"bgMask"`
			// 两个时间档位同理用指针：后台可能只改案例那一栏，不能把服务刷成缺省。
			ProjectTimeMode *string `json:"projectTimeMode"`
			ServiceTimeMode *string `json:"serviceTimeMode"`
			// 两轴风格 / 色系（P5 起的新写入形态）：styleId 决定布局族、
			// brandColorId 只覆盖颜色（nil = 用该风格默认色）。
			StyleID      *string `json:"styleId"`
			BrandColorID *string `json:"brandColorId"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 写入侧归一（第五十九轮）。bgImage 是 *string：nil 表示「本次不提交背景」，
		// 必须在解引用**之前**判，否则会把「只改板块顺序」的请求当成「清空背景」。
		if req.BgImage != nil {
			v, err := normalizeResourceRef(cfg, cid, "home-layout.bgImage", *req.BgImage)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "背景图地址不合法，请重新上传"})
				return
			}
			req.BgImage = &v
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

		// ===== 风格 / 色系（两轴，P5 起全叉乘）=====
		// styleId 决定布局族，brandColorId 只覆盖颜色；任意风格 × 任意已知色系都合法
		// （4 × 9 = 36 种）。两轴是唯一写入形态，不存在单列 templateId 兼容分支。
		//
		// themeTouched 是「三个页签各存各的」不变量的开关：只有本次请求**显式带了**
		// 任一风格字段，才允许写风格三列。「名片页面样式」页签只提交
		// order/背景/时间档位，若这里无条件写缺省值，会把客户选好的高级风格
		// 静默冲回 default——没人报错、后台与访客所见悄悄不一致。
		themeTouched := req.StyleID != nil || req.BrandColorID != nil
		styleID := mptemplate.DefaultID
		var brandColorID *string
		if req.StyleID != nil {
			if sid := mptemplate.NormalizeStyle(*req.StyleID); sid == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "整体风格无效"})
				return
			} else {
				styleID = sid
			}
		}
		if req.BrandColorID != nil {
			bc := *req.BrandColorID
			if bc != "" {
				if !mptemplate.IsKnownBrandColor(bc) {
					c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "品牌色系无效"})
					return
				}
				// P5：树形归属已废止，这里只留组合合法性兜底（挡未知风格 / 未知色系）。
				// 不能再按「色系属于哪个风格」拒绝，否则 3/4 的组合会被无声砍掉。
				if !mptemplate.IsValidCombo(styleID, &bc) {
					c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "风格与品牌色系组合无效"})
					return
				}
				brandColorID = &bc
			}
			// 显式传空串 = 用该风格默认色（brandColorID 保持 nil）。
		}

		// 授权只卡「新选高级款」这一个动作，不把整个 PUT 挂 RequireFeature：
		// 后者会连首页背景、板块顺序、时间档位一起拦掉，没买高级模板的客户
		// 连基础展示设置都保存不了。
		// 已在用高级款但授权到期时，带着同一个组合保存其它设置仍放行；
		// 切回默认也放行——降级只禁止「新选」，不破坏客户既有配置。
		if themeTouched && mptemplate.IsAdvancedCombo(styleID, brandColorID) {
			var cur model.HomeLayout
			curStyle, curBrand := mptemplate.DefaultID, (*string)(nil)
			if db.First(&cur, "company_id = ?", cid).Error == nil {
				curStyle, curBrand = effectiveStyleBrand(cur)
			}
			changed := curStyle != styleID || !brandEqual(curBrand, brandColorID)
			if changed &&
				!license.ResolveFor(c.Request.Context(), cid).HasFeature(mptemplate.FeatureAdvanced) {
				c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "当前授权未开通高级风格模板，请联系服务商升级"})
				return
			}
		}

		orderJSON := ""
		if req.Order != nil {
			// []string 的 Marshal 没有失败分支（无 chan/func/循环引用），忽略 err 是安全的。
			b, _ := json.Marshal(*req.Order)
			orderJSON = string(b)
		}
		var hl model.HomeLayout
		if db.First(&hl, "company_id = ?", cid).Error != nil {
			// 新行：没显式提交 order 时也要落成合法缺省值，不能写 "null"
			// （端上会把空 id 当成不认识的模板）。风格两轴由 styleID（缺省 DefaultID）
			// / brandColorID 直接写入，不再有单列 templateId 派生。
			createdOrder := orderJSON
			if createdOrder == "" {
				createdOrder = `["achievement","service"]`
			}
			created := model.HomeLayout{
				CompanyID:       cid,
				Order:           createdOrder,
				ProjectTimeMode: model.DefaultTimeMode,
				ServiceTimeMode: model.DefaultTimeMode,
				StyleID:         styleID,
				BrandColorID:    brandColorID,
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
			upd := map[string]interface{}{}
			if orderJSON != "" {
				upd["order_col"] = orderJSON
			}
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
			// 只在本次显式提交了风格字段时才写风格两列：否则只提交 order / 背景 /
			// 时间档位的页签会把客户选好的模板静默冲回 default（见 themeTouched 注释）。
			// style_id / brand_color_id 是存储真值；brand_color_id 是 *string，
			// nil 即写 NULL（用风格默认色）。
			if themeTouched {
				upd["style_id"] = styleID
				upd["brand_color_id"] = brandColorID
			}
			// 三个页签各存各的：只带风格字段的 payload 只改风格三列，
			// 其余展示配置一律不动。upd 为空说明没带任何可写字段，直接回成功即可。
			if len(upd) > 0 {
				if err := db.Model(&hl).Updates(upd).Error; err != nil {
					dbWriteFailed(c, "更新首页布局", "保存失败，请稍后重试", err)
					return
				}
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

// effectiveStyleBrand 返回某行的有效两轴值：优先读 style_id / brand_color_id。
// style_id 在 0042 迁移与 SQLite AutoMigrate 均有 DEFAULT 'default'，存量行已回填，
// 理论上非空；空串兜底按 DefaultID 渲染（视觉与上线前逐字一致），不再依赖已删除的
// template_id 兼容列（见迁移 0043）。
func effectiveStyleBrand(hl model.HomeLayout) (string, *string) {
	if hl.StyleID != "" {
		return hl.StyleID, hl.BrandColorID
	}
	return mptemplate.DefaultID, nil
}

// brandEqual 比较两个 *string 色系是否相等（均 nil 视为相等）。
func brandEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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
// bindProject 解析并归一项目案例入参。
//
// 归一放在这里而不是两个调用点各写一遍：POST 与 PUT 共用这个函数，两处各写一次的话，
// 将来加第三个入口（批量导入 / 复制案例）必然漏，而漏了不会报错——只是那个入口写进
// 库的又是历史形态，半年后查不出来是谁绕过了归一。
//
// 归一失败返回 false 且已经写好响应：cover / album 里出现别家企业的键或伪协议时，
// 放弃整个保存比「把非法那一张丢掉、其余照写」安全——后者会让用户以为图集全存上了。
func bindProject(c *gin.Context, cfg config.Config, companyID int64) (projectInput, bool) {
	var in projectInput
	if c.ShouldBindJSON(&in) != nil || in.Name == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
		return in, false
	}
	cover, err := normalizeResourceRef(cfg, companyID, "project.cover", in.Cover)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "封面地址不合法，请重新上传"})
		return in, false
	}
	album, err := normalizeResourceRefs(cfg, companyID, "project.album", in.Album)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "图集地址不合法，请重新上传"})
		return in, false
	}
	in.Cover, in.Album = cover, album
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
	// 后台出参与公开 /api/projects/:id 同一口径：库里是裸键时也要补回 /uploads/ 前缀，
	// 否则运营在编辑页看到封面/图集破图，而公开页正常——这种「一半好一半坏」最容易被
	// 当成前端 bug 排查半天。p 是入参的值副本，解析不会影响 DB。
	p.ResolveRefs()
	album = model.ResolveResourceRefs(album)
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
		// 后台列表的「更新时间」列靠它：运营要判断的是「哪些案例内容旧了该翻新」，
		// 更新时间比创建时间更贴近这个问题。服务列表直接序列化整个模型，天然带 updatedAt。
		"updatedAt": p.UpdatedAt,
	}
}
