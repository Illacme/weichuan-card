package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicTeamWall 名片墙（GET /api/team）——小程序裸启动的落地页数据源。
//
// 这个接口是「公开」的：任何扫过码、知道 companyId 的人都能拉。所以它同时受两条
// 互斥的约束，缺一不可：
//  1. 必须够用：姓名 / 职位 / 头像 / 编号，缺一个宫格就画不出来；
//  2. 必须够少：`model.Employee` 上挂着 Phone / Phone2 / Email / Landline / OpenID，
//     直接把结构体序列化出去，等于把全员手机号交给任何扫过码的人。
//
// 另外钉住两件容易静默出错的事：
//   - 停用员工不上墙（点进去只会得到「该员工名片已停用」）；
//   - 姓名要能解密出明文（姓名是 PII 密文列，列裁剪时漏掉 name 不会报错，
//     只会让宫格上每个人都没有名字）。
func TestPublicTeamWall(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 1 号企业（引擎预置的存量租户）建 3 人，其中最后一人停用
	ids := make([]int64, 0, 3)
	for i, name := range []string{"张三", "李四", "王五"} {
		_, body := c.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
			"name":  name,
			"phone": fmt.Sprintf("1390000000%d", i+1),
			"title": fmt.Sprintf("岗位%d", i+1),
		})
		require.EqualValues(t, 0, body["code"])
		ids = append(ids, int64(body["staffId"].(float64)))
	}
	_, body := c.doWithCookie("POST", fmt.Sprintf("/api/admin/employees/%d/disable", ids[2]), nil)
	require.EqualValues(t, 0, body["code"])

	// 再开一家企业并给它建 1 个人，用于验证租户隔离
	_, body = c.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "乙公司", "seedCategories": false,
	})
	require.EqualValues(t, 0, body["code"])
	company2 := int64(body["data"].(map[string]interface{})["id"].(float64))
	_, body = c.doWithCookie("POST", fmt.Sprintf("/api/admin/employees?companyId=%d", company2), map[string]interface{}{
		"name": "赵六", "phone": "13900000009", "title": "乙岗位",
	})
	require.EqualValues(t, 0, body["code"])

	// 访客视角：不带任何登录态
	anon := newTestClient(r, t)
	_, body = anon.do("GET", "/api/team?companyId=1", nil, "")
	require.EqualValues(t, 0, body["code"])
	list := body["data"].([]interface{})
	require.Len(t, list, 2, "停用员工不该上墙")
	require.EqualValues(t, 2, body["total"], "total 只数启用员工，前端据此判断是否被截断")

	// 字段白名单：一个都不能多
	for _, it := range list {
		m := it.(map[string]interface{})
		for _, banned := range []string{
			"phone", "phone2", "email", "landline", "openid",
			"passwordHash", "tokenVersion", "status", "companyId", "createdAt",
		} {
			require.NotContains(t, m, banned, "公开接口不得输出 %s", banned)
		}
		for _, keep := range []string{"staffId", "name", "title", "avatar"} {
			require.Contains(t, m, keep)
		}
	}
	// 姓名必须解出明文；顺序按 staff_id 升序（老员工在前）
	require.Equal(t, "张三", list[0].(map[string]interface{})["name"])
	require.Equal(t, "李四", list[1].(map[string]interface{})["name"], "应按 staff_id 升序")
	require.Equal(t, "岗位1", list[0].(map[string]interface{})["title"])

	// 企业抬头随列表一起下发（分两次请求会让首屏先闪一下空标题）。
	// fullName 允许为空：企业信息还没填的新企业就是这种状态，端上用品牌名兜底，
	// 所以这里只钉「抬头对象在、id 对」，不钉它非空。
	comp, ok := body["company"].(map[string]interface{})
	require.True(t, ok, "应随列表下发企业抬头")
	require.EqualValues(t, 1, comp["id"])

	// 租户隔离：乙公司的墙上看不到甲公司的人
	_, body = anon.do("GET", fmt.Sprintf("/api/team?companyId=%d", company2), nil, "")
	require.EqualValues(t, 0, body["code"])
	list2 := body["data"].([]interface{})
	require.Len(t, list2, 1)
	require.Equal(t, "赵六", list2[0].(map[string]interface{})["name"])

	// 企业不存在 / 已停用 → code=1（小程序据此给出「企业不可用」提示，而不是空白页）
	_, body = anon.do("GET", "/api/team?companyId=999", nil, "")
	require.EqualValues(t, 1, body["code"])
}
