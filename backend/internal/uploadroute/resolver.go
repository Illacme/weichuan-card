// Package uploadroute 按租户把上传路由到该客户自己的对象桶。
//
// 它是 tenant_storage_configs（迁移 0044）与「真正写文件」之间唯一的一跳：
// 第六十轮之前那张表只是「能读、能写、能自检」，一张配置表；
// 从这里开始，上传、直传预签名、违规图下架覆盖三条写路径会按 company_id 取实例。
//
// 三条边界决定了本包为什么长这样，改之前请先读完：
//
//  1. **无静默降级**。配置行存在、enabled=true，但凭据解密失败 / 基址为空 /
//     驱动不在枚举内——一律返回 error，**绝不**换成平台实例顶上。客户以为文件
//     在自己的桶里、实际躺在平台盘上，事发前没有任何人会发现。
//     「没配 / 没启用 / driver=local」是另一回事：那是明确配置成走平台，不是降级。
//  2. **判定复用 nginxsnippet.Target.NeedsSnippet**。Go 侧「要不要用租户桶」与
//     nginx 侧「要不要生成回源段」必须是同一个判定。各写一份的结果是：
//     上传写进客户桶、nginx 却还在从平台盘回源，全站图 404 而后台配置显示正常。
//  3. **实例按 company_id + updated_at 缓存**。S3 客户端构造要建连接池，
//     每次上传都重建等于把每个请求都变成一次 TLS 握手；但配置改了必须立刻生效，
//     所以缓存版本取 updated_at（第六十轮的 PUT 走 gorm Updates，会自动刷新该列）。
//     多实例部署下每个进程各有一份缓存，版本号比对同样成立。
package uploadroute

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/pii"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// Config 是构造一家租户的存储实例所需的全部输入（凭据已解密）。
//
// 为什么不用 model.TenantStorageConfig：那个类型里有密文列、有自检状态、
// 有 KeyID 这类与本包无关的字段。用独立类型，缺什么就编译不过，
// 顺带保证明文凭据不会被顺手带进别的结构体里被 %+v 打出来。
type Config struct {
	CompanyID int64
	Driver    string
	Endpoint  string
	Bucket    string
	Region    string
	UseSSL    bool
	// HostHeader 决定 S3 客户端按路径风格还是虚拟主机风格发请求（第六十三轮真桶实测）。
	// 上传侧与回填侧共用 storage.S3BucketLookup 这一个判定，不许任一侧自己猜。
	HostHeader string
	AccessKey  string
	SecretKey  string
	// BaseURL 是该租户对象的**上传返回基址**，恒为平台统一入口
	// （cfg.UploadBaseURL），由平台 nginx 按 t/<cid>/ 前缀回源到客户桶，见设计稿 §3.4。
	//
	// 绝不能拿 public_base_url 覆盖它：写入侧 normalizeResourceRef 用平台
	// cfg.UploadBaseURL 把上传返回的 URL 剥回裸键（storage.legacyRel）。一旦换成
	// 客户域名，剥不掉基址、URL 里也没有 /uploads/ 段，NormalizeOrKeep 会把它
	// 判成外链**原样写库**——库里从裸键退回绝对 URL，media_check 反推键失败。
	// 这个雷在 TENANT_STORAGE_ROUTE=off 时不响，翻开关那天才响。
	BaseURL string
	// PublicOrigin 是客户自有访问域名（public_base_url）的归一化值。
	//
	// 它属于**访客侧 / 出参侧**，回答「端上 URL 以哪个域名开头」；与回源侧的
	// endpoint / host_header 正交。它**不参与实例构造**（上传返回基址恒为 BaseURL），
	// build 只校验它的格式；真正按租户选 origin 的判定在
	// handler.publicOriginState + storage.ResolveRefForTenant（第六十九轮接入），
	// 读的是同一张配置行。本包把值带进 Config 是为了让上传侧与出参侧对账时不缺字段。
	PublicOrigin string
	// PublicRead 桶是否公开读。第一阶段只支持公开读桶：私有桶需要预签名链路，
	// 出参契约（存裸键还是存 URL）会跟着变，本期没做。
	//
	// 把它读进来并在 build 里显式拒绝，是为了堵住「库里被人手工改成 0」：
	// 在此之前这一列只有写入侧校验会看，运行时完全不读，于是配置位与实际
	// 行为脱钩却没有任何报错。回填侧不调 build，不受影响（私有桶照样能写对象）。
	PublicRead bool
	// Version 用于缓存失效，取行的 updated_at 纳秒值。
	Version int64
}

