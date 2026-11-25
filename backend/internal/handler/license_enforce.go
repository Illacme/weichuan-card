package handler

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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

// isMutating 判断一次请求是否会「改内容」。默认按 HTTP 方法判断；
// 但有一类 GET 名读实写——预签名直传端点发的虽是 GET，返回的却是**上传 PUT URL**，
// 拿到它就能往桶里覆盖对象。只读降级若只按方法放行，授权到期后客户仍能换图，
// 「后台已只读」的承诺当场作废。所以这里按注册路由精确点名这类 GET。
//
// 反过来，导出 / 下载类 GET（员工 CSV、统计 CSV、备份包、素材图）**故意放行**：
// 客户带走自己的数据是数据可携带权，只读的语义是「不能改」而不是「不能拿走」。
// 拦导出只有合规风险、没有商业收益——产品决策是保留，别当成 bug 修掉。
var licenseMutatingGetRoutes = map[string]bool{
	"/api/admin/upload/presign":    true,
	"/api/employee/upload/presign": true,
}

func isMutating(c *gin.Context) bool {
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	case http.MethodGet:
		return licenseMutatingGetRoutes[c.FullPath()]
	}
	return false
}

// seatExceeded 判断某租户的已用员工数是否已达授权席位上限。
//
// 抽成独立函数的原因：建员工有**两条**路径（管理员直接新增 / 审批开通申请），
// 两条都必须卡席位上限。此前只有前者被卡，审批路径可以反复通过申请无限建号——
// 席位上限形同虚设，客户超额使用却不付费。
//
// 返回「已用 / 上限」两个数（而不是一个 bool）：拦截文案要能说清 5/5 还是 5/8，
// 只说「已达上限」的话，管理员既不知道差多少、也不知道该找谁加。
//
// 统计按「当前租户」隔离：企业 A 的员工数不会顶掉企业 B 的建人额度。
func seatUsage(db *gorm.DB, ent *license.Entitlement, cid int64) (used int64, cap int, exceeded bool) {
	if ent == nil || ent.Seats <= 0 || cid <= 0 {
		return 0, 0, false
	}
	if err := db.Model(&model.Employee{}).Where("company_id = ?", cid).Count(&used).Error; err != nil {
		// 查不出人数时放行：宁可少拦一次，也不能因为一次统计失败让客户建不了人
		return 0, ent.Seats, false
	}
	return used, ent.Seats, int(used) >= ent.Seats
}

// seatExceeded 保留布尔语义的薄封装（既有调用点与测试用它）。
func seatExceeded(db *gorm.DB, ent *license.Entitlement, cid int64) bool {
	_, _, exceeded := seatUsage(db, ent, cid)
	return exceeded
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
		//
		// 文案必须说清两件事：**哪天**到期（客户要核对合同）、以及**怎么恢复**。
		// 原本文案是「请续费后重启」——对 SaaS 形态是错的：续费只改 tenant_license
		// 一行，ResolveFor 每次请求都查库，立刻恢复，重启纯属白折腾一次生产服务；
		// 而私有部署恰恰相反，不换 license.key 并重启就永远只读。两种形态的恢复
		// 路径完全不同，所以按模式分别拼（见 license.RenewHint）。
		if ent != nil && ent.IsExpired() && isMutating(c) && !licenseExpiryAllowed(c.Request.URL.Path) {
			st := license.StatusOf(ent, time.Now())
			log.Printf("[LICENSE] 授权已过期（%s），拒绝写操作（只读降级）：%s %s",
				st.ExpiresAtDate(), c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 2, "msg": fmt.Sprintf(
				"授权已于 %s 到期，系统已切换为只读模式（可登录查看、导出带走数据，不能修改内容或上传新文件）。%s。",
				st.ExpiresAtDate(), license.RenewHint(license.PrivateMode()))})
			return
		}

		// 2) 席位上限（仅管理员新建员工这一条创建路径）：按「当前租户」隔离统计，
		// 不再全局计数——企业 A 的员工数不会顶掉企业 B 的建人额度（Phase 3 租户解析统一）。
		if ent != nil && ent.Seats > 0 && c.Request.Method == http.MethodPost && c.FullPath() == "/api/admin/employees" {
			cid := resolveTenantForLicense(c)
			if used, cap, exceeded := seatUsage(db, ent, cid); exceeded {
				c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 403, "msg": fmt.Sprintf(
					"员工席位已达授权上限（%d/%d），请升级授权后再新增", used, cap)})
				return
			}
		}

		c.Next()
	}
}

// RequireFeature 功能开关拦截：授权未开通该 feature 时拒绝请求（code=403）。
// 用于把「可选能力」（如小程序代发 mp_release）按授权收紧；授权无 Features 约束（nil/空）时视为全部开通。
// 同样按当前租户解析授权（Phase 4）：可以给 A 企业开通代发、B 企业不开。
//
// **只适用于租户维度路由**（URL 里带企业 id，或企业管理员身份自带 companyId）。
// 记录维度路由（如 /api/admin/releases/:id/*）必须改用 RequireFeatureFor：
// resolveTenantForLicense 对平台超管且无 ?companyId= 的请求会回落到默认租户 1，
// 于是「5 号企业没买、1 号买了，超管仍能给 5 号发布」——按租户收费直接失效。
func RequireFeature(name string) gin.HandlerFunc {
	return RequireFeatureFor(name, func(c *gin.Context) (int64, bool) {
		return resolveTenantForLicense(c), true
	})
}

// RequireFeatureFor 与 RequireFeature 相同，但租户由调用方从请求里解析。
//
// tenantOf 返回 (companyID, ok)：
//   - ok=false 表示**无法确定归属**，一律 403，不能退化成默认租户或平台授权
//     （「判不出来就放行」等于收费闸门可以靠删参数绕过）；
//   - companyID=0 表示平台自有对象（如平台小程序的发布记录），走平台授权 Current()。
//
// 记录维度路由应从**数据库里的记录**取归属，而不是信 URL 里的另一个 id——
// 后者会出现「URL 写 A、记录属于 B」的错配。
func RequireFeatureFor(name string, tenantOf func(*gin.Context) (int64, bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		cid, ok := tenantOf(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 403, "msg": "无法确认该记录所属企业，已拒绝操作"})
			return
		}
		ent := license.ResolveFor(c.Request.Context(), cid)
		if !ent.HasFeature(name) {
			c.AbortWithStatusJSON(http.StatusOK, gin.H{"code": 403, "msg": "当前授权未开通该功能：" + name})
			return
		}
		c.Next()
	}
}
