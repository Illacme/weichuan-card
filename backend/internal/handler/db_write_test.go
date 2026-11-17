package handler

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 写库失败与登录失败审计的接口级守门。
//
// 为什么用「真实制造数据库故障」而不是 mock：本项目吃过两次「只换二进制、
// 漏同步 migrations/」的亏（0035 / 0038），真实表现就是某个写接口开始报
// Unknown column。用 DROP TABLE / 注入 GORM 回调能复现同一类故障，且顺带
// 验证「错误不回显到客户端」这条边界；mock 掉 db 反而测不到实际 SQL 路径。
//
// 库一律用临时文件库（不用 :memory:）：gorm 连接池会把 :memory: 拆成多个
// 互不相关的库，DROP TABLE 与请求可能落到不同连接上。
func dbWriteEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	t.Setenv("UPLOAD_DIR", t.TempDir())

	tmpDB, err := os.CreateTemp("", "weichuan-dbwrite-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{
		ID: 1, Name: "测试企业", ShortName: "测试", Status: "enabled", LoginCode: "test",
	}).Error)

	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true,
		JWTKey: "test-jwt-key", AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024, AllowExt: storage.DefaultAllowExt,
	}
	r := buildEngine(t, db, cfg, up, nil)
	require.NoError(t, setting.Upsert("content_sec_enabled", "false"))
	return r, db
}

type writeCase struct {
	name, method, path string
	body               map[string]interface{}
	tbl                interface{}
}

func contentWriteCases() []writeCase {
	return []writeCase{
		{"公司基础信息", "PUT", "/api/admin/company-info",
			map[string]interface{}{"fullName": "甲公司"}, &model.CompanyInfo{}},
		{"业绩分类", "POST", "/api/admin/categories",
			map[string]interface{}{"name": "机房工程"}, &model.AchievementCategory{}},
		{"项目案例", "POST", "/api/admin/projects",
			map[string]interface{}{"name": "案例", "categoryId": 1}, &model.ProjectCase{}},
		{"业务服务", "POST", "/api/admin/services",
			map[string]interface{}{"title": "运维服务"}, &model.BizService{}},
		{"首页布局", "PUT", "/api/admin/home-layout",
			map[string]interface{}{"order": []string{"achievement", "service"}}, &model.HomeLayout{}},
	}
}

// TestContentWriteFailureIsReported：写库失败绝不能回 code:0。
// 每条先发一次「库还在」的请求证明参数合法，否则「失败」可能只是参数错误，
// 测试会绿得毫无意义。
func TestContentWriteFailureIsReported(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	for _, tc := range contentWriteCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, okBody := c.do(tc.method, tc.path, tc.body, tok)
			require.EqualValues(t, 0, okBody["code"], "%s 前置请求应成功：%v", tc.name, okBody)

			require.NoError(t, db.Migrator().DropTable(tc.tbl))
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.NotEqualValues(t, 0, body["code"], "%s 写库失败却回了 code:0", tc.name)
			msg := fmt.Sprint(body["msg"])
			require.Contains(t, msg, "失败", "%s 应给出可理解的失败文案", tc.name)
			// 运维要能在服务端日志看到真实 SQL 错误，客户端则只能拿到固定文案
			require.NotContains(t, msg, "no such table", "%s 把原始 SQL 错误回显给了客户端", tc.name)
		})
	}
}

