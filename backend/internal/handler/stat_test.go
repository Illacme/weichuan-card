package handler

import (
	"bytes"
	"encoding/csv"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ===== 统计模块（埋点进水口 + 日期维度 + 导出 + 内容浏览）专项测试 =====
//
// 覆盖本轮改造的五个面：
//  1. 进水口：/api/stat/track 限流（按 IP）+ staffId 存在校验 + 事件白名单
//     + targetId 校验（project_view/service_view 强制存在且同租户）；
//  2. 查询：/api/admin/stats 与 /api/employee/me/stat 的 from/to 日期范围 + 按天趋势
//     + 内容浏览汇总与 TOP 榜；
//  3. 导出：/api/admin/stats/export 的 BOM / 转义 / 公式注入 / 租户隔离 / 意向列；
//  4. 明细：/api/admin/stats/detail 的倒序分页 / 四类筛选 / 对象名 / 租户隔离 / 坏参数拒收。
//
// setupSQLiteEngine 只回引擎，而统计测试需要直写**任意日期**的埋点行
// （track 接口只能写今天），故这里自建一套并回传 db。

// setupStatEngine 建测试引擎并回传 DB 句柄（含 1 号朗汇 + 2 号乙公司两个租户）。
func setupStatEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "weichuan-stat-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "廊坊市朗汇机电设备有限公司", ShortName: "朗汇机电", Status: "enabled"}).Error)
	require.NoError(t, db.Create(&model.Company{ID: 2, Name: "测试乙公司", ShortName: "乙公司", Status: "enabled"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
	}
	up := &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
	return buildEngine(t, db, cfg, up, nil), db
}

// mkStatEmployee 直建一个员工（绕过申请流，测试只关心统计口径）。
func mkStatEmployee(t *testing.T, db *gorm.DB, companyID int64, name, title, phone string) int64 {
	t.Helper()
	e := model.Employee{CompanyID: companyID, Name: name, Title: title, Phone: phone, Status: "enabled"}
	require.NoError(t, db.Create(&e).Error)
	return e.StaffID
}

// mkStatEvent 直写一条指定日期的埋点（track 接口只能写今天）。
// targetID 可选（内容事件用）：缺省 0，与「无关联对象」事件的进水口形态一致。
func mkStatEvent(t *testing.T, db *gorm.DB, staffID int64, eventType, date string, targetID ...int64) {
	t.Helper()
	var tid int64
	if len(targetID) > 0 {
		tid = targetID[0]
	}
	require.NoError(t, db.Create(&model.StatEvent{
		StaffID: staffID, EventType: eventType, StatDate: date, TargetID: tid,
	}).Error)
}

// mkStatEventSrc 同 mkStatEvent，但显式指定 source（流量来源 / 裂变测试用）。
// 直写时 DB 层对空串 source 不兜底（与 track 进水口的 normalizeSource 不同），
// 故测试须给明确值；想测「缺省归一」走 track 接口而非此 helper。
func mkStatEventSrc(t *testing.T, db *gorm.DB, staffID int64, eventType, date, source string, targetID ...int64) {
	t.Helper()
	var tid int64
	if len(targetID) > 0 {
		tid = targetID[0]
	}
	require.NoError(t, db.Create(&model.StatEvent{
		StaffID: staffID, EventType: eventType, StatDate: date, TargetID: tid, Source: source,
	}).Error)
}

// mkStatCase 直建一个案例（统计测试只关心 target 存在与租户归属）。
func mkStatCase(t *testing.T, db *gorm.DB, companyID int64, name string) int64 {
	t.Helper()
	pc := model.ProjectCase{CompanyID: companyID, Name: name, Enabled: true}
	require.NoError(t, db.Create(&pc).Error)
	return pc.ID
}

// mkStatService 直建一个服务（BizService 的名字列是 title）。
func mkStatService(t *testing.T, db *gorm.DB, companyID int64, title string) int64 {
	t.Helper()
	sv := model.BizService{CompanyID: companyID, Title: title}
	require.NoError(t, db.Create(&sv).Error)
	return sv.ID
}

// exportCSVRecords 打导出接口并解析成 CSV 记录（校验 BOM 后交给标准解析器）。
func exportCSVRecords(t *testing.T, r *gin.Engine, token, query string) [][]string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/admin/stats/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	raw := w.Body.Bytes()
	require.True(t, bytes.HasPrefix(raw, []byte("\xEF\xBB\xBF")), "CSV 必须带 BOM，否则 Excel 打开中文乱码")
	recs, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	require.NoError(t, err)
	return recs
}

// dayOffset 取相对今天的日期串（YYYY-MM-DD）。
func dayOffset(days int) string {
	return time.Now().AddDate(0, 0, days).Format("2006-01-02")
}

// ===== 1. 进水口 =====

// TestTrackRejectsUnknownStaff：不存在的 staffId 不得进库——
// 否则脚本就能给统计表灌幽灵访问量，而按员工汇总照单全收。
func TestTrackRejectsUnknownStaff(t *testing.T) {
	r, db := setupStatEngine(t)
	c := newTestClient(r, t)

	_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": 99999, "eventType": "visit",
	}, "")
	require.EqualValues(t, 1, body["code"])
	require.Equal(t, "员工不存在", body["msg"])

	var n int64
	require.NoError(t, db.Model(&model.StatEvent{}).Count(&n).Error)
	require.Zero(t, n, "被拒的上报不应留下任何行")
}

// TestTrackRejectsUnknownEventType：白名单之外的事件类型不得进库——
// 查询侧只认 visit/call/save，脏值会让总量与分类悄悄对不上。
func TestTrackRejectsUnknownEventType(t *testing.T) {
	r, db := setupStatEngine(t)
	staffID := mkStatEmployee(t, db, 1, "白名单", "测试", "13800000001")
	c := newTestClient(r, t)

	_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": staffID, "eventType": "hack",
	}, "")
	require.EqualValues(t, 1, body["code"])

	var n int64
	require.NoError(t, db.Model(&model.StatEvent{}).Count(&n).Error)
	require.Zero(t, n)
}

// TestTrackRateLimitedByIP：超限请求回 429 且不落库。
// 阈值压到 3（statTrackLimitFn 间接层）——注意必须在**建引擎之前**覆盖：
// 限流器在 New(db) 构造时就把 limitFn 收下了，事后再改变量捕到的是旧函数。
// 测完还原，避免影响同包其他测试。
func TestTrackRateLimitedByIP(t *testing.T) {
	old := statTrackLimitFn
	statTrackLimitFn = func() int { return 3 }
	t.Cleanup(func() { statTrackLimitFn = old })

	r, db := setupStatEngine(t)
	staffID := mkStatEmployee(t, db, 1, "限流", "测试", "13800000002")

	c := newTestClient(r, t)
	for i := 0; i < 3; i++ {
		_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": staffID, "eventType": "visit",
		}, "")
		require.EqualValues(t, 0, body["code"])
	}
	code, body := c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": staffID, "eventType": "visit",
	}, "")
	require.Equal(t, 200, code, "限流约定是 HTTP 200 + 信封码，不是 429 状态码")
	require.EqualValues(t, 429, body["code"])

	var n int64
	require.NoError(t, db.Model(&model.StatEvent{}).Count(&n).Error)
	require.EqualValues(t, 3, n, "被限流挡下的请求不应落库")
}

