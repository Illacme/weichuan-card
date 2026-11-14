package refmigrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
	"weichuan/internal/uploadroute"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"
)

// 本文件是 P3 的「按 company_id 回源真桶」适配器（第六十六轮）。
//
// 第六十五轮只给了本地盘 → 本地盘：目标目录是所有租户共用的一个根，键自带
// t/<cid>/ 所以不串号，但它证明不了「对象真的进了客户的桶」。真桶适配器要补的
// 不是「再实现一个 ObjectStore」这么简单，有三条不能退的约束：
//
//  1. **无静默降级**。DBLoader 的 (nil, nil) 语义是「这家没配，走平台实例」，
//     在上传链路里完全正确；在搬迁链路里却是最坏的结果——对象被复制到平台盘的
//     新键下、库也改成 t/<cid>/...，后台显示搬迁成功，等把流量切到客户桶才发现
//     那边一个对象都没有。所以这里把 (nil, nil) 转成**显式错误**。
//  2. **路径风格与 nginx 同源**。minio-go 默认 BucketLookupAuto 只看 endpoint
//     是不是 IP，看不到 host_header 列。第六十三轮真桶实测就是猜错后拿到
//     400 InvalidBucketName。这里与上传侧共用 storage.S3BucketLookup。
//  3. **凭据与桶方响应不进错误文本**。S3 的 ErrorResponse.Message 可能回显
//     AccessKey 片段，回填命令的输出会进运维日志/工单，属于一次写入长期暴露。
//     所有错误只压成「错误码 + HTTP 状态码」。

// S3StoreConfig 是构造一家租户 S3 适配器所需的全部输入（凭据已解密）。
// 与 uploadroute.Config 分开，是为了让 NewS3Store 不依赖数据库，可被 httptest 单测。
type S3StoreConfig struct {
	CompanyID  int64
	Driver     string
	Endpoint   string
	Bucket     string
	Region     string
	UseSSL     bool
	HostHeader string
	AccessKey  string
	SecretKey  string
}

// S3Store 把一家租户的自有桶实现成 ObjectStore。
type S3Store struct {
	client *minio.Client
	bucket string
}

// NewS3Store 构造真桶适配器。它只构造客户端，不发起网络请求——
// 「桶到底通不通」由 preflight 的 Stat 去撞，报错会带明确的错误码。
func NewS3Store(cfg S3StoreConfig) (*S3Store, error) {
	if cfg.CompanyID <= 0 {
		return nil, fmt.Errorf("company_id=%d 不是合法租户（0 是平台自身，没有自有桶）", cfg.CompanyID)
	}
	switch cfg.Driver {
	case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
		// 本轮真桶适配器只走 S3 协议。oss 驱动用阿里云自有 SDK，签名与分块
		// 语义都不同，硬套 minio-go 会写出「看似成功、控制台里没有」的对象。
	case nginxsnippet.DriverOSS:
		return nil, fmt.Errorf("企业 %d 的 driver=oss 暂不支持真桶回填（需接 OSS SDK），"+
			"本轮只支持 minio/s3；不要改成本地盘顶上", cfg.CompanyID)
	default:
		return nil, fmt.Errorf("企业 %d 的 driver=%q 不在枚举内（minio/s3/oss/local）", cfg.CompanyID, cfg.Driver)
	}
	t := nginxsnippet.Target{
		CompanyID:  cfg.CompanyID,
		Driver:     cfg.Driver,
		Endpoint:   cfg.Endpoint,
		Bucket:     cfg.Bucket,
		UseSSL:     cfg.UseSSL,
		HostHeader: cfg.HostHeader,
		Enabled:    true, // 见 uploadroute.build：不置 true 会让 Validate 早退、跳过全部字段校验
	}
	lookup, err := storage.S3BucketLookup(t)
	if err != nil {
		return nil, fmt.Errorf("企业 %d 的桶地址不合法：%w", cfg.CompanyID, err)
	}
	// 虚拟主机风格里，minio-go 只会把 <bucket> 拼到 endpoint 前面当 Host，
	// **不会按 host_header 改写 Host / TLS SNI**。回填命令不经过 nginx（nginx
	// 才负责 proxy_set_header Host），所以 host_header 去掉 "<bucket>." 之后
	// 必须正好等于 endpoint；否则我们连的是另一个域名，而没有任何人报错。
	// 客户自有域名（public_base_url）属于下一阶段，这里显式拒绝而不是猜。
	if !t.PathStyle() {
		host := strings.TrimPrefix(strings.TrimSpace(cfg.HostHeader), cfg.Bucket+".")
		if host != strings.TrimSpace(cfg.Endpoint) {
			return nil, fmt.Errorf("企业 %d 的 hostHeader=%q 与 endpoint=%q 不是同一个域名："+
				"回填命令不经 nginx，无法改写 Host/SNI；请用路径风格（hostHeader 留空或等于 endpoint）",
				cfg.CompanyID, cfg.HostHeader, cfg.Endpoint)
		}
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("企业 %d 的自有桶凭据不完整（AccessKey / SecretKey 缺一）", cfg.CompanyID)
	}
	client, err := minio.New(strings.TrimSpace(cfg.Endpoint), &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		// 构造失败的错误里会带 endpoint；endpoint 不是秘密，但凭据绝不能带。
		return nil, fmt.Errorf("企业 %d 的 S3 客户端构造失败（endpoint=%q）：%v",
			cfg.CompanyID, strings.TrimSpace(cfg.Endpoint), err)
	}
	return &S3Store{client: client, bucket: strings.TrimSpace(cfg.Bucket)}, nil
}

