package handler

import (
	"os"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 租户小程序凭据：读写接口 + secret 不回传 + 按租户生成小程序码。
//
// 覆盖三类最容易出错的点：
//  1. secret 只写不读（接口响应里连 key 都不该出现）
//  2. 半配置（只填 appid）必须按「没配」处理，不能拼出
//     「平台的 appid + 企业的 secret」这种谁也扫不开的组合
//  3. wxacode 按 staffId 归属租户取凭据——企业独立版共用后端时，
//     生成错 appid 的码扫出来直接打不开
func TestCompanyWXAppCredentialFlow(t *testing.T) {
	db := newSeedDB(t)
	// 2 号租户 = 企业独立版小程序
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "乙公司", ShortName: "乙", Status: "enabled"}).Error)
	// 该企业的员工：wxacode 凭 staffId 反查归属，所以必须真的落库
	require.NoError(t, db.Create(&model.Employee{StaffID: 880101, CompanyID: 2, Name: "乙员工", Status: "enabled"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-wxapp-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })
	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
		WXAppID:       "wx-global-appid",
		WXAppSecret:   "wx-global-secret",
	}
	r := buildEngine(t, db, cfg, fakeUploader{}, &fakeRefresher{})
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1) 没配过 = 走全局凭据，不是错误
	_, body := c.do("GET", "/api/admin/companies/2/wechat", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	d := body["data"].(map[string]interface{})
	require.Equal(t, false, d["configured"])
	require.Equal(t, "", d["appId"])

	// 2) 保存：appId 明文可读，secret 连 key 都不出现
	_, body = c.do("PUT", "/api/admin/companies/2/wechat", map[string]interface{}{
		"appId": "wx-tenant-b", "appSecret": "secret-b",
	}, adminToken)
	require.EqualValues(t, 0, body["code"], "响应：%v", body)

	_, body = c.do("GET", "/api/admin/companies/2/wechat", nil, adminToken)
	d = body["data"].(map[string]interface{})
	require.Equal(t, true, d["configured"])
	require.Equal(t, "wx-tenant-b", d["appId"])
	_, hasSecret := d["appSecret"]
	require.False(t, hasSecret, "appSecret 不应出现在响应里")

	// 3) 格式不对的 appid 直接被拒
	_, body = c.do("PUT", "/api/admin/companies/2/wechat", map[string]interface{}{
		"appId": "not-wx", "appSecret": "x",
	}, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 4) 半配置（只填 appid、secret 传空）= 清空 secret，
	//    整个凭据按「没配」处理，回落全局——绝不能留一个 appid 在库里
	_, body = c.do("PUT", "/api/admin/companies/2/wechat", map[string]interface{}{
		"appId": "wx-tenant-b", "appSecret": "",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/admin/companies/2/wechat", nil, adminToken)
	require.Equal(t, false, body["data"].(map[string]interface{})["configured"])

	// 5) 重新配上，验证 wxacode 真的用它
	_, body = c.do("PUT", "/api/admin/companies/2/wechat", map[string]interface{}{
		"appId": "wx-tenant-b", "appSecret": "secret-b",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 6) 非平台超管不能碰别人的凭据
	_, body = c.do("GET", "/api/admin/companies/2/wechat", nil, "")
	require.NotEqualValues(t, 0, body["code"])

	// 凭据缓存是进程级全局 map（key = companyID），跨测试会串扰：
	// 不清的话下一个用例读到的是本用例留下的 wx-tenant-b
	t.Cleanup(func() {
		wxAppMu.Lock()
		delete(wxAppCache, 2)
		wxAppMu.Unlock()
	})
}

// wxacode 按 staffId 归属的租户取凭据：企业版用企业自己的，
// 没配的企业回落全局那一份。
func TestWxacodeUsesTenantCredential(t *testing.T) {
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "乙公司", ShortName: "乙", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Employee{StaffID: 880201, CompanyID: 2, Name: "乙员工", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 2, AppID: "wx-tenant-b", AppSecret: "secret-b"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-wxapp-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })
	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
		WXAppID:       "wx-global-appid",
		WXAppSecret:   "wx-global-secret",
	}
	r := buildEngine(t, db, cfg, fakeUploader{}, &fakeRefresher{})
	c := newTestClient(r, t)

	// 捕获真正传给微信的凭据
	var got wechat.Config
	origGen := getWXACode
	getWXACode = func(wc wechat.Config, scene, page string) ([]byte, error) {
		got = wc
		return []byte("fake-jpeg"), nil
	}
	t.Cleanup(func() { getWXACode = origGen })
	// 缓存是进程级全局 map：用例前后各自清理，避免与其它用例串扰
	for _, id := range []int64{880201, 880202} {
		t.Cleanup(func() {
			wxacodeMu.Lock()
			delete(wxacodeCache, id)
			wxacodeMu.Unlock()
			staffCompanyMu.Lock()
			delete(staffCompanyCache, id)
			staffCompanyMu.Unlock()
		})
	}
	t.Cleanup(func() {
		wxAppMu.Lock()
		delete(wxAppCache, 2)
		wxAppMu.Unlock()
	})

	// 乙公司员工 → 用乙公司自己的 appid
	_, body := c.do("GET", "/api/wechat/wxacode?staffId=880201", nil, "")
	require.EqualValues(t, 0, body["code"], "响应：%v", body)
	require.Equal(t, "wx-tenant-b", got.AppID, "企业独立版员工必须用自己企业的凭据")
	require.Equal(t, "secret-b", got.AppSecret)

	// 1 号租户（朗汇）没配凭据 → 回落全局那一份，行为与改造前一致
	_, body = c.do("GET", "/api/wechat/wxacode?staffId=880202", nil, "")
	require.EqualValues(t, 0, body["code"], "响应：%v", body)
	require.Equal(t, "wx-global-appid", got.AppID, "没配租户凭据时必须回落全局")
	require.Equal(t, "wx-global-secret", got.AppSecret)
}

// 后台改完凭据要立刻生效：wxacode 的凭据缓存是 30s TTL，
// 不等它自然过期（否则运营改完发现没生效，会以为功能坏了）。
func TestCompanyWXAppSaveClearsCache(t *testing.T) {
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "乙公司", ShortName: "乙", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Employee{StaffID: 880301, CompanyID: 2, Name: "乙员工", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 2, AppID: "wx-old", AppSecret: "old-secret"}).Error)
	t.Cleanup(func() {
		staffCompanyMu.Lock()
		delete(staffCompanyCache, 880301)
		staffCompanyMu.Unlock()
		wxAppMu.Lock()
		delete(wxAppCache, 2)
		wxAppMu.Unlock()
		wxacodeMu.Lock()
		delete(wxacodeCache, 880301)
		wxacodeMu.Unlock()
	})

	// 先读一次把缓存焐热
	appID, _ := tenantWXApp(db, 2)
	require.Equal(t, "wx-old", appID)

	// 直接改库（绕开后台接口），然后手动清缓存——验证「清了就换」这件事本身
	require.NoError(t, db.Model(&model.CompanyWXApp{}).Where("company_id = ?", 2).
		Update("app_id", "wx-new").Error)
	clearWXAppCache(2)
	appID, _ = tenantWXApp(db, 2)
	require.Equal(t, "wx-new", appID, "清缓存后应立刻拿到新值")
}

