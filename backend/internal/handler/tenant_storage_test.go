package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"weichuan/internal/auth"
	"weichuan/internal/model"
	"weichuan/internal/pii"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 租户自有对象桶配置接口的集成测试。
//
// 这一轮（P1 后半段）之前，tenant_storage_configs 只能手工 SQL 写入——
// 也就是说「只有平台超管能改」这条权限模型还从未被真正执行过。
// 本文件的重点不是 CRUD 能不能跑通，而是三件事：
//   1. 企业管理员拿着接口地址硬调会拿到 403，且配置表一个字节都不变（R9）；
//   2. 凭据只以密文落库、出参只有掩码，响应体里搜不到明文（PII）；
//   3. 改了配置就把上一次自检的结论作废，不让过期的绿点继续骗人。

const (
	testStorageAK = "AKIAIOSFODNN7EXAMPLE"
	testStorageSK = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

// stubStorageProbe 替换包级探测函数，让测试不联网。
//
// 与 urlProbeFn 同一套路：探测是唯一会**按库里配置发起出向请求**的地方，
// 集成测试若真去连 play.min.io，会变成「有网就绿、没网就红」的不稳定用例。
func stubStorageProbe(t *testing.T, fn func(driver, endpoint, ak, sk, bucket, region string, useSSL bool) (string, error)) {
	t.Helper()
	old := storageProbeFn
	storageProbeFn = fn
	t.Cleanup(func() { storageProbeFn = old })
}

func stubStorageProbeOK(t *testing.T) {
	t.Helper()
	stubStorageProbe(t, func(driver, endpoint, ak, sk, bucket, region string, useSSL bool) (string, error) {
		return "凭据有效，桶 " + bucket + " 可达（测试桩）", nil
	})
}

// loginTenantAdmin 造一个企业管理员并登录，返回令牌（companyId=1，role=admin）。
//
// 必须用**真企业管理员**而不是员工令牌：PlatformAdminMiddleware 拦的是
// 「role=admin 但 companyId != 0」，员工令牌在 authenticate 阶段就被挡了，
// 用它测出来的 403 证明不了 R9 那条风险（企业管理员也是 admin）。
func loginTenantAdmin(t *testing.T, c *testClient, db *gorm.DB) string {
	t.Helper()
	hash, err := auth.HashPassword("entpass123")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: "entboss", PasswordHash: hash,
	}).Error)
	_, body := c.do("POST", "/api/admin/login/mobile", map[string]interface{}{
		"username": "entboss", "password": "entpass123",
	}, "")
	require.EqualValues(t, 0, body["code"], "企业管理员登录应成功：%v", body["msg"])
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	return token
}

// doStorageOK 发一次请求并要求 code:0，返回整个信封。
//
// 单独抽出来是因为这一组用例里绝大多数请求都期望成功，逐处写
// `_, body := c.do(...)` + `require.EqualValues(t, 0, body["code"])` 会把
// 真正要断言的字段淹掉。
func doStorageOK(t *testing.T, c *testClient, method, path string, body interface{}, token string) map[string]interface{} {
	t.Helper()
	_, env := c.do(method, path, body, token)
	require.EqualValues(t, 0, env["code"], "%s %s 应成功：%v", method, path, env["msg"])
	return env
}

// doStorage 在 doStorageOK 之上再要求信封里有 data 对象，并把它取出来。
// DELETE 只有 {code,msg}，那条走 doStorageOK。
func doStorage(t *testing.T, c *testClient, method, path string, body interface{}, token string) map[string]interface{} {
	t.Helper()
	env := doStorageOK(t, c, method, path, body, token)
	data, ok := env["data"].(map[string]interface{})
	require.True(t, ok, "%s %s 的 data 应为对象：%v", method, path, env)
	return data
}

func storagePayload() map[string]interface{} {
	return map[string]interface{}{
		"driver":     "minio",
		"endpoint":   "play.min.io",
		"bucket":     "wc-demo",
		"region":     "",
		"useSsl":     true,
		"hostHeader": "wc-demo.play.min.io",
		"enabled":    true,
		"publicRead": true,
		"accessKey":  testStorageAK,
		"secretKey":  testStorageSK,
	}
}

