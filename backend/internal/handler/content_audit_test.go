package handler

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
)

// content.go 此前整份零审计：客户名片页展示什么的最终决定权都在这几个接口，
// 主页被人改了却查不出是谁。两道守门：
//  1. 接口级：真实调写端点，查 audit_logs 证明埋点真落库（源码写了调用不代表写进表）；
//  2. 源码级 AST：以后新增写路由却漏埋点，直接红。
func TestContentWritesLeaveAudit(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	// 每个请求先确认 code:0，否则「没有审计」可能只是因为请求本身失败了。
	call := func(method, path string, body any) map[string]interface{} {
		t.Helper()
		_, resp := c.do(method, path, body, tok)
		require.EqualValues(t, 0, resp["code"], "%s %s 前置请求失败：%v", method, path, resp)
		return resp
	}

	call("PUT", "/api/admin/company-info", map[string]any{"fullName": "甲公司"})
	// videoFeedToken 是取片凭证：特意填一个可识别的串，稍后断言它没进审计表。
	call("PUT", "/api/admin/company-profile", map[string]any{
		"videoSource": "none", "videoFeedToken": "SECRET-FEED-TOKEN-DO-NOT-AUDIT",
	})

	created := call("POST", "/api/admin/categories", map[string]any{"name": "分类A"})
	data, ok := created["data"].(map[string]interface{})
	require.True(t, ok, "新建分类应回 data：%v", created)
	catID := int64(data["id"].(float64))
	require.NotZero(t, catID)

	catPath := "/api/admin/categories/" + strconv.FormatInt(catID, 10)
	call("PUT", catPath, map[string]any{"name": "分类B", "sort": 1})
	call("PUT", "/api/admin/categories/sort", []map[string]any{{"id": catID, "sort": 2}})
	call("PUT", "/api/admin/home-layout", map[string]any{"order": []string{"achievement", "service"}})
	call("DELETE", catPath, nil)

	var logs []model.AuditLog
	require.NoError(t, db.Where("action LIKE ?", "content.%").Find(&logs).Error)
	require.GreaterOrEqual(t, len(logs), 7, "写端点应该至少留下 7 条内容审计")

	seen := map[string][]model.AuditLog{}
	for _, row := range logs {
		seen[row.Action] = append(seen[row.Action], row)
		require.Equal(t, audit.ResultSuccess, row.Result, "成功写操作应记 success")
		require.Equal(t, "admin", row.ActorType)
	}

	for _, action := range []string{
		audit.ActionContentInfo, audit.ActionContentProfile, audit.ActionContentCategory,
		audit.ActionContentSort, audit.ActionContentLayout,
	} {
		require.NotEmpty(t, seen[action], "动作 %s 没有留下审计", action)
	}

	catLogs := seen[audit.ActionContentCategory]
	require.GreaterOrEqual(t, len(catLogs), 3, "分类增改删应各留一条")
	ops := map[string]bool{}
	for _, row := range catLogs {
		var detail map[string]any
		require.NoError(t, json.Unmarshal([]byte(row.Detail), &detail), "detail 必须是合法 JSON：%q", row.Detail)
		if op, ok := detail["op"].(string); ok {
			ops[op] = true
		}
		require.Equal(t, "category", row.TargetType)
	}
	for _, op := range []string{"create", "update", "delete"} {
		require.True(t, ops[op], "分类 %s 操作没有留痕：%v", op, ops)
	}

	// 原文凭证不许进审计表——只记档位，不记取片 token。
	for _, row := range seen[audit.ActionContentProfile] {
		require.NotContains(t, row.Detail, "SECRET-FEED-TOKEN-DO-NOT-AUDIT")
	}
}

// TestEveryContentWriteRouteRecordsAudit：新增写路由漏埋点必须红。
// 只数 audit.Record 总数是假守门：某个路由漏了、另一个多记一次也会相等。
func TestEveryContentWriteRouteRecordsAudit(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "content.go", nil, 0)
	require.NoError(t, err)

	routes := 0
	var missing []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "r" {
			return true
		}
		switch sel.Sel.Name {
		case "POST", "PUT", "DELETE":
		default:
			return true
		}
		routes++
		found := false
		if len(call.Args) >= 3 {
			ast.Inspect(call.Args[2], func(x ast.Node) bool {
				inner, ok := x.(*ast.CallExpr)
				if !ok {
					return true
				}
				isel, ok := inner.Fun.(*ast.SelectorExpr)
				if ok && isel.Sel.Name == "Record" {
					found = true
					return false
				}
				return true
			})
		}
		if !found {
			missing = append(missing, sel.Sel.Name)
		}
		return true
	})

	// 量尺自己先有读数：写路由抓取数偏少说明选择器写错，下面全绿毫无意义。
	require.GreaterOrEqual(t, routes, 13, "只抓到 %d 个写路由，扫描器可能空转", routes)
	require.Empty(t, missing, "这些写路由没有 audit.Record：%s", strings.Join(missing, ", "))
}
