package storage

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// P0b 的边界清单见 docs/tenant-storage-url-boundaries.md。
//
// 这些断言**不依赖运行时行为**，纯源码对账：接入 NormalizeRef / ResolveRef 时
// 最容易出事的不是函数写错，是漏点——漏一个就是「同一张图 A 页面新键、
// B 页面旧绝对 URL」或后台表单把绝对 URL 写回库，页面全 200 无人报错。
// 所以清单必须是可执行的：新增写入点 / 公开接口 / 资源字段 / 端上消费点，一律先红。
//
// 与 TestRefWiringGate 一样，真正开始接线时这些断言会提示「按清单逐点接入并把断言改成已接入」，
// 不是让人删断言。

const (
	handlerDir = "../handler"
	modelDir   = "../model"
	frontSrc   = "../../../frontend/src"
	miniRoot   = "../../../miniprogram"
)

type boundaryToken struct {
	file  string
	token string
	why   string
}

// boundaryTokens 是清单里每个点的「锚」。代码挪走 / 改名 / 重写后锚必须还在，
// 否则说明清单腐了，先更新清单再谈接入。
var boundaryTokens = []boundaryToken{
	{handlerDir + "/upload.go", "up.Save(fh, objKey)", "W1 上传主入口（写入侧已接：落带前缀键）"},
	{handlerDir + "/upload.go", "storage.ObjectKeyFor(scope, cid, randName)", "W1 生成带前缀键（写入侧接线，第五十四轮）"},
	{handlerDir + "/upload.go", "submitMediaCheck(db, cfg, c, u)", "W1→W6 送检写 url / object_key"},
	{handlerDir + "/assets.go", "up.SaveBytes(objKey, data, assets.ContentType(item.File))", "W2 素材采用（固定平台 scope）"},
	{handlerDir + "/assets.go", "storage.ObjectKeyFor(storage.ScopePlatform, 0, rel)", "W2 平台资源落 p/ 前缀（写入侧接线，第五十四轮）"},
	{handlerDir + "/wechat.go", `up.SaveBytes(key, b, "image/jpeg")`, "W3 小程序码（固定平台 scope）"},
	{handlerDir + "/wechat.go", "storage.ObjectKeyFor(storage.ScopePlatform, 0, rel)", "W3 小程序码落 p/ 前缀（写入侧接线，第五十四轮）"},
	{handlerDir + "/wechat.go", `gin.H{"url": url}`, "R10 小程序码出参"},
	{handlerDir + "/media_check.go", "up.SaveBytes(row.ObjectKey, data, contentType)", "W4 下架占位图覆盖（将来必须带 row.CompanyID）"},
	{handlerDir + "/media_check.go", "func submitMediaCheck(", "W6 送检入口"},
	{handlerDir + "/media_check.go", "ObjectKey: d.Key", "W6 落库对象键"},
	{handlerDir + "/storage.go", `"url": finalURL`, "W5 直传 finalURL 回端上"},
	{handlerDir + "/storage.go", "storage.ObjectKeyFor(scope, cid, name)", "W5 预签名落带前缀键（写入侧接线，第五十四轮）"},
	{handlerDir + "/content.go", "func projectToMap(", `R12 album 展开（模型是 json:"-" 的隐藏边界）`},
	{handlerDir + "/handler.go", `"album":`, "R5/R6 案例详情 album 数组出参（已接 ResolveResourceRef）"},
	{handlerDir + "/handler.go", "ResolveResourceRef(ci.Logo)", "R2/R3 企业 logo 出参（已接 ResolveResourceRef）"},
	{handlerDir + "/handler.go", "ResolveResourceRef(e.Avatar)", "R3 名片墙 avatar 白名单出参（已接 ResolveResourceRef）"},
	{handlerDir + "/handler.go", "ResolveResourceRef(p.Cover)", "R6 案例详情 cover 出参（已接 ResolveResourceRef）"},
	{handlerDir + "/handler.go", "ResolveResourceRef(bgImage)", "R9 首页背景出参（已接 ResolveResourceRef）"},
	{handlerDir + "/handler.go", "model.BizService", "R8 服务详情内嵌整个模型（含 icon，已接 ResolveRefs）"},
	{modelDir + "/models.go", "column:album;type:json", "§2 Album 隐藏列：存的是 URL 数组"},
	{modelDir + "/media_check.go", `json:"objectKey"`, "§2 MediaCheck.ObjectKey：从 URL 反推的裸键"},
	{frontSrc + "/api/uploadFile.ts", "return p.data.url", "W7 直传 URL 回流业务表单"},
	{frontSrc + "/components/UploadField.vue", "(res.data.urls || [])[0]", "W8 multipart URL 回流"},
	{frontSrc + "/components/AssetPicker.vue", "emit('picked', resp.data.url)", "W9 素材采用 URL 回流"},
	{frontSrc + "/utils/thumb.ts", "thumbnailURL", "C2 缩略图派生契约"},
	{miniRoot + "/utils/request.ts", "function assetUrl", "C1 端上 assetUrl 契约"},
}

