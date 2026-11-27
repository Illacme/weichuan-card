// 小程序展示配置「版本号自动刷新」（0067）。
//
// 背景：后台「小程序设置」保存后，访客端可能仍看到旧配置——
//   · 生产环境 /api/home-layout 挂了 HTTP 缓存（默认 60 秒，可在后台调整）；
//   · 微信开发者工具里已打开的页面不会自己重新 onLoad 拉配置。
// 当初想过「后台加清缓存按钮」，但后台无法清用户设备 / 已打开页面的缓存。
// 真正的解是版本号：后台每次保存递增 config_version（0067 迁移），端上用一个
// 独立 no-store 探针 GET /api/home-layout/version 读到真实版本，发现变化就
// 重新拉一次 /api/home-layout。
//
// 为什么版本号不塞进 /api/home-layout 响应体：那条响应被 HTTP 缓存（默认 60 秒），
// 版本跟着被缓存就永远晚一拍，等于没检测。探针单独开且显式 no-store。
//
// 设计原则：
//   1. 纯粹被动检测：onShow 时比对一次，不轮询、不弹窗、不打断访客；
//   2. 失败静默：探针失败（旧后端 / 断网）就当版本没变，绝不阻塞名片页渲染；
//   3. 版本存在本地 Storage（按租户隔离），只记「上次见到的版本号」，
//      不做配置内容缓存——配置始终以服务端 /api/home-layout 为准；
//   4. 首次见到某租户的版本号只记录、不刷新（首屏本来就是最新拉取）。

import { request } from './request'
import { getCompanyId } from './tenant'

const VERSION_KEY = 'homeLayoutConfigVersion'

// 每次 onShow 调用：返回本次是否检测到「版本变化」。
// 变化时调用方应重新拉一次配置（loadAll({ silent: true })）。
//
// 用 async 但不 await 会阻塞渲染吗？不会——本函数只发一个极小的 GET，
// 失败 / 慢网络都不影响页面主流程。调用方 fire-and-forget 即可。
export async function configVersionChanged(): Promise<boolean> {
  try {
    const res = await request<{ code: number; configVersion?: number }>(
      'GET',
      '/api/home-layout/version'
    )
    const raw = res && res.configVersion
    // 旧后端 / 脏数据不下发版本：静默当没变，保持现状（与加这个机制前一样）。
    if (typeof raw !== 'number' || raw < 1) return false

    const cid = getCompanyId()
    const storeKey = VERSION_KEY + ':' + cid
    let last = 0
    try {
      const v = wx.getStorageSync(storeKey)
      if (typeof v === 'number' && v > 0) last = v
    } catch {
      // 存储不可用：退化为「每次 onShow 都当变化」也不安全（会重复拉配置），
      // 故这里只记 0，下面首次记录分支会把它设成当前值，之后正常比对。
      last = 0
    }

    if (last === raw) return false
    // 首次记录（last===0）：只记下，不触发刷新——首屏本来就是最新拉的，
    // 专门为升级后的第一次 onShow 刷一次只会浪费一个请求。
    try {
      wx.setStorageSync(storeKey, raw)
    } catch {
      // 写存储失败不影响本次判断（本进程内已比对出变化，仍会让调用方刷新一次）
    }
    return last !== 0
  } catch {
    // 探针失败：静默。绝不因为一个「辅助刷新」的探测让名片页报错或空白。
    return false
  }
}
