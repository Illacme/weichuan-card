package handler

// 第六十九轮「出参侧按租户选 origin」的守门与端到端取证。
//
// 这一轮最容易骗过自己的做法，是只断言「publicBaseUrlActive 变成 true」——
// 那只是后台界面上的一个布尔值。真正的承诺是「访客拿到的对象 URL 真的换了域名，
// 且平台对象没有被误换」。所以下面必须有真发 GET 的集成用例，
// 并且把开关 off / on 两态都跑一遍，证明默认值下是零行为变更。

import (
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// activeRow 是一行「其它条件全齐」的配置。凭据列只判非空，不需要真密文：
// tenantStorageActive 关心的是「凭据是否完整」，不是能否解密（解密失败是上传侧的事）。
func activeRow() *model.TenantStorageConfig {
	return &model.TenantStorageConfig{
		CompanyID:     1,
		Driver:        "minio",
		Endpoint:      "play.min.io",
		Bucket:        "wc-demo",
		HostHeader:    "wc-demo.play.min.io",
		AccessKeyEnc:  "v1:stub-ak",
		SecretKeyEnc:  "v1:stub-sk",
		PublicBaseURL: "https://img.cust.com",
		PublicRead:    true,
		Enabled:       true,
	}
}

// TestPublicOriginStateFollowsRealConditions 钉住「填了不等于生效」。
//
// 每个负例都对应一种真实的线上状态；放行任何一个，都会把出参 URL 指到
// 一个取不到对象的客户域名，而后台看起来一切正常。
func TestPublicOriginStateFollowsRealConditions(t *testing.T) {
	t.Setenv("TENANT_STORAGE_ROUTE", "on")

	base, active, reason := publicOriginState(activeRow())
	require.Equal(t, "https://img.cust.com", base)
	require.True(t, active)
	require.Empty(t, reason, "生效时不该挂一段没生效的解释")

	cases := []struct {
		name       string
		mut        func(*model.TenantStorageConfig)
		wantReason string
	}{
		{"没登记域名", func(r *model.TenantStorageConfig) { r.PublicBaseURL = "" }, ""},
		{"域名带路径", func(r *model.TenantStorageConfig) { r.PublicBaseURL = "https://img.cust.com/prefix" }, "不合法"},
		{"域名带 scheme 之外的内容", func(r *model.TenantStorageConfig) { r.PublicBaseURL = "javascript:alert(1)" }, "不合法"},
		{"配置未启用", func(r *model.TenantStorageConfig) { r.Enabled = false }, "配置未启用"},
		{"驱动是 local", func(r *model.TenantStorageConfig) { r.Driver = "local" }, "本地磁盘"},
		{"凭据不完整", func(r *model.TenantStorageConfig) { r.SecretKeyEnc = "" }, "凭据不完整"},
		{"私有桶", func(r *model.TenantStorageConfig) { r.PublicRead = false }, "403"},
	}
	require.GreaterOrEqual(t, len(cases), 7, "量尺要有读数：负例集合不能被悄悄删短")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := activeRow()
			tc.mut(row)
			gotBase, gotActive, gotReason := publicOriginState(row)
			require.Empty(t, gotBase, "未生效时 origin 必须为空串，不能把半截域名传给出参")
			require.False(t, gotActive)
			if tc.wantReason != "" {
				require.Contains(t, gotReason, tc.wantReason)
			}
		})
	}

	t.Run("路由开关未开", func(t *testing.T) {
		t.Setenv("TENANT_STORAGE_ROUTE", "off")
		gotBase, gotActive, gotReason := publicOriginState(activeRow())
		require.Empty(t, gotBase)
		require.False(t, gotActive)
		require.Contains(t, gotReason, "TENANT_STORAGE_ROUTE")
	})

	t.Run("没配置行", func(t *testing.T) {
		gotBase, gotActive, gotReason := publicOriginState(nil)
		require.Empty(t, gotBase)
		require.False(t, gotActive)
		require.Contains(t, gotReason, "尚未配置")
	})
}

// TestPublicOriginForRequiresActiveRow 证明「按 cid 读库」与「按行判定」是同一份结论，
// 且默认开关 off 时出参零行为变更。
func TestPublicOriginForRequiresActiveRow(t *testing.T) {
	_, db := bindingEngine(t)
	require.NoError(t, db.Create(activeRow()).Error)

	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	require.Equal(t, model.PlatformOrigin(), publicOriginFor(db, 1),
		"开关默认 off，填了域名也不能改变任何出参")

	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	o := publicOriginFor(db, 1)
	require.Equal(t, "https://img.cust.com", o.Base)
	require.EqualValues(t, 1, o.CompanyID, "CompanyID 必须跟着走，否则归属判定无从做起")
	require.Equal(t, model.PlatformOrigin(), publicOriginFor(db, 99), "没配过的企业走平台入口")
	require.Equal(t, model.PlatformOrigin(), publicOriginFor(db, 0), "cid<=0 不做无效查询")
}

