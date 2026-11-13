package handler

// 写入侧资源引用归一（第五十九轮）的守门与端到端断言。
//
// 这一轮改的是「库里存什么」，而接口层看起来毫无变化——出参永远是解析后的 URL。
// 所以断言必须落到**库里的值**上：只测出参的话，归一没生效也照样全绿。

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/model"
)

// 写入点数量守门。
//
// 拦的是这一轮最可能的退化：新增一个存资源字段的接口，忘了调归一。它不会报错、
// 不会裂图（出参仍能把历史形态解析出来），只是库里的形态慢慢又混起来，
// 而混起来之后第一次搬存储（P3）就会踩到「有的键能推、有的推不出」。
//
// 数量一变就红，逼人回答「新加的这处接没接」。这只是**计数**层面的粗守门，
// 精确到「每个字段都接了吗」靠的是下面那些端到端断言。
func TestResourceRefWriteSiteCounts(t *testing.T) {
	want := map[string]struct{ single, multi int }{
		// company-info.logo / company-profile(videoUrl/videoBackupUrl/cover 循环) /
		// category.icon ×2 / project.cover / service.icon ×2 / home-layout.bgImage
		"content.go": {single: 8, multi: 1},
		// open-apply / card-modify 提交 / 开通审批 / 超管新增 / 超管编辑 / 名片修改审批
		"apply.go": {single: 6, multi: 0},
	}
	single := regexp.MustCompile(`normalizeResourceRef\(`)
	multi := regexp.MustCompile(`normalizeResourceRefs\(`)

	var files []string
	for name := range want {
		files = append(files, name)
	}
	sort.Strings(files)

	for _, name := range files {
		b, err := os.ReadFile(name)
		require.NoError(t, err, "读 %s 失败（工作目录应是 internal/handler）", name)
		src := string(b)
		require.Equal(t, want[name].single, len(single.FindAllString(src, -1)),
			"%s 的 normalizeResourceRef 调用点数变了：新增写入点必须接归一，删点也要确认（见 docs/tenant-storage-url-boundaries.md §1）", name)
		require.Equal(t, want[name].multi, len(multi.FindAllString(src, -1)),
			"%s 的 normalizeResourceRefs 调用点数变了（album 这类数组字段专用）", name)
	}
}

// 端到端：本地路径写进去 → 库是裸键 → 出参补回 /uploads/。
//
// 这条覆盖最常见的形态：前端表单回传的是**出参解析过的 URL**（/uploads/t/1/x.png），
// 直接写库会让「裸键」这件事永远不成立——上传侧收敛多少都没用。
func TestResourceRefWriteNormalizesLocalPath(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
		"fullName": "测试企业", "shortName": "测试", "logo": "/uploads/t/1/logo.png",
	}, token)
	require.EqualValues(t, 0, body["code"], "保存失败：%v", body)

	var ci model.CompanyInfo
	require.NoError(t, db.First(&ci, "company_id = ?", defaultCompanyID).Error)
	require.Equal(t, "t/1/logo.png", ci.Logo,
		"库里必须是带归属前缀的裸键；存成 /uploads/... 等于写入侧收敛没生效")

	// 出参契约不变：端上拿到的仍是 /uploads/ 开头的可访问地址。
	_, body = c.do("GET", "/api/admin/company-info", nil, token)
	data := body["data"].(map[string]interface{})
	require.Equal(t, "/uploads/t/1/logo.png", data["logo"])
}

// 端到端：album 是字符串数组，逐项归一。
func TestResourceRefAlbumNormalized(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/projects", map[string]interface{}{
		"name":  "案例甲",
		"cover": "/uploads/t/1/cover.png",
		"album": []string{"/uploads/t/1/a1.png", "https://cdn.other.com/a2.jpg", ""},
	}, token)
	require.EqualValues(t, 0, body["code"], "新建案例失败：%v", body)

	var p model.ProjectCase
	require.NoError(t, db.First(&p, "name = ?", "案例甲").Error)
	require.Equal(t, "t/1/cover.png", p.Cover)
	// album 列里存的是 JSON 数组字符串
	require.JSONEq(t, `["t/1/a1.png","https://cdn.other.com/a2.jpg",""]`, p.Album,
		"逐项归一：本地路径收敛成裸键、外链原样保留、空串保持空")

	// 出参仍把裸键补回 URL，且外链与空串不变形。
	_, body = c.do("GET", "/api/admin/projects", nil, token)
	list := body["data"].([]interface{})
	require.Len(t, list, 1)
	album := list[0].(map[string]interface{})["album"].([]interface{})
	require.Equal(t, "/uploads/t/1/a1.png", album[0])
	require.Equal(t, "https://cdn.other.com/a2.jpg", album[1])
	require.Equal(t, "", album[2])
}

