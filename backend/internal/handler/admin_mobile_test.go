package handler

import (
	"os"
	"strconv"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// adminMobileEngine 建一套独立引擎并返回 db，便于直接断言 admins.wx_openid 与审计行。
// 与 setupSQLiteEngine 同构，但额外配上小程序凭据、调高登录限流，并把 code2Session
// 换成桩（真接口要外网 + 真凭据，测试不该依赖它）。
func adminMobileEngine(t *testing.T, openid string) (*gin.Engine, *gorm.DB) {
	t.Helper()
	t.Setenv("UPLOAD_DIR", t.TempDir())

	tmpDB, err := os.CreateTemp("", "weichuan-adminmobile-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{
		ID: 1, Name: "测试企业", ShortName: "测试", Status: "enabled", LoginCode: "test",
	}).Error)

	uploadDir := t.TempDir()
	cfg := config.Config{
		UploadDir: uploadDir, UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true,
		JWTKey: "test-jwt-key", AdminUser: "admin", AdminPass: "admin123456",
		WXAppID: "wx-test-appid", WXAppSecret: "wx-test-secret",
	}
	up := &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024, AllowExt: storage.DefaultAllowExt,
	}
	r := buildEngine(t, db, cfg, up, nil)
	require.NoError(t, setting.Upsert("login_rate_limit_per_min", "200"))
	require.NoError(t, setting.Upsert("wx_appid", cfg.WXAppID))
	require.NoError(t, setting.Upsert("wx_appsecret", cfg.WXAppSecret))

	orig := code2Session
	t.Cleanup(func() { code2Session = orig })
	code2Session = func(_ wechat.Config, _ string) (string, string, error) {
		return openid, "sk-test", nil
	}
	return r, db
}

func mobileAdminLogin(t *testing.T, c *testClient, username, password string) map[string]interface{} {
	t.Helper()
	_, body := c.do("POST", "/api/admin/login/mobile", map[string]interface{}{
		"username": username, "password": password,
	}, "")
	return body
}

func TestAdminMobileLoginAndBindFlow(t *testing.T) {
	r, db := adminMobileEngine(t, "openid-mobile-1")
	c := newTestClient(r, t)

	t.Run("账号不存在与密码错误同文案，且不泄露令牌", func(t *testing.T) {
		wrong := mobileAdminLogin(t, c, "admin", "wrong")
		missing := mobileAdminLogin(t, c, "no-such-admin", "wrong")
		require.EqualValues(t, 1, wrong["code"])
		require.EqualValues(t, 1, missing["code"])
		require.Equal(t, "用户名或密码错误", wrong["msg"])
		require.Equal(t, wrong["msg"], missing["msg"], "快=不存在 会变成账号枚举接口")
		require.Empty(t, wrong["token"])
		require.Empty(t, missing["token"])
	})

	var tok string
	t.Run("密码登录把 admin 令牌放进响应体，身份字段齐全", func(t *testing.T) {
		body := mobileAdminLogin(t, c, "admin", "admin123456")
		require.EqualValues(t, 0, body["code"])
		tok, _ = body["token"].(string)
		require.NotEmpty(t, tok, "小程序没有 Cookie 容器，token 必须在 body 里")
		require.NotEmpty(t, body["refreshToken"])
		require.Equal(t, "admin", body["username"])
		require.Equal(t, false, body["wechatBound"])
		require.Empty(t, body["passwordHash"], "绝不能把哈希带进响应体")
		require.Empty(t, body["wxOpenid"])
	})

	t.Run("Bearer 令牌可直接调用现有审核端点（审核接口零改动）", func(t *testing.T) {
		_, body := c.do("GET", "/api/admin/apply/open", nil, tok)
		require.EqualValues(t, 0, body["code"])
		require.NotNil(t, body["data"])
		_, body = c.do("GET", "/api/admin/apply/modify", nil, tok)
		require.EqualValues(t, 0, body["code"])
	})

	t.Run("未绑定时免密登录给 code:3 并说清怎么办", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
			"code": "any", "companyId": 0,
		}, "")
		require.EqualValues(t, 3, body["code"])
		require.Contains(t, body["msg"], "账号密码登录")
		require.Empty(t, body["token"])
	})

	t.Run("绑定必须已登录，且 openid 真的写进 admins", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/wechat/bind", map[string]interface{}{"code": "any"}, "")
		require.EqualValues(t, 401, body["code"])

		_, body = c.do("POST", "/api/admin/wechat/bind", map[string]interface{}{"code": "any"}, tok)
		require.EqualValues(t, 0, body["code"])

		var a model.Admin
		require.NoError(t, db.First(&a, "username = ?", "admin").Error)
		require.Equal(t, "openid-mobile-1", a.WXOpenID)
	})

	t.Run("绑定后免密登录签发同样的管理员令牌", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
			"code": "any", "companyId": 0,
		}, "")
		require.EqualValues(t, 0, body["code"])
		tok2, _ := body["token"].(string)
		require.NotEmpty(t, tok2)
		require.Equal(t, true, body["wechatBound"])
		require.NotEmpty(t, body["refreshToken"])

		_, body = c.do("GET", "/api/admin/apply/open", nil, tok2)
		require.EqualValues(t, 0, body["code"])
	})

	t.Run("登录与绑定都留痕", func(t *testing.T) {
		var n int64
		require.NoError(t, db.Model(&model.AuditLog{}).
			Where("action = ?", audit.ActionAdminMobileLogin).Count(&n).Error)
		require.GreaterOrEqual(t, n, int64(2), "密码登录 + 微信免密登录各一条")
		require.NoError(t, db.Model(&model.AuditLog{}).
			Where("action = ?", audit.ActionAdminWXBind).Count(&n).Error)
		require.EqualValues(t, 1, n)
	})
}

