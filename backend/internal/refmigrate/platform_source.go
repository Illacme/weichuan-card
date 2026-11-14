package refmigrate

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
)

// 本文件是 P3 的「平台自有 S3 兼容桶当源」适配器（第六十七轮）。
//
// 第六十六轮把**目标端**接到了客户自己的桶，源端仍只有平台本地盘。本轮补上
// 对称的另一半：存量历史对象本来就存在平台自己的 MinIO / S3 桶里（老上传
// 直接 PutObject(bucket, relPath)，键就是 legacyRel 推出的那个无前缀相对键），
// 不需要先下载到本地再从 --source-dir 读一遍。
//
// 三条不能退的约束：
//
//  1. **与生产上传侧同源**。平台自身存储没有 company_id、没有 host_header 列，
//     所以不能用 NewS3Store（那是租户语义：company_id>0 + S3BucketLookup 按
//     host_header 判路径风格）。这里复用 storage.NewMinioUploader 且**省略
//     lookup 变参**，拿到与 cmd/server.newUploader 完全同一个客户端构造
//     （BucketLookupAuto）。有源码级守门钉住「不许自己 minio.New」，否则
//     源端与写入侧的路径风格会分叉，症状是「对象明明在桶里却全部读不到」。
//  2. **源端只读**。ObjectStore 接口要求 Put，但回填绝不改动源桶。Put 显式
//     报错而不是委托给内部 S3Store：万一哪天有人把 Source / Target 接反，
//     会立刻失败，而不是静默把对象覆盖写回平台桶（那会污染全部存量）。
//  3. **桶不存在必须显式报桶**。第六十六轮靠「Put 的显式错误码 + 写后回读」
//     兜底 HEAD 不带 body 的协议限制；源端没有 Put，若桶名或凭据写错，
//     Stat 的 404 会被 isMissingObject 当成「源对象不存在」——运维会去查
//     对象为什么丢了，而真正错的是配置。这里在第一次 Stat / Open 前用
//     BucketExists 探一次（sync.Once 缓存，不逐对象重复发请求）。
//
// 键的口径：SourceKey 是 legacyRel 推出的历史相对键（如 shared-logo.png），
// 与平台桶里老对象一一对应；**不许加 p/ 或 t/<cid>/ 前缀**，那是写入侧
// ObjectKeyFor 的产物，加在这里会整批 404。
//
// 配置来源见 cmd/storage-backfill/source_config.go：env / CONFIG_FILE +
// sys_setting 只读覆盖，与生产上传侧同源且不写库。

// PlatformS3Config 是构造平台源所需的全部输入（凭据已由 CLI 层解析成明文）。
// 与 S3StoreConfig 分开，是因为它不需要 CompanyID / HostHeader，
// 也**不允许**出现 Driver=oss（OSS 用阿里云自有 SDK，签名语义不同）。
type PlatformS3Config struct {
	Driver    string
	Endpoint  string
	Bucket    string
	Region    string
	UseSSL    bool
	AccessKey string
	SecretKey string
}

// PlatformS3Source 是只读的平台桶源。
type PlatformS3Source struct {
	inner     *S3Store
	once      sync.Once
	bucketErr error
}

