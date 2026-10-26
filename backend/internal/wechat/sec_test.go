package wechat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 文本内容安全（/wxa/msg_sec_check v2）的测试。
//
// 盯三类错：
//  1. 参数没按 v2 传（version / scene / openid 少一个，微信就按 v1 语义判，
//     结论可能从 risky 变成 pass——等于静默失效）；
//  2. suggest 没被正确读出来（把 risky 读成空串当成 pass，违规内容直接放行）；
//  3. 87014 被当成「调用失败」抛出去——上层按 fail-open 处理，同样放行。

func TestMsgSecCheckPass(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/wxa/msg_sec_check", r.URL.Path)

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "正常内容", body["content"])
		require.EqualValues(t, 2, body["version"], "v2 必须显式传 version=2，否则微信按 v1 判")
		require.EqualValues(t, SceneProfile, body["scene"])
		require.Equal(t, "openid-abc", body["openid"])

		writeJSON(t, w, map[string]any{
			"errcode":  0,
			"errmsg":   "ok",
			"trace_id": "trace-1",
			"result":   map[string]any{"suggest": "pass", "label": 100},
		})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "正常内容", SceneProfile)
	require.NoError(t, err)
	require.Equal(t, "pass", v.Suggest)
	require.False(t, v.Risky())
	require.Equal(t, "trace-1", v.TraceID)
	require.Equal(t, 1, f.hits("/wxa/msg_sec_check"))
}

func TestMsgSecCheckRisky(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{
			"errcode":  0,
			"trace_id": "trace-2",
			"result":   map[string]any{"suggest": "risky", "label": 20001},
		})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "违规内容", SceneProfile)
	require.NoError(t, err)
	require.True(t, v.Risky())
	require.Equal(t, 20001, v.Label)
}

func TestMsgSecCheckReview(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 0, "result": map[string]any{"suggest": "review", "label": 20002}})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "疑似违规", SceneProfile)
	require.NoError(t, err)
	require.True(t, v.Review())
	require.False(t, v.Risky())
}

// 87014 必须被翻成「命中」而不是 error：抛成 error 会被上层 fail-open 放行。
func TestMsgSecCheckErrCode87014IsRiskyNotError(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 87014, "errmsg": "content risky"})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "违规内容", SceneProfile)
	require.NoError(t, err, "87014 是「命中违规」，不是调用失败")
	require.True(t, v.Risky())
	require.Equal(t, 87014, v.Label)
}

// 没有 result 字段（接口降级 / 老账号）时按通过处理：
// 否则一次接口降级会把所有正常提交一起拦下，用户改多少遍都没用。
func TestMsgSecCheckMissingResultTreatedAsPass(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "正常内容", SceneProfile)
	require.NoError(t, err)
	require.Equal(t, "pass", v.Suggest)
}

// 空内容不发请求：内容安全是「有内容才检」，白跑一次外网调用只会增加延迟。
func TestMsgSecCheckEmptyContentSkipsRequest(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 0})
	})

	v, err := MsgSecCheck(testCfg, "openid-abc", "  ", SceneProfile)
	require.NoError(t, err)
	require.Equal(t, "pass", v.Suggest)
	require.Equal(t, 0, f.hits("/wxa/msg_sec_check"))
}

// 没有 openid 时本地报错、不发请求：v2 接口 openid 必填，
// 空着打过去必然是参数错误，只会把「没检测」伪装成「检测失败」。
func TestMsgSecCheckNoOpenIDFailsLocally(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errcode": 0})
	})

	_, err := MsgSecCheck(testCfg, "", "正常内容", SceneProfile)
	require.Error(t, err)
	require.Equal(t, 0, f.hits("/wxa/msg_sec_check"))
}

// 场景值缺省回落「资料」：0 不是合法场景值，直传会被微信拒。
func TestMsgSecCheckDefaultsScene(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.EqualValues(t, SceneProfile, body["scene"])
		writeJSON(t, w, map[string]any{"errcode": 0, "result": map[string]any{"suggest": "pass"}})
	})

	_, err := MsgSecCheck(testCfg, "openid-abc", "内容", 0)
	require.NoError(t, err)
}
