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
	"weichuan/internal/safego"
	"weichuan/internal/storage"
	"weichuan/internal/storageprofile"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 一键迁移任务（迁移 0054）。
//
// 边界（勿回退）：
//   - 只复制不删除。源桶对象保留，切绑定后旧对象仍在源桶，便于回滚与对账。
//   - 租户任务只搬 t/<cid>/ 前缀；全局任务搬整桶（平台对象主要是 p/ 与 wxacode/）。
//   - 单对象失败不中断整批，落 storage_migration_errors 供导出；autoSwitch
//     只在零失败时执行，避免「切过去了但图丢了一批」。
//   - 每个对象流式搬运，视频不会整段进内存（见 storage.ObjectCopier）。

// RegisterStorageMigration 挂载迁移任务接口（平台超管专属）。
func RegisterStorageMigration(r *gin.Engine, db *gorm.DB, resolver *storageprofile.Resolver) {
	plat := middleware.PlatformAdminMiddleware(db)

	r.GET("/api/admin/storage-migration-jobs", plat, func(c *gin.Context) {
		var rows []model.StorageMigrationJob
		if err := db.Order("id DESC").Limit(200).Find(&rows).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取迁移任务失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": rows})
	})

	r.POST("/api/admin/storage-migration-jobs", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		var req struct {
			TenantID        int64 `json:"tenantId"`
			SourceProfileID int64 `json:"sourceProfileId"`
			TargetProfileID int64 `json:"targetProfileId"`
			DryRun          bool  `json:"dryRun"`
			AutoSwitch      bool  `json:"autoSwitch"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if req.TenantID < 0 || req.SourceProfileID <= 0 || req.TargetProfileID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误：请选择源档案与目标档案"})
			return
		}
		if req.SourceProfileID == req.TargetProfileID {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "源档案与目标档案不能相同"})
			return
		}
		if req.TenantID > 0 {
			var n int64
			if err := db.Model(&model.Company{}).Where("id = ?", req.TenantID).Count(&n).Error; err != nil || n == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
				return
			}
		}
		var n int64
		if err := db.Model(&model.StorageProfile{}).Where("id IN ?", []int64{req.SourceProfileID, req.TargetProfileID}).Count(&n).Error; err != nil || n != 2 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "源或目标档案不存在"})
			return
		}
		if req.AutoSwitch && req.DryRun {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "试运行不会切换绑定，请取消自动切换"})
			return
		}
		job := model.StorageMigrationJob{
			TenantID: req.TenantID, SourceProfileID: req.SourceProfileID, TargetProfileID: req.TargetProfileID,
			Status: "created", DryRun: req.DryRun, AutoSwitch: req.AutoSwitch, CreatedBy: c.GetString("actorName"),
		}
		if err := db.Create(&job).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建任务失败"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageMigration, TargetType: "storage_migration_job", TargetID: strconv.FormatInt(job.ID, 10),
			Detail: map[string]any{"tenantId": job.TenantID, "sourceProfileId": job.SourceProfileID,
				"targetProfileId": job.TargetProfileID, "dryRun": job.DryRun, "autoSwitch": job.AutoSwitch}})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": job})
	})

	r.POST("/api/admin/storage-migration-jobs/:id/run", plat, func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var job model.StorageMigrationJob
		if db.First(&job, id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "任务不存在"})
			return
		}
		if job.Status == "running" || job.Status == "done" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "任务已在运行或已完成"})
			return
		}
		if err := db.Model(&model.StorageMigrationJob{}).Where("id = ?", id).
			Updates(map[string]any{"status": "running", "error": "", "finished_at": nil}).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "启动失败"})
			return
		}
		// 异步执行：大桶迁移可能跑几十分钟，不能让 HTTP 连接一直挂着。
		// 断点续跑从 last_key 继续，失败对象逐条留痕。
		safego.Run("storage-migration", func() { runStorageMigration(db, resolver, id) })
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	r.POST("/api/admin/storage-migration-jobs/:id/cancel", plat, func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		res := db.Model(&model.StorageMigrationJob{}).Where("id = ? AND status = ?", id, "running").
			Updates(map[string]any{"status": "canceled", "finished_at": time.Now()})
		if res.Error != nil || res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "任务未在运行中"})
			return
		}
		audit.Record(db, c, audit.Entry{Action: audit.ActionStorageMigration, TargetType: "storage_migration_job", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"cancel": true}})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	r.GET("/api/admin/storage-migration-jobs/:id/errors", plat, func(c *gin.Context) {
		id, ok := profileID(c)
		if !ok {
			return
		}
		var rows []model.StorageMigrationError
		if err := db.Order("id ASC").Limit(1000).Where("job_id = ?", id).Find(&rows).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取失败清单失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": rows})
	})
}

func migrationPrefix(tenantID int64) string {
	if tenantID > 0 {
		return "t/" + strconv.FormatInt(tenantID, 10) + "/"
	}
	return ""
}

// runStorageMigration 执行一次迁移。导出供测试直接调用（测试里不靠 sleep 等 goroutine）。
func runStorageMigration(db *gorm.DB, resolver *storageprofile.Resolver, jobID int64) {
	var job model.StorageMigrationJob
	if db.First(&job, jobID).Error != nil {
		return
	}
	finish := func(status, errMsg string) {
		now := time.Now()
		_ = db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).
			Updates(map[string]any{"status": status, "error": errMsg, "finished_at": now}).Error
		log.Printf("[STORAGE_MIGRATION] job=%d status=%s total=%d done=%d failed=%d err=%s", jobID, status, job.Total, job.Done, job.Failed, errMsg)
	}
	src, err := resolver.UploaderForProfile(job.SourceProfileID)
	if err != nil {
		finish("failed", "源档案不可用："+err.Error())
		return
	}
	dst, err := resolver.UploaderForProfile(job.TargetProfileID)
	if err != nil {
		finish("failed", "目标档案不可用："+err.Error())
		return
	}
	lister, ok := src.(storage.ObjectLister)
	if !ok {
		finish("failed", "源档案的驱动不支持列举对象，无法迁移")
		return
	}
	copier, ok := src.(storage.ObjectCopier)
	if !ok {
		finish("failed", "源档案的驱动不支持对象复制，无法迁移")
		return
	}
	sink, ok := dst.(storage.ObjectStreamWriter)
	if !ok {
		finish("failed", "目标档案的驱动不支持流式写入，无法迁移")
		return
	}

	prefix := migrationPrefix(job.TenantID)
	var keys []string
	// 先完整列举一遍拿总数：进度条要有分母；列举失败就整体失败，
	// 不做「边列边搬」——那样分母会一直变，运营看不出还剩多少。
	if err := lister.ListObjects(prefix, func(key string, _ int64) error {
		keys = append(keys, key)
		return nil
	}); err != nil {
		finish("failed", "列举源对象失败："+err.Error())
		return
	}
	if err := db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).Update("total", len(keys)).Error; err != nil {
		finish("failed", "写入对象总数失败："+err.Error())
		return
	}
	job.Total = int64(len(keys))

	for _, key := range keys {
		// 取消是协作式的：每个对象之间检查一次状态，不会留下半途被强杀的连接。
		var status string
		if err := db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).Select("status").Scan(&status).Error; err != nil || status != "running" {
			return // cancel 接口已经写了终态，这里不要再覆盖
		}
		if key <= job.LastKey {
			continue // 断点续跑：上次成功位点之前的对象跳过
		}
		if !job.DryRun {
			if err := copier.CopyObjectTo(sink, key); err != nil {
				job.Failed++
				_ = db.Create(&model.StorageMigrationError{JobID: jobID, ObjectKey: key, Error: clipErr(err)}).Error
			}
		}
		job.Done++
		job.LastKey = key
		// 每 20 个对象落一次进度：每个都写会让大桶迁移把数据库写爆。
		if job.Done%20 == 0 {
			_ = db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).
				Updates(map[string]any{"done": job.Done, "failed": job.Failed, "last_key": job.LastKey}).Error
		}
	}
	_ = db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).
		Updates(map[string]any{"done": job.Done, "failed": job.Failed, "last_key": job.LastKey}).Error

	status := "done"
	if job.Failed > 0 {
		// 有失败对象时任务本身仍然结束，但绝不自动切绑定。
		status = "done"
	}
	if job.AutoSwitch && job.Failed == 0 && len(keys) > 0 {
		if err := switchBindingAfterMigration(db, &job); err != nil {
			finish("failed", "对象已复制完成，但自动切换绑定失败："+err.Error())
			return
		}
		touchProfileResolver(resolver)
	}
	finish(status, "")
}

func switchBindingAfterMigration(db *gorm.DB, job *model.StorageMigrationJob) error {
	// 自动切换只换存储档案，保留原有 CDN 分发档案；两者独立是这个模块的设计前提。
	var b model.StorageBinding
	res := db.Where("tenant_id = ?", job.TenantID).Limit(1).Find(&b)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		b = model.StorageBinding{TenantID: job.TenantID, Version: 1}
	}
	b.StorageProfileID = job.TargetProfileID
	b.UpdatedBy = "migration:" + strconv.FormatInt(job.ID, 10)
	b.Version++
	b.CreatedAt = time.Now()
	return db.Save(&b).Error
}

func clipErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	if len(s) > 400 {
		s = s[:400]
	}
	return s
}
