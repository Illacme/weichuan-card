package repository

// 档案 → nginx 回源目标的读取守门。
//
// 这一层的失败模式全是静默的：漏展开全局绑定 → 那些企业的 location 消失、
// 图全 404；把「绑定指向坏档案」当成「没绑定」跳过 → 那家悄悄回落平台盘，
// 而后台显示配置是好的。所以每条都要有断言。

import (
	"errors"
	"testing"

	"weichuan/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func profileTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Company{}, &model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
	))
	return db
}

func TestListProfileTargetsMissingTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	_, err = ListProfileTargets(db)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrProfileTablesMissing),
		"迁移 0054 未应用要能被调用方分辨出来（处置步骤不同），实际：%v", err)
	require.Contains(t, err.Error(), "0054")
}

func TestListProfileTargetsNoBindings(t *testing.T) {
	db := profileTestDB(t)
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "三家", Status: "enabled"}).Error)
	got, err := ListProfileTargets(db)
	require.NoError(t, err)
	require.Empty(t, got, "没有任何绑定是合法状态，不能报错也不能造出目标")
}

// 全局绑定必须展开成逐家企业：nginx 的 location 是按 /uploads/t/<cid>/ 生成的，
// 没有「全局 location」这种形态。漏展开 = 所有继承默认的企业图全 404。
func TestListProfileTargetsExpandsGlobalBinding(t *testing.T) {
	db := profileTestDB(t)
	sp := model.StorageProfile{Name: "默认桶", Driver: "minio", Endpoint: "https://s.example.com",
		Bucket: "b", UseSSL: true, PublicRead: true, Version: 1, Status: "ok"}
	require.NoError(t, db.Create(&sp).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 0, StorageProfileID: sp.ID, Version: 1}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "三家", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 9, Name: "九家", Status: "disabled"}).Error)

	got, err := ListProfileTargets(db)
	require.NoError(t, err)
	require.Len(t, got, 2, "全局绑定要展开成每一家企业（停用的也算：多一条 location 无害）")
	require.Equal(t, int64(3), got[0].CompanyID)
	require.Equal(t, int64(9), got[1].CompanyID)
	require.True(t, got[0].Global, "没有自己的绑定行 → 走全局默认")
	require.Equal(t, "s.example.com", got[0].Target.Endpoint, "endpoint 必须去掉 scheme，否则拼出 https://https://")
	require.True(t, got[0].Target.NeedsSnippet())
	require.Equal(t, "ok", got[0].ProfileStatus)
}

// 企业自己的绑定优先于全局；只有自己绑定行的企业才被纳入（无全局时不乱展开）。
func TestListProfileTargetsExplicitBindingOnly(t *testing.T) {
	db := profileTestDB(t)
	g := model.StorageProfile{Name: "全局", Driver: "minio", Endpoint: "g.example.com", Bucket: "gb", Version: 1, Status: "ok"}
	own := model.StorageProfile{Name: "自有", Driver: "oss", Endpoint: "o.example.com", Bucket: "ob", Version: 1, Status: "unknown"}
	require.NoError(t, db.Create(&g).Error)
	require.NoError(t, db.Create(&own).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 7, StorageProfileID: own.ID, Version: 1}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 3, Name: "三家", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 7, Name: "七家", Status: "enabled"}).Error)

	got, err := ListProfileTargets(db)
	require.NoError(t, err)
	require.Len(t, got, 1, "没有全局绑定，就不该给 3 号造一条目标")
	require.Equal(t, int64(7), got[0].CompanyID)
	require.False(t, got[0].Global)
	require.Equal(t, "ob", got[0].Target.Bucket)
}

// 绑定指向不存在 / 已停用的档案必须报错，**不能**当成「没绑定」跳过：
// 跳过等于让那家悄悄回落平台盘，而运行时上传会报同一个错，两边对不上。
func TestListProfileTargetsRejectsBrokenBinding(t *testing.T) {
	db := profileTestDB(t)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 7, StorageProfileID: 999, Version: 1}).Error)

	_, err := ListProfileTargets(db)
	require.Error(t, err)
	require.Contains(t, err.Error(), "企业 7")
	require.Contains(t, err.Error(), "不可用")
}
