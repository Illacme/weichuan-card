package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// 注意：这里生成的是**全空图**（像素全零）。用来验证尺寸/编码路径没问题，
// 但像素值恒定的图**测不出插值类 bug**（见 TestResizeContainKeepsGradient）——
// 任何涉及像素内容正确性的断言都必须另造带明暗变化的图。
func makeImageBytes(t *testing.T, w, h int, ext string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	switch ext {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
	case "jpg", "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func maxEdgeOf(b image.Rectangle) int {
	w, h := b.Dx(), b.Dy()
	if w > h {
		return w
	}
	return h
}

// 大图上传：原图应被压到 FullMaxEdge 以内，并额外得到一张 ≤ ThumbMaxEdge 的缩略图。
func TestProcessUploadResizesAndThumbnails(t *testing.T) {
	data := makeImageBytes(t, 4000, 3000, "png")
	full, thumb, err := ProcessUpload("png", data)
	if err != nil {
		t.Fatal(err)
	}
	if full == nil {
		t.Fatal("full 不应为 nil")
	}
	if thumb == nil {
		t.Fatal("缩略图不应为 nil")
	}
	if bytes.Equal(full, data) {
		t.Fatal("full 应被重编码（与原始不同）")
	}
	fi, err := png.Decode(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("full 解码失败: %v", err)
	}
	if maxEdgeOf(fi.Bounds()) > FullMaxEdge {
		t.Fatalf("full 长边 %d 超过上限 %d", maxEdgeOf(fi.Bounds()), FullMaxEdge)
	}
	ti, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("thumb 解码失败: %v", err)
	}
	if maxEdgeOf(ti.Bounds()) > ThumbMaxEdge {
		t.Fatalf("thumb 长边 %d 超过上限 %d", maxEdgeOf(ti.Bounds()), ThumbMaxEdge)
	}
	if len(thumb) >= len(full) {
		t.Fatal("缩略图应明显小于原图")
	}
}

// JPEG 同样应压缩 + 出缩略图，且解码后为合法 JPEG。
func TestProcessUploadJpeg(t *testing.T) {
	data := makeImageBytes(t, 3000, 2000, "jpg")
	full, thumb, err := ProcessUpload("jpg", data)
	if err != nil {
		t.Fatal(err)
	}
	if full == nil || thumb == nil {
		t.Fatal("jpg 应得到 full 与 thumb")
	}
	if _, err := jpeg.Decode(bytes.NewReader(thumb)); err != nil {
		t.Fatalf("thumb 不是合法 JPEG: %v", err)
	}
}

// 非图片类型（如 mp4）应原样透传，且不生成缩略图。
func TestProcessUploadNonImagePassthrough(t *testing.T) {
	raw := []byte("not an image at all")
	full, thumb, err := ProcessUpload("mp4", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full, raw) {
		t.Fatal("非图片应原样透传")
	}
	if thumb != nil {
		t.Fatal("非图片不应生成缩略图")
	}
}

// 缩放插值必须「先转 float 再相减」。
//
// 曾经写成 float64(v1-v0)：那是 **uint8 相减**，v1<v0 时按 256 回绕
// （如 100-200 → 156），插值结果变成噪声像素——同一张图同尺寸同质量下，
// 双线性缩放的输出体积比最近邻大 2.3 倍，且肉眼可见噪点/花屏。
//
// 这条用例用「渐变图」钉住它：makeImageBytes 生成的是全空图（像素全零），
// 像素值恒定，v1-v0 永远为 0，回绕根本触发不到——所以用空图写的缩放测试
// 是测不出这个 bug 的，必须有真实的明暗变化。
func TestResizeContainKeepsGradient(t *testing.T) {
	// 递增与递减**两个方向都要测**：uint8 回绕只在 v1<v0（递减）时发作，
	// 只测递增会漏掉这个 bug（反向验证时实测过：单测递增时 bug 版本照样 PASS）。
	for _, descending := range []bool{false, true} {
		t.Run(map[bool]string{false: "递增", true: "递减"}[descending], func(t *testing.T) {
			const w, h = 64, 8
			src := image.NewGray(image.Rect(0, 0, w, h))
			for x := 0; x < w; x++ {
				v := uint8(x * 255 / (w - 1))
				if descending {
					v = 255 - v
				}
				for y := 0; y < h; y++ {
					src.SetGray(x, y, color.Gray{Y: v})
				}
			}

			out := resizeContain(src, w/2)
			if got := out.Bounds().Dx(); got != w/2 {
				t.Fatalf("缩放后宽度应为 %d，实为 %d", w/2, got)
			}

			// 渐变缩放后仍应单调（方向由 descending 决定），且相邻步长接近理想值（约 8）
			y := out.Bounds().Min.Y
			prev := -1
			for x := 0; x < out.Bounds().Dx(); x++ {
				g := int(color.GrayModel.Convert(out.At(out.Bounds().Min.X+x, y)).(color.Gray).Y)
				if prev >= 0 {
					if descending && g > prev {
						t.Fatalf("递减渐变被破坏（插值回绕成噪声）：x=%d 灰度 %d > 前一个 %d", x, g, prev)
					}
					if !descending && g < prev {
						t.Fatalf("递增渐变被破坏（插值回绕成噪声）：x=%d 灰度 %d < 前一个 %d", x, g, prev)
					}
					step := g - prev
					if step < 0 {
						step = -step
					}
					if step > 24 {
						t.Fatalf("渐变跳变过大：x=%d 灰度 %d，前一个 %d", x, g, prev)
					}
				}
				prev = g
			}
		})
	}
}
