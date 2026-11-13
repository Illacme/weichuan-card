package model

import (
	"weichuan/internal/storage"
)

// 出参侧资源引用解析：把库里的任一形态（云驱动绝对 URL / 本地 /uploads/ 相对路径 /
// 裸键 / p/ / t/ 形态）统一解析成端上可访问的 URL。
//
// 现阶段平台统一入口与源站同源，origin 留空——对现行生产数据是**零行为变更**：
//   - 云驱动绝对 URL 原样返回（线上 s3 驱动的上传对象本就是完整 CDN URL）；
//   - 本地 /uploads/ 相对路径原样保留（nginx / 端上 baseUrl 同源可达）；
//   - 写入侧收敛成裸键后，这里会自动补回 /uploads/ 前缀，端上契约不变。
//
// 解析失败时返回原值：当前生产数据不存在不安全引用（data:/javascript:），返回原值
// 与「直接出库」现状一致；一旦出现此类脏数据，同现状一样原样出参，由写入侧校验兜底，
// 不在此处静默降级成某个猜测 URL（避免「没人报错时用户无从察觉」）。
//
// **覆盖范围是「所有出参」，不只是公开接口**：后台编辑页拿到裸键同样会裂图，
// 而且更难被发现（公开页正常、只有运营看到预览破图，第一反应会怪前端）。
// 所以 R11–R15 那些后台出口也在这一层收口，与公开接口同一口径。
//
// 一条落地纪律：**只解析「本次请求不会再写库」的值**。
//   - GET：查询结果是局部变量，就地解析；
//   - PUT/POST：先落库，再对**副本**解析后出参（`resp := x; resp.ResolveRefs()`）。
//
// 反过来（先解析再 Save）会把 `/uploads/t/5/x.jpg` 写回库，形态从裸键退回历史形态，
// 而且没有任何报错——`upload_base_url` 改动影响存量的老问题就是这么复发的。
//
// 为什么放在 model 包而不是 handler 包：后台与公开接口里既有整模型序列化、也有
// 显式 gin.H；整模型的情况用模型方法最干净，handler 只需在序列化前一刀 ResolveRefs()。
// 详见 docs/tenant-storage-url-boundaries.md §3。
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

// ResolveResourceRefs 批量解析，给 album 这种「JSON 数组字符串展开成 []string」的字段用。
//
// nil / 空切片原样返回：调用方常把这个值直接塞进响应，返回一个非 nil 的空切片会让
// 「没有图集」从 JSON `null` 变成 `[]`——那是契约变化，前端判空逻辑会跟着变。
func ResolveResourceRefs(list []string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = ResolveResourceRef(v)
	}
	return out
}

// ResolveRefs 把模型自身的资源型字段就地解析成端上可访问 URL。
// 这些方法只在「出参」路径调用（公开只读 + 后台编辑回填），绝不写回数据库。
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
	// Album 是 json:"-" 的 JSON 数组字符串，模型方法里解不开它——
	// 展开点有两处（公开 /api/projects/:id 与后台 projectToMap），各自展开后调
	// ResolveResourceRefs。守门会盯住「有展开点就必须有解析」。
	p.Cover = ResolveResourceRef(p.Cover)
}

func (s *BizService) ResolveRefs() { s.Icon = ResolveResourceRef(s.Icon) }
