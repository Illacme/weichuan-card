package handler

import (
	"os"
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
// 漏 devserver = 静默 404」。本轮不新增路由，但把参数类型换了——漏改一处就是编译不过，
// 所以这里的价值不在"能不能编译"，而在于**三处都真的用了 resolver 而不是自己包一层
// 假分流**（例如图省事写 NewStaticResolver 到生产里，租户路由就永远不生效）。
func TestAssemblyPassesResolverNotUploader(t *testing.T) {
	src, err := os.ReadFile("handler_test.go")
	require.NoError(t, err)
	body := stripGoComments(string(src))
	require.Contains(t, body, "storage.NewStaticResolver(up, cfg.StorageDriver)",
		"buildEngine 必须用 StaticResolver 包住传入的 up：测试默认不启租户分流")

	// 三处装配都要把 resolver 往下传，不能还留着旧的 up。
	require.NotContains(t, body, "RegisterUpload(r, db, cfg, up,",
		"buildEngine 仍把裸 up 传给 RegisterUpload")
	require.NotContains(t, body, "RegisterStorage(r, db, up,",
		"buildEngine 仍把裸 up 传给 RegisterStorage")

	for _, f := range []string{"../../cmd/server/main.go", "../../cmd/devserver/main.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		body := stripGoComments(string(src))
		require.True(t,
			strings.Contains(body, "storage.NewStaticResolver(") &&
				strings.Contains(body, "uploadroute.New("),
			"%s 必须同时有「默认走 StaticResolver」与「开关打开后走 uploadroute.New」两条分支", f)
	}
}
