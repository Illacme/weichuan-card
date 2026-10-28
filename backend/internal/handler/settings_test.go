package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/setting"

	"github.com/stretchr/testify/require"
)

// TestSettingsAPI 覆盖系统设置接口：平台超管读取/更新、类型校验、未知 key 拒绝、公开接口白名单。
func TestSettingsAPI(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1) 平台超管读取全部可配置项：含分组与默认值
	code, body := c.do("GET", "/api/admin/settings", nil, adminToken)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])
	list, ok := body["data"].([]interface{})
	require.True(t, ok, "data 应为设置项数组")
	require.NotEmpty(t, list)

	// 找到 brand_name 与 upload_max_mb 的默认值（应保持 registry 缺省）
	var brandDef, uploadDef string
	for _, it := range list {
		m := it.(map[string]interface{})
		if m["key"] == "brand_name" {
			brandDef = m["value"].(string)
		}
		if m["key"] == "upload_max_mb" {
			uploadDef = m["value"].(string)
		}
	}
	require.Equal(t, "微传名片", brandDef)
	require.Equal(t, "10", uploadDef)

	// 2) 批量更新：改品牌名 / 上传上限 / 指标开关 → 成功
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{
		"brand_name":      "朗汇名片",
		"upload_max_mb":   "20",
		"metrics_enabled": "true",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 3) 再次读取：值已更新（即时生效，无需重启）
	_, body = c.do("GET", "/api/admin/settings", nil, adminToken)
	list = body["data"].([]interface{})
	var brandNew, uploadNew, metricsNew string
	for _, it := range list {
		m := it.(map[string]interface{})
		switch m["key"] {
		case "brand_name":
			brandNew = m["value"].(string)
		case "upload_max_mb":
			uploadNew = m["value"].(string)
		case "metrics_enabled":
			metricsNew = m["value"].(string)
		}
	}
	require.Equal(t, "朗汇名片", brandNew)
	require.Equal(t, "20", uploadNew)
	require.Equal(t, "true", metricsNew)

	// 4) 类型校验：upload_max_mb 传非整数 → code:1
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{
		"upload_max_mb": "abc",
	}, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 5) 未知 key → code:1
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{
		"not_a_real_key": "x",
	}, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 6) 企业管理员（非平台超管）访问 → 403（PlatformAdminMiddleware 兜底）
	//    用开通的乙公司管理员登录后被拒；这里直接以无平台权限的 Bearer 访问验证中间件：
	//    用员工 token（非 admin）访问应被 authenticate 拦为 401/403，均非 0。
	empToken := loginEmployee(t, c) // 见下辅助：建员工并登录
	_, body = c.do("GET", "/api/admin/settings", nil, empToken)
	require.NotEqual(t, 0, body["code"])

	// 7) 公开接口：仅含非敏感项（brand_name），不含 wx_appid
	code, body = c.do("GET", "/api/public/settings", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])
	pub := body["data"].(map[string]interface{})
	require.Equal(t, "朗汇名片", pub["brand_name"])
	_, hasAppID := pub["wx_appid"]
	require.False(t, hasAppID, "wx_appid 为敏感项，不应出现在公开接口")
}

// TestWXAppIDSettingOverridesEnv wx_appid 接线验证。
// 历史缺陷：该项曾在「系统设置」界面可见可存，但代码里**从不读取**（死配置，改了没用）。
// 本测试把语义钉死：设置非空 → 以设置为准；设置为空 → 回退环境变量 WX_APPID。
// （不能写成「设置项存在就覆盖」：seed 会把它写成空串，那样会让只配环境变量的部署凭空失效。）
func TestWXAppIDSettingOverridesEnv(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	cfg := config.Config{WXAppID: "env-appid"}
	// 1) seed 缺省为空串 → 回退环境变量
	require.Equal(t, "env-appid", resolveWXAppID(cfg))

	// 2) 后台设置 AppID → 覆盖环境变量，且无需重启
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"wx_appid": "ui-appid"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "ui-appid", resolveWXAppID(cfg))

	// 3) 清空设置 → 重新回退环境变量
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"wx_appid": ""}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "env-appid", resolveWXAppID(cfg))
}

