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

	// 重复启动：展示名保留管理员改过的；local_dir 必须被 cfg.UploadDir 纠正回来。
	// 静态路由 r.Static 绑在 cfg.UploadDir 上，档案目录若分叉，绑定它的租户
	// 写到新目录、静态路由却从旧目录读 → 全站裂图且两边都不报错。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", first.ID).
		Updates(map[string]any{"name": "我的本地盘", "local_dir": "/mnt/custom"}).Error)
	second, err := EnsureBuiltinLocal(db, "/data/uploads", "https://card.example.com/uploads")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "我的本地盘", second.Name, "展示名是管理员可改的")
	require.Equal(t, "/data/uploads", second.LocalDir, "目录必须纠正回 cfg.UploadDir，不能保留分叉值")

	var count int64
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("builtin = ?", true).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

// 第二套不可删档案：本地备份盘。它与租户退路档案同属 builtin，但用途完全不同，
// 必须按 purpose 精确寻址——若查询只按 builtin=true，先创建的退路档案会被
// 镜像 ensure 当成自己，导致镜像目录写进 uploads（私有对象被静态路由公开）。
func TestEnsureBuiltinMirror(t *testing.T) {
	db := testDB(t)
	fallback, err := EnsureBuiltinLocal(db, "/data/uploads", "https://card.example.com/uploads")
	require.NoError(t, err)
	mirror, err := EnsureBuiltinMirror(db, "/data/mirror")
	require.NoError(t, err)

	require.NotEqual(t, fallback.ID, mirror.ID)
	require.Equal(t, model.ProfilePurposeTenantFallback, fallback.Purpose)
	require.Equal(t, model.ProfilePurposePlatformMirror, mirror.Purpose)
	require.True(t, mirror.Builtin)
	require.Equal(t, "local", mirror.Driver)
	require.Equal(t, "/data/mirror", mirror.LocalDir)
	require.False(t, mirror.PublicRead, "镜像副本不对外公开")
	require.Empty(t, mirror.BaseURL, "镜像不参与出参")

	// 反向改目录后重复启动必须纠正回来：运行期 MirrorManager 和备份脚本
	// 都读这条档案的 local_dir，分叉会让备份写进 A、打包打的是 B。
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", mirror.ID).
		Update("local_dir", "/mnt/wrong").Error)
	again, err := EnsureBuiltinMirror(db, "/data/mirror")
	require.NoError(t, err)
	require.Equal(t, mirror.ID, again.ID)
	require.Equal(t, "/data/mirror", again.LocalDir)

	fbAgain, err := EnsureBuiltinLocal(db, "/data/uploads", "https://card.example.com/uploads")
	require.NoError(t, err)
	require.Equal(t, fallback.ID, fbAgain.ID, "两套档案不能互相覆盖")

	var n int64
	require.NoError(t, db.Model(&model.StorageBinding{}).Count(&n).Error)
	require.Zero(t, n, "两套内置档案都不能自动变成全局绑定")
}

// 升级前的内置行 purpose 为空（SQLite AutoMigrate 不执行迁移里的 UPDATE），
// ensure 必须自己补齐，否则两套档案会同时以空用途存在。
func TestEnsureBuiltinBackfillsPurpose(t *testing.T) {
	db := testDB(t)
	require.NoError(t, db.Create(&model.StorageProfile{
		Name: builtinLocalName, Driver: "local", LocalDir: "/old/uploads",
		Builtin: true, Version: 1,
	}).Error)

	fallback, err := EnsureBuiltinLocal(db, "/new/uploads", "")
	require.NoError(t, err)
	require.Equal(t, model.ProfilePurposeTenantFallback, fallback.Purpose)
	_, err = EnsureBuiltinMirror(db, "/new/mirror")
	require.NoError(t, err)
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
// 那会覆盖企业已有的云档案绑定，静默改变文件落点。
func TestEnsureBuiltinLocalDoesNotBind(t *testing.T) {
	db := testDB(t)
	_, err := EnsureBuiltinLocal(db, "/tmp/u", "")
	require.NoError(t, err)

	var n int64
	require.NoError(t, db.Model(&model.StorageBinding{}).Count(&n).Error)
	require.EqualValues(t, 0, n)
}

// 老库里的内置行还叫「平台本地存储 / 平台本地镜像」等旧名：启动 ensure 必须把它们
// 升级成角色名，否则升级后的部署仍然显示三个分不清的名字。
func TestBuiltinLegacyNameUpgradedOnStartup(t *testing.T) {
	db := testDB(t)
	local, err := EnsureBuiltinLocal(db, "/data/uploads", "")
	require.NoError(t, err)
	// 模拟升级前的库：展示名还是旧默认值
	require.NoError(t, db.Model(&model.StorageProfile{}).Where("id = ?", local.ID).
		Update("name", legacyBuiltinLocalName).Error)

	again, err := EnsureBuiltinLocal(db, "/data/uploads", "")
	require.NoError(t, err)
	require.Equal(t, local.ID, again.ID)
	require.Equal(t, builtinLocalName, again.Name, "仍叫旧默认名的内置行必须升级成角色名")
}

// 新名已被别的档案占用时不改名：name 有唯一索引，硬改会让整个进程起不来。
// 宁可继续显示旧名，也不能为了好看挡住启动。
func TestBuiltinLegacyNameKeptWhenNewNameTaken(t *testing.T) {
	db := testDB(t)
	require.NoError(t, db.Create(&model.StorageProfile{
		Name: builtinMirrorName, Driver: "local", Version: 1,
	}).Error)
	legacy := model.StorageProfile{
		Name: legacyBuiltinMirrorName, Driver: "local", Builtin: true,
		Purpose: model.ProfilePurposePlatformMirror, Version: 1,
	}
	require.NoError(t, db.Create(&legacy).Error)

	got, err := EnsureBuiltinMirror(db, "/data/mirror")
	require.NoError(t, err)
	require.Equal(t, legacy.ID, got.ID)
	require.Equal(t, legacyBuiltinMirrorName, got.Name, "撞名时保留旧名，不能违反唯一索引")
}

// 平台主存储走同一个 helper，单独钉一次。
func TestRenameBuiltinPlatformLegacyName(t *testing.T) {
	db := testDB(t)
	row := model.StorageProfile{
		Name: legacyBuiltinPlatformName, Driver: "local", Builtin: true,
		Purpose: model.ProfilePurposePlatformPrimary, Version: 1,
	}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, renameBuiltinIfLegacy(db, &row, legacyBuiltinPlatformName, builtinPlatformName))

	var got model.StorageProfile
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, builtinPlatformName, got.Name)
}
