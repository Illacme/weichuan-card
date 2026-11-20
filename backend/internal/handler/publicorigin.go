package handler

// 「客户自有访问域名」出参侧的单点判定。
//
// 这个文件只回答一个问题：**这家企业的对象 URL 此刻该以哪个域名开头**。
// 9 条公开 GET 的 origin、/api/public/settings 的诊断出参，全部走同一份判定。
//
// 为什么必须单点：以前「后台说生效了」和「出参真的换了域名」是两套代码各自判断，
// 一旦漂移，表现是超管在界面上看到绿勾、访客那边的图却全裂（或反过来），
// 而且没有任何一边会报错。
//
// 最要紧的一条边界：**域名生效必须滞后于对象真的搬进客户桶**。
// 档案是 local / 私有桶 / 没绑分发域名时，对象仍躺在平台盘上；此刻把 URL
// 指到客户域名，客户域名的 /uploads/t/<企业id>/ 回源规则取不到对象，
// 全站裂图且无人报错。所以判定在 storageprofile.Resolver.OriginFor 里
// 按「谁在写对象」分叉，而不是在这里另写一份「看域名有没有值」的乐观判断。

import (
	"weichuan/internal/model"
)

// profileOriginResolver 由三处启动入口注入（cmd/server、cmd/devserver、测试 harness）。
//
// 为什么用函数变量而不是在 publicOriginFor 里直接读 storageprofile：handler 包
// 不该依赖具体解析器实现，测试也要能在不建档案表的情况下跑通。
//
// 返回值语义：
//
//	handled=false → 这家企业没有档案绑定，对象在平台盘上，出参用平台统一入口；
//	handled=true  → 档案接管了写入。此时 origin.Base 为空表示应当使用平台统一入口。
var profileOriginResolver func(cid int64) (model.Origin, bool)

// SetProfileOriginResolver 注入档案出参判定（幂等，重复调用以最后一次为准）。
func SetProfileOriginResolver(fn func(cid int64) (model.Origin, bool)) { profileOriginResolver = fn }

// profileOriginDiag 是诊断版（带「为什么没生效」），只服务运维排查接口。
// 与 profileOriginResolver 分开注入：出参路径只需要一个 Origin，
// 让它每次都算一遍原因文案是纯浪费。
var profileOriginDiag func(cid int64) (base string, active bool, reason string)

// SetProfileOriginDiag 注入档案诊断判定（幂等）。
func SetProfileOriginDiag(fn func(cid int64) (base string, active bool, reason string)) {
	profileOriginDiag = fn
}

// publicOriginFor 给公开出参取该企业此刻应使用的 Origin。
//
// 一家企业**只有一个权威来源**：谁在写它的对象，就由谁决定域名。
// 没有注入解析器（部分单测）或没有档案绑定时，一律返回平台零值。
//
// 刻意**不缓存**：配置一改出参必须立刻跟着变，再加一层 TTL / 版本缓存
// 就要多维护一套失效逻辑；解析本身已是内存快照查询。
func publicOriginFor(cid int64) model.Origin {
	if profileOriginResolver == nil || cid <= 0 {
		return model.PlatformOrigin()
	}
	origin, _ := profileOriginResolver(cid)
	return origin
}

// publicOriginDiagnosis 给 /api/public/settings 的运维诊断用。
// 没注入解析器时如实说明「未接入」，而不是假装生效。
func publicOriginDiagnosis(cid int64) (base string, active bool, reason string) {
	if profileOriginDiag == nil {
		return "", false, "本进程未接入存储档案解析"
	}
	return profileOriginDiag(cid)
}
