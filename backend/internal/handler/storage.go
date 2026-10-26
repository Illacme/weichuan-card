package handler

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterStorage 注册存储增强接口：直传预签名 / 私有桶限时签名 / CDN 刷新 / 本地私有文件流
func RegisterStorage(r *gin.Engine, db *gorm.DB, up storage.Uploader, refresher storage.CDNRefresher, cfg config.Config) {
	// 直传预签名（超管 / 员工）：客户端拿 putUrl 后直接 PUT 到对象存储
	r.GET("/api/admin/upload/presign", middleware.AuthMiddleware(db, "admin"), presignHandler(db, up))
	r.GET("/api/employee/upload/presign", middleware.AuthMiddleware(db, "employee"), presignHandler(db, up))
	// 存储能力探测：后台「公司简介 → 视频来源」用它判断「云存储上传」档是否可选
	r.GET("/api/admin/upload/capability", middleware.AuthMiddleware(db, "admin"), capabilityHandler(up, cfg))
	// 私有桶：已存 URL → 限时签名 URL（超管 / 员工）
	r.POST("/api/admin/storage/sign", middleware.AuthMiddleware(db, "admin"), signHandler(up, cfg))
	r.POST("/api/employee/storage/sign", middleware.AuthMiddleware(db, "employee"), signHandler(up, cfg))
	// CDN 刷新（超管）
	r.POST("/api/admin/cdn/refresh", middleware.AuthMiddleware(db, "admin"), cdnRefreshHandler(refresher))
	// 本地私有文件流（仅本地驱动 + 私有模式：文件不通过 /uploads 静态公开，需鉴权后读取）
	if local, ok := up.(*storage.LocalUploader); ok && !cfg.StoragePublic {
		r.GET("/api/storage/file", middleware.AuthMiddleware(db, "admin"), localFileHandler(local))
		r.GET("/api/employee/storage/file", middleware.AuthMiddleware(db, "employee"), localFileHandler(local))
	}
}

// presignHandler 返回预签名 PUT URL + 最终可访问 URL；本地驱动不支持时返回 code=1（前端回退普通上传）
//
// 关于存储配额（B3）：直传由客户端直接 PUT 到对象存储，**后端永远看不到这批字节**，
// 所以只能靠客户端在申请签名时把 size 报上来（UploadField.vue 已带上）。
// 这里做两件事：① size 可信时先判配额，超了直接拒（前端会回退普通上传，那边还会再判一次）；
// ② 签名成功即按 size 记一次用量——**即使随后 PUT 失败也会记**。
// 这个偏向是有意的：不记就完全统计不到对象存储的用量（那配额等于形同虚设），
// 记多了只会让该租户早一点看到"空间不足"，用户可核对、平台可调；反过来则永远查不出谁在占。
func presignHandler(db *gorm.DB, up storage.Uploader) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := up.(storage.Presigner)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "当前存储模式不支持直传预签名，已回退普通上传"})
			return
		}
		ext := strings.TrimPrefix(c.Query("ext"), ".")
		contentType := c.Query("contentType")
		// size 为可选：老客户端不带 → 不计量也不拦（不能因为缺一个可选参数就让上传失败）
		size, _ := strconv.ParseInt(c.Query("size"), 10, 64)
		cid := uploadCompanyID(c)
		if size > 0 {
			if okq, msg := checkStorageQuota(db, cid, size); !okq {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
				return
			}
		}
		key, err := storage.RandName(ext)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		// 签名时长取系统设置（后台「系统设置 → 上传」可改，即时生效）；
		// 下限 1 分钟——设成 0 会让前端一拿链接就过期，等于直传功能整体失效。
		ttl := signTTL("presign_put_ttl_minutes", 15)
		putURL, finalURL, err := p.PresignPut(key, contentType, ttl)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		if size > 0 {
			// 函数内部已按 [STORAGE][WARN] 记日志；这里显式丢弃返回值，
			// 表示「不拦上传」是有意决定（文件已直传成功，回错会让用户重传）。
			_ = addStorageUsage(db, cid, size, 1)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"putUrl": putURL, "url": finalURL, "key": key}})
	}
}

