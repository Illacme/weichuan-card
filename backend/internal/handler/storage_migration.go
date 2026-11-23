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

// migrationCopyObject 是单个对象的复制动作，默认走驱动的流式复制。
// 抽成变量只为测试能精确模拟「某个对象失败」——续跑必须重试失败对象这条
// 承诺，靠真实本地驱动很难稳定复现（删文件会被列举阶段就发现）。
var migrationCopyObject = func(copier storage.ObjectCopier, sink storage.ObjectStreamWriter, key string) error {
	return copier.CopyObjectTo(sink, key)
}

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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误：请选择源文件存储与目标文件存储"})
			return
		}
		if req.SourceProfileID == req.TargetProfileID {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "源文件存储与目标文件存储不能相同"})
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
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "源或目标文件存储不存在"})
			return
		}
		// 内置档案不是普通业务桶：镜像是备份落点（里面是副本，不是真数据），
		// 主存储承载 p/ 平台对象与无绑定企业的字节。两者当迁移源或目标，
		// 都会让「主写入在哪」这件事实失去唯一答案，自动切换后出参全错。
		if builtinProfileInvolved(db, req.SourceProfileID, req.TargetProfileID) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "系统自带的文件存储（主存储 / 备份存储）不能作为迁移的源或目标"})
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
		if !canRunMigration(job.Status, job.Failed) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "任务已在运行或已全部完成"})
			return
		}
		// 有失败对象的 done 任务允许续跑重试：失败清单就是待重试队列，
		// 续跑只重试那些对象，已成功的靠 last_key 跳过。
		if job.Status == "done" && job.Failed > 0 {
			log.Printf("[STORAGE_MIGRATION] job=%d retry %d failed objects", id, job.Failed)
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

// canRunMigration 判断任务能否启动/续跑：运行中不重入；已完成的只有仍有失败对象才允许重试。
func canRunMigration(status string, failed int64) bool {
	if status == "running" {
		return false
	}
	if status == "done" {
		return failed > 0
	}
	return true
}

func migrationPrefix(tenantID int64) string {
	if tenantID > 0 {
		return "t/" + strconv.FormatInt(tenantID, 10) + "/"
	}
	return ""
}

// migrationJobStatus 单独抽出状态查询：这里要把「查不到」与「查出错」分开，
// 测试也需要精确命中 DB 报错分支（不靠拖库或回调污染同一个 db 会话）。
var migrationJobStatus = func(db *gorm.DB, jobID int64) (string, error) {
	var status string
	err := db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).
		Select("status").Scan(&status).Error
	return status, err
}

