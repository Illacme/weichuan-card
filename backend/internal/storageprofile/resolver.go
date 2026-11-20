// Package storageprofile 把「存储 / CDN 档案 + 绑定」解析成运行时生效配置。
//
// 设计要点（勿回退）：
//  1. 启动期 config.OverlayFromSettings 的单一快照不再是真值源；这里按请求解析绑定。
//  2. 档案和绑定各自带 version，快照按版本失效；多副本各进程独立缓存，秒级最终一致。
//  3. 绑定存在但档案缺失 / status=deprecated 时**报错**，绝不静默回退平台——
//     与 uploadroute 的既有边界一致，文件落错位置比上传失败严重得多。
package storageprofile

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
	"weichuan/internal/uploadroute"

	"gorm.io/gorm"
)

// Snapshot 是一次性读出的全部档案与绑定。用 atomic.Value 整份替换，
// 避免读到「绑定换了、档案还没读到」的半新半旧状态。
type Snapshot struct {
	Storages   map[int64]*model.StorageProfile
	Deliveries map[int64]*model.DeliveryProfile
	Bindings   map[int64]*model.StorageBinding
	// Fingerprint 是「档案 + 绑定」的整体指纹，必须同时含版本和行数。
	// 只用 MAX(version) 之和会漏掉「新增一行绑定但 version=1」这种变化：
	// 旧快照里没有这一行、MAX 却还是 1，两边签名相等 → 永远不重载。
	Fingerprint string
}

// Effective 是一次解析的结果。Storage 为 nil 表示「没有档案绑定」，
// 调用方应走原有 tenant_storage_configs / 平台配置，这不是错误。
type Effective struct {
	Storage  *model.StorageProfile
	Delivery *model.DeliveryProfile
	Version  int64
}

// Resolver 是档案绑定的运行时解析器。
type Resolver struct {
	db       *gorm.DB
	localDir string
	snap     atomic.Value // *Snapshot
	mu       sync.Mutex
	uploader map[string]storage.Uploader // profileID:version -> uploader
	upMu     sync.RWMutex
}

// New 构造解析器并立即加载一次快照；加载失败不阻止启动，
// 后续每次解析会重试（见 reload）。
func New(db *gorm.DB) *Resolver {
	return NewWithLocal(db, "./uploads")
}

// NewWithLocal 额外指定本地驱动的根目录。devserver / 测试用它指向临时 uploads，
// 否则「本地档案」会写到进程当前目录下的 ./uploads，与静态路由托管目录分叉。
func NewWithLocal(db *gorm.DB, dir string) *Resolver {
	r := &Resolver{db: db, localDir: dir, uploader: make(map[string]storage.Uploader)}
	r.snap.Store(&Snapshot{
		Storages: map[int64]*model.StorageProfile{}, Deliveries: map[int64]*model.DeliveryProfile{},
		Bindings: map[int64]*model.StorageBinding{},
	})
	_ = r.reload()
	return r
}

// snapshot 返回当前快照；侦测到数据库版本变化时重载。
func (r *Resolver) snapshot() *Snapshot {
	cur, _ := r.snap.Load().(*Snapshot)
	if cur == nil || r.db == nil {
		return cur
	}
	if r.fingerprint() != cur.Fingerprint {
		_ = r.reload()
		cur, _ = r.snap.Load().(*Snapshot)
	}
	return cur
}

// fingerprint 同时取三张表的行数与 MAX(version)：
// 行数抓新增 / 删除，MAX(version) 抓修改，缺一不可（见 Snapshot.Fingerprint 注释）。
func (r *Resolver) fingerprint() string {
	var maxS, maxD, maxB, countS, countD, countB int64
	_ = r.db.Raw("SELECT COALESCE(MAX(version),0) FROM storage_profiles").Scan(&maxS).Error
	_ = r.db.Raw("SELECT COALESCE(MAX(version),0) FROM delivery_profiles").Scan(&maxD).Error
	_ = r.db.Raw("SELECT COALESCE(MAX(version),0) FROM storage_bindings").Scan(&maxB).Error
	_ = r.db.Model(&model.StorageProfile{}).Count(&countS).Error
	_ = r.db.Model(&model.DeliveryProfile{}).Count(&countD).Error
	_ = r.db.Model(&model.StorageBinding{}).Count(&countB).Error
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d", maxS, maxD, maxB, countS, countD, countB)
}

