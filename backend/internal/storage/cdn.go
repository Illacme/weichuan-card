package storage

import (
	"fmt"
	"strings"
)

// 支持的 CDN 厂商。取值与「分发档案」的 provider 枚举、前端下拉选项共用同一份
// （迁移 0060 之前是设置项 cdn_provider），新增厂商只需：① 加常量 + 进 CDNProviders
// ② 实现 CDNRefresher ③ 在 NewCDNRefresher 里加一个 case ④ 在分发档案表单里加选项
// 与各厂商凭据说明。
const (
	CDNProviderAliyun     = "aliyun"     // 阿里云 CDN
	CDNProviderTencent    = "tencent"    // 腾讯云 CDN
	CDNProviderQiniu      = "qiniu"      // 七牛云融合 CDN
	CDNProviderCloudflare = "cloudflare" // Cloudflare
	CDNProviderCloudFront = "cloudfront" // AWS CloudFront
	// CDNProviderNone 是分发档案里「明确不刷新」的取值：不是厂商，但需要与
	// 「没配」（空串按阿里云处理）区分开——空串是老配置的兼容语义，none 是
	// 管理员的明确选择，不能混为一谈。
	CDNProviderNone = "none"
)

// CDNProviders 厂商枚举（按界面展示顺序）
var CDNProviders = []string{
	CDNProviderAliyun, CDNProviderTencent, CDNProviderQiniu,
	CDNProviderCloudflare, CDNProviderCloudFront,
}

// CDNProviderLabels 厂商中文名（界面下拉与日志用）
var CDNProviderLabels = map[string]string{
	CDNProviderAliyun:     "阿里云 CDN",
	CDNProviderTencent:    "腾讯云 CDN",
	CDNProviderQiniu:      "七牛云融合 CDN",
	CDNProviderCloudflare: "Cloudflare",
	CDNProviderCloudFront: "AWS CloudFront",
}

// CDNDefaultEndpoints 各厂商的刷新接口默认端点（配置留空即用该值）
var CDNDefaultEndpoints = map[string]string{
	CDNProviderAliyun:     "cdn.aliyuncs.com",
	CDNProviderTencent:    "cdn.tencentcloudapi.com",
	CDNProviderQiniu:      "fusion.qiniuapi.com",
	CDNProviderCloudflare: "api.cloudflare.com",
	CDNProviderCloudFront: "cloudfront.amazonaws.com",
}

// CDNProviderNeedsResource 该厂商是否必须填「资源标识」：
// Cloudflare 要 Zone ID、CloudFront 要 Distribution ID，AK/SK 三家不需要
// （它们从提交 URL 的域名自己定位加速域名）。
func CDNProviderNeedsResource(provider string) bool {
	switch normalizeCDNProvider(provider) {
	case CDNProviderCloudflare, CDNProviderCloudFront:
		return true
	}
	return false
}

// CDNProviderNeedsKeyPair 该厂商是否需要 AK/SK：Cloudflare 只用 API Token，不需要 AK。
func CDNProviderNeedsKeyPair(provider string) bool {
	return normalizeCDNProvider(provider) != CDNProviderCloudflare
}

// CDNConfig 厂商无关的刷新配置：由 config（环境变量）经 setting 覆盖后填好传入。
// 同一组字段在不同厂商下含义不同，见 NewCDNRefresher 与各实现的注释。
type CDNConfig struct {
	Provider        string // 厂商标识，留空按 aliyun（兼容既有部署）
	AccessKeyID     string // AK：阿里云 AccessKeyId / 腾讯云 SecretId / 七牛 AccessKey / CloudFront AccessKeyId
	AccessKeySecret string // SK：阿里云 Secret / 腾讯云 SecretKey / 七牛 SecretKey / CloudFront SecretAccessKey / Cloudflare API Token
	Endpoint        string // 刷新接口端点，留空按厂商取默认值
	ResourceID      string // 资源标识：Cloudflare Zone ID / CloudFront Distribution ID
}

// NewCDNRefresher 按厂商构造刷新器；provider 为空按 aliyun（既有部署没有这一项）。
//
// 配置不全或厂商未知时返回 (nil, error)：调用方只记一行日志、不启用刷新——
// **绝不能因为 CDN 配错就让服务起不来**（刷新是可选增强，主流程不依赖它）。
func NewCDNRefresher(c CDNConfig) (CDNRefresher, error) {
	switch normalizeCDNProvider(c.Provider) {
	case CDNProviderAliyun:
		if c.AccessKeyID == "" || c.AccessKeySecret == "" {
			return nil, fmt.Errorf("阿里云 CDN 需要填 AccessKeyId 与 AccessKeySecret")
		}
		return NewAliyunCDNRefresher(c.AccessKeyID, c.AccessKeySecret, c.Endpoint), nil

	case CDNProviderTencent:
		if c.AccessKeyID == "" || c.AccessKeySecret == "" {
			return nil, fmt.Errorf("腾讯云 CDN 需要填 SecretId（AccessKeyId）与 SecretKey（AccessKeySecret）")
		}
		return NewTencentCDNRefresher(c.AccessKeyID, c.AccessKeySecret, c.Endpoint), nil

	case CDNProviderQiniu:
		if c.AccessKeyID == "" || c.AccessKeySecret == "" {
			return nil, fmt.Errorf("七牛云 CDN 需要填 AccessKey 与 SecretKey")
		}
		return NewQiniuCDNRefresher(c.AccessKeyID, c.AccessKeySecret, c.Endpoint), nil

	case CDNProviderCloudflare:
		// Cloudflare 只有 Token（放 Secret 字段），另有 Zone ID（放资源标识）
		if c.AccessKeySecret == "" || c.ResourceID == "" {
			return nil, fmt.Errorf("Cloudflare 需要填 API Token（AccessKeySecret）与 Zone ID（资源标识）")
		}
		return NewCloudflareCDNRefresher(c.AccessKeySecret, c.ResourceID, c.Endpoint), nil

	case CDNProviderCloudFront:
		if c.AccessKeyID == "" || c.AccessKeySecret == "" || c.ResourceID == "" {
			return nil, fmt.Errorf("CloudFront 需要填 AccessKeyId / SecretAccessKey 与 Distribution ID（资源标识）")
		}
		return NewCloudFrontCDNRefresher(c.AccessKeyID, c.AccessKeySecret, c.ResourceID, c.Endpoint, ""), nil

	default:
		return nil, fmt.Errorf("未知 CDN 厂商：%q（可选：%s）", c.Provider, strings.Join(CDNProviders, " / "))
	}
}

// CDNDefaultEndpoint 取厂商默认刷新端点（配置留空时实际用这个值，日志与界面提示用）。
// 未知厂商返回空串。
func CDNDefaultEndpoint(provider string) string {
	return CDNDefaultEndpoints[normalizeCDNProvider(provider)]
}

// CDNProviderLabel 取厂商中文名；未知厂商回退原值，空值按 aliyun。
func CDNProviderLabel(provider string) string {
	if l, ok := CDNProviderLabels[normalizeCDNProvider(provider)]; ok {
		return l
	}
	return provider
}

// normalizeCDNProvider 归一化厂商标识：大小写与空格容错，空值回退 aliyun。
func normalizeCDNProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return CDNProviderAliyun
	}
	return p
}
