package handler

// 分发档案的边界守门（迁移 0060）。
//
// 平台自身 CDN 收进 delivery_profiles 之后，它就成了「平台对象刷新凭据的唯一
// 真值源」。因此三件事必须钉死：不可删除、不可绑定给企业、平台域名不从这份
// 档案来（否则同一个域名存两处，改了存储基址这里还是旧的）。

import (
	"os"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/storage"
)

func setupDeliveryEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	tmpDB, err := os.CreateTemp("", "weichuan-delivery-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	seedDefaultCompany(t, db)

	uploadDir, err := os.MkdirTemp("", "weichuan-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir: uploadDir, UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true,
		JWTKey: "test-jwt-key", AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024, AllowExt: storage.DefaultAllowExt,
	}
	return buildEngine(t, db, cfg, up, nil), db
}

func TestBuiltinDeliveryProfileCannotBeDeleted(t *testing.T) {
	r, db := setupDeliveryEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	builtin := model.DeliveryProfile{
		Name: "平台自身 CDN", Provider: storage.CDNProviderNone,
		Builtin: true, Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(&builtin).Error)

	_, body := c.do("DELETE", "/api/admin/delivery-profiles/"+strconv.FormatInt(builtin.ID, 10), nil, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不可删除")

	var left int64
	require.NoError(t, db.Model(&model.DeliveryProfile{}).Where("id = ?", builtin.ID).Count(&left).Error)
	require.EqualValues(t, 1, left, "内置档案必须还在")

	// 列表要带上 builtin / purpose，界面据此渲染「不可删除 / 平台自身」标签。
	_, body = c.do("GET", "/api/admin/delivery-profiles", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	rows := body["data"].([]interface{})
	require.NotEmpty(t, rows)
	first := rows[0].(map[string]interface{})
	require.Equal(t, true, first["builtin"])
	require.Equal(t, model.DeliveryPurposePlatformPrimary, first["purpose"])
}

func TestPlatformDeliveryProfileCannotBeBoundToTenant(t *testing.T) {
	r, db := setupDeliveryEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	sp := model.StorageProfile{
		Name: "客户桶", Driver: "local", LocalDir: "/tmp/customer",
		BaseURL: "https://cdn.example.com/uploads", PublicRead: true, Version: 1,
	}
	require.NoError(t, db.Create(&sp).Error)
	dp := model.DeliveryProfile{
		Name: "平台自身 CDN", Provider: storage.CDNProviderAliyun,
		AccessKey: "AK", SecretKey: "SK", Domain: "https://platform.example.com",
		Builtin: true, Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(&dp).Error)

	_, body := c.do("PUT", "/api/admin/storage-bindings/global", map[string]interface{}{
		"storageProfileId": sp.ID, "deliveryProfileId": dp.ID, "force": true,
	}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "平台自身 CDN")

	var n int64
	require.NoError(t, db.Model(&model.StorageBinding{}).Count(&n).Error)
	require.Zero(t, n, "被拒绝的绑定不能落库")
}

// 平台分发档案不承载域名：平台对象的出参域名唯一真值源是存储档案的访问基址。
// 所以它允许 domain 为空，也允许改厂商而不填域名——否则管理员会被逼着把平台
// 域名抄第二遍，改一处另一处不动。
func TestPlatformDeliveryProfileNeedsNoDomain(t *testing.T) {
	r, db := setupDeliveryEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	dp := model.DeliveryProfile{
		Name: "平台自身 CDN", Provider: storage.CDNProviderNone,
		Builtin: true, Purpose: model.DeliveryPurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(&dp).Error)

	_, body := c.do("PUT", "/api/admin/delivery-profiles/"+strconv.FormatInt(dp.ID, 10), map[string]interface{}{
		"provider": "aliyun", "accessKey": "AK", "secretKey": "SK",
	}, adminToken)
	require.EqualValues(t, 0, body["code"], "平台档案不该被域名校验挡住：%v", body["msg"])

	// 对照：普通分发档案仍然必须填完整域名——它的域名就是企业出参的域名。
	_, body = c.do("POST", "/api/admin/delivery-profiles", map[string]interface{}{
		"name": "客户 CDN", "provider": "aliyun", "accessKey": "AK", "secretKey": "SK",
	}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "https://")
}

func TestDeliveryProfileProviderValidation(t *testing.T) {
	r, _ := setupDeliveryEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 配错厂商最坏结果是「刷新静默失效」，必须在写入时拦住。
	_, body := c.do("POST", "/api/admin/delivery-profiles", map[string]interface{}{
		"name": "x", "provider": "akamai", "domain": "https://cdn.example.com",
	}, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "厂商")

	for _, ok := range []string{"aliyun", "tencent", "qiniu", "cloudflare", "cloudfront"} {
		_, body := c.do("POST", "/api/admin/delivery-profiles", map[string]interface{}{
			"name": "ok-" + ok, "provider": ok, "domain": "https://cdn.example.com",
			"accessKey": "AK", "secretKey": "SK", "resourceId": "rid",
		}, adminToken)
		require.EqualValues(t, 0, body["code"], "合法厂商应可保存：%s（%v）", ok, body["msg"])
	}
}
