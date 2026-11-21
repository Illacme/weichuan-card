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

// 一个可控的 ObjectLister：用 map 当桶，能精确制造「列举失败」而不真的连网络。
type fakeLister struct {
	keys      map[string]int64
	listErr   error
	listedFor string
}

func (f *fakeLister) ListObjects(prefix string, each func(string, int64) error) error {
	f.listedFor = prefix
	if f.listErr != nil {
		return f.listErr
	}
	for k, v := range f.keys {
		if len(prefix) == 0 || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			if err := each(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestVerifySwitchObjects(t *testing.T) {
	t.Run("齐全通过", func(t *testing.T) {
		src := &fakeLister{keys: map[string]int64{"t/5/a.png": 1, "t/5/b.png": 2}}
		dst := &fakeLister{keys: map[string]int64{"t/5/a.png": 1, "t/5/b.png": 2, "t/6/other.png": 3}}
		missing, total, err := verifySwitchObjects(src, dst, "t/5/")
		require.NoError(t, err)
		require.Equal(t, 0, total)
		require.Empty(t, missing)
	})

	t.Run("缺对象报错并采样 key", func(t *testing.T) {
		src := &fakeLister{keys: map[string]int64{
			"t/5/b.png": 1, "t/5/a.png": 1, "t/5/z.png": 1, "t/5/c.png": 1,
			"t/5/d.png": 1, "t/5/e.png": 1, "t/5/f.png": 1,
		}}
		dst := &fakeLister{keys: map[string]int64{"t/5/a.png": 1}}
		missing, total, err := verifySwitchObjects(src, dst, "t/5/")
		require.NoError(t, err)
		require.Equal(t, 6, total, "总数按完整差集计，不受采样上限影响")
		require.Len(t, missing, switchMissingSample, "示例最多采样 5 个")
		require.Equal(t, "b.png", missing[0][len("t/5/"):])
		require.Equal(t, "c.png", missing[1][len("t/5/"):])
	})

	t.Run("前缀隔离：只核对本租户", func(t *testing.T) {
		src := &fakeLister{keys: map[string]int64{"t/5/a.png": 1, "t/6/other.png": 1}}
		dst := &fakeLister{keys: map[string]int64{"t/5/a.png": 1}}
		_, total, err := verifySwitchObjects(src, dst, "t/5/")
		require.NoError(t, err)
		require.Equal(t, 0, total, "别家对象不参与本租户切换核对")
		require.Equal(t, "t/5/", src.listedFor)
	})

	t.Run("列举失败 fail closed", func(t *testing.T) {
		boom := errors.New("bucket unreachable")
		src := &fakeLister{keys: map[string]int64{"t/5/a.png": 1}}
		dst := &fakeLister{listErr: boom}
		_, _, err := verifySwitchObjects(src, dst, "t/5/")
		require.ErrorIs(t, err, boom, "目标列举失败必须返回错误，不能当成空集放行")
	})

	t.Run("源列举失败 fail closed", func(t *testing.T) {
		boom := errors.New("source unreachable")
		src := &fakeLister{listErr: boom}
		dst := &fakeLister{keys: map[string]int64{}}
		_, _, err := verifySwitchObjects(src, dst, "t/5/")
		require.ErrorIs(t, err, boom)
	})
}

// rollbackBinding 的核心是交换语义与「凭据/目的档案有效」把关。
func TestRollbackBinding(t *testing.T) {
	setup := func(t *testing.T) (*gorm.DB, int64, int64) {
		t.Helper()
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(model.All()...))
		profileA := model.StorageProfile{Name: "A", Driver: "local", Status: "ok", Version: 1}
		profileB := model.StorageProfile{Name: "B", Driver: "local", Status: "ok", Version: 1}
		require.NoError(t, db.Create(&profileA).Error)
		require.NoError(t, db.Create(&profileB).Error)
		return db, profileA.ID, profileB.ID
	}

	t.Run("交换语义：来回切都保住历史", func(t *testing.T) {
		db, a, b := setup(t)
		// 模拟「从 A 切到 B」后的绑定状态。
		require.NoError(t, db.Create(&model.StorageBinding{
			TenantID: 5, StorageProfileID: b, PreviousStorageProfileID: a, Version: 2,
		}).Error)

		require.NoError(t, rollbackBinding(db, 5, "tester"))
		var got model.StorageBinding
		require.NoError(t, db.Where("tenant_id = ?", 5).First(&got).Error)
		require.Equal(t, a, got.StorageProfileID, "回滚应回到 A")
		require.Equal(t, b, got.PreviousStorageProfileID, "被换下的 B 进 previous，可再回滚")

		require.NoError(t, rollbackBinding(db, 5, "tester"))
		require.NoError(t, db.Where("tenant_id = ?", 5).First(&got).Error)
		require.Equal(t, b, got.StorageProfileID, "再回滚一次应回到 B")
		require.Equal(t, a, got.PreviousStorageProfileID)
	})

	t.Run("previous=0 拒绝", func(t *testing.T) {
		db, _, b := setup(t)
		require.NoError(t, db.Create(&model.StorageBinding{
			TenantID: 6, StorageProfileID: b, Version: 1,
		}).Error)
		err := rollbackBinding(db, 6, "tester")
		require.Error(t, err)
		require.Contains(t, err.Error(), "从未切换过")
	})

	t.Run("目的档案停用拒绝", func(t *testing.T) {
		db, a, b := setup(t)
		require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", a).Update("status", "deprecated").Error)
		require.NoError(t, db.Create(&model.StorageBinding{
			TenantID: 7, StorageProfileID: b, PreviousStorageProfileID: a, Version: 2,
		}).Error)
		err := rollbackBinding(db, 7, "tester")
		require.Error(t, err)
		require.Contains(t, err.Error(), "停用")
		var got model.StorageBinding
		require.NoError(t, db.Where("tenant_id = ?", 7).First(&got).Error)
		require.Equal(t, b, got.StorageProfileID, "拒绝回滚时绑定不得被改动")
	})

	t.Run("目的档案不存在拒绝", func(t *testing.T) {
		db, _, b := setup(t)
		require.NoError(t, db.Create(&model.StorageBinding{
			TenantID: 8, StorageProfileID: b, PreviousStorageProfileID: 9999, Version: 2,
		}).Error)
		err := rollbackBinding(db, 8, "tester")
		require.Error(t, err)
		require.Contains(t, err.Error(), "不存在")
	})

	t.Run("没有绑定记录拒绝", func(t *testing.T) {
		db, _, _ := setup(t)
		require.Error(t, rollbackBinding(db, 9, "tester"))
	})
}

// saveBinding 只在档案真变化时记 previous，重复提交不能把回滚游标覆盖成 no-op。
func TestSaveBindingRecordsPreviousOnlyOnChange(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.StorageProfile{Name: "A", Driver: "local", Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "B", Driver: "local", Version: 1}).Error)

	require.NoError(t, saveBinding(db, 5, 1, 0, "t"))
	var b model.StorageBinding
	require.NoError(t, db.Where("tenant_id = ?", 5).First(&b).Error)
	require.EqualValues(t, 0, b.PreviousStorageProfileID, "首次绑定没有上一套")

	require.NoError(t, saveBinding(db, 5, 2, 0, "t"))
	require.NoError(t, db.Where("tenant_id = ?", 5).First(&b).Error)
	require.EqualValues(t, 1, b.PreviousStorageProfileID, "档案变化应记 previous")

	// 重复提交同一档案：previous 不应被覆盖成 2（否则回滚变 no-op）。
	require.NoError(t, saveBinding(db, 5, 2, 0, "t"))
	require.NoError(t, db.Where("tenant_id = ?", 5).First(&b).Error)
	require.EqualValues(t, 1, b.PreviousStorageProfileID, "重复提交不得覆盖回滚游标")
}