// s3ErrDigest 把桶方错误压成「错误码 + 状态码」。
//
// 只认这两个字段，不取 Message / RequestID / Host / 响应体：probe.go 里已经写明，
// Message 会回显 AccessKey 的部分片段，而回填命令的输出会被贴进工单长期留存。
func s3ErrDigest(err error) string {
	resp := minio.ToErrorResponse(err)
	if resp.Code != "" || resp.StatusCode != 0 {
		return fmt.Sprintf("桶方返回 %s（HTTP %d）", resp.Code, resp.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	return "网络不可达或协议错误（原始信息未记录，避免把桶方响应体带出去）"
}

// isMissingObject 判断「对象不存在」。
//
// 最危险的错误是把 AccessDenied / 网络错当成「不存在」：preflight 会据此决定
// 去复制，而复制失败时库还没动，看上去像「只是没搬成」，实际是凭据从头就错了。
//
// 有一条协议层面的限制必须写明：S3 的 HEAD 响应按规范**不带 body**，所以
// 「404 是对象不存在还是桶不存在」在 Stat 这一层分不出来（服务端给的 XML
// 错误码客户端读不到）。因此这里对 404 一律按「对象不存在」处理，
// 桶不存在的兜底放在 Put：它会带错误码显式失败，且写完还有一次 Stat 回读。
// 也就是说这类配置错误会在「复制阶段」被报出来，而绝不会变成搬迁成功。
func isMissingObject(err error) bool {
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NotFound":
		return true
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return false
	}
	return resp.StatusCode == http.StatusNotFound
}

func (s *S3Store) Stat(key string) (ObjectInfo, error) {
	if err := validateObjectKey(key); err != nil {
		return ObjectInfo{}, err
	}
	info, err := s.client.StatObject(context.Background(), s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return ObjectInfo{Exists: true, Size: info.Size}, nil
	}
	if isMissingObject(err) {
		return ObjectInfo{}, nil
	}
	return ObjectInfo{}, fmt.Errorf("检查桶对象 %q 失败：%s", key, s3ErrDigest(err))
}

func (s *S3Store) Open(key string) (io.ReadCloser, error) {
	if err := validateObjectKey(key); err != nil {
		return nil, err
	}
	// minio-go 的 GetObject 是懒连接：网络/权限错误要到第一次 Read 才出现。
	// 调用方（Stores.Copy）先对源做过 Stat；本实现作为目标端只走 Put，
	// 这个方法留着是为了让「平台 S3 桶当源」下一轮接进来时不必改接口。
	obj, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("打开桶对象 %q 失败：%s", key, s3ErrDigest(err))
	}
	return obj, nil
}

// Put 把对象写进租户桶。
//
// 原子性由 S3 协议本身保证，不需要临时对象 + 改名：单次 PUT 要么完整可见、
// 要么不可见；minio-go 对大对象走的分块上传，在 CompleteMultipartUpload
// 之前同样不会被 StatObject 看见。所以「复制到一半进程崩了」不会留下一个
// 存在但短一截的对象让下次重跑误跳过。
//
// 写完仍立即回读一次 size：既确认对象已可见，也挡住「请求打到了一个会回 200
// 但不真正存储的错误端点」这种谁都不报错的假成功。
func (s *S3Store) Put(key string, r io.Reader, size int64, contentType string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("对象 %q 的长度 %d 非法", key, size)
	}
	if _, err := s.client.PutObject(context.Background(), s.bucket, key, r, size,
		minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return fmt.Errorf("写入桶对象 %q 失败：%s", key, s3ErrDigest(err))
	}
	info, err := s.client.StatObject(context.Background(), s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("写入 %q 后回读失败：%s", key, s3ErrDigest(err))
	}
	if info.Size != size {
		return fmt.Errorf("写入 %q 后大小不符：期望 %d 字节，桶里是 %d 字节（不要重跑，先查桶）",
			key, size, info.Size)
	}
	return nil
}

var _ ObjectStore = (*S3Store)(nil)

// TenantTargetFor 返回「按 company_id 组装真桶」的 TargetFor。
//
// platformBase 恒传空串即可：DBLoader 用它兜 public_base_url，而回填不生成
// 任何公开 URL，BaseURL 在这个适配器里不参与构造。
//
// 与 uploadroute.Resolver.For 的区别必须记住：那里 (nil, nil) 是「走平台实例」，
// 这里必须变成错误。两者共享同一个 DBLoader（查表 + 解密 + NeedsSnippet 判定），
// 但**对三态的解释不同**——这正是「回填绝不回退平台盘」的落点。
func TenantTargetFor(db *gorm.DB, platformBase string) func(int64) (ObjectStore, error) {
	load := uploadroute.DBLoader(db, platformBase)
	return func(cid int64) (ObjectStore, error) {
		if cid <= 0 {
			return nil, fmt.Errorf("company_id=%d 不是合法租户", cid)
		}
		cfg, err := load(cid)
		if err != nil {
			return nil, fmt.Errorf("企业 %d 的自有存储读取失败（不回退平台盘）：%w", cid, err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("企业 %d 没有生效的自有桶配置（没配 / 未启用 / driver=local）："+
				"不回退平台盘——那样对象只会被复制到平台本地目录，客户桶里一个都没有", cid)
		}
		st, err := NewS3Store(S3StoreConfig{
			CompanyID:  cfg.CompanyID,
			Driver:     cfg.Driver,
			Endpoint:   cfg.Endpoint,
			Bucket:     cfg.Bucket,
			Region:     cfg.Region,
			UseSSL:     cfg.UseSSL,
			HostHeader: cfg.HostHeader,
			AccessKey:  cfg.AccessKey,
			SecretKey:  cfg.SecretKey,
		})
		if err != nil {
			return nil, fmt.Errorf("企业 %d 的真桶适配器不可用（不回退平台盘）：%w", cid, err)
		}
		return st, nil
	}
}