func TestAdminWXBindUniquenessAndAmbiguity(t *testing.T) {
	r, db := adminMobileEngine(t, "openid-shared")
	c := newTestClient(r, t)

	hash, err := auth.HashPassword("pass123456")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Company{
		ID: 7, Name: "七号企业", ShortName: "七号", Status: "enabled", LoginCode: "c7",
	}).Error)
	boss := model.Admin{CompanyID: 7, Username: "boss7", PasswordHash: hash, WXOpenID: "openid-shared"}
	require.NoError(t, db.Create(&boss).Error)
	other := model.Admin{CompanyID: 7, Username: "other7", PasswordHash: hash}
	require.NoError(t, db.Create(&other).Error)

	// 另一名同企业管理员用同一个微信号绑定 → 必须拦下，否则两人会互相登进对方账号
	otherTok := mobileAdminLogin(t, c, "other7", "pass123456")
	require.EqualValues(t, 0, otherTok["code"])
	_, body := c.do("POST", "/api/admin/wechat/bind", map[string]interface{}{"code": "any"}, otherTok["token"].(string))
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "其他管理员账号")

	// 同一个 openid 绑到两个不同企业的管理员：带 companyId 收窄后应精准落到该企业的账号
	boss8 := model.Admin{CompanyID: 8, Username: "boss8", PasswordHash: hash, WXOpenID: "openid-shared"}
	require.NoError(t, db.Create(&boss8).Error)

	_, body = c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
		"code": "any", "companyId": 7,
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "boss7", body["username"])

	// companyId=0（平台模式没带租户）且有多条 → 绝不随便挑一个签发令牌
	_, body = c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
		"code": "any", "companyId": 0,
	}, "")
	require.EqualValues(t, 3, body["code"])
	require.Empty(t, body["token"])
}

func TestAdminMobileLoginRejectsDisabledCompany(t *testing.T) {
	r, db := adminMobileEngine(t, "openid-disabled")
	c := newTestClient(r, t)

	hash, err := auth.HashPassword("pass123456")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 7, Username: "boss7", PasswordHash: hash, WXOpenID: "openid-disabled",
	}).Error) // 7 号企业不存在 → companyEnabled=false

	body := mobileAdminLogin(t, c, "boss7", "pass123456")
	require.EqualValues(t, 2, body["code"])
	require.Empty(t, body["token"])

	_, body = c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
		"code": "any", "companyId": 7,
	}, "")
	require.EqualValues(t, 2, body["code"])
	require.Empty(t, body["token"])
}

// TestApplyApprovalAudit 钉住「四个审批动作必须留痕」：小程序端新增了免密审核通路，
// 谁在什么时候通过/驳回了哪份申请，是这条通路上唯一可追责的证据。
func TestApplyApprovalAudit(t *testing.T) {
	r, db := adminMobileEngine(t, "openid-audit")
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "审核留痕", "phone": "13900000001", "title": "工程师",
	}, "")
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("GET", "/api/admin/apply/open", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	list := body["data"].([]interface{})
	require.NotEmpty(t, list)
	id := int64(list[0].(map[string]interface{})["id"].(float64))

	_, body = c.do("POST", "/api/admin/apply/open/"+
		strconv.FormatInt(id, 10)+"/approve", nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	var approve, reject int64
	require.NoError(t, db.Model(&model.AuditLog{}).
		Where("action = ?", audit.ActionApplyApprove).Count(&approve).Error)
	require.EqualValues(t, 1, approve)
	require.NoError(t, db.Model(&model.AuditLog{}).
		Where("action = ?", audit.ActionApplyReject).Count(&reject).Error)
	require.Zero(t, reject, "只通过了申请，不该凭空多一条驳回")
}

// runAdminMobileFlow 由 runAPIFlow 调用，让 SQLite 与 MySQL parity 都覆盖新端点。
// 刻意放在 runAPIFlow 的末尾：它会往 sys_setting 写 wx_appid，早于其他用例执行会让
// 开通申请的内容安全分支误以为凭据已配好（见 login_wechat_test.go 的注释）。
func runAdminMobileFlow(t *testing.T, c *testClient, adminToken string) {
	t.Helper()
	require.NoError(t, setting.Upsert("login_rate_limit_per_min", "200"))
	require.NoError(t, setting.Upsert("wx_appid", "wx-test-appid"))
	require.NoError(t, setting.Upsert("wx_appsecret", "wx-test-secret"))
	orig := code2Session
	t.Cleanup(func() { code2Session = orig })
	code2Session = func(_ wechat.Config, _ string) (string, string, error) {
		return "openid-apiflow", "sk-test", nil
	}

	body := mobileAdminLogin(t, c, "admin", "admin123456")
	require.EqualValues(t, 0, body["code"])
	tok, _ := body["token"].(string)
	require.NotEmpty(t, tok)

	// 审核接口零改动：Bearer 直接可用
	_, body = c.do("GET", "/api/admin/apply/open", nil, tok)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
		"code": "any", "companyId": 0,
	}, "")
	require.EqualValues(t, 3, body["code"])

	_, body = c.do("POST", "/api/admin/wechat/bind", map[string]interface{}{"code": "any"}, tok)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("POST", "/api/admin/login/wechat", map[string]interface{}{
		"code": "any", "companyId": 0,
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["wechatBound"])
	tok2, _ := body["token"].(string)
	require.NotEmpty(t, tok2)

	_, body = c.do("GET", "/api/admin/audit-logs?action=admin.mobile_login", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.NotEmpty(t, body["data"], "登录审计必须能在后台列表里查到")
}
