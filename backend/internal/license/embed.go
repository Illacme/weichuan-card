package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"sync"
)

// embeddedPublicKeyB64 是内嵌的授权验签公钥（Ed25519）。发行方用配对的私钥给许可证签名，
// 二进制内置这份公钥做验签——公钥本就可公开，内嵌不会泄露任何机密。
//
// 生产分发前请把下面这行替换成你们自己签发用的公钥：
//
//	用 GenerateKeyPair() 生成，私钥留在签发工具里、绝不进仓库 / 二进制；
//	公钥 base64 填到这里。当前是占位演示密钥，仅用于本地把整条链路跑通。
//
// 也可以不改代码：把真公钥放进环境变量 LICENSE_PUB（base64）即可覆盖内嵌值，
// 优先于这里的内嵌公钥（见 defaultPublicKey）。这样发布流水线可以在构建时
// 注入真公钥，不必为了换一把钥匙就改源码重新编译。
var embeddedPublicKeyB64 = "l2Ev4Iq9hLJ5yYprKNwtpvFWo5UldZRp0X2HfO0+N9o="

// placeholderPublicKeyB64 是上面那个占位演示密钥的原文。
//
// 单独记一份是为了**识别**它：占位密钥意味着「谁都能伪造一张许可证」——
// 因为配套的 demo 私钥就写在签发工具的注释里，理论上可被推导。
// 发布前必须换掉（见 IsPlaceholderPublicKey）。
const placeholderPublicKeyB64 = "l2Ev4Iq9hLJ5yYprKNwtpvFWo5UldZRp0X2HfO0+N9o="

// PubKeyEnvVar 是覆盖内嵌公钥的环境变量名。
//
// 为什么要有这条：内嵌公钥是**编译期**常量，换一把钥匙就要改源码 + 重新编译 + 重发二进制。
// 私有部署的交付形态恰恰是「给客户一个二进制 + 一份许可证」——
// 密钥要在交付时能注入，而不是焊死在代码里。
const PubKeyEnvVar = "LICENSE_PUB"

var (
	pubMu       sync.RWMutex
	overridePub ed25519.PublicKey // 测试 / 签发工具注入用，生产一般不设置
)

// SetPublicKey 覆盖默认验签公钥（测试 / 签发工具注入）。生产路径不使用。
func SetPublicKey(pub ed25519.PublicKey) {
	pubMu.Lock()
	defer pubMu.Unlock()
	overridePub = pub
}

// DefaultPublicKey 导出当前生效的验签公钥，供签发 / 运维工具（cmd/license）
// 的 show 子命令验签展示一份许可证文件。
func DefaultPublicKey() ed25519.PublicKey { return defaultPublicKey() }

// defaultPublicKey 返回当前生效的公钥，优先级：
//  1. SetPublicKey 注入值（测试 / 签发工具）；
//  2. 环境变量 LICENSE_PUB（发布时注入，优先于内嵌）；
//  3. 内嵌公钥（编译期常量）。
//
// 环境变量优先于内嵌的理由：内嵌是「源码里写死的默认值」，环境变量是
// 「这次部署实际生效的值」——后者更具体，理应优先。
func defaultPublicKey() ed25519.PublicKey {
	pubMu.RLock()
	p := overridePub
	pubMu.RUnlock()
	if p != nil {
		return p
	}
	if v := strings.TrimSpace(os.Getenv(PubKeyEnvVar)); v != "" {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b)
		}
		// 环境变量写错了（不是合法 base64 或长度不对）时**不静默**：
		// 落到内嵌公钥意味着「用了一把可能不该用的钥匙验签」，
		// 而调用方以为用的是环境变量那把——错了却无从察觉。
		// 这里退回内嵌值，但由调用方（IsPlaceholderPublicKey / 启动自检）报出来。
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(embeddedPublicKeyB64)
	if err != nil {
		return nil
	}
	return ed25519.PublicKey(b)
}

// IsPlaceholderPublicKey 判断当前生效的公钥是否仍是占位演示密钥。
//
// 用于发布前的自检：带着占位密钥发布的二进制，等于「任何拿到 demo 私钥的人
// 都能伪造一张永久 / 无限席位的许可证」，授权机制形同虚设。
//
// 返回 true 表示**应该**被拦住（当前是占位密钥，或公钥根本没解析出来）。
func IsPlaceholderPublicKey() bool {
	pub := DefaultPublicKey()
	if pub == nil {
		// 公钥解析不出来（环境变量写错 / 内嵌值损坏）：验签必然失败，
		// 与其让人拿到一个「永远验不过」的二进制，不如明确报出来。
		return true
	}
	return base64.StdEncoding.EncodeToString(pub) == placeholderPublicKeyB64
}
