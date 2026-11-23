package handler

// 「出参侧按租户选 origin」的守门与端到端取证。
//
// 这一层最容易骗过自己的做法，是只断言「publicBaseUrlActive 变成 true」——
// 那只是后台界面上的一个布尔值。真正的承诺是「访客拿到的对象 URL 真的换了域名，
// 且平台对象没有被误换」。所以下面必须有真发 GET 的集成用例。
//
// 判定只有一份：storageprofile.Resolver.OriginFor / Diagnose，按「谁在写对象」
// 分叉。历史上有过 tenant_storage_configs 那一套五道关判定，第七十五轮删除。

import (
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedCloudBinding 给 1 号企业绑一套「云桶 + 分发域名」，返回两个档案 id。
func seedCloudBinding(t *testing.T, db *gorm.DB) (profileID, deliveryID int64) {
	t.Helper()
	sp := model.StorageProfile{
		Name: "云桶", Driver: "minio", Endpoint: "play.min.io", Bucket: "wc-demo",
		HostHeader: "wc-demo.play.min.io", AccessKey: "AK-STUB", SecretKey: "SK-STUB",
		BaseURL: "http://127.0.0.1:8080/uploads", PublicRead: true, UseSSL: true, Version: 1,
	}
	require.NoError(t, db.Create(&sp).Error)
	dp := model.DeliveryProfile{
		Name: "cdn", Provider: "aliyun", Domain: "https://img.cust.com", Version: 1,
	}
	require.NoError(t, db.Create(&dp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{
		TenantID: 1, StorageProfileID: sp.ID, DeliveryProfileID: dp.ID, Version: 1,
	}).Error)
	return sp.ID, dp.ID
}

// TestPublicOriginDiagnosisFollowsWriteOwnership 钉住「填了不等于生效」，
// 且每种未生效都给出可照做的原因。
//
// 每个负例都对应一种真实的线上状态；放行任何一个，都会把出参 URL 指到
// 一个取不到对象的客户域名，而后台看起来一切正常。
func TestPublicOriginDiagnosisFollowsWriteOwnership(t *testing.T) {
	_, db := bindingEngine(t)
	profileID, deliveryID := seedCloudBinding(t, db)

	base, active, reason := publicOriginDiagnosis(1)
	require.Equal(t, "https://img.cust.com", base)
	require.True(t, active)
	require.Empty(t, reason, "生效时不该挂一段没生效的解释")

	// 档案的即时生效靠 version 递增（解析器按 version 判快照是否过期），
	// 直接改列而不升版本等于「改了但运行时看不见」——那正是这条守门要防的。
	bumpVersions := func(t *testing.T) {
		t.Helper()
		for _, tbl := range []string{"storage_profiles", "delivery_profiles", "storage_bindings"} {
			require.NoError(t, db.Exec("UPDATE "+tbl+" SET version = version + 1").Error)
		}
	}

	cases := []struct {
		name       string
		mut        func(t *testing.T)
		wantReason string
	}{
		{"没绑 CDN 分发", func(t *testing.T) {
			require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = ?", 1).
				Update("delivery_profile_id", 0).Error)
		}, "没有绑定公开域名"},
		{"域名带路径", func(t *testing.T) {
			require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", deliveryID).
				Update("domain", "https://img.cust.com/prefix").Error)
		}, "不合法"},
		{"域名是伪协议", func(t *testing.T) {
			require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", deliveryID).
				Update("domain", "javascript:alert(1)").Error)
		}, "不合法"},
		{"驱动是 local", func(t *testing.T) {
			require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", profileID).
				Update("driver", "local").Error)
		}, "本地磁盘"},
		{"私有桶", func(t *testing.T) {
			require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", profileID).
				Update("public_read", false).Error)
		}, "私有桶"},
	}
	require.GreaterOrEqual(t, len(cases), 5, "量尺要有读数：负例集合不能被悄悄删短")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个子用例都从「条件齐全」重新开始，避免前一个用例的修改串味。
			require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", profileID).
				Updates(map[string]any{"driver": "minio", "public_read": true}).Error)
			require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", deliveryID).
				Update("domain", "https://img.cust.com").Error)
			require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = ?", 1).
				Update("delivery_profile_id", deliveryID).Error)
			bumpVersions(t)
			_, ok, _ := publicOriginDiagnosis(1)
			require.True(t, ok, "基线必须先成立，否则下面的负例是空对空")

			tc.mut(t)
			bumpVersions(t)

			gotBase, gotActive, gotReason := publicOriginDiagnosis(1)
			require.Empty(t, gotBase, "未生效时 origin 必须为空串，不能把半截域名传给出参")
			require.False(t, gotActive)
			require.Contains(t, gotReason, tc.wantReason)
		})
	}

	t.Run("没绑定", func(t *testing.T) {
		gotBase, gotActive, gotReason := publicOriginDiagnosis(99)
		require.Empty(t, gotBase)
		require.False(t, gotActive)
		require.Contains(t, gotReason, "未绑定")
	})
}

