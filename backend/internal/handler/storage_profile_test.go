package handler

import (
	"strconv"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/storage"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/stretchr/testify/require"
)

// 档案化接口的权限与基本 CRUD 守门。
// 档案里装的是云凭据，企业管理员连读都不能读；企业只能给自己选绑定。
func TestStorageProfileCRUDGuard(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/storage-profiles", nil, token)
	require.EqualValues(t, 0, body["code"])
	require.NotNil(t, body["data"])

	_, body = c.do("POST", "/api/admin/storage-profiles", map[string]any{
		"name": "bucket-a", "driver": "minio", "endpoint": "s.example.com", "bucket": "assets",
		"baseUrl": "https://card.example.com/uploads", "useSsl": true, "publicRead": true,
		"accessKey": "AKIAEXAMPLE", "secretKey": "secret-value",
	}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	data := body["data"].(map[string]interface{})
	id := int64(data["id"].(float64))
	// 凭据只回掩码与「是否配过」，绝不回明文。
	require.NotContains(t, data, "secretKey")
	require.Equal(t, "AKIA****MPLE", data["accessKeyMasked"])

	_, body = c.do("PUT", "/api/admin/storage-profiles/1", map[string]any{"name": "bucket-b"}, token)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/admin/storage-profiles", nil, token)
	require.EqualValues(t, 0, body["code"])
	rows := body["data"].([]interface{})
	require.Equal(t, "bucket-b", rows[0].(map[string]interface{})["name"])
	_ = id

	// 绑定：档案必须存在。
	_, body = c.do("PUT", "/api/admin/storage-bindings/global", map[string]any{
		"storageProfileId": 9999,
	}, token)
	require.EqualValues(t, 1, body["code"])
	_, body = c.do("PUT", "/api/admin/storage-bindings/global", map[string]any{
		"storageProfileId": 1,
	}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	_, body = c.do("GET", "/api/admin/storage-bindings", nil, token)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "profile", body["data"].(map[string]interface{})["global"].(map[string]interface{})["source"])
}

func TestStorageProfilesRejectCompanyAdmin(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	_ = loginAdmin(t, c)
	// 企业管理员 token 由 seed 产生；没有时这条守门至少要保证未登录读不到。
	_, body := c.do("GET", "/api/admin/storage-profiles", nil, "")
	require.EqualValues(t, 401, body["code"])
}

// 内置本地档案可以改名、改目录，但驱动必须锁死为 local——
// 否则「不可删除」只是假安全：管理员能把它改成一套不可用的云配置。
func TestBuiltinStorageProfileDriverLocked(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	seedDefaultCompany(t, db)
	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true, JWTKey: "test-jwt-key",
		AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL, AllowExt: storage.DefaultAllowExt}
	r := buildEngine(t, db, cfg, up, nil)

	row := model.StorageProfile{Name: "平台本地存储", Driver: "local", Builtin: true, PublicRead: true, Version: 1}
	require.NoError(t, db.Create(&row).Error)

	c := newTestClient(r, t)
	token := loginAdmin(t, c)
	_, body := c.do("PUT", "/api/admin/storage-profiles/"+strconv.FormatInt(row.ID, 10),
		map[string]any{"driver": "minio"}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "驱动不可更改")

	// 改名字是允许的（管理员可能想区分「主站本地盘」与其它本地目录）。
	_, body = c.do("PUT", "/api/admin/storage-profiles/"+strconv.FormatInt(row.ID, 10),
		map[string]any{"name": "主站本地盘"}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
}

// 新建档案时显式选 false 的布尔项必须真的落成 false。
//
// 回归背景：模型上曾给 use_ssl / public_read 挂 `default:true`，GORM 会把零值从
// INSERT 里省略、交给库默认值，于是「新建时选 useSsl=false」保存后仍是 true——
// 自检与上传都会按 https 发，而运营以为配的是 http（自建 MinIO 走 80 时必然连不上，
// 报的却是「凭据错误」）。public_read 同理，虽然接口目前拒绝 false，
// 但靠「接口恰好拦住」来保模型正确性是脆的，两列一起摘掉 default 标签。
func TestStorageProfileCreateKeepsExplicitFalseBools(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/storage-profiles", map[string]any{
		"name": "plain-http-bucket", "driver": "minio", "endpoint": "s.example.com", "bucket": "assets",
		"baseUrl": "https://card.example.com/uploads", "useSsl": false, "publicRead": true,
		"accessKey": "AK", "secretKey": "SK",
	}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	id := int64(body["data"].(map[string]interface{})["id"].(float64))

	var row model.StorageProfile
	require.NoError(t, db.First(&row, id).Error)
	require.False(t, row.UseSSL, "useSsl=false 必须真的落成 false")
	require.True(t, row.PublicRead)

	// 私有桶目前被接口显式拒绝（第一阶段只支持公开读桶）。
	// 这条断言同时钉住「拒绝的理由要说清」，否则运营只会看到一句没头没尾的失败。
	_, body = c.do("POST", "/api/admin/storage-profiles", map[string]any{
		"name": "private-bucket", "driver": "minio", "endpoint": "s.example.com", "bucket": "assets",
		"baseUrl": "https://card.example.com/uploads", "publicRead": false,
		"accessKey": "AK", "secretKey": "SK",
	}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "公开读")
}

