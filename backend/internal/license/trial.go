package license

import (
	"time"

	"weichuan/internal/model"
	"weichuan/internal/setting"

	"gorm.io/gorm"
)

// 本文件闭合「平台模式默认零授权约束」这个洞。
//
// 问题：`tenant_license` 表为空时（SaaS 新部署）没有任何约束——
// `ResolveFor` 查不到行就回退全局设置，而全局缺省是「不限席位、不过期」，
// 于是「新租户想用多久用多久、想建多少人建多少人」，到期自动降服、超员收费
// 这些商业约定在机制层根本不存在，只能靠人工盯。
//
// 解法：**开通企业时落一条试用授权行**（席位 + 到期日）。之所以落库而不是
// 每次现算，是因为「到期日从哪天算」必须是一个固定的事实：
//   - 现算（用 company.created_at）会随试用天数配置变化漂移——运营把 14 天改成 30 天，
//     所有在试用中的客户立刻多出 16 天；
//   - 落库后改配置只影响「以后新开的」，已开通的按当初的约定走，这才谈得上履约。
//
// 存量企业**刻意不补行**：它们已经在用（多数已付费），升级瞬间被打成只读是事故。
// 未落行 → 回退全局设置 → 行为与升级前一致；要约束它们，在后台「授权」里显式设一条。

// TrialSpec 试用授权的口径（来自「系统设置 → 授权」）。
type TrialSpec struct {
	Enabled  bool
	Days     int
	Seats    int
	Features []string
}

// TrialSpecFromSettings 读取当前试用口径。
//
// 是**包级变量**而不是普通函数（全仓仅此一处这类写法）：试用口径要在测试里换成
// "关掉试用""改成 3 天"这类组合，而 setting 的全局实例一旦 Init 就会影响同包
// 其它用例（它是进程级单例）。抽成可替换的函数值，测试就不必动全局状态。
// 生产代码一律直接调用它，不要替换。
//
// 每项都给出兜底值：`setting.GetInt` 在未初始化（测试 / 启动早期）时返回零值，
// 若直接使用会得到 "0 天 / 0 席位"——0 席位意味着新租户一个员工都建不了，
// 那是比「零约束」更糟的事故，所以这里必须自己兜。
var TrialSpecFromSettings = func() TrialSpec {
	days := setting.GetInt("license_trial_days", 14)
	if days <= 0 {
		days = 14
	}
	seats := setting.GetInt("license_trial_seats", 5)
	if seats <= 0 {
		seats = 5
	}
	return TrialSpec{
		Enabled: setting.GetBool("license_trial_enabled", true),
		Days:    days,
		Seats:   seats,
	}
}

// New 按口径构造某租户的试用授权。now 由调用方传入（可测）。
func (s TrialSpec) New(companyID int64, now time.Time) *Entitlement {
	return &Entitlement{
		TenantID:  companyID,
		Edition:   "trial",
		Seats:     s.Seats,
		ExpiresAt: now.AddDate(0, 0, s.Days),
		Features:  s.Features,
	}
}

// CreateTrialLicense 给新开通的企业落一条试用授权行。
//
// 返回 nil 的两种情形都属正常：
//  1. 试用被关掉（license_trial_enabled=false）→ 不写行，行为回到「不限制」；
//  2. 该企业已有授权行 → 不覆盖（幂等，允许重放）。
//
// companyID <= 0 直接视为错误：0 是平台自身，不该有租户授权行
// （平台全局那份走 sys_setting 的 license_*）。
func CreateTrialLicense(db *gorm.DB, companyID int64) error {
	if companyID <= 0 {
		return nil
	}
	spec := TrialSpecFromSettings()
	if !spec.Enabled {
		return nil
	}
	var cnt int64
	if err := db.Model(&model.TenantLicense{}).Where("company_id = ?", companyID).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil
	}
	row := model.TenantLicense{
		CompanyID: companyID,
		Edition:   "trial",
		Seats:     spec.Seats,
		ExpiresAt: spec.New(companyID, time.Now()).ExpiresAt,
	}
	return db.Create(&row).Error
}

// SetTenantLicense 写入/覆盖某租户的授权（后台「企业管理 → 授权」）。
//
// 用 map 而不是结构体写库，原因只有一个但很致命：**ExpiresAt 为零值时必须落成 NULL**
// （NULL = 不过期）。结构体零值会被 GORM 序列化成 '0000-00-00 00:00:00'，
// MySQL 严格模式下直接报错——而"设置成不过期"恰恰是给正式客户转正时最常用的操作。
// 同理 seats=0（不限席位）也是合法值，不能被当成"没传"跳过。
//
// expiresAt 传 nil 表示不过期。
func SetTenantLicense(db *gorm.DB, companyID int64, edition string, seats int, expiresAt *time.Time, features string) error {
	if db == nil || companyID <= 0 {
		return nil
	}
	vals := map[string]any{
		"edition":    edition,
		"seats":      seats,
		"features":   features,
		"expires_at": nil,
		"updated_at": time.Now(),
	}
	if expiresAt != nil && !expiresAt.IsZero() {
		vals["expires_at"] = *expiresAt
	}
	var cnt int64
	if err := db.Model(&model.TenantLicense{}).Where("company_id = ?", companyID).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return db.Model(&model.TenantLicense{}).Where("company_id = ?", companyID).Updates(vals).Error
	}
	vals["company_id"] = companyID
	vals["created_at"] = time.Now()
	return db.Model(&model.TenantLicense{}).Create(vals).Error
}

// TenantLicenseOf 读某租户的授权行；没有行时返回 ErrNotFound（由调用方决定怎么展示）。
func TenantLicenseOf(db *gorm.DB, companyID int64) (*model.TenantLicense, error) {
	if db == nil || companyID <= 0 {
		return nil, ErrNotFound
	}
	var row model.TenantLicense
	if err := db.Where("company_id = ?", companyID).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}
