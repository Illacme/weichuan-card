package storage

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeFileHeader 用给定文件名与内容构造一个 *multipart.FileHeader，供 checkFile 校验。
// 复用与集成测试一致的 ParseMultipartForm 路径，避免直接 ReadForm 的边界问题。
func makeFileHeader(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, _ = part.Write(content)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	require.NoError(t, req.ParseMultipartForm(int64(len(content))+1024))
	return req.MultipartForm.File["file"][0]
}

func TestCheckFileSizeAndExt(t *testing.T) {
	allow := DefaultAllowExt

	t.Run("大小超限", func(t *testing.T) {
		fh := makeFileHeader(t, "a.png", []byte("\x89PNG\r\n\x1a\n"))
		require.Error(t, checkFile(fh, 1, allow)) // 上限 1 字节
	})

	t.Run("扩展名不在白名单", func(t *testing.T) {
		fh := makeFileHeader(t, "a.exe", []byte("MZ"))
		require.Error(t, checkFile(fh, 10<<20, allow))
	})

	t.Run("合法 PNG 通过大小/扩展名校验", func(t *testing.T) {
		fh := makeFileHeader(t, "a.png", []byte("\x89PNG\r\n\x1a\nsomedata"))
		require.NoError(t, checkFile(fh, 10<<20, allow))
	})
}

// TestSniffExt 直接对文件头字节做内容嗅探，避免 multipart 短内容解析的边界问题。
func TestSniffExt(t *testing.T) {
	cases := []struct {
		ext   string
		head  []byte
		match bool
	}{
		{"png", []byte("\x89PNG\r\n\x1a\nsomedata"), true},
		{"png", []byte("this is definitely not a png"), false},
		{"jpg", []byte("\xff\xd8\xff\x00some"), true},
		{"jpeg", []byte("\xff\xd8\xff"), true},
		{"gif", []byte("GIF89a"), true},
		{"gif", []byte("AGIFxx"), false},
		{"webp", []byte("RIFF\x1a\x00\x00\x00WEBP"), true},
		{"webp", []byte("RIFF\x1a\x00\x00\x00JPEG"), false},
		{"mp4", []byte("\x00\x00\x00\x1cftypmp42"), true},
		{"mp4", []byte("not an mp4 file at all"), false},
		// 无嗅探规则的扩展名一律放行（仅依赖白名单）
		{"xyz", []byte("anything"), true},
	}
	for _, c := range cases {
		require.Equalf(t, c.match, sniffExt(c.ext, c.head), "sniffExt(%q)", c.ext)
	}
}

func TestRandName(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		name, err := RandName("png")
		require.NoError(t, err)
		require.True(t, len(name) > 4 && name[len(name)-4:] == ".png")
		require.False(t, seen[name], "RandName 出现重复: %s", name)
		seen[name] = true
	}
	// 无扩展名时不应带点
	noExt, err := RandName("")
	require.NoError(t, err)
	require.NotContains(t, noExt, ".")
}

// TestRandNameEntropyFailure 守门「熵源读取失败必须报错，而不是产出全零名字」。
// 从前写成 `_, _ = rand.Read(b)`：失败时 b 保持全零，于是每一次上传都叫同一个名字，
// 后上传的静默覆盖先上传的——日志里干干净净，现场只表现为「图片莫名变成别人的」。
func TestRandNameEntropyFailure(t *testing.T) {
	orig := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("entropy exhausted") }
	defer func() { randRead = orig }()

	name, err := RandName("png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "生成随机文件名失败")
	require.Empty(t, name)
}

// tinyImage 构造一个 2x2 的纯色图，供 ProcessImage 的编码/解码往返测试。
func tinyImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{10, 20, 30, 255})
	img.Set(1, 1, color.RGBA{200, 100, 50, 255})
	return img
}

// realPNG / realJPG 用标准库编码出一段真实可解码的图片字节，供 Save 路径的
// 「上传即剥离」集成测试使用（避免用假文件头骗过 checkFile 却骗不过 ProcessImage 解码）。
func realPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, tinyImage())
	return b.Bytes()
}

