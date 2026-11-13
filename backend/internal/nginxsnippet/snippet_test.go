package nginxsnippet

import (
	"strings"
	"testing"
)

func cloud(cid int64, endpoint, bucket string) Target {
	return Target{
		CompanyID: cid, Driver: DriverMinio, Endpoint: endpoint,
		Bucket: bucket, Enabled: true,
	}
}

// 三条不变量是本包存在的全部理由，逐条钉死。
// 这三条都能"看起来没问题"地改坏，然后把故障推到线上：
// 改坏 1 → 串桶；改坏 2 → 换键布局后整家租户 404；改坏 3 → CDN 缓存时长不确定。
func TestRenderEnforcesInvariants(t *testing.T) {
	out, err := Render([]Target{
		cloud(6, "h6:9002", "b6"),
		cloud(5, "h5:9001", "b5"),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// 不变量 1：location 必须带尾斜杠。
	if !strings.Contains(out, "location ^~ /uploads/t/5/ {") {
		t.Fatalf("缺少带尾斜杠的 location：\n%s", out)
	}
	if strings.Contains(out, "location ^~ /uploads/t/5 {") {
		t.Fatalf("出现了不带尾斜杠的 location（会吞掉 t/50/）：\n%s", out)
	}

	// 不变量 1b：按 cid 升序，输出稳定（否则每次生成都产生无意义 diff）。
	if strings.Index(out, "/uploads/t/5/") > strings.Index(out, "/uploads/t/6/") {
		t.Fatalf("location 未按 company_id 升序：\n%s", out)
	}

	// 不变量 2：proxy_pass 不带 URI 部分；前缀靠 rewrite 剥。
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "proxy_pass ") {
			continue
		}
		if strings.Contains(line, "/t/") {
			t.Fatalf("proxy_pass 里出现了租户前缀（键布局会双写）：%q", line)
		}
		if got := strings.TrimSpace(line); got != "proxy_pass http://h5:9001;" && got != "proxy_pass http://h6:9002;" {
			t.Fatalf("proxy_pass 形态不对：%q", got)
		}
	}
	if n := strings.Count(out, "rewrite ^/uploads/(.*)$ /$1 break;"); n != 2 {
		t.Fatalf("rewrite 剥前缀应每段一条，实际 %d 条", n)
	}

	// 不变量 3：先藏桶的缓存头，再发自己的。
	hideAt := strings.Index(out, "proxy_hide_header Cache-Control;")
	addAt := strings.Index(out, "add_header Cache-Control $uploads_cache always;")
	if hideAt < 0 || addAt < 0 || hideAt > addAt {
		t.Fatalf("proxy_hide_header 必须出现在 add_header 之前（hide=%d add=%d）", hideAt, addAt)
	}
	if !strings.Contains(out, "proxy_hide_header Expires;") {
		t.Fatalf("漏了 hide Expires")
	}

	// 安全头：每段 5 条（Cache-Control + 四件套）。add_header 继承陷阱靠这组重声明绕开。
	// 只数指令行 —— 注释里也提到 add_header，按子串数会多出来。
	directives := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "add_header ") {
			directives++
		}
	}
	if directives != 10 {
		t.Fatalf("两段 location 应有 10 条 add_header 指令，实际 %d", directives)
	}
}

func TestRenderSSLAddsSNI(t *testing.T) {
	tg := cloud(7, "bucket.example.com", "b7")
	tg.UseSSL = true
	out, err := Render([]Target{tg})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "proxy_pass https://bucket.example.com;") {
		t.Fatalf("use_ssl 没生效：\n%s", out)
	}
	if !strings.Contains(out, "proxy_ssl_server_name on;") {
		t.Fatalf("HTTPS 桶缺 proxy_ssl_server_name（实测会被桶拒 421）：\n%s", out)
	}
	if !strings.Contains(out, `proxy_ssl_name "bucket.example.com";`) {
		t.Fatalf("缺少 proxy_ssl_name：\n%s", out)
	}
}

