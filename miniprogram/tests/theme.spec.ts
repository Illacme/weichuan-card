import { describe, it, expect, vi, afterEach } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
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

  it('每款高级模板都有真实的版式差异，不能只是换了个颜色', () => {
    const all = readThemeVars()
    const base = all[THEME_DEFAULT]
    for (const id of THEME_IDS.filter((v) => v !== THEME_DEFAULT)) {
      const vars = all[id]
      const diffs = LAYOUT_TOKENS.filter((k) => vars[k] !== base[k])
      expect(diffs.length, `${id} 只有 ${diffs.length} 项版式差异`).toBeGreaterThanOrEqual(4)
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

  it('后台预览卡为每个高级款画出版式差异（加款忘了补示意会变红）', () => {
    const src = readFileSync(resolve(REPO, 'frontend/src/views/content/MpTheme.vue'), 'utf-8')
    expect(src).toContain(`:class="'layout-' + t.id"`)
    // default 不需要示意规则：它用的就是预览卡基线样式
    expect(src).not.toContain('.layout-default')
    for (const id of THEME_IDS.filter((v) => v !== THEME_DEFAULT)) {
      expect(src, `缺 .layout-${id} 的卡片示意`).toMatch(
        new RegExp(`\\.layout-${id}\\s+\\.preview-card\\s*\\{`)
      )
      expect(src, `缺 .layout-${id} 的按钮示意`).toContain(`.layout-${id} .preview-btn`)
    }
  })
})
