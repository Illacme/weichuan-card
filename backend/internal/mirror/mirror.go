// Package mirror 实现「上传成功后把云桶对象异步回拉一份到平台本地盘」。
//
// 三条不可变边界：
//  1. 出参不受影响。主写入与 URL 仍走原来的云桶 / CDN，镜像只是备份副本；
//  2. 失败只留痕，不阻断上传。对象已经写成功，回拉失败不该让用户重传；
//  3. 镜像目录独立于 /uploads 静态路由，否则私有桶对象一回拉就变成免鉴权公开文件。
//
// 传输走 ObjectCopier + ObjectStreamWriter 的流式接口，视频上百 MB 也不会
// 整段读进内存（ObjectStore.ReadObject 有 16MB 上限，只服务缩略图那条链路）。
package mirror

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/model"
	"weichuan/internal/safego"
	"weichuan/internal/setting"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// runAsync 是异步执行的间接层：生产用 safego.Run（panic 屏障，见该包注释），
// 测试替换成同步执行以便断言副作用。不要在生产代码里改成裸 go func()。
var runAsync = safego.Run

// copyTimeout 只是慢任务看门狗的阈值：真超时由各驱动的 HTTP 客户端控制。
// AfterFunc 不会中断已在进行的 S3/OSS 读取——强行中断会留下半截对象，
// 比慢一点糟得多；这里只把「卡住」这件事喊出来，让运维能发现。
const copyTimeout = 2 * time.Minute

// Manager 是本地镜像的入口。一个进程一个实例，启动时由两个 main 构造。
type Manager struct {
	db      *gorm.DB
	res     storage.Resolver
	dest    storage.ObjectStreamWriter
	deleter storage.ObjectDeleter
	timeout time.Duration
}

// New 从「备份存储」内置档案构造管理器。目录以档案的 local_dir 为唯一真值源：
// 启动时 EnsureBuiltinMirror 已把它同步成 cfg.MirrorDir，两边不会分叉。
func New(db *gorm.DB, res storage.Resolver, profile *model.StorageProfile) *Manager {
	if profile == nil {
		return nil
	}
	local := &storage.LocalUploader{
		Dir: strings.TrimSpace(profile.LocalDir), AllowExt: storage.DefaultAllowExt,
	}
	return &Manager{db: db, res: res, dest: local, deleter: local, timeout: copyTimeout}
}

// Enabled 判断某个租户是否开启镜像。两级相与：
//   - 平台总闸 local_mirror_enabled（即时生效，不需要重启）；
//   - 租户列 company.local_mirror_enabled（默认 0，逐家显式打开）。
//
// cid=0 是平台自身对象（小程序码、素材采用等 p/ 对象），不受租户列约束。
// 查库失败按关闭处理：备份是增强功能，不能因为一次库抖动拖垮上传链路。
func (m *Manager) Enabled(cid int64) bool {
	if m == nil || !setting.GetBool("local_mirror_enabled", true) {
		return false
	}
	if cid <= 0 || m.db == nil {
		return cid <= 0
	}
	var row struct {
		Enabled bool `gorm:"column:local_mirror_enabled"`
	}
	if err := m.db.Model(&model.Company{}).Select("local_mirror_enabled").
		Where("id = ?", cid).Limit(1).Scan(&row).Error; err != nil {
		return false
	}
	return row.Enabled
}

// Schedule 在上传成功后调用：通过开关与归属校验后，丢进后台任务执行。
// 调用方**不要**等待它，也不要把错误回给客户端。
func (m *Manager) Schedule(cid int64, key string) {
	if m == nil || !m.Enabled(cid) {
		return
	}
	if err := m.checkOwner(cid, key); err != nil {
		// key 来自服务端生成或客户端上报（直传确认），归属不符时绝不读 / 写别人的对象。
		log.Printf("[MIRROR][WARN] 拒绝镜像归属不符的对象 cid=%d key=%s: %v", cid, key, err)
		return
	}
	runAsync("local_mirror.copy", func() {
		timer := time.AfterFunc(m.timeout, func() {
			log.Printf("[MIRROR][WARN] 回拉超过 %s，可能存在网络阻塞 key=%s", m.timeout, key)
		})
		defer timer.Stop()
		if err := m.Copy(cid, key); err != nil {
			log.Printf("[MIRROR][WARN] 回拉失败 cid=%d key=%s: %v", cid, key, err)
			if m.db != nil {
				audit.Record(m.db, nil, audit.Entry{
					Action: audit.ActionMirrorBackup, ActorType: audit.ActorSystem,
					TargetType: "storage_object", Result: audit.ResultFail,
					Detail: map[string]any{"tenant_id": cid, "ext": strings.ToLower(filepath.Ext(key))},
				})
			}
		}
	})
}

// Copy 做一次同步回拉。导出是为了测试与运维补拉；正常上传走 Schedule。
func (m *Manager) Copy(cid int64, key string) error {
	if m == nil || m.res == nil || m.dest == nil {
		return fmt.Errorf("本地镜像未初始化")
	}
	if err := m.checkOwner(cid, key); err != nil {
		return err
	}
	// 平台对象（p/ 前缀）写在 Platform() 实例里，不是「全局默认绑定」。
	// 这两者常常不是同一个桶：`resolve(0)` 用的是 storage_bindings.tenant_id=0，
	// 而素材采用 / 小程序码走的是 SetPlatform 注入的平台实例。用 For(0) 会去
	// 一个不含该对象的桶里找，结果是 p/ 对象全部回拉失败、且只留一行 warn。
	var up storage.Uploader
	if cid <= 0 {
		up = m.res.Platform()
		if up == nil {
			return fmt.Errorf("平台存储实例不可用")
		}
	} else {
		var err error
		up, err = m.res.For(cid)
		if err != nil {
			return fmt.Errorf("读取存储实例失败：%w", err)
		}
	}
	// 源已经在本地盘：它本身就是本地副本，再复制一份只会让磁盘翻倍。
	// 云桶租户才需要回拉；本地档案租户要备份应改走备份脚本打 uploads 目录。
	if _, ok := up.(*storage.LocalUploader); ok {
		return nil
	}
	copier, ok := up.(storage.ObjectCopier)
	if !ok {
		return fmt.Errorf("当前存储驱动不支持流式回拉")
	}
	if err := copier.CopyObjectTo(m.dest, key); err != nil {
		return fmt.Errorf("流式回拉失败：%w", err)
	}
	log.Printf("[MIRROR][OK] cid=%d key=%s", cid, key)
	return nil
}

// Remove 删除本地镜像副本。删除主对象时双清用；不存在按成功处理。
func (m *Manager) Remove(key string) error {
	if m == nil || m.deleter == nil {
		return nil
	}
	if _, err := storage.ParseRef(key); err != nil {
		return err
	}
	return m.deleter.DeleteObject(key)
}

// checkOwner 校验对象键归属与调用方算出的 cid 一致。
// 与 thumbEnsureHandler / signHandler 同一口径，避免「签名允许、镜像拒绝」这类
// 自相矛盾的封锁。
func (m *Manager) checkOwner(cid int64, key string) error {
	ref, err := storage.ParseRef(key)
	if err != nil {
		return fmt.Errorf("对象键非法或为历史形态：%w", err)
	}
	want := storage.ScopeTenant
	if cid <= 0 {
		want = storage.ScopePlatform
	}
	if ref.Scope != want || ref.CompanyID != cid {
		return fmt.Errorf("对象归属与租户不符")
	}
	return nil
}
