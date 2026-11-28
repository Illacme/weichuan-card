package setting

import (
	"os"
	"strconv"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestBumpPostgres TEXT 列原子递增在 PG 上的方言兼容。
//
// 回归场景（2026-11-28）：Bump 用 `gorm.Expr("value + 1")` 对 sys_setting.value
// （TEXT 列）做 SQL 层 +1。MySQL / SQLite 会隐式转换，PG 严格类型直接报
// 42883 operator does not exist: text + integer——「立即刷新缓存」在 PG 上
// 永远失败。这个 bug 不会被 SQLite parity 抓到，必须有真实 PG 守门。
//
// 有 TEST_POSTGRES_DSN 才跑；CI 的 postgres service 容器会注入。
func TestBumpPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run PostgreSQL Bump parity")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect PG: %v", err)
	}
	// 独立 schema，避免与 handler 的 parity 测试互相干扰。
	for _, stmt := range []string{
		"DROP SCHEMA IF EXISTS setting_bump_test CASCADE",
		"CREATE SCHEMA setting_bump_test",
		"SET search_path TO setting_bump_test",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	gmu.RLock()
	prev := global
	gmu.RUnlock()
	defer func() {
		gmu.Lock()
		global = prev
		gmu.Unlock()
	}()

	// Init 会建表 + seed 缺省值（public_cache_version=1）+ reload 内存缓存。
	if err := Init(db); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for want := int64(2); want <= 4; want++ {
		got, err := Bump(KeyPublicCacheVersion)
		if err != nil {
			t.Fatalf("Bump 第 %d 次: %v", want, err)
		}
		if got != want {
			t.Errorf("Bump 第 %d 次 = %d，want %d", want, got, want)
		}
	}
	// 直接查库确认 SQL 层真的写了 TEXT 值，不是只改内存缓存。
	var row Setting
	if err := db.Where(map[string]interface{}{"key": KeyPublicCacheVersion}).First(&row).Error; err != nil {
		t.Fatalf("回读: %v", err)
	}
	n, err := strconv.ParseInt(row.Value, 10, 64)
	if err != nil || n != 4 {
		t.Errorf("库里值 = %q（%d），want 4", row.Value, n)
	}
}
