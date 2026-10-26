package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/license"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本文件守 B4 + B3 两条商用闭环：
//   B4 平台模式新租户默认拿到一条**落库的**试用授权（席位 + 到期），
//      到期由 EnforceLicense 降级只读；运营在后台「授权」里转正 / 延期 / 改席位。
//   B3 按租户累计存储用量，超过配额直接拒绝上传，用量与配额可查。
//
// 为什么必须在 handler 层再测一遍（license 包已测过写入语义）：
//   - B4 的关键是「建企业这条路径真的写了行」——只测 SetTenantLicense 通过，
//     完全可能忘在 Create 里调用它（这就是原来那个洞的成因）；
//   - B3 的关键是「上传路径真的先判配额、再记用量」——函数各自单测通过，
//     也可能漏接线（先存后判、或者干脆没记）。

// uploadMultipart 发一次 multipart 上传（testClient.do 只发 JSON）。
func uploadMultipart(t *testing.T, r *gin.Engine, path, token, filename string, data []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return rec.Code, out
}

// fakeMP4 造一个「合法文件头 + 指定大小」的假 mp4。
//
// 用 mp4 而不是 png：图片会被 ProcessImage 解码再编码，落盘大小不可控；
// mp4 是透传分支，上传字节数 == 落盘字节数，用量断言才能精确到字节。
func fakeMP4(size int) []byte {
	head := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}
	buf := make([]byte, size)
	copy(buf, head)
	return buf
}

// TestCompanyCreateGrantsTrialLicense 开通企业 → 自动落一条试用授权行（B4 的核心）。
func TestCompanyCreateGrantsTrialLicense(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	code, body := c.do("POST", "/api/admin/companies", map[string]any{"name": "试用客户甲"}, admin)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])
	cid := int64(body["data"].(map[string]any)["id"].(float64))

	row, err := license.TenantLicenseOf(db, cid)
	require.NoError(t, err, "开通企业必须同时写入试用授权行，否则该租户回到「无约束」")
	require.Equal(t, "trial", row.Edition)
	require.Equal(t, 5, row.Seats)
	require.WithinDuration(t, time.Now().AddDate(0, 0, 14), row.ExpiresAt, time.Minute)

	// 后台能查到这条授权（界面据此显示剩余天数 / 席位）
	_, lb := c.do("GET", "/api/admin/companies/"+strconv.FormatInt(cid, 10)+"/license", nil, admin)
	require.EqualValues(t, 0, lb["code"])
	d := lb["data"].(map[string]any)
	require.Equal(t, true, d["exists"])
	require.EqualValues(t, 5, d["seats"])
	require.Equal(t, false, d["expired"])
	require.NotEmpty(t, d["expiresAt"])
	// 试用口径一并下发，界面才提示得出「新开通是 5 席 14 天」
	trial := d["trial"].(map[string]any)
	require.EqualValues(t, 14, trial["days"])
	require.EqualValues(t, 5, trial["seats"])
}

// TestCompanyLicensePut 后台改授权：转正（不限席位 / 不过期）、
// 延期、改席位；非法入参一律拒绝。
func TestCompanyLicensePut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/companies", map[string]any{"name": "试用客户乙"}, admin)
	cid := int64(body["data"].(map[string]any)["id"].(float64))
	path := "/api/admin/companies/" + strconv.FormatInt(cid, 10) + "/license"

	// 转正：不限席位 + 不过期（正式客户最常见的形态）
	_, body = c.do("PUT", path, map[string]any{
		"edition": "platform", "seats": 0, "expiresAt": "", "features": "",
	}, admin)
	require.EqualValues(t, 0, body["code"])
	row, err := license.TenantLicenseOf(db, cid)
	require.NoError(t, err)
	require.Equal(t, "platform", row.Edition)
	require.Equal(t, 0, row.Seats, "0 = 不限席位")
	require.True(t, row.ExpiresAt.IsZero(), "留空 = 不过期（库里为 NULL，不能是零值时间）")

	// 延期：日期取当天 23:59:59，客户理解的「到 12-31 到期」是这一天整天才算完
	_, body = c.do("PUT", path, map[string]any{
		"edition": "platform", "seats": 20, "expiresAt": "2026-12-31", "features": "mp_release",
	}, admin)
	require.EqualValues(t, 0, body["code"])
	row, err = license.TenantLicenseOf(db, cid)
	require.NoError(t, err)
	require.Equal(t, 20, row.Seats)
	require.Equal(t, "mp_release", row.Features)
	require.Equal(t, 2026, row.ExpiresAt.Year())
	require.Equal(t, 23, row.ExpiresAt.Hour())
	require.Equal(t, 59, row.ExpiresAt.Minute())

	// 审计留痕：改授权是计费动作，必须能追溯谁把到期日改成了哪天
	var na int64
	db.Model(&model.AuditLog{}).Where("action = ?", audit.ActionCompanyLicense).Count(&na)
	require.EqualValues(t, 2, na)

	// 非法入参逐条拒绝（坏参数不静默忽略）
	for _, bad := range []map[string]any{
		{"edition": "gold", "seats": 1, "expiresAt": ""},
		{"edition": "trial", "seats": -1, "expiresAt": ""},
		{"edition": "trial", "seats": 1, "expiresAt": "2026/12/31"},
	} {
		_, body = c.do("PUT", path, bad, admin)
		require.EqualValues(t, 1, body["code"], "非法入参必须拒绝：%v", bad)
	}
}