// Loader 按企业读取生效配置。
//
// 三态必须彼此可区分，这是本类型存在的全部理由：
//
//	(nil, nil)  → 这家没有生效配置（没配 / 没启用 / driver=local），走平台实例
//	(nil, err)  → 读库或解密失败，调用方必须失败，不得回退
//	(cfg, nil)  → 用这家自己的桶
//
// 若把第一种和第二种混成「反正都返回 nil」，故障时上传会安静地落到平台盘上。
type Loader func(cid int64) (*Config, error)

// Resolver 是 storage.Resolver 的租户实现。
type Resolver struct {
	platform       storage.Uploader
	platformDriver string
	load           Loader

	mu    sync.RWMutex
	cache map[int64]entry
}

type entry struct {
	version int64
	up      storage.Uploader
}

// New 构造租户路由解析器。platform 是平台自身那一份实例（client 为 nil 时
// 所有租户都会退到平台，见 NewStaticResolver 的用途；这里不做 nil 兜底，
// 因为「租户桶构造失败」必须报错而不是借用 nil）。
func New(platform storage.Uploader, platformDriver string, load Loader) *Resolver {
	return &Resolver{
		platform:       platform,
		platformDriver: platformDriver,
		load:           load,
		cache:          make(map[int64]entry),
	}
}

// For 见 storage.Resolver.For。三种分支的语义在 Loader 的注释里，务必保持一致。
func (r *Resolver) For(cid int64) (storage.Uploader, error) {
	if cid <= 0 || r.load == nil {
		return r.Platform(), nil
	}
	cfg, err := r.load(cid)
	if err != nil {
		// 这里刻意不改写错误里的「企业 id」之外的任何内容：解密失败的原因
		// （密钥不符 / 数据被篡改）要原样上去，运维才分得清是配置问题还是密钥问题。
		return nil, fmt.Errorf("企业 %d 的自有存储读取失败：%w", cid, err)
	}
	if cfg == nil {
		return r.Platform(), nil
	}
	if up, ok := r.lookup(cid, cfg.Version); ok {
		return up, nil
	}
	up, err := build(cfg)
	if err != nil {
		// 关键：不回退平台实例。载荷里写清「没有退回平台存储」，
		// 否则用户看到「上传失败」只会以为网络问题，反复重试。
		return nil, fmt.Errorf(
			"企业 %d 的自有存储不可用（已按要求不回退平台存储，以免文件落错位置）：%w", cid, err)
	}
	r.store(cid, cfg.Version, up)
	return up, nil
}

func (r *Resolver) Platform() storage.Uploader { return r.platform }

// DriverFor 见 storage.Resolver.DriverFor。取不到配置时回平台驱动：
// 它只喂界面文案，任何写入路径都不看它。真正的失败会在 For 里报出来。
func (r *Resolver) DriverFor(cid int64) string {
	if cid <= 0 || r.load == nil {
		return r.platformDriver
	}
	cfg, err := r.load(cid)
	if err != nil || cfg == nil {
		return r.platformDriver
	}
	return cfg.Driver
}

func (r *Resolver) lookup(cid, version int64) (storage.Uploader, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.cache[cid]
	if !ok || e.version != version {
		return nil, false
	}
	return e.up, true
}

func (r *Resolver) store(cid, version int64, up storage.Uploader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 并发首访时会重复构造同一个实例并覆盖写一次：两条都等价，覆盖无害，
	// 比在锁里做 TLS 握手（会阻塞其它租户的上传）划算得多。
	r.cache[cid] = entry{version: version, up: up}
}

// 单文件上限的兜底值。运行时会被 storage.UploadLimitBytes（系统设置
// upload_max_mb）覆盖，见 checkFile；这里的常量只为对齐平台实例的构造口径。
const maxBytes = 10 * 1024 * 1024