// TestTrackTargetValidation：内容事件的 targetId 校验。
// project_view/service_view 的 target 直接进后台 TOP 榜的对象名 JOIN——
// 放别家 targetId 进去，别家的案例名就会出现在本家榜单里（跨租户泄露的
// 静默形态），故强制「存在 + 同租户」；share 允许任意非负值（不进 TOP 榜）。
func TestTrackTargetValidation(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "意向", "测试", "13800000080")
	case1 := mkStatCase(t, db, 1, "朗汇案例A")
	case2 := mkStatCase(t, db, 2, "乙公司案例B") // 别家案例
	svc1 := mkStatService(t, db, 1, "朗汇服务A")

	c := newTestClient(r, t)

	// project_view 不带 targetId → 参数错误
	_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "project_view",
	}, "")
	require.EqualValues(t, 1, body["code"])

	// 不存在的 target → 对象不存在
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "project_view", "targetId": 99999,
	}, "")
	require.EqualValues(t, 1, body["code"])
	require.Equal(t, "对象不存在", body["msg"])

	// 别家案例 → 同样「对象不存在」（同一句回，不泄露别家存在性）
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "project_view", "targetId": case2,
	}, "")
	require.EqualValues(t, 1, body["code"])
	require.Equal(t, "对象不存在", body["msg"])

	// service_view 不带 target / 别家服务 → 同样拒收
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "service_view",
	}, "")
	require.EqualValues(t, 1, body["code"])
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "service_view", "targetId": 88888,
	}, "")
	require.EqualValues(t, 1, body["code"])

	// 已软删的案例 → 对象不存在（详情页根本走不到，进水口也不放行）
	require.NoError(t, db.Delete(&model.ProjectCase{}, case1).Error)
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "project_view", "targetId": case1,
	}, "")
	require.EqualValues(t, 1, body["code"])

	// 负 targetId 一律参数错误（share 也不收）
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": s1, "eventType": "share", "targetId": -5,
	}, "")
	require.EqualValues(t, 1, body["code"])

	// 合法组合全部放行（share 带一个别家 id 也放行——不进 TOP 榜，不强制存在）
	newCase := mkStatCase(t, db, 1, "朗汇案例C")
	for _, tc := range []struct {
		event  string
		target int64
	}{
		{"project_view", newCase}, {"service_view", svc1},
		{"video_play", 0}, {"share", 0}, {"share", case2}, {"poster_save", 0},
	} {
		payload := map[string]interface{}{"staffId": s1, "eventType": tc.event}
		if tc.target > 0 {
			payload["targetId"] = tc.target
		}
		_, body = c.do("POST", "/api/stat/track", payload, "")
		require.EqualValues(t, 0, body["code"], "%s/%d 应放行", tc.event, tc.target)
	}

	// 落库的 target_id 与上报一一对应（顺序即插入顺序）
	var rows []model.StatEvent
	require.NoError(t, db.Order("id asc").Find(&rows).Error)
	require.Len(t, rows, 6)
	wantTargets := []int64{newCase, svc1, 0, 0, case2, 0}
	for i, row := range rows {
		require.Equal(t, wantTargets[i], row.TargetID, "第 %d 条的 target_id", i+1)
	}
}

// TestTrackDoesNotStarveLogin：埋点（track: 桶，60/分）与登录（login: 桶，
// 10/分）共用 rate_limit_hits 表但必须分开计数。若桶键退化成裸 IP，同一行
// 计数被两个限流器互相挤占——访客埋点刷够 10 条/分就能把同 IP（办公网
// NAT 出口）的管理员登录打到 429，而排查时两个限流器看起来毫不相干。
// 2026-10-23 加意向子测试后全量包跑挂，根因即此；这条测试钉住修复。
func TestTrackDoesNotStarveLogin(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "限流隔离", "测试", "13800000099")
	c := newTestClient(r, t)

	// 打超过登录阈值（10）的埋点：埋点自身上限是 60/分，应全部放行
	for i := 0; i < 12; i++ {
		_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": s1, "eventType": "visit",
		}, "")
		require.EqualValues(t, 0, body["code"], "第 %d 条埋点应放行（埋点上限 60/分）", i+1)
	}

	// 同一 IP 上登录必须仍然可用：埋点的 12 次不能算进登录的 10/分桶
	admin := loginAdmin(t, c)
	require.NotEmpty(t, admin)
}

// ===== 2. 日期维度与趋势 =====

// TestAdminStatsDateRangeAndTrend：from/to 只放行范围内事件；趋势按天升序；
// 零数据员工仍补 0；坏参数回 code:1 而不是静默忽略。
func TestAdminStatsDateRangeAndTrend(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "张三", "工程师", "13800000010")
	s2 := mkStatEmployee(t, db, 1, "李四", "销售", "13800000011")
	mkStatEmployee(t, db, 1, "零数据", "实习生", "13800000012") // 全程无事件

	d0, d1, d2 := dayOffset(0), dayOffset(-1), dayOffset(-2)
	mkStatEvent(t, db, s1, "visit", d2)
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "visit", d0)
	mkStatEvent(t, db, s1, "call", d0)
	mkStatEvent(t, db, s2, "visit", d2)
	mkStatEvent(t, db, s2, "save", d0)
	// 意向事件也进趋势：d1 有 1 次案例浏览 + 1 次服务浏览，d0 有 1 次案例浏览
	mkStatEvent(t, db, s1, "project_view", d1, 101)
	mkStatEvent(t, db, s1, "service_view", d1, 201)
	mkStatEvent(t, db, s2, "project_view", d0, 101)

	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	// 不带范围 = 全部时间（向后兼容）：visit4 / call1 / save1
	_, body := c.do("GET", "/api/admin/stats", nil, admin)
	require.EqualValues(t, 0, body["code"])
	stats := body["data"].(map[string]interface{})
	require.EqualValues(t, 4, stats["summary"].(map[string]interface{})["visit"])
	require.EqualValues(t, 1, stats["summary"].(map[string]interface{})["call"])
	require.EqualValues(t, 1, stats["summary"].(map[string]interface{})["save"])
	// 趋势三天齐全且升序
	trend := stats["trend"].([]interface{})
	require.Len(t, trend, 3)
	for i, want := range []string{d2, d1, d0} {
		require.Equal(t, want, trend[i].(map[string]interface{})["date"])
	}
	// 趋势行带意向计数（前端图例点选后要有线可画）：d1 = 案例浏览 1 / 服务浏览 1
	require.EqualValues(t, 1, trend[1].(map[string]interface{})["projectView"])
	require.EqualValues(t, 1, trend[1].(map[string]interface{})["serviceView"])
	require.EqualValues(t, 0, trend[1].(map[string]interface{})["videoPlay"])
	// 零数据员工出现在列表里（计数 0）
	var zeroFound bool
	for _, it := range stats["byStaff"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["name"] == "零数据" {
			zeroFound = true
			require.EqualValues(t, 0, m["visit"])
		}
	}
	require.True(t, zeroFound, "零数据员工也应出现在统计列表")

	// 范围 [d1, d0]：排除 d2 → visit2（s1 两天 + s2 无）/ call1 / save1
	_, body = c.do("GET", "/api/admin/stats?from="+d1+"&to="+d0, nil, admin)
	require.EqualValues(t, 0, body["code"])
	stats = body["data"].(map[string]interface{})
	require.EqualValues(t, 2, stats["summary"].(map[string]interface{})["visit"])
	require.EqualValues(t, 1, stats["summary"].(map[string]interface{})["call"])
	require.EqualValues(t, 1, stats["summary"].(map[string]interface{})["save"])
	trend = stats["trend"].([]interface{})
	require.Len(t, trend, 2, "范围外的日期不应出现在趋势里")
	require.Equal(t, d1, trend[0].(map[string]interface{})["date"])
	require.Equal(t, d0, trend[1].(map[string]interface{})["date"])
	// 意向计数同样按范围收窄：d1 的案例浏览 1、d0 的案例浏览 1（d2 的无事件日不在内）
	require.EqualValues(t, 1, trend[0].(map[string]interface{})["projectView"])
	require.EqualValues(t, 1, trend[1].(map[string]interface{})["projectView"])
	require.EqualValues(t, 1, trend[0].(map[string]interface{})["serviceView"])
	require.EqualValues(t, 0, trend[1].(map[string]interface{})["serviceView"])

	// 坏参数：from > to / 不存在的日期 / 错格式 —— 都必须 code:1
	for _, q := range []string{"?from=" + d0 + "&to=" + d1, "?from=2026-02-31", "?to=2026-13-01", "?from=20260101"} {
		_, body = c.do("GET", "/api/admin/stats"+q, nil, admin)
		require.EqualValues(t, 1, body["code"], "坏日期参数应被拒绝: %s", q)
	}
}

