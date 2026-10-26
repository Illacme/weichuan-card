package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupEngineWithSourceDir 装配与生产等价的路由，但把 MPSourceDir 指向测试夹具，
// 使 /api/admin/settings/paths?kind=source_dir 在有配置值时能进入那个目录浏览
// （否则默认白名单 + 未配置值会落到「无起点目录」的拒绝分支）。
func setupEngineWithSourceDir(t *testing.T, srcDir string) *gin.Engine {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "weichuan-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "朗汇", ShortName: "朗汇", Status: "enabled"}).Error)

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
		MPSourceDir:   srcDir,
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

func TestSettingsPathsRequiresAuth(t *testing.T) {
	srcDir := t.TempDir()
	r := setupEngineWithSourceDir(t, srcDir)
	c := newTestClient(r, t)

	// 未登录 → 401（HTTP 仍 200，按本工程约定看 body.code）
	code, body := c.do("GET", "/api/admin/settings/paths?kind=source_dir", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 401, body["code"])
}

func TestSettingsPathsRejectsUnknownKind(t *testing.T) {
	srcDir := t.TempDir()
	r := setupEngineWithSourceDir(t, srcDir)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/settings/paths?kind=bogus", nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "未知的路径类型")
}

func TestSettingsPathsModuleDirDefaultsWithoutConfig(t *testing.T) {
	// 用户截图场景：module_dir 未配置时，旧实现把起点硬塞成家目录，家目录不在白名单
	// 就报「该路径不在允许浏览的范围内」。修复后应正常返回一个可浏览的起点。
	r := setupEngineWithSourceDir(t, "")
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	code, body := c.do("GET", "/api/admin/settings/paths?kind=module_dir", nil, tok)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"], "响应：%v", body)

	data := body["data"].(map[string]interface{})
	require.NotEmpty(t, data["path"])
}

func TestSettingsPathsListsConfiguredSourceDir(t *testing.T) {
	// 造一个工程目录：srcDir 本身即工程目录（含 project.config.json），
	// 其子目录 myapp 同样含 project.config.json，用于验证「徽标」能标出正确目录。
	srcDir := t.TempDir()
	// macOS 上 TMPDIR 常常本身就是软链（/var/folders → /private/var/folders），
	// 而服务端出于安全会对路径做 EvalSymlinks 后返回真实路径。断言必须用同一口径
	// 比对，否则的表现是：Linux CI 全绿、macOS 上必然失败，且与被测逻辑无关。
	if resolved, err := filepath.EvalSymlinks(srcDir); err == nil {
		srcDir = resolved
	}
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "project.config.json"), []byte("{}"), 0o644))
	appDir := filepath.Join(srcDir, "myapp")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "project.config.json"), []byte("{}"), 0o644))

	r := setupEngineWithSourceDir(t, srcDir)
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	code, body := c.do("GET", "/api/admin/settings/paths?kind=source_dir", nil, tok)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["code"], "响应：%v", body)

	data := body["data"].(map[string]interface{})
	require.Equal(t, "source_dir", data["kind"])
	require.Equal(t, srcDir, data["path"]) // 起点应是已配置的源码目录

	entries := data["entries"].([]interface{})
	var sawApp bool
	for _, it := range entries {
		m := it.(map[string]interface{})
		if m["name"] == "myapp" {
			sawApp = true
			require.Equal(t, "小程序工程目录", m["marker"])
		}
	}
	require.True(t, sawApp, "应在列表中看到带徽标的工程目录 myapp")

	// 当前项（源码目录本身）校验应通过
	cur := data["current"].(map[string]interface{})
	require.Equal(t, true, cur["ok"])
}

func TestSettingsPathsArraysAreNeverNull(t *testing.T) {
	// 用户实测「另外两项点浏览弹不出窗口」的根因：Go 的 nil slice 序列化成 JSON null，
	// 而选择器模板按数组用（entries.length / suggestions.length），null 直接抛
	// TypeError，弹窗体渲染失败。接口层面必须保证这两个字段永远是数组。
	for _, kind := range []string{"node_bin", "module_dir", "source_dir"} {
		r := setupEngineWithSourceDir(t, "")
		c := newTestClient(r, t)
		tok := loginAdmin(t, c)

		code, body := c.do("GET", "/api/admin/settings/paths?kind="+kind, nil, tok)
		require.Equal(t, http.StatusOK, code)
		require.EqualValues(t, 0, body["code"], "kind=%s 响应：%v", kind, body)

		data := body["data"].(map[string]interface{})
		require.IsType(t, []interface{}{}, data["entries"], "kind=%s 的 entries 不是数组：%v", kind, data["entries"])
		require.IsType(t, []interface{}{}, data["suggestions"], "kind=%s 的 suggestions 不是数组：%v", kind, data["suggestions"])
		// 选中路径与「是否目录」是界面的高亮/确认依据，必须下发
		require.NotEmpty(t, data["selected"], "kind=%s 未下发 selected", kind)
		require.Contains(t, data, "selectedIsDir", "kind=%s 未下发 selectedIsDir", kind)
	}
}
