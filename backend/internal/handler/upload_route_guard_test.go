package handler

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUploadWritesRouteByObjectKeyPrefix 是本轮的「键前缀 ⇔ 实例」守门。
//
// 背景：对象键有两条互斥的路——
//
//	t/<cid>/...  由该租户自己的桶托管（nginx 按租户回源）
//	p/...        由平台 /uploads/ 回退段托管
//
// 实例与键前缀必须同源。两者错配时**不会报错**：文件真的写成功了、URL 也返回了，
// 只是取图 404；而素材采用与小程序码都是「固定对象名 + 幂等覆盖写」，
// 第一次错也不会有人发现（直到某天有人点开那张图）。
//
// 所以这里用源码级断言把每个文件的用法钉死：
//
//	upload.go / media_check.go  → 只能 For(cid)（写的是租户对象）
//	assets.go / wechat.go       → 只能用 Platform()（写的是平台对象）
//	storage.go                  → 两者都要有（presign/sign 走 For，本地文件流走 Platform）
//
// 将来某个文件确实需要跨这两种用法时，**改这条测试是正确动作**——
// 说明有人重新想了一遍归属，而不是顺手复制了一行。
func TestUploadWritesRouteByObjectKeyPrefix(t *testing.T) {
	cases := []struct {
		file     string
		wantFor  bool
		wantPlat bool
		why      string
	}{
		{
			file: "upload.go", wantFor: true, wantPlat: false,
			why: "普通上传写 t/<cid>/ 键，必须用该租户的实例",
		},
		{
			file: "media_check.go", wantFor: true, wantPlat: false,
			why: "一键下架是覆盖写 row.ObjectKey（可能是 t/<cid>/…），必须写到该租户的桶",
		},
		{
			file: "assets.go", wantFor: false, wantPlat: true,
			why: "素材采用写 p/library/… 平台前缀，写进客户桶后取图 404",
		},
		{
			file: "wechat.go", wantFor: false, wantPlat: true,
			why: "小程序码写 p/wxacode/… 平台前缀，同理",
		},
		{
			file: "storage.go", wantFor: true, wantPlat: true,
			why: "直传预签名/限时签名按租户取实例，本地私有文件流只读平台盘",
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			src, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			body := stripGoComments(string(src))

			if tc.wantFor {
				require.Contains(t, body, "res.For(",
					"%s 必须按租户取实例：%s", tc.file, tc.why)
			} else {
				require.NotContains(t, body, "res.For(",
					"%s 不得按租户取实例：%s", tc.file, tc.why)
			}
			if tc.wantPlat {
				require.Contains(t, body, "res.Platform()",
					"%s 必须用平台实例：%s", tc.file, tc.why)
			} else {
				require.NotContains(t, body, "res.Platform()",
					"%s 不得用平台实例：%s", tc.file, tc.why)
			}
		})
	}
}

// TestRegisterFuncsTakeResolver 防止有人把某个 Register 的参数改回 storage.Uploader。
//
// 改回去的那一处不会编译报错（Up 也是 Resolver 之外的另一种类型，只需要改调用点），
// 症状是该路径**悄悄失去租户路由**：别的路径写进客户桶，它继续写平台盘。
// 五个注入点里漏一个，等于承诺了「文件在你自己桶里」却没有兑现。
//
// 只判 Register 那一行签名，不判整个文件：像 resolveWxacodeURL 这类辅助函数
// 拿 storage.Uploader 是正常的（它要的是"某个实例"，而不是"按租户取实例"），
// 一刀切会把合理解耦也判成违规。
func TestRegisterFuncsTakeResolver(t *testing.T) {
	want := map[string]string{
		"upload.go":      "RegisterUpload",
		"storage.go":     "RegisterStorage",
		"media_check.go": "RegisterMediaCheck",
		"assets.go":      "RegisterAssets",
		"wechat.go":      "RegisterWechat",
	}

	for file, fn := range want {
		src, err := os.ReadFile(file)
		require.NoError(t, err)
		body := stripGoComments(string(src))

		sig := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "func "+fn+"(") {
				sig = line
				break
			}
		}
		require.NotEmpty(t, sig, "%s 里找不到 %s 的签名行", file, fn)
		require.Contains(t, sig, "res storage.Resolver",
			"%s 的 %s 必须收 storage.Resolver，实际签名：%s", file, fn, sig)
		require.NotContains(t, sig, "storage.Uploader",
			"%s 的 %s 不能退回 storage.Uploader（那一层已由 Resolver 接管）：%s", file, fn, sig)
	}
}