// build 按配置构造实例。校验只做「能构造出来」这一层，字段格式（endpoint 不带
// scheme、bucket 不含路径）已经在写入接口用 nginxsnippet.Target.Validate 单点验过；
// 在读取侧再验一遍等于把同一套规则写两份，迟早漂移成「接口说存成功、这里报错」。
func build(cfg *Config) (storage.Uploader, error) {
	// 库里的自有域名可能来自第六十八轮之前的手工 SQL，未必过了写入侧校验。
	// 开关翻开后让它安静地躺着等于「配置看着正常、将来拼出怪 URL」，故在此显式报错。
	if _, err := storage.ValidatePublicOrigin(cfg.PublicOrigin); err != nil {
		return nil, fmt.Errorf("企业 %d 的自有访问域名不合法：%w", cfg.CompanyID, err)
	}
	// 私有桶没有预签名链路，继续按公开读拼 URL 会得到一个「上传成功、端上全 403」
	// 的结果。这里必须拒，而不是忽略这一列——忽略正是本轮要修的脱钩。
	if !cfg.PublicRead {
		return nil, errors.New(
			"桶未开启公开读（public_read=false）：第一阶段只支持公开读桶，" +
				"私有桶需要预签名链路、出参契约会跟着变，本期未实现；不按公开读拼 URL")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New(
			"对象公开访问基址为空：云存储必须能拼出可访问 URL，否则存进库的引用是坏的（图会全裂）")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("凭据不完整（AccessKey / SecretKey 缺一）：请到企业管理的「存储」里补齐")
	}
	switch cfg.Driver {
	// minio 与 s3 共用同一套 S3 客户端，与 cmd/server 的 newUploader 口径一致：
	// 枚举分开只为界面归属说明，region 对两者都生效。
	case nginxsnippet.DriverMinio, nginxsnippet.DriverS3:
		if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
			return nil, fmt.Errorf("driver=%s 要求 endpoint 与 bucket 都非空", cfg.Driver)
		}
		// Enabled 置 true 不是替配置做决定：DBLoader 只在 Target.NeedsSnippet()
		// 为真时才返回 cfg。这里必须让它为真，否则 Target.Validate 会走
		// 「不需要 snippet」的早退分支，把 host_header / endpoint / bucket 的
		// 校验全跳过——那就等于把路径风格判定又交回给 SDK 猜。
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
		// 表里的 endpoint 是 host[:port]（不带 scheme，写入接口已挡掉带 scheme 的填法），
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
		// NeedsSnippet 已把 local 判成「不用自家桶」，正常进不到这里。
		// 走到这里说明有人绕过 Loader 直接喊 build，属于代码错误，报出来而不是猜。
		return nil, errors.New("driver=local 不该构造租户实例（该走平台存储，见 Target.NeedsSnippet）")
	default:
		return nil, fmt.Errorf("driver=%q 不在枚举内（minio/oss/s3）", cfg.Driver)
	}
}