func (r *Resolver) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return fmt.Errorf("档案解析器：数据库为空")
	}
	var sps []model.StorageProfile
	if err := r.db.Find(&sps).Error; err != nil {
		return fmt.Errorf("读取存储档案：%w", err)
	}
	var dps []model.DeliveryProfile
	if err := r.db.Find(&dps).Error; err != nil {
		return fmt.Errorf("读取分发档案：%w", err)
	}
	var bs []model.StorageBinding
	if err := r.db.Find(&bs).Error; err != nil {
		return fmt.Errorf("读取绑定：%w", err)
	}
	s := &Snapshot{
		Storages:   make(map[int64]*model.StorageProfile, len(sps)),
		Deliveries: make(map[int64]*model.DeliveryProfile, len(dps)),
		Bindings:   make(map[int64]*model.StorageBinding, len(bs)),
	}
	for i := range sps {
		sp := sps[i]
		s.Storages[sp.ID] = &sp
	}
	for i := range dps {
		dp := dps[i]
		s.Deliveries[dp.ID] = &dp
	}
	for i := range bs {
		b := bs[i]
		s.Bindings[b.TenantID] = &b
	}
	s.Fingerprint = r.fingerprint()
	r.snap.Store(s)
	return nil
}

// Resolve 按企业解析生效档案。cid<=0 按全局默认处理。
func (r *Resolver) Resolve(cid int64) (Effective, error) {
	s := r.snapshot()
	if s == nil {
		return Effective{}, fmt.Errorf("档案快照未就绪")
	}
	b := s.Bindings[0]
	if cid > 0 {
		if own := s.Bindings[cid]; own != nil {
			b = own
		}
	}
	if b == nil {
		return Effective{}, nil
	}
	sp := s.Storages[b.StorageProfileID]
	if sp == nil {
		return Effective{}, fmt.Errorf("绑定引用的存储档案 %d 不存在", b.StorageProfileID)
	}
	if sp.Status == "deprecated" {
		return Effective{}, fmt.Errorf("绑定引用的存储档案 %d 已停用", sp.ID)
	}
	var dp *model.DeliveryProfile
	if b.DeliveryProfileID > 0 {
		dp = s.Deliveries[b.DeliveryProfileID]
		if dp == nil || dp.Status == "deprecated" {
			return Effective{}, fmt.Errorf("绑定引用的分发档案 %d 不存在或已停用", b.DeliveryProfileID)
		}
	}
	return Effective{Storage: sp, Delivery: dp, Version: b.Version + sp.Version}, nil
}

// UploaderFor 按生效档案构造（并缓存）Uploader。缓存键含档案版本，
// 保存档案时 version 递增，下一次请求自然重建，不需要重启进程。
func (r *Resolver) UploaderFor(cid int64) (storage.Uploader, error) {
	eff, err := r.Resolve(cid)
	if err != nil {
		return nil, err
	}
	if eff.Storage == nil {
		return nil, nil
	}
	if eff.Storage.Driver == "local" {
		return r.localUploader(), nil
	}
	key := fmt.Sprintf("%d:%d", eff.Storage.ID, eff.Storage.Version)
	r.upMu.RLock()
	up, ok := r.uploader[key]
	r.upMu.RUnlock()
	if ok {
		return up, nil
	}
	cfg := uploadConfig(eff.Storage, cid, eff.Version)
	up, err = uploadroute.Build(cfg)
	if err != nil {
		return nil, err
	}
	r.upMu.Lock()
	r.uploader[key] = up
	r.upMu.Unlock()
	return up, nil
}

// DynamicResolver 在既有 uploadroute.Resolver（兼容 tenant_storage_configs）之上
// 插入档案解析：有绑定就走档案，没有绑定就沿用旧实现。
// 这样旧部署升级后零突变，切换档案后下一个请求即生效。
type DynamicResolver struct {
	base storage.Resolver
	prof *Resolver
}

func NewDynamic(base storage.Resolver, prof *Resolver) *DynamicResolver {
	return &DynamicResolver{base: base, prof: prof}
}

