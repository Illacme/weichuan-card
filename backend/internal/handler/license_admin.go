package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 本文件是「企业管理 → 授权」的后台接口（平台超管专用）。
//
// 为什么必须有这组接口：B4 让新开通的企业自动拿到一条试用授权行，
// 但授权是**商业动作**——试用到期后要转正、客户加人要加席位、续费要延长期限。
// 如果这些只能在数据库里 UPDATE，那"到期自动降权"就变成了"到期必须找 DBA"，
// 收钱闭环照样断在一半：降权会自动发生，恢复却要人肉改库，且没有审计留痕。
//
// 权限：仅平台超管（PlatformAdminMiddleware）。企业管理员能看自己的到期日（见
// /api/admin/me），但**不能改**——否则客户自己就能给自己延期，等于没有授权。

// licenseEditions 允许的版本标识。空串 = 未设置（业务展示为空，不影响强制判定）。
var licenseEditions = map[string]bool{
	"": true, "trial": true, "platform": true, "private": true,
}

// parseLicenseExpiry 解析到期时间：
//   - 空串      → nil（不过期）
//   - YYYY-MM-DD → 当天 23:59:59（客户理解的"到 12-31 到期"是这一天整天才算完，
//     存成当天 00:00 会让客户在最后一天上午就被停服）
//   - RFC3339   → 原样
func parseLicenseExpiry(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		e := t.Add(24*time.Hour - time.Second)
		return &e, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("到期时间格式应为 YYYY-MM-DD")
}

// tenantLicensesByCompany 批量取多家企业的授权行（企业管理列表用，避免 N+1）。
// 缺失的企业不在 map 里 = 未配置（不限制）。
func tenantLicensesByCompany(db *gorm.DB, ids []int64) map[int64]model.TenantLicense {
	out := make(map[int64]model.TenantLicense, len(ids))
	if len(ids) == 0 {
		return out
	}
	var rows []model.TenantLicense
	db.Where("company_id IN ?", ids).Find(&rows)
	for _, r := range rows {
		out[r.CompanyID] = r
	}
	return out
}

// RegisterCompanyLicense 注册「按企业查看 / 设置授权」的两个接口。
func RegisterCompanyLicense(r *gin.Engine, db *gorm.DB) {
	plat := middleware.PlatformAdminMiddleware(db)

	r.GET("/api/admin/companies/:id/license", plat, companyLicenseGet(db))
	r.PUT("/api/admin/companies/:id/license", plat, companyLicensePut(db))
}

// companyLicenseGet 查看某企业的授权状态。
// 没有授权行时**不报错**，而是 exists=false：那是"未配置 = 不限制"的合法状态
// （存量企业升级后就是这种），界面据此显示"未配置（不限制）"。
func companyLicenseGet(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var comp model.Company
		if db.First(&comp, "id = ?", id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		spec := license.TrialSpecFromSettings()
		var empCnt int64
		db.Model(&model.Employee{}).Where("company_id = ?", id).Count(&empCnt)

		data := gin.H{
			"companyId":     id,
			"exists":        false,
			"edition":       "",
			"seats":         0,
			"expiresAt":     "",
			"features":      "",
			"expired":       false,
			"employeeCount": empCnt,
			// 试用口径一并下发：界面要能提示"新开通的企业会拿到 5 席 / 14 天"
			"trial": gin.H{"enabled": spec.Enabled, "days": spec.Days, "seats": spec.Seats},
		}
		if row, err := license.TenantLicenseOf(db, id); err == nil && row != nil {
			exp := ""
			if !row.ExpiresAt.IsZero() {
				exp = row.ExpiresAt.Format("2006-01-02")
			}
			data["exists"] = true
			data["edition"] = row.Edition
			data["seats"] = row.Seats
			data["expiresAt"] = exp
			data["features"] = row.Features
			data["expired"] = !row.ExpiresAt.IsZero() && time.Now().After(row.ExpiresAt)
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
	}
}

// companyLicensePut 设置某企业的授权：转正 / 延期 / 改席位 / 开关功能。
//
// seats=0 表示不限席位、expiresAt 留空表示不过期——两者都是**正式客户**的常态，
// 不能被当成"没填"忽略（这也是 license.SetTenantLicense 用 map 写库的原因）。
func companyLicensePut(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Edition   string `json:"edition"`
			Seats     int    `json:"seats"`
			ExpiresAt string `json:"expiresAt"`
			Features  string `json:"features"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if !licenseEditions[req.Edition] {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "版本标识只能是 trial / platform / private 或空"})
			return
		}
		if req.Seats < 0 || req.Seats > 100000 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "席位需在 0~100000 之间（0 = 不限制）"})
			return
		}
		exp, err := parseLicenseExpiry(req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		var comp model.Company
		if db.First(&comp, "id = ?", id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}
		if err := license.SetTenantLicense(db, id, req.Edition, req.Seats, exp, req.Features); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败：" + err.Error()})
			return
		}
		expStr := ""
		if exp != nil {
			expStr = exp.Format("2006-01-02")
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionCompanyLicense, TargetType: "company", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{
				"edition": req.Edition, "seats": req.Seats, "expiresAt": expStr, "features": req.Features,
			},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	}
}
