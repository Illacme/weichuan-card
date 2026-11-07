package handler

import (
	"errors"
	"testing"

	"weichuan/internal/audit"
	"weichuan/internal/model"

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
