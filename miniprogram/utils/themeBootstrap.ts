// 冷启动预热「整体风格」：把本租户选中的模板写进缓存，并广播给页面栈。
//
// 为什么需要它：首页 loadAll 拿到 /api/home-layout 后会调 applyTheme，但访客常从
// 分享 / 小程序码**直接进二级页**（名片墙、案例详情、服务详情），首页根本没被打开过。
// 那时 onLoad 里的 bindTheme 只能读到空缓存，整页停在默认蓝，直到下次冷启动且打开首页
// 才统一——后台明明已经换了风格，客户却看不到，属于典型的静默失败。
//
// 为什么不复用 utils/request.ts：那个模块在**模块加载期**就执行 `getApp()` 并持有
// 引用。app.ts 静态 import 的模块会在 App({...}) 求值前被加载，此时 getApp() 还是
// undefined，请求层会永久丢掉后台下发的 baseUrl 覆盖（utils/config.ts 的引导机制失效）。
// 所以这里用裸 wx.request，由调用方把已解析好的 baseUrl / companyId 传进来，
// 既不触发那个时序坑，也不需要登录态（/api/home-layout 是公开接口）。
import { applyTheme } from './theme'

export function prefetchTheme(baseUrl: string, companyId: number): void {
  const base = String(baseUrl || '').trim().replace(/\/+$/, '')
  if (!base) return
  const query = companyId > 0 ? '?companyId=' + encodeURIComponent(String(companyId)) : ''
  wx.request({
    url: base + '/api/home-layout' + query,
    method: 'GET',
    timeout: 5000,
    success: (res) => {
      const d = res.data as { code?: number; templateId?: string } | null
      // 只认 code:0；失败 / 旧后端 / 未授权回落 default 都交给 normalizeTheme 处理。
      // 纯展示配置，任何一种异常都不该弹错或拖住启动。
      if (d && d.code === 0) applyTheme(d.templateId)
    },
    fail: () => {
      /* 静默：保留上次缓存，没有缓存就按缺省款渲染 */
    },
  })
}
