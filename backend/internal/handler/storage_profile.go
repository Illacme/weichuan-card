package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
	"weichuan/internal/storageprofile"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 存储与分发档案的后台接口（迁移 0054）。
//
// 权限分两层，不能合并：
//   - 档案 / 全局绑定 / 迁移任务：平台超管专属。档案里装的是云凭据，
//     企业管理员若能改，就能把整个平台的文件流指向自己的桶。
//   - 企业绑定：平台超管 + 本企业管理员。这里只选「用哪一套」，
//     看不到也改不了凭据，所以放给企业自助是安全的（用户 2026-11-20 决策）。
//
// 所有写接口在保存后调 touchProfileResolver(resolver)：全局默认切换必须即时生效，
// 不依赖重启，也不依赖各进程缓存自然过期。

// storageProfileReq 的字段全部是指针：nil = 本次没传（保留原值）。
// 与 tenantStorageReq 同一条纪律——界面只改名字时不该把 SecretKey 清空。
type storageProfileReq struct {
	Name       *string `json:"name"`
	Driver     *string `json:"driver"`
	Endpoint   *string `json:"endpoint"`
	Bucket     *string `json:"bucket"`
	Region     *string `json:"region"`
	BaseURL    *string `json:"baseUrl"`
	HostHeader *string `json:"hostHeader"`
	UseSSL     *bool   `json:"useSsl"`
	LocalDir   *string `json:"localDir"`
	AccessKey  *string `json:"accessKey"`
	SecretKey  *string `json:"secretKey"`
	PublicRead *bool   `json:"publicRead"`
}

type deliveryProfileReq struct {
	Name       *string `json:"name"`
	Provider   *string `json:"provider"`
	Domain     *string `json:"domain"`
	AccessKey  *string `json:"accessKey"`
	SecretKey  *string `json:"secretKey"`
	Endpoint   *string `json:"endpoint"`
	ResourceID *string `json:"resourceId"`
}

