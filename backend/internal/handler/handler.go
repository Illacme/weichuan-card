package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"weichuan/internal/model"
	"weichuan/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// New 构建 Gin 引擎并注册首页相关路由。
// 注意：用 gin.New() + 显式 Recovery，而非 gin.Default()——Default 自带的 Logger
// 会与 middleware.AccessLog 重复输出；访问日志统一由调用方（main / devserver）
// 挂 RequestID + AccessLog 中间件，格式见 middleware/observability.go。
func New(db *gorm.DB) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	// 授权强制层（Phase 2）：把启动期解析出的授权落到运行期——
	// 过期降级只读、新建员工超席位拒建。未解析授权时全部放行，等价于现状。
	r.Use(EnforceLicense(db))

	// 健康检查（就绪探针）：容器编排 / 负载均衡探活用。
	// 注意：这里必须探 DB 连通性，而不是无条件返回 200。若只做存活探测，
	// DB 故障（连接断开 / 实例宕机）时 /healthz 仍为 200，docker-compose 的
	// backend healthcheck（依赖本端点）与 alert_check.sh 的 R1 探活都会误判为
	// "可用"，实际所有 /api 请求已 5xx。改为 DB 不可达时返回 503，让编排的
	// depends_on/service_healthy 与外部探活告警能识别"进程在但不可用"。
	r.GET("/healthz", func(c *gin.Context) {
		if err := db.Exec("SELECT 1").Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "degraded",
				"db":     "down",
				"error":  err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "db": "up"})
	})

	// 聚合名片（员工 + 企业 + 简介）
	r.GET("/api/card", func(c *gin.Context) {
		staffID, _ := strconv.ParseInt(c.Query("staffId"), 10, 64)
		e, ci, cp, err := repository.GetCard(db, staffID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工不存在"})
			return
		}
		if e.Status == "disabled" {
			c.JSON(http.StatusOK, gin.H{"code": 2, "msg": "该员工名片已停用"})
			return
		}
		// video_source 是后加列，历史行是空串；归一成 none，免得每个消费端各写一遍兜底
		if cp.VideoSource == "" {
			cp.VideoSource = model.VideoSourceNone
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "employee": e, "company": ci, "profile": cp})
	})

	// 企业公开信息（多租户分发用）：小程序申请页展示「申请目标企业」、
	// 企业独立版部署时自检配置。companyId 缺省落朗汇；企业不存在/停用返回 code=1。
	r.GET("/api/company", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		var comp model.Company
		if db.First(&comp, "id = ?", cid).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		var ci model.CompanyInfo
		db.First(&ci, "company_id = ?", cid)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"id": comp.ID, "name": comp.Name, "status": comp.Status,
			"shortName": ci.ShortName, "logo": ci.Logo,
		}})
	})

	// 经典业绩：分类 + 精选项目（按租户：?companyId= 缺省朗汇）
	r.GET("/api/achievements", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		cats, featured, err := repository.GetAchievements(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		// 公开只读接口：短缓存给 CDN / 反代用（端上不受益，详见 cdnCache 注释）。
		// 只在成功分支设置——错误响应不该被缓存。
		cdnCache(c, 60)
		c.JSON(http.StatusOK, gin.H{"code": 0, "categories": cats, "featured": featured})
	})

	// 项目列表（按分类，按租户）
	r.GET("/api/projects", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		catID, _ := strconv.ParseInt(c.Query("categoryId"), 10, 64)
		// ?limit= 只做上限保护（缺省与上限见 repository.MaxProjectsList），端上无分页 UI
		limit := limitParam(c, repository.MaxProjectsList, repository.MaxProjectsList)
		ps, err := repository.ListProjects(db, cid, catID, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		cdnCache(c, 60)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": ps})
	})

	// 项目详情（解析 album JSON，按租户）
	r.GET("/api/projects/:id", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		p, err := repository.GetProject(db, cid, id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "案例不存在或已下架"})
			return
		}
		var album []string
		if p.Album != "" {
			_ = json.Unmarshal([]byte(p.Album), &album)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"id":           p.ID,
			"name":         p.Name,
			"summary":      p.Summary,
			"cover":        p.Cover,
			"detail":       p.Detail,
			"album":        album,
			"categoryId":   p.CategoryID,
			"categoryName": p.CategoryName, // 由 repository 回填，前端免二次查询
			"createdAt":    p.CreatedAt,
			"updatedAt":    p.UpdatedAt,
		}})
	})

	// 业务服务列表（按租户）
	r.GET("/api/services", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		limit := limitParam(c, repository.MaxServicesList, repository.MaxServicesList)
		s, err := repository.ListServices(db, cid, limit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		cdnCache(c, 60)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": s})
	})

	// 业务服务详情（按租户）
	r.GET("/api/services/:id", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		s, err := repository.GetService(db, cid, id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "服务不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": s})
	})

	// 首页布局：板块顺序 + 首页背景（按租户）
	//
	// 背景图是可选增强：bg_image 为空即保持原有灰底布局，小程序端据此决定是否
	// 铺背景层与遮罩。bg_mask（0~100）为黑色蒙层浓度，用于压暗花哨背景、
	// 保证白色内容卡片与文字的可读性。
	r.GET("/api/home-layout", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		order := []string{"achievement", "service"}
		var bgImage string
		var bgMask int
		if l, err := repository.GetHomeLayout(db, cid); err == nil {
			if l.Order != "" {
				_ = json.Unmarshal([]byte(l.Order), &order)
			}
			bgImage, bgMask = l.BgImage, l.BgMask
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "order": order, "bgImage": bgImage, "bgMask": bgMask,
		})
	})

	// 埋点上报（公开接口：小程序访客产生，无登录态——也因此是统计数据的
	// 唯一进水口，按「会被刷」设计，限流与校验的取舍见 stat.go 各函数注释）。
	r.POST("/api/stat/track", statTrackLimiter(db).Handler(), func(c *gin.Context) {
		var req struct {
			StaffID   int64  `json:"staffId"`
			EventType string `json:"eventType"`
			TargetID  int64  `json:"targetId"`
			// Source 流量来源（可选）。来自客户端、不可信，落库前必须归一；
			// 不参与任何权限 / 租户判断，只作统计维度（见 stat.go statSourceTypes）。
			Source string `json:"source"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.StaffID <= 0 || !statEventTypes[req.EventType] {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if req.TargetID < 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 员工必须存在：否则任何脚本都能往统计表灌不存在员工的「访问量」，
		// 而后台按员工汇总会照单全收（补 0 只兜「真员工没数据」这一种）。
		// 停用与否不拦——名片页对停用员工根本不展示，走不到这里。
		var emp model.Employee
		if err := db.First(&emp, "staff_id = ?", req.StaffID).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工不存在"})
			return
		}
		// targetId 校验（project_view/service_view 强制「存在 + 同租户」）：
		// 这两个事件的 target 会进后台 TOP 榜的对象名 JOIN，放过一个别家
		// targetId 就把别家案例名泄进了本家榜单
		if err := validateStatTarget(db, req.EventType, req.TargetID, emp.CompanyID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		// source 归一后再落库：空串→unknown，白名单外脏值→other，
		// 保证「来源分布」永远干净（见 stat.go normalizeSource）。
		if err := repository.TrackStat(db, req.StaffID, req.EventType, req.TargetID, normalizeSource(req.Source)); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "上报失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	return r
}
