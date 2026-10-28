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

// 引导种子地址 —— 本仓库是**已上线部署仓**，直接内置生产域名
// `https://weichuan.ziyuanxingqiu.cn`：首次冷启动即可连后端拉取后台下发的
// `miniprogram_base_url`，之后由后台值接管（目标域名须在微信后台配为合法域名）。
//
// 历史背景（为何早期「默认留空、源码不放任何地址」）：种子基址是整条链路的**唯一入口**。
// 拉不到 /api/public/settings 就永远拿不到后台下发的正确基址，
// 后台再怎么配 miniprogram_base_url 也救不回来——只能重新打包提审。
// 此前这里是开发机的局域网 IP（`http://192.168.x.x:8080`），一旦随源码分发：
//   1. 别人部署后小程序拿**别人家的内网地址**当首连地址，请求必然超时；
//   2. 真机首连还是明文 http，而微信正式版要求 https。
// 本部署仓已确认唯一归属 weichuan.ziyuanxingqiu.cn，故直接写死生产域名；
// 若改域名，只需改这一处（或后台「系统设置 → 小程序」下发覆盖），并重新打包小程序。
//   - 发布 / CI：`scripts/preflight.sh` 第 3 节对「私有网段 / 明文 http」直接判 FAIL；
//   - 本机联调：可临时把这里改回 `http://127.0.0.1:8080`，并用
//     `PREFLIGHT_ALLOW_PRIVATE=1` 跳过该项——**别把这段改动提交**。
// `tests/config.spec.ts` 有守门用例：出现内网 IP 或非 https 的明文地址即变红。
export const DEFAULT_BASE_URL = 'https://weichuan.ziyuanxingqiu.cn'

// ─────────────────────────────────────────────────────────────
// 运行环境识别：开发者工具 / 体验版 / 正式版 自动选不同后端地址
// ─────────────────────────────────────────────────────────────
// 微信官方机制：`wx.getAccountInfoSync().miniProgram.envVersion` 在
//   - 开发者工具（模拟器，或连真机调试）返回 `'develop'`
//   - 体验版（上传后扫码 / 分享的版本）返回 `'trial'`
//   - 正式版（已发布）返回 `'release'`
// 据此让「开发者工具联调」自动走测试后端，体验版 / 正式版走生产，无需手动改代码或重新打包。
//
// 优先级（从高到低）：
//   1. 手动覆盖 Storage['mpBaseUrlOverride'] —— 临时顶替自动选择（如想让 develop 也连生产验证）
//   2. envVersion==='develop' → DEV_BASE_URL（测试后端）
//   3. 其余（trial / release）→ DEFAULT_BASE_URL（生产种子，并允许后台 miniprogram_base_url 下发覆盖）
//
// 注意：DEV_BASE_URL 仅在 develop 下使用，永不进正式版请求链；发布前即使它还是
// `http://127.0.0.1:8080` 也安全（preflight 第 3 节只扫 DEFAULT_BASE_URL，不会误拦）。
export type WxEnvVersion = 'develop' | 'trial' | 'release'

// 当前微信运行环境；非微信环境（单测桩未注入）回落 `'release'`（按正式处理，避免误走测试地址）。
export function wxEnvVersion(): WxEnvVersion {
  try {
    const info = wx.getAccountInfoSync()
    const v = info && info.miniProgram && info.miniProgram.envVersion
    if (v === 'develop' || v === 'trial' || v === 'release') return v
  } catch {
    // 未注入微信运行时（如单测桩未提供 getAccountInfoSync）：按正式处理
  }
  return 'release'
}

// 开发者工具联调用的测试后端基址（用户确认走本机后端）。
// 选本机地址时需在微信开发者工具勾选「不校验合法域名」
// （设置 → 项目设置 → 本地设置），否则 `http://127.0.0.1:8080` 会被当作不合法域名拦截。
// 若测试后端在别的机器 / 域名，改这一处即可；或运行时用 Storage 手动覆盖（见 mpBaseUrlOverride）。
export const DEV_BASE_URL = 'http://127.0.0.1:8080'

// 手动覆盖键：开发者工具里可临时写入一个基址顶替上面的自动选择（如让 develop 连生产做验证）。
// 写空串（或删除该键）即取消覆盖，恢复按环境自动选择。
const OVERRIDE_KEY = 'mpBaseUrlOverride'

// 读取手动覆盖基址（已 trim）；无 / 空返回 ''。
export function baseUrlOverride(): string {
  try {
    const v = wx.getStorageSync(OVERRIDE_KEY)
    return v ? String(v).trim() : ''
  } catch {
    return ''
  }
}

// 解析冷启动使用的基址：手动覆盖 > develop 走测试地址 > 否则生产种子。
export function resolveBaseUrl(): string {
  const ov = baseUrlOverride()
  if (ov) return ov
  if (wxEnvVersion() === 'develop') return DEV_BASE_URL
  return DEFAULT_BASE_URL
}

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
}

// 独立版锁定租户；未配置 / 非法返回 0（平台模式）。
export function pinnedCompanyOf(data: PublicSettings | null): number {
  if (!data) return 0
  const n = parseInt(String(data.pinned_company_id ?? '0'), 10)
  return n > 0 ? n : 0
}
