package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
)

// 上传图重编码尺寸上限（像素，长边）。
const (
	// FullMaxEdge 落盘原图的长边上限：手机照片常 3000-4000px，压到 2000px
	// 即可在大多数屏上无损观感，体积却降数倍（压缩的主要来源就是这一步缩放）。
	FullMaxEdge = 2000
	// ThumbMaxEdge 缩略图长边上限：列表/宫格用，远小于原图即可。
	ThumbMaxEdge = 480
	// full/thumb 的 JPEG 编码质量（0-100）。原图略高保留细节，缩略图可更狠。
	fullJPEGQuality = 85
	thumbJPEGQuality = 80
)

// ProcessImage 对上传图片做「隐私脱敏」：解码后重新编码，丢弃一切不透明元数据
// （EXIF / GPS 经纬度 / 相机型号 / 拍摄时间 / 缩略图等），仅保留像素本身。
// 不缩放（保持原分辨率），JPEG 质量固定 90。
//
// 返回语义与 ProcessUpload 一致：jpg/jpeg/png 重编码剥离元数据；其他类型
// （gif/webp/mp4/未知）原样透传。解码/编码失败按 fail-open：放弃处理、原样
// 透传该文件，而非阻断本次上传（理由见 ProcessUpload 注释）。
//
// 保留为导出的独立函数：既有单测 TestProcessImagePngRoundTrip 依赖其行为，
// 且可作为「只脱敏不缩放」的公共工具被复用。
func ProcessImage(ext string, data []byte) ([]byte, error) {
	return processResize(ext, data, 0, 90)
}

// ProcessUpload 上传图片处理：在 ProcessImage 的「隐私脱敏」基础上，额外按长边
// 限制重编码（压缩）并生成一张缩略图。返回：
//   - full：长边 ≤ FullMaxEdge 的重编码图（EXIF 已被剥离）；
//   - thumb：长边 ≤ ThumbMaxEdge 的缩略图；对 gif/webp/不支持类型返回 nil
//     （这类本就原样透传，不生成缩略图，列表页改用原图即可）。
//
// 关于 fail-open（与 ProcessImage 同口径，不可改为 fail-closed）：
//   1. 不引入上传回归——任何通过 checkFile 校验的文件仍照常上传，安全姿态不弱于
//      改造前（改造前本就不过滤元数据）；
//   2. 仅对极小比例「标准库无法解析的合法图片」放弃处理，绝大多数真实上传
//      （手机 JPEG、截图 PNG）都能被 stdlib 正常解码并完成压缩/缩略图。
//
// 这样上传头像/案例图时，用户手机相册里的 GPS 坐标不会随名片外泄（PIPL 最小化
// 收集），同时原图体积大幅下降、列表页用缩略图显著省流量。
func ProcessUpload(ext string, data []byte) (full, thumb []byte, err error) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "jpg", "jpeg", "png":
		full, e1 := processResize(ext, data, FullMaxEdge, fullJPEGQuality)
		if e1 != nil {
			return data, nil, nil // 压缩失败：原样透传，不阻断
		}
		thumb, e2 := processResize(ext, data, ThumbMaxEdge, thumbJPEGQuality)
		if e2 != nil {
			// 缩略图失败不影响主图：仅丢缩略图，列表页回退原图
			return full, nil, nil
		}
		return full, thumb, nil
	default:
		// 非目标图片类型：原样返回，不生成缩略图
		return data, nil, nil
	}
}

// processResize 解码 →（必要时缩放）→ 重编码。maxEdge≤0 表示不缩放（仅脱敏重编码）。
// fail-open：解码或编码失败一律返回原字节 + nil error，绝不阻断上传。
func processResize(ext string, data []byte, maxEdge, quality int) ([]byte, error) {
	img, decErr := decodeImage(ext, data)
	if decErr != nil {
		return data, nil
	}
	out, encErr := encodeImage(ext, resizeContain(img, maxEdge), quality)
	if encErr != nil {
		return data, nil
	}
	return out, nil
}

