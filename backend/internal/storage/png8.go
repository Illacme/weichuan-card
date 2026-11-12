package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sort"
)

// thumbPaletteColors 缩略图 PNG 量化的最大颜色数（含透明色）。
//
// 取 256 是因为 PNG 调色板最多 256 项：每像素从「4 通道 NRGBA」变成「1 字节索引」，
// 这就是体积下降的主要来源。再多就要用 cbTC8 真彩编码，反而没有收益。
const thumbPaletteColors = 256

// quantizePNG 把任意 image.Image 量化成 ≤ maxColors 色的调色板图（保留 alpha）。
//
// 为什么必须自己实现而不是换编码参数：PNG 的 png.Encode 只会按「源图是什么模型」
// 决定色深 —— 传 *image.RGBA / *image.NRGBA 时一律走真彩（每像素 3~4 字节）。
// 只有传 *image.Paletted 才会写出 PLTE + 8-bit 索引。而 Go 标准库没有任何颜色量化函数，
// 所以「把真彩图变成调色板图」这一步只能自己做。
//
// 算法：中位切分（median cut）。
//  1. 先按 5bit/通道把像素聚进直方图（最多 2^20 桶，实际图标通常几百到几千桶），
//     避免逐像素参与排序；
//  2. 反复挑「颜色跨度 × 桶数」最大的盒子，沿跨度最大的通道排序后按像素数加权
//     取中位切开，直到盒子数达到上限或切不动；
//  3. 每个盒子的加权平均色作为调色板一项；
//  4. 每个桶映射到最近的代表色，逐像素写 ColorIndex。
//
// 为什么不用「固定 256 色均匀量化」（RGB 3/3/2 位截断）：那种调色板对蓝白系图标
// 会把渐变压出明显色带；中位切分按实际颜色分布分配调色板项，实测三张代表性图
// （分类图标 / 服务竖卡 / LOGO）与原件肉眼几乎无差。
//
// 为什么保留 alpha 参与切分：图标普遍带透明背景与抗锯齿半透明边缘，
// 只按 RGB 切会把「同色不同透明度」混在一起，边缘出现脏色。
func quantizePNG(img image.Image, maxColors int) (*image.Paletted, error) {
	if maxColors < 2 || maxColors > thumbPaletteColors {
		maxColors = thumbPaletteColors
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errEmptyImage{}
	}

	// ---- 1. 5bit/通道直方图 ----
	hist := make(map[uint32]*colorBucket)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			key := bucketKey(c.R, c.G, c.B, c.A)
			e := hist[key]
			if e == nil {
				e = &colorBucket{key: key}
				hist[key] = e
			}
			e.count++
			e.rSum += int64(c.R)
			e.gSum += int64(c.G)
			e.bSum += int64(c.B)
			e.aSum += int64(c.A)
		}
	}
	// ⚠️ 必须按 key 排序后再切分，不能直接用 `for _, e := range hist`：
	// Go 的 map 迭代顺序是随机的，桶的初始顺序会影响中位切分里「相等元素」的
	// 相对位置，进而让**同一张图两次量化得到不同字节**（实测差 31 字节）。
	// 视觉上完全一样，但会破坏幂等：`thumbbackfill -force` 反复跑会不断改写文件、
	// 无谓地让 CDN 缓存失效，上传链路与回填链路也会对不上（png8_test.go 守门）。
	keys := make([]uint32, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	buckets := make([]*colorBucket, 0, len(hist))
	for _, k := range keys {
		e := hist[k]
		e.snap()
		buckets = append(buckets, e)
	}

	// ---- 2. 中位切分 ----
	boxes := cutBoxes(buckets, maxColors)

	// ---- 3. 代表色 = 盒内按像素数加权平均 ----
	pal := make(color.Palette, 0, len(boxes))
	reps := make([]rgba8, len(boxes))
	for i, bx := range boxes {
		rep := boxAverage(buckets[bx.lo:bx.hi])
		reps[i] = rep
		pal = append(pal, color.NRGBA{rep.r, rep.g, rep.b, rep.a})
	}

	// ---- 4. 桶 → 最近代表色（每个桶只算一次，逐像素只做一次查表）----
	index := make(map[uint32]uint8, len(buckets))
	for _, e := range buckets {
		best, bestDist := 0, math.MaxFloat64
		for i, rep := range reps {
			if d := colorDist(e.mean, rep); d < bestDist {
				bestDist, best = d, i
			}
		}
		index[e.key] = uint8(best)
	}

	out := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out.SetColorIndex(x, y, index[bucketKey(c.R, c.G, c.B, c.A)])
		}
	}
	return out, nil
}

// rgba8 非预乘 RGBA，量化全程用它（避免预乘 alpha 让透明像素的 RGB 失真）。
type rgba8 struct{ r, g, b, a uint8 }

// colorBucket 一个 5bit/通道直方图桶：key 用于回查，mean 是桶内加权平均色。
type colorBucket struct {
	key   uint32
	count int
	rSum  int64
	gSum  int64
	bSum  int64
	aSum  int64
	mean  rgba8
}

func (e *colorBucket) snap() {
	n := int64(e.count)
	e.mean = rgba8{
		uint8(e.rSum / n), uint8(e.gSum / n),
		uint8(e.bSum / n), uint8(e.aSum / n),
	}
}

