package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"weichuan/internal/nginxsnippet"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// 租户存储配置的「连通性自检」。
//
// 为什么这件事不能省：这张表装的是客户桶的地址与凭据，而在 P2（UploaderResolver）
// 落地之前，配置写错了**不会有任何东西报错**——上传走的还是平台那一个实例，
// 平台照常工作，直到某天切到该租户才发现图全裂。所以「填完点一下、当场知道通不通」
// 是这个功能唯一的反馈渠道。
//
// 为什么必须带凭据去探：S3 语义下，匿名请求对「不存在的桶」和「存在但无权访问」
// 返回的是同一个 403（防桶名枚举）。也就是说**不带签名的探测对 S3 没有判别力**，
// 探出 200 只说明域名解析通了。做一个「看起来验过了、其实没验凭据」的自检，
// 比不做更坏——它把「没验」伪装成「验过了」。
//
// 边界（写在这里，也要展示给运营）：本函数验证的是「凭据有效 + 桶可达」，
// **不验证接入层**。nginx 侧的 Host/SNI（R1 的 421）、CDN 缓存、证书链都不在
// 覆盖范围内——那是 tools/nginx-r1-check 与 snippet 生成器的职责。

// probeTimeout 单次自检的整体超时。
//
// 取 10s：S3 签名请求在跨区/跨境链路上 1–3s 常见，10s 足够区分「慢」与「不通」；
// 再长会让后台按钮转圈到用户以为卡死（Element Plus 按钮 loading 没有进度）。
const probeTimeout = 10 * time.Second

// ProbeBucket 用给定配置做一次真实的桶可达性 + 凭据校验。
//
// 返回的 detail 是**给运营看的一句话**（成功时），error 是给运营看的失败原因。
// 两者都不含凭据、不含桶方返回的原始 XML/JSON——S3 的 AccessDenied 响应会回显
// AccessKeyId 的片段，而那正是模型注释里 last_error「脱敏摘要」要挡住的东西。
//
// endpoint 是本表的约定形态：**host[:port]，不带 scheme**（它同时喂给 nginx snippet，
// 带了 scheme 会拼出 http://https://xxx）。OSS SDK 需要 scheme，这里自己补。
func ProbeBucket(driver, endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (string, error) {
	switch driver {
	case nginxsnippet.DriverLocal:
		// 本地驱动 = 「这个租户不用自己的桶」，没有可探的对端。
		// 报成功而不是报错：这是合法配置（切回平台盘的表达方式之一），
		// 把它标成 error 会让后台列表出现一片没有意义的红点。
		return "本地驱动：对象写在平台盘，不需要桶连通性检查", nil
	case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
		return probeS3Bucket(endpoint, accessKey, secretKey, bucket, region, useSSL)
	case nginxsnippet.DriverOSS:
		return probeOSSBucket(endpoint, accessKey, secretKey, bucket, useSSL)
	default:
		return "", fmt.Errorf("driver=%q 不在枚举内（local/minio/oss/s3）", driver)
	}
}

// probeS3Bucket 覆盖 MinIO 与所有 S3 兼容云（COS / OBS / 七牛 / AWS / R2）。
//
// 用 BucketExists（HEAD /bucket）而不是 GetBucketLocation 之类：HEAD 只要
// s3:ListBucket 或无权限时的 403，是最小的权限面；换成列举对象会让「只给了
// 写入权限、没给列举权限」的正常桶被误判成不可用。
func probeS3Bucket(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (string, error) {
	if endpoint == "" {
		return "", errors.New("endpoint 为空，先填桶的访问域名")
	}
	if bucket == "" {
		return "", errors.New("bucket 为空")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		// 这里只说「构造失败」不贴原始错误：minio.New 的报错会把 endpoint 原样带出来，
		// 而 endpoint 本身不是秘密，真正要避免的是把更多上下文塞进 last_error。
		return "", errors.New("客户端构造失败：endpoint 形态不对（这里只要 host[:port]，不要带 http:// 或路径）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	ok, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return "", fmt.Errorf("探测失败：%s", classifyS3ProbeErr(err))
	}
	if !ok {
		return "", fmt.Errorf("桶 %q 不存在，或当前凭据看不到它", bucket)
	}
	return fmt.Sprintf("凭据有效，桶 %s 可达（本结论不含 nginx 侧 Host/SNI 配置）", bucket), nil
}

// classifyS3ProbeErr 把 SDK 错误压成「运营看了知道该改什么」的一句话。
//
// 只取错误码与状态码，不取 Message / RequestID / Host：那些字段在排障时有用，
// 但它们会被原样存进 last_error 并在后台常驻显示，属于「一次写入、长期暴露」。
func classifyS3ProbeErr(err error) string {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		switch resp.Code {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Sprintf("凭据被拒绝（%s）——检查 AccessKey/SecretKey 是否属于这个桶的账号", resp.Code)
		case "NoSuchBucket":
			return "桶不存在（NoSuchBucket）——检查 bucket 名与 endpoint 是否属于同一区域"
		case "PermanentRedirect", "AuthorizationHeaderMalformed":
			return fmt.Sprintf("区域不匹配（%s）——填上 region，或确认 endpoint 是该桶所在区域", resp.Code)
		}
		return fmt.Sprintf("桶方返回 %s（HTTP %d）", resp.Code, resp.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("超时（%s）——endpoint 不通或被防火墙丢包", probeTimeout)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("域名解析失败（%s）——检查 endpoint 拼写", dnsErr.Name)
	}
	return "网络不可达或协议错误（原始信息未记录，避免把桶方响应写进库）"
}

// probeOSSBucket 覆盖阿里云 OSS。
//
// OSS 的 endpoint 与其 SDK 的约定与 minio 相反：SDK 要带 scheme。本表的 endpoint
// 不带 scheme（喂 nginx 用），所以这里按 useSSL 补一个。
func probeOSSBucket(endpoint, accessKey, secretKey, bucket string, useSSL bool) (string, error) {
	if endpoint == "" {
		return "", errors.New("endpoint 为空，先填桶的访问域名")
	}
	if bucket == "" {
		return "", errors.New("bucket 为空")
	}
	if strings.Contains(endpoint, "://") {
		return "", errors.New("endpoint 不要带 scheme（这里只要 host[:port]），是否走 HTTPS 由 useSsl 决定")
	}
	scheme := "https://"
	if !useSSL {
		scheme = "http://"
	}
	client, err := oss.New(scheme+endpoint, accessKey, secretKey)
	if err != nil {
		return "", errors.New("客户端构造失败：endpoint 形态不对（只要 host[:port]）")
	}
	ok, err := client.IsBucketExist(bucket)
	if err != nil {
		// OSS SDK 的错误里会带 RequestID 与完整 XML；一律不透出。
		return "", errors.New("凭据被拒绝或网络不可达（原始信息未记录）")
	}
	if !ok {
		return "", fmt.Errorf("桶 %q 不存在，或当前凭据看不到它", bucket)
	}
	return fmt.Sprintf("凭据有效，桶 %s 可达（本结论不含 nginx 侧 Host/SNI 配置）", bucket), nil
}
