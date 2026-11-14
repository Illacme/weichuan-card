// Package nginxsnippet 把「哪些租户把自己的对象存到自己桶里」渲染成 nginx snippet。
//
// 为什么不把这件事写成一段部署脚本：snippet 里每一行都对应一个**静默失败**的坑
// （实证见 docs/tenant-storage-r1-feasibility.md）。把它做成代码 + 单测，
// 才可能在半年后有人「顺手改一下」时挡住他。三条不变量：
//
//  1. `location` 必须以 `/uploads/t/<cid>/` **带尾斜杠**结束。去掉斜杠，
//     `/uploads/t/50/...` 会被 `t/5` 前缀吃掉——用户的图从别人的桶里取，
//     而页面照常 200、没有一条报错。
//  2. `proxy_pass` **不带 URI 部分**（不写 `http://桶/t/5/`），靠 `rewrite` 剥前缀。
//     对象键布局的唯一真相在 `storage.ObjectKeyFor`；在这里再写一遍 `t/<cid>/`，
//     将来键布局一改就要同时改 Go 和每一条 snippet，漏一条 = 那家租户全站裂图且无从察觉。
//     **但桶名必须出现且只出现一次**：路径风格时进 path（`/<bucket>/$1`），
//     虚拟主机风格时由 Host 携带（`/$1`）。缺了它，请求打到桶的根上，键变成
//     `<bucket>/key`——第六十三轮真桶实测就是 400 InvalidBucketName，全站裂图。
//  3. `proxy_hide_header Cache-Control; proxy_hide_header Expires;` 必须在
//     `add_header Cache-Control ...` 之前出现。桶会把对象元数据里的 Cache-Control
//     一起回传，不藏掉就叠成两条，CDN 取哪条不确定（RFC 9111 不规定），
//     而且同样没有任何报错。
//
// 这个包只做纯计算：不读库、不写文件、不碰 nginx。落盘与执行在 cmd/nginx-snippets。
package nginxsnippet

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 驱动枚举，与 config.StorageDriver 的取值保持一致。
const (
	DriverLocal = "local"
	DriverMinio = "minio"
	DriverOSS   = "oss"
	DriverS3    = "s3"
)

// Target 是一个租户的存储归属。字段刻意与 tenant_storage_configs 对齐，
// 将来那张表落地时可以直接映射，不必再转换一层。
type Target struct {
	CompanyID int64  `json:"companyId"`
	Driver    string `json:"driver"`
	Endpoint  string `json:"endpoint"` // host[:port]，**不带 scheme**
	Bucket    string `json:"bucket"`
	UseSSL    bool   `json:"useSsl"`
	// HostHeader 是发给桶的 Host（virtual-hosted-style 的桶名在那里面，
	// 也是 TLS SNI 的取值）。留空时退化成 Endpoint。
	HostHeader string `json:"hostHeader"`
	Enabled    bool   `json:"enabled"`
}

// NeedsSnippet 判断这个租户是否需要一段 location。
//
// 「不需要」有两种，语义不同但结果一样，都靠它一个口子判定：
//   - 没启用（enabled=false）：关掉配置切回平台默认，对象回到平台盘；
//   - 驱动是 local：租户选择了「还是存平台盘」，没有桶可以把请求转过去。
//
// 这两种都必须让 snippet 消失。如果只判 enabled，把驱动改成 local 时会留下一条
// 指向不存在桶的 location → 那家租户所有图 404，而后台显示配置是好的。
func (t Target) NeedsSnippet() bool {
	return t.Enabled && t.Driver != DriverLocal
}

func (t Target) hostHeader() string {
	if strings.TrimSpace(t.HostHeader) != "" {
		return t.HostHeader
	}
	return t.Endpoint
}

// PathStyle 决定桶名进 path（/<bucket>/<key>）还是进 Host（<bucket>.<域名>/<key>）。
//
// 这条判定**不能按 driver 猜**：同为 "minio"，自建单机（一个 IP、没有通配 DNS）
// 只能是路径风格，而托管服务控制台上给你的多半是虚拟主机风格。所以由 host_header
// 这一列表达（列语义见 model/tenant_storage.go）：
//
//	留空 / 等于 endpoint          → 路径风格：rewrite 成 /<bucket>/$1
//	以 "<bucket>." 开头           → 虚拟主机风格：Host 已带桶名，path 只留对象键
//
// 为什么必须显式区分：两种风格拼出的**访问路径不同**，猜错的那一侧不会报错，
// 只会全站裂图。第六十三轮真桶实测（tools/s3-e2e）：按虚拟主机风格发给真 S3 端点，
// S3 把路径第一段当成桶名 → 400 InvalidBucketName（BucketName=t），
// 而后台那行配置看起来完全正常。
func (t Target) PathStyle() bool {
	return !strings.HasPrefix(t.hostHeader(), t.Bucket+".")
}

func (t Target) scheme() string {
	if t.UseSSL {
		return "https"
	}
	return "http"
}

