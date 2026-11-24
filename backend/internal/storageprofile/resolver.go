// Package storageprofile 把「存储 / 分发档案 + 绑定」解析成运行时生效配置。
//
// 这是**唯一**的存储配置模型。历史上还并存过两套（sys_setting 的平铺项、
// tenant_storage_configs 的按企业一行），已在第七十五轮删除：产品尚未商用，
// 兼容层带来的每请求一次必然落空的查询与两套判定标准不值得留。
//
// 设计要点（勿回退）：
//  1. 档案 + 绑定是唯一真值源；平台自身那份存储由 SetPlatform 注入的实例表达。
//  2. 档案和绑定各自带 version，快照按版本失效；多副本各进程独立缓存，秒级最终一致。
//  3. 绑定存在但档案缺失 / status=deprecated 时**报错**，绝不静默回退平台——
//     文件落错位置比上传失败严重得多。
package storageprofile

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// defaultFingerprintTTL 是「多久查一次库看档案变没变」的默认间隔。
//
// 为什么需要节流：解析器在**每个**取 origin / 判配额 / 构造上传器的请求上都要
// 确认快照是不是最新的。以前每次确认打 6 条聚合查询（三张表的 MAX(version) +
// Count），一个请求里 OriginFor 与 OwnerOf 各来一遍就是 12 条。现在合并成 1 条，
// 再叠一层短 TTL：稳态下每个进程每秒最多 1 条。
//
// 代价是**跨进程**的变更最多晚 TTL 生效。这与本包既有的「秒级最终一致」一致；
// 而**本进程**写入时 handler 会调 Touch() 立即重载，所以「保存后立刻刷新页面」
// 看到的永远是新值。
const defaultFingerprintTTL = 700 * time.Millisecond

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
	// resolved 缓存本快照内的解析结果。快照整份替换时它随之丢弃，
	// 因此不会出现「档案改了、缓存还留着旧结论」。同一个请求里
	// OriginFor 与 OwnerOf 各调一次 Resolve 时省掉重复的版本运算与错误构造。
	resolved sync.Map // int64 -> resolveResult
}

type resolveResult struct {
	eff Effective
	err error
}

// Effective 是一次解析的结果。Storage 为 nil 表示「这家企业没有档案绑定」，
// 调用方应走平台实例，这不是错误。
type Effective struct {
	Storage  *model.StorageProfile
	Delivery *model.DeliveryProfile
	Version  int64
}

// Resolver 是档案绑定的运行时解析器，同时是 storage.Resolver 的实现。
type Resolver struct {
	db       *gorm.DB
	localDir string
	// platform 是平台自身那一份存储实例（sys_setting / STORAGE_* 构造）。
	// 它只服务两类调用方：平台对象（p/ 前缀）与**没有档案绑定**的租户。
	// 它不参与租户路由判定——「这家有没有自己的桶」只看绑定。
	platform       storage.Uploader
	platformDriver string
	// platformUp / platformCDNUp 缓存按内置档案 version 现建的平台实例。
	// 每次整份替换（见 platform_live.go），并发读无锁；为空表示尚未建成。
	platformUp    atomic.Value // *platformInstance
	platformCDNUp atomic.Value // *platformRefresherEntry
	// 构建串行化：两个请求同时发现 version 变化、各自慢构建时，旧版本的慢构建
	// 完成得比新版本晚会把新实例覆盖回旧实例。加互斥锁保证"先到先构建、后到
	// 看到缓存直接用"，构建期间其它请求短等，不会撕裂。
	platformBuildMu    sync.Mutex
	platformCDNBuildMu sync.Mutex
	// 告警按 version 去重，避免凭据配错期间每个请求都打一行 WARN 刷屏。
	warnedStorageVer string

	snap atomic.Value // *Snapshot
	mu   sync.Mutex
	// uploader 缓存已构造的实例，键为「档案 id:版本」。
	uploader map[string]storage.Uploader
	upMu     sync.RWMutex

	fpTTL time.Duration
	fpMu  sync.Mutex
	fpAt  time.Time
}

