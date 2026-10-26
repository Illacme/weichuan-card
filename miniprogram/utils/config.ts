// 小程序端「可由后台下发」的运行时配置。
//
// 背景：接口基址与「企业独立版锁定租户」原本分别写死在 app.ts 与 tenant.ts，
// 改一次就得重新打包提审。现在收敛为后台「系统设置 → 小程序」维护，
// 冷启动拉一次 /api/public/settings（公开、无需登录）覆盖：
//   - miniprogram_base_url  接口基址
//   - pinned_company_id     企业独立版锁定租户（>0 锁定，0 = 平台模式）
//
// 内置 DEFAULT_BASE_URL 只是「还没拿到后台配置时」的引导种子：
// 种子需能连通一次，之后即可完全由后台控制（目标域名须在微信后台配为合法域名）。
// 拿到过的配置会缓存在 Storage，下次冷启动首帧同步生效，不必等网络。

const CACHE_KEY = 'publicSettingsCache'

// 引导种子地址 —— **默认留空，源码里不放任何地址**。
//
// 为什么不是「填一个能用的地址」：种子基址是整条链路的**唯一入口**。
// 拉不到 /api/public/settings 就永远拿不到后台下发的正确基址，
// 后台再怎么配 miniprogram_base_url 也救不回来——只能重新打包提审。
// 此前这里是开发机的局域网 IP（`http://192.168.x.x:8080`），一旦随源码分发：
//   1. 别人部署后小程序拿**别人家的内网地址**当首连地址，请求必然超时；
//   2. 真机首连还是明文 http，而微信正式版要求 https。
// 所以源码默认空，发布前由部署方填成自己的 https 域名：
//   - 发布 / CI：`scripts/preflight.sh` 第 3 节对「空值」与「私有网段」直接判 FAIL；
//   - 本机联调：模拟器填 `http://127.0.0.1:8080`，真机填电脑局域网 IP，
//     并在 preflight 里用 `PREFLIGHT_ALLOW_PRIVATE=1` 跳过该项——**别把这段改动提交**。
// `tests/config.spec.ts` 有守门用例：出现内网 IP 或非 https 的明文地址即变红。
export const DEFAULT_BASE_URL = ''

// 是否已配置可用的基址（空串 = 未配置）。
// 调用方据此决定「要不要发请求」：对空基址调 wx.request 只会拼出 '/api/...'
// 这种非法 URL，白等一个 5s 超时——首屏白屏 5 秒、日志里还看不出所以然。
export function hasBaseUrl(url?: string | null): boolean {
  return String(url == null ? '' : url).trim() !== ''
}

// 后台「系统设置」公开下发的非敏感项（与后端 setting.publicKeys 一致）
export interface PublicSettings {
  brand_name?: string
  miniprogram_base_url?: string
  pinned_company_id?: string
}

// 取上次缓存的后台配置（同步）：冷启动首帧先用它，避免首屏请求打到旧地址。
export function cachedSettings(): PublicSettings | null {
  try {
    const raw = wx.getStorageSync(CACHE_KEY)
    if (!raw) return null
    return JSON.parse(raw) as PublicSettings
  } catch {
    return null
  }
}

// 缓存后台配置供下次冷启动同步使用（存储不可用时仅影响首帧，不阻塞启动）。
export function cacheSettings(data: PublicSettings): void {
  try {
    wx.setStorageSync(CACHE_KEY, JSON.stringify(data))
  } catch {
    // 忽略：隐私模式 / 空间不足等
  }
}

// 拉取后台公开配置。fromUrl 缺省用引导种子；失败静默返回 null，调用方保持当前值。
// 基址为空（源码默认状态）时**不发起请求**：非法 URL 只会白等一个超时。
export function fetchPublicSettings(fromUrl?: string): Promise<PublicSettings | null> {
  // 去掉末尾斜杠：调用方可能传后台下发的原值（baseUrlOf 已归一，但种子基址不一定），
  // 留着就会拼出 `https://host//api/public/settings`——多数服务端能吃下，
  // 但 nginx 的 location /api/ 前缀匹配会因此落到静态兜底，症状极难排查。
  const base = String(fromUrl || DEFAULT_BASE_URL || '')
    .trim()
    .replace(/\/+$/, '')
  if (!base) return Promise.resolve(null)
  return new Promise((resolve) => {
    wx.request({
      url: base + '/api/public/settings',
      method: 'GET',
      timeout: 5000,
      success: (res) => {
        const d = res.data as { code?: number; data?: PublicSettings } | null
        if (d && d.code === 0 && d.data) resolve(d.data)
        else resolve(null)
      },
      fail: () => resolve(null),
    })
  })
}

// 归一化的接口基址（去首尾空白与末尾斜杠）；未配置返回空串，由调用方保持原值。
export function baseUrlOf(data: PublicSettings | null): string {
  if (!data) return ''
  return String(data.miniprogram_base_url || '')
    .trim()
    .replace(/\/+$/, '')
  if (!base) return Promise.resolve(null)
}

// 独立版锁定租户；未配置 / 非法返回 0（平台模式）。
export function pinnedCompanyOf(data: PublicSettings | null): number {
  if (!data) return 0
  const n = parseInt(String(data.pinned_company_id ?? '0'), 10)
  return n > 0 ? n : 0
}