// RegisterStorageProfiles 挂载存储档案 / 分发档案 / 绑定关系接口。
func RegisterStorageProfiles(r *gin.Engine, db *gorm.DB, resolver *storageprofile.Resolver) {
	// 全部接口只对平台超管开放：档案里装的是客户的云凭据，绑定决定「文件写进哪个桶」。
	// 企业管理员连读都不给（§1.1 的权限模型），因此这里不需要第二档中间件。
	plat := middleware.PlatformAdminMiddleware(db)

	// ---- 存储档案 ----
	r.GET("/api/admin/storage-profiles", plat, func(c *gin.Context) {
		var rows []model.StorageProfile
		if err := db.Order("builtin DESC, id ASC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取文件存储失败"})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for i := range rows {
			out = append(out, storageProfileView(&rows[i]))
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": out})
	})

	r.POST("/api/admin/storage-profiles", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		var req storageProfileReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		row := &model.StorageProfile{Driver: nginxsnippet.DriverLocal, UseSSL: true, PublicRead: true, Status: storageStatusUnknown}
		applyStorageProfileReq(row, &req)
		if msg := validateStorageProfile(row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := db.Create(row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：名称可能已存在"})
			return
		}
		storageProfileAudit(c, db, audit.ActionStorageProfileUpdate, row.ID, row)
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": storageProfileView(row)})
	})

	r.PUT("/api/admin/storage-profiles/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, ok := profileID(c)
		if !ok {
			return
		}
		var row model.StorageProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		origLocalDir := strings.TrimSpace(row.LocalDir)
		var req storageProfileReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// local_dir 若随请求改动，下面 builtin 分支要能识别出来（apply 已覆盖 row）。
		reqChangedLocalDir := req.LocalDir != nil && strings.TrimSpace(*req.LocalDir) != origLocalDir
		applyStorageProfileReq(&row, &req)
		if row.Builtin {
			// 三套内置档案的边界不同，不能一刀切：
			//   - platform_primary 是平台自己的连接信息，驱动与桶本来就该能改（改完
			//     重启），自检接口复用同一条；
			//   - tenant_fallback / platform_mirror 是两块本地盘，驱动锁死 local。
			//     允许改成云等于让管理员亲手删掉「云配置全坏时还能上传」的退路，
			//     而删除按钮又是禁用的——形成「删不掉但能改成不可用」的假安全。
			if row.Purpose != model.ProfilePurposePlatformPrimary &&
				row.Driver != nginxsnippet.DriverLocal {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "系统自带文件存储的驱动不可更改：它是云存储不可用时的兜底"})
				return
			}
			// 目录对所有内置档案都锁死：静态路由 r.Static("/uploads", cfg.UploadDir)
			// 与平台实例都绑在 cfg.UploadDir 上。若内置档案单独改成别的目录，绑定它的
			// 租户写新目录、静态路由却从旧目录读，全站裂图且两边都不报错。
			if reqChangedLocalDir {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "系统自带文件存储的目录不可单独修改：请到「主存储 → 本地上传目录 / 备份存储目录」修改后重启服务"})
				return
			}
			row.LocalDir = origLocalDir
			row.Builtin = true
		}
		if msg := validateStorageProfile(&row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		row.Version++
		row.Status = storageStatusUnknown // 改了配置，旧的自检结论不再代表当前配置
		row.LastError = ""
		if err := db.Save(&row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		}
		storageProfileAudit(c, db, audit.ActionStorageProfileUpdate, row.ID, &row)
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": storageProfileView(&row)})
	})

	r.DELETE("/api/admin/storage-profiles/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, ok := profileID(c)
		if !ok {
			return
		}
		var row model.StorageProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		if row.Builtin {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "系统自带的文件存储不可删除"})
			return
		}
		var bound int64
		if err := db.Model(&model.StorageBinding{}).Where("storage_profile_id = ?", id).Count(&bound).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取绑定关系失败"})
			return
		}
		if bound > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "还有 " + strconv.FormatInt(bound, 10) + " 条绑定在使用该配置，请先改绑"})
			return
		}
		if err := db.Delete(&row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "删除失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageProfileDelete, TargetType: "storage_profile", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"name": row.Name, "driver": row.Driver, "bucket": row.Bucket}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	limiter := middleware.NewRateLimiter(
		func() int { return storageProbeLimitFn() }, storageProbeLimitPerMin, time.Minute,
	).WithKey(func(c *gin.Context) string {
		return "storageprofile:" + strconv.FormatInt(c.GetInt64("staffId"), 10)
	}).WithMessage("自检过于频繁，请稍后再试").WithCounter(dbRateCounter{db: db})

	r.POST("/api/admin/storage-profiles/:id/check", plat, limiter.Handler(), func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var row model.StorageProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		cfg := storageProfileUploadConfig(&row)
		detail, err := storage.ProbeBucket(cfg.Driver, cfg.Endpoint, row.AccessKey, row.SecretKey, cfg.Bucket, cfg.Region, cfg.UseSSL)
		status, lastErr := storageStatusOK, ""
		if err != nil {
			status, lastErr = storageStatusError, sanitizeProbeError(err)
		}
		now := time.Now()
		if err := db.Model(&model.StorageProfile{}).Where("id = ?", id).
			Updates(map[string]any{"status": status, "last_error": lastErr, "last_probe_at": now}).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "自检完成，但结果写入失败，请重试"})
			return
		}
		entry := audit.Entry{Action: audit.ActionStorageProfileCheck, TargetType: "storage_profile", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"ok": err == nil, "driver": row.Driver, "bucket": row.Bucket}}
		if err != nil {
			entry.Result = audit.ResultFail
			entry.Detail["error"] = lastErr
		}
		audit.Record(db, c, entry)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"ok": err == nil, "detail": detail, "error": lastErr, "status": status,
			"checkedScope": "凭据有效性与桶可达性；不含 nginx 侧 Host/SNI、CDN 与证书链",
		}})
	})

	// ---- 分发档案 ----
	r.GET("/api/admin/delivery-profiles", plat, func(c *gin.Context) {
		var rows []model.DeliveryProfile
		if err := db.Order("id ASC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取 CDN 分发配置失败"})
			return
		}
		out := make([]gin.H, 0, len(rows))
		for i := range rows {
			out = append(out, deliveryProfileView(&rows[i]))
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": out})
	})

	r.POST("/api/admin/delivery-profiles", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		var req deliveryProfileReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		row := &model.DeliveryProfile{Provider: storage.CDNProviderNone, Status: storageStatusUnknown}
		applyDeliveryProfileReq(row, &req)
		if msg := validateDeliveryProfile(row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := db.Create(row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：名称可能已存在"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageProfileUpdate, TargetType: "delivery_profile", TargetID: strconv.FormatInt(row.ID, 10),
			Detail: map[string]any{"name": row.Name, "provider": row.Provider, "domain": row.Domain}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": deliveryProfileView(row)})
	})

	r.PUT("/api/admin/delivery-profiles/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, ok := profileID(c)
		if !ok {
			return
		}
		var row model.DeliveryProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		var req deliveryProfileReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		applyDeliveryProfileReq(&row, &req)
		if msg := validateDeliveryProfile(&row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		row.Version++
		row.Status = storageStatusUnknown
		row.LastError = ""
		if err := db.Save(&row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageProfileUpdate, TargetType: "delivery_profile", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"name": row.Name, "provider": row.Provider, "domain": row.Domain}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": deliveryProfileView(&row)})
	})

	r.DELETE("/api/admin/delivery-profiles/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, ok := profileID(c)
		if !ok {
			return
		}
		var bound int64
		if err := db.Model(&model.StorageBinding{}).Where("delivery_profile_id = ?", id).Count(&bound).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取绑定关系失败"})
			return
		}
		if bound > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "还有 " + strconv.FormatInt(bound, 10) + " 条绑定在使用该配置，请先改绑"})
			return
		}
		// 内置分发档案（platform_primary）承载平台对象与未绑定企业的刷新凭据，
		// 删掉它等于让 p/ 对象的覆盖写悄悄不再刷新——而 p/ 对象恰恰是素材采用、
		// 小程序码重生成这两条「同名覆盖」路径的落点。要停刷新请把厂商改成
		// 「不刷新」，不是删档案。
		var row model.DeliveryProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		if row.Builtin {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "系统自带的 CDN 分发不可删除；要停用刷新请把厂商改成「不刷新」"})
			return
		}
		if err := db.Delete(&model.DeliveryProfile{}, id).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "删除失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageProfileDelete, TargetType: "delivery_profile", TargetID: strconv.FormatInt(id, 10)})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	r.POST("/api/admin/delivery-profiles/:id/check", plat, limiter.Handler(), func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var row model.DeliveryProfile
		if db.First(&row, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置不存在"})
			return
		}
		// CDN 自检只验「配置足以构造刷新器 + 域名形态合法」，不发起刷新请求：
		// 真正的刷新需要具体 URL，拿一个假 URL 去刷既污染 CDN 又不能证明域名配对了。
		detail, err := checkDeliveryProfile(&row)
		status, lastErr := storageStatusOK, ""
		if err != nil {
			status, lastErr = storageStatusError, err.Error()
		}
		now := time.Now()
		if err := db.Model(&model.DeliveryProfile{}).Where("id = ?", id).
			Updates(map[string]any{"status": status, "last_error": lastErr, "last_probe_at": now}).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "自检完成，但结果写入失败，请重试"})
			return
		}
		entry := audit.Entry{Action: audit.ActionStorageProfileCheck, TargetType: "delivery_profile", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"ok": err == nil, "provider": row.Provider, "domain": row.Domain}}
		if err != nil {
			entry.Result = audit.ResultFail
			entry.Detail["error"] = lastErr
		}
		audit.Record(db, c, entry)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"ok": err == nil, "detail": detail, "error": lastErr, "status": status,
			"checkedScope": "配置完整性与域名形态；不发起真实刷新请求"}})
	})

	// ---- 绑定关系 ----
	//
	// 绑定决定「文件写进哪个桶」，属于平台级动作：企业管理员能改它，就能把自己企业的
	// 对象写进**别家客户的桶**（档案里有别家的桶名与凭据），而两边都不会报错。
	// 所以这一组接口一律挂 PlatformAdminMiddleware，企业侧不开放。
	// 企业管理员要换存储，走工单让平台改——这是产品决策，不是实现偷懒。
	r.GET("/api/admin/storage-bindings", plat, func(c *gin.Context) {
		var rows []model.StorageBinding
		var storageRows []model.StorageProfile
		var deliveryRows []model.DeliveryProfile
		if err := db.Order("tenant_id ASC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取绑定失败"})
			return
		}
		_ = db.Find(&storageRows).Error
		_ = db.Find(&deliveryRows).Error
		storageNames := map[int64]string{}
		for i := range storageRows {
			storageNames[storageRows[i].ID] = storageRows[i].Name
		}
		deliveryNames := map[int64]string{}
		for i := range deliveryRows {
			deliveryNames[deliveryRows[i].ID] = deliveryRows[i].Name
		}
		global := map[string]any{"bound": false, "source": "platform", "note": "未配置绑定，沿用平台自身的存储与 CDN 配置"}
		out := make([]gin.H, 0, len(rows))
		for i := range rows {
			b := &rows[i]
			v := bindingView(b, storageNames, deliveryNames)
			if b.TenantID == 0 {
				global = v
				global["source"] = "profile"
			}
			out = append(out, v)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"global": global, "companies": out,
			"storageProfiles": storageNames, "deliveryProfiles": deliveryNames}})
	})

	r.PUT("/api/admin/storage-bindings/global", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, deliveryID, ok, force := bindingIDs(c)
		if !ok {
			return
		}
		if msg := validateBindingTargets(db, id, deliveryID); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		// 切换前核对目标档案是否已包含当前全部对象；漏搬就拒绝，错误文案原样回显
		// （里面有缺失 key 示例和处置路径，不能被「保存绑定失败」这种空话盖掉）。
		if err := guardManualSwitch(db, resolver, 0, id, force); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		if err := saveBinding(db, 0, id, deliveryID, c.GetString("actorName")); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存绑定失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingUpdate, TargetType: "storage_binding", TargetID: "0",
			Detail: map[string]any{"storageProfileId": id, "deliveryProfileId": deliveryID, "scope": "global", "force": force}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 给某家企业绑定档案。平台超管专属（理由见本节开头的注释）。
	r.PUT("/api/admin/storage-bindings/companies/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		id, deliveryID, ok, force := bindingIDs(c)
		if !ok {
			return
		}
		if msg := validateBindingTargets(db, id, deliveryID); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := guardManualSwitch(db, resolver, cid, id, force); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		if err := saveBinding(db, cid, id, deliveryID, c.GetString("actorName")); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存绑定失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingUpdate, TargetType: "storage_binding", TargetID: strconv.FormatInt(cid, 10),
			Detail: map[string]any{"storageProfileId": id, "deliveryProfileId": deliveryID, "scope": "company", "force": force}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 删掉企业绑定（回落到全局默认）。同样平台超管专属。
	r.DELETE("/api/admin/storage-bindings/companies/:id", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		if err := db.Where("tenant_id = ?", cid).Delete(&model.StorageBinding{}).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "删除失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingUpdate, TargetType: "storage_binding", TargetID: strconv.FormatInt(cid, 10),
			Detail: map[string]any{"scope": "company", "reset": true}})
		touchProfileResolver(resolver)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 一键回滚：把绑定切回「本次切换前的档案」。两个作用域各一条路由。
	// 平台超管专属，与改绑同一档权限（理由见本节开头）。
	r.POST("/api/admin/storage-bindings/global/rollback", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		rollbackBindingHandler(c, db, resolver, 0, "0")
	})
	r.POST("/api/admin/storage-bindings/companies/:id/rollback", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		rollbackBindingHandler(c, db, resolver, cid, strconv.FormatInt(cid, 10))
	})
}