// TestUploadStorageQuota 上传累计用量 + 超额拒绝（B3 的核心）。
func TestUploadStorageQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	// 把全局配额压到 1MB（函数值注入，见 globalStorageQuotaMBFn 的注释）
	old := globalStorageQuotaMBFn
	globalStorageQuotaMBFn = func() int64 { return 1 }
	t.Cleanup(func() { globalStorageQuotaMBFn = old })

	const cid = 2 // 平台超管用 ?companyId= 指定租户
	// 600KB：低于 1MB 配额 → 放行并计量
	code, body := uploadMultipart(t, r, "/api/admin/upload?companyId=2", admin, "a.mp4", fakeMP4(600*1024))
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])

	row, err := storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 600*1024, row.Bytes)
	require.EqualValues(t, 1, row.Files)

	// 再传 600KB：累计 1.2MB > 1MB → 拒绝，且用量**不能**变（先判后存）
	_, body = uploadMultipart(t, r, "/api/admin/upload?companyId=2", admin, "b.mp4", fakeMP4(600*1024))
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "存储空间已用尽")
	row, err = storageUsageOf(db, cid)
	require.NoError(t, err)
	require.EqualValues(t, 600*1024, row.Bytes, "被拒绝的上传不能计入用量")
	require.EqualValues(t, 1, row.Files)

	// 用量与配额可查（percent 由余额算出来，界面直接画进度条）
	_, sb := c.do("GET", "/api/admin/storage?companyId=2", nil, admin)
	require.EqualValues(t, 0, sb["code"])
	d := sb["data"].(map[string]any)
	require.EqualValues(t, 600*1024, d["bytes"])
	require.EqualValues(t, 1024*1024, d["quotaBytes"])
	require.EqualValues(t, 58, d["percent"]) // 600/1024 ≈ 58%
	require.Equal(t, false, d["unlimited"])
}

// TestStorageQuotaOverride 给单个租户单独放大配额，不影响全局；改配额不清零已用量。
func TestStorageQuotaOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	old := globalStorageQuotaMBFn
	globalStorageQuotaMBFn = func() int64 { return 1 }
	t.Cleanup(func() { globalStorageQuotaMBFn = old })

	// 改配额要校验企业存在（防止给不存在的租户写配置），先建一家
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "丙公司", Status: "enabled"}).Error)

	// 先制造一点用量（10KB），验证「改配额不清用量」
	_, body := uploadMultipart(t, r, "/api/admin/upload?companyId=3", admin, "a.mp4", fakeMP4(10*1024))
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("PUT", "/api/admin/storage/quota", map[string]any{"companyId": 3, "quotaMb": 50}, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 50*bytesPerMB, storageQuotaBytesFor(db, 3), "租户覆盖应压过全局 1MB")

	row, err := storageUsageOf(db, 3)
	require.NoError(t, err)
	require.EqualValues(t, 10*1024, row.Bytes, "改配额不能丢掉已用量")

	// 审计留痕
	var na int64
	db.Model(&model.AuditLog{}).Where("action = ?", audit.ActionCompanyStorageQuota).Count(&na)
	require.EqualValues(t, 1, na)

	// 非法配额（负数 / 超过 1TB）拒绝
	_, body = c.do("PUT", "/api/admin/storage/quota", map[string]any{"companyId": 3, "quotaMb": -1}, admin)
	require.EqualValues(t, 1, body["code"])
	_, body = c.do("PUT", "/api/admin/storage/quota", map[string]any{"companyId": 3, "quotaMb": 2 * 1024 * 1024}, admin)
	require.EqualValues(t, 1, body["code"])
}
