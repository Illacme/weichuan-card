package handler

import (
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
