package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 本文件钉住 2026-11-13 加的「缩略图 PNG 调色板量化」。
//
// 老实现的问题不是「缩得不够小」，而是**根本没编码优化**：resizeContain 在
// 原图 ≤ ThumbMaxEdge 时原样返回，encodeImage 又对 PNG 走无损 png.Encode，
// 于是 256×256 的图标被原封不动重存一份，_thumb 和原图体积一字不差。
// 真正的收益来自「真彩 → ≤256 色索引」，不是少画像素——所以这些用例
// 都断言**尺寸不变**，谁把尺寸缩下去反而应该红。

// seedIcon 取演示素材里那张真实的 256×256 分类图标。
// 它同时满足：≤ ThumbMaxEdge（不会被 resizeContain 缩）、带透明与抗锯齿、
// 颜色数远超 256（必须量化才省得下来）。
func seedIcon(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "demoseed", "seedassets", "cat-1.png"))
	if err != nil {
		t.Fatalf("读演示图标失败（素材目录被挪走会让守门失效）: %v", err)
	}
	return data
}

// writeFlatPNG 写一整张纯色不透明 PNG：颜色数=1，量化后的调色板封装反而更大，
// 用来覆盖「绝不写出比原图更大的缩略图」这条兜底。
func writeFlatPNG(t *testing.T, path string, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{32, 96, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return buf.Bytes()
}

// 真实图标：体积必须显著下降，且**尺寸一点都不能缩**。
func TestThumbPNGQuantizesWithoutResizing(t *testing.T) {
	data := seedIcon(t)
	thumb, err := Thumbnail("png", data)
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if len(thumb) >= len(data)/2 {
		t.Fatalf("256×256 图标量化后应至少省一半：原图 %d → 缩略图 %d（%d%%）",
			len(data), len(thumb), len(thumb)*100/len(data))
	}
	orig, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("原图解码: %v", err)
	}
	out, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("缩略图不是合法 PNG: %v", err)
	}
	if out.Bounds() != orig.Bounds() {
		t.Fatalf("量化不该改尺寸：原图 %v，缩略图 %v", orig.Bounds(), out.Bounds())
	}
	pal, ok := out.(*image.Paletted)
	if !ok {
		t.Fatalf("缩略图必须是调色板 PNG（否则量化没生效），实际 %T", out)
	}
	if len(pal.Palette) > thumbPaletteColors {
		t.Fatalf("调色板 %d 色超过上限 %d", len(pal.Palette), thumbPaletteColors)
	}
	// 透明背景必须还在：角点在原图里是全透明的
	_, _, _, oa := orig.At(0, 0).RGBA()
	if oa == 0 {
		_, _, _, ta := out.At(0, 0).RGBA()
		if ta != 0 {
			t.Fatalf("量化把透明背景弄脏了：原角点 alpha=0，缩略图 alpha=%d", ta>>8)
		}
	}
	// 颜色保真：逐像素平均通道误差要小（目视对比的量化版）
	// 只抽样，避免把测试拖慢；阈值放宽到 24（256 级里约 9%）。
	var sum, n int
	for y := 0; y < orig.Bounds().Dy(); y += 4 {
		for x := 0; x < orig.Bounds().Dx(); x += 4 {
			a := color.NRGBAModel.Convert(orig.At(x, y)).(color.NRGBA)
			b := color.NRGBAModel.Convert(out.At(x, y)).(color.NRGBA)
			sum += absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) +
				absInt(int(a.B)-int(b.B)) + absInt(int(a.A)-int(b.A))
			n += 4
		}
	}
	if avg := sum / n; avg > 24 {
		t.Fatalf("量化后平均通道误差 %d 过大（调色板算法退化？）", avg)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// 上传链路与回填链路必须**逐字节一致**：这是「改了规格要跑 -force 回填」的根据。
// 两条链路一旦漂移，同一张图在「新上传」和「存量回填」后会得到不同体积的 _thumb，
// 而页面上完全看不出来，只会表现为「有的图省流量有的不省」。
func TestProcessUploadAndThumbnailAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		ext  string
		data []byte
	}{
		{"png-真实图标", "png", seedIcon(t)},
		{"jpg-照片类", "jpg", func() []byte {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, photoLike(1000, 800, 12345), &jpeg.Options{Quality: 90}); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, upThumb, err := ProcessUpload(tc.ext, tc.data)
			if err != nil {
				t.Fatalf("ProcessUpload: %v", err)
			}
			backThumb, err := Thumbnail(tc.ext, tc.data)
			if err != nil {
				t.Fatalf("Thumbnail: %v", err)
			}
			if !bytes.Equal(upThumb, backThumb) {
				t.Fatalf("上传 %d 字节 / 回填 %d 字节，两条链路不一致", len(upThumb), len(backThumb))
			}
		})
	}
}

