package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
)

// 审计是「出了事能不能查到人」的最后一道凭证，它的正确性要求与业务代码同级：
// 记错操作人、漏记租户、把明文写进 Detail，都比不记更糟。

func newAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open(filepath.Join(t.TempDir(), "audit.db"))
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}, &model.Admin{}, &model.Employee{}))
	return db
}

// ctxWithActor 造一个带登录态的 gin 上下文（模拟中间件已注入的身份信息）。
func ctxWithActor(role string, actorID, companyID int64, ip string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/whatever", nil)
	c.Request.RemoteAddr = ip + ":12345"
	c.Set("staffId", actorID)
	c.Set("role", role)
	c.Set("companyId", companyID)
	return c
}

func lastLog(t *testing.T, db *gorm.DB) model.AuditLog {
	t.Helper()
	var row model.AuditLog
	require.NoError(t, db.Order("id DESC").First(&row).Error)
	return row
}

// 操作人身份必须落到行里，且 admin / employee 分别按自己的表反查姓名
// ——只存 id 的话，管理员还得自己回 admin 表对号，等于没记。
func TestRecordResolvesActorName(t *testing.T) {
	db := newAuditDB(t)
	require.NoError(t, db.Create(&model.Admin{Username: "alice"}).Error)
	require.NoError(t, db.Create(&model.Employee{StaffID: 1001, Name: "张伟"}).Error)

	var adminID, empStaff int64
	require.NoError(t, db.Model(&model.Admin{}).Where("username = ?", "alice").Pluck("id", &adminID).Error)

	Record(db, ctxWithActor("admin", adminID, 0, "10.0.0.1"), Entry{
		Action: ActionAdminReset, TargetType: "admin", TargetID: "7", Result: ResultSuccess,
	})
	row := lastLog(t, db)
	require.Equal(t, ActorAdmin, row.ActorType)
	require.Equal(t, "alice", row.ActorName)
	require.EqualValues(t, adminID, row.ActorID)
	require.EqualValues(t, 0, row.CompanyID)
	require.Equal(t, ActionAdminReset, row.Action)
	require.Equal(t, "7", row.TargetID)
	require.Equal(t, ResultSuccess, row.Result)
	require.Equal(t, "10.0.0.1", row.IP)

	empStaff = 1001
	Record(db, ctxWithActor("employee", empStaff, 5, "10.0.0.2"), Entry{Action: ActionEmployeeUpdate})
	row = lastLog(t, db)
	require.Equal(t, ActorEmployee, row.ActorType)
	require.Equal(t, "张伟", row.ActorName)
	require.EqualValues(t, 5, row.CompanyID, "租户必须记下来，否则查不到「谁动了本企业的数据」")
}

// 登录失败时还没有身份：必须记为 anonymous 而不是 0 号管理员。
func TestRecordAnonymousWhenNoActor(t *testing.T) {
	db := newAuditDB(t)

	Record(db, nil, Entry{Action: ActionLoginFail, Result: ResultFail})
	row := lastLog(t, db)
	require.Equal(t, ActorAnonymous, row.ActorType)
	require.EqualValues(t, 0, row.ActorID)
	require.Equal(t, ResultFail, row.Result)
	require.Empty(t, row.IP)

	// 上下文存在但 staffId=0（未登录）同样按匿名
	Record(db, ctxWithActor("admin", 0, 0, "10.0.0.9"), Entry{Action: ActionLoginFail})
	row = lastLog(t, db)
	require.Equal(t, ActorAnonymous, row.ActorType)
}

// Result 为空视同 success：调用方普遍只在失败时显式写 Result，
// 缺省必须是「成功」而不是留空（否则查询「失败操作」时会漏）。
func TestRecordDefaultsResultToSuccess(t *testing.T) {
	db := newAuditDB(t)
	Record(db, nil, Entry{Action: ActionSettingUpdate})
	require.Equal(t, ResultSuccess, lastLog(t, db).Result)
}

