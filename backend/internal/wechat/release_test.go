package wechat

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 版本发布四个接口的测试。重点盯三类错：
//  1. 参数漏传 / 传错（auditid 没带上是最常见的接线错误）；
//  2. errcode 没被翻成 error（调用方以为成功了，往下走就把状态机推错了）；
//  3. token 缓存串号——按 appid 分键的那个回归测试在文件末尾，
//     那是「把代码发到别家小程序」级别的 bug，必须有钉子钉住。

// ---- 提交审核 ----

func TestSubmitAuditReturnsAuditID(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/wxa/submit_audit", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		// 不传 item_list 时 body 必须是空对象（类目取后台已配的）
		require.Empty(t, body, "不传 item_list 时不应有多余字段")
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok", "auditid": 1234567})
	})

	auditID, err := SubmitAudit(testCfg, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1234567), auditID)
	require.Equal(t, 1, f.hits("/wxa/submit_audit"))
}

func TestSubmitAuditPassesItemList(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		var body struct {
			ItemList []map[string]any `json:"item_list"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Len(t, body.ItemList, 1)
		require.Equal(t, "社交", body.ItemList[0]["first_class"])
		writeJSON(t, w, map[string]any{"errcode": 0, "auditid": 99})
	})

	auditID, err := SubmitAudit(testCfg, []map[string]any{{"first_class": "社交", "second_class": "社区"}})
	require.NoError(t, err)
	require.Equal(t, int64(99), auditID)
}

// errcode 已为 0 却没返回 auditid：微信偶发半截响应。若当成成功，
// 上层会把 audit_id=0 存下来，之后查状态永远查不到，且看不出是哪一步坏的。
func TestSubmitAuditMissingAuditIDIsError(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})

	_, err := SubmitAudit(testCfg, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auditid")
}

// 提审失败要把错误码翻成 APIError：上层要区分「类目没配」和「频繁提交」
// （后者不该重试，前者要人去后台配），只靠 errmsg 字符串判断不可靠。
func TestSubmitAuditErrCodeIsAPIError(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 9402202, "errmsg": "请勿频繁提交"})
	})

	_, err := SubmitAudit(testCfg, nil)
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr), "errcode 应能被 errors.As 取出")
	require.Equal(t, 9402202, apiErr.Code)
}

// ---- 查询审核状态 ----

func TestGetAuditStatusAllEnumValues(t *testing.T) {
	cases := []struct {
		status     int
		reason     string
		screenshot string
	}{
		{0, "", ""}, // 审核成功
		{1, "帐号信息不合规范", "m1|m2|m3"}, // 审核被拒绝
		{2, "", ""},     // 审核中
		{3, "", ""},     // 已撤回
		{4, "延后审核", ""}, // 审核延后
	}
	for _, c := range cases {
		t.Run(map[int]string{0: "成功", 1: "拒绝", 2: "审核中", 3: "已撤回", 4: "延后"}[c.status], func(t *testing.T) {
			f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/token" {
					writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
					return
				}
				require.Equal(t, "/wxa/get_auditstatus", r.URL.Path)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				// auditid 必须真的带上——漏传是这类接口最常见的接线错误
				require.Equal(t, float64(777), body["auditid"])
				writeJSON(t, w, map[string]any{"errcode": 0, "status": c.status, "reason": c.reason, "screenshot": c.screenshot})
			})
			st, err := GetAuditStatus(testCfg, 777)
			require.NoError(t, err)
			require.Equal(t, c.status, st.Status)
			require.Equal(t, c.reason, st.Reason)
			require.Equal(t, c.screenshot, st.Screenshot)
			require.Equal(t, 1, f.hits("/wxa/get_auditstatus"))
		})
	}
}

// auditid 无效时必须在本地就拒掉，不能带着 0 去打微信——微信只会回
// 85012「invalid audit id」，排查时看不出其实是自己没传。
func TestGetAuditStatusRejectsInvalidAuditID(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("不应发起请求") })

	_, err := GetAuditStatus(testCfg, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auditid")
}

// ---- 发布 / 撤回 ----

func TestReleaseSuccess(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/wxa/release", r.URL.Path)
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})
	require.NoError(t, Release(testCfg))
	require.Equal(t, 1, f.hits("/wxa/release"))
}

func TestUndoCodeAuditSuccess(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		require.Equal(t, "/wxa/undo_code_audit", r.URL.Path)
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})
	require.NoError(t, UndoCodeAudit(testCfg))
	require.Equal(t, 1, f.hits("/wxa/undo_code_audit"))
}

// 发布前没提审 / 提审没过：微信会拒。错误码必须透上来，让上层告诉用户
// 「先提审并通过」，而不是干巴巴一句「发布失败」。
func TestReleaseErrCode(t *testing.T) {
	newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/token" {
			writeJSON(t, w, map[string]any{"access_token": "tk", "expires_in": 7200})
			return
		}
		writeJSON(t, w, map[string]any{"errcode": 85085, "errmsg": "invalid release"})
	})
	err := Release(testCfg)
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 85085, apiErr.Code)
}

// ---- access_token 失效重试 ----

// 第一次调 40001 → 刷新 token → 重试应成功，且只多打一次 token 接口。
// 这条支线漏了的话，表现为「每隔一段时间（token 过期）发布就失败一次」，
// 很难和微信侧故障区分开。
func TestPostWXRetriesOnInvalidToken(t *testing.T) {
	var n int
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			writeJSON(t, w, map[string]any{"access_token": "tk-" + string(rune('a'+n)), "expires_in": 7200})
		case "/wxa/release":
			n++
			if n == 1 {
				writeJSON(t, w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})
				return
			}
			writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	require.NoError(t, Release(testCfg))
	require.Equal(t, 2, f.hits("/cgi-bin/token"), "失效后应重新取一次 token")
	require.Equal(t, 2, f.hits("/wxa/release"), "业务接口应重试一次")
}

// ---- token 缓存必须按 appid 分键（回归测试）----

// 背景（本次修复）：tokenCache 原先是一个全局变量，不按 appid 分键。
// 平台模式下只有一个小程序，碰巧能跑；企业独立版是「一个后端服务 N 个小程序」，
// A 企业来调接口时若命中 B 企业的 token，微信会拿 B 的身份执行——
// 生成出来的码属于别的小程序；版本发布更严重，等于把代码发到别家去。
//
// 这个测试故意用两个 appid 交替调用，若缓存退化回全局变量，
// 第二个 appid 会拿到第一个的 token，下面两处断言必红。
func TestAccessTokenCacheIsKeyedByAppID(t *testing.T) {
	f := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/cgi-bin/token", r.URL.Path)
		// 按 appid 回不同的 token：这样「拿错 token」一定被抓到
		writeJSON(t, w, map[string]any{
			"access_token": "token-for-" + r.URL.Query().Get("appid"),
			"expires_in":   7200,
		})
	})

	cfgA := Config{AppID: "wxaaaa0000000001", AppSecret: "secret-a"}
	cfgB := Config{AppID: "wxbbbb0000000002", AppSecret: "secret-b"}

	tkA, err := GetAccessToken(cfgA)
	require.NoError(t, err)
	require.Equal(t, "token-for-wxaaaa0000000001", tkA)

	tkB, err := GetAccessToken(cfgB)
	require.NoError(t, err)
	require.Equal(t, "token-for-wxbbbb0000000002", tkB, "B 企业拿到了 A 企业的 token——缓存串号了")

	// 再取一次 A：必须还是 A 自己的（走缓存，不应再打接口）
	tkA2, err := GetAccessToken(cfgA)
	require.NoError(t, err)
	require.Equal(t, tkA, tkA2)
	require.Equal(t, 2, f.hits("/cgi-bin/token"), "两个 appid 各取一次 token，不应多打")

	// 业务请求也要带对 token
	f2 := newFakeWX(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-for-wxaaaa0000000001", r.URL.Query().Get("access_token"))
		writeJSON(t, w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})
	require.NoError(t, Release(cfgA))
	require.Equal(t, 1, f2.hits("/wxa/release"))
}