// TestEmployeeStatDateRange：员工端统计同样支持 from/to，且看不到别人的数。
func TestEmployeeStatDateRange(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "王五", "工程师", "13800000020")
	s2 := mkStatEmployee(t, db, 1, "赵六", "销售", "13800000021")
	hash, err := auth.HashPassword("emp-pass-1")
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Employee{}).Where("staff_id = ?", s1).Update("password_hash", hash).Error)

	d0, d1 := dayOffset(0), dayOffset(-1)
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "call", d0)
	mkStatEvent(t, db, s2, "visit", d0) // 别人的事件不能算到王五头上

	c := newTestClient(r, t)
	_, body := c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13800000020", "password": "emp-pass-1",
	}, "")
	require.EqualValues(t, 0, body["code"])
	empToken := body["token"].(string)

	// 全部：visit2 / call1 / save0
	_, body = c.do("GET", "/api/employee/me/stat", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.EqualValues(t, 2, data["visit"])
	require.EqualValues(t, 1, data["call"])
	require.EqualValues(t, 0, data["save"])

	// 只要今天：visit0 / call1
	_, body = c.do("GET", "/api/employee/me/stat?from="+d0+"&to="+d0, nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data = body["data"].(map[string]interface{})
	require.EqualValues(t, 0, data["visit"])
	require.EqualValues(t, 1, data["call"])

	// 坏日期参数 → code:1
	_, body = c.do("GET", "/api/employee/me/stat?from=2026-02-31", nil, empToken)
	require.EqualValues(t, 1, body["code"])
}

// loginStatEmployee 给员工设密码并登录，返回 Bearer token（员工端统计测试专用）。
func loginStatEmployee(t *testing.T, r *gin.Engine, db *gorm.DB, staffID int64, phone, pass string) string {
	t.Helper()
	hash, err := auth.HashPassword(pass)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Employee{}).Where("staff_id = ?", staffID).
		Update("password_hash", hash).Error)
	c := newTestClient(r, t)
	_, body := c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": phone, "password": pass,
	}, "")
	require.EqualValues(t, 0, body["code"])
	return body["token"].(string)
}