// New 构造解析器并立即加载一次快照；加载失败不阻止启动，
// 后续每次解析会重试（见 snapshot）。
func New(db *gorm.DB) *Resolver {
	return NewWithLocal(db, "./uploads")
}

// NewWithLocal 额外指定本地驱动的根目录。devserver / 测试用它指向临时 uploads，
// 否则「本地档案」会写到进程当前目录下的 ./uploads，与静态路由托管目录分叉。
func NewWithLocal(db *gorm.DB, dir string) *Resolver {
	r := &Resolver{
		db: db, localDir: dir,
		uploader: make(map[string]storage.Uploader),
		fpTTL:    defaultFingerprintTTL,
	}
	r.snap.Store(&Snapshot{
		Storages: map[int64]*model.StorageProfile{}, Deliveries: map[int64]*model.DeliveryProfile{},
		Bindings: map[int64]*model.StorageBinding{},
	})
	_ = r.reload()
	return r
}

// SetPlatform 注入平台自身那一份实例。必须在开始服务前调用一次。
//
// 为什么不从档案里推：平台对象（p/library/... 素材采用、p/wxacode/... 小程序码）
// 的键前缀写死了平台，它们的字节必须落在平台自己的存储上，否则 URL 指向平台域名
// 而对象在客户桶里，取图必然 404。平台实例是部署期就确定的事实，不该由一条
// 可被误删的绑定来表达。
func (r *Resolver) SetPlatform(up storage.Uploader, driver string) {
	r.platform = up
	r.platformDriver = driver
}

// SetFingerprintTTL 调整快照新鲜度检查的间隔，主要给测试用（传 0 = 每次都查库）。
func (r *Resolver) SetFingerprintTTL(d time.Duration) { r.fpTTL = d }

// snapshot 返回当前快照；距上次校验超过 TTL 时查一次库，变了就重载。
func (r *Resolver) snapshot() *Snapshot {
	cur, _ := r.snap.Load().(*Snapshot)
	if cur == nil || r.db == nil {
		return cur
	}
	if !r.fingerprintDue() {
		return cur
	}
	fp, err := r.fingerprint()
	if err != nil {
		// 查不到（表还没建 / 库抖动）时保留上一份快照。宁可短暂用旧配置，
		// 也不能因为一次查询失败就让所有租户的上传走不通。
		return cur
	}
	if fp == cur.Fingerprint {
		return cur
	}
	if err := r.reload(); err != nil {
		return cur
	}
	cur, _ = r.snap.Load().(*Snapshot)
	return cur
}

// fingerprintDue 判断是否到了下一次查库的时间，并推进时间戳。
// 必须在这里推进（而不是拿到结果之后），否则并发请求会同时穿透去查库。
func (r *Resolver) fingerprintDue() bool {
	if r.fpTTL <= 0 {
		return true
	}
	r.fpMu.Lock()
	defer r.fpMu.Unlock()
	if time.Since(r.fpAt) < r.fpTTL {
		return false
	}
	r.fpAt = time.Now()
	return true
}

// fingerprint 用**一条** SQL 取三张表的行数与 MAX(version)。
//
// 行数抓新增 / 删除，MAX(version) 抓修改，缺一不可（见 Snapshot.Fingerprint 注释）。
// 合并成一条是因为这条路径在每个取 origin / 判配额 / 构造上传器的请求上都会走，
// 六次往返换成一次是这一层最直接的性能收益。
func (r *Resolver) fingerprint() (string, error) {
	const q = `SELECT
 (SELECT COALESCE(MAX(version),0) FROM storage_profiles),
 (SELECT COALESCE(MAX(version),0) FROM delivery_profiles),
 (SELECT COALESCE(MAX(version),0) FROM storage_bindings),
 (SELECT COUNT(*) FROM storage_profiles),
 (SELECT COUNT(*) FROM delivery_profiles),
 (SELECT COUNT(*) FROM storage_bindings)`
	var maxS, maxD, maxB, countS, countD, countB int64
	if err := r.db.Raw(q).Row().Scan(&maxS, &maxD, &maxB, &countS, &countD, &countB); err != nil {
		return "", fmt.Errorf("读取配置指纹失败：%w", err)
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d", maxS, maxD, maxB, countS, countD, countB), nil
}

func (r *Resolver) reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return fmt.Errorf("文件存储解析器：数据库为空")
	}
	var sps []model.StorageProfile
	if err := r.db.Find(&sps).Error; err != nil {
		return fmt.Errorf("读取文件存储配置：%w", err)
	}
	var dps []model.DeliveryProfile
	if err := r.db.Find(&dps).Error; err != nil {
		return fmt.Errorf("读取 CDN 分发配置：%w", err)
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
	// 指纹从**刚读到的这份数据**反算，而不是再查一次库：两次查询之间若有写入，
	// 重算会得到一个与内存数据不匹配的指纹，下次比较必然不等，白重载一轮。
	s.Fingerprint = snapshotFingerprint(sps, dps, bs)
	r.snap.Store(s)
	// 刚查过库，重置节流时间戳，避免紧接着的第一次 snapshot() 又白查一遍。
	r.fpMu.Lock()
	r.fpAt = time.Now()
	r.fpMu.Unlock()
	return nil
}

