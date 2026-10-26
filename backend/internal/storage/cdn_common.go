package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 本文件放各厂商 CDN 刷新实现之间的共享件：端点归一化、分批、带超时的 HTTP 客户端
// 与签名小工具。每家厂商各自的协议细节见 cdn_<vendor>.go。
//
// 设计前提（2026-10-23 补多家实现时统一）：
//  1. 刷新是**同步串在用户请求里**的（上传 / 素材采用 / 小程序码），厂商接口不响应
//     不能把用户请求挂死——所以统一走带超时的客户端，超时与失败一样只 [WARN] 记日志；
//  2. 每家的「单次提交条数上限」不同（20 / 30 / 1000…），统一由 cdnBatch 分批，
//     任一批失败立即返回，不再打后续批次（失败的刷新会在下一次覆盖写时重试，
//     继续打只是白白消耗厂商配额）；
//  3. 端点允许显式写 scheme，单测据此把流量导向 httptest 假服务器——否则这些实现
//     根本没法测（生产地址是硬编码的 https）。

// cdnHTTPTimeout 单次刷新请求的超时：刷新是 best-effort 的增强，宁可早早放弃。
const cdnHTTPTimeout = 15 * time.Second

var cdnHTTPClient = &http.Client{Timeout: cdnHTTPTimeout}

// cdnBaseURL 归一化服务端点：用户填的一般是裸域名（cdn.aliyuncs.com），补 https://；
// 已带 scheme 则原样使用（末尾斜杠去掉），单测据此指向 httptest。
func cdnBaseURL(endpoint, fallback string) string {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		e = fallback
	}
	return strings.TrimRight(func() string {
		if strings.Contains(e, "://") {
			return e
		}
		return "https://" + e
	}(), "/")
}

// cdnBatch 按厂商单次上限分批调用 once。
func cdnBatch(urls []string, limit int, once func([]string) error) error {
	if len(urls) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = len(urls)
	}
	for start := 0; start < len(urls); start += limit {
		end := start + limit
		if end > len(urls) {
			end = len(urls)
		}
		if err := once(urls[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// cdnDo 发出一次刷新请求并读回响应体；非 2xx 视为失败（错误里带响应体片段便于定位）。
// 返回的 body 在成功时同样给调用方解析厂商自己的业务错误码。
func cdnDo(req *http.Request) ([]byte, error) {
	resp, err := cdnHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cdn refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误无后果
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return body, fmt.Errorf("cdn refresh http %d: %s", resp.StatusCode, cdnSnippet(body))
	}
	return body, nil
}

// cdnSnippet 截断响应体，避免把厂商整页 HTML 塞进日志。
func cdnSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// hostOf 从端点取 Host（去掉 scheme 与路径），用于签名里的 host 头。
func hostOf(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	s := baseURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// ---- 签名小工具 ----

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hmacSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

// awsSignV4 按 AWS Signature Version 4 计算 Authorization 头（纯标准库）。
//
// 抽成通用函数而不是写在 CloudFront 实现里，是为了能拿 AWS 官方公布的签名测试向量
// 直接验证「派生密钥 + 规范请求」这两步——这两步写错的话，服务端只会回一句
// SignatureDoesNotMatch，本地根本无从定位（见 cdn_test.go 的官方向量用例）。
//
// headers 传参与签名的头（key 用小写，函数内按字典序拼接并生成 SignedHeaders）。
type awsSignV4Input struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	Service         string
	Method          string
	CanonicalURI    string
	Query           string            // 不含前导 ?，无查询串时传空
	Headers         map[string]string // 小写 key，必须含 host
	Payload         []byte
	Time            time.Time
}

type awsSignV4Result struct {
	Authorization string // 直接写 Authorization 头
	AmzDate       string // X-Amz-Date
	PayloadHash   string // X-Amz-Content-Sha256（body 的 SHA256 十六进制）
	SignedHeaders string
}

func awsSignV4(in awsSignV4Input) awsSignV4Result {
	now := in.Time.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(in.Payload)

	keys := make([]string, 0, len(in.Headers))
	for k := range in.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	signedHeaders := strings.Join(keys, ";")
	var canonHeaders strings.Builder
	for _, k := range keys {
		canonHeaders.WriteString(k)
		canonHeaders.WriteString(":")
		canonHeaders.WriteString(strings.TrimSpace(in.Headers[k]))
		canonHeaders.WriteString("\n")
	}

	canonical := strings.Join([]string{
		in.Method,
		in.CanonicalURI,
		in.Query,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := dateStamp + "/" + in.Region + "/" + in.Service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")

	k := hmacSHA256([]byte("AWS4"+in.SecretAccessKey), dateStamp)
	k = hmacSHA256(k, in.Region)
	k = hmacSHA256(k, in.Service)
	k = hmacSHA256(k, "aws4_request")
	signature := hmacSHA256Hex(k, stringToSign)

	return awsSignV4Result{
		Authorization: fmt.Sprintf(
			"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
			in.AccessKeyID, scope, signedHeaders, signature),
		AmzDate:       amzDate,
		PayloadHash:   payloadHash,
		SignedHeaders: signedHeaders,
	}
}

// awsURIEncode 按 SigV4 规则编码 URI 路径：非 [A-Za-z0-9-_.~] 与 / 之外的字符转 %XX。
// 厂商资源 ID（Distribution ID / Zone ID）都是字母数字，实际不会触发转义，
// 保留完整实现是为了路径里万一出现特殊字符时不至于签出与服务端不一致的串。
func awsURIEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' || c == '/' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
