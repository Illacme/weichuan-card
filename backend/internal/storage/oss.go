package storage

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// OssUploader 实现 Uploader：写入阿里云 OSS。假定 Bucket 已设为公开读（或绑定自定义域名/CDN），
// 返回 "<BaseURL>/<object>" 作为公开访问 URL。与 MinIO 实现共享同一 Uploader 接口，业务代码不变。
type OssUploader struct {
	Client   *oss.Client
	Bucket   *oss.Bucket
	BaseURL  string            // 公开访问基址，如 https://cdn.xxx 或 https://<bucket>.oss-cn-xx.aliyuncs.com
	MaxBytes int64
	AllowExt map[string]bool
}

// NewOssUploader 构造 OSS 客户端（endpoint 形如 "oss-cn-hangzhou.aliyuncs.com"）。
// endpoint / bucket 为空时直接报错：SDK 对空 endpoint 不校验，会拖到首次上传
// 才以难以定位的方式失败，这里在装配期 fail-fast。
func NewOssUploader(endpoint, accessKeyID, accessKeySecret, bucket, baseURL string, maxBytes int64, allow map[string]bool) (*OssUploader, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("oss endpoint is required")
	}
	if bucket == "" {
		return nil, fmt.Errorf("oss bucket is required")
	}
	client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("oss new client: %w", err)
	}
	b, err := client.Bucket(bucket)
	if err != nil {
		return nil, fmt.Errorf("oss bucket: %w", err)
	}
	return &OssUploader{Client: client, Bucket: b, BaseURL: baseURL, MaxBytes: maxBytes, AllowExt: allow}, nil
}

func (o *OssUploader) Save(fh *multipart.FileHeader) (string, error) {
	if err := checkFile(fh, o.MaxBytes, o.AllowExt); err != nil {
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
	// 读取全部字节：剥离 EXIF + 按长边压缩原图，并额外生成缩略图。
	raw, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	full, thumb, err := ProcessUpload(ext, raw)
	if err != nil {
		return "", err
	}
	// 对象名直接用 name：URL 为 BaseURL/name，与本地驱动（静态路由 /uploads -> Dir，文件落 Dir/name）形态一致
	if err := o.Bucket.PutObject(name, bytes.NewReader(full), oss.ContentType(fh.Header.Get("Content-Type"))); err != nil {
		return "", fmt.Errorf("oss put: %w", err)
	}
	// 缩略图 best-effort：生成失败不影响主图上传（列表页改用原图即可）。
	// 约定命名 <hash>_thumb.<ext>，列表页按「原 URL 末位扩展名前插 _thumb」派生。
	if thumb != nil {
		tn := strings.TrimSuffix(name, "."+ext) + "_thumb." + ext
		if perr := o.Bucket.PutObject(tn, bytes.NewReader(thumb), oss.ContentType(fh.Header.Get("Content-Type"))); perr != nil {
			log.Printf("[WARN] 缩略图上传失败 %s: %v", tn, perr)
		}
	}
	return strings.TrimRight(o.BaseURL, "/") + "/" + name, nil
}

func (o *OssUploader) SaveBytes(relPath string, data []byte, contentType string) (string, error) {
	clean := strings.TrimPrefix(relPath, "/")
	if err := o.Bucket.PutObject(clean, bytes.NewReader(data), oss.ContentType(contentType)); err != nil {
		return "", fmt.Errorf("oss put bytes: %w", err)
	}
	return strings.TrimRight(o.BaseURL, "/") + "/" + clean, nil
}

// PresignPut 生成预签名 PUT URL（直传用）；contentType 参与 OSS 签名，调用方 PUT 必须带相同 Content-Type
func (o *OssUploader) PresignPut(key, contentType string, expiry time.Duration) (string, string, error) {
	var opts []oss.Option
	if contentType != "" {
		opts = append(opts, oss.ContentType(contentType))
	}
	u, err := o.Bucket.SignURL(key, http.MethodPut, int64(expiry.Seconds()), opts...)
	if err != nil {
		return "", "", fmt.Errorf("oss presign put: %w", err)
	}
	return u, strings.TrimRight(o.BaseURL, "/") + "/" + key, nil
}

// SignGetURL 生成限时 GET URL（私有桶用）
func (o *OssUploader) SignGetURL(key string, expiry time.Duration) (string, error) {
	u, err := o.Bucket.SignURL(key, http.MethodGet, int64(expiry.Seconds()))
	if err != nil {
		return "", fmt.Errorf("oss presign get: %w", err)
	}
	return u, nil
}
