// 小程序整体风格模板：把后端下发的 templateId 翻译成挂在页面顶层节点上的 class。
//
// 真实色值不在这里——它们随小程序版本内置在 app.wxss 的 .wc-theme-* 里。
// 端上不能动态加载 WXSS（代码是编译上传审核的），后台也绝不下发自由 CSS：
// 这里只是一份**白名单 id → class 名**的映射。
//
// id 集合的唯一真值在 backend/internal/mptemplate/mptemplate.go；
// miniprogram/tests/theme.spec.ts 会读 Go 源码逐项对账，任一端加款忘了同步就变红。
export const THEME_DEFAULT = 'default'
export const THEME_IDS = ['default', 'ink', 'warm', 'teal'] as const
export type ThemeId = (typeof THEME_IDS)[number]

const STORAGE_KEY = 'wc_theme_id'

/** 未知 id / 空值一律回缺省款：连旧后端（不下发 templateId）也能正常渲染。 */
export function normalizeTheme(id?: string | null): ThemeId {
  const v = String(id || '').trim()
  return (THEME_IDS as readonly string[]).includes(v) ? (v as ThemeId) : THEME_DEFAULT
}

/** default 不加 class：变量已在 page 上定义，避免多一层无意义的选择器。 */
export function themeClass(id?: string | null): string {
  const v = normalizeTheme(id)
  return v === THEME_DEFAULT ? '' : 'wc-theme-' + v
}

/** 页面 onLoad 时从缓存恢复上次配色（首次冷启动 / 旧后端都是 default）。 */
export function currentThemeClass(): string {
  let id = ''
  try {
    id = String(wx.getStorageSync(STORAGE_KEY) || '')
  } catch {
    // 存储不可用（隐私模式 / 测试环境）时按缺省款渲染，不该让整页打不开
  }
  return themeClass(id)
}

/**
 * 首页拿到 /api/home-layout 后调用：缓存本租户模板，并广播给已渲染的页面。
 *
 * 只靠 onLoad 读缓存不够——访客常从名片墙点进名片，名片墙那一页早已渲染完，
 * 若不广播就会停在默认蓝色，直到下次冷启动才统一。
 */
export function applyTheme(id?: string | null): void {
  const safe = normalizeTheme(id)
  try {
    wx.setStorageSync(STORAGE_KEY, safe)
  } catch {
    // 缓存写失败不影响本次渲染：下面仍会广播到当前页面栈
  }
  const cls = themeClass(safe)
  try {
    const pages = getCurrentPages()
    for (const p of pages) (p as any).setData({ themeClass: cls })
  } catch {
    // getCurrentPages 在 app 启动早期 / 单测环境可能不可用，忽略即可
  }
}

/** 每个访客页 onLoad 第一行调用：把缓存里的主题 class 同步到本页面 data。 */
export function bindTheme(page: { setData: (d: Record<string, unknown>) => void }): void {
  page.setData({ themeClass: currentThemeClass() })
}
