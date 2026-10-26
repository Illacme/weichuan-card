package storage

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"strings"
)

// ProcessImage 对上传图片做「隐私脱敏」：解码后重新编码，丢弃一切不透明元数据
// （EXIF / GPS 经纬度 / 相机型号 / 拍摄时间 / 缩略图等），仅保留像素本身。
//
// 返回语义：
//   - jpg/jpeg/png：解码后重新编码，元数据被彻底剥离（含 GPS 经纬度）。
//   - 其他类型（gif/webp/mp4/未知）：原样透传。
//
// 关于 pass-through 的取舍：
//   - gif：标准库 image/gif 重新编码只会保留首帧、丢尽动画，而 GIF 规范本身
//     不携带 GPS 等位置信息，重新编码弊大于利，故原样透传。
//   - webp：标准库无法解码（需第三方依赖），属已知盲区，原样透传。
//   - mp4：视频非图片，不处理。
//
// 解码失败（如极异常的 JPEG/PNG）按 fail-open 处理：放弃剥离、原样透传该文件，
// 而非阻断本次上传。理由：
//  1. 不引入上传回归——任何通过 checkFile 校验的文件仍照常上传，安全姿态不弱于
//     改造前（改造前本就不过滤元数据）；
//  2. 仅对极小比例「标准库无法解析的合法图片」放弃剥离，绝大多数真实上传
//     （手机 JPEG、截图 PNG）都能被 stdlib 正常解码并完成剥离。
//
// 这样上传头像/案例图时，用户手机相册里的 GPS 坐标不会随名片外泄，符合 PIPL
// 对「行踪轨迹、住宿信息」等敏感个人信息的最小化收集要求。
func ProcessImage(ext string, data []byte) ([]byte, error) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "jpg", "jpeg":
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return data, nil // 解码失败：原样透传，不阻断上传
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return data, nil // 编码失败：同上，宁可透传也不阻断
		}
		return buf.Bytes(), nil
	case "png":
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return data, nil // 解码失败：原样透传，不阻断上传
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return data, nil // 编码失败：同上
		}
		return buf.Bytes(), nil
	default:
		// 非目标图片类型：原样返回，不引入破坏性改动
		return data, nil
	}
}
