import { describe, it, expect, vi, afterEach } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, resolve, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  THEME_IDS,
  THEME_DEFAULT,
  normalizeTheme,
  themeClass,
  currentThemeClass,
  applyTheme,
  bindTheme,
} from '../utils/theme'

// 小程序整体风格模板的跨端契约守门。
//
// 为什么需要它：模板 id 在三个地方出现（Go 白名单 / 后台清单 / 小程序 class），
// 色值在 app.wxss 的三套变量块里，页面根节点要挂 class 才生效。
// 任意一处漏改都不会报错——访客只是悄悄看到默认蓝色，后台却显示「已选高级款」，
// 属于典型的静默失败，只能靠读源码的断言钉住。
const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const MP = resolve(REPO, 'miniprogram')
const GO_TPL = resolve(REPO, 'backend/internal/mptemplate/mptemplate.go')
const APP_WXSS = resolve(MP, 'app.wxss')

// 两个系列的约束是**相反**的：style 款必须有真实版式差异，brand 款必须完全没有
// 版式差异（只换配色）。所以守门不能一刀切地「对所有非 default 款都要求差异」。
// 后台按服务端下发的 series 分组，端上守门按 brand- 前缀分组，两份声明由下面的
// 用例读 Go 源码对账，任一侧漂移都会变红。
const BRAND_PREFIX = 'brand-'
const BRAND_IDS = THEME_IDS.filter((id) => id.startsWith(BRAND_PREFIX))
const STYLE_ADVANCED_IDS = THEME_IDS.filter(
  (id) => id !== THEME_DEFAULT && !id.startsWith(BRAND_PREFIX)
)

/** 读 Go 白名单里的 ID → Series 配对；后台分组的真值在这里，不在前端猜。 */
function goSeriesById(): Record<string, string> {
  const src = readFileSync(GO_TPL, 'utf-8')
  const def = /DefaultID\s*=\s*"([a-z0-9_-]+)"/.exec(src)?.[1]
  if (!def) throw new Error('mptemplate.go 找不到 DefaultID')
  const out: Record<string, string> = {}
  const re = /ID:\s*(DefaultID|"([a-z0-9_-]+)")[\s\S]*?Series:\s*(SeriesStyle|SeriesBrand),/g
  for (const m of src.matchAll(re)) {
    out[m[2] || def] = m[3] === 'SeriesBrand' ? 'brand' : 'style'
  }
  return out
}

function varsOf(block: string): string[] {
  return Array.from(block.matchAll(/(--wc-[\w-]+)\s*:/g)).map((m) => m[1]).sort()
}

function collectWxss(dir: string, acc: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      if (name === 'node_modules') continue
      collectWxss(p, acc)
    } else if (name.endsWith('.wxss')) {
      acc.push(p)
    }
  }
  return acc
}

const VISITOR_PAGES: { wxml: string; ts: string; roots: number }[] = [
  { wxml: 'pages/index/index.wxml', ts: 'pages/index/index.ts', roots: 3 },
  { wxml: 'pages/team/team.wxml', ts: 'pages/team/team.ts', roots: 6 },
  { wxml: 'pages/team/all.wxml', ts: 'pages/team/all.ts', roots: 6 },
  { wxml: 'pages/project/list/list.wxml', ts: 'pages/project/list/list.ts', roots: 4 },
  { wxml: 'pages/project/detail/detail.wxml', ts: 'pages/project/detail/detail.ts', roots: 3 },
  { wxml: 'pages/service/detail/detail.wxml', ts: 'pages/service/detail/detail.ts', roots: 3 },
]

