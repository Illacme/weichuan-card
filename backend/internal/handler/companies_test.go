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

// TestCreateCompanySeedsCategoryTemplate 开通企业时自动复制业绩分类模板。
//
// 为什么要有这条：零数据版装完后新企业一个分类都没有，企业管理员登录后业绩页是空的、
// 小程序业绩 Tab 也渲染不出来。但模板**只能落在新企业名下**——落到别家就是把 A 家的
// 业务内容写进 B 家，这正是上一批在修的串租户问题。
func TestCreateCompanySeedsCategoryTemplate(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 1) 不传 seedCategories → 按复制处理（指针 nil 视为「没表态」，不是「明确不要」）
	_, body := c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "有分类公司",
	})
	require.EqualValues(t, 0, body["code"])
	withTpl := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = c.doWithCookie("GET", "/api/admin/categories?companyId="+withTpl, nil)
	require.EqualValues(t, 0, body["code"])
	rows := body["data"].([]interface{})
	require.Len(t, rows, 8, "应复制经典业绩 8 分类")

	var names []string
	for i, it := range rows {
		m := it.(map[string]interface{})
		names = append(names, fmt.Sprint(m["name"]))
		require.EqualValues(t, withTpl, fmt.Sprint(m["companyId"]), "分类必须落在新企业名下")
		require.EqualValues(t, i+1, m["sort"], "排序应为 1..8（列表按 sort asc）")
		// 图标留空：演示图标的文件在干净库里不存在，照抄 URL 只会得到裂图
		require.Equal(t, "", fmt.Sprint(m["icon"]), "模板分类不应带图标")
		// 不锁定：客户要按自己行业改名或删掉用不上的分类
		require.Equal(t, false, m["isLocked"], "模板分类必须可删可改")
	}
	require.Equal(t, []string{
		"居住住宅", "政务办公", "医院、学校", "商业、酒店",
		"公共交通", "生产制造", "仓储物流", "私人住宅",
	}, names, "分类名与顺序应与设计稿一致")

	// 2) 显式 seedCategories=false → 一个都不建
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "无分类公司", "seedCategories": false,
	})
	require.EqualValues(t, 0, body["code"])
	noTpl := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = c.doWithCookie("GET", "/api/admin/categories?companyId="+noTpl, nil)
	require.EqualValues(t, 0, body["code"])
	require.Len(t, body["data"].([]interface{}), 0, "显式不要时不应建任何分类")

	// 3) 1 号企业（测试引擎预置的存量租户）不该被顺带写入分类
	_, body = c.doWithCookie("GET", "/api/admin/categories", nil)
	require.EqualValues(t, 0, body["code"])
	require.Len(t, body["data"].([]interface{}), 0, "平台超管不带 companyId 时缺省 1 号企业，不应受新企业模板影响")
}

// TestPlatformResetCompanyAdminPassword 平台超管重置企业管理员密码。
//
// 为什么要有这条：企业管理员自己改密必须提供原密码，忘了我方无从找回（库里只存哈希），
// 唯一的救济入口就是平台超管代重置。而「重置密码」是个会伤到对方当下的动作——
// 安全戳自增会把该管理员所有在线会话一并挤下线，所以顺带把「旧密码失效」也钉住，
// 免得哪天改坏了变成「重置完还能用旧密码进」，那等于重置没生效却没人发现。
func TestPlatformResetCompanyAdminPassword(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	// 开通企业并顺带建管理员
	_, body := plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "重置测试企业", "adminUsername": "entadmin", "adminPassword": "oldpass123",
	})
	require.EqualValues(t, 0, body["code"])
	cid := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = plat.doWithCookie("GET", "/api/admin/companies/"+cid+"/admins", nil)
	require.EqualValues(t, 0, body["code"])
	rows := body["data"].([]interface{})
	require.Len(t, rows, 1)
	aid := fmt.Sprint(rows[0].(map[string]interface{})["id"])

	// 重置前：旧密码可登录（确认这条断言的基线成立，不是「本来就登不上」）
	before := newTestClient(r, t)
	_, body = before.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "entadmin", "password": "oldpass123",
	}, "")
	require.EqualValues(t, 0, body["code"], "重置前旧密码应能登录")

	// 重置 → 返回新初始密码
	_, body = plat.doWithCookie("POST", "/api/admin/companies/"+cid+"/admins/"+aid+"/reset-password", nil)
	require.EqualValues(t, 0, body["code"])
	newPwd := fmt.Sprint(body["initialPassword"])
	require.NotEmpty(t, newPwd, "应回传新初始密码")
	require.NotEqual(t, "oldpass123", newPwd)

	// 新密码可登录
	after := newTestClient(r, t)
	_, body = after.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "entadmin", "password": newPwd,
	}, "")
	require.EqualValues(t, 0, body["code"], "重置后新密码应能登录")

	// 旧密码立即失效（安全戳自增令旧令牌作废，这里用「能否再登」来观测）
	stale := newTestClient(r, t)
	_, body = stale.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "entadmin", "password": "oldpass123",
	}, "")
	require.EqualValues(t, 1, body["code"], "重置后旧密码不应再能登录")

	// 特权动作必须留痕，但绝不能把新密码写进审计
	_, body = plat.do("GET", "/api/admin/audit-logs?action=company.admin.reset_password", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	logs := body["data"].([]interface{})
	require.GreaterOrEqual(t, len(logs), 1, "重置他人密码应记审计")
	require.NotContains(t, fmt.Sprint(logs[0]), newPwd, "审计不得记录新密码")

	// 不存在的管理员 → 拒绝
	_, body = plat.doWithCookie("POST", "/api/admin/companies/"+cid+"/admins/999999/reset-password", nil)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不存在")

	// 错配企业：乙家的管理员拿甲家的 company id 去重置 → 拒绝。
	// 路径上 id 和 aid 各自都对，只有组合起来是错的；不做双约束就会改到别人账号上。
	_, body = plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "乙企业", "adminUsername": "entadmin2", "adminPassword": "pass2222",
	})
	require.EqualValues(t, 0, body["code"])
	cid2 := fmt.Sprint(body["data"].(map[string]interface{})["id"])

	_, body = plat.doWithCookie("GET", "/api/admin/companies/"+cid2+"/admins", nil)
	require.EqualValues(t, 0, body["code"])
	aid2 := fmt.Sprint(body["data"].([]interface{})[0].(map[string]interface{})["id"])

	_, body = plat.doWithCookie("POST", "/api/admin/companies/"+cid+"/admins/"+aid2+"/reset-password", nil)
	require.EqualValues(t, 1, body["code"], "错配企业 id 应拒绝，否则会误改别家账号")
}