// 两种「不需要 snippet」的语义都必须让段消失，否则切回平台默认会留下一条
// 指向不存在桶的 location —— 那家租户所有图 404，而后台显示配置是好的。
func TestRenderSkipsLocalAndDisabled(t *testing.T) {
	local := cloud(8, "h8:9003", "b8")
	local.Driver = DriverLocal
	off := cloud(9, "h9:9009", "b9")
	off.Enabled = false

	out, err := Render([]Target{cloud(5, "h5:9001", "b5"), local, off})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := strings.Count(out, "location "); n != 1 {
		t.Fatalf("只应有 1 段 location，实际 %d：\n%s", n, out)
	}
	for _, bad := range []string{"/uploads/t/8/", "/uploads/t/9/"} {
		if strings.Contains(out, bad) {
			t.Fatalf("不该出现 %s：\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "location ^~ /uploads/t/5/ {") {
		t.Fatalf("该接管的租户没接管：\n%s", out)
	}
}

func TestRenderNoTenantKeepsSnippetValid(t *testing.T) {
	out, err := Render(nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "location ") {
		t.Fatalf("空配置不该有条目：\n%s", out)
	}
	// 空文件也要能当合法 include 目标（nginx 对空 include 文件是允许的），
	// 留一行注释是为了让人打开文件时知道「这是空的，不是生成失败」。
	if !strings.Contains(out, "没有任何租户接管存储") {
		t.Fatalf("空配置缺少说明注释：\n%s", out)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		tg   Target
		want string
	}{
		{"cid 为 0", Target{CompanyID: 0, Driver: DriverMinio, Endpoint: "h:9000", Bucket: "b", Enabled: true}, "company_id=0"},
		{"driver 未知", Target{CompanyID: 1, Driver: "ftp", Endpoint: "h:9000", Bucket: "b", Enabled: true}, "driver="},
		{"endpoint 带 scheme", Target{CompanyID: 1, Driver: DriverMinio, Endpoint: "https://h:9000", Bucket: "b", Enabled: true}, "带了 scheme"},
		{"endpoint 含路径", Target{CompanyID: 1, Driver: DriverOSS, Endpoint: "h/9000", Bucket: "b", Enabled: true}, "含路径或空格"},
		{"bucket 含斜杠", Target{CompanyID: 1, Driver: DriverOSS, Endpoint: "h:9000", Bucket: "a/b", Enabled: true}, "含路径分隔符"},
		{"bucket 为空", Target{CompanyID: 1, Driver: DriverOSS, Endpoint: "h:9000", Bucket: "", Enabled: true}, "bucket 为空"},
		{"注入引号", Target{CompanyID: 1, Driver: DriverMinio, Endpoint: `h:9000`, Bucket: "b", HostHeader: `a";}`, Enabled: true}, "含引号"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Render([]Target{c.tg})
			if err == nil {
				t.Fatalf("应被拒绝但通过了：%+v", c.tg)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息没点明原因，期望包含 %q，实际 %q", c.want, err.Error())
			}
		})
	}
}

// 未接管的行（local / 停用）不校验桶地址：桶名留空是正常状态，
// 若在这里报错，运维会遇到「只想关掉一家却存不进去」。
func TestValidateIgnoresInactiveTargets(t *testing.T) {
	if _, err := Render([]Target{
		{CompanyID: 3, Driver: DriverLocal, Enabled: true},
		{CompanyID: 4, Driver: DriverOSS, Enabled: false},
	}); err != nil {
		t.Fatalf("未接管的行不该被校验：%v", err)
	}
}

func TestRenderRejectsDuplicateCID(t *testing.T) {
	_, err := Render([]Target{cloud(5, "h5:9001", "b5"), cloud(5, "h5b:9001", "b5b")})
	if err == nil || !strings.Contains(err.Error(), "出现多次") {
		t.Fatalf("重复 cid 应被拒绝，实际 err=%v", err)
	}
}

func TestReconcileBothDirections(t *testing.T) {
	// 只生成 5，但配置表里 5 和 6 都该有 → 6 missing。
	out, err := Render([]Target{cloud(5, "h5:9001", "b5")})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	d := Reconcile([]Target{cloud(5, "h5:9001", "b5"), cloud(6, "h6:9002", "b6")}, out)
	if d.OK() {
		t.Fatal("少一段应该报出来")
	}
	if len(d.Missing) != 1 || d.Missing[0] != 6 {
		t.Fatalf("Missing 应恰好是 [6]，实际 %v", d.Missing)
	}
	if !strings.Contains(d.String(), "6") {
		t.Fatalf("文案要点明是哪家租户：%s", d.String())
	}

	// 反向：snippet 里有 7，但配置表已经停用 7 → extra。
	d2 := Reconcile([]Target{cloud(5, "h5:9001", "b5")}, out+"    location ^~ /uploads/t/7/ {\n    }\n")
	if len(d2.Extra) != 1 || d2.Extra[0] != 7 {
		t.Fatalf("Extra 应恰好是 [7]，实际 %v", d2.Extra)
	}
	if !strings.Contains(d2.String(), "白开了个口子") {
		t.Fatalf("extra 的文案要说明危害：%s", d2.String())
	}
}

// 对账必须能解析自己生成的输出 —— 否则「生成成功但对账报错」会变成常态噪声，
// 运维就会开始忽略它。
func TestReconcileAcceptsOwnOutput(t *testing.T) {
	targets := []Target{cloud(5, "h5:9001", "b5"), cloud(12, "h12:9012", "b12")}
	out, err := Render(targets)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d := Reconcile(targets, out); !d.OK() {
		t.Fatalf("自己生成的输出对不上：%v", d)
	}
	// local 驱动的租户不该被算进 want，否则永远报 missing。
	targets = append(targets, Target{CompanyID: 20, Driver: DriverLocal, Enabled: true})
	if d := Reconcile(targets, out); !d.OK() {
		t.Fatalf("local 驱动的租户被误算成缺失：%v", d)
	}
}