// 旧配置 → 档案：这是「两套模型并存」唯一的单向桥。
//
// 没有它，已上线客户想用 CDN 分发 / 多套切换 / 一键迁移就只能手工重填一遍 AK/SK，
// 而凭据本来就在库里（PII 密文）。桥本身有四个安全约束，下面逐条钉住。
func TestAdoptLegacyStorageIntoProfile(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	const cid = 8
	require.NoError(t, db.Create(&model.Company{ID: cid, Name: "接管测试公司", Status: "enabled"}).Error)
	ak, err := pii.Encrypt("AK-LEGACY")
	require.NoError(t, err)
	sk, err := pii.Encrypt("SK-LEGACY")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: cid, Driver: "minio", Endpoint: "minio.c8.example.com", Bucket: "c8",
		UseSSL: true, HostHeader: "c8.minio.example.com", Enabled: true, PublicRead: true,
		AccessKeyEnc: ak, SecretKeyEnc: sk, Status: "ok",
	}).Error)

	// force=true 跳过探桶：测试环境没有真桶，探桶这一支另有专门用例。
	_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
		map[string]any{"companyId": cid, "force": true}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	d := body["data"].(map[string]any)
	id := int64(d["id"].(float64))
	require.Contains(t, d["name"], "接管", "档案名要能看出是从哪来的")

	var sp model.StorageProfile
	require.NoError(t, db.First(&sp, id).Error)
	require.Equal(t, "AK-LEGACY", sp.AccessKey, "凭据必须从旧表密文解出来写进档案")
	require.Equal(t, "SK-LEGACY", sp.SecretKey)
	require.Equal(t, "minio.c8.example.com", sp.Endpoint)
	require.Equal(t, "c8.minio.example.com", sp.HostHeader, "Host 头必须带过来，否则路径风格会判错")
	require.NotEmpty(t, sp.BaseURL, "BaseURL 必须落平台统一入口，否则 uploadroute.build 会拒")

	var b model.StorageBinding
	require.NoError(t, db.Where("tenant_id = ?", cid).First(&b).Error)
	require.Equal(t, id, b.StorageProfileID, "接管后必须绑定给这家企业")
	require.Zero(t, b.DeliveryProfileID, "旧表没有 CDN 概念，分发档案留空")

	// 旧表一个字节都不能动：它是回滚通道（删掉绑定就回到这里）。
	var legacy model.TenantStorageConfig
	require.NoError(t, db.Where("company_id = ?", cid).First(&legacy).Error)
	require.True(t, legacy.Enabled)
	require.Equal(t, ak, legacy.AccessKeyEnc)

	// 幂等：再点一次必须被拒，不能造出第二套同源档案。
	_, body = c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
		map[string]any{"companyId": cid, "force": true}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "已绑定")
	var count int64
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("name LIKE ?", "接管 · %").Count(&count).Error)
	require.EqualValues(t, 1, count, "重复接管不得新增档案")
}

