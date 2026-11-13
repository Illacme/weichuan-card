package nginxsnippet

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Resolver 是 net.DefaultResolver 的最小子集。留出接口只是为了单测能注入失败，
// 生产不会有人换实现。
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// dnsTimeout 是单个域名的解析超时。取 3 秒：运维在后台点「保存」时等得起，
// 又不至于让一个黑洞 DNS 把整次生成拖到分钟级。
const dnsTimeout = 3 * time.Second

// PrecheckFailure 是某一家租户的桶域名解析失败。
type PrecheckFailure struct {
	CompanyID int64
	Host      string
	Err       error
}

func (f PrecheckFailure) Error() string {
	return fmt.Sprintf("company_id=%d 的桶域名 %q 解析失败：%v", f.CompanyID, f.Host, f.Err)
}

// Precheck 在落盘**之前**把每个桶域名解析一遍。
//
// 为什么必须先做这一步，而不是交给 nginx 去发现：`proxy_pass https://桶域名;` 里的
// 主机名是在**加载配置时**解析的，解不出来 `nginx -t` 直接 emerg。而宝塔「保存并 reload」
// 的顺序是 test 失败就不 reload —— 于是**整份配置的更新全部被卡住**，不只是这一家租户。
// 现象是「保存了但没生效」，没有任何一条报错指向那个打错的域名，
// 而所有人第一反应都会去查刚改的业务功能。
//
// 所以：解析失败就拒绝生成，并把是哪家租户、哪个域名、什么错一并交出去，
// 让调用方写进该租户的 status / last_error 并在后台标红。
//
// IP 字面量直接跳过 —— 它不需要解析，拿去做 LookupHost 反而在某些解析器上返回 NXDOMAIN。
func Precheck(ctx context.Context, r Resolver, targets []Target) []PrecheckFailure {
	if r == nil {
		r = net.DefaultResolver
	}
	var out []PrecheckFailure
	for _, t := range targets {
		if !t.NeedsSnippet() {
			continue
		}
		// HostHeader 留空时等于 Endpoint，别把同一个域名解析两遍。
		hosts := []string{hostOnly(t.Endpoint)}
		if hh := hostOnly(t.hostHeader()); hh != hosts[0] {
			hosts = append(hosts, hh)
		}
		for _, host := range hosts {
			if host == "" || net.ParseIP(host) != nil {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
			_, err := r.LookupHost(cctx, host)
			cancel()
			if err != nil {
				out = append(out, PrecheckFailure{CompanyID: t.CompanyID, Host: host, Err: err})
			}
		}
	}
	return out
}

// hostOnly 去掉端口。endpoint 允许写 `host` 或 `host:port`（也允许 `[::1]:9000`）。
func hostOnly(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		return h
	}
	// 没有端口：可能是裸 IPv6（含冒号但没方括号），去掉方括号后原样返回。
	return strings.Trim(endpoint, "[]")
}
