package wechat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 订阅消息发送的测试。重点盯四类错：
//  1. data 的包装形状写错（微信要 {"thing1":{"value":"x"}}，直接给字符串会 47003）；
//  2. page 带前导斜杠（微信要 pages/x/x，写成 /pages/x/x 会 41030，
//     而且**只在真机上暴露**——本地永远发得出去）；
//  3. errcode 没被翻成 error（调用方以为通知到了，实际用户什么都没收到）；
//  4. 43101 被当成故障（它是「用户没同意」，属正常，不该告警）。

func TestSendSubscribeShape(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/cgi-bin/message/subscribe/send", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		require.Equal(t, "o-user", body["touser"])
		require.Equal(t, "TPL-1", body["template_id"])
		// 前导斜杠必须被去掉
		require.Equal(t, "pages/employee/me/me", body["page"])
		require.Equal(t, "trial", body["miniprogram_state"])

		// 关键：每个字段都要包成 {"value": ...}，不是裸字符串
		data, ok := body["data"].(map[string]any)
		require.True(t, ok, "data 必须是对象")
		require.Equal(t, map[string]any{"value": "名片开通申请"}, data["thing1"])
		require.Equal(t, map[string]any{"value": "审核通过"}, data["phrase2"])

		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})

	err := SendSubscribe(testCfg, SubscribeMsg{
		ToUser:     "o-user",
		TemplateID: "TPL-1",
		Page:       "/pages/employee/me/me",
		State:      "trial",
		Data:       map[string]string{"thing1": "名片开通申请", "phrase2": "审核通过"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, f.hits("/cgi-bin/message/subscribe/send"))
}

// page 留空时不能带这个字段：微信会把它当空字符串校验并回 41030，
// 而「不传」才是「打开首页」。
func TestSendSubscribeOmitsEmptyPage(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.NotContains(t, body, "page")
		require.NotContains(t, body, "miniprogram_state")
		writeJSON(t, w, map[string]any{"errcode": 0})
	})

	require.NoError(t, SendSubscribe(testCfg, SubscribeMsg{
		ToUser: "o", TemplateID: "T", Data: map[string]string{"thing1": "x"},
	}))
}

// 参数不全时本地就挡：少 openid / 少模板 ID / 少数据都别去打外网。
func TestSendSubscribeRejectsIncomplete(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("不应发起请求") })

	cases := []struct {
		name string
		msg  SubscribeMsg
		want string
	}{
		{"缺 openid", SubscribeMsg{TemplateID: "T", Data: map[string]string{"thing1": "x"}}, "openid"},
		{"缺模板", SubscribeMsg{ToUser: "o", Data: map[string]string{"thing1": "x"}}, "模板 ID"},
		{"缺数据", SubscribeMsg{ToUser: "o", TemplateID: "T"}, "模板数据"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := SendSubscribe(testCfg, c.msg)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
		})
	}
}

// 未配凭据：本地报错，不打外网。
func TestSendSubscribeRejectsUnconfigured(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("不应发起请求") })
	err := SendSubscribe(Config{}, SubscribeMsg{
		ToUser: "o", TemplateID: "T", Data: map[string]string{"thing1": "x"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "WX_APPID")
}

// errcode 非 0 必须变成 error：吞掉的话调用方以为通知到了。
func TestSendSubscribeSurfacesErrCode(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 47003, "errmsg": "argument invalid"})
	})

	err := SendSubscribe(testCfg, SubscribeMsg{
		ToUser: "o", TemplateID: "T", Data: map[string]string{"thing1": "x"},
	})
	require.Error(t, err)
	require.Equal(t, 47003, SubscribeErrCode(err))
	require.False(t, IsSubscribeNotGranted(err))
	// 提示要能直接指向「去改哪个配置」，而不是让运维去猜 47003 是什么
	require.Contains(t, SubscribeErrHint(err), "模板字段")
}

// 43101 = 用户没同意 / 配额用完。这是**正常现象**（一次性订阅的固有约束），
// 必须能被识别出来，否则每次审批都会刷一条 error 日志，真正的故障就被淹了。
func TestSubscribeNotGranted(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 43101, "errmsg": "user refuse to accept the msg"})
	})

	err := SendSubscribe(testCfg, SubscribeMsg{
		ToUser: "o", TemplateID: "T", Data: map[string]string{"thing1": "x"},
	})
	require.Error(t, err)
	require.True(t, IsSubscribeNotGranted(err))
	require.Contains(t, SubscribeErrHint(err), "一次性订阅")
}

// 非微信业务错误（网络断了）取不到 errcode，不能误判成「用户没订阅」。
func TestSubscribeErrCodeNonAPIError(t *testing.T) {
	require.Equal(t, 0, SubscribeErrCode(errPlain{}))
	require.False(t, IsSubscribeNotGranted(errPlain{}))
	require.Empty(t, SubscribeErrHint(errPlain{}))
}

type errPlain struct{}

func (errPlain) Error() string { return "network down" }

// token 过期（40001）要刷新重试一次：access_token 是多接口共用的，
// 别处刚刷掉时这次发送不该白白丢掉用户唯一的一次配额。
func TestSendSubscribeRetriesOnStaleToken(t *testing.T) {
	var sendHits int
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			// 每次取 token 都给新值，便于区分「有没有重新取」
			writeJSON(t, w, map[string]any{"access_token": "tk-new", "expires_in": 7200})
		case "/cgi-bin/message/subscribe/send":
			sendHits++
			if sendHits == 1 {
				writeJSON(t, w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})
				return
			}
			require.Equal(t, "tk-new", r.URL.Query().Get("access_token"))
			writeJSON(t, w, map[string]any{"errcode": 0})
		default:
			t.Fatalf("意外请求 %s", r.URL.Path)
		}
	})

	require.NoError(t, SendSubscribe(testCfg, SubscribeMsg{
		ToUser: "o", TemplateID: "T", Data: map[string]string{"thing1": "x"},
	}))
	require.Equal(t, 2, sendHits)
}