func (d *DynamicResolver) For(cid int64) (storage.Uploader, error) {
	if d.prof != nil {
		if up, err := d.prof.UploaderFor(cid); err != nil {
			return nil, err
		} else if up != nil {
			return up, nil
		}
	}
	return d.base.For(cid)
}

// Platform 必须原样交回平台实例，**绝不能**走档案解析。
//
// 有一类对象键前缀写死了平台：p/library/...（素材采用）、p/wxacode/...（小程序码）。
// 它们的 URL 由平台 nginx 的 /uploads/ 回退段托管；若被全局档案指到客户桶，
// 字节进了客户桶、URL 仍是平台域名，取图必然 404。键前缀与实例必须同源
// （见 storage.Resolver.Platform 的注释与 TestPlatformScopedWritesUsePlatformInstance）。
func (d *DynamicResolver) Platform() storage.Uploader { return d.base.Platform() }

func (d *DynamicResolver) DriverFor(cid int64) string {
	if d.prof != nil {
		if eff, err := d.prof.Resolve(cid); err == nil && eff.Storage != nil {
			return eff.Storage.Driver
		}
	}
	return d.base.DriverFor(cid)
}

// CDNRefresherFor 按 URL 里的 /uploads/t/<cid>/ 前缀选租户分发档案；
// 解析不到租户时用全局绑定。返回 nil 表示没有配置 CDN 刷新。
// 这样既支持多套 CDN，又不必改动所有 refreshCDN 调用签名。
func (r *Resolver) CDNRefresherFor(urls ...string) storage.CDNRefresher {
	cid := int64(0)
	for _, u := range urls {
		if i := strings.Index(u, "/uploads/t/"); i >= 0 {
			tail := u[i+len("/uploads/t/"):]
			if j := strings.IndexAny(tail, "/?#"); j > 0 {
				if n, err := strconv.ParseInt(tail[:j], 10, 64); err == nil {
					cid = n
					break
				}
			}
		}
	}
	eff, err := r.Resolve(cid)
	if err != nil || eff.Delivery == nil || eff.Delivery.Provider == "none" || eff.Delivery.Provider == "" {
		return nil
	}
	dp := eff.Delivery
	rf, err := storage.NewCDNRefresher(storage.CDNConfig{
		Provider: dp.Provider, AccessKeyID: dp.AccessKey, AccessKeySecret: dp.SecretKey,
		Endpoint: dp.Endpoint, ResourceID: dp.ResourceID,
	})
	if err != nil {
		return nil
	}
	return rf
}

// PublicURL 按分发档案拼出对外 URL；没有分发档案时返回空串（调用方回退既有逻辑）。
func (r *Resolver) PublicURL(cid int64, key string) string {
	eff, err := r.Resolve(cid)
	if err != nil || eff.Delivery == nil || eff.Delivery.Domain == "" {
		return ""
	}
	domain := strings.TrimRight(eff.Delivery.Domain, "/")
	prefix := strings.Trim(eff.Delivery.PathPrefix, "/")
	if prefix != "" {
		return domain + "/" + prefix + "/" + strings.TrimLeft(key, "/")
	}
	return domain + "/" + strings.TrimLeft(key, "/")
}

func (r *Resolver) localUploader(sp ...*model.StorageProfile) storage.Uploader {
	dir := r.localDir
	if len(sp) > 0 && sp[0] != nil && strings.TrimSpace(sp[0].LocalDir) != "" {
		dir = strings.TrimSpace(sp[0].LocalDir)
	}
	if dir == "" {
		dir = "./uploads"
	}
	return &storage.LocalUploader{Dir: dir, BaseURL: "/uploads", MaxBytes: 10 << 20, AllowExt: storage.DefaultAllowExt}
}

// uploadConfig 把档案转成 uploadroute.Config。自检查询和运行时上传共用这一个函数，
// 避免「自检说配置 OK、真上传却因字段口径不同而失败」这种最难查的漂移。
func uploadConfig(sp *model.StorageProfile, cid, version int64) *uploadroute.Config {
	ep, useSSL := splitEndpointScheme(sp.Endpoint, sp.UseSSL)
	return &uploadroute.Config{
		CompanyID: cid, Driver: sp.Driver, Endpoint: ep, Bucket: sp.Bucket,
		Region: sp.Region, UseSSL: useSSL, HostHeader: sp.HostHeader,
		AccessKey: sp.AccessKey, SecretKey: sp.SecretKey, BaseURL: sp.BaseURL,
		PublicRead: sp.PublicRead, Version: version,
	}
}