func jsonText(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func loadStorageRow(t *testing.T, db *gorm.DB, cid int64) model.TenantStorageConfig {
	t.Helper()
	var row model.TenantStorageConfig
	require.NoError(t, db.Where("company_id = ?", cid).First(&row).Error)
	return row
}

// TestTenantStorageSuperAdminLifecycle 走一遍「没配 → 新建 → 掩码回显 → 自检 → 切回平台」。
func TestTenantStorageSuperAdminLifecycle(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	stubStorageProbeOK(t)

	const path = "/api/admin/companies/1/storage"

	// 1) 没配过：正常状态，不是错误。界面靠 configured=false 进「新建」态。
	data := doStorage(t, c, "GET", path, nil, plat)
	require.Equal(t, false, data["configured"])
	require.Equal(t, "local", data["driver"])
	require.Equal(t, false, data["enabled"])
	require.Equal(t, storageStatusUnknown, data["status"])
	// 默认（未设 TENANT_STORAGE_ROUTE）配置不承载流量，界面必须显示这句话，
	// 否则超管填完会以为已生效。开关两态由 TestTrafficEnabledFollowsRouteSwitch 覆盖。
	require.Equal(t, false, data["trafficEnabled"])

	// 2) 新建：启用 + 凭据齐全
	data = doStorage(t, c, "PUT", path, storagePayload(), plat)
	require.Equal(t, true, data["configured"])
	require.Equal(t, "minio", data["driver"])
	require.Equal(t, "wc-demo", data["bucket"])

	// 库里是密文，不是明文
	row := loadStorageRow(t, db, 1)
	require.True(t, strings.HasPrefix(row.AccessKeyEnc, "v1:"), "凭据必须是 pii.Encrypt 的密文")
	require.NotContains(t, row.AccessKeyEnc, testStorageAK)
	ak, err := pii.Decrypt(row.AccessKeyEnc)
	require.NoError(t, err)
	require.Equal(t, testStorageAK, ak)

	// 3) 回显：只有掩码，响应体里搜不到明文
	_, env := c.do("GET", path, nil, plat)
	require.EqualValues(t, 0, env["code"])
	data = env["data"].(map[string]interface{})
	require.Equal(t, "AKIA****MPLE", data["accessKeyMasked"])
	require.Equal(t, true, data["accessKeyConfigured"])
	require.Equal(t, true, data["secretKeyConfigured"])
	require.NotContains(t, data, "secretKeyMasked", "SecretKey 连掩码都不给")
	full := jsonText(t, env)
	require.NotContains(t, full, testStorageAK, "响应体绝不能出现 AccessKey 明文")
	require.NotContains(t, full, testStorageSK, "响应体绝不能出现 SecretKey 明文")

	// 4) 自检成功：status=ok + 时间戳落库
	data = doStorage(t, c, "POST", path+"/check", nil, plat)
	require.Equal(t, true, data["ok"])
	require.Equal(t, storageStatusOK, data["status"])
	require.NotEmpty(t, data["checkedScope"], "必须说清自检验了什么、没验什么")
	row = loadStorageRow(t, db, 1)
	require.Equal(t, storageStatusOK, row.Status)
	require.NotNil(t, row.LastCheckedAt)

	// 5) 改配置 → 自检结论作废（否则后台会一直显示上一次的绿点）
	payload := storagePayload()
	payload["bucket"] = "wc-demo-2"
	doStorage(t, c, "PUT", path, payload, plat)
	row = loadStorageRow(t, db, 1)
	require.Equal(t, storageStatusUnknown, row.Status, "改了配置，上一次自检的结论就不再代表当前配置")
	require.Empty(t, row.LastError)

	// 6) 切回平台存储：删行
	doStorageOK(t, c, "DELETE", path, nil, plat)
	require.Error(t, db.Where("company_id = ?", 1).First(&model.TenantStorageConfig{}).Error,
		"切回平台存储应删掉这一行")
	data = doStorage(t, c, "GET", path, nil, plat)
	require.Equal(t, false, data["configured"])

	// 7) 幂等：再删一次不报错（已经是平台存储了）
	doStorageOK(t, c, "DELETE", path, nil, plat)
}

// TestTenantStorageTenantAdminForbidden 是 R9 的运行时证据：
// 企业管理员四个接口全 403，且配置表一个字节都不变。
func TestTenantStorageTenantAdminForbidden(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	ent := loginTenantAdmin(t, c, db)
	stubStorageProbeOK(t)

	const path = "/api/admin/companies/1/storage"
	doStorage(t, c, "PUT", path, storagePayload(), plat)

	before := loadStorageRow(t, db, 1)

	// 企业管理员硬调四个接口：全部 403。注意 role 是 admin，只是 companyId != 0，
	// 所以 AuthMiddleware(db,"admin") 会放行——这正是必须用 PlatformAdminMiddleware 的原因。
	cases := []struct {
		method, p string
		body      interface{}
	}{
		{"GET", path, nil},
		{"PUT", path, storagePayload()},
		{"DELETE", path, nil},
		{"POST", path + "/check", nil},
	}
	for _, tc := range cases {
		_, body := c.do(tc.method, tc.p, tc.body, ent)
		require.EqualValues(t, 403, body["code"],
			"%s %s 对企业管理员必须是 403，实际：%v", tc.method, tc.p, body)
	}

	var after model.TenantStorageConfig
	require.NoError(t, db.Where("company_id = ?", 1).First(&after).Error,
		"越权 DELETE 不该真把行删掉")
	require.Equal(t, before.Endpoint, after.Endpoint)
	require.Equal(t, before.Bucket, after.Bucket)
	require.Equal(t, before.AccessKeyEnc, after.AccessKeyEnc)
	require.Equal(t, before.SecretKeyEnc, after.SecretKeyEnc)
	require.Equal(t, before.Enabled, after.Enabled)
	require.Equal(t, before.Status, after.Status, "越权自检不该回写 status")
}

// TestTenantStorageCredentialTriState 钉住凭据三态：不传=保留、空串=清空、有值=覆盖。
//
// 三态而不是两态的理由：界面回显的是掩码，超管「只看不改点保存」时提交的是不带
// accessKey 字段的请求。若把「不传」当成「清空」，一次改 bucket 的操作会把凭据抹掉，
// 而那一刻接口返回 code:0——配置看上去保存成功，上传时才全线签名失败。
func TestTenantStorageCredentialTriState(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	stubStorageProbeOK(t)

	const path = "/api/admin/companies/1/storage"
	doStorage(t, c, "PUT", path, storagePayload(), plat)
	originalCipher := loadStorageRow(t, db, 1).AccessKeyEnc

	// ① 不传凭据字段 → 保留原值（密文都不重新生成）
	doStorage(t, c, "PUT", path, map[string]interface{}{
		"driver": "minio", "endpoint": "play.min.io", "bucket": "wc-changed",
		"enabled": true, "publicRead": true,
	}, plat)
	row := loadStorageRow(t, db, 1)
	require.Equal(t, "wc-changed", row.Bucket)
	require.Equal(t, originalCipher, row.AccessKeyEnc, "没传凭据就不该动它")

	// ② 空串 → 清空。必须先停用：启用状态下凭据不能为空（见下面 ③），
	//    这个顺序本身也是运维的真实操作顺序（先停、再清、再换）。
	doStorage(t, c, "PUT", path, map[string]interface{}{
		"driver": "minio", "endpoint": "play.min.io", "bucket": "wc-changed",
		"enabled": false, "accessKey": "", "secretKey": "",
	}, plat)
	row = loadStorageRow(t, db, 1)
	require.Empty(t, row.AccessKeyEnc)
	require.Empty(t, row.SecretKeyEnc)

	// ③ 没有凭据却要启用 → 拒。放行会得到一个「配置成功、切量那天全失败」的桶。
	_, env := c.do("PUT", path, map[string]interface{}{
		"driver": "minio", "endpoint": "play.min.io", "bucket": "wc-changed", "enabled": true,
	}, plat)
	require.EqualValues(t, 1, env["code"])
	require.Contains(t, env["msg"], "AccessKey")

	// ④ 重新填凭据 → 覆盖
	doStorage(t, c, "PUT", path, map[string]interface{}{
		"driver": "minio", "endpoint": "play.min.io", "bucket": "wc-changed",
		"enabled": true, "accessKey": "AKIAZZZZZZZZZZZZZZZZ", "secretKey": "newsecret",
	}, plat)
	row = loadStorageRow(t, db, 1)
	ak, err := pii.Decrypt(row.AccessKeyEnc)
	require.NoError(t, err)
	require.Equal(t, "AKIAZZZZZZZZZZZZZZZZ", ak)
	require.NotEqual(t, originalCipher, row.AccessKeyEnc, "覆盖必须重新加密（随机 nonce）")
}

// TestTenantStorageValidation 钉住写入侧的拒绝条件。每一条都对应一个
// 「接口说存成功了、上线才出事」的形态。
func TestTenantStorageValidation(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	stubStorageProbeOK(t)

	const path = "/api/admin/companies/1/storage"
	put := func(mut func(map[string]interface{})) map[string]interface{} {
		p := storagePayload()
		if mut != nil {
			mut(p)
		}
		_, env := c.do("PUT", path, p, plat)
		return env
	}

	// endpoint 带 scheme：控制台里粘 URL 是最常见的填错方式，会让 snippet 拼出
	// http://https://xxx。校验来自 nginxsnippet.Target.Validate（单点，不在这里重写）。
	env := put(func(p map[string]interface{}) { p["endpoint"] = "https://play.min.io" })
	require.EqualValues(t, 1, env["code"])
	require.Contains(t, env["msg"], "scheme")

	// driver 不在枚举内
	env = put(func(p map[string]interface{}) { p["driver"] = "ftp" })
	require.EqualValues(t, 1, env["code"])
	require.Contains(t, env["msg"], "枚举")

	// 私有桶：本期没有预签名链路，放行等于上线裂图
	env = put(func(p map[string]interface{}) { p["publicRead"] = false })
	require.EqualValues(t, 1, env["code"])
	require.Contains(t, env["msg"], "公开读")

	// bucket 含路径分隔符
	env = put(func(p map[string]interface{}) { p["bucket"] = "wc/demo" })
	require.EqualValues(t, 1, env["code"])

	// 企业不存在：配给一个不存在的 id 只会产出一段永远匹配不到的 location
	_, env = c.do("PUT", "/api/admin/companies/999/storage", storagePayload(), plat)
	require.EqualValues(t, 1, env["code"])
	require.Contains(t, env["msg"], "企业不存在")

	// company id 为 0：0 表示平台自身，平台那份走 sys_setting，不该有这一行
	_, env = c.do("PUT", "/api/admin/companies/0/storage", storagePayload(), plat)
	require.EqualValues(t, 1, env["code"])

	// 以上全部被拒 → 表里仍然只有 0 行
	var n int64
	require.NoError(t, db.Model(&model.TenantStorageConfig{}).Count(&n).Error)
	require.Zero(t, n, "被拒的请求不该留下任何行")
}

// TestTenantStorageCheckFailure 自检失败要把可读原因写进 last_error，
// 且这条原因不能把凭据带回来。
func TestTenantStorageCheckFailure(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	stubStorageProbe(t, func(driver, endpoint, ak, sk, bucket, region string, useSSL bool) (string, error) {
		// 模拟真实 SDK 的拒绝：文案里故意带上凭据片段，验证它不会进库/进响应
		return "", &probeTestError{"凭据被拒绝（AccessDenied）——检查 AccessKey " + ak + " 是否属于这个桶的账号"}
	})

	const path = "/api/admin/companies/1/storage"
	doStorage(t, c, "PUT", path, storagePayload(), plat)

	_, env := c.do("POST", path+"/check", nil, plat)
	require.EqualValues(t, 0, env["code"])
	data := env["data"].(map[string]interface{})
	require.Equal(t, false, data["ok"])
	require.Equal(t, storageStatusError, data["status"])
	require.NotEmpty(t, data["error"])

	row := loadStorageRow(t, db, 1)
	require.Equal(t, storageStatusError, row.Status)
	require.NotEmpty(t, row.LastError)
	// 这一条真正守住的是「探测函数自己脱敏」：本用例的桩故意把 AK 拼进错误文案，
	// 如果 handler 原样透传，它就会常驻后台的 last_error 里。
	// 真实实现里 storage.ProbeBucket 只回我们自己的文案，不拼 SDK 原文。
	require.NotContains(t, jsonText(t, env), testStorageSK, "响应体绝不能出现 SecretKey")
}

// probeTestError 让测试能精确控制自检失败的文案。
type probeTestError struct{ msg string }

func (e *probeTestError) Error() string { return e.msg }

// TestTrafficEnabledFollowsRouteSwitch 钉住「出参如实回报开关」。
//
// 这个字段是给超管看的唯一一句「到底生不生效」。写死 false 会在开关打开后继续
// 显示「尚未承载流量」（填对了也以为没生效）；写死 true 是更坏的方向（以为生效、
// 实际文件全在平台盘上）。所以它必须读同一个判定源：uploadroute.RouteEnabled()。
func TestTrafficEnabledFollowsRouteSwitch(t *testing.T) {
	row := &model.TenantStorageConfig{CompanyID: 1, Driver: "minio", Enabled: true}

	t.Setenv("TENANT_STORAGE_ROUTE", "")
	require.Equal(t, false, tenantStorageView(row)["trafficEnabled"], "默认 off")
	require.Equal(t, false, emptyTenantStorageView(1)["trafficEnabled"], "没配过的企业也要同一个口径")

	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	require.Equal(t, true, tenantStorageView(row)["trafficEnabled"], "开关打开后必须如实回报")
	require.Equal(t, true, emptyTenantStorageView(1)["trafficEnabled"], "没配过的企业也要同一个口径")
}

// TestActiveForTenantReasons 钉住「这家到底在不在用自有桶」的五种未生效成因。
//
// 只回一个 false 是不够的：五种成因的处置方式完全不同（去填 / 设计如此 / 改驱动 /
// 找运维开开关 / 补凭据），超管拿到 false 只能猜。所以每一条都要能单独断言到。
func TestActiveForTenantReasons(t *testing.T) {
	full := func(mu func(*model.TenantStorageConfig)) *model.TenantStorageConfig {
		row := &model.TenantStorageConfig{
			CompanyID: 7, Driver: "minio", Endpoint: "127.0.0.1:9000", Bucket: "b",
			Enabled: true, PublicRead: true,
			AccessKeyEnc: "v1:x", SecretKeyEnc: "v1:y",
		}
		if mu != nil {
			mu(row)
		}
		return row
	}

	t.Setenv("TENANT_STORAGE_ROUTE", "on")
	ok, why := tenantStorageActive(full(nil))
	require.True(t, ok)
	require.Empty(t, why, "生效时不该有理由文案")

	cases := []struct {
		name string
		row  *model.TenantStorageConfig
		want string
	}{
		{"没配过", nil, "尚未配置"},
		{"未启用", full(func(r *model.TenantStorageConfig) { r.Enabled = false }), "配置未启用"},
		{"驱动是本地", full(func(r *model.TenantStorageConfig) { r.Driver = "local" }), "驱动为本地磁盘"},
		{"凭据不完整", full(func(r *model.TenantStorageConfig) { r.AccessKeyEnc = "" }), "凭据不完整"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, why := tenantStorageActive(tc.row)
			require.False(t, ok)
			require.Contains(t, why, tc.want)
		})
	}

	// 路由关掉时，其余条件再齐也不算生效——否则界面会在关着开关的部署上
	// 显示「正在使用自有桶」，而文件其实全在平台盘上。
	t.Run("路由未开", func(t *testing.T) {
		t.Setenv("TENANT_STORAGE_ROUTE", "off")
		ok, why := tenantStorageActive(full(nil))
		require.False(t, ok)
		require.Contains(t, why, "TENANT_STORAGE_ROUTE")
	})
}
