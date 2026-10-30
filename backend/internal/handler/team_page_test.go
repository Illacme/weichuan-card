package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// 名片墙分页 + 搜索（GET /api/team?page=&size=&keyword=）。
//
// 背景（2026-10-30）：这个接口原来是「一次全量，最多 300」。企业超过 300 人时
// 访客只能看到前 300 位，且没有任何入口看到剩下的人——500 人以上的企业等于不能用。
// 改成服务端分页后，必须钉住四件事：
//  1. 分页真的在服务端切（不是取全量再让端上切片），两页之间不重叠、不丢人；
//  2. total 是**匹配总数**，不是当前页条数——端上要靠它显示「共 N 位同事」；
//  3. hasMore 由服务端算，端上不要自己用 total 减已加载条数去推（搜索场景会算错）；
//  4. 关键字能搜姓名（含前缀，如「张」）与职位，且大小写不敏感。
//
// 第 4 条是本轮最容易出错的地方：姓名是 PII 密文列（model.Employee.NameEnc），
// SQL 层只有盲索引能做等值匹配、做不了 LIKE，所以带关键字时必须走
// 「全员解密 → Go 侧过滤」。如果哪天有人把它"优化"回 SQL LIKE，这里会红。
func TestTeamPaged(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c)

	// 25 人：姓名「分页员工01」…「分页员工25」，职位奇偶交替，用于验证按职位搜
	const n = 25
	for i := 1; i <= n; i++ {
		title := "技术"
		if i%2 == 1 {
			title = "销售"
		}
		_, body := c.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
			"name":  fmt.Sprintf("分页员工%02d", i),
			"phone": fmt.Sprintf("1380000%04d", i),
			"title": title,
		})
		require.EqualValues(t, 0, body["code"])
	}

	anon := newTestClient(r, t)
	page := func(q string) ([]interface{}, map[string]interface{}) {
		t.Helper()
		_, body := anon.do("GET", "/api/team?companyId=1"+q, nil, "")
		require.EqualValues(t, 0, body["code"])
		return body["data"].([]interface{}), body
	}

	// ---- 1. 分页元信息 ----
	list, body := page("&size=10&page=1")
	require.Len(t, list, 10)
	require.EqualValues(t, n, body["total"], "total 是匹配总数，不是当前页条数")
	require.EqualValues(t, 1, body["page"])
	require.EqualValues(t, 10, body["size"])
	require.Equal(t, true, body["hasMore"])

	// ---- 2. 末页与 hasMore ----
	list, body = page("&size=10&page=3")
	require.Len(t, list, 5, "25 人分三页，末页只剩 5 个")
	require.EqualValues(t, 3, body["page"])
	require.Equal(t, false, body["hasMore"], "末页不该再提示还有更多")

	// ---- 3. 两页不重叠、不丢人 ----
	first, _ := page("&size=10&page=1")
	second, _ := page("&size=10&page=2")
	seen := map[float64]bool{}
	for _, it := range first {
		seen[it.(map[string]interface{})["staffId"].(float64)] = true
	}
	require.Len(t, seen, 10)
	for _, it := range second {
		id := it.(map[string]interface{})["staffId"].(float64)
		require.False(t, seen[id], "第二页不应重复第一页的人（staffId=%v）", id)
		seen[id] = true
	}
	require.Len(t, seen, 20)

	// ---- 4. 越界页码夹到末页（删完数据后停在原页码不该看到空列表）----
	list, body = page("&size=10&page=99")
	require.EqualValues(t, 3, body["page"], "越界页码应夹到末页")
	require.Len(t, list, 5)

	// ---- 5. size 上限保护：请求 1000 条也只给 MaxTeamList ----
	_, body = page("&size=1000")
	require.EqualValues(t, 300, body["size"], "单页硬上限防止一次拉全量")

	// ---- 6. 搜索姓名前缀（中文子串，密文列必须能搜）----
	list, body = page("&keyword=" + "分页员工1")
	require.Len(t, list, 10, "「分页员工1x」共 10 人")
	require.EqualValues(t, 10, body["total"])
	require.Equal(t, "分页员工1", body["keyword"])

	// ---- 7. 搜索姓名的更短前缀（「员工0」命中 01–09）----
	list, _ = page("&keyword=" + "员工0")
	require.Len(t, list, 9)

	// ---- 8. 搜索职位（明文列，模糊匹配）----
	list, body = page("&keyword=" + "销售")
	require.Len(t, list, 13, "奇数编号 13 人是销售")
	require.EqualValues(t, 13, body["total"])

	// ---- 9. 搜索结果同样分页：13 个匹配里取第 2 页只剩 3 人 ----
	list, body = page("&keyword=" + "销售&size=10&page=2")
	require.Len(t, list, 3)
	require.EqualValues(t, 13, body["total"], "搜索时分页不能把 total 变成当前页条数")
	require.Equal(t, false, body["hasMore"])

	// ---- 10. 关键字两端的空格要吃掉（端上 input 很容易带空格）----
	list, _ = page("&keyword=" + "%20销售%20")
	require.Len(t, list, 13)

	// ---- 11. 搜不到：空数组而不是 null，且 hasMore=false ----
	list, body = page("&keyword=" + "不存在的人")
	require.Len(t, list, 0)
	require.EqualValues(t, 0, body["total"])
	require.Equal(t, false, body["hasMore"])

	// ---- 12. 停用员工不上墙（搜索也一样）----
	_, b := page("&size=1")
	firstID := int64(b["data"].([]interface{})[0].(map[string]interface{})["staffId"].(float64))
	_, body = c.doWithCookie("POST", fmt.Sprintf("/api/admin/employees/%d/disable", firstID), nil)
	require.EqualValues(t, 0, body["code"])

	list, body = page("&size=100")
	require.Len(t, list, n-1, "停用后墙上少一个人")
	require.EqualValues(t, n-1, body["total"])

	list, body = page("&keyword=" + "分页员工01")
	require.Len(t, list, 0, "停用的人搜不到")
	require.EqualValues(t, 0, body["total"])
}