// rollbackBindingHandler 回滚绑定的 HTTP 外壳：只处理响应与审计，
// 真正的交换逻辑在 rollbackBinding 里，便于单测直接调用不依赖 gin。
func rollbackBindingHandler(c *gin.Context, db *gorm.DB, resolver *storageprofile.Resolver, tenantID int64, targetID string) {
	if err := rollbackBinding(db, tenantID, c.GetString("actorName")); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	scope := "global"
	if tenantID > 0 {
		scope = "company"
	}
	audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingRollback, TargetType: "storage_binding", TargetID: targetID,
		Detail: map[string]any{"scope": scope, "toPrevious": true}})
	touchProfileResolver(resolver)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
}

// touchProfileResolver 保存后主动重载档案快照。
// 重载失败不回滚已保存的配置：下一次解析会按指纹重试，最多是延迟一个请求生效；
// 但必须打日志——「改了没生效」是这套模块最难自查的故障。
func touchProfileResolver(r *storageprofile.Resolver) {
	if err := r.Touch(); err != nil {
		log.Printf("[STORAGE][WARN] 文件存储配置缓存重载失败（下一次请求会按指纹重试）：%v", err)
	}
}

func profileID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配置 ID 不合法"})
		return 0, false
	}
	return id, true
}

func bindingIDs(c *gin.Context) (int64, int64, bool, bool) {
	var req struct {
		StorageProfileID  int64 `json:"storageProfileId"`
		DeliveryProfileID int64 `json:"deliveryProfileId"`
		// Force 显式跳过「目标档案必须包含当前全部对象」的核对。
		// 默认 false：正常切换必须先迁移完整；只有运维确认目标本就是新桶、
		// 或有意不搬存量时才会打开，且打开会被审计记一笔。
		Force bool `json:"force"`
	}
	if c.ShouldBindJSON(&req) != nil || req.StorageProfileID <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误：请选择一套文件存储"})
		return 0, 0, false, false
	}
	return req.StorageProfileID, req.DeliveryProfileID, true, req.Force
}

