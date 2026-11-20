package storageprofile

// CDN 静默回退守门。
//
// 早先 DynamicCDN.Refresh 在「解析档案失败」或「构造刷新器失败」时会悄悄落到
// 平台全局 refresher，base 为空就直接 return nil。企业配了分发档案但凭据写错时，
// 违规图下架后边缘仍是旧图，接口却报成功——下架在 CDN 边缘静默失效。
// 这与本包「绑定存在但档案坏了要报错，绝不静默回退」的纪律冲突。

import (
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/model"
)

type countingRefresher struct{ calls int }

func (r *countingRefresher) Refresh([]string) error { r.calls++; return nil }

func TestDynamicCDNFailsLoudlyWhenDeliveryProfileBroken(t *testing.T) {
	db := testDB(t)
	sp := model.StorageProfile{
		Name: "s", Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK",
		PublicRead: true, UseSSL: true, Version: 1,
	}
	// 分发档案配了厂商却没填凭据 → NewCDNRefresher 必失败。
	dp := model.DeliveryProfile{Name: "d", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&dp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{
		TenantID: 12, StorageProfileID: sp.ID, DeliveryProfileID: dp.ID, Version: 1,
	}).Error)

	r := newTestResolver(t, db)
	base := &countingRefresher{}
	cdn := NewDynamicCDN(base, r)

	urls := []string{"https://x.com/uploads/t/12/a.png"}
	err := cdn.Refresh(urls)
	require.Error(t, err, "凭据坏了必须显式失败")
	require.Contains(t, err.Error(), "拒绝静默回落")
	require.Zero(t, base.calls, "有分发档案时不得偷偷用平台 refresher 顶替")

	// 明确配了 provider=none 是合法「不刷新」，也不能落到 base。
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", dp.ID).
		Updates(map[string]any{"provider": "none", "version": 2}).Error)
	require.NoError(t, r.Touch())
	require.NoError(t, cdn.Refresh(urls))
	require.Zero(t, base.calls)
}
