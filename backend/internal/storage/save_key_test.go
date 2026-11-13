package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 写入侧接线（第五十四轮）：Save 按调用方给的带归属前缀 key 落盘，
// 返回相对 /uploads/<key>；缩略图与主图同前缀。
// 这条锁住「对象实际键 == 库里引用键」——两者不一致时页面会 404，且没有任何报错。
func TestLocalSaveScopedKey(t *testing.T) {
	dir := t.TempDir()
	up := &LocalUploader{Dir: dir, MaxBytes: 10 << 20, AllowExt: DefaultAllowExt}

	// 租户对象：t/<cid>/...
	url, err := up.Save(makeFileHeader(t, "a.jpg", jpgWithExif()), "t/5/abc.jpg")
	require.NoError(t, err)
	require.Equal(t, "/uploads/t/5/abc.jpg", url, "本地驱动返回相对 /uploads/<key>")
	require.FileExists(t, filepath.Join(dir, "t", "5", "abc.jpg"), "对象必须落在带前缀的 key 下")
	require.FileExists(t, filepath.Join(dir, "t", "5", "abc_thumb.jpg"), "缩略图必须与主图同前缀")

	// 平台对象：p/...
	urlP, err := up.Save(makeFileHeader(t, "b.jpg", jpgWithExif()), "p/library/x.jpg")
	require.NoError(t, err)
	require.Equal(t, "/uploads/p/library/x.jpg", urlP)
	require.FileExists(t, filepath.Join(dir, "p", "library", "x.jpg"))
	require.FileExists(t, filepath.Join(dir, "p", "library", "x_thumb.jpg"))
}

// 非法对象键必须在落盘之前被拒绝：路径穿越 / 绝对路径 / 反斜杠 / 空值。
// 否则 filepath.Join(Dir, "../x") 会把文件写到 Dir 之外，且不会有任何报错。
func TestLocalSaveRejectsUnsafeKey(t *testing.T) {
	dir := t.TempDir()
	up := &LocalUploader{Dir: dir, MaxBytes: 10 << 20, AllowExt: DefaultAllowExt}

	for _, bad := range []string{"", "../evil.jpg", "/abs.jpg", "t\\5\\x.jpg", "t/5/../../x.jpg"} {
		_, err := up.Save(makeFileHeader(t, "x.jpg", jpgWithExif()), bad)
		require.Error(t, err, "非法对象键必须被拒绝：%q", bad)
	}
	entries, rerr := os.ReadDir(dir)
	require.NoError(t, rerr)
	require.Empty(t, entries, "非法 key 不得落盘任何文件")
}
