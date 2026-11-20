package handler

import (
	"net/http"
	"strconv"
	"testing"

	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件守「配额口径」：企业切到自有桶后，平台配额从「硬拦截」
// 改为「计量 / 展示」——checkStorageQuota 不再因平台配额拒绝这类租户的上传，
// 但字节仍计入 storage_usage 供展示；GET /api/admin/storage 用 platformQuotaApplies
// 告知前端这是哪一种口径。
//
// 判据只有一条：生效档案的驱动不是 local（见 tenantUsesOwnBucket）。
// 绑定本身不是判据——绑到本地档案的企业，对象仍在平台盘上，平台配额照样该管。
//
// 关键不变量（必须用测试钉死，否则上线后会悄悄回退成"客户自己的空间被平台配额卡住"）：
//   1. 绑到云档案的租户，checkStorageQuota 恒放行；
//   2. 没绑档案、或绑的是本地档案时，平台配额照常拦截（不能把豁免扩大化）；
//   3. 上传接口对自有桶租户不再拒绝（端到端）；
//   4. GET 出参的 platformQuotaApplies 与判定同源。

// insertOwnBucketConfig 给 cid 绑一套云档案。endpoint / bucket 不进判定，
// 用占位是安全的——这里测的是配额口径，不是上传能不能连上桶。
func insertOwnBucketConfig(t *testing.T, db *gorm.DB, cid int64) {
	t.Helper()
	sp := model.StorageProfile{
		Name: "桶-" + strconv.FormatInt(cid, 10), Driver: "minio",
		Endpoint: "s.example.com", Bucket: "b", BaseURL: "https://x/uploads",
		AccessKey: "AK", SecretKey: "SK", PublicRead: true, UseSSL: true, Version: 1,
	}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{
		TenantID: cid, StorageProfileID: sp.ID, Version: 1,
	}).Error)
}

// TestCheckStorageQuotaExemptsOwnBucketTenant 是配额口径的核心：
// 自有桶租户在 1MB 全局配额下传 10MB 仍放行；开关关掉 / 没配则照常拦截。
func TestCheckStorageQuotaExemptsOwnBucketTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, db := setupEngineWithDB(t)

	// 全局配额压到 1MB（函数值注入，与 TestUploadStorageQuota 同一手法）。
	old := globalStorageQuotaMBFn
	globalStorageQuotaMBFn = func() int64 { return 1 }
	t.Cleanup(func() { globalStorageQuotaMBFn = old })

	const cid = 2
	insertOwnBucketConfig(t, db, cid)

	// 绑到云档案：不受平台配额限制（10MB >> 1MB 仍放行）。
	ok, msg := checkStorageQuota(db, cid, 10*1024*1024)
	require.True(t, ok, "已切到自有桶的租户不应被平台配额拦截")
	require.Empty(t, msg)

	// 反向验证（其一）：绑到本地档案 = 对象仍在平台盘 → 回到平台配额硬拦截。
	// 这一支把"驱动非 local"条件摘掉，断言必须翻红，证明放行确实由判定驱动。
	local := model.StorageProfile{Name: "平台盘-2", Driver: "local", PublicRead: true, UseSSL: true, Version: 1}
	require.NoError(t, db.Create(&local).Error)
	require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = ?", cid).
		Update("storage_profile_id", local.ID).Error)
	ok, msg = checkStorageQuota(db, cid, 10*1024*1024)
	require.False(t, ok, "本地档案必须恢复平台配额拦截（不能把豁免扩大化）")
	require.Contains(t, msg, "存储空间已用尽")

	// 反向验证（其二）：另一家没绑档案 → 照样拦截。
	ok, msg = checkStorageQuota(db, 3, 10*1024*1024)
	require.False(t, ok, "没绑档案的租户必须仍受平台配额拦截")
	require.Contains(t, msg, "存储空间已用尽")
}

// TestUploadOwnBucketTenantNotBlocked 端到端证明：自有桶租户在全局配额极小的情况下，
// 上传接口不再返回「存储空间已用尽」。
//
// 这里绑的是云档案，测试环境没有真桶，所以上传本身会因桶不可达而失败——
// 但失败原因**不能**是平台配额。配额闸门放行与否由
// TestCheckStorageQuotaExemptsOwnBucketTenant 完整覆盖（含两条反向），
// 这条只钉「上传这条路径真的用上了同一个判定」，避免闸门被接在别处。
func TestUploadOwnBucketTenantNotBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	old := globalStorageQuotaMBFn
	globalStorageQuotaMBFn = func() int64 { return 1 } // 1MB 全局配额
	t.Cleanup(func() { globalStorageQuotaMBFn = old })

	const cid = 2
	insertOwnBucketConfig(t, db, cid)

	// 2MB 远超 1MB 全局配额：改造前会被配额拒，改造后自有桶租户放行。
	code, body := uploadMultipart(t, r, "/api/admin/upload?companyId=2", admin, "big.mp4", fakeMP4(2*1024*1024))
	require.Equal(t, http.StatusOK, code)
	msg, _ := body["msg"].(string)
	require.NotContains(t, msg, "存储空间已用尽",
		"自有桶租户不应被平台配额拒绝：%v", body)
}