func realJPG() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, tinyImage(), &jpeg.Options{Quality: 90})
	return b.Bytes()
}

// jpgWithExif 在合法 JPEG 的 SOI 之后注入一段 APP1/Exif 标记，模拟「手机相册里带
// GPS 坐标的原图」。供 Save 集成测试验证：上传到存储的字节确实已剥离 EXIF。
func jpgWithExif() []byte {
	base := realJPG()
	exif := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	return append([]byte{0xFF, 0xD8}, append(exif, base[2:]...)...)
}

// TestProcessImageStripsExif 守门「上传图片必须剥离 EXIF/GPS」。
// 在一段合法 JPEG 的 SOI（FF D8）之后注入一个 APP1/Exif 段，验证：
//   - 输入含 "Exif\0\0" 标记；
//   - ProcessImage 输出不再含该标记（整段 EXIF 连同 GPS 被丢弃）；
//   - 输出仍是可解码的合法 JPEG，且尺寸与原文一致（像素无损，仅丢元数据）。
// 反向验证：若把 ProcessImage 改回「jpeg 原样透传」，本用例会 FAIL，证明它不是空跑。
func TestProcessImageStripsExif(t *testing.T) {
	var base bytes.Buffer
	require.NoError(t, jpeg.Encode(&base, tinyImage(), &jpeg.Options{Quality: 90}))
	require.True(t, bytes.HasPrefix(base.Bytes(), []byte{0xFF, 0xD8}), "base 应为合法 JPEG(SOI)")

	// 注入 APP1/Exif 段：FF E1 <len> "Exif\0\0" + 6 字节伪 GPS 数据
	exif := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	injected := append([]byte{0xFF, 0xD8}, append(exif, base.Bytes()[2:]...)...)
	require.Contains(t, string(injected), "Exif\x00\x00", "前置条件：注入的 JPEG 应含 Exif 标记")

	out, err := ProcessImage("jpg", injected)
	require.NoError(t, err)
	require.NotContains(t, string(out), "Exif\x00\x00", "输出必须已剥离 EXIF/GPS 标记")
	// 输出仍可解码且尺寸不变
	dec, err := jpeg.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 2), dec.Bounds())
}

// TestProcessImagePngRoundTrip 验证 PNG 解码重编码后仍为合法可解码 PNG。
func TestProcessImagePngRoundTrip(t *testing.T) {
	// 用 realPNG() 而不是就地再编一次：同一个 helper 也服务 Save 路径的
	// 集成用例，两处共用一份「真实可解码的图片」才不会各造各的假数据。
	out, err := ProcessImage("png", realPNG())
	require.NoError(t, err)
	dec, err := png.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 2), dec.Bounds())
}

// TestProcessImagePassesThroughNonImage 验证 gif/webp/mp4 等非目标类型原样透传，
// 不引入破坏性改动（gif 尤其不能重新编码，否则丢动画帧）。
func TestProcessImagePassesThroughNonImage(t *testing.T) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff\x21\xf9\x04\x00\x00\x00\x00\x00\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00\x3b")
	out, err := ProcessImage("gif", gif)
	require.NoError(t, err)
	require.Equal(t, gif, out, "gif 应原样透传")

	mp4 := []byte("\x00\x00\x00\x1cftypmp42mp42\x00\x00\x00\x00")
	out, err = ProcessImage("mp4", mp4)
	require.NoError(t, err)
	require.Equal(t, mp4, out, "mp4 应原样透传")
}

// TestProcessImagePassthroughOnDecodeError 守门「解码失败的图片不能阻断上传」。
// 文件头是合法 JPEG（能通过 checkFile 嗅探），但体损坏无法解码：ProcessImage 必须
// fail-open 原样透传，而不是让本次上传失败——这样安全姿态不弱于改造前。
func TestProcessImagePassthroughOnDecodeError(t *testing.T) {
	broken := append([]byte{0xFF, 0xD8, 0xFF}, []byte("not a real jpeg body")...)
	out, err := ProcessImage("jpg", broken)
	require.NoError(t, err)
	require.Equal(t, broken, out, "解码失败时原样透传，不阻断合法上传")
}
