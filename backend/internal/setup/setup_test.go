package setup

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"weichuan/internal/config"
	"weichuan/internal/pii"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// noConfigFile 把 CONFIG_FILE 指到一个不存在的临时路径：否则本机若跑过安装向导
// （./data/runtime.env 存在），文件里的值会渗进测试结果。
func noConfigFile(t *testing.T) {
	t.Helper()
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "absent.env"))
}

// 核心判据：只有「DB_DSN 根本没提供」才算未配置。
// DB_DSN 给了但连不上是线上真故障——若也算未配置，一次库抖动就会把引导页
// 暴露给终端用户（这正是本包刻意回避的行为）。
func TestUnconfiguredOnlyWhenDBDSNNotProvided(t *testing.T) {
	noConfigFile(t)
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"未设置", "", true},
		{"只有空白", "   ", true},
		{"已设置", "root:root@tcp(127.0.0.1:3306)/weichuan", false},
		{"设置了但明显是错的", "not-a-dsn", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DB_DSN", c.dsn)
			if got := Unconfigured(); got != c.want {
				t.Fatalf("DB_DSN=%q: Unconfigured()=%v, want %v", c.dsn, got, c.want)
			}
		})
	}
}

// 默认值不能用来判断「有没有配」：DB_DSN/JWT_KEY 都有内置缺省，
// 用 config.Load() 之后的值判断会把「没配」误判成「配了」。
func TestUnconfiguredUsesRawEnvNotDefaultedValue(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	if !Unconfigured() {
		t.Fatal("DB_DSN 未设置时必须判定为未配置（不能因为存在内置默认值就认为已配置）")
	}
}

// 安装向导把配置写进 CONFIG_FILE：重启后必须据此判定为「已配置」，
// 否则装完又回到引导页——这是配置层文件优先级的闭环点。
func TestUnconfiguredFalseWhenConfigFileHasDBDSN(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runtime.env")
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("DB_DSN", "")
	if !Unconfigured() {
		t.Fatal("环境变量和文件都没有 DB_DSN 时应判定为未配置")
	}
	if err := config.WriteFileValues(cfgPath, map[string]string{"DB_DSN": "sqlite:///data/weichuan.db"}); err != nil {
		t.Fatal(err)
	}
	if Unconfigured() {
		t.Fatal("配置文件里已有 DB_DSN，应判定为已配置")
	}
}

