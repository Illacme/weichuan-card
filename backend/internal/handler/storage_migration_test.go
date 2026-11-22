package handler

import (
	"errors"
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

// 断点续跑必须重试失败对象：旧实现里失败对象也推进 last_key，
// 续跑那句 key <= last_key 会把它永久跳过——表面「零失败」实际对象从没搬过去。
func TestRunStorageMigrationRetriesFailedObject(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
		&model.StorageMigrationJob{}, &model.StorageMigrationError{}))

	srcDir, err := os.MkdirTemp("", "wc-mig-retry-src-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(srcDir) })
	dstDir, err := os.MkdirTemp("", "wc-mig-retry-dst-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dstDir) })

	src := &storage.LocalUploader{Dir: srcDir, AllowExt: storage.DefaultAllowExt}
	require.NoError(t, src.PutObject("t/7/a.png", []byte("a"), "image/png"))
	require.NoError(t, src.PutObject("t/7/b.png", []byte("b"), "image/png"))

	srcProfile := model.StorageProfile{Name: "src", Driver: "local", Version: 1, LocalDir: srcDir}
	dstProfile := model.StorageProfile{Name: "dst", Driver: "local", Version: 1, LocalDir: dstDir}
	require.NoError(t, db.Create(&srcProfile).Error)
	require.NoError(t, db.Create(&dstProfile).Error)
	job := model.StorageMigrationJob{TenantID: 7, SourceProfileID: srcProfile.ID, TargetProfileID: dstProfile.ID, Status: "running"}
	require.NoError(t, db.Create(&job).Error)

	// 第一次：让 b.png 失败，a.png 正常。
	failKey := "t/7/b.png"
	origCopy := migrationCopyObject
	t.Cleanup(func() { migrationCopyObject = origCopy })
	migrationCopyObject = func(copier storage.ObjectCopier, sink storage.ObjectStreamWriter, key string) error {
		if key == failKey {
			return os.ErrPermission
		}
		return copier.CopyObjectTo(sink, key)
	}
	resolver := storageprofile.NewWithLocal(db, srcDir)
	runStorageMigration(db, resolver, job.ID)

	var first model.StorageMigrationJob
	require.NoError(t, db.First(&first, job.ID).Error)
	require.EqualValues(t, 2, first.Total)
	require.EqualValues(t, 1, first.Done, "第一次只有 a 成功")
	require.EqualValues(t, 1, first.Failed, "b 失败应入账")
	var errs []model.StorageMigrationError
	require.NoError(t, db.Where("job_id = ?", job.ID).Find(&errs).Error)
	require.Len(t, errs, 1, "失败清单应记下 b")

	// 第二次：让 b 成功，模拟「修好源对象后续跑」。
	migrationCopyObject = origCopy
	require.NoError(t, db.Model(&model.StorageMigrationJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": "running", "finished_at": nil}).Error)
	runStorageMigration(db, resolver, job.ID)

	var second model.StorageMigrationJob
	require.NoError(t, db.First(&second, job.ID).Error)
	require.EqualValues(t, 2, second.Done, "续跑应重试 b 并补上 Done")
	require.EqualValues(t, 0, second.Failed, "b 重试成功后失败数归零")
	require.NoError(t, db.Where("job_id = ?", job.ID).Find(&errs).Error)
	require.Empty(t, errs, "重试成功后失败清单应清空")
	dst := &storage.LocalUploader{Dir: dstDir, AllowExt: storage.DefaultAllowExt}
	data, err := dst.ReadObject(failKey)
	require.NoError(t, err, "失败对象续跑后必须真的出现在目标桶")
	require.Equal(t, "b", string(data))
}

func TestCanRunMigration(t *testing.T) {
	cases := []struct {
		status string
		failed int64
		want   bool
	}{
		{"created", 0, true},
		{"canceled", 0, true},
		{"canceled", 3, true},
		{"running", 0, false},
		{"running", 5, false},
		{"done", 0, false},
		{"done", 2, true}, // 有失败对象必须允许续跑，否则失败清单里的对象没有重试入口
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, canRunMigration(tc.status, tc.failed), "status=%s failed=%d", tc.status, tc.failed)
	}
}

// TestRunStorageMigrationStatusQueryFailure 区分「被取消」与「查状态失败」。
// 旧实现把两者合并成同一个 if 后直接 return：DB 抖动时 goroutine 静默退出，
// 任务永久停在 running，既没有错误提示也没有失败终态。
func TestRunStorageMigrationStatusQueryFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
		&model.StorageMigrationJob{}, &model.StorageMigrationError{}))

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := &storage.LocalUploader{Dir: srcDir, AllowExt: storage.DefaultAllowExt}
	require.NoError(t, src.PutObject("t/5/a.png", []byte("a"), "image/png"))
	srcProfile := model.StorageProfile{Name: "src", Driver: "local", Version: 1, LocalDir: srcDir}
	dstProfile := model.StorageProfile{Name: "dst", Driver: "local", Version: 1, LocalDir: dstDir}
	require.NoError(t, db.Create(&srcProfile).Error)
	require.NoError(t, db.Create(&dstProfile).Error)
	job := model.StorageMigrationJob{TenantID: 5, SourceProfileID: srcProfile.ID, TargetProfileID: dstProfile.ID, Status: "running"}
	require.NoError(t, db.Create(&job).Error)

	// 精确命中状态查询失败：真实查询本身由默认实现覆盖在其它用例里。
	origStatus := migrationJobStatus
	t.Cleanup(func() { migrationJobStatus = origStatus })
	migrationJobStatus = func(*gorm.DB, int64) (string, error) {
		return "", errors.New("injected status query failure")
	}

	resolver := storageprofile.NewWithLocal(db, srcDir)
	runStorageMigration(db, resolver, job.ID)

	var got model.StorageMigrationJob
	require.NoError(t, db.First(&got, job.ID).Error)
	require.Equal(t, "failed", got.Status, "DB 错误必须写失败终态，不能让任务卡在 running")
	require.Contains(t, got.Error, "injected status query failure")
	require.NotNil(t, got.FinishedAt)

	// 反向对照：明确的 canceled 不能被 finish 覆盖成 failed。
	migrationJobStatus = func(*gorm.DB, int64) (string, error) { return "canceled", nil }
	require.NoError(t, db.Model(&model.StorageMigrationJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": "canceled", "error": ""}).Error)
	runStorageMigration(db, resolver, job.ID)
	var canceled model.StorageMigrationJob
	require.NoError(t, db.First(&canceled, job.ID).Error)
	require.Equal(t, "canceled", canceled.Status)
	require.Empty(t, canceled.Error)
}