// runStorageMigration 执行一次迁移。导出供测试直接调用（测试里不靠 sleep 等 goroutine）。
func runStorageMigration(db *gorm.DB, resolver *storageprofile.Resolver, jobID int64) {
	var job model.StorageMigrationJob
	if err := db.First(&job, jobID).Error; err != nil {
		// 读不到任务行时没法可靠地写终态（可能就是这张表坏了），但至少要留痕，
		// 不能让一次 DB 故障表现为「任务凭空消失」。
		log.Printf("[STORAGE_MIGRATION] job=%d 读取任务失败：%v", jobID, err)
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
		finish("failed", "源文件存储不可用："+err.Error())
		return
	}
	dst, err := resolver.UploaderForProfile(job.TargetProfileID)
	if err != nil {
		finish("failed", "目标文件存储不可用："+err.Error())
		return
	}
	lister, ok := src.(storage.ObjectLister)
	if !ok {
		finish("failed", "源文件存储的驱动不支持列举对象，无法迁移")
		return
	}
	copier, ok := src.(storage.ObjectCopier)
	if !ok {
		finish("failed", "源文件存储的驱动不支持对象复制，无法迁移")
		return
	}
	sink, ok := dst.(storage.ObjectStreamWriter)
	if !ok {
		finish("failed", "目标文件存储的驱动不支持流式写入，无法迁移")
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

	// 失败清单就是待重试队列：上次失败的对象即使落在 last_key 之前，续跑也必须重试。
	// 旧实现里失败对象照样推进 last_key，续跑那句 `key <= last_key` 会把它们永久跳过——
	// 表面「任务完成、零失败」的分母对得上，实际那几个对象从没被搬到目标桶。
	failedKeys := map[string]bool{}
	var priorErrors []model.StorageMigrationError
	if err := db.Where("job_id = ?", jobID).Find(&priorErrors).Error; err != nil {
		finish("failed", "读取失败清单失败："+err.Error())
		return
	}
	for _, e := range priorErrors {
		failedKeys[e.ObjectKey] = true
	}
	// 用真实失败条数校准，避免续跑时把上次失败又累加一遍。
	job.Failed = int64(len(failedKeys))

	processed := 0
	for _, key := range keys {
		// 取消是协作式的：每个对象之间检查一次状态，不会留下半途被强杀的连接。
		// DB 查询失败与「明确被取消」必须分开：前者是故障，要让任务进 failed 并留痕，
		// 否则 goroutine 静默 return，任务永远停在 running，运营看到的是卡住而不是失败。
		status, statusErr := migrationJobStatus(db, jobID)
		if statusErr != nil {
			finish("failed", "检查任务状态失败："+statusErr.Error())
			return
		}
		if status != "running" {
			return // cancel 接口已经写了终态，这里不要再覆盖
		}
		retry := failedKeys[key]
		if key <= job.LastKey && !retry {
			continue // 断点续跑：上次已成功处理的对象跳过；失败对象例外，必须重试
		}
		processed++
		if !job.DryRun {
			if err := migrationCopyObject(copier, sink, key); err != nil {
				if !retry {
					_ = db.Create(&model.StorageMigrationError{JobID: jobID, ObjectKey: key, Error: clipErr(err)}).Error
					job.Failed++
				}
				// 失败仍推进 last_key：不让一个坏对象卡住后面所有对象；
				// 它已留在 failedKeys 里，下次续跑会被精确找回重试。
				job.LastKey = key
				if processed%20 == 0 {
					_ = db.Model(&model.StorageMigrationJob{}).Where("id = ?", jobID).
						Updates(map[string]any{"done": job.Done, "failed": job.Failed, "last_key": job.LastKey}).Error
				}
				continue
			}
			if retry {
				// 重试成功：销掉失败记录并把计数还回去，Done 在此刻才 +1。
				_ = db.Where("job_id = ? AND object_key = ?", jobID, key).Delete(&model.StorageMigrationError{}).Error
				job.Failed--
				delete(failedKeys, key)
			}
		}
		job.Done++ // Done = 成功搬过去的对象数；失败不计，续跑重试成功才补上
		job.LastKey = key
		// 每 20 个对象落一次进度：每个都写会让大桶迁移把数据库写爆。
		if processed%20 == 0 {
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
		// 复制端说「零失败」还不够——独立从目标文件存储读一遍，确认对象真的齐全，
		// 再覆盖绑定。两道门各管一段（写 vs 读），任一不过都不切。
		if err := guardAutoSwitch(resolver, &job); err != nil {
			finish("failed", err.Error())
			return
		}
		if err := switchBindingAfterMigration(db, &job); err != nil {
			finish("failed", "对象已复制完成，但自动切换绑定失败："+err.Error())
			return
		}
		touchProfileResolver(resolver)
	}
	finish(status, "")
}

func switchBindingAfterMigration(db *gorm.DB, job *model.StorageMigrationJob) error {
	// 自动切换也是一次绑定写入，必须和手动保存/回滚用同一套事务纪律。
	return db.Transaction(func(tx *gorm.DB) error {
		// 自动切换只换存储档案，保留原有 CDN 分发档案；两者独立是这个模块的设计前提。
		var b model.StorageBinding
		res := tx.Where("tenant_id = ?", job.TenantID).Limit(1).Find(&b)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			b = model.StorageBinding{TenantID: job.TenantID, Version: 1}
		} else if b.StorageProfileID != job.TargetProfileID {
			// 与手动 saveBinding 同一条纪律：档案真变了才记 previous，
			// 让切过去之后还能一键退回迁移前的那套档案。
			b.PreviousStorageProfileID = b.StorageProfileID
			b.PreviousDeliveryProfileID = b.DeliveryProfileID
		}
		b.StorageProfileID = job.TargetProfileID
		b.UpdatedBy = "migration:" + strconv.FormatInt(job.ID, 10)
		b.Version++
		b.UpdatedAt = time.Now()
		return tx.Save(&b).Error
	})
}

// builtinProfileInvolved 判断给定的存储里有没有系统自带的那几条（主存储 / 应急存储 / 备份存储）。
// 查库失败按「有」处理（fail closed）：宁可拒绝一次合法迁移，
// 也不能在库抖动时让主写入悄悄落到备份存储或平台桶。
func builtinProfileInvolved(db *gorm.DB, ids ...int64) bool {
	var n int64
	if err := db.Model(&model.StorageProfile{}).
		Where("id IN ? AND purpose IN ?", ids, []string{
			model.ProfilePurposePlatformPrimary, model.ProfilePurposePlatformMirror,
		}).Count(&n).Error; err != nil {
		return true
	}
	return n > 0
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