// 量化反而变大时，上传路径必须回退成「直接复制落盘原图」，绝不能写出更大的 _thumb。
// 回填路径已有同一条兜底（backfill_test.go），这条专守上传路径。
func TestProcessUploadNeverWritesBiggerThumb(t *testing.T) {
	dir := t.TempDir()
	data := writeFlatPNG(t, filepath.Join(dir, "flat.png"), 8, 8)
	full, thumb, err := ProcessUpload("png", data)
	if err != nil {
		t.Fatalf("ProcessUpload: %v", err)
	}
	if thumb == nil {
		t.Fatal("缩略图不该消失：它存在的意义是让端上不再先 404 再回退")
	}
	if len(thumb) > len(full) {
		t.Fatalf("缩略图 %d 字节 > 落盘原图 %d 字节，兜底没生效", len(thumb), len(full))
	}
	if len(thumb) == len(full) && !bytes.Equal(thumb, full) {
		t.Fatal("等长时应直接复制 full")
	}
}

// JPEG 路径不能被量化波及：它本来就在正常压缩（480px + q80，实测省约 50%）。
func TestJPEGThumbPathUnchanged(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photoLike(1000, 800, 6789), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	thumb, err := Thumbnail("jpg", data)
	if err != nil {
		t.Fatalf("Thumbnail jpg: %v", err)
	}
	out, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("缩略图不是合法 JPEG: %v", err)
	}
	if maxEdgeOf(out.Bounds()) > ThumbMaxEdge {
		t.Fatalf("JPEG 缩略图长边 %d 超过 %d", maxEdgeOf(out.Bounds()), ThumbMaxEdge)
	}
	if len(thumb) >= len(data) {
		t.Fatalf("JPEG 缩略图应比原图小：%d → %d", len(data), len(thumb))
	}
}

// 量化器的边界：空图不能 panic，也不能伪造一张「0×0 的缩略图」出去。
// 真实链路里 decodeImage 会先挡掉它，这里只钉住「别 panic、别静默产出垃圾」。
func TestEncodeThumbPNGEmptyImage(t *testing.T) {
	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	if _, err := encodeThumbPNG(empty); err == nil {
		t.Fatal("0×0 空图不是合法 PNG，应返回错误而不是产出伪缩略图")
	}
}

// 全量收益守门：演示素材里 16 张 PNG（分类图标 / 服务竖卡 / LOGO / 头像 / 相册）
// 逐张走真实的 Thumbnail，合计必须显著下降。这条防的是「量化器悄悄退化成无损编码」
// ——那种退化不会让任何功能报错，只会让流量悄悄涨回去（典型静默失败）。
func TestThumbPNGSavesAcrossSeedAssets(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "demoseed", "seedassets", "*.png"))
	if err != nil || len(files) == 0 {
		t.Fatalf("找不到演示素材: %v (%d 张)", err, len(files))
	}
	var before, after int64
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		thumb, err := Thumbnail("png", data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		// 单张也绝不许变大（backfill 有兜底，这里守的是生成器本身）
		if len(thumb) > len(data) {
			t.Fatalf("%s 的缩略图反而变大：%d → %d", filepath.Base(f), len(data), len(thumb))
		}
		before += int64(len(data))
		after += int64(len(thumb))
	}
	t.Logf("演示素材 %d 张 PNG：%.1f KB → %.1f KB（%.1f%%）",
		len(files), float64(before)/1024, float64(after)/1024, float64(after)/float64(before)*100)
	if after*100 > before*40 {
		t.Fatalf("量化收益退化：%.1f KB → %.1f KB（%.1f%%），应 ≤40%%",
			float64(before)/1024, float64(after)/1024, float64(after)/float64(before)*100)
	}
}

// 量化必须**确定性**：同一张图连续生成多次要逐字节一致。
// 曾因 histogram 直接用 map 遍历（Go 迭代顺序随机）导致两次差 31 字节：
// 视觉无差，但 -force 回填会反复改写文件、让 CDN 缓存无谓失效。
func TestQuantizePNGDeterministic(t *testing.T) {
	data := seedIcon(t)
	first, err := Thumbnail("png", data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := Thumbnail("png", data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("第 %d 次量化与第一次不一致（%d vs %d 字节）", i+2, len(first), len(again))
		}
	}
}
