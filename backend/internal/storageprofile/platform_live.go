package storageprofile

// 平台自身实例的运行期热更新。
//
// 迁移 0059 / 0060 后，「主存储」与「平台自身 CDN」两条内置档案是平台实例
// （上传器 / 刷新器）的唯一连接信息真值源。但历史上这两个实例在启动期构造一次、
// 之后作为固定指针注入各 handler，于是改凭据 / 驱动 / 厂商必须重启才生效——
// 与「绑定改完下一个请求生效」形成两套语义，管理员最容易踩的坑。
//
// 本文件让 Resolver 按内置档案的 version 现建并缓存平台实例：
//   - Platform() / DriverFor() 读「主存储」档案；
//   - PlatformCDNRefresher() 读「平台自身 CDN」档案。
//
// 两条纪律（与租户实例一致，不能退）：
//  1. 只认 builtin && purpose=platform_primary 的那一行，**绝不**读全局绑定档案。
//     p/ 平台对象一旦写进「无绑定企业的默认桶」，URL 仍是平台域名，取图必 404。
//  2. 构造失败时保留上一次成功的实例（平台上传器）或明确「暂停刷新」（CDN），
//     绝不静默回落到别的配置。凭据填错要先能被管理员在后台改正。
//
// 目录类（upload_dir / mirror_dir）不在本文件范围：它们绑在 r.Static 与镜像回拉
// 目录上，改目录仍需重启，界面与文档照旧说明。

import (
	"fmt"
	"log"
	"strings"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/storage"
)

// platformInstance 是一份完整替换的平台上传器快照。整份放进 atomic.Value，
// 保证并发请求要么全看到旧实例、要么全看到新实例，不会出现
// 「这次上传走新桶、下次走旧桶」这种最难自查的漂移。
type platformInstance struct {
	up      storage.Uploader
	driver  string
	version string // 主存储档案的指纹：id:version:driver
	// baseURL / public 与 up 取自**同一份**构造成功的档案。
	// 关键：构造失败时整份实例（含这两个字段）一起保留旧的，绝不能出现
	// 「上传器还在旧桶、写入归一却用新基址」这种剥不出键的静默漂移。
	baseURL string
	public  bool
}

// platformRefresherEntry 是平台 CDN 刷新器快照。rf=nil 是**有效状态**：
// 表示「档案选中了不刷新」或「凭据无效、暂停刷新」，与「还没读到档案」区分开。
type platformRefresherEntry struct {
	rf      storage.CDNRefresher
	version string
}

// platformPrimary 从当前快照取「主存储」内置档案。只认 purpose，不认全局绑定。
func (r *Resolver) platformPrimary() *model.StorageProfile {
	s := r.snapshot()
	if s == nil {
		return nil
	}
	for _, sp := range s.Storages {
		if sp.Builtin && sp.Purpose == model.ProfilePurposePlatformPrimary {
			return sp
		}
	}
	return nil
}

// platformCDNPrimary 从当前快照取「平台自身 CDN」内置分发档案。
func (r *Resolver) platformCDNPrimary() *model.DeliveryProfile {
	s := r.snapshot()
	if s == nil {
		return nil
	}
	for _, dp := range s.Deliveries {
		if dp.Builtin && dp.Purpose == model.DeliveryPurposePlatformPrimary {
			return dp
		}
	}
	return nil
}

// livePlatform 按主存储档案现算平台实例；version 未变时直接复用缓存。
// 返回 nil 表示「当前没有可用的主存储档案」或「构造失败」，调用方回落到启动兜底实例。
func (r *Resolver) livePlatform() *platformInstance {
	sp := r.platformPrimary()
	if sp == nil {
		return nil
	}
	// 指纹必须覆盖所有影响实例构造的字段。档案任何字段改动都会 increment
	// version（写接口统一 row.Version++），所以版本足够，重复拼字段只是双保险。
	ver := fmt.Sprintf("%d:%d:%s", sp.ID, sp.Version, sp.Driver)
	if v := r.platformUp.Load(); v != nil {
		inst := v.(*platformInstance)
		if inst.version == ver {
			return inst
		}
	}
	// 串行化构建：慢的旧版本构建完成得晚时，不能覆盖已经缓存的新版本。
	// 锁内再查一次，前一个请求已经建好就直接复用。
	r.platformBuildMu.Lock()
	defer r.platformBuildMu.Unlock()
	if v := r.platformUp.Load(); v != nil {
		inst := v.(*platformInstance)
		if inst.version == ver {
			return inst
		}
	}
	up, err := r.buildPlatformUploader(sp)
	if err != nil {
		// 改错凭据不能让平台上传整体瘫掉：整份保留上一次成功的实例
		// （含 baseURL/public），管理员还能进后台把它改回来。
		// 日志按 version 去重：配错期间每个请求都会走到这里，不去重会刷屏。
		if r.warnedStorageVer != ver {
			r.warnedStorageVer = ver
			log.Printf("[STORAGE][WARN] 主存储配置 %d 重建实例失败，暂用上一份可用实例："+
				"请到「存储与分发 → 文件存储 → 主存储」修正凭据后保存（无需重启）：%v", sp.ID, err)
		}
		if v := r.platformUp.Load(); v != nil {
			return v.(*platformInstance)
		}
		return nil
	}
	// 成功一次就清掉告警记忆，下次配错仍能打出来。
	r.warnedStorageVer = ""
	inst := &platformInstance{
		up: up, driver: sp.Driver, version: ver,
		baseURL: strings.TrimSpace(sp.BaseURL), public: sp.PublicRead,
	}
	r.platformUp.Store(inst)
	return inst
}