// guardManualSwitch 的 force 只跳过对象核对，不跳过读库失败。
func TestGuardManualSwitchForce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))

	srcDir, _ := os.MkdirTemp("", "wc-guard-src-")
	t.Cleanup(func() { _ = os.RemoveAll(srcDir) })
	dstDir, _ := os.MkdirTemp("", "wc-guard-dst-")
	t.Cleanup(func() { _ = os.RemoveAll(dstDir) })
	src := &storage.LocalUploader{Dir: srcDir, AllowExt: storage.DefaultAllowExt}
	require.NoError(t, src.PutObject("t/5/a.png", []byte("a"), "image/png"))
	// 目标故意留空。
	resolver := storageprofile.NewWithLocal(db, srcDir)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "src", Driver: "local", LocalDir: srcDir, Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "dst", Driver: "local", LocalDir: dstDir, Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 5, StorageProfileID: 1, Version: 1}).Error)

	err = guardManualSwitch(db, resolver, 5, 2, false)
	require.Error(t, err, "目标缺对象应拒绝")
	require.Contains(t, err.Error(), "t/5/a.png", "错误要带缺失 key 示例")

	require.NoError(t, guardManualSwitch(db, resolver, 5, 2, true), "force 应跳过对象核对")
}

// autoSwitch 在复制端「零失败」之外独立读端复核，同样 fail closed。
func TestGuardAutoSwitchFailClosed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	dir, _ := os.MkdirTemp("", "wc-guard-auto-")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src := &storage.LocalUploader{Dir: dir, AllowExt: storage.DefaultAllowExt}
	require.NoError(t, src.PutObject("t/5/a.png", []byte("a"), "image/png"))
	resolver := storageprofile.NewWithLocal(db, dir)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "src", Driver: "local", LocalDir: dir, Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "dst", Driver: "local", LocalDir: t.TempDir(), Version: 1}).Error)
	job := &model.StorageMigrationJob{TenantID: 5, SourceProfileID: 1, TargetProfileID: 2}

	err = guardAutoSwitch(resolver, job)
	require.Error(t, err, "目标未搬全不得自动切换")
	require.Contains(t, err.Error(), "拒绝自动切换")
}

// 回滚不得因「对象齐全核对」被卡：用一个永远列举失败的目标档案证明
// rollbackBinding 完全不碰对象列举（它只查库 + 校验档案有效性）。
func TestRollbackDoesNotProbeObjects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.StorageProfile{Name: "A", Driver: "minio", Endpoint: "unreachable.invalid", Bucket: "x", BaseURL: "https://x/uploads", PublicRead: true, Status: "ok", Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageProfile{Name: "B", Driver: "local", Status: "ok", Version: 1}).Error)
	require.NoError(t, db.Create(&model.StorageBinding{TenantID: 5, StorageProfileID: 2, PreviousStorageProfileID: 1, Version: 2}).Error)
	// 目的档案是不可达的 minio，若回滚去做列举就会因网络失败；这里应当成功。
	require.NoError(t, rollbackBinding(db, 5, "tester"), "回滚是应急动作，不得被对象探测卡住")
}
