package testsupport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/storage"
)

// TestStaticResolverIgnoresCompanyID 钉住桩的核心契约：不管问哪个租户，
// 拿到的都是同一个实例。
//
// 它测的是**桩本身**，不是产品行为——生产实现
// （`storageprofile.Resolver`）的按企业分叉由 `internal/storageprofile` 的
// 测试覆盖。这里存在的意义是：任何把桩当成「省事实现」塞进生产装配的改动，
// 都会先让本文件的语义变得可疑，从而被 review 抓住。
func TestStaticResolverIgnoresCompanyID(t *testing.T) {
	up := &storage.LocalUploader{Dir: t.TempDir()}
	res := NewStaticResolver(up, "minio")

	for _, cid := range []int64{-1, 0, 1, 42, 9999} {
		got, err := res.For(cid)
		require.NoError(t, err, "cid=%d 不应报错", cid)
		require.Same(t, up, got, "cid=%d 必须拿到同一个实例", cid)
		require.Same(t, up, res.Platform())
		require.Equal(t, "minio", res.DriverFor(cid))
	}
}

// TestStaticResolverNilUploaderReportsError 钉住「不返回 (nil, nil)」：
// 调用方拿 nil 去 Save 会 panic，而 panic 在 gin 里变成 500 纯文本，
// 用户看到的是"点了没反应"，排查时也拿不到一句可操作的话。
func TestStaticResolverNilUploaderReportsError(t *testing.T) {
	res := NewStaticResolver(nil, "local")

	up, err := res.For(1)
	require.ErrorIs(t, err, storage.ErrNoUploader)
	require.Nil(t, up, "报错时不得同时给出一个可用的 nil 实例")
	require.Nil(t, res.Platform())
}

// TestStaticResolverEmptyDriverMeansLocal 钉住空驱动的口径：
// capabilityHandler 原实现是 `driver == "" → "local"`，这里必须与它一致，
// 否则「存储能力探测」会在驱动名留空的老部署上报出一个不存在的驱动名。
func TestStaticResolverEmptyDriverMeansLocal(t *testing.T) {
	res := NewStaticResolver(&storage.LocalUploader{Dir: t.TempDir()}, "")
	require.Equal(t, "local", res.DriverFor(7))
}
