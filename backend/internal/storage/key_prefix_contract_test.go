package storage

import (
	"strings"
	"testing"

	"weichuan/internal/nginxsnippet"
)

// TestLocationPrefixMatchesGoObjectKey 钉住一条跨包契约：
//
//	Go 侧写对象的键前缀  ==  nginx 侧 location 的前缀
//
// 两边各自独立拼字符串：`ObjectKeyFor` 拼 `TenantKeyPrefix + cid + "/"`，
// `nginxsnippet.writeLocation` 拼 `location ^~ /uploads/t/<cid>/`。任何一边被改
// （把 `t/` 改成 `tenants/`、或 location 里忘了带 cid），症状都是
// **对象写进去了、URL 也返回了、取图 404**，而且后台配置显示一切正常。
//
// 这类漂移只会在上线后才被发现，所以必须有一条测试把两个实现绑在一起：
// 它允许任何一方重构，只要两边仍然一致。
func TestLocationPrefixMatchesGoObjectKey(t *testing.T) {
	for _, cid := range []int64{1, 5, 50, 999999} {
		key, err := ObjectKeyFor(ScopeTenant, cid, "abc.jpg")
		if err != nil {
			t.Fatalf("cid=%d 生成对象键失败：%v", cid, err)
		}
		// 键去掉文件名，就是这个租户所有对象共有的前缀；nginx 的 location 必须正好是它。
		prefix, _, ok := strings.Cut(key, "abc.jpg")
		if !ok {
			t.Fatalf("cid=%d 的键形态异常（应含文件名）：%q", cid, key)
		}

		out, err := nginxsnippet.Render([]nginxsnippet.Target{{
			CompanyID: cid,
			Driver:    nginxsnippet.DriverMinio,
			Endpoint:  "bucket.example.com",
			Bucket:    "wc-bucket",
			Enabled:   true,
		}})
		if err != nil {
			t.Fatalf("cid=%d 渲染失败：%v", cid, err)
		}
		want := "location ^~ /uploads/" + prefix + " {"
		if !strings.Contains(out, want) {
			t.Fatalf("cid=%d：Go 侧键前缀是 %q，nginx 侧却没有对应的 location\n期望包含：%s\n实际输出：\n%s",
				cid, prefix, want, out)
		}
	}
}

// TestRewriteKeepsObjectKeyIntact 钉住另一半：location 里的 rewrite 只剥 /uploads/，
// 剩下的必须原样是对象键。
//
// 多剥一段（顺手把 t/<cid>/ 也剥掉）会让桶里的对象路径少一层，取图 404；
// 少剥一段则会在桶里多出一层 uploads/，同样 404。两个方向都不会报错。
func TestRewriteKeepsObjectKeyIntact(t *testing.T) {
	out, err := nginxsnippet.Render([]nginxsnippet.Target{{
		CompanyID: 7,
		Driver:    nginxsnippet.DriverMinio,
		Endpoint:  "bucket.example.com",
		Bucket:    "wc-t7",
		Enabled:   true,
	}})
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	const want = "rewrite ^/uploads/(.*)$ /$1 break;"
	if !strings.Contains(out, want) {
		t.Fatalf("rewrite 必须把 /uploads/<key> 映射成 /<key>（键布局只在 Go 侧定义）\n期望包含：%s\n实际输出：\n%s", want, out)
	}
}