describe('小程序整体风格模板 utils/theme', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('未知 / 空 / 脏 id 一律回缺省款（连旧后端也能渲染）', () => {
    expect(normalizeTheme('')).toBe(THEME_DEFAULT)
    expect(normalizeTheme(null)).toBe(THEME_DEFAULT)
    expect(normalizeTheme(undefined)).toBe(THEME_DEFAULT)
    expect(normalizeTheme('neon')).toBe(THEME_DEFAULT)
    expect(normalizeTheme('INK')).toBe(THEME_DEFAULT)
    expect(normalizeTheme(' ink ')).toBe('ink')
    expect(themeClass(THEME_DEFAULT)).toBe('')
    expect(themeClass('warm')).toBe('wc-theme-warm')
    expect(themeClass('neon')).toBe('')
  })

  it('模板 id 与 Go 白名单一一对应（一端加款忘了同步即变红）', () => {
    const go = readFileSync(GO_TPL, 'utf-8')
    const ids = new Set<string>()
    const def = go.match(/DefaultID\s*=\s*"([a-z0-9_-]+)"/)
    if (def) ids.add(def[1])
    for (const m of go.matchAll(/ID:\s*"([a-z0-9_-]+)"/g)) ids.add(m[1])
    expect(Array.from(ids).sort()).toEqual([...THEME_IDS].sort())
  })

  it('app.wxss 每套主题都定义齐全同一组 CSS 变量', () => {
    const src = readFileSync(APP_WXSS, 'utf-8')
    const defaultBlock = src.match(/page\s*\{([\s\S]*?)\n\}/)
    expect(defaultBlock).not.toBeNull()
    const base = varsOf(defaultBlock![1])
    expect(base.length).toBeGreaterThanOrEqual(10)

    const blocks = Array.from(src.matchAll(/\.wc-theme-([a-z0-9_-]+)\s*\{([\s\S]*?)\n\}/g))
    const themed = blocks.map((m) => m[1]).sort()
    expect(themed).toEqual([...THEME_IDS].filter((id) => id !== THEME_DEFAULT).sort())
    for (const m of blocks) {
      expect(varsOf(m[2])).toEqual(base)
    }
  })

  it('全仓 WXSS 不再残留主色硬编码（默认色只能出现在变量定义块里）', () => {
    const legacy = [
      '#1a8cff', '#1b66df', '#0e6fe0', '#2f6bff', '#f5faff', '#b3d9ff',
      '#e8f1fb', '#e8f3ff', '#eaf4ff', '#e5ebf2', 'rgba(26, 140, 255',
    ]
    for (const p of collectWxss(MP)) {
      let s = readFileSync(p, 'utf-8')
      if (p === APP_WXSS) {
        // 变量定义块本身就是这些默认值，只检查它之后的「真实样式区」
        s = s.slice(s.indexOf('page { background:'))
      }
      for (const color of legacy) expect(s).not.toContain(color)
    }
  })

  it('六个访客页的每个顶层状态节点都挂主题 class，且 onLoad 绑定主题', () => {
    const cls = 'wc-theme {{themeClass}}'
    for (const page of VISITOR_PAGES) {
      const wxml = readFileSync(resolve(MP, page.wxml), 'utf-8')
      const ts = readFileSync(resolve(MP, page.ts), 'utf-8')
      const hits = wxml.split(cls).length - 1
      expect(hits, page.wxml).toBeGreaterThanOrEqual(page.roots)
      expect(ts, page.ts).toContain('bindTheme(this)')
      expect(ts, page.ts).toContain("themeClass: ''")
      // 二级页面是 '../../utils/theme'，三级是 '../../../utils/theme'，统一用相对层级正则
      expect(ts, page.ts).toMatch(/from '(?:\.\.\/)+utils\/theme'/)
    }
  })

  it('首页从 /api/home-layout 拿到 templateId 后应用并广播', () => {
    const src = readFileSync(resolve(MP, 'pages/index/index.ts'), 'utf-8')
    expect(src).toContain('applyTheme(layout.templateId)')
    expect(src).toContain("request<HomeLayoutResp>('GET', '/api/home-layout')")
  })

  it('currentThemeClass 从 Storage 恢复；存储不可用时也不抛错', () => {
    vi.stubGlobal('getCurrentPages', () => [])
    vi.stubGlobal('wx', {
      getStorageSync: () => 'ink',
      setStorageSync: vi.fn(),
    })
    expect(currentThemeClass()).toBe('wc-theme-ink')

    vi.stubGlobal('wx', {
      getStorageSync: () => {
        throw new Error('storage disabled')
      },
      setStorageSync: vi.fn(),
    })
    expect(currentThemeClass()).toBe('')
  })

  it('applyTheme 缓存 id 并广播到页面栈；bindTheme 把当前主题写回本页', () => {
    const setStorageSync = vi.fn()
    const pageA = { setData: vi.fn() }
    const pageB = { setData: vi.fn() }
    vi.stubGlobal('wx', { getStorageSync: () => 'warm', setStorageSync })
    vi.stubGlobal('getCurrentPages', () => [pageA, pageB])

    applyTheme('ink')
    expect(setStorageSync).toHaveBeenCalledWith('wc_theme_id', 'ink')
    expect(pageA.setData).toHaveBeenCalledWith({ themeClass: 'wc-theme-ink' })
    expect(pageB.setData).toHaveBeenCalledWith({ themeClass: 'wc-theme-ink' })

    const page = { setData: vi.fn() }
    bindTheme(page)
    expect(page.setData).toHaveBeenCalledWith({ themeClass: 'wc-theme-warm' })
  })
})

// ===== 两个系列的对账 =====
//
// 品牌配色是「客户按自有 VI 挑主色」的增值能力，产品口径明确只换配色、不动版式。
// 如果 Go 的 Series 与端上守门的 brand- 前缀分了叉，客户只会看到卡片出现在错误的
// 分组里，不会有任何报错；如果后台改成自己猜前缀，服务端就失去分组的真值地位。
describe('小程序风格模板 系列分组', () => {
  it('Go 的 Series 与端上 brand- 前缀一一对应（后台分组不能和守门分组漂移）', () => {
    const series = goSeriesById()
    expect(Object.keys(series).sort()).toEqual([...THEME_IDS].sort())
    for (const id of THEME_IDS) {
      expect(series[id], `${id} 的系列`).toBe(id.startsWith(BRAND_PREFIX) ? 'brand' : 'style')
    }
  })

  it('预设色板固定 8 款，整体风格高级款固定 3 款（加款必须同步扩色板与预览图）', () => {
    expect(BRAND_IDS).toHaveLength(8)
    expect(STYLE_ADVANCED_IDS).toEqual(['ink', 'warm', 'teal'])
  })

  it('后台按服务端 series 分组渲染，不自己猜 id 前缀', () => {
    const vue = readFileSync(resolve(REPO, 'frontend/src/views/content/MpTheme.vue'), 'utf-8')
    expect(vue).toContain("t.series === 'brand'")
    expect(vue).toContain('brandTemplates')
    expect(vue).toContain('styleTemplates')
    expect(vue).toContain('品牌配色')
    expect(vue).not.toContain("startsWith('brand-')")
  })
})