// TestContentUpdateFailureIsReported：覆盖**更新**分支（DROP TABLE 只会走到新建分支）。
// 用 GORM 的 Update 回调注入错误，模拟「表在、行在，但 UPDATE 本身失败」——
// 这正是漏同步 migrations 后最典型的线上形态。
func TestContentUpdateFailureIsReported(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	// 注入回调之前先把待更新的行建好（回调只拦 Update，不拦 Create）
	require.NoError(t, db.Create(&model.CompanyInfo{CompanyID: 1, FullName: "旧名"}).Error)
	// 公司简介也必须预建：否则它走「新建」分支（Create 不受 Update 回调影响），
	// 用例会假绿——测到的根本不是更新失败这条路径。
	require.NoError(t, db.Create(&model.CompanyProfile{CompanyID: 1, VideoSource: model.VideoSourceNone}).Error)
	require.NoError(t, db.Create(&model.AchievementCategory{CompanyID: 1, Name: "旧分类"}).Error)
	require.NoError(t, db.Create(&model.BizService{CompanyID: 1, Title: "旧服务"}).Error)
	require.NoError(t, db.Create(&model.HomeLayout{CompanyID: 1, Order: `["achievement"]`}).Error)

	const cbName = "test:inject_update_error"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		tx.AddError(errors.New("injected update failure"))
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cbName) })

	cases := []struct {
		name, method, path string
		body               map[string]interface{}
	}{
		{"公司基础信息", "PUT", "/api/admin/company-info",
			map[string]interface{}{"fullName": "新名"}},
		{"公司简介（既有对照实现）", "PUT", "/api/admin/company-profile",
			map[string]interface{}{"introText": "新简介", "videoSource": "none"}},
		{"业绩分类", "PUT", "/api/admin/categories/1",
			map[string]interface{}{"name": "新分类", "icon": "", "sort": 0, "isLocked": false}},
		{"业务服务", "PUT", "/api/admin/services/1",
			map[string]interface{}{"title": "新服务", "icon": "", "content": "", "sort": 0}},
		{"首页布局", "PUT", "/api/admin/home-layout",
			map[string]interface{}{"order": []string{"service", "achievement"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.NotEqualValues(t, 0, body["code"], "%s 更新失败却回了 code:0", tc.name)
			require.NotContains(t, fmt.Sprint(body["msg"]), "injected", "%s 把底层错误原文回显给了客户端", tc.name)
		})
	}
}

// TestIdempotentUpdatesReportDBError：员工编辑 / 企业更新 / 企业停用这三处
// 过去只判 RowsAffected：SQL 执行失败时它同样是 0，于是真实错误被吞，
// 客户端只收到「未找到」。现在存在性单独查、UPDATE 只判 .Error。
func TestIdempotentUpdatesReportDBError(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, created := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "张三", "phone": "13900001234", "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, created["code"], "前置建员工失败：%v", created)
	staffID := int64(created["staffId"].(float64))

	const cbName = "test:inject_update_error_idempotent"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		tx.AddError(errors.New("injected update failure"))
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cbName) })

	cases := []struct {
		name, method, path string
		body               map[string]interface{}
	}{
		{"员工编辑", "PUT", fmt.Sprintf("/api/admin/employees/%d", staffID),
			map[string]interface{}{"name": "新名", "phone": "13900001234"}},
		{"企业更新", "PUT", "/api/admin/companies/1", map[string]interface{}{"name": "新名"}},
		{"企业停用", "POST", "/api/admin/companies/1/status", map[string]interface{}{"status": "disabled"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.NotEqualValues(t, 0, body["code"], "%s 写库失败却回了 ok", tc.name)
			msg := fmt.Sprint(body["msg"])
			require.NotContains(t, msg, "未找到", "%s 把写库失败误报成目标不存在", tc.name)
			require.NotContains(t, msg, "injected", "%s 回显了底层错误", tc.name)
			require.Contains(t, msg, "失败", "%s 应给出可理解的失败文案", tc.name)
		})
	}
}

// TestIdempotentUpdatesIgnoreZeroRowsAffected：MySQL 对「值没变」的 UPDATE 返回 0 行。
// 用回调强制模拟这一形态，证明三处保存不会被误报「未找到」——SQLite 本地跑
// 相同值仍报 1 行，测不到这个差异，所以必须显式注入。
func TestIdempotentUpdatesIgnoreZeroRowsAffected(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, created := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "李四", "phone": "13900005678", "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, created["code"])
	staffID := int64(created["staffId"].(float64))

	const cbName = "test:force_zero_rows"
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(cbName, func(tx *gorm.DB) {
		tx.RowsAffected = 0
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cbName) })

	cases := []struct {
		name, method, path string
		body               map[string]interface{}
	}{
		{"员工编辑", "PUT", fmt.Sprintf("/api/admin/employees/%d", staffID),
			map[string]interface{}{"name": "李四", "phone": "13900005678"}},
		{"企业更新", "PUT", "/api/admin/companies/1", map[string]interface{}{"name": "测试企业"}},
		{"企业停用", "POST", "/api/admin/companies/1/status", map[string]interface{}{"status": "disabled"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.EqualValues(t, 0, body["code"], "%s 原样保存被 MySQL 的 0 行误报：%v", tc.name, body)
		})
	}
}