func TestP0bBoundaryTokensStillExist(t *testing.T) {
	for _, b := range boundaryTokens {
		data, err := os.ReadFile(b.file)
		require.NoError(t, err, "清单锚点文件不存在：%s（%s）", b.file, b.why)
		require.Contains(t, string(data), b.token,
			"%s 缺少 %q —— %s；代码挪走后必须先更新边界清单", b.file, b.token, b.why)
	}
}

// TestP0bUploaderWriteSitesMatchBoundaryList 钉住 W1–W4：
// 后端只有这四个文件真正调 Uploader 写对象并拿到 URL。
// 新增第五个写入点（比如某个新 handler 直接 up.Save）必须显式登记。
func TestP0bUploaderWriteSitesMatchBoundaryList(t *testing.T) {
	call := regexp.MustCompile(`\bup\.Save(Bytes)?\(`)
	got := map[string]bool{}
	entries, err := os.ReadDir(handlerDir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(handlerDir, e.Name()))
		require.NoError(t, err)
		if call.Match(b) {
			got[e.Name()] = true
		}
	}
	want := map[string]bool{
		"upload.go":      true, // W1
		"assets.go":      true, // W2
		"wechat.go":      true, // W3
		"media_check.go": true, // W4
	}
	require.Equal(t, want, got,
		"写入侧文件集合变了：新增/删除上传写入点必须先更新 docs/tenant-storage-url-boundaries.md §1")
}

// TestP0bPublicGetRoutesMatchBoundaryList 钉住 §3.1 的 9 条公开 GET。
//
// 关键事实：带 60 秒边缘缓存的只有 5 条，另外 4 条（company / team /
// projects/:id / services/:id）同样下发资源 URL 却没有缓存头。按「那五条」
// 去接 ResolveRef 就会漏一半出参。
func TestP0bPublicGetRoutesMatchBoundaryList(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(handlerDir, "handler.go"))
	require.NoError(t, err)
	src := string(b)

	routeRe := regexp.MustCompile(`r\.GET\("(/api/[a-zA-Z0-9/:_-]+)"`)
	got := map[string]bool{}
	for _, m := range routeRe.FindAllStringSubmatch(src, -1) {
		got[m[1]] = true
	}
	want := map[string]bool{
		"/api/card":         true,
		"/api/company":      true,
		"/api/team":         true,
		"/api/achievements": true,
		"/api/projects":     true,
		"/api/projects/:id": true,
		"/api/services":     true,
		"/api/services/:id": true,
		"/api/home-layout":  true,
	}
	require.Equal(t, want, got,
		"公开 GET 路由集合变了：新增公开接口必须确认是否下发资源 URL，并登记到边界清单 §3.1")

	// 只数真正的代码行，不算 handler.go:206 那条解释「刻意不设」的注释。
	cacheCall := regexp.MustCompile(`(?m)^\s*cdnCache\(c, 60\)\s*$`)
	require.Len(t, cacheCall.FindAllString(src, -1), 5,
		"P1 的五条 60 秒边缘缓存接口数量变了；ResolveRef 必须在这五条的 cdnCache 之前完成")
}

