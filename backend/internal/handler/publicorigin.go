package handler

// 「客户自有访问域名」出参侧的单点判定（第六十九轮）。
//
// 这个文件只回答一个问题：**这家企业的对象 URL 此刻该以哪个域名开头**。
// 后台配置视图的 publicBaseUrlActive、9 条公开 GET 的 origin、
// /api/public/settings 的诊断出参，全部走同一份判定。
//
// 为什么必须单点：以前「后台说生效了」和「出参真的换了域名」是两套代码各自判断，
// 一旦漂移，表现是超管在界面上看到绿勾、访客那边的图却全裂（或反过来），
// 而且没有任何一边会报错。现在两者共用 publicOriginState，漂移在结构上不可能发生。
//
// 最要紧的一条边界：**域名生效必须滞后于对象真的搬进客户桶**。
// TENANT_STORAGE_ROUTE=off / enabled=false / driver=local / 凭据不全时，
// 对象仍躺在平台盘上；此刻把 URL 指到客户域名，客户域名的
// /uploads/t/<企业id>/ 回源规则取不到对象，全站裂图且无人报错。
// 所以判定直接复用 tenantStorageActive（它按「谁会先挡住上传」的顺序检查），
// 而不是另写一份「只看 public_base_url 有没有值」的乐观判断。
//
// 这条边界同时保证本轮是**零行为变更的纯代码侧改动**：开关默认 off 时，
// 每一家的 publicOriginFor 都返回平台零值，出参与第六十八轮完全一致。
// 翻开关属于运营动作，不在本轮。

import (
	"log"
	"strings"

	"weichuan/internal/model"
	"weichuan/internal/storage"

	"gorm.io/gorm"
)

// publicOriginState 判定一行配置此刻的 origin 实况。
//
// 三态缺一不可，少了任何一个都没法向超管解释「为什么没生效」：
//
//	base   应当传给 model.ResolveRefs 的纯 origin；未生效时恒为空串
//	active publicBaseUrlActive 的实况（只有它为 true 时出参才真的换域名）
//	reason 未生效原因，可直接展示给超管；没登记域名时为空串（界面不该挂警告）
func publicOriginState(row *model.TenantStorageConfig) (base string, active bool, reason string) {
	if row == nil {
		return "", false, "尚未配置该企业的自有存储"
	}
	// 与写入侧、ResolveRef 出参侧共用同一个格式裁判。手工 SQL 塞进来的带路径
	// 坏值在这里被挡住，退化到平台入口，绝不能拼进端上 URL。
	clean, err := storage.ValidatePublicOrigin(row.PublicBaseURL)
	if err != nil {
		return "", false, "自有访问域名不合法：" + err.Error()
	}
	if clean == "" {
		return "", false, ""
	}
	// 对象还没真进客户桶之前，不换域名（理由见文件头）。
	if ok, why := tenantStorageActive(row); !ok {
		return "", false, why
	}
	// 私有桶直连会 403：上传侧 uploadroute.build 已经拒绝 false，
	// 出参侧必须同样拒绝，否则「上传失败、存量 URL 却全变了」更难查。
	if !row.PublicRead {
		return "", false, "桶未开启公开读（publicRead=false）：客户域名直连私有桶会 403"
	}
	return clean, true, ""
}

// publicOriginStateFor 按 company_id 取实况：读库 + publicOriginState 的唯一组合点。
// 9 条公开 GET 与 /api/public/settings 都走这里，读库失败的处理也只写一份。
//
// 读库失败时退回平台入口并打一条 WARN：这不是静默降级——平台 nginx 仍有
// /uploads/t/<企业id>/ 回源，图不会裂，只是没走客户域名；日志让运维能查到
// 「为什么今天没切」。后台视图的 active 由 tenantStorageView 独立计算，不受影响。
func publicOriginStateFor(db *gorm.DB, cid int64) (base string, active bool, reason string) {
	if cid <= 0 {
		return "", false, "企业 id 不合法"
	}
	row, err := loadTenantStorage(db, cid)
	if err != nil {
		log.Printf("[STORAGE][WARN] 企业 %d 的自有域名读取失败，出参退回平台统一入口：%v", cid, err)
		return "", false, "读取存储配置失败：" + err.Error()
	}
	return publicOriginState(row)
}

// publicOriginFor 给公开出参取该企业此刻应使用的 Origin。
//
// 刻意**不缓存**：配置一改（PUT 会刷 updated_at）出参必须立刻跟着变，
// 再加一层 TTL / 版本缓存就要多维护一套失效逻辑；这里只是主键上的一次轻量查询，
// 而 9 条公开 GET 里 5 条本来就有 60 秒边缘缓存。
func publicOriginFor(db *gorm.DB, cid int64) model.Origin {
	base, active, _ := publicOriginStateFor(db, cid)
	if !active || base == "" {
		return model.PlatformOrigin()
	}
	return model.Origin{Base: base, CompanyID: cid}
}

// publicBaseURLNote 给超管的「生效 / 未生效」文案。
//
// base 传**库里的登记值**，不是 publicOriginState 返回的 base：后者在「未生效」
// 时恒为空串（那是「该不该进出参」的口径），拿它拼文案会得到整段空字符串，
// 超管反而看不到自己登记的是哪个域名。
//
// 没登记时返回空串：每家没配的企业都挂一条看似的故障提示，会把真正需要
// 注意的那家淹掉。生效与未生效是两套文案，不能只把旧的「尚未接入」改几个字——
// 已生效还写「出参侧尚未接入」就是新一轮的误导。
func publicBaseURLNote(base string, active bool, reason string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	const ingress = "接入层须在该域名上配好 /uploads/t/<企业id>/ 的回源规则与证书，且桶为公开读。"
	if active {
		return "自有访问域名 " + base + " 已生效：该企业公开出参里归属自己的对象键会以它开头；" +
			"平台对象（p/ 前缀）、历史无前缀裸键与合法外链仍走平台统一入口。" + ingress
	}
	why := strings.TrimSpace(reason)
	if why == "" {
		why = "生效条件尚未满足"
	}
	return "已登记自有访问域名 " + base + "，但此刻未生效：" + why +
		"。对象 URL 仍走平台统一入口，这项不会改变出参（不要以为保存就切换了）。" +
		"需同时满足：本进程开启 TENANT_STORAGE_ROUTE、配置已启用、驱动非 local、凭据完整、桶公开读。" +
		ingress
}
