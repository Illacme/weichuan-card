import { bindCompanyFromScene, tenantConfig } from './utils/tenant'
import {
  baseUrlOf,
  cacheSettings,
  cachedSettings,
  fetchPublicSettings,
  hasBaseUrl,
  pinnedCompanyOf,
  resolveBaseUrl,
  wxEnvVersion,
  type PublicSettings,
} from './utils/config'

// globalData 提到模块级：request.ts 在模块加载时就取过 getApp() 并持有同一对象引用，
// 这里直接改属性即可让后续所有请求用上新基址（不必等到下次冷启动）。
const globalData = {
  // 后端接口基址：冷启动按微信运行环境自动选（develop→测试地址，其余→生产种子），
  // 详见 utils/config.ts 的 resolveBaseUrl()。后台「系统设置 → 小程序 → miniprogram_base_url」
  // 的下发覆盖仅在非 develop 环境生效（避免把开发者工具的测试请求又指回生产）。
  baseUrl: resolveBaseUrl(),
  // 当前登录员工的 staffId：供「我的 → 我的名片」navigateBack 回名片页时回带。
  myStaffId: 0,
}

// 应用后台下发的公开配置：只覆盖「已配置」的项，留空的项保持当前值。
// allowBackendOverride=false 时（develop 环境）不应用后台下发的 miniprogram_base_url，
// 以免把开发者工具的测试请求又指回生产（后台下发的是生产地址）。
// 手动 Storage 覆盖仍可在 develop 下生效（它在 resolveBaseUrl 中优先级最高）。
function applySettings(data: PublicSettings | null, allowBackendOverride: boolean) {
  if (!data) return
  const url = baseUrlOf(data)
  if (url && allowBackendOverride) globalData.baseUrl = url
  const pinned = pinnedCompanyOf(data)
  if (pinned > 0) tenantConfig.pinnedCompanyId = pinned
}

App({
  globalData,
  onLaunch(options: any) {
    // develop 环境（微信开发者工具）下，缓存 / 后台下发的生产基址不应把测试请求指回生产。
    const devMode = wxEnvVersion() === 'develop'
    // 冷启动引导：先用上次缓存的后台配置（同步），保证首屏请求就打到正确基址；
    // 再静默刷新一次，拿到最新值后覆盖并缓存——接口基址不再写死在代码里。
    // develop 环境下缓存里若带生产 miniprogram_base_url，不应用（保持测试地址）。
    applySettings(cachedSettings(), !devMode)
    // 基址为空 = 部署方还没填引导种子（源码默认状态）。
    // 此时不发请求：拼出 '/api/public/settings' 这种非法 URL 只会白等一个超时，
    // 而首屏那 5 秒白屏会让运维误判成「后端挂了」。preflight 第 3 节会拦住这种包。
    if (hasBaseUrl(globalData.baseUrl)) {
      fetchPublicSettings(globalData.baseUrl).then((data) => {
        if (!data) return
        cacheSettings(data)
        // develop 下不应用后台下发的基址覆盖（保持测试地址）；其余环境照旧。
        applySettings(data, !devMode)
      })
    }

    // 冷启动即尝试绑定租户（平台模式）：访客扫企业小程序码进来时，
    // scene / query 携带 companyId，写入 Storage 粘滞（见 utils/tenant.ts）。
    // 企业独立版（后台下发 pinnedCompanyId > 0）在此函数内被忽略，全程锁定。
    try {
      bindCompanyFromScene(
        options && (options.scene || (options.query && options.query.scene)),
        options && options.query && options.query.companyId
      )
    } catch {}
  },
})
