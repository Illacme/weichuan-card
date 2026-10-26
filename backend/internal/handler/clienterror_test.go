package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func clientErrorEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterClientError(r)
	return r
}

func TestClientErrorReceivesAndLogs(t *testing.T) {
	r := clientErrorEngine()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	body := `{"message":"boom","stack":"at foo (a.js:1:2)","url":"https://x/y","level":"error","requestId":"req-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/client-error", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)

	out := buf.String()
	require.Contains(t, out, "[CLIENT-ERROR]")
	require.Contains(t, out, "reqId=\"req-123\"")
	require.Contains(t, out, "msg=\"boom\"")
	require.Contains(t, out, "stack=\"at foo (a.js:1:2)\"")
}

func TestClientErrorRejectsEmpty(t *testing.T) {
	r := clientErrorEngine()
	// 既无 message 也无 stack：视为垃圾直接拒，不写日志
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	body := `{"level":"error","url":"https://x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/client-error", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "\"code\":1")
	require.NotContains(t, buf.String(), "[CLIENT-ERROR]")
}

func TestClientErrorRejectsBadJSON(t *testing.T) {
	r := clientErrorEngine()
	req := httptest.NewRequest(http.MethodPost, "/api/client-error", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Contains(t, w.Body.String(), "\"code\":1")
}

func TestClientErrorRateLimited(t *testing.T) {
	r := clientErrorEngine()
	// 同一 X-Forwarded-For 在窗口内连发超过阈值后应返回 429；
	// 上限是包级常量 60，这里发 70 条确保触发（与同包其它用例互不干扰：用独立 XFF）。
	const ip = "203.0.113.9"
	limited := 0
	for i := 0; i < 70; i++ {
		body := `{"message":"x` + string(rune('a'+i%26)) + `","requestId":"r"}`
		req := httptest.NewRequest(http.MethodPost, "/api/client-error", strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", ip)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "\"code\":429") {
			limited++
		}
	}
	require.GreaterOrEqual(t, limited, 1, "窗口内高频上报应被限流")
}