// capabilityHandler 返回当前存储驱动的能力，供后台决定「云存储上传」档能不能选。
//
// 判据：驱动必须是对象存储（非 local），且实现了 Presigner。
// 本地磁盘驱动下上传视频会把文件落在应用服务器上——磁盘占我们的、出网带宽也算我们的，
// 与「客户自己承担存储与流量」的前提相悖；没有预签名则大视频只能经服务器中转，
// 几十上百 MB 走内存拷贝会拖垮服务。两种情况都让界面禁用该档并给出可操作的引导。
func capabilityHandler(up storage.Uploader, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, presign := up.(storage.Presigner)
		driver := cfg.StorageDriver
		if driver == "" {
			driver = "local"
		}
		reason := ""
		switch {
		case driver == "local":
			// 分组名要与前端「系统设置」里的分组中文名一致（文件存储与 CDN），
			// 否则按提示找过去会找不到对应位置
			reason = "当前为本地磁盘存储：视频会占用服务器磁盘与带宽。请先在「系统设置 → 文件存储与 CDN」配置对象存储"
		case !presign:
			reason = "当前存储驱动不支持直传预签名，大视频经服务器中转会拖垮服务"
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"driver":      driver,
			"presign":     presign,
			"videoUpload": driver != "local" && presign,
			// 前端据此在选文件时预校验大小；服务端无法强拦（直传由对象存储直接收文件）
			"videoMaxMb": setting.GetInt("upload_video_max_mb", 100),
			"reason":     reason,
		}})
	}
}

// signHandler 私有桶场景：把已存的公网 URL 换成限时签名 URL（key 由 BaseURL 前缀反推）
func signHandler(up storage.Uploader, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		s, ok := up.(storage.Signer)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "当前存储模式不支持限时签名访问（仅对象存储私有桶支持）"})
			return
		}
		var req struct {
			Urls []string `json:"urls"`
		}
		if c.ShouldBindJSON(&req) != nil || len(req.Urls) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		base := strings.TrimRight(cfg.UploadBaseURL, "/") + "/"
		out := make(map[string]string, len(req.Urls))
		for _, u := range req.Urls {
			// 优先按绝对基址反推 key（云存储绝对 CDN URL）；
			// 兼容本地存储返回的相对路径 /uploads/<name>：key 退化成去掉 /uploads/ 前缀。
			key := strings.TrimPrefix(u, base)
			if key == u {
				key = strings.TrimPrefix(u, "/uploads/")
			}
			if key == "" || key == u {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无法解析对象 key: " + u})
				return
			}
			signed, err := s.SignGetURL(key, signTTL("sign_get_ttl_minutes", 30))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
				return
			}
			out[u] = signed
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"signed": out}})
	}
}

// signTTL 取限时签名时长（分钟）：优先系统设置，未配置 / 非法时回落 def，
// 并保底 1 分钟——0 或负数会让链接一生成即失效，前端表现为「签名了还是打不开」。
func signTTL(key string, def int) time.Duration {
	m := setting.GetInt(key, def)
	if m < 1 {
		m = def
	}
	return time.Duration(m) * time.Minute
}

// cdnRefreshHandler 触发 CDN 缓存刷新（覆盖写后使旧缓存失效）
func cdnRefreshHandler(rf storage.CDNRefresher) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rf == nil {
			// 五家厂商要求的凭据不同（Cloudflare 是 Token+Zone ID，AK/SK 三家不要资源标识），
			// 所以提示只指路、不点名具体环境变量，避免指错厂商
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "CDN 刷新未配置：请在「系统设置 → 文件存储与 CDN」启用刷新，选好厂商并填全它要求的凭据后重启服务"})
			return
		}
		var req struct {
			Urls []string `json:"urls"`
		}
		if c.ShouldBindJSON(&req) != nil || len(req.Urls) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if err := rf.Refresh(req.Urls); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	}
}

// localFileHandler 本地私有文件流：鉴权后按 key 读取，防目录穿越
func localFileHandler(l *storage.LocalUploader) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Query("key")
		if key == "" || strings.Contains(key, "..") {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		fp := filepath.Join(l.Dir, key)
		abs, err := filepath.Abs(fp)
		if err != nil || !strings.HasPrefix(abs, filepath.Clean(l.Dir)) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "非法路径"})
			return
		}
		c.File(fp)
	}
}