func TestStatusListsMissingAndMarksUnconfigured(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	t.Setenv("JWT_KEY", "")
	t.Setenv("ADMIN_PASS", "")
	st := StatusPayload()
	if st.Configured {
		t.Fatal("三项都缺时应为未配置")
	}
	if len(st.Missing) != 3 {
		t.Fatalf("应列出 3 项缺失，实际 %d：%+v", len(st.Missing), st.Missing)
	}
	seen := map[string]bool{}
	for _, m := range st.Missing {
		seen[m.Env] = true
	}
	for _, k := range []string{"DB_DSN", "JWT_KEY", "ADMIN_PASS"} {
		if !seen[k] {
			t.Fatalf("缺失清单应包含 %s，实际 %+v", k, st.Missing)
		}
	}

	// 补齐后：已配置、无缺失，且不再生成这几行
	t.Setenv("DB_DSN", "root@tcp(1.1.1.1:3306)/x")
	t.Setenv("JWT_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("ADMIN_PASS", "some-pass")
	st2 := StatusPayload()
	if !st2.Configured {
		t.Fatal("DB_DSN 已提供时应为已配置")
	}
	if len(st2.Missing) != 0 {
		t.Fatalf("补齐后不应有缺失项，实际 %+v", st2.Missing)
	}
	if strings.Contains(st2.EnvText, "JWT_KEY=") || strings.Contains(st2.EnvText, "DB_DSN=") {
		t.Fatalf("已配置的项不该再出现在建议配置里：\n%s", st2.EnvText)
	}
}

// 状态接口要带齐安装向导需要的预填信息，否则页面第一次渲染是全空的。
func TestStatusPayloadInstallerFields(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	t.Setenv("MYSQL_ROOT_PASSWORD", "some-root-pass") // 模拟 compose 编排环境
	st := StatusPayload()
	if !st.Installable {
		t.Fatal("未配置时应允许安装")
	}
	if len(st.Engines) != 2 || st.Engines[0] != "mysql" || st.Engines[1] != "sqlite" {
		t.Fatalf("应支持 mysql/sqlite 两种引擎，实际 %+v", st.Engines)
	}
	if st.SQLitePath == "" || st.ConfigFile == "" || st.AdminUser == "" {
		t.Fatalf("安装向导预填信息不全：%+v", st)
	}
	if filepath.Base(st.SQLitePath) != "weichuan.db" {
		t.Fatalf("SQLite 建议路径应落在配置目录旁，实际 %q", st.SQLitePath)
	}
	if st.MySQLHint == nil || st.MySQLHint.Host != "mysql" || st.MySQLHint.Port != "3306" {
		t.Fatalf("检测到编排环境时应给出 MySQL 预填值，实际 %+v", st.MySQLHint)
	}
	if st.MySQLHint.Note == "" {
		t.Fatal("预填值应带说明（密码要运维自己从 .env 抄）")
	}
	// 非编排环境不给预填，免得把无关部署的用户带偏
	t.Setenv("MYSQL_ROOT_PASSWORD", "")
	if StatusPayload().MySQLHint != nil {
		t.Fatal("无 MYSQL_ROOT_PASSWORD 时不应给 MySQL 预填值")
	}
}

// 生成的密钥必须够强且每次不同：绝不能回落到「已知默认值」——
// 那等于把公开文档里的凭据写进客户配置。
func TestGeneratedSecretsAreStrongAndUnique(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	t.Setenv("JWT_KEY", "")
	t.Setenv("ADMIN_PASS", "")
	first := EnvText()
	second := EnvText()

	jwt1 := valueOf(t, first, "JWT_KEY")
	jwt2 := valueOf(t, second, "JWT_KEY")
	if len(jwt1) < 16 {
		t.Fatalf("生成的 JWT_KEY 太短（%d 字符），低于 16 位下限", len(jwt1))
	}
	if jwt1 == "change-me" {
		t.Fatal("绝不能生成内置缺省密钥 change-me")
	}
	if jwt1 == jwt2 {
		t.Fatal("两次生成的 JWT_KEY 相同，随机源有问题")
	}
	if valueOf(t, first, "ADMIN_PASS") == valueOf(t, second, "ADMIN_PASS") {
		t.Fatal("两次生成的 ADMIN_PASS 相同")
	}
}

// 生成的 PII 密钥必须能被 pii 包原样接受。
//
// 这条专门防一个隐蔽的坑：pii 按 base64.StdEncoding 解码并要求恰好 32 字节，
// 而同文件里的 secret() 用的是 RawURLEncoding（- / _ 是合法字符）。
// 直接拿 secret(32) 当 PII_ENC_KEY，只有在随机值里恰好出现 - 或 _ 时才会
// 「密钥格式错误」——本地跑十次都正常、上线某次就起不来，极难归因。
func TestGeneratedPIIKeyAcceptedByPIIPackage(t *testing.T) {
	raw := piiKey()
	if raw == "" {
		t.Fatal("piiKey() 不应返回空串（随机源可用）")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("生成的 PII 密钥不是合法标准 base64：%v（值 %q）", err, raw)
	}
	if len(b) != 32 {
		t.Fatalf("生成的 PII 密钥应解码为 32 字节，实际 %d", len(b))
	}

	// 真正交给 pii 校验，而不是只比对编码规则
	t.Setenv(pii.KeyEnvVar, raw)
	pii.Reload()
	t.Cleanup(func() {
		os.Unsetenv(pii.KeyEnvVar)
		pii.Reload()
	})
	if err := pii.KeyError(); err != nil {
		t.Fatalf("生成的密钥被 pii 判为格式错误：%v", err)
	}
	if pii.UsingPlaceholderKey() {
		t.Fatal("给了真密钥后不应再报告占位密钥")
	}
}

// DB_DSN 是唯一无法存进库的项（配置本身就存在库里），建议里必须给出可改写的模板
func TestEnvTextContainsDBDSNTemplateAndCookieNote(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	txt := EnvText()
	for _, want := range []string{"DB_DSN=", "sqlite://", "JWT_KEY=", "ADMIN_PASS=", "COOKIE_SECURE="} {
		if !strings.Contains(txt, want) {
			t.Fatalf("建议配置应包含 %q，实际：\n%s", want, txt)
		}
	}
}

func TestStatusEndpointJSONShape(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	r := gin.New()
	RegisterStatus(r)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("状态接口应 200，实际 %d", w.Code)
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Configured bool     `json:"configured"`
			Missing    []string `json:"-"`
			EnvText    string   `json:"envText"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, w.Body.String())
	}
	if body.Code != 0 {
		t.Fatalf("code 应为 0，实际 %d", body.Code)
	}
	if body.Data.Configured {
		t.Fatal("DB_DSN 未设置时接口应返回 configured=false")
	}
	if body.Data.EnvText == "" {
		t.Fatal("接口应返回可复制的配置文本")
	}
}

// 引导模式：根路径跳到 /setup；/healthz 必须 503（进程在但业务不可用，
// 不能报 200 让编排误判为健康）
func TestSetupModeRoutes(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	r := Engine()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/setup" {
		t.Fatalf("/ 应 302 到 /setup，实际 %d %q", w.Code, w.Header().Get("Location"))
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "安装向导") {
		t.Fatalf("/setup 应返回安装向导页，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "手动配置") {
		t.Fatal("安装页应保留手动配置入口（老 .env 用户的退路）")
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置时 /healthz 应 503，实际 %d", w.Code)
	}
}

// 正常模式下 /setup 只是别名：安装完成后还有人手动敲 /setup，302 回首页，
// 而不是一屏裸 404，更不是又把安装页挂回来（重放漏洞）。
func TestNormalModeSetupIsAliasOnly(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "root@tcp(127.0.0.1:3306)/weichuan")
	r := gin.New()
	RegisterStatus(r)
	RegisterSetupAlias(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("已配置时 /setup 应 302 回 /，实际 %d %q", w.Code, w.Header().Get("Location"))
	}
	// 正常模式绝不挂写接口：装完还能再装一次 = 重置漏洞
	for _, m := range []string{http.MethodPost, http.MethodPut} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(m, "/api/setup/install", strings.NewReader("{}")))
		if w.Code != http.StatusNotFound {
			t.Fatalf("正常模式不应有安装写接口：%s /api/setup/install 返回 %d", m, w.Code)
		}
	}
}

// 安装模式的写接口必须带同源检查：这是未鉴权页面挡 CSRF / DNS rebinding 的底线。
func TestWriteEndpointsRequireSameOrigin(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	r := Engine()

	cross := httptest.NewRequest(http.MethodPost, "/api/setup/install", strings.NewReader("{}"))
	cross.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cross)
	if w.Code != http.StatusForbidden {
		t.Fatalf("跨源 POST 应 403，实际 %d", w.Code)
	}

	cross2 := httptest.NewRequest(http.MethodPost, "/api/setup/test", strings.NewReader("{}"))
	cross2.Header.Set("Referer", "http://evil.example/page")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, cross2)
	if w.Code != http.StatusForbidden {
		t.Fatalf("跨源 Referer POST 应 403，实际 %d", w.Code)
	}

	// 同源放行（这里会走到参数校验，返回 400 而不是 403）
	same := httptest.NewRequest(http.MethodPost, "/api/setup/test", strings.NewReader("{}"))
	same.Header.Set("Origin", "http://127.0.0.1:8080")
	same.Host = "127.0.0.1:8080"
	w = httptest.NewRecorder()
	r.ServeHTTP(w, same)
	if w.Code == http.StatusForbidden {
		t.Fatal("同源请求不应被 403 拦下")
	}
}

// 引导模式不提供业务写接口：安装向导是唯一的写入口，且自带护栏。
func TestSetupModeHasNoBusinessWriteEndpoints(t *testing.T) {
	noConfigFile(t)
	t.Setenv("DB_DSN", "")
	r := Engine()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, p := range []string{"/setup", "/api/setup/status", "/api/admin/settings", "/api/admin/login"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(m, p, nil))
			if w.Code == http.StatusOK {
				t.Fatalf("引导模式不应提供业务写接口：%s %s 返回 200", m, p)
			}
		}
	}
}

func valueOf(t *testing.T, envText, key string) string {
	t.Helper()
	for _, line := range strings.Split(envText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	t.Fatalf("配置文本中找不到 %s：\n%s", key, envText)
	return ""
}
