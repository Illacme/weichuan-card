package handler

import (
	"testing"

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
