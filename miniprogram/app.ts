import { bindCompanyFromScene, tenantConfig } from './utils/tenant'
import {
  DEFAULT_BASE_URL,
  baseUrlOf,
  cacheSettings,
  cachedSettings,
  fetchPublicSettings,
  hasBaseUrl,
  pinnedCompanyOf,
  type PublicSettings,
} from './utils/config'

// globalData 提到模块级：request.ts 在模块加载时就取过 getApp() 并持有同一对象引用，
// 这里直接改属性即可让后续所有请求用上新基址（不必等到下次冷启动）。
const globalData = {
  // 后端接口基址；由后台「系统设置 → 小程序 → miniprogram_base_url」下发覆盖。
  // 未拿到配置前用引导种子（见 utils/config.ts：源码默认空，发布前由部署方填 https 域名）。
  baseUrl: DEFAULT_BASE_URL,
  // 当前登录员工的 staffId：供「我的 → 我的名片」navigateBack 回名片页时回带。
  myStaffId: 0,
}

// 应用后台下发的公开配置：只覆盖「已配置」的项，留空的项保持当前值。
function applySettings(data: PublicSettings | null) {
  if (!data) return
  const url = baseUrlOf(data)
  if (url) globalData.baseUrl = url
  const pinned = pinnedCompanyOf(data)
  if (pinned > 0) tenantConfig.pinnedCompanyId = pinned
}

App({
  globalData,
  onLaunch(options: any) {
    // 冷启动引导：先用上次缓存的后台配置（同步），保证首屏请求就打到正确基址；
    // 再静默刷新一次，拿到最新值后覆盖并缓存——接口基址不再写死在代码里。
    applySettings(cachedSettings())
    // 基址为空 = 部署方还没填引导种子（源码默认状态）。
    // 此时不发请求：拼出 '/api/public/settings' 这种非法 URL 只会白等一个超时，
    // 而首屏那 5 秒白屏会让运维误判成「后端挂了」。preflight 第 3 节会拦住这种包。
    if (hasBaseUrl(globalData.baseUrl)) {
      fetchPublicSettings(globalData.baseUrl).then((data) => {
        if (!data) return
        cacheSettings(data)
        applySettings(data)
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