func validateBindingTargets(db *gorm.DB, storageID, deliveryID int64) string {
	var sp model.StorageProfile
	if err := db.First(&sp, storageID).Error; err != nil {
		return "文件存储不存在"
	}
	// 两套内置档案绝不能成为租户的写入目标：
	//   - 平台镜像只接收异步回拉的备份副本，绑定后新文件写进备份目录，
	//     备份脚本与出参域名都不认这个落点；
	//   - 主存储装的是 p/ 平台对象与无绑定企业的字节，绑定给租户会把
	//     客户文件写进平台桶（或让平台对象写进客户桶），两边都不报错。
	switch sp.Purpose {
	case model.ProfilePurposePlatformMirror:
		return "这一套是备份存储，只用于备份副本，不能绑定为存储写入目标"
	case model.ProfilePurposePlatformPrimary:
		return "这一套是主存储，只供平台对象与未单独指定的企业使用，不能绑定给企业"
	}
	if deliveryID == 0 {
		return ""
	}
	var dp model.DeliveryProfile
	if err := db.First(&dp, deliveryID).Error; err != nil {
		return "CDN 分发配置不存在"
	}
	// 平台自身 CDN 档案只服务 p/ 平台对象：它的 domain 为空（平台域名真值源是
	// 存储档案的访问基址），绑给企业会让出参回落到平台入口，企业自己的 CDN 域名
	// 反而失效——看起来「配了 CDN」，实际全站走平台域名。
	if dp.Purpose == model.DeliveryPurposePlatformPrimary {
		return "这一条是平台自身 CDN，只供平台对象使用，不能绑定给企业"
	}
	return ""
}