// splitEndpointScheme 把 endpoint 拆成「host[:port] + 是否走 https」。
//
// 档案的 endpoint 允许带 scheme（界面没强拦），但 nginx 侧拼的是
// `proxy_pass https://<endpoint>`，带 scheme 会得到 `https://https://...`；
// OSS SDK 又反过来要求带 scheme。所以两个消费方都从这里取，别各自判一次。
//
// 大小写不敏感地剥：手填 `HTTPS://` 在旧实现里剥不掉，会静默留下一个坏 endpoint。
func splitEndpointScheme(endpoint string, useSSL bool) (host string, ssl bool) {
	ep := strings.TrimSpace(endpoint)
	low := strings.ToLower(ep)
	switch {
	case strings.HasPrefix(low, "https://"):
		return ep[len("https://"):], true
	case strings.HasPrefix(low, "http://"):
		return ep[len("http://"):], false
	}
	return ep, useSSL
}

// NginxTarget 把「某家企业的生效存储档案」映射成 nginx 渲染用的 Target。
//
// 与 uploadroute.Build 里的那份映射必须同口径（同一套 driver / endpoint / bucket /
// host_header），否则会出现「上传按虚拟主机风格发、nginx 按路径风格回源」——
// 那是第六十三轮真桶实测里最贵的一个坑（400 InvalidBucketName，后台看起来全对）。
//
// Enabled 恒 true：档案没有 enabled 列，「要不要生成 location」由
// Target.NeedsSnippet 按 driver 判定（local 档案没有桶可回源，自然不生成）。
func NginxTarget(cid int64, sp *model.StorageProfile) nginxsnippet.Target {
	host, useSSL := splitEndpointScheme(sp.Endpoint, sp.UseSSL)
	return nginxsnippet.Target{
		CompanyID:  cid,
		Driver:     sp.Driver,
		Endpoint:   host,
		Bucket:     sp.Bucket,
		UseSSL:     useSSL,
		HostHeader: sp.HostHeader,
		Enabled:    true,
	}
}

// UploaderForProfile 按档案 id 构造上传实例，不经过绑定。
// 自检和迁移必须显式指定源/目标档案，不能被「当前绑定」悄悄改掉。
func (r *Resolver) UploaderForProfile(id int64) (storage.Uploader, error) {
	var sp model.StorageProfile
	if err := r.db.First(&sp, id).Error; err != nil {
		return nil, fmt.Errorf("读取存储档案 %d 失败：%w", id, err)
	}
	if sp.Driver == "local" {
		return r.localUploader(&sp), nil
	}
	return uploadroute.Build(uploadConfig(&sp, 0, sp.Version))
}

// Profile 按 id 读一份存储档案（只读模型，查询层不在此包重复实现）。
func (r *Resolver) Profile(id int64) (*model.StorageProfile, error) {
	var sp model.StorageProfile
	if err := r.db.First(&sp, id).Error; err != nil {
		return nil, err
	}
	return &sp, nil
}

// Touch 供写接口在保存档案 / 绑定后主动重载，保证紧接着的下一次解析看到新值。
func (r *Resolver) Touch() error {
	return r.reload()
}

var _ storage.Resolver = (*DynamicResolver)(nil)

