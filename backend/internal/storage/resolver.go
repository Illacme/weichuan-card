package storage

import "errors"

// ErrNoUploader 表示根本没有可用的存储实例。
//
// 单独定义而不返回 nil、nil：调用方拿到 nil 实例去调 Save 会 panic，
// 而 panic 在 gin 的 recovery 里会变成 500 纯文本，前端拿到的是
// "Internal Server Error" 而不是本项目统一的 {code:1,msg:...} 信封，
// 用户看到的就是「上传按钮点了没反应」。
var ErrNoUploader = errors.New("存储未初始化（没有可用的上传实例）")

// Resolver 按租户给出该用哪个存储实例。
//
// 为什么要有这一层：平台自身的存储由「主存储」那一行档案构造，全平台共用一份。
// 客户说「文件存进我自己公司的桶」时，单份档案装不下第二个租户——
// Resolver 就是「档案绑定 → 实例」这一跳。
//
// **生产实现只有一个**：`internal/storageprofile.Resolver`（第七十五轮起直接实现
// 本接口）。历史上还有过 storage.StaticResolver / uploadroute.Resolver /
// DynamicResolver 三个包装层，随 TENANT_STORAGE_ROUTE 开关一并删除；
// 测试用的桩实现挪到了 `internal/storage/testsupport`，不再留在生产包里。
//
// 接口只留三个方法，是刻意的：
//   - 设计稿 §3.3 还列了 PresignerFor / SignerFor / RefresherFor，三者都会退化成
//     「先 For 再类型断言」，而且要「静态 / 租户」两套实现各写一遍。同一个口子写三遍，
//     漏一处的症状是「A 租户能直传、B 租户不能」，查起来比现在多绕两圈。
//   - 能力判定沿用 handler 里已有的 up.(storage.Presigner) 写法，改动面最小；
//     本轮要验证的是「注入形状变了、行为没变」，多引入三个方法只会扩大验证面。
//
// 两条纪律（任何一条破了都会出事）：
//
//  1. **租户实例构造失败必须报错，绝不回退平台实例。** 客户以为文件在自己桶里、
//     实际躺在平台盘上，是「没人报错时用户无从察觉」的典型静默失败——比上传直接失败
//     严重得多：失败用户会重传、会找客服；写错位置可能半年都没人发现，等发现时
//     已经有一批数据落在不该落的地方，回填要按对象逐个搬。
//     「这家没配 / 没启用 / 驱动是 local」不属于失败，那是**明确配置成走平台**。
//  2. For 只在同时返回 error 时才返回 nil 实例；调用方必须先判 error 再判 nil。
type Resolver interface {
	// For 返回该租户应使用的存储实例。cid <= 0 表示平台自身（scope=p/ 的对象）。
	For(cid int64) (Uploader, error)
	// Platform 返回平台自身那一份实例，**不受租户路由影响**。
	//
	// 它存在是因为有一类对象键前缀就写死了平台：p/library/...（素材采用）、
	// p/wxacode/...（小程序码）。这些对象由平台 nginx 的 /uploads/ 回退段托管，
	// 若把它们写进客户桶，URL 里却是平台域名，取图必然 404——
	// 键前缀与实例必须同源。这条约束由 handler 侧的源码级守门测试盯着
	// （TestPlatformScopedWritesUsePlatformInstance）。
	Platform() Uploader
	// DriverFor 返回该租户实际生效的驱动名（local/minio/oss/s3），供后台能力探测展示。
	// 取不到配置时返回平台驱动：它只用于界面文案，不影响任何写入路径。
	DriverFor(cid int64) string
}
