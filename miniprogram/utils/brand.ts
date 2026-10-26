// 小程序「品牌名」解析与导航栏标题设置。
//
// 背景：`app.json` 的全局 `navigationBarTitleText` 曾写死成演示租户名（首个客户的名字）。
// 商业交付给任何别的客户时，他那套小程序只要进了**没有单独设标题**的页面
// （海报 / 编辑名片 / 登录 / 案例详情 / 服务详情）就会看到别人家的公司名——
// 这类「看起来像没改干净」的问题最伤信任，而且提审截图里一眼就能看见。
//
// 修法分两层：
//   1. 全局兜底必须是**产品名**（app.json），不是任何客户名；
//   2. 真正需要体现客户的页面（海报等），在运行时按当前企业覆盖。
//
// 为什么不给每个页面都在 app.json 里写死企业名：小程序是「一套代码多租户」，
// 编译期写死的字符串对第二个客户就是错的，只能在运行时解析。

import { cachedSettings } from './config'

// 产品名兜底。**必须与 app.json 的 navigationBarTitleText 一致**
// （tests/brand.spec.ts 有守卫用例比对这两处）。
export const FALLBACK_BRAND = '微传名片'

// 可用的企业信息子集（/api/card 的 company 与后台企业列表同形）
export interface BrandCompany {
  shortName?: string
  fullName?: string
}

// 解析当前应显示的品牌名。优先级：
//   1. **当前企业**——名片/海报是员工替自己企业做的营销物料，标题体现企业才对；
//   2. 后台下发的 `brand_name`——部署方品牌，用于企业信息还没加载出来的间隙；
//   3. 产品名兜底——刚装好 / 断网 / 配置全空的极端情况。
//
// 注意顺序不能反：平台模式下 `brand_name` 是**平台**品牌（如「微传名片」），
// 员工分享出去的海报若顶着平台名而不是他公司的名字，等于把客户企业的物料做没了。
export function resolveBrandTitle(company?: BrandCompany | null): string {
  const c = company || {}
  const short = String(c.shortName == null ? '' : c.shortName).trim()
  if (short) return short
  const full = String(c.fullName == null ? '' : c.fullName).trim()
  if (full) return full
  const s = cachedSettings()
  const brand = String((s && s.brand_name) || '').trim()
  if (brand) return brand
  return FALLBACK_BRAND
}

// 设置当前页面导航栏标题。
// 标题只是装饰：`setNavigationBarTitle` 在页面尚未创建 / 部分调试环境会抛错，
// 绝不能因为「标题没设上」把页面主流程带崩，故整段吞掉。
export function applyBrandTitle(company?: BrandCompany | null): void {
  try {
    wx.setNavigationBarTitle({ title: resolveBrandTitle(company) })
  } catch {
    // 忽略：标题属装饰，失败不影响功能
  }
}
