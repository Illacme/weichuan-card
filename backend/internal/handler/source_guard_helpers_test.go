package handler

// 源码级守门用的小工具。这些 helper 原先散落在 tenant_storage_guard_test.go 里，
// 第七十五轮删除旧存储模型时该文件被删，但另有几个守门测试还在用它们。

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"testing"

	"weichuan/internal/auth"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// b64Rep 返回「32 个相同字节」的 base64，用作一把合法长度但不正确的 PII 密钥。
// 32 字节是 pii 要求的密钥长度，长度不对会在装载阶段就被拒，
// 那就测不到「换钥后读到旧密文」这条路径了。
func b64Rep(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

// loginTenantAdmin 造一个 companyId=1 的企业管理员并登录，返回其令牌。
//
// 与 loginAdmin（平台超管）分开：大量「只能管本企业」的断言要靠这个身份，
// 用平台超管去测永远通过，等于没测。
func loginTenantAdmin(t *testing.T, c *testClient, db *gorm.DB) string {
	t.Helper()
	const (
		user = "tenant-admin"
		pass = "tenant-pass-123"
	)
	hash, err := auth.HashPassword(pass)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: user, PasswordHash: hash,
	}).Error)

	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": user, "password": pass,
	}, "")
	require.EqualValues(t, 0, body["code"], "企业管理员登录失败：%v", body["msg"])
	tok := c.cookies["access_token"]
	require.NotEmpty(t, tok)
	return tok
}

// readHandlerSource 读 handler 包内的一个源文件（相对本包目录）。
func readHandlerSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	require.NoError(t, err, "读不到 %s", name)
	return string(b)
}

// routeCall 是一条路由注册调用的文本（已剥注释）。
//
// 只要 text 一个字段：守门断言做的是「这一行里中间件在 handler 之前吗」，
// 记录行号只会让失败信息更好看一点，却要多维护一套偏移计算。
type routeCall struct {
	text string
}

// routeCallRE 只认 r.GET/POST/PUT/DELETE/PATCH( 这种注册形态。
var routeCallRE = regexp.MustCompile(`\br\.(?:GET|POST|PUT|DELETE|PATCH)\(`)

// scanRouteCalls 扫出源码里所有路由注册调用的完整文本（括号配平）。
//
// 配平是必须的：注册调用跨多行，中间还嵌着 func(c *gin.Context) { ... }，
// 用「取到行尾」会把中间件和 handler 一起截掉，于是「限流挂在 handler 之前」
// 这条断言会永远绿。
func scanRouteCalls(src string) []routeCall {
	var out []routeCall
	for _, loc := range routeCallRE.FindAllStringIndex(src, -1) {
		open := loc[1] - 1 // '(' 的位置
		depth := 0
		end := -1
		for i := open; i < len(src); i++ {
			switch src[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i + 1
				}
			}
			if end > 0 {
				break
			}
		}
		if end > 0 {
			out = append(out, routeCall{text: src[loc[0]:end]})
		}
	}
	return out
}

// stripGoComments 剥掉 Go 源码里的行注释与块注释，保留字符串字面量原样。
//
// 为什么要剥：守门断言都是「某文件必须/不得出现某个调用」。注释里写一句
// 「以前这里用 NewStaticResolver」不该被判成「真的在用」；反过来，
// 字符串字面量里出现的 `http://` 也不能被当成行注释截断。
//
// 只处理足以覆盖本仓库源码的形态：双引号字符串、反引号原始字符串、
// 单引号 rune 字面量。不做完整的词法分析（不需要，也不该在这里重复 Go 的词法）。
func stripGoComments(src string) string {
	out := make([]rune, 0, len(src))
	rs := []rune(src)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '"' || c == '\'':
			// 字符串 / rune：原样拷贝到配对的引号（处理反斜杠转义）。
			out = append(out, c)
			i++
			for ; i < len(rs); i++ {
				out = append(out, rs[i])
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
					out = append(out, rs[i])
					continue
				}
				if rs[i] == c {
					break
				}
			}
		case c == '`':
			out = append(out, c)
			i++
			for ; i < len(rs); i++ {
				out = append(out, rs[i])
				if rs[i] == '`' {
					break
				}
			}
		case c == '/' && i+1 < len(rs) && rs[i+1] == '/':
			for ; i < len(rs) && rs[i] != '\n'; i++ {
			}
			if i < len(rs) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(rs) && rs[i+1] == '*':
			i += 2
			for ; i+1 < len(rs); i++ {
				if rs[i] == '*' && rs[i+1] == '/' {
					i++
					break
				}
			}
		default:
			out = append(out, c)
		}
	}
	return string(out)
}