// Detail 以 JSON 落库且能原样取回
func TestRecordSerializesDetail(t *testing.T) {
	db := newAuditDB(t)
	Record(db, nil, Entry{
		Action: ActionCompanyUpdate, TargetType: "company", TargetID: "3",
		Detail: map[string]any{"name": "朗汇机电", "shortName": "朗汇"},
	})
	row := lastLog(t, db)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.Detail), &got))
	require.Equal(t, "朗汇机电", got["name"])
	require.Equal(t, "朗汇", got["shortName"])
}

// 调用方已给 ActorName 时不再反查（登录失败时接口只知道用户名、没有 id）
func TestRecordKeepsProvidedActorName(t *testing.T) {
	db := newAuditDB(t)
	Record(db, nil, Entry{Action: ActionLoginFail, ActorName: "bob", Result: ResultFail})
	require.Equal(t, "bob", lastLog(t, db).ActorName)
}

// 系统动作（无 gin 上下文的后台任务）：必须标成 system 且有个可读的操作人名，
// 否则审计列表里这一行会显示成「未登录的人删了审计日志」——假警报比没有警报更糟。
func TestRecordSystemActor(t *testing.T) {
	db := newAuditDB(t)
	Record(db, nil, Entry{
		Action: ActionAuditPurge, ActorType: ActorSystem, TargetType: "audit_log",
		Detail: map[string]any{"deleted": 12},
	})
	row := lastLog(t, db)
	require.Equal(t, ActorSystem, row.ActorType)
	require.Equal(t, "系统", row.ActorName)
	require.EqualValues(t, 0, row.ActorID)
}

// 写入失败只告警、不 panic：审计是旁路，不能因为记不下来就让业务请求失败。
func TestRecordDoesNotPanicOnWriteFailure(t *testing.T) {
	db := newAuditDB(t)
	// 删掉审计表，让 Create 必然报错
	require.NoError(t, db.Exec("DROP TABLE audit_log").Error)

	require.NotPanics(t, func() {
		Record(db, ctxWithActor("admin", 1, 0, "10.0.0.1"), Entry{Action: ActionAdminDelete})
	}, "审计写不进去也不能把业务请求带崩")
}

// db 为 nil 时静默返回（部分调用点可能拿不到库连接）
func TestRecordNilDBIsNoop(t *testing.T) {
	require.NotPanics(t, func() {
		Record(nil, ctxWithActor("admin", 1, 0, "10.0.0.1"), Entry{Action: ActionAdminDelete})
	})
}

// ---- DetailKeys：敏感值脱敏 ----

func TestDetailKeysMasksSecrets(t *testing.T) {
	out := DetailKeys(map[string]string{
		"cdn_access_key_secret": "real-secret",
		"wx_appsecret":          "real-secret",
		"cleared_secret":        "",
		"upload_max_mb":         "10",
	}, func(k string) bool {
		return k == "cdn_access_key_secret" || k == "wx_appsecret" || k == "cleared_secret"
	})

	require.Equal(t, Sensitive, out["cdn_access_key_secret"], "敏感值绝不能落明文")
	require.Equal(t, Sensitive, out["wx_appsecret"])
	require.Equal(t, "10", out["upload_max_mb"], "非敏感项照常记录（审计才有意义）")

	// 清空敏感值时用 changed/cleared 表达，而不是也来一串星号
	cl, ok := out["cleared_secret"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, cl["changed"])
	require.Equal(t, true, cl["cleared"])
}

// isSecret 为 nil 时全部按非敏感处理（调用方没给判据不代表全是秘密）
func TestDetailKeysWithoutSecretPredicate(t *testing.T) {
	out := DetailKeys(map[string]string{"a": "1"}, nil)
	require.Equal(t, map[string]any{"a": "1"}, out)
}

func TestDetailKeysEmpty(t *testing.T) {
	require.Empty(t, DetailKeys(map[string]string{}, func(string) bool { return true }))
}
