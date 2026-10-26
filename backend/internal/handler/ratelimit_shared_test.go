package handler

// 登录限流的跨副本共享：两个 engine 共用同一个库时，阈值必须是**总共** 10 次，
// 而不是每个副本各 10 次。这正是本次改造要保证的那件事。

import (
	"os"
	"testing"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/repository"
	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newSharedRateEngine 建一个独立 engine（模拟一个副本），与给定 db 共用存储。
// 与 setupSQLiteEngine 的差别只在于 db 由调用方给——多副本要共用同一个库。
func newSharedRateEngine(t *testing.T, db *gorm.DB) *gin.Engine {
	t.Helper()
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
	return buildEngine(t, db, cfg, &storage.LocalUploader{
		Dir: uploadDir, BaseURL: cfg.UploadBaseURL, MaxBytes: 10 << 20,
	}, nil)
}

// freezeRateClock 固定限流窗口的时间源。
//
// 为什么必须固定：窗口按墙上时钟的整分钟对齐（repository.windowStart），
// 这十几次请求只要跨过一次分钟边界，共享计数就翻页重置，「第 11 次必须被拒」
// 会变成放行——表现为并发跑全量包时偶发失败，且与被测逻辑无关
// （实测抓到过：expected 429, actual 1）。固定时钟后窗口不再自己翻页。
func freezeRateClock(t *testing.T) {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)
	restore := repository.SetNowFunc(func() time.Time { return base })
	t.Cleanup(restore)
}

// 计数必须真的落到表里——否则中间件挂了个假 counter，测试全绿而线上什么都没发生。
func TestLoginRateLimitWritesSharedCounter(t *testing.T) {
	freezeRateClock(t)
	db := openSharedRateDB(t)
	r := newSharedRateEngine(t, db)
	c := newTestClient(r, t)

	for i := 1; i <= 2; i++ {
		c.do("POST", "/api/admin/login", map[string]interface{}{
			"username": "admin", "password": "definitely-wrong",
		}, "")

		// 不把具体 IP 写进断言：httptest 的默认对端地址是实现细节，
		// 换一套测试基建就会变。这里断言的是「计数确实落表且在累加」。
		hits, rows := sharedRateTotals(t, db)
		require.Equal(t, i, hits, "第 %d 次登录尝试后，共享计数应等于 %d", i, i)
		require.Equal(t, 1, rows, "同一来源应只落一行：分裂成多行会让计数偏小、限流被绕开")
	}
}

// 核心用例：两个副本共用一库，总阈值仍为 10 次/分，而不是 20 次。
//
// 改造前每个副本各有一份进程内桶，副本 A 打 6 次 + 副本 B 打 5 次都能成功，
// 攻击者只要把请求摊到不同副本上就能把尝试次数翻倍——而线上看起来毫无异常。
func TestLoginRateLimitSharedAcrossReplicas(t *testing.T) {
	freezeRateClock(t)
	db := openSharedRateDB(t)
	replicaA := newTestClient(newSharedRateEngine(t, db), t)
	replicaB := newTestClient(newSharedRateEngine(t, db), t)

	// 副本 A 打 6 次（都在限流内）
	for i := 0; i < 6; i++ {
		_, body := replicaA.do("POST", "/api/admin/login", map[string]interface{}{
			"username": "admin", "password": "definitely-wrong",
		}, "")
		require.EqualValues(t, 1, body["code"], "前 6 次应放行")
	}

	// 副本 B 再打 4 次：累计到 10，仍应放行
	for i := 0; i < 4; i++ {
		_, body := replicaB.do("POST", "/api/admin/login", map[string]interface{}{
			"username": "admin", "password": "definitely-wrong",
		}, "")
		require.EqualValues(t, 1, body["code"], "累计到 10 次都应放行")
	}

	// 第 11 次，走另一个副本：必须被拒。改造前这里会成功（副本 B 自己只记了 5 次）
	_, body := replicaB.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "admin", "password": "definitely-wrong",
	}, "")
	require.EqualValues(t, 429, body["code"], "总阈值 10 次与副本数无关：第 11 次必须被拒")
}

// 打开一个供多副本共用的临时库。model.All() 已包含 RateLimitHit，
// 所以这里不需要额外 AutoMigrate。
func openSharedRateDB(t *testing.T) *gorm.DB {
	t.Helper()
	tmp, err := os.CreateTemp("", "weichuan-shared-rate-*.db")
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	t.Cleanup(func() { os.Remove(tmp.Name()) })

	db, err := gorm.Open(sqlite.Open(tmp.Name()), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	// 1 号租户：登录限流之外的接口不依赖它，但 buildEngine 里的 seed 与设置需要
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "测试租户", Status: "enabled"}).Error)
	return db
}

// sharedRateTotals 返回当前表里的最大 hits 与行数。
// 用 MAX 而不是按 key 查，是为了不把「httptest 默认对端地址」这种实现细节钉进断言。
func sharedRateTotals(t *testing.T, db *gorm.DB) (maxHits, rows int) {
	t.Helper()
	var hits, cnt int64
	require.NoError(t, db.Model(&model.RateLimitHit{}).
		Select("COALESCE(MAX(hits), 0)").Scan(&hits).Error)
	require.NoError(t, db.Model(&model.RateLimitHit{}).Count(&cnt).Error)
	return int(hits), int(cnt)
}
