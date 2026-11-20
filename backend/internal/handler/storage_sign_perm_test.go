package handler

// 私有桶限时签名接口（/api/admin/storage/sign 与 /api/employee/storage/sign）的归属守门。
//
// 这个接口的 urls 完全由客户端提供。早先反推出 key 后直接拿 `res.For(调用者 cid)`
// 签名，不校验 key 属不属于这个 cid。平台盘是私有桶时，平台实例持有全部 t/<cid>/
// 对象的签名权，任何登录用户都能拿公开名片页上别家的 URL 换到签名地址。
//
// 两条纪律一起钉：
//  1. 归属不符 → code=403（不是 code:1，「没权限」和「签不了」处置不同）；
//  2. **按对象归属选实例**（t/<cid> → For(cid)，p/ → Platform()），不按调用者身份选，
//     否则签名凭据对不上对象所在的桶，端上照样 404 且没有任何报错。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"weichuan/internal/config"
	"weichuan/internal/storage"
)

type namedSigner struct {
	*storage.LocalUploader
	name string
}

func (s *namedSigner) SignGetURL(key string, _ time.Duration) (string, error) {
	return s.name + ":" + key, nil
}

type signStubResolver struct {
	platform storage.Uploader
	tenants  map[int64]storage.Uploader
}

func (r *signStubResolver) For(cid int64) (storage.Uploader, error) {
	if up, ok := r.tenants[cid]; ok {
		return up, nil
	}
	return r.platform, nil
}

func (r *signStubResolver) Platform() storage.Uploader { return r.platform }
func (r *signStubResolver) DriverFor(int64) string     { return "minio" }

func TestSignHandlerChecksObjectOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)

	plat := &namedSigner{LocalUploader: &storage.LocalUploader{}, name: "platform"}
	t5 := &namedSigner{LocalUploader: &storage.LocalUploader{}, name: "tenant5"}
	t9 := &namedSigner{LocalUploader: &storage.LocalUploader{}, name: "tenant9"}
	res := &signStubResolver{platform: plat, tenants: map[int64]storage.Uploader{5: t5, 9: t9}}
	h := signHandler(res, config.Config{UploadBaseURL: "https://cdn.example.com/uploads"})

	cases := []struct {
		name string
		cid  int64
		key  string
		code int
		want string
	}{
		{"签自己的租户对象", 5, "t/5/a.jpg", 0, "tenant5:t/5/a.jpg"},
		{"签别家租户对象：403", 5, "t/9/b.jpg", 403, ""},
		{"平台对象用平台实例签", 5, "p/library/c.png", 0, "platform:p/library/c.png"},
		{"租户不得签历史裸键：403", 5, "old.jpg", 403, ""},
		{"平台超管可代签租户对象", 0, "t/9/b.jpg", 0, "tenant9:t/9/b.jpg"},
		{"平台超管可签历史裸键", 0, "old.jpg", 0, "platform:old.jpg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string][]string{"urls": {tc.key}})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/api/admin/storage/sign?companyId=" + strconv.FormatInt(tc.cid, 10)
			c.Request = httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			h(c)

			var resp struct {
				Code int `json:"code"`
				Data struct {
					Signed map[string]string `json:"signed"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "响应应为 JSON：%s", w.Body.String())
			require.Equal(t, tc.code, resp.Code, "%s", w.Body.String())
			require.Equal(t, tc.want, resp.Data.Signed[tc.key])
		})
	}
}
