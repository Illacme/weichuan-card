package handler

import (
	"log"
	"net/http"
	"strings"

	"weichuan/internal/license"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// licenseExpiryAllowlist 过期只读降级时仍放行的路径前缀：
// 登录 / 刷新 / 安装向导 / 探针 / 指标 / 前端报错——保证管理员仍能登录看到只读状态、运维仍能探活。
//
// "/healthz" 是前缀匹配，一条覆盖 /healthz、/healthz/、/healthz/live、/healthz/ready。
// 新增健康检查子端点时不必再往这里加，但若改了根路径名（如挪到 /api/healthz），
// 必须同步——漏改的话授权一过期，探活就会收到 {code:2}，监控误报「服务已死」。
var licenseExpiryAllowlist = []string{
	"/api/auth", "/api/login", "/api/refresh",
	"/setup", "/healthz", "/metrics", "/api/client-error",
}

func licenseExpiryAllowed(path string) bool {
	for _, p := range licenseExpiryAllowlist {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// seatExceeded 判断某租户的已用员工数是否已达授权席位上限。
//
// 抽成独立函数的原因：建员工有**两条**路径（管理员直接新增 / 审批开通申请），
// 两条都必须卡席位上限。此前只有前者被卡，审批路径可以反复通过申请无限建号——
// 席位上限形同虚设，客户超额使用却不付费。
//
// 返回 true 表示已达上限（应当拒绝建员工）。
// 未解析出授权（ent==nil）、或授权不限席位（Seats<=0）时返回 false（放行）。
//
// 统计按「当前租户」隔离：企业 A 的员工数不会顶掉企业 B 的建人额度。
func seatExceeded(db *gorm.DB, ent *license.Entitlement, cid int64) bool {
	if ent == nil || ent.Seats <= 0 || cid <= 0 {
		return false
	}
	var n int64
	if err := db.Model(&model.Employee{}).Where("company_id = ?", cid).Count(&n).Error; err != nil {
		return false
	}
	return int(n) >= ent.Seats
}

// EnforceLicense 把 Phase 1 解析出的授权（license.Current）真正落到运行期：
//  1. 过期降级只读：授权过期后，除白名单外的写请求一律 403（code=2，与现有只读语义一致），
//     管理员仍可登录查看、运维仍可探活。
//  2. 席位上限：在管理员新建员工（POST /api/admin/employees）时，若已用席位 >= 授权上限，
//     拒绝创建（code=403）。Seats<=0 表示不限。
//
// 授权来源（Phase 4）：按当前租户解析（license.ResolveFor）——有该租户的
// tenant_license 行就用它，没有则回退全局设置（等价于 Phase 1~3 行为）。
// 未解析出授权（如测试、未配置部署）时全部放行，等价于现状。
func EnforceLicense(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := resolveTenantForLicense(c)
		ent := license.ResolveFor(c.Request.Context(), tenantID)

		// 1) 过期 → 只读降级
		if ent != nil && ent.IsExpired() && isMutating(c.Request.Method) && !licenseExpiryAllowed(c.Request.URL.Path) {
			log.Printf("[LICENSE] 授权已过期，拒绝写操作（只读降级）：%s %s", c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 2, "msg": "授权已过期，系统已切换为只读模式，请续费后重启"})
			return
		}

		// 2) 席位上限（仅管理员新建员工这一条创建路径）：按「当前租户」隔离统计，
		// 不再全局计数——企业 A 的员工数不会顶掉企业 B 的建人额度（Phase 3 租户解析统一）。
		if ent != nil && ent.Seats > 0 && c.Request.Method == http.MethodPost && c.FullPath() == "/api/admin/employees" {
			cid := resolveTenantForLicense(c)
			if seatExceeded(db, ent, cid) {
				c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 403, "msg": "员工席位已达授权上限，请升级授权"})
				return
			}
		}

		c.Next()
	}
}

// RequireFeature 功能开关拦截：授权未开通该 feature 时拒绝请求（code=403）。
// 用于把「可选能力」（如小程序代发 mp_release）按授权收紧；授权无 Features 约束（nil/空）时视为全部开通。
// 同样按当前租户解析授权（Phase 4）：可以给 A 企业开通代发、B 企业不开。
func RequireFeature(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ent := license.ResolveFor(c.Request.Context(), resolveTenantForLicense(c))
		if !ent.HasFeature(name) {
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 403, "msg": "当前授权未开通该功能：" + name})
			return
		}
		c.Next()
	}
}