// OriginFor 回答两个问题，一次答完：这家企业此刻该用哪个出参 origin，
// 以及档案有没有接管它的对象写入。
//
//	handled=false  档案没有接管（无绑定 / cid 不合法）→ 调用方继续走既有
//	               tenant_storage_configs 判定，本轮行为不变。
//	handled=true   档案接管了写入。此时 Base 为空表示「应当使用平台统一入口」，
//	               调用方**不得**再回落旧口径——旧口径里那个 public_base_url
//	               指向的是旧桶，对象已经不在那里了，回落就是全站裂图。
//
// 为什么要合并成一次判定（第七十四轮）：以前这里是「分发档案有合法域名就返回」，
// 而旧路径要过 tenantStorageActive（开关 / 启用 / 非 local / 凭据完整 / 公开读）
// 五道关。两套标准并存时，一家企业绑了 local 档案、分发档案又填了域名，
// 出参就会指到那个域名——而对象明明还在平台盘上。判定必须按「谁在写对象」分叉，
// 不能按「谁配了域名」分叉。
func (r *Resolver) OriginFor(cid int64) (model.Origin, bool) {
	if cid <= 0 {
		// Origin 的不变式是「Base 非空则 CompanyID > 0」。cid 未知时不能凭全局
		// 绑定去猜域名，交回调用方按平台入口处理。
		return model.PlatformOrigin(), false
	}
	eff, err := r.Resolve(cid)
	if err != nil || eff.Storage == nil {
		return model.PlatformOrigin(), false
	}
	// 以下都是「档案接管了写入、但这个域名不能用」：统一回平台入口。
	// 本地档案没有桶可回源；私有桶直连会 403；域名不合法会被 ResolveRefForTenant 拒。
	if eff.Storage.Driver == nginxsnippet.DriverLocal {
		return model.PlatformOrigin(), true
	}
	if !eff.Storage.PublicRead {
		return model.PlatformOrigin(), true
	}
	if eff.Delivery == nil || eff.Delivery.Domain == "" {
		return model.PlatformOrigin(), true
	}
	base, err := storage.ValidatePublicOrigin(eff.Delivery.Domain)
	if err != nil || base == "" {
		return model.PlatformOrigin(), true
	}
	return model.Origin{Base: base, CompanyID: cid}, true
}

// TenantFromURLs 从对象 URL 里解析 /uploads/t/<cid>/ 前缀里的企业 id；
// 解析不到返回 0（按全局绑定处理）。多副本下这个判定是纯字符串运算，无 DB 查询。
func TenantFromURLs(urls ...string) int64 {
	for _, u := range urls {
		if i := strings.Index(u, "/uploads/t/"); i >= 0 {
			tail := u[i+len("/uploads/t/"):]
			if j := strings.IndexAny(tail, "/?#"); j > 0 {
				if n, err := strconv.ParseInt(tail[:j], 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// DynamicCDN 在既有全局刷新器外侧插入档案分发配置：
// 有分发绑定就按绑定走；绑定了 provider=none 是明确「不刷新」，
// 不能落到全局刷新器上（那会让运营以为已经关掉、实际还在刷）。
type DynamicCDN struct {
	base storage.CDNRefresher
	prof *Resolver
}

func NewDynamicCDN(base storage.CDNRefresher, prof *Resolver) storage.CDNRefresher {
	return &DynamicCDN{base: base, prof: prof}
}

// HasDeliveryBinding 回报是否至少有一套可用的分发档案绑定。
// 用来保留「CDN 未配置」的旧语义：包了一层动态刷新器不等于真的配了 CDN。
func (r *Resolver) HasDeliveryBinding() bool {
	s := r.snapshot()
	if s == nil {
		return false
	}
	for _, b := range s.Bindings {
		if b.DeliveryProfileID <= 0 {
			continue
		}
		dp := s.Deliveries[b.DeliveryProfileID]
		if dp != nil && dp.Provider != "" && dp.Provider != "none" {
			return true
		}
	}
	return false
}

// Configured 让 handler 能区分「没配 CDN」与「配了但这次刷新失败」。
func (d *DynamicCDN) Configured() bool {
	return d.base != nil || (d.prof != nil && d.prof.HasDeliveryBinding())
}

func (d *DynamicCDN) Refresh(urls []string) error {
	if d.prof != nil {
		eff, err := d.prof.Resolve(TenantFromURLs(urls...))
		if err == nil && eff.Delivery != nil {
			if eff.Delivery.Provider == "none" || eff.Delivery.Provider == "" {
				return nil
			}
			if rf, err := storage.NewCDNRefresher(storage.CDNConfig{
				Provider: eff.Delivery.Provider, AccessKeyID: eff.Delivery.AccessKey,
				AccessKeySecret: eff.Delivery.SecretKey, Endpoint: eff.Delivery.Endpoint,
				ResourceID: eff.Delivery.ResourceID,
			}); err == nil {
				return rf.Refresh(urls)
			}
		}
	}
	if d.base == nil {
		return nil
	}
	return d.base.Refresh(urls)
}

var _ storage.CDNRefresher = (*DynamicCDN)(nil)