func saveBinding(db *gorm.DB, tenantID, storageID, deliveryID int64, actor string) error {
	// 读-改-写必须在同一事务里：两个管理员同时切换 / 回滚时，分开的 Find 与 Save
	// 会把对方刚写进去的 previous 覆盖回旧值，回滚游标静默丢失。
	return db.Transaction(func(tx *gorm.DB) error {
		var b model.StorageBinding
		res := tx.Where("tenant_id = ?", tenantID).Limit(1).Find(&b)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			b = model.StorageBinding{TenantID: tenantID, Version: 1}
		} else if b.StorageProfileID != storageID || b.DeliveryProfileID != deliveryID {
			// 档案确实变了才记 previous：重复提交同一绑定不该把「上一套」覆盖成当前值，
			// 否则回滚会变成 no-op，管理员以为退回去了、其实什么都没动。
			b.PreviousStorageProfileID = b.StorageProfileID
			b.PreviousDeliveryProfileID = b.DeliveryProfileID
		}
		b.StorageProfileID = storageID
		b.DeliveryProfileID = deliveryID
		b.UpdatedBy = actor
		b.Version++
		b.UpdatedAt = time.Now()
		// 不碰 CreatedAt：它是这条绑定创建时间的语义，重复保存不该把它改掉；
		// 新行的 CreatedAt 交给 gorm 按惯例自动写入。
		return tx.Save(&b).Error
	})
}

