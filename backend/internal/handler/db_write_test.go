package handler

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 写库失败与登录失败审计的接口级守门。
//
// 为什么用「真实制造数据库故障」而不是 mock：本项目吃过两次「只换二进制、
// 漏同步 migrations/」的亏（0035 / 0038），真实表现就是某个写接口开始报
// Unknown column。用 DROP TABLE / 注入 GORM 回调能复现同一类故障，且顺带
// 验证「错误不回显到客户端」这条边界；mock 掉 db 反而测不到实际 SQL 路径。
//
// 库一律用临时文件库（不用 :memory:）：gorm 连接池会把 :memory: 拆成多个
// 互不相关的库，DROP TABLE 与请求可能落到不同连接上。
func dbWriteEngine(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	t.Setenv("UPLOAD_DIR", t.TempDir())

	tmpDB, err := os.CreateTemp("", "weichuan-dbwrite-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{
		ID: 1, Name: "测试企业", ShortName: "测试", Status: "enabled", LoginCode: "test",
	}).Error)

	cfg := config.Config{
		UploadDir: t.TempDir(), UploadBaseURL: "http://127.0.0.1:8080/uploads",
		StorageDriver: "local", StoragePublic: true,
		JWTKey: "test-jwt-key", AdminUser: "admin", AdminPass: "admin123456",
	}
	up := &storage.LocalUploader{
		Dir: cfg.UploadDir, BaseURL: cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024, AllowExt: storage.DefaultAllowExt,
	}
	r := buildEngine(t, db, cfg, up, nil)
	require.NoError(t, setting.Upsert("content_sec_enabled", "false"))
	return r, db
}

type writeCase struct {
	name, method, path string
	body               map[string]interface{}
	tbl                interface{}
}

func contentWriteCases() []writeCase {
	return []writeCase{
		{"公司基础信息", "PUT", "/api/admin/company-info",
			map[string]interface{}{"fullName": "甲公司"}, &model.CompanyInfo{}},
		{"业绩分类", "POST", "/api/admin/categories",
			map[string]interface{}{"name": "机房工程"}, &model.AchievementCategory{}},
		{"项目案例", "POST", "/api/admin/projects",
			map[string]interface{}{"name": "案例", "categoryId": 1}, &model.ProjectCase{}},
		{"业务服务", "POST", "/api/admin/services",
			map[string]interface{}{"title": "运维服务"}, &model.BizService{}},
		{"首页布局", "PUT", "/api/admin/home-layout",
			map[string]interface{}{"order": []string{"achievement", "service"}}, &model.HomeLayout{}},
	}
}

// TestContentWriteFailureIsReported：写库失败绝不能回 code:0。
// 每条先发一次「库还在」的请求证明参数合法，否则「失败」可能只是参数错误，
// 测试会绿得毫无意义。
func TestContentWriteFailureIsReported(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	for _, tc := range contentWriteCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, okBody := c.do(tc.method, tc.path, tc.body, tok)
			require.EqualValues(t, 0, okBody["code"], "%s 前置请求应成功：%v", tc.name, okBody)

			require.NoError(t, db.Migrator().DropTable(tc.tbl))
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.NotEqualValues(t, 0, body["code"], "%s 写库失败却回了 code:0", tc.name)
			msg := fmt.Sprint(body["msg"])
			require.Contains(t, msg, "失败", "%s 应给出可理解的失败文案", tc.name)
			// 运维要能在服务端日志看到真实 SQL 错误，客户端则只能拿到固定文案
			require.NotContains(t, msg, "no such table", "%s 把原始 SQL 错误回显给了客户端", tc.name)
		})
	}
}

