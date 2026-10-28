package storage

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

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