// TestPublicSettingsAssetOrigin 钉住 /api/public/settings 的诊断出参。
//
// 关键负例是「不带 companyId 时绝不下发」：冷启动的 fetchPublicSettings 没有
// 租户参数，此时按缺省企业下发会让所有访客拿到 1 号企业的域名（跨租户串号）。
func TestPublicSettingsAssetOrigin(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	require.NoError(t, db.Create(activeRow()).Error)
	t.Setenv("TENANT_STORAGE_ROUTE", "on")

	_, env := c.do("GET", "/api/public/settings", nil, "")
	require.EqualValues(t, 0, env["code"])
	require.NotContains(t, env, "assetOrigin", "裸拉不许猜租户：这是串号入口")
	require.NotContains(t, env, "assetOriginActive")

	_, env = c.do("GET", "/api/public/settings?companyId=1", nil, "")
	require.Equal(t, "https://img.cust.com", env["assetOrigin"])
	require.Equal(t, true, env["assetOriginActive"])
	require.Equal(t, "", env["assetOriginNote"])

	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	_, env = c.do("GET", "/api/public/settings?companyId=1", nil, "")
	require.Equal(t, "", env["assetOrigin"])
	require.Equal(t, false, env["assetOriginActive"])
	require.Contains(t, env["assetOriginNote"], "TENANT_STORAGE_ROUTE")

	// 非法 / 缺失的 companyId 一律不回传，别让 ParseInt 的 0 落到缺省企业上。
	for _, q := range []string{"?companyId=0", "?companyId=-1", "?companyId=abc"} {
		_, env = c.do("GET", "/api/public/settings"+q, nil, "")
		require.NotContains(t, env, "assetOrigin", "%s 不该回传诊断态", q)
	}
}

// setCompanyLogo 把 1 号企业的 logo 改成指定形态（表可能已有行，也可能没有）。
func setCompanyLogo(t *testing.T, db *gorm.DB, logo string) {
	t.Helper()
	var ci model.CompanyInfo
	res := db.Where("company_id = ?", 1).Limit(1).Find(&ci)
	require.NoError(t, res.Error)
	if res.RowsAffected == 0 {
		ci = model.CompanyInfo{CompanyID: 1}
	}
	ci.Logo = logo
	require.NoError(t, db.Save(&ci).Error)
}

// TestPublicCompanyGetUsesTenantOrigin 是本轮的主取证：真发公开 GET，
// 证明访客拿到的 URL 真的换了域名，且平台对象 / 历史裸键没有被误换。
func TestPublicCompanyGetUsesTenantOrigin(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	require.NoError(t, db.Create(activeRow()).Error)

	getLogo := func() string {
		_, env := c.do("GET", "/api/company?companyId=1", nil, "")
		require.EqualValues(t, 0, env["code"], "%v", env["msg"])
		data, ok := env["data"].(map[string]interface{})
		require.True(t, ok, "data 不是对象：%v", env["data"])
		v, _ := data["logo"].(string)
		require.NotEmpty(t, v, "量尺要有读数：logo 不能是空串，否则下面全是空对空绿")
		return v
	}

	setCompanyLogo(t, db, "t/1/logo.png")

	// ① 默认开关 off：与第六十八轮完全一致，零行为变更。
	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	require.Equal(t, "/uploads/t/1/logo.png", getLogo())

	// ② 开关打开且条件齐全：本租户新键换成客户域名。
	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	require.Equal(t, "https://img.cust.com/uploads/t/1/logo.png", getLogo())

	// ③ 平台对象不在客户桶里，必须继续走平台入口。
	setCompanyLogo(t, db, "p/library/logo.png")
	require.Equal(t, "/uploads/p/library/logo.png", getLogo())

	// ④ 历史无前缀裸键同样还在平台盘上。
	setCompanyLogo(t, db, "old-logo.png")
	require.Equal(t, "/uploads/old-logo.png", getLogo())

	// ⑤ 别家租户的键不能指到这家的域名。
	setCompanyLogo(t, db, "t/9/logo.png")
	require.Equal(t, "/uploads/t/9/logo.png", getLogo())

	// ⑥ 存量绝对 URL 原样返回（改了老数据立刻裂图）。
	setCompanyLogo(t, db, "https://cdn.old.example.com/logo.png")
	require.Equal(t, "https://cdn.old.example.com/logo.png", getLogo())

	// ⑦ 配置停用后同一秒内就退回平台入口，不依赖缓存过期。
	setCompanyLogo(t, db, "t/1/logo.png")
	require.NoError(t, db.Model(&model.TenantStorageConfig{}).
		Where("company_id = ?", 1).Update("enabled", false).Error)
	require.Equal(t, "/uploads/t/1/logo.png", getLogo())
}
