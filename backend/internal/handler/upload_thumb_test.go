package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 直传后补缩略图的守门。
//
// 这条链路存在的理由：普通上传（multipart）的字节在服务端手上，ProcessUpload
// 顺手就把 _thumb 写出来了；但**预签名直传**是浏览器直接把字节 PUT 到对象存储，
// 服务端从头到尾没见过这份数据，于是直传出来的图没有 _thumb 兄弟对象。
// 端上所有列表（service-grid / achievement-grid / featured-projects）都用
// 「原 URL 末位扩展名前插 _thumb」派生地址，缺了它每次都要先吃一个 404 再回退。
//
// 最要紧的不是「能不能生成」，而是**归属校验**：key 来自客户端，不校验前缀的话
// A 租户可以上报 B 租户的键，让服务端读别人的图、再往别人目录里写文件。
// 所以下面每个用例都同时断言「文件有没有被写」，只判 code 会漏掉越权写。

// testPhotoPNG 造一张真实 PNG。600×900 是为了确保长边 > storage.ThumbMaxEdge(480)，
// 缩略图会真的被缩小——用小于 480 的图测，「生成了缩略图」与「原样复制原图」
// 两种实现都能过，测不出差别。
//
// 像素用**确定性伪随机**而不是渐变：渐变图 PNG 压缩率极高（600×900 只有几 KB），
// 量化后的 320×480 缩略图反而可能更大，于是走到 handler 里「不比原图小就复制原图」
// 的兜底分支，断言尺寸就测不出来了。噪声图接近真实照片，缩略图必然更小。
func testPhotoPNG(t *testing.T) []byte {
	t.Helper()
	const w, h = 600, 900
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(20261118)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			seed = seed*1664525 + 1013904223
			r := uint8(seed >> 24)
			seed = seed*1664525 + 1013904223
			g := uint8(seed >> 24)
			seed = seed*1664525 + 1013904223
			b := uint8(seed >> 24)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// thumbPaths 返回「原图路径 / 缩略图路径」两个绝对路径。
func thumbPaths(dir, key string) (string, string) {
	ext := filepath.Ext(key)
	thumbKey := key[:len(key)-len(ext)] + "_thumb" + ext
	return filepath.Join(dir, filepath.FromSlash(key)), filepath.Join(dir, filepath.FromSlash(thumbKey))
}

func writeObject(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, data, 0644))
}

func TestUploadThumbEnsure(t *testing.T) {
	r, db, cfg := setupMediaEngine(t)
	c := newTestClient(r, t)
	tok := loginTenantAdmin(t, c, db) // companyId=1 的企业管理员
	photo := testPhotoPNG(t)

	t.Run("租户自己的键：读回原图、生成 _thumb 写回同目录", func(t *testing.T) {
		key := "t/1/shot.png"
		full, thumb := thumbPaths(cfg.UploadDir, key)
		writeObject(t, full, photo)
		t.Cleanup(func() { _ = os.Remove(thumb) })

		code, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": key}, tok)
		require.Equal(t, 200, code)
		require.EqualValues(t, 0, body["code"], "补缩略图失败：%v", body["msg"])

		raw, err := os.ReadFile(thumb)
		require.NoError(t, err, "_thumb 没有落盘")
		require.NotEmpty(t, raw)
		// 真的缩小了：600×900 的长边缩到 480
		img, err := png.Decode(bytes.NewReader(raw))
		require.NoError(t, err, "_thumb 不是合法 PNG")
		b := img.Bounds()
		require.Equal(t, 480, b.Dy(), "_thumb 长边应为 480，实际 %dx%d", b.Dx(), b.Dy())
		require.Equal(t, 320, b.Dx(), "_thumb 宽应按比例，实际 %dx%d", b.Dx(), b.Dy())
	})

	t.Run("归属不符：拒绝处理别的租户的键，且一个字节都不写", func(t *testing.T) {
		// 企业管理员是 companyId=1，这里上报 t/2/（别人的目录）
		key := "t/2/other.png"
		full, thumb := thumbPaths(cfg.UploadDir, key)
		writeObject(t, full, photo)
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(cfg.UploadDir, "t", "2")) })

		code, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": key}, tok)
		require.Equal(t, 200, code)
		require.EqualValues(t, 403, body["code"], "跨租户的键必须 403")

		_, err := os.Stat(thumb)
		require.True(t, os.IsNotExist(err), "越权请求不得写出 _thumb")
	})

	t.Run("越权平台前缀：租户管理员不能借 p/ 写平台对象", func(t *testing.T) {
		key := "p/library/plat.png"
		full, thumb := thumbPaths(cfg.UploadDir, key)
		writeObject(t, full, photo)
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(cfg.UploadDir, "p")) })

		_, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": key}, tok)
		require.EqualValues(t, 403, body["code"])

		_, err := os.Stat(thumb)
		require.True(t, os.IsNotExist(err), "越权请求不得写出 _thumb")
	})

	t.Run("非 jpg/png 直接跳过：不算失败，也不写 _thumb", func(t *testing.T) {
		key := "t/1/clip.mp4"
		full, thumb := thumbPaths(cfg.UploadDir, key)
		writeObject(t, full, []byte("not a real video"))
		t.Cleanup(func() { _ = os.Remove(full) })

		_, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": key}, tok)
		require.EqualValues(t, 0, body["code"], "非图片不该报错：%v", body["msg"])
		data, _ := body["data"].(map[string]interface{})
		require.Equal(t, true, data["skipped"])

		_, err := os.Stat(thumb)
		require.True(t, os.IsNotExist(err), "非图片不该产生 _thumb")
	})

	t.Run("对象不存在：返回失败而不是凭空造一张缩略图", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": "t/1/ghost.png"}, tok)
		require.EqualValues(t, 1, body["code"])
		// 底层是 os.Open 的 *PathError，原样回给客户端等于把服务器目录结构报出去。
		msg, _ := body["msg"].(string)
		require.NotContains(t, msg, cfg.UploadDir, "错误信息不得泄露服务器绝对路径")
		require.NotContains(t, msg, "no such file", "错误信息不得直接回传底层 errno 文案")
	})

	t.Run("历史形态的键（/uploads/... 或裸键）拒绝：它们由 thumbbackfill 批量回填", func(t *testing.T) {
		for _, key := range []string{"/uploads/a.png", "a.png"} {
			_, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": key}, tok)
			require.EqualValues(t, 1, body["code"], "历史形态 %q 应被拒绝", key)
		}
	})

	t.Run("空 key / 非法请求体：参数错误而不是 500", func(t *testing.T) {
		_, body := c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": "  "}, tok)
		require.EqualValues(t, 1, body["code"])
		_, body = c.do("POST", "/api/admin/upload/thumb", map[string]interface{}{}, tok)
		require.EqualValues(t, 1, body["code"])
	})

	t.Run("未登录：被鉴权拦住", func(t *testing.T) {
		anon := newTestClient(r, t)
		_, body := anon.do("POST", "/api/admin/upload/thumb", map[string]interface{}{"key": "t/1/shot.png"}, "")
		require.NotEqualValues(t, 0, body["code"])
	})
}
