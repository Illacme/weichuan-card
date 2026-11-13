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
// 为什么要有这一层：直到第六十轮，进程里只有**一个** storage.Uploader，
// 由 sys_setting 的全局单值（storage_driver / bucket / 凭据）构造，所有租户共用。
// 客户说「文件存进我自己公司的桶」时，全局单值装不下第二个租户——
// tenant_storage_configs（迁移 0044）装得下配置，但**表本身不会让上传改道**。
// Resolver 就是「配置行 → 实例」这一跳，也是那张表第一次真正承载流量的入口。
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

// StaticResolver 是「所有租户都走平台那一份实例」的实现。
//
// 它不是临时脚手架，而是 TENANT_STORAGE_ROUTE=off 时的**正式实现**：
// 默认配置下它就是生产行为，所以这里必须与第六十轮之前逐字节等价——
// 这正是本系列能先「只改形状」的前提，也是把开关默认关掉的意义：
// 出事时把它关回去，进程内只剩这一条路径，与升级前完全一致。
type StaticResolver struct {
	up     Uploader
	driver string
}

// NewStaticResolver 包装一个平台实例。driver 传空串时的口径与
// capabilityHandler 原实现一致（空 = local），不要把空串当「未知驱动」另立分支。
func NewStaticResolver(up Uploader, driver string) *StaticResolver {
	return &StaticResolver{up: up, driver: driver}
}

// For 忽略租户，永远返回平台实例。返回 error 的唯一情形是平台实例本身没建起来，
// 此时**不能**返回 (nil, nil)：调用方会拿 nil 去 Save。
func (s *StaticResolver) For(int64) (Uploader, error) {
	if s.up == nil {
		return nil, ErrNoUploader
	}
	return s.up, nil
}

func (s *StaticResolver) Platform() Uploader { return s.up }

func (s *StaticResolver) DriverFor(int64) string {
	if s.driver == "" {
		return "local"
	}
	return s.driver
}

var _ Resolver = (*StaticResolver)(nil)