// TestEmployeeStatSnapshot：员工端 /api/employee/me/stat 的四块内容——
// 八类事件汇总 + 按天趋势 + 案例/服务内容浏览 TOP3，
// 且三块共用同一个日期范围口径、且只统计本人（同租户同事的数字一个字都不能串）。
//
// 为什么「串到同事」要专门断言：stat_events 没带 company_id，员工端靠
// staff_id = ? 隔离。若这里误写成管理端那套 staff_id IN (本租户) 子查询，
// 页面看起来完全正常（数字都变大而已），员工会觉得「我哪有这么多访问」——
// 静默错误，没人会想到报 bug。
func TestEmployeeStatSnapshot(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "孙七", "工程师", "13800000030")
	s2 := mkStatEmployee(t, db, 1, "同事", "销售", "13800000031")
	caseA := mkStatCase(t, db, 1, "热门案例")
	caseB := mkStatCase(t, db, 1, "冷门案例")
	caseC := mkStatCase(t, db, 1, "第三案例")
	caseD := mkStatCase(t, db, 1, "第四案例") // 只碰过一次 → 被 TOP3 的 limit 挡掉
	svc1 := mkStatService(t, db, 1, "服务一")

	d0, d1 := dayOffset(0), dayOffset(-1)
	// 本人：d1 = 2 次访问 + 3 次热门案例浏览；d0 = 拨号/保存/视频/转发/海报 + 三种案例 + 服务
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "call", d0)
	mkStatEvent(t, db, s1, "save", d0)
	mkStatEvent(t, db, s1, "video_play", d0)
	mkStatEvent(t, db, s1, "share", d0)
	mkStatEvent(t, db, s1, "poster_save", d0)
	mkStatEvent(t, db, s1, "project_view", d1, caseA)
	mkStatEvent(t, db, s1, "project_view", d1, caseA)
	mkStatEvent(t, db, s1, "project_view", d1, caseA)
	mkStatEvent(t, db, s1, "project_view", d0, caseB)
	mkStatEvent(t, db, s1, "project_view", d0, caseB)
	mkStatEvent(t, db, s1, "project_view", d0, caseC)
	mkStatEvent(t, db, s1, "project_view", d0, caseD)
	mkStatEvent(t, db, s1, "service_view", d0, svc1)
	// 同租户同事的事件：会把 visit/call/projectView 全部顶高一位，串了立刻红
	mkStatEvent(t, db, s2, "visit", d0)
	mkStatEvent(t, db, s2, "call", d0)
	mkStatEvent(t, db, s2, "project_view", d0, caseA)

	empToken := loginStatEmployee(t, r, db, s1, "13800000030", "emp-pass-2")
	c := newTestClient(r, t)

	// 全部时间：八类计数逐项断言（漏一项就是某个指标恒 0 的静默缺数据）
	_, body := c.do("GET", "/api/employee/me/stat", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.EqualValues(t, 2, data["visit"])
	require.EqualValues(t, 1, data["call"])
	require.EqualValues(t, 1, data["save"])
	require.EqualValues(t, 7, data["projectView"])
	require.EqualValues(t, 1, data["serviceView"])
	require.EqualValues(t, 1, data["videoPlay"])
	require.EqualValues(t, 1, data["share"])
	require.EqualValues(t, 1, data["posterSave"])

	// 趋势：两天、升序、只含本人
	trend := data["trend"].([]interface{})
	require.Len(t, trend, 2)
	require.Equal(t, d1, trend[0].(map[string]interface{})["date"])
	require.EqualValues(t, 2, trend[0].(map[string]interface{})["visit"])
	require.EqualValues(t, 3, trend[0].(map[string]interface{})["projectView"])
	require.Equal(t, d0, trend[1].(map[string]interface{})["date"])
	require.EqualValues(t, 1, trend[1].(map[string]interface{})["call"])
	require.EqualValues(t, 1, trend[1].(map[string]interface{})["serviceView"])

	// 内容浏览 TOP3：热门(3) > 冷门(2) > 第三(1)，第四名不入榜
	top := data["topProjects"].([]interface{})
	require.Len(t, top, 3, "员工端 TOP 榜只给 3 条")
	require.EqualValues(t, caseA, top[0].(map[string]interface{})["targetId"])
	require.Equal(t, "热门案例", top[0].(map[string]interface{})["name"])
	require.EqualValues(t, 3, top[0].(map[string]interface{})["count"])
	require.Equal(t, "冷门案例", top[1].(map[string]interface{})["name"])
	require.EqualValues(t, 2, top[1].(map[string]interface{})["count"])
	require.EqualValues(t, 1, top[2].(map[string]interface{})["count"])

	svcTop := data["topServices"].([]interface{})
	require.Len(t, svcTop, 1)
	require.EqualValues(t, svc1, svcTop[0].(map[string]interface{})["targetId"])
	require.Equal(t, "服务一", svcTop[0].(map[string]interface{})["name"])

	// 只取今天：日期范围必须同样作用于趋势与 TOP 榜，否则「汇总按 30 天、
	// 趋势画全部」这类错位就会出现，而页面上完全看不出来
	_, body = c.do("GET", "/api/employee/me/stat?from="+d0+"&to="+d0, nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data = body["data"].(map[string]interface{})
	require.EqualValues(t, 0, data["visit"], "范围外的访问不该算进来")
	require.EqualValues(t, 1, data["call"])
	require.EqualValues(t, 4, data["projectView"], "范围外（昨天）的 3 次案例浏览不该算进来")
	trend = data["trend"].([]interface{})
	require.Len(t, trend, 1, "范围外的日期不应出现在趋势里")
	require.Equal(t, d0, trend[0].(map[string]interface{})["date"])
	top = data["topProjects"].([]interface{})
	require.Len(t, top, 3)
	require.Equal(t, "冷门案例", top[0].(map[string]interface{})["name"],
		"热门案例的事件全在范围外，不该再排第一")
}

// TestEmployeeStatSourceDist：员工端「访客从哪来」。
// 这是唯一能回答「海报有没有用」的数据——裂变两计数（share/poster_save）
// 主体可能是员工本人，证明不了海报带来了访问。
//
// 两条硬约束：
//  1. 各行 count 之和**必须等于**同一范围的 visit 数（对账口径）：
//     每一行 visit 都要落进某一个来源桶，漏了就等于「访问 20 次、来源加起来 18 次」，
//     而这类缺口在页面上完全看不出来。
//  2. 只统计 visit（call/save 等不带入口语义），范围与本人隔离同其余四块。
func TestEmployeeStatSourceDist(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "吴九", "工程师", "13800000040")
	s2 := mkStatEmployee(t, db, 1, "同事", "销售", "13800000041")

	d0 := dayOffset(0)
	// 本人：扫码 3 + 海报 2 + 转发 1 = 6 次访问；另有 2 次拨号（不该进分布）
	mkStatEventSrc(t, db, s1, "visit", d0, "scan")
	mkStatEventSrc(t, db, s1, "visit", d0, "scan")
	mkStatEventSrc(t, db, s1, "visit", d0, "scan")
	mkStatEventSrc(t, db, s1, "visit", d0, "poster")
	mkStatEventSrc(t, db, s1, "visit", d0, "poster")
	mkStatEventSrc(t, db, s1, "visit", d0, "share_card")
	mkStatEvent(t, db, s1, "call", d0)
	mkStatEvent(t, db, s1, "save", d0)
	// 同事的访问：一个字都不能出现在吴九的分布里
	mkStatEventSrc(t, db, s2, "visit", d0, "poster")

	empToken := loginStatEmployee(t, r, db, s1, "13800000040", "emp-pass-4")
	c := newTestClient(r, t)
	_, body := c.do("GET", "/api/employee/me/stat", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})

	// 汇总：visit 6（同事那次不算）
	require.EqualValues(t, 6, data["visit"])

	dist := data["sourceDist"].([]interface{})
	// 按 count DESC、source ASC：scan(3) > poster(2) > share_card(1)
	require.Len(t, dist, 3)
	require.Equal(t, "scan", dist[0].(map[string]interface{})["source"])
	require.EqualValues(t, 3, dist[0].(map[string]interface{})["count"])
	require.Equal(t, "poster", dist[1].(map[string]interface{})["source"])
	require.EqualValues(t, 2, dist[1].(map[string]interface{})["count"])
	require.Equal(t, "share_card", dist[2].(map[string]interface{})["source"])
	require.EqualValues(t, 1, dist[2].(map[string]interface{})["count"])

	// 对账：分布各行之和 == visit（拨号/保存不进分布）
	var sum int64
	for _, it := range dist {
		sum += int64(it.(map[string]interface{})["count"].(float64))
	}
	require.EqualValues(t, data["visit"], sum, "来源分布各行之和必须等于访问数")
}

