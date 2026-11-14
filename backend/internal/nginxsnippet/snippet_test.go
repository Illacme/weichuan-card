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

	// 不变量 2：proxy_pass 不带 URI 部分；前缀靠 rewrite 剥，桶名必须出现且只出现一次。
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
	// 这两个 target 的 hostHeader 都留空 ⇒ 路径风格 ⇒ 各自把桶名带进 path。
	// 少了桶名那一段，真 S3 会把键的第一段当成桶名（第六十三轮真桶实测）。
	for _, want := range []string{
		"rewrite ^/uploads/(.*)$ /b5/$1 break;",
		"rewrite ^/uploads/(.*)$ /b6/$1 break;",
	} {
		if n := strings.Count(out, want); n != 1 {
			t.Fatalf("路径风格应各出现一次 %q，实际 %d 次：\n%s", want, n, out)
		}
	}
	// 桶名不许出现两次（路径里一次 + 别处又一次 = 键变成 <bucket>/<bucket>/...）。
	if n := strings.Count(out, "/b5/b5/"); n != 0 {
		t.Fatalf("桶名被拼了两遍：\n%s", out)
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

// 路径风格与虚拟主机风格的差别不是写法偏好，而是**对象在桶里的访问路径不同**。
// 这条用例把两种形态钉死，避免有人「统一一下」把它们合成一种。
func TestRenderBucketAppearsExactlyOnce(t *testing.T) {
	// 路径风格：host_header 留空，桶名只能靠 path 携带。
	path := cloud(5, "minio.internal:9000", "wc-t5")
	out, err := Render([]Target{path})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "rewrite ^/uploads/(.*)$ /wc-t5/$1 break;") {
		t.Fatalf("路径风格缺桶名段（真 S3 会 400 InvalidBucketName）：\n%s", out)
	}
	if !strings.Contains(out, `proxy_set_header Host "minio.internal:9000";`) {
		t.Fatalf("路径风格的 Host 必须是 endpoint 本身：\n%s", out)
	}

	// 虚拟主机风格：Host 已带桶名，path 里再带一次 = 键变成 wc-t5/wc-t5/...
	vh := cloud(6, "s3.ap-guangzhou.amazonaws.com", "wc-t6")
	vh.HostHeader = "wc-t6.s3.ap-guangzhou.amazonaws.com"
	out, err = Render([]Target{vh})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "rewrite ^/uploads/(.*)$ /$1 break;") {
		t.Fatalf("虚拟主机风格不该把桶名拼进 path：\n%s", out)
	}
	if strings.Contains(out, "/uploads/(.*)$ /wc-t6/") {
		t.Fatalf("虚拟主机风格的 rewrite 里出现了桶名（键会变成 wc-t6/wc-t6/...）：\n%s", out)
	}
	if !strings.Contains(out, `proxy_set_header Host "wc-t6.s3.ap-guangzhou.amazonaws.com";`) {
		t.Fatalf("虚拟主机风格的 Host 应原样使用 host_header：\n%s", out)
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
		// host_header 既不是 endpoint、也不带 "<bucket>." 前缀 ⇒ 判断不出路径风格。
		// 猜错的后果是整家租户裂图，而配置看起来完全正常，所以必须当场拒绝。
		{"hostHeader 判断不出风格", Target{CompanyID: 1, Driver: DriverMinio, Endpoint: "h:9000", Bucket: "wc-t1", HostHeader: "cdn.example.com", Enabled: true}, "判断不出路径风格"},
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

// TestValidateHostHeaderErrorSeparatesOriginFromUpstream 钉住第六十八轮的文案订正。
//
// 旧文案写「客户自有访问域名属于下一阶段（public_base_url），本期不生成」，
// 把**回源侧** host_header 与**访客侧** public_base_url 混成了一件事。运维看到
// 这句话会以为「桶要等自有域名上线才能配」，其实这里当场就有两种可照做的填法。
// 把文案改回旧版，下面三条断言必红——这就是本守门的反向验证。
func TestValidateHostHeaderErrorSeparatesOriginFromUpstream(t *testing.T) {
	tg := Target{CompanyID: 1, Driver: DriverMinio, Endpoint: "h:9000",
		Bucket: "wc-t1", HostHeader: "cdn.example.com", Enabled: true}
	_, err := Render([]Target{tg})
	if err == nil {
		t.Fatal("判断不出路径风格的 hostHeader 必须被拒绝")
	}
	msg := err.Error()
	if !strings.Contains(msg, "public_base_url") {
		t.Fatalf("报错必须指出客户自有域名该填哪个字段：%q", msg)
	}
	if strings.Contains(msg, "下一阶段") {
		t.Fatalf("别再说「下一阶段」：这会让运维以为桶现在配不了：%q", msg)
	}
	if !strings.Contains(msg, "回源侧") {
		t.Fatalf("报错必须说清 hostHeader 是回源侧而非访客侧：%q", msg)
	}
	// 拒绝的是 hostHeader 本身，不是「配了自有域名」这件事：把 hostHeader 改合法，
	// 同一个桶立刻能生成，不需要等任何功能上线。
	tg.HostHeader = ""
	if _, err := Render([]Target{tg}); err != nil {
		t.Fatalf("路径风格（hostHeader 留空）必须当场可用：%v", err)
	}
}