// ===== 配色可读性守门 =====
//
// 模板是要卖给客户的增值能力：深主色压在「白字按钮」或「浅底主色小字」上时，
// 客户选完款才发现字看不清，属于典型的静默失败，只能靠实测钉住。
// default 是模板功能上线前就有的配色（白字/主色仅 3.37，最低 2.95），
// 按「升级零突变」不能改，所以这里只锁「不许变差」；新增款一律按 WCAG AA
// 正文级 4.5 卡，加第 4 款时会被自动覆盖。
function srgbLuminance(hex: string): number {
  const m = /^#([0-9a-fA-F]{6})$/.exec(hex.trim())
  if (!m) throw new Error(`不是可计算的实色，拒绝让 NaN 混过断言：${hex}`)
  const channels = [0, 2, 4].map((i) => parseInt(m[1].slice(i, i + 2), 16) / 255)
  const [r, g, b] = channels.map((c) =>
    c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  )
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrastRatio(a: string, b: string): number {
  const la = srgbLuminance(a)
  const lb = srgbLuminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

type ThemeVars = Record<string, string>

function readThemeVars(): Record<string, ThemeVars> {
  const src = readFileSync(APP_WXSS, 'utf-8')
  const parse = (block: string): ThemeVars =>
    Object.fromEntries(
      Array.from(block.matchAll(/(--wc-[\w-]+)\s*:\s*([^;]+);/g)).map((m) => [m[1], m[2].trim()])
    )
  const def = src.match(/page\s*\{([^{}]*--wc-primary[^{}]*)\}/)
  if (!def) throw new Error('app.wxss 找不到 page 上的默认主题变量块')
  const out: Record<string, ThemeVars> = { [THEME_DEFAULT]: parse(def[1]) }
  for (const m of src.matchAll(/\.wc-theme-([a-z0-9_-]+)\s*\{([^{}]*)\}/g)) {
    out[m[1]] = parse(m[2])
  }
  return out
}

function colorOf(vars: ThemeVars, key: string): string {
  if (key.startsWith('#')) return key
  const v = vars[key]
  if (!v) throw new Error(`主题缺少 ${key}`)
  return v
}

// 源码里真实出现过的 12 组搭配（见 app.wxss 的 .primary-btn/.case-btn/.chip.on/
// .pcat-chip.on/.cv-ph 等），不是凭空挑的「好看组合」。
const PAIRS: { label: string; fg: string; bg: string }[] = [
  { label: '白字/primary', fg: '#ffffff', bg: '--wc-primary' },
  { label: '白字/deep', fg: '#ffffff', bg: '--wc-primary-deep' },
  { label: '白字/grad', fg: '#ffffff', bg: '--wc-primary-grad' },
  { label: '白字/alt', fg: '#ffffff', bg: '--wc-primary-alt' },
  { label: 'primary/白底', fg: '--wc-primary', bg: '#ffffff' },
  { label: 'primary/soft', fg: '--wc-primary', bg: '--wc-primary-soft' },
  { label: 'primary/tint', fg: '--wc-primary', bg: '--wc-primary-tint' },
  { label: 'primary/chip', fg: '--wc-primary', bg: '--wc-primary-chip' },
  { label: 'primary/pcat', fg: '--wc-primary', bg: '--wc-primary-pcat' },
  { label: 'deep/白底', fg: '--wc-primary-deep', bg: '#ffffff' },
  { label: 'deep/soft', fg: '--wc-primary-deep', bg: '--wc-primary-soft' },
  { label: 'alt/白底', fg: '--wc-primary-alt', bg: '#ffffff' },
]

// 模板功能上线前（afb5705 之前）默认配色的实测值。它们低于 AA 是历史事实，
// 不是本次引入；这里只钉住「不许更差」，改进默认色不受限。
const DEFAULT_BASELINE: Record<string, number> = {
  '白字/primary': 3.37, '白字/deep': 5.23, '白字/grad': 4.81, '白字/alt': 4.50,
  'primary/白底': 3.37, 'primary/soft': 3.21, 'primary/tint': 2.95,
  'primary/chip': 3.00, 'primary/pcat': 3.03, 'deep/白底': 5.23,
  'deep/soft': 4.98, 'alt/白底': 4.50,
}

function goPreviewColors(): Record<string, { primary: string; soft: string }> {
  const go = readFileSync(GO_TPL, 'utf-8')
  const defId = go.match(/DefaultID\s*=\s*"([a-z0-9_-]+)"/)
  if (!defId) throw new Error('mptemplate.go 找不到 DefaultID')
  const out: Record<string, { primary: string; soft: string }> = {}
  const re = /ID:\s*(DefaultID|"([a-z0-9_-]+)")[\s\S]*?PreviewPrimary:\s*"(#[0-9a-fA-F]{6})"\s*,\s*PreviewSoft:\s*"(#[0-9a-fA-F]{6})"/g
  for (const m of go.matchAll(re)) out[m[2] || defId[1]] = { primary: m[3], soft: m[4] }
  return out
}

describe('小程序整体风格模板 可读性 / 预览色对账', () => {
  it('新增模板的关键搭配全部达到 WCAG AA；默认款不低于历史基线', () => {
    const all = readThemeVars()
    expect(Object.keys(all).sort()).toEqual([...THEME_IDS].sort())
    for (const [id, vars] of Object.entries(all)) {
      for (const p of PAIRS) {
        const ratio = contrastRatio(colorOf(vars, p.fg), colorOf(vars, p.bg))
        const threshold = id === THEME_DEFAULT ? DEFAULT_BASELINE[p.label] - 0.01 : 4.5
        expect(
          ratio >= threshold,
          `${id} ${p.label} 对比度 ${ratio.toFixed(2)} < ${threshold.toFixed(2)}`
        ).toBe(true)
      }
    }
  })

  it('--wc-primary-rgb 必须与 --wc-primary 是同一颜色（阴影/光晕不能和按钮错色）', () => {
    for (const [id, vars] of Object.entries(readThemeVars())) {
      const hex = colorOf(vars, '--wc-primary').replace('#', '')
      const expected = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16))
      const actual = colorOf(vars, '--wc-primary-rgb').split(',').map((s) => Number(s.trim()))
      expect(actual, id).toEqual(expected)
    }
  })

  it('后台模板卡预览色与端上真实色值一致（端上调色不能漏改后台）', () => {
    const all = readThemeVars()
    const previews = goPreviewColors()
    expect(Object.keys(previews).sort()).toEqual([...THEME_IDS].sort())
    for (const [id, p] of Object.entries(previews)) {
      const vars = all[id]
      expect(vars, `app.wxss 缺主题 ${id}`).toBeDefined()
      expect(p.primary.toLowerCase(), `${id} 预览主色`).toBe(colorOf(vars, '--wc-primary').toLowerCase())
      // 预览浅色允许取该主题任一档浅底（后台色块要看得见，通常取 tint 而非最浅的 soft）
      const lights = ['--wc-primary-soft', '--wc-primary-tint', '--wc-primary-chip', '--wc-primary-pcat', '--wc-primary-border']
      expect(lights.map((k) => colorOf(vars, k).toLowerCase()), `${id} 预览浅色`).toContain(p.soft.toLowerCase())
    }
  })
})

