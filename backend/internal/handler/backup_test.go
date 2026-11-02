package handler

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/config"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// resetBackupState 清掉进程内的运行态：bkState 是包级单例，
// 上一个用例留下的 running/last 会污染下一个（例如「正在运行」永远为真）。
func resetBackupState(t *testing.T) {
	t.Helper()
	clear := func() {
		bkState.mu.Lock()
		bkState.running = false
		bkState.last = nil
		bkState.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// useSyncDispatch 把备份改成同步执行：异步 + sleep 等待既慢又不稳，
// 这里直接跑完再断言（与 subscribe.go 的 dispatch 同一套路）。
func useSyncDispatch(t *testing.T) {
	t.Helper()
	old := backupDispatch
	backupDispatch = func(fn func()) { fn() }
	t.Cleanup(func() { backupDispatch = old })
}

// makeSourceSQLite 造一个真实可快照的 SQLite 库（有表、有数据）。
func makeSourceSQLite(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.db")
	db, err := gorm.Open(sqlite.Open(p), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{Name: "备份测试企业", Status: "enabled"}).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return p
}

func TestBackupKindOf(t *testing.T) {
	cases := map[string]string{
		"weichuan-20261102-030000.db.gz":     "db",
		"weichuan-20261102-030000.sql.gz":    "db",
		"uploads-20261102-030000.tar.gz":     "uploads",
		"secrets-20261102-030000.tar.gz.enc": "secrets",
		// 以下都不该被认成备份产物：下载接口靠这个判断拒绝请求
		"notes.txt":                          "",
		"../../etc/passwd":                   "",
		"weichuan-20261102-030000.db.gz.bak": "",
		"uploads-20261102-030000.tar.gz.enc": "",
		"secrets-20261102-030000.tar.gz":     "",
	}
	for name, want := range cases {
		require.Equal(t, want, backupKindOf(name), "文件名 %q", name)
	}
}

// 目录不存在不是错误：新部署还没跑过备份，面板该显示「还没有备份」而不是报错。
func TestBackupStatusEmptyWhenDirMissing(t *testing.T) {
	resetBackupState(t)
	bkDir := filepath.Join(t.TempDir(), "not-created")
	r := setupSQLiteEngine(t, func(c *config.Config) { c.BackupDir = bkDir })
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/backup/status", nil, tok)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.Empty(t, data["items"])
	require.Equal(t, false, data["running"])
	require.Nil(t, data["lastRun"])

	// 缺密钥包必须明说，不能让人以为「三件套齐了」
	notes := data["notes"].([]interface{})
	require.NotEmpty(t, notes)
	joined := ""
	for _, n := range notes {
		joined += n.(string)
	}
	require.Contains(t, joined, "运行时密钥")
}

// 端到端：触发一次备份 → 产出库与图片 → 状态能看到 → 能下载 → 路径穿越被拒。
func TestBackupRunThenDownload(t *testing.T) {
	resetBackupState(t)
	useSyncDispatch(t)

	srcDB := makeSourceSQLite(t)
	bkDir := filepath.Join(t.TempDir(), "backups")
	upDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upDir, "avatar.png"), []byte("IMG"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(upDir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(upDir, "sub", "case.jpg"), []byte("IMG2"), 0o644))

	r := setupSQLiteEngine(t, func(c *config.Config) {
		c.DBDSN = srcDB
		c.BackupDir = bkDir
		c.UploadDir = upDir
	})
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/backup/run", nil, tok)
	require.EqualValues(t, 0, body["code"], "触发备份应成功：%v", body["msg"])

	_, body = c.do("GET", "/api/admin/backup/status", nil, tok)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, true, data["canRun"])
	require.Equal(t, "sqlite", data["driver"])

	items := data["items"].([]interface{})
	require.Len(t, items, 2, "应产出库与图片两件")
	// 顺序固定：库在前、图片在后（前端按这个顺序渲染）
	first := items[0].(map[string]interface{})
	second := items[1].(map[string]interface{})
	require.Equal(t, "db", first["kind"])
	require.Equal(t, "uploads", second["kind"])
	require.Equal(t, true, first["fresh"])
	require.Greater(t, first["bytes"].(float64), float64(0))

	lastRun := data["lastRun"].(map[string]interface{})
	require.Equal(t, true, lastRun["ok"], "lastRun 应记录成功：%v", lastRun["message"])

	// 文件真的落在磁盘上
	dbFile := first["file"].(string)
	upFile := second["file"].(string)
	require.FileExists(t, filepath.Join(bkDir, dbFile))
	require.FileExists(t, filepath.Join(bkDir, upFile))

	// 下载：拿到的是 gzip 原始字节（不是 JSON），且带附件头
	w := doRawRequest(r, tok, "/api/admin/backup/download?name="+url.QueryEscape(dbFile))
	require.Equal(t, 200, w.Code)
	require.Greater(t, w.Body.Len(), 2)
	require.Equal(t, byte(0x1f), w.Body.Bytes()[0], "应是 gzip 魔数")
	require.Equal(t, byte(0x8b), w.Body.Bytes()[1])
	require.Contains(t, w.Header().Get("Content-Disposition"), dbFile)

	// 路径穿越与非白名单文件名都必须被拒
	for _, bad := range []string{"../../etc/passwd", "notes.txt", "weichuan-20261102-030000.db.gz.bak"} {
		_, body = c.do("GET", "/api/admin/backup/download?name="+url.QueryEscape(bad), nil, tok)
		require.EqualValues(t, 1, body["code"], "应拒绝 %q", bad)
	}
}

