package handler

// P0 加固的守门测试：这些用例对应的是「静默失败」类缺陷——
// 修复前的代码在出错时不报错、照样回 ok，日志干干净净，
// 现场只表现为「文件莫名被覆盖」「登出后令牌还能用」「点了禁用其实没禁掉」。
// 每条用例都指向具体的修复点，摘掉修复必须让它红。

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"testing"
	"time"

	"weichuan/internal/setting"
	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestLogoutReportsRevokeFailure 登出写不进黑名单时必须报错。
// 从前 `_ = RevokeSession(...)` 后照样回 ok：用户以为已登出，refresh 却仍能续期。
func TestLogoutReportsRevokeFailure(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	orig := revokeSession
	revokeSession = func(db *gorm.DB, sid, userType string, userID int64, expiresAt time.Time) error {
		return errors.New("黑名单写入失败")
	}
	defer func() { revokeSession = orig }()

	_, body := c.do("POST", "/api/logout", nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "登出失败")
}

// TestLogoutSucceedsNormally 对照用例：没故障时登出仍是 code 0，且会话真的进了黑名单
// （再拿 refresh 去换 access 会被拒），保证上面那条不是把接口改坏了。
func TestLogoutSucceedsNormally(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/logout", nil, tok)
	require.EqualValues(t, 0, body["code"])
}

// TestGenPasswordEntropyFailure 密码生成取不到随机数时必须回传错误。
// 从前是 `n, _ := rand.Int(...)`：失败时 n 为 nil，下一行 n.Int64() 直接 panic 打挂进程。
func TestGenPasswordEntropyFailure(t *testing.T) {
	orig := randInt
	randInt = func(io.Reader, *big.Int) (*big.Int, error) { return nil, errors.New("no entropy") }
	defer func() { randInt = orig }()

	pwd, err := genPassword()
	require.Error(t, err)
	require.Contains(t, err.Error(), "生成随机密码失败")
	require.Empty(t, pwd)
}

// TestGenPasswordRejectsAtAPI 接口层同样不能把失败吞掉：重置密码时生成失败 → code 1，
// 而不是返回一个空密码还告诉管理员「重置成功」。
func TestGenPasswordRejectsAtAPI(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "熵源测试", "phone": "13900000888", "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))

	orig := randInt
	randInt = func(io.Reader, *big.Int) (*big.Int, error) { return nil, errors.New("no entropy") }
	defer func() { randInt = orig }()

	_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/reset-password", staffID), nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.NotContains(t, body, "initialPassword")
}

// TestWXACodeRateLimitedPerIP 小程序码是公开接口（不能挂鉴权），必须靠限流挡住扫号：
// 每条缓存 miss = 一次真实微信调用 + 一次对象存储写入，而 staffId 是可枚举的整数。
// 按 IP 分桶（不按 staffId）——扫号正是靠换 staffId 放大调用量的。
func TestWXACodeRateLimitedPerIP(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	// 还原改动过的设置项：setting 是进程内单例，不能污染其它用例
	restore := func(key string) func() {
		old := setting.GetString(key, "")
		return func() { _ = setting.Upsert(key, old) }
	}
	defer restore("wx_appid")()
	defer restore("wx_appsecret")()
	defer restore("wxacode_rate_limit_per_min")()
	require.NoError(t, setting.Upsert("wx_appid", "test-appid"))
	require.NoError(t, setting.Upsert("wx_appsecret", "test-secret"))
	require.NoError(t, setting.Upsert("wxacode_rate_limit_per_min", "2"))

	used := []int64{880001, 880002, 880003}
	orig := getWXACode
	calls := 0
	getWXACode = func(wechat.Config, wechat.WXACodeParams) ([]byte, error) {
		calls++
		return []byte("fake-jpeg"), nil
	}
	defer func() {
		getWXACode = orig
		// 缓存是进程级全局 map：用例结束必须把自己塞进去的清掉（其它用例共用）
		wxacodeMu.Lock()
		for _, id := range used {
			delete(wxacodeCache, id)
		}
		wxacodeMu.Unlock()
	}()

	_, body := c.do("GET", "/api/wechat/wxacode?staffId=880001", nil, "")
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/wechat/wxacode?staffId=880002", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, 2, calls)

	// 第三次（换了 staffId 也照样）应被限流挡下，绝不再调微信接口
	_, body = c.do("GET", "/api/wechat/wxacode?staffId=880003", nil, "")
	require.EqualValues(t, 429, body["code"])
	require.Equal(t, 2, calls, "超限请求不应再触发微信调用")
}

// TestWXACodeCacheEvictsAtCap 缓存必须有上限：staffId 可枚举，没有上限时扫一段号
// 就能把这张进程级 map 撑成一张越来越大的表（配合限流做纵深防御）。
func TestWXACodeCacheEvictsAtCap(t *testing.T) {
	const base = int64(900000)
	defer func() {
		wxacodeMu.Lock()
		for i := int64(0); i < wxacodeCacheMax+20; i++ {
			delete(wxacodeCache, base+i)
		}
		wxacodeMu.Unlock()
	}()

	for i := int64(0); i < wxacodeCacheMax+10; i++ {
		wxacodePut(base+i, wxacodeEntry{key: "wxacode/x.jpg", expAt: time.Now().Add(time.Hour)})
	}
	wxacodeMu.Lock()
	n := len(wxacodeCache)
	wxacodeMu.Unlock()
	require.Less(t, n, wxacodeCacheMax, "缓存达到上限后应淘汰，而不是无限增长")
}

// TestEmployeeStatusAuditAndNotFound 启停员工是特权动作：
//  1. 必须留痕（从前不记审计，改自己密码反倒记了，日志里看不出谁动了谁）；
//  2. 目标不存在 / 不属于本租户时必须报「未找到」，而不是回 ok 假装成功。
func TestEmployeeStatusAuditAndNotFound(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "审计对象", "phone": "13900000777", "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, body["code"])
	staffID := int64(body["staffId"].(float64))

	_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/disable", staffID), nil, tok)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("GET", "/api/admin/audit-logs?action=employee.status", nil, tok)
	require.EqualValues(t, 0, body["code"])
	list, ok := body["data"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, list, "停用员工应留下审计记录")
	first := list[0].(map[string]interface{})
	require.Equal(t, "employee.status", first["action"])
	require.Equal(t, fmt.Sprint(staffID), first["targetId"])

	// 不存在的员工：明确报错，而不是「看起来成功了」
	_, body = c.do("POST", "/api/admin/employees/99999999/disable", nil, tok)
	require.EqualValues(t, 1, body["code"])
	_, body = c.do("POST", "/api/admin/employees/99999999/reset-password", nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.NotContains(t, body, "initialPassword")
}