func bindingView(b *model.StorageBinding, storageNames, deliveryNames map[int64]string) gin.H {
	source := "inherit"
	if b.TenantID == 0 {
		source = "global"
	}
	return gin.H{
		"tenantId": b.TenantID, "storageProfileId": b.StorageProfileID,
		"storageProfileName":  storageNames[b.StorageProfileID],
		"deliveryProfileId":   b.DeliveryProfileID,
		"deliveryProfileName": deliveryNames[b.DeliveryProfileID],
		"version":             b.Version, "updatedBy": b.UpdatedBy, "updatedAt": b.UpdatedAt, "source": source,
	}
}

func applyStorageProfileReq(row *model.StorageProfile, req *storageProfileReq) {
	if req.Name != nil {
		row.Name = strings.TrimSpace(*req.Name)
	}
	if req.Driver != nil {
		row.Driver = strings.ToLower(strings.TrimSpace(*req.Driver))
	}
	if req.Endpoint != nil {
		row.Endpoint = strings.TrimSpace(*req.Endpoint)
	}
	if req.Bucket != nil {
		row.Bucket = strings.TrimSpace(*req.Bucket)
	}
	if req.Region != nil {
		row.Region = strings.TrimSpace(*req.Region)
	}
	if req.BaseURL != nil {
		row.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.HostHeader != nil {
		row.HostHeader = strings.TrimSpace(*req.HostHeader)
	}
	if req.UseSSL != nil {
		row.UseSSL = *req.UseSSL
	}
	if req.LocalDir != nil {
		row.LocalDir = strings.TrimSpace(*req.LocalDir)
	}
	if req.AccessKey != nil {
		row.AccessKey = strings.TrimSpace(*req.AccessKey)
	}
	if req.SecretKey != nil {
		row.SecretKey = strings.TrimSpace(*req.SecretKey)
	}
	if req.PublicRead != nil {
		row.PublicRead = *req.PublicRead
	}
}

func validateStorageProfile(row *model.StorageProfile) string {
	if row.Name == "" {
		return "请填写名称"
	}
	switch row.Driver {
	case nginxsnippet.DriverLocal, nginxsnippet.DriverMinio, nginxsnippet.DriverOSS, nginxsnippet.DriverS3:
	default:
		return "驱动不在支持范围内（local / minio / oss / s3）"
	}
	// 私有读只对「绑定到企业的对象存储」有意义：平台对象走匿名静态入口，
	// 本地磁盘没有 Signer。这两个判定必须在 local 的早退之前做，
	// 否则「本地 + 私有」会被当成普通本地配置存下来，读侧却完全没有签名能力。
	if !row.PublicRead {
		if row.Builtin || row.Purpose == model.ProfilePurposePlatformPrimary {
			return "平台自身存储必须开启公开读：私有桶只支持绑定到企业的文件存储"
		}
		if row.Driver == nginxsnippet.DriverLocal {
			return "本地磁盘无法签发限时访问地址：私有桶请选对象存储驱动（minio / oss / s3）"
		}
	}
	if row.Driver == nginxsnippet.DriverLocal {
		// 内置 local 只代表平台盘，不需要桶地址；也不可被绑定为「客户自有桶」之外的新用途。
		return ""
	}
	cfg := storageProfileUploadConfig(row)
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return "请填写 endpoint 与 bucket"
	}
	if strings.Contains(row.Endpoint, "://") || strings.ContainsAny(cfg.Endpoint, "/\\ ") {
		return "endpoint 只要 host[:port]，不要带 http:// 或路径"
	}
	if strings.ContainsAny(cfg.Bucket, "/\\") || strings.Contains(cfg.Bucket, "..") {
		return "bucket 不能包含路径分隔符或 .."
	}
	if cfg.BaseURL == "" {
		return "请填写上传返回基址（平台统一入口，如 https://example.com/uploads）"
	}
	if cfg.HostHeader != "" && cfg.HostHeader != cfg.Endpoint && !strings.HasPrefix(cfg.HostHeader, cfg.Bucket+".") {
		return "hostHeader 留空表示路径风格；虚拟主机风格必须写成 <桶名>.<域名>"
	}
	if row.AccessKey == "" || row.SecretKey == "" {
		return "请填写 AccessKey 与 SecretKey"
	}
	return ""
}

