package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// DefaultLicensePath 默认许可证文件路径，可被 LICENSE_PATH 环境变量覆盖。
// 与 data/ 目录约定（runtime.env、SQLite 库同处）保持一致。
const DefaultLicensePath = "data/license.key"

// FileResolver 从本地签名许可证文件解析授权（私有 / 独立部署用）。
// 文件两行：第 1 行 = base64(JSON 载荷)，第 2 行 = base64(Ed25519 签名)。
// 验签公钥来自 defaultPublicKey()（内嵌，可被 SetPublicKey 覆盖）。
type FileResolver struct {
	pub  ed25519.PublicKey
	path string
}

// NewFileResolver 显式构造（测试 / 签发工具注入公钥与路径）。
func NewFileResolver(pub ed25519.PublicKey, path string) *FileResolver {
	return &FileResolver{pub: pub, path: path}
}

// DefaultFileResolver 用内嵌公钥与默认（或 LICENSE_PATH 指定的）路径构造。
func DefaultFileResolver() *FileResolver {
	p := DefaultLicensePath
	if v := os.Getenv("LICENSE_PATH"); v != "" {
		p = v
	}
	return &FileResolver{pub: defaultPublicKey(), path: p}
}

// Resolve 读取并验签许可证文件。文件缺失视为 ErrNotFound（调用方据此决定是否致命）。
func (r *FileResolver) Resolve(_ context.Context, _ int64) (*Entitlement, error) {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, r.path)
		}
		return nil, fmt.Errorf("license: read %s: %w", r.path, err)
	}
	return ParseLicense(r.pub, raw)
}

// ParseLicense 解析并验签一份许可证内容。
func ParseLicense(pub ed25519.PublicKey, raw []byte) (*Entitlement, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("%w: need payload and signature lines", ErrInvalidFormat)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[0]))
	if err != nil {
		return nil, fmt.Errorf("%w: payload not base64: %v", ErrInvalidFormat, err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, fmt.Errorf("%w: signature not base64: %v", ErrInvalidFormat, err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return nil, ErrBadSignature
	}
	var e Entitlement
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}
	if e.IsExpired() {
		return nil, fmt.Errorf("%w: expiresAt=%s", ErrExpired, e.ExpiresAt.Format(time.RFC3339))
	}
	return &e, nil
}

// SignLicense 用私钥给授权签名，产出可直接写入文件的文本内容（两行 base64）。
// 仅签发工具使用；私钥绝不进仓库 / 二进制。
func SignLicense(priv ed25519.PrivateKey, e *Entitlement) (string, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	return base64.StdEncoding.EncodeToString(payload) + "\n" +
		base64.StdEncoding.EncodeToString(sig), nil
}

// GenerateKeyPair 生成 Ed25519 密钥对，返回 base64 编码的公钥 / 私钥。
// 公钥内嵌进二进制（替换 embeddedPublicKeyB64），私钥留在签发工具。
func GenerateKeyPair() (pubB64, privB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}
