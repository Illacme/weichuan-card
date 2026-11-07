package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterAdmins 平台超管「账号管理」接口（仅平台超管可用）。
//
// 与「企业管理 → 企业管理员」的分工：
//   - 本组只操作 company_id=0 的平台超管账号（可改自己的用户名、新增同级账号、重置他人密码）；
//   - 企业管理员（company_id>0）仍归 companies.go，按企业管理。
//
// 背景：初始超管由 SeedAdmin 按 ADMIN_USER 一次性 seed，此后没有改用户名的入口，
// 账号名等于写死在首次启动那一刻（要么提前设环境变量，要么直接改库）——本组接口补上这个缺口。
//
// 安全护栏：
//   - 用户名全表唯一（含企业管理员），避免两个后台账号同名造成登录歧义；
//   - 重置密码会自增安全戳，该账号所有会话立即失效；
//   - 删除时禁止删自己、且至少保留一个平台超管，防止把自己锁在系统外面。
func RegisterAdmins(r *gin.Engine, db *gorm.DB) {
	plat := middleware.PlatformAdminMiddleware(db)

	// 平台超管列表（page / size 见 handler/pagination.go 的文件头约定）
	r.GET("/api/admin/platform-admins", plat, func(c *gin.Context) {
		page, size := pageParams(c)
		var list []model.Admin
		total, page := pagedQuery(db.Model(&model.Admin{}).Where("company_id = ?", 0), "id asc", &list, page, size)
		data := make([]gin.H, 0, len(list))
		for _, a := range list {
			data = append(data, withWechatBinding(gin.H{
				"id": a.ID, "username": a.Username, "createdAt": a.CreatedAt,
			}, a.WXOpenID))
		}
		pagedOK(c, data, total, page, size)
	})

	// 新增平台超管
	r.POST("/api/admin/platform-admins", plat, func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		req.Username = strings.TrimSpace(req.Username)
		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名与密码不能为空"})
			return
		}
		if usernameTaken(db, req.Username, 0) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名已存在"})
			return
		}
		hash, _ := auth.HashPassword(req.Password)
		a := model.Admin{CompanyID: 0, Username: req.Username, PasswordHash: hash, CreatedAt: time.Now()}
		if err := db.Create(&a).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建失败"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminCreate, TargetType: "admin", TargetID: strconv.FormatInt(a.ID, 10),
			Detail: map[string]any{"username": a.Username},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"id": a.ID, "username": a.Username}})
	})

	// 改用户名：可用于改自己，也可改同级账号
	r.PUT("/api/admin/platform-admins/:id", plat, func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Username string `json:"username"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		req.Username = strings.TrimSpace(req.Username)
		if req.Username == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名不能为空"})
			return
		}
		// 限定 company_id=0：防止借本接口改到企业管理员账号
		var a model.Admin
		if db.Where("id = ? AND company_id = ?", id, 0).First(&a).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在"})
			return
		}
		if usernameTaken(db, req.Username, id) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "用户名已存在"})
			return
		}
		db.Model(&a).Update("username", req.Username)
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminRename, TargetType: "admin", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"from": a.Username, "to": req.Username},
		})
		// 令牌里不含用户名，改名不必踢会话；回传 self 供前端刷新顶栏展示
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "self": id == c.GetInt64("staffId")})
	})

	// 重置密码（不需原密码——用于给别人重置；改自己的密码仍走 /api/admin/password 校验原密码）
	r.POST("/api/admin/platform-admins/:id/password", plat, func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Password == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "新密码不能为空"})
			return
		}
		var a model.Admin
		if db.Where("id = ? AND company_id = ?", id, 0).First(&a).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在"})
			return
		}
		hash, _ := auth.HashPassword(req.Password)
		// 安全戳自增 → 该账号已签发的所有令牌立即失效（重置自己则需重新登录）
		db.Model(&a).Updates(map[string]interface{}{
			"password_hash": hash, "token_version": a.TokenVersion + 1,
		})
		// 审计只记「给谁重置了」，绝不记新密码
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminReset, TargetType: "admin", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"username": a.Username, "self": id == c.GetInt64("staffId")},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "self": id == c.GetInt64("staffId")})
	})

	// 删除平台超管
	r.DELETE("/api/admin/platform-admins/:id", plat, func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		// 护栏一：不能删当前登录的账号（否则自己把自己踢出去，后续操作全 401）
		if id == c.GetInt64("staffId") {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "不能删除当前登录的账号"})
			return
		}
		// 护栏二：至少保留一个平台超管，避免删空后彻底进不去系统
		var cnt int64
		db.Model(&model.Admin{}).Where("company_id = ?", 0).Count(&cnt)
		if cnt <= 1 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "至少保留一个平台超管"})
			return
		}
		var a model.Admin
		if db.Where("id = ? AND company_id = ?", id, 0).First(&a).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在"})
			return
		}
		if err := db.Delete(&a).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "删除失败"})
			return
		}
		// 用户名写进详情：账号行已删，只留 id 就追溯不到是谁
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionAdminDelete, TargetType: "admin", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"username": a.Username},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
}

// usernameTaken 用户名是否已被占用（全表唯一，含企业管理员）；excludeID>0 时排除自己（改名用）。
func usernameTaken(db *gorm.DB, username string, excludeID int64) bool {
	q := db.Model(&model.Admin{}).Where("username = ?", username)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var cnt int64
	q.Count(&cnt)
	return cnt > 0
}
