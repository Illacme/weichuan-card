package model

// All 返回需要建表的全部业务模型，是建表清单的**唯一权威来源**。
//
// 历史上这份清单被复制了三份（cmd/devserver 的 devModels、handler_test 的两处
// AutoMigrate），新增模型时漏改任何一份都不报编译错，只在运行期报
// `no such table`——曾因漏加 RevokedSession，被中间件「查询出错按已吊销处理」
// 放大成「登录成功后所有请求 401」。现在三处都改为调用本函数。
//
// 不含 `setting.Setting`：sys_setting 由 `setting.Init` 自行 AutoMigrate
// （两个入口都在建表后调用它），且 setting 包依赖 model，反向引用会成环。
func All() []interface{} {
	return []interface{}{
		&Employee{}, &CompanyInfo{}, &CompanyProfile{},
		&AchievementCategory{}, &ProjectCase{}, &BizService{},
		&HomeLayout{}, &StatEvent{}, &Admin{},
		&OpenApply{}, &CardModifyApply{},
		&RevokedSession{}, &Company{},
		&AuditLog{},      // 操作审计日志（后台写操作与登录失败留痕）
		&RateLimitHit{},  // 限流计数（多副本共享，见该类型注释）
		&PosterPref{},    // 海报个性化设置（自定义背景 + 版式，按员工一行）
		&CompanyWXApp{},  // 租户小程序凭据（按企业隔离，一个企业一个小程序）
		&MPRelease{},     // 小程序版本发布记录（平台代租户上传/提审/发布）
		&MediaCheck{},    // 图片/音频内容安全检测记录（异步送检 → 消息推送回调 → 后台下架）
		&TenantLicense{}, // 按租户（企业）的授权记录：席位 / 到期 / 功能开关（Phase 4）
		&StorageUsage{},  // 按租户累计的存储用量（上传字节数 / 文件数，配额检查用）
	}
}