// ===== 版式差异守门 =====
//
// 只换色的「高级款」卖不出增值价：客户选完发现版式一模一样，是典型的静默失败。
// 但版式也不能动布局盒模型（padding / width / flex 一律不进 token），
// 更不能把默认款外观改掉（存量客户升级零突变）。
const LAYOUT_TOKENS = [
  '--wc-r-card', '--wc-r-card-bg', '--wc-r-control', '--wc-r-chip',
  '--wc-card-shadow', '--wc-card-shadow-soft', '--wc-card-shadow-bg',
] as const

// 模板功能上线前（afb5705）的原值，逐字锁定。
const DEFAULT_LAYOUT_BASELINE: Record<string, string> = {
  '--wc-r-card': '20rpx',
  '--wc-r-card-bg': '16rpx',
  '--wc-r-control': '40rpx',
  '--wc-r-chip': '28rpx',
  '--wc-card-shadow': '0 6rpx 18rpx rgba(30, 90, 160, 0.08)',
  '--wc-card-shadow-soft': '0 6rpx 18rpx rgba(30, 90, 160, 0.06)',
  '--wc-card-shadow-bg': '0 6rpx 20rpx rgba(0,0,0,.10)',
}

describe('小程序整体风格模板 版式差异', () => {
  it('默认款版式 token 逐字等于上线前原值（升级零突变）', () => {
    const vars = readThemeVars()[THEME_DEFAULT]
    for (const key of LAYOUT_TOKENS) {
      expect(vars[key], key).toBe(DEFAULT_LAYOUT_BASELINE[key])
    }
  })

  it('每款整体风格高级模板都有真实的版式差异，不能只是换了个颜色', () => {
    const all = readThemeVars()
    const base = all[THEME_DEFAULT]
    for (const id of STYLE_ADVANCED_IDS) {
      const vars = all[id]
      const diffs = LAYOUT_TOKENS.filter((k) => vars[k] !== base[k])
      expect(diffs.length, `${id} 只有 ${diffs.length} 项版式差异`).toBeGreaterThanOrEqual(4)
    }
  })

  // 与上面的约束正好相反：品牌配色的卖点是「只对齐主色，版式不动」。
  // 客户挑完主色发现按钮圆角也变了，或者后来有人顺手给某款加了「更好看」的
  // 圆角，都是不会报错的静默改版，必须逐字钉住。
  it('品牌配色 8 款的版式 token 逐字等于 default（只换配色，不动版式）', () => {
    const all = readThemeVars()
    const base = all[THEME_DEFAULT]
    for (const id of BRAND_IDS) {
      const vars = all[id]
      expect(vars, `app.wxss 缺主题 ${id}`).toBeDefined()
      for (const key of LAYOUT_TOKENS) {
        expect(vars[key], `${id} 的 ${key} 不沿用经典蓝版式`).toBe(base[key])
      }
      // 换色必须真的发生，否则这一款就是 default 的别名
      expect(vars['--wc-primary'], `${id} 没有自己的主色`).not.toBe(base['--wc-primary'])
    }
  })

  it('每个版式 token 都真的被样式引用（定义了却没用 = 假差异）', () => {
    let styles = ''
    for (const p of collectWxss(MP)) styles += readFileSync(p, 'utf-8') + '\n'
    for (const key of LAYOUT_TOKENS) {
      const hits = styles.split(`var(${key})`).length - 1
      expect(hits, `${key} 没有被任何 WXSS 使用`).toBeGreaterThanOrEqual(1)
    }
    expect(styles.split('var(--wc-r-control)').length - 1).toBeGreaterThanOrEqual(8)
  })

  it('后台预览卡为每款整体风格高级款画出版式差异（加款忘了补示意会变红）', () => {
    const src = readFileSync(resolve(REPO, 'frontend/src/views/content/MpTheme.vue'), 'utf-8')
    expect(src).toContain(`:class="'layout-' + t.id"`)
    // default 与品牌配色都不需要专属示意规则：它们用的就是预览卡基线样式。
    // 给品牌款加了 .layout-brand-*，等于在示意图里凭空造出并不存在的版式差异。
    expect(src).not.toContain('.layout-default')
    for (const id of STYLE_ADVANCED_IDS) {
      expect(src, `缺 .layout-${id} 的卡片示意`).toMatch(
        new RegExp(`\\.layout-${id}\\s+\\.preview-card\\s*\\{`)
      )
      expect(src, `缺 .layout-${id} 的按钮示意`).toContain(`.layout-${id} .preview-btn`)
    }
    for (const id of BRAND_IDS) {
      expect(src, `${id} 不该有专属版式示意`).not.toContain(`.layout-${id}`)
    }
  })
})

// ===== 真实预览图 跨端对账 =====
//
// 后台模板卡若退回「手画 CSS 色块」，或加了一款模板却忘了重跑生成脚本，
// 都不会有人报错：客户只是看到一张和端上对不上的示意图，或一张 404 空白。
// 这里把 Go 白名单 / PNG 产物 / 生成脚本画布 / 后台容器比例 / 接入代码钉成一条链，
// 并且和生成脚本的 --check 互为补充：那个查产物，这个查「接入代码有没有被改回去」。
const PREVIEW_DIR = resolve(REPO, 'frontend/public/theme-previews')
const PREVIEW_SCRIPT = resolve(REPO, 'scripts/gen_theme_previews.py')
const MP_THEME_VUE = resolve(REPO, 'frontend/src/views/content/MpTheme.vue')

