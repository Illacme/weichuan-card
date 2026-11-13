package nginxsnippet

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeResolver struct {
	bad   map[string]bool
	calls []string
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	f.calls = append(f.calls, host)
	if f.bad[host] {
		return nil, errors.New("no such host")
	}
	return []string{"10.0.0.1"}, nil
}

// IP 字面量不需要解析；拿去做 LookupHost 在部分解析器上反而返回 NXDOMAIN，
// 于是「配置没错却被拒生成」。
func TestPrecheckSkipsIPLiterals(t *testing.T) {
	r := &fakeResolver{}
	targets := []Target{
		cloud(5, "127.0.0.1:9001", "b5"),
		cloud(6, "[::1]:9002", "b6"),
	}
	if fails := Precheck(context.Background(), r, targets); len(fails) != 0 {
		t.Fatalf("IP 字面量不该被解析：%v", fails)
	}
	if len(r.calls) != 0 {
		t.Fatalf("不该发生任何解析调用，实际 %v", r.calls)
	}
}

func TestPrecheckReportsFailureWithIdentity(t *testing.T) {
	r := &fakeResolver{bad: map[string]bool{"bucket-does-not-exist.invalid": true}}
	fails := Precheck(context.Background(), r, []Target{
		cloud(5, "good.example.com:9000", "b5"),
		cloud(6, "bucket-does-not-exist.invalid", "b6"),
	})
	if len(fails) != 1 {
		t.Fatalf("应恰好 1 条失败，实际 %d：%v", len(fails), fails)
	}
	if fails[0].CompanyID != 6 {
		t.Fatalf("失败项要能指到租户，实际 company_id=%d", fails[0].CompanyID)
	}
	if !strings.Contains(fails[0].Error(), "company_id=6") ||
		!strings.Contains(fails[0].Error(), "bucket-does-not-exist.invalid") {
		t.Fatalf("错误文案要同时给出租户与域名：%s", fails[0].Error())
	}
}

// 只解析一次：HostHeader 留空时它等于 Endpoint，重复解析会把同一个失败报两遍，
// 运维会误以为配了两处。
func TestPrecheckDeduplicatesHostHeader(t *testing.T) {
	r := &fakeResolver{}
	Precheck(context.Background(), r, []Target{cloud(5, "one.example.com:9000", "b5")})
	if len(r.calls) != 1 {
		t.Fatalf("同一域名应只解析一次，实际 %d 次：%v", len(r.calls), r.calls)
	}

	r2 := &fakeResolver{}
	tg := cloud(5, "one.example.com:9000", "b5")
	tg.HostHeader = "bucket.example.com"
	Precheck(context.Background(), r2, []Target{tg})
	if len(r2.calls) != 2 {
		t.Fatalf("endpoint 与 hostHeader 不同时应各解析一次，实际 %v", r2.calls)
	}
}

func TestPrecheckSkipsInactiveTargets(t *testing.T) {
	r := &fakeResolver{bad: map[string]bool{"x.invalid": true}}
	local := cloud(8, "x.invalid:9000", "b8")
	local.Driver = DriverLocal
	off := cloud(9, "x.invalid:9000", "b9")
	off.Enabled = false
	if fails := Precheck(context.Background(), r, []Target{local, off}); len(fails) != 0 {
		t.Fatalf("不该为未接管的租户解析域名：%v", fails)
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"host:9000":       "host",
		"host":            "host",
		"[::1]:9000":      "::1",
		"127.0.0.1:9001":  "127.0.0.1",
		"bucket.s3.cn":    "bucket.s3.cn",
		"[2001:db8::1]":   "2001:db8::1",
		"  spaced:9000  ": "spaced",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q)=%q，期望 %q", in, got, want)
		}
	}
}
