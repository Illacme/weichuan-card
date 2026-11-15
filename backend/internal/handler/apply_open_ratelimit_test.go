package handler

// /api/apply/open 的限流守门。
//
// 这个端点原本是「公开 + 写库 + 调微信内容安全」，却没有任何限流；同链路的
// 匿名头像上传早有 uploadLimiter，但那只挡文件上传，提交申请是另一个入口。
// 漏洞的形态是「一切正常，只是能被脚本无限调用」——所以这里既测运行时 429，
// 也用源码级守门钉住「limiter 真的挂到了路由上」：只定义函数、没挂进
// r.POST(...) 的话，运行时测试必红，但要让后来改代码的人一眼看到是哪条约束被破。
//
// 反向验证要点（改动后请照做一遍）：
//   1. 去掉 r.POST("/api/apply/open", ...) 的 applyOpenLimiter → 静态守门 + 429 用例红；
//   2. 把 WithKey 的 "apply-open:" 前缀删掉 → 桶键隔离用例红（登录被申请额度打挂）；
//   3. 把 NewRateLimiter 的 fallback 改成 0 → 「填 0 不能关防护」用例红。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// postApplyOpen 用指定出口 IP 发一次公开开通申请。
// X-Forwarded-For 是 gin 默认信任的代理头，用来区分不同访客 IP。
func postApplyOpen(t *testing.T, r *gin.Engine, ip, phone string) (int, map[string]interface{}) {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"companyId": 1, "name": "申请人", "phone": phone,
		"nickname": "", "title": "", "email": "",
	})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/apply/open", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

// TestApplyOpenRateLimited 超过阈值必须回 429，且**不落库**。
// 只断言 code=429 是不够的：限流器挂在 handler 之后也可能回 429（写已经发生了），
// 所以这里直接数 open_applies 表，证明被挡的请求一次库都没写。
func TestApplyOpenRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)

	orig := applyOpenLimitFn
	applyOpenLimitFn = func() int { return 2 }
	defer func() { applyOpenLimitFn = orig }()

	const ip = "198.51.100.7"
	for i := 1; i <= 2; i++ {
		code, body := postApplyOpen(t, r, ip, fmt.Sprintf("1390000%04d", i))
		require.Equal(t, http.StatusOK, code)
		require.EqualValues(t, 0, body["code"], "第 %d 次应在额度内放行：%v", i, body)
	}
	var cnt int64
	require.NoError(t, db.Model(&model.OpenApply{}).Count(&cnt).Error)
	require.EqualValues(t, 2, cnt, "前两次必须真的落库，否则「放行」是假绿")

	code, body := postApplyOpen(t, r, ip, "13900009999")
	require.Equal(t, http.StatusOK, code, "限流沿用既有约定：HTTP 200 + 信封 code=429")
	require.EqualValues(t, 429, body["code"], "第 3 次必须被限流（阈值 2）")
	require.NoError(t, db.Model(&model.OpenApply{}).Count(&cnt).Error)
	require.EqualValues(t, 2, cnt, "被限流的请求绝不能写库")

	// 换 IP 仍能提交：证明这是按 IP 分桶，不是「一个脚本打挂全站」的全局熔断。
	_, other := postApplyOpen(t, r, "198.51.100.8", "13900008888")
	require.EqualValues(t, 0, other["code"], "别的访客 IP 不该被前一个 IP 的额度牵连：%v", other)
}