function goTemplateIds(): string[] {
  const src = readFileSync(GO_TPL, 'utf-8')
  const def = /DefaultID\s*=\s*"([a-z0-9_-]+)"/.exec(src)?.[1]
  if (!def) throw new Error('mptemplate.go 找不到 DefaultID')
  return Array.from(src.matchAll(/ID:\s*(DefaultID|"([a-z0-9_-]+)")/g)).map((m) => m[2] || def)
}

describe('小程序整体风格模板 真实预览图', () => {
  it('每款模板都有一张合法 PNG，尺寸与生成脚本画布一致，没有多余图', () => {
    const ids = goTemplateIds()
    expect(ids.length).toBeGreaterThanOrEqual(2)
    expect([...THEME_IDS].sort()).toEqual([...ids].sort(), '小程序 class 白名单与 Go 白名单漂移')
    const files = readdirSync(PREVIEW_DIR)
      .filter((f) => f.endsWith('.png'))
      .map((f) => f.slice(0, -4))
    expect(files.sort()).toEqual([...ids].sort(), '加款忘了重跑 gen_theme_previews.py')

    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)
    const scale = /SCALE\s*=\s*(\d+)/.exec(script)
    expect(dim, '生成脚本缺画布尺寸').not.toBeNull()
    expect(scale, '生成脚本缺 SCALE').not.toBeNull()
    const expected = [Number(dim![1]) * Number(scale![1]), Number(dim![2]) * Number(scale![1])]

    for (const id of ids) {
      const buf = readFileSync(join(PREVIEW_DIR, `${id}.png`))
      expect(buf.subarray(0, 8).toString('hex'), `${id} 不是 PNG`).toBe('89504e470d0a1a0a')
      expect([buf.readUInt32BE(16), buf.readUInt32BE(20)], `${id} 尺寸`).toEqual(expected)
      expect(buf.length, `${id} 疑似空白图`).toBeGreaterThan(5000)
    }
  })

  // 「每张图都存在、尺寸都对、token 快照也对」仍挡不住一种静默失败：生成脚本
  // 若把 12 款都用 default 的 token 出图，客户会看到 12 张一模一样的预览，
  // 而上面所有检查都还是绿的。逐张比内容摘要才能钉住「图真的按各自 token 画了」。
  it('每款预览图内容互不相同（不能 12 张都是同一张默认款）', () => {
    const ids = goTemplateIds()
    const digests = ids.map((id) =>
      createHash('sha256').update(readFileSync(join(PREVIEW_DIR, `${id}.png`))).digest('hex')
    )
    expect(new Set(digests).size, '有重复预览图：某一款没按自己的 token 出图').toBe(ids.length)
  })

  it('生成脚本从 app.wxss 取 token，不手画第二套主题色', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toContain('miniprogram/app.wxss')
    expect(script).toContain('read_tokens()')
    expect(script).toMatch(/TOKEN_RE\s*=\s*re\.compile/)
    // 模板 id 与顺序必须以 Go 白名单为准，脚本里不能自己再列一份
    expect(script).toContain('mptemplate.go')

    const html = script.slice(script.indexOf('HTML = Template("""'), script.indexOf('def find_chrome'))
    expect(html.length, '取不到预览 HTML 模板').toBeGreaterThan(1000)
    for (const id of THEME_IDS) {
      const primary = colorOf(readThemeVars()[id], '--wc-primary')
      expect(html.toLowerCase(), `${id} 主色被硬编码进预览 HTML`).not.toContain(primary.toLowerCase())
    }
    expect(html.split('var(--wc-').length - 1, '预览 HTML 没有真正用 token').toBeGreaterThanOrEqual(15)
  })

  it('后台容器比例与生成画布一致，否则真实图会被裁切/拉伸', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)!
    const ratio = /aspect-ratio:\s*(\d+)\s*\/\s*(\d+)/.exec(vue)
    expect(ratio, '后台预览容器缺 aspect-ratio，图没加载完会跳版').not.toBeNull()
    expect([Number(ratio![1]), Number(ratio![2])]).toEqual([Number(dim[1]), Number(dim[2])])
    expect(vue).toContain('object-fit: cover')
  })

  it('后台确实渲染真实图并保留按张降级的兜底（改回色块示意会变红）', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('class="preview-shot"')
    expect(vue).toContain(':src="previewUrl(t.id)"')
    expect(vue).toContain('theme-previews/')
    expect(vue).toContain('encodeURIComponent(id)')
    expect(vue).toContain('import.meta.env.BASE_URL')
    expect(vue).toContain('@error="onPreviewError(t.id)"')
    expect(vue).toContain('class="preview-fallback"')
    expect(vue).toContain('class="fallback-tag"')
    expect(vue).toContain('failedPreviews.value = {}')
  })

  // 只查「PNG 都在」挡不住「app.wxss 改了配色却没重出图」：文件一个不少，
  // 客户看到的仍是旧样式，而且没人会报错。manifest 是出图时写下的 token 快照。
  it('token 快照与 app.wxss 同版（改了配色 / 圆角没重出图会变红）', () => {
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    expect(manifest.source).toBe('miniprogram/app.wxss')
    const ids = goTemplateIds()
    expect(Object.keys(manifest.tokens).sort()).toEqual([...ids].sort())

    const current = readThemeVars()
    for (const id of ids) {
      expect(manifest.tokens[id], `${id} 的 token 快照`).toEqual(current[id])
    }

    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)!
    const scale = /SCALE\s*=\s*(\d+)/.exec(script)!
    expect(manifest.canvas).toEqual([Number(dim[1]), Number(dim[2])])
    expect(manifest.scale).toBe(Number(scale[1]))
  })
})

