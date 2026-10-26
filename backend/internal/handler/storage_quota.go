package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 本文件是 B3 的落地：按租户的存储用量统计 + 配额拦截。
//
// 解决的是什么：此前上传只受「单文件上限 upload_max_mb」约束，**总用量没有任何上限**。
// 一个租户可以无限传：磁盘/对象存储被占满、按量计费的成本无法封顶，
// 也没有可以拿来报价的计量单位；出问题时连"谁占了多少"都答不上来。
//
// 口径（务必与界面文案一致，见 model.StorageUsage 的注释）：
//   - 统计**从本功能启用起**累计新上传，历史文件不回溯（无法可靠归属，硬凑估算更误导）；
//   - 按上传时的文件大小累加（剥 EXIF / 压缩后实际落盘可能更小，取偏大口径）；
//   - 覆盖写（SaveBytes 的固定名对象）不扣减——统计的是"上传量"，不是"存量"。
//
// 配额取值链：租户覆盖（storage_usage.quota_mb）→ 全局设置（storage_quota_mb）→ 0 不限。
// 两级而不是只有全局：给个别大客户单独放大，不必为一个客户放松所有人的上限。
// **默认 0 = 不限**，与 B4 同一原则：升级不能让存量部署突然开始拒绝上传。

const bytesPerMB int64 = 1024 * 1024

// globalStorageQuotaMBFn 全局配额（MB），0 = 不限。
//
// 包级变量（不是直接调 setting.GetInt）：测试要能在不重启、不改全局设置的前提下
// 把阈值压到 1MB 来验证「超额拒绝」。同 uploadLimitFn 的手法。
var globalStorageQuotaMBFn = func() int64 {
	return int64(setting.GetInt("storage_quota_mb", 0))
}

// storageUsageOf 读某租户的用量行；没有行时返回零值行与 nil（"还没传过东西"不是错误）。
func storageUsageOf(db *gorm.DB, cid int64) (model.StorageUsage, error) {
	var row model.StorageUsage
	if cid <= 0 {
		return row, nil
	}
	err := db.Where("company_id = ?", cid).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return model.StorageUsage{CompanyID: cid}, nil
	}
	return row, err
}

// storageQuotaBytesFor 该租户生效的配额（字节）；0 = 不限。
func storageQuotaBytesFor(db *gorm.DB, cid int64) int64 {
	if cid <= 0 {
		return 0
	}
	if row, err := storageUsageOf(db, cid); err == nil && row.QuotaMB > 0 {
		return row.QuotaMB * bytesPerMB
	}
	if mb := globalStorageQuotaMBFn(); mb > 0 {
		return mb * bytesPerMB
	}
	return 0
}

// checkStorageQuota 上传前判定：incoming 字节数是否还装得下。
// 返回 (true, "") 表示放行；否则第二项是可直接回给客户端的原因。
func checkStorageQuota(db *gorm.DB, cid, incoming int64) (bool, string) {
	if cid <= 0 {
		// 平台自身对象（素材库 / 小程序码 / 平台超管直传）不归属任何租户，不计量也不拦。
		return true, ""
	}
	quota := storageQuotaBytesFor(db, cid)
	if quota <= 0 {
		return true, ""
	}
	row, err := storageUsageOf(db, cid)
	if err != nil {
		// 查不到用量就**不要拦**：宁可少算一次，也不要因为一次数据库抖动
		// 让整个企业的上传全挂（那时用户看到的是"存储空间已用尽"，而其实没满）。
		log.Printf("[STORAGE][WARN] 读取租户 %d 用量失败，本次跳过配额检查：%v", cid, err)
		return true, ""
	}
	if row.Bytes+incoming <= quota {
		return true, ""
	}
	return false, fmt.Sprintf("存储空间已用尽（已用 %s / 上限 %s），请联系平台管理员扩容",
		humanMB(row.Bytes), humanMB(quota))
}

// addStorageUsage 累加上传用量（一个租户一行，upsert）。
//
// 失败只记日志，**不**让上传失败：文件此时已经落盘/入对象存储，
// 回错误会让用户重传，产生孤儿文件；而且配额只是低估一点，不影响已传文件的可用性。
// 这是"宁可少算"的同一取舍，代价是这条日志一旦频繁出现，配额会失真——所以要 WARN。
func addStorageUsage(db *gorm.DB, cid, bytes, files int64) error {
	if cid <= 0 || (bytes == 0 && files == 0) {
		return nil
	}
	now := time.Now()
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "company_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"bytes":      gorm.Expr("bytes + ?", bytes),
			"files":      gorm.Expr("files + ?", files),
			"updated_at": now,
		}),
	}).Create(&model.StorageUsage{
		CompanyID: cid, Bytes: bytes, Files: files, UpdatedAt: now,
	}).Error
	if err != nil {
		log.Printf("[STORAGE][WARN] 累计租户 %d 用量失败（配额会低估）：%v", cid, err)
	}
	return err
}

