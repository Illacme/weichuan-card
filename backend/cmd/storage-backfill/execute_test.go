package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/refmigrate"
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
	_, err := localStores("", "/tmp/x")
	if err == nil {
		t.Error("缺 --source-dir 必须报错")
	}
	_, err = localStores(t.TempDir(), "")
	if err == nil {
		t.Error("缺 --target-dir 必须报错")
	}
	_, err = localStores(filepath.Join(t.TempDir(), "nope"), "/tmp/x")
	if err == nil {
		t.Error("源目录不存在必须报错，不许当成空桶静默跳过")
	}
}

func TestLocalStoresUsesTheGivenRoots(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	stores, err := localStores(src, dst)
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