// decodeImage 仅解码 jpg/png；其余类型返回错误（调用方据此原样透传）。
func decodeImage(ext string, data []byte) (image.Image, error) {
	switch ext {
	case "jpg", "jpeg":
		return jpeg.Decode(bytes.NewReader(data))
	case "png":
		return png.Decode(bytes.NewReader(data))
	default:
		return nil, errUnsupportedImage{ext}
	}
}

// encodeImage 编码 jpg（按 quality）/ png（无损）；其余类型返回错误。
func encodeImage(ext string, img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch ext {
	case "jpg", "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	default:
		return nil, errUnsupportedImage{ext}
	}
	return buf.Bytes(), nil
}

// resizeContain 等比缩放使长边 ≤ maxEdge（contain，不裁剪）。
// maxEdge≤0 或原图已足够小则原样返回（避免无谓拷贝与重采样）。
//
// 缩放手写双线性采样，不依赖 golang.org/x/image（标准库 image/draw 不含
// CatmullRom 插值器），保持零额外依赖。对缩略图（480px 内）观感足够。
func resizeContain(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	longest := w
	if h > longest {
		longest = h
	}
	if maxEdge <= 0 || longest <= maxEdge {
		return img
	}
	scale := float64(maxEdge) / float64(longest)
	nw := int(float64(w)*scale + 0.5)
	nh := int(float64(h)*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	sx := float64(w) / float64(nw)
	sy := float64(h) / float64(nh)
	for y := 0; y < nh; y++ {
		syf := (float64(y)+0.5)*sy - 0.5
		y0 := int(syf)
		yf := syf - float64(y0)
		if y0 < 0 {
			y0, yf = 0, 0
		} else if y0 >= h-1 {
			y0, yf = h-1, 0
		}
		for x := 0; x < nw; x++ {
			sxf := (float64(x)+0.5)*sx - 0.5
			x0 := int(sxf)
			xf := sxf - float64(x0)
			if x0 < 0 {
				x0, xf = 0, 0
			} else if x0 >= w-1 {
				x0, xf = w-1, 0
			}
			c00 := toNRGBA(img.At(b.Min.X+x0, b.Min.Y+y0))
			c10 := toNRGBA(img.At(b.Min.X+x0+1, b.Min.Y+y0))
			c01 := toNRGBA(img.At(b.Min.X+x0, b.Min.Y+y0+1))
			c11 := toNRGBA(img.At(b.Min.X+x0+1, b.Min.Y+y0+1))
			dst.Set(x, y, bilinear(c00, c10, c01, c11, xf, yf))
		}
	}
	return dst
}

// toNRGBA 把任意 color.Color 归一到非预乘 NRGBA（缩略图混合用，兼容调色板/灰度图）。
func toNRGBA(c color.Color) color.NRGBA {
	return color.NRGBAModel.Convert(c).(color.NRGBA)
}

// bilinear 四角双线性插值（xf/yf 为 [0,1) 内的权重）。
func bilinear(c00, c10, c01, c11 color.NRGBA, xf, yf float64) color.NRGBA {
	lerp := func(v0, v1 uint8, t float64) uint8 {
		return uint8(float64(v0) + float64(v1-v0)*t)
	}
	rTop, gTop, bTop, aTop := lerp(c00.R, c10.R, xf), lerp(c00.G, c10.G, xf), lerp(c00.B, c10.B, xf), lerp(c00.A, c10.A, xf)
	rBot, gBot, bBot, aBot := lerp(c01.R, c11.R, xf), lerp(c01.G, c11.G, xf), lerp(c01.B, c11.B, xf), lerp(c01.A, c11.A, xf)
	return color.NRGBA{
		R: lerp(rTop, rBot, yf),
		G: lerp(gTop, gBot, yf),
		B: lerp(bTop, bBot, yf),
		A: lerp(aTop, aBot, yf),
	}
}

// errUnsupportedImage 让「不支持的类型」与「真的编解码错误」在内部区分，
// 但对外都按 fail-open 处理（返回原字节），仅用于 err 值语义，不影响上传结果。
type errUnsupportedImage struct{ ext string }

func (e errUnsupportedImage) Error() string { return "unsupported image ext: " + e.ext }
