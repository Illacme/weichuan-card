package handler

import (
	"net/http"
	"path"
	"strconv"
	"strings"

	"weichuan/internal/assets"
	"weichuan/internal/middleware"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterAssets 注册「常用开源素材库」接口：
//
//	GET  /api/assets/file/*path    素材预览图。公开 + 长缓存：素材是通用图形、
//	                                不含租户数据；<img> 标签也无法带 Authorization 头，
//	                                公开可避免任何部署形态（同源/跨域/CDN）下的预览问题。
//	GET  /api/assets/posters       海报素材清单（公开：员工在小程序里挑分享背景用）
//	GET  /api/admin/assets/library 素材清单（管理员登录态）
//	POST /api/admin/assets/adopt   采用素材：把内嵌素材复制进上传存储，返回与普通
//	                                上传完全同构的 URL（管理员登录态）
//
// 采用（adopt）而非直接引用素材库 URL 的原因：产物落进上传存储后，
// URL / CDN / 下游消费（小程序 <image>）与普通上传零差异，不引入新的依赖域。
//
// refresher 可选：采用路径是固定对象名（library/<素材ID>.<ext>），重复采用 = 覆盖写，
// 这是全系统少数几个「同名不同内容」的场景（素材库升级换了图），覆盖后必须让 CDN
// 边缘失效——2026-10-23 之前刷新只挂在上传接口（对象名随机、永不覆盖），等于没接线。
func RegisterAssets(r *gin.Engine, db *gorm.DB, up storage.Uploader, refresher storage.CDNRefresher) {
	r.GET("/api/assets/file/*path", assetFileHandler)
	r.GET("/api/assets/posters", posterListHandler)
	r.GET("/api/admin/assets/library", middleware.AuthMiddleware(db, "admin"), libraryListHandler)
	r.POST("/api/admin/assets/adopt", middleware.AuthMiddleware(db, "admin"), assetAdoptHandler(up, refresher))
}

// assetFileHandler 提供素材预览图（选择器缩略图 + 原图）。
func assetFileHandler(c *gin.Context) {
	rel := strings.TrimPrefix(c.Param("path"), "/")
	// 双保险：显式拒绝穿越路径（assets.ReadFile 与 io/fs 语义本身也会拒）
	if rel == "" || path.IsAbs(rel) || strings.Contains(rel, "..") {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "not found"})
		return
	}
	data, err := assets.ReadFile(rel)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "not found"})
		return
	}
	// 素材不可变：长缓存 + immutable，CDN 边缘可长期持有
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, assets.ContentType(rel), data)
}

// libraryListHandler 返回素材清单（分页 + 分类/调性/关键字过滤，管理员登录态）。
//
// 查询参数：page（缺省 1）/ size（缺省 24，上限 60）/ type（用途分类，空=全部）/
// style（视觉调性，空=全部）/ keyword（名称或标签匹配）。
//
// 两个筛选维度正交：type 是「用在哪」（图标/封面/LOGO/头像/背景），
// style 是「什么气质」（简约清晰/温暖自然/轻奢尊贵/柔美清新/光影流动）。
// 同时返回 styleCounts——当前 分类+关键字 下各调性的素材数，前端据此隐藏
// 0 结果的调性（例如图标分类下只有「简约清晰」，就没必要把五档全列出来）。
func libraryListHandler(c *gin.Context) {
	m, err := assets.Load()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "素材库加载失败"})
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("size")) // 解析失败→0，由 List 回落默认值
	res := assets.List(assets.Query{
		Page:    page,
		Size:    size,
		Type:    c.Query("type"),
		Style:   c.Query("style"),
		Keyword: c.Query("keyword"),
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"version":     m.Version,
		"categories":  m.Categories,
		"styles":      m.Styles,
		"items":       res.Items,
		"total":       res.Total,
		"page":        res.Page,
		"size":        res.Size,
		"pages":       res.Pages,
		"styleCounts": res.StyleCounts,
	}})
}

// posterListHandler 返回海报素材清单（公开端点，供小程序「选海报背景」用）。
//
// 为什么单开一个端点而不是放开 /api/admin/assets/library：后者带管理员登录态，
// 且会返回全量 900+ 素材（含图标配色变体），给小程序用既越权也浪费流量。
// 这里只回 poster 分类，字段也压到小程序真正需要的四五个。
//
// 素材是通用的、不含租户数据，公开无信息泄露风险；海报背景由分享者本人挑选，
// 也不需要鉴权（未登录的访客打开别人分享的海报页同样要能换背景再看）。
func posterListHandler(c *gin.Context) {
	m, err := assets.Load()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "素材库加载失败"})
		return
	}
	// MaxPageSize(60) 足够覆盖当前 44 张（24 节气 + 20 场景）；海报不分页，
	// 小程序一次拉全后在本地横滑选择，省掉翻页交互。
	res := assets.List(assets.Query{Type: "poster", Page: 1, Size: assets.MaxPageSize})
	items := make([]gin.H, 0, len(res.Items))
	for _, it := range res.Items {
		thumb := it.Thumb
		if thumb == "" {
			thumb = it.File // 清单里 thumb 缺省时退回原图，别给前端一个空 src
		}
		items = append(items, gin.H{
			"id":    it.ID,
			"name":  it.Name,
			"style": it.Style,
			// file 是内嵌素材路径（posters/xx.jpg），统一补成可直取的 URL；
			// 小程序再过 assetUrl 拼成绝对地址（canvas 的 img.src 不认相对路径）。
			"file":  "/api/assets/file/" + it.File,
			"thumb": "/api/assets/file/" + thumb,
		})
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"version": m.Version,
		"items":   items,
	}})
}

// assetAdoptHandler 采用素材：复制进上传存储并返回 URL。
func assetAdoptHandler(up storage.Uploader, refresher storage.CDNRefresher) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ID string `json:"id"`
		}
		if c.ShouldBindJSON(&req) != nil || req.ID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		item, err := assets.Find(req.ID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "素材不存在"})
			return
		}
		data, err := assets.ReadFile(item.File)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "素材读取失败"})
			return
		}
		// 采用路径含素材 ID：重复采用同一素材为幂等覆盖（内容不变，还省存储）
		rel := "library/" + item.ID + item.File[strings.LastIndex(item.File, "."):]
		url, err := up.SaveBytes(rel, data, assets.ContentType(item.File))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "采用失败：" + err.Error()})
			return
		}
		// 固定对象名 = 覆盖写：这里才是「同名可能被换成新内容」的场景（素材库升级换图），
		// 必须让 CDN 边缘失效，否则已经采用过该素材的页面会继续拿到旧图
		refreshCDN(refresher, url)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"url": url,
			"item": gin.H{
				"id": item.ID, "name": item.Name, "category": item.Category,
			},
		}})
	}
}
