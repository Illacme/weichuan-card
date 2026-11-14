package handler

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 本文件是 R9「权限误放开」的源码级守门（设计稿 §1.1 / §4 P1 行）。
//
// 风险长什么样：新接口照抄 RegisterStorage 的 `middleware.AuthMiddleware(db, "admin")`
// ——那是「登录 + role=admin」，**企业管理员完全能过**。于是企业管理员可以改自己企业的
// 云凭据（把文件写到任意桶、把别人的桶指到自己账号下），而平台从业务侧察觉不到。
// 这条漏法在 code review 里极难被抓：两行中间件长得几乎一样，只看 diff 看不出差别。
//
// 所以不能靠自觉，要靠一条会红的测试。

// routeCall 是一次 `r.VERB(...)` 路由注册。text 是从 `r` 到配对右括号的完整调用。
type routeCall struct {
	offset int
	text   string
}

// TestTenantStorageRoutesUsePlatformAdminGuard 钉住三件事：
//
//  1. tenant_storage.go 里每一条路由都挂了 `plat`（PlatformAdminMiddleware 的实例）；
//  2. 挂的位置在**第一个 func 之前**——写在 handler body 里等于没挂；
//  3. 文件里不出现 AuthMiddleware（R9 的具体形态，出现即说明有人照抄了旁边的写法）。
//
// 第 2 条不是吹毛求疵：`plat` 出现在 body 里当变量名用的可能性虽低，但
// 「用 contains 判整个调用」会让这种写法蒙混过关，而它恰恰是没挂中间件。
func TestTenantStorageRoutesUsePlatformAdminGuard(t *testing.T) {
	src := readHandlerSource(t, "tenant_storage.go")

	calls := scanRouteCalls(src)
	// 数量对账：新增/删除路由时必须回来重新确认守门覆盖，不能让守门靠「没扫到就通过」。
	// 4 条 = GET / PUT / DELETE 配 + POST check。
	require.Len(t, calls, 4, "tenant_storage.go 的路由数量变了：新增或删除路由时，"+
		"必须同步确认每一条都挂了 PlatformAdminMiddleware，再调整这个数字")

	for _, call := range calls {
		head := call.text
		if i := strings.Index(head, "func("); i >= 0 {
			head = head[:i]
		}
		require.Contains(t, head, "plat",
			"路由缺 PlatformAdminMiddleware（只挂 AuthMiddleware 的话企业管理员能改客户的云凭据）：\n%s", call.text)
		require.NotContains(t, head, "AuthMiddleware",
			"这条路由不能用 AuthMiddleware：企业管理员 role 也是 admin，会直接过")
	}

	// 这条必须**剥掉注释**再判：本文件的文档注释里恰好写了「不要照抄
	// RegisterStorage 的 AuthMiddleware」，裸 contains 会把说明书本身当违规。
	// 这类「守门误伤自己」的坑只会在第一次跑时暴露，所以注释里记一笔。
	require.NotContains(t, stripGoComments(src), "AuthMiddleware",
		"本文件不应出现 AuthMiddleware —— 它只校验登录与 role=admin，企业管理员能过。"+
			"租户存储配置里装的是客户的云凭据，必须用 PlatformAdminMiddleware")
}

// TestTenantStorageViewNeverExposesCredentials 静态守住「凭据列不进任何出参」。
//
// 运行时也有断言（响应体里不含明文 AK/SK），但那条只覆盖测到的那几个字段；
// 静态这条盯的是**视图构造函数的白名单性质**：accessKeyMasked 必须来自 maskCredential，
// 不能直接来自 AccessKeyEnc。
func TestTenantStorageViewNeverExposesCredentials(t *testing.T) {
	src := readHandlerSource(t, "tenant_storage.go")

	// SecretKey 连掩码都不给：它没有任何核对价值，泄漏等于把桶交出去。
	require.NotContains(t, src, `"secretKeyMasked"`,
		"SecretKey 不应有掩码出参：它没有核对价值，只回「是否已配置」")
	require.Contains(t, src, `view["accessKeyMasked"] = maskCredential(ak)`,
		"AccessKey 掩码必须经 maskCredential，不能直接把解密结果塞进出参")
}

func readHandlerSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	require.NoError(t, err, "读不到 %s：守门测试必须在 handler 包目录下运行", name)
	return string(b)
}

// TestTenantStorageResetDeletesOnlyConfigRow 是「保守保留」政策的源码级守门
// （第七十轮，用户选定口径：解约/撤桶只删配置行，客户桶对象/回填台账/业务引用一律保留）。
//
// 运行时用例（TestTenantStorageResetPreservesCustomerData）只能证明「当前没删」，
// 守不住「有人给 DELETE 加了删除」。这条静态守门钉的是 DELETE handler 的**边界**：
// 它的函数体除了删 tenant_storage_configs 这一行，绝不能出现任何一张被保护表的删除。
//
// 被保护的清单来自 refmigrate.Inventory() 的 14 个引用列 + 回填台账 + 对象存储删除：
//   - 业务表：company_info / media_check / project_case / employee / open_apply /
//     achievement_category / biz_service / home_layout
//   - 回填台账：storage_backfill_refs
//   - 对象存储删除：DeleteObject / RemoveObject（客户桶对象由客户自己管，平台不代删）
//
// 反向验证：在 handler 里临时加一行 `db.Delete(&model.StorageBackfillRef{})` 或
// `s3.DeleteObject(...)`，本测试必红；删掉后复绿。
func TestTenantStorageResetDeletesOnlyConfigRow(t *testing.T) {
	src := readHandlerSource(t, "tenant_storage.go")
	body := deleteHandlerBody(t, src)

	// 必须存在且仅存在一个「删配置行」的调用，且目标是 TenantStorageConfig，
	// 作用域被 company_id 限定（不会误伤其它租户）。
	require.Contains(t, body, `db.Where("company_id = ?", cid).Delete(&model.TenantStorageConfig{})`,
		"DELETE 必须删 tenant_storage_configs 这一行，且按 company_id 限定作用域")

	// 剥掉注释再判：本文件 handler 注释里会提到这些表名（解释为什么不能删），
	// 裸 contains 会把说明书本身当成违规，造成「永远通过 / 误伤自己」。
	stripped := stripGoComments(body)

	// 「至少抓到 N 处」的读数兜底：守门要盯的表清单必须非空，否则守门空跑=假绿。
	protected := []string{
		"StorageBackfillRef", "storage_backfill_refs",
		"CompanyInfo", "company_info",
		"MediaCheck", "media_check",
		"ProjectCase", "project_case",
		"Employee", "employee",
		"OpenApply", "open_apply",
		"AchievementCategory", "achievement_category",
		"BizService", "biz_service",
		"HomeLayout", "home_layout",
		"DeleteObject", "RemoveObject",
	}
	require.GreaterOrEqual(t, len(protected), 18, "守门清单本身要有读数，别让守门空跑")
	// 整段函数体里只允许出现一次 .Delete( —— 也就是删配置行那一次。
	require.Equal(t, 1, strings.Count(stripped, ".Delete("),
		"DELETE handler 里只能有「删配置行」这一次 .Delete(，不得有任何额外的删除")
	for _, tok := range protected {
		require.NotContains(t, stripped, tok,
			"DELETE 不得触碰被保护的对象（%s）：解约只删配置行，客户桶对象/回填台账/业务引用一律保留", tok)
	}
}

// deleteHandlerBody 取出 DELETE 路由 handler 的完整函数体（含外层花括号）。
//
// 注释块在 r.DELETE(...) 之前，不在这个调用文本里，所以取出的是纯函数体，
// 交给 stripGoComments 后判守门条件最干净。
func deleteHandlerBody(t *testing.T, src string) string {
	t.Helper()
	for _, call := range scanRouteCalls(src) {
		if !strings.HasPrefix(call.text, "r.DELETE(") {
			continue
		}
		i := strings.Index(call.text, "func(")
		require.GreaterOrEqual(t, i, 0, "DELETE 路由必须有 handler 函数体")
		brace := strings.Index(call.text[i:], "{") + i
		close := matchBrace(call.text, brace)
		require.GreaterOrEqual(t, close, 0, "DELETE handler 花括号不配对")
		return call.text[brace : close+1]
	}
	t.Fatalf("tenant_storage.go 里找不到 r.DELETE 路由")
	return ""
}

