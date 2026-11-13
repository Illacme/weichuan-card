package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stubSigner 满足 storage.Uploader + storage.Signer，仅供 signHandler 测试。
type stubSigner struct {
	*storage.LocalUploader
}

func newStubSigner() *stubSigner {
	return &stubSigner{LocalUploader: &storage.LocalUploader{}}
}

func (stubSigner) SignGetURL(key string, _ time.Duration) (string, error) {
	return "SIGNED:" + key, nil
}

// TestSignHandlerEmptyBaseFix 钉住 signHandler 改用 ObjectKeyFromURL 后，
// UploadBaseURL 为空时 "/uploads/x.jpg" 正确反推成 "x.jpg"，
// 而不是旧实现的 "uploads/x.jpg"（多一层目录，会写到错误位置）。
func TestSignHandlerEmptyBaseFix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{UploadBaseURL: ""}
	// 第六十一轮起 signHandler 拿的是 Resolver：桩用 StaticResolver 包一层，
	// 这样本用例仍然只验证「URL → key 反推」这一件事，不被租户分流干扰。
	h := signHandler(storage.NewStaticResolver(newStubSigner(), ""), cfg)

	body, _ := json.Marshal(map[string][]string{"urls": {"/uploads/x.jpg"}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/employee/storage/sign", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Signed map[string]string `json:"signed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "响应应为 JSON")
	require.Equal(t, 0, resp.Code, "应成功解析")
	require.Equal(t, "SIGNED:x.jpg", resp.Data.Signed["/uploads/x.jpg"],
		"空 base 下 /uploads/x.jpg 必须反推成 x.jpg（旧实现会错截成 uploads/x.jpg）")
}
