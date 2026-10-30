// Package handler 接口集成测试（白盒 HTTP 层）。
//
// 设计说明：
//  1. 通过 handler.New(db) + 各 Register* 函数，用 httptest 把完整 Gin 路由跑起来，
//     覆盖「登录鉴权 / 审核流 / 员工管理 / 企业内容 / 上传存储」等全部 /api/* 接口。
//  2. 默认测试不依赖外部 MySQL：注入一个临时文件的 SQLite 库（纯 Go 驱动 glebarez/sqlite，
//     免 cgo），并用 GORM AutoMigrate 按模型建表。SQLite 把模型里的 JSON 列按 TEXT 存储，
//     与 MySQL JSON 列在读写字符串语义上等价，足以验证 handler 业务逻辑。
//  3. 生产契约一致性：设置 TEST_MYSQL_DSN 后，TestAPIFlowMySQL 会连真实 MySQL、应用
//     migrations/*.sql，再跑与 SQLite 完全相同的断言（runAPIFlow），用于 CI 中的 parity 校验。
//  4. 鉴权中间件约定：未登录/无权限返回的是 HTTP 200 + {code:401|403}，而不是 HTTP 状态
//     码，因此本测试统一断言 body["code"]，不依赖状态码。
//  5. 存储增强的「成功分支」用 fake Presigner/Signer/CDNRefresher 覆盖（见 TestStorageEnhanceSuccess）；
//     本地驱动不支持预签名/签名的「回退分支 code:1」在 runAPIFlow 中覆盖。
//
// 运行：
//
//	cd backend && go test ./internal/handler/ -v
//	TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/weichuan_test?charset=utf8mb4&parseTime=true' go test ./internal/handler/ -run TestAPIFlowMySQL -v
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/migrate"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// testClient 对引擎发起一次 HTTP 调用并解析 JSON 响应体。
// 带 cookie jar：累积服务端 Set-Cookie（浏览器式），并记录最近一次响应的 Set-Cookie 供断言。
type testClient struct {
	r              *gin.Engine
	t              *testing.T
	cookies        map[string]string // cookie jar
	lastSetCookies []*http.Cookie    // 最近一次响应的 Set-Cookie
}

func newTestClient(r *gin.Engine, t *testing.T) *testClient {
	return &testClient{r: r, t: t, cookies: map[string]string{}}
}

// do  Bearer 头鉴权调用（token 为空则不带头，也不带 cookie）。
func (c *testClient) do(method, path string, body interface{}, token string) (int, map[string]interface{}) {
	return c.doRaw(method, path, body, token, "")
}

// doWithCookie 携带 jar 中的 cookie（模拟浏览器自动携带），不发送 Bearer——用于验证 Cookie 鉴权路径。
func (c *testClient) doWithCookie(method, path string, body interface{}) (int, map[string]interface{}) {
	var pairs []string
	for k, v := range c.cookies {
		pairs = append(pairs, k+"="+v)
	}
	return c.doRaw(method, path, body, "", strings.Join(pairs, "; "))
}

func (c *testClient) doRaw(method, path string, body interface{}, token, cookieHeader string) (int, map[string]interface{}) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(c.t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	w := httptest.NewRecorder()
	c.r.ServeHTTP(w, req)

	// 累积 cookie：MaxAge<0 表示删除（浏览器行为）
	c.lastSetCookies = w.Result().Cookies()
	for _, ck := range c.lastSetCookies {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}

	var m map[string]interface{}
	if w.Body.Len() > 0 {
		require.NoError(c.t, json.Unmarshal(w.Body.Bytes(), &m))
	}
	return w.Code, m
}

// buildEngine 用给定的 db / 配置 / 存储实现装配与生产 main.go 等价的路由。
// 把「建库」与「装配」分离，便于 SQLite（AutoMigrate）与 MySQL（migrations SQL）复用同一套断言。
func buildEngine(t *testing.T, db *gorm.DB, cfg config.Config, up storage.Uploader, refresher storage.CDNRefresher) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	t.Setenv("JWT_KEY", cfg.JWTKey)

	SeedAdmin(db, cfg) // 首启 seed 初始超管

	// 系统设置：seed 缺省值 + 载入内存缓存（测试依赖公开/后台设置接口）
	require.NoError(t, setting.Init(db))

	r := New(db)
	RegisterAuth(r, db, cfg)
	RegisterApply(r, db, cfg)
	RegisterStatsExport(r, db) // 统计 CSV 导出（与 /api/admin/stats 同口径）
	RegisterStatsDetail(r, db) // 埋点明细列表（聚合的下钻）
	RegisterContent(r, db, cfg)
	RegisterMPEntry(r, db) // 小程序「默认进入」：名片墙 / 指定员工名片
	RegisterCompanies(r, db)
	RegisterCompanyLicense(r, db) // 按企业查看/设置授权（B4：试用转正 / 延期 / 改席位）
	RegisterStorageQuota(r, db)   // 按租户存储用量与配额（B3）
	RegisterCompanyWXApp(r, db)
	RegisterMPRelease(r, db, cfg) // 小程序版本发布（代租户提审 / 发布 / 撤回）
	RegisterAdmins(r, db)         // 平台超管账号管理（列表 / 新增 / 改用户名 / 重置密码 / 删除）
	RegisterAuditLogs(r, db)      // 操作审计日志（只读查询）
	// 海报个性化设置：GET/PUT /api/employee/poster-prefs + 公开快照 GET /api/poster-prefs。
	// 原先这里没注册（只在两个 cmd/main.go 里注册），于是任何带 DB 的海报接口测试
	// 都会拿到 gin 的 404 纯文本——JSON 解析报 "invalid character 'p' after top-level
	// value"（"404 page not found" 里的 p），看不出是路由缺失。
	RegisterPoster(r, db)

	// 静态托管需要目录存在
	require.NoError(t, os.MkdirAll(cfg.UploadDir, 0755))
	r.Static("/uploads", cfg.UploadDir)
	RegisterUpload(r, db, cfg, up, refresher)
	RegisterStorage(r, db, up, refresher, cfg)
	RegisterAssets(r, db, up, refresher) // 常用素材库（内嵌 Lucide 图标 + 通用封面/LOGO）
	RegisterWechat(r, db, cfg, up, refresher)
	RegisterMediaCheck(r, db, cfg, up, refresher) // 图片内容安全：消息推送回调 / 记录列表 / 一键下架
	RegisterSettings(r, db, cfg)                  // 系统设置：GET/PUT /api/admin/settings + GET /api/public/settings

	return r
}

// setupSQLiteEngine 创建临时 SQLite 库 + AutoMigrate + 本地存储，返回引擎。
func setupSQLiteEngine(t *testing.T, mutate ...func(*config.Config)) *gin.Engine {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "weichuan-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	// 1 号租户（朗汇）：公开接口缺省落点；存量行为不随多租户改造而变
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "廊坊市朗汇机电设备有限公司", ShortName: "朗汇机电", Status: "enabled", LoginCode: "langhui"}).Error)

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
	// 个别用例要改配置（如打开腾讯视频档）：变参传入，其余用例不受影响
	for _, m := range mutate {
		m(&cfg)
	}
	up := &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
	var refresher storage.CDNRefresher
	return buildEngine(t, db, cfg, up, refresher)
}

// loginAdmin 用 seed 的超管登录并返回 access 令牌值。
// PC 后台改为 Cookie 鉴权后，token 不再出现在响应体，而从 Set-Cookie 的 access_token 取。
func loginAdmin(t *testing.T, c *testClient) string {
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])
	tok := c.cookies["access_token"]
	require.NotEmpty(t, tok)
	return tok
}

