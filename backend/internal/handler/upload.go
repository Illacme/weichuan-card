package handler

import (
	"log"
	"net/http"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 匿名上传限流阈值（每分钟 / 每 IP）。
//
// 为什么匿名上传要限流：/api/apply/open/avatar 无鉴权，任何人都能往存储里写文件。
// 原本只有「扩展名白名单 + 文件头魔数嗅探 + 10MB 上限」兜着——那三条管的是
// 「写进来的是什么」，管不了「写进来多少」。不限流的话，一个脚本就能把
// 对象存储当免费网盘用（成本直���上升），也能把违规图批量刷到公开 URL 上
// （拿不到 openid 就送不了内容安全检测，等于绕过 A1 的图片送检）。
//
// uploadLimitFn 当前生效的上限。
//
// 抽成包级变量是为了测试能把阈值压低（同 randRead 的间接层手法）。
//
// 注意：限流器构造时会把**函数值**存进去，所以这里必须传「读变量的闭包」而不是
// 「变量当前的值」——否则测试里替换 uploadLimitFn 不会生效（限流器仍用旧值）。
var uploadLimitFn = func() int {
	return setting.GetInt("upload_rate_limit_per_min", 20)
}

// uploadLimiter 匿名上传的限流器：按客户端 IP 分桶 + 数据库共享计数。
//
// 桶键带 upload: 前缀：与 login:/refresh:/track: 共用同一张 rate_limit_hits 表，
// 裸 IP 当键会挤进同一行计数——用上传额度把同 IP 的登录打成 429。
//
// 不按业务 ID 分桶：脚本正是靠换 ID 放大调用量，按 ID 分桶等于没限。
func uploadLimiter(db *gorm.DB) *middleware.RateLimiter {
	return middleware.NewRateLimiter(func() int { return uploadLimitFn() }, 20, time.Minute).
		WithKey(func(c *gin.Context) string { return "upload:" + c.ClientIP() }).
		WithMessage("上传过于频繁，请稍后再试").
		WithCounter(dbRateCounter{db: db})
}

// RegisterUpload 注册文件上传接口：超管(/api/admin/upload) 与 员工(/api/employee/upload)
// 返回可访问 URL，填回封面/相册/头像/LOGO/员工头像等。
// 存储实现通过 storage.Uploader 注入：本地磁盘（默认）/ MinIO / OSS 可互换，业务代码不变。
// refresher 可选：上传后 best-effort 触发 CDN 刷新（覆盖写使边缘旧缓存失效）。
//
// 需要 cfg：图片上传后要异步送内容安全检测（media_check_async），
// 送检地址由 UPLOAD_BASE_URL 拼出来（微信必须能访问到），见 submitMediaCheck。
func RegisterUpload(r *gin.Engine, db *gorm.DB, cfg config.Config, up storage.Uploader, refresher storage.CDNRefresher) {
	h := uploadHandler(db, cfg, up, refresher)
	r.POST("/api/admin/upload", middleware.AuthMiddleware(db, "admin"), h)
	r.POST("/api/employee/upload", middleware.AuthMiddleware(db, "employee"), h)
	// 访客自助开通申请上传头像：无鉴权公开端点。
	// 放开匿名上传的考量：申请页在账号创建前就要收头像，访客没有 token；
	// 安全由 storage.Save 兜底——仅允许图片扩展名 + 文件头魔数嗅探 + 10MB 上限，
	// 无法借机落地非图片/超大文件。
	//
	// 但「写什么」之外还要管「写多少」：公开端点不限流等于把对象存储开成免费网盘，
	// 也能批量刷违规图到公开 URL（匿名上传拿不到 openid，送不了内容安全检测）。
	// 所以这里叠一层按 IP 的固定窗口限流，计数走数据库共享（多副本不翻倍）。
	r.POST("/api/apply/open/avatar", uploadLimiter(db).Handler(), h)
}

func uploadHandler(db *gorm.DB, cfg config.Config, up storage.Uploader, refresher storage.CDNRefresher) gin.HandlerFunc {
	return func(c *gin.Context) {
		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		files := form.File["file"]
		if len(files) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未选择文件"})
			return
		}
		// 本次上传计入哪个租户（见 uploadCompanyID）：0 = 平台自身对象，不计量不限配额。
		cid := uploadCompanyID(c)
		urls := make([]string, 0, len(files))
		for _, fh := range files {
			// 配额检查必须在**落盘之前**：先存后判会留下一堆"传上去了但没记用量"的
			// 孤儿文件，而且用户看到失败会重传，占用继续涨。
			if ok, msg := checkStorageQuota(db, cid, fh.Size); !ok {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
				return
			}
			u, err := up.Save(fh)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
				return
			}
			urls = append(urls, u)
			// 用量累加：失败只记日志（函数内 [STORAGE][WARN]）、不失败本次上传
			// ——文件已落盘，回错误会让用户重传，反而多出孤儿文件。
			_ = addStorageUsage(db, cid, fh.Size, 1)
			// 图片异步送检：不阻塞本次响应，也不影响上传结果
			// （未配凭据 / 无 openid / 本地地址时自行跳过，见 mediachk.Plan）。
			submitMediaCheck(db, cfg, c, u)
		}
		// 上传后触发 CDN 刷新（覆盖写场景使旧缓存失效），失败仅记录，不影响本次上传结果
		refreshCDN(refresher, urls...)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"urls": urls}})
	}
}

// refreshCDN 覆盖写之后让 CDN 边缘缓存失效；未启用（refresher==nil）或无 URL 时直接返回。
//
// 失败只记录日志，绝不回传错误：对象已经写成功了，CDN 刷新只是「让改动尽快可见」
// 的增强——厂商接口抖动 / 凭据填错就把一次成功的操作报成失败，比多缓存几分钟糟得多，
// 也会把「改错可自愈」的口子堵上（管理员要能进后台把凭据改回来）。
//
// 三处调用点：普通上传（对象名随机，实际不覆盖）、素材采用与小程序码重生成
// （这两处是真正覆盖同名对象的路径，2026-10-23 补接线前它们根本没触发刷新）。
func refreshCDN(refresher storage.CDNRefresher, urls ...string) {
	if refresher == nil || len(urls) == 0 {
		return
	}
	if err := refresher.Refresh(urls); err != nil {
		log.Printf("[WARN] cdn refresh failed: %v", err)
	}
}
