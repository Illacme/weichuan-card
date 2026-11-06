// Package urlprobe 提供后台「测试地址」用的**受限**出向探测：只发一个 HEAD、
// 不读响应体、不跟随重定向，用来回答「管理员填的 CDN 地址到底取不取得到」。
//
// 为什么不能简单 http.Head(url)：这个接口的入参是管理员自由文本，等于把服务端
// 变成一台可以打任意地址的代理（SSRF）。最经典的打法是填 http://169.254.169.254/
// 读云元数据、填 http://127.0.0.1:8080/ 打内网管理口，或用一个先解析到公网、
// 再重绑定到私网的域名绕过「只看字面量」的校验（DNS rebinding）。
//
// 这里的三层防护：
//  1. URL 形态：只 http/https、禁用户名密码、禁本机/内网/保留域名与端口写法；
//  2. 自己解析 DNS，**逐个**结果 IP 校验，任何一个落在私网/回环/链路本地/
//     组播/保留段就整体拒绝（不做「挑一个能用的」，那正是 rebinding 的入口）；
//  3. 把已校验的 IP **固定进 Transport.DialContext**，请求里 dial 的永远是这个 IP，
//     主机名只留在 Host 头与 TLS SNI 里。于是后续无论 DNS 怎么变都改不了目标，
//     Proxy=nil 也保证不会经代理绕开这层。
//
// 它只回答「服务端能不能取到」，不回答「用户手机能不能播」：运营商 / 微信客户端
// 的网络策略、证书链在真机上的表现仍要真机验证。文档里必须写清这个边界。
package urlprobe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxURLLen             = 2048
	dialTimeout           = 3 * time.Second
	tlsHandshakeTimeout   = 3 * time.Second
	responseHeaderTimeout = 4 * time.Second
	requestTimeout        = 6 * time.Second
	userAgent             = "weichuan-urlprobe/1.0"
)

// 对外可直接展示的错误文案：刻意不含内部主机名、解析结果与底层 err，
// 免得把服务器拓扑当成报错回给页面。细节只进服务端日志。
var (
	ErrEmptyURL      = errors.New("请先填写地址")
	ErrTooLong       = errors.New("地址过长")
	ErrInvalidScheme = errors.New("只支持 http / https 开头的地址")
	ErrCredentials   = errors.New("地址不能带用户名或密码")
	ErrInvalidPort   = errors.New("端口号不合法")
	ErrBlockedHost   = errors.New("不允许探测本机、内网或保留地址")
	ErrNoPublicIP    = errors.New("域名解析失败，或没有可用的公网地址")
	ErrRequest       = errors.New("连接超时或读取响应失败，请检查域名、证书与防火墙")
)

// Result 是给后台看的探测结论。HTTP 4xx/3xx 不是 Go error——那是「探测成功，
// 地址不通」，界面要显示状态码与具体建议；只有根本发不出请求才算 error。
type Result struct {
	Reachable     bool   `json:"reachable"`
	Status        int    `json:"status"`
	ContentType   string `json:"contentType"`
	ContentLength int64  `json:"contentLength"`
	Message       string `json:"message"`
}

// lookupIPFn / dialFn 抽成包级变量，让单测能在不联网、不碰公网的前提下
// 覆盖完整 HTTP 路径（测试把解析结果换成公网 IP、把拨号指向 httptest）。
// 生产代码不覆盖它们；与项目里 statTrackLimitFn 同一套路。
var (
	lookupIPFn = func(ctx context.Context, host string) ([]net.IP, error) {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		out := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, a.IP)
		}
		return out, nil
	}
	dialFn = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: dialTimeout}
		return d.DialContext(ctx, network, addr)
	}
)

// blockedCIDRs：Go 的 IsPrivate/IsLoopback 只覆盖 RFC1918 等常用段，
// 云元数据 169.254.169.254 已在链路本地里，但 CGNAT / TEST-NET / 基准测试段 /
// IPv6 文档段与 discard 段仍会被放过去。出站探测按「白名单之外一律拒绝」补齐。
var blockedCIDRs = func() []*net.IPNet {
	raw := []string{
		"0.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32",
		"fc00::/7", "fe80::/10", "ff00::/8",
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("urlprobe: 内置保留网段写错了: " + s)
		}
		out = append(out, n)
	}
	return out
}()

// blockedHosts 是精确匹配；blockedHostSuffixes 是后缀匹配，两者不能混：
// 写成一个列表再用 HasSuffix 会把 notlocalhost.example.com 也误杀。
var blockedHosts = []string{"localhost"}
var blockedHostSuffixes = []string{
	".localhost", ".local", ".internal", ".localdomain",
	".home.arpa", ".test", ".invalid",
}

// blockedIP 只对「能拨出去的公网单播地址」放行。
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6 先归一成 4 字节，否则 127.0.0.1 写成 ::ffff:127.0.0.1
	// 会躲过 v4 的 CIDR（IPNet.Contains 只在 v4 网段上做 To4 归一）。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// looksLikeObfuscatedIP 拦 0x7f000001 / 2130706433 这类整数或十六进制写法。