// checkedStorageConfig 是校验与自检用的最小视图（凭据不进这个结构）。
type checkedStorageConfig struct {
	Driver, Endpoint, Bucket, Region, BaseURL, HostHeader string
	UseSSL                                                bool
}

// storageProfileUploadConfig 与 storageprofile 包里的同名逻辑保持一致：
// 这里只用于校验与自检，不直接构造上传实例。
func storageProfileUploadConfig(row *model.StorageProfile) checkedStorageConfig {
	ep := strings.TrimSpace(row.Endpoint)
	useSSL := row.UseSSL
	switch {
	case strings.HasPrefix(strings.ToLower(ep), "https://"):
		useSSL, ep = true, strings.TrimPrefix(ep, "https://")
	case strings.HasPrefix(strings.ToLower(ep), "http://"):
		useSSL, ep = false, strings.TrimPrefix(ep, "http://")
	}
	type cfgShape = checkedStorageConfig
	return cfgShape{row.Driver, ep, row.Bucket, row.Region, row.BaseURL, row.HostHeader, useSSL}
}

func applyDeliveryProfileReq(row *model.DeliveryProfile, req *deliveryProfileReq) {
	if req.Name != nil {
		row.Name = strings.TrimSpace(*req.Name)
	}
	if req.Provider != nil {
		row.Provider = strings.ToLower(strings.TrimSpace(*req.Provider))
	}
	if req.Domain != nil {
		row.Domain = strings.TrimSpace(*req.Domain)
	}
	if req.AccessKey != nil {
		row.AccessKey = strings.TrimSpace(*req.AccessKey)
	}
	if req.SecretKey != nil {
		row.SecretKey = strings.TrimSpace(*req.SecretKey)
	}
	if req.Endpoint != nil {
		row.Endpoint = strings.TrimSpace(*req.Endpoint)
	}
	if req.ResourceID != nil {
		row.ResourceID = strings.TrimSpace(*req.ResourceID)
	}
}