// TestP0bResourceJSONFieldsMatchBoundaryList 钉住 §2 的字段集合。
// 名字里带 url/image/logo/cover/avatar/icon/bg/file/album 的 json 字段，
// 必须是「8 个资源字段 + 5 个已写明理由的非资源字段」——新增同类字段必红。
//
// 这条守门在第五十八轮真拦下过一次：新增 model.TenantStorageConfig 时带了
// publicBaseUrl，没人会主动想到「配置表里的基址算不算资源引用」。红一次，
// 逼着把「它是 origin 的候选值、是解析器输入」写进清单，比事后回忆可靠。
func TestP0bResourceJSONFieldsMatchBoundaryList(t *testing.T) {
	tagRe := regexp.MustCompile(`json:"([^"]+)"`)
	kw := regexp.MustCompile(`(?i)(url|image|logo|cover|avatar|icon|bg|file|album)`)
	got := map[string]bool{}
	err := filepath.WalkDir(modelDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range tagRe.FindAllStringSubmatch(string(b), -1) {
			name := strings.Split(m[1], ",")[0]
			if name == "" || name == "-" {
				continue
			}
			if kw.MatchString(name) {
				got[name] = true
			}
		}
		return nil
	})
	require.NoError(t, err)

	want := map[string]bool{
		// 8 个真资源字段
		"avatar": true, "logo": true, "videoUrl": true, "videoBackupUrl": true,
		"cover": true, "icon": true, "bgImage": true, "url": true,
		// 5 个同名但不是「资源引用」的白名单（理由见清单 §2）
		"videoFallbackUrl": true, // bool 开关
		"bgMask":           true, // int 蒙层浓度
		"bgId":             true, // 内置背景 id
		"files":            true, // 存储用量累计文件数
		// publicBaseUrl 是唯一「确实是 URL、但角色相反」的一条：它是租户自有访问域名，
		// 也就是 ResolveRef(raw, origin) 里 origin 的候选值——解析器的**输入**，不是输出。
		// 对它调 ResolveRef 不会裂图，只会把配置页显示的基址悄悄改形，而且要到客户
		// 启用自有域名之后才暴露。第五十八轮新增模型时被这条守门抓到，登记在案。
		"publicBaseUrl": true,
	}
	require.Equal(t, want, got,
		"资源相关 json 字段集合变了：新增字段必须判断是不是 URL，并登记到边界清单 §2")

	// 两个「扫 json tag 必然漏掉」的隐藏边界，单独钉住。
	modelsGo, err := os.ReadFile(filepath.Join(modelDir, "models.go"))
	require.NoError(t, err)
	require.Regexp(t, `Album\s+string.*json:"-"`, string(modelsGo),
		"ProjectCase.Album 是 json:\"-\" 的 URL 数组列，靠 projectToMap 展开；不许悄悄改成直接出参")

	mediaCheckGo, err := os.ReadFile(filepath.Join(modelDir, "media_check.go"))
	require.NoError(t, err)
	require.Contains(t, string(mediaCheckGo), `column:object_key`,
		"MediaCheck.ObjectKey 是送检时反推的裸键；P0 之后 mediachk.ObjectKey 应退休")
}

// TestP0bMiniProgramAssetUrlConsumersMatchBoundaryList 钉住端上消费点：
// 小程序里凡是用 assetUrl 拼绝对资源地址的 .ts 文件都在这里，
// 新增页面用到资源 URL 就要重新确认 C1/C2 契约。
func TestP0bMiniProgramAssetUrlConsumersMatchBoundaryList(t *testing.T) {
	got := map[string]bool{}
	err := filepath.WalkDir(miniRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "miniprogram_npm", "dist", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".ts" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "assetUrl(") {
			rel, err := filepath.Rel(miniRoot, p)
			if err != nil {
				return err
			}
			got[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	require.NoError(t, err)

	want := map[string]bool{
		"pages/apply/open/open.ts":       true,
		"pages/employee/edit/edit.ts":    true,
		"pages/employee/login/login.ts":  true,
		"pages/employee/me/me.ts":        true,
		"pages/index/index.ts":           true,
		"pages/mydata/mydata.ts":         true,
		"pages/poster/poster.ts":         true,
		"pages/project/detail/detail.ts": true,
		"pages/project/list/list.ts":     true,
		"pages/team/team.ts":             true,
		"tests/company_video.spec.ts":    true,
		"utils/request.ts":               true,
		"utils/team.ts":                  true,
	}
	require.Equal(t, want, got,
		"小程序 assetUrl 消费文件集合变了：新增消费点必须确认端上 URL 形态契约")
}