// ===== 完整首页示意 =====
//
// 早期版本为了在 375x430 里保住底部按钮的圆角差异，把公司简介 / 业务服务区
// 整个裁掉，还用「◈」菱形当案例占位——客户在后台看到的根本认不出是自己的小程序。
// 现在按 index.wxml 的区块顺序复刻完整首页。这些守门防止有人「为了卡片好看」
// 又退回简化版：页面不会报错，客户只是拿到一张不准确的示意图，属于静默失败。
const HOME_SECTION_ANCHORS = [
  { sec: 'nav', pattern: 'class="custom-nav"' },
  { sec: 'card', pattern: '<employee-card' },
  { sec: 'company', pattern: '<company-video' },
  { sec: 'achievement', pattern: "item === 'achievement'" },
  { sec: 'service', pattern: "item === 'service'" },
  { sec: 'bar', pattern: '<action-bar' },
] as const

function previewHtml(): string {
  const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
  return script.slice(script.indexOf('HTML = Template("""'), script.indexOf('def find_chrome'))
}

describe('小程序风格模板 完整首页示意', () => {
  it('预览 HTML 复刻首页全部区块，且顺序与 index.wxml 一致', () => {
    const wxml = readFileSync(resolve(MP, 'pages/index/index.wxml'), 'utf-8')
    let cursor = -1
    for (const a of HOME_SECTION_ANCHORS) {
      const at = wxml.indexOf(a.pattern)
      expect(at, `index.wxml 找不到 ${a.sec} 锚点`).toBeGreaterThan(-1)
      expect(at, `index.wxml 里 ${a.sec} 的相对顺序变了`).toBeGreaterThan(cursor)
      cursor = at
    }

    const html = previewHtml()
    const secs = Array.from(html.matchAll(/data-sec="([a-z]+)"/g)).map((m) => m[1])
    // status 是真机系统状态栏的占位，真实 WXML 里由系统绘制、没有对应节点
    expect(secs.filter((s) => s !== 'status')).toEqual(HOME_SECTION_ANCHORS.map((a) => a.sec))
    expect(html, '不该再用菱形占位代替真实案例卡').not.toContain('◈')
    for (const label of ['公司简介', '经典业绩', '业务服务', '一键拨号', '存入通讯录']) {
      expect(html, `预览缺「${label}」`).toContain(label)
    }
    // 底部按钮的实心 / 描边左右顺序也和 action-bar 组件一致，别再写反
    expect(html).toMatch(/class="call">一键拨号<\/button><button class="save">存入通讯录/)
    // 真机首页的 8 个分类图标与 4 张竖版服务卡，不能退回 chips / 横条色块
    expect(html).toContain('${cat_cards}')
    expect(html).toContain('${svc_cards}')
    expect(html).toContain('cover-daxing.jpg')
    expect(html).toContain('cover-hospital.jpg')
  })

  it('18 个 token 全部被预览 HTML 用 var() 引用（定义了却没用 = 假同源）', () => {
    const html = previewHtml()
    const css = html.slice(0, html.indexOf('</style>'))
    const vars = Object.keys(readThemeVars()[THEME_DEFAULT])
    expect(vars, '主题 token 数量变了').toHaveLength(18)
    for (const v of vars) {
      expect(css, `${v} 定义了却没被示意图引用`).toContain(`var(${v})`)
    }
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toContain('css_tokens(tokens)')
    expect(script).toContain('build_html(active[i])')
    expect(script).toContain('${tokens}')
  })

  it('画布按内容自然收口：区块齐全、尾部不留大空白、底栏不会盖住服务卡', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)
    const parts = /CONTENT_BOTTOM,\s*TAIL_GAP,\s*BAR_HEIGHT\s*=\s*(\d+),\s*(\d+),\s*(\d+)/.exec(script)
    expect(dim, '生成脚本缺画布尺寸').not.toBeNull()
    expect(parts, '画布必须由「内容底部 + 呼吸 + 底栏」三项算出，别留一条大尾巴').not.toBeNull()
    const [content, gap, bar] = parts!.slice(1).map(Number)
    expect(Number(dim![1])).toBe(375)
    expect(Number(dim![2]), '不能压回 430 / 640 的旧简化版').toBeGreaterThanOrEqual(1150)
    expect(Number(dim![2]), '尾部空白不该再回到 1300 的长尾巴').toBeLessThanOrEqual(1250)
    expect(Number(dim![2])).toBe(content + gap + bar)
    expect(gap, '呼吸超过 24px 就会让按钮和服务卡重新割裂').toBeLessThanOrEqual(24)
    // CSS 必须吃同一组常量，不能脚本算一套、样式写死另一套 60px
    expect(script).toContain('padding-bottom: ${tail_gap}px')
    expect(script).toContain('height: ${bar_h}px')
    // 改了占位文案 / 区块高度，absolute 底栏会静默盖住服务卡：出图前必须实测
    expect(script).toContain('measure_content_bottom(chrome, build_html(')
    expect(script).toContain('bottom + TAIL_GAP > bar_top')
  })

  it('预览只引用通用行业素材，且照片不被主题色染（换模板不该连视频都变色）', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const html = previewHtml()
    const seed = resolve(REPO, 'backend/internal/demoseed/seedassets')
    // 分类图标 / 服务卡 / 精选案例 / 公司简介封面，少一张就会退回占位色块。
    // 真人头像与带企业名的 LOGO 刻意不在此列：见下方「通用平台脱敏」守门。
    for (const name of [
      'cover-company.jpg', 'cover-daxing.jpg', 'cover-hospital.jpg',
      ...Array.from({ length: 8 }, (_, i) => `cat-${i + 1}.png`),
      ...Array.from({ length: 4 }, (_, i) => `svc-${i + 1}.png`),
    ]) {
      expect(readFileSync(join(seed, name)).length, `缺通用素材 ${name}`).toBeGreaterThan(0)
    }
    // 只查名字会假绿：函数定义 / 变量引用里也有这些字样，删掉真正的调用照样放行
    expect(script).toContain('"seedAssets": SEED_FILES')
    expect(script).toContain('DEMO_SOURCES')
    expect(script).toContain('check_sources() + check_no_identity(build_html(')
    // 8 张分类图 / 4 张服务卡由 build_html() 循环拼出，静态引用则直接写在模板里
    expect(script).toContain('assets/cat-')
    expect(script).toContain('assets/svc-')
    expect(html).toContain('assets/cover-company.jpg')
    expect(html).toContain('assets/cover-daxing.jpg')

    // 第二十七轮实测发现的坑：cover-shade 曾把主色 rgba() 叠在租户照片上，
    // 绛红款的封面被整片染红。真机上租户上传的照片/视频不随模板变色。
    const shade = html.match(/\.cover-shade\s*\{[^}]*\}/)?.[0] || ''
    expect(shade, '取不到 .cover-shade 规则').not.toBe('')
    expect(shade, '封面照片被主题色染了').not.toContain('--wc-primary-rgb')
    expect(shade).toContain('rgba(0,0,0,')
  })

  it('右缘悬浮音符必须可见，不能被底部操作栏盖住', () => {
    const html = previewHtml()
    const rule = html.match(/\.music\s*\{[^}]*\}/)?.[0] || ''
    expect(rule, '取不到 .music 规则').not.toBe('')
    expect(rule).toContain('position: absolute')
    // 第二十七轮实测：bottom:8px 锚到 .phone 底部，被后画的 .bar 整块盖住。
    // DOM 和 CSS 都在、客户却看不见，是典型静默失败，必须钉死。
    expect(rule, '音符被压到页面底部会看不见').toContain('top:')
    expect(rule).not.toContain('bottom:')
    // 必须是 .phone 的直接子节点：塞回某个 .section 里会跟着卡片裁切/换行
    const at = html.indexOf('<span class="music">')
    expect(at).toBeGreaterThan(html.indexOf('data-sec="service"'))
    expect(html.slice(at, html.indexOf('data-sec="bar"'))).not.toContain('</div>')
    // 这两个 token 只在音符上用到，别为了「顺便清理」把它们删掉
    expect(rule).toContain('var(--wc-primary-pcat)')
    expect(rule).toContain('var(--wc-primary-alt)')
  })

  it('manifest 记下通用素材与占位文案，换了素材 / 文案却没重出图会变红', () => {
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const listed = Array.from(script.matchAll(/"(cat-\d\.png|svc-\d\.png|cover-[\w-]+\.jpg)"/g))
      .map((m) => m[1])
    expect(Array.isArray(manifest.seedAssets)).toBe(true)
    expect(manifest.seedAssets, '身份素材不能出现在素材清单里').not.toContain('logo.png')
    expect(manifest.seedAssets.length, '8 分类 + 4 服务 + 3 照片').toBe(15)
    for (const name of listed) {
      expect(manifest.seedAssets, `manifest 漏记素材 ${name}`).toContain(name)
    }
    // 占位文案也要留快照：改了「示例企业」却没重出图，图片会停在旧文案上而没人报错
    const ph = manifest.placeholders as Record<string, string> | undefined
    expect(ph, 'manifest 缺占位文案快照').toBeDefined()
    expect(Object.keys(ph!).sort()).toEqual(
      ['short_name', 'full_name', 'address', 'person', 'title', 'phone', 'tel', 'email', 'intro'].sort()
    )
    expect(ph!.email).toBe('name@example.com')
    for (const v of Object.values(ph!)) {
      expect(v, '占位文案里混进了真实客户身份').not.toContain('朗汇')
      expect(v).not.toContain('廊坊')
    }
  })

  it('后台常显「非真机截图」并提供查看大图（改回旧标注会变红）', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('class="preview-badge"')
    expect(vue).toContain('风格示意 · 非真机截图')
    expect(vue).toContain('class="preview-zoom-btn"')
    expect(vue).toContain('@click.stop="openZoom(t.id)"')
    expect(vue).toContain('class="preview-zoom"')
    expect(vue).toContain('class="zoom-shot"')
    // 750x2600 长图不能再用 max-height 压成一条细线：固定面板宽度 + 内部纵向滚动
    expect(vue).toContain('class="zoom-scroll"')
    expect(vue).toContain('overflow-y: auto')
    expect(vue).toContain('width: min(420px, 92vw)')
    expect(vue).toContain('height: auto')
    expect(vue, '说明文案必须写明非真机截图').toContain('首页风格示意，不是真机截图')
  })
})

