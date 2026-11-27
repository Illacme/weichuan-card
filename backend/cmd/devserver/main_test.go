package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestDevRoutesMatchProduction 守卫：devserver 的路由装配必须与生产（cmd/server）一致。
//
// 历史事故（2026-10-24，同源、连抓两次）：
//   - setup.RegisterStatus / RegisterSetupAlias：前端 main.ts 启动必探
//     /api/setup/status，devserver 没挂 → 每次启动一条 404；
//   - RegisterStatsExport / RegisterStatsDetail：/api/admin/stats 汇总本身在
//     RegisterApply 里，所以统计页能开，但「明细」抽屉与 CSV 导出一点就 404。
//
// 共同点：devserver 文件头承诺「复用与生产完全一致的路由装配」，但每次新增
// Register* 只改了 cmd/server，devserver **静默漂移**——漏注册不报编译错、
// 不报启动错，只在「点开某个功能」时才暴露，是典型的静默失败。
// 新增 handler.Register* 时两边都要加，否则本测试会红。
func TestDevRoutesMatchProduction(t *testing.T) {
	prod := handlerRegistrations(t, filepath.Join("..", "server", "main.go"))
	dev := handlerRegistrations(t, "main.go")
	if len(prod) == 0 {
		t.Fatal("未从 cmd/server/main.go 解析到任何 handler.Register* 调用——正则失效？")
	}
	for _, name := range sortedKeys(prod) {
		if !dev[name] {
			t.Errorf("devserver 缺少路由注册 %s（生产 cmd/server 有）——联调点开该功能会静默 404", name)
		}
	}

	// setup 的两个端点单列：生产在「已配置」分支挂它们，另有「未配置」分支的
	// setup.Register（devserver 不该有），故不参与上面的集合比较。
	devSrc := mustRead(t, "main.go")
	for _, name := range []string{"setup.RegisterStatus(", "setup.RegisterSetupAlias("} {
		if !strings.Contains(devSrc, name) {
			t.Errorf("devserver 缺少 %s（生产已配置分支有）", name)
		}
	}
}

// handlerRegistrations 从 main.go 源码抽出 `handler.RegisterXxx(` 形式的路由注册。
// 只认 handler 包：setup 包在生产里有「未配置」分支的调用，混进来会误报。
func handlerRegistrations(t *testing.T, path string) map[string]bool {
	t.Helper()
	src := mustRead(t, path)
	re := regexp.MustCompile(`handler\.Register([A-Za-z]*)\(`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out["handler.Register"+m[1]] = true
	}
	return out
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// sortedKeys 让报错顺序稳定（map 遍历顺序随机，多条红时不好读）。
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestUploadBaseURL(t *testing.T) {
	t.Setenv("DEV_UPLOAD_BASE_URL", "")
	if got := uploadBaseURL("8080"); got != "http://127.0.0.1:8080/uploads" {
		t.Fatalf("未设置时应回退本机默认值，got %s", got)
	}

	// 覆盖为局域网地址（末尾斜杠应被裁剪，避免 URL 出现双斜杠）
	t.Setenv("DEV_UPLOAD_BASE_URL", "http://192.168.1.10:8080/uploads/")
	if got := uploadBaseURL("8080"); got != "http://192.168.1.10:8080/uploads" {
		t.Fatalf("设置后应使用覆盖值并裁剪末尾斜杠，got %s", got)
	}

	// 仅空白字符等同于未设置
	t.Setenv("DEV_UPLOAD_BASE_URL", "   ")
	if got := uploadBaseURL("9090"); got != "http://127.0.0.1:9090/uploads" {
		t.Fatalf("空白值应回退默认值，got %s", got)
	}
}

// TestDevModelsCoverAllTables 守卫：devModels 必须覆盖 model 包的全部模型。
// 历史事故：A3 新增 RevokedSession（会话黑名单）时漏加进 devserver 的建表
// 清单——漏加不报编译错，只在运行期查 revoked_sessions 时报 no such table，
// 又被中间件「查询出错按已吊销处理」放大成「登录成功后所有请求 401」。
// 新增 GORM 模型时，请同步更新 devModels 与本列表。
func TestDevModelsCoverAllTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "guard.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(devModels...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	want := []string{
		"employees", "company_infos", "company_profiles", "achievement_categories",
		"project_cases", "biz_services", "home_layouts", "stat_events", "admins",
		"open_applies", "card_modify_applies", "revoked_sessions",
	}
	for _, name := range want {
		var cnt int64
		if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&cnt).Error; err != nil {
			t.Fatalf("query sqlite_master: %v", err)
		}
		if cnt == 0 {
			t.Errorf("表 %s 不存在——devModels 漏加了对应模型？", name)
		}
	}
}

// TestResolveDevDBPath 守门禁：DEV_DB_PATH 两种分支的来源正确。
// 回归场景（2026-11-27）：改了 Go 代码要重启 devserver，但默认临时库重启即重置，
// 会丢掉联调数据。加了 DEV_DB_PATH 复用选项后，必须确保：
//   - 未设置时仍走临时库（重启即重置的默认语义不能丢）；
//   - 设置时原样复用（含首尾空白容错），否则用户以为保留了其实又建了新库。
func TestResolveDevDBPath(t *testing.T) {
	t.Setenv("DEV_DB_PATH", "")
	p1, err := resolveDevDBPath()
	if err != nil {
		t.Fatalf("默认分支: %v", err)
	}
	if p1 == "" || !strings.Contains(filepath.Base(p1), "weichuan-dev-") {
		t.Errorf("未设 DEV_DB_PATH 应新建 weichuan-dev-* 临时库，得到 %q", p1)
	}
	p2, err := resolveDevDBPath()
	if err != nil {
		t.Fatalf("默认分支第二次: %v", err)
	}
	if p1 == p2 {
		t.Errorf("两次默认调用应各建一个新库，却都返回 %q", p1)
	}

	custom := filepath.Join(t.TempDir(), "my-dev.db")
	t.Setenv("DEV_DB_PATH", "  "+custom+"  ")
	got, err := resolveDevDBPath()
	if err != nil {
		t.Fatalf("指定分支: %v", err)
	}
	if got != custom {
		t.Errorf("DEV_DB_PATH 应原样复用（含 trim 空白），want %q got %q", custom, got)
	}
}
