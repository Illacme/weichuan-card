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

// 小程序配置文件里的名称（app.json → window.navigationBarTitleText）。
//
// 企业单独发布一套小程序时，会把 app.json 的标题改成自己的名字；启动首帧必须显示
// **这个名字**，而不是后台下发的 brand_name（平台品牌）或编译期写死的产品名。
//
// 小程序运行时没有读取 app.json 的 API，只能在这里镜像一份。
// 本行由 scripts/sync_mp_app_name.py 从 app.json 生成——改标题只改 app.json，
// 然后跑同步脚本（pre-push 与 CI 都有守门）。不要手改这一行。
export const CONFIGURED_APP_NAME = '微传名片'

// 产品名兜底。与配置文件名称同源：企业改了 app.json 标题时，海报/分享的兜底
// 也跟着变成它的名字，不必再维护第二个字面量。
export const FALLBACK_BRAND = CONFIGURED_APP_NAME

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

// 只取后台已配置的品牌名，不做产品名兜底。
//
// 与 resolveBrandTitle 的区别很重要：后者在「什么都没配」时会回产品名，
// 而启动首帧如果也这么干，未配置品牌的企业会先闪一下平台名。
// 首屏要的是「配了就显示、没配就留白」，所以必须读原始 brand_name。
export function cachedBrandName(): string {
  try {
    const s = cachedSettings()
    return String((s && s.brand_name) || '').trim()
  } catch {
    return ''
  }
}

// 启动首帧标题：**配置文件名称最高优先级**。
//
// 为什么不能直接用 cachedBrandName()：那是后台「系统设置 → 品牌名称」，
// 平台模式下它是**平台**品牌。企业单独发布的小程序把 app.json 标题改成了自己的
// 公司名，首帧却被后台下发的平台名盖掉，看起来就像没改干净（用户 2026-11-25 反馈）。
//
// 与 resolveBrandTitle 的分工：这里管「还没拿到任何企业数据时显示什么」，
// 只认配置文件；后者管「数据加载后海报/导航栏显示什么」，企业名优先。
// 两个语义不要合并——合并后要么平台名顶掉企业名，要么企业名提前闪出来。
export function startupBrandName(): string {
  const configured = String(CONFIGURED_APP_NAME || '').trim()
  // 配置名为空只可能是改错了（守门会拦），此时才退回后台值；
  // 即便如此也不回落产品名，避免首屏冒出一个谁都没配过的名字。
  return configured || cachedBrandName()
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
