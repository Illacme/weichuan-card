package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 微信绑定的「可维护」层（P1，2026-11-08）。
//
// P0 只解决了「看得见」，P1 补上三件事：集中清单、按完整 openid 反查、后台代解绑。
// 设计底线（改这里之前先读一遍）：
//   - **绝不代绑**。绑定必须本人在小程序用一次性 code 经 code2session 换取；
//     后台只能把已有绑定清掉，不能凭一个 openid 给别人开免密入口。
//   - **openid 只进不出**。反查是「输入完整值 → 输出账号」，输出里连掩码之外的
//     原文都不能有；审计 Detail 里连掩码都不写，只记命中条数。
//   - **租户隔离**靠 scopeCond：企业管理员只能看/解绑本企业，平台超管可看全部。
//     反查是跨租户关联查询，只给平台超管。
//   - **解绑同时踢会话**：自增 token_version，让该账号已签发的所有令牌立即失效。
//     手机丢失时「解了绑但手机上还登着」等于没解，这个副作用必须在确认弹窗说清楚。
//
// 三个端点刻意挂在 RegisterAdmins 内部（见文件末），不新增顶层 RegisterX——
// 新增顶层注册函数要同步改 cmd/server、cmd/devserver、handler_test 三处，
// 漏一处就是「测试全绿、线上静默 404」。
func registerWechatAdmin(r *gin.Engine, db *gorm.DB) {
	admin := middleware.AuthMiddleware(db, "admin")
	plat := middleware.PlatformAdminMiddleware(db)

	// ===== 1. 绑定清单（两类管理员都可用，按身份收窄租户）=====
	// 只列**已绑定**的账号：这里是「可解绑对象」列表，未绑定的行没有可执行的动作，
	// 而「谁没绑」已由 P0 的三个账号页各自展示。
	r.GET("/api/admin/wechat/bindings", admin, func(c *gin.Context) {
		var admins []model.Admin
		if err := db.Model(&model.Admin{}).Where("wx_openid <> ?", "").
			Scopes(scopeCond(c)).Find(&admins).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		var emps []model.Employee
		if err := db.Model(&model.Employee{}).Where("openid <> ?", "").
			Scopes(scopeCond(c)).Find(&emps).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		rows := filterBindingRows(buildBindingRows(db, admins, emps), c.Query("keyword"), c.Query("kind"))
		pageBindingRows(c, rows)
	})

	// ===== 2. 按完整 openid 反查（平台超管专属，只读但破例留痕）=====
	r.POST("/api/admin/wechat/lookup", plat, func(c *gin.Context) {
		var req struct {
			OpenID string `json:"openid"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		openid := strings.TrimSpace(req.OpenID)
		// 微信 openid 通常 28 字符；设上限是为了不让这里变成任意长度字符串的
		// 全表扫描放大器，不是格式校验（别在这里试图「验证 openid 长得对不对」）。
		if openid == "" || len(openid) > 128 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "请填写完整的 openid（不超过 128 字符）"})
			return
		}
		var admins []model.Admin
		if err := db.Where("wx_openid = ?", openid).Find(&admins).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		var emps []model.Employee
		if err := db.Where("openid = ?", openid).Find(&emps).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		rows := buildBindingRows(db, admins, emps)
		// 只读却必须留痕：这是把「微信身份 → 后台账号/企业」关联起来的少数入口。
		// Detail 只记条数，绝不记 openid / 掩码 / 命中的姓名。
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionWechatLookup, TargetType: "wechat", TargetID: "openid",
			Detail: map[string]any{"admin_count": len(admins), "employee_count": len(emps)},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": rows, "total": len(rows)})
	})

	// ===== 3. 解绑（两类管理员都可用，按身份收窄租户）=====
	r.POST("/api/admin/wechat/unbind", admin, func(c *gin.Context) {
		var req struct {
			Kind string `json:"kind"` // admin / employee
			ID   int64  `json:"id"`
		}
		if c.ShouldBindJSON(&req) != nil || req.ID <= 0 ||
			(req.Kind != "admin" && req.Kind != "employee") {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		scope := scopeCond(c)
		var companyID int64
		var self bool
		var action string

		if req.Kind == "admin" {
			var a model.Admin
			if err := db.Where("id = ?", req.ID).Scopes(scope).First(&a).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "账号不存在（或不在本企业）"})
				return
			}
			if a.WXOpenID == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该账号未绑定微信"})
				return
			}
			companyID, self, action = a.CompanyID, unbindIsSelf("admin", a.ID, c), audit.ActionAdminWXUnbind
			// 一次写完成「清绑定 + 安全戳自增」：分两条 UPDATE 之间若进程挂掉，
			// 会留下「绑已解、手机上令牌还有效」的窗口。
			res := db.Model(&model.Admin{}).Where("id = ?", a.ID).Scopes(scope).
				Updates(map[string]any{
					"wx_openid":     "",
					"token_version": gorm.Expr("token_version + 1"),
				})
			if res.Error != nil || res.RowsAffected == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "解绑失败"})
				return
			}
		} else {
			var e model.Employee
			if err := db.Where("staff_id = ?", req.ID).Scopes(scope).First(&e).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "员工不存在（或不在本企业）"})
				return
			}
			if e.OpenID == "" {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该账号未绑定微信"})
				return
			}
			companyID, self, action = e.CompanyID, unbindIsSelf("employee", e.StaffID, c), audit.ActionEmployeeWXUnbind
			res := db.Model(&model.Employee{}).Where("staff_id = ?", e.StaffID).Scopes(scope).
				Updates(map[string]any{
					"openid":        "",
					"token_version": gorm.Expr("token_version + 1"),
				})
			if res.Error != nil || res.RowsAffected == 0 {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "解绑失败"})
				return
			}
		}
		// 写库成功之后再记：审计要回答「真的解掉了没有」，先记后写会在失败时留下假证据。
		// Detail 不记姓名 / openid / 掩码——审计表不该变成第二份 PII 副本。
		audit.Record(db, c, audit.Entry{
			Action: action, TargetType: req.Kind, TargetID: strconv.FormatInt(req.ID, 10),
			Detail: map[string]any{"companyId": companyID},
		})
		// self=true 表示解的是自己：安全戳自增后当前登录也会失效，前端据此提示重新登录。
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "msg": "已解绑", "self": self, "companyId": companyID,
		})
	})
}

// unbindIsSelf 只有「解绑的是当前登录的同一个管理员账号」才算动到了自己。
//
// 员工与管理员分属两张表、主键各自自增，数值撞车纯属巧合；而本端点要求 role=admin，
// 员工根本不可能是调用者。若直接拿 id 比 staffId，企业管理员解绑一个 staff_id 恰好
// 等于自己 admin.id 的员工时会误报 self=true —— 前端据此把人踢去重新登录，
// 而真实情况是「别人的账号被解绑了，我却被迫退出」。
func unbindIsSelf(kind string, id int64, c *gin.Context) bool {
	return kind == "admin" && c.GetString("role") == "admin" && id == c.GetInt64("staffId")
}

// wechatBindingRow 清单 / 反查共用的展示行。
// 刻意不含 openid 原文：两个端点都只回掩码。
type wechatBindingRow struct {
	Kind           string    `json:"kind"` // admin / employee
	ID             int64     `json:"id"`
	Name           string    `json:"name"` // 管理员 username / 员工姓名
	CompanyID      int64     `json:"companyId"`
	CompanyName    string    `json:"companyName"`
	WechatBound    bool      `json:"wechatBound"`
	WXOpenidMasked string    `json:"wxOpenidMasked"`
	CreatedAt      time.Time `json:"createdAt"`
}

// buildBindingRows 把两张表的记录拼成统一行，并批量补企业名（一次查表，不做 N+1）。
// 排序按「类型 → 企业 → id」，让平台超管在跨租户清单里能按企业成组看。
func buildBindingRows(db *gorm.DB, admins []model.Admin, emps []model.Employee) []wechatBindingRow {
	names := make(map[int64]string)
	var comps []model.Company
	_ = db.Find(&comps).Error // 企业名只是展示增强，查不到时回落「企业 #id」，不阻断主流程
	for _, comp := range comps {
		n := strings.TrimSpace(comp.ShortName)
		if n == "" {
			n = comp.Name
		}
		names[comp.ID] = n
	}
	label := func(id int64) string {
		if id == 0 {
			return "平台"
		}
		if n := names[id]; n != "" {
			return n
		}
		return "企业 #" + strconv.FormatInt(id, 10)
	}

	rows := make([]wechatBindingRow, 0, len(admins)+len(emps))
	for _, a := range admins {
		rows = append(rows, wechatBindingRow{
			Kind: "admin", ID: a.ID, Name: a.Username,
			CompanyID: a.CompanyID, CompanyName: label(a.CompanyID),
			WechatBound: a.WXOpenID != "", WXOpenidMasked: maskOpenIDForDisplay(a.WXOpenID),
			CreatedAt: a.CreatedAt,
		})
	}
	for _, e := range emps {
		rows = append(rows, wechatBindingRow{
			Kind: "employee", ID: e.StaffID, Name: e.Name,
			CompanyID: e.CompanyID, CompanyName: label(e.CompanyID),
			WechatBound: e.OpenID != "", WXOpenidMasked: maskOpenIDForDisplay(e.OpenID),
			CreatedAt: e.CreatedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		if rows[i].CompanyID != rows[j].CompanyID {
			return rows[i].CompanyID < rows[j].CompanyID
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

// filterBindingRows 关键字匹配展示字段（姓名 / 企业名 / 掩码 / id），kind 按账号类型收窄。
// 在内存里过滤是因为员工姓名是 PII 密文列，SQL 层 LIKE 打不到明文；
// 掩码列同理是展示层派生的，库里没有。过滤必须在分页**之前**做，否则
// 「管理员 / 员工」页签下 total 仍是全量，翻页会翻出空白页。
func filterBindingRows(rows []wechatBindingRow, kw, kind string) []wechatBindingRow {
	kw = strings.ToLower(strings.TrimSpace(kw))
	kind = strings.TrimSpace(kind)
	if kind != "admin" && kind != "employee" {
		kind = "" // 非法值按「全部」处理：手拼查询串不该把清单弄成空列表
	}
	if kw == "" && kind == "" {
		return rows
	}
	out := make([]wechatBindingRow, 0, len(rows))
	for _, r := range rows {
		if kind != "" && r.Kind != kind {
			continue
		}
		if kw == "" ||
			strings.Contains(strings.ToLower(r.Name), kw) ||
			strings.Contains(strings.ToLower(r.CompanyName), kw) ||
			strings.Contains(strings.ToLower(r.WXOpenidMasked), kw) ||
			strings.Contains(strconv.FormatInt(r.ID, 10), kw) {
			out = append(out, r)
		}
	}
	return out
}

// pageBindingRows 对内存合并结果切片，沿用项目统一的分页信封与「越界页码夹到末页」。
// 不清空数据本身（总数是真实命中数），因此解绑后列表能自然回到有效页。
func pageBindingRows(c *gin.Context, rows []wechatBindingRow) {
	page, size := pageParams(c)
	total := int64(len(rows))
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * size
	end := start + size
	if start > len(rows) {
		start = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	pagedOK(c, rows[start:end], total, page, size)
}
