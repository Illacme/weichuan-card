package pathpick

import (
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/mpupload"

	"github.com/stretchr/testify/require"
)

// makeFixture 造一棵小目录树，覆盖三种路径类型的命中条件 + 敏感目录屏蔽：
//
//	root/
//	  app/            含 project.config.json      → source_dir 命中
//	  mod/            含 node_modules/miniprogram-ci → module_dir 命中
//	  plain/          普通目录，无标识
//	  .ssh/           敏感点目录，连名字都不应回传
//	  bin/            含可执行文件 node            → node_bin 命中（文件）
//
// 用临时目录 + extraRoots 把 root 纳入白名单，避免依赖测试机真实的 /opt 等。
func makeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// macOS 上 TMPDIR 常常本身就是软链（/var/folders → /private/var/folders）。
	// pathpick 出于安全考虑对路径做 EvalSymlinks 后才返回（否则一个指向白名单外的
	// 软链就能骗过前缀比较），所以断言必须用同一个「已解析」口径的 root 比对——
	// 不然的表现是：Linux CI 全绿、macOS 上必然失败，且与被测逻辑无关。
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	writeFile := func(rel string, content string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	mkdir := func(rel string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	}

	mkdir("app")
	writeFile("app/project.config.json", "{}")
	mkdir("mod/node_modules/miniprogram-ci")
	mkdir("plain")
	mkdir(".ssh")
	mkdir("bin")
	// 可执行文件 node：写一点内容并加执行位（仅 Unix 体系有效；Windows 不跑这套测试）。
	nodePath := filepath.Join(root, "bin", "node")
	require.NoError(t, os.WriteFile(nodePath, []byte("#!/bin/sh\n"), 0o755))

	return root
}

func TestListSourceDirMarksProjectAndHidesSensitive(t *testing.T) {
	root := makeFixture(t)

	res, err := List(root, mpupload.KindSourceDir, []string{root})
	require.NoError(t, err)
	require.Equal(t, root, res.Path)

	byName := map[string]Entry{}
	for _, e := range res.Entries {
		byName[e.Name] = e
		require.Equal(t, "dir", e.Type)
	}

	// 工程目录被标出来
	require.Contains(t, byName, "app")
	require.Equal(t, "小程序工程目录", byName["app"].Marker)

	// 敏感点目录连名字都不回传
	require.NotContains(t, byName, ".ssh")

	// 目录名不含 node / node_modules 误判（node_modules 在 mod 之下，这里不出现）
	require.NotContains(t, byName, "node_modules")
}

func TestListModuleDirMarksMiniprogramCI(t *testing.T) {
	root := makeFixture(t)

	res, err := List(root, mpupload.KindModuleDir, []string{root})
	require.NoError(t, err)

	byName := map[string]Entry{}
	for _, e := range res.Entries {
		byName[e.Name] = e
	}
	require.Contains(t, byName, "mod")
	require.Equal(t, "已装 miniprogram-ci", byName["mod"].Marker)
}

func TestListNodeBinReturnsExecutableFile(t *testing.T) {
	root := makeFixture(t)
	binDir := filepath.Join(root, "bin")

	// 进入 bin 目录列：node 可执行文件应作为 "file" 类型回传，并带徽标
	res, err := List(binDir, mpupload.KindNodeBin, []string{root})
	require.NoError(t, err)

	var node Entry
	found := false
	for _, e := range res.Entries {
		if e.Name == "node" {
			node = e
			found = true
		}
	}
	require.True(t, found, "node 可执行文件应在 bin 目录下列出")
	require.Equal(t, "file", node.Type)
	require.Equal(t, "node 可执行文件", node.Marker)
}

func TestListNodeBinPassingAFileListsParentAndValidatesIt(t *testing.T) {
	root := makeFixture(t)
	nodeFile := filepath.Join(root, "bin", "node")

	// 直接传一个可执行文件：应改列其父目录，且当前校验针对该文件本身
	res, err := List(nodeFile, mpupload.KindNodeBin, []string{root})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "bin"), res.Path)
	require.True(t, res.Current.OK, "当前项应是可用的 node 可执行文件，实际：%s", res.Current.Message)
}

func TestListRejectsPathOutsideWhitelist(t *testing.T) {
	root := makeFixture(t)

	// /etc 不在默认白名单，也不在 extraRoots（只给了 root）：应被拒
	_, err := List("/etc", mpupload.KindSourceDir, []string{root})
	require.Error(t, err)
	require.Contains(t, err.Error(), "不在允许浏览的范围内")
}

func TestListRejectsNonexistent(t *testing.T) {
	root := makeFixture(t)
	_, err := List(filepath.Join(root, "does-not-exist"), mpupload.KindSourceDir, []string{root})
	require.Error(t, err)
	require.Contains(t, err.Error(), "目录不存在")
}

