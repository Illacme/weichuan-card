package mirror

import (
	"bytes"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/storage/testsupport"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeCloud 只实现回拉需要的 ObjectCopier：字节放在内存里，
// 这样测试能证明「流式从云读回、写进镜像目录」这条链路真的走通。
type fakeCloud struct {
	objects map[string][]byte
}

func (f *fakeCloud) Save(*multipart.FileHeader, string) (string, error) { return "", nil }
func (f *fakeCloud) SaveBytes(string, []byte, string) (string, error)   { return "", nil }
func (f *fakeCloud) CopyObjectTo(dst storage.ObjectStreamWriter, key string) error {
	data, ok := f.objects[key]
	if !ok {
		return os.ErrNotExist
	}
	return dst.PutObjectStream(key, bytes.NewReader(data), int64(len(data)), "image/png")
}

func mirrorDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	return db
}

func TestCopyStreamsCloudObjectIntoMirrorDir(t *testing.T) {
	db := mirrorDB(t)
	key, err := storage.ObjectKeyFor(storage.ScopeTenant, 7, "abc.png")
	require.NoError(t, err)
	dir := t.TempDir()
	cloud := &fakeCloud{objects: map[string][]byte{key: []byte("image-bytes")}}
	m := New(db, testsupport.NewStaticResolver(cloud, "minio"), &model.StorageProfile{LocalDir: dir})

	require.NoError(t, m.Copy(7, key))
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.Equal(t, []byte("image-bytes"), got)

	// 归属不符必须拒绝：否则 A 租户可以借这个接口把 B 的对象复制到自己的镜像里。
	require.Error(t, m.Copy(8, key))

	// 删除双清：幂等，不存在不报错。
	require.NoError(t, m.Remove(key))
	_, err = os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, m.Remove(key))
}

// 源已经在本地盘时不能再复制一份：那是同一份字节的两份本地副本，
// 只会把磁盘吃满，不增加任何可用性。
func TestCopySkipsLocalSource(t *testing.T) {
	db := mirrorDB(t)
	src := t.TempDir()
	key, err := storage.ObjectKeyFor(storage.ScopePlatform, 0, "wxacode/1.jpg")
	require.NoError(t, err)
	srcFile := filepath.Join(src, filepath.FromSlash(key))
	require.NoError(t, os.MkdirAll(filepath.Dir(srcFile), 0755))
	require.NoError(t, os.WriteFile(srcFile, []byte("local"), 0644))

	local := &storage.LocalUploader{Dir: src}
	m := New(db, testsupport.NewStaticResolver(local, "local"), &model.StorageProfile{LocalDir: t.TempDir()})
	require.NoError(t, m.Copy(0, key))
	var n int
	_ = filepath.WalkDir(m.dest.(*storage.LocalUploader).Dir, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	require.Zero(t, n)
}

// 两级开关：平台总闸 AND 租户列。缺一级都不能回拉，
// 否则「按租户开关」会变成「开了总闸全网都备份」。
func TestEnabledRequiresBothSwitches(t *testing.T) {
	db := mirrorDB(t)
	require.NoError(t, setting.Init(db))
	require.NoError(t, db.Create(&model.Company{ID: 7, Name: "A", Status: "enabled"}).Error)
	m := New(db, nil, &model.StorageProfile{LocalDir: t.TempDir()})

	require.NoError(t, setting.Upsert("local_mirror_enabled", "true"))
	require.False(t, m.Enabled(7), "租户未开启时不能回拉")
	require.True(t, m.Enabled(0), "平台对象不受租户列约束")

	require.NoError(t, db.Model(&model.Company{}).Where("id = ?", 7).Update("local_mirror_enabled", true).Error)
	require.True(t, m.Enabled(7))

	require.NoError(t, setting.Upsert("local_mirror_enabled", "false"))
	require.False(t, m.Enabled(7), "总闸关闭时任何租户都不能回拉")
	require.False(t, m.Enabled(0))

	t.Cleanup(func() { _ = setting.Upsert("local_mirror_enabled", "true") })
}

// 归属校验是这条链路唯一的越权闸门：键来自客户端上报（直传确认），
// 不校验就能用 A 的凭据把 B 桶里的对象回拉到 A 的备份目录，两边都不报错。
// 四种组合都要覆盖——只比 scope 不比 cid、或只比 cid 不比 scope，都会漏掉一半。
func TestCopyRejectsForeignTenantKey(t *testing.T) {
	db := mirrorDB(t)
	tenant7, err := storage.ObjectKeyFor(storage.ScopeTenant, 7, "a.png")
	require.NoError(t, err)
	platform, err := storage.ObjectKeyFor(storage.ScopePlatform, 0, "wxacode/1.jpg")
	require.NoError(t, err)

	cloud := &fakeCloud{objects: map[string][]byte{
		tenant7: []byte("t7"), platform: []byte("p"),
	}}
	dir := t.TempDir()
	m := New(db, testsupport.NewStaticResolver(cloud, "minio"), &model.StorageProfile{LocalDir: dir})

	// 别人的租户键：拒绝
	require.Error(t, m.Copy(8, tenant7))
	// 租户身份去拉平台对象：拒绝
	require.Error(t, m.Copy(7, platform))
	// 平台身份去拉租户对象：拒绝（否则平台备份盘会混进客户素材）
	require.Error(t, m.Copy(0, tenant7))
	// 身份与归属一致：放行
	require.NoError(t, m.Copy(7, tenant7))
	require.NoError(t, m.Copy(0, platform))

	// 被拒的三次不能留下任何文件：镜像目录里只该有两个合法对象。
	var n int
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	require.Equal(t, 2, n)
}

// splitResolver 模拟「平台实例 ≠ 全局默认绑定」这一现实形态：
// Platform() 是素材 / 小程序码真正落盘的桶，For(0) 却会解析到
// storage_bindings.tenant_id=0 指向的另一套档案。
type splitResolver struct {
	platform storage.Uploader
	byTenant storage.Uploader
}

func (s *splitResolver) For(int64) (storage.Uploader, error) { return s.byTenant, nil }
func (s *splitResolver) Platform() storage.Uploader          { return s.platform }
func (s *splitResolver) DriverFor(int64) string              { return "minio" }

// 平台对象必须从 Platform() 读：p/ 对象写在平台实例里，
// 而 resolve(0) 走的是全局默认绑定。用 For(0) 会去另一个桶里找，
// 结果是所有平台对象（素材采用、小程序码）静默没有镜像。
func TestCopyReadsPlatformObjectsFromPlatformInstance(t *testing.T) {
	db := mirrorDB(t)
	key, err := storage.ObjectKeyFor(storage.ScopePlatform, 0, "wxacode/1.jpg")
	require.NoError(t, err)

	platformBucket := &fakeCloud{objects: map[string][]byte{key: []byte("in-platform")}}
	defaultBucket := &fakeCloud{objects: map[string][]byte{}} // 不含该对象
	dir := t.TempDir()
	m := New(db, &splitResolver{platform: platformBucket, byTenant: defaultBucket},
		&model.StorageProfile{LocalDir: dir})

	require.NoError(t, m.Copy(0, key))
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.Equal(t, []byte("in-platform"), got)
}
