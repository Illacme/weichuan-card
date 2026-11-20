package storageprofile

import (
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
)

// 内置本地档案是「云存储全坏时还能上传」的唯一退路，必须幂等且不被覆盖。
func TestEnsureBuiltinLocal(t *testing.T) {
	db := testDB(t)

	first, err := EnsureBuiltinLocal(db, "/data/uploads", "https://card.example.com/uploads")
	require.NoError(t, err)
	require.True(t, first.Builtin)
	require.Equal(t, "local", first.Driver)
	require.Equal(t, "/data/uploads", first.LocalDir)
	require.Equal(t, "https://card.example.com/uploads", first.BaseURL)

	// 重复启动：不能重复插入，也不能覆盖管理员改过的目录与名字。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", first.ID).
		Updates(map[string]any{"name": "我的本地盘", "local_dir": "/mnt/custom"}).Error)
	second, err := EnsureBuiltinLocal(db, "/data/uploads", "https://card.example.com/uploads")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "我的本地盘", second.Name)
	require.Equal(t, "/mnt/custom", second.LocalDir)

	var count int64
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("builtin = ?", true).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

// 同名非内置档案存在时不能让进程起不来：退让成不冲突的名字。
func TestEnsureBuiltinLocalNameConflict(t *testing.T) {
	db := testDB(t)
	require.NoError(t, db.Create(&model.StorageProfile{Name: builtinLocalName, Driver: "minio", Version: 1}).Error)

	row, err := EnsureBuiltinLocal(db, "/tmp/u", "")
	require.NoError(t, err)
	require.True(t, row.Builtin)
	require.NotEqual(t, builtinLocalName, row.Name)
}

// 没有绑定档案时，内置档案只是「可用选项」，不能自动变成全局默认：
// 那会覆盖租户已有的 tenant_storage_configs，静默改变文件落点。
func TestEnsureBuiltinLocalDoesNotBind(t *testing.T) {
	db := testDB(t)
	_, err := EnsureBuiltinLocal(db, "/tmp/u", "")
	require.NoError(t, err)

	var n int64
	require.NoError(t, db.Model(&model.StorageBinding{}).Count(&n).Error)
	require.EqualValues(t, 0, n)
}