// NewPlatformS3Source 构造平台源。只构造客户端、校验配置，不发起网络请求；
// 桶是否真的存在由第一次 Stat/Open 的 ensureBucket 探测并缓存。
func NewPlatformS3Source(cfg PlatformS3Config) (*PlatformS3Source, error) {
	switch cfg.Driver {
	case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
		// 平台侧只有这两种驱动走 S3 协议。
	case nginxsnippet.DriverOSS:
		return nil, fmt.Errorf("平台存储 driver=oss 不支持 S3 源（需接 OSS SDK 的 GetObject）：" +
			"不回退本地盘，也不要把 OSS 端点当 S3 端点连")
	case nginxsnippet.DriverLocal, "":
		return nil, fmt.Errorf("平台存储 driver=%q 是本地盘 / 未配置，没有 S3 源可读："+
			"不回退本地盘，请用默认的 --source-dir；不要拿一个空桶冒充源", cfg.Driver)
	default:
		return nil, fmt.Errorf("平台存储 driver=%q 不在枚举内（local/minio/oss/s3）：不回退本地盘", cfg.Driver)
	}
	for _, f := range []struct{ name, value string }{
		{"storage_endpoint / MINIO_ENDPOINT", cfg.Endpoint},
		{"storage_bucket / MINIO_BUCKET", cfg.Bucket},
		{"storage_access_key / MINIO_ACCESS_KEY", cfg.AccessKey},
		{"storage_secret_key / MINIO_SECRET_KEY", cfg.SecretKey},
	} {
		if strings.TrimSpace(f.value) == "" {
			return nil, fmt.Errorf("平台 S3 源的 %s 为空：不回退本地盘，"+
				"请在环境变量 / CONFIG_FILE / 系统设置里补齐（与生产上传侧同一份配置）", f.name)
		}
	}
	// 刻意复用上传侧的构造函数：省略 lookup 变参 = SDK 默认 BucketLookupAuto，
	// 与 cmd/server.newUploader 一致。这里只取 Client / Bucket，不调用 Save。
	// maxBytes / allow 对只读源没有意义，传 0 / nil 不会触发任何校验。
	up, err := storage.NewMinioUploader(cfg.Endpoint, cfg.AccessKey, cfg.SecretKey,
		cfg.Bucket, "", cfg.Region, cfg.UseSSL, 0, nil)
	if err != nil {
		// minio.New 的错误只涉及 endpoint 形态，不含凭据；凭据绝不进错误文本。
		return nil, fmt.Errorf("平台 S3 客户端构造失败：%v", err)
	}
	return &PlatformS3Source{
		inner: &S3Store{client: up.Client, bucket: up.Bucket},
	}, nil
}

// ensureBucket 把「桶不存在 / 凭据错 / 网络不可达」与「对象不存在」分开。
//
// HEAD 响应按 HTTP 规范不带 body：StatObject 的 404 分不出是对象没有还是桶
// 没有，isMissingObject 只能一律按「对象不存在」处理。目标端还有 Put 的
// NoSuchBucket 错误码兜底，源端没有。BucketExists 走的是 HEAD /<bucket>，
// minio-go 会把无 body 的 404 归一成 NoSuchBucket → (false, nil)；
// AccessDenied / 网络错则返回 error，这里用 s3ErrDigest 脱敏。
func (s *PlatformS3Source) ensureBucket() error {
	s.once.Do(func() {
		ok, err := s.inner.client.BucketExists(context.Background(), s.inner.bucket)
		if err != nil {
			s.bucketErr = fmt.Errorf("平台源桶 %q 探测失败：%s（检查端点 / 桶名 / 凭据 / 网络）",
				s.inner.bucket, s3ErrDigest(err))
			return
		}
		if !ok {
			s.bucketErr = fmt.Errorf("平台源桶 %q 不存在：这不是「源对象不存在」，"+
				"请核对 storage_bucket / MINIO_BUCKET 是否指向存量对象所在的那个桶", s.inner.bucket)
		}
	})
	return s.bucketErr
}

func (s *PlatformS3Source) Stat(key string) (ObjectInfo, error) {
	if err := s.ensureBucket(); err != nil {
		return ObjectInfo{}, err
	}
	return s.inner.Stat(key)
}

func (s *PlatformS3Source) Open(key string) (io.ReadCloser, error) {
	if err := s.ensureBucket(); err != nil {
		return nil, err
	}
	return s.inner.Open(key)
}

// Put 是接口要求的第三个动作，但源端永远不该写。显式拒绝，见文件头第 2 条。
func (s *PlatformS3Source) Put(key string, _ io.Reader, _ int64, _ string) error {
	return fmt.Errorf("平台 S3 源是只读的：拒绝写入 %q（回填绝不改动源桶）", key)
}

var _ ObjectStore = (*PlatformS3Source)(nil)
