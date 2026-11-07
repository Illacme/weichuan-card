package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 微信绑定「可见性」的接口级守门（P0）。
//
// 背景：admins.wx_openid 一直是 json:"-"，后台连「绑没绑」都看不到；员工侧反倒
// 把 openid 全文摊在列表里。P0 把两边收敛成「布尔状态 + 掩码」，这里钉住的是
// **四类出网口子都真的收敛了**，而不是只测了工具函数：
//   - 平台超管列表 / 企业管理员列表 / 当前管理员身份 / 员工列表（含 staffIds 精确取）；
//   - 整包 JSON 里绝不出现 openid 原文；
//   - 展示脱敏不能把「按 openid 搜索」的能力一起脱掉（后端的 LIKE 打在明文列上）；
//   - PIPL 单员工导出仍回本人完整 openid（口径里写明的例外）。
//
// 独立建库并返回 db：既要能直接写 admins.wx_openid（没有代绑接口，也不该有），
// 又要断言「模型上 json:\"-\" 没被拆掉」。与 adminMobileEngine 同构，但不注入
// 小程序凭据 / code2Session 桩——本用例不碰微信外呼。
func bindingEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	t.Setenv("UPLOAD_DIR", t.TempDir())

	tmpDB, err := os.CreateTemp("", "weichuan-binding-*.db")
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
	}
	up := &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024, AllowExt: storage.DefaultAllowExt,
	}
	r := buildEngine(t, db, cfg, up, nil)
	// 申请开通里带 openid，别让内容安全真的去打微信接口（测试不该依赖外网）
	require.NoError(t, setting.Upsert("content_sec_enabled", "false"))
	return r, db
}

func bindingRows(t *testing.T, c *testClient, path, token string) []map[string]interface{} {
	t.Helper()
	_, body := c.do("GET", path, nil, token)
	require.EqualValues(t, 0, body["code"], "%s -> %v", path, body["msg"])
	arr, ok := body["data"].([]interface{})
	require.True(t, ok, "%s 的 data 不是数组：%v", path, body["data"])
	out := make([]map[string]interface{}, 0, len(arr))
	for _, it := range arr {
		out = append(out, it.(map[string]interface{}))
	}
	return out
}

func bindingJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestMaskOpenIDForDisplay(t *testing.T) {
	require.Equal(t, "", maskOpenIDForDisplay(""), "空串必须还是空：界面上显示 - 会被读成「绑了个叫 - 的微信」")
	require.Equal(t, "****", maskOpenIDForDisplay("abc"), "短串一律只回星号，避免掩码反而露完原文")
	require.Equal(t, "****", maskOpenIDForDisplay("abcdef"))
	require.Equal(t, "o****cdef", maskOpenIDForDisplay("o1234abcdef"))
}

