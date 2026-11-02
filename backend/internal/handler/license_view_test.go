package handler

import (
	"fmt"
	"testing"
	"time"

	"weichuan/internal/auth"
	"weichuan/internal/license"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
)

// 授权到期视图（P0-3）：企业管理员看自己那份，平台超管看「哪些客户快到期」。
//
// 这组用例守的是「提醒到底能不能被人看见」——此前授权到期只有一条 code=2 的
// 拦截，客户是在某个上午改不了内容时才知道的，运营是在客户打电话来问时才知道的。

// 建一家企业（平台超管操作），返回企业 id；顺带把企业管理员账号建好。
func createCompanyWithAdmin(t *testing.T, plat *testClient, name, username string) int64 {
	t.Helper()
	_, body := plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": name, "adminUsername": username, "adminPassword": "pwd123456",
	})
	require.EqualValues(t, 0, body["code"], "开通企业应成功：%v", body["msg"])
	id, ok := body["data"].(map[string]interface{})["id"].(float64)
	require.True(t, ok, "开通企业应回传 id")
	return int64(id)
}

// 企业管理员：看到自己的到期日、还剩几天、到期后果与续费路径，
// 且**看不到**别家企业的到期情况（跨租户名单是商业信息）。
func TestLicenseViewTenantScope(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	loginAdmin(t, plat)
	createCompanyWithAdmin(t, plat, "朗汇机电", "boss1")

	comp := newTestClient(r, t)
	_, body := comp.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "boss1", "password": "pwd123456",
	}, "")
	require.EqualValues(t, 0, body["code"])

	_, body = comp.doWithCookie("GET", "/api/admin/license", nil)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})

	require.Equal(t, "tenant", data["scope"])
	require.Equal(t, "platform", data["mode"])
	require.Nil(t, data["alerts"], "企业管理员不该拿到跨租户汇总")
	require.Nil(t, data["expiring"], "企业管理员不该拿到别家企业的到期名单")

	self := data["self"].(map[string]interface{})
	// 新开通企业自动落 14 天试用（license_trial_days 缺省 14）→ 落在 warn 档
	require.Equal(t, "trial", self["edition"])
	require.EqualValues(t, 5, self["seats"])
	require.Equal(t, "warn", self["level"], "14 天应在提醒窗口内：%v", self)
	require.EqualValues(t, 14, self["daysLeft"])
	require.NotEmpty(t, self["expiresAt"])
	require.Equal(t, false, self["expired"])

	// 文案三件套：还剩多久、到期会怎样、怎么恢复
	require.Contains(t, self["title"], "到期")
	require.Contains(t, self["consequence"], "只读")
	// 平台模式续费后即时生效，让客户去重启生产服务是错的（私有部署才需要）
	require.Contains(t, self["renewHint"], "无需重启")
	require.NotContains(t, self["renewHint"], "重启服务")

	// 席位用满与否要能看出来：建到 5 个员工后 seatsFull 必须为 true，
	// 否则管理员只会看到「席位已达上限（5/5）」的报错而不知道 5 是哪里来的。
	for i := 0; i < 5; i++ {
		_, b := comp.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
			"name": fmt.Sprintf("员工%d", i), "phone": fmt.Sprintf("139000000%02d", i),
		})
		require.EqualValues(t, 0, b["code"], "第 %d 个员工应建成功：%v", i+1, b["msg"])
	}
	_, body = comp.doWithCookie("GET", "/api/admin/license", nil)
	self = body["data"].(map[string]interface{})["self"].(map[string]interface{})
	require.EqualValues(t, 5, self["seatsUsed"])
	require.Equal(t, true, self["seatsFull"])
}

// 平台超管：拿到汇总计数与按紧急度排序的名单。
func TestLicenseViewPlatformScope(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	loginAdmin(t, plat)

	// 三家：两家在试用（14 天），一家手工改成 3 天后到期、一家改成已过期
	id1 := createCompanyWithAdmin(t, plat, "企业甲", "a1")
	id2 := createCompanyWithAdmin(t, plat, "企业乙", "b1")
	id3 := createCompanyWithAdmin(t, plat, "企业丙", "c1")

	db := license.TenantDB()
	require.NotNil(t, db, "测试引擎应注入按租户解析用的库连接")
	require.NoError(t, db.Model(&model.TenantLicense{}).Where("company_id = ?", id1).
		Update("expires_at", time.Now().AddDate(0, 0, 3)).Error)
	require.NoError(t, db.Model(&model.TenantLicense{}).Where("company_id = ?", id2).
		Update("expires_at", time.Now().AddDate(0, 0, -2)).Error)

	_, body := plat.doWithCookie("GET", "/api/admin/license", nil)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})

	require.Equal(t, "platform", data["scope"])

	// 平台自己那份没配 → 不打扰（不能因为「平台超管」就凭空造一条到期提示）
	self := data["self"].(map[string]interface{})
	require.Equal(t, "none", self["level"])
	require.Empty(t, self["expiresAt"])
	require.Empty(t, self["consequence"], "不临期就不该摆一句「到期后会只读」")

	alerts := data["alerts"].(map[string]interface{})
	require.EqualValues(t, 1, alerts["expired"])
	require.EqualValues(t, 2, alerts["within7"], "已过期的也算在 7 天内")
	require.EqualValues(t, 3, alerts["within30"])

	list := data["expiring"].([]interface{})
	require.Len(t, list, 3)
	// 排序：已过期永远最前，其后按天数升序
	first := list[0].(map[string]interface{})
	require.EqualValues(t, id2, first["companyId"], "已过期的应排第一")
	require.Equal(t, true, first["expired"])
	require.Equal(t, "企业乙", first["companyName"])
	second := list[1].(map[string]interface{})
	require.EqualValues(t, id1, second["companyId"])
	require.EqualValues(t, 3, second["daysLeft"])
	require.Equal(t, "urgent", second["level"])
	third := list[2].(map[string]interface{})
	require.EqualValues(t, id3, third["companyId"])
	require.EqualValues(t, 14, third["daysLeft"])
}

// 存量企业（没有授权行 = 不限制）：不能凭空冒出「已过期」，也不能报错。
func TestLicenseViewNoLicense(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	loginAdmin(t, plat)

	// 直接建一家**没有**授权行的企业（模拟升级前就存在的客户）
	db := license.TenantDB()
	require.NotNil(t, db)
	require.NoError(t, db.Create(&model.Company{Name: "存量客户", Status: "enabled"}).Error)
	var comp model.Company
	require.NoError(t, db.Where("name = ?", "存量客户").First(&comp).Error)
	hash, err := auth.HashPassword("pwd123456")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: comp.ID, Username: "old1", PasswordHash: hash,
	}).Error)

	c := newTestClient(r, t)
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "old1", "password": "pwd123456",
	}, "")
	require.EqualValues(t, 0, body["code"])

	_, body = c.doWithCookie("GET", "/api/admin/license", nil)
	require.EqualValues(t, 0, body["code"])
	self := body["data"].(map[string]interface{})["self"].(map[string]interface{})
	require.Equal(t, "none", self["level"], "没有授权行 = 不限制，不是已过期")
	require.Equal(t, false, self["expired"])
	require.Equal(t, "", self["expiresAt"], "不过期不该显示 0001-01-01")
	require.Equal(t, false, self["seatsFull"])
}
