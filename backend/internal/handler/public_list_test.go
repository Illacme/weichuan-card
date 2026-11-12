package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// C 端内容型列表的容量收口（2026-10-19）：
//
// 这三个接口（/api/projects、/api/services、/api/achievements 的 featured）
// 端上是「一次全量展示」、没有分页 UI，所以不做分页，但要防住两件事：
//  1. payload 无上限膨胀——案例 detail / 服务 content 都是富文本，单条可达几十 KB，
//     列表页只画封面 + 标题 + 摘要，详情由 /xxx/:id 单独取，不该跟着列表走；
//  2. 条数无天花板——内容随运营时间增长，必须有一个夹断点。
//
// 本文件把这两条钉死，同时守住两个「别改坏了」的底线：详情接口仍返回正文、
// 列表的软删除与分类名回填在列裁剪后仍然生效。

// 名片接口缓存头测试用的两位员工：一位在职、一位已停用。
// StaffID 显式给定，避免测试依赖插入顺序（AI 自增在不同驱动下表现不同）。
const (
	cardStaffEnabled  int64 = 9001
	cardStaffDisabled int64 = 9002
)

// setupPublicEngine 建临时库 + 造量级数据（案例、服务均超过各自上限），返回引擎与库。
func setupPublicEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpDB, err := os.CreateTemp("", "weichuan-public-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Company{}, &model.AchievementCategory{}, &model.ProjectCase{}, &model.BizService{},
		// 名片聚合（/api/card）与首页布局（/api/home-layout）也在本轮缓存头的
		// 覆盖范围内，这几张表缺了就只能验到「错误分支」，成功分支无从断言。
		&model.Employee{}, &model.CompanyInfo{}, &model.CompanyProfile{}, &model.HomeLayout{},
	))
	// 1 号租户（朗汇）：公开接口缺省落点
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "朗汇机电", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.AchievementCategory{ID: 1, CompanyID: 1, Name: "机房工程"}).Error)
	require.NoError(t, db.Create(&model.CompanyInfo{CompanyID: 1, ShortName: "朗汇"}).Error)
	require.NoError(t, db.Create(&model.CompanyProfile{CompanyID: 1}).Error)
	require.NoError(t, db.Create(&model.HomeLayout{CompanyID: 1}).Error)
	// 一张在职名片、一张停用名片：停用走 code=2，是「错误分支不设缓存头」的关键用例。
	require.NoError(t, db.Create(&model.Employee{
		StaffID: cardStaffEnabled, CompanyID: 1, Name: "在职员工", Status: "enabled",
	}).Error)
	require.NoError(t, db.Create(&model.Employee{
		StaffID: cardStaffDisabled, CompanyID: 1, Name: "停用员工", Status: "disabled",
	}).Error)

	// 富文本正文：故意造大，列表若带着它，响应体会明显不一样
	longDetail := strings.Repeat("<p>工程正文内容</p>", 200)
	total := repository.MaxProjectsList + 50 // 超过上限，验证会被夹断
	for i := 1; i <= total; i++ {
		require.NoError(t, db.Create(&model.ProjectCase{
			CompanyID:      1,
			CategoryID:     1,
			Name:           fmt.Sprintf("案例%d", i),
			Summary:        fmt.Sprintf("摘要%d", i),
			Detail:         longDetail,
			Cover:          fmt.Sprintf("/uploads/cover%d.png", i),
			IsHomeFeatured: i <= repository.MaxFeaturedList+5, // 超过精选上限
			Enabled:        true,
			Sort:           i,
		}).Error)
	}
	for i := 1; i <= repository.MaxServicesList+10; i++ {
		require.NoError(t, db.Create(&model.BizService{
			CompanyID: 1,
			Title:     fmt.Sprintf("服务%d", i),
			Icon:      fmt.Sprintf("/uploads/icon%d.png", i),
			Content:   longDetail,
			Sort:      i,
		}).Error)
	}
	return New(db), db
}

