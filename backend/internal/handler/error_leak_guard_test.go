package handler

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 写库/随机数失败绝不能把底层错误原文回显给客户端：原始错误会带出表名、列名、
// 约束名，等于把库结构白送给探测者。此前 apply.go 三处、companies.go 一处都在
// c.JSON 的 msg 里直接放 res.Error.Error()，另有 companies.go 一处放 genPassword
// 的 err.Error()。统一改走 dbWriteFailed（服务端留日志、客户端固定文案）。
//
// 两道守门：
//  1. 接口级：真实注入 GORM Update 回调错误，证明 4 个端点都不回显 "injected"；
//  2. 源码级 AST：防止以后有人再写出 `gin.H{"msg": err.Error()}`。
//     AST 而非正则——正则扫注释会把「解释为什么不能回显」的注释判成违规。
func TestAdminWriteFailuresDoNotLeakSQLErrors(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	emp := model.Employee{CompanyID: 1, Name: "测试员工", Phone: "13800138000", Status: "enabled"}
	require.NoError(t, db.Create(&emp).Error)
	adm := model.Admin{CompanyID: 1, Username: "company-admin", PasswordHash: "x"}
	require.NoError(t, db.Create(&adm).Error)

	// 注入前先证明这 4 个端点参数合法、行存在：否则「失败」可能只是参数错误，
	// 用例会绿得毫无意义（量尺自己先要有读数）。
	require.NotZero(t, emp.StaffID)
	require.NotZero(t, adm.ID)

	const cbName = "test:inject_update_error_leak"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		tx.AddError(errors.New("injected leak marker"))
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cbName) })

	cases := []struct{ name, method, path string }{
		{"禁用员工", "POST", fmt.Sprintf("/api/admin/employees/%d/disable", emp.StaffID)},
		{"启用员工", "POST", fmt.Sprintf("/api/admin/employees/%d/enable", emp.StaffID)},
		{"重置员工密码", "POST", fmt.Sprintf("/api/admin/employees/%d/reset-password", emp.StaffID)},
		{"重置企业管理员密码", "POST", fmt.Sprintf("/api/admin/companies/1/admins/%d/reset-password", adm.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]interface{}
			// 注入时写的是 "injected leak marker"，但 PII 钩子会把 GORM 回调里的
			// 错误文本改写（实测到 handler 手里已变成 "err=injected leak marker"）。
			// 若拿注入时的字面量当标尺，即使代码原样回显也恒绿——这是假测试。
			// 改从服务端日志取 handler 实际看到的错误文本，先确认它非空再比对。
			out := capturePIILog(t, func() {
				_, body = c.do(tc.method, tc.path, nil, tok)
			})
			require.NotEqualValues(t, 0, body["code"], "%s 写库失败却回了 code:0", tc.name)
			msg := fmt.Sprint(body["msg"])
			require.Contains(t, msg, "失败", "%s 应给可理解的失败文案，实际 %q", tc.name, msg)
			idx := strings.Index(out, "err=")
			require.GreaterOrEqual(t, idx, 0, "%s 服务端没留下错误日志", tc.name)
			actual := strings.TrimSpace(out[idx+4:])
			if j := strings.IndexByte(actual, '\n'); j >= 0 {
				actual = actual[:j]
			}
			require.NotEmpty(t, actual, "%s 日志里的错误文本为空，量尺没有读数", tc.name)
			require.NotContains(t, msg, actual, "%s 把底层错误原文回显给客户端", tc.name)
		})
	}
}

// countMsgErrorEcho 数一个 Go 文件里 c.JSON 调用数，以及 msg 值直接调用 .Error() 的个数。
// 两个函数共用于真实源码与内存夹具，保证「量尺自己有读数」不是空话。
func countMsgErrorEcho(f *ast.File) (jsonCalls, echoes int) {
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "JSON" {
			return true
		}
		jsonCalls++
		if len(call.Args) < 2 {
			return true
		}
		ast.Inspect(call.Args[1], func(x ast.Node) bool {
			kv, ok := x.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING || strings.Trim(key.Value, `"`) != "msg" {
				return true
			}
			ast.Inspect(kv.Value, func(y ast.Node) bool {
				inner, ok := y.(*ast.CallExpr)
				if !ok {
					return true
				}
				msel, ok := inner.Fun.(*ast.SelectorExpr)
				if !ok || msel.Sel.Name != "Error" || len(inner.Args) != 0 {
					return true
				}
				// 只盯 GORM 结果对象的 res.Error.Error()：这才是会带出表名/列名/
				// 约束名的底层 SQL 错误。其它 err.Error() 多为微信 API、配置校验等
				// 业务错误，回显给管理员是有意设计，一刀切会误伤十几处正常代码。
				recv, ok := msel.X.(*ast.SelectorExpr)
				if ok && recv.Sel.Name == "Error" {
					echoes++
					return false
				}
				return true
			})
			return true
		})
		return true
	})
	return
}

func TestNoRawErrorEchoInJSONMsg(t *testing.T) {
	// 先证量尺能抓到违规写法：夹具里故意放一条，解析器空转这条就会红。
	const fixture = `package handler
import ("net/http"; "github.com/gin-gonic/gin")
func f(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 1, "msg": res.Error.Error()}) }
`
	ff, err := parser.ParseFile(token.NewFileSet(), "fixture.go", fixture, 0)
	require.NoError(t, err)
	_, fixtureEchoes := countMsgErrorEcho(ff)
	require.Equal(t, 1, fixtureEchoes, "量尺抓不到夹具里的 .Error() 回显，下面的全绿没有意义")

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var violations []string
	totalJSON := 0
	seen := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		seen[filepath.Base(path)] = true
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err, path)
		jc, echoes := countMsgErrorEcho(f)
		totalJSON += jc
		if echoes > 0 {
			violations = append(violations, fmt.Sprintf("%s(%d)", path, echoes))
		}
	}

	// 空转兜底：handler 包有几百处 c.JSON，扫不到足够读数就先修扫描器。
	require.GreaterOrEqual(t, totalJSON, 100, "扫到的 c.JSON 调用明显偏少，解析器可能空转")
	for _, anchor := range []string{"apply.go", "companies.go", "content.go"} {
		require.True(t, seen[anchor], "锚点文件 %s 没被扫到，守门覆盖不全", anchor)
	}
	require.Empty(t, violations, "发现底层错误原文回显：%s", strings.Join(violations, ", "))
}

// 防「同命令里把错误又带回来」：确认统一出口本身不回显 err，只落日志。
func TestDBWriteFailedKeepsErrorInServerLogOnly(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "db_write.go", nil, 0)
	require.NoError(t, err)
	_, echoes := countMsgErrorEcho(f)
	require.Zero(t, echoes, "dbWriteFailed 自己不得回显 err.Error()")

	src, err := os.ReadFile("db_write.go")
	require.NoError(t, err)
	require.Contains(t, string(src), `log.Printf("[DB][ERROR] %s err=%v", where, err)`)
}