// TestMetricsEnabledSettingFallsBackToEnv metrics_enabled 接线验证（同为曾经的死配置项）。
// 缺省空串 =「未设置」→ 回退环境变量 METRICS_ENABLED；界面显式设置后以界面为准。
func TestMetricsEnabledSettingFallsBackToEnv(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1) 未设置：空串视同缺失，回退传入的（环境变量）默认值
	require.False(t, setting.GetBool("metrics_enabled", false))
	require.True(t, setting.GetBool("metrics_enabled", true))

	// 2) 界面显式开启 → 环境变量为 false 也应以界面为准
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"metrics_enabled": "true"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.True(t, setting.GetBool("metrics_enabled", false))

	// 3) 界面显式关闭 → 环境变量为 true 也应以界面为准
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"metrics_enabled": "false"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.False(t, setting.GetBool("metrics_enabled", true))
}

// TestIndependentLockMakesCompaniesReadOnly independent_lock 接线验证（同为曾经的死配置项）。
// 语义：开启后企业管理页整体只读——列表 / 管理员列表照常可查，
// 新建企业、编辑企业、启停企业、新建企业管理员一律 403。
func TestIndependentLockMakesCompaniesReadOnly(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c) // 企管接口走 Cookie 鉴权：登录即写入 jar

	// 1) 未锁定：可正常新建企业
	_, body := c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{"name": "甲公司"})
	require.EqualValues(t, 0, body["code"])
	cid := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	// 2) 开启独立版锁定
	_, body = c.doWithCookie("PUT", "/api/admin/settings", map[string]string{"independent_lock": "true"})
	require.EqualValues(t, 0, body["code"])

	// 3) 四类写操作全部被拒（403），且给出明确原因
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{"name": "乙公司"})
	require.EqualValues(t, 403, body["code"])
	require.Contains(t, body["msg"], "只读")

	_, body = c.doWithCookie("PUT", "/api/admin/companies/"+cid, map[string]interface{}{"name": "改名"})
	require.EqualValues(t, 403, body["code"])

	_, body = c.doWithCookie("POST", "/api/admin/companies/"+cid+"/status",
		map[string]interface{}{"status": "disabled"})
	require.EqualValues(t, 403, body["code"])

	_, body = c.doWithCookie("POST", "/api/admin/companies/"+cid+"/admins",
		map[string]interface{}{"username": "boss2", "password": "pwd12345"})
	require.EqualValues(t, 403, body["code"])

	// 4) 只读接口仍然可用（列表 / 管理员列表）
	_, body = c.doWithCookie("GET", "/api/admin/companies", nil)
	require.EqualValues(t, 0, body["code"])
	_, body = c.doWithCookie("GET", "/api/admin/companies/"+cid+"/admins", nil)
	require.EqualValues(t, 0, body["code"])

	// 5) /admin/me 下发锁定状态，供前端把企管页切为只读
	_, body = c.doWithCookie("GET", "/api/admin/me", nil)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, true, body["data"].(map[string]interface{})["independentLock"])

	// 6) 关闭锁定后写操作立刻恢复（运行期开关，无需重启）
	_, body = c.doWithCookie("PUT", "/api/admin/settings", map[string]string{"independent_lock": "false"})
	require.EqualValues(t, 0, body["code"])
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{"name": "丙公司"})
	require.EqualValues(t, 0, body["code"])
}