// TestEmployeeStatRangeAppliesToAllBlocks：日期范围必须同时作用于五块数据。
// 任一块漏挂 stat_date，页面就会出现「汇总 30 天、趋势画全部」这种
// 看不出来、只能靠对账发现的错位。
func TestEmployeeStatRangeAppliesToAllBlocks(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "郑十", "工程师", "13800000042")
	caseA := mkStatCase(t, db, 1, "老案例")

	d0, d9 := dayOffset(0), dayOffset(-9) // 9 天前：落在 30 天内、7 天外
	mkStatEventSrc(t, db, s1, "visit", d9, "poster")
	mkStatEvent(t, db, s1, "project_view", d9, caseA)
	mkStatEventSrc(t, db, s1, "visit", d0, "scan")

	empToken := loginStatEmployee(t, r, db, s1, "13800000042", "emp-pass-5")
	c := newTestClient(r, t)

	// 30 天：两块都看得到（1 次海报访问 + 1 次案例浏览）
	_, body := c.do("GET", "/api/employee/me/stat?from="+dayOffset(-29)+"&to="+d0, nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.EqualValues(t, 2, data["visit"])
	require.EqualValues(t, 1, data["projectView"])
	require.Len(t, data["topProjects"].([]interface{}), 1)
	require.Len(t, data["trend"].([]interface{}), 2) // d9 与 d0 两天
	distAll := data["sourceDist"].([]interface{})
	require.Len(t, distAll, 2, "范围内有海报与扫码两个来源")

	// 7 天：9 天前那次掉出范围 → 五块同步收窄
	_, body = c.do("GET", "/api/employee/me/stat?from="+dayOffset(-6)+"&to="+d0, nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data = body["data"].(map[string]interface{})
	require.EqualValues(t, 1, data["visit"])
	require.EqualValues(t, 0, data["projectView"])
	require.Len(t, data["topProjects"].([]interface{}), 0)
	require.Len(t, data["trend"].([]interface{}), 1)
	distWeek := data["sourceDist"].([]interface{})
	require.Len(t, distWeek, 1)
	require.Equal(t, "scan", distWeek[0].(map[string]interface{})["source"],
		"海报那次访问在范围外，来源分布里不该再有海报")
}
// 名字回落「已删除」，计数不减——否则「榜单各行加起来对不上汇总」，
// 而这正是最难排查的一类对账事故（同管理端 statTopTargets 的口径约定）。
func TestEmployeeStatTopDeletedTarget(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "周八", "工程师", "13800000032")
	caseGone := mkStatCase(t, db, 1, "已下架案例")

	d0 := dayOffset(0)
	mkStatEvent(t, db, s1, "project_view", d0, caseGone)
	mkStatEvent(t, db, s1, "project_view", d0, caseGone)
	// 软删除（与业务下线同一条路径）
	require.NoError(t, db.Delete(&model.ProjectCase{}, caseGone).Error)

	empToken := loginStatEmployee(t, r, db, s1, "13800000032", "emp-pass-3")
	c := newTestClient(r, t)
	_, body := c.do("GET", "/api/employee/me/stat", nil, empToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.EqualValues(t, 2, data["projectView"], "对象被删，汇总计数不能跟着减")
	top := data["topProjects"].([]interface{})
	require.Len(t, top, 1)
	require.Equal(t, "已删除", top[0].(map[string]interface{})["name"])
	require.EqualValues(t, 2, top[0].(map[string]interface{})["count"])
}

// ===== 2.5 内容浏览汇总与 TOP 榜 =====

// TestAdminStatsIntentSummaryAndTop：意向五计数 + TOP 案例/服务榜 +
// 租户隔离 + 日期范围 + 已删对象回落「已删除」（计数不减，保对账）。
func TestAdminStatsIntentSummaryAndTop(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "张三", "工程师", "13800000090")
	s2 := mkStatEmployee(t, db, 2, "乙公司员工", "销售", "13800000091")
	caseHot := mkStatCase(t, db, 1, "热门案例")
	caseCold := mkStatCase(t, db, 1, "冷门案例")
	caseOther := mkStatCase(t, db, 2, "乙公司案例") // 别家案例：不进本家榜单
	svc1 := mkStatService(t, db, 1, "服务一")

	d := dayOffset(0)
	mkStatEvent(t, db, s1, "project_view", d, caseHot)
	mkStatEvent(t, db, s1, "project_view", d, caseHot)
	mkStatEvent(t, db, s1, "project_view", d, caseCold)
	mkStatEvent(t, db, s1, "service_view", d, svc1)
	mkStatEvent(t, db, s1, "video_play", d)
	mkStatEvent(t, db, s1, "share", d)
	mkStatEvent(t, db, s1, "poster_save", d)
	// 别家员工的事件：本家管理员的榜单与汇总都不该看到（staff 子查询隔离）
	mkStatEvent(t, db, s2, "project_view", d, caseOther)

	// 1 号租户的管理员（平台超管会合并两个租户，测不出隔离）
	hash, err := auth.HashPassword("comp1pass")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: "comp1", PasswordHash: hash,
	}).Error)
	c := newTestClient(r, t)
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp1", "password": "comp1pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	comp1 := c.cookies["access_token"]
	require.NotEmpty(t, comp1)

	_, body = c.do("GET", "/api/admin/stats", nil, comp1)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	sum := data["summary"].(map[string]interface{})
	require.EqualValues(t, 3, sum["projectView"])
	require.EqualValues(t, 1, sum["serviceView"])
	require.EqualValues(t, 1, sum["videoPlay"])
	require.EqualValues(t, 1, sum["share"])
	require.EqualValues(t, 1, sum["posterSave"])

	// TOP 案例：热门案例 2 条居首；别家案例不在榜（staff 子查询隔离）
	top := data["topProjects"].([]interface{})
	require.Len(t, top, 2)
	require.EqualValues(t, caseHot, top[0].(map[string]interface{})["targetId"])
	require.Equal(t, "热门案例", top[0].(map[string]interface{})["name"])
	require.EqualValues(t, 2, top[0].(map[string]interface{})["count"])
	require.Equal(t, "冷门案例", top[1].(map[string]interface{})["name"])
	require.EqualValues(t, 1, top[1].(map[string]interface{})["count"])

	// TOP 服务：名称取 title 列（不是 name）
	topSvc := data["topServices"].([]interface{})
	require.Len(t, topSvc, 1)
	require.EqualValues(t, svc1, topSvc[0].(map[string]interface{})["targetId"])
	require.Equal(t, "服务一", topSvc[0].(map[string]interface{})["name"])

	// 日期范围：排除今天 → 意向全 0、榜单空数组（不是 null）
	_, body = c.do("GET", "/api/admin/stats?from=2020-01-01&to=2020-01-02", nil, comp1)
	require.EqualValues(t, 0, body["code"])
	data = body["data"].(map[string]interface{})
	sum = data["summary"].(map[string]interface{})
	require.EqualValues(t, 0, sum["projectView"])
	require.EqualValues(t, 0, sum["serviceView"])
	require.Empty(t, data["topProjects"].([]interface{}))
	require.Empty(t, data["topServices"].([]interface{}))

	// 已删对象：TOP 榜名字回落「已删除」但计数不减——否则榜单数字加起来
	// 对不上汇总（「汇总说 3、榜单加起来 2」是最难排查的对账事故）
	require.NoError(t, db.Delete(&model.ProjectCase{}, caseCold).Error)
	_, body = c.do("GET", "/api/admin/stats", nil, comp1)
	require.EqualValues(t, 0, body["code"])
	top = body["data"].(map[string]interface{})["topProjects"].([]interface{})
	require.Len(t, top, 2, "对象删了事件还在，行数不能少")
	require.EqualValues(t, 3, body["data"].(map[string]interface{})["summary"].(map[string]interface{})["projectView"])
	byName := map[string]int64{}
	for _, it := range top {
		m := it.(map[string]interface{})
		byName[m["name"].(string)] = int64(m["count"].(float64))
	}
	require.EqualValues(t, 2, byName["热门案例"])
	require.EqualValues(t, 1, byName["已删除"])
}

