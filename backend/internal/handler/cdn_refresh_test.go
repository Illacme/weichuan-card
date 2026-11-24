package handler

import (
	"errors"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/model"
	"weichuan/internal/storage"

	"github.com/stretchr/testify/require"
)

// TestRefreshCDNRecordsAuditOnFailure 验证 CDN 刷新失败时记一条审计（失败可观测），
// 且审计 Detail 只含 url_count —— 不写错误原文或具体 URL，防连接串 / 签名 token 泄露。
func TestRefreshCDNRecordsAuditOnFailure(t *testing.T) {
	db := statPurgeDB(t)
	ref := &fakeRefresher{err: errors.New("cdn provider http 500")}
	refreshCDN(db, ref, "https://cdn.example.com/uploads/a.png", "https://cdn.example.com/uploads/b.png")

	var rows []model.AuditLog
	db.Where("action = ?", audit.ActionCDNRefresh).Find(&rows)
	require.Len(t, rows, 1)
	require.Equal(t, audit.ResultFail, rows[0].Result)
	require.Equal(t, "cdn", rows[0].TargetType)
	require.Equal(t, audit.ActorSystem, rows[0].ActorType)
	require.Equal(t, "系统", rows[0].ActorName) // 系统动作展示名固定为「系统」
	// Detail 只含 url_count，不泄露具体 URL 或错误原文
	require.Contains(t, rows[0].Detail, `"url_count":2`)
	require.NotContains(t, rows[0].Detail, "cdn.example.com")
	require.NotContains(t, rows[0].Detail, "500")
}

// TestRefreshCDNSkipsPrivateBucketAudit 私有桶对象是设计上的「不刷新」，不是故障。
// 自动刷新（覆盖写后）拿到哨兵错误必须静默：记 WARN + 失败审计会让每个用私有桶的
// 企业每次上传都造一条告警，把真故障淹掉。管理员确实需要知道时，走的是手动刷新
// 端点——那里把 err.Error() 直接回显，不受这里静默影响。
func TestRefreshCDNSkipsPrivateBucketAudit(t *testing.T) {
	db := statPurgeDB(t)
	ref := &fakeRefresher{err: storage.ErrPrivateBucketCDN}
	refreshCDN(db, ref, "https://cdn.example.com/uploads/t/7/a.png")

	var rows []model.AuditLog
	db.Where("action = ?", audit.ActionCDNRefresh).Find(&rows)
	require.Empty(t, rows, "私有桶不刷新是预期边界，不应写失败审计")
}

// TestRefreshCDNSkipsWhenRefresherNil refresher 为 nil 或 URL 为空时既不该刷也不该记审计。
func TestRefreshCDNSkipsWhenRefresherNil(t *testing.T) {
	db := statPurgeDB(t)
	refreshCDN(db, nil) // 早期返回
	var c int64
	db.Model(&model.AuditLog{}).Count(&c)
	require.Equal(t, int64(0), c)
}

// TestRefreshCDNSkipsWhenNoURL URL 为空时同样早期返回，不记审计。
func TestRefreshCDNSkipsWhenNoURL(t *testing.T) {
	db := statPurgeDB(t)
	ref := &fakeRefresher{}
	refreshCDN(db, ref) // 无 URL
	require.Equal(t, 0, ref.calls)
	var c int64
	db.Model(&model.AuditLog{}).Count(&c)
	require.Equal(t, int64(0), c)
}