// TestContentUpdateFailureIsReported：覆盖**更新**分支（DROP TABLE 只会走到新建分支）。
// 用 GORM 的 Update 回调注入错误，模拟「表在、行在，但 UPDATE 本身失败」——
// 这正是漏同步 migrations 后最典型的线上形态。
func TestContentUpdateFailureIsReported(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	// 注入回调之前先把待更新的行建好（回调只拦 Update，不拦 Create）
	require.NoError(t, db.Create(&model.CompanyInfo{CompanyID: 1, FullName: "旧名"}).Error)
	// 公司简介也必须预建：否则它走「新建」分支（Create 不受 Update 回调影响），
	// 用例会假绿——测到的根本不是更新失败这条路径。
	require.NoError(t, db.Create(&model.CompanyProfile{CompanyID: 1, VideoSource: model.VideoSourceNone}).Error)
	require.NoError(t, db.Create(&model.AchievementCategory{CompanyID: 1, Name: "旧分类"}).Error)
	require.NoError(t, db.Create(&model.BizService{CompanyID: 1, Title: "旧服务"}).Error)
	require.NoError(t, db.Create(&model.HomeLayout{CompanyID: 1, Order: `["achievement"]`}).Error)

	const cbName = "test:inject_update_error"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		tx.AddError(errors.New("injected update failure"))
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(cbName) })

	cases := []struct {
		name, method, path string
		body               map[string]interface{}
	}{
		{"公司基础信息", "PUT", "/api/admin/company-info",
			map[string]interface{}{"fullName": "新名"}},
		{"公司简介（既有对照实现）", "PUT", "/api/admin/company-profile",
			map[string]interface{}{"introText": "新简介", "videoSource": "none"}},
		{"业绩分类", "PUT", "/api/admin/categories/1",
			map[string]interface{}{"name": "新分类", "icon": "", "sort": 0, "isLocked": false}},
		{"业务服务", "PUT", "/api/admin/services/1",
			map[string]interface{}{"title": "新服务", "icon": "", "content": "", "sort": 0}},
		{"首页布局", "PUT", "/api/admin/home-layout",
			map[string]interface{}{"order": []string{"service", "achievement"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, tok)
			require.NotEqualValues(t, 0, body["code"], "%s 更新失败却回了 code:0", tc.name)
			require.NotContains(t, fmt.Sprint(body["msg"]), "injected", "%s 把底层错误原文回显给了客户端", tc.name)
		})
	}
}

// TestEmployeeLoginFailAuditHasNoPlainPII：登录失败审计不得成为 PII 加密的旁路。
// 员工表 phone/name 是静态加密列，审计表却是只追加的明文表；把手机号或姓名
// 写进去，等于任何一个能看审计日志的人都能拿到明文 PII。
func TestEmployeeLoginFailAuditHasNoPlainPII(t *testing.T) {
	r, db := dbWriteEngine(t)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	phone, name := "13900001234", "张三"
	_, created := c.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": name, "phone": phone, "title": "工程师",
	}, tok)
	require.EqualValues(t, 0, created["code"], "建员工应成功：%v", created)
	staffID := int64(created["staffId"].(float64))

	probe := newTestClient(r, t)
	_, body := probe.do("POST", "/api/employee/login", map[string]interface{}{
		"phone": phone, "password": "definitely-wrong",
	}, "")
	require.EqualValues(t, 1, body["code"])

	var row model.AuditLog
	require.NoError(t, db.Where("action = ?", audit.ActionLoginFail).
		Order("id desc").First(&row).Error)
	require.Equal(t, fmt.Sprint(staffID), row.TargetID, "审计应落到员工编号，而不是手机号")
	for _, field := range []string{row.TargetID, row.ActorName, row.Detail, row.IP} {
		require.NotContains(t, field, phone)
		require.NotContains(t, field, name)
	}
}

// TestNoIgnoredDBWritesInTouchedHandlers：源码级守门，防止后续新增接口又把
// GORM 的 Error 丢掉。剥掉整行注释后逐行找「裸写库」语句。
func TestNoIgnoredDBWritesInTouchedHandlers(t *testing.T) {
	commentLine := regexp.MustCompile(`(?m)^[ \t]*//.*$`)
	for _, file := range []string{"content.go", "apply.go", "auth.go", "admins.go"} {
		src, err := os.ReadFile(file)
		require.NoError(t, err)
		clean := commentLine.ReplaceAllString(string(src), "")
		for i, line := range strings.Split(clean, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "//") || l == "" {
				continue
			}
			isWrite := strings.HasPrefix(l, "db.Create(") ||
				strings.HasPrefix(l, "db.Save(") ||
				strings.HasPrefix(l, "db.Delete(") ||
				(strings.HasPrefix(l, "db.Model(") && strings.Contains(l, ".Updates("))
			if !isWrite {
				continue
			}
			require.Failf(t, "裸写库", "%s:%d 写库未判 Error：%s", file, i+1, l)
		}
	}
}