// buildPlatformUploader 按主存储档案构造平台上传器，口径与 BuildPlatformUploader 一致：
// local 用部署期目录（r.localDir = cfg.UploadDir，目录类本轮不热更新），
// 云驱动复用档案侧的 buildUploader（自检与真实上传同一套构造逻辑）。
func (r *Resolver) buildPlatformUploader(sp *model.StorageProfile) (storage.Uploader, error) {
	if sp.Driver == nginxsnippet.DriverLocal {
		base := strings.TrimSpace(sp.BaseURL)
		if base == "" {
			base = "/uploads" // local 档案未填基址时用相对路径，与 localUploader 同口径
		}
		return &storage.LocalUploader{
			Dir: r.localDir, BaseURL: base,
			MaxBytes: maxBytes, AllowExt: storage.DefaultAllowExt,
		}, nil
	}
	return buildUploader(uploaderConfigFor(sp, 0))
}

// PlatformCDNRefresher 按「平台自身 CDN」档案现算平台刷新器。
//
// 第二个返回值 found 区分两种「没有刷新器」：
//   - found=false：还没有这份内置档案（未迁移的旧库 / 测试 harness）→ 调用方回落启动期 base；
//   - found=true 且 rf=nil：档案明确「不刷新」或凭据无效 → 真的不刷，不能回落 base。
//
// 这条区分是第八十一轮「p/ 不得借用全局分发档案」纪律的延续：
// 档案存在就以它为唯一真值源，不静默用另一份配置顶替。
func (r *Resolver) PlatformCDNRefresher() (storage.CDNRefresher, bool) {
	dp := r.platformCDNPrimary()
	if dp == nil {
		return nil, false
	}
	ver := fmt.Sprintf("%d:%d:%s:%s:%s:%s:%s", dp.ID, dp.Version, dp.Provider,
		dp.AccessKey, dp.SecretKey, dp.Endpoint, dp.ResourceID)
	if v := r.platformCDNUp.Load(); v != nil {
		e := v.(*platformRefresherEntry)
		if e.version == ver {
			return e.rf, true
		}
	}
	// 与平台上传器同理：串行化构建，防止旧版本慢构建覆盖新版本。
	r.platformCDNBuildMu.Lock()
	defer r.platformCDNBuildMu.Unlock()
	if v := r.platformCDNUp.Load(); v != nil {
		e := v.(*platformRefresherEntry)
		if e.version == ver {
			return e.rf, true
		}
	}
	rf, err := BuildPlatformRefresher(dp)
	if err != nil {
		// 暂停刷新而非回落：凭据无效时让管理员看见「刷新没生效」，
		// 而不是拿另一套凭据刷到错误的边缘。上传主流程不受影响。
		log.Printf("[CDN][WARN] 平台自身 CDN 配置 %d 凭据无效，暂停刷新："+
			"请到「存储与分发 → CDN 分发 → 平台自身 CDN」修正后保存（无需重启）：%v", dp.ID, err)
		r.platformCDNUp.Store(&platformRefresherEntry{version: ver})
		return nil, true
	}
	r.platformCDNUp.Store(&platformRefresherEntry{rf: rf, version: ver})
	return rf, true
}

// PlatformCDNConfigured 回报平台是否真的有可用的刷新器。
//
// 不能只看 provider 非 none：凭据写错时 PlatformCDNRefresher 返回 (nil, true)
// （暂停刷新），此时界面若还显示「已启用」，管理员会以为违规图下架已经让
// 边缘失效、实际没有——这是最贵的一种静默失败。所以这里以现建的刷新器为准。
func (r *Resolver) PlatformCDNConfigured() bool {
	rf, found := r.PlatformCDNRefresher()
	return found && rf != nil
}

// PlatformBaseURL / PlatformPublic 是平台主存储**当前生效实例**的访问基址与公开读开关，
// 供 handler 侧写入归一 / 内容安全下架 / 私有文件流在**运行期**读取。
//
// 读的是 livePlatform 那份与上传器同源的快照，而不是直接读档案：
// 档案写错凭据导致构造失败时，上传器保留旧实例，这两个值也必须跟着是旧的，
// 否则「上传器还在旧桶、归一却按新基址剥前缀」会产生剥不出键的静默失败。
//
// ok=false 表示还没有可用的实例（未迁移旧库 / 首次构造就失败），
// 调用方必须回落到启动期 cfg，不能拿零值当真值。
func (r *Resolver) PlatformBaseURL() (string, bool) {
	inst := r.livePlatform()
	if inst == nil {
		return "", false
	}
	return inst.baseURL, true
}

func (r *Resolver) PlatformPublic() (bool, bool) {
	inst := r.livePlatform()
	if inst == nil {
		return false, false
	}
	return inst.public, true
}