// ===== 通用平台脱敏 + 底部收口 =====
//
// 第二十八轮用户反馈：这是通用平台产品，示意图里出现「廊坊市朗汇机电设备有限公司 /
// 高立新 / 13832603521 / 真实 LOGO」会让客户误以为这是别人的真实案例；业务服务区底部
// 一大片空白又把图内按钮和卡片外的模板名称连成一片。两件事都不会报错，只能靠
// 读源码 + 读产物快照的断言钉住。
const DEMO_GO = resolve(REPO, 'backend/internal/demoseed/demoseed.go')

function identityBlacklist(): string[] {
  const go = readFileSync(DEMO_GO, 'utf-8')
  const literals = [
    '廊坊', '紫金城', '0316-', '@163.com', '成立于 2015', 'langhui',
    '13832603521', '高立新', '朗汇',
  ]
  const fromGo = Array.from(go.matchAll(
    /Demo(?:CompanyName|ShortName|LoginCode|EmployeePhone|EmployeeName|AdminUser)\s*=\s*"([^"]+)"/g
  )).map((m) => m[1])
  return [...literals, ...fromGo]
}

describe('小程序风格模板 通用平台脱敏', () => {
  it('预览 HTML 与占位文案里不出现任何真实或演示客户身份', () => {
    const html = previewHtml()
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const placeholders = Object.values(manifest.placeholders as Record<string, string>)
    for (const token of identityBlacklist()) {
      expect(html, `预览 HTML 出现真实客户身份「${token}」`).not.toContain(token)
      for (const v of placeholders) {
        expect(v, `占位文案出现真实客户身份「${token}」`).not.toContain(token)
      }
    }
    // 身份素材连引用都不许有：logo.png 里画着企业名，真人头像也是具体客户的人
    expect(html).not.toContain('assets/avatar-')
    expect(html).not.toContain('assets/logo.png')
    // 只查 SEED_FILES 清单，不查整份脚本：黑名单常量里本来就有这些字面量
    const seedBlock = script.match(/SEED_FILES\s*=\s*\[([\s\S]*?)\]/)?.[1] || ''
    expect(seedBlock, '取不到 SEED_FILES 清单').not.toBe('')
    expect(seedBlock, '素材清单混进了真人头像').not.toContain('avatar-')
    expect(seedBlock, '素材清单混进了企业 LOGO').not.toContain('logo.')
  })

  it('企业 / 联系人 / 联系方式全部换成明显的中性占位，且保留原版式密度', () => {
    const html = previewHtml()
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const ph = manifest.placeholders as Record<string, string>
    for (const key of ['short_name', 'full_name', 'person', 'title', 'phone', 'tel', 'email', 'address', 'intro']) {
      expect(ph[key], `缺占位 ${key}`).toBeTruthy()
    }
    // 头像 / LOGO 用 CSS 画占位，不能退回真实素材
    expect(html).toContain('class="avatar-ph"')
    expect(html).toContain('class="logo-ph"')
    expect(html).toContain('<span class="logo-ph">LOGO</span>')
    expect(html).toContain('class="me-avatar"')
    // 脱敏不能顺手把上一轮复刻好的版式密度简化掉
    expect(html).toContain('${cat_cards}')
    expect(html).toContain('${svc_cards}')
    expect(html).toContain('data-sec="company"')
  })

  it('占位文案字典本身也不含真实客户身份（改了字典却没重出图也要能发现）', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    // 反向验证抓到过一次假绿：HTML 模板里只有 ${short_name}，manifest 还是旧快照，
    // 于是把字典改回「朗汇机电」时 --check 会红、端上守门却全绿。必须直接查字典。
    const block = script.match(/PLACEHOLDER_TEXT\s*=\s*\{([\s\S]*?)\n\}/)?.[1] || ''
    expect(block, '取不到 PLACEHOLDER_TEXT 字典').not.toBe('')
    for (const token of identityBlacklist()) {
      expect(block, `占位文案字典出现真实客户身份「${token}」`).not.toContain(token)
    }
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    expect(manifest.placeholders.short_name).toBe('示例企业')
    expect(manifest.placeholders.person).toBe('示例联系人')
  })

  it('脱敏守门真的被调用，且查的是最终 HTML 而不是脚本源码', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    // 第二十七轮的教训：只断言函数 / 变量名字，删掉真正调用照样全绿
    expect(script).toContain('check_sources() + check_no_identity(build_html(active[ids[0]]))')
    expect(script).toContain('for token in identity_tokens():')
    expect(script).toContain('GO_IDENTITY_RE.finditer(DEMO_GO.read_text(')
    expect(script).toContain('PLACEHOLDER_TEXT[key] not in html')
  })

  it('后台说明写明「通用平台 / 不含真实客户资料」', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('本产品是通用平台')
    expect(vue).toContain('不含任何真实企业客户资料')
  })
})

