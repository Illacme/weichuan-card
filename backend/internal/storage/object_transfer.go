package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/minio/minio-go/v7"
)

// 一键迁移的对象层实现（迁移 0054）。
//
// 三条边界（勿改）：
//  1. 只复制不删除。源桶对象在迁移后原样保留，避免「迁完发现漏了、源也没了」。
//  2. 流式搬运。视频可能上百 MB，绝不能走 ObjectStore.ReadObject 的 []byte 路径。
//  3. 前缀按「对象键」解释，不做路径穿越；写侧仍然走各驱动的 validateObjectKey。

// contentTypeForKey 从扩展名猜 Content-Type；猜不到给 octet-stream。
// 图片缩略图链路靠扩展名识别，这里保持同一口径。
func contentTypeForKey(key string) string {
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(key)))
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

// --- 本地磁盘 ---

// ListObjects 枚举 Dir 下 prefix 匹配的文件，返回以 / 分隔的相对键。
func (l *LocalUploader) ListObjects(prefix string, each func(key string, size int64) error) error {
	root := filepath.Join(l.Dir, filepath.FromSlash(prefix))
	// 前缀可能落在不存在的目录（还没上传过任何文件）：这是空桶，不是错误。
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(l.Dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return each(key, info.Size())
	})
}

// PutObjectStream 流式写本地文件，同 key 覆盖。
func (l *LocalUploader) PutObjectStream(key string, r io.Reader, size int64, _ string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	dst := filepath.Join(l.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }() // 关闭错误不影响已写完的内容，且此处无法回话
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	if size > 0 {
		// 流式写入无法靠 io.Copy 的返回值区分「短读后正常结束」与「对端提前断开」；
		// 对象存储侧会在 PutObject 时自行校验，本地侧用 Stat 补一道。
		info, err := out.Stat()
		if err != nil {
			return err
		}
		if info.Size() != size {
			_ = os.Remove(dst) // 半截对象比没有更坏：下游会当成迁移成功
			return fmt.Errorf("本地对象 %s 写入不完整：期望 %d 字节，实际 %d 字节", key, size, info.Size())
		}
	}
	return nil
}

// CopyObjectTo 从本地文件流式复制到目标存储。
func (l *LocalUploader) CopyObjectTo(dst ObjectStreamWriter, key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(l.Dir, filepath.FromSlash(key)))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // 只读句柄，关闭错误无后果
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return dst.PutObjectStream(key, f, info.Size(), contentTypeForKey(key))
}

// --- MinIO / S3 ---

func (m *MinioUploader) ListObjects(prefix string, each func(key string, size int64) error) error {
	ctx := context.Background()
	ch := m.Client.ListObjects(ctx, m.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for obj := range ch {
		if obj.Err != nil {
			return obj.Err
		}
		if err := each(obj.Key, obj.Size); err != nil {
			return err
		}
	}
	return nil
}

func (m *MinioUploader) PutObjectStream(key string, r io.Reader, size int64, contentType string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = contentTypeForKey(key)
	}
	_, err := m.Client.PutObject(context.Background(), m.Bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("minio put stream: %w", err)
	}
	return nil
}

func (m *MinioUploader) CopyObjectTo(dst ObjectStreamWriter, key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	obj, err := m.Client.GetObject(context.Background(), m.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("minio get object: %w", err)
	}
	defer func() { _ = obj.Close() }() // 只读句柄，关闭错误无后果
	info, err := obj.Stat()
	if err != nil {
		return fmt.Errorf("minio stat object: %w", err)
	}
	return dst.PutObjectStream(key, obj, info.Size, contentTypeForKey(key))
}

// --- 阿里云 OSS ---

func (o *OssUploader) ListObjects(prefix string, each func(key string, size int64) error) error {
	marker := ""
	for {
		lor, err := o.Bucket.ListObjects(oss.Prefix(prefix), oss.Marker(marker), oss.MaxKeys(1000))
		if err != nil {
			return fmt.Errorf("oss list objects: %w", err)
		}
		for _, obj := range lor.Objects {
			if err := each(obj.Key, obj.Size); err != nil {
				return err
			}
		}
		if !lor.IsTruncated {
			return nil
		}
		marker = lor.NextMarker
		if marker == "" {
			return fmt.Errorf("oss list objects: 分页未推进，已停止以免死循环")
		}
	}
}

func (o *OssUploader) PutObjectStream(key string, r io.Reader, size int64, contentType string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = contentTypeForKey(key)
	}
	if err := o.Bucket.PutObject(key, r, oss.ContentType(contentType), oss.ContentLength(size)); err != nil {
		return fmt.Errorf("oss put stream: %w", err)
	}
	return nil
}

func (o *OssUploader) CopyObjectTo(dst ObjectStreamWriter, key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	body, err := o.Bucket.GetObject(key)
	if err != nil {
		return fmt.Errorf("oss get object: %w", err)
	}
	defer func() { _ = body.Close() }() // 只读句柄，关闭错误无后果
	// GetObject 的响应头里带 Content-Length，SDK 的 GetObjectResult 未直接暴露；
	// size 传 -1 让 PutObjectStream 跳过完整性校验（对象存储侧会自行校验）。
	return dst.PutObjectStream(key, body, -1, contentTypeForKey(key))
}

var (
	_ ObjectLister       = (*LocalUploader)(nil)
	_ ObjectLister       = (*MinioUploader)(nil)
	_ ObjectLister       = (*OssUploader)(nil)
	_ ObjectStreamWriter = (*LocalUploader)(nil)
	_ ObjectStreamWriter = (*MinioUploader)(nil)
	_ ObjectStreamWriter = (*OssUploader)(nil)
	_ ObjectCopier       = (*LocalUploader)(nil)
	_ ObjectCopier       = (*MinioUploader)(nil)
	_ ObjectCopier       = (*OssUploader)(nil)
)

// --- 删除能力（删除双清，见 capabilities.ObjectDeleter）---

// DeleteObject 删除本地对象。不存在按成功处理：清理链路会被重复触发
// （上传补偿、人工重试），把「已经清过了」报成失败只会制造无效告警。
func (l *LocalUploader) DeleteObject(key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(l.Dir, filepath.FromSlash(key)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *MinioUploader) DeleteObject(key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	return m.Client.RemoveObject(context.Background(), m.Bucket, key, minio.RemoveObjectOptions{})
}

func (o *OssUploader) DeleteObject(key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	return o.Bucket.DeleteObject(key)
}
