package handler

import (
	"errors"
	"net/http"
	"strconv"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 小程序「默认进入」设置（0034）：裸启动落到名片墙还是某个员工的名片。
//
// 为什么这件事必须由服务端说了算：裸启动（扫微信「体验版二维码」/ 直接打开）时
// 微信不带任何参数，端上连 staffId 都没有，更不可能知道自己该去哪个页面。
// 端上只会先落到 entryPagePath（名片墙），拿到本接口下发的 entry 再决定要不要改道。
//
// 两组函数的分工（别合并）：
//   - mpEntryConfig    读**配置原值**，给管理端看「你设的是什么」；
//   - resolveMPEntry   给**实际生效值**，给公开接口用。
// 两者会不一致，且不一致正是管理端要显示出来的信息：管理员设了「指定张三」，
// 而张三后来停用了 —— 访客实际看到的是名片墙。如果管理端也回填生效值，
// 管理员只会看到「名片墙」，永远不知道自己那份配置被静默忽略了。

// mpEntryConfig 读某租户的落地页配置原值（不校验员工是否还有效）。
func mpEntryConfig(db *gorm.DB, companyID int64) (mode string, staffID int64) {
	var ci model.CompanyInfo
	if err := db.Where("company_id = ?", companyID).First(&ci).Error; err != nil {
		// 企业信息行不存在（独立版 / 存量库手工建的租户）：等价于「没配过」
		return model.MpEntryWall, 0
	}
	return model.NormalizeMpEntryMode(ci.MpEntryMode), ci.MpEntryStaffID
}

// mpStaffUsable 指定员工当前是否还能作为落地页：存在、属于本企业、启用中。
//
// 用 Scan 而不是 First(&model.Employee{})：员工的姓名/手机是 PII 密文列，
// AfterFind 钩子要靠 name 列解密，而我们这里只想确认「这个人还在不在」。
// 走结构体查询会让每一都解密一次（`Select` 不带 name 时还会打解密失败的 WARN），
// 一个只要三个字段的判断不该有那些副作用。
func mpStaffUsable(db *gorm.DB, companyID, staffID int64) bool {
	if staffID <= 0 {
		return false
	}
	var row struct {
		StaffID   int64  `gorm:"column:staff_id"`
		CompanyID int64  `gorm:"column:company_id"`
		Status    string `gorm:"column:status"`
	}
	res := db.Model(&model.Employee{}).
		Select("staff_id, company_id, status").
		Where("staff_id = ? AND company_id = ?", staffID, companyID).
		Limit(1).Scan(&row)
	if res.Error != nil {
		return false
	}
	if res.RowsAffected == 0 {
		return false
	}
	return row.Status == "enabled"
}

// resolveMPEntry 解析「实际生效」的裸启动落地策略。
//
// 指定员工已不存在 / 已不属于本企业 / 已停用 → **回落名片墙**，而不是把访客
// 送去一张「该员工名片已停用」的空页。回落而不是报错：落地页是访客第一眼，
// 任何情况下都得有东西看。
func resolveMPEntry(db *gorm.DB, companyID int64) (mode string, staffID int64) {
	mode, staffID = mpEntryConfig(db, companyID)
	if mode != model.MpEntryStaff {
		return model.MpEntryWall, 0
	}
	if !mpStaffUsable(db, companyID, staffID) {
		return model.MpEntryWall, 0
	}
	return model.MpEntryStaff, staffID
}

// saveMPEntry 写落地页配置。
//
// 用 map 而不是结构体：GORM 会跳过结构体里的零值，用结构体写就**永远清不掉**
// staff_id（从「指定员工」切回「名片墙」时本该归零）。
func saveMPEntry(db *gorm.DB, companyID int64, mode string, staffID int64) error {
	fields := map[string]interface{}{"mp_entry_mode": mode, "mp_entry_staff_id": staffID}
	var ci model.CompanyInfo
	err := db.Where("company_id = ?", companyID).First(&ci).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 企业信息行不存在：补一行，其余字段留空，管理员后续在「公司信息」页完善。
		// 这里不能用 Updates —— 打空的表会 0 行且不报错，配置就此静默丢失。
		return db.Create(&model.CompanyInfo{
			CompanyID: companyID, MpEntryMode: mode, MpEntryStaffID: staffID,
		}).Error
	}
	if err != nil {
		return err
	}
	// 只判 res.Error，不判 RowsAffected：行已确认存在，MySQL 在「值没变」时
	// 也返回 0（affected rows 而非 matched rows），那不是失败。
	return db.Model(&ci).Updates(fields).Error
}

