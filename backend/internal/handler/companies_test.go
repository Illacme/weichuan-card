package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCompanyLoginCode 登录页白标码（login_code）的写入校验：
// 格式约束（小写字母/数字/连字符）+ 全局唯一（排除自身）；留空可清除。
// 覆盖开通与编辑两条写路径（均走 Cookie 鉴权的平台超管中间件）。
func TestCompanyLoginCode(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c) // 企管接口走 Cookie 鉴权

	// 1) 开通企业并指定合法白标码 → 成功，且列表/详情回传该码
	_, body := c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "甲公司", "loginCode": "acode",
	})
	require.EqualValues(t, 0, body["code"])
	aid := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = c.doWithCookie("GET", "/api/admin/companies", nil)
	rows := body["data"].([]interface{})
	var aLogin string
	for _, it := range rows {
		m := it.(map[string]interface{})
		if fmt.Sprint(m["id"]) == aid {
			aLogin = fmt.Sprint(m["loginCode"])
		}
	}
	require.Equal(t, "acode", aLogin, "列表应回传白标码")

	// 2) 再开一家用相同白标码 → 拒绝（已存在）
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "乙公司", "loginCode": "acode",
	})
	require.EqualValues(t, 1, body["code"], "白标码应全局唯一")
	require.Contains(t, body["msg"], "已存在")

	// 3) 非法格式（大写 + 特殊字符）→ 拒绝
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "丙公司", "loginCode": "A-code!",
	})
	require.EqualValues(t, 1, body["code"], "非法白标码格式应被拒")
	require.Contains(t, body["msg"], "小写字母")

	// 4) 编辑甲公司：改成另一个唯一码 → 成功
	_, body = c.doWithCookie("PUT", "/api/admin/companies/"+aid, map[string]interface{}{
		"name": "甲公司", "loginCode": "acode2",
	})
	require.EqualValues(t, 0, body["code"], "改为唯一码应成功")

	// 5) 编辑甲公司：改回已被占用的 "acode"（此刻已无企业持有，应成功）
	//    先给乙公司一个码，再验证「排除自身」与「占用拒绝」
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "丁公司", "loginCode": "acode",
	})
	require.EqualValues(t, 0, body["code"])
	did := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	// 甲公司（acode2）改成丁公司占用的 acode → 拒绝（被丁占用）
	_, body = c.doWithCookie("PUT", "/api/admin/companies/"+aid, map[string]interface{}{
		"name": "甲公司", "loginCode": "acode",
	})
	require.EqualValues(t, 1, body["code"], "改为被别家占用的码应拒绝")

	// 丁公司改成自己的 acode（排除自身）→ 成功
	_, body = c.doWithCookie("PUT", "/api/admin/companies/"+did, map[string]interface{}{
		"name": "丁公司", "loginCode": "acode",
	})
	require.EqualValues(t, 0, body["code"], "自身持有相同码应放行")

	// 6) 编辑甲公司：留空白标码 → 成功（清除）
	_, body = c.doWithCookie("PUT", "/api/admin/companies/"+aid, map[string]interface{}{
		"name": "甲公司", "loginCode": "",
	})
	require.EqualValues(t, 0, body["code"], "清除白标码应成功")
}
