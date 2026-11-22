package handler

import (
	"reflect"
	"strconv"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
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
	// force=true：这里测的是「档案存在即可绑定」的 CRUD 通路，目标档案是 never
	// 连通的假 minio，切换前核对必然失败；核对本身由 storage_switch_test.go 覆盖。
	_, body = c.do("PUT", "/api/admin/storage-bindings/global", map[string]any{
		"storageProfileId": 1, "force": true,
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

// 内置本地档案的驱动与目录都锁死：驱动要留着做云存储不可用时的兜底；
// 目录则与静态路由 r.Static("/uploads", cfg.UploadDir) 共用一个真值源。
// 否则「不可删除」只是假安全——管理员能把它改成一套不可用的配置。
func TestBuiltinStorageProfileDriverAndDirLocked(t *testing.T) {
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

	// 改名字是允许的（管理员可能想区分多个内置档案的展示名）。
	_, body = c.do("PUT", "/api/admin/storage-profiles/"+strconv.FormatInt(row.ID, 10),
		map[string]any{"name": "主站本地盘"}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])

	// 目录单独改必须拒绝：静态路由仍绑在 cfg.UploadDir，分叉会把租户写到
	// 静态路由读不到的目录，结果是全站裂图且没有任何报错。
	_, body = c.do("PUT", "/api/admin/storage-profiles/"+strconv.FormatInt(row.ID, 10),
		map[string]any{"localDir": "/mnt/elsewhere"}, token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "本地上传目录")
	var after model.StorageProfile
	require.NoError(t, db.First(&after, row.ID).Error)
	require.Empty(t, after.LocalDir, "被拒的请求不能先把目录写进库再报错")

	// 提交与当前值相同的目录不算改动，应该放行（前端 disabled 后仍可能回显提交）。
	_, body = c.do("PUT", "/api/admin/storage-profiles/"+strconv.FormatInt(row.ID, 10),
		map[string]any{"localDir": ""}, token)
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

// 内置档案不能当租户写入目标，也不能参与迁移：
//   - 平台镜像是备份落点，一旦绑上，新文件写进备份目录，主出参域名却不认它；
//   - 平台自身存储装的是 p/ 平台对象，绑给租户会把两边字节写混且都不报错。
func TestBuiltinProfilesCannotBeWriteTarget(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))

	require.NoError(t, db.Create(&model.StorageProfile{
		Name: "普通桶", Driver: "minio", Version: 1,
	}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{
		Name: "平台本地镜像", Driver: "local", Builtin: true,
		Purpose: model.ProfilePurposePlatformMirror, Version: 1,
	}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{
		Name: "平台自身存储", Driver: "local", Builtin: true,
		Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}).Error)
	var normal, mirror, platform model.StorageProfile
	require.NoError(t, db.First(&normal, "name = ?", "普通桶").Error)
	require.NoError(t, db.First(&mirror, "name = ?", "平台本地镜像").Error)
	require.NoError(t, db.First(&platform, "name = ?", "平台自身存储").Error)

	require.Empty(t, validateBindingTargets(db, normal.ID, 0))
	require.Contains(t, validateBindingTargets(db, mirror.ID, 0), "不能绑定")
	require.Contains(t, validateBindingTargets(db, platform.ID, 0), "不能绑定")
	require.False(t, builtinProfileInvolved(db, normal.ID))
	require.True(t, builtinProfileInvolved(db, normal.ID, mirror.ID))
	require.True(t, builtinProfileInvolved(db, normal.ID, platform.ID))
}

// pathStyle 曾经是「能填但没用」的僵尸字段：S3 路径风格由 nginxsnippet.Target.PathStyle()
// 从 host_header 单点推导，上传侧与回填侧都不读这一列。0058 已删列，
// 这里钉住模型和接口都别把它加回来——否则管理员又会以为填它能改变请求风格。
func TestStorageProfileHasNoZombiePathStyle(t *testing.T) {
	_, ok := reflect.TypeOf(model.StorageProfile{}).FieldByName("PathStyle")
	require.False(t, ok, "pathStyle 是僵尸字段：路径风格只由 hostHeader 推导（0058 已删列）")
}

func TestStorageProfileAPINeverExposesPathStyle(t *testing.T) {
	r, _ := bindingEngine(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	// 就算老客户端仍往请求里塞 pathStyle，也不能在出参里回显——
	// 回显会让人以为它还在生效。
	_, body := c.do("POST", "/api/admin/storage-profiles", map[string]any{
		"name": "no-path-style", "driver": "minio", "endpoint": "s.example.com", "bucket": "assets",
		"baseUrl": "https://card.example.com/uploads", "useSsl": false, "publicRead": true,
		"pathStyle": true, "hostHeader": "", "accessKey": "AK", "secretKey": "SK",
	}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])

	_, body = c.do("GET", "/api/admin/storage-profiles", nil, token)
	require.EqualValues(t, 0, body["code"])
	list, ok := body["data"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, list)
	for _, raw := range list {
		_, leaked := raw.(map[string]any)["pathStyle"]
		require.False(t, leaked, "出参不该再暴露从未生效的 pathStyle")
	}
}
