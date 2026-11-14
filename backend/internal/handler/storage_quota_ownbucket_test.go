package handler

import (
	"net/http"
	"testing"

	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件守 P4 的「配额口径」改造：企业切到自有桶后，平台配额从「硬拦截」
// 改为「计量 / 展示」——checkStorageQuota 不再因平台配额拒绝这类租户的上传，
// 但字节仍计入 storage_usage 供展示；GET /api/admin/storage 用 platformQuotaApplies
// 告知前端这是哪一种口径。
//
// 关键不变量（必须用测试钉死，否则上线后会悄悄回退成"客户自己的空间被平台配额卡住"）：
//   1. 已切到自有桶（路由 on + 配置生效）的租户，checkStorageQuota 恒放行；
//   2. 同一把开关关掉、或这家没配自有桶时，平台配额照常拦截（不能把豁免扩大化）；
//   3. 上传接口对自有桶租户不再拒绝（端到端）；
//   4. GET 出参的 platformQuotaApplies 与判定同源。
//
// 第 2 条就是反向验证：把条件里的"路由 on"或"配置生效"摘掉，断言必须翻红，
// 证明第一条的放行是真的由"是否自有桶"驱动，而不是把硬拦截整个删了。

// insertOwnBucketConfig 插一行「已生效的自有桶配置」：只填 tenantStorageActive
// 判定的必要字段（enabled / driver 非 local / 凭据非空）。endpoint / bucket 不进判定，
// 用占位是安全的——这里测的是配额口径，不是上传能不能连上桶。
func insertOwnBucketConfig(t *testing.T, db *gorm.DB, cid int64) {
	t.Helper()
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID:    cid,
		Driver:       "minio", // 非 local，tenantStorageActive 据此放行
		Enabled:      true,
		AccessKeyEnc: "ak-encrypted-placeholder", // 只判非空，不解密
		SecretKeyEnc: "sk-encrypted-placeholder",
		PublicRead:   true,
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

	// 开关打开：自有桶租户不受平台配额限制（10MB >> 1MB 仍放行）。
	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	ok, msg := checkStorageQuota(db, cid, 10*1024*1024)
	require.True(t, ok, "已切到自有桶的租户不应被平台配额拦截")
	require.Empty(t, msg)

	// 反向验证（其一）：同一家企业，开关关掉 → 回到平台配额硬拦截。
	// 这一支把"自有桶"条件摘掉，断言必须翻红，证明上面的放行确实由判定驱动。
	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	ok, msg = checkStorageQuota(db, cid, 10*1024*1024)
	require.False(t, ok, "开关关掉后必须恢复平台配额拦截（不能把豁免扩大化）")
	require.Contains(t, msg, "存储空间已用尽")

	// 反向验证（其二）：开关开着，但另一家没配自有桶 → 照样拦截。
	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	ok, msg = checkStorageQuota(db, 3, 10*1024*1024)
	require.False(t, ok, "没配自有桶的租户必须仍受平台配额拦截")
	require.Contains(t, msg, "存储空间已用尽")
}

// TestUploadOwnBucketTenantNotBlocked 端到端证明：自有桶租户在全局配额极小的情况下，
// 上传接口不再返回"存储空间已用尽"，且字节照常计入展示用量。
//
// 注意测试引擎用的是 StaticResolver（恒落本地盘），所以这里不真的去连客户桶；
// 我们只验证"配额这层不再拦"，上传落盘由既有测试覆盖。
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
	t.Setenv("TENANT_STORAGE_ROUTE", "on")

	// 2MB 远超 1MB 全局配额：改造前会被拒，改造后自有桶租户放行。
	code, body := uploadMultipart(t, r, "/api/admin/upload?companyId=2", admin, "big.mp4", fakeMP4(2*1024*1024))
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"], "自有桶租户不应被平台配额拒绝：%v", body)

	// 字节仍计入 storage_usage（计量，只是不拦）。
	row, err := storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 2*1024*1024, row.Bytes, "自有桶租户的上传仍应计量展示")
	require.EqualValues(t, 1, row.Files)
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

	// 开关打开：自有桶租户，platformQuotaApplies = false，percent 不画满格。
	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	_, sb := c.do("GET", "/api/admin/storage?companyId=2", nil, admin)
	require.EqualValues(t, 0, sb["code"])
	d := sb["data"].(map[string]any)
	require.Equal(t, false, d["platformQuotaApplies"], "自有桶租户必须标明平台配额不适用")

	// 反向：开关关掉 → platformQuotaApplies = true（与改造前行为一致）。
	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	_, sb = c.do("GET", "/api/admin/storage?companyId=2", nil, admin)
	require.EqualValues(t, 0, sb["code"])
	d = sb["data"].(map[string]any)
	require.Equal(t, true, d["platformQuotaApplies"], "开关关掉后平台配额必须重新适用")
}
