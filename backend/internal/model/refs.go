package model

import (
	"weichuan/internal/storage"
)

// 出参侧资源引用解析：把库里的任一形态（云驱动绝对 URL / 本地 /uploads/ 相对路径 /
// 裸键 / p/ / t/ 形态）统一解析成端上可访问的 URL。
//
// Origin 是**出参 URL 的访客侧原点**，由调用方按 company_id 决定（第六十九轮接入）：
//   - 传 model.PlatformOrigin() = 走平台统一入口，返回相对 /uploads/...。
//     对现行生产数据是零行为变更：云驱动绝对 URL 原样返回；本地相对路径原样
//     保留；裸键自动补回 /uploads/ 前缀。
//   - 传 model.Origin{Base: "https://img.cust.com", CompanyID: 5} =
//     该企业自己的对象 URL 以客户自有域名开头；平台对象 / 历史裸键仍走平台入口。
//
// 为什么不把 origin 做成包级全局 / context 隐式取值：出参点分散在 26 处，隐式取值
// 会让「这个接口到底用哪家企业的域名」变成看不见的输入，漏接一处就是**跨租户串号**
// （A 企业的图指到 B 的域名）。显式传参后，每个出参点都必须当场回答这个问题，
// 漏改一处直接编译不过。origin 的合法性由 storage.ValidatePublicOrigin 单点裁判，
// 「按哪家企业取」由 handler.publicOriginState / publicOriginFor 单点判定，两侧共用同一份规则。
//
// 端上契约不变：小程序 utils/request.ts 的 assetUrl 对 ^https?:// 原样返回，
// 所以从相对 /uploads/ 换成绝对客户域名 URL 不需要改任何端上拼接代码。
//
// 解析失败时返回原值：当前生产数据不存在不安全引用（data:/javascript:），返回原值
// 与「直接出库」现状一致；一旦出现此类脏数据，同现状一样原样出参，由写入侧校验兜底，
// 不在此处静默降级成某个猜测 URL（避免「没人报错时用户无从察觉」）。
//
// **覆盖范围是「所有出参」，不只是公开接口**：后台编辑页拿到裸键同样会裂图，
// 而且更难被发现（公开页正常、只有运营看到预览破图，第一反应会怪前端）。
// 所以 R11–R15 那些后台出口也在这一层收口，与公开接口同一口径。
// 但后台出口**一律传 model.PlatformOrigin()**：管理端跑在平台域名下，/uploads/ 同源可达；
// 客户自有域名是访客侧的事，让后台预览也依赖客户证书 / 回源规则只会多一条无谓的裂图路径。
//
// 一条落地纪律：**只解析「本次请求不会再写库」的值**。
//   - GET：查询结果是局部变量，就地解析；
//   - PUT/POST：先落库，再对**副本**解析后出参（`resp := x; resp.ResolveRefs(model.PlatformOrigin())`）。
//
// 反过来（先解析再 Save）会把 `/uploads/t/5/x.jpg` 写回库，形态从裸键退回历史形态，
// 而且没有任何报错——`upload_base_url` 改动影响存量的老问题就是这么复发的。
//
// 为什么放在 model 包而不是 handler 包：后台与公开接口里既有整模型序列化、也有
// 显式 gin.H；整模型的情况用模型方法最干净，handler 只需在序列化前一刀 ResolveRefs()。
// 详见 docs/tenant-storage-url-boundaries.md §3。
// Origin 是一次出参解析的访客侧原点。
//
// 为什么不是单传一个字符串：客户自有域名只对「归属该企业的对象键」有效，
// 平台对象 / 历史裸键必须继续走平台入口（见 storage.ResolveRefForTenant）。
// 把 CompanyID 一起带上，归属判定才有依据；只传域名的话，解析器只能在
// 「无差别拼域名」和「全部忽略」之间二选一，两条都是错的。
type Origin struct {
	Base      string // 纯 origin，如 https://img.cust.com；空串 = 平台统一入口
	CompanyID int64  // Base 非空时必须 > 0
}

// PlatformOrigin 是「走平台统一入口」的零值。后台出参一律用它。
func PlatformOrigin() Origin { return Origin{} }

func ResolveResourceRef(raw string, o Origin) string {
	if raw == "" {
		return ""
	}
	out, err := storage.ResolveRefForTenant(raw, o.Base, o.CompanyID)
	if err != nil {
		return raw
	}
	return out
}

// ResolveResourceRefs 批量解析，给 album 这种「JSON 数组字符串展开成 []string」的字段用。
//
// nil / 空切片原样返回：调用方常把这个值直接塞进响应，返回一个非 nil 的空切片会让
// 「没有图集」从 JSON `null` 变成 `[]`——那是契约变化，前端判空逻辑会跟着变。
func ResolveResourceRefs(list []string, o Origin) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = ResolveResourceRef(v, o)
	}
	return out
}

// ResolveRefs 把模型自身的资源型字段就地解析成端上可访问 URL。
// o 语义见文件头与 Origin：公开出参传按 company_id 取到的 Origin，后台出参传
// model.PlatformOrigin()。这些方法只在「出参」路径调用，绝不写回数据库。
// 字段集合严格对齐 docs/tenant-storage-url-boundaries.md §2（含同名非 URL 字段白名单），
// 新增资源字段时必须同步这里与边界清单。

func (e *Employee) ResolveRefs(o Origin) { e.Avatar = ResolveResourceRef(e.Avatar, o) }

func (ci *CompanyInfo) ResolveRefs(o Origin) {
	ci.Logo = ResolveResourceRef(ci.Logo, o)
}

func (cp *CompanyProfile) ResolveRefs(o Origin) {
	cp.Cover = ResolveResourceRef(cp.Cover, o)
	cp.VideoURL = ResolveResourceRef(cp.VideoURL, o)
	cp.VideoBackupURL = ResolveResourceRef(cp.VideoBackupURL, o)
}

func (c *AchievementCategory) ResolveRefs(o Origin) {
	c.Icon = ResolveResourceRef(c.Icon, o)
}

func (p *ProjectCase) ResolveRefs(o Origin) {
	// Album 是 json:"-" 的 JSON 数组字符串，模型方法里解不开它——
	// 展开点有两处（公开 /api/projects/:id 与后台 projectToMap），各自展开后调
	// ResolveResourceRefs。守门会盯住「有展开点就必须有解析」。
	p.Cover = ResolveResourceRef(p.Cover, o)
}

func (s *BizService) ResolveRefs(o Origin) {
	s.Icon = ResolveResourceRef(s.Icon, o)
}