describe('小程序风格模板 预览区与名称区的视觉分隔', () => {
  it('预览图底部有硬边界、名称区用浅灰底，图内按钮不再和模板说明连成一片', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    const preview = vue.match(/\.theme-preview\s*\{[^}]*\}/)?.[0] || ''
    const meta = vue.match(/\.theme-meta\s*\{[^}]*\}/)?.[0] || ''
    expect(preview, '取不到 .theme-preview 规则').not.toBe('')
    expect(meta, '取不到 .theme-meta 规则').not.toBe('')
    expect(preview, '预览区与名称区没有分界线').toContain('border-bottom:')
    expect(meta, '名称区仍是纯白底，会和图内白色操作栏混在一起').toContain('background:')
    expect(meta).toContain('#f8fafc')
  })

  it('底栏自身也要有清晰的顶边，长图尾部不再是一整片纯底色', () => {
    const html = previewHtml()
    // 不能用 /\.bar\s*\{[^}]*\}/：规则里有 ${bar_h}，正则会被变量自带的 } 截断，
    // 拿半条规则去断言 border-top 必然是假红。按下一条 .bar button 规则切片。
    const start = html.indexOf('.bar {')
    const next = html.indexOf('.bar button', start)
    expect(start, '取不到 .bar 规则').toBeGreaterThan(-1)
    expect(next, '取不到 .bar 规则结束位置').toBeGreaterThan(start)
    const bar = html.slice(start, next)
    expect(bar, '底栏没有顶部分界线').toContain('border-top: 1px solid #d8dee6')
    expect(bar, '底栏缺少「页面到此结束」的层次').toContain('box-shadow: 0 -3px 12px')
    expect(bar).toContain('height: ${bar_h}px')
  })
})
