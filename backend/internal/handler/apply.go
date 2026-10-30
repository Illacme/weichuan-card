package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/license"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/pii"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterApply 注册写操作：开通申请 / 名片修改审核 / 员工管理 / 统计。
// 多租户：所有 /api/admin 端点按 tenantScope 收窄（企业管理员只见本企业）；
// 公开申请端点按请求体 companyId 归属租户（缺省 1 号租户朗汇）。
func RegisterApply(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	// 员工自助提交开通申请（公开）
	r.POST("/api/apply/open", func(c *gin.Context) {
		var req model.OpenApply
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 租户归属：客户端可传 companyId（第二期按企业分发用）；缺省落朗汇。
		// 以服务端白名单为准——只接受已存在且启用的租户，防越权申请到别家。
		if req.CompanyID <= 0 {
			req.CompanyID = defaultCompanyID
		}
		if !companyEnabled(db, req.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "申请目标企业不存在或已停用"})
			return
		}
		// 姓名 + 手机为必填：手机是登录账号，空串会让盲索引撞上空值行、
		// 且无法作为账号发放初始密码。
		if req.Name == "" || req.Phone == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "姓名与手机为必填"})
			return
		}
		// 同一企业手机号唯一（见 phoneInUse 注释）：同一号反复提交开通申请、
		// 每次都回「已提交」是需求明确禁止的不合理行为。
		if taken, err := phoneInUse(db, req.CompanyID, req.Phone, 0); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "提交失败"})
			return
		} else if taken {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该手机号已申请过，请勿重复提交"})
			return
		}
		// 内容安全：申请里的姓名 / 昵称 / 职位 / 邮箱都是访客自己填的 UGC。
		//
		// 注意 openid 是客户端提交的，省略它就能跳过检测——所以机器检测在这条
		// 链路上是**增强**而不是硬闸，真正兜底的是后台人工审核（pending → 审核）。
		// 不改成「无 openid 就拒」是因为 v2 接口的 openid 必填，那样等于把匿名
		// 申请整条路封死。
		if d := contentSecText(db, cfg, req.CompanyID, req.OpenID,
			req.Name, req.Nickname, req.Title, req.Email); !d.Allowed {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": d.Reason})
			return
		}
		req.Status = "pending"
		if err := db.Create(&req).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "提交失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已提交，等待审核"})
	})

	// 员工本人名片信息（需登录）。
	// 响应根级附带 pendingModify：是否有待审的名片修改申请——编辑名片页据此
	// 展示「审核中」标签（需求 B9：审核期对外仍显示旧信息，仅 approve 时写回）。
	// 放根级而非塞进 data，是不动既有 data=Employee 契约（小程序 edit/me 两页在用）。
	r.GET("/api/employee/me", middleware.AuthMiddleware(db, "employee"), func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var e model.Employee
		if err := db.First(&e, "staff_id = ?", staffID).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		var pending int64
		db.Model(&model.CardModifyApply{}).Where("staff_id = ? AND status = ?", staffID, "pending").Count(&pending)
		// 企业名称（非敏感，供小程序「个人信息」页展示「所属企业」）：
		// 与 pendingModify 同理挂在根级，不塞进 data——不动既有 data=Employee 契约。
		// 查库失败只是名称留空，不阻断名片读取（员工自己仍能看资料）。
		companyName, companyShortName := "", ""
		if e.CompanyID > 0 {
			var comp model.Company
			if db.First(&comp, "id = ?", e.CompanyID).Error == nil {
				companyName = comp.Name
				companyShortName = comp.ShortName
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "data": e, "pendingModify": pending > 0,
			// 是否已绑定微信：「我的」页据此显示「已绑定 / 点击绑定」。
			// 单独给一个布尔值，而不是让前端去判 data.openid 是否为空——
			// 「有没有绑定」是产品语义，前端不该知道它是由 openid 这个字段承载的。
			"wechatBound": e.OpenID != "",
			"companyName": companyName, "companyShortName": companyShortName,
		})
	})

	// 员工提交名片修改（待审，对外仍显示旧信息）
	r.PUT("/api/employee/me", middleware.AuthMiddleware(db, "employee"), func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var patch struct {
			Avatar   string `json:"avatar"`
			Name     string `json:"name"`
			Title    string `json:"title"`
			Phone    string `json:"phone"`
			Phone2   string `json:"phone2"`
			Landline string `json:"landline"`
			Email    string `json:"email"`
		}
		if err := c.ShouldBindJSON(&patch); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var me model.Employee
		if db.First(&me, "staff_id = ?", staffID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		// 内容安全：名片上的姓名 / 职位是员工自己填的 UGC，审核通过后会对外展示。
		// 在**提交时**拦，而不是等审核时人工发现——否则违规内容会先在待审队列里
		// 躺几天，期间还可能被别的管理员一键通过。
		if d := contentSecText(db, cfg, me.CompanyID, me.OpenID, patch.Name, patch.Title); !d.Allowed {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": d.Reason})
			return
		}
		snap, _ := json.Marshal(patch)
		apply := model.CardModifyApply{CompanyID: me.CompanyID, StaffID: staffID, Snapshot: string(snap), Status: "pending"}
		if err := db.Create(&apply).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "提交失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已提交，等待审核"})
	})

	// 员工本人注销账号（PIPL 删除权）：删除本人员工记录及关联的 PII。
	//
	// 删除范围：员工主记录 + 待审名片修改申请（CardModifyApply）+ 本人访客统计
	// （StatEvent，按 staff_id 隔离，删员工后这些行本就无法被任何查询命中，这里直接清掉）。
	// 头像文件保留在对象存储里（storage 接口目前无 Delete 方法，留孤儿文件）——这是已知
	// 限制：数据库侧的身份关联已全部断开，残留的只是无法关联到任何人的一张图。
	//
	// 这是不可逆的特权动作，必须留痕；且必须按**已登录的本人**身份删，不能传 id 删别人。
	r.POST("/api/employee/me/cancel", middleware.AuthMiddleware(db, "employee"), func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var e model.Employee
		if db.First(&e, "staff_id = ?", staffID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		// 用一个显式事务把三张表的删除包起来：要么全删、要么全不删，
		// 避免「员工删了但统计还在」这种半成品（半成品会让后续审计/统计对不上）。
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("staff_id = ?", staffID).Delete(&model.CardModifyApply{}).Error; err != nil {
				return err
			}
			if err := tx.Where("staff_id = ?", staffID).Delete(&model.StatEvent{}).Error; err != nil {
				return err
			}
			// 主记录最后删：前面两张表靠 staff_id 关联，主记录没了关联也就断了
			if err := tx.Where("staff_id = ?", staffID).Delete(&model.Employee{}).Error; err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "注销失败：" + err.Error()})
			return
		}
		// 注销是特权动作，记审计（绝不记姓名/手机等明文 PII，只记编号）
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionEmployeeCancel,
			TargetType: "employee",
			TargetID:   strconv.FormatInt(staffID, 10),
			Detail:     map[string]any{"companyId": e.CompanyID},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "账号已注销"})
	})

	// 员工本人统计（?from=&to= 日期范围，YYYY-MM-DD 闭区间，均可缺省=全部时间）。
	//
	// 一次 statMeSnapshot 拿齐五块：八类事件汇总 + 按天趋势 + 案例/服务
	// 内容浏览 TOP3 + **访客来源分布**。前四块见上方说明，来源分布单独说：
	// 海报是唯一需要员工主动产出的物料（生成 → 传播），「海报带来了多少访问」
	// 只有来源分布能回答——裂变那两个计数（share/poster_save）主体可能是
	// 员工本人，证明不了海报有没有用。
	//
	// 兼容性：data 里原有的 visit/call/save 三个键保留不动（additive），
	// 老版本小程序照旧能读；新增五个意向计数与 trend/topProjects/topServices/sourceDist。
	// 形状对齐管理端 /api/admin/stats 的 data.summary / data.trend / data.topXxx / data.sourceDist。
	r.GET("/api/employee/me/stat", middleware.AuthMiddleware(db, "employee"), func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		from, to, ok := statDateRange(c)
		if !ok {
			// 坏参数不静默忽略：筛了却没生效比不筛更难排查
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "日期参数错误"})
			return
		}
		snap, err := statMeSnapshot(db, staffID, from, to)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		sum := snap.Summary
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"visit": sum.Visit, "call": sum.Call, "save": sum.Save,
				"projectView": sum.ProjectView, "serviceView": sum.ServiceView,
				"videoPlay": sum.VideoPlay, "share": sum.Share, "posterSave": sum.PosterSave,
				"trend":       statTrendJSON(snap.Trend),
				"topProjects": statTopJSON(snap.TopProjects),
				"topServices": statTopJSON(snap.TopServices),
				// 访客来源分布：只统计 visit（扫码/转发卡片/搜索/海报/未知/其他），
				// 各行 count 之和恒等于上面的 visit——前端可据此对账
				"sourceDist": statSourceJSON(snap.SourceDist),
			},
		})
	})

	// 员工修改本人密码
	r.PUT("/api/employee/password", middleware.AuthMiddleware(db, "employee"), func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var req struct {
			Old string `json:"old"`
			New string `json:"new"`
		}
		if c.ShouldBindJSON(&req) != nil || req.New == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var e model.Employee
		db.First(&e, "staff_id = ?", staffID)
		if !auth.CheckPassword(e.PasswordHash, req.Old) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "原密码错误"})
			return
		}
		h, _ := auth.HashPassword(req.New)
		// 改密同时自增安全戳 → 旧令牌立即失效，需重新登录
		db.Model(&e).Updates(map[string]interface{}{"password_hash": h, "token_version": e.TokenVersion + 1})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// ===== 超管端（多租户：按 tenantScope 收窄） =====

	// inScope 判断某租户 id 是否在当前用户可见范围内（平台超管恒真）。
	inScope := func(c *gin.Context, cid int64) bool {
		scid, all := tenantScope(c)
		return all || scid == cid
	}

	// 开通申请列表（按租户）：分页 + 状态筛选（pending/approved/rejected，空=全部）
	// + 关键字（姓名/手机/邮箱/昵称）——关键字必须落在服务端，否则前端「只筛当前页」
	// 会让运营以为搜到的数据翻页后丢了。
	r.GET("/api/admin/apply/open", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		page, size := pageParams(c)
		q := db.Model(&model.OpenApply{}).Scopes(scopeCond(c))
		// 申请单的姓名/手机/邮箱/昵称已加密：用盲索引做等值匹配（密文无法 LIKE）。
		if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
			bidx := pii.BlindIndex(kw)
			q = q.Where("name_bidx = ? OR phone_bidx = ? OR email_bidx = ? OR nickname_bidx = ?",
				bidx, bidx, bidx, bidx)
		}
		if cond, v := statusCond(c.Query("status"), "pending", "approved", "rejected"); cond != "" {
			q = q.Where(cond, v)
		}
		var list []model.OpenApply
		total, page := pagedQuery(q, "created_at desc", &list, page, size)
		pagedOK(c, list, total, page, size)
	})

	// 开通申请通过 → 自动建员工账号（员工落到申请所属租户）
	r.POST("/api/admin/apply/open/:id/approve", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var a model.OpenApply
		if db.First(&a, "id = ?", id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		if !inScope(c, a.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "无权操作其他企业的申请"})
			return
		}
		if a.Status != "pending" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "已处理"})
			return
		}
		// 同一企业手机号唯一：申请通过后要建员工账号，若此时该号已被占用
		// （别人已建 / 另有待审申请），不能静默建出重复账号。
		if taken, err := phoneInUse(db, a.CompanyID, a.Phone, a.ID); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建员工账号失败"})
			return
		} else if taken {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该手机号已存在，无法重复建号"})
			return
		}
		// 席位上限：审批通过会自动建员工，与「管理员直接新增员工」是同一条
		// 建号路径，必须同样受授权席位约束。此前只有直接新增那条被卡，
		// 管理员可以反复通过申请无限建号——席位上限形同虚设，客户超额使用
		// 却不付费。按**申请所属租户**判（不是操作者所在租户）。
		if ent := license.ResolveFor(c.Request.Context(), a.CompanyID); seatExceeded(db, ent, a.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "员工席位已达授权上限，请升级授权"})
			return
		}
		pwd, err := genPassword()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		hash, err := auth.HashPassword(pwd)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "密码加密失败"})
			return
		}
		emp := model.Employee{CompanyID: a.CompanyID, Name: a.Name, Phone: a.Phone, Phone2: a.Phone2, Email: a.Email, Title: a.Title, Landline: a.Landline, Avatar: a.Avatar, OpenID: a.OpenID, Nickname: a.Nickname, PasswordHash: hash, Status: "enabled"}
		// 建号失败必须报错：此前 db.Create 的错误被丢掉，失败时照样返回
		// code:0 + staffId + 初始密码——管理员拿到一个**不存在的员工编号**
		// 和一个**永远登不进去的密码**，而申请单还会被标成 approved
		// （无从重试，只能手工改库）。宁可如实报错，让管理员再点一次。
		if err := db.Create(&emp).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建员工账号失败：" + err.Error()})
			return
		}
		a.Status = "approved"
		// 同理：申请单状态没落库时不能装作成功——申请还留在 pending，
		// 管理员会看到「待审核」却已经建了号，再点一次会重复建人。
		if err := db.Save(&a).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工已创建，但申请状态更新失败，请刷新确认"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "staffId": emp.StaffID, "initialPassword": pwd})
	})

	// 开通申请驳回（按租户）
	r.POST("/api/admin/apply/open/:id/reject", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Reason string `json:"reason"`
		}
		// 驳回理由可选：绑定失败（空 body / 非 JSON）按「没有理由」处理，
		// 不因此拒绝这次操作——强制要求理由只会逼管理员随手填一串无意义字符。
		_ = c.ShouldBindJSON(&body)
		q := db.Model(&model.OpenApply{}).Where("id = ? AND status = ?", id, "pending").Scopes(scopeCond(c))
		if q.Updates(map[string]interface{}{"status": "rejected", "reject_reason": body.Reason}).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到或已处理"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 名片修改申请列表（按租户：company_id 冗余自员工归属）：分页 + 状态筛选。
	// 关键字按**员工姓名**匹配（子查询）：申请行本身只有 staff_id + 变更快照，
	// 直接对快照做 LIKE 在 MySQL 的 JSON 列上行为不一致（SQLite 存的是 TEXT），
	// 而运营搜索的实际意图就是「找某个人的申请」。
	r.GET("/api/admin/apply/modify", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		page, size := pageParams(c)
		q := db.Model(&model.CardModifyApply{}).Scopes(scopeCond(c))
		// 员工姓名已加密：用盲索引做等值匹配（密文无法 LIKE），语义由「模糊」收窄为「精确」。
		if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
			sub := db.Model(&model.Employee{}).Select("staff_id").Where("name_bidx = ?", pii.BlindIndex(kw))
			q = q.Where("staff_id IN (?)", sub)
		}
		if cond, v := statusCond(c.Query("status"), "pending", "approved", "rejected"); cond != "" {
			q = q.Where(cond, v)
		}
		var list []model.CardModifyApply
		total, page := pagedQuery(q, "created_at desc", &list, page, size)
		pagedOK(c, list, total, page, size)
	})

	// 名片修改通过 → 应用快照到员工（按租户校验）
	r.POST("/api/admin/apply/modify/:id/approve", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var a model.CardModifyApply
		if db.First(&a, "id = ?", id).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		var e model.Employee
		if db.First(&e, "staff_id = ?", a.StaffID).Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工不存在"})
			return
		}
		if !inScope(c, e.CompanyID) {
			c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "无权操作其他企业的申请"})
			return
		}
		if a.Status != "pending" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "已处理"})
			return
		}
		var patch map[string]string
		_ = json.Unmarshal([]byte(a.Snapshot), &patch)
		updates := map[string]interface{}{}
		// 非 PII：直接明文更新
		if v, ok := patch["avatar"]; ok {
			updates["avatar"] = v
		}
		if v, ok := patch["title"]; ok {
			updates["title"] = v
		}
		if v, ok := patch["landline"]; ok {
			updates["landline"] = v
		}
		// PII（姓名/手机/手机2/邮箱）：map 更新不触发模型钩子，必须显式加密 + 补盲索引，
		// 否则会绕过 Employee 的加密、把明文写进库。
		for _, kv := range []struct{ col, key string }{
			{"name", "name"}, {"phone", "phone"}, {"phone2", "phone2"}, {"email", "email"},
		} {
			if v, ok := patch[kv.key]; ok {
				if err := setPII(updates, kv.col, v); err != nil {
					c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "资料加密失败"})
					return
				}
			}
		}
		if err := db.Model(&e).Updates(updates).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "更新员工资料失败"})
			return
		}
		a.Status = "approved"
		db.Save(&a)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 名片修改驳回（按租户）
	r.POST("/api/admin/apply/modify/:id/reject", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var body struct {
			Reason string `json:"reason"`
		}
		// 同开通申请：理由可选，绑定失败按「没有理由」处理（见上面那条注释）
		_ = c.ShouldBindJSON(&body)
		q := db.Model(&model.CardModifyApply{}).Where("id = ? AND status = ?", id, "pending").Scopes(scopeCond(c))
		if q.Updates(map[string]interface{}{"status": "rejected", "reject_reason": body.Reason}).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到或已处理"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 超管手动新增员工（自动生成账号 + 初始密码）。
	// 租户归属：企业管理员 → 本企业；平台超管 → ?companyId=（缺省朗汇）。
	r.POST("/api/admin/employees", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		var req model.Employee
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		cid, _ := tenantScope(c)
		if cid == 0 {
			cid = targetCompanyID(c)
		}
		if !companyEnabled(db, cid) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "目标企业不存在或已停用"})
			return
		}
		// 手机为必填（登录账号），且同一企业内唯一（见 phoneInUse 注释）。
		if req.Phone == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "手机为必填"})
			return
		}
		if taken, err := phoneInUse(db, cid, req.Phone, 0); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建失败"})
			return
		} else if taken {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该手机号已存在，请勿重复添加"})
			return
		}
		pwd, err := genPassword()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		hash, err := auth.HashPassword(pwd)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "密码加密失败"})
			return
		}
		emp := model.Employee{CompanyID: cid, Name: req.Name, Phone: req.Phone, Phone2: req.Phone2, Email: req.Email, Title: req.Title, Landline: req.Landline, Avatar: req.Avatar, PasswordHash: hash, Status: "enabled"}
		// 建号失败必须报错（同审批路径）：失败时返回 code:0 会让管理员拿到
		// 一个不存在的员工编号与永远登不进去的密码，且无从察觉。
		if err := db.Create(&emp).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "创建员工账号失败：" + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "staffId": emp.StaffID, "initialPassword": pwd})
	})

	// 超管编辑员工资料（即时生效，不经过员工端审核流）。
	// 只开放白名单字段：姓名 / 职位 / 手机 / 手机 2 / 座机 / 邮箱 / 头像。
	// 状态、密码各有专门端点（disable/enable/reset-password），此处不混写，避免绕过安全戳逻辑。
	// 租户条件进 Where：企业管理员编辑不到别家员工。
	r.PUT("/api/admin/employees/:id", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		var req struct {
			Name     string `json:"name"`
			Title    string `json:"title"`
			Phone    string `json:"phone"`
			Phone2   string `json:"phone2"`
			Landline string `json:"landline"`
			Email    string `json:"email"`
			Avatar   string `json:"avatar"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if req.Name == "" || req.Phone == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "姓名与手机为必填"})
			return
		}
		updates := map[string]interface{}{
			"title": req.Title, "landline": req.Landline, "avatar": req.Avatar,
		}
		// PII 字段必须显式加密 + 补盲索引（map 更新绕过模型钩子）。
		for _, kv := range []struct{ col, val string }{
			{"name", req.Name}, {"phone", req.Phone}, {"phone2", req.Phone2}, {"email", req.Email},
		} {
			if err := setPII(updates, kv.col, kv.val); err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "资料加密失败"})
				return
			}
		}
		if db.Model(&model.Employee{}).Where("staff_id = ?", id).Scopes(scopeCond(c)).
			Updates(updates).RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 员工列表（按租户）
	//
	// 分页 + 关键字（姓名/手机/手机 2/职位/座机/昵称/openid）+ 状态筛选。
	// 关键字覆盖的字段与 PC 后台搜索框的匹配范围保持一致——否则「搜得到但翻页后
	// 又没了」（前端只筛当前页）会让运营以为数据丢了。
	//
	// 另有 ?staffIds=1,2,3：按 id 精确取少数几条，供「名片修改申请」页回显
	// 员工原值（替代原先拉全量员工建映射的做法），上限 200 条。
	r.GET("/api/admin/employees", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		page, size := pageParams(c)
		q := db.Model(&model.Employee{}).Scopes(scopeCond(c))
		// 员工姓名/手机/手机2/昵称已静态加密：这些列用盲索引做等值匹配（密文无法 LIKE）；
		// 职位/座机/openid 仍是明文，保留 LIKE 模糊。语义由「全字段模糊」收窄为
		// 「加密字段精确 + 明文字段模糊」——这是静态加密换来的必然取舍。
		if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
			bidx := pii.BlindIndex(kw)
			like := likeKeyword(kw)
			q = q.Where(
				"name_bidx = ? OR phone_bidx = ? OR phone2_bidx = ? OR nickname_bidx = ? "+
					"OR title LIKE ? OR landline LIKE ? OR openid LIKE ?",
				bidx, bidx, bidx, bidx, like, like, like,
			)
		}
		if cond, v := statusCond(c.Query("status"), "enabled", "disabled"); cond != "" {
			q = q.Where(cond, v)
		}
		if ids := intListParam(c.Query("staffIds"), 200); len(ids) > 0 {
			// 精确取：条数由调用方给的 id 数决定，不再分页（本身已是有界集合）
			var list []model.Employee
			q.Where("staff_id IN ?", ids).Order("staff_id asc").Find(&list)
			if size = len(list); size == 0 {
				size = defaultPageSize
			}
			pagedOK(c, list, int64(len(list)), 1, size)
			return
		}
		var list []model.Employee
		total, page := pagedQuery(q, "staff_id asc", &list, page, size)
		pagedOK(c, list, total, page, size)
	})

	// 批量导出员工（CSV，按租户隔离）：租户范围内全部员工（可带与列表页一致的
	// 关键字 / 状态筛选），解密 PII 后落 CSV。BOM + 公式注入防护复用 stats 导出同款。
	//
	// 这是「把本租户全部员工 PII 一次性带走」的高风险动作，必须记审计
	// （同 ActionEmployeeExport 的破例留痕约定，见 audit 包文件头）。
	r.GET("/api/admin/employees/export", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		q := db.Model(&model.Employee{}).Scopes(scopeCond(c))
		// 与列表页同口径的关键字 / 状态筛选：导出的就是「当前看到的筛选结果」
		if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
			bidx := pii.BlindIndex(kw)
			like := likeKeyword(kw)
			q = q.Where(
				"name_bidx = ? OR phone_bidx = ? OR phone2_bidx = ? OR nickname_bidx = ? "+
					"OR title LIKE ? OR landline LIKE ? OR openid LIKE ?",
				bidx, bidx, bidx, bidx, like, like, like,
			)
		}
		if cond, v := statusCond(c.Query("status"), "enabled", "disabled"); cond != "" {
			q = q.Where(cond, v)
		}
		var list []model.Employee
		if err := q.Order("staff_id asc").Find(&list).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		body, err := employeesCSV(list)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "导出失败"})
			return
		}
		// 高风险提取动作：先留痕再下发（写入失败只打日志、不影响本次导出）
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionEmployeeExportBatch,
			TargetType: "employee",
			TargetID:   "batch",
			Detail:     map[string]any{"count": len(list)},
		})
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", employeeExportDisposition())
		c.Data(http.StatusOK, "text/csv; charset=utf-8", body)
	})

	// 启用 / 禁用（禁用同时自增安全戳 → 该员工在线会话立即失效；按租户）
	r.POST("/api/admin/employees/:id/disable", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		// RowsAffected 必须查：从前不查，禁用一个不存在的 / 别家租户的员工也回 ok，
		// 管理员无从察觉操作压根没生效（更新的 Error 同样不能丢）
		res := db.Model(&model.Employee{}).Where("staff_id = ?", id).Scopes(scopeCond(c)).
			Updates(map[string]interface{}{"status": "disabled", "token_version": gorm.Expr("token_version + 1")})
		if res.Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": res.Error.Error()})
			return
		}
		if res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		// 启停员工是特权动作：从前这里不记审计，改自己密码反倒记了，日志里看不出谁动了谁
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionEmployeeStatus, TargetType: "employee", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"status": "disabled"},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})
	r.POST("/api/admin/employees/:id/enable", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		res := db.Model(&model.Employee{}).Where("staff_id = ?", id).Scopes(scopeCond(c)).
			Updates(map[string]interface{}{"status": "enabled", "token_version": gorm.Expr("token_version + 1")})
		if res.Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": res.Error.Error()})
			return
		}
		if res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionEmployeeStatus, TargetType: "employee", TargetID: strconv.FormatInt(id, 10),
			Detail: map[string]any{"status": "enabled"},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 重置密码（返回新初始密码；按租户）
	r.POST("/api/admin/employees/:id/reset-password", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		pwd, err := genPassword()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		hash, err := auth.HashPassword(pwd)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "密码加密失败"})
			return
		}
		// 重置密码同时自增安全戳 → 该员工所有旧会话立即失效
		res := db.Model(&model.Employee{}).Where("staff_id = ?", id).Scopes(scopeCond(c)).
			Updates(map[string]interface{}{"password_hash": hash, "token_version": gorm.Expr("token_version + 1")})
		if res.Error != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": res.Error.Error()})
			return
		}
		if res.RowsAffected == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到"})
			return
		}
		// 重置他人密码是特权动作，必须留痕（从前只记「改自己密码」，这条漏了）
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionEmployeeReset, TargetType: "employee", TargetID: strconv.FormatInt(id, 10),
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "initialPassword": pwd})
	})

	// 按员工导出个人信息（PIPL 查阅复制权协助义务）：管理员把某员工的全部 PII
	// 打包返回，用于响应数据主体的查阅/复制请求。
	//
	// 按租户隔离（scopeCond）：管理员只能导出自己企业下的员工，不能跨租户捞人。
	// 返回 envelope.data = 该员工完整记录（字段与模型一致，不含 password_hash 等
	// json:"-" 项）。前端「导出」按钮拿到后落盘成文件即完成交付。
	//
	// 审计：把一个人的全部 PII 打包导出是高风险动作，特意破例记录（审计包约定
	// 只读不记，但此处属于「可能泄露大量个人信息的提取」必须留痕）。
	r.GET("/api/admin/employees/:id/export", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if id <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var e model.Employee
		if err := db.Model(&model.Employee{}).Where("staff_id = ?", id).Scopes(scopeCond(c)).First(&e).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到（或不在本租户）"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionEmployeeExport,
			TargetType: "employee",
			TargetID:   strconv.FormatInt(id, 10),
			Detail:     map[string]any{"companyId": e.CompanyID},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": e})
	})

	// 超管修改自己密码
	r.PUT("/api/admin/password", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		adminID := c.GetInt64("staffId")
		var req struct {
			Old string `json:"old"`
			New string `json:"new"`
		}
		if c.ShouldBindJSON(&req) != nil || req.New == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		var a model.Admin
		db.First(&a, "id = ?", adminID)
		if !auth.CheckPassword(a.PasswordHash, req.Old) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "原密码错误"})
			return
		}
		h, _ := auth.HashPassword(req.New)
		// 改密同时自增安全戳 → 旧令牌立即失效，需重新登录
		db.Model(&a).Updates(map[string]interface{}{"password_hash": h, "token_version": a.TokenVersion + 1})
		// 审计只记「谁改了自己的密码」，绝不记新旧密码
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionPassword, TargetType: "admin", TargetID: strconv.FormatInt(adminID, 10),
			Detail: map[string]any{"username": a.Username},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	// 全局名片统计（超管）：汇总 + 按员工分组 + 按天趋势。
	// 与 /api/employee/me/stat（仅本人）互补，供 PC 后台「数据统计」页使用。
	// 实现要点：
	//   1. 用 SUM(CASE WHEN ...) 而非 SUM(布尔表达式)——后者是 MySQL 方言，
	//      SQLite 不认，MySQL parity 与 SQLite 会一边挂一边绿。
	//   2. 分组结果与员工表合并：零数据的员工也要出现在列表里（补 0），
	//      否则「有员工但没访问」在后台看不出来。
	//   3. 别名必须加反引号：call 是 MySQL 8 保留字（存储过程关键字），
	//      裸 AS call 直接 1064（SQLite 不保留所以潜伏——2026-10-21 parity 首跑抓出）。
	//      反引号在 SQLite 同样合法（MySQL 兼容引用符），两边都能跑。
	//   4. ?from=&to= 日期范围（YYYY-MM-DD 闭区间，可缺省）：stat_date 有索引，
	//      字符串字典序即时间序，范围查询直接走索引；缺省即「全部时间」。
	//   5. summary 后五个分两组：前三个（projectView/serviceView/videoPlay）
	//      是「内容浏览」，带 target_id（migrations/0021），按 target 聚合
	//      JOIN 对象名进 topProjects/topServices，回答「访客对什么内容感兴趣」——
	//      统计对企业客户的核心价值。后两个（share/posterSave）是「裂变动作」：
	//      不带 target_id、主体可能是员工本人，故不进 TOP 榜、也不与浏览相加
	//      （分组理由详见 stat.go 文件头）。
	r.GET("/api/admin/stats", middleware.AuthMiddleware(db, "admin"), func(c *gin.Context) {
		from, to, ok := statDateRange(c)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "日期参数错误"})
			return
		}
		rows, err := statRowsByStaff(c, db, from, to)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		trend, err := statTrend(c, db, from, to)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		topProjects, err := statTopTargets(c, db, from, to, "project_view", "project_cases", "name", statTopLimit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		topServices, err := statTopTargets(c, db, from, to, "service_view", "biz_services", "title", statTopLimit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		sourceDist, err := statSourceDist(c, db, from, to)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		fissionTop, err := statFissionTop(c, db, from, to, statFissionLimit)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		list := make([]gin.H, 0, len(rows))
		var sumVisit, sumCall, sumSave int64
		var sumProjectView, sumServiceView, sumVideoPlay, sumShare, sumPosterSave int64
		for _, r := range rows {
			sumVisit += r.Visit
			sumCall += r.Call
			sumSave += r.Save
			sumProjectView += r.ProjectView
			sumServiceView += r.ServiceView
			sumVideoPlay += r.VideoPlay
			sumShare += r.Share
			sumPosterSave += r.PosterSave
			list = append(list, gin.H{
				"staffId": r.StaffID,
				"name":    r.Name,
				"title":   r.Title,
				"avatar":  r.Avatar,
				"visit":   r.Visit,
				"call":    r.Call,
				"save":    r.Save,
				// 意向计数随行返回（CSV 同源），PC 表格不展示——列宽装不下
				"projectView": r.ProjectView,
				"serviceView": r.ServiceView,
				"videoPlay":   r.VideoPlay,
				"share":       r.Share,
				"posterSave":  r.PosterSave,
			})
		}
		// 趋势 / TOP 榜 / 来源分布的响应形状由 stat.go 的三个转换函数统一
		// （与员工端共用），避免两处各自拼 gin.H 后悄悄漂移
		// 裂变榜行 → 响应：{staffId, name, count}
		topListFission := func(rows []statFissionRow) []gin.H {
			out := make([]gin.H, 0, len(rows))
			for _, r := range rows {
				out = append(out, gin.H{"staffId": r.StaffID, "name": r.Name, "count": r.Count})
			}
			return out
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"summary": gin.H{
					"visit": sumVisit, "call": sumCall, "save": sumSave,
					"projectView": sumProjectView, "serviceView": sumServiceView,
					"videoPlay": sumVideoPlay, "share": sumShare, "posterSave": sumPosterSave,
				},
				"byStaff":     list,
				"trend":       statTrendJSON(trend),
				"topProjects": statTopJSON(topProjects),
				"topServices": statTopJSON(topServices),
				// 流量来源分布：visit 事件按 source 分组（scan/share_card/search/poster/unknown/other）
				"sourceDist": statSourceJSON(sourceDist),
				// 裂变榜：按 staff_id 聚合「source=share_card 的 visit」（谁的名片被转发带来的新访客多）
				"fissionTop": topListFission(fissionTop),
			},
		})
	})
}

// phoneInUse 判断某手机号在某企业内是否已被占用：
//   - 已建员工（enabled / disabled 都算，禁用只是停权不代表账号可回收）命中；
//   - 仍有「待审」开通申请（status=pending）也算——需求明确禁止同一号反复提交申请；
//     已驳回（rejected）的申请允许重新申请，故不拦；已通过（approved）的申请应已
//     落成员工，由上面的员工查重兜底。
//
// 命中即视为「该手机号在本企业已存在」，调用方应拒绝本次建号 / 申请。
//
// excludeApplyID 用于「审批通过」路径：待审申请在审批瞬间仍是 pending 行，若不排除自身
// 会被自己这条 pending 命中而误拒。提交 / 手动建号路径传 0（无排除）。
//
// 实现要点：明文手机是加密存储的，无法等值直查或 LIKE，只能靠 phone_bidx 盲索引
// 做等值比对（盲索引对同一明文恒定，故可判重）。空号直接返回未占用，避免空串盲
// 索引撞上别的空值行。
func phoneInUse(db *gorm.DB, companyID int64, phone string, excludeApplyID int64) (bool, error) {
	if phone == "" {
		return false, nil
	}
	bidx := pii.BlindIndex(phone)
	var n int64
	if err := db.Model(&model.Employee{}).
		Where("company_id = ? AND phone_bidx = ?", companyID, bidx).
		Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	q := db.Model(&model.OpenApply{}).
		Where("company_id = ? AND phone_bidx = ? AND status = ?", companyID, bidx, "pending")
	if excludeApplyID > 0 {
		q = q.Where("id <> ?", excludeApplyID)
	}
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// employeesCSV 把员工列表写成 CSV（BOM + 公式注入防护，与 statsCSV 同款）。
// 员工经 GORM 查询已触发 AfterFind 解密，字段都是明文；头像 URL 等自由文本
// 同样过 csvCell 防注入（见 stat.go 的 csvCell 注释：= + - @ 等开头的单元格
// Excel/WPS 会当公式执行）。
func employeesCSV(rows []model.Employee) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM：没有它 Excel 双击打开中文全乱码
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{
		"编号", "姓名", "职位", "手机", "手机2", "座机", "邮箱", "昵称", "状态", "头像", "创建时间",
	}); err != nil {
		return nil, err
	}
	for _, e := range rows {
		rec := []string{
			strconv.FormatInt(e.StaffID, 10),
			csvCell(e.Name),
			csvCell(e.Title),
			csvCell(e.Phone),
			csvCell(e.Phone2),
			csvCell(e.Landline),
			csvCell(e.Email),
			csvCell(e.Nickname),
			e.Status,
			csvCell(e.Avatar),
			e.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	// Write 的错要到 Flush 之后才在 Error() 上现形：不查就等于把
	// 「导出只剩半行」当成功交付。
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// employeeExportDisposition 下载文件名（中文走 RFC 5987 filename*，留 ASCII 兜底）。
func employeeExportDisposition() string {
	name := "员工数据_全部.csv"
	return "attachment; filename=\"employees.csv\"; filename*=UTF-8''" + url.QueryEscape(name)
}

// genPassword 生成 8 位随机密码（字母+数字）。
//
// crypto/rand 取值失败**必须回传错误**：从前写成 `n, _ := rand.Int(...)`，
// 失败时 n 是 nil，下一行 n.Int64() 直接 panic 把进程打挂——
// 一次密码生成失败不该让整个服务挂掉，更不该给员工发一个可预测的密码。
// randInt 随机源的间接层：唯一目的是可测——crypto/rand 正常不会失败，
// 不注入就覆盖不到「失败时该怎么办」这条支线（从前忽略错误会直接 panic）。
var randInt = rand.Int

func genPassword() (string, error) {
	const chars = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	for i := range b {
		n, err := randInt(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", fmt.Errorf("生成随机密码失败: %w", err)
		}
		b[i] = chars[n.Int64()]
	}
	return string(b), nil
}
