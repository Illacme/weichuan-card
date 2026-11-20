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

	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	put := map[string]any{"companyId": cid, "quotaMb": 50}
	_, body := c.do("PUT", "/api/admin/storage/quota", put, admin)
	require.EqualValues(t, 1, body["code"], "自有桶租户不应能设置平台配额：%v", body)
	require.Contains(t, body["msg"], "不适用", "必须因「平台配额不适用」被拒，不是参数错误 / 企业不存在")

	row, err := storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 0, row.QuotaMB, "被拒的请求不能留下 quota_mb 覆盖值")

	// 反向验证：开关关掉后平台配额重新适用，同一家企业的 PUT 应正常落库。
	// 若上面把整个写接口禁掉了（而不是按自有桶判定），这一支会红。
	t.Setenv("TENANT_STORAGE_ROUTE", "off")
	_, body = c.do("PUT", "/api/admin/storage/quota", put, admin)
	require.EqualValues(t, 0, body["code"], "开关关掉后必须恢复配额写入：%v", body)

	row, err = storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 50, row.QuotaMB)
}

// 档案绑定必须与旧表同等地豁免平台配额。
//
// 这是新模型下最容易漏的一处：上传早已进客户桶（DynamicResolver 先问档案），
// 配额却还在按 tenant_storage_configs 判定 → 客户被平台上限卡住，
// 而「已用 / 上限」那根进度条画的还是平台盘的数字。
//
// 特别注意这里把 TENANT_STORAGE_ROUTE 设为 off：那是**旧表通道**的开关，
// 档案不依赖它。按开关短路就会重现上面那个错误结论。
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

	t.Setenv("TENANT_STORAGE_ROUTE", "off")
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

// GET /api/admin/companies/:id/storage 必须说明「这张表已被存储档案接管」。
//
// 不说的话，超管会对着一个「已生效」的绿勾改这里，改完发现毫无效果——
// 上传与出参早已走档案。没有旧表行时更要说：企业只要绑了档案就能用自有桶，
// 此时界面写「尚未配置该企业的自有存储」是纯粹的误导。
func TestTenantStorageViewReportsProfileSupersede(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	const cid = 2
	sp := model.StorageProfile{Name: "已接管档案", Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		BaseURL: "https://x/uploads", AccessKey: "AK", SecretKey: "SK", PublicRead: true, UseSSL: true, Version: 1}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: cid, StorageProfileID: sp.ID, Version: 1}).Error)

	// 没有 tenant_storage_configs 行：视图仍要报「已被档案接管」。
	_, body := c.do("GET", "/api/admin/companies/2/storage", nil, admin)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	d := body["data"].(map[string]any)
	require.Equal(t, false, d["configured"], "这家没填过旧表")
	require.Equal(t, true, d["supersededByProfile"], "绑定档案后必须提示已接管")
	require.Equal(t, "已接管档案", d["supersededByProfileName"])

	// 反向：另一家没有绑定 → 不提示接管。
	_, body = c.do("GET", "/api/admin/companies/3/storage", nil, admin)
	require.EqualValues(t, 0, body["code"], "%v", body["msg"])
	d = body["data"].(map[string]any)
	require.Equal(t, false, d["supersededByProfile"])
	require.Equal(t, "", d["supersededByProfileName"])
}