// TestEmployeeLoginFailAuditHasNoPlainPII：登录失败审计不得成为 PII 加密的旁路。
// 员工表 phone/name 是静态加密列，审计表却是只追加的明文表；把手机号或姓名
// 写进去，等于任何一个能看审计日志的人都能拿到明文 PII。
func TestEmployeeLoginFailAuditHasNoPlainPII(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	phone, name := "13900001234", "张三"
	_, created := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": name, "phone": phone, "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, created["code"], "建员工应成功：%v", created)
	staffID := int64(created["staffId"].(float64))

	probe := newTestClient(r, t)
	_, body := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": phone, "password": "definitely-wrong",
	}, "")
	require.EqualValues(t, 1, body["code"])

	var row model.AuditLog
	require.NoError(t, db.Where("action = ?", audit.ActionLoginFail).
		Order("id desc").First(&row).Error)
	require.Equal(t, fmt.Sprint(staffID), row.TargetID, "审计应落到员工编号，而不是手机号")
	for _, field := range []string{row.TargetID, row.ActorName, row.Detail, row.IP} {
		require.NotContains(t, field, phone)
		require.NotContains(t, field, name)
	}
}

// gormWriteMethods：handler 层会直接调用的 GORM 写方法。
// 只盯接收者根为 db / tx 的调用——os.Create、up.Save、setting.Upsert 这些
// 同名/近名方法各有自己的错误通道，混进来只会制造误报，把守门变成噪音。
var gormWriteMethods = map[string]bool{
	"Create": true, "Save": true, "Delete": true, "Exec": true,
	"Updates": true, "Update": true, "UpdateColumn": true, "UpdateColumns": true,
	"CreateInBatches": true, "FirstOrCreate": true, "Upsert": true,
}

type scannedWrite struct {
	file, method, snippet string
	line                  int
	direct                bool // 结果直接被取 .Error / .RowsAffected
	checked               bool // 结果确实被检查了
}

// rootReceiverName 沿链式调用找到最左端的接收者，如 db.Model(&x).Updates(...) → "db"。
func rootReceiverName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return rootReceiverName(v.X)
	case *ast.CallExpr:
		return rootReceiverName(v.Fun)
	case *ast.IndexExpr:
		return rootReceiverName(v.X)
	}
	return ""
}

// enclosingFuncBody 找离写调用最近的函数体：判断「赋值后的变量有没有被检查」
// 必须限定在同一个函数里，否则文件里另一处 res.Error 会让本处漏判变绿。
func enclosingFuncBody(n ast.Node, parents map[ast.Node]ast.Node) ast.Node {
	for p := parents[n]; p != nil; p = parents[p] {
		switch v := p.(type) {
		case *ast.FuncLit:
			return v.Body
		case *ast.FuncDecl:
			return v.Body
		}
	}
	return nil
}

// resultChecked 判断一次写调用的结果有没有被消费。
//
// 合规形态只有两种：
//   - 直接取 .Error（含 `if err := db.Create(&x).Error; err != nil`）；
//   - 结果赋给变量，且同一函数体内检查了该变量的 .Error。
//
// **只看 RowsAffected 不算合规**：GORM 的 RowsAffected 在 SQL 执行失败时同样是 0，
// 只判它就会把「写库失败」误报成「未找到」，真实错误被彻底丢掉（曾出现在
// 员工编辑与企业更新 / 停用三处）。RowsAffected 仍可额外判，但不能替代 .Error。
//
// 裸语句（db.Create(&x)）、显式丢弃（_ = ...）、赋了值却从不看 .Error，一律算未检查。
func resultChecked(call *ast.CallExpr, parents map[ast.Node]ast.Node) (direct, checked bool) {
	switch v := parents[call].(type) {
	case *ast.SelectorExpr:
		if v.Sel.Name == "Error" {
			return true, true
		}
		if v.Sel.Name == "RowsAffected" {
			return true, false
		}
	case *ast.AssignStmt:
		if len(v.Lhs) == 0 {
			return false, false
		}
		id, ok := v.Lhs[0].(*ast.Ident)
		if !ok || id.Name == "_" {
			return false, false
		}
		body := enclosingFuncBody(call, parents)
		if body == nil {
			return false, false
		}
		found := false
		ast.Inspect(body, func(n ast.Node) bool {
			s, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := s.X.(*ast.Ident)
			if ok && x.Name == id.Name && s.Sel.Name == "Error" {
				found = true
			}
			return true
		})
		return false, found
	}
	return false, false
}

