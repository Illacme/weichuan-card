package mpupload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// tempExecutable 造一个带执行位的临时文件（仅类 Unix 有效；Windows 不跑这套单测）。
func tempExecutable(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755))
	return p
}

func TestCheckPathNodeBin(t *testing.T) {
	bin := tempExecutable(t)

	ok, msg := CheckPath(KindNodeBin, bin)
	require.True(t, ok, "真实存在的可执行文件应通过，实际：%s", msg)
	require.Contains(t, msg, "node 可执行文件可用")

	ok, msg = CheckPath(KindNodeBin, filepath.Join(t.TempDir(), "nope"))
	require.False(t, ok)
	require.Contains(t, msg, "找不到 node 可执行文件")
}

func TestCheckPathModuleDir(t *testing.T) {
	// 空串：历史文案必须逐字节相同（界面/文档可能依赖）
	ok, msg := CheckPath(KindModuleDir, "")
	require.False(t, ok)
	require.Equal(t, "未配置 miniprogram-ci 安装目录（MP_CI_MODULE_DIR）", msg)

	// 目录存在但没有 node_modules/miniprogram-ci
	empty := t.TempDir()
	ok, msg = CheckPath(KindModuleDir, empty)
	require.False(t, ok)
	require.Contains(t, msg, "下没有 node_modules/miniprogram-ci")

	// 装好的目录
	good := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(good, "node_modules", "miniprogram-ci"), 0o755))
	ok, msg = CheckPath(KindModuleDir, good)
	require.True(t, ok, "含 node_modules/miniprogram-ci 应通过，实际：%s", msg)
	require.Contains(t, msg, "已有 node_modules/miniprogram-ci")
}

func TestCheckPathSourceDir(t *testing.T) {
	// 空串
	ok, msg := CheckPath(KindSourceDir, "")
	require.False(t, ok)
	require.Equal(t, "未配置小程序源码目录（MP_SOURCE_DIR）", msg)

	// 不存在
	ok, msg = CheckPath(KindSourceDir, filepath.Join(t.TempDir(), "gone"))
	require.False(t, ok)
	require.Contains(t, msg, "小程序源码目录不存在")

	// 存在但不是工程目录（缺 project.config.json）
	notProj := t.TempDir()
	ok, msg = CheckPath(KindSourceDir, notProj)
	require.False(t, ok)
	require.Contains(t, msg, "下没有 project.config.json")

	// 真正的工程目录
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "project.config.json"), []byte("{}"), 0o644))
	ok, msg = CheckPath(KindSourceDir, proj)
	require.True(t, ok, "含 project.config.json 应通过，实际：%s", msg)
	require.Contains(t, msg, "是一个小程序工程目录")
}

func TestMarker(t *testing.T) {
	// source_dir
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "project.config.json"), []byte("{}"), 0o644))
	require.Equal(t, "小程序工程目录", Marker(KindSourceDir, proj))
	require.Equal(t, "", Marker(KindSourceDir, t.TempDir()))

	// module_dir
	mod := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mod, "node_modules", "miniprogram-ci"), 0o755))
	require.Equal(t, "已装 miniprogram-ci", Marker(KindModuleDir, mod))
	require.Equal(t, "", Marker(KindModuleDir, t.TempDir()))

	// node_bin
	bin := tempExecutable(t)
	require.Equal(t, "node 可执行文件", Marker(KindNodeBin, bin))
	require.Equal(t, "", Marker(KindNodeBin, t.TempDir()))
}

func TestLooksLikeNodeName(t *testing.T) {
	require.True(t, looksLikeNodeName("node"))
	require.True(t, looksLikeNodeName("node20"))
	require.True(t, looksLikeNodeName("node-18"))
	require.True(t, looksLikeNodeName("node_16"))

	// 绝不能把 node_modules / nodectl / nodejs 当 node 可执行文件
	require.False(t, looksLikeNodeName("node_modules"))
	require.False(t, looksLikeNodeName("nodectl"))
	require.False(t, looksLikeNodeName("nodejs"))
	require.False(t, looksLikeNodeName("node.v18")) // 分隔符后跟字母，不是版本号
	require.False(t, looksLikeNodeName("n"))
	require.False(t, looksLikeNodeName(""))
}