// ===== 2.6 流量来源分布 + 裂变榜 =====

// TestNormalizeSource：normalizeSource 把来源钳进白名单，脏值不污染分布图。
// 反向验证：本函数被 track 进水口调用，摘掉归一逻辑（改成原样返回）会让
// TestTrackSourceNormalized 的「脏值归 other」断言失败。
func TestNormalizeSource(t *testing.T) {
	require.Equal(t, "unknown", normalizeSource(""))    // 空串兜底
	require.Equal(t, "unknown", normalizeSource("   ")) // 纯空白同空串
	require.Equal(t, "scan", normalizeSource("scan"))   // 白名单原样
	require.Equal(t, "share_card", normalizeSource("share_card"))
	require.Equal(t, "other", normalizeSource("DROP TABLE")) // 任意脏值归 other
	require.Equal(t, "other", normalizeSource("SCAN"))       // 大小写敏感，非白名单即 other
}

// TestTrackSourceNormalized：track 进水口把客户端 source 归一后再落库——
// 脏值变 other、空串变 unknown，且 source 仅作统计维度、不参与任何判断。
func TestTrackSourceNormalized(t *testing.T) {
	r, db := setupStatEngine(t)
	staffID := mkStatEmployee(t, db, 1, "来源", "测试", "13800000077")
	c := newTestClient(r, t)

	// 脏值 → 落库为 other
	_, body := c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": staffID, "eventType": "visit", "source": "hacked",
	}, "")
	require.EqualValues(t, 0, body["code"])
	// 空串 → 落库为 unknown
	_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
		"staffId": staffID, "eventType": "visit",
	}, "")
	require.EqualValues(t, 0, body["code"])

	var rows []model.StatEvent
	require.NoError(t, db.Where("staff_id = ?", staffID).Order("id asc").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "other", rows[0].Source)
	require.Equal(t, "unknown", rows[1].Source)
}

// TestAdminStatsSourceAndFission：来源分布 + 裂变榜的口径与租户隔离。
func TestAdminStatsSourceAndFission(t *testing.T) {
	r, db := setupStatEngine(t)
	// 本家（company 1）两名员工
	a := mkStatEmployee(t, db, 1, "裂变甲", "销售", "13800000101")
	b := mkStatEmployee(t, db, 1, "裂变乙", "销售", "13800000102")
	// 别家（company 2）一名员工：其事件不应出现在本家管理员视图
	other := mkStatEmployee(t, db, 2, "别家公司", "销售", "13800000103")
	d := dayOffset(0)

	// 甲：扫码 2 + 搜索 1 + 转发带来 3 + 海报 1 = 7 次访问
	mkStatEventSrc(t, db, a, "visit", d, "scan")
	mkStatEventSrc(t, db, a, "visit", d, "scan")
	mkStatEventSrc(t, db, a, "visit", d, "search")
	mkStatEventSrc(t, db, a, "visit", d, "share_card")
	mkStatEventSrc(t, db, a, "visit", d, "share_card")
	mkStatEventSrc(t, db, a, "visit", d, "share_card")
	mkStatEventSrc(t, db, a, "visit", d, "poster")
	// 乙：转发带来 2 次访问
	mkStatEventSrc(t, db, b, "visit", d, "share_card")
	mkStatEventSrc(t, db, b, "visit", d, "share_card")
	// 别家：转发带来 1 次——必须被租户隔离挡掉
	mkStatEventSrc(t, db, other, "visit", d, "share_card")

	c := newTestClient(r, t)
	// 1 号租户管理员（本测试要验证租户隔离，必须用品管而非平台超管）
	hash, err := auth.HashPassword("comp1pass")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 1, Username: "comp1", PasswordHash: hash,
	}).Error)
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp1", "password": "comp1pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	comp1 := c.cookies["access_token"]
	require.NotEmpty(t, comp1)

	_, body = c.do("GET", "/api/admin/stats", nil, comp1)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})

	// 来源分布：只含本家（甲 7 + 乙 2 = 9 次访问），别家的 share_card 不计入
	src := data["sourceDist"].([]interface{})
	bySrc := map[string]int64{}
	var srcTotal int64
	for _, it := range src {
		m := it.(map[string]interface{})
		bySrc[m["source"].(string)] = int64(m["count"].(float64))
		srcTotal += int64(m["count"].(float64))
	}
	require.EqualValues(t, 9, srcTotal, "别家的来源事件不应进入本家分布")
	require.EqualValues(t, 2, bySrc["scan"])
	require.EqualValues(t, 1, bySrc["search"])
	require.EqualValues(t, 5, bySrc["share_card"], "甲 3 + 乙 2，同租户聚合")
	require.EqualValues(t, 1, bySrc["poster"])

	// 裂变榜：甲 3 > 乙 2，按 count 降序；别家不出现
	fis := data["fissionTop"].([]interface{})
	require.Len(t, fis, 2, "别家员工不应出现在本家裂变榜")
	require.EqualValues(t, a, fis[0].(map[string]interface{})["staffId"])
	require.Equal(t, "裂变甲", fis[0].(map[string]interface{})["name"])
	require.EqualValues(t, 3, fis[0].(map[string]interface{})["count"])
	require.EqualValues(t, b, fis[1].(map[string]interface{})["staffId"])
	require.EqualValues(t, 2, fis[1].(map[string]interface{})["count"])

	// 日期范围：排除今天 → 来源与裂变都空数组（不是 null）
	_, body = c.do("GET", "/api/admin/stats?from=2020-01-01&to=2020-01-02", nil, comp1)
	require.EqualValues(t, 0, body["code"])
	data = body["data"].(map[string]interface{})
	require.Empty(t, data["sourceDist"].([]interface{}))
	require.Empty(t, data["fissionTop"].([]interface{}))
}

// ===== 3. CSV 导出 =====

// TestStatsExportCSV：BOM / 表头 / 公式注入 / 逗号引号转义 / 文件名 / 意向列。
func TestStatsExportCSV(t *testing.T) {
	r, db := setupStatEngine(t)
	// 姓名带公式注入前缀、职位带逗号与引号——Excel 打开时的两个经典坑
	s1 := mkStatEmployee(t, db, 1, "=cmd|'/c calc'!A1", "销售, 高级\"顾问\"", "13800000030")
	caseID := mkStatCase(t, db, 1, "导出案例")
	mkStatEvent(t, db, s1, "visit", dayOffset(0))
	mkStatEvent(t, db, s1, "project_view", dayOffset(0), caseID) // 意向列要有非零值

	c := newTestClient(r, t)
	admin := loginAdmin(t, c)
	recs := exportCSVRecords(t, r, admin, "")

	require.Len(t, recs, 2)
	require.Equal(t, []string{"编号", "姓名", "职位", "访问", "拨号", "保存",
		"案例浏览", "服务浏览", "视频播放", "转发", "海报保存"}, recs[0])
	// 公式注入被 ' 前缀挡下；逗号/引号经 csv 包裹后原样读回
	require.Equal(t, "'=cmd|'/c calc'!A1", recs[1][1])
	require.Equal(t, "销售, 高级\"顾问\"", recs[1][2])
	require.Equal(t, "1", recs[1][3]) // 访问
	require.Equal(t, "0", recs[1][4]) // 拨号
	require.Equal(t, "0", recs[1][5]) // 保存
	require.Equal(t, "1", recs[1][6]) // 案例浏览（意向列与 byStaff 同源）
	require.Equal(t, "0", recs[1][7]) // 服务浏览
	require.Equal(t, "0", recs[1][8]) // 视频播放
	require.Equal(t, "0", recs[1][9]) // 转发
	require.Equal(t, "0", recs[1][10])
}

