package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"weichuan/internal/storage"
)

// 私有桶读侧守门。
//
// 这里要钉住的不是「能签出 URL」（那条能力 signHandler 已经守了），
// 而是私有桶最容易出现的一类静默故障：**出参路径没变、读侧却没接上**。
// 只要中间件漏签，访客拿到的是一个 403 或 404 的静态文件，后台上传却全部成功。

func TestPrivateKeyFromPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		cid  int64
		key  string
		ok   bool
	}{
		{"正常租户对象", "/uploads/t/5/a.jpg", 5, "t/5/a.jpg", true},
		{"嵌套目录", "/uploads/t/12/x/y_thumb.png", 12, "t/12/x/y_thumb.png", true},
		{"平台对象不在此列", "/uploads/p/library/a.png", 0, "", false},
		{"历史裸键不在此列", "/uploads/a.jpg", 0, "", false},
		{"缺对象名", "/uploads/t/5/", 0, "", false},
		{"缺 cid", "/uploads/t//a.jpg", 0, "", false},
		{"cid 非数字", "/uploads/t/abc/a.jpg", 0, "", false},
		{"cid 为 0", "/uploads/t/0/a.jpg", 0, "", false},
		{"路径穿越", "/uploads/t/5/../9/a.jpg", 0, "", false},
		{"反斜杠", `/uploads/t/5/\9\a.jpg`, 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cid, key, ok := privateKeyFromPath(tc.path)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.cid, cid)
			require.Equal(t, tc.key, key)
		})
	}
}

type failingSigner struct {
	*storage.LocalUploader
}

func (s *failingSigner) SignGetURL(string, time.Duration) (string, error) {
	return "", errors.New("sign failed")
}

type privateStubResolver struct {
	tenants map[int64]storage.Uploader
	dead    map[int64]bool
}

func (r *privateStubResolver) For(cid int64) (storage.Uploader, error) {
	if r.dead[cid] {
		return nil, storage.ErrNoUploader
	}
	if up, ok := r.tenants[cid]; ok {
		return up, nil
	}
	return nil, storage.ErrNoUploader
}

func (r *privateStubResolver) Platform() storage.Uploader { return &storage.LocalUploader{} }
func (r *privateStubResolver) DriverFor(int64) string     { return "minio" }

func TestPrivateObjectRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "t", "9"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "t", "9", "a.jpg"), []byte("public-bucket-file"), 0o644))

	res := &privateStubResolver{tenants: map[int64]storage.Uploader{
		5: &namedSigner{LocalUploader: &storage.LocalUploader{}, name: "tenant5"},
		6: &failingSigner{LocalUploader: &storage.LocalUploader{}},
		7: &storage.LocalUploader{}, // 有实例但没有 Signer
	}, dead: map[int64]bool{8: true}}

	old := privateReadFn
	t.Cleanup(func() { privateReadFn = old })
	privateReadFn = func(cid int64) bool { return cid == 5 || cid == 6 || cid == 7 || cid == 8 }

	r := gin.New()
	r.Use(PrivateObjectRedirect(res))
	r.Static("/uploads", dir)

	cases := []struct {
		name     string
		path     string
		status   int
		location string
		wantBody string
		cacheCtl string
	}{
		{"私有桶：现签后 302", "/uploads/t/5/a.jpg", http.StatusFound, "tenant5:t/5/a.jpg", "", "no-store"},
		{"公开租户：照旧走静态文件", "/uploads/t/9/a.jpg", http.StatusOK, "", "public-bucket-file", ""},
		{"签名失败：502 且不回显错误", "/uploads/t/6/a.jpg", http.StatusBadGateway, "", "", ""},
		{"没有签名能力：500", "/uploads/t/7/a.jpg", http.StatusInternalServerError, "", "", ""},
		{"实例构造失败：502", "/uploads/t/8/a.jpg", http.StatusBadGateway, "", "", ""},
		{"平台对象：不进签名分支", "/uploads/p/library/a.png", http.StatusNotFound, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.location, w.Header().Get("Location"))
			if tc.cacheCtl != "" {
				require.Equal(t, tc.cacheCtl, w.Header().Get("Cache-Control"))
			}
			if tc.wantBody != "" {
				require.Contains(t, w.Body.String(), tc.wantBody)
			}
		})
	}

	// 反向验证的形态：把私有判定关掉，302 必须消失，回落到静态 404。
	privateReadFn = func(int64) bool { return false }
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/uploads/t/5/a.jpg", nil))
	require.Equal(t, http.StatusNotFound, w.Code, "判定一关就必须回到静态路径，否则守门是空转")
	require.Empty(t, w.Header().Get("Location"))
}

func TestPrivateObjectRedirectSkipsWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := privateReadFn
	t.Cleanup(func() { privateReadFn = old })
	privateReadFn = func(int64) bool { return true }

	res := &privateStubResolver{tenants: map[int64]storage.Uploader{}}
	r := gin.New()
	r.Use(PrivateObjectRedirect(res))
	r.POST("/uploads", func(c *gin.Context) { c.Status(http.StatusTeapot) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/uploads", nil))
	require.Equal(t, http.StatusTeapot, w.Code, "非读请求不能被签名中间件吃掉")
}