// snapshotFingerprint 从已读出的行算指纹，口径与 fingerprint 的 SQL 一致。
func snapshotFingerprint(sps []model.StorageProfile, dps []model.DeliveryProfile, bs []model.StorageBinding) string {
	var maxS, maxD, maxB int64
	for i := range sps {
		if sps[i].Version > maxS {
			maxS = sps[i].Version
		}
	}
	for i := range dps {
		if dps[i].Version > maxD {
			maxD = dps[i].Version
		}
	}
	for i := range bs {
		if bs[i].Version > maxB {
			maxB = bs[i].Version
		}
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d", maxS, maxD, maxB, len(sps), len(dps), len(bs))
}

// Resolve 按企业解析生效档案。cid<=0 按全局默认处理。
func (r *Resolver) Resolve(cid int64) (Effective, error) {
	s := r.snapshot()
	if s == nil {
		return Effective{}, fmt.Errorf("配置快照未就绪")
	}
	if v, ok := s.resolved.Load(cid); ok {
		rr := v.(resolveResult)
		return rr.eff, rr.err
	}
	eff, err := s.resolve(cid)
	s.resolved.Store(cid, resolveResult{eff: eff, err: err})
	return eff, err
}

func (s *Snapshot) resolve(cid int64) (Effective, error) {
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
		return Effective{}, fmt.Errorf("绑定引用的文件存储 %d 不存在", b.StorageProfileID)
	}
	if sp.Status == "deprecated" {
		return Effective{}, fmt.Errorf("绑定引用的文件存储 %d 已停用", sp.ID)
	}
	var dp *model.DeliveryProfile
	if b.DeliveryProfileID > 0 {
		dp = s.Deliveries[b.DeliveryProfileID]
		if dp == nil || dp.Status == "deprecated" {
			return Effective{}, fmt.Errorf("绑定引用的 CDN 分发 %d 不存在或已停用", b.DeliveryProfileID)
		}
	}
	return Effective{Storage: sp, Delivery: dp, Version: b.Version + sp.Version}, nil
}

// For 见 storage.Resolver.For。没有档案绑定时走平台实例——那是「明确没配」，
// 不是降级；有绑定但构造失败时**报错**，绝不回退平台，否则文件会落错位置。
func (r *Resolver) For(cid int64) (storage.Uploader, error) {
	up, err := r.UploaderFor(cid)
	if err != nil {
		return nil, err
	}
	if up != nil {
		return up, nil
	}
	return r.Platform(), nil
}

// Platform 返回平台自身那一份实例，**不参与**任何租户绑定解析。
//
// 平台实例的真值源是「主存储」内置档案（迁移 0059）：改了凭据/驱动/基址后
// 由 livePlatform 按档案 version 现建，保存即生效，不再需要重启。
// 只有档案还没读到（旧库/启动瞬间）或构造失败时才回落到启动期注入的兜底实例，
// 保证管理员还能进后台修正配置。理由见 SetPlatform 与 storage.Resolver.Platform。
func (r *Resolver) Platform() storage.Uploader {
	if inst := r.livePlatform(); inst != nil && inst.up != nil {
		return inst.up
	}
	return r.platform
}