// Validate 校验单条配置。这里的每一条都对应一种「配置看起来合法、实际会出事」。
func (t Target) Validate() error {
	if t.CompanyID <= 0 {
		return fmt.Errorf("company_id=%d 非法：0 是平台自身，平台对象走 /uploads/ 回退段，不该有 snippet", t.CompanyID)
	}
	switch t.Driver {
	case DriverLocal, DriverMinio, DriverOSS, DriverS3:
	default:
		return fmt.Errorf("company_id=%d 的 driver=%q 不在枚举内（local/minio/oss/s3）", t.CompanyID, t.Driver)
	}
	if !t.NeedsSnippet() {
		// 不生成 snippet 的行，桶地址填什么都不影响接入层，不必校验。
		return nil
	}
	if err := validHost(t.CompanyID, "endpoint", t.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(t.Bucket) == "" {
		return fmt.Errorf("company_id=%d 的 bucket 为空", t.CompanyID)
	}
	if strings.ContainsAny(t.Bucket, "/\\") || strings.Contains(t.Bucket, "..") {
		return fmt.Errorf("company_id=%d 的 bucket=%q 含路径分隔符或 ..", t.CompanyID, t.Bucket)
	}
	if t.HostHeader != "" {
		if err := validHost(t.CompanyID, "hostHeader", t.HostHeader); err != nil {
			return err
		}
		hh := strings.TrimSpace(t.HostHeader)
		// 既不是 endpoint、又不带 "<bucket>." 前缀的 Host，等于没说清是哪种风格。
		// 不在这里拦住，生成器只能猜；猜错的结果是那家租户全站裂图。
		if hh != strings.TrimSpace(t.Endpoint) && !strings.HasPrefix(hh, t.Bucket+".") {
			// 这段文案曾把 public_base_url 写成「属于下一阶段」，是把两件事混了：
			// host_header 是**回源侧**发给桶的 Host / TLS SNI，public_base_url 是
			// **访客侧** origin，两者正交。写「下一阶段」会让人以为要等自有域名
			// 功能上线才能配桶，其实这里此刻就有可照做的填法（见下面两句）。
			return fmt.Errorf("company_id=%d 的 hostHeader=%q 判断不出路径风格："+
				"它既不是 endpoint（%q），也不以 %q 开头。"+
				"留空表示路径风格（桶名进 path）；虚拟主机风格必须写成 <桶名>.<域名>。"+
				"hostHeader 是回源侧发给桶的 Host / TLS SNI，不是客户自有访问域名："+
				"后者填 public_base_url，两者互不影响",
				t.CompanyID, hh, strings.TrimSpace(t.Endpoint), t.Bucket+".")
		}
	}
	return nil
}

func validHost(cid int64, field, v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("company_id=%d 的 %s 为空", cid, field)
	}
	if strings.Contains(v, "://") {
		// 带 scheme 是最常见的填错方式：直接把控制台里的 URL 粘过来。
		return fmt.Errorf("company_id=%d 的 %s=%q 带了 scheme，这里只要 host[:port]", cid, field, v)
	}
	if strings.ContainsAny(v, "/\\ ") {
		return fmt.Errorf("company_id=%d 的 %s=%q 含路径或空格", cid, field, v)
	}
	if strings.Contains(v, `"`) {
		return fmt.Errorf("company_id=%d 的 %s=%q 含引号，会破坏 snippet 语法", cid, field, v)
	}
	return nil
}

// headerBlock 是每段 location 末尾必须原样出现的一组头。
//
// `add_header` 的继承陷阱：location 里只要出现任何一个 add_header，
// server 级那一组就整组失效。所以每段都要重声明，少一行就等于那家租户的
// 响应缺一个安全头，而且没有任何报错（线上 nginx-site.live.conf:160-173 已写明这点）。
const headerBlock = `        add_header Cache-Control $uploads_cache always;
        add_header X-Content-Type-Options nosniff always;
        add_header X-Frame-Options SAMEORIGIN always;
        add_header Referrer-Policy strict-origin-when-cross-origin always;
        add_header X-Robots-Tag "noindex, nofollow, noarchive" always;
`

// Render 渲染整份 snippet。输出**只有 location 段**，不含 map、不含 server：
// 生产上它要被 include 进 nginx-site.live.conf 那个 server{} 里
// （`$uploads_cache` 那个 map 已经在 http 级存在，snippet 里再定义一次就是重复定义，
// nginx -t 直接报错）。
func Render(targets []Target) (string, error) {
	seen := make(map[int64]bool, len(targets))
	for _, t := range targets {
		if err := t.Validate(); err != nil {
			return "", err
		}
		if !t.NeedsSnippet() {
			continue
		}
		if seen[t.CompanyID] {
			// 重复 cid 会生成两个同名 location → nginx -t 报 duplicate location，
			// 但报错只说行号，指不到是哪家租户配了两遍。
			return "", fmt.Errorf("company_id=%d 出现多次：会生成重复的 location，nginx -t 将以 duplicate location 失败", t.CompanyID)
		}
		seen[t.CompanyID] = true
	}

	live := make([]Target, 0, len(targets))
	for _, t := range targets {
		if t.NeedsSnippet() {
			live = append(live, t)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].CompanyID < live[j].CompanyID })

	var b strings.Builder
	b.WriteString("# 由 nginx-snippets 从 tenant_storage_configs 生成，勿手改。\n")
	b.WriteString("# 被 include 进 nginx-site.live.conf 的 server{} 里；$uploads_cache 由 http 级的 map 提供。\n")
	if len(live) == 0 {
		b.WriteString("# 当前没有任何租户接管存储：全部 /uploads/ 请求走回退段（平台盘）。\n")
	}
	for _, t := range live {
		writeLocation(&b, t)
	}
	return b.String(), nil
}

