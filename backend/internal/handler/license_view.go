package handler

import (
	"net/http"
	"sort"
	"time"

	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 授权到期视图（P0-3 的「客户侧」与「运营侧」两个出口）。
//
// 为什么单独一个接口而不是塞进 /api/admin/me：
//   - /me 是**身份**接口，每次刷新都要用，且被大量测试依赖；塞进业务汇总会让它
//     变成「一个接口回答所有问题」，将来加东西越来越不敢动。
//   - 授权信息需要能**单独刷新**：运营在后台给客户续了费，客户那边刷新页面就该
//     看到天数变了，不必重新登录。
//
// 一个接口同时服务两种视角（按登录身份区分），是因为它们要的是同一份数据的两种
// 切法：企业管理员关心「我自己还剩几天」，平台超管关心「哪些客户快到期了」。
// 分成两个接口会让前端在布局里按身份分支请求，反而更容易漏。
const licenseExpiringLimit = 10

// RegisterLicenseView 注册授权到期视图接口（登录即可访问，不限定平台超管）。
func RegisterLicenseView(r *gin.Engine, db *gorm.DB) {
	admin := middleware.AuthMiddleware(db, "admin")
	r.GET("/api/admin/license", admin, licenseView(db))
}

func licenseView(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var a model.Admin
		if db.First(&a, "id = ?", c.GetInt64("staffId")).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}

		now := time.Now()
		private := license.PrivateMode()

		// 自己这一份：企业管理员拿到本企业的授权，平台超管拿到全局那份
		// （平台模式通常没配 → level=none，界面据此不显示任何提示）。
		ent := license.ResolveFor(c.Request.Context(), a.CompanyID)
		st := license.StatusOf(ent, now)

		edition, seats := "", 0
		if ent != nil {
			edition, seats = ent.Edition, ent.Seats
		}
		var seatsUsed int64
		if a.CompanyID > 0 {
			db.Model(&model.Employee{}).Where("company_id = ?", a.CompanyID).Count(&seatsUsed)
		}

		self := gin.H{
			"edition":   edition,
			"seats":     seats,
			"seatsUsed": seatsUsed,
			// 席位满了要单独说：此时新建员工会被 code=403 拒，
			// 而错误文案只说「已达上限」，管理员不知道该找谁加、加多少。
			"seatsFull": seats > 0 && seatsUsed >= int64(seats),
			"expiresAt": st.ExpiresAtDate(),
			"daysLeft":  st.DaysLeft,
			"expired":   st.Expired,
			"level":     st.Level,
			"title":     st.Text(),
			// 只有真要提醒时才给后果与续费路径：还剩半年的授权天天摆一句
			// 「到期后会只读」等于噪音，反而稀释了真正临期时的提示。
			"consequence": "",
			"renewHint":   "",
		}
		if st.NeedsAttention() {
			self["consequence"] = license.Consequence(private)
			self["renewHint"] = license.RenewHint(private)
		}

		data := gin.H{
			"mode":  modeName(private),
			"scope": "tenant",
			"self":  self,
		}

		// 平台超管额外拿到「哪些客户快到期」——这是运营每天唯一需要看的那一屏。
		// 企业管理员**不给**：跨租户的到期名单本身就是商业信息。
		if a.CompanyID == 0 {
			data["scope"] = "platform"
			items := tenantExpiring(db, now)
			var nExpired, nWithin7, nWithin30 int
			list := make([]gin.H, 0, len(items))
			for _, it := range items {
				// 三个计数是**包含关系**：已过期也算「7 天内」与「30 天内」，
				// 否则界面上会出现「30 天内到期 3 家、其中 7 天内 1 家、已过期 1 家」
				// 这种加起来对不上的数字，运营第一反应是「统计错了」。
				nWithin30++
				if it.Status.Expired {
					nExpired++
					nWithin7++
				} else if it.Status.DaysLeft <= license.ExpiryUrgentDays {
					nWithin7++
				}
				if len(list) >= licenseExpiringLimit {
					continue // 计数要全量，名单只取最急的前 N 条
				}
				list = append(list, gin.H{
					"companyId":   it.CompanyID,
					"companyName": it.CompanyName,
					"edition":     it.Edition,
					"seats":       it.Seats,
					"expiresAt":   it.Status.ExpiresAtDate(),
					"daysLeft":    it.Status.DaysLeft,
					"expired":     it.Status.Expired,
					"level":       it.Status.Level,
					"title":       it.Status.Text(),
				})
			}
			data["alerts"] = gin.H{
				"expired": nExpired, "within7": nWithin7, "within30": nWithin30,
			}
			data["expiring"] = list
		}

		c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
	}
}

// tenantExpiring 取需要提醒的**租户**授权，按紧急程度排序（过期 → 天数最少）。
//
// 全局那份（平台自己的授权）不在这里：它已经由 self 表达，重复列出会让运营
// 以为「平台也是我的客户」。
func tenantExpiring(db *gorm.DB, now time.Time) []license.Reminder {
	all := license.CollectExpiring(db, now)
	out := make([]license.Reminder, 0, len(all))
	for _, it := range all {
		if it.Scope == "tenant" {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Status.Expired != out[j].Status.Expired {
			return out[i].Status.Expired // 已过期的永远排最前
		}
		if out[i].Status.DaysLeft != out[j].Status.DaysLeft {
			return out[i].Status.DaysLeft < out[j].Status.DaysLeft
		}
		return out[i].CompanyID < out[j].CompanyID // 天数相同按 id 稳定排序
	})
	return out
}

func modeName(private bool) string {
	if private {
		return "private"
	}
	return "platform"
}
