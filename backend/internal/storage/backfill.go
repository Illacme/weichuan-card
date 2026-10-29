package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BackfillOptions 存量缩略图回填选项。
type BackfillOptions struct {
	// DryRun 只统计不落盘（先看量再决定要不要跑）。
	DryRun bool
	// Force 忽略「缩略图已存在且是最新的」，全部重新生成。
	// 用于缩略图规格变更（如调整 ThumbMaxEdge）后整体刷新。
	Force bool
	// Warn 单文件失败回调（可为 nil）。回填是批处理，单张失败不该中断整轮，
	// 但必须留下线索——否则「有一批图永远没有缩略图」这件事没人知道。
	Warn func(path string, err error)
}

// BackfillResult 回填结果计数。
type BackfillResult struct {
	Scanned    int   // 候选原图数（已排除缩略图自身与非 jpg/png）
	Generated  int   // 本次成功生成的缩略图数（比原图小）
	Copied     int   // 原图已足够小/已足够紧凑，重编码反而更大 → 直接存原样
	Skipped    int   // 缩略图已存在且是最新的，跳过
	Failed     int   // 读取 / 解码 / 写入失败
	BytesSaved int64 // 原图合计 − 缩略图合计（>0 即列表页省下的传输量）
}

// BackfillThumbs 扫描 dir 下所有 jpg/jpeg/png，为「缺缩略图」或「缩略图已过期」的
// 原图补生成 <name>_thumb.<ext> 兄弟文件——命名与上传路径（Save）完全一致，
// 各端据此「原 URL 末位扩展名前插 _thumb」派生缩略图地址。
//
// 设计要点（决定它能不能被放心地反复执行）：
//   - **幂等**：缩略图已存在且 mtime 不早于原图 → 跳过；重复执行无副作用、计数稳定；
//   - **只读原图**：绝不改写/删除原图，只新增 _thumb 文件；
//   - **跳过缩略图自身**（*_thumb.<ext>），避免套娃生成 _thumb_thumb；
//   - **单文件失败不中断整轮**（记 WARN 继续），适合大目录一次性补齐；
//   - **扩展名统一小写**：与客户端 thumbnailURL（派生时用小写扩展名）保持一致，
//     否则 `cover.JPG` 会生成 `cover_thumb.JPG` 而端上找的是 `cover_thumb.jpg`。
//
// 只对本地磁盘存储生效；对象存储（minio/oss/s3）的对象不在该目录，需另走对应通道。
func BackfillThumbs(dir string, opt BackfillOptions) (BackfillResult, error) {
	var res BackfillResult
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// 跳过：非位图（gif/webp/mp4 后端本就不生成缩略图）
		dot := filepath.Ext(d.Name())
		ext := strings.ToLower(strings.TrimPrefix(dot, "."))
		switch ext {
		case "jpg", "jpeg", "png":
		default:
			return nil
		}
		base := strings.TrimSuffix(d.Name(), dot)
		// 跳过：它本身就是缩略图
		if strings.HasSuffix(base, "_thumb") {
			return nil
		}
		res.Scanned++

		if e := backfillOne(path, base, ext, opt, &res); e != nil {
			res.Failed++
			if opt.Warn != nil {
				opt.Warn(path, e)
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

// backfillOne 处理单个原图：判断是否需要生成 → 生成 → 落盘。
// 返回 nil 表示「已处理或无需处理」；返回非 nil 表示该文件失败（调用方计数并 WARN）。
func backfillOne(path, base, ext string, opt BackfillOptions, res *BackfillResult) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// 扩展名统一小写，与客户端派生规则一致
	thumbPath := filepath.Join(filepath.Dir(path), base+"_thumb."+ext)
	if !opt.Force {
		if ti, e := os.Stat(thumbPath); e == nil && !ti.ModTime().Before(info.ModTime()) {
			res.Skipped++
			return nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	thumb, err := Thumbnail(ext, data)
	if err != nil {
		return err
	}
	if len(thumb) == 0 {
		return errors.New("生成结果为空")
	}
	// 兜底：绝不写出比原图更大的「缩略图」。
	// 原图本就 ≤ ThumbMaxEdge（无需缩放），或是压缩率已经很高的小图时，
	// 重编码只会更大（实测 200x200 的 PNG 3.4KB → 3.6KB）。此时直接存原图字节：
	// 体积没有变化，但 _thumb **存在**——端上就不会 404 再回退一次，
	// 省掉的正是那次失败请求（实测每个请求 0.3-0.5s）。
	if len(thumb) >= len(data) {
		thumb = data
		res.Copied++
	} else {
		res.Generated++
		res.BytesSaved += int64(len(data)) - int64(len(thumb))
	}
	if !opt.DryRun {
		if e := os.WriteFile(thumbPath, thumb, 0644); e != nil {
			return e
		}
	}
	return nil
}

// String 便于命令行直接打印汇总。
func (r BackfillResult) String() string {
	return fmt.Sprintf("扫描 %d 张，生成 %d 张，原样存 %d 张(原图已够小)，跳过(已有) %d 张，失败 %d 张，预计省传输 %.1f KB",
		r.Scanned, r.Generated, r.Copied, r.Skipped, r.Failed, float64(r.BytesSaved)/1024)
}