// ===== 4. 明细细列表 =====

// TestStatsDetailTargetFilterAndNames：targetId 筛选 + 「对象」列——
// 案例取 name、服务取 title、share 的 target 案例优先、无对象事件显示占位符、
// 对象已删回落「已删除」（事件不消失，保对账）；坏 targetId 参数 code:1。
func TestStatsDetailTargetFilterAndNames(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "张三", "工程师", "13800000095")
	case1 := mkStatCase(t, db, 1, "案例甲")
	svc1 := mkStatService(t, db, 1, "服务甲")

	d := dayOffset(0)
	mkStatEvent(t, db, s1, "project_view", d, case1)
	mkStatEvent(t, db, s1, "project_view", d, case1)
	mkStatEvent(t, db, s1, "service_view", d, svc1)
	mkStatEvent(t, db, s1, "share", d, case1) // 转发案例：target 案例优先
	mkStatEvent(t, db, s1, "visit", d)        // 无关联对象

	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	// targetId 筛选：该案例的 project_view 恰好 2 条，对象名带出
	_, body := c.do("GET", "/api/admin/stats/detail?eventType=project_view&targetId="+
		strconv.FormatInt(case1, 10), nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 2, body["total"])
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		require.EqualValues(t, case1, m["targetId"])
		require.Equal(t, "案例甲", m["targetName"])
	}

	// 服务详情的对象名取 title 列
	_, body = c.do("GET", "/api/admin/stats/detail?eventType=service_view", nil, admin)
	require.EqualValues(t, 1, body["total"])
	require.Equal(t, "服务甲", body["data"].([]interface{})[0].(map[string]interface{})["targetName"])

	// share 的 targetId 是案例 id → 显示案例名（纯展示，不聚计入 TOP 榜）
	_, body = c.do("GET", "/api/admin/stats/detail?eventType=share", nil, admin)
	require.Equal(t, "案例甲", body["data"].([]interface{})[0].(map[string]interface{})["targetName"])

	// visit 无关联对象：targetId=0 + 占位符
	_, body = c.do("GET", "/api/admin/stats/detail?eventType=visit", nil, admin)
	m := body["data"].([]interface{})[0].(map[string]interface{})
	require.EqualValues(t, 0, m["targetId"])
	require.Equal(t, "—", m["targetName"])

	// 对象被删：事件仍在（明细是汇总的下钻，行数不能少），名字回落「已删除」
	require.NoError(t, db.Delete(&model.ProjectCase{}, case1).Error)
	_, body = c.do("GET", "/api/admin/stats/detail?eventType=project_view", nil, admin)
	require.EqualValues(t, 2, body["total"], "对象删了事件也不能少")
	require.Equal(t, "已删除", body["data"].([]interface{})[0].(map[string]interface{})["targetName"])

	// 坏 targetId 参数 → code:1（静默忽略会让「筛了却没生效」）
	for _, q := range []string{"?targetId=abc", "?targetId=0", "?targetId=-1"} {
		_, body = c.do("GET", "/api/admin/stats/detail"+q, nil, admin)
		require.EqualValues(t, 1, body["code"], q)
	}
}

