package model

import (
	"weichuan/internal/storage"
)

// 出参侧资源引用解析：把库里的任一形态（云驱动绝对 URL / 本地 /uploads/ 相对路径 /
// 将来出现的裸键 / p/ / t/ 形态）统一解析成端上可访问的 URL。
//
// 现阶段平台统一入口与源站同源，origin 留空——对现行生产数据是**零行为变更**：
//   - 云驱动绝对 URL 原样返回（线上 s3 驱动的上传对象本就是完整 CDN URL）；
//   - 本地 /uploads/ 相对路径原样保留（nginx / 端上 baseUrl 同源可达）；
//   - 将来写入侧收敛成裸键后，这里会自动补回 /uploads/ 前缀，端上契约不变。
//
// 解析失败时返回原值：当前生产数据不存在不安全引用（data:/javascript:），返回原值
// 与「直接出库」现状一致；一旦出现此类脏数据，同现状一样原样出参，由写入侧校验兜底，
// 不在此处静默降级成某个猜测 URL（避免「没人报错时用户无从察觉」）。
//
// 为什么放在 model 包而不是 handler 包：9 条公开只读接口里既有整模型序列化、也有
// 显式 gin.H；整模型的情况用模型方法最干净，handler 只需在序列化前一刀 ResolveRefs()。
// 详见 docs/tenant-storage-url-boundaries.md §3（R11–R15 的结论：统一出口优于逐点改 handler）。
func ResolveResourceRef(raw string) string {
	if raw == "" {
		return ""
	}
	out, err := storage.ResolveRef(raw, "")
	if err != nil {
		return raw
	}
	return out
}

// ResolveRefs 把模型自身的资源型字段就地解析成端上可访问 URL。
// 这些方法只在「公开只读出参」路径调用，绝不写回数据库——只读后序列化即丢弃。
// 字段集合严格对齐 docs/tenant-storage-url-boundaries.md §2（含同名非 URL 字段白名单），
// 新增资源字段时必须同步这里与边界清单。

func (e *Employee) ResolveRefs() { e.Avatar = ResolveResourceRef(e.Avatar) }

func (ci *CompanyInfo) ResolveRefs() { ci.Logo = ResolveResourceRef(ci.Logo) }

func (cp *CompanyProfile) ResolveRefs() {
	cp.Cover = ResolveResourceRef(cp.Cover)
	cp.VideoURL = ResolveResourceRef(cp.VideoURL)
	cp.VideoBackupURL = ResolveResourceRef(cp.VideoBackupURL)
}

func (c *AchievementCategory) ResolveRefs() { c.Icon = ResolveResourceRef(c.Icon) }

func (p *ProjectCase) ResolveRefs() {
	// Album 是 json:"-" 的 JSON 数组字符串，只在项目详情接口显式展开出参，
	// 不在此处处理（见 handler.go /api/projects/:id）。这里只解析序列化字段 Cover。
	p.Cover = ResolveResourceRef(p.Cover)
}

func (s *BizService) ResolveRefs() { s.Icon = ResolveResourceRef(s.Icon) }
