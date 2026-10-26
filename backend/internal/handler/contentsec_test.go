package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 内容安全的**接线**测试：策略本身在 contentsec 包里测，这里只验证
// 「申请提交这条链路上真的会去检、命中真的会拦、拦下真的不落库」。
//
// 少了这一层，策略再对也没用——接线漏了的表现是「一切正常，只是从不检测」。

// fakeWXSec 起一个假微信接口：token 照发，msg_sec_check 按 suggest 回。
func fakeWXSec(t *testing.T, suggest string, label int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cgi-bin/token":
			_, _ = w.Write([]byte(`{"access_token":"tk","expires_in":7200}`))
		case "/wxa/msg_sec_check":
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","trace_id":"trace-test","result":{"suggest":"` +
				suggest + `","label":` + strconv.Itoa(label) + `}}`))
		default:
			_, _ = w.Write([]byte(`{"errcode":0}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(wechat.SetAPIBase(srv.URL))
}

// setupEngineWithDB 与 setupSQLiteEngine 等价，但把 *gorm.DB 也交出来——
// 内容安全的断言要直接数表（「被拦下的申请有没有落库」接口层看不出来）。
func setupEngineWithDB(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "weichuan-cs-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "朗汇", ShortName: "朗汇", Status: "enabled"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-cs-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
	}
	up := &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
	var refresher storage.CDNRefresher
	return buildEngine(t, db, cfg, up, refresher), db
}

// configureWX 通过后台设置接口写入小程序凭据。
// 走真实配置链路而不是直接改内存——顺带验证「界面上配了凭据，内容安全就生效」。
func configureWX(t *testing.T, c *testClient, adminToken string) {
	t.Helper()
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"wx_appid":     "wx-content-sec-appid",
		"wx_appsecret": "wx-content-sec-secret",
	}, adminToken)
	require.EqualValues(t, 0, body["code"], "写入小程序凭据失败：%v", body)
}

// 命中违规：开通申请被拒，且**不落库**。
func TestApplyOpenBlockedByContentSec(t *testing.T) {
	fakeWXSec(t, "risky", 20001)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	configureWX(t, c, loginAdmin(t, c))

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "张三", "phone": "13800001111", "openid": "openid-1",
	}, "")
	require.EqualValues(t, 1, body["code"], "违规内容必须被拦")
	require.Contains(t, body["msg"], "违规")

	var n int64
	require.NoError(t, db.Model(&model.OpenApply{}).Count(&n).Error)
	require.EqualValues(t, 0, n, "被拦下的申请不能落库")
}

// 正常内容：照常通过。
func TestApplyOpenAllowedByContentSec(t *testing.T) {
	fakeWXSec(t, "pass", 100)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	configureWX(t, c, loginAdmin(t, c))

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "李四", "phone": "13800002222", "openid": "openid-2",
	}, "")
	require.EqualValues(t, 0, body["code"])

	var n int64
	require.NoError(t, db.Model(&model.OpenApply{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

// 没配凭据时跳过检测、不影响提交：这是 fail-open 的默认形态，
// 也是「升级到这一版但还没配微信凭据的部署」的实际行为——不能因为加了检测就挂。
func TestApplyOpenSkippedWithoutCredential(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	_, body := c.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "王五", "phone": "13800003333", "openid": "openid-3",
	}, "")
	require.EqualValues(t, 0, body["code"])

	var n int64
	require.NoError(t, db.Model(&model.OpenApply{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

// 员工名片修改走同一条策略：命中违规时提交被拒。
// 这条比开通申请更要紧——名片是对外展示的，违规内容上线即被举报。
func TestEmployeeCardModifyBlockedByContentSec(t *testing.T) {
	fakeWXSec(t, "risky", 20001)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	configureWX(t, c, loginAdmin(t, c))
	empToken := loginEmployee(t, c)

	// 员工记录里没有 openid 时检测会被跳过（匿名场景），这里补一个，
	// 模拟「员工已在小程序登录、openid 已采集」的真实状态。
	require.NoError(t, db.Model(&model.Employee{}).
		Where("phone_bidx = ?", pii.BlindIndex("13900000999")).Update("openid", "openid-emp").Error)

	_, body := c.do("PUT", "/api/employee/me", map[string]interface{}{
		"name": "张三", "title": "销售总监",
	}, empToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "违规")

	var n int64
	require.NoError(t, db.Model(&model.CardModifyApply{}).Count(&n).Error)
	require.EqualValues(t, 0, n, "被拦下的修改申请不能落库")
}