// DriverFor 回报该租户实际生效的驱动名，只用于后台能力探测展示。
// 取不到档案时回平台驱动：它不影响任何写入路径，真正的失败会在 For 里报出来。
func (r *Resolver) DriverFor(cid int64) string {
	if cid > 0 {
		if eff, err := r.Resolve(cid); err == nil && eff.Storage != nil {
			return eff.Storage.Driver
		}
	}
	// 平台驱动跟随主存储档案实时变化（换驱动后界面能力探测立即反映新落点），
	// 与 Platform() 用的是同一份 livePlatform 快照，不会出现"显示新驱动、实例还是旧的"。
	if inst := r.livePlatform(); inst != nil && inst.driver != "" {
		return inst.driver
	}
	if r.platformDriver == "" {
		return "local"
	}
	return r.platformDriver
}

// UploaderFor 按生效档案构造（并缓存）Uploader。没有绑定时返回 (nil, nil)。
// 缓存键含档案版本，保存档案时 version 递增，下一次请求自然重建，不需要重启进程。
func (r *Resolver) UploaderFor(cid int64) (storage.Uploader, error) {
	eff, err := r.Resolve(cid)
	if err != nil {
		return nil, err
	}
	if eff.Storage == nil {
		return nil, nil
	}
	if eff.Storage.Driver == nginxsnippet.DriverLocal {
		return r.localUploader(eff.Storage), nil
	}
	key := fmt.Sprintf("%d:%d", eff.Storage.ID, eff.Storage.Version)
	r.upMu.RLock()
	up, ok := r.uploader[key]
	r.upMu.RUnlock()
	if ok {
		return up, nil
	}
	up, err = buildUploader(uploaderConfigFor(eff.Storage, cid))
	if err != nil {
		return nil, fmt.Errorf(
			"企业 %d 的自有存储不可用（已按要求不回退平台存储，以免文件落错位置）：%w", cid, err)
	}
	r.upMu.Lock()
	r.uploader[key] = up
	r.upMu.Unlock()
	return up, nil
}

func (r *Resolver) localUploader(sp *model.StorageProfile) storage.Uploader {
	dir := r.localDir
	if sp != nil && strings.TrimSpace(sp.LocalDir) != "" {
		dir = strings.TrimSpace(sp.LocalDir)
	}
	if dir == "" {
		dir = "./uploads"
	}
	return &storage.LocalUploader{Dir: dir, BaseURL: "/uploads", MaxBytes: 10 << 20, AllowExt: storage.DefaultAllowExt}
}

// uploaderConfigFor 把档案转成构造参数。自检查询和运行时上传共用这一个函数，
// 避免「自检说配置 OK、真上传却因字段口径不同而失败」这种最难查的漂移。
func uploaderConfigFor(sp *model.StorageProfile, cid int64) *uploaderConfig {
	ep, useSSL := splitEndpointScheme(sp.Endpoint, sp.UseSSL)
	return &uploaderConfig{
		CompanyID: cid, Driver: sp.Driver, Endpoint: ep, Bucket: sp.Bucket,
		Region: sp.Region, UseSSL: useSSL, HostHeader: sp.HostHeader,
		AccessKey: sp.AccessKey, SecretKey: sp.SecretKey, BaseURL: sp.BaseURL,
		PublicRead: sp.PublicRead, Purpose: sp.Purpose,
	}
}

// splitEndpointScheme 把 endpoint 拆成「host[:port] + 是否走 https」。
//
// 档案的 endpoint 允许带 scheme（界面没强拦），但 nginx 侧拼的是
// `proxy_pass https://<endpoint>`，带 scheme 会得到 `https://https://...`；
// OSS SDK 又反过来要求带 scheme。所以两个消费方都从这里取，别各自判一次。
//
// 大小写不敏感地剥：手填 `HTTPS://` 在老实现里剥不掉，会静默留下一个坏 endpoint。
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
// 与 buildUploader 里的那份映射必须同口径（同一套 driver / endpoint / bucket /
// host_header），否则会出现「上传按虚拟主机风格发、nginx 按路径风格回源」——
// 那是真桶实测里最贵的一个坑（400 InvalidBucketName，后台看起来全对）。
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
		// 私有桶不能生成匿名回源桶的 location：那条路径不带签名，必 403。
		// 生成器改为把 /uploads/t/<cid>/ 指向后端，由它现签后 302。
		Private: !sp.PublicRead,
	}
}

