package handler

import (
	"errors"
	"fmt"
	"log"
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
//
// 从第六十一轮起这里拿的是 storage.Resolver 而不是单个实例：三条写路径
// （直传预签名、违规图下架覆盖、普通上传）要按 company_id 取该租户自己的桶。
// 另外两条（素材采用、小程序码）的对象键前缀写死了 p/，必须留在平台实例上——
// 见 storage.Resolver.Platform 的注释。
func RegisterStorage(r *gin.Engine, db *gorm.DB, res storage.Resolver, refresher storage.CDNRefresher, cfg config.Config) {
	// 直传预签名（超管 / 员工）：客户端拿 putUrl 后直接 PUT 到对象存储
	r.GET("/api/admin/upload/presign", middleware.AuthMiddleware(db, "admin"), presignHandler(db, res))
	r.GET("/api/employee/upload/presign", middleware.AuthMiddleware(db, "employee"), presignHandler(db, res))
	// 直传后补缩略图（超管 / 员工）：直传的字节不经过服务端，_thumb 只能事后补
	r.POST("/api/admin/upload/thumb", middleware.AuthMiddleware(db, "admin"), thumbEnsureHandler(res))
	r.POST("/api/employee/upload/thumb", middleware.AuthMiddleware(db, "employee"), thumbEnsureHandler(res))
	// 存储能力探测：后台「公司简介 → 视频来源」用它判断「云存储上传」档是否可选
	r.GET("/api/admin/upload/capability", middleware.AuthMiddleware(db, "admin"), capabilityHandler(res))
	// 私有桶：已存 URL → 限时签名 URL（超管 / 员工）
	r.POST("/api/admin/storage/sign", middleware.AuthMiddleware(db, "admin"), signHandler(res, cfg))
	r.POST("/api/employee/storage/sign", middleware.AuthMiddleware(db, "employee"), signHandler(res, cfg))
	// CDN 刷新（超管）
	r.POST("/api/admin/cdn/refresh", middleware.AuthMiddleware(db, "admin"), cdnRefreshHandler(refresher))
	// 本地私有文件流（仅本地驱动 + 私有模式：文件不通过 /uploads 静态公开，需鉴权后读取）。
	// 判的是**平台实例**：本路由只会读到平台盘上的文件，租户桶里的对象由客户自己的桶托管，
	// 不经过这个进程。用 For(0) 只是绕一圈得同一个东西，直接取 Platform() 更明确。
	if local, ok := res.Platform().(*storage.LocalUploader); ok && !cfg.StoragePublic {
		r.GET("/api/storage/file", middleware.AuthMiddleware(db, "admin"), localFileHandler(local))
		r.GET("/api/employee/storage/file", middleware.AuthMiddleware(db, "employee"), localFileHandler(local))
	}
}

// thumbEnsureHandler 直传后补缩略图：客户端上报对象键，服务端读回原图、
// 生成 _thumb 兄弟对象写回同目录。
//
// 为什么需要这条接口：普通上传走 Uploader.Save，字节在服务端手上，ProcessUpload
// 顺手就把 _thumb 一起写出来了。但预签名直传是浏览器直接把字节 PUT 到对象存储，
// 服务端从头到尾没见过这份数据——于是直传出来的图没有 _thumb，端上每次都要
// 先吃一个 404 再回退原图（service-grid / achievement-grid 等列表都用 _thumb 派生）。
// 补的办法只能是事后按 key 读回来重算。
//
// 归属校验不可省：key 来自客户端，若不校验前缀，A 租户可以上报 B 租户的键，
// 让服务端读别人的图、再往别人目录里写文件。这里用 ParseRef 还原归属，
// 与当前登录态算出的 scope/cid 严格比对，不一致直接 403。
// 校验口径与 presignHandler 完全一致（同一个 uploadCompanyID），
// 因此「presign 发出去的 key」必然能通过这里，不会出现自相矛盾的拒绝。
//
// 语义约定：
//   - 非 jpg/png（gif/webp/mp4 等）不生成缩略图，直接返回 skipped，不算失败；
//   - 缩略图生成失败、写回失败都返回 code=1，但**前端只记日志不阻断上传**——
//     图已经传上去了，缺 _thumb 只是多一次回退，不该让用户重传；
//   - 绝不写出比原图更大的 _thumb（与 ProcessUpload 同口径），此时直接复制原图，
//     省掉端上那次注定失败的请求。
func thumbEnsureHandler(res storage.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Key string `json:"key"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "请求体非法"})
			return
		}
		key := strings.TrimSpace(req.Key)
		ref, perr := storage.ParseRef(key)
		if perr != nil {
			// 历史形态（/uploads/xxx、裸键）不走这条接口：它们由 cmd/thumbbackfill 批量回填。
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "对象键非法或为历史形态"})
			return
		}
		cid := uploadCompanyID(c)
		wantScope := storage.ScopeTenant
		if cid == 0 {
			wantScope = storage.ScopePlatform
		}
		if ref.Scope != wantScope || ref.CompanyID != cid {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "无权处理该对象"})
			return
		}
		up, uerr := res.For(cid)
		if uerr != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": uerr.Error()})
			return
		}
		obj, ok := up.(storage.ObjectStore)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "当前存储驱动不支持补缩略图"})
			return
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(key), "."))
		if ext != "jpg" && ext != "jpeg" && ext != "png" {
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"skipped": true, "reason": "非 jpg/png，不生成缩略图"}})
			return
		}
		raw, rerr := obj.ReadObject(key)
		if rerr != nil {
			// 底层错误里有服务器绝对路径（本地驱动是 os.Open 的 *PathError），
			// 原样回给客户端等于把内部目录结构报出去。日志留全量，响应只给结论。
			log.Printf("[WARN] 补缩略图读取失败 key=%s: %v", key, rerr)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取原图失败（对象不存在或不可读）"})
			return
		}
		thumb, terr := storage.Thumbnail(ext, raw)
		if terr != nil {
			log.Printf("[WARN] 补缩略图生成失败 key=%s: %v", key, terr)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "生成缩略图失败（图片可能已损坏）"})
			return
		}
		if len(thumb) >= len(raw) {
			thumb = raw
		}
		thumbKey := strings.TrimSuffix(key, filepath.Ext(key)) + "_thumb" + filepath.Ext(key)
		contentType := "image/png"
		if ext == "jpg" || ext == "jpeg" {
			contentType = "image/jpeg"
		}
		if werr := obj.PutObject(thumbKey, thumb, contentType); werr != nil {
			log.Printf("[WARN] 补缩略图写入失败 key=%s: %v", thumbKey, werr)
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "写入缩略图失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"key": thumbKey, "size": len(thumb)}})
	}
}

// presignHandler 返回预签名 PUT URL + 最终可访问 URL；本地驱动不支持时返回 code=1（前端回退普通上传）
//
// 关于存储配额（B3）：直传由客户端直接 PUT 到对象存储，**后端永远看不到这批字节**，
// 所以只能靠客户端在申请签名时把 size 报上来（frontend/src/api/uploadFile.ts 统一带上，
// UploadField 与富文本编辑器图片菜单共用它）。
// 这里做两件事：① size 可信时先判配额，超了直接拒（前端会回退普通上传，那边还会再判一次）；
// ② 签名成功即按 size 记一次用量——**即使随后 PUT 失败也会记**。
// 这个偏向是有意的：不记就完全统计不到对象存储的用量（那配额等于形同虚设），
// 记多了只会让该租户早一点看到"空间不足"，用户可核对、平台可调；反过来则永远查不出谁在占。
func presignHandler(db *gorm.DB, res storage.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid := uploadCompanyID(c)
		// 先取实例再判能力：能力是"实例的属性"，不是全局配置的属性。
		// 顺序反过来（先看全局 cfg.StorageDriver）会在 A 配了桶、B 没配时给出
		// 「B 也能直传」的结论，而 B 的请求落到本地驱动上必然失败。
		up, uerr := res.For(cid)
		if uerr != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": uerr.Error()})
			return
		}
		p, ok := up.(storage.Presigner)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "当前存储模式不支持直传预签名，已回退普通上传"})
			return
		}
		ext := strings.TrimPrefix(c.Query("ext"), ".")
		contentType := c.Query("contentType")
		// size 为可选：老客户端不带 → 不计量也不拦（不能因为缺一个可选参数就让上传失败）
		size, _ := strconv.ParseInt(c.Query("size"), 10, 64)
		if size > 0 {
			if okq, msg := checkStorageQuota(db, cid, size); !okq {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
				return
			}
		}
		// 写入侧接线（第五十四轮）：预签名签的是带归属前缀的键（平台 p/...、租户 t/<cid>/...），
		// 客户端直传落桶即落到该前缀下；返回给前端的 url（finalURL）形态不变。
		name, err := storage.RandName(ext)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		scope := storage.ScopeTenant
		if cid == 0 {
			scope = storage.ScopePlatform
		}
		key, kerr := storage.ObjectKeyFor(scope, cid, name)
		if kerr != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": kerr.Error()})
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

// capabilityHandler 返回**当前租户**的存储能力，供后台决定「云存储上传」档能不能选。
//
// 判据：该租户实际生效的实例必须是对象存储（非 local），且实现了 Presigner。
// 本地磁盘驱动下上传视频会把文件落在应用服务器上——磁盘占我们的、出网带宽也算我们的，
// 与「客户自己承担存储与流量」的前提相悖；没有预签名则大视频只能经服务器中转，
// 几十上百 MB 走内存拷贝会拖垮服务。两种情况都让界面禁用该档并给出可操作的引导。
//
// 第六十一轮把「按全局 cfg.StorageDriver 判」改成「按该租户的实例判」：
// 全局单值只有一个，而 A 配了自有桶、B 仍走本地盘是常态——按全局判的结果是
// 后台会给 B 也显示「可直传」，用户选了那一档，直传请求落到本地驱动必然失败，
// 而界面全程没给出任何提示。
func capabilityHandler(res storage.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid := uploadCompanyID(c)
		up, uerr := res.For(cid)
		if uerr != nil {
			// 配置坏了不静默回退成「看起来能用」：把原因原样回给后台，
			// 这是企业管理员唯一能看到自家存储出错的地方（写接口只有平台超管能看）。
			// 用 code=0 + videoUpload=false 而不是 code=1：这是「探测结果」而不是
			// 「请求失败」，后者会让前端整块降级成 fallback，连 reason 都显示不出来。
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
				"driver":      res.DriverFor(cid),
				"presign":     false,
				"videoUpload": false,
				"videoMaxMb":  setting.GetInt("upload_video_max_mb", 100),
				"reason":      uerr.Error(),
			}})
			return
		}
		_, presign := up.(storage.Presigner)
		driver := res.DriverFor(cid)
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
//
// 第六十一轮只改「用哪个实例签」，**不动入参契约**：设计稿 §3.3 原写「改为直接按
// objectKey 签名」，但那条要求已经不必成立——storage.legacyRel 在基址对不上时会
// 退到「只认 /uploads/ 段」的兜底，而租户对象存库的形态是
// <平台入口>/uploads/t/<cid>/x.jpg，从里面反推 t/<cid>/x.jpg 是确定性的。
// 改入参形态等于让前端跟着改，收益为零、回归面却是一整条链路。
//
// **归属校验（第七十六轮补）**：入参 urls 完全由客户端提供，早先反推出 key 后
// 直接拿 `res.For(调用者 cid)` 签名，不校验 key 属不属于这个 cid。平台盘为私有桶时，
// 平台实例持有全部 t/<cid>/ 对象的签名权，任何登录用户都能拿公开名片页上别家的
// URL 换到签名地址（可枚举的 IDOR）。同文件 thumbEnsureHandler 早就做了
// `ref.Scope/ref.CompanyID != cid → 403`，这里必须同口径。
//
// 另一条同源纪律：**按对象归属选实例**，不按调用者身份选。租户对象用自己的桶签、
// 平台对象用平台实例签——用 A 的凭据去签 B 桶里的 key，签名对不上，端上照样 404，
// 而且没有任何报错。两者一起改才能既堵越权又不出半截故障。
func signHandler(res storage.Resolver, cfg config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid := uploadCompanyID(c)
		var req struct {
			Urls []string `json:"urls"`
		}
		if c.ShouldBindJSON(&req) != nil || len(req.Urls) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		out := make(map[string]string, len(req.Urls))
		for _, u := range req.Urls {
			// 从存库的资源引用反推对象键（云驱动绝对基址 / 本地 /uploads/ 相对路径 /
			// 裸键都覆盖）。先判 /uploads/ 再判基址，修复了旧实现在 UploadBaseURL
			// 为空时把 /uploads/x.jpg 错截成 uploads/x.jpg 的旧 bug（多一层目录）。
			key, err := storage.ObjectKeyFromURL(u, cfg.UploadBaseURL)
			if err != nil || key == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无法解析对象 key: " + u})
				return
			}

			s, err := signerForKey(cid, key, res)
			if err != nil {
				code := 1
				if errors.Is(err, storage.ErrScopeMismatch) {
					code = 403
				}
				c.JSON(http.StatusOK, gin.H{"code": code, "msg": err.Error()})
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

// signerForKey 校验 key 归属并给出**与该对象同源**的签名器。
//
// 三种形态分开处理，不能合并成一个 bool：
//   - t/<cid>/…：租户对象。cid>0 时只许签自己；平台超管（cid=0）可代签任意企业。
//   - p/…：平台对象（素材 / 小程序码 / 下架占位图），恒用平台实例。
//   - 历史裸键：无归属信息、对象在平台盘。只放平台超管；租户签它等于能碰平台盘上
//     别家的老对象，而这类数据本来就归 P3 搬迁处理，不值得为它开这个口子。
//
// 用 ErrScopeMismatch 包装拒绝，handler 据此回 403 而非 code:1——
// 「没权限」和「签不了」的处置动作完全不同。
func signerForKey(cid int64, key string, res storage.Resolver) (storage.Signer, error) {
	var (
		up  storage.Uploader
		err error
	)
	ref, perr := storage.ParseRef(key)
	switch {
	case perr == nil && ref.Scope == storage.ScopeTenant:
		if cid > 0 && ref.CompanyID != cid {
			return nil, fmt.Errorf("%w: 无权签名企业 %d 的对象", storage.ErrScopeMismatch, ref.CompanyID)
		}
		up, err = res.For(ref.CompanyID)
	case perr == nil: // ScopePlatform
		up = res.Platform()
	case errors.Is(perr, storage.ErrLegacyRef):
		if cid > 0 {
			return nil, fmt.Errorf("%w: 历史形态对象不支持租户签名，请联系平台迁移后再试", storage.ErrScopeMismatch)
		}
		up = res.Platform()
	default:
		return nil, fmt.Errorf("对象键非法：%w", perr)
	}
	if err != nil {
		return nil, err
	}
	s, ok := up.(storage.Signer)
	if !ok {
		return nil, errors.New("当前存储模式不支持限时签名访问（仅对象存储私有桶支持）")
	}
	return s, nil
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
		// 动态刷新器是个壳：包了壳但没有全局刷新器、也没有分发档案绑定时，
		// 仍然等同「未配置」，不能因为类型非 nil 就把失败伪装成成功。
		if checker, ok := rf.(interface{ Configured() bool }); ok && !checker.Configured() {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "CDN 刷新未配置：请在「文件存储与分发 → 分发档案」新建一套并绑定，或在系统设置里填全凭据"})
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
