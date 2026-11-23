package storageprofile

import (
	"errors"
	"fmt"
	"strings"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
)

// uploaderConfig 是构造一个存储实例所需的全部输入（凭据已解密）。
//
// 为什么不用 model.StorageProfile：那个类型里有自检状态、builtin 标记、版本号
// 这类与构造无关的字段。用独立类型，缺什么就编译不过，顺带保证明文凭据
// 不会被顺手带进别的结构体里被 %+v 打出来。
type uploaderConfig struct {
	CompanyID int64
	Driver    string
	Endpoint  string
	Bucket    string
	Region    string
	UseSSL    bool
	// HostHeader 决定 S3 客户端按路径风格还是虚拟主机风格发请求（真桶实测：
	// 漏了它会被 400 InvalidBucketName 拒掉，而后台看起来全对）。
	// 上传侧与回填侧共用 storage.S3BucketLookup 这一个判定，不许任一侧自己猜。
	HostHeader string
	AccessKey  string
	SecretKey  string
	// BaseURL 是对象的**上传返回基址**。写入侧 normalizeResourceRef 用它把
	// 上传返回的 URL 剥回裸键，所以它必须是平台统一入口，不能换成客户域名——
	// 一旦换成客户域名就剥不掉基址，URL 里也没有 /uploads/ 段，
	// NormalizeOrKeep 会把它判成外链原样写库，media_check 反推键失败。
	BaseURL string
	// PublicRead 桶是否公开读。私有桶（false）仅用于**租户云档案**：
	// 出参仍是稳定的 /uploads/t/<cid>/...，读侧由后端现签 GET URL 后 302 回源
	// （handler.PrivateObjectRedirect），不需要前端/小程序感知签名过期。
	//
	// 平台自身档案（Purpose=platform_primary）必须公开读：p/ 对象没有逐租户
	// 的回源 location，私有化之后只能靠应用层鉴权，而公开名片页上的素材是匿名的。
	PublicRead bool
	// Purpose 只对内置档案非空。带上它是为了堵住「库里手工把平台档案改成
	// public_read=0」——否则配置位与实际行为脱钩却没有任何报错。
	Purpose string
}

// 单文件上限的兜底值。运行时会被 storage.UploadLimitBytes（系统设置
// upload_max_mb）覆盖，见 checkFile；这里的常量只为对齐平台实例的构造口径。
const maxBytes = 10 * 1024 * 1024

// buildUploader 按配置构造实例。校验只做「能构造出来」这一层，字段格式
// （endpoint 不带 scheme、bucket 不含路径）已经在写入接口用
// nginxsnippet.Target.Validate 单点验过；在读取侧再验一遍等于把同一套规则
// 写两份，迟早漂移成「接口说存成功、这里报错」。
func buildUploader(cfg *uploaderConfig) (storage.Uploader, error) {
	// 平台自身那份必须公开读；租户私有桶的读侧由签名跳转接管，不在这里拒。
	if !cfg.PublicRead && cfg.Purpose == model.ProfilePurposePlatformPrimary {
		return nil, errors.New(
			"平台自身存储必须开启公开读（public_read=true）：p/ 平台对象走匿名静态入口，" +
				"没有租户级的签名跳转；私有化请改用绑定到企业的文件存储")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New(
			"上传返回基址为空：写入侧要据此把上传结果剥回对象键（存裸键），" +
				"否则存进库的引用是坏的；私有桶同样需要这一列")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("凭据不完整（AccessKey / SecretKey 缺一）：请到文件存储里补齐")
	}
	switch cfg.Driver {
	// minio 与 s3 共用同一套 S3 客户端，与 cmd/server 的 newUploader 口径一致：
	// 枚举分开只为界面归属说明，region 对两者都生效。
	case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
		if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
			return nil, fmt.Errorf("driver=%s 要求 endpoint 与 bucket 都非空", cfg.Driver)
		}
		// Enabled 置 true 不是替配置做决定：调用方只在「这是云驱动」时才进来。
		// 这里必须让它为真，否则 Target.Validate 会走「不需要 snippet」的早退分支，
		// 把 host_header / endpoint / bucket 的校验全跳过——那就等于把路径风格判定
		// 又交回给 SDK 猜。
		bl, err := storage.S3BucketLookup(nginxsnippet.Target{
			CompanyID:  cfg.CompanyID,
			Driver:     cfg.Driver,
			Endpoint:   cfg.Endpoint,
			Bucket:     cfg.Bucket,
			UseSSL:     cfg.UseSSL,
			HostHeader: cfg.HostHeader,
			Enabled:    true,
		})
		if err != nil {
			return nil, fmt.Errorf("企业 %d 的桶地址不合法：%w", cfg.CompanyID, err)
		}
		return storage.NewMinioUploader(cfg.Endpoint, cfg.AccessKey, cfg.SecretKey,
			cfg.Bucket, cfg.BaseURL, cfg.Region, cfg.UseSSL, maxBytes, storage.DefaultAllowExt, bl)
	case nginxsnippet.DriverOSS:
		if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
			return nil, fmt.Errorf("driver=%s 要求 endpoint 与 bucket 都非空", cfg.Driver)
		}
		// 档案里的 endpoint 是 host[:port]（写入接口已挡掉带 scheme 的填法），
		// 而 OSS SDK 自己要 scheme。与 storage.ProbeBucket 同一处理，别在这里另立规则。
		ep := strings.TrimSpace(cfg.Endpoint)
		if !strings.Contains(ep, "://") {
			if cfg.UseSSL {
				ep = "https://" + ep
			} else {
				ep = "http://" + ep
			}
		}
		return storage.NewOssUploader(ep, cfg.AccessKey, cfg.SecretKey,
			cfg.Bucket, cfg.BaseURL, maxBytes, storage.DefaultAllowExt)
	case nginxsnippet.DriverLocal:
		// local 档案不该走到这里（调用方在 Driver==local 时直接返回本地实例）。
		// 走到这里说明有人绕过调用方直接喊 buildUploader，属于代码错误，报出来而不是猜。
		return nil, errors.New("driver=local 不该构造云实例（应直接返回本地上传器）")
	default:
		return nil, fmt.Errorf("driver=%q 不在枚举内（minio/oss/s3）", cfg.Driver)
	}
}
