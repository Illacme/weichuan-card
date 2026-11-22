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

// 平台对象（p/）只该用平台自己的 CDN 档案。
//
// 第八十一轮修掉的串味：TenantFromURLs 只认 /uploads/t/<cid>/，p/ 返回 0，
// 而 Resolve(0) 读的是「全局默认绑定」那一行——于是平台对象的刷新会借用
// 「无绑定企业默认分发档案」的凭据。这份全局分发档案配错了，平台对象的覆盖写
// 就会报错（或更糟：用别家账号刷），而平台自己那份配置根本没被读过。
func TestDynamicCDNPlatformObjectsNeverBorrowGlobalDelivery(t *testing.T) {
	db := testDB(t)
	sp := model.StorageProfile{
		Name: "s", Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK",
		PublicRead: true, UseSSL: true, Version: 1,
	}
	// 全局默认绑定带一份**凭据不全**的分发档案：一旦被 p/ 借走，刷新必失败。
	broken := model.DeliveryProfile{Name: "global-cdn", Provider: "aliyun", Domain: "https://cdn.example.com", Version: 1}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&broken).Error)
	require.NoError(t, db.Create(&model.StorageBinding{
		TenantID: 0, StorageProfileID: sp.ID, DeliveryProfileID: broken.ID, Version: 1,
	}).Error)

	r := newTestResolver(t, db)
	base := &countingRefresher{}
	cdn := NewDynamicCDN(base, r)

	// 平台对象：走平台刷新器，全局绑定的坏凭据不得参与。
	require.NoError(t, cdn.Refresh([]string{"https://x.com/uploads/p/library/cat-1.png"}))
	require.Equal(t, 1, base.calls, "平台对象必须用平台 CDN 档案")

	// 裸键形态（p/ 开头）同样算平台对象。
	require.NoError(t, cdn.Refresh([]string{"p/library/cat-1.png"}))
	require.Equal(t, 2, base.calls)

	// 对照：同一份全局绑定对「无自身绑定的企业」仍然生效，且凭据坏了要报错——
	// 不能因为修了 p/ 就把租户侧的显式失败也一起改掉。
	err := cdn.Refresh([]string{"https://x.com/uploads/t/12/a.png"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "拒绝静默回落")
	require.Equal(t, 2, base.calls, "租户刷新失败时不得被平台刷新器顶替")

	// 混批：p/ 走平台并成功，t/ 仍按绑定报错；两边的错误都要浮出来。
	err = cdn.Refresh([]string{
		"https://x.com/uploads/p/library/cat-1.png",
		"https://x.com/uploads/t/12/a.png",
	})
	require.Error(t, err)
	require.Equal(t, 3, base.calls)
}
