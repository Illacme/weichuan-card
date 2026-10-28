// Package cattemplate 业绩分类模板：新建企业时自动复制一份的「分类骨架」。
//
// 为什么需要它：零数据版装完之后，平台超管建第二家企业时那家企业**一个分类都没有**，
// 企业管理员登录后第一页（经典业绩）是空的，得先自己建分类才能录案例——小程序那边
// 业绩页连 Tab 都渲染不出来。所以开通企业时必须给一份可用的骨架。
//
// 为什么单独成包而不是写死在 handler：演示数据包（internal/demoseed）用的是同一批
// 分类名，两处各写一份迟早漂移——某天演示数据叫「医院、学校」、新企业模板叫「医疗」，
// 而客户看到的两套东西本该是同一套。模板是叶子包（零依赖），两个使用方都引它。
//
// 模板只提供「名称 + 排序」，刻意不含图标与锁定标记，理由见 handler/companies.go
// 里 seedCategoryTemplate 的注释。
package cattemplate

// Category 模板里的一条分类：只有名称与排序。
type Category struct {
	Name string
	Sort int
}

// defaults 经典业绩八分类（客户已确认，与设计稿一致）。
//
// 与旧 migrations/0001_init.sql 里的七条种子有一处差异：那条把「生产制造、仓储物流」
// 合成一类，后来设计稿确认为两个分类，此处以设计稿（八类）为准。
var defaults = []Category{
	{Name: "居住住宅", Sort: 1},
	{Name: "政务办公", Sort: 2},
	{Name: "医院、学校", Sort: 3},
	{Name: "商业、酒店", Sort: 4},
	{Name: "公共交通", Sort: 5},
	{Name: "生产制造", Sort: 6},
	{Name: "仓储物流", Sort: 7},
	{Name: "私人住宅", Sort: 8},
}

// Default 返回模板分类的副本。
//
// 返回副本而不是直接暴露 defaults：调用方会把它塞进 model 结构体、也可能就地改排序，
// 共享切片一旦被改，下一次开通企业拿到的就是上一次改过的版本——这种 bug 只在
// 「连续开通两家企业」时才现形。
func Default() []Category {
	out := make([]Category, len(defaults))
	copy(out, defaults)
	return out
}