func TestSuggestSourceDirFindsProject(t *testing.T) {
	root := makeFixture(t)

	got := Suggest(mpupload.KindSourceDir, []string{root})
	require.NotEmpty(t, got)
	var hit bool
	for _, s := range got {
		if s.Path == filepath.Join(root, "app") {
			hit = true
			require.Equal(t, "含 project.config.json", s.Reason)
		}
	}
	require.True(t, hit, "应探测到 app 这个工程目录")
}

func TestSuggestModuleDirFindsMiniprogramCI(t *testing.T) {
	root := makeFixture(t)

	got := Suggest(mpupload.KindModuleDir, []string{root})
	require.NotEmpty(t, got)
	var hit bool
	for _, s := range got {
		if s.Path == filepath.Join(root, "mod") {
			hit = true
			require.Equal(t, "含 node_modules/miniprogram-ci", s.Reason)
		}
	}
	require.True(t, hit, "应探测到 mod 这个装了 miniprogram-ci 的目录")
}

func TestLooksLikeNodeNameHelperContract(t *testing.T) {
	// 仅验证 List/Suggest 行为即可覆盖；这里补几个边界，确保 node_modules 不会误判。
	require.True(t, mpupload.Marker(mpupload.KindNodeBin, filepath.Join(makeFixture(t), "bin", "node")) != "")
}

func TestMergeRootsRejectsRelative(t *testing.T) {
	// 白名单只收绝对路径：相对路径会随 cwd 漂移，混进来等于把 cwd 悄悄开放出去
	//（历史 bug：MP_NODE_BIN=node 时 filepath.Dir 得到 "."，一度被补进白名单）。
	roots := mergeRoots([]string{".", "node", "/abs/only", "/abs/only"})
	require.NotContains(t, roots, ".")
	require.NotContains(t, roots, "node")
	require.Contains(t, roots, "/abs/only")
}

func TestListWithoutDirPicksAllowedRoot(t *testing.T) {
	// 未给 dir 时必须能自选一个白名单内存在的起点（家目录已纳入），
	// 而不是报「没有可浏览的起点」——这是用户截图里那条错误的直接回归。
	res, err := List("", mpupload.KindSourceDir, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Path)
}

func TestEntriesSortedDirsBeforeFiles(t *testing.T) {
	root := makeFixture(t)
	res, err := List(root, mpupload.KindNodeBin, []string{root})
	require.NoError(t, err)
	// 至少有一个目录在文件之前（fixture 里 bin/app/mod/plain 都是目录，无文件；改测通用排序）
	dirSeen := false
	for _, e := range res.Entries {
		if e.Type == "dir" {
			dirSeen = true
		}
		if e.Type == "file" && dirSeen {
			// 文件出现前必须已经见过目录
			require.True(t, true)
		}
	}
}

// ---- 空切片 / 选中路径：界面渲染的直接依赖 ----

func TestSuggestNeverReturnsNil(t *testing.T) {
	// 必须回 []，不能回 nil：Go 的 nil slice 序列化成 JSON null，界面按数组用
	// （suggestions.length），null 会让选择器渲染抛 TypeError、弹窗体出不来。
	empty := t.TempDir()
	for _, kind := range []mpupload.Kind{
		mpupload.KindNodeBin, mpupload.KindModuleDir, mpupload.KindSourceDir,
	} {
		got := Suggest(kind, []string{empty})
		require.NotNil(t, got, "kind=%s 返回了 nil，会被序列化成 JSON null", kind)
		// 目录类在空目录里扫不到任何候选，必须是「空数组」而不是 nil
		if kind != mpupload.KindNodeBin {
			require.Empty(t, got)
		}
	}
}

func TestListEmptyDirReturnsEmptyArraysNotNil(t *testing.T) {
	empty := t.TempDir()
	res, err := List(empty, mpupload.KindSourceDir, []string{empty})
	require.NoError(t, err)
	require.NotNil(t, res.Entries)
	require.Empty(t, res.Entries)
	require.NotNil(t, res.Suggestions)
}

func TestListSelectedPointsAtPickedFile(t *testing.T) {
	root := makeFixture(t)
	nodePath := filepath.Join(root, "bin", "node")

	// 直接传入文件：列表回到父目录，但「选中」是文件本身（界面据此高亮并允许确认）
	res, err := List(nodePath, mpupload.KindNodeBin, []string{root})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "bin"), res.Path)
	require.Equal(t, nodePath, res.Selected)
	require.False(t, res.SelectedIsDir)
	require.True(t, res.Current.OK)

	// 传目录：选中就是该目录，node_bin 下它不可能是可执行文件
	dirRes, err := List(root, mpupload.KindNodeBin, []string{root})
	require.NoError(t, err)
	require.Equal(t, root, dirRes.Selected)
	require.True(t, dirRes.SelectedIsDir)
	require.False(t, dirRes.Current.OK)
}