// bucketKey 各通道取高 5 位拼成 20bit 键（5+5+5+5）。
func bucketKey(r, g, b, a uint8) uint32 {
	return uint32(r>>3)<<15 | uint32(g>>3)<<10 | uint32(b>>3)<<5 | uint32(a>>3)
}

// colorBox 是 buckets 的一段 [lo, hi)。
type colorBox struct{ lo, hi int }

// cutBoxes 反复切分「跨度最大」的盒子，返回最终的盒子列表。
func cutBoxes(buckets []*colorBucket, maxColors int) []colorBox {
	boxes := []colorBox{{0, len(buckets)}}
	for len(boxes) < maxColors {
		best, bestScore := -1, int64(-1)
		for i, bx := range boxes {
			if bx.hi-bx.lo < 2 {
				continue
			}
			if s := boxScore(buckets[bx.lo:bx.hi]); s > bestScore {
				bestScore, best = s, i
			}
		}
		if best < 0 {
			break
		}
		bx := boxes[best]
		ch := widestChannel(buckets[bx.lo:bx.hi])
		part := buckets[bx.lo:bx.hi]
		sort.Slice(part, func(i, j int) bool {
			return channelOf(part[i].mean, ch) < channelOf(part[j].mean, ch)
		})
		// 按像素数加权取中位：不能按桶数均分，否则「一大片背景色 + 一堆零星高光」
		// 会把大片背景挤成十几项，高光却各占一项。
		var total int64
		for _, e := range part {
			total += int64(e.count)
		}
		var acc int64
		mid := bx.lo
		for k := bx.lo; k < bx.hi-1; k++ {
			acc += int64(buckets[k].count)
			mid = k + 1
			if acc*2 >= total {
				break
			}
		}
		boxes = append(boxes[:best], append([]colorBox{{bx.lo, mid}, {mid, bx.hi}}, boxes[best+1:]...)...)
	}
	return boxes
}

// boxScore 用「最大通道跨度 × 桶数」挑下一个要切的盒子：跨度大说明色差明显，
// 乘桶数是为了优先拆「颜色多且分散」的大盒子，而不是只有两个离群点的小盒子。
func boxScore(part []*colorBucket) int64 {
	var mn, mx [4]int
	for i := range mn {
		mn[i], mx[i] = 255, 0
	}
	for _, e := range part {
		v := [4]int{int(e.mean.r), int(e.mean.g), int(e.mean.b), int(e.mean.a)}
		for i := range v {
			if v[i] < mn[i] {
				mn[i] = v[i]
			}
			if v[i] > mx[i] {
				mx[i] = v[i]
			}
		}
	}
	var widest int
	for i := range mn {
		if d := mx[i] - mn[i]; d > widest {
			widest = d
		}
	}
	return int64(widest) * int64(len(part))
}

// widestChannel 返回盒子内跨度最大的通道下标（0=r 1=g 2=b 3=a）。
func widestChannel(part []*colorBucket) int {
	var mn, mx [4]int
	for i := range mn {
		mn[i], mx[i] = 255, 0
	}
	for _, e := range part {
		v := [4]int{int(e.mean.r), int(e.mean.g), int(e.mean.b), int(e.mean.a)}
		for i := range v {
			if v[i] < mn[i] {
				mn[i] = v[i]
			}
			if v[i] > mx[i] {
				mx[i] = v[i]
			}
		}
	}
	ch, widest := 0, -1
	for i := range mn {
		if d := mx[i] - mn[i]; d > widest {
			widest, ch = d, i
		}
	}
	return ch
}

func channelOf(c rgba8, ch int) uint8 {
	switch ch {
	case 0:
		return c.r
	case 1:
		return c.g
	case 2:
		return c.b
	default:
		return c.a
	}
}

func boxAverage(part []*colorBucket) rgba8 {
	var rs, gs, bs, as, n int64
	for _, e := range part {
		rs += e.rSum
		gs += e.gSum
		bs += e.bSum
		as += e.aSum
		n += int64(e.count)
	}
	return rgba8{uint8(rs / n), uint8(gs / n), uint8(bs / n), uint8(as / n)}
}

// colorDist 加权欧氏距离。alpha 权重 1.5：抗锯齿边缘的透明度差比轻微 RGB 差更扎眼。
func colorDist(a, b rgba8) float64 {
	dr := float64(a.r) - float64(b.r)
	dg := float64(a.g) - float64(b.g)
	db := float64(a.b) - float64(b.b)
	da := float64(a.a) - float64(b.a)
	return dr*dr + dg*dg + db*db + 1.5*da*da
}

// encodeThumbPNG 量化 + 调色板编码。量化失败一律回退无损 PNG（fail-open）：
// 缩略图是增强，绝不能因为量化器出问题就让整张图没有 _thumb（端上会多打一次 404）。
//
// 唯一会返回 error 的是「连 png.Encode 都不成立的输入」（如 0×0 空图）——
// 那本来就不是合法图片，decodeImage 在真实链路上已经挡掉了，不该伪造一张出来。
func encodeThumbPNG(img image.Image) ([]byte, error) {
	pal, err := quantizePNG(img, thumbPaletteColors)
	if err == nil {
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if e := enc.Encode(&buf, pal); e == nil {
			return buf.Bytes(), nil
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// errEmptyImage 空图无法量化，调用方据此回退无损编码。
type errEmptyImage struct{}

func (errEmptyImage) Error() string { return "empty image" }