// 外链原样保留：video_url 的「外部 MP4 直链」档就是任意外域地址。
//
// 如果这条红了（外链被报错或被改写），说明归一写成了「一律要求推导出键」，
// 那会让「填外链」这个正常功能直接不可用。
func TestResourceRefKeepsForeignURL(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	const ext = "https://cdn.other.com/video/demo.mp4"
	_, body := c.do("PUT", "/api/admin/company-profile", map[string]interface{}{
		"introText": "", "videoSource": "url", "videoUrl": ext,
	}, token)
	require.EqualValues(t, 0, body["code"], "外链视频保存失败：%v", body)

	var cp model.CompanyProfile
	require.NoError(t, db.First(&cp, "company_id = ?", defaultCompanyID).Error)
	require.Equal(t, ext, cp.VideoURL, "外链必须原样保留，不能被改写或报错")

	// 反面：本平台的自托管 MP4 档，同一个字段就该收敛成裸键。
	_, body = c.do("PUT", "/api/admin/company-profile", map[string]interface{}{
		"introText": "", "videoSource": "upload", "videoUrl": "/uploads/t/1/v.mp4",
	}, token)
	require.EqualValues(t, 0, body["code"], "自托管视频保存失败：%v", body)
	require.NoError(t, db.First(&cp, "company_id = ?", defaultCompanyID).Error)
	require.Equal(t, "t/1/v.mp4", cp.VideoURL,
		"同一字段在不同档位语义不同：本平台对象要归一，外链要保留")
}

// 别家企业的键必须拒绝，且库不变。
//
// 这是「串号」的唯一防线：宽松归一放宽了「推不出键」和「平台对象」，
// 但绝不能放宽「这个键属于别人」。
func TestResourceRefRejectsForeignTenantKey(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	before := model.CompanyInfo{CompanyID: defaultCompanyID, FullName: "原名", Logo: "t/1/old.png"}
	require.NoError(t, db.Create(&before).Error)

	_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
		"fullName": "被改的名字", "logo": "/uploads/t/9/stolen.png",
	}, token)
	require.EqualValues(t, 1, body["code"], "别家企业的键必须拒绝：%v", body)

	var after model.CompanyInfo
	require.NoError(t, db.First(&after, "company_id = ?", defaultCompanyID).Error)
	require.Equal(t, "t/1/old.png", after.Logo, "被拒的请求不能改动库")
	require.Equal(t, "原名", after.FullName, "整条请求都该放弃，不是只跳过 logo")
}

// 不安全输入必须拒绝。
func TestResourceRefRejectsUnsafeValue(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
		"fullName": "测试", "logo": "javascript:alert(1)",
	}, token)
	require.EqualValues(t, 1, body["code"], "伪协议必须拒绝：%v", body)

	// 整条请求被放弃 = 连行都不该建。若这里查到了行，说明归一失败后代码继续往下写了。
	var ci model.CompanyInfo
	require.Error(t, db.First(&ci, "company_id = ?", defaultCompanyID).Error,
		"被拒的请求不该落下任何数据")
}

// env 回滚：STORAGE_REF_STRICT=0 退回第五十八轮的行为（收到什么写什么）。
//
// 这条是「一个 env 能整体回滚」这个承诺的可执行版本。回滚之后：
//   - 库里又变回 /uploads/... 形态；
//   - 出参侧 ResolveRef 照样能解析（它本来就兼容三种形态），所以页面不会坏。
//
// 也就是说回滚是安全的降级，不是断崖——这正是选「默认开 + 可关」的理由。
func TestResourceRefStrictOffRollsBack(t *testing.T) {
	t.Setenv(storageRefStrictEnv, "0")
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
		"fullName": "测试企业", "logo": "/uploads/t/1/logo.png",
	}, token)
	require.EqualValues(t, 0, body["code"])

	var ci model.CompanyInfo
	require.NoError(t, db.First(&ci, "company_id = ?", defaultCompanyID).Error)
	require.Equal(t, "/uploads/t/1/logo.png", ci.Logo,
		"STRICT=0 时必须退回旧行为；否则这个开关就没起作用")

	// 回滚模式下出参照样正确（ResolveRef 对已解析过的 URL 幂等）。
	_, body = c.do("GET", "/api/admin/company-info", nil, token)
	require.Equal(t, "/uploads/t/1/logo.png", body["data"].(map[string]interface{})["logo"])
}

// 上传 → 保存 → 读回：整条链路跑一遍，确认上传返回的 URL 能被归一接住。
//
// 平台超管登录时 companyID 为 0，上传落在 p/ 下（见 uploadCompanyID）；
// 把它存进租户字段是允许的（checkedScope 的宽松分支，对应访客匿名上传那条链路）。
func TestResourceRefUploadThenSave(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	token := loginAdmin(t, c)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "a.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("\x89PNG\r\n\x1a\n"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/admin/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var up map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &up))
	require.EqualValues(t, 0, up["code"])
	urls := up["data"].(map[string]interface{})["urls"].([]interface{})
	require.NotEmpty(t, urls)
	uploaded := urls[0].(string)

	_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
		"fullName": "测试企业", "logo": uploaded,
	}, token)
	require.EqualValues(t, 0, body["code"], "上传后保存失败：%v", body)

	var ci model.CompanyInfo
	require.NoError(t, db.First(&ci, "company_id = ?", defaultCompanyID).Error)
	require.True(t, strings.HasPrefix(ci.Logo, "p/") || strings.HasPrefix(ci.Logo, "t/"),
		"库里应是带归属前缀的裸键，实际 %q", ci.Logo)
	require.NotContains(t, ci.Logo, "/uploads/",
		"库里不该再出现 /uploads/ 前缀，实际 %q", ci.Logo)

	// 出参必须变回可访问地址，且与上传时返回的一致（键没被改写）。
	_, body = c.do("GET", "/api/admin/company-info", nil, token)
	require.Equal(t, uploaded, body["data"].(map[string]interface{})["logo"])
}