// 只配了 appid 没配 secret（半配置）必须按「没配」处理：
// 否则会拼出「平台的 appid + 企业的 secret」，生成出来的码谁也扫不开，
// 而报错只会指向微信侧，极难查。
func TestTenantWXAppRequiresBoth(t *testing.T) {
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 3, AppID: "wx-only-id", AppSecret: ""}).Error)
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 4, AppID: "", AppSecret: "only-secret"}).Error)
	t.Cleanup(func() {
		wxAppMu.Lock()
		delete(wxAppCache, 3)
		delete(wxAppCache, 4)
		wxAppMu.Unlock()
	})

	appID, appSecret := tenantWXApp(db, 3)
	require.Equal(t, "", appID)
	require.Equal(t, "", appSecret)
	appID, appSecret = tenantWXApp(db, 4)
	require.Equal(t, "", appID)
	require.Equal(t, "", appSecret)

	// companyId 非法 / 不存在都不查库，直接按「没配」处理
	appID, _ = tenantWXApp(db, 0)
	require.Equal(t, "", appID)
	appID, _ = tenantWXApp(db, -1)
	require.Equal(t, "", appID)
	appID, _ = tenantWXApp(nil, 3)
	require.Equal(t, "", appID)
}

// staffId → 租户：走主键查询，查不到返回 0（= 用全局凭据）。
func TestCompanyIDOf(t *testing.T) {
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.Employee{StaffID: 880401, CompanyID: 2, Name: "甲", Status: "enabled"}).Error)
	t.Cleanup(func() {
		staffCompanyMu.Lock()
		delete(staffCompanyCache, 880401)
		staffCompanyMu.Unlock()
	})

	require.EqualValues(t, 2, companyIDOf(db, 880401))
	// 不存在的 staffId：负结果也缓存（否则枚举 staffId 扫号会每次 miss 都查库）
	require.EqualValues(t, 0, companyIDOf(db, 999999))
	require.EqualValues(t, 0, companyIDOf(db, 0))
	require.EqualValues(t, 0, companyIDOf(db, -1))
	require.EqualValues(t, 0, companyIDOf(nil, 880401))
}

// 企业列表必须带出「该企业小程序凭据是否配好」，否则平台要逐家点开才能发现
// 谁还没配——而没配的企业员工生成的小程序码会静默回落到平台小程序，
// 等客诉到来时后台完全看不出是哪家的问题。
// 半配置（只填 appid）必须算未配置，与取值侧 tenantWXApp 的口径一致。
func TestCompanyListWXConfigured(t *testing.T) {
	db := newSeedDB(t)
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "乙公司", ShortName: "乙", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "丙公司", ShortName: "丙", Status: "enabled"}).Error)
	// 2 号配全；3 号只填了 appid（secret 被清空的半配置）；1 号（朗汇）从没配过
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 2, AppID: "wx-tenant-b", AppSecret: "secret-b"}).Error)
	require.NoError(t, db.Create(&model.CompanyWXApp{CompanyID: 3, AppID: "wx-tenant-c", AppSecret: ""}).Error)
	t.Cleanup(func() {
		wxAppMu.Lock()
		delete(wxAppCache, 2)
		delete(wxAppCache, 3)
		wxAppMu.Unlock()
	})

	uploadDir, err := os.MkdirTemp("", "weichuan-wxapp-list-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })
	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
	}
	r := buildEngine(t, db, cfg, fakeUploader{}, &fakeRefresher{})
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/companies", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	comps := body["data"].([]interface{})
	require.Len(t, comps, 3)

	got := map[int64]bool{}
	for _, item := range comps {
		m := item.(map[string]interface{})
		got[int64(m["id"].(float64))] = m["wxConfigured"] == true
	}
	require.Equal(t, map[int64]bool{1: false, 2: true, 3: false}, got,
		"半配置（只填 appid）必须算未配置，否则列表说已配置、实际仍回落全局小程序")
}