// RegisterMPEntry 注册小程序「默认进入」设置接口（企业管理员 / 平台超管均可）。
//
// 企业管理员改本企业（targetCompanyID 固定本企业），平台超管可用 ?companyId= 指定。
func RegisterMPEntry(r *gin.Engine, db *gorm.DB) {
	auth := middleware.AuthMiddleware(db, "admin")

	// 读取当前设置（配置原值 + 实际生效值 + 被指定员工的现状）
	r.GET("/api/admin/mp-entry", auth, func(c *gin.Context) {
		cid := targetCompanyID(c)
		configuredMode, configuredStaffID := mpEntryConfig(db, cid)
		effectiveMode, _ := resolveMPEntry(db, cid)

		data := gin.H{
			"mode":    configuredMode,
			"staffId": configuredStaffID,
			// effectiveMode 与 mode 不一致时，界面必须提示「指定员工已失效，
			// 访客实际看到的是名片墙」——否则管理员会以为自己的设置生效了。
			"effectiveMode": effectiveMode,
			"staffName":     "",
			"staffTitle":    "",
			"staffAvatar":   "",
			"staffStatus":   "",
			// staffMissing：指定的员工记录已经不在了（被删除），与「停用」分开报，
			// 因为界面文案不同（一个要他重新选，一个可以点启用恢复）
			"staffMissing": false,
		}
		if configuredStaffID > 0 {
			var e model.Employee
			if err := db.Where("staff_id = ? AND company_id = ?", configuredStaffID, cid).First(&e).Error; err == nil {
				data["staffName"] = e.Name
				data["staffTitle"] = e.Title
				data["staffAvatar"] = e.Avatar
				data["staffStatus"] = e.Status
			} else {
				data["staffMissing"] = true
			}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
	})

	// 保存设置
	r.PUT("/api/admin/mp-entry", auth, func(c *gin.Context) {
		cid := targetCompanyID(c)
		var req struct {
			Mode    string `json:"mode"`
			StaffID int64  `json:"staffId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 显式传了非法值就拒绝。NormalizeMpEntryMode 的「归一」是留给历史脏数据的，
		// 不能拿它兜客户的错误输入——那样拼错的 'wal' 会静默变成名片墙，
		// 管理员以为设了却没生效、也查不出原因。
		if req.Mode != model.MpEntryWall && req.Mode != model.MpEntryStaff {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "mode 只能是 wall 或 staff"})
			return
		}
		staffID := int64(0)
		if req.Mode == model.MpEntryStaff {
			if req.StaffID <= 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "请选择要展示的员工"})
				return
			}
			// 必须属于本企业：不校验的话 A 企业能把落地页指向 B 企业的员工，
			// 访客扫 A 家的码进来看到的是 B 家的人——静默串租户，且没人会报错。
			var emp model.Employee
			if err := db.Where("staff_id = ? AND company_id = ?", req.StaffID, cid).First(&emp).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工不存在或不属于本企业"})
				return
			}
			if emp.Status != "enabled" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该员工已停用，不能作为小程序入口"})
				return
			}
			staffID = req.StaffID
		}
		if err := saveMPEntry(db, cid, req.Mode, staffID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		}
		// 这一项决定所有访客的第一眼，属于特权动作，必须留痕
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionMPEntry, TargetType: "company", TargetID: strconv.FormatInt(cid, 10),
			Detail: map[string]any{"mode": req.Mode, "staffId": staffID},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}
