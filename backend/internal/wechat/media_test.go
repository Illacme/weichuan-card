package wechat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 图片异步检测（/wxa/media_check_async）的测试。
//
// 盯三件最容易静默出错的事：
//  1. v2 的三个必填项（version / scene / openid）漏传——微信会按 v1 语义判，
//     或者直接回参数错误，两种都是「看起来送检了，其实没检」；
//  2. trace_id 没被读出来——它是回调唯一的对账凭据，丢了这条记录就永久 pending；
//  3. 本地就该拦下的参数（空 URL / 无 openid / 错 media_type）真的发了请求，
//     白耗 token 与外网往返，还把「没送检」伪装成「送检失败」。

func TestMediaCheckAsyncSendsV2Params(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/wxa/media_check_async", r.URL.Path)

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "https://cdn.example.com/a.jpg", body["media_url"])
		require.EqualValues(t, MediaImage, body["media_type"])
		require.EqualValues(t, 2, body["version"], "v2 必须显式传 version=2")
		require.EqualValues(t, SceneProfile, body["scene"])
		require.Equal(t, "openid-abc", body["openid"])

		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok", "trace_id": "trace-img-1"})
	})

	trace, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: MediaImage,
		OpenID:    "openid-abc",
		Scene:     SceneProfile,
	})
	require.NoError(t, err)
	require.Equal(t, "trace-img-1", trace)
	require.Equal(t, 1, f.hits("/wxa/media_check_async"))
}

// 没有 trace_id 就不是「成功」：回调永远对不上，记录会永久停在 pending。
func TestMediaCheckAsyncMissingTraceIDFails(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: MediaImage,
		OpenID:    "openid-abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "trace_id")
}

func TestMediaCheckAsyncEmptyURLFailsLocally(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 0, "trace_id": "x"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "   ",
		MediaType: MediaImage,
		OpenID:    "openid-abc",
	})
	require.Error(t, err)
	require.Equal(t, 0, f.hits("/wxa/media_check_async"))
}

func TestMediaCheckAsyncNoOpenIDFailsLocally(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 0, "trace_id": "x"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: MediaImage,
	})
	require.Error(t, err)
	require.Equal(t, 0, f.hits("/wxa/media_check_async"))
}

// media_type 只认 1/2：0 或 3 打过去是参数错误，本地拦下更省事也更好排查。
func TestMediaCheckAsyncBadMediaTypeFailsLocally(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 0, "trace_id": "x"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: 0,
		OpenID:    "openid-abc",
	})
	require.Error(t, err)
	require.Equal(t, 0, f.hits("/wxa/media_check_async"))
}

func TestMediaCheckAsyncDefaultsScene(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.EqualValues(t, SceneProfile, body["scene"])
		writeJSON(t, w, map[string]any{"errcode": 0, "trace_id": "t"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: MediaImage,
		OpenID:    "openid-abc",
		Scene:     0,
	})
	require.NoError(t, err)
}

// 微信侧业务错误必须回传为 error（而不是被当成「送检成功」）：
// 否则会落一条 trace_id 为空、永远等不到回调的 pending 记录。
func TestMediaCheckAsyncAPIError(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 61010, "errmsg": "media_url invalid"})
	})

	_, err := MediaCheckAsync(testCfg, MediaCheckReq{
		MediaURL:  "https://cdn.example.com/a.jpg",
		MediaType: MediaImage,
		OpenID:    "openid-abc",
	})
	require.Error(t, err)
	var ae *APIError
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 61010, ae.Code)
}