// TestSecretSettingsNeverLeakRealValue 敏感项（AppSecret / 存储 SecretKey / DB 连接串 / JWT 密钥）
// 的真实值永不出现在接口响应里——后台是给运营用的，响应体里的明文密钥等于泄漏。
func TestSecretSettingsNeverLeakRealValue(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	const secret = "real-app-secret-9f3a"
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"wx_appsecret": secret}, adminToken)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("GET", "/api/admin/settings", nil, adminToken)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret, "敏感项真实值绝不能出现在接口响应里")

	var got map[string]interface{}
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["key"] == "wx_appsecret" {
			got = m
		}
	}
	require.NotNil(t, got)
	require.Equal(t, setting.Mask, got["value"])
	require.Equal(t, true, got["secret"])
	require.Equal(t, true, got["configured"])

	// 掩码提交要被拒绝：否则用户一点保存就会把 "******" 当成真值写进库
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"wx_appsecret": setting.Mask}, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 真实值仍然被代码读到（回退链：设置 → 环境变量）
	cfg := config.Config{WXAppSecret: "env-secret"}
	require.Equal(t, secret, resolveWXAppSecret(cfg))
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"wx_appsecret": ""}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "env-secret", resolveWXAppSecret(cfg))
}

// TestPublicSettingsLoginCode 登录页白标（?company=）验证：
// 命中 enabled 企业的白标码 → 回传该企业简称（login_brand_name）；
// 未命中（无参数 / 不匹配 / 企业停用）→ 不回传该键，前端回落全局品牌。
// 安全口径：只回传 short_name，绝不泄露企业 ID / 名称等其它字段。
func TestPublicSettingsLoginCode(t *testing.T) {
	// 测试种子：1 号租户朗汇，login_code=langhui，short_name=朗汇机电
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	// 1) 命中 → 回传 login_brand_name = 朗汇机电
	code, body := c.do("GET", "/api/public/settings?company=langhui", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"])
	pub := body["data"].(map[string]interface{})
	require.Equal(t, "朗汇机电", pub["login_brand_name"], "命中白标码应回传企业简称")

	// 2) 不匹配 → 不回传该键（前端回落全局品牌名）
	_, body = c.do("GET", "/api/public/settings?company=nope", nil, "")
	pub = body["data"].(map[string]interface{})
	_, hasLoginBrand := pub["login_brand_name"]
	require.False(t, hasLoginBrand, "白标码不匹配不应回传 login_brand_name")

	// 3) 无参数 → 同样不回传该键
	_, body = c.do("GET", "/api/public/settings", nil, "")
	pub = body["data"].(map[string]interface{})
	_, hasLoginBrand = pub["login_brand_name"]
	require.False(t, hasLoginBrand, "无 company 参数不应回传 login_brand_name")

	// 4) 仅回传 short_name：响应里不应出现企业 id / 全称等其它字段
	_, body = c.do("GET", "/api/public/settings?company=langhui", nil, "")
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "廊坊市朗汇机电设备有限公司", "公开接口不得泄露企业全称")
}

// TestReadOnlySettingsCannotBeWrittenFromUI 只读项（DB 连接串 / JWT 密钥 / Cookie 安全）
// 必须在界面可见（管理员得知道当前配了什么），但不可改——改错会把「改回来的入口」一起弄丢。
func TestReadOnlySettingsCannotBeWrittenFromUI(t *testing.T) {
	// 只读项的值来自环境变量（测试装配里的 cfg.DBDSN 是空的），先把它设上，
	// 才能验证「界面如实展示已配置」而不是显示一片空白。
	t.Setenv("DB_DSN", "sqlite://test-only")
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1) 只读项照常出现在列表里，且带 readOnly / envVar 元数据
	_, body := c.do("GET", "/api/admin/settings", nil, adminToken)
	meta := map[string]map[string]interface{}{}
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		meta[m["key"].(string)] = m
	}
	for _, k := range []string{"db_dsn", "jwt_key", "cookie_secure", "cookie_domain"} {
		require.Contains(t, meta, k)
		require.Equal(t, true, meta[k]["readOnly"], k)
		require.NotEmpty(t, meta[k]["envVar"], k)
	}
	// 只读项界面改不了，展示它的全部意义就是「让人核对当前配的是什么」，
	// 所以给的是可辨认预览而不是六颗星：无凭据形态（sqlite://）原样显示，
	// 含口令的连接串只遮口令段（遮罩规则见 setting 包的 preview 单测）。
	require.Equal(t, "sqlite://test-only", meta["db_dsn"]["value"])

	// 2) 试图从界面改写 → 拒绝，并提示改法（只说「不允许」等于没说）
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"db_dsn": "root@tcp(bad)"}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "DB_DSN")

	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"jwt_key": "x"}, adminToken)
	require.EqualValues(t, 1, body["code"])

	// 3) 只读项不落库：库里没有 db_dsn 行（否则会出现「界面没改、库里有值」的误解）
	require.Equal(t, "", setting.GetString("db_dsn", ""), "只读项不应被 seed 进 sys_setting")
}

