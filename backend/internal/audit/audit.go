// Package audit 管理操作审计：把后台的写操作与登录失败记进 audit_log，
// 回答「谁在什么时候、对什么对象、做了什么、成功没有」。
//
// 约定（重要）：
//   - 只记**写操作**（含失败 / 被拒的尝试）与登录失败；只读查询不记，否则日志量会淹没有用信息。
//   - Detail 里**绝不能出现明文敏感值**（密钥 / 密码 / 连接串 / token）。
//     需要记录「改了什么」时，敏感项只写「已修改」——审计表一旦泄密，
//     就与它存在的初衷（安全追责）相悖。
//   - 写入失败只打日志、不影响主流程：审计是旁路，不能因为记不下来就让业务失败。
package audit

import (
	"encoding/json"
	"log"
	"time"

	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 动作常量：命名沿用「对象.动作」，便于按前缀聚合统计。
const (
	ActionSettingUpdate = "setting.update"

	ActionLoginFail = "login.fail"
	ActionLogin     = "login.success"
	ActionLogout    = "logout"
	ActionPassword  = "password.change"

	ActionAdminCreate = "admin.create"
	ActionAdminRename = "admin.rename"
	ActionAdminReset  = "admin.reset_password"
	ActionAdminDelete = "admin.delete"

	ActionCompanyCreate     = "company.create"
	ActionCompanyUpdate     = "company.update"
	ActionCompanyStatus     = "company.status"
	ActionCompanyAdminAdd   = "company.admin.create"
	ActionCompanyAdminReset = "company.admin.reset_password"
	// ActionCompanyLicense 改某企业的授权（席位 / 到期 / 功能开关）。
	// 「改授权」直接决定客户能不能继续用：延长是收钱、缩短/过期是停服，
	// 两边都会引起客诉，必须留痕（谁在什么时候把谁的到期日改成了哪天）。
	ActionCompanyLicense = "company.license"
	// ActionCompanyStorageQuota 改某企业的存储配额（MB）。
	// 与授权同理：调大是让客户继续上传，调小可能立刻让上传失败，属计费/限流动作。
	ActionCompanyStorageQuota = "company.storage_quota"
	// ActionMPEntry 改某企业「小程序默认进入」（名片墙 / 指定员工名片）。
	// 这一项决定**所有访客**扫进来第一眼看到谁：从名片墙改成指定员工，
	// 等于把公司的门面从「全员」换成「一个人」，客户发现时通常已经很晚了。
	ActionMPEntry = "mp_entry.update"

	ActionEmployeeCreate = "employee.create"
	ActionEmployeeUpdate = "employee.update"
	ActionEmployeeReset  = "employee.reset_password"
	ActionEmployeeStatus = "employee.status"
	// ActionEmployeeCancel 员工本人注销账号（PIPL 删除权）：删除员工记录及关联 PII。
	// 这是不可逆的特权动作，必须留痕——否则「谁注销了谁、什么时候」无从查起。
	ActionEmployeeCancel = "employee.cancel"
	// ActionEmployeeExport 后台导出某员工个人信息（PIPL 查阅复制权协助义务）。
	// 虽是只读查询，但「把一个人的全部 PII 打包导出」本身就是高风险动作，
	// 必须留痕（与审计包「只读不记」的约定在此处特意破例，见文件头说明）。
	ActionEmployeeExport = "employee.export"
	// ActionEmployeeExportBatch 后台批量导出本租户全部员工 PII（CSV）。
	// 只读但「一次性带走全租户 PII」风险更高，沿用 employee.export 的破例留痕约定。
	ActionEmployeeExportBatch = "employee.export_batch"

	ActionApplyApprove = "apply.approve"
	ActionApplyReject  = "apply.reject"

	// 小程序版本发布（代租户提审 / 发布）。这条链上每个动作都对应微信侧的
	// 真实后果（提审消耗审核额度、发布改变线上行为），所以逐个登记，
	// 且轮询导致的被动状态同步只在**状态真的变了**时记一条。
	ActionMPReleaseCreate    = "mp_release.create"
	ActionMPReleaseSubmit    = "mp_release.submit_audit"
	ActionMPReleaseStatus    = "mp_release.audit_status"
	ActionMPReleaseRelease   = "mp_release.release"
	ActionMPReleaseUndoAudit = "mp_release.undo_audit"
	// ActionMPReleaseUpload 后台代传代码。与上面几个动作不同，它**不改变微信侧
	// 业务状态**（只是生成体验版），但它是整条链上唯一「把代码送上服务器」的
	// 动作，且会消耗一次编译+上传——必须留痕，否则「这版代码是谁传的」无从查起。
	ActionMPReleaseUpload = "mp_release.upload"

	// ActionMediaTakedown 图片内容安全「下架」：把已确认违规的对象覆盖成占位图。
	// 这条必须留痕——覆盖是不可逆的（原图当场丢失），事后要能回答
	// 「谁在什么时候下的架、依据是哪条检测结论」。
	ActionMediaTakedown = "media_check.takedown"

	// ActionAuditPurge 按保留期清理审计日志（后台定时任务，见 purge.go）。
	// 删审计日志是最该被追责的动作之一：能删日志的人同时也能抹掉自己的痕迹。
	// 留痕后审计页能回答「清掉了多少条、按多少天的保留期清的」。
	ActionAuditPurge = "audit.purge"
)

// ResultSuccess / ResultFail 操作结果。
const (
	ResultSuccess = "success"
	ResultFail    = "fail"
)

// ActorType 操作者类型。
const (
	ActorAdmin     = "admin"
	ActorEmployee  = "employee"
	ActorAnonymous = "anonymous"
	// ActorSystem 系统自身（无 gin 上下文的后台任务，如审计日志按保留期清理）。
	// 不能归到 anonymous：anonymous 的语义是「未登录的人」，而清理是系统在无人
	// 值守时做的动作——混在一起会让「有人未登录就删了日志」变成假警报。
	ActorSystem = "system"
)

// Sensitive 敏感值在审计详情里的占位符。
// Detail 里凡是需要表达「这个值改了但不记录内容」的地方一律用它。
const Sensitive = "******"

// Entry 一条审计记录（调用方按需填字段，未填的由 Record 从 gin 上下文补齐）。
type Entry struct {
	Action     string         // 见动作常量
	TargetType string         // setting / admin / company / employee ...
	TargetID   string         // 目标标识（字符串，兼容非数字主键）
	Detail     map[string]any // 变更摘要（敏感值用 Sensitive 占位）
	Result     string         // ResultSuccess / ResultFail；为空视同 success
	ActorName  string         // 可选：接口已知用户名时传入（登录失败时还没有身份）
	ActorType  string         // 可选：系统动作填 ActorSystem；留空则按 gin 上下文判定
}

// Record 写入一条审计日志：操作者身份优先从 gin 上下文取（中间件已注入），
// 取不到时降级为 anonymous（如登录接口本身失败）。
// 写入失败只打印 [WARN] 并继续——审计是旁路，不能因为它失败就让业务请求失败。
func Record(db *gorm.DB, c *gin.Context, e Entry) {
	if db == nil {
		return
	}
	actorType, actorID, companyID := ActorAnonymous, int64(0), int64(0)
	if c != nil {
		actorID = c.GetInt64("staffId")
		companyID = c.GetInt64("companyId")
		switch {
		case actorID == 0:
			actorType = ActorAnonymous
		case c.GetString("role") == "employee":
			actorType = ActorEmployee
		default:
			actorType = ActorAdmin
		}
	}
	// 显式指定的操作者类型优先（系统动作没有 gin 上下文，只能由调用方指定）
	if e.ActorType != "" {
		actorType = e.ActorType
	}
	result := e.Result
	if result == "" {
		result = ResultSuccess
	}
	// 操作人姓名：调用方没给（多数接口只知道自己的 id）时按 id 反查一次。
	// 不能省——日志里只有 actor_id 的话，管理员还得自己去 admin 表对号，等于没记。
	actorName := e.ActorName
	if actorName == "" && actorID > 0 {
		actorName = lookupName(db, actorType, actorID)
	}
	// 系统动作没有 id 可反查，给个固定展示名——否则审计列表里这一行操作人是空的。
	if actorName == "" && actorType == ActorSystem {
		actorName = "系统"
	}

	detail := ""
	if len(e.Detail) > 0 {
		if b, err := json.Marshal(e.Detail); err == nil {
			detail = string(b)
		}
	}

	ip := ""
	if c != nil {
		ip = c.ClientIP()
	}

	row := model.AuditLog{
		ActorType:  actorType,
		ActorID:    actorID,
		ActorName:  actorName,
		CompanyID:  companyID,
		Action:     e.Action,
		TargetType: e.TargetType,
		TargetID:   e.TargetID,
		Detail:     detail,
		IP:         ip,
		Result:     result,
		CreatedAt:  time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		log.Printf("[WARN] audit record failed (action=%s): %v", e.Action, err)
	}
}

// lookupName 按操作人类型与 id 反查姓名（后台操作低频，多一次查询可接受）。
// 查不到时返回空串而不是报错：审计不能因为名字查不到就整条不记。
func lookupName(db *gorm.DB, actorType string, actorID int64) string {
	switch actorType {
	case ActorAdmin:
		var a model.Admin
		if err := db.Select("username").First(&a, "id = ?", actorID).Error; err == nil {
			return a.Username
		}
	case ActorEmployee:
		var e model.Employee
		if err := db.Select("name").First(&e, "staff_id = ?", actorID).Error; err == nil {
			return e.Name
		}
	}
	return ""
}

// DetailKeys 把「变更了哪些设置项」转成审计详情；敏感项只记「已修改」，不落明文。
// isSecret 由调用方传入（setting.IsSecret），避免本包反向依赖 setting 的内部实现。
func DetailKeys(values map[string]string, isSecret func(string) bool) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		if isSecret != nil && isSecret(k) {
			// 敏感项：只表达「改动了」，值的有无用 changed/cleared 区分
			if v == "" {
				out[k] = map[string]any{"changed": true, "cleared": true}
			} else {
				out[k] = Sensitive
			}
			continue
		}
		out[k] = v
	}
	return out
}