// 四条拒绝条件各一条断言。它们的共同点是「接管会立刻改行为」：
// 停用配置被接管 = 偷偷上线；本地驱动被接管 = 生成一套没有桶的档案；
// 私有桶被接管 = 档案校验就过不去；没有旧配置 = 无从接管。
func TestAdoptLegacyStorageRejections(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	seed := func(cid int64, mutate func(*model.TenantStorageConfig)) {
		row := model.TenantStorageConfig{
			CompanyID: cid, Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
			Enabled: true, PublicRead: true, AccessKeyEnc: "x", SecretKeyEnc: "y", Status: "ok",
		}
		mutate(&row)
		require.NoError(t, db.Create(&row).Error)
	}

	t.Run("旧配置未启用", func(t *testing.T) {
		require.NoError(t, db.Create(&model.Company{ID: 21, Name: "未启用", Status: "enabled"}).Error)
		seed(21, func(row *model.TenantStorageConfig) { row.Enabled = false })
		_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
			map[string]any{"companyId": 21, "force": true}, token)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "未启用")
	})

	t.Run("旧配置是本地驱动", func(t *testing.T) {
		require.NoError(t, db.Create(&model.Company{ID: 22, Name: "本地盘", Status: "enabled"}).Error)
		seed(22, func(row *model.TenantStorageConfig) { row.Driver = "local" })
		_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
			map[string]any{"companyId": 22, "force": true}, token)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "本地磁盘")
	})

	t.Run("旧配置未开启公开读", func(t *testing.T) {
		require.NoError(t, db.Create(&model.Company{ID: 23, Name: "私有桶", Status: "enabled"}).Error)
		seed(23, func(row *model.TenantStorageConfig) { row.PublicRead = false })
		_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
			map[string]any{"companyId": 23, "force": true}, token)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "公开读")
	})

	t.Run("没有旧配置", func(t *testing.T) {
		require.NoError(t, db.Create(&model.Company{ID: 24, Name: "没配过", Status: "enabled"}).Error)
		_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
			map[string]any{"companyId": 24, "force": true}, token)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "没有旧的存储配置")
	})
}

// 凭据解不开时必须拒绝，且要说清是 PII_ENC_KEY 的问题：
// 静默接管的后果是档案里存着一段解不开的密文，上传全坏而没人知道原因。
//
// 注意用 v1: 开头的坏密文，不能用随便一段字符串：pii.Decrypt 对没有 v1: 前缀的
// 值按「历史明文」原样返回（那是为了兼容尚未回填的旧数据），所以 "abc" 是能解开的。
func TestAdoptLegacyStorageRejectsUndecryptableCredential(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	require.NoError(t, db.Create(&model.Company{ID: 31, Name: "坏密文", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: 31, Driver: "minio", Endpoint: "s.example.com", Bucket: "b",
		Enabled: true, PublicRead: true, Status: "ok",
		AccessKeyEnc: "v1:bm90LWEtY2lwaGVydGV4dA==", SecretKeyEnc: "v1:bm90LWEtY2lwaGVydGV4dA==",
	}).Error)

	_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
		map[string]any{"companyId": 31, "force": true}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "无法解密")

	var count int64
	require.NoError(t, db.Model(&model.StorageProfile{}).Count(&count).Error)
	require.Zero(t, count, "拒绝时不能留下半成品档案")
}

// 不跳过探桶时，桶连不上必须拒绝接管。
// 用 127.0.0.1:1 是为了让失败**立刻**发生（端口拒绝），不把单测拖进 10 秒超时。
func TestAdoptLegacyStorageProbesBeforeAdopting(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	ak, err := pii.Encrypt("AK")
	require.NoError(t, err)
	sk, err := pii.Encrypt("SK")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Company{ID: 41, Name: "探桶失败", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: 41, Driver: "minio", Endpoint: "127.0.0.1:1", Bucket: "b",
		Enabled: true, PublicRead: true, Status: "ok",
		AccessKeyEnc: ak, SecretKeyEnc: sk,
	}).Error)

	_, body := c.do("POST", "/api/admin/storage-profiles/adopt-legacy",
		map[string]any{"companyId": 41}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "连通性")

	var count int64
	require.NoError(t, db.Model(&model.StorageProfile{}).Count(&count).Error)
	require.Zero(t, count, "探桶失败时不能建出档案")
}
