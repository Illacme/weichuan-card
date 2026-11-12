// 小程序主题两轴：把后端下发的 (styleId, brandColorId) 翻译成页面根节点上的两层 class。
//
// 真实色值不在这里——它们随小程序版本内置在 app.wxss 的 .wc-style-* / .wc-brand-* 里。
// 端上不能动态加载 WXSS（代码是编译上传审核的），后台也绝不下发自由 CSS：
// 这里只是一份**白名单 id → class 名**的映射。
//
// 两轴的含义（见 docs/theming-two-axis-design.md）：
//   - style（整体风格）决定各模块布局 + 该风格的默认色 + 版式 token → .wc-style-<id>
//   - brandColor（品牌配色）是叠加在风格之上的纯颜色层 → .wc-brand-<id>
// 根节点同时挂两层：布局来自 style 层，颜色来自 brand 层，换色系不会顺手改掉版式。
//
// P5 起两轴**全叉乘**：任意风格 × (nil + 8 款色系) = 4 × 9 = 36 个组合都合法，
// 色系不再隶属于某个风格。app.wxss 的颜色层写在布局层之后，同为单类选择器时
// 后定义者覆盖前者，所以「墨款布局 + 海蓝色」本来就能渲染；P4 的
// normalizeSelection 把它打回 default 是唯一的阻碍，P5 已放开。
//
// 缓存（wc_theme_id）因此不能再存扁平 templateId：12 个扁平 id 装不下 36 个组合
// （ink + brand-crimson 压扁后只剩 brand-crimson，旧端会看成「标准均衡 + 绛红」）。
// P5 起写带前缀的两轴串 `wc2:<styleId>|<brandColorId 或 ->`；不带前缀的旧值
// 仍按扁平 12 款解析，老用户升级零迁移。后端的 template_id 兼容列同理，只是
// **旧端近似值、不是可逆编码**，两轴列才是真值。
//
// id 集合的唯一真值在 backend/internal/mptemplate/mptemplate.go；
// miniprogram/tests/theme.spec.ts 会读 Go 源码逐项对账，任一端加款忘了同步就变红。
export const THEME_DEFAULT = 'default'
export const BRAND_PREFIX = 'brand-'

export const STYLE_IDS = ['default', 'ink', 'warm', 'teal'] as const
export const BRAND_IDS = [
  'brand-crimson', 'brand-coral', 'brand-amber', 'brand-forest',
  'brand-ocean', 'brand-indigo', 'brand-violet', 'brand-magenta',
] as const
/** 扁平 12 个 id 仍是后端白名单 / 兼容派生列的取值集合；36 个组合由两轴表达，不在此集合内。 */
export const THEME_IDS = [...STYLE_IDS, ...BRAND_IDS] as const

export type StyleId = (typeof STYLE_IDS)[number]
export type BrandColorId = (typeof BRAND_IDS)[number]
export type ThemeId = (typeof THEME_IDS)[number]

const STORAGE_KEY = 'wc_theme_id'
/** P5 两轴缓存前缀：与旧扁平 id 区分，旧值原样走 splitTheme。 */
const CACHE_PREFIX = 'wc2:'
const CACHE_NONE = '-'

/** /api/home-layout 信封里与主题相关的三个根级字段（旧后端只有 templateId）。 */
export interface ThemePayload {
  styleId?: string | null
  brandColorId?: string | null
  templateId?: string | null
}

export interface ThemeSelection {
  styleId: StyleId
  brandColorId: BrandColorId | null
}

/** 未知 id / 空值一律回缺省款：连旧后端（不下发主题字段）也能正常渲染。 */
export function normalizeTheme(id?: string | null): ThemeId {
  const v = String(id || '').trim()
  return (THEME_IDS as readonly string[]).includes(v) ? (v as ThemeId) : THEME_DEFAULT
}

export function normalizeStyle(id?: string | null): StyleId {
  const v = String(id || '').trim()
  return (STYLE_IDS as readonly string[]).includes(v) ? (v as StyleId) : THEME_DEFAULT
}

/** 品牌色系是「可选层」：nil / 未知值都表示「用风格默认色」。 */
export function normalizeBrand(id?: string | null): BrandColorId | null {
  const v = String(id || '').trim()
  return (BRAND_IDS as readonly string[]).includes(v) ? (v as BrandColorId) : null
}

/**
 * 两轴归一：风格与色系**互相独立**，各自归一后原样保留。
 * P4 曾在这里把带色系的组合强制打回 default（树形归属），P5 后端已放开全叉乘；
 * 若还留那个分支，访客会看到「后台保存成功、真机却是标准均衡布局」这种静默失败。
 */