func TestWechatBindingVisibility(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	const platOpenID = "oFULL-plat-wx-9876543210"
	const entOpenID = "oFULL-ent-wx-1234567890"
	const empOpenID = "oFULL-emp-openid-abcdef"

	// 平台超管：一个已绑、一个没绑（覆盖布尔两支）
	require.NoError(t, db.Model(&model.Admin{}).Where("company_id = ?", 0).
		Update("wx_openid", platOpenID).Error)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 0, Username: "platnobind", PasswordHash: "unused",
	}).Error)
	// 企业管理员（1 号企业由 bindingEngine 建好）：同样一绑一未绑
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: "entboss", PasswordHash: "unused", WXOpenID: entOpenID,
	}).Error)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: "entnobind", PasswordHash: "unused",
	}).Error)
	// 员工：走自助申请 → 审批，openid 随建号落库（一键登录「开箱可用」的那批人）
	staffID := applyAndApprove(t, c, adminToken, "可见甲", "13500000041", empOpenID, 0)

	// ===== 1. 平台超管列表 =====
	plat := bindingRows(t, c, "/api/admin/platform-admins", adminToken)
	platJSON := bindingJSON(t, plat)
	require.NotContains(t, platJSON, platOpenID, "管理员列表绝不能回 openid 原文")
	require.NotContains(t, platJSON, entOpenID)
	var platBound, platUnbound bool
	for _, row := range plat {
		require.Contains(t, row, "wechatBound")
		require.Contains(t, row, "wxOpenidMasked")
		switch row["username"] {
		case "admin":
			platBound = true
			require.Equal(t, true, row["wechatBound"])
			require.Equal(t, maskOpenIDForDisplay(platOpenID), row["wxOpenidMasked"])
		case "platnobind":
			platUnbound = true
			require.Equal(t, false, row["wechatBound"])
			require.Equal(t, "", row["wxOpenidMasked"])
		}
	}
	require.True(t, platBound, "应看到已绑定的平台超管")
	require.True(t, platUnbound, "应看到未绑定的平台超管")

	// ===== 2. 企业管理员列表 =====
	ent := bindingRows(t, c, "/api/admin/companies/1/admins", adminToken)
	entJSON := bindingJSON(t, ent)
	require.NotContains(t, entJSON, entOpenID)
	var entBound, entUnbound bool
	for _, row := range ent {
		require.Contains(t, row, "wechatBound")
		switch row["username"] {
		case "entboss":
			entBound = true
			require.Equal(t, true, row["wechatBound"])
			require.Equal(t, maskOpenIDForDisplay(entOpenID), row["wxOpenidMasked"])
		case "entnobind":
			entUnbound = true
			require.Equal(t, false, row["wechatBound"])
			require.Equal(t, "", row["wxOpenidMasked"])
		}
	}
	require.True(t, entBound, "企业管理员弹窗此前完全看不到这一维")
	require.True(t, entUnbound)

	// ===== 3. 当前登录身份 /api/admin/me =====
	_, me := c.do("GET", "/api/admin/me", nil, adminToken)
	require.EqualValues(t, 0, me["code"])
	meData := me["data"].(map[string]interface{})
	require.Equal(t, true, meData["wechatBound"])
	require.Equal(t, maskOpenIDForDisplay(platOpenID), meData["wxOpenidMasked"])
	require.NotContains(t, bindingJSON(t, me), platOpenID)

	// ===== 4. 员工列表：掩码 + 状态，且既有字段一个没丢 =====
	emps := bindingRows(t, c, "/api/admin/employees?keyword=13500000041", adminToken)
	require.Len(t, emps, 1)
	empJSON := bindingJSON(t, emps)
	require.NotContains(t, empJSON, empOpenID, "员工列表不再铺开 openid 原文")
	require.Equal(t, true, emps[0]["wechatBound"])
	require.Equal(t, maskOpenIDForDisplay(empOpenID), emps[0]["openid"])
	require.Equal(t, "可见甲", emps[0]["name"], "嵌入 *model.Employee 后 PII 解密不能丢")
	require.Equal(t, "13500000041", emps[0]["phone"])

	// staffIds 精确取走的是同一套展示视图，不能漏掉（审核页回显员工原值走这条）
	byIDs := bindingRows(t, c, fmt.Sprintf("/api/admin/employees?staffIds=%d", staffID), adminToken)
	require.Len(t, byIDs, 1)
	require.Equal(t, maskOpenIDForDisplay(empOpenID), byIDs[0]["openid"])
	require.NotContains(t, bindingJSON(t, byIDs), empOpenID)

	// ===== 5. 展示脱敏 ≠ 检索脱敏：按完整 openid 仍能搜到 =====
	hit := bindingRows(t, c, "/api/admin/employees?keyword="+empOpenID, adminToken)
	require.Len(t, hit, 1, "后端 LIKE 打在库里的明文列上，展示脱敏不该把搜索能力一起脱掉")
	require.EqualValues(t, staffID, int64(hit[0]["staffId"].(float64)))

	// ===== 6. PIPL 单员工导出仍回本人完整 openid（口径里写明的例外）=====
	_, exported := c.do("GET", fmt.Sprintf("/api/admin/employees/%d/export", staffID), nil, adminToken)
	require.EqualValues(t, 0, exported["code"])
	require.Contains(t, bindingJSON(t, exported), empOpenID,
		"查阅复制权协助接口导出的是本人全部信息，这个例外不能被顺手「脱敏」掉")
}
