package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/pii"
	"weichuan/internal/refmigrate"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExitForMapsNeedsHumanToTwo(t *testing.T) {
	// 执行器的「数据需人工处理」与「命令挂了」必须是两个退出码：
	// 前者让人去看数据，后者让人去重跑命令。
	if got := exitFor(fmt.Errorf("%w：%v", refmigrate.ErrStalePlan, errors.New("库值变了"))); got != 2 {
		t.Errorf("过期计划应返回 2，实际 %d", got)
	}
	if got := exitFor(refmigrate.ErrPlanHasProblems); got != 2 {
		t.Errorf("含异常条目的计划应返回 2，实际 %d", got)
	}
	if got := exitFor(fmt.Errorf("%w", refmigrate.ErrObjectConflict)); got != 2 {
		t.Errorf("对象冲突应返回 2，实际 %d", got)
	}
	if got := exitFor(errors.New("连不上库")); got != 1 {
		t.Errorf("真错误应返回 1，实际 %d", got)
	}
}

func TestLocalStoresRefusesMissingDirs(t *testing.T) {
	// 「目标没配就回退到平台盘」是最危险的便利：对象会复制到本地新键下、
	// 库改成 t/<cid>/...，后台显示搬迁成功，真把流量切到客户桶才发现一个都没有。
	_, err := sourceFor("local", "", nil)
	if err == nil {
		t.Error("缺 --source-dir 必须报错")
	}
	_, err = sourceFor("local", filepath.Join(t.TempDir(), "nope"), nil)
	if err == nil {
		t.Error("源目录不存在必须报错，不许当成空桶静默跳过")
	}
	src := t.TempDir()
	_, err = localStores(mustSource(t, "local", src, nil), "")
	if err == nil {
		t.Error("缺 --target-dir 必须报错")
	}
}

// mustSource 直接走唯一的源选择口，避免测试自己搭一条绕过分派的路径。
func mustSource(t *testing.T, store, dir string, db *gorm.DB) refmigrate.ObjectStore {
	t.Helper()
	src, err := sourceFor(store, dir, db)
	require.NoError(t, err)
	return src
}

func TestLocalStoresUsesTheGivenRoots(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	stores, err := localStores(mustSource(t, "local", src, nil), dst)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stores.Source.Stat("missing.jpg")
	if err != nil || info.Exists {
		t.Errorf("源存储应指向给定目录且能正常判空，实际 info=%+v err=%v", info, err)
	}
	target, err := stores.TargetFor(7)
	if err != nil {
		t.Fatal(err)
	}
	ls, ok := target.(*refmigrate.LocalStore)
	if !ok {
		t.Fatalf("目标存储类型不对：%T", target)
	}
	absDst, _ := filepath.Abs(dst)
	if ls.Dir != absDst {
		t.Errorf("目标根目录应是 %q，实际 %q", absDst, ls.Dir)
	}
}

// testPiiKey 显式装一把真密钥。tenant 模式拒绝内置 dev 密钥，
// 且 pii 是包级全局状态，必须在用例结束后还原，否则会污染同包其它测试。
func testPiiKey(t *testing.T) {
	t.Helper()
	t.Setenv(pii.KeyEnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'k'}, 32)))
	require.NoError(t, pii.KeyError())
	pii.Reload()
	t.Cleanup(pii.Reload)
	require.False(t, pii.UsingPlaceholderKey())
}

func testEmptyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cli.db")),
		&gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	return db
}

func TestBuildStoresDefaultsToLocal(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	stores, err := buildStores("local", "local", src, filepath.Join(root, "dst"), nil)
	require.NoError(t, err)
	target, err := stores.TargetFor(7)
	require.NoError(t, err)
	require.IsType(t, &refmigrate.LocalStore{}, target,
		"默认必须还是第六十五轮的本地盘行为，一句话不改也能跑")
	require.IsType(t, &refmigrate.LocalStore{}, stores.Source,
		"默认源也必须是本地盘")
}

func TestBuildStoresRejectsUnknownTargetStore(t *testing.T) {
	_, err := buildStores("local", "s3", t.TempDir(), t.TempDir(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "未知 --target-store")
}

func TestBuildStoresRejectsUnknownSourceStore(t *testing.T) {
	_, err := buildStores("oss", "local", t.TempDir(), t.TempDir(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "未知 --source-store")
}

func TestBuildStoresTenantRejectsTargetDir(t *testing.T) {
	testPiiKey(t)
	_, err := buildStores("local", "tenant", t.TempDir(), t.TempDir(), testEmptyDB(t))
	require.Error(t, err, "两个目标同时出现时无法判断哪个生效，必须拒绝")
	require.Contains(t, err.Error(), "--target-dir")
}

func TestBuildStoresTenantRequiresSourceDir(t *testing.T) {
	testPiiKey(t)
	_, err := buildStores("local", "tenant", "", "", testEmptyDB(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source-dir")
}

func TestBuildStoresTenantRefusesPlaceholderKey(t *testing.T) {
	t.Setenv(pii.KeyEnvVar, "")
	pii.Reload()
	t.Cleanup(pii.Reload)
	require.True(t, pii.UsingPlaceholderKey())
	_, err := buildStores("local", "tenant", t.TempDir(), "", testEmptyDB(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), pii.KeyEnvVar)
	require.Contains(t, err.Error(), "dev 密钥")
}

func TestBuildStoresTenantNeverFallsBackToLocalDisk(t *testing.T) {
	testPiiKey(t)
	db := testEmptyDB(t) // 故意不建 tenant_storage_configs 表
	stores, err := buildStores("local", "tenant", t.TempDir(), "", db)
	require.NoError(t, err)
	target, err := stores.TargetFor(7)
	require.Error(t, err, "读不到配置时必须整批停下")
	require.Contains(t, err.Error(), "不回退平台盘")
	require.Nil(t, target, "绝不返回本地盘实例顶上")
}

// runApply 必须走 buildStores 这个唯一分派口，且源 / 目标两个参数都要真的传进去。
// 绕过去直接调 localStores，就等于把 --source-store=s3 / --target-store=tenant
// 变成「参数收下了但不生效」的开关。
func TestRunApplyDispatchesThroughBuildStores(t *testing.T) {
	b, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, "stores, err := buildStores(sourceStore, targetStore, srcDir, dstDir, db)")
	require.NotContains(t, s, "stores, err := localStores(", "runApply 不该直接调 localStores")
	require.Contains(t, s, "fs.StringVar(&sourceStore, \"source-store\", \"local\"",
		"必须真的注册 --source-store 参数，不能只在 buildStores 里收着")
}
