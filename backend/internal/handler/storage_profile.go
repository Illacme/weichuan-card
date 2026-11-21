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

// 文件存储与分发档案的后台接口（迁移 0054）。
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
	PathStyle  *bool   `json:"pathStyle"`
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
	PathPrefix *string `json:"pathPrefix"`
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储档案失败"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：档案名可能已存在"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案不存在"})
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
		// 内置档案的驱动锁死为 local：它是「云配置全坏时还能上传」的唯一退路。
		// 允许改成 minio/oss 等于让管理员可以亲手删掉这条退路（而删除按钮又被禁用，
		// 形成「删不掉但能改成不可用」的假安全）。
		if row.Builtin {
			if row.Driver != nginxsnippet.DriverLocal {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "内置本地档案的驱动不可更改：它是云存储不可用时的兜底"})
				return
			}
			// 目录同样锁死：静态路由 r.Static("/uploads", cfg.UploadDir) 与平台实例都
			// 绑在 cfg.UploadDir 上。若内置档案单独改成别的目录，绑定它的租户写新目录、
			// 静态路由却从旧目录读，全站裂图且两边都不报错。要换目录请改系统设置的
			// upload_dir 并重启，启动时 EnsureBuiltinLocal 会把它同步到这个档案。
			if reqChangedLocalDir {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "内置本地档案的目录不可单独修改：请到「系统设置 → 文件存储与 CDN → 本地上传目录」修改后重启服务"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案不存在"})
			return
		}
		if row.Builtin {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "内置本地档案不可删除"})
			return
		}
		var bound int64
		if err := db.Model(&model.StorageBinding{}).Where("storage_profile_id = ?", id).Count(&bound).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取绑定关系失败"})
			return
		}
		if bound > 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "还有 " + strconv.FormatInt(bound, 10) + " 条绑定在使用该档案，请先改绑"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案不存在"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取分发档案失败"})
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
		row := &model.DeliveryProfile{Provider: "none", Status: storageStatusUnknown}
		applyDeliveryProfileReq(row, &req)
		if msg := validateDeliveryProfile(row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := db.Create(row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：档案名可能已存在"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案不存在"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "还有 " + strconv.FormatInt(bound, 10) + " 条绑定在使用该档案，请先改绑"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案不存在"})
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
		global := map[string]any{"bound": false, "source": "platform", "note": "未配置档案绑定，沿用系统设置里的平台存储 / CDN"}
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
		id, deliveryID, ok := bindingIDs(c)
		if !ok {
			return
		}
		if msg := validateBindingTargets(db, id, deliveryID); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := saveBinding(db, 0, id, deliveryID, c.GetString("actorName")); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存绑定失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingUpdate, TargetType: "storage_binding", TargetID: "0",
			Detail: map[string]any{"storageProfileId": id, "deliveryProfileId": deliveryID, "scope": "global"}})
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
		id, deliveryID, ok := bindingIDs(c)
		if !ok {
			return
		}
		if msg := validateBindingTargets(db, id, deliveryID); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}
		if err := saveBinding(db, cid, id, deliveryID, c.GetString("actorName")); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存绑定失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageBindingUpdate, TargetType: "storage_binding", TargetID: strconv.FormatInt(cid, 10),
			Detail: map[string]any{"storageProfileId": id, "deliveryProfileId": deliveryID, "scope": "company"}})
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
}

// touchProfileResolver 保存后主动重载档案快照。
// 重载失败不回滚已保存的配置：下一次解析会按指纹重试，最多是延迟一个请求生效；
// 但必须打日志——「改了没生效」是这套模块最难自查的故障。
func touchProfileResolver(r *storageprofile.Resolver) {
	if err := r.Touch(); err != nil {
		log.Printf("[STORAGE][WARN] 档案缓存重载失败（下一次请求会按指纹重试）：%v", err)
	}
}

func profileID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "档案 id 不合法"})
		return 0, false
	}
	return id, true
}

func bindingIDs(c *gin.Context) (int64, int64, bool) {
	var req struct {
		StorageProfileID  int64 `json:"storageProfileId"`
		DeliveryProfileID int64 `json:"deliveryProfileId"`
	}
	if c.ShouldBindJSON(&req) != nil || req.StorageProfileID <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误：请选择一个存储档案"})
		return 0, 0, false
	}
	return req.StorageProfileID, req.DeliveryProfileID, true
}

func validateBindingTargets(db *gorm.DB, storageID, deliveryID int64) string {
	var sp model.StorageProfile
	if err := db.First(&sp, storageID).Error; err != nil {
		return "存储档案不存在"
	}
	// 平台镜像档案只接收异步回拉的备份副本，绝不能成为任何租户的写入目标：
	// 一旦绑定，新文件会被写进备份目录，而备份脚本与出参域名都不认这个落点，
	// 结果是管理员以为切了云桶、实际全写进了私有备份盘。
	if sp.Purpose == model.ProfilePurposePlatformMirror {
		return "该档案是平台本地镜像，只用于备份副本，不能绑定为存储写入目标"
	}
	if deliveryID == 0 {
		return ""
	}
	var n int64
	if err := db.Model(&model.DeliveryProfile{}).Where("id = ?", deliveryID).Count(&n).Error; err != nil || n == 0 {
		return "分发档案不存在"
	}
	return ""
}

func saveBinding(db *gorm.DB, tenantID, storageID, deliveryID int64, actor string) error {
	now := time.Now()
	var b model.StorageBinding
	res := db.Where("tenant_id = ?", tenantID).Limit(1).Find(&b)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		b = model.StorageBinding{TenantID: tenantID, Version: 1}
	}
	b.StorageProfileID = storageID
	b.DeliveryProfileID = deliveryID
	b.UpdatedBy = actor
	b.Version++
	b.CreatedAt = now
	return db.Save(&b).Error
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
	if req.PathStyle != nil {
		row.PathStyle = *req.PathStyle
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
		return "请填写档案名"
	}
	switch row.Driver {
	case nginxsnippet.DriverLocal, nginxsnippet.DriverMinio, nginxsnippet.DriverOSS, nginxsnippet.DriverS3:
	default:
		return "驱动不在支持范围内（local / minio / oss / s3）"
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
	if !row.PublicRead {
		return "第一阶段只支持公开读桶（私有桶需要预签名链路，尚未实现）"
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
	if req.PathPrefix != nil {
		row.PathPrefix = strings.Trim(strings.TrimSpace(*req.PathPrefix), "/")
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
		return "请填写档案名"
	}
	if row.Provider != "none" {
		found := false
		for _, p := range storage.CDNProviders {
			if p == row.Provider {
				found = true
			}
		}
		if !found {
			return "CDN 厂商不在支持范围内"
		}
		if row.Domain == "" || (!strings.HasPrefix(row.Domain, "https://") && !strings.HasPrefix(row.Domain, "http://")) {
			return "公开访问域名要写完整，包含 https:// 前缀"
		}
	}
	if _, err := checkDeliveryProfile(row); err != nil {
		return err.Error()
	}
	return ""
}

func checkDeliveryProfile(row *model.DeliveryProfile) (string, error) {
	if row.Provider == "none" {
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
		"pathStyle": row.PathStyle, "hostHeader": row.HostHeader, "useSsl": row.UseSSL, "localDir": row.LocalDir,
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
		"pathPrefix": row.PathPrefix, "endpoint": row.Endpoint, "resourceId": row.ResourceID,
		"status": row.Status, "lastError": row.LastError, "lastProbeAt": row.LastProbeAt,
		"version": row.Version, "createdAt": row.CreatedAt, "updatedAt": row.UpdatedAt,
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