// DBLoader 从 tenant_storage_configs 读一家企业的配置。
//
// platformBase 是平台统一入口基址（cfg.UploadBaseURL），**恒**作为 Config.BaseURL。
// 第六十八轮之前这里会被 public_base_url 覆盖，那是把「访客侧 origin」错当成
// 「上传返回基址」——写入侧剥不回裸键，详见 Config.BaseURL 的注释。
// public_base_url 现在只登记进 Config.PublicOrigin，不参与实例构造。
//
// 只查单行且走 company_id 唯一索引；凭据列在此处解密，明文不出本函数之外
// （Config 只在本包 build 里被消费）。
func DBLoader(db *gorm.DB, platformBase string) Loader {
	return func(cid int64) (*Config, error) {
		var row model.TenantStorageConfig
		// 刻意用 Limit(1).Find 而不是 First：First 在「没有这行」时会让 gorm
		// 默认 logger 打一条 `record not found`，而「这家没配自有桶」是 Loader
		// 三态里**完全正常**的一态（每个走平台盘的租户每次上传都会命中）。
		// 把正常路径打成错误日志，运维会以为配置读挂了；第六十六轮真桶灰度
		// 取证日志里就出现了这条噪音。
		tx := db.Where("company_id = ?", cid).Limit(1).Find(&row)
		if tx.Error != nil {
			// 表不存在（迁移 0044 未应用）也会落到这里。不吞、不返回 nil：
			// 吞掉的后果是所有上传安静地写进平台盘，而租户以为在自己桶里。
			return nil, fmt.Errorf("读取 tenant_storage_configs 失败：%w", tx.Error)
		}
		if tx.RowsAffected == 0 {
			return nil, nil
		}
		target := nginxsnippet.Target{
			CompanyID:  row.CompanyID,
			Driver:     row.Driver,
			Endpoint:   row.Endpoint,
			Bucket:     row.Bucket,
			UseSSL:     row.UseSSL,
			HostHeader: row.HostHeader,
			Enabled:    row.Enabled,
		}
		if !target.NeedsSnippet() {
			// enabled=false 或 driver=local：明确要平台存储。与 nginx 侧同一个判定。
			return nil, nil
		}
		ak, err := pii.Decrypt(row.AccessKeyEnc)
		if err != nil {
			return nil, fmt.Errorf("AccessKey 解密失败（PII_ENC_KEY 与写入时不一致？）：%w", err)
		}
		sk, err := pii.Decrypt(row.SecretKeyEnc)
		if err != nil {
			return nil, fmt.Errorf("SecretKey 解密失败（PII_ENC_KEY 与写入时不一致？）：%w", err)
		}
		return &Config{
			CompanyID:    row.CompanyID,
			Driver:       row.Driver,
			Endpoint:     row.Endpoint,
			Bucket:       row.Bucket,
			Region:       row.Region,
			UseSSL:       row.UseSSL,
			HostHeader:   row.HostHeader,
			AccessKey:    ak,
			SecretKey:    sk,
			BaseURL:      strings.TrimSpace(platformBase),
			PublicOrigin: strings.TrimSpace(row.PublicBaseURL),
			PublicRead:   row.PublicRead,
			Version:      row.UpdatedAt.UnixNano(),
		}, nil
	}
}

// RouteEnabled 读 TENANT_STORAGE_ROUTE，默认 off。
//
// 只有显式的 on / 1 / true / yes 才打开：写错值的部署（拼错、留空、复制时带了
// 别的变量名）必须落到**最保守**的那一侧。反过来（拼错即打开）会让一次手误
// 把线上上传整体切到客户桶——而这件事在日志里只有一行"已启用"，并不显眼。
//
// 放在本包而不是各自 main 里，是因为 cmd/server 与 cmd/devserver 都要判同一个
// 环境变量：两份实现迟早漂移成「生产认得 on、联调不认」，联调即验证契约的承诺就破了。
func RouteEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TENANT_STORAGE_ROUTE"))) {
	case "on", "1", "true", "yes":
		return true
	}
	return false
}

// Verify 是打开租户路由前的启动自检：表在不在。
//
// 为什么要在启动时判，而不是等第一次上传：路由一旦打开，每个上传都要读这张表；
// 表不存在时**所有租户**的上传都会失败（包括没有配自有桶的那些，因为不读表就
// 不知道谁配了）。那是一个整体不可用，且第一现场只是一句 MySQL 1146。
// 在启动时 Fatal 掉，运维拿到的是一句可以直接照做的提示。
//
// 只在 TENANT_STORAGE_ROUTE=on 时调用；off 时这条路根本不走，表不存在也无影响。
func Verify(db *gorm.DB) error {
	if err := db.Exec("SELECT 1").Error; err != nil {
		return fmt.Errorf("数据库不可达，无法启用租户存储路由：%w", err)
	}
	if !db.Migrator().HasTable(&model.TenantStorageConfig{}) {
		return errors.New("tenant_storage_configs 表不存在：先跑 `./server migrate` 应用迁移 0044" +
			"（若 migrations/ 里也没有 0044_tenant_storage_configs.sql，说明发布包不完整）；" +
			"确认无误后再用 TENANT_STORAGE_ROUTE=on 重启，或改为 off 继续用平台存储")
	}
	return nil
}

var _ storage.Resolver = (*Resolver)(nil)