// UploaderForProfile 按档案 id 构造上传实例，不经过绑定。
// 自检和迁移必须显式指定源/目标档案，不能被「当前绑定」悄悄改掉。
func (r *Resolver) UploaderForProfile(id int64) (storage.Uploader, error) {
	var sp model.StorageProfile
	if err := r.db.First(&sp, id).Error; err != nil {
		return nil, fmt.Errorf("读取文件存储 %d 失败：%w", id, err)
	}
	if sp.Driver == nginxsnippet.DriverLocal {
		return r.localUploader(&sp), nil
	}
	return buildUploader(uploaderConfigFor(&sp, 0))
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
// 同时清掉节流时间戳，否则 TTL 内的下一次 snapshot() 会继续用旧快照。
func (r *Resolver) Touch() error {
	r.fpMu.Lock()
	r.fpAt = time.Time{}
	r.fpMu.Unlock()
	return r.reload()
}

// OriginFor 回答两个问题，一次答完：这家企业此刻该用哪个出参 origin，
// 以及档案有没有接管它的对象写入。
//
//	handled=false  没有绑定（cid 不合法 / 未绑定）→ 调用方按平台统一入口处理。
//	handled=true   档案接管了写入。此时 Base 为空表示「应当使用平台统一入口」。
//
// 判定必须按「谁在写对象」分叉，不能按「谁配了域名」分叉：一家企业绑了 local
// 档案、分发档案又填了域名时，若只看域名就会把出参指到那个域名上，
// 而对象明明还在平台盘上——全站裂图且无人报错。
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

// PrivateRead 判断该租户的对象是否必须走后端限时签名跳转。
//
// 只认**租户自己的生效档案**：平台对象、未绑定企业、本地驱动都不在此列。
// 与 OriginFor 相反，这里返回 true 时出参仍用平台统一入口，只是读侧多一跳签名。
// 注意它不替代 nginx snippet：切成私有后必须重新 render + reload，否则
// 旧的匿名回源 location 会继续直连桶，请求根本到不了这里的签名逻辑。
func (r *Resolver) PrivateRead(cid int64) bool {
	if cid <= 0 {
		return false
	}
	eff, err := r.Resolve(cid)
	if err != nil || eff.Storage == nil {
		return false
	}
	return !eff.Storage.PublicRead && eff.Storage.Driver != nginxsnippet.DriverLocal
}

// Diagnose 是 OriginFor 的诊断版：除了结论还给出「为什么没生效」。
// 只有运维诊断接口（/api/public/settings?companyId=）用它，出参路径一律走 OriginFor。
//
// 本函数的 reason 会出现在**无需登录**的公开接口上，所以只用固定文案：
// 底层错误、管理员填的原始域名、内部路径都只进服务端日志，绝不拼进 reason。
func (r *Resolver) Diagnose(cid int64) (base string, active bool, reason string) {
	if cid <= 0 {
		return "", false, "企业 id 不合法"
	}
	eff, err := r.Resolve(cid)
	if err != nil {
		log.Printf("[STORAGE-PROFILE] diagnose cid=%d resolve_failed: %v", cid, err)
		return "", false, "读取文件存储配置失败：请联系管理员检查服务端日志"
	}
	if eff.Storage == nil {
		return "", false, "该企业未绑定文件存储：对象走平台统一入口"
	}
	if eff.Storage.Driver == nginxsnippet.DriverLocal {
		return "", false, "生效的文件存储是本地磁盘：对象在平台盘上，没有可用的客户域名"
	}
	if eff.Delivery == nil || eff.Delivery.Domain == "" {
		return "", false, "生效的 CDN 分发没有绑定公开域名：对象 URL 仍以平台统一入口开头"
	}
	clean, err := storage.ValidatePublicOrigin(eff.Delivery.Domain)
	if err != nil {
		log.Printf("[STORAGE-PROFILE] diagnose cid=%d invalid_delivery_domain: %v", cid, err)
		return "", false, "分发域名不合法：需为 http(s)://host[:port]，且不带路径 / 查询 / 片段"
	}
	if clean == "" {
		return "", false, "生效的 CDN 分发没有绑定公开域名：对象 URL 仍以平台统一入口开头"
	}
	if !eff.Storage.PublicRead {
		return "", false, "该桶是私有桶：对象走平台统一入口，由后端签发限时访问地址；客户域名不能直连私有桶"
	}
	return clean, true, ""
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

// OwnerOf 回报档案有没有接管这家企业的对象写入，以及生效档案的身份。
//
//	ok=false → 没有绑定，调用方按「走平台存储」处理。
//	ok=true  → 档案接管。Driver 可能是 local，那**同样是接管**：对象落在平台盘，
//	           配额与平台域名仍适用，调用方不能把它当成「没接管」再猜一次。
func (r *Resolver) OwnerOf(cid int64) (model.StorageOwner, bool) {
	if cid <= 0 {
		return model.StorageOwner{}, false
	}
	eff, err := r.Resolve(cid)
	if err != nil || eff.Storage == nil {
		return model.StorageOwner{}, false
	}
	return model.StorageOwner{
		Driver:      eff.Storage.Driver,
		ProfileID:   eff.Storage.ID,
		ProfileName: eff.Storage.Name,
	}, true
}

// CDNRefresherFor 按 URL 里的 /uploads/t/<cid>/ 前缀选租户分发档案；
// 解析不到租户时用全局绑定。返回 nil 表示没有配置 CDN 刷新。
func (r *Resolver) CDNRefresherFor(urls ...string) storage.CDNRefresher {
	eff, err := r.Resolve(TenantFromURLs(urls...))
	if err != nil || eff.Delivery == nil || eff.Delivery.Provider == storage.CDNProviderNone || eff.Delivery.Provider == "" {
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
		if dp != nil && dp.Provider != "" && dp.Provider != storage.CDNProviderNone {
			return true
		}
	}
	return false
}

// DynamicCDN 在平台全局刷新器外侧插入档案分发配置：
// 有分发绑定就按绑定走；绑定了 provider=none 是明确「不刷新」，
// 不能落到全局刷新器上（那会让运营以为已经关掉、实际还在刷）。
type DynamicCDN struct {
	base storage.CDNRefresher
	prof *Resolver
}

func NewDynamicCDN(base storage.CDNRefresher, prof *Resolver) storage.CDNRefresher {
	return &DynamicCDN{base: base, prof: prof}
}

// Configured 让 handler 能区分「没配 CDN」与「配了但这次刷新失败」。
func (d *DynamicCDN) Configured() bool {
	if d.base != nil || (d.prof != nil && d.prof.HasDeliveryBinding()) {
		return true
	}
	// 平台自身 CDN 档案热更新后可能新配了厂商，启动期 base 还是 nil——
	// 这里按当前档案再判一次，避免界面一直显示「未启用」。
	return d.prof != nil && d.prof.PlatformCDNConfigured()
}

// platformRefresher 返回当前平台刷新器：档案存在时以档案为真值源（found=true，
// rf 可能为 nil 表示明确不刷新/凭据无效），档案不存在才回落启动期 base。
func (d *DynamicCDN) platformRefresher() storage.CDNRefresher {
	if d.prof == nil {
		return d.base
	}
	rf, found := d.prof.PlatformCDNRefresher()
	if found {
		return rf
	}
	return d.base
}

// Refresh 按 URL 归属选 CDN 刷新器。
//
// 第七十六轮修掉一处静默回退：早先 Resolve 失败或 NewCDNRefresher 构造失败时
// 会悄悄落到平台全局 refresher（base），base 为空则直接 return nil——
// 企业配了分发档案但凭据写错时，违规图下架后边缘仍是旧图，接口却报成功。
// 这与本包「绑定存在但档案坏了要报错，绝不静默回退」的纪律直接冲突。
//
// 第八十一轮再修一处串味：`TenantFromURLs` 只认 `/uploads/t/<cid>/`，平台对象
// （`/uploads/p/`）返回 0，而 `Resolve(0)` 读的是**全局默认绑定**那一行——
// 于是平台对象的刷新会借用「无绑定企业的默认分发档案」的凭据。今天有 sys_setting
// 兜底时不明显，但语义从一开始就是错的：平台对象只该用平台自己的 CDN 档案。
//
// 所以按 URL 分两批：
//   - `/uploads/p/` → 平台刷新器（base，由 platform_primary 分发档案构造）；
//   - 其余 → 按企业绑定的分发档案；解析失败或凭据无效都报错，不回落平台。
func (d *DynamicCDN) Refresh(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	platformURLs, rest := splitPlatformURLs(urls)
	var errs []error
	if len(platformURLs) > 0 {
		// 平台刷新器按「平台自身 CDN」档案实时构造；档案选中「不刷新」时返回
		// nil，是明确决定而非失败。凭据无效时 PlatformCDNRefresher 记日志并
		// 返回 (nil,true)，这里不报错——刷新是增强，不能挡住平台对象覆盖写。
		if rf := d.platformRefresher(); rf != nil {
			if err := rf.Refresh(platformURLs); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(rest) > 0 {
		if err := d.refreshByBinding(rest); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *DynamicCDN) refreshByBinding(urls []string) error {
	if d.prof != nil {
		eff, err := d.prof.Resolve(TenantFromURLs(urls...))
		if err != nil {
			return fmt.Errorf("读取 CDN 分发配置失败，已拒绝静默回落到平台 CDN：%w", err)
		}
		// 私有桶对象的读侧是后端签名 302，CDN 根本不缓存这批 URL；拿租户 CDN
		// 凭据去刷既无效果又浪费厂商配额。与 PrivateRead 同一口径：只认租户自己的
		// 生效档案，local 驱动不算私有桶（它没有 Signer）。
		if eff.Storage != nil && !eff.Storage.PublicRead && eff.Storage.Driver != nginxsnippet.DriverLocal {
			return storage.ErrPrivateBucketCDN
		}
		if eff.Delivery != nil {
			if eff.Delivery.Provider == storage.CDNProviderNone || eff.Delivery.Provider == "" {
				return nil // 明确配了「不刷新」
			}
			rf, err := storage.NewCDNRefresher(storage.CDNConfig{
				Provider: eff.Delivery.Provider, AccessKeyID: eff.Delivery.AccessKey,
				AccessKeySecret: eff.Delivery.SecretKey, Endpoint: eff.Delivery.Endpoint,
				ResourceID: eff.Delivery.ResourceID,
			})
			if err != nil {
				return fmt.Errorf("CDN 分发的凭据无效，已拒绝静默回落到平台 CDN：%w", err)
			}
			return rf.Refresh(urls)
		}
	}
	if d.base == nil {
		return nil
	}
	return d.base.Refresh(urls)
}

// splitPlatformURLs 把平台对象（p/）与其余 URL 分开：一次批量刷新里混了两类时
// 各走各的刷新器，不能让任何一边被另一边顶替。
func splitPlatformURLs(urls []string) (platform, rest []string) {
	for _, u := range urls {
		if isPlatformObjectURL(u) {
			platform = append(platform, u)
			continue
		}
		rest = append(rest, u)
	}
	return platform, rest
}

// isPlatformObjectURL 认两种形态：完整 URL（含 /uploads/p/）与裸对象键（p/ 开头）。
func isPlatformObjectURL(u string) bool {
	if strings.Contains(u, storage.UploadsPathPrefix+storage.PlatformKeyPrefix) {
		return true
	}
	return strings.HasPrefix(u, storage.PlatformKeyPrefix)
}

var _ storage.CDNRefresher = (*DynamicCDN)(nil)
var _ storage.Resolver = (*Resolver)(nil)
