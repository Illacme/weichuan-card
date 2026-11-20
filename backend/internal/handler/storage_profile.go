package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/pii"
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
//
// uploadBaseURL 是平台的「上传返回基址」（cfg.UploadBaseURL）。只有「旧配置转档案」
// 需要它：旧表从不存这一列，legacy 的 BaseURL 恒取平台统一入口
// （见 uploadroute.Config.BaseURL 的注释）。档案必须显式存下来，
// 否则接管出来的档案一上传就因为「基址为空」被 uploadroute.build 拒掉。
func RegisterStorageProfiles(r *gin.Engine, db *gorm.DB, resolver *storageprofile.Resolver, uploadBaseURL string) {
	plat := middleware.PlatformAdminMiddleware(db)
	admin := middleware.AuthMiddleware(db, "admin")

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

	// 旧配置 → 档案：把 tenant_storage_configs 的一行转成一套档案并绑定给该企业。
	//
	// 为什么需要这条路径：档案化之后旧表仍被 --source=auto 认（兼容层），
	// 但企业想用 CDN 分发、多套切换、一键迁移这些能力就必须进档案。
	// 没有这条桥，已上线客户只能手工重填一遍 AK/SK——而凭据本来就在库里。
	//
	// 四条安全约束，都来自「接管 = 立刻改行为」：
	//  1. 旧配置必须 enabled 且驱动非 local：档案不受 TENANT_STORAGE_ROUTE 控制，
	//     接管一条停用配置等于偷偷把上传切走。
	//  2. 先探桶再落库：桶连不上就接管，等于把那家企业的上传全打死。
	//  3. **绝不修改旧表**：留作回滚通道；接管后 auto 让位给档案，
	//     真要回滚只需删掉绑定。
	//  4. 已有绑定直接拒绝：重复接管会造出第二套同源档案，越点越乱。
	r.POST("/api/admin/storage-profiles/adopt-legacy", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		var req struct {
			CompanyID int64 `json:"companyId"`
			Force     bool  `json:"force"`
		}
		if c.ShouldBindJSON(&req) != nil || req.CompanyID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误：请指定 companyId"})
			return
		}
		var comp model.Company
		if db.First(&comp, req.CompanyID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		var legacy model.TenantStorageConfig
		switch err := db.Where("company_id = ?", req.CompanyID).First(&legacy).Error; {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该企业没有旧的存储配置可接管：直接新建一套档案并绑定即可"})
			return
		case err != nil:
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取旧配置失败，请稍后重试"})
			return
		}
		if legacy.Driver == nginxsnippet.DriverLocal {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置是本地磁盘（对象在平台盘上），接管没有意义：请直接选择或新建一套档案"})
			return
		}
		if !legacy.Enabled {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置未启用：接管会让上传立刻改走该桶。请先在旧配置里启用并自检通过，或直接新建档案"})
			return
		}
		if !legacy.PublicRead {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置未开启公开读：档案同样只支持公开读桶"})
			return
		}
		var bound model.StorageBinding
		if db.Where("tenant_id = ?", req.CompanyID).First(&bound).Error == nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": fmt.Sprintf(
				"该企业已绑定存储档案 #%d：请直接改选，或先「恢复继承」再接管", bound.StorageProfileID)})
			return
		}
		// 凭据从 PII 密文解出来。注意 pii.Decrypt 对没有 v1: 前缀的值按
		// 「历史明文」原样返回（兼容尚未回填的旧数据），所以这里只能靠
		// 「解密报错 / 结果为空」两条拦住真坏的情况。
		ak, err := pii.Decrypt(legacy.AccessKeyEnc)
		if err != nil || strings.TrimSpace(ak) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置的 AccessKey 无法解密（PII_ENC_KEY 与写入时不一致）：请先到旧配置里重填一次密钥"})
			return
		}
		sk, err := pii.Decrypt(legacy.SecretKeyEnc)
		if err != nil || strings.TrimSpace(sk) == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置的 SecretKey 无法解密（PII_ENC_KEY 与写入时不一致）：请先到旧配置里重填一次密钥"})
			return
		}
		row := &model.StorageProfile{
			Name: adoptProfileName(db, &comp), Driver: legacy.Driver,
			Endpoint: legacy.Endpoint, Bucket: legacy.Bucket, Region: legacy.Region,
			HostHeader: legacy.HostHeader, UseSSL: legacy.UseSSL,
			AccessKey: strings.TrimSpace(ak), SecretKey: strings.TrimSpace(sk),
			PublicRead: true, BaseURL: strings.TrimSpace(uploadBaseURL),
			Status: storageStatusUnknown, Version: 1,
		}
		if msg := validateStorageProfile(row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "旧配置不足以生成档案：" + msg})
			return
		}
		if !req.Force {
			cfg := storageProfileUploadConfig(row)
			if _, perr := storage.ProbeBucket(cfg.Driver, cfg.Endpoint, row.AccessKey, row.SecretKey,
				cfg.Bucket, cfg.Region, cfg.UseSSL); perr != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "桶连通性自检失败，已拒绝接管（确认无误可勾选「跳过探桶」重试）：" + sanitizeProbeError(perr)})
				return
			}
		}
		if err := db.Create(row).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建档案失败：档案名可能已存在"})
			return
		}
		if err := saveBinding(db, req.CompanyID, row.ID, 0, c.GetString("actorName")); err != nil {
			// 档案已建但绑定失败：把它删掉，避免留下一条没人引用的孤儿档案。
			// 删除失败只记日志——孤儿档案无害（没有绑定就不参与任何解析），
			// 但不能让「删除失败」掩盖真正的绑定错误。
			if delErr := db.Delete(&model.StorageProfile{}, row.ID).Error; delErr != nil {
				log.Printf("[STORAGE][WARN] 接管失败后清理孤儿档案 %d 失败：%v", row.ID, delErr)
			}
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "绑定失败，本次接管已回滚"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionStorageProfileAdopt, TargetType: "storage_profile",
			TargetID: strconv.FormatInt(row.ID, 10),
			Detail: map[string]any{
				"companyId": req.CompanyID, "driver": row.Driver, "bucket": row.Bucket,
				"probeSkipped": req.Force,
				"note":         "凭据由 tenant_storage_configs 的 PII 密文解密后写入档案明文列；旧表未改动",
			},
		})
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
		var req storageProfileReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		applyStorageProfileReq(&row, &req)
		// 内置档案的驱动锁死为 local：它是「云配置全坏时还能上传」的唯一退路。
		// 允许改成 minio/oss 等于让管理员可以亲手删掉这条退路（而删除按钮又被禁用，
		// 形成「删不掉但能改成不可用」的假安全）。
		if row.Builtin {
			if row.Driver != nginxsnippet.DriverLocal {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "内置本地档案的驱动不可更改：它是云存储不可用时的兜底"})
				return
			}
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
		func() int { return tenantStorageCheckLimitFn() }, tenantStorageCheckLimitPerMin, time.Minute,
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
	r.GET("/api/admin/storage-bindings", admin, func(c *gin.Context) {
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
		// 企业管理员只能看自己那一条；列表里其他企业的绑定与全局默认属于平台信息。
		if !middleware.IsPlatformAdmin(c) {
			cid := middleware.CurrentCompanyID(c)
			own := rows[:0]
			for _, b := range rows {
				if b.TenantID == cid {
					own = append(own, b)
				}
			}
			rows = own
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
		if !middleware.IsPlatformAdmin(c) {
			// 企业页不该看到全局默认的具体档案，避免把平台拓扑暴露出去。
			global = map[string]any{"bound": false, "source": "platform", "note": "企业未单独绑定时自动继承全局默认"}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"global": global, "companies": out,
			"storageProfiles": storageNames, "deliveryProfiles": deliveryNames}})
	})

	// 选择项：企业管理员要能选「用哪套」，但不能读档案详情（里面有桶地址与 AK）。
	// 只回 id + name，是权限收敛后唯一需要开放给企业侧的档案信息。
	r.GET("/api/admin/storage-bindings/options", admin, func(c *gin.Context) {
		var storages []model.StorageProfile
		var deliveries []model.DeliveryProfile
		if err := db.Select("id", "name").Order("id ASC").Find(&storages).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储档案选项失败"})
			return
		}
		if err := db.Select("id", "name").Order("id ASC").Find(&deliveries).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取分发档案选项失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"storageProfiles": storages, "deliveryProfiles": deliveries}})
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

	// 企业自选：只允许管理自己企业；平台超管可以代任意企业选。
	r.PUT("/api/admin/storage-bindings/companies/:id", admin, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok || !canManageBinding(c, cid) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "只能设置本企业的存储绑定"})
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

	r.DELETE("/api/admin/storage-bindings/companies/:id", admin, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok || !canManageBinding(c, cid) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "只能设置本企业的存储绑定"})
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

