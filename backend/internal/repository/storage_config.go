package repository

import (
	"errors"
	"fmt"

	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"

	"gorm.io/gorm"
)

// ErrStorageConfigMissing 表示 tenant_storage_configs 表还不存在（迁移 0044 未应用）。
//
// 单独定义一个哨兵错误，是因为这件事在运维现场的**特征与别的错误完全不同**：
//
//   - 它不是「配置写错了」，改配置没用，重跑也不会好；
//   - 它最可能的成因是「只换了二进制、没同步 migrations/ 目录」（2026-11-03 事故的同款），
//     或者刚升级完还没跑 `./server migrate`；
//   - 一旦被当成「查询失败，跳过」处理，生成器会安静地产出零条 snippet，
//     对账还全绿，然后线上所有租户的图 404。
//
// 所以它必须是**可被调用方区分出来并单独给出处置建议**的错误，而不是一句
// 混在一起的 "Error 1146"。
var ErrStorageConfigMissing = errors.New(
	"tenant_storage_configs 表不存在：先跑 `./server migrate` 应用迁移 0044（" +
		"若目录里也没有 0044_tenant_storage_configs.sql，说明发布包不完整，先同步整个 migrations/）",
)

// StorageTarget 是「生成 nginx snippet 所需」的租户存储配置最小视图。
//
// 为什么不是直接返回 model.TenantStorageConfig：
// 本视图只 SELECT 了 8 个非密文列（见 storageTargetCols），其余字段在模型里
// 全是零值。若返回完整模型，调用方看到 PublicRead=false / KeyID="" 无从分辨
// 那是「库里就是 false」还是「根本没查这一列」——这类误读在三个月后一定会发生。
// 给它一个独立类型，缺什么就编译不过，比注释可靠。
//
// 更要紧的是：**凭据列不能出现在这个类型上**。cmd/nginx-snippets 跑在能写
// nginx 配置的机器上，它没有任何理由把租户的 AccessKey/SecretKey 读进内存，
// 更没有理由在 --verbose 之类的功能里不小心打出来。少一个字段，少一条泄露路径。
type StorageTarget struct {
	CompanyID  int64
	Driver     string
	Endpoint   string
	Bucket     string
	UseSSL     bool
	HostHeader string
	Enabled    bool
	// Status 是连通性自检结果（unknown / ok / error）。生成器不用它做判定，
	// 但要能把它报给运维：一个 status=error 的租户要么正在出故障，要么刚配完还没自检。
	Status string
}

// storageTargetCols 是生成 snippet 要读的全部列，逐列写死。
//
// 别改成 `*` 或留给 gorm 默认：默认会把 access_key_enc / secret_key_enc 一起
// SELECT 回来，密文进内存没有任何收益，只有风险（内存 dump、错误时被 %+v 打出来）。
const storageTargetCols = "company_id, driver, endpoint, bucket, use_ssl, host_header, enabled, status"

// NginxTarget 把库里的行映射成渲染用的 Target。
//
// 这里只做字段搬运，**不做任何过滤**：enabled=false 与 driver=local 要不要生成
// snippet，判定只允许在 nginxsnippet.Target.NeedsSnippet 一处（见其注释：
// 只判 enabled 会在驱动改成 local 时留下一条指向不存在桶的 location，
// 那家租户全站图 404，而后台显示配置是好的）。
func (t StorageTarget) NginxTarget() nginxsnippet.Target {
	return nginxsnippet.Target{
		CompanyID:  t.CompanyID,
		Driver:     t.Driver,
		Endpoint:   t.Endpoint,
		Bucket:     t.Bucket,
		UseSSL:     t.UseSSL,
		HostHeader: t.HostHeader,
		Enabled:    t.Enabled,
	}
}

// ListStorageTargets 读出全部租户的存储配置（含未启用的行，交给 NeedsSnippet 判定）。
//
// 三种结果必须彼此可区分，这是本函数存在的全部理由：
//
//	表不存在  → ErrStorageConfigMissing（运维按它给的步骤处理）
//	查询出错  → 原样包装返回，**绝不**返回空切片 + nil
//	真的没人配 → ([] , nil)，调用方据此输出「当前没有任何租户接管存储」
//
// 前两种若退化成第三种，症状是「生成器成功、snippet 为空、nginx -t 通过、
// 平台照常工作」，直到某天某租户上传完发现图全裂——而那时距离出错已经很久了。
func ListStorageTargets(db *gorm.DB) ([]StorageTarget, error) {
	// 先打一发 SELECT 1，再问「表在不在」。
	//
	// 理由是 gorm 的 Migrator().HasTable 只返回 bool，**把 error 吞掉了**
	// （内部那个 information_schema / sqlite_master 查询失败也只是 count 保持 0）。
	// 于是「库停了 / 网络抖了 / 权限不够读系统表」会和「表不存在」得到同一个 false，
	// 运维拿到一句「先跑 ./server migrate」——提示指向偏了，还得再排查一轮。
	//
	// 注意分工：真实的「连不上」大多在 dbopen.Open 就失败了（gorm.Open 默认 Ping），
	// 这一句兜的是连接已建立但随后失效、以及将来有人给 gorm.Config 加上
	// DisableAutomaticPing 之后的情形。
	if err := db.Exec("SELECT 1").Error; err != nil {
		return nil, fmt.Errorf("数据库不可达，无法读取 tenant_storage_configs：%w", err)
	}
	if !db.Migrator().HasTable(&model.TenantStorageConfig{}) {
		return nil, ErrStorageConfigMissing
	}

	// 按 company_id 排序：输出稳定，运维用 diff 比对两份 snippet 时不会被
	// 行序变化淹掉真正的差异（Render 里还会再排一次，那是对账用的，不冲突）。
	var rows []model.TenantStorageConfig
	if err := db.Model(&model.TenantStorageConfig{}).
		Select(storageTargetCols).
		Order("company_id asc").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 tenant_storage_configs 失败：%w", err)
	}

	out := make([]StorageTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, StorageTarget{
			CompanyID:  r.CompanyID,
			Driver:     r.Driver,
			Endpoint:   r.Endpoint,
			Bucket:     r.Bucket,
			UseSSL:     r.UseSSL,
			HostHeader: r.HostHeader,
			Enabled:    r.Enabled,
			Status:     r.Status,
		})
	}
	return out, nil
}