// scanRouteCalls 扫出所有 `r.GET(` / `r.PUT(` / `r.POST(` / `r.DELETE(` / `r.PATCH(` 调用。
//
// 为什么不用按行正则：路由注册经常跨多行（本文件里 check 那条就是
// `r.POST(path, plat, limiter.Handler(), func(...)` 分三行写），按行判会漏。
// 括号配对 + 跳过字符串字面量，才能拿到完整的调用文本。
func scanRouteCalls(src string) []routeCall {
	verbs := []string{"GET(", "PUT(", "POST(", "DELETE(", "PATCH("}
	var out []routeCall
	for i := 0; i+2 < len(src); i++ {
		// 只认标识符边界上的 `r.`：`r.` 出现在字符串里的情形（如 "r.GET("）先排除，
		// 否则注释或文案里的示例会被当成真路由。
		if src[i] != 'r' || src[i+1] != '.' {
			continue
		}
		if i > 0 && isIdentByte(src[i-1]) {
			continue
		}
		for _, v := range verbs {
			rest := src[i+2:]
			if !strings.HasPrefix(rest, v) {
				continue
			}
			open := i + 2 + len(v) - 1 // '(' 的下标
			close := matchParen(src, open)
			if close < 0 {
				continue
			}
			call := src[i : close+1]
			// 只在真实代码里算数：前面是行首缩进或空白，且不是在注释里。
			if isInComment(src, i) {
				continue
			}
			out = append(out, routeCall{offset: i, text: call})
			break
		}
	}
	return out
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '.' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// isInComment 判断 offset 是否落在 `//` 行注释内（本文件只用到行注释）。
func isInComment(src string, offset int) bool {
	lineStart := strings.LastIndexByte(src[:offset], '\n') + 1
	return strings.Contains(src[lineStart:offset], "//")
}

// matchParen 返回与 src[open] 处左括号配对的右括号下标；不配对返回 -1。
// 跳过 "..." / '...' / `...` 三种字面量——handler body 里的中文全角括号不是
// ASCII 括号不会干扰，但字符串里的 ASCII 括号会，所以必须跳过。
func matchParen(src string, open int) int {
	if open < 0 || open >= len(src) || src[open] != '(' {
		return -1
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '"', '\'', '`':
			i = skipStringLiteral(src, i)
			if i < 0 {
				return -1
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// matchBrace 返回与 src[open] 处左花括号配对的右花括号下标；不配对返回 -1。
// 同样要跳过字符串字面量：审计 detail 里那些 JSON 串的引号是 "，里面的字符
// 不得随意被当成结构括号。逻辑与 matchParen 对称，只换括号字符。
func matchBrace(src string, open int) int {
	if open < 0 || open >= len(src) || src[open] != '{' {
		return -1
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '"', '\'', '`':
			i = skipStringLiteral(src, i)
			if i < 0 {
				return -1
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// skipStringLiteral 从 src[i]（引号）跳到该字面量的闭合引号，返回闭合引号的下标。
// 只处理本仓库会出现的形态：转义 \" 与转义 \\，不支持原始字符串里的反引号嵌套。
func skipStringLiteral(src string, i int) int {
	quote := src[i]
	for j := i + 1; j < len(src); j++ {
		if src[j] == '\\' && quote != '`' {
			j++ // 跳过被转义的字符
			continue
		}
		if src[j] == quote {
			return j
		}
	}
	return -1
}

// stripGoComments 去掉 `//` 行注释，保留换行以维持行号感。
//
// 为什么需要它：本文件的文档注释里写着「不要用 AuthMiddleware」，
// 而守门要判的恰恰是「代码里有没有 AuthMiddleware」。不剥注释就会误伤自己。
// 字符串字面量要跳过，否则 `"http://..."` 里的 `//` 会被当成注释起点、
// 把后面的代码整段吃掉——那会让守门变成永远通过。
func stripGoComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '"' || src[i] == '\'' || src[i] == '`':
			end := skipStringLiteral(src, i)
			if end < 0 {
				b.WriteString(src[i:])
				return b.String()
			}
			b.WriteString(src[i : end+1])
			i = end
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				b.WriteByte('\n')
			}
		default:
			b.WriteByte(src[i])
		}
	}
	return b.String()
}
