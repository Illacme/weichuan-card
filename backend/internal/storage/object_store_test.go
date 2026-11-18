package storage

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 对象读写能力（ObjectStore）的守门。
//
// 这两个方法只服务「直传后补缩略图」：普通上传的字节在服务端手上，_thumb 顺手
// 就写了；直传是浏览器直接 PUT 到对象存储，服务端没见过数据，只能事后按 key
// 读回来重算。因此**两条性质必须成立**：
//   1) 不存在时错误要能按 fs.ErrNotExist 判定——调用方据此区分「图没传上来」
//      与「存储挂了」，两者的处置完全不同；
//   2) 读要有上限——key 来自客户端，不封顶时一个大对象能把进程内存吃穿
//      （解码后的 RGBA 通常是原文件的 10~30 倍）。

func newTempLocal(t *testing.T) *LocalUploader {
	t.Helper()
	dir, err := os.MkdirTemp("", "wc-objectstore-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return &LocalUploader{Dir: dir, AllowExt: DefaultAllowExt}
}

func TestLocalObjectStore(t *testing.T) {
	l := newTempLocal(t)

	t.Run("写入后能原样读回（含子目录）", func(t *testing.T) {
		key := "t/5/deep/a.png"
		require.NoError(t, l.PutObject(key, []byte("hello"), "image/png"))

		got, err := l.ReadObject(key)
		require.NoError(t, err)
		require.Equal(t, []byte("hello"), got)
		require.FileExists(t, filepath.Join(l.Dir, filepath.FromSlash(key)),
			"PutObject 必须自动创建中间目录")
	})

	t.Run("同 key 覆盖写", func(t *testing.T) {
		key := "t/5/a.png"
		require.NoError(t, l.PutObject(key, []byte("v1"), "image/png"))
		require.NoError(t, l.PutObject(key, []byte("v2"), "image/png"))

		got, err := l.ReadObject(key)
		require.NoError(t, err)
		require.Equal(t, []byte("v2"), got)
	})

	t.Run("不存在：错误满足 fs.ErrNotExist", func(t *testing.T) {
		_, err := l.ReadObject("t/5/nope.png")
		require.Error(t, err)
		require.True(t, errors.Is(err, fs.ErrNotExist), "调用方靠这个区分「没传上来」与「存储故障」")
	})

	t.Run("路径穿越与空键一律拒绝", func(t *testing.T) {
		for _, key := range []string{"", "../etc/passwd", "t/5/../../x.png", "/abs.png"} {
			_, rerr := l.ReadObject(key)
			require.Error(t, rerr, "ReadObject 应拒绝 %q", key)
			require.Error(t, l.PutObject(key, []byte("x"), "image/png"), "PutObject 应拒绝 %q", key)
		}
	})

	t.Run("超过上限：拒绝而不是悄悄截断", func(t *testing.T) {
		key := "t/5/huge.png"
		// 截断是最坏的实现：解码器拿到半张图可能「成功」解出一张残缺的缩略图，
		// 端上看到的就是一条被切掉的图，比直接失败更难查。
		require.NoError(t, l.PutObject(key, make([]byte, MaxObjectReadBytes+1), "image/png"))
		_, err := l.ReadObject(key)
		require.ErrorIs(t, err, ErrObjectTooLarge)
	})
}

// TestReadLimited 钉住 readLimited 的两个边界。
func TestReadLimited(t *testing.T) {
	t.Run("刚好等于上限：通过", func(t *testing.T) {
		got, err := readLimited(bytes.NewReader(make([]byte, MaxObjectReadBytes)))
		require.NoError(t, err)
		require.Len(t, got, MaxObjectReadBytes)
	})

	t.Run("超出一个字节：拒绝", func(t *testing.T) {
		_, err := readLimited(bytes.NewReader(make([]byte, MaxObjectReadBytes+1)))
		require.ErrorIs(t, err, ErrObjectTooLarge)
	})

	t.Run("小对象原样返回", func(t *testing.T) {
		got, err := readLimited(bytes.NewReader([]byte("abc")))
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), got)
	})
}