// TestApplyOpenRateLimitBucketsAreSeparated 桶键必须带动作前缀。
//
// 只断言「刷满申请后登录/上传还能用」是**测不到承诺**的代理：登录用 login: 前缀、
// 上传用 upload: 前缀，就算申请退化成裸 IP，两边也仍然不撞、用例照样全绿。
// 所以这里先往共享计数表里灌一个「裸 IP」诱饵，把裸键这一行记到远超阈值；
// 申请若真的用裸键，第一次提交就会被这堆计数打挂。再反查表里确实有
// apply-open: 前缀的行，证明额度落在自己那一行上。
func TestApplyOpenRateLimitBucketsAreSeparated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	orig := applyOpenLimitFn
	applyOpenLimitFn = func() int { return 1 }
	defer func() { applyOpenLimitFn = orig }()

	// 诱饵：httptest.NewRequest 的默认出口 IP 是 192.0.2.1，不给 XFF 时就是它。
	shared := dbRateCounter{db: db}
	hits := 0
	for i := 0; i < 25; i++ {
		n, err := shared.Hit("192.0.2.1", time.Minute)
		require.NoError(t, err)
		hits = n
	}
	require.GreaterOrEqual(t, hits, 25,
		"诱饵必须真的写进 rate_limit_hits，否则这条用例测不到桶键是否分家")

	// 申请仍应放行：它用 apply-open:192.0.2.1，与裸 IP 是两行。
	_, body := postApplyOpen(t, r, "", "13700000001")
	require.EqualValues(t, 0, body["code"],
		"申请额度被别人的裸 IP 计数挤掉了（桶键退化成裸 IP？）：%v", body)

	// 数据库层取证：不靠读源码猜，直接确认前缀行存在。
	var rows []model.RateLimitHit
	require.NoError(t, db.Where("bucket_key LIKE ?", "apply-open:%").Find(&rows).Error)
	require.NotEmpty(t, rows, "申请限流必须落带 apply-open: 前缀的桶键")

	// 另一方向：刷满申请额度后，登录与匿名上传仍照常可用。
	for i := 0; i < 3; i++ {
		postApplyOpen(t, r, "", fmt.Sprintf("1370000%04d", i+2))
	}
	_, body = c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"], "申请限流不得牵连登录限流：%v", body)

	code, up := postAnonymousAvatar(t, r, nil)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, up["code"], "申请限流不得牵连同链路的匿名头像上传：%v", up)
}

// TestApplyOpenRateLimitFallbackWhenSettingInvalid 界面把阈值填成 0 / 负数，
// 不能把这道防护关掉（registry 的 Desc 就是这么承诺的）。
func TestApplyOpenRateLimitFallbackWhenSettingInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := setupEngineWithDB(t)

	orig := applyOpenLimitFn
	applyOpenLimitFn = func() int { return 0 }
	defer func() { applyOpenLimitFn = orig }()

	const ip = "198.51.100.9"
	for i := 0; i < defaultApplyOpenRateLimit; i++ {
		_, body := postApplyOpen(t, r, ip, fmt.Sprintf("1360000%04d", i))
		require.EqualValues(t, 0, body["code"], "兜底额度内第 %d 次应放行：%v", i+1, body)
	}
	_, body := postApplyOpen(t, r, ip, "13600009999")
	require.EqualValues(t, 429, body["code"], "填 0 不能关掉限流，必须回落到缺省阈值：%v", body)

	// 量尺自己有读数：缺省值必须是有意义的正额度，否则「10 次内放行」会退化成
	// 「0 次放行」，循环一次都不跑、最后一条 429 也可能来自别的分支。
	require.Greater(t, defaultApplyOpenRateLimit, 0)
	require.LessOrEqual(t, defaultApplyOpenRateLimit, 60, "公开写库端点的缺省额度不该放宽到几十次以上")
}

// TestApplyOpenRouteHasLimiter 源码级守门：limiter 必须挂在路由的**第一个
// handler 之前**。只定义 applyOpenLimiter 却没接进 r.POST，或把它塞进 handler
// body 里当变量用，运行时看起来一切正常，防护却等于没有。
func TestApplyOpenRouteHasLimiter(t *testing.T) {
	src := stripGoComments(readHandlerSource(t, "apply.go"))
	calls := scanRouteCalls(src)
	require.GreaterOrEqual(t, len(calls), 10,
		"apply.go 的路由一条也没扫到：守门空转会让下面全部恒绿，先修扫描器")

	var target []routeCall
	for _, call := range calls {
		if strings.Contains(call.text, `"/api/apply/open"`) {
			target = append(target, call)
		}
	}
	require.Len(t, target, 1, "应恰好有一条 /api/apply/open 路由注册")

	head := target[0].text
	if i := strings.Index(head, "func("); i >= 0 {
		head = head[:i]
	}
	require.Contains(t, head, "applyOpenLimiter(db).Handler()",
		"公开申请路由必须在 handler 之前挂限流中间件：\n%s", target[0].text)

	// 桶键前缀与共享计数同样要盯住：前者防串桶，后者防多副本下阈值翻倍。
	require.Contains(t, src, `"apply-open:" + c.ClientIP()`,
		"桶键必须带 apply-open: 前缀，不能退化成裸 IP")
	require.Contains(t, src, `WithCounter(dbRateCounter{db: db})`,
		"限流计数必须落 rate_limit_hits 表，多副本下阈值才不会翻倍")
}
