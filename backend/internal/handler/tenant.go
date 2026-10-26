package handler

import (
	"net/http"
	"strconv"

	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 多租户第一期的两级后台模型：
//   - 平台超级管理员（admin.company_id = 0）：可见/管理全部租户；
//   - 企业管理员（admin.company_id > 0）：仅可见/管理本企业数据。
// 员工（employee）端到端按本人 staff_id 自作用，不涉及跨租户查询。
//
// 公开内容接口（小程序）不传 companyId 时缺省落 1 号租户（朗汇），
// 保证第一期小程序零改动；按企业分发留给第二期。

// defaultCompanyID 公开接口缺省租户：朗汇机电（存量数据全部归属）
const defaultCompanyID int64 = 1

// tenantScope 解析后台写接口的租户范围：
//   - 企业管理员 → (本企业 company_id, all=false)；
//   - 平台超管   → (0, all=true)，另可用 ?companyId= 收窄到指定企业。
//
// 返回的 companyID 仅在 all=false 时有效。
func tenantScope(c *gin.Context) (companyID int64, all bool) {
	if middleware.IsPlatformAdmin(c) {
		if q, err := strconv.ParseInt(c.Query("companyId"), 10, 64); err == nil && q > 0 {
			return q, false
		}
		return 0, true
	}
	return middleware.CurrentCompanyID(c), false
}

// scopeCond 把租户范围转成 GORM 查询条件（平台超管不过滤）。
func scopeCond(c *gin.Context) func(db *gorm.DB) *gorm.DB {
	cid, all := tenantScope(c)
	if all {
		return func(db *gorm.DB) *gorm.DB { return db }
	}
	return func(db *gorm.DB) *gorm.DB { return db.Where("company_id = ?", cid) }
}

// targetCompanyID 企业内容类接口（公司信息/简介/布局/分类/项目/服务）的目标租户：
//   - 企业管理员 → 固定本企业（忽略查询参数，防越权改别家内容）；
//   - 平台超管   → 取 ?companyId=，缺省 1（朗汇，保持既有行为不变）。
func targetCompanyID(c *gin.Context) int64 {
	if !middleware.IsPlatformAdmin(c) {
		return middleware.CurrentCompanyID(c)
	}
	if q, err := strconv.ParseInt(c.Query("companyId"), 10, 64); err == nil && q > 0 {
		return q
	}
	return defaultCompanyID
}

// companyEnabled 企业存在且未停用（登录与公开内容接口共用）。
func companyEnabled(db *gorm.DB, id int64) bool {
	if id <= 0 {
		return false
	}
	var c model.Company
	if err := db.First(&c, "id = ?", id).Error; err != nil {
		return false
	}
	return c.Status == "enabled"
}

// resolveTenantForLicense 返回本次请求用于「授权判定（席位上限）」的租户（企业）。
// 这是授权与多租户两层逻辑的接合点：
//   - 私有 / 独立部署（DEPLOY_MODE=private 或存在许可证文件）：单租户形态，
//     固定为许可证声明的 TenantID（未声明则回退默认 1 号租户）；
//   - 集中 / 平台模式：沿用既有两级后台模型——后台写接口用 tenantScope
//     （平台超管可 ?companyId= 收窄到指定企业，否则 all=true 时回退 targetCompanyID），
//     企业管理员固定本企业。
//
// 与 apply.go 创建员工时的 cid 解析保持一致（tenantScope → targetCompanyID 兜底），
// 确保「席位上限」统计的正是「即将写入员工的那个企业」。
func resolveTenantForLicense(c *gin.Context) int64 {
	if license.PrivateMode() {
		if t := license.CurrentTenantID(); t > 0 {
			return t
		}
		return defaultCompanyID
	}
	cid, all := tenantScope(c)
	if all {
		return targetCompanyID(c)
	}
	return cid
}

// publicCompanyID 公开内容接口的租户：取 ?companyId=（缺省朗汇），并校验企业存在且启用。
// 停用 / 不存在时向调用方返回明确错误（小程序据此降级展示）。
func publicCompanyID(c *gin.Context, db *gorm.DB) (int64, bool) {
	cid := defaultCompanyID
	if q, err := strconv.ParseInt(c.Query("companyId"), 10, 64); err == nil && q > 0 {
		cid = q
	}
	if !companyEnabled(db, cid) {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在或已停用"})
		return 0, false
	}
	return cid, true
}