// TestStorageSettingsOverlayEnv 云存储 / CDN 凭据的覆盖语义：
// 界面设置非空 → 覆盖环境变量；留空 → 回退环境变量（不是「设了空串就把它清成空」）。
func TestStorageSettingsOverlayEnv(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	base := func() config.Config {
		return config.Config{
			StorageDriver:  "local",
			OssEndpoint:    "oss-env.aliyuncs.com",
			OssBucket:      "env-bucket",
			CDNAccessKeyID: "env-cdn-id",
			CDNEnabled:     false,
			CDNProvider:    "qiniu",
			CDNResourceID:  "env-resource",
		}
	}

	// 1) 界面全空 → 原样保留环境变量值
	cfg := base()
	cfg.OverlayFromSettings()
	require.Equal(t, "local", cfg.StorageDriver)
	require.Equal(t, "oss-env.aliyuncs.com", cfg.OssEndpoint)
	require.Equal(t, "env-cdn-id", cfg.CDNAccessKeyID)
	require.False(t, cfg.CDNEnabled)
	// 2026-10-23 新增：厂商与资源标识同样遵循「界面留空 → 保留环境变量」
	require.Equal(t, "qiniu", cfg.CDNProvider)
	require.Equal(t, "env-resource", cfg.CDNResourceID)

	// 2) 界面填了 → 覆盖（MinIO 与 OSS 共用同一组界面字段，按驱动二选一）
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"storage_driver":   "oss",
		"storage_endpoint": "oss-cn-hangzhou.aliyuncs.com",
		"storage_bucket":   "ui-bucket",
		"cdn_enabled":      "true",
		"cdn_provider":     "cloudflare",
		"cdn_resource_id":  "ui-zone-id",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	cfg = base()
	cfg.OverlayFromSettings()
	require.Equal(t, "oss", cfg.StorageDriver)
	require.Equal(t, "oss-cn-hangzhou.aliyuncs.com", cfg.OssEndpoint)
	require.Equal(t, "ui-bucket", cfg.OssBucket)
	require.True(t, cfg.CDNEnabled)
	require.Equal(t, "cloudflare", cfg.CDNProvider)
	require.Equal(t, "ui-zone-id", cfg.CDNResourceID)

	// 3) 清空 → 回到环境变量
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{
		"storage_driver":   "",
		"storage_endpoint": "",
		"storage_bucket":   "",
		"cdn_enabled":      "",
		"cdn_provider":     "",
		"cdn_resource_id":  "",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	cfg = base()
	cfg.OverlayFromSettings()
	require.Equal(t, "local", cfg.StorageDriver)
	require.Equal(t, "oss-env.aliyuncs.com", cfg.OssEndpoint)
	require.False(t, cfg.CDNEnabled)
	require.Equal(t, "qiniu", cfg.CDNProvider)
	require.Equal(t, "env-resource", cfg.CDNResourceID)
}

// TestCDNProviderValidation 厂商枚举：非法值被拒、合法值可存。
// 配错厂商最坏结果是「刷新静默失效」，所以要在写入时就拦住。
func TestCDNProviderValidation(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	for _, ok := range []string{"aliyun", "tencent", "qiniu", "cloudflare", "cloudfront"} {
		_, body := c.do("PUT", "/api/admin/settings", map[string]string{"cdn_provider": ok}, adminToken)
		require.EqualValues(t, 0, body["code"], "合法厂商应可保存：%s", ok)
	}
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"cdn_provider": "akamai"}, adminToken)
	require.EqualValues(t, 1, body["code"], "未支持的厂商应被拒")
}