// TestStorageGetReportsPlatformQuotaNotApplicable GET 出参必须说出"平台配额适不适用"，
// 否则前端只能自己猜、把自有桶租户的累计用量画成满格拦截条。
func TestStorageGetReportsPlatformQuotaNotApplicable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	const cid = 2
	insertOwnBucketConfig(t, db, cid)

	// 自有桶租户：platformQuotaApplies = false，percent 不画满格。
	_, sb := c.do("GET", "/api/admin/storage?companyId=2", nil, admin)
	require.EqualValues(t, 0, sb["code"])
	d := sb["data"].(map[string]any)
	require.Equal(t, false, d["platformQuotaApplies"], "自有桶租户必须标明平台配额不适用")

	// 反向：没绑档案的企业 → platformQuotaApplies = true。
	_, sb = c.do("GET", "/api/admin/storage?companyId=3", nil, admin)
	require.EqualValues(t, 0, sb["code"])
	d = sb["data"].(map[string]any)
	require.Equal(t, true, d["platformQuotaApplies"], "没绑档案的企业平台配额必须适用")
}

// TestStorageQuotaPutRejectsOwnBucketTenant 守住写侧：管理后台已跳过提交，后端仍要拒收。
// 否则任何绕过界面的调用方都能写进一条永远不生效的 quota_mb，还留一条审计。
func TestStorageQuotaPutRejectsOwnBucketTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	const cid = 2
	// PUT 会先校验企业存在。不建这行，「企业不存在」同样返回 code=1，
	// 断言就会假绿——根本没走到自有桶闸门。先让前置条件成立，再钉住拒绝原因。
	require.NoError(t, db.Create(&model.Company{ID: cid, Name: "乙公司", Status: "enabled"}).Error)
	insertOwnBucketConfig(t, db, cid)

	put := map[string]any{"companyId": cid, "quotaMb": 50}
	_, body := c.do("PUT", "/api/admin/storage/quota", put, admin)
	require.EqualValues(t, 1, body["code"], "自有桶租户不应能设置平台配额：%v", body)
	require.Contains(t, body["msg"], "不适用", "必须因「平台配额不适用」被拒，不是参数错误 / 企业不存在")

	row, err := storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 0, row.QuotaMB, "被拒的请求不能留下 quota_mb 覆盖值")

	// 反向验证：改绑本地档案后平台配额重新适用，同一家企业的 PUT 应正常落库。
	// 若上面把整个写接口禁掉了（而不是按自有桶判定），这一支会红。
	local := model.StorageProfile{Name: "平台盘-put", Driver: "local", PublicRead: true, UseSSL: true, Version: 1}
	require.NoError(t, db.Create(&local).Error)
	require.NoError(t, db.Model(&model.StorageBinding{}).Where("tenant_id = ?", cid).
		Update("storage_profile_id", local.ID).Error)
	_, body = c.do("PUT", "/api/admin/storage/quota", put, admin)
	require.EqualValues(t, 0, body["code"], "改绑本地档案后必须恢复配额写入：%v", body)

	row, err = storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 50, row.QuotaMB)
}

// 档案绑定必须与旧表同等地豁免平台配额。
//
// 这是最容易漏的一处：上传早已进客户桶，配额若还按平台盘判定，
// 客户会被平台上限卡住，而「已用 / 上限」那根进度条画的还是平台盘的数字。
func TestCheckStorageQuotaExemptsProfileBoundTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, db := setupEngineWithDB(t)

	old := globalStorageQuotaMBFn
	globalStorageQuotaMBFn = func() int64 { return 1 } // 1MB 全局配额
	t.Cleanup(func() { globalStorageQuotaMBFn = old })

	const cid = 4
	cloud := model.StorageProfile{Name: "客户桶", Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, UseSSL: true, Version: 1}
	require.NoError(t, db.Create(&cloud).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: cid, StorageProfileID: cloud.ID, Version: 1}).Error)

	ok, msg := checkStorageQuota(db, cid, 10*1024*1024)
	require.True(t, ok, "档案绑定后对象已进客户桶，平台配额不应再拦：%v", msg)

	// 反向一：没有绑定的企业仍受平台配额拦截（豁免不能扩大化）。
	ok, msg = checkStorageQuota(db, 5, 10*1024*1024)
	require.False(t, ok, "没绑档案的企业必须仍受平台配额拦截")
	require.Contains(t, msg, "存储空间已用尽")

	// 反向二：绑到本地档案 = 对象仍在平台盘，平台配额照常适用。
	// 这一支证明豁免是由「驱动非 local」驱动，不是「只要绑了档案就放行」。
	local := model.StorageProfile{Name: "平台盘", Driver: "local", PublicRead: true, UseSSL: true, Version: 1}
	require.NoError(t, db.Create(&local).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 6, StorageProfileID: local.ID, Version: 1}).Error)
	ok, msg = checkStorageQuota(db, 6, 10*1024*1024)
	require.False(t, ok, "本地档案的对象仍在平台盘，平台配额必须照常适用")
	require.Contains(t, msg, "存储空间已用尽")
}
