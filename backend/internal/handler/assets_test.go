package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAssetLibrary 常用素材库：清单 / 公开预览图 / 采用（复制进上传存储）/
// 未登录拒绝 / 未知素材 / 路径穿越防护
func TestAssetLibrary(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)

	_, body := plat.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])

	// 1. 未登录拿不到清单（走 Cookie 鉴权，匿名请求 401）
	anon := newTestClient(r, t)
	_, body = anon.do("GET", "/api/admin/assets/library", nil, "")
	require.EqualValues(t, 401, body["code"])

	// 2. 清单（默认第一页）：六个分类 + 分页元信息
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library", nil)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.Len(t, data["categories"].([]interface{}), 6, "图标/封面/LOGO/头像/背景/海报 六个分类")
	items := data["items"].([]interface{})
	total := int(data["total"].(float64))
	require.GreaterOrEqual(t, total, 940, "素材应覆盖六大类九百四十项以上（含图标配色变体）")
	require.EqualValues(t, 1, data["page"])
	require.EqualValues(t, 15, data["size"], "缺省每页 15 条（5 列 × 3 排）")
	require.EqualValues(t, 15, len(items), "第一页按 size 截断")
	pages := int(data["pages"].(float64))
	require.GreaterOrEqual(t, pages, 37, "九百项素材应有多页")

	// 2.1 翻页：第 2 页换一批素材，且不与第 1 页重复
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?page=2", nil)
	require.EqualValues(t, 0, body["code"])
	page2 := body["data"].(map[string]interface{})
	require.EqualValues(t, 2, page2["page"])
	require.EqualValues(t, total, page2["total"], "total 为过滤后的总条数，不随页码变化")
	firstIDs := map[string]bool{}
	for _, it := range items {
		firstIDs[it.(map[string]interface{})["id"].(string)] = true
	}
	dup := 0
	for _, it := range page2["items"].([]interface{}) {
		if firstIDs[it.(map[string]interface{})["id"].(string)] {
			dup++
		}
	}
	require.Zero(t, dup, "第 2 页不应与第 1 页重复")

	// 2.2 size 生效（且上限 60 生效：请求 999 只回落 60）
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?size=5", nil)
	require.EqualValues(t, 5, len(body["data"].(map[string]interface{})["items"].([]interface{})))
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?size=999", nil)
	require.EqualValues(t, 60, body["data"].(map[string]interface{})["size"])

	// 2.3 非法页码不报错：page=0 → 1；超界页码被夹到末页
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?page=0", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["data"].(map[string]interface{})["page"])
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?page=9999", nil)
	require.EqualValues(t, pages, body["data"].(map[string]interface{})["page"], "超界页码夹到末页")
	require.NotEmpty(t, body["data"].(map[string]interface{})["items"], "末页仍应有内容")

	// 2.4 分类过滤：只返回背景（竖幅 750×1334，含深浅两色系），total 随之收窄
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?type=background", nil)
	require.EqualValues(t, 0, body["code"])
	bgData := body["data"].(map[string]interface{})
	bgItems := bgData["items"].([]interface{})
	bgTotal := int(bgData["total"].(float64))
	require.GreaterOrEqual(t, bgTotal, 40, "背景分类应覆盖四十款")
	require.Less(t, bgTotal, total, "分类过滤后总数应小于全量")
	for _, it := range bgItems {
		m := it.(map[string]interface{})
		require.Equal(t, "background", m["category"])
		require.EqualValues(t, 750, m["width"])
		require.EqualValues(t, 1334, m["height"])
	}

	// 2.5 关键字过滤：命中名称或标签，且翻页前重置到第 1 页。
	// 同一图标有 原版/暖阳/鎏金/素雅 四个配色变体，故命中 4 项；排序按生成
	// 顺序，原版（clean 无后缀）在最前。
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?keyword=电梯", nil)
	require.EqualValues(t, 0, body["code"])
	kwData := body["data"].(map[string]interface{})
	kwItems := kwData["items"].([]interface{})
	require.EqualValues(t, 4, kwData["total"], "同一图标应有四个配色变体")
	require.Equal(t, "icon-arrow-up-down", kwItems[0].(map[string]interface{})["id"])
	// 英文标签同样命中（大小写不敏感）
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?keyword=ELEVATOR", nil)
	require.EqualValues(t, 4, body["data"].(map[string]interface{})["total"])
	// 背景分类 + 关键字组合过滤：分类内再按关键字收窄
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?type=background&keyword=金", nil)
	require.EqualValues(t, 0, body["code"])
	combo := body["data"].(map[string]interface{})
	require.GreaterOrEqual(t, int(combo["total"].(float64)), 1, "背景内应有命中「金」的素材")
	require.LessOrEqual(t, int(combo["total"].(float64)), bgTotal, "组合过滤不应超出分类总数")
	for _, it := range combo["items"].([]interface{}) {
		require.Equal(t, "background", it.(map[string]interface{})["category"])
	}
	// 无命中：空列表且 total=0，不应报错
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?keyword=zzz-not-exist", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 0, body["data"].(map[string]interface{})["total"])
	require.Empty(t, body["data"].(map[string]interface{})["items"])

	// 2.6 视觉调性（style）：与分类正交的第二筛选维度
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library", nil)
	require.EqualValues(t, 0, body["code"])
	allData := body["data"].(map[string]interface{})
	require.Len(t, allData["styles"].([]interface{}), 5, "简约清晰/温暖自然/轻奢尊贵/柔美清新/光影流动 五档调性")
	counts := allData["styleCounts"].([]interface{})
	require.Len(t, counts, 5)
	// 调性计数之和 = 当前条件下的总条数（此处为全量）
	sum := 0
	for _, c := range counts {
		sum += int(c.(map[string]interface{})["count"].(float64))
	}
	require.Equal(t, total, sum, "调性计数之和应等于未加调性过滤时的 total")
	for _, it := range items {
		m := it.(map[string]interface{})
		require.NotEmpty(t, m["style"], "每项素材都应带调性: %s", m["id"])
	}

	// 2.6.1 调性过滤生效：返回项全部命中，total 随之收窄
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?style=luxury", nil)
	require.EqualValues(t, 0, body["code"])
	luxData := body["data"].(map[string]interface{})
	luxTotal := int(luxData["total"].(float64))
	require.GreaterOrEqual(t, luxTotal, 6, "轻奢尊贵档应有多款素材")
	require.Less(t, luxTotal, total)
	require.NotEmpty(t, luxData["items"].([]interface{}))
	for _, it := range luxData["items"].([]interface{}) {
		require.Equal(t, "luxury", it.(map[string]interface{})["style"])
	}
	// 关键：调性计数不受 style 参数影响，否则前端选中一档后其余全变 0
	require.Equal(t, sum, styleCountSum(t, luxData), "styleCounts 应基于 分类+关键字 统计，排除 style 本身")

	// 2.6.2 分类 + 调性组合过滤
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?type=background&style=nature", nil)
	require.EqualValues(t, 0, body["code"])
	combo2 := body["data"].(map[string]interface{})
	require.GreaterOrEqual(t, int(combo2["total"].(float64)), 5)
	for _, it := range combo2["items"].([]interface{}) {
		m := it.(map[string]interface{})
		require.Equal(t, "background", m["category"])
		require.Equal(t, "nature", m["style"])
	}

	// 2.6.3 图标分类：四档配色变体齐全（冷色原版 / 暖阳 / 鎏金 / 素雅浅底）。
	// 素雅浅底即客户原型图风格（白→浅蓝底 + 蓝色细线图形）；vivid 档刻意
	// 不做——高饱和底配白色线形图标对比度差，count=0 的档位由前端自动隐藏。
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?type=icon", nil)
	require.EqualValues(t, 0, body["code"])
	iconData := body["data"].(map[string]interface{})
	got := map[string]int{}
	for _, c := range iconData["styleCounts"].([]interface{}) {
		m := c.(map[string]interface{})
		got[m["key"].(string)] = int(m["count"].(float64))
	}
	require.Equal(t, 187, got["clean"], "图标冷色原版")
	require.Equal(t, 187, got["nature"], "图标暖阳变体")
	require.Equal(t, 187, got["luxury"], "图标鎏金变体")
	require.Equal(t, 187, got["soft"], "图标素雅浅底变体（客户原型图风格）")
	require.Zero(t, got["vivid"], "高饱和底白线刺眼，不做")
	// 同一图标应能按调性挑到四个配色变体
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?type=icon&keyword=居住住宅", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 4, int(body["data"].(map[string]interface{})["total"].(float64)),
		"同一图标应有 原版/暖阳/鎏金/素雅 四个变体")

	// 2.6.4 调性中文名并入搜索标签：搜「轻奢」能命中该档素材
	_, body = plat.doWithCookie("GET", "/api/admin/assets/library?keyword=轻奢", nil)
	require.EqualValues(t, 0, body["code"])
	require.GreaterOrEqual(t, int(body["data"].(map[string]interface{})["total"].(float64)), 6)

	// 3. 采用图标：URL 落进上传存储（/uploads/library/...），静态托管可取到真实 PNG
	_, body = plat.doWithCookie("POST", "/api/admin/assets/adopt", map[string]interface{}{"id": "icon-house"})
	require.EqualValues(t, 0, body["code"])
	adopted := body["data"].(map[string]interface{})["url"].(string)
	require.True(t, strings.HasSuffix(adopted, "/uploads/library/icon-house.png"), "采用 URL: %s", adopted)

	req := httptest.NewRequest("GET", "/uploads/library/icon-house.png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Greater(t, w.Body.Len(), 500, "采用的素材应真实落盘")

	// 4. 预览图：公开可取（无登录），带长缓存 + immutable
	req = httptest.NewRequest("GET", "/api/assets/file/icons/icon-house.png", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Contains(t, w.Header().Get("Cache-Control"), "immutable")
	require.Greater(t, w.Body.Len(), 500)

	// 5. 封面缩略图同样公开可取（JPEG）
	req = httptest.NewRequest("GET", "/api/assets/file/thumbs/covers/cover-skyline.jpg", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))

	// 5.1 首页背景：竖幅缩略图公开可取，采用后落地为 JPEG（与手动上传同构）
	req = httptest.NewRequest("GET", "/api/assets/file/thumbs/backgrounds/bg-deepspace.jpg", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))

	_, body = plat.doWithCookie("POST", "/api/admin/assets/adopt", map[string]interface{}{"id": "bg-deepspace"})
	require.EqualValues(t, 0, body["code"])
	bgURL := body["data"].(map[string]interface{})["url"].(string)
	require.True(t, strings.HasSuffix(bgURL, "/uploads/library/bg-deepspace.jpg"), "背景采用 URL: %s", bgURL)

	// 6. 异常分支：未知素材 / 空参数 / 路径穿越 / 不存在文件
	_, body = plat.doWithCookie("POST", "/api/admin/assets/adopt", map[string]interface{}{"id": "not-exist"})
	require.EqualValues(t, 1, body["code"])
	_, body = plat.doWithCookie("POST", "/api/admin/assets/adopt", map[string]interface{}{})
	require.EqualValues(t, 1, body["code"])
	for _, p := range []string{
		"/api/assets/file/",
		"/api/assets/file/thumbs/covers/nope.jpg",
		"/api/assets/file/..%2f..%2fgo.mod",
		"/api/assets/file/icons/../../manifest.json",
	} {
		req = httptest.NewRequest("GET", p, nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusOK, w.Code, p)
	}
}