// TestRestartKeysReportedOnSave 云存储 / CDN 客户端在启动时构造，改完必须重启才生效。
// 接口要点名告知，否则管理员会认为「保存成功但没生效」是 bug。
func TestRestartKeysReportedOnSave(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 常规项：不提示重启
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"brand_name": "甲"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, body["data"].(map[string]interface{})["restartKeys"])

	// 需重启项：点名返回
	_, body = c.do("PUT", "/api/admin/settings", map[string]string{"storage_driver": "oss"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	keys := body["data"].(map[string]interface{})["restartKeys"].([]interface{})
	require.Contains(t, keys, "storage_driver")
}

// TestTokenAndSignTTLHaveSafetyFloor 第二批写死常量接入设置后的语义：
// 界面可改、即时生效，但 **0 / 负数一律回落缺省**——
// access 有效期设 0 会让所有人一登录就掉线、签名时长设 0 会让链接一生成即失效，
// 都属于「改错之后连改回来的入口都没了」，必须有下限兜底。
func TestTokenAndSignTTLHaveSafetyFloor(t *testing.T) {
	setupSQLiteEngine(t)

	// 1) 未配置 → 编译期缺省（15 分钟 / 30 天）
	require.Equal(t, 15*time.Minute, auth.AccessTokenTTL())
	require.Equal(t, 30*24*time.Hour, auth.RefreshTokenTTL())
	require.Equal(t, 15*time.Minute, signTTL("presign_put_ttl_minutes", 15))
	require.Equal(t, 30*time.Minute, signTTL("sign_get_ttl_minutes", 30))
	require.Equal(t, time.Hour, signTTL("wxacode_sign_ttl_minutes", 60))

	// 2) 界面设置 → 立即生效
	require.NoError(t, setting.Upsert("access_token_ttl_minutes", "45"))
	require.NoError(t, setting.Upsert("refresh_token_ttl_days", "7"))
	require.NoError(t, setting.Upsert("presign_put_ttl_minutes", "5"))
	require.Equal(t, 45*time.Minute, auth.AccessTokenTTL())
	require.Equal(t, 7*24*time.Hour, auth.RefreshTokenTTL())
	require.Equal(t, 5*time.Minute, signTTL("presign_put_ttl_minutes", 15))

	// 3) 非法值（0 / 负数）→ 回落缺省，不把自己锁在外面
	require.NoError(t, setting.Upsert("access_token_ttl_minutes", "0"))
	require.NoError(t, setting.Upsert("refresh_token_ttl_days", "-1"))
	require.NoError(t, setting.Upsert("presign_put_ttl_minutes", "0"))
	require.Equal(t, 15*time.Minute, auth.AccessTokenTTL())
	require.Equal(t, 30*24*time.Hour, auth.RefreshTokenTTL())
	require.Equal(t, 15*time.Minute, signTTL("presign_put_ttl_minutes", 15))
}

// TestLoginRateLimitSettingTakesEffect 登录限流阈值取自系统设置且**无需重启**：
// 过去 NewLoginRateLimiter(10, time.Minute) 在注册期定死，后台改了也没用。
func TestLoginRateLimitSettingTakesEffect(t *testing.T) {
	r := setupSQLiteEngine(t)
	// 直接写设置（不走接口，避免登录请求本身消耗限流配额干扰断言）。
	// 阈值 1 = 窗口内允许 1 次：第 1 次放行，第 2 次起拒绝。
	require.NoError(t, setting.Upsert("login_rate_limit_per_min", "1"))

	c := newTestClient(r, t)
	bad := map[string]interface{}{"phone": "13900000001", "password": "wrong-pwd"}

	// 第 1 次：放行（密码错误是业务失败 code=1，不是限流）
	_, b1 := c.do("POST", "/api/employee/login", bad, "")
	require.EqualValues(t, 1, b1["code"])
	// 第 2 次：达到阈值 → 429
	_, b2 := c.do("POST", "/api/employee/login", bad, "")
	require.EqualValues(t, 429, b2["code"])
	require.Contains(t, b2["msg"], "频繁")
}

// loginEmployee 建一个员工并登录，返回其 Bearer 令牌（用于越权断言）。
func loginEmployee(t *testing.T, c *testClient) string {
	t.Helper()
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "设置测试员工", "phone": "13900000999", "email": "s@a.com", "title": "测试",
	}, c.cookies["access_token"])
	require.EqualValues(t, 0, body["code"])
	initPwd := body["initialPassword"].(string)
	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13900000999", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	return body["token"].(string)
}

