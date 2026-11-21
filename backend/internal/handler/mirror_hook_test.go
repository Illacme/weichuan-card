package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/mirror"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/storage/testsupport"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type mirrorFakeCloud struct {
	objects map[string][]byte
	deleted map[string]bool
}

func (f *mirrorFakeCloud) DeleteObject(key string) error {
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	f.deleted[key] = true
	return nil
}
func (f *mirrorFakeCloud) Save(*multipart.FileHeader, string) (string, error) { return "", nil }
func (f *mirrorFakeCloud) SaveBytes(string, []byte, string) (string, error)   { return "", nil }
func (f *mirrorFakeCloud) ReadObject(key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}
func (f *mirrorFakeCloud) PutObject(key string, data []byte, _ string) error {
	f.objects[key] = data
	return nil
}
func (f *mirrorFakeCloud) CopyObjectTo(dst storage.ObjectStreamWriter, key string) error {
	data, ok := f.objects[key]
	if !ok {
		return os.ErrNotExist
	}
	return dst.PutObjectStream(key, bytes.NewReader(data), int64(len(data)), "video/mp4")
}

// 预签名直传的字节不经过服务端，thumb 接口是唯一的「上传完成确认」点。
// 非图片（mp4）不会生成缩略图，但**必须照样回拉镜像**——视频正是最需要备份的一类。
func TestDirectUploadConfirmSchedulesMirrorForVideo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	seedDefaultCompany(t, db)
	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true, JWTKey: "test-jwt-key",
		AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{Dir: cfg.UploadDir, AllowExt: storage.DefaultAllowExt}
	dir := t.TempDir()
	key, err := storage.ObjectKeyFor(storage.ScopeTenant, 1, "movie.mp4")
	require.NoError(t, err)
	cloud := &mirrorFakeCloud{objects: map[string][]byte{key: []byte("video-bytes")}, deleted: map[string]bool{}}
	res := testsupport.NewStaticResolver(cloud, "minio")
	r := buildEngine(t, db, cfg, up, nil, res)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)
	old := mirrorManager
	mirrorManager = mirror.New(db, res, &model.StorageProfile{LocalDir: dir})
	t.Cleanup(func() { mirrorManager = old })

	require.NoError(t, setting.Upsert("local_mirror_enabled", "true"))
	_, body := c.do("PUT", "/api/admin/companies/1", map[string]any{
		"name": "默认企业", "localMirrorEnabled": true,
	}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])

	status, body := c.do("POST", "/api/admin/upload/thumb?companyId=1", map[string]any{"key": key}, token)
	require.Equal(t, http.StatusOK, status)
	// 非图片跳过缩略图是既有语义，不能因为加了镜像就改成报错。
	require.EqualValues(t, 0, body["code"])

	want := filepath.Join(dir, filepath.FromSlash(key))
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(want)
		return err == nil && string(data) == "video-bytes"
	}, 3*time.Second, 20*time.Millisecond, "直传确认后应异步生成镜像副本")
}

// 删除必须双清：只删云桶会留下过期 / 违规备份，只删镜像会在恢复时露出旧文件。
// 且必须显式二次确认——对象键仍可能被别的业务行引用，误删会造出整片裂图。
func TestObjectDeleteCleansBothCopiesWithConfirmation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	seedDefaultCompany(t, db)
	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true, JWTKey: "test-jwt-key",
		AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{Dir: cfg.UploadDir, AllowExt: storage.DefaultAllowExt}
	dir := t.TempDir()
	key, err := storage.ObjectKeyFor(storage.ScopeTenant, 1, "old.mp4")
	require.NoError(t, err)
	cloud := &mirrorFakeCloud{
		objects: map[string][]byte{key: []byte("old")}, deleted: map[string]bool{},
	}
	res := testsupport.NewStaticResolver(cloud, "minio")
	r := buildEngine(t, db, cfg, up, nil, res)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)
	old := mirrorManager
	mirrorManager = mirror.New(db, testsupport.NewStaticResolver(cloud, "minio"), &model.StorageProfile{LocalDir: dir})
	t.Cleanup(func() { mirrorManager = old })
	require.NoError(t, setting.Upsert("local_mirror_enabled", "true"))
	require.NoError(t, mirrorManager.Copy(1, key))
	mirrorPath := filepath.Join(dir, filepath.FromSlash(key))
	_, err = os.Stat(mirrorPath)
	require.NoError(t, err)

	_, body := c.do("POST", "/api/admin/storage/object/delete", map[string]any{"key": key}, token)
	require.EqualValues(t, 1, body["code"], "没有二次确认不能删")
	require.False(t, cloud.deleted[key])
	_, err = os.Stat(mirrorPath)
	require.NoError(t, err, "未确认时镜像也不能被动过")

	_, body = c.do("POST", "/api/admin/storage/object/delete", map[string]any{"key": key, "confirm": "DELETE"}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	require.True(t, cloud.deleted[key], "云桶对象必须删除")
	_, err = os.Stat(mirrorPath)
	require.True(t, os.IsNotExist(err), "本地镜像必须同时删除")
}

// handlerSplitResolver 区分「平台实例」与「全局默认绑定」：
// 平台对象（p/）写在 Platform() 里，而 For(0) 会解析到另一套档案。
type handlerSplitResolver struct {
	platform storage.Uploader
	byTenant storage.Uploader
}

func (s *handlerSplitResolver) For(int64) (storage.Uploader, error) { return s.byTenant, nil }
func (s *handlerSplitResolver) Platform() storage.Uploader          { return s.platform }
func (s *handlerSplitResolver) DriverFor(int64) string              { return "minio" }

// 删除平台对象必须删 Platform() 里的那一份。用 For(0) 会「成功」删掉
// 全局默认绑定桶里的同名键，真正的小程序码 / 素材对象仍在——管理员看到
// 「已删除」，实际内容还挂在 CDN 上。
func TestObjectDeleteUsesPlatformInstanceForPlatformKeys(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	seedDefaultCompany(t, db)
	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true, JWTKey: "test-jwt-key",
		AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{Dir: cfg.UploadDir, AllowExt: storage.DefaultAllowExt}
	key, err := storage.ObjectKeyFor(storage.ScopePlatform, 0, "wxacode/9.jpg")
	require.NoError(t, err)

	platformCloud := &mirrorFakeCloud{objects: map[string][]byte{key: []byte("p")}, deleted: map[string]bool{}}
	defaultCloud := &mirrorFakeCloud{objects: map[string][]byte{key: []byte("wrong")}, deleted: map[string]bool{}}
	res := &handlerSplitResolver{platform: platformCloud, byTenant: defaultCloud}
	r := buildEngine(t, db, cfg, up, nil, res)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/storage/object/delete",
		map[string]any{"key": key, "confirm": "DELETE"}, token)
	require.EqualValues(t, 0, body["code"], body["msg"])
	require.True(t, platformCloud.deleted[key], "必须删平台实例里的对象")
	require.False(t, defaultCloud.deleted[key], "不能去全局默认绑定桶里删同名键")
}
