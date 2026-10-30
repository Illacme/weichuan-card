package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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

	// 健康检查：按「存活 / 就绪」分离，根路径只做索引页。
	//
	//   GET /healthz/live   存活探针：进程能响应就 200，不碰任何外部依赖
	//   GET /healthz/ready  就绪探针：SELECT 1 探 DB，不可达返回 503
	//   GET /healthz[/]     索引页：列出上面两个端点，恒 200，不做健康判断
	//
	// 为什么要拆（这不只是「更规范」，是线上事故的直接产物）：
	//
	//  1) 语义混用会引发重启风暴。此前只有一个 /healthz，既当存活探针又查 DB。
	//     若被 K8s liveness 使用，DB 抖动一次就判定进程死亡 → 重启 → 新进程起来
	//     DB 仍不可用 → 再判死 → 循环。K8s 官方文档明确要求 liveness 不检查
	//     外部依赖。拆开后：liveness 只看进程，readiness 才看 DB。
	//
	//  2) 根路径必须同时注册带斜杠与不带斜杠两种形态，否则会与 Nginx 形成
	//     301 死循环。Nginx 把「以 / 结尾的 location」当目录建模，请求 /healthz
	//     会被内建的 auto_redirect 301 到 /healthz/；若 Gin 没注册 /healthz/，
	//     它又按 RedirectTrailingSlash 301 回 /healthz —— 两边互踢，直到浏览器
	//     放弃并报 ERR_TOO_MANY_REDIRECTS。线上真实发生过。
	//
	//  3) 子端点（/healthz/live、/healthz/ready）天然免疫上面这个循环：它们的
	//     路径比 Nginx 的反代前缀 /healthz/ 长，不是「父路径」，不触发
	//     auto_redirect。这也是推荐监控改用子端点的原因。
	//
	// HEAD 必须单独注册：Gin 不会用 GET 的处理器兜住 HEAD，不注册就 404，
	// 而多数监控探针用 HEAD。
	healthzLive := func(c *gin.Context) {
		// 存活探针：只确认进程能响应 HTTP。不查 DB、不查磁盘、不查任何依赖——
		// 依赖故障时重启进程没有任何帮助，只会放大故障。
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	healthzReady := func(c *gin.Context) {
		// 就绪探针：DB 不可达即 503，让负载均衡把本实例摘出去，等 DB 恢复再放回。
		// 进程本身是健康的，所以这里不涉及重启，只是暂时不接流量。
		if err := db.Exec("SELECT 1").Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "degraded",
				"db":     "down",
				"error":  err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "db": "up"})
	}
	healthzRoot := func(c *gin.Context) {
		// 纯索引页：只列出可用端点，不做任何健康判断，**恒返回 200**。
		//
		// ⚠️ 这不是探针，不要拿它做告警——DB 挂了它照样 200。
		//    存活看 /healthz/live，就绪看 /healthz/ready。
		//
		// 保留根路径不是为了兼容，是两个实际用途：
		//  1) 发现：人手 curl 或运维排查时先打根路径，一眼看到该用哪个子端点，
		//     不用翻文档；
		//  2) 兜底：Nginx 对「以 / 结尾的 location」有内建 auto_redirect，会把
		//     /healthz 301 到 /healthz/。这里必须有个 200 的处理器接住，
		//     否则与 Gin 的 RedirectTrailingSlash 互踢成死循环。
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"configured": true,
			"endpoints": gin.H{
				"live":  "/healthz/live",
				"ready": "/healthz/ready",
			},
		})
	}
	for _, rt := range []struct {
		path string
		h    gin.HandlerFunc
	}{
		{"/healthz", healthzRoot},
		{"/healthz/", healthzRoot},
		{"/healthz/live", healthzLive},
		{"/healthz/ready", healthzReady},
	} {
		r.GET(rt.path, rt.h)
		r.HEAD(rt.path, rt.h)
	}

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

	// 名片墙（企业全员）：小程序裸启动时的落地页数据源。
	//
	// 为什么需要它：扫体验版二维码 / 直接打开小程序是「裸启动」，微信不会带任何参数，
	// 页面拿不到 staffId。原先那种情况会弹「输入员工编号」的输入框（开发期兜底），
	// 真实访客看到的是一个要求输入数字的弹窗。名片墙给了一个自然的落点：
	// 「这家公司有谁」，点谁看谁的名片。
	//
	// **只输出白名单字段**（staffId / name / title / avatar）：model.Employee 上还挂着
	// Phone / Phone2 / Email / Landline / OpenID，把结构体直接序列化给公开接口，
	// 等于把全员手机号交给任何扫过码的人。这里的 gin.H 是刻意的，不要"简化"成直接返回 list。
	//
	// 分页与搜索（2026-10-30）：?page= &size= &keyword=
	//   - size 缺省取 MaxTeamList(300)，与改造前「一次最多 300」的行为一致，
	//     老版本小程序不带参数时表现不变；
	//   - 新端固定传 size=60 做滚动加载；
	//   - 回包新增 page / size / hasMore / keyword，total 仍是**匹配总数**
	//     （不是当前页条数）——端上要靠它显示「共 N 位同事」与「还有更多」。
	r.GET("/api/team", func(c *gin.Context) {
		cid, ok := publicCompanyID(c, db)
		if !ok {
			return
		}
		page := atoiDefault(c.Query("page"), 1)
		// size 缺省取 MaxTeamList(300)：改造前这个接口一次最多给 300，
		// 老版本小程序不带参数时行为保持不变；新端固定传 size=60 做滚动加载。
		// 超上限一律夹回 300——不夹的话一个 size=100000 就能把响应体拉爆。
		size := atoiDefault(c.Query("size"), repository.MaxTeamList)
		if size <= 0 || size > repository.MaxTeamList {
			size = repository.MaxTeamList
		}
		keyword := strings.TrimSpace(c.Query("keyword"))
		list, total, effPage, err := repository.ListTeamPage(db, cid, keyword, page, size)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		data := make([]gin.H, 0, len(list))
		for _, e := range list {
			data = append(data, gin.H{
				"staffId": e.StaffID,
				"name":    e.Name,
				"title":   e.Title,
				"avatar":  e.Avatar,
			})
		}
		// 企业名 / LOGO 一起下发：名片墙顶部要画公司抬头，分两次请求会让首屏
		// 先闪一下空标题再补上。
		// id 用请求解析出的 cid 而不是 ci.CompanyID：企业信息还没填的新企业
		// 根本没有 company_infos 行，此时 ci 是零值，CompanyID 会是 0——
		// 端上拿 0 去拼「点某人」的 companyId 就会把访客带到别家。
		var ci model.CompanyInfo
		db.First(&ci, "company_id = ?", cid)
		// 落地页策略（谁来决定裸启动去哪个页面）：企业可以在后台选「名片墙」或
		// 「直接进某个员工的名片」，见 handler/mp_entry.go。端上拿到 entry 后若为
		// staff 就 reLaunch 到该员工名片，不再渲染这一页。
		//
		// **这里刻意不设 Cache-Control**：其它公开内容接口都挂了 cdnCache(c, 60)，
		// 但这一条是路由决策——管理员改完设置立刻扫码验证，若被缓存 60 秒，
		// 他会以为设置没生效，然后反复改、反复验，越查越乱。
		entryMode, entryStaffID := resolveMPEntry(db, cid)
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"company": gin.H{
				"id": cid, "fullName": ci.FullName,
				"shortName": ci.ShortName, "logo": ci.Logo,
			},
			"entry": gin.H{"mode": entryMode, "staffId": entryStaffID},
			"data":  data, "total": total,
			// 分页元信息：hasMore 由端上决定「要不要显示加载更多 / 查看全部」，
			// 不要让它自己用 total 与已加载条数去推——搜索场景下 total 是匹配总数，
			// 而 data 是当前页，两者相减会得出错误的剩余数。
			"page": effPage, "size": size,
			"hasMore": effPage*size < int(total),
			"keyword": keyword,
		})
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