// TestAuditLogRecordsAdminOperations 审计日志的核心价值：后台高危操作必须留痕。
// 覆盖「改系统设置 / 新增超管 / 重置他人密码 / 删除超管」，并断言能按 action 过滤查到。
func TestAuditLogRecordsAdminOperations(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1) 改系统设置
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{"brand_name": "审计测试"}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 2) 新增平台超管 → 重置其密码 → 删除
	_, body = c.do("POST", "/api/admin/platform-admins",
		map[string]interface{}{"username": "audit-boss", "password": "pwd12345"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	newID := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = c.do("POST", "/api/admin/platform-admins/"+newID+"/password",
		map[string]interface{}{"password": "another-pwd"}, adminToken)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("DELETE", "/api/admin/platform-admins/"+newID, nil, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 3) 查询审计日志：四条动作都在，且操作人是当前登录的超管
	//    分页信封：data 是当前页数组，总数与页码在顶层（见 handler/pagination.go）
	_, body = c.do("GET", "/api/admin/audit-logs", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	list := body["data"].([]interface{})
	require.NotEmpty(t, list)
	require.NotZero(t, body["total"], "分页回包应带总数")

	actions := map[string]bool{}
	for _, it := range list {
		m := it.(map[string]interface{})
		actions[m["action"].(string)] = true
		require.Equal(t, "admin", m["actorType"], "后台操作的操作人类型应为 admin")
		// 操作人姓名必须直接可读：只留 actor_id 的话还得自己回 admin 表对号，等于没记
		require.Equal(t, "admin", m["actorName"])
	}
	for _, a := range []string{
		"setting.update", "admin.create", "admin.reset_password", "admin.delete",
	} {
		require.True(t, actions[a], "缺少审计记录: %s", a)
	}

	// 4) 按 action 过滤可用
	_, body = c.do("GET", "/api/admin/audit-logs?action=admin.delete", nil, adminToken)
	filtered := body["data"].([]interface{})
	require.Len(t, filtered, 1)
	// 账号已删除，详情里仍应留着用户名（只存 id 就追溯不到删的是谁）
	require.Contains(t, filtered[0].(map[string]interface{})["detail"], "audit-boss")
}

// TestAuditLogNeverStoresSecretValues 审计表不能变成新的泄密面：
// 改敏感配置时只记「已修改」，绝不落明文（否则密钥会以明文形式长期留在库里）。
func TestAuditLogNeverStoresSecretValues(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	const secret = "super-secret-value-7788"
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"wx_appsecret": secret,
		"brand_name":   "明文可记",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("GET", "/api/admin/audit-logs?action=setting.update", nil, adminToken)
	list := body["data"].([]interface{})
	require.NotEmpty(t, list)
	detail := list[0].(map[string]interface{})["detail"].(string)
	require.NotContains(t, detail, secret, "审计详情绝不能出现密钥明文")
	require.Contains(t, detail, "明文可记", "非敏感项仍应记录变更值")
}

// TestAuditLogRecordsFailedLogin 登录失败要留痕：连续失败是爆破信号。
func TestAuditLogRecordsFailedLogin(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 用另一个 client 打错密码（避免污染主 client 的 cookie 身份）
	other := newTestClient(r, t)
	_, body := other.do("POST", "/api/admin/login",
		map[string]interface{}{"username": "admin", "password": "definitely-wrong"}, "")
	require.EqualValues(t, 1, body["code"])

	_, body = c.do("GET", "/api/admin/audit-logs?action=login.fail", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	list := body["data"].([]interface{})
	require.NotEmpty(t, list)
	m := list[0].(map[string]interface{})
	require.Equal(t, "fail", m["result"])
	// 登录失败时还没有登录身份，操作人应降级为 anonymous，但目标账号名要留下
	require.Equal(t, "anonymous", m["actorType"])
	require.Equal(t, "admin", m["targetId"])
}

// TestSettingsValueValidation 值域校验：类型合法 ≠ 取值合法。
// 类型校验（整数 / 布尔）拦不住「上传上限填 0」「基址漏写协议头」这类——
// 它们不会报错在配置上，而是表现为「所有上传失败」「小程序永远连不上」，
// 排查方向完全跑偏。这些项必须在保存入口拦下。
func TestSettingsValueValidation(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 合法值：先确认放行路径没被校验误伤
	for k, v := range map[string]string{
		"upload_max_mb":         "30",
		"miniprogram_base_url":  "https://api.example.com",
		"upload_base_url":       "http://127.0.0.1:8080/uploads",
		"storage_driver":        "oss",
		"pinned_company_id":     "1", // 朗汇（seed 的 1 号租户，enabled）
	} {
		_, body := c.do("PUT", "/api/admin/settings", map[string]string{k: v}, adminToken)
		require.EqualValues(t, 0, body["code"], "%s=%s 应合法：%v", k, v, body["msg"])
	}

	// 上传上限：0 / 负数 / 超上限都拒绝（0 会让所有上传失败且不指向配置）
	for _, bad := range []string{"0", "-5", "99999"} {
		_, body := c.do("PUT", "/api/admin/settings", map[string]string{"upload_max_mb": bad}, adminToken)
		require.EqualValues(t, 1, body["code"], "upload_max_mb=%s 应被拒绝", bad)
		require.Contains(t, body["msg"], "1 ~ 1024")
	}

	// 接口基址：漏写协议头（真机上最典型的手误：直接填 127.0.0.1:8080）
	_, body := c.do("PUT", "/api/admin/settings",
		map[string]string{"miniprogram_base_url": "127.0.0.1:8080"}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "http://")

	_, body = c.do("PUT", "/api/admin/settings",
		map[string]string{"upload_base_url": "/uploads"}, adminToken)
	require.EqualValues(t, 1, body["code"], "相对路径当基址会导致小程序拼出错误 URL")

	// 存储驱动：枚举之外的值（重启后会静默降级本地磁盘，运营以为在用对象存储）
	_, body = c.do("PUT", "/api/admin/settings",
		map[string]string{"storage_driver": "ftp"}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "local / minio / oss")

	// 锁定租户：不存在 / 已停用的企业会让小程序全部接口报「企业不存在或已停用」
	_, body = c.do("PUT", "/api/admin/settings",
		map[string]string{"pinned_company_id": "9999"}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不存在或已停用")

	// 0 = 平台模式（不锁定），必须放行
	_, body = c.do("PUT", "/api/admin/settings",
		map[string]string{"pinned_company_id": "0"}, adminToken)
	require.EqualValues(t, 0, body["code"], "0 表示平台模式，不应被拒")
}