// runAPIFlow 是 SQLite 与 MySQL parity 共用的核心断言集合。
func runAPIFlow(t *testing.T, c *testClient, adminToken string) {
	r := c.r

	// ===== 1. 公开接口（无需登录） =====
	t.Run("公开接口", func(t *testing.T) {
		code, body := c.do("GET", "/api/achievements", nil, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 0, body["code"])

		// 尚不存在的员工 → code:1
		_, body = c.do("GET", "/api/card?staffId=1", nil, "")
		require.EqualValues(t, 1, body["code"])

		// 提交开通申请 → code:0
		_, body = c.do("POST", "/api/apply/open", map[string]interface{}{
			"name": "张三", "phone": "13800000001", "email": "z@a.com", "title": "工程师",
		}, "")
		require.EqualValues(t, 0, body["code"])

		// 埋点：参数缺失 → code:1；员工不存在 → code:1（进水口校验，见 stat.go）
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{"staffId": 1}, "")
		require.EqualValues(t, 1, body["code"])
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": 99999, "eventType": "visit",
		}, "")
		require.EqualValues(t, 1, body["code"], "不存在的员工不应计入统计")
		// 非法事件类型 → code:1（白名单外的值不进库）
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": 99999, "eventType": "hack",
		}, "")
		require.EqualValues(t, 1, body["code"])
	})

	// ===== 2. 超管登录与鉴权 =====
	t.Run("超管登录与鉴权", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
			"username": "admin", "password": "admin123456",
		}, "")
		require.EqualValues(t, 0, body["code"])
		require.Empty(t, body["token"])                // token 不再入响应体
		require.NotEmpty(t, c.cookies["access_token"]) // 改为下发 httpOnly Cookie

		// 错误密码 → code:1
		_, body = c.do("POST", "/api/admin/login", map[string]interface{}{
			"username": "admin", "password": "wrong",
		}, "")
		require.EqualValues(t, 1, body["code"])

		// 缺 token → code:401（HTTP 仍为 200）
		code, body := c.do("GET", "/api/admin/employees", nil, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 401, body["code"])

		// 非法 token → code:401
		_, body = c.do("GET", "/api/admin/employees", nil, "not.a.valid.token")
		require.EqualValues(t, 401, body["code"])
	})

	// ===== 3. 开通申请审核流（通过→自动建员工） =====
	var openApplyID int64
	var newStaffID int64
	t.Run("开通申请审核流", func(t *testing.T) {
		_, body := c.do("GET", "/api/admin/apply/open", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		list := body["data"].([]interface{})
		require.GreaterOrEqual(t, len(list), 1)
		openApplyID = int64(list[0].(map[string]interface{})["id"].(float64))

		_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", openApplyID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		require.NotEmpty(t, body["initialPassword"])
		require.NotEmpty(t, body["staffId"])
		newStaffID = int64(body["staffId"].(float64))

		// 员工已存在，此时上报才应被接受（后面统计汇总断言依赖这条 visit）
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": newStaffID, "eventType": "visit",
		}, "")
		require.EqualValues(t, 0, body["code"])

		// 重复审核已处理的申请 → code:1
		_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", openApplyID), nil, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 驳回接口对已是 approved 的记录同样返回 code:1
		_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/reject", openApplyID),
			map[string]interface{}{"reason": "x"}, adminToken)
		require.EqualValues(t, 1, body["code"])
	})

	// ===== 4. 员工管理 + 员工端自服务 =====
	t.Run("员工管理与员工端", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
			"name": "李四", "phone": "13900000002", "email": "l@a.com", "title": "销售",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		empID := int64(body["staffId"].(float64))
		require.NotEmpty(t, body["initialPassword"])

		// 创建即落库职位（曾疑「创建员工不保存职位」）：列表回读断言
		_, body = c.do("GET", "/api/admin/employees", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		var created bool
		for _, it := range body["data"].([]interface{}) {
			m := it.(map[string]interface{})
			if int64(m["staffId"].(float64)) != empID {
				continue
			}
			created = true
			require.Equal(t, "销售", m["title"])
			require.Equal(t, "l@a.com", m["email"])
		}
		require.True(t, created, "新建员工应出现在列表中")

		_, body = c.do("GET", "/api/admin/employees", nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 超管编辑员工资料（即时生效，不经员工端审核流）——含座机、手机 2 字段
		_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", empID), map[string]interface{}{
			"name": "李四四", "title": "高级销售", "phone": "13900000002",
			"phone2": "13900000022", "landline": "021-12345678", "email": "l4@a.com", "avatar": "http://x/a.png",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])

		_, body = c.do("GET", "/api/admin/employees", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		var found bool
		for _, it := range body["data"].([]interface{}) {
			m := it.(map[string]interface{})
			if int64(m["staffId"].(float64)) != empID {
				continue
			}
			found = true
			require.Equal(t, "李四四", m["name"])
			require.Equal(t, "高级销售", m["title"])
			require.Equal(t, "13900000022", m["phone2"])
			require.Equal(t, "021-12345678", m["landline"])
			require.Equal(t, "l4@a.com", m["email"])
		}
		require.True(t, found, "编辑后的员工应仍在列表中")

		// 手机 2 也要能被搜索命中（服务端关键字范围含 phone2）
		_, body = c.do("GET", "/api/admin/employees?keyword=13900000022", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		var hit2 bool
		for _, it := range body["data"].([]interface{}) {
			m := it.(map[string]interface{})
			if int64(m["staffId"].(float64)) == empID {
				hit2 = true
			}
		}
		require.True(t, hit2, "按手机 2 搜索应能查到该员工")

		// 编辑不存在的员工 / 姓名为空 → code:1
		_, body = c.do("PUT", "/api/admin/employees/999999", map[string]interface{}{
			"name": "x", "phone": "1",
		}, adminToken)
		require.EqualValues(t, 1, body["code"])
		_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", empID), map[string]interface{}{
			"name": "", "phone": "13900000002",
		}, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 重置密码 → 返回新初始密码（覆盖创建时的初始密码，登录须用此值）
		_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/reset-password", empID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		require.NotEmpty(t, body["initialPassword"])
		resetPwd := body["initialPassword"].(string)

		// 禁用 / 启用
		_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/disable", empID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("POST", fmt.Sprintf("/api/admin/employees/%d/enable", empID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 全局名片统计：汇总 + 按员工 + 按天趋势（审核通过后上报过一次 visit）
		_, body = c.do("GET", "/api/admin/stats", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		stats := body["data"].(map[string]interface{})
		summary := stats["summary"].(map[string]interface{})
		require.GreaterOrEqual(t, int(summary["visit"].(float64)), 1)
		byStaff := stats["byStaff"].([]interface{})
		require.GreaterOrEqual(t, len(byStaff), 1)
		// 按员工列表须含 staffId/name 与三个计数，且总数与汇总一致
		var sumVisit int64
		for _, it := range byStaff {
			m := it.(map[string]interface{})
			require.Contains(t, m, "staffId")
			require.Contains(t, m, "name")
			sumVisit += int64(m["visit"].(float64))
		}
		require.EqualValues(t, summary["visit"], sumVisit)
		// 趋势按天升序，且至少包含今天（上面那条 visit 落在今天）
		trend := stats["trend"].([]interface{})
		require.GreaterOrEqual(t, len(trend), 1)
		last := trend[len(trend)-1].(map[string]interface{})
		require.Equal(t, time.Now().Format("2006-01-02"), last["date"])

		// 日期范围：只要今天 → 至少含上面那条 visit（MySQL 的 stat_date 是 DATE 列，
		// 字符串范围条件 + 扫进 string 字段的行为必须与 SQLite 一致，故进 parity 流）
		today := time.Now().Format("2006-01-02")
		_, body = c.do("GET", "/api/admin/stats?from="+today+"&to="+today, nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		require.GreaterOrEqual(t, int(body["data"].(map[string]interface{})["summary"].(map[string]interface{})["visit"].(float64)), 1)

		// 坏日期参数 → code:1（而不是静默按全部时间查）
		_, body = c.do("GET", "/api/admin/stats?from=2026-02-31", nil, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 埋点明细（汇总的下钻）：MySQL 上必须与 SQLite 同形——
		// ① stat_date 经 CAST 归一回 YYYY-MM-DD（DATE 列直扫会变成 RFC3339，
		//    2026-10-23 parity 首跑抓过，故这里守门）；
		// ② JOIN 带姓名 + 分页信封 {data,total,page,size} 形状一致
		_, body = c.do("GET", "/api/admin/stats/detail?from="+today+"&to="+today, nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		require.GreaterOrEqual(t, int(body["total"].(float64)), 1)
		detail := body["data"].([]interface{})
		first := detail[0].(map[string]interface{})
		require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, first["statDate"].(string))
		require.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`, first["createdAt"].(string))
		require.NotEmpty(t, first["name"], "JOIN 应带出员工姓名")

		// 按员工筛选：只放行该员工的事件
		_, body = c.do("GET", "/api/admin/stats/detail?staffId="+
			strconv.FormatInt(newStaffID, 10)+"&from="+today+"&to="+today, nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		for _, it := range body["data"].([]interface{}) {
			require.EqualValues(t, newStaffID, it.(map[string]interface{})["staffId"])
		}

		// 坏筛选参数 → code:1（白名单外事件类型 / 非数字 staffId）
		_, body = c.do("GET", "/api/admin/stats/detail?eventType=hack", nil, adminToken)
		require.EqualValues(t, 1, body["code"])
		_, body = c.do("GET", "/api/admin/stats/detail?staffId=abc", nil, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 员工登录（用重置后的密码）
		_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
			"phone": "13900000002", "password": resetPwd,
		}, "")
		require.EqualValues(t, 0, body["code"])
		empToken := body["token"].(string)

		// 员工本人名片 + 统计
		_, body = c.do("GET", "/api/employee/me", nil, empToken)
		require.EqualValues(t, 0, body["code"])
		// 根级下发所属企业名称（小程序「个人信息」页展示「所属企业」用）：员工建在 1 号朗汇租户
		require.Equal(t, "廊坊市朗汇机电设备有限公司", body["companyName"])
		require.Equal(t, "朗汇机电", body["companyShortName"])
		_, body = c.do("GET", "/api/employee/me/stat", nil, empToken)
		require.EqualValues(t, 0, body["code"])

		// 权限越界：员工 token 调超管接口 → code:403（读与写都拦）
		_, body = c.do("GET", "/api/admin/employees", nil, empToken)
		require.EqualValues(t, 403, body["code"])
		_, body = c.do("PUT", fmt.Sprintf("/api/admin/employees/%d", empID), map[string]interface{}{
			"name": "越权改", "phone": "000",
		}, empToken)
		require.EqualValues(t, 403, body["code"])

		// 员工提交名片修改申请（含座机与手机 2，审核通过后应落到员工资料上）
		_, body = c.do("PUT", "/api/employee/me", map[string]interface{}{
			"title": "高级销售", "landline": "021-87654321", "phone2": "13900000222",
		}, empToken)
		require.EqualValues(t, 0, body["code"])

		// 有待审申请时 /api/employee/me 根级返回 pendingModify=true（编辑页「审核中」标签用）
		_, body = c.do("GET", "/api/employee/me", nil, empToken)
		require.EqualValues(t, 0, body["code"])
		require.Equal(t, true, body["pendingModify"])

		// 超管审核名片修改
		_, body = c.do("GET", "/api/admin/apply/modify", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		mods := body["data"].([]interface{})
		require.GreaterOrEqual(t, len(mods), 1)
		modID := int64(mods[0].(map[string]interface{})["id"].(float64))
		_, body = c.do("POST", fmt.Sprintf("/api/admin/apply/modify/%d/approve", modID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 审核处理后 pendingModify 回落 false
		_, body = c.do("GET", "/api/employee/me", nil, empToken)
		require.EqualValues(t, 0, body["code"])
		require.Equal(t, false, body["pendingModify"])

		// 审核通过后快照生效：title / landline / phone2 都写到员工资料
		_, body = c.do("GET", "/api/admin/employees", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		for _, it := range body["data"].([]interface{}) {
			m := it.(map[string]interface{})
			if int64(m["staffId"].(float64)) != empID {
				continue
			}
			require.Equal(t, "高级销售", m["title"])
			require.Equal(t, "021-87654321", m["landline"])
			require.Equal(t, "13900000222", m["phone2"])
		}
	})

	// ===== 5. 分类与项目强校验（防止孤立数据 / 锁定保护） =====
	t.Run("分类与项目强校验", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/categories", map[string]interface{}{
			"name": "测试分类", "icon": "ic", "sort": 10, "isLocked": false,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		catID := int64(body["data"].(map[string]interface{})["id"].(float64))

		// 在该分类下建项目
		_, body = c.do("POST", "/api/admin/projects", map[string]interface{}{
			"categoryId": catID, "name": "案例A", "summary": "s", "detail": "d",
			"album": []string{"u1"}, "isHomeFeatured": false, "enabled": true, "sort": 1,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 公开列表接口回填 categoryName（跳转页文档 §4：免前端二次查询）
		_, pubBody := c.do("GET", fmt.Sprintf("/api/projects?categoryId=%d", catID), nil, "")
		require.EqualValues(t, 0, pubBody["code"])
		pubList := pubBody["data"].([]interface{})
		require.NotEmpty(t, pubList)
		pubItem := pubList[0].(map[string]interface{})
		require.Equal(t, "测试分类", pubItem["categoryName"])
		pubID := int64(pubItem["id"].(float64))

		// 公开详情接口含 categoryName + updatedAt（跳转页文档 §2.2）
		_, pubBody = c.do("GET", fmt.Sprintf("/api/projects/%d", pubID), nil, "")
		require.EqualValues(t, 0, pubBody["code"])
		pubDetail := pubBody["data"].(map[string]interface{})
		require.Equal(t, "测试分类", pubDetail["categoryName"])
		require.Contains(t, pubDetail, "updatedAt")
		require.NotEmpty(t, pubDetail["updatedAt"])

		// 分类下有项目 → 删除失败 code:1
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/categories/%d", catID), nil, adminToken)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "项目")

		// 建一个锁定分类 → 删除失败 code:1（锁定保护）
		_, body = c.do("POST", "/api/admin/categories", map[string]interface{}{
			"name": "锁定分类", "isLocked": true,
		}, adminToken)
		lockID := int64(body["data"].(map[string]interface{})["id"].(float64))
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/categories/%d", lockID), nil, adminToken)
		require.EqualValues(t, 1, body["code"])
		require.Contains(t, body["msg"], "锁定")

		// 清理：先删项目，再删分类（此时无项目且非锁定）→ 成功
		_, body = c.do("GET", fmt.Sprintf("/api/admin/projects?categoryId=%d", catID), nil, adminToken)
		proj := body["data"].([]interface{})[0].(map[string]interface{})
		projID := int64(proj["id"].(float64))
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/projects/%d", projID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/categories/%d", catID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
	})

	// ===== 6. 业务服务 CRUD =====
	t.Run("业务服务 CRUD", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/services", map[string]interface{}{
			"title": "机电维保", "content": "<p>test</p>", "sort": 1,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		sid := int64(body["data"].(map[string]interface{})["id"].(float64))

		_, body = c.do("PUT", fmt.Sprintf("/api/admin/services/%d", sid), map[string]interface{}{
			"title": "机电维保2", "content": "<p>x</p>", "sort": 2,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 公开详情含 updatedAt（跳转页文档 §3.2）
		_, body = c.do("GET", fmt.Sprintf("/api/services/%d", sid), nil, "")
		require.EqualValues(t, 0, body["code"])
		require.Contains(t, body["data"].(map[string]interface{}), "updatedAt")

		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/services/%d", sid), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
	})

	// ===== 6.5 内容浏览埋点与 TOP 榜 =====
	// parity 重点：target_id 落库值、按 target 聚合 + LEFT JOIN 对象名
	// （COALESCE 回落「已删除」）、明细 targetId 筛选——这几种 SQL 形状
	// 在 MySQL 8.0 与 SQLite 上必须同结果，故进共用流（migrations/0021）。
	t.Run("内容浏览埋点与TOP榜", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/categories", map[string]interface{}{
			"name": "意向分类", "icon": "ic", "sort": 20, "isLocked": false,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		catID := int64(body["data"].(map[string]interface{})["id"].(float64))

		_, body = c.do("POST", "/api/admin/projects", map[string]interface{}{
			"categoryId": catID, "name": "意向案例", "summary": "s", "detail": "d",
			"album": []string{}, "isHomeFeatured": false, "enabled": true, "sort": 1,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		projID := int64(body["data"].(map[string]interface{})["id"].(float64))

		_, body = c.do("POST", "/api/admin/services", map[string]interface{}{
			"title": "意向服务", "content": "<p>t</p>", "sort": 5,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		svcID := int64(body["data"].(map[string]interface{})["id"].(float64))

		// 进水口：project_view 必须带合法 targetId（该值直接进 TOP 榜的 JOIN）
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": newStaffID, "eventType": "project_view",
		}, "")
		require.EqualValues(t, 1, body["code"])
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": newStaffID, "eventType": "project_view", "targetId": 999999,
		}, "")
		require.EqualValues(t, 1, body["code"])

		// 五类意向事件（project_view 两条同 target，share 带案例 id）
		for _, ev := range []map[string]interface{}{
			{"staffId": newStaffID, "eventType": "project_view", "targetId": projID},
			{"staffId": newStaffID, "eventType": "project_view", "targetId": projID},
			{"staffId": newStaffID, "eventType": "service_view", "targetId": svcID},
			{"staffId": newStaffID, "eventType": "video_play"},
			{"staffId": newStaffID, "eventType": "share", "targetId": projID},
			{"staffId": newStaffID, "eventType": "poster_save"},
		} {
			_, body = c.do("POST", "/api/stat/track", ev, "")
			require.EqualValues(t, 0, body["code"])
		}

		// 汇总：意向五计数（别名 snake_case，Scan 才收得到——见 statRowsByStaff 注释）
		_, body = c.do("GET", "/api/admin/stats", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		sum := body["data"].(map[string]interface{})["summary"].(map[string]interface{})
		require.EqualValues(t, 2, sum["projectView"])
		require.EqualValues(t, 1, sum["serviceView"])
		require.EqualValues(t, 1, sum["videoPlay"])
		require.EqualValues(t, 1, sum["share"])
		require.EqualValues(t, 1, sum["posterSave"])

		// 趋势行同样带意向计数：新加的 5 个 SUM(CASE) 别名必须 snake_case 才扫得进来，
		// 写成驼峰会**静默恒 0**（不报错），parity 是唯一能稳定抓到它的地方
		tr := body["data"].(map[string]interface{})["trend"].([]interface{})
		require.NotEmpty(t, tr)
		lastDay := tr[len(tr)-1].(map[string]interface{})
		require.Equal(t, time.Now().Format("2006-01-02"), lastDay["date"])
		require.EqualValues(t, 2, lastDay["projectView"])
		require.EqualValues(t, 1, lastDay["serviceView"])
		require.EqualValues(t, 1, lastDay["videoPlay"])
		require.EqualValues(t, 1, lastDay["share"])
		require.EqualValues(t, 1, lastDay["posterSave"])

		// TOP 榜：LEFT JOIN + COALESCE 取对象名（服务取 title 列）
		top := body["data"].(map[string]interface{})["topProjects"].([]interface{})
		require.NotEmpty(t, top)
		require.Equal(t, "意向案例", top[0].(map[string]interface{})["name"])
		require.EqualValues(t, 2, top[0].(map[string]interface{})["count"])
		topSvc := body["data"].(map[string]interface{})["topServices"].([]interface{})
		require.NotEmpty(t, topSvc)
		require.Equal(t, "意向服务", topSvc[0].(map[string]interface{})["name"])
		require.EqualValues(t, 1, topSvc[0].(map[string]interface{})["count"])

		// 明细下钻：targetId 筛选 + 对象名列
		_, body = c.do("GET", fmt.Sprintf("/api/admin/stats/detail?eventType=project_view&targetId=%d", projID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		require.EqualValues(t, 2, body["total"])
		for _, it := range body["data"].([]interface{}) {
			m := it.(map[string]interface{})
			require.EqualValues(t, projID, m["targetId"])
			require.Equal(t, "意向案例", m["targetName"])
		}
		_, body = c.do("GET", "/api/admin/stats/detail?targetId=abc", nil, adminToken)
		require.EqualValues(t, 1, body["code"])

		// ===== 2.5 来源分布 + 裂变榜（迁移 0022，parity 重点）=====
		// 新建一名员工专供来源/裂变断言，避免与上面 newStaffID 既有的
		// 1 条 unknown 来源 visit 计数耦合。这两处 SQL 形状——
		//   * sourceDist：visit 按 source 分组，CASE WHEN source='' THEN 'unknown'
		//     归一（历史空串数据也要落进 unknown，不能静默丢）；
		//   * fissionTop：visit + source='share_card' 按 staff_id LEFT JOIN
		//     employees 取名字（员工硬删除、employees 表无 deleted_at，JOIN 不能带
		//     deleted_at IS NULL，否则 MySQL 直接报「无此列」）；
		// ——只在 MySQL 8.0 上与 SQLite 有差异，故必须进共用流（让 TestAPIFlowMySQL
		// 用真 MySQL 跑同一套断言）。scan/search/poster/share_card/other 这几种来源
		// 全工程只有本员工发，故可下精确值；unknown 含 newStaffID 的那条，只断存在。
		_, body = c.do("POST", "/api/admin/employees", map[string]interface{}{
			"name": "来源员工", "phone": "13700000111", "email": "src@a.com", "title": "推广",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		srcStaffID := int64(body["staffId"].(float64))

		for _, ev := range []map[string]interface{}{
			{"staffId": srcStaffID, "eventType": "visit", "source": "scan"},
			{"staffId": srcStaffID, "eventType": "visit", "source": "scan"},
			{"staffId": srcStaffID, "eventType": "visit", "source": "search"},
			{"staffId": srcStaffID, "eventType": "visit", "source": "share_card"},
			{"staffId": srcStaffID, "eventType": "visit", "source": "share_card"},
			{"staffId": srcStaffID, "eventType": "visit", "source": "poster"},
		} {
			_, body = c.do("POST", "/api/stat/track", ev, "")
			require.EqualValues(t, 0, body["code"])
		}
		// 脏值 → other；空串 → unknown（normalizeSource 在进水口生效，source 只作统计维度）
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": srcStaffID, "eventType": "visit", "source": "DROP TABLE",
		}, "")
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("POST", "/api/stat/track", map[string]interface{}{
			"staffId": srcStaffID, "eventType": "visit",
		}, "")
		require.EqualValues(t, 0, body["code"])

		_, body = c.do("GET", "/api/admin/stats", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		src := body["data"].(map[string]interface{})["sourceDist"].([]interface{})
		bySrc := map[string]int64{}
		for _, it := range src {
			m := it.(map[string]interface{})
			bySrc[m["source"].(string)] = int64(m["count"].(float64))
		}
		require.EqualValues(t, 2, bySrc["scan"])
		require.EqualValues(t, 1, bySrc["search"])
		require.EqualValues(t, 2, bySrc["share_card"])
		require.EqualValues(t, 1, bySrc["poster"])
		require.EqualValues(t, 1, bySrc["other"])
		require.GreaterOrEqual(t, bySrc["unknown"], int64(1), "空串来源必须归一进 unknown")

		fis := body["data"].(map[string]interface{})["fissionTop"].([]interface{})
		require.Len(t, fis, 1, "只有 srcStaff 发了 share_card 来源的 visit")
		require.EqualValues(t, srcStaffID, fis[0].(map[string]interface{})["staffId"])
		require.Equal(t, "来源员工", fis[0].(map[string]interface{})["name"])
		require.EqualValues(t, 2, fis[0].(map[string]interface{})["count"])

		// 对象删除后：TOP 榜名字回落「已删除」但计数不减（汇总与榜单可对账）
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/projects/%d", projID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/stats", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		top = body["data"].(map[string]interface{})["topProjects"].([]interface{})
		require.NotEmpty(t, top)
		require.Equal(t, "已删除", top[0].(map[string]interface{})["name"])
		require.EqualValues(t, 2, top[0].(map[string]interface{})["count"])

		// 清理：服务与分类（项目已删）
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/services/%d", svcID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("DELETE", fmt.Sprintf("/api/admin/categories/%d", catID), nil, adminToken)
		require.EqualValues(t, 0, body["code"])
	})

	// ===== 7. 企业信息与首页布局 =====
	t.Run("企业内容与布局", func(t *testing.T) {
		_, body := c.do("PUT", "/api/admin/company-info", map[string]interface{}{
			"fullName": "朗汇机电", "address": "上海", "tel": "021-1", "logo": "l.png",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/company-info", nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		_, body = c.do("PUT", "/api/admin/company-profile", map[string]interface{}{
			"introText": "简介", "videoSource": "channels", "videoChannelId": "sph_abc",
			"videoFeedId": "feed123", "videoUrl": "", "videoFallbackUrl": false, "cover": "c.png",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/company-profile", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		// 视频来源能力：界面据此禁用「腾讯视频」档（默认部署未开启，必须带原因）
		_, body = c.do("GET", "/api/admin/video-capability", nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		_, body = c.do("PUT", "/api/admin/home-layout", map[string]interface{}{
			"order": []string{"service", "achievement"},
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/home-layout", nil, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 首页背景：写入 → 后台读回 → 公开接口下发
		bgURL := "http://127.0.0.1:8080/uploads/library/bg-deepspace.jpg"
		_, body = c.do("PUT", "/api/admin/home-layout", map[string]interface{}{
			"order": []string{"service", "achievement"}, "bgImage": bgURL, "bgMask": 40,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/home-layout", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		hl := body["data"].(map[string]interface{})
		require.Equal(t, bgURL, hl["bgImage"])
		require.EqualValues(t, 40, hl["bgMask"])

		// 公开接口根级下发（小程序按 order/bgImage/bgMask 读取）
		_, body = c.do("GET", "/api/home-layout", nil, "")
		require.EqualValues(t, 0, body["code"])
		require.Equal(t, bgURL, body["bgImage"])
		require.EqualValues(t, 40, body["bgMask"])
		require.EqualValues(t, []interface{}{"service", "achievement"}, body["order"])

		// 只提交 order（不带 bgImage）时背景保持——指针语义区分「未提供」与「清空」
		_, body = c.do("PUT", "/api/admin/home-layout", map[string]interface{}{
			"order": []string{"achievement", "service"},
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/home-layout", nil, adminToken)
		require.Equal(t, bgURL, body["data"].(map[string]interface{})["bgImage"], "未提供 bgImage 不应清空")

		// 遮罩浓度钳制到 0~100
		_, body = c.do("PUT", "/api/admin/home-layout", map[string]interface{}{
			"order": []string{"achievement", "service"}, "bgMask": 999,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/home-layout", nil, adminToken)
		require.EqualValues(t, 100, body["data"].(map[string]interface{})["bgMask"])

		// 显式传空串即清除背景（恢复灰底布局）
		_, body = c.do("PUT", "/api/admin/home-layout", map[string]interface{}{
			"order": []string{"achievement", "service"}, "bgImage": "", "bgMask": 0,
		}, adminToken)
		require.EqualValues(t, 0, body["code"])
		_, body = c.do("GET", "/api/admin/home-layout", nil, adminToken)
		require.Equal(t, "", body["data"].(map[string]interface{})["bgImage"])
		_, body = c.do("GET", "/api/home-layout", nil, "")
		require.Equal(t, "", body["bgImage"], "清空后公开接口应为空串，小程序据此不铺背景")
	})

	// ===== 8. 文件上传与存储增强（本地驱动回退分支） =====
	t.Run("上传与存储增强", func(t *testing.T) {
		// 构造 multipart/form-data 上传
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", "a.png")
		require.NoError(t, err)
		// 写入合法 PNG 文件头（魔法字节），满足上传内容嗅探校验
		_, _ = part.Write([]byte("\x89PNG\r\n\x1a\n"))
		require.NoError(t, mw.Close())

		req := httptest.NewRequest("POST", "/api/admin/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		var up map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &up))
		require.EqualValues(t, 0, up["code"])
		urls := up["data"].(map[string]interface{})["urls"].([]interface{})
		require.NotEmpty(t, urls)

		// 本地驱动不支持预签名 → code:1（前端回退普通上传）
		_, body := c.do("GET", "/api/admin/upload/presign?ext=png", nil, adminToken)
		require.EqualValues(t, 1, body["code"])

		// CDN 未配置 → code:1
		_, body = c.do("POST", "/api/admin/cdn/refresh", map[string]interface{}{"urls": []string{"x"}}, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 私有签名：本地驱动无 Signer 能力 → code:1
		_, body = c.do("POST", "/api/admin/storage/sign", map[string]interface{}{"urls": []string{"x"}}, adminToken)
		require.EqualValues(t, 1, body["code"])

		// 小程序码未配置 WX_APPID → code:1
		_, body = c.do("GET", "/api/wechat/wxacode?staffId=1", nil, "")
		require.EqualValues(t, 1, body["code"])
	})

	// ===== 9. 超管修改自身密码（同时验证令牌吊销） =====
	t.Run("超管改密与令牌吊销", func(t *testing.T) {
		oldToken := adminToken
		_, body := c.do("PUT", "/api/admin/password", map[string]interface{}{
			"old": "admin123456", "new": "admin123456",
		}, adminToken)
		require.EqualValues(t, 0, body["code"])

		// 改密后旧令牌立即失效（安全戳自增）→ 401
		_, body = c.do("GET", "/api/admin/employees", nil, oldToken)
		require.EqualValues(t, 401, body["code"])

		// 重新登录拿到新令牌（密码未变）
		adminToken = loginAdmin(t, c)

		// 原密码错误 → code:1（用新令牌才能走到密码校验逻辑）
		_, body = c.do("PUT", "/api/admin/password", map[string]interface{}{
			"old": "wrong", "new": "admin123456",
		}, adminToken)
		require.EqualValues(t, 1, body["code"])
	})
}

// TestAPIFlow 默认用 SQLite 跑完整接口流程（无需外部 MySQL）。
func TestAPIFlow(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	runAPIFlow(t, c, adminToken)
}

// TestCompanyProfileVideoSource 钉死「视频多来源」的语义。
// 这几条一旦松动，小程序端就会出现「明明配了却播不出来」的疑难问题——
// 排查时根本看不出当前走的是哪一路，所以必须在服务端把档位收敛成显式枚举。
func TestCompanyProfileVideoSource(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	put := func(m map[string]interface{}) map[string]interface{} {
		_, body := c.do("PUT", "/api/admin/company-profile", m, adminToken)
		return body
	}
	get := func() map[string]interface{} {
		_, body := c.do("GET", "/api/admin/company-profile", nil, adminToken)
		require.EqualValues(t, 0, body["code"])
		return body["data"].(map[string]interface{})
	}

	// 1) 档位是显式枚举，不在枚举内一律拒绝
	require.EqualValues(t, 1, put(map[string]interface{}{"videoSource": "bilibili"})["code"])

	// 2) 腾讯视频档默认关闭：插件必须在 app.json 静态声明，未授权会让整个小程序编译失败，
	//    故由部署方用 MP_TENCENT_VIDEO 显式开启；默认就该拒，防前端置灰被绕过
	//    （开启后的行为见 TestCompanyProfileVideoSourceTencent）
	require.EqualValues(t, 1, put(map[string]interface{}{"videoSource": "tencent", "videoVid": "v001"})["code"])

	// 3) 视频号档：channelId 只作留档，真正定位视频的是 feedId；兜底开关独立
	require.EqualValues(t, 0, put(map[string]interface{}{
		"videoSource": "channels", "videoChannelId": "sph_abc", "videoFeedId": "feed_1",
		"videoFeedToken": "tok_1",
		"videoUrl":       "https://cdn.example.com/fallback.mp4", "videoFallbackUrl": true,
	})["code"])
	d := get()
	require.Equal(t, "channels", d["videoSource"])
	require.Equal(t, "sph_abc", d["videoChannelId"])
	require.Equal(t, "feed_1", d["videoFeedId"])
	require.Equal(t, "tok_1", d["videoFeedToken"])
	require.EqualValues(t, true, d["videoFallbackUrl"])

	// 3.1) 只填 feed-token 也必须能保存：平台模式下客户视频号与我们小程序不同主体，
	//      feed-id 官方明确「仅同主体生效」，跨主体只能靠 feed-token
	require.EqualValues(t, 0, put(map[string]interface{}{
		"videoSource": "channels", "videoFeedId": "", "videoFeedToken": "tok_cross",
	})["code"])
	d = get()
	require.Equal(t, "", d["videoFeedId"])
	require.Equal(t, "tok_cross", d["videoFeedToken"])

	// 4) 外部直链档：videoUrl 是主源而非兜底
	require.EqualValues(t, 0, put(map[string]interface{}{
		"videoSource": "url", "videoUrl": "https://cdn.example.com/a.mp4",
	})["code"])
	d = get()
	require.Equal(t, "url", d["videoSource"])
	require.Equal(t, "https://cdn.example.com/a.mp4", d["videoUrl"])

	// 5) 空串归一为 none：该列后加，老客户端 / 脚本可能不带该字段
	require.EqualValues(t, 0, put(map[string]interface{}{"videoSource": ""})["code"])
	require.Equal(t, "none", get()["videoSource"])
}

// TestCompanyProfileVideoSourceTencent 钉死腾讯视频档的「系统设置开关 + vid 必填」语义。
//
// 为什么单独一个用例：该档不是「配了就能播」——它依赖小程序侧静态声明插件，
// 声明了却没授权会让整个小程序编译失败，所以默认关、由运维在系统设置里显式打开。
// 重点不是「开关能存」而是「开关真的接到了校验上」：一度有配置项存得进去却不生效
// （死配置项），界面上看着开了，保存仍被拒或仍放行，排查只能靠猜。
func TestCompanyProfileVideoSourceTencent(t *testing.T) {
	capOf := func(r *gin.Engine, tok string) map[string]interface{} {
		_, body := newTestClient(r, t).do("GET", "/api/admin/video-capability", nil, tok)
		require.EqualValues(t, 0, body["code"])
		return body["data"].(map[string]interface{})
	}
	putProfile := func(r *gin.Engine, tok string, m map[string]interface{}) map[string]interface{} {
		_, body := newTestClient(r, t).do("PUT", "/api/admin/company-profile", m, tok)
		return body
	}
	putSetting := func(r *gin.Engine, tok, v string) {
		_, body := newTestClient(r, t).do("PUT", "/api/admin/settings", map[string]string{"video_tencent_enabled": v}, tok)
		require.EqualValues(t, 0, body["code"], "设置项应可保存（registry 里必须有 video_tencent_enabled）")
	}

	// ---- 默认关闭（界面未设、部署环境变量也未开）----
	r := setupSQLiteEngine(t)
	tok := loginAdmin(t, newTestClient(r, t))

	cap1 := capOf(r, tok)
	require.EqualValues(t, false, cap1["tencentVideo"])
	require.NotEmpty(t, cap1["tencentReason"]) // 必须给可执行指引，不能只留一个灰选项

	body := putProfile(r, tok, map[string]interface{}{"videoSource": "tencent", "videoVid": "n0041aa087e"})
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "系统设置")

	// ---- 在系统设置里开启：即时生效，不需要重启 ----
	putSetting(r, tok, "true")
	cap2 := capOf(r, tok)
	require.EqualValues(t, true, cap2["tencentVideo"])
	require.Equal(t, "", cap2["tencentReason"])

	// 开了档却没填 vid：vid 是这一档唯一的取片凭证，空着就是配了个永远播不出来的档
	body = putProfile(r, tok, map[string]interface{}{"videoSource": "tencent", "videoVid": "  "})
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "vid")

	body = putProfile(r, tok, map[string]interface{}{
		"videoSource": "tencent", "videoVid": " n0041aa087e ",
		"videoUrl": "https://cdn.example.com/fallback.mp4", "videoFallbackUrl": true,
	})
	require.EqualValues(t, 0, body["code"])

	_, gb := newTestClient(r, t).do("GET", "/api/admin/company-profile", nil, tok)
	require.EqualValues(t, 0, gb["code"])
	d := gb["data"].(map[string]interface{})
	require.Equal(t, "tencent", d["videoSource"])
	// 首尾空白要剥掉：从腾讯视频页面复制 vid 时极易带上换行/空格，原样存库等于配了个坏链
	require.Equal(t, "n0041aa087e", d["videoVid"])

	// ---- 部署环境变量已开的机器上，界面显式关掉必须以界面为准 ----
	// （否则运维在界面关了却仍放行，界面就成了摆设）
	r2 := setupSQLiteEngine(t, func(c *config.Config) { c.TencentVideo = true })
	tok2 := loginAdmin(t, newTestClient(r2, t))
	require.EqualValues(t, true, capOf(r2, tok2)["tencentVideo"], "未设置时应回退环境变量 MP_TENCENT_VIDEO")

	putSetting(r2, tok2, "false")
	cap3 := capOf(r2, tok2)
	require.EqualValues(t, false, cap3["tencentVideo"])
	require.Contains(t, cap3["tencentReason"], "系统设置") // 显式关掉与「从未设置」的措辞要能区分
	require.EqualValues(t, 1, putProfile(r2, tok2, map[string]interface{}{
		"videoSource": "tencent", "videoVid": "n0041aa087e",
	})["code"])
}

// TestAPIFlowMySQL 生产契约 parity：连真实 MySQL、应用 migrations/*.sql，再跑同一套断言。
// 本地无 TEST_MYSQL_DSN 时跳过；CI 中用 MySQL service 容器提供 DSN。
func TestAPIFlowMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN (e.g. root:root@tcp(127.0.0.1:3306)/weichuan_test?charset=utf8mb4&parseTime=true) to run MySQL parity")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	// 可重复跑：先断表清空业务数据，再重新应用迁移（种子随 INSERT 重新写入），
	// 恢复到「刚迁移完」的状态。否则上一次跑出的员工/申请会把
	// 「尚不存在的员工 → code:1」这类断言带偏（本地迭代与 CI 共库都会踩）。
	var tables []string
	require.NoError(t, db.Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()",
	).Scan(&tables).Error)
	// 不能断言 tables 非空：全新库（mysql 容器刚建好、从未跑过迁移）就是 0 张表，
	// 而 parity 测试必须能在这种库上独立跑——CI 的 MySQL service 容器首启正是
	// 这个状态（曾因断言非空，空库上 0.01s 假失败，2026-10-21 修）。
	// 空列表时下面的 TRUNCATE 循环是空转，随后 applyMigrations 建全量 schema，终态一致。
	require.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error)
	for _, tbl := range tables {
		require.NoError(t, db.Exec("TRUNCATE TABLE `"+tbl+"`").Error)
	}
	require.NoError(t, db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error)
	applyMigrations(t, db)

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
	var refresher storage.CDNRefresher
	r := buildEngine(t, db, cfg, up, refresher)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	runAPIFlow(t, c, adminToken)
}

// TestMySQLMigrations 只验证「连得上 MySQL + migrations 能建表」，不跑业务断言。
// 用于在 CI 中把「连接 / 建表失败」与「业务逻辑在 MySQL 下不等价」区分开。
func TestMySQLMigrations(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run MySQL migrations check")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("SELECT 1").Error)

	applyMigrations(t, db)

	// 校验 migrations 确实建出了关键表（表名与 gorm 模型一致——复数；
	// 单数名是 2026-10-21 修掉的历史不一致，见 model 包的守门测试）
	for _, tbl := range []string{"employees", "achievement_categories", "home_layouts", "project_cases", "admins", "open_applies", "card_modify_applies"} {
		var cnt int64
		require.NoError(t, db.Raw(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", tbl,
		).Scan(&cnt).Error)
		require.EqualValues(t, 1, cnt, "table %s should exist after migrations", tbl)
	}
}

// applyMigrations 顺序应用 migrations 下的建表 SQL。
// 实现委托给 internal/migrate.Apply——与安装向导（MySQL 分支）同一条代码路径，
// 测试即生产。Apply 自带幂等容错（Duplicate column / already exists /
// Duplicate entry 忽略），parity 用例可重复跑。
func applyMigrations(t *testing.T, db *gorm.DB) {
	require.NoError(t, migrate.Apply(db))
}

// ---- 存储增强「成功分支」测试：用 fake 实现覆盖预签名 / 私有签名 / CDN 刷新 ----

// fakeUploader 同时实现 Uploader + Presigner + Signer，用于验证成功路径。
type fakeUploader struct{}

func (fakeUploader) Save(*multipart.FileHeader) (string, error) { return "http://up/x.png", nil }
func (fakeUploader) SaveBytes(key string, _ []byte, _ string) (string, error) {
	// 按 key 回 URL：素材采用 / 小程序码两条「固定对象名」路径都走这里，
	// 测试据此断言刷新的是刚覆盖写的那个对象，而不是上传的随机名对象。
	return "http://final/" + key, nil
}
func (fakeUploader) PresignPut(key, _ string, _ time.Duration) (string, string, error) {
	return "http://put/" + key, "http://final/" + key, nil
}
func (fakeUploader) SignGetURL(key string, _ time.Duration) (string, error) {
	return "http://signed/" + key, nil
}

// fakeRefresher 记录调用次数、每次刷新的 URL，并可按需返回错误（验证失败不冒泡）。
type fakeRefresher struct {
	calls int
	urls  []string
	err   error // 非 nil 时 Refresh 返回该错误
}

func (f *fakeRefresher) Refresh(urls []string) error {
	f.calls++
	f.urls = append(f.urls, urls...)
	return f.err
}

// TestStorageEnhanceSuccess 验证存储增强在对象存储驱动可用时的成功分支。
func TestStorageEnhanceSuccess(t *testing.T) {
	uploadDir, err := os.MkdirTemp("", "weichuan-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final", // sign 处理按此前缀反推 key
		StorageDriver: "minio",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
	}
	up := fakeUploader{}
	ref := &fakeRefresher{}
	r := buildEngine(t, newSeedDB(t), cfg, up, ref)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 预签名成功：返回 putUrl / url / key
	_, body := c.do("GET", "/api/admin/upload/presign?ext=png", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.NotEmpty(t, data["putUrl"])
	require.NotEmpty(t, data["url"])
	require.NotEmpty(t, data["key"])

	// 私有签名成功：返回 signed 映射（key 由 BaseURL 前缀反推）
	_, body = c.do("POST", "/api/admin/storage/sign",
		map[string]interface{}{"urls": []string{"http://final/abc.jpg"}}, adminToken)
	require.EqualValues(t, 0, body["code"])
	signed := body["data"].(map[string]interface{})["signed"].(map[string]interface{})
	require.Equal(t, "http://signed/abc.jpg", signed["http://final/abc.jpg"])

	// CDN 刷新成功：refresher 被调用且无错误
	_, body = c.do("POST", "/api/admin/cdn/refresh",
		map[string]interface{}{"urls": []string{"http://final/a.jpg"}}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, 1, ref.calls)
	require.Equal(t, []string{"http://final/a.jpg"}, ref.urls)
}

// TestCDNRefreshOnOverwritePaths 覆盖写路径的 CDN 刷新守门：
//  1. 素材采用（对象名 library/<素材ID>.<ext>）→ 刷新刚写入的 URL；
//  2. 小程序码重生成（对象名 wxacode/<staffID>.jpg）→ 刷新公开 URL；
//  3. 刷新失败只 [WARN] 记日志，接口仍返回 code:0（CDN 挂了不该让用户改图失败）。
//
// 背景（2026-10-23 修）：此前 refreshCDN 只挂在上传接口，而上传用随机对象名、
// 永不覆盖——真正会「同名换内容」的两条固定名路径反而一条都没接线。
// 未启用 CDN（refresher=nil）不炸由 TestAssetLibrary 覆盖：它用本地驱动 + nil
// refresher 走完整采用路径。
func TestCDNRefreshOnOverwritePaths(t *testing.T) {
	uploadDir, err := os.MkdirTemp("", "weichuan-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "http://final",
		StorageDriver: "minio",
		StoragePublic: true, // 公开桶：小程序码返回公开 URL（私有桶的签名 URL 不该交给 CDN 刷）
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
		WXAppID:       "wx-appid-for-test",
		WXAppSecret:   "wx-secret-for-test",
	}

	// 小程序码要调微信外网接口：注入假生成器，只覆盖「生成 → 覆盖写 → 刷新」这条支线
	origGen := getWXACode
	getWXACode = func(wechat.Config, string, string) ([]byte, error) { return []byte("fake-jpeg"), nil }
	t.Cleanup(func() { getWXACode = origGen })

	ref := &fakeRefresher{}
	r := buildEngine(t, newSeedDB(t), cfg, fakeUploader{}, ref)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 1. 素材采用：对象名固定，重复采用 = 覆盖写 → 必须让 CDN 边缘失效
	_, body := c.do("POST", "/api/admin/assets/adopt", map[string]interface{}{"id": "icon-house"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, []string{"http://final/library/icon-house.png"}, ref.urls,
		"采用后应刷新刚覆盖写的对象（而非上传的随机名对象）")

	// 2. 小程序码：对象名固定 wxacode/<staffID>.jpg，TTL 到期重生成 = 覆盖写
	const staffID = 990001 // 偏僻 ID：小程序码缓存是进程级全局 map，避免与其它用例串扰
	t.Cleanup(func() {
		wxacodeMu.Lock()
		delete(wxacodeCache, staffID)
		wxacodeMu.Unlock()
	})
	_, body = c.do("GET", fmt.Sprintf("/api/wechat/wxacode?staffId=%d", staffID), nil, "")
	require.EqualValues(t, 0, body["code"], "响应：%v", body)
	require.Equal(t, fmt.Sprintf("http://final/wxacode/%d.jpg", staffID),
		body["data"].(map[string]interface{})["url"])
	require.Equal(t, []string{
		"http://final/library/icon-house.png",
		fmt.Sprintf("http://final/wxacode/%d.jpg", staffID),
	}, ref.urls, "小程序码覆盖写后应刷新公开 URL")

	// 3. 刷新失败不冒泡：接口仍返回成功
	ref.err = errors.New("cdn down")
	_, body = c.do("POST", "/api/admin/assets/adopt", map[string]interface{}{"id": "icon-house"}, adminToken)
	require.EqualValues(t, 0, body["code"], "CDN 刷新失败不应让用户看到采用失败")
	require.Equal(t, 3, ref.calls, "失败也算一次尝试（有 [WARN] 日志可查）")
}

// TestTokenRefresh 验证 refresh 端点（Body 流，小程序端）与令牌吊销：
//  1. 用 refresh 令牌换取新 access，且新 access 可用；
//  2. 垃圾 refresh / 把 access 当 refresh 均被拒绝（类型与签名校验）；
//  3. 员工改密后旧令牌立即失效（安全戳机制）。
//
// 注：PC 超管的 Cookie 流刷新由 TestCookieAuth 覆盖。
func TestTokenRefresh(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 建员工并登录，拿到双令牌（员工端 Body 流：token/refreshToken 在响应体）
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "王五", "phone": "13700000003", "email": "w@a.com", "title": "经理",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	empID := int64(body["staffId"].(float64))
	initPwd := body["initialPassword"].(string)

	_, body = c.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": "13700000003", "password": initPwd,
	}, "")
	require.EqualValues(t, 0, body["code"])
	access := body["token"].(string)
	refresh := body["refreshToken"].(string)
	require.NotEmpty(t, access)
	require.NotEmpty(t, refresh)
	require.EqualValues(t, 900, body["expiresIn"])

	// 用 refresh 换新的 access → 新 access 可访问员工接口
	_, body = c.do("POST", "/api/refresh", map[string]interface{}{"refreshToken": refresh}, "")
	require.EqualValues(t, 0, body["code"])
	newAccess := body["token"].(string)
	require.NotEmpty(t, newAccess)
	_, body = c.do("GET", "/api/employee/me", nil, newAccess)
	require.EqualValues(t, 0, body["code"])

	// 垃圾 refresh → 401
	_, body = c.do("POST", "/api/refresh", map[string]interface{}{"refreshToken": "not-a-token"}, "")
	require.EqualValues(t, 401, body["code"])

	// 把 access 当 refresh 用（类型不符）→ 401
	_, body = c.do("POST", "/api/refresh", map[string]interface{}{"refreshToken": access}, "")
	require.EqualValues(t, 401, body["code"])

	// 员工改密（用初始密码）→ 旧令牌立即失效
	_, body = c.do("PUT", "/api/employee/password", map[string]interface{}{
		"old": initPwd, "new": "newpass1",
	}, access)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/employee/me", nil, access)
	require.EqualValues(t, 401, body["code"])

	_ = empID
}

// TestCookieAuth 验证 PC 后台的 httpOnly Cookie 鉴权路径：
//  1. 超管登录下发 access/refresh Cookie（HttpOnly + SameSite=Lax），且 body 不含 token；
//  2. 不带 Bearer、仅带 Cookie 即可访问受保护接口（浏览器自动携带的场景）；
//  3. /api/refresh 走 Cookie 流轮换新 access Cookie；
//  4. /api/logout 清除 Cookie，之后同 jar 访问 → 401。
func TestCookieAuth(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)

	// 1. 登录：下发 Cookie，body 无 token
	_, body := c.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, body["token"])
	var sawAccess bool
	for _, ck := range c.lastSetCookies {
		if ck.Name == "access_token" {
			sawAccess = true
			require.True(t, ck.HttpOnly, "access_token 必须 HttpOnly")
			require.Equal(t, http.SameSiteLaxMode, ck.SameSite, "SameSite=Lax 缓解 CSRF")
		}
		if ck.Name == "refresh_token" {
			require.True(t, ck.HttpOnly, "refresh_token 必须 HttpOnly")
		}
	}
	require.True(t, sawAccess, "应下发 access_token Cookie")

	// 2. 仅带 Cookie（无 Bearer）访问受保护接口 → 成功
	code, body := c.doWithCookie("GET", "/api/admin/employees", nil)
	require.Equal(t, 200, code)
	require.EqualValues(t, 0, body["code"])

	// 3. /api/refresh Cookie 流：轮换出新 access Cookie（不回传 token）
	_, body = c.doWithCookie("POST", "/api/refresh", nil)
	require.EqualValues(t, 0, body["code"])
	require.Empty(t, body["token"])
	_, body = c.doWithCookie("GET", "/api/admin/employees", nil)
	require.EqualValues(t, 0, body["code"])

	// 4. 登出：清 Cookie，之后同 jar 访问 → 401
	_, body = c.doWithCookie("POST", "/api/logout", nil)
	require.EqualValues(t, 0, body["code"])
	_, ok := c.cookies["access_token"]
	require.False(t, ok, "登出后 access_token 应从 jar 删除")
	_, body = c.doWithCookie("GET", "/api/admin/employees", nil)
	require.EqualValues(t, 401, body["code"])
}

// TestSingleDeviceRevocation 验证单设备精确吊销（会话黑名单，A3）：
//  1. 同一账号登录两次 = 两个独立会话（sid 不同），两个令牌均可用；
//  2. 设备 A 登出（携带其 Bearer）→ 只有 A 的 access 失效；
//  3. A 的 refresh 也失效（登出终结整条令牌链，刷新不换 sid）；
//  4. 设备 B 完全不受影响（与 token_version 账号级吊销的区别所在）。
func TestSingleDeviceRevocation(t *testing.T) {
	r := setupSQLiteEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// 建员工（超管创建后需用初始密码登录）
	_, body := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "赵六", "phone": "13600000004", "email": "z@a.com", "title": "技术",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	initPwd := body["initialPassword"].(string)

	login := func() (access, refresh string) {
		_, body := c.do("POST", "/api/employee/login", map[string]interface{}{
			"phone": "13600000004", "password": initPwd,
		}, "")
		require.EqualValues(t, 0, body["code"])
		return body["token"].(string), body["refreshToken"].(string)
	}

	// 设备 A / 设备 B 分别登录（两个会话）
	accessA, refreshA := login()
	accessB, refreshB := login()
	require.NotEqual(t, accessA, accessB, "两次登录应签发不同令牌")

	// 1. 两个令牌均可用
	_, body = c.do("GET", "/api/employee/me", nil, accessA)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/employee/me", nil, accessB)
	require.EqualValues(t, 0, body["code"])

	// 2. 设备 A 登出 → 只吊销 A 的会话
	_, body = c.do("POST", "/api/logout", nil, accessA)
	require.EqualValues(t, 0, body["code"])
	_, body = c.do("GET", "/api/employee/me", nil, accessA)
	require.EqualValues(t, 401, body["code"], "登出设备的 access 应立即失效")

	// 3. A 的 refresh 同样失效（会话已终结，不能靠刷新绕过登出）
	_, body = c.do("POST", "/api/refresh", map[string]interface{}{"refreshToken": refreshA}, "")
	require.EqualValues(t, 401, body["code"], "登出设备的 refresh 应立即失效")

	// 4. 设备 B 不受影响
	_, body = c.do("GET", "/api/employee/me", nil, accessB)
	require.EqualValues(t, 0, body["code"])

	// B 的 refresh 仍可正常换发（其 sid 未被吊销），且换出的新 access 可用
	_, body = c.do("POST", "/api/refresh", map[string]interface{}{"refreshToken": refreshB}, "")
	require.EqualValues(t, 0, body["code"])
	newAccessB := body["token"].(string)
	require.NotEmpty(t, newAccessB)
	_, body = c.do("GET", "/api/employee/me", nil, newAccessB)
	require.EqualValues(t, 0, body["code"])
}

// newSeedDB 建一个 SQLite 库并 seed 超管，供成功路径测试装配路由。
func newSeedDB(t *testing.T) *gorm.DB {
	tmpDB, err := os.CreateTemp("", "weichuan-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "廊坊市朗汇机电设备有限公司", ShortName: "朗汇机电", Status: "enabled"}).Error)
	return db
}

// TestHealthzRootOK 根路径是纯索引页：恒 200、列出子端点，不做健康判断。
// 保留它不是为兼容，是为「发现」——人手 curl 时一眼看到该用哪个子端点；
// 同时给 Nginx 的 auto_redirect 一个 200 的落点（否则 301 会打回后端形成循环）。
func TestHealthzRootOK(t *testing.T) {
	db := newSeedDB(t)
	r := New(db)
	code, body := newTestClient(r, t).do("GET", "/healthz", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["configured"])
	require.Nil(t, body["db"], "索引页不查 DB，不应有 db 字段")
	eps, ok := body["endpoints"].(map[string]interface{})
	require.True(t, ok, "根路径应列出子端点")
	require.Equal(t, "/healthz/live", eps["live"])
	require.Equal(t, "/healthz/ready", eps["ready"])
}

// TestHealthzRootIgnoresDB 索引页在 DB 挂掉时仍返回 200——这是刻意的设计，不是缺陷。
//
// 反过来说：**绝不能拿 /healthz 做告警**，DB 挂了它照样绿，监控会静默失效。
// 告警与编排一律打 /healthz/ready。这条测试的作用就是把「恒 200」这个约定钉死，
// 将来有人「顺手」给索引页加一句 SELECT 1 时，它会立刻红。
func TestHealthzRootIgnoresDB(t *testing.T) {
	db := closedDB(t)
	r := New(db)
	code, body := newTestClient(r, t).do("GET", "/healthz", nil, "")
	require.Equal(t, http.StatusOK, code, "索引页不查 DB，DB 挂了也应 200")
	require.Equal(t, "ok", body["status"])
	require.Nil(t, body["db"])
}

// TestHealthzReadyOK 就绪探针：DB 可达时 200 且声明 db=up。
// docker-compose 的 backend healthcheck 与 alert_check.sh 的 R1 都依赖它。
func TestHealthzReadyOK(t *testing.T) {
	db := newSeedDB(t)
	r := New(db)
	code, body := newTestClient(r, t).do("GET", "/healthz/ready", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "up", body["db"])
}

// closedDB 返回一个底层连接已关闭的库，使后续 Exec 必然失败。
func closedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return db
}

// TestHealthzReadyDBDown DB 不可达时就绪探针必须返回 503 且声明 db=down，
// 而不是伪装成健康的 200——否则负载均衡会把流量继续打给一个查不了库的实例。
func TestHealthzReadyDBDown(t *testing.T) {
	db := closedDB(t)
	r := New(db)
	code, body := newTestClient(r, t).do("GET", "/healthz/ready", nil, "")
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, "degraded", body["status"])
	require.Equal(t, "down", body["db"])
}

// TestHealthzLiveIgnoresDB 存活探针绝不查 DB。
//
// 这是本次拆分的目的：DB 故障时进程本身是健康的，liveness 若也报错，
// 编排会不停重启容器——而重启对「数据库连不上」毫无帮助，只会让服务反复
// 起停、日志被冲掉、排障更难。K8s 官方文档明确要求 liveness 不检查外部依赖。
func TestHealthzLiveIgnoresDB(t *testing.T) {
	db := closedDB(t)
	r := New(db)
	code, body := newTestClient(r, t).do("GET", "/healthz/live", nil, "")
	require.Equal(t, http.StatusOK, code, "DB 挂了 liveness 仍须 200")
	require.Equal(t, "ok", body["status"])
	require.Nil(t, body["db"], "liveness 不应报告任何依赖状态")
}

// TestHealthzTrailingSlashAndHead 探活端点必须对尾斜杠和 HEAD 都返回 200，
// 不能 301。这是线上 ERR_TOO_MANY_REDIRECTS 的回归测试：
// 只注册 /healthz 时，上游收到 /healthz/ 会 301 回 /healthz；若反代又把
// /healthz 改写成 /healthz/（宝塔目标URL 末尾多一个 /），两边互相踢皮球，
// 浏览器在 20 次后放弃。所有组合都必须是终态 200，不能有跳转。
//
// 子端点（/healthz/live、/healthz/ready）在 Nginx 侧天然免疫该循环——它们比
// 反代前缀 /healthz/ 长，不是「父路径」，不会触发 auto_redirect。这里一并断言，
// 避免将来有人把它们改成只注册 GET 或只注册一个形态。
func TestHealthzTrailingSlashAndHead(t *testing.T) {
	db := newSeedDB(t)
	r := New(db)
	cli := newTestClient(r, t)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/healthz/"},
		{"HEAD", "/healthz"},
		{"HEAD", "/healthz/"},
		{"GET", "/healthz/live"},
		{"HEAD", "/healthz/live"},
		{"GET", "/healthz/ready"},
		{"HEAD", "/healthz/ready"},
	} {
		code, _ := cli.do(tc.method, tc.path, nil, "")
		require.Equal(t, http.StatusOK, code,
			"%s %s 必须直接返回 200；若为 301 说明该组合没注册，反代改写路径时会与 Gin 的 RedirectTrailingSlash 形成死循环", tc.method, tc.path)
	}
}

// ===== 多租户第一期：数据隔离 + 两级后台 =====
//
// 覆盖点：
//  1. 平台超管登录 → isPlatform=true、companyId=0；可开通企业（一并建企业管理员）；
//  2. 企业管理员登录 → companyId=企业、isPlatform=false；只能看/改本企业数据；
//  3. 公开内容接口缺省落 1 号租户（朗汇），?companyId= 可切；企业停用即拒；
//  4. 越权防护：企业管理员访问企业管理接口 403、改不到别家分类/员工；
//  5. 申请流按租户归属：默认落朗汇，显式 companyId 落指定企业，审核后员工落到同企业。
func TestMultiTenantIsolation(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)

	// ===== 1. 平台超管登录与身份 =====
	_, body := plat.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, true, body["isPlatform"])
	require.EqualValues(t, 0, body["companyId"])

	_, body = plat.doWithCookie("GET", "/api/admin/me", nil)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, true, body["data"].(map[string]interface{})["isPlatform"])

	// 平台超管在朗汇（缺省目标租户）建一个分类
	_, body = plat.doWithCookie("POST", "/api/admin/categories", map[string]interface{}{"name": "朗汇分类A"})
	require.EqualValues(t, 0, body["code"])
	weichuanCatID := int64(body["data"].(map[string]interface{})["id"].(float64))
	require.NotZero(t, weichuanCatID)

	// ===== 2. 开通企业 2（一并建企业管理员；简称种子公司信息 + 侧边栏品牌名） =====
	// 显式 seedCategories=false：本用例要验的是**租户隔离**，乙公司一开始就该是空的，
	// 后面每条断言（「看不到别家分类」「?companyId=2 只看到自己的」）都是拿 0/1 在数。
	// 让业绩分类模板进来会把这套计数全改成「8 + n」，隔离意图被淹掉。
	// 模板本身由 TestCreateCompanySeedsCategoryTemplate 专门覆盖（含「必须落在新企业名下」）。
	_, body = plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "测试乙公司", "shortName": "乙公司简称", "adminUsername": "comp2", "adminPassword": "comp2pass",
		"seedCategories": false,
	})
	require.EqualValues(t, 0, body["code"])
	comp2ID := int64(body["data"].(map[string]interface{})["id"].(float64))
	require.NotZero(t, comp2ID)
	require.Equal(t, "乙公司简称", body["data"].(map[string]interface{})["shortName"], "创建响应应回显企业简称")

	_, body = plat.doWithCookie("GET", "/api/admin/companies", nil)
	require.EqualValues(t, 0, body["code"])
	comps := body["data"].([]interface{})
	require.Len(t, comps, 2, "应有朗汇 + 乙公司两个租户")
	for _, item := range comps {
		m := item.(map[string]interface{})
		if int64(m["id"].(float64)) == comp2ID {
			require.Equal(t, "乙公司简称", m["shortName"], "企业列表应返回简称")
		}
	}

	// ===== 3. 企业管理员登录与隔离 =====
	comp2 := newTestClient(r, t)
	_, body = comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, false, body["isPlatform"])
	require.EqualValues(t, comp2ID, body["companyId"])
	require.Equal(t, "测试乙公司", body["companyName"])
	require.Equal(t, "乙公司简称", body["companyShortName"], "登录响应应带企业简称（侧边栏品牌名）")

	// /api/admin/me 同样返回简称（页面刷新 hydration 用）
	_, body = comp2.doWithCookie("GET", "/api/admin/me", nil)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "乙公司简称", body["data"].(map[string]interface{})["companyShortName"])

	// 开通时填写的简称种子为公司信息简称的初始值（企业管理员登录后在此基础上完善）
	_, body = comp2.doWithCookie("GET", "/api/admin/company-info", nil)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "乙公司简称", body["data"].(map[string]interface{})["shortName"])

	// 企业管理员看不到朗汇的分类（空列表），也看不到企业管理入口
	_, body = comp2.doWithCookie("GET", "/api/admin/categories", nil)
	require.EqualValues(t, 0, body["code"])
	require.Len(t, body["data"].([]interface{}), 0, "企业管理员不应看到别家分类")

	_, body = comp2.doWithCookie("GET", "/api/admin/companies", nil)
	require.EqualValues(t, 403, body["code"], "企业管理员不得访问企业管理接口")

	// 企业管理员在自己的租户里建分类 → 仅自己可见
	_, body = comp2.doWithCookie("POST", "/api/admin/categories", map[string]interface{}{"name": "乙分类"})
	require.EqualValues(t, 0, body["code"])
	_, body = comp2.doWithCookie("GET", "/api/admin/categories", nil)
	list := body["data"].([]interface{})
	require.Len(t, list, 1)
	require.Equal(t, "乙分类", list[0].(map[string]interface{})["name"])

	// 平台超管用 ?companyId=2 能看到乙公司的分类
	_, body = plat.doWithCookie("GET", "/api/admin/categories?companyId=2", nil)
	require.EqualValues(t, 0, body["code"])
	require.Len(t, body["data"].([]interface{}), 1)

	// ===== 4. 公开内容接口：缺省朗汇，?companyId= 可切 =====
	_, body = plat.do("GET", "/api/achievements", nil, "")
	require.EqualValues(t, 0, body["code"])
	cats := body["categories"].([]interface{})
	require.Len(t, cats, 1)
	require.Equal(t, "朗汇分类A", cats[0].(map[string]interface{})["name"])

	_, body = plat.do("GET", "/api/achievements?companyId=2", nil, "")
	require.EqualValues(t, 0, body["code"])
	cats = body["categories"].([]interface{})
	require.Len(t, cats, 1)
	require.Equal(t, "乙分类", cats[0].(map[string]interface{})["name"])

	// 不存在的租户 → 明确拒绝
	_, body = plat.do("GET", "/api/achievements?companyId=99", nil, "")
	require.EqualValues(t, 1, body["code"])

	// 企业公开信息（小程序申请页展示申请目标用）：缺省朗汇，?companyId= 可切
	_, body = plat.do("GET", "/api/company", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "廊坊市朗汇机电设备有限公司", body["data"].(map[string]interface{})["name"])
	_, body = plat.do("GET", "/api/company?companyId=2", nil, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "测试乙公司", body["data"].(map[string]interface{})["name"])
	_, body = plat.do("GET", "/api/company?companyId=99", nil, "")
	require.EqualValues(t, 1, body["code"])

	// ===== 5. 越权修改防护 =====
	// 企业管理员改不到朗汇的分类
	_, body = comp2.doWithCookie("PUT", "/api/admin/categories/"+fmt.Sprint(weichuanCatID), map[string]interface{}{"name": "被篡改"})
	require.EqualValues(t, 1, body["code"])
	_, body = plat.doWithCookie("GET", "/api/admin/categories", nil)
	cats = body["data"].([]interface{})
	require.Equal(t, "朗汇分类A", cats[0].(map[string]interface{})["name"], "别家分类不应被改")

	// 平台超管手动建一个朗汇员工；企业管理员禁不掉他
	_, body = plat.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
		"name": "朗汇员工", "phone": "13900000001", "title": "工程师",
	})
	require.EqualValues(t, 0, body["code"])
	weichuanStaff := int64(body["staffId"].(float64))

	_, body = comp2.doWithCookie("GET", "/api/admin/employees", nil)
	require.Len(t, body["data"].([]interface{}), 0, "企业管理员不应看到别家员工")

	_, body = comp2.doWithCookie("POST", "/api/admin/employees/"+fmt.Sprint(weichuanStaff)+"/disable", nil)
	// 现在越权禁用会明确报「未找到」（RowsAffected==0），而不是像从前那样回 ok 假装成功——
	// 静默成功的代价是管理员无从察觉自己的操作压根没生效。隔离本身不变：员工仍是 enabled。
	require.EqualValues(t, 1, body["code"])
	_, body = plat.doWithCookie("GET", "/api/admin/employees", nil)
	emps := body["data"].([]interface{})
	require.Equal(t, "enabled", emps[0].(map[string]interface{})["status"], "越权禁用不应生效")

	// 平台超管 ?companyId=1 只看朗汇员工
	_, body = plat.doWithCookie("GET", "/api/admin/employees?companyId=1", nil)
	require.Len(t, body["data"].([]interface{}), 1)

	// ===== 6. 申请流按租户归属 =====
	// 默认申请 → 落朗汇；企业管理员看不到
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "默认申请人", "phone": "13800000002", "title": "销售",
	}, "")
	require.EqualValues(t, 0, body["code"])
	_, body = comp2.doWithCookie("GET", "/api/admin/apply/open", nil)
	require.Len(t, body["data"].([]interface{}), 0)

	// 显式 companyId=2 的申请 → 企业管理员可见并可审核
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "乙公司申请人", "phone": "13800000003", "phone2": "13800000033", "title": "销售", "companyId": comp2ID,
	}, "")
	require.EqualValues(t, 0, body["code"])
	_, body = comp2.doWithCookie("GET", "/api/admin/apply/open", nil)
	applies := body["data"].([]interface{})
	require.Len(t, applies, 1)
	applyID := int64(applies[0].(map[string]interface{})["id"].(float64))

	_, body = comp2.doWithCookie("POST", "/api/admin/apply/open/"+fmt.Sprint(applyID)+"/approve", nil)
	require.EqualValues(t, 0, body["code"])
	newStaff := int64(body["staffId"].(float64))
	require.NotZero(t, newStaff)

	// 审核出的员工落在乙公司：企业管理员列表可见，朗汇列表不可见
	_, body = comp2.doWithCookie("GET", "/api/admin/employees", nil)
	require.Len(t, body["data"].([]interface{}), 1)
	_, body = plat.doWithCookie("GET", "/api/admin/employees?companyId=1", nil)
	require.Len(t, body["data"].([]interface{}), 1, "朗汇仍只有手动建的那个员工")

	// 公开名片按员工归属企业取公司信息（乙公司的 CompanyInfo 为空但可读）；
	// 同时校验申请里填的第二个手机号已随审核落到员工资料（名片联系表第二行要用）
	_, body = plat.do("GET", "/api/card?staffId="+fmt.Sprint(newStaff), nil, "")
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, comp2ID, body["company"].(map[string]interface{})["companyId"])
	require.Equal(t, "13800000033", body["employee"].(map[string]interface{})["phone2"])

	// ===== 7. 企业停用：管理员不能登录 =====
	_, body = plat.doWithCookie("POST", "/api/admin/companies/"+fmt.Sprint(comp2ID)+"/status", map[string]interface{}{"status": "disabled"})
	require.EqualValues(t, 0, body["code"])

	_, body = comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 2, body["code"], "企业停用后管理员不能登录")

	// 停用企业的公开内容也被拒
	_, body = plat.do("GET", "/api/achievements?companyId=2", nil, "")
	require.EqualValues(t, 1, body["code"])

	// 恢复启用 → 可再登录
	_, body = plat.doWithCookie("POST", "/api/admin/companies/"+fmt.Sprint(comp2ID)+"/status", map[string]interface{}{"status": "enabled"})
	require.EqualValues(t, 0, body["code"])
	_, body = comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 0, body["code"])

	// ===== 8. 编辑企业（名称/简称）与简称解耦 =====
	_, body = plat.doWithCookie("PUT", "/api/admin/companies/"+fmt.Sprint(comp2ID), map[string]interface{}{
		"name": "测试乙公司改名后", "shortName": "乙司",
	})
	require.EqualValues(t, 0, body["code"])

	_, body = plat.doWithCookie("GET", "/api/admin/companies", nil)
	require.EqualValues(t, 0, body["code"])
	for _, item := range body["data"].([]interface{}) {
		m := item.(map[string]interface{})
		if int64(m["id"].(float64)) == comp2ID {
			require.Equal(t, "测试乙公司改名后", m["name"])
			require.Equal(t, "乙司", m["shortName"], "编辑后列表应返回新简称")
		}
	}

	// 公司信息里的简称不随编辑变化（两个简称相互独立：公司信息简称仅小程序展示）
	_, body = comp2.doWithCookie("GET", "/api/admin/company-info", nil)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "乙公司简称", body["data"].(map[string]interface{})["shortName"], "编辑企业不应回写公司信息简称")

	// 企业管理员重新登录：侧边栏品牌名取改后的简称
	_, body = comp2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "comp2", "password": "comp2pass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "乙司", body["companyShortName"])
}

// TestCompanyShortNameDefault 企业简称缺省值：企业管理里未设置简称（留空/纯空白）时，
// company.short_name 回落「微传名片」（侧边栏品牌名）；公司信息简称不跟随该缺省值
// （留空 → 小程序顶栏展示企业全称），两个简称相互独立。
func TestCompanyShortNameDefault(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)

	_, body := plat.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "admin123456",
	}, "")
	require.EqualValues(t, 0, body["code"])

	// 不填简称开通 → company.short_name 缺省「微传名片」
	_, body = plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "缺省简称公司", "adminUsername": "defcomp", "adminPassword": "defpass",
	})
	require.EqualValues(t, 0, body["code"])
	compID := int64(body["data"].(map[string]interface{})["id"].(float64))
	require.Equal(t, "微传名片", body["data"].(map[string]interface{})["shortName"], "未填简称应回落缺省值")

	// 企业管理员登录：companyShortName 取缺省值（侧边栏「微传名片 · 管理后台」）
	defc := newTestClient(r, t)
	_, body = defc.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "defcomp", "password": "defpass",
	}, "")
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "微传名片", body["companyShortName"])

	// 公司信息简称不跟随缺省值：留空 → 小程序展示企业全称
	_, body = defc.doWithCookie("GET", "/api/admin/company-info", nil)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "", body["data"].(map[string]interface{})["shortName"])

	// 编辑时清空（纯空白）简称 → 同样回落缺省值
	_, body = plat.doWithCookie("PUT", "/api/admin/companies/"+fmt.Sprint(compID), map[string]interface{}{
		"name": "缺省简称公司", "shortName": "   ",
	})
	require.EqualValues(t, 0, body["code"])
	_, body = plat.doWithCookie("GET", "/api/admin/companies", nil)
	require.EqualValues(t, 0, body["code"])
	for _, item := range body["data"].([]interface{}) {
		m := item.(map[string]interface{})
		if int64(m["id"].(float64)) == compID {
			require.Equal(t, "微传名片", m["shortName"], "清空简称后应回落缺省值")
		}
	}
}