// TestAssemblyPassesResolverNotUploader 盯三处装配里最容易被漏掉的那一处。
//
// 项目纪律是「新增 handler.RegisterX 必须同时改 main.go / devserver / buildEngine 三处，
// 漏 devserver = 静默 404」。这一轮把「平台实例 + 档案解析」合并成同一个
// storage.Resolver 实现（storageprofile.Resolver），所以三处装配都必须
// SetPlatform 注入平台实例、并把 resolver 本身往下传。
//
// 为什么不能图省事退回 StaticResolver：那会让租户桶永远不生效——档案界面配了、
// 上传却一直写平台盘，而任何地方都不会报错。
func TestAssemblyPassesResolverNotUploader(t *testing.T) {
	src, err := os.ReadFile("handler_test.go")
	require.NoError(t, err)
	body := stripGoComments(string(src))
	require.Contains(t, body, "profiles.SetPlatform(up, cfg.StorageDriver)",
		"buildEngine 必须把平台实例注入档案解析器")
	require.Contains(t, body, "var effective storage.Resolver = profiles",
		"buildEngine 必须直接把档案解析器当 storage.Resolver 用")

	// 三处装配都要把 resolver 往下传，不能还留着旧的 up。
	require.NotContains(t, body, "RegisterUpload(r, db, cfg, up,",
		"buildEngine 仍把裸 up 传给 RegisterUpload")
	require.NotContains(t, body, "RegisterStorage(r, db, up,",
		"buildEngine 仍把裸 up 传给 RegisterStorage")

	for _, f := range []string{"../../cmd/server/main.go", "../../cmd/devserver/main.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		body := stripGoComments(string(src))
		require.Contains(t, body, "profiles.SetPlatform(up, platformDriver)",
			"%s 必须把平台实例注入档案解析器，且驱动取实际生效值（档案构造失败会降级 local）", f)
		// 静态包装层已整体移出生产包（测试桩在 internal/storage/testsupport），
		// 这里连「测试支持包」一起拦：装配点引用它等于把桩当成生产实现。
		require.NotContains(t, body, "NewStaticResolver(",
			"%s 不得再装配静态解析器：租户桶会永远不生效", f)
		require.NotContains(t, body, "storage/testsupport",
			"%s 不得把测试桩包进生产装配", f)
		require.NotContains(t, body, "uploadroute.",
			"%s 还在引用已删除的 uploadroute 包", f)
	}
}

// assemblyCallRE 只认「行首缩进后的路由装配调用」：不认注释里的解释、不认字符串里的
// 样例，也不会把 func RegisterXxx( 定义行当成已接线。注释已在 stripGoComments 剥掉，
// 这里再用行首锚点收一次，避免「注释写了记得注册」被判成「真的注册了」。
var assemblyCallRE = regexp.MustCompile(`(?m)^[ \t]*(?:handler\.)?(Register[A-Za-z0-9_]+)[ \t]*\(`)

// assemblyCalls 抓一个装配点的 RegisterXxx 名字集合；scope 非空时只看该函数体。
func assemblyCalls(t *testing.T, file, scope string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(file)
	require.NoError(t, err, "%s 读不到", file)
	body := stripGoComments(string(src))
	if scope != "" {
		start := strings.Index(body, "func "+scope+"(")
		require.GreaterOrEqual(t, start, 0, "%s 里找不到 func %s( 的函数体", file, scope)
		body = body[start:]
		if next := strings.Index(body, "\nfunc "); next >= 0 {
			body = body[:next]
		}
	}
	out := map[string]bool{}
	for _, m := range assemblyCallRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	return out
}

// namesOnlyIn 返回 a 有、b 没有的名字（排序后便于读失败信息）。
func namesOnlyIn(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestAssemblyParityAcrossThreeSites 盯「新增 handler.RegisterX 必须同时改三处」这条纪律。
//
// 背景：RegisterClientError 在两个 cmd/main.go 里都注册了，唯独 buildEngine 漏了，
// 于是 handler_test.go 顶部「装配与生产 main.go 等价」的承诺是假的。这类缺口不会
// 编译失败、单测也不会红（clienterror_test.go 自带独立引擎，跑得好好的），只有真的
// 对 buildEngine 发 /api/client-error 才会撞上 gin 的 404 纯文本。
//
// 判据用「两个生产入口互相相等」+「生产入口与测试引擎双向相等」，而不是写死一份
// 清单：写死的清单在加新路由时忘了更新，守门反而恒绿。
// 但仍保留数量下限与 RegisterClientError 锚点——抓取型守门最坏的坏法就是改个写法
// 后一条也抓不到，于是「三个空集合互相相等」全部通过。
func TestAssemblyParityAcrossThreeSites(t *testing.T) {
	server := assemblyCalls(t, "../../cmd/server/main.go", "")
	dev := assemblyCalls(t, "../../cmd/devserver/main.go", "")
	engine := assemblyCalls(t, "handler_test.go", "buildEngine")

	for name, got := range map[string]map[string]bool{
		"cmd/server/main.go":    server,
		"cmd/devserver/main.go": dev,
		"buildEngine":           engine,
	} {
		require.GreaterOrEqual(t, len(got), 20,
			"%s 只抓到 %d 条装配，正则可能失效了（守门不能空转通过）", name, len(got))
		require.Contains(t, got, "RegisterClientError", "%s 没抓到 RegisterClientError 锚点", name)
	}

	require.Empty(t, namesOnlyIn(server, dev),
		"cmd/devserver 漏注册这些路由（dev 环境静默 404）")
	require.Empty(t, namesOnlyIn(dev, server),
		"cmd/server 漏注册这些路由（生产静默 404）")
	require.Empty(t, namesOnlyIn(server, engine),
		"buildEngine 漏注册这些路由：集成测试 / SQLite-MySQL parity 覆盖不到，且「与生产等价」是假承诺")
	require.Empty(t, namesOnlyIn(engine, server),
		"buildEngine 注册了生产没有的路由（测试装配与线上不一致）")
}