// writesInFile 用 AST 扫一个 Go 文件里的 GORM 写调用。
// 用 AST 而不是按行正则，是因为真实代码里写库几乎都长成
// `if err := db.Create(&x).Error; err != nil {` 或跨行的 `res := db.Model(...)`
// ——旧守门只认「行首 db.Create(」，一条也抓不到，于是永远全绿。
func writesInFile(fset *token.FileSet, f *ast.File, name string, src []byte) []scannedWrite {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})

	lines := strings.Split(string(src), "\n")
	var out []scannedWrite
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !gormWriteMethods[sel.Sel.Name] {
			return true
		}
		if root := rootReceiverName(sel.X); root != "db" && root != "tx" {
			return true
		}
		direct, checked := resultChecked(call, parents)
		line := fset.Position(call.Pos()).Line
		snippet := ""
		if line-1 < len(lines) {
			snippet = strings.TrimSpace(lines[line-1])
		}
		out = append(out, scannedWrite{name, sel.Sel.Name, snippet, line, direct, checked})
		return true
	})
	return out
}

func scanHandlerWrites(t *testing.T) ([]scannedWrite, []string) {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "守门必须在 handler 包目录下运行")
	var writes []scannedWrite
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, "解析 %s 失败", name)
		writes = append(writes, writesInFile(fset, f, name, src)...)
	}
	return writes, files
}

// TestNoIgnoredDBWritesAcrossHandlers：源码级守门，防止新增接口又把 GORM 的
// Error 丢掉。覆盖 handler 包**全部**生产 .go 文件（旧版只扫 4 个文件，且一条
// 真实语句都匹配不到）。
//
// 「量尺自己有读数」：写调用总数、直接取 .Error 的数量、锚点文件三道下限，
// 任何一道归零都说明解析器空转——那正是旧守门「全绿但什么都没盯」的死法。
func TestNoIgnoredDBWritesAcrossHandlers(t *testing.T) {
	writes, files := scanHandlerWrites(t)
	require.GreaterOrEqual(t, len(writes), 60,
		"扫到的写调用明显偏少：解析器空转会让下面全部恒绿，先修扫描器")
	direct := 0
	for _, w := range writes {
		if w.direct {
			direct++
		}
	}
	require.GreaterOrEqual(t, direct, 40,
		"直接取 .Error/.RowsAffected 的写法几乎没抓到，守门可能退化成只看赋值分支")
	for _, anchor := range []string{"apply.go", "content.go", "companies.go", "admins.go", "tenant_storage.go"} {
		require.Contains(t, files, anchor, "守门应覆盖 %s", anchor)
	}

	var bad []string
	for _, w := range writes {
		if !w.checked {
			bad = append(bad, fmt.Sprintf("%s:%d [%s] %s", w.file, w.line, w.method, w.snippet))
		}
	}
	require.Empty(t, bad, "以下写库语句没有判 Error / RowsAffected：\n%s", strings.Join(bad, "\n"))
}

// TestGormWriteGuardCatchesRealWorldShapes 守门自己的反向验证，跑在内存夹具上，
// 不需要临时改生产文件：故意混合「合规 / 裸写 / 裸写 / 赋值后不看结果」四种形态，
// 证明扫描器既能认出项目里真实使用的 `if err := ...Error` 写法，也确实会抓漏判。
//
// 少了这一条，只要有人把正则/方法清单改坏，上一条就会退化成「零违规 → 通过」。
//
// 夹具里还特意混进「只判 RowsAffected」和「先判 Error 再判 RowsAffected」两种形态：
// 前者必须红、后者必须绿——这正是本轮修掉的放水口。
func TestGormWriteGuardCatchesRealWorldShapes(t *testing.T) {
	const fixture = `package p
func f(db *gorm.DB, tx *gorm.DB) {
	if err := db.Create(&x).Error; err != nil { return }
	db.Model(&a).Update("username", "b")
	tx.Create(&c)
	res := db.Delete(&d)
	_ = res
	if db.Model(&e).Updates(v).RowsAffected == 0 { return }
	res2 := db.Model(&f).Updates(v)
	if res2.Error != nil { return }
	if res2.RowsAffected == 0 { return }
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
	require.NoError(t, err)
	writes := writesInFile(fset, f, "fixture.go", []byte(fixture))
	require.Len(t, writes, 6, "夹具里应有 6 处写调用，少一条说明扫描器漏抓")
	require.True(t, writes[0].checked, "if err := db.Create(&x).Error 是项目主流合规写法，必须放行")
	require.True(t, writes[0].direct)
	for i := 1; i <= 4; i++ {
		require.False(t, writes[i].checked,
			"第 %d 处是 bad case（%s），守门必须判为未检查", i+1, writes[i].snippet)
	}
	require.True(t, writes[5].checked,
		"先判 .Error 再看 RowsAffected 是完整写法，不能误报")
	require.False(t, writes[5].direct, "赋给变量的写法应走赋值分支")
}