function normalizeSelection(styleId?: string | null, brandColorId?: string | null): ThemeSelection {
  return {
    styleId: normalizeStyle(styleId),
    brandColorId: normalizeBrand(brandColorId),
  }
}

/**
 * 扁平 templateId → 两轴（旧缓存 / 旧后端 / 兼容派生列都走这里）。
 * 8 款 brand-* 反推成 (default, brand-*) 是**旧端近似语义**：扁平列根本没记风格，
 * 只能落到 default；这不表示 P5 的色系仍归属 default 风格。
 */
export function splitTheme(id?: string | null): ThemeSelection {
  const v = normalizeTheme(id)
  if ((BRAND_IDS as readonly string[]).includes(v)) {
    return { styleId: THEME_DEFAULT, brandColorId: v as BrandColorId }
  }
  return { styleId: v as StyleId, brandColorId: null }
}

/** 两轴 → P5 缓存串。绝不压扁成 12 个扁平 id（那会丢掉风格，36 → 12 塌缩）。 */
export function encodeCachedTheme(styleId?: string | null, brandColorId?: string | null): string {
  const sel = normalizeSelection(styleId, brandColorId)
  return `${CACHE_PREFIX}${sel.styleId}|${sel.brandColorId || CACHE_NONE}`
}

/**
 * 缓存串 → 两轴：新格式按前缀解，旧扁平值 / 空值 / 脏值都走 splitTheme 归一。
 * 未知风格、未知色系会各自回落，不会拼出白名单外的 class。
 */
export function decodeCachedTheme(raw?: string | null): ThemeSelection {
  const v = String(raw || '').trim()
  if (!v.startsWith(CACHE_PREFIX)) return splitTheme(v)
  const parts = v.slice(CACHE_PREFIX.length).split('|')
  const brand = parts[1] === CACHE_NONE ? null : parts[1]
  return normalizeSelection(parts[0], brand)
}

/**
 * 从 /api/home-layout 信封解析主题：优先两轴，旧后端只下发 templateId 时拆它。
 * 两个调用点（首页 loadAll、冷启动 prefetchTheme）必须都走这里，别各自猜字段，
 * 否则「后台显示与访客所见不一致」又是一次没人报错的静默失败。
 */
export function resolveTheme(payload?: ThemePayload | null): ThemeSelection {
  if (!payload) return splitTheme(null)
  if (payload.styleId != null || payload.brandColorId != null) {
    return normalizeSelection(payload.styleId, payload.brandColorId)
  }
  return splitTheme(payload.templateId)
}

/**
 * 生成根节点的两层 class。default 也挂 .wc-style-default（布局层每个风格都有
 * 自己的模块规则，不能靠「不挂」表示基线）；没有品牌色系时只挂 style 一层。
 */
export function themeClass(styleId?: string | null, brandColorId?: string | null): string {
  const sel = normalizeSelection(styleId, brandColorId)
  const classes = ['wc-style-' + sel.styleId]
  if (sel.brandColorId) classes.push('wc-brand-' + sel.brandColorId.slice(BRAND_PREFIX.length))
  return classes.join(' ')
}

/** 页面 onLoad 时从缓存恢复上次主题（首次冷启动 / 旧后端都是 default）。 */
export function currentThemeClass(): string {
  let raw = ''
  try {
    raw = String(wx.getStorageSync(STORAGE_KEY) || '')
  } catch {
    // 存储不可用（隐私模式 / 测试环境）时按缺省款渲染，不该让整页打不开
  }
  const sel = decodeCachedTheme(raw)
  return themeClass(sel.styleId, sel.brandColorId)
}

/**
 * 首页拿到 /api/home-layout 后调用：缓存本租户主题，并广播给已渲染的页面。
 *
 * 只靠 onLoad 读缓存不够——访客常从名片墙点进名片，名片墙那一页早已渲染完，
 * 若不广播就会停在默认蓝色，直到下次冷启动才统一。
 * 参数是两轴；调用方先用 resolveTheme(信封) 归一化，旧后端兼容在那里做。
 */
export function applyTheme(styleId?: string | null, brandColorId?: string | null): void {
  const sel = normalizeSelection(styleId, brandColorId)
  try {
    wx.setStorageSync(STORAGE_KEY, encodeCachedTheme(sel.styleId, sel.brandColorId))
  } catch {
    // 缓存写失败不影响本次渲染：下面仍会广播到当前页面栈
  }
  const cls = themeClass(sel.styleId, sel.brandColorId)
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