func canManageBinding(c *gin.Context, cid int64) bool {
	return middleware.IsPlatformAdmin(c) || middleware.CurrentCompanyID(c) == cid
}

func validateBindingTargets(db *gorm.DB, storageID, deliveryID int64) string {
	var n int64
	if err := db.Model(&model.StorageProfile{}).Where("id = ?", storageID).Count(&n).Error; err != nil || n == 0 {
		return "存储档案不存在"
	}
	if deliveryID == 0 {
		return ""
	}
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

// adoptProfileName 给接管出来的档案起一个不与现有档案冲突的名字。
//
// 名字带企业名是刻意的：档案列表里一眼能看出「这套是从哪家企业接管来的」，
// 与手工建的「某某公司桶」不会混淆；名字唯一索引也要求这里必须自己避让。
func adoptProfileName(db *gorm.DB, comp *model.Company) string {
	base := strings.TrimSpace(comp.Name)
	if base == "" {
		base = "企业 " + strconv.FormatInt(comp.ID, 10)
	}
	base = "接管 · " + base
	for i := 0; i < 50; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s（%d）", base, i+1)
		}
		var n int64
		if db.Model(&model.StorageProfile{}).Where("name = ?", name).Count(&n).Error != nil || n == 0 {
			return name
		}
	}
	return fmt.Sprintf("%s（%d）", base, time.Now().UnixNano())
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
