package handler

import (
	"os"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/storage"
	"weichuan/internal/storageprofile"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 迁移执行器的核心性质：按前缀列举、逐对象复制、进度落库、不删除源对象。
func TestRunStorageMigrationLocal(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
		&model.StorageMigrationJob{}, &model.StorageMigrationError{}))

	srcDir, err := os.MkdirTemp("", "wc-migration-src-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(srcDir) })
	dstDir, err := os.MkdirTemp("", "wc-migration-dst-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dstDir) })

	src := &storage.LocalUploader{Dir: srcDir, AllowExt: storage.DefaultAllowExt}
	require.NoError(t, src.PutObject("t/5/a.png", []byte("a"), "image/png"))
	require.NoError(t, src.PutObject("t/5/b.png", []byte("b"), "image/png"))
	require.NoError(t, src.PutObject("t/6/other.png", []byte("c"), "image/png"))

	srcProfile := model.StorageProfile{Name: "src", Driver: "local", Version: 1, LocalDir: srcDir}
	dstProfile := model.StorageProfile{Name: "dst", Driver: "local", Version: 1, LocalDir: dstDir}
	require.NoError(t, db.Create(&srcProfile).Error)
	require.NoError(t, db.Create(&dstProfile).Error)
	job := model.StorageMigrationJob{TenantID: 5, SourceProfileID: srcProfile.ID, TargetProfileID: dstProfile.ID, Status: "running"}
	require.NoError(t, db.Create(&job).Error)

	resolver := storageprofile.NewWithLocal(db, srcDir)
	runStorageMigration(db, resolver, job.ID)

	var got model.StorageMigrationJob
	require.NoError(t, db.First(&got, job.ID).Error)
	require.Equal(t, "done", got.Status)
	require.EqualValues(t, 2, got.Total, "租户任务只搬 t/<cid>/，不带别家企业")
	require.EqualValues(t, 2, got.Done)
	require.EqualValues(t, 0, got.Failed)
	// 源对象必须还在（只复制不删除）。
	_, err = src.ReadObject("t/5/a.png")
	require.NoError(t, err)
}
