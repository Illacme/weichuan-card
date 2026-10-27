// Package pii 提供个人可识别信息（PII）的「静态加密 + 盲索引」原语。
//
// 设计目标（与 C5 决策对齐）：
//   - 敏感字段在数据库里只存密文（AES-256-GCM，随机 nonce，密文带 "v1:" 版本前缀）；
//   - 需要等值查询的字段（手机号=登录账号、姓名/邮箱/昵称=后台搜索）额外存一列
//     HMAC-SHA256 盲索引，查询时比对盲索引、而不是明文或密文，从而做到
//     「库里看不到明文，但仍能按值精确检索」。
//
// 为什么不用确定性加密直接当索引用：确定性密文会泄露「哪些行取值相同」这一
// 结构信息，且一旦泄漏密钥则所有密文都可离线爆破。盲索引把「加密」与「可检索」
// 拆成两把独立派生的子密钥，职责更清晰、代价更可控。
//
// 密钥来源：环境变量 PII_ENC_KEY（base64，32 字节）。未设置时回落到内置 dev
// 密钥，仅供测试 / 本地联调——生产必须显式设置，否则 UsingPlaceholderKey()
// 返回 true，由启动自检拒绝（与 license 占位公钥同一套思路）。
package pii

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// KeyEnvVar 是覆盖内置 dev 密钥的环境变量名。
const KeyEnvVar = "PII_ENC_KEY"

// cipherPrefix 标识「本包产出的密文」。历史明文没有该前缀，Decrypt 会原样返回，
// 使未回填的存量行在读路径上仍可用（回填完成后即全部带前缀）。
const cipherPrefix = "v1:"

var (
	encKey      [32]byte
	idxKey      [32]byte
	keyErr      error
	placeholder bool
)

func init() { loadKeys() }

// loadKeys 读取环境变量中的密钥（缺失则回落内置 dev 密钥）并派生盲索引子密钥。
func loadKeys() {
	keyErr = nil
	placeholder = false
	if v := strings.TrimSpace(os.Getenv(KeyEnvVar)); v != "" {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			keyErr = fmt.Errorf("%s 不是合法 base64：%w", KeyEnvVar, err)
			return
		}
		if len(b) != 32 {
			keyErr = fmt.Errorf("%s 必须解码为 32 字节（AES-256），当前 %d 字节", KeyEnvVar, len(b))
			return
		}
		copy(encKey[:], b)
	} else {
		// 内置 dev 密钥：由固定串派生，测试 / 本地开箱即用。
		encKey = sha256.Sum256([]byte("weichuan-pii-dev-key-v1"))
		placeholder = true
	}
	// 盲索引子密钥：从主密钥域分离派生，避免与加密直接共用同一把钥匙。
	h := sha256.New()
	h.Write(encKey[:])
	h.Write([]byte("|blind-index"))
	copy(idxKey[:], h.Sum(nil))
}

// UsingPlaceholderKey 报告当前是否在用内置 dev 密钥。生产启动自检应据此拒绝
// （与 license.IsPlaceholderPublicKey 同一思路：带占位密钥上线 = 加密形同虚设）。
func UsingPlaceholderKey() bool { return placeholder }

// Reload 重新读取环境变量并重新派生子密钥。
//
// 为什么需要它：包级 init 早于 cmd/server 的配置加载，而安装向导是把
// PII_ENC_KEY 写进**配置文件**（runtime.env）的——init 跑完时那份值还不存在，
// 于是「装完就有真密钥」只会在环境变量形态下成立。main 把配置文件的值
// 补进环境变量后调用本函数，两种部署形态才一致。
func Reload() { loadKeys() }

// KeyError 返回密钥加载错误（配置了 PII_ENC_KEY 但格式/长度不对时非 nil）。
// 此时 Encrypt/Decrypt 会直接返回错误——宁可让写入/读取显式失败，也不静默降级。
func KeyError() error { return keyErr }

// IsEncrypted 判断某个库值是否已是本包产出的密文（用于回填时跳过已加密行）。
func IsEncrypted(v string) bool { return strings.HasPrefix(v, cipherPrefix) }

// Encrypt 加密明文。空串视为「无此信息」，直接返回空串（不产生密文）。
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if keyErr != nil {
		return "", keyErr
	}
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败：%w", err)
	}
	// Seal 把 nonce 作为前缀一并输出，解密时再切出来。
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return cipherPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt 解密密文。
//
// 兼容语义：不带 "v1:" 前缀的值按「历史明文」原样返回——这样回填前的老数据
// 也能正常读出来（回填见 storage 层/启动自检 Backfill）。真正的密文解密失败
// （密钥不对 / 数据被篡改）返回错误，绝不把密文或错误明文交给上层。
func Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if !strings.HasPrefix(enc, cipherPrefix) {
		return enc, nil // 历史明文（尚未回填）
	}
	if keyErr != nil {
		return "", keyErr
	}
	raw, err := base64.StdEncoding.DecodeString(enc[len(cipherPrefix):])
	if err != nil {
		return "", fmt.Errorf("密文不是合法 base64：%w", err)
	}
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文长度异常（%d 字节）", len(raw))
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥不符或数据被篡改）：%w", err)
	}
	return string(plain), nil
}

// BlindIndex 计算可检索字段的盲索引（HMAC-SHA256 → 小写十六进制）。
//
// 归一化：去首尾空白 + 转小写 + 去掉内部所有空白。这样「张三」「 张三 」「张 三」
// 以及手机号里夹的空格都能命中同一条。空值返回空串（不建索引）。
func BlindIndex(value string) string {
	v := normalize(value)
	if v == "" || keyErr != nil {
		return ""
	}
	mac := hmac.New(sha256.New, idxKey[:])
	mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}

// normalize 统一盲索引参与计算的字符串形态。
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// 去掉内部空白（Fields 按任意空白切分，再拼回去即等价于删除所有空白）
	return strings.Join(strings.Fields(s), "")
}