// 密钥包认得出来（面板要显示有没有），但后台不给下载。
// 这是「密钥不与数据同路」的代码约束——只靠前端不画按钮，改天有人手拼 URL 就绕过去了。
func TestBackupDownloadRejectsSecrets(t *testing.T) {
	resetBackupState(t)
	bkDir := t.TempDir()
	// 真放一个密钥包在目录里：证明拒绝的原因不是「文件不存在」
	name := "secrets-20261102-030000.tar.gz.enc"
	require.NoError(t, os.WriteFile(filepath.Join(bkDir, name), []byte("CIPHERTEXT"), 0o600))

	r := setupSQLiteEngine(t, func(c *config.Config) { c.BackupDir = bkDir })
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("GET", "/api/admin/backup/download?name="+url.QueryEscape(name), nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"].(string), "scp")

	// 状态接口仍然如实报告它存在，并解释为什么下不了
	_, body = c.do("GET", "/api/admin/backup/status", nil, tok)
	data := body["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	require.Len(t, items, 1)
	require.Equal(t, "secrets", items[0].(map[string]interface{})["kind"])

	joined := ""
	for _, n := range data["notes"].([]interface{}) {
		joined += n.(string)
	}
	require.Contains(t, joined, "不提供下载")
}

// MySQL 形态后台不代跑：mysqldump 不在运行镜像里，硬跑只会产出半份备份。
func TestBackupRunRejectsMySQL(t *testing.T) {
	resetBackupState(t)
	useSyncDispatch(t)

	r := setupSQLiteEngine(t, func(c *config.Config) {
		// 默认 cfg.DBDSN 为空 → 非 SQLite 形态
		c.BackupDir = filepath.Join(t.TempDir(), "backups")
	})
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/backup/run", nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"].(string), "MySQL")

	_, body = c.do("GET", "/api/admin/backup/status", nil, tok)
	data := body["data"].(map[string]interface{})
	require.Equal(t, false, data["canRun"])
	require.Equal(t, "mysql", data["driver"])
}

// 连点两次不能并发出两份（同一秒的两个包会互相覆盖，且图片可能被打包两次）。
func TestBackupRunRejectsConcurrent(t *testing.T) {
	resetBackupState(t)
	old := backupDispatch
	// 故意不执行：模拟「任务还在跑」
	backupDispatch = func(fn func()) {}
	t.Cleanup(func() { backupDispatch = old })

	r := setupSQLiteEngine(t, func(c *config.Config) {
		c.DBDSN = makeSourceSQLite(t)
		c.BackupDir = filepath.Join(t.TempDir(), "backups")
	})
	c := newTestClient(r, t)
	tok := loginAdmin(t, c)

	_, body := c.do("POST", "/api/admin/backup/run", nil, tok)
	require.EqualValues(t, 0, body["code"])

	_, body = c.do("POST", "/api/admin/backup/run", nil, tok)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"].(string), "在进行")
}

// doRawRequest 直接打引擎：下载返回的是二进制，testClient.do 会尝试 JSON 解析。
func doRawRequest(r *gin.Engine, tok, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
