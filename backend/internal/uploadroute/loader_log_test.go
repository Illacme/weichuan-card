package uploadroute

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"weichuan/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// errCollectingLogger 只收 gorm 在 Trace 里报出来的 error。
// 默认 logger 会把 ErrRecordNotFound 也当错误打一行，我们要验的正是这件事。
type errCollectingLogger struct {
	mu   sync.Mutex
	errs []error
}

func (l *errCollectingLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (l *errCollectingLogger) Info(context.Context, string, ...any)     {}
func (l *errCollectingLogger) Warn(context.Context, string, ...any)     {}
func (l *errCollectingLogger) Error(context.Context, string, ...any)    {}
func (l *errCollectingLogger) Trace(_ context.Context, _ time.Time, _ func() (string, int64), err error) {
	if err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

// TestDBLoaderMissingRowDoesNotLogAsError 钉住第六十六轮真桶灰度里抓到的日志噪音。
//
// 「这家没配自有桶」是 Loader 三态里正常的一态：每个走平台盘的租户每次上传都命中。
// 用 First 查会让 gorm 打一条 `record not found`，运维看到会以为配置读挂了，
// 而实际一切正常。改用 Limit(1).Find + RowsAffected 后，正常路径不再产生错误。
// 反向验证：把 Find 改回 First，这条用例必红。
func TestDBLoaderMissingRowDoesNotLogAsError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "loader.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))

	rec := &errCollectingLogger{}
	cfg, err := DBLoader(db.Session(&gorm.Session{Logger: rec}), "https://card.example.com/uploads")(7)
	require.NoError(t, err)
	require.Nil(t, cfg, "没配 = 明确走平台，不是错误")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Empty(t, rec.errs, "缺配置行不该产生任何 gorm error 日志：%v", rec.errs)
}