func validateDeliveryProfile(row *model.DeliveryProfile) string {
	if row.Name == "" {
		return "请填写名称"
	}
	platform := row.Purpose == model.DeliveryPurposePlatformPrimary
	if row.Provider != storage.CDNProviderNone {
		found := false
		for _, p := range storage.CDNProviders {
			if p == row.Provider {
				found = true
			}
		}
		if !found {
			return "CDN 厂商不在支持范围内"
		}
		// 平台自身 CDN 档案不承载域名：平台对象的出参域名唯一真值源是
		// storage_profiles 里 platform_primary 的 base_url。这里再存一份必然漂移
		// （改了存储档案的基址、分发档案还指着旧域名），所以不要求填、也不使用。
		if !platform && (row.Domain == "" || (!strings.HasPrefix(row.Domain, "https://") && !strings.HasPrefix(row.Domain, "http://"))) {
			return "公开访问域名要写完整，包含 https:// 前缀"
		}
	}
	if _, err := checkDeliveryProfile(row); err != nil {
		return err.Error()
	}
	return ""
}

func checkDeliveryProfile(row *model.DeliveryProfile) (string, error) {
	if row.Provider == storage.CDNProviderNone {
		return "不启用 CDN：出参 URL 直接用存储基址", nil
	}
	if _, err := storage.NewCDNRefresher(storage.CDNConfig{
		Provider: row.Provider, AccessKeyID: row.AccessKey, AccessKeySecret: row.SecretKey,
		Endpoint: row.Endpoint, ResourceID: row.ResourceID,
	}); err != nil {
		return "", err
	}
	return "配置完整，可用于缓存刷新（未发起真实刷新请求）", nil
}

func storageProfileAudit(c *gin.Context, db *gorm.DB, action string, id int64, row *model.StorageProfile) {
	audit.Record(db, c, audit.Entry{Action: action, TargetType: "storage_profile", TargetID: strconv.FormatInt(id, 10),
		Detail: map[string]any{"name": row.Name, "driver": row.Driver, "endpoint": row.Endpoint, "bucket": row.Bucket,
			"credentialsPresent": row.AccessKey != "" && row.SecretKey != ""}})
}

func storageProfileView(row *model.StorageProfile) gin.H {
	view := gin.H{
		"id": row.ID, "name": row.Name, "driver": row.Driver, "endpoint": row.Endpoint,
		"bucket": row.Bucket, "region": row.Region, "baseUrl": row.BaseURL,
		"hostHeader": row.HostHeader, "useSsl": row.UseSSL, "localDir": row.LocalDir,
		"publicRead": row.PublicRead, "status": row.Status, "lastError": row.LastError,
		"lastProbeAt": row.LastProbeAt, "version": row.Version, "builtin": row.Builtin,
		"purpose":   row.Purpose,
		"createdAt": row.CreatedAt, "updatedAt": row.UpdatedAt,
		"accessKeyConfigured": row.AccessKey != "", "secretKeyConfigured": row.SecretKey != "",
		"accessKeyMasked": maskCredential(row.AccessKey),
	}
	return view
}

func deliveryProfileView(row *model.DeliveryProfile) gin.H {
	return gin.H{
		"id": row.ID, "name": row.Name, "provider": row.Provider, "domain": row.Domain,
		"endpoint": row.Endpoint, "resourceId": row.ResourceID,
		"status": row.Status, "lastError": row.LastError, "lastProbeAt": row.LastProbeAt,
		"version": row.Version, "createdAt": row.CreatedAt, "updatedAt": row.UpdatedAt,
		"builtin": row.Builtin, "purpose": row.Purpose,
		"accessKeyConfigured": row.AccessKey != "", "secretKeyConfigured": row.SecretKey != "",
		"accessKeyMasked": maskCredential(row.AccessKey),
	}
}

// sanitizeProbeError 只保留探测失败的摘要；SDK 的底层错误可能带 endpoint 与凭据片段。
func sanitizeProbeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
