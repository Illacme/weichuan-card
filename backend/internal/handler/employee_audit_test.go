package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 员工新建 / 编辑是写入 PII 的特权动作：谁在什么时候改了姓名、手机、邮箱必须
// 可追溯。两个 Action 常量早就在 audit.go 里定义了，但生产代码从未调用——
// 前端中文名也早补好了，于是「常量有、标签有、审计记录没有」，跨端字典对账
// 全绿，实际审计表却空着。这类缺口只有直接查生产调用点才能发现。
func TestEmployeeWriteRoutesRecordAudit(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "apply.go", nil, 0)
	require.NoError(t, err)

	// 量尺自检：必须先抓到目标路由，否则下面的「每个路由都有审计」会空转。
	const wantRoutes = 2
	seen := map[string]bool{}
	var missing []string
	for _, action := range []string{"ActionEmployeeCreate", "ActionEmployeeUpdate"} {
		seen[action] = false
	}
	routes := 0

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
		if sel.Sel.Name != "POST" && sel.Sel.Name != "PUT" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		path := strings.Trim(lit.Value, `"`)
		target := ""
		switch {
		case strings.HasSuffix(path, "/api/admin/employees"):
			target = "ActionEmployeeCreate"
		case strings.HasSuffix(path, "/api/admin/employees/:id"):
			target = "ActionEmployeeUpdate"
		default:
			return true
		}
		routes++
		if len(call.Args) < 3 {
			missing = append(missing, path+": 参数不足")
			return true
		}
		found := false
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
		if !found {
			missing = append(missing, path)
			return true
		}
		// audit.Record(...) 的参数里必须出现对应 Action 常量，否则是「记了审计
		// 但记成了别的动作」——比如把新建记成 status，字典对账照样全绿。
		ast.Inspect(call.Args[2], func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok {
				if id.Name == target {
					seen[target] = true
					return false
				}
			}
			return true
		})
		return true
	})

	require.Equal(t, wantRoutes, routes,
		"量尺自检：应精确抓到 %d 个员工写路由，实际 %d 个（路由改名会让守门空转）", wantRoutes, routes)
	require.Empty(t, missing, "这些员工写路由没有 audit.Record：%s", strings.Join(missing, ", "))
	for action, ok := range seen {
		require.True(t, ok, "%s 在 apply.go 里没有生产调用（只有定义和前端标签不算）", action)
	}
}

// 前端中文名必须覆盖这两个动作。audit.spec.ts 已做全量双向对账，但这里
// 再钉一次具体条目，防止「后端补了埋点、前端字典那轮恰好有人改坏又没人跑 CI」。
func TestEmployeeAuditActionsHaveFrontendLabels(t *testing.T) {
	b, err := os.ReadFile("../../../frontend/src/api/audit.ts")
	require.NoError(t, err)
	src := string(b)
	for _, label := range []string{"'employee.create':", "'employee.update':"} {
		require.Contains(t, src, label, "前端审计字典缺 %s", label)
	}
}
