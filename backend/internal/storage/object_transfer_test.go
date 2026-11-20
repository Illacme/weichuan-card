package storage

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// 一键迁移依赖的三个新能力：列举 / 流式写 / 流式复制。
// 用本地驱动做行为验证（三个云驱动共用同一套接口契约，真实桶由 tools 验证）。
func TestLocalObjectTransfer(t *testing.T) {
	src := newTempLocal(t)
	dst := newTempLocal(t)

	require.NoError(t, src.PutObjectStream("t/5/a.jpg", bytes.NewReader([]byte("alpha")), 5, "image/jpeg"))
	require.NoError(t, src.PutObjectStream("t/5/sub/b.png", bytes.NewReader([]byte("bravo")), 5, "image/png"))
	require.NoError(t, src.PutObjectStream("p/library/c.png", bytes.NewReader([]byte("charlie")), 7, "image/png"))

	t.Run("按前缀列举，只返回对象键", func(t *testing.T) {
		var keys []string
		require.NoError(t, src.ListObjects("t/5/", func(key string, size int64) error {
			keys = append(keys, key)
			require.Positive(t, size)
			return nil
		}))
		require.ElementsMatch(t, []string{"t/5/a.jpg", "t/5/sub/b.png"}, keys)
	})

	t.Run("不存在的目录是空桶，不报错", func(t *testing.T) {
		called := false
		require.NoError(t, src.ListObjects("t/999/", func(string, int64) error {
			called = true
			return nil
		}))
		require.False(t, called)
	})

	t.Run("回调错误原样透传（取消迁移靠它）", func(t *testing.T) {
		boom := stopTransferErr
		err := src.ListObjects("t/5/", func(string, int64) error { return boom })
		require.ErrorIs(t, err, boom)
	})

	t.Run("流式复制保留完整字节与键", func(t *testing.T) {
		var srcKey string
		require.NoError(t, src.ListObjects("t/5/sub/", func(key string, _ int64) error { srcKey = key; return nil }))
		require.NotEmpty(t, srcKey)
		require.NoError(t, src.CopyObjectTo(dst, srcKey))
		got, err := dst.ReadObject(srcKey)
		require.NoError(t, err)
		require.Equal(t, []byte("bravo"), got)
	})

	t.Run("写入不完整要报错，不能留下半截对象当成功", func(t *testing.T) {
		err := dst.PutObjectStream("t/5/bad.png", bytes.NewReader([]byte("x")), 9, "image/png")
		require.Error(t, err)
		require.Contains(t, err.Error(), "写入不完整")
	})
}

var stopTransferErr = &stopError{}

type stopError struct{}

func (*stopError) Error() string { return "stop" }
