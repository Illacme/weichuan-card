package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinioUploader 实现 Uploader：写入 MinIO（或任意 S3 兼容对象存储，s3 驱动即腾讯云
// COS / 华为云 OBS / 七牛云 / AWS S3 / R2 等）。假定 bucket 已设为公开读（或通过
// CDN 域名公开），返回 "<BaseURL>/<object>" 作为公开访问 URL。
type MinioUploader struct {
	Client   *minio.Client
	Bucket   string
	BaseURL  string            // 公开访问基址，如 https://cdn.xxx 或 https://minio.example.com/bucket
	MaxBytes int64
	AllowExt map[string]bool
}

// NewMinioUploader 构造 MinIO 客户端（endpoint 不含协议头，如 "play.min.io" / "s3.amazonaws.com"）
// region 为 S3 区域（如 ap-guangzhou）：留空时 minio-go 先按 endpoint URL 猜（AWS /
// Google 可猜中），猜不到再向服务端发 GetBucketLocation 探测并缓存，主流 S3 云无需手填。
func NewMinioUploader(endpoint, accessKey, secretKey, bucket, baseURL, region string, useSSL bool, maxBytes int64, allow map[string]bool) (*MinioUploader, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("minio new client: %w", err)
	}
	return &MinioUploader{Client: client, Bucket: bucket, BaseURL: baseURL, MaxBytes: maxBytes, AllowExt: allow}, nil
}

func (m *MinioUploader) Save(fh *multipart.FileHeader) (string, error) {
	if err := checkFile(fh, m.MaxBytes, m.AllowExt); err != nil {
		return "", err
	}
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }() // 只读句柄，关闭错误无处可报也无后果

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fh.Filename), "."))
	name, err := RandName(ext)
	if err != nil {
		return "", err
	}
	// 读取全部字节做 EXIF/GPS 剥离（非图片原样透传），再用剥离后的字节上传。
	raw, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	clean, err := ProcessImage(ext, raw)
	if err != nil {
		return "", err
	}
	// 对象名直接用 name：URL 为 BaseURL/name，与本地驱动（静态路由 /uploads -> Dir，文件落 Dir/name）形态一致
	if _, err := m.Client.PutObject(context.Background(), m.Bucket, name, bytes.NewReader(clean), int64(len(clean)), minio.PutObjectOptions{
		ContentType: fh.Header.Get("Content-Type"),
	}); err != nil {
		return "", fmt.Errorf("minio put: %w", err)
	}
	return strings.TrimRight(m.BaseURL, "/") + "/" + name, nil
}

func (m *MinioUploader) SaveBytes(relPath string, data []byte, contentType string) (string, error) {
	clean := strings.TrimPrefix(relPath, "/")
	if _, err := m.Client.PutObject(context.Background(), m.Bucket, clean, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return "", fmt.Errorf("minio put bytes: %w", err)
	}
	return strings.TrimRight(m.BaseURL, "/") + "/" + clean, nil
}

// PresignPut 生成预签名 PUT URL（直传用）；MinIO 对 PUT Content-Type 不强制签名，contentType 仅作占位
func (m *MinioUploader) PresignPut(key, _ string, expiry time.Duration) (string, string, error) {
	u, err := m.Client.PresignedPutObject(context.Background(), m.Bucket, key, expiry)
	if err != nil {
		return "", "", fmt.Errorf("minio presign put: %w", err)
	}
	return u.String(), strings.TrimRight(m.BaseURL, "/") + "/" + key, nil
}

// SignGetURL 生成限时 GET URL（私有桶用）
func (m *MinioUploader) SignGetURL(key string, expiry time.Duration) (string, error) {
	u, err := m.Client.PresignedGetObject(context.Background(), m.Bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("minio presign get: %w", err)
	}
	return u.String(), nil
}