// TestStatsDetailPaginationAndFilters：倒序 + 分页信封 + 三类筛选各自生效且可组合；
// stat_date 必须是 YYYY-MM-DD（CAST 归一，MySQL DATE 列直接扫会带 T00:00:00Z）；
// createdAt 形如 "YYYY-MM-DD HH:mm:ss"（服务端格式化，前端不再换算时区）。
func TestStatsDetailPaginationAndFilters(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "张三", "工程师", "13800000050")
	s2 := mkStatEmployee(t, db, 1, "李四", "销售", "13800000051")

	d0, d1 := dayOffset(0), dayOffset(-1)
	// 按 id 递增顺序建：最后一条是 s2 的 save，倒序后应排最前
	mkStatEvent(t, db, s1, "visit", d1)
	mkStatEvent(t, db, s1, "visit", d0)
	mkStatEvent(t, db, s1, "call", d0)
	mkStatEvent(t, db, s2, "save", d0)

	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	// 缺省参数：全部 4 条，倒序（最新在前），分页元信息齐全
	_, body := c.do("GET", "/api/admin/stats/detail", nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 4, body["total"])
	require.EqualValues(t, 1, body["page"])
	require.EqualValues(t, 20, body["size"], "size 缺省 20（全站约定）")
	list := body["data"].([]interface{})
	require.Len(t, list, 4)
	first := list[0].(map[string]interface{})
	require.EqualValues(t, 4, first["id"], "倒序：最新事件在最前")
	require.Equal(t, "save", first["eventType"])
	require.Equal(t, "李四", first["name"], "姓名由 JOIN 带出")
	require.Equal(t, d0, first["statDate"], "stat_date 必须是 YYYY-MM-DD，不能带 T00:00:00Z")
	require.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`, first["createdAt"])

	// 分页：size=2&page=2 → 第 2 页恰好是 id 2、3（与第 1 页不重叠）
	_, body = c.do("GET", "/api/admin/stats/detail?size=2&page=2", nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 4, body["total"])
	require.EqualValues(t, 2, body["page"])
	require.EqualValues(t, 2, body["size"])
	list = body["data"].([]interface{})
	require.Len(t, list, 2)
	require.EqualValues(t, 2, list[0].(map[string]interface{})["id"])

	// staffId 筛选：只有 s1 的 3 条
	_, body = c.do("GET", "/api/admin/stats/detail?staffId="+strconv.FormatInt(s1, 10), nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 3, body["total"])
	for _, it := range body["data"].([]interface{}) {
		require.EqualValues(t, s1, it.(map[string]interface{})["staffId"])
	}

	// eventType 筛选：visit 2 条 / call 1 条 / save 1 条
	for et, want := range map[string]int{"visit": 2, "call": 1, "save": 1} {
		_, body = c.do("GET", "/api/admin/stats/detail?eventType="+et, nil, admin)
		require.EqualValues(t, 0, body["code"])
		require.EqualValues(t, want, body["total"], "eventType=%s", et)
	}

	// 日期范围：只要 d0 → 3 条（d1 的 visit 被排除）
	_, body = c.do("GET", "/api/admin/stats/detail?from="+d0+"&to="+d0, nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 3, body["total"])

	// 组合筛选：s1 + visit + d0 → 1 条
	_, body = c.do("GET", "/api/admin/stats/detail?staffId="+strconv.FormatInt(s1, 10)+
		"&eventType=visit&from="+d0+"&to="+d0, nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["total"])
}

// TestStatsDetailTenantScope：企业管理员只能下钻自家事件；
// 拿别家员工 id 来筛是空列表——不是报错、更不是漏出数据。
func TestStatsDetailTenantScope(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "朗汇员工", "工程师", "13800000060")
	s2 := mkStatEmployee(t, db, 2, "乙公司员工", "销售", "13800000061")
	d := dayOffset(0)
	mkStatEvent(t, db, s1, "visit", d)
	mkStatEvent(t, db, s2, "visit", d)
	mkStatEvent(t, db, s2, "call", d)

	hash, err := auth.HashPassword("comp2pass")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 2, Username: "comp2", PasswordHash: hash,
	}).Error)

	plat := newTestClient(r, t)
	platAdmin := loginAdmin(t, plat)

	comp2 := newTestClient(r, t)
	_, body := comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	comp2Token := comp2.cookies["access_token"]
	require.NotEmpty(t, comp2Token)

	// 平台超管：两家 3 条全见
	_, body = plat.do("GET", "/api/admin/stats/detail", nil, platAdmin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 3, body["total"])

	// 乙公司管理员：只有自家 2 条
	_, body = comp2.do("GET", "/api/admin/stats/detail", nil, comp2Token)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 2, body["total"])
	for _, it := range body["data"].([]interface{}) {
		require.Equal(t, "乙公司员工", it.(map[string]interface{})["name"])
	}

	// 拿朗汇员工的 id 来筛：空列表（隔离由员工子查询保证，不靠 JOIN）
	_, body = comp2.do("GET", "/api/admin/stats/detail?staffId="+strconv.FormatInt(s1, 10), nil, comp2Token)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 0, body["total"])
	require.Len(t, body["data"].([]interface{}), 0)
}

// TestStatsDetailDeletedStaffScope：员工从库里删掉后，其历史事件在明细与汇总中
// 一致地消失。这条测试钉住的是「下钻可对账」约束——明细是汇总的展开，
// 若明细多出行数，运营拿着汇总数逐条找就对不上（当前产品无删员工功能，
// 直改库或未来加删除时都不能破坏这个约束）。
func TestStatsDetailDeletedStaffScope(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "在职", "工程师", "13800000070")
	s2 := mkStatEmployee(t, db, 1, "离职同事", "工程师", "13800000071")
	d := dayOffset(0)
	mkStatEvent(t, db, s1, "visit", d)
	mkStatEvent(t, db, s2, "visit", d)
	mkStatEvent(t, db, s2, "call", d)
	require.NoError(t, db.Delete(&model.Employee{}, s2).Error)

	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	// 明细：只剩在职员工的 1 条
	_, body := c.do("GET", "/api/admin/stats/detail", nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["total"])
	require.Equal(t, "在职", body["data"].([]interface{})[0].(map[string]interface{})["name"])

	// 汇总：访问同样只剩 1（visit 原为 2，离职同事那份不计入）
	_, body = c.do("GET", "/api/admin/stats", nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 1, body["data"].(map[string]interface{})["summary"].(map[string]interface{})["visit"])
}

// TestStatsDetailBadParams：空结果信封完整（data 是空数组不是 null）；
// 坏参数一律 code:1——静默忽略会让「筛了却没生效」比不筛更难排查。
func TestStatsDetailBadParams(t *testing.T) {
	r, _ := setupStatEngine(t)
	c := newTestClient(r, t)
	admin := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/stats/detail", nil, admin)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 0, body["total"])
	require.NotNil(t, body["data"], "空结果也要给数组，前端省一层判空")
	require.Len(t, body["data"].([]interface{}), 0)

	for _, q := range []string{
		"?from=2026-02-31",          // 不存在的日期
		"?to=2026-13-01",            // 不存在的月份
		"?from=20260101",            // 错格式
		"?eventType=hack",           // 白名单外的事件类型
		"?staffId=abc",              // 非数字
		"?staffId=0",                // 非正数
		"?staffId=-1",               // 负数
		"?targetId=abc",             // 非数字
		"?targetId=0",               // 非正数（TOP 榜下钻传的永远是正 id）
		"?targetId=-1",              // 负数
	} {
		_, body = c.do("GET", "/api/admin/stats/detail"+q, nil, admin)
		require.EqualValues(t, 1, body["code"], "坏参数应被拒绝: %s", q)
	}
}

// TestStatsExportTenantScope：企业管理员只能导出自家员工——
// 统计接口按租户隔离，导出是同一份数据的另一个出口，不能成为越权口子。
func TestStatsExportTenantScope(t *testing.T) {
	r, db := setupStatEngine(t)
	s1 := mkStatEmployee(t, db, 1, "朗汇员工", "工程师", "13800000040")
	s2 := mkStatEmployee(t, db, 2, "乙公司员工", "销售", "13800000041")
	d := dayOffset(0)
	mkStatEvent(t, db, s1, "visit", d)
	mkStatEvent(t, db, s2, "visit", d)
	mkStatEvent(t, db, s2, "call", d)

	// 2 号租户的企业管理员直建入库（本测试关注的是隔离，不是开户流程；
	// 开户流程本身由 TestMultiTenantIsolation 覆盖）
	hash, err := auth.HashPassword("comp2pass")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Admin{
		CompanyID: 2, Username: "comp2", PasswordHash: hash,
	}).Error)

	plat := newTestClient(r, t)
	platAdmin := loginAdmin(t, plat)

	comp2 := newTestClient(r, t)
	_, body := comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	comp2Token := comp2.cookies["access_token"]
	require.NotEmpty(t, comp2Token)

	// 平台超管：两家员工都在
	recs := exportCSVRecords(t, r, platAdmin, "")
	require.Len(t, recs, 3, "平台超管应看到两个租户的员工")
	names := recs[1][1] + "," + recs[2][1]
	require.Contains(t, names, "朗汇员工")
	require.Contains(t, names, "乙公司员工")

	// 乙公司管理员：只有自家
	recs = exportCSVRecords(t, r, comp2Token, "")
	require.Len(t, recs, 2, "企业管理员只应看到自家员工")
	require.Equal(t, "乙公司员工", recs[1][1])

	// 接口层同样隔离：乙公司管理员查 /api/admin/stats 只汇总自家
	_, body = comp2.do("GET", "/api/admin/stats", nil, comp2Token)
	require.EqualValues(t, 0, body["code"])
	byStaff := body["data"].(map[string]interface{})["byStaff"].([]interface{})
	require.Len(t, byStaff, 1)
	require.Equal(t, "乙公司员工", byStaff[0].(map[string]interface{})["name"])
	require.EqualValues(t, 1, byStaff[0].(map[string]interface{})["visit"])
	require.EqualValues(t, 1, byStaff[0].(map[string]interface{})["call"])
	require.EqualValues(t, 1, body["data"].(map[string]interface{})["summary"].(map[string]interface{})["visit"])
}