// TestPublicOriginForRequiresBinding 证明「按 cid 查解析器」与「诊断」是同一份结论。
func TestPublicOriginForRequiresBinding(t *testing.T) {
	_, db := bindingEngine(t)
	seedCloudBinding(t, db)

	o := publicOriginFor(1)
	require.Equal(t, "https://img.cust.com", o.Base)
	require.EqualValues(t, 1, o.CompanyID, "CompanyID 必须跟着走，否则归属判定无从做起")
	require.Equal(t, model.PlatformOrigin(), publicOriginFor(99), "没绑定的企业走平台入口")
	require.Equal(t, model.PlatformOrigin(), publicOriginFor(0), "cid<=0 不做无效查询")
}

// TestPublicSettingsAssetOrigin 钉住 /api/public/settings 的诊断出参。
//
// 关键负例是「不带 companyId 时绝不下发」：冷启动的 fetchPublicSettings 没有
// 租户参数，此时按缺省企业下发会让所有访客拿到 1 号企业的域名（跨租户串号）。
func TestPublicSettingsAssetOrigin(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	seedCloudBinding(t, db)

	_, env := c.do("GET", "/api/public/settings", nil, "")
	require.EqualValues(t, 0, env["code"])
	require.NotContains(t, env, "assetOrigin", "裸拉不许猜租户：这是串号入口")
	require.NotContains(t, env, "assetOriginActive")

	_, env = c.do("GET", "/api/public/settings?companyId=1", nil, "")
	require.Equal(t, "https://img.cust.com", env["assetOrigin"])
	require.Equal(t, true, env["assetOriginActive"])
	require.Equal(t, "", env["assetOriginNote"])

	// 非法 / 缺失的 companyId 一律不回传，别让 ParseInt 的 0 落到缺省企业上。
	for _, q := range []string{"?companyId=0", "?companyId=-1", "?companyId=abc"} {
		_, env = c.do("GET", "/api/public/settings"+q, nil, "")
		require.NotContains(t, env, "assetOrigin", "%s 不该回传诊断态", q)
	}
}

// assetOriginNote 出现在无需登录的公开接口上：诊断原因只能是固定分类文案，
// 不能把档案里的原始域名或底层错误原文带给任意访客。
func TestPublicSettingsAssetOriginNoteNeverEchoesRawConfig(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	const marker = "SECRET-INTERNAL-DOMAIN-8c2d"
	_, deliveryID := seedCloudBinding(t, db)
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", deliveryID).
		Updates(map[string]any{"domain": "javascript:alert('" + marker + "')", "version": 2}).Error)

	_, env := c.do("GET", "/api/public/settings?companyId=1", nil, "")
	require.EqualValues(t, 0, env["code"])
	require.Equal(t, "", env["assetOrigin"])
	require.Equal(t, false, env["assetOriginActive"])
	note, _ := env["assetOriginNote"].(string)
	require.Contains(t, note, "不合法", "要保留可照做的分类原因")
	require.NotContains(t, note, marker, "公开接口不得回显档案里的原始域名")
	require.NotContains(t, note, "javascript:")
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

// TestPublicCompanyGetUsesTenantOrigin 是本层的主取证：真发公开 GET，
// 证明访客拿到的 URL 真的换了域名，且平台对象 / 历史裸键没有被误换。
func TestPublicCompanyGetUsesTenantOrigin(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	profileID, _ := seedCloudBinding(t, db)

	getLogo := func() string {
		_, env := c.do("GET", "/api/company?companyId=1", nil, "")
		require.EqualValues(t, 0, env["code"], "%v", env["msg"])
		data, ok := env["data"].(map[string]interface{})
		require.True(t, ok, "data 不是对象：%v", env["data"])
		v, _ := data["logo"].(string)
		require.NotEmpty(t, v, "量尺要有读数：logo 不能是空串，否则下面全是空对空绿")
		return v
	}

	// ① 条件齐全：本租户新键换成客户域名。
	setCompanyLogo(t, db, "t/1/logo.png")
	require.Equal(t, "https://img.cust.com/uploads/t/1/logo.png", getLogo())

	// ② 平台对象不在客户桶里，必须继续走平台入口。
	setCompanyLogo(t, db, "p/library/logo.png")
	require.Equal(t, "/uploads/p/library/logo.png", getLogo())

	// ③ 历史无前缀裸键同样还在平台盘上。
	setCompanyLogo(t, db, "old-logo.png")
	require.Equal(t, "/uploads/old-logo.png", getLogo())

	// ④ 别家租户的键不能指到这家的域名。
	setCompanyLogo(t, db, "t/9/logo.png")
	require.Equal(t, "/uploads/t/9/logo.png", getLogo())

	// ⑤ 存量绝对 URL 原样返回（改了老数据立刻裂图）。
	setCompanyLogo(t, db, "https://cdn.old.example.com/logo.png")
	require.Equal(t, "https://cdn.old.example.com/logo.png", getLogo())

	// ⑥ 档案改成本地后同一秒内就退回平台入口，不依赖缓存过期。
	setCompanyLogo(t, db, "t/1/logo.png")
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", profileID).
		Updates(map[string]any{"driver": "local", "version": 2}).Error)
	require.Equal(t, "/uploads/t/1/logo.png", getLogo())
}
