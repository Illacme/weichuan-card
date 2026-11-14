package storage

// 第六十九轮：出参侧「按租户选 origin」的接线守门。
//
// TestRefWiringGate 守的是「哪些文件可以调 ref API」，守不住另一类漏点：
// 新增第 10 条公开 GET、或有人把某条接口的 origin 顺手换成平台零值——
// 编译照过、现有单测照绿，只有访客那一页的图走错域名（或者该走客户域名的
// 没走）。所以这里按**路由块**逐个对账，并要求「至少 9 条」的量尺读数。
//
// 另一组断言盯的是「单点判定」：publicOriginFor 必须经 publicOriginStateFor
// → publicOriginState → tenantStorageActive。绕开任何一层，都会出现
// 「后台显示已生效、访客 URL 却没换」或反之，两边都不报错。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// goCodeWithoutLineComments 先剥整行注释：注释里解释「为什么不能用某写法」
// 不是一次真实调用（第六十八轮在 TestRefWiringGate 踩过同一条）。
func goCodeWithoutLineComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestPublicOriginWiringGate(t *testing.T) {
	read := func(parts ...string) string {
		p := filepath.Join(append([]string{".."}, parts...)...)
		b, err := os.ReadFile(p)
		require.NoError(t, err, "读取 %s 失败", p)
		return goCodeWithoutLineComments(string(b))
	}

	// ① 9 条公开 GET：凡下发资源引用，就必须按租户取 origin。
	handlerSrc := read("handler", "handler.go")
	routeRe := regexp.MustCompile(`r\.GET\("(/api/[^"]+)"`)
	resolveCallRe := regexp.MustCompile(`\.ResolveRefs\(|model\.ResolveResourceRefs?\(`)
	locs := routeRe.FindAllStringSubmatchIndex(handlerSrc, -1)
	require.GreaterOrEqual(t, len(locs), 9,
		"量尺要有读数：handler.go 里连 9 条 /api/ GET 都没抓到，检查正则或文件是否挪走")

	wired := 0
	for i, loc := range locs {
		end := len(handlerSrc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := handlerSrc[loc[0]:end]
		if !resolveCallRe.MatchString(block) {
			continue
		}
		wired++
		route := handlerSrc[loc[2]:loc[3]]
		require.Contains(t, block, "publicOriginFor(db, ",
			"公开路由 %s 下发了资源引用却没按租户取 origin；漏接就是该页图走错域名", route)
		require.NotContains(t, block, "model.PlatformOrigin()",
			"公开路由 %s 用了平台零值：后台出口才该这么传", route)
	}
	require.Equal(t, 9, wired,
		"9 条公开 GET 都必须接按租户 origin；数量变了要同步 docs/tenant-storage-url-boundaries.md")

	// ② /api/card 只能按员工归属取租户，绝不能读访客可改的 ?companyId=。
	require.Contains(t, handlerSrc, "origin := publicOriginFor(db, e.CompanyID)")

	// ③ 后台出口一律平台零值；apply.go 里「我的」是端上消费点、导出是后台出口。
	adminSrc := read("handler", "content.go")
	require.Contains(t, adminSrc, ".ResolveRefs(model.PlatformOrigin())")
	require.NotContains(t, adminSrc, "publicOriginFor(",
		"后台管理端不该接客户域名：多一条无谓的证书 / 回源裂图路径")
	applySrc := read("handler", "apply.go")
	require.Contains(t, applySrc, "publicOriginFor(db, e.CompanyID)",
		"小程序「我的」页是端上消费点，头像域名要与公开页一致")
	require.Contains(t, applySrc, ".ResolveRefs(model.PlatformOrigin())",
		"后台员工导出跑在平台域名下")

	// ④ 单点判定链：不许绕开任何一层另写乐观判断。
	originSrc := read("handler", "publicorigin.go")
	for _, want := range []string{
		"storage.ValidatePublicOrigin(row.PublicBaseURL)",
		"if ok, why := tenantStorageActive(row); !ok {",
		"if !row.PublicRead {",
		"return publicOriginState(row)",
		"base, active, _ := publicOriginStateFor(db, cid)",
		"if !active || base == \"\" {",
	} {
		require.Contains(t, originSrc, want, "origin 判定链缺了 %q", want)
	}

	// ⑤ model 层必须走「按对象归属选 origin」的解析器，不能简化成无差别拼域名。
	refsSrc := read("model", "refs.go")
	require.Contains(t, refsSrc, "storage.ResolveRefForTenant(raw, o.Base, o.CompanyID)")
	b, err := os.ReadFile("ref.go")
	require.NoError(t, err)
	refGo := goCodeWithoutLineComments(string(b))
	require.Contains(t, refGo, "func ResolveRefForTenant(")
	require.Contains(t, refGo, "refRel(s)")

	// ⑥ /api/public/settings 的诊断态只能挂在「显式 companyId」分支里。
	// 冷启动不带参数时按缺省企业下发 = 跨租户串号；行为由
	// TestPublicSettingsAssetOrigin 真发请求兜住，这里只钉住结构。
	settingsSrc := read("handler", "settings.go")
	guardIdx := strings.Index(settingsSrc, `strconv.ParseInt(c.Query("companyId")`)
	require.Greater(t, guardIdx, 0, "找不到显式 companyId 的闸门")
	require.Greater(t, strings.Index(settingsSrc, `resp["assetOriginActive"]`), guardIdx,
		"诊断字段必须在显式 companyId 闸门之后")
	require.Contains(t, settingsSrc, "publicOriginStateFor(db, q)")
}