// storageBytesByCompany 批量取多家企业的已用字节数（企业管理列表用，避免 N+1）。
// 注意 Scan 的列别名必须是 snake_case：GORM 按字段 DBName 匹配，
// 写驼峰会静默扫不进来、全是 0（这个坑在统计模块已经踩过一次）。
func storageBytesByCompany(db *gorm.DB, ids []int64) map[int64]int64 {
	out := make(map[int64]int64, len(ids))
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		CompanyID int64 `gorm:"column:company_id"`
		Bytes     int64 `gorm:"column:bytes"`
	}
	db.Model(&model.StorageUsage{}).
		Select("company_id, bytes").
		Where("company_id IN ?", ids).
		Scan(&rows)
	for _, r := range rows {
		out[r.CompanyID] = r.Bytes
	}
	return out
}

// humanMB 把字节数说成人话（<1GB 用 MB，否则 GB）：错误提示里写 1073741824 字节没人看得懂。
func humanMB(n int64) string {
	if n < 1024*bytesPerMB {
		return fmt.Sprintf("%.1f MB", float64(n)/float64(bytesPerMB))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/float64(1024*bytesPerMB))
}

// uploadCompanyID 本次上传应计入哪个租户。
//
// 三种入口分别对应：
//   - /api/admin/upload、/api/employee/upload：登录态里就有 company_id，直接用
//     （平台超管自己的 company_id=0 → 再尝试 ?companyId=，仍为 0 则按"平台自身对象"不计量）；
//   - /api/apply/open/avatar：匿名访客没有登录态，取表单里带的 companyId
//     （小程序申请页会带；不带则无法归属，不计入任何租户——限流仍在，见 uploadLimiter）。
//
// 为什么不给匿名上传兜一个默认租户：独立版的默认租户是 1，把公网上所有匿名
// 垃圾上传都记到 1 号租户头上，会让这个租户的用量数字彻底失真。
func uploadCompanyID(c *gin.Context) int64 {
	if cid := middleware.CurrentCompanyID(c); cid > 0 {
		return cid
	}
	if v, err := strconv.ParseInt(c.PostForm("companyId"), 10, 64); err == nil && v > 0 {
		return v
	}
	if v, err := strconv.ParseInt(c.Query("companyId"), 10, 64); err == nil && v > 0 {
		return v
	}
	return 0
}

// RegisterStorageQuota 注册存储用量的查询与配额设置接口。
//
// GET /api/admin/storage：企业管理员看本企业、平台超管看 ?companyId=（缺省 1 号）。
// 两种角色都能看，因为"空间还剩多少"是日常运营需要的信息，不是特权。
//
// PUT /api/admin/storage/quota：仅平台超管——配额是计费参数，企业管理员自己不能放大。
func RegisterStorageQuota(r *gin.Engine, db *gorm.DB) {
	auth := middleware.AuthMiddleware(db, "admin")
	r.GET("/api/admin/storage", auth, storageGetHandler(db))
	r.PUT("/api/admin/storage/quota", middleware.PlatformAdminMiddleware(db), storageQuotaPutHandler(db))
}

// storageGetHandler 查询某租户的用量与配额。
func storageGetHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid := resolveTenantForLicense(c)
		if cid <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无法确定租户（平台超管请指定 companyId）"})
			return
		}
		row, err := storageUsageOf(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		quota := storageQuotaBytesFor(db, cid)
		percent := 0
		if quota > 0 {
			percent = int(row.Bytes * 100 / quota)
			if percent > 100 {
				percent = 100
			}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"companyId":   cid,
			"bytes":       row.Bytes,
			"files":       row.Files,
			"quotaBytes":  quota,
			"quotaMb":     row.QuotaMB, // 0 = 未单独设置，跟随全局
			"percent":     percent,
			"humanBytes":  humanMB(row.Bytes),
			"humanQuota":  humanMB(quota),
			"unlimited":   quota <= 0,
			"updatedAt":   row.UpdatedAt,
			"quotaGlobal": globalStorageQuotaMBFn(),
		}})
	}
}

// storageQuotaPutHandler 平台超管设置某企业的配额覆盖。
func storageQuotaPutHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			CompanyID int64 `json:"companyId"`
			QuotaMB   int64 `json:"quotaMb"`
		}
		if c.ShouldBindJSON(&req) != nil || req.CompanyID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 上限 1TB：再大就不是"配额"而是笔误（多写几个 0 会让上限形同虚设）
		if req.QuotaMB < 0 || req.QuotaMB > 1024*1024 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "配额需在 0~1048576 MB 之间（0 = 跟随全局设置）"})
			return
		}
		var comp model.Company
		if db.First(&comp, "id = ?", req.CompanyID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		// 只改 quota_mb，不动 bytes/files：这两列是累计事实，改配额不该清零用量。
		// 行不存在时先插一条零用量行（upsert 只在这一列上，与 addStorageUsage 互补）。
		now := time.Now()
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "company_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"quota_mb": req.QuotaMB, "updated_at": now,
			}),
		}).Create(&model.StorageUsage{
			CompanyID: req.CompanyID, QuotaMB: req.QuotaMB, UpdatedAt: now,
		}).Error
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：" + err.Error()})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyStorageQuota, TargetType: "company",
			TargetID: strconv.FormatInt(req.CompanyID, 10),
			Detail:   map[string]any{"quotaMb": req.QuotaMB},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	}
}