func writeLocation(b *strings.Builder, t Target) {
	fmt.Fprintf(b, "\n    # 租户 %d · driver=%s · bucket=%s · key 前缀 t/%d/\n",
		t.CompanyID, t.Driver, t.Bucket, t.CompanyID)
	// 尾斜杠是隔离边界，别删：没有它，/uploads/t/50/... 会被 t/5 吞掉。
	fmt.Fprintf(b, "    location ^~ /uploads/t/%d/ {\n", t.CompanyID)
	fmt.Fprintf(b, "        proxy_pass %s://%s;\n", t.scheme(), t.Endpoint)
	if t.PathStyle() {
		// 桶名必须进 path：只剥 /uploads/ 的话，请求会打到桶的根上，
		// 键的第一段（t）被 S3 当成桶名 → 400 InvalidBucketName。
		b.WriteString("        # 路径风格（host_header 未带桶名）：桶名进 path，否则 S3 会把键的第一段当桶名\n")
		fmt.Fprintf(b, "        rewrite ^/uploads/(.*)$ /%s/$1 break;\n", t.Bucket)
	} else {
		b.WriteString("        # 虚拟主机风格：桶名在 Host 里，path 只留对象键；键布局只在 Go 侧定义\n")
		b.WriteString("        rewrite ^/uploads/(.*)$ /$1 break;\n")
	}
	fmt.Fprintf(b, "        proxy_set_header Host \"%s\";\n", t.hostHeader())
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_read_timeout 60s;\n")
	b.WriteString("        # 桶自带的缓存头必须先藏掉，否则与下面的 add_header 叠成两条\n")
	b.WriteString("        proxy_hide_header Cache-Control;\n")
	b.WriteString("        proxy_hide_header Expires;\n")
	if t.UseSSL {
		// 真桶校验 SNI：只给 verify + CA + ssl_name 而漏掉 server_name，实测会被拒（421）。
		b.WriteString("        proxy_ssl_server_name on;\n")
		fmt.Fprintf(b, "        proxy_ssl_name \"%s\";\n", t.hostHeader())
	}
	b.WriteString(headerBlock)
	b.WriteString("    }\n")
}

// Diff 是「配置表」与「已生成的 snippet」之间的差集，两个方向都是故障：
//
//	Missing：配了桶却没有 snippet → 这些请求落回平台盘 → 文件不在那儿 → 404，页面裂；
//	Extra：  有 snippet 但配置表已停用 → 白开一个口子，把流量往一个没人维护的桶里送。
type Diff struct {
	Missing []int64
	Extra   []int64
}

func (d Diff) OK() bool { return len(d.Missing) == 0 && len(d.Extra) == 0 }

func (d Diff) String() string {
	if d.OK() {
		return "配置表与 snippet 一致"
	}
	var parts []string
	if len(d.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("配了存储但没有 snippet（这些租户的图会 404）：%v", d.Missing))
	}
	if len(d.Extra) > 0 {
		parts = append(parts, fmt.Sprintf("有 snippet 但配置表没启用（白开了个口子）：%v", d.Extra))
	}
	return strings.Join(parts, "；")
}

var locationCID = regexp.MustCompile(`(?m)^\s*location\s+\^~\s+/uploads/t/(\d+)/\s*\{`)

// Reconcile 对账：targets 里该有 snippet 的集合，必须与 snippet 文本里的 location 集合相等。
//
// 存在的理由很具体：加租户要经过「写配置表 → 生成 → nginx -t → reload」四步，
// 任何一步漏掉的表现都只是「新租户的图不显示」，而平台侧没有任何报错。
// 所以让它在生成阶段就报出来，而不是等用户发现裂图。
func Reconcile(targets []Target, snippet string) Diff {
	want := make(map[int64]bool, len(targets))
	for _, t := range targets {
		if t.NeedsSnippet() {
			want[t.CompanyID] = true
		}
	}
	got := make(map[int64]bool, len(want))
	for _, m := range locationCID.FindAllStringSubmatch(snippet, -1) {
		var cid int64
		if _, err := fmt.Sscanf(m[1], "%d", &cid); err != nil {
			continue
		}
		got[cid] = true
	}

	var d Diff
	for cid := range want {
		if !got[cid] {
			d.Missing = append(d.Missing, cid)
		}
	}
	for cid := range got {
		if !want[cid] {
			d.Extra = append(d.Extra, cid)
		}
	}
	sort.Slice(d.Missing, func(i, j int) bool { return d.Missing[i] < d.Missing[j] })
	sort.Slice(d.Extra, func(i, j int) bool { return d.Extra[i] < d.Extra[j] })
	return d
}