func TestPublicListCapacity(t *testing.T) {
	r, db := setupPublicEngine(t)
	c := newTestClient(r, t)

	// 1) 案例列表：条数夹到上限，且不带富文本正文
	_, body := c.do("GET", "/api/projects", nil, "")
	require.Equal(t, float64(0), body["code"])
	list := body["data"].([]interface{})
	require.Len(t, list, repository.MaxProjectsList, "案例列表应被夹到上限")
	for _, it := range list {
		m := it.(map[string]interface{})
		require.Empty(t, m["detail"], "列表不应携带富文本 detail（详情由 /api/projects/:id 单独取）")
		require.NotEmpty(t, m["name"])
		require.NotEmpty(t, m["cover"], "列表需要的封面字段不能丢")
		// 列裁剪后分类名回填仍要生效（依赖 select 里保留 category_id）
		require.Equal(t, "机房工程", m["categoryName"], "裁剪列不能破坏分类名回填")
	}

	// 2) ?limit= 生效且越界夹到上限
	_, body = c.do("GET", "/api/projects?limit=5", nil, "")
	require.Len(t, body["data"].([]interface{}), 5)
	_, body = c.do("GET", "/api/projects?limit=99999", nil, "")
	require.Len(t, body["data"].([]interface{}), repository.MaxProjectsList, "超限应夹到上限而非全量")
	_, body = c.do("GET", "/api/projects?limit=0", nil, "")
	require.Len(t, body["data"].([]interface{}), repository.MaxProjectsList, "非法值应回落默认量级")

	// 3) 服务列表：同样夹上限 + 不带富文本正文
	_, body = c.do("GET", "/api/services", nil, "")
	svcs := body["data"].([]interface{})
	require.Len(t, svcs, repository.MaxServicesList)
	for _, it := range svcs {
		m := it.(map[string]interface{})
		require.Empty(t, m["content"], "列表不应携带服务正文 content")
		require.NotEmpty(t, m["title"])
		require.NotEmpty(t, m["icon"])
	}

	// 4) 首页精选：夹到首页画得下的量级，且不带正文
	_, body = c.do("GET", "/api/achievements", nil, "")
	feat := body["featured"].([]interface{})
	require.Len(t, feat, repository.MaxFeaturedList)
	for _, it := range feat {
		require.Empty(t, it.(map[string]interface{})["detail"], "首页精选不应携带富文本")
	}
	require.NotEmpty(t, body["categories"].([]interface{}), "分类列表不受精选上限影响")

	// 5) 详情接口仍返回正文：裁剪只作用于列表，不能误伤详情
	var first model.ProjectCase
	require.NoError(t, db.Order("sort asc").First(&first).Error)
	_, body = c.do("GET", fmt.Sprintf("/api/projects/%d", first.ID), nil, "")
	require.Equal(t, float64(0), body["code"])
	require.NotEmpty(t, body["data"].(map[string]interface{})["detail"], "详情必须返回富文本正文")

	// 6) 软删除仍然生效：列裁剪不能把 deleted_at IS NULL 条件弄丢
	require.NoError(t, db.Delete(&first).Error)
	_, body = c.do("GET", "/api/projects?limit=5", nil, "")
	for _, it := range body["data"].([]interface{}) {
		require.NotEqual(t, float64(first.ID), it.(map[string]interface{})["id"], "已删除案例不应出现在列表里")
	}
}

// 公开只读接口的缓存头：成功分支发 max-age + s-maxage，错误分支一个头都不发。
//
// 为什么两个 TTL 都要断（2026-11-12 补）：s-maxage 才是共享缓存（CDN / 反代）读的
// 那个字段，但各家实现取哪个字段并不统一——只发 s-maxage，遇到只读 max-age 的
// 边缘节点就静默失效（头看着对、cf-cache-status 一直 DYNAMIC，且零报错）；
// 只发 max-age 又拿不到「只给共享缓存、不给端上」的语义。所以两个都得在、值一致。
//
// 为什么错误分支必须一个头都不带：一旦 4xx/5xx 也挂上 Cache-Control，边缘会把
// 故障一起缓存 60 秒——「员工不存在」被缓存一分钟事小，接口抖动期间把 500
// 缓存住才是灾难。这条最容易在改造时破：只要有人把 cdnCache 提到 error 分支之前。
func TestPublicListCacheHeader(t *testing.T) {
	r, _ := setupPublicEngine(t)

	// 请求一次，同时拿到信封里的 code 与响应头：断言「分支对不对」和「头有没有」
	// 必须来自同一个响应，否则「验的其实是另一条路径」这类错误会漏过去。
	hit := func(path string) (float64, string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), path+" 的响应不是 JSON")
		code, _ := body["code"].(float64)
		return code, w.Header().Get("Cache-Control")
	}

	// cached：成功分支必须同时带 max-age 与 s-maxage。
	cached := func(path string) {
		t.Helper()
		code, cc := hit(path)
		require.Equal(t, float64(0), code, path+" 应走成功分支（否则下面的断言验的是错误路径）")
		require.Contains(t, cc, "public", path+" 必须是可被共享缓存的")
		require.Contains(t, cc, "max-age=60",
			path+" 缺 max-age：只认 max-age 的边缘节点会退化成完全不缓存")
		require.Contains(t, cc, "s-maxage=60",
			path+" 缺 s-maxage：CDN 拿不到共享缓存语义，同样退化成不缓存")
	}

	// uncached：错误分支连头都不能有。
	uncached := func(path, why string) {
		t.Helper()
		code, cc := hit(path)
		require.NotEqual(t, float64(0), code, why+"（应确认真的走到了错误分支）")
		require.Empty(t, cc, why)
	}

	// 名片页首屏就是这 4 个接口并发（card / home-layout / achievements / services），
	// 加 /api/projects 共 5 条，全部必须可被边缘缓存。
	for _, p := range []string{
		"/api/projects",
		"/api/services",
		"/api/achievements",
		"/api/card?staffId=" + strconv.FormatInt(cardStaffEnabled, 10),
		"/api/home-layout",
	} {
		cached(p)
	}

	// 企业不存在 → publicCompanyID 的失败分支
	uncached("/api/projects?companyId=9999", "企业不存在的错误响应不应被缓存")
	// 员工不存在 → code=1
	uncached("/api/card?staffId=99999", "员工不存在的错误响应不应被缓存")
	// 员工已停用 → code=2：这一支最容易被写成「拼完 JSON 才设头」，
	// 必须在 c.JSON 之前就 return，头才落不到响应上。
	uncached("/api/card?staffId="+strconv.FormatInt(cardStaffDisabled, 10),
		"名片已停用的错误响应不应被缓存")

	// /api/team 是路由决策（裸启动落名片墙还是某个员工的名片），刻意不缓存：
	// 管理员改完设置立刻扫码验证，缓存 60 秒会让他以为没生效，越查越乱。
	// 注意它是**成功响应却不带头**，所以不能套 uncached（那支还断言 code != 0）。
	code, cc := hit("/api/team")
	require.Equal(t, float64(0), code, "/api/team 应正常返回")
	require.Empty(t, cc, "/api/team 是路由决策，刻意不缓存")
}