// 只拦「不带点的纯数字」和 0x 前缀，避免误杀 8.8.8.8 这种合法公网 IP 字面量；
// 带点的八进制写法（0177.0.0.1）交给后面的「自己解析 + blockedIP」兜住。
// 真正的安全边界是固定 IP 拨号，这里只让报错更早更明确。
func looksLikeObfuscatedIP(host string) bool {
	if strings.HasPrefix(strings.ToLower(host), "0x") {
		return true
	}
	if strings.Contains(host, ".") {
		return false
	}
	digits := 0
	for _, r := range host {
		if r < '0' || r > '9' {
			return false
		}
		digits++
	}
	return digits > 0
}

func validate(raw string) (*url.URL, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", ErrEmptyURL
	}
	if len(raw) > maxURLLen {
		return nil, "", fmt.Errorf("%w（超过 %d 字符）", ErrTooLong, maxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", ErrInvalidScheme
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, "", ErrInvalidScheme
	}
	u.Scheme = scheme
	if u.User != nil {
		return nil, "", ErrCredentials
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", ErrInvalidScheme
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range blockedHosts {
		if lower == h {
			return nil, "", ErrBlockedHost
		}
	}
	for _, suf := range blockedHostSuffixes {
		if strings.HasSuffix(lower, suf) {
			return nil, "", ErrBlockedHost
		}
	}
	if looksLikeObfuscatedIP(lower) {
		return nil, "", ErrBlockedHost
	}
	if net.ParseIP(lower) == nil && !strings.Contains(lower, ".") {
		// 单标签主机名会走本机搜索域（如 "intranet" → intranet.local），
		// 公网 CDN 域名一定带点。
		return nil, "", ErrBlockedHost
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, "", ErrInvalidPort
	}
	// fragment 不会发出去，但留着会让后续 u.String() 与真正请求的 URL 不一致。
	u.Fragment = ""
	u.RawFragment = ""
	return u, port, nil
}

// Probe 校验并发起一次受限 HEAD 请求。返回的 error 都是上面那几个可直接展示的
// sentinel（用 errors.Is 判断），底层错误只写日志。
func Probe(ctx context.Context, raw string) (Result, error) {
	u, port, err := validate(raw)
	if err != nil {
		return Result{}, err
	}
	host := u.Hostname()

	ips, err := lookupIPFn(ctx, host)
	if err != nil {
		log.Printf("[URLPROBE][WARN] 解析失败 host=%q err=%v", host, err)
		return Result{}, ErrNoPublicIP
	}
	if len(ips) == 0 {
		return Result{}, ErrNoPublicIP
	}
	pinned := net.IP(nil)
	for _, ip := range ips {
		if blockedIP(ip) {
			log.Printf("[URLPROBE][WARN] 拒绝解析到非公网地址 host=%q ip=%s", host, ip)
			return Result{}, ErrBlockedHost
		}
		if pinned == nil {
			pinned = ip
		}
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return Result{}, ErrInvalidScheme
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := newPinnedClient(pinned, port).Do(req)
	if err != nil {
		log.Printf("[URLPROBE][WARN] 探测失败 host=%q err=%v", host, err)
		return Result{}, ErrRequest
	}
	defer func() { _ = resp.Body.Close() }() // HEAD 无响应体，关闭只为复用连接；失败无需上报
	return describe(resp), nil
}

// newPinnedClient 把「已校验的 IP + 原始端口」写死进 DialContext。
// net/http 传进来的 addr 刻意忽略：它可能来自第二次 DNS 查询（rebinding）。
func newPinnedClient(pinned net.IP, port string) *http.Client {
	target := net.JoinHostPort(pinned.String(), port)
	tr := &http.Transport{
		Proxy: nil, // 显式关代理：否则请求会交给代理去解析主机名，绕过固定 IP
		DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			return dialFn(ctx, "tcp", target)
		},
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		IdleConnTimeout:       time.Second,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
	}
	// 不跟随重定向：3xx 的 Location 可能指向内网，也不能拿我们的 Host 头去访问。
	// ErrUseLastResponse 让 Do 把 3xx 当正常响应返回，由 describe 给出提示。
	return &http.Client{
		Transport:     tr,
		Timeout:       requestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func describe(resp *http.Response) Result {
	ct := resp.Header.Get("Content-Type")
	statusText := "HTTP " + strconv.Itoa(resp.StatusCode)
	r := Result{
		Status:        resp.StatusCode,
		ContentType:   ct,
		ContentLength: resp.ContentLength,
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/html") {
			r.Message = "返回的是网页（Content-Type: text/html），多半是 CDN 的错误页或目录写错，不是视频文件"
			return r
		}
		r.Reachable = true
		r.Message = statusText + "，地址可访问"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		r.Message = statusText + " 重定向；出于安全不跟随跳转，请直接填最终地址"
	case resp.StatusCode == 403:
		r.Message = statusText + " 禁止访问：检查 CDN 是否启用了 HTTPS（七牛未启用时 https 会回 httpsDisabled）、防盗链 Referer 或签名有效期"
	case resp.StatusCode == 404:
		r.Message = statusText + " 找不到对象：检查文件访问基址是否带上了对象存储里的目录前缀"
	case resp.StatusCode == 405 || resp.StatusCode == 501:
		r.Message = statusText + "：对方不支持 HEAD，请点开链接在浏览器里确认"
	default:
		r.Message = statusText + "，地址当前不可用"
	}
	return r
}