// TestPublicPosterList 小程序「选海报背景」用的公开清单。
//
// 守三件事：1) 免登录可取（访客扫别人的海报进来也要能换背景）；
//  2. 只回 poster 分类（不能把 900+ 全量素材泄给小程序，也不该混进图标）；
//  3. 每项都给出能直取的 URL，且缩略图与原图公开可下——URL 拼错在真机上
//     表现为「背景选了但画布全黑」，本地开发（同源）永远复现不了。
func TestPublicPosterList(t *testing.T) {
	r := setupSQLiteEngine(t)
	anon := newTestClient(r, t)

	_, body := anon.do("GET", "/api/assets/posters", nil, "")
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	// 24 节气 + 20 场景
	require.GreaterOrEqual(t, len(items), 44, "海报应覆盖二十四节气与各商务场景")

	first := items[0].(map[string]interface{})
	require.NotEmpty(t, first["id"])
	require.NotEmpty(t, first["name"])
	require.NotEmpty(t, first["style"])
	file, _ := first["file"].(string)
	thumb, _ := first["thumb"].(string)
	require.Contains(t, file, "/api/assets/file/posters/")
	require.Contains(t, thumb, "/api/assets/file/thumbs/posters/")

	// 只回海报：不得混入其它分类的 id
	for _, it := range items {
		m := it.(map[string]interface{})
		require.Contains(t, m["id"].(string), "poster-", "海报清单里混进了非海报素材: %v", m["id"])
	}

	// 原图与缩略图都公开可取（无登录、无 Authorization）
	for _, p := range []string{file, thumb} {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, p)
		require.Equal(t, "image/jpeg", w.Header().Get("Content-Type"), p)
		require.Greater(t, w.Body.Len(), 1000, "海报图不应是空文件: %s", p)
	}
}

// styleCountSum 汇总响应里 styleCounts 的 count，用于校验调性计数之和
func styleCountSum(t *testing.T, data map[string]interface{}) int {
	counts, ok := data["styleCounts"].([]interface{})
	require.True(t, ok, "响应应带 styleCounts")
	sum := 0
	for _, c := range counts {
		sum += int(c.(map[string]interface{})["count"].(float64))
	}
	return sum
}
