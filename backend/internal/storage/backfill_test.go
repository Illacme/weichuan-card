package storage

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 素材统一用 photoLike（见 storage_test.go）：低频色块 + 高频噪声，压缩率接近真实照片。
// 纯色 / 棋盘格这类规整图原尺寸下压缩率极高，缩放后反而更大，会把"生成"路径
// 掩盖成"原样存"——上一版 5 条用例就是这么集体失效的。

func writeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, photoLike(w, h, int64(w*1000+h)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, photoLike(w, h, int64(w*1000+h))); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestBackfillThumbsGeneratesMissing(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "cover-a.jpg"), 1000, 800)
	writePNG(t, filepath.Join(dir, "logo-b.png"), 900, 900)

	res, err := BackfillThumbs(dir, BackfillOptions{})
	if err != nil {
		t.Fatalf("BackfillThumbs: %v", err)
	}
	if res.Scanned != 2 || res.Generated != 2 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("首次回填计数不符: %+v", res)
	}
	if res.BytesSaved <= 0 {
		t.Fatalf("真实照片类素材必须省下传输量，实际 %+v（省不出量说明缩放或编码有问题）", res)
	}
	// 缩略图按 <name>_thumb.<ext> 落盘，且长边被压到 ThumbMaxEdge
	for _, name := range []string{"cover-a_thumb.jpg", "logo-b_thumb.png"} {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("缩略图未生成 %s: %v", name, err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		cfg, _, err := image.DecodeConfig(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("decode config %s: %v", name, err)
		}
		longest := cfg.Width
		if cfg.Height > longest {
			longest = cfg.Height
		}
		if longest > ThumbMaxEdge {
			t.Fatalf("%s 长边 %d 超过 %d", name, longest, ThumbMaxEdge)
		}
		if len(b) == 0 {
			t.Fatalf("%s 内容为空", name)
		}
	}
}

func TestBackfillThumbsIdempotentSecondRun(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "a.jpg"), 1000, 800)

	first, err := BackfillThumbs(dir, BackfillOptions{})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Generated != 1 {
		t.Fatalf("首次应生成 1 张: %+v", first)
	}
	// 第二次：全部跳过，不重复生成（这就是「可随时多次执行」的依据）
	second, err := BackfillThumbs(dir, BackfillOptions{})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Generated != 0 || second.Skipped != 1 || second.Failed != 0 {
		t.Fatalf("二次执行应全部跳过: %+v", second)
	}
}

func TestBackfillThumbsDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "a.jpg"), 1000, 800)

	res, err := BackfillThumbs(dir, BackfillOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if res.Generated != 1 || res.Scanned != 1 {
		t.Fatalf("dry-run 应统计出 1 张待生成: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_thumb.jpg")); !os.IsNotExist(err) {
		t.Fatalf("dry-run 不应写盘，但 a_thumb.jpg 存在（err=%v）", err)
	}
}

func TestBackfillThumbsForceRegenerates(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "a.jpg"), 1000, 800)

	if _, err := BackfillThumbs(dir, BackfillOptions{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	res, err := BackfillThumbs(dir, BackfillOptions{Force: true})
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if res.Generated != 1 || res.Skipped != 0 {
		t.Fatalf("force 应重新生成: %+v", res)
	}
}

// 原图本就比缩略图规格小（≤ ThumbMaxEdge）或压缩率已到极限时，重编码只会更大。
// 此时直接存原图字节：体积虽没省，但 _thumb **存在**——端上不再先 404 再回退，
// 省掉的是那次失败请求（实测每次 0.3-0.5s），这才是回填对小图的主要收益。
func TestBackfillThumbsCopiesTinyOriginal(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "avatar.png")
	writePNG(t, src, 120, 120)

	res, err := BackfillThumbs(dir, BackfillOptions{})
	if err != nil {
		t.Fatalf("BackfillThumbs: %v", err)
	}
	if res.Scanned != 1 || res.Copied != 1 || res.Generated != 0 || res.Failed != 0 {
		t.Fatalf("小图应走原样存分支: %+v", res)
	}
	// 无论走哪条分支，_thumb 都必须存在——回填的核心承诺是"不再 404"
	thumbPath := filepath.Join(dir, "avatar_thumb.png")
	tb, err := os.ReadFile(thumbPath)
	if err != nil {
		t.Fatalf("小图也应产出缩略图文件: %v", err)
	}
	orig, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读原图: %v", err)
	}
	if !bytes.Equal(tb, orig) {
		t.Fatalf("原样存分支应写原图字节，实际缩略图 %d 字节 / 原图 %d 字节", len(tb), len(orig))
	}
	// 且绝不写出比原图更大的东西
	if len(tb) > len(orig) {
		t.Fatalf("缩略图 %d 字节 > 原图 %d 字节，回填反而变大了", len(tb), len(orig))
	}
}

func TestBackfillThumbsSkipsThumbItself(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "a.jpg"), 1000, 800)
	// 预置一张缩略图：它不能被当成原图再生成一次（否则会套娃出 _thumb_thumb）
	writeJPEG(t, filepath.Join(dir, "a_thumb.jpg"), 480, 384)

	res, err := BackfillThumbs(dir, BackfillOptions{Force: true})
	if err != nil {
		t.Fatalf("BackfillThumbs: %v", err)
	}
	if res.Scanned != 1 {
		t.Fatalf("缩略图自身不应计入候选: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_thumb_thumb.jpg")); !os.IsNotExist(err) {
		t.Fatalf("不应生成 a_thumb_thumb.jpg（err=%v）", err)
	}
}

func TestBackfillThumbsIgnoresNonImage(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "a.jpg"), 1000, 800)
	// gif/webp/mp4 后端本就不生成缩略图；普通文件同理
	if err := os.WriteFile(filepath.Join(dir, "b.gif"), []byte("GIF89a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := BackfillThumbs(dir, BackfillOptions{})
	if err != nil {
		t.Fatalf("BackfillThumbs: %v", err)
	}
	if res.Scanned != 1 || res.Generated != 1 || res.Failed != 0 {
		t.Fatalf("非位图应被忽略且不报错: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "b_thumb.gif")); !os.IsNotExist(err) {
		t.Fatalf("gif 不应生成缩略图（err=%v）", err)
	}
}

// 扩展名大小写：端上派生的是小写扩展名，回填必须写成小写，否则 cover.JPG 的
// 缩略图存成 cover_thumb.JPG 而端上找 cover_thumb.jpg → 又是一次 404。
func TestBackfillThumbsLowercasesExt(t *testing.T) {
	dir := t.TempDir()
	writeJPEG(t, filepath.Join(dir, "cover.JPG"), 1000, 800)

	if _, err := BackfillThumbs(dir, BackfillOptions{}); err != nil {
		t.Fatalf("BackfillThumbs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cover_thumb.jpg")); err != nil {
		t.Fatalf("应生成小写扩展名的 cover_thumb.jpg: %v", err)
	}
}

func TestThumbnailUnsupportedExt(t *testing.T) {
	if _, err := Thumbnail("gif", []byte("GIF89a")); err == nil {
		t.Fatal("gif 应返回错误（回填据此跳过，而不是把原字节当缩略图）")
	}
}
