import { describe, it, expect, vi, afterEach } from 'vitest'
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, resolve, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  THEME_IDS,
  STYLE_IDS,
  THEME_DEFAULT,
  normalizeTheme,
  normalizeStyle,
  normalizeBrand,
  splitTheme,
  encodeCachedTheme,
  decodeCachedTheme,
  resolveTheme,
  themeClass,
  currentThemeClass,
  applyTheme,
  bindTheme,
} from '../utils/theme'

// 小程序主题（两轴：风格 × 色系）的跨端契约守门。
//
// 为什么需要它：模板 id 在三个地方出现（Go 白名单 / 后台清单 / 小程序 class），
// 色值在 app.wxss 的**两层**块里（.wc-style-* 布局层 + .wc-brand-* 颜色层），
// 页面根节点要同时挂两层 class 才生效。任意一处漏改都不会报错——访客只是悄悄
// 看到默认蓝色或错误的版式，后台却显示「已选高级款」，属于典型的静默失败，
// 只能靠读源码的断言钉住。
const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const MP = resolve(REPO, 'miniprogram')
const GO_TPL = resolve(REPO, 'backend/internal/mptemplate/mptemplate.go')
const APP_WXSS = resolve(MP, 'app.wxss')

// 两个系列的约束是**相反**的：style 款必须有真实版式差异（P2 起还要有 4 风格 ×
// 模块的布局规则），brand 款必须完全没有版式差异（颜色层块里连一个 --wc-r-* 都
// 不许出现）。所以守门不能一刀切地「对所有非 default 款都要求差异」。
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
    expect(normalizeStyle('neon')).toBe('default')
    expect(normalizeStyle('warm')).toBe('warm')
    expect(normalizeBrand('brand-neon')).toBeNull()
    expect(normalizeBrand('brand-ocean')).toBe('brand-ocean')
    expect(themeClass(THEME_DEFAULT)).toBe('wc-style-default')
    expect(themeClass('warm')).toBe('wc-style-warm')
    expect(themeClass('neon')).toBe('wc-style-default')
  })

  it('splitTheme 只服务旧扁平 id：8 款 brand-* 反推 (default, brand-*)，不代表 P5 归属', () => {
    expect(splitTheme('brand-ocean')).toEqual({ styleId: 'default', brandColorId: 'brand-ocean' })
    expect(splitTheme('ink')).toEqual({ styleId: 'ink', brandColorId: null })
    expect(splitTheme(null)).toEqual({ styleId: 'default', brandColorId: null })
    expect(splitTheme('neon')).toEqual({ styleId: 'default', brandColorId: null })
  })

  it('P5 全叉乘：色系叠在非 default 风格上保留该风格布局，不再被打回 default', () => {
    // P4 的 normalizeSelection 曾把带色系的组合收敛成 default（树形归属）；
    // 后端 IsValidCombo 放开后，这里再收敛就是「后台保存成功、真机版式没换」的静默失败。
    expect(themeClass('ink', 'brand-ocean')).toBe('wc-style-ink wc-brand-ocean')
    expect(themeClass('warm', 'brand-crimson')).toBe('wc-style-warm wc-brand-crimson')
    expect(themeClass('teal', 'brand-violet')).toBe('wc-style-teal wc-brand-violet')
    expect(themeClass('default', 'brand-ocean')).toBe('wc-style-default wc-brand-ocean')
    expect(themeClass('default', null)).toBe('wc-style-default')
    expect(themeClass('ink', 'brand-neon'), '未知色系回落到该风格默认色').toBe('wc-style-ink')
    expect(themeClass('neon', 'brand-ocean'), '未知风格回落 default，色系仍生效')
      .toBe('wc-style-default wc-brand-ocean')
  })

  it('resolveTheme 两轴优先，旧后端只有 templateId 时拆成两轴', () => {
    expect(resolveTheme({ styleId: 'warm', brandColorId: null, templateId: 'brand-amber' }))
      .toEqual({ styleId: 'warm', brandColorId: null })
    expect(resolveTheme({ styleId: 'default', brandColorId: 'brand-crimson' }))
      .toEqual({ styleId: 'default', brandColorId: 'brand-crimson' })
    // P5：非 default 风格 + 色系必须原样保留，兼容派生列也不能把它带偏
    expect(resolveTheme({ styleId: 'ink', brandColorId: 'brand-ocean' }))
      .toEqual({ styleId: 'ink', brandColorId: 'brand-ocean' })
    expect(resolveTheme({ styleId: 'warm', brandColorId: 'brand-magenta', templateId: 'brand-magenta' }))
      .toEqual({ styleId: 'warm', brandColorId: 'brand-magenta' })
    expect(resolveTheme({ templateId: 'brand-crimson' }))
      .toEqual({ styleId: 'default', brandColorId: 'brand-crimson' })
    expect(resolveTheme({ templateId: 'teal' })).toEqual({ styleId: 'teal', brandColorId: null })
    expect(resolveTheme({})).toEqual({ styleId: 'default', brandColorId: null })
    expect(resolveTheme(null)).toEqual({ styleId: 'default', brandColorId: null })
  })

  it('模板 id 与 Go 白名单一一对应（一端加款忘了同步即变红）', () => {
    const go = readFileSync(GO_TPL, 'utf-8')
    const ids = new Set<string>()
    const def = go.match(/DefaultID\s*=\s*"([a-z0-9_-]+)"/)
    if (def) ids.add(def[1])
    for (const m of go.matchAll(/ID:\s*"([a-z0-9_-]+)"/g)) ids.add(m[1])
    expect(Array.from(ids).sort()).toEqual([...THEME_IDS].sort())
  })

  it('布局层每款风格给齐 18 个 token；颜色层只给 11 个颜色 token', () => {
    const src = readFileSync(APP_WXSS, 'utf-8')
    const defaultBlock = src.match(/page\s*\{([\s\S]*?)\n\}/)
    expect(defaultBlock).not.toBeNull()
    const base = varsOf(defaultBlock![1])
    expect(base).toHaveLength(18)
    const isLayout = (v: string) => v.startsWith('--wc-r-') || v.startsWith('--wc-card-shadow')
    const colorVars = base.filter((v) => !isLayout(v))
    expect(colorVars).toHaveLength(11)

    // default 由 page 提供 token，不另写一层（避免两处同值漂移）
    const styleBlocks = Array.from(src.matchAll(/\.wc-style-([a-z0-9_-]+)\s*\{([\s\S]*?)\n\}/g))
    expect(styleBlocks.map((m) => m[1]).sort()).toEqual([...STYLE_ADVANCED_IDS].sort())
    for (const m of styleBlocks) expect(varsOf(m[2])).toEqual(base)

    const brandBlocks = Array.from(src.matchAll(/\.wc-brand-([a-z0-9_-]+)\s*\{([\s\S]*?)\n\}/g))
    expect(brandBlocks.map((m) => 'brand-' + m[1]).sort()).toEqual([...BRAND_IDS].sort())
    for (const m of brandBlocks) {
      expect(varsOf(m[2]), `${m[1]} 的颜色层不是 11 个颜色 token`).toEqual(colorVars)
    }
    // 单层 .wc-theme-* 必须全部拆干净，否则页面挂两层 class 时它是死代码 / 优先级陷阱
    expect(src).not.toMatch(/\.wc-theme-[\w-]+\s*\{/)
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

  it('首页从 /api/home-layout 根级拿两轴主题后应用并广播（旧后端仍走 templateId）', () => {
    const src = readFileSync(resolve(MP, 'pages/index/index.ts'), 'utf-8')
    expect(src).toContain('resolveTheme(layout)')
    expect(src).toContain('applyTheme(sel.styleId, sel.brandColorId)')
    expect(src).toContain("request<HomeLayoutResp>('GET', '/api/home-layout')")
  })

  it('根节点挂两层 class：theme.ts 生成 wc-style-* 与 wc-brand-*', () => {
    const src = readFileSync(resolve(MP, 'utils/theme.ts'), 'utf-8')
    expect(src).toContain("'wc-style-' + sel.styleId")
    expect(src).toContain("'wc-brand-' + sel.brandColorId.slice(BRAND_PREFIX.length)")
    expect(src, '不能退回单层 wc-theme-*').not.toContain("'wc-theme-' +")
    expect(readFileSync(APP_WXSS, 'utf-8')).not.toMatch(/\.wc-theme-[\w-]+\s*\{/)
    // P5：归一函数里绝不能出现 THEME_DEFAULT（那意味着又把带色系的组合打回 default）
    const norm = src.match(/function normalizeSelection\([\s\S]*?\n\}/)?.[0] || ''
    expect(norm, '取不到 normalizeSelection 函数体').not.toBe('')
    expect(norm, 'normalizeSelection 不得再把跨风格组合收敛成 default').not.toContain('THEME_DEFAULT')
    // P5：缓存必须写两轴串，不能压扁回 12 个扁平 templateId
    expect(src).toContain('CACHE_PREFIX')
    expect(src).toContain('encodeCachedTheme')
    expect(src).toContain('decodeCachedTheme')
    expect(src, '不能再留「两轴 → 扁平 id」的写缓存入口').not.toContain('deriveThemeId')
    expect(src).toMatch(/setStorageSync\(STORAGE_KEY,\s*encodeCachedTheme\(/)
    expect(src).toMatch(/decodeCachedTheme\(raw\)/)
  })

  it('4 个风格 × 4 个模块都有专属布局规则，且同模块四风格互不雷同', () => {
    const wxss = readFileSync(APP_WXSS, 'utf-8')
    // 只取「行首 + 同行带 {」的规则：逗号续行的选择器不参与，避免把注释一起吞进来
    const rules = Array.from(wxss.matchAll(/^\.wc-style-([a-z0-9_-]+)\s+([^{}\n]+)\{([^{}]*)\}/gm))
    const hooks = ['.emp-card', '.mp-profile', '.mp-achievements', '.mp-services']
    for (const style of STYLE_IDS) {
      for (const hook of hooks) {
        expect(
          rules.some((m) => m[1] === style && m[2].includes(hook)),
          `${style} 缺 ${hook} 布局规则`
        ).toBe(true)
      }
    }
    // 只查「选择器存在」会假绿：四个风格复制粘贴同一套度量，客户等于没换风格。
    // 逐模块比较每个风格的全部规则体，四份必须两两不同。
    for (const hook of hooks) {
      const bodies = STYLE_IDS.map((style) =>
        rules.filter((m) => m[1] === style && m[2].includes(hook))
          .map((m) => m[3].trim()).join('|')
      )
      expect(bodies.every((b) => b.length > 0), `${hook} 有风格没规则`).toBe(true)
      expect(new Set(bodies).size, `${hook} 的四风格布局雷同（等于没换风格）`).toBe(STYLE_IDS.length)
    }
    const wxml = readFileSync(resolve(MP, 'pages/index/index.wxml'), 'utf-8')
    for (const hook of ['mp-card-area', 'mp-profile', 'mp-achievements', 'mp-services']) {
      expect(wxml, `index.wxml 缺模块钩子 ${hook}`).toContain(hook)
    }
    // 布局规则必须锁在模块钩子内，不能写成全局 .section / .intro / .sec-title
    for (const m of rules) {
      expect(m[2], `${m[1]} 的布局规则越界`)
        .toMatch(/\.emp-card|\.mp-profile|\.mp-achievements|\.mp-services/)
    }
  })

  // 上一条只查「四风格同模块规则体两两不同」，这是个会漏的守门：
  // 只要把 padding 从 24rpx 改成 30rpx、头像从 112rpx 改成 136rpx，规则体就已经不同了，
  // 断言全绿——可客户看到的仍然是同一种版式，只是元素大了一圈。
  // 2026-11-12 用户就是照这个假绿打回来的：「印刷的名片也有很多种布局样式啊，
  // 为什么你就没有更多灵活性的考虑呢」。所以这里改查**结构指纹**：
  // 名片区看 .ec-head / .ec-contact 的 flex 方向与卡片色块，公司简介区看
  // .mp-profile 与 .pf-text 的 display、封面分栏宽度、对齐方式。这些字段一变，
  // 版式就真的换了族；只改尺寸则纹丝不动。
  it('名片区与公司简介区是版式级差异：结构指纹四款互不相同，且不是只改了尺寸', () => {
    const rules = Array.from(
      readFileSync(APP_WXSS, 'utf-8')
        .matchAll(/^\.wc-style-([a-z0-9_-]+)\s+([^{}\n]+)\{([^{}]*)\}/gm)
    )
    // 选择器 → { 属性: 值 }，选择器去掉 .wc-style-<id> 前缀，逗号续行的拆开逐条记
    const declOf = (style: string, selector: string) => {
      const decl: Record<string, string> = {}
      for (const m of rules) {
        if (m[1] !== style) continue
        const hit = m[2]
          .split(',')
          .map((s) => s.trim().replace(/^\.wc-style-[a-z0-9_-]+\s+/, ''))
          .includes(selector)
        if (!hit) continue
        for (const part of m[3].split(';')) {
          const i = part.indexOf(':')
          if (i > 0) decl[part.slice(0, i).trim()] = part.slice(i + 1).trim()
        }
      }
      return (prop: string, fallback: string) => decl[prop] ?? fallback
    }

    // 默认值取 app.wxss 基线（.ec-head/.ec-contact 是 row、.emp-card 无左边框、
    // .ec-avatar 是 50% 圆形、.mp-profile/.pf-text 是普通块级）
    const card = (style: string) => {
      const d = declOf(style, '.emp-card')
      const head = declOf(style, '.emp-card .ec-head')
      const contact = declOf(style, '.emp-card .ec-contact')
      const avatar = declOf(style, '.emp-card .ec-avatar')
      return {
        head: head('flex-direction', 'row'),
        contact: contact('flex-direction', 'row'),
        spine: d('border-left', 'none'),
        avatar: avatar('border-radius', '50%'),
      }
    }
    const profile = (style: string) => {
      const box = declOf(style, '.mp-profile')
      const text = declOf(style, '.mp-profile .pf-text')
      const video = declOf(style, '.mp-profile .company-video')
      return {
        display: box('display', 'block'),
        align: box('text-align', 'left'),
        textDisplay: text('display', 'block'),
        videoBasis: video('flex', 'auto'),
      }
    }

    // 逐款钉住版式族，改回「只调尺寸」必红
    expect(card('default'), 'default 必须是横版左图右文 + 两列联系表').toEqual({
      head: 'row', contact: 'row', spine: 'none', avatar: '50%',
    })
    expect(card('ink').contact, 'ink 的联系表要真的变成单列四行').toBe('column')
    expect(card('ink').head, 'ink 仍保持头像在左，靠单列联系表与行间细线区分').toBe('row')
    expect(card('warm').head, 'warm 要竖版居中：头部改纵向').toBe('column')
    expect(card('warm').contact, 'warm 的联系表也要单列居中').toBe('column')
    expect(card('teal').head, 'teal 要头像在右：头部 row-reverse').toBe('row-reverse')
    expect(card('teal').contact, 'teal 保留两列表格式').toBe('row')
    expect(card('teal').spine, 'teal 要有左缘通高色块').toContain('solid')

    expect(profile('default').display, 'default 简介区必须是上图下文').toBe('block')
    expect(profile('ink').textDisplay, 'ink 简介区要拆成左右两栏').toBe('grid')
    expect(profile('teal').display, 'teal 简介区要左图右文').toBe('flex')
    expect(profile('teal').videoBasis, 'teal 封面要定宽成左栏').toContain('42%')
    expect(profile('warm').align, 'warm 简介区要居中').toBe('center')

    // 指纹整体两两不同：以后谁把某一款改回「和别款只是尺寸差」会在这里变红
    for (const [name, fp] of [
      ['名片区', card],
      ['简介区', profile],
    ] as const) {
      const prints = STYLE_IDS.map((s) => JSON.stringify(fp(s)))
      expect(new Set(prints).size, `${name} 的四款结构指纹有重复（等于只换了尺寸）`)
        .toBe(STYLE_IDS.length)
    }

    // 结构重排不得靠隐藏元素实现：公司视频、任一联系列被 display:none 就是静默失败
    for (const m of rules) {
      expect(m[3], `${m[1]} 用 display:none 藏内容`).not.toMatch(/display\s*:\s*none/)
    }
    // CSS 里的 .pf-text 必须真的存在于页面结构，否则版式规则是死的
    const wxml = readFileSync(resolve(MP, 'pages/index/index.wxml'), 'utf-8')
    expect(wxml, 'index.wxml 缺 .pf-text 分组容器，ink/teal 的简介版式落不下去')
      .toContain('class="pf-text"')
  })

  it('服务卡标题烧在图内：布局规则不得改 .svc-icon 尺寸或 aspect-ratio', () => {
    const wxss = readFileSync(APP_WXSS, 'utf-8')
    const svc = Array.from(
      wxss.matchAll(/^\.wc-style-[a-z0-9_-]+\s+[^{}\n]*\.svc-(?:icon|icon-ph)[^{}\n]*\{([^{}]*)\}/gm)
    )
    expect(svc.length, '取不到服务卡布局规则').toBeGreaterThan(0)
    for (const m of svc) {
      expect(m[1], '改图片尺寸 / 比例会让烧在图内的服务标题静默看不清')
        .not.toMatch(/(?:^|;)\s*(?:width|height|aspect-ratio|max-height)\s*:/)
    }
    const wxml = readFileSync(resolve(MP, 'components/service-grid/index.wxml'), 'utf-8')
    expect(wxml).toContain('标题已绘制在图内')
  })

  it('currentThemeClass 从两轴缓存恢复 36 组合；旧扁平缓存 / 脏值 / 存储不可用都不抛错', () => {
    vi.stubGlobal('getCurrentPages', () => [])
    const cached = (v: string | (() => string)) => {
      const getter = typeof v === 'function' ? v : () => v
      vi.stubGlobal('wx', { getStorageSync: getter, setStorageSync: vi.fn() })
    }

    cached('wc2:ink|brand-ocean')
    expect(currentThemeClass()).toBe('wc-style-ink wc-brand-ocean')
    cached('wc2:warm|-')
    expect(currentThemeClass()).toBe('wc-style-warm')
    cached('wc2:default|-')
    expect(currentThemeClass()).toBe('wc-style-default')

    // 旧缓存是扁平 12 款：原样按 splitTheme 拆，老用户升级零迁移
    cached('ink')
    expect(currentThemeClass()).toBe('wc-style-ink')
    cached('brand-ocean')
    expect(currentThemeClass()).toBe('wc-style-default wc-brand-ocean')
    cached('')
    expect(currentThemeClass()).toBe('wc-style-default')

    // 脏缓存：未知风格 / 未知色系各自回落，不能拼出白名单外的 class
    cached('wc2:neon|brand-neon')
    expect(currentThemeClass()).toBe('wc-style-default')
    cached('wc2:teal|brand-magenta')
    expect(currentThemeClass()).toBe('wc-style-teal wc-brand-magenta')

    cached(() => {
      throw new Error('storage disabled')
    })
    expect(currentThemeClass()).toBe('wc-style-default')
  })

  it('applyTheme 写两轴缓存并广播两层 class；bindTheme 把当前主题写回本页', () => {
    const setStorageSync = vi.fn()
    const pageA = { setData: vi.fn() }
    const pageB = { setData: vi.fn() }
    vi.stubGlobal('wx', { getStorageSync: () => 'wc2:warm|brand-amber', setStorageSync })
    vi.stubGlobal('getCurrentPages', () => [pageA, pageB])

    applyTheme('ink', null)
    expect(setStorageSync).toHaveBeenCalledWith('wc_theme_id', 'wc2:ink|-')
    expect(pageA.setData).toHaveBeenCalledWith({ themeClass: 'wc-style-ink' })
    expect(pageB.setData).toHaveBeenCalledWith({ themeClass: 'wc-style-ink' })

    applyTheme('ink', 'brand-ocean')
    expect(setStorageSync).toHaveBeenCalledWith('wc_theme_id', 'wc2:ink|brand-ocean')
    expect(pageA.setData).toHaveBeenCalledWith({ themeClass: 'wc-style-ink wc-brand-ocean' })

    applyTheme('default', 'brand-crimson')
    expect(setStorageSync).toHaveBeenCalledWith('wc_theme_id', 'wc2:default|brand-crimson')
    expect(pageA.setData).toHaveBeenCalledWith({ themeClass: 'wc-style-default wc-brand-crimson' })

    const page = { setData: vi.fn() }
    bindTheme(page)
    expect(page.setData).toHaveBeenCalledWith({ themeClass: 'wc-style-warm wc-brand-amber' })
  })
})

// ===== P5：两轴全叉乘 + 缓存升级 =====
//
// 树形归属时期只有 12 个合法组合，缓存存扁平 templateId 就够了。P5 放开成
// 4 风格 × 9 色档 = 36 个组合后，扁平列不可逆地丢掉风格：访客明明选了
// 「紧凑信息 + 绛红」，下次冷启动却按「标准均衡 + 绛红」渲染，后台还显示保存成功。
// 这类静默失败只能靠「组合矩阵 + 缓存往返」的穷举断言钉住。
const COMBOS: { styleId: string; brandColorId: string | null }[] = []
for (const styleId of STYLE_IDS) {
  COMBOS.push({ styleId, brandColorId: null })
  for (const brandColorId of BRAND_IDS) COMBOS.push({ styleId, brandColorId })
}

describe('P5 两轴全叉乘与两轴缓存', () => {
  it('4 风格 × 9 色档 = 36 个组合，每组的 class 唯一且风格层恒挂', () => {
    expect(COMBOS).toHaveLength(36)
    const classes = COMBOS.map((c) => themeClass(c.styleId, c.brandColorId))
    expect(new Set(classes).size, '有组合被压扁成同一套 class').toBe(COMBOS.length)
    for (const c of COMBOS) {
      const cls = themeClass(c.styleId, c.brandColorId)
      expect(cls, `${c.styleId}+${c.brandColorId} 丢了风格层`).toContain(`wc-style-${c.styleId}`)
      if (c.brandColorId) {
        expect(cls).toContain(`wc-brand-${c.brandColorId.slice(BRAND_PREFIX.length)}`)
      } else {
        expect(cls).not.toContain('wc-brand-')
      }
    }
    // 关键回归：同一色系在四个风格上只换布局层，颜色层逐字一致
    const ocean = STYLE_IDS.map((s) => themeClass(s, 'brand-ocean'))
    expect(new Set(ocean.map((c) => c.split(' ')[0])).size).toBe(STYLE_IDS.length)
    expect(new Set(ocean.map((c) => c.split(' ')[1])).size, '统一色板不该按风格分叉').toBe(1)
  })

  it('缓存串覆盖 36 组合且编解码幂等；扁平 12 款旧缓存零迁移', () => {
    const cache = COMBOS.map((c) => encodeCachedTheme(c.styleId, c.brandColorId))
    expect(new Set(cache).size, '有组合被写成同一个缓存值（风格丢了）').toBe(COMBOS.length)
    for (let i = 0; i < COMBOS.length; i++) {
      expect(decodeCachedTheme(cache[i])).toEqual(COMBOS[i])
    }
    // 旧扁平值不带前缀，仍按旧语义解析；12 个白名单 id 一个都不能失守
    for (const id of THEME_IDS) {
      expect(decodeCachedTheme(id)).toEqual(splitTheme(id))
      expect(id.startsWith('wc2:'), '扁平 id 不能和 P5 前缀撞车').toBe(false)
    }
    expect(encodeCachedTheme('neon', 'brand-neon')).toBe('wc2:default|-')
    expect(decodeCachedTheme('wc2:ink|brand-nope')).toEqual({ styleId: 'ink', brandColorId: null })
  })

  it('theme.ts 不再把两轴压扁：缓存格式由常量定义，applyTheme 只有一个写入口', () => {
    const src = readFileSync(resolve(MP, 'utils/theme.ts'), 'utf-8')
    expect(src).toContain("const CACHE_PREFIX = 'wc2:'")
    expect(src, '缓存 key 换了会让存量用户主题失效').toContain("const STORAGE_KEY = 'wc_theme_id'")
    expect(src.split('setStorageSync(').length - 1, '写入口必须只有一个').toBe(1)
    expect(src, '不能把兼容列 templateId 当缓存真值').not.toContain('deriveThemeId')
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

  it('后台按服务端 series 分两步渲染，不自己猜 id 前缀；固定预览区随两轴组合切换', () => {
    const vue = readFileSync(resolve(REPO, 'frontend/src/views/content/MpTheme.vue'), 'utf-8')
    expect(vue).toContain("t.series === 'brand'")
    expect(vue).toContain('brandTemplates')
    expect(vue).toContain('styleTemplates')
    expect(vue).toContain('品牌配色')
    expect(vue).not.toContain("startsWith('brand-')")
    // P5：整页只有一个固定预览区（sticky），两步选择先风格后色系；
    // 保存必须发两轴而不是旧的单列 templateId，否则两轴化等于没落后台。
    expect(vue).toContain('class="preview-pane"')
    expect(vue).toContain('position: sticky')
    expect(vue).toContain(':src="thumbUrl(currentId)"')
    expect(vue).toContain('const currentId = computed(() => comboId(styleId.value, brandColorId.value))')
    expect(vue).toContain('function comboId(s: string, b: string | null): string')
    expect(vue).toContain('v-for="t in styleTemplates"')
    expect(vue).toContain('v-for="t in brandTemplates"')
    expect(vue).toContain('第 1 步')
    expect(vue).toContain('第 2 步')
    expect(vue).toContain('36 种组合')
    expect(vue).toContain(
      'saveHomeLayout({ styleId: styleId.value, brandColorId: brandColorId.value })'
    )
    expect(vue, '不能退回只发单列 templateId').not.toContain('saveHomeLayout({ templateId:')
    // P5 树形归属已废止：切风格不清空色系、非 default 不禁用色卡、不留旧提示。
    // 任何一条退回，客户都会「后台保存成功、真机却少了刚对齐的 VI 主色」。
    expect(vue, '不能退回切风格清空色系').not.toContain('if (t.id !== DEFAULT_STYLE) brandColorId.value = null')
    expect(vue, '不能退回非 default 禁用色卡').not.toContain('if (styleId.value !== DEFAULT_STYLE) return')
    expect(vue, '不能退回按风格置灰色卡').not.toContain('disabled: styleId !== DEFAULT_STYLE')
    expect(vue, '不能退回树形提示').not.toContain('暂无可用品牌配色')
    // 后端直接下发两轴，前端 load 原样读取两轴；不再有「旧后端只发单列 id 按前缀拆」的分支
    // （templateId 兼容列已在 0043 删除，端上旧缓存经小程序 splitTheme 零迁移读取，与前端无关）。
    expect(vue).toContain('d?.styleId')
    expect(vue).toContain('d?.brandColorId')
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

/**
 * 把 app.wxss 的两层块合并成 12 组完整 token，供可读性 / 预览色 / 版式守门复用。
 * - default 直接取 page；ink/warm/teal 取 .wc-style-*（全套 18 个）。
 * - brand-* 取 .wc-brand-* 的 11 个颜色，再补 default 的 7 个版式。
 * 合并口径必须与 scripts/gen_theme_previews.py 的 read_tokens() 逐字一致，
 * 否则「预览图守门绿着、真机却是另一套色」又是没人报错的静默失败。
 */
function readThemeVars(): Record<string, ThemeVars> {
  const src = readFileSync(APP_WXSS, 'utf-8')
  const parse = (block: string): ThemeVars =>
    Object.fromEntries(
      Array.from(block.matchAll(/(--wc-[\w-]+)\s*:\s*([^;]+);/g)).map((m) => [m[1], m[2].trim()])
    )
  const def = src.match(/page\s*\{([^{}]*--wc-primary[^{}]*)\}/)
  if (!def) throw new Error('app.wxss 找不到 page 上的默认主题变量块')
  const base = parse(def[1])
  const layoutTokens = Object.fromEntries(
    Object.entries(base).filter(([k]) => k.startsWith('--wc-r-') || k.startsWith('--wc-card-shadow'))
  )
  const out: Record<string, ThemeVars> = { [THEME_DEFAULT]: base }
  for (const m of src.matchAll(/\.wc-style-([a-z0-9_-]+)\s*\{([^{}]*)\}/g)) {
    out[m[1]] = parse(m[2])
  }
  for (const m of src.matchAll(/\.wc-brand-([a-z0-9_-]+)\s*\{([^{}]*)\}/g)) {
    out['brand-' + m[1]] = { ...layoutTokens, ...parse(m[2]) }
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
  // P2 拆层后这条从「人工自觉」升级为结构约束：颜色层块里根本不许出现版式 token，
  // 谁想给品牌款顺手改个圆角都会变红；合并后的版式仍逐字等于 default。
  it('品牌配色颜色层不含任何版式 token，12 组基础色板里品牌款版式逐字等于 default', () => {
    const src = readFileSync(APP_WXSS, 'utf-8')
    const brandBlocks = Array.from(src.matchAll(/\.wc-brand-([a-z0-9_-]+)\s*\{([\s\S]*?)\n\}/g))
    expect(brandBlocks).toHaveLength(BRAND_IDS.length)
    for (const m of brandBlocks) {
      for (const key of LAYOUT_TOKENS) {
        expect(m[2], `${m[1]} 的颜色层私自写了 ${key}`).not.toContain(key)
      }
    }
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

  it('后台固定预览区的降级示意为每款整体风格高级款画出版式差异（加款忘了补示意会变红）', () => {
    const src = readFileSync(resolve(REPO, 'frontend/src/views/content/MpTheme.vue'), 'utf-8')
    // P4：预览区从「每张卡一个」变成「整页一个」，class 跟着当前风格切，
    // 不再是遍历模板时的 t.id。真实 PNG 本身已按风格出图，这里只服务图降级兜底。
    expect(src).toContain(`:class="'layout-' + styleId"`)
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

/** P5 组合键：<styleId>--<brandColorId 或 default>，两轴都进文件名，不压扁成 12 个扁平 id。 */
const COMBO_SEP = '--'
const NO_BRAND = 'default'
function comboId(styleId: string, brandColorId: string | null): string {
  return `${styleId}${COMBO_SEP}${brandColorId || NO_BRAND}`
}
function styleOfCombo(cid: string): string {
  return cid.split(COMBO_SEP)[0]
}
/** 从 Go 白名单复算 4 风格 × (默认色 + 8 色系) = 36 个组合映射。 */
function expectedCombos(): Record<string, { styleId: string; brandColorId: string | null }> {
  const ids = goTemplateIds()
  const styles = ids.filter((i) => !i.startsWith(BRAND_PREFIX))
  const brands = ids.filter((i) => i.startsWith(BRAND_PREFIX))
  const out: Record<string, { styleId: string; brandColorId: string | null }> = {}
  for (const s of styles) {
    out[comboId(s, null)] = { styleId: s, brandColorId: null }
    for (const b of brands) out[comboId(s, b)] = { styleId: s, brandColorId: b }
  }
  return out
}
/** 按生成脚本 combo_tokens() 同口径复算：风格全套 18 + 色系 11 颜色，版式 token 不被覆盖。 */
function comboTokens(styleId: string, brandColorId: string | null): ThemeVars {
  const all = readThemeVars()
  const merged: ThemeVars = { ...all[styleId] }
  if (brandColorId) {
    for (const [k, v] of Object.entries(all[brandColorId])) {
      if (!k.startsWith('--wc-r-') && !k.startsWith('--wc-card-shadow')) merged[k] = v
    }
  }
  return merged
}

/**
 * 按生成脚本 read_layout_rules() 的同一口径，从 app.wxss 复算 4 套布局规则快照。
 * 用于和 manifest.layouts 逐字对账：改了 WXSS 布局却没重出图，图片会停在旧版式，
 * 而「文件都在 / token 没变 / 尺寸都对」这三类检查全都发现不了。
 */
function expectedLayoutRules(): Record<string, string> {
  const src = readFileSync(APP_WXSS, 'utf-8')
  const out: Record<string, string[]> = {}
  for (const sid of STYLE_IDS) out[sid] = []
  const toPx = (s: string) =>
    s.replace(/(\d+(?:\.\d+)?)rpx/g, (_, n: string) => `${Number(n) * 0.5}px`)
  const re = /^\.wc-style-([a-z0-9_-]+)\s+([^{}]+?)\s*\{([^{}]*)\}/gm
  for (const m of src.matchAll(re)) {
    const sid = m[1]
    if (!(sid in out)) throw new Error(`app.wxss 出现未登记的风格布局规则：${sid}`)
    const selector = m[2].replace(/\.wc-style-[a-z0-9_-]+\s+/g, '').trim()
    const body = toPx(m[3]).trim()
    out[sid].push(`${selector} { ${body} }`)
  }
  return Object.fromEntries(Object.entries(out).map(([sid, rules]) => [sid, rules.join('\n  ')]))
}

function goTemplateIds(): string[] {
  const src = readFileSync(GO_TPL, 'utf-8')
  const def = /DefaultID\s*=\s*"([a-z0-9_-]+)"/.exec(src)?.[1]
  if (!def) throw new Error('mptemplate.go 找不到 DefaultID')
  return Array.from(src.matchAll(/ID:\s*(DefaultID|"([a-z0-9_-]+)")/g)).map((m) => m[2] || def)
}

describe('小程序整体风格模板 真实预览图', () => {
  it('36 个组合各有一张合法 PNG，按所属风格画布高度一致，没有旧扁平 id 残留', () => {
    const ids = goTemplateIds()
    expect(ids.length).toBeGreaterThanOrEqual(2)
    expect([...THEME_IDS].sort()).toEqual([...ids].sort(), '小程序 class 白名单与 Go 白名单漂移')
    const combos = expectedCombos()
    expect(Object.keys(combos), '组合数必须是 4 × 9 = 36').toHaveLength(36)
    const files = readdirSync(PREVIEW_DIR)
      // 目录里同目录还放着一套 460px 缩略图（`<cid>.thumb.png`），它们的 stem 会多出
      // `.thumb` 后缀，不排除掉就会把「36 张」比成 72 张而误报。
      .filter((f) => f.endsWith('.png') && !f.endsWith('.thumb.png'))
      .map((f) => f.slice(0, -4))
    expect(files.sort()).toEqual(Object.keys(combos).sort(), '加款 / 改组合忘了重跑 gen_theme_previews.py')

    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)
    const scale = /SCALE\s*=\s*(\d+)/.exec(script)
    expect(dim, '生成脚本缺画布尺寸').not.toBeNull()
    expect(scale, '生成脚本缺 SCALE').not.toBeNull()
    // 长图按风格内容高度各自收口，同风格的 9 档颜色共用该风格画布。
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const heights = manifest.canvasByStyle as Record<string, number>
    expect(Object.keys(heights).sort()).toEqual([...STYLE_IDS].sort())

    for (const [cid, c] of Object.entries(combos)) {
      const buf = readFileSync(join(PREVIEW_DIR, `${cid}.png`))
      const h = heights[c.styleId]
      expect(h, `${cid} 所属风格没有画布高度`).toBeGreaterThan(0)
      const expected = [Number(dim![1]) * Number(scale![1]), h * Number(scale![1])]
      expect(buf.subarray(0, 8).toString('hex'), `${cid} 不是 PNG`).toBe('89504e470d0a1a0a')
      expect([buf.readUInt32BE(16), buf.readUInt32BE(20)], `${cid} 尺寸`).toEqual(expected)
      expect(buf.length, `${cid} 疑似空白图`).toBeGreaterThan(5000)
    }
  })

  // 「每张图都在、尺寸都对、快照也对」仍挡不住一种静默失败：只用一套布局或只用
  // 一套颜色出 36 张图，客户看到的是一堆重复预览。36 张摘要互不相同，
  // 再加「同风格 9 档互不相同」「同色档 4 风格互不相同」两层分组，才钉得住两轴。
  it('36 张预览图两两不同：布局与颜色都真的进了图', () => {    const combos = expectedCombos()
    const digests: Record<string, string> = {}
    for (const cid of Object.keys(combos)) {
      digests[cid] = createHash('sha256')
        .update(readFileSync(join(PREVIEW_DIR, `${cid}.png`))).digest('hex')
    }
    expect(new Set(Object.values(digests)).size, '有重复预览图：某一维没进图')
      .toBe(Object.keys(combos).length)
    for (const sid of STYLE_IDS) {
      const group = Object.keys(combos).filter((cid) => combos[cid].styleId === sid)
      expect(group).toHaveLength(9)
      expect(new Set(group.map((cid) => digests[cid])).size, `${sid} 的 9 档颜色没进图`)
        .toBe(group.length)
    }
    for (const bid of [null, ...BRAND_IDS]) {
      const group = Object.keys(combos).filter((cid) => combos[cid].brandColorId === bid)
      expect(group).toHaveLength(STYLE_IDS.length)
      expect(new Set(group.map((cid) => digests[cid])).size, `${bid} 的 4 风格布局没进图`)
        .toBe(group.length)
    }
  })

  // 后台预览窗口只有 230px 宽，原图却是 750px 宽、单张 0.8~1.8MB（warm 款 5144 高）。
  // 直接喂原图的代价有两个且都不会报错：① 切一次风格就拉 1~2MB（换风格就换图，
  // 而 36 组原图共约 40MB）；② 750×5144 位图解码约 15MB 内存，老机器切风格明显卡。
  // 所以每个组合出两档：`.thumb.png`（460px / 100~210KB）给列表窗口，
  // `.png` 只在「查看大图」浮层里按需加载。下面四段缺任何一段，优化都会悄悄失效：
  // 前端指错档位（无运行时症状）、缩略图没生成（退回拉原图）、缩略图没真缩（等于没做）。
  it('预览图分「缩略图 / 大图」两档：列表用缩略图，大图浮层才用原图', () => {
    // ① 前端取图必须分成两个函数，且各自指对档位
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue, '列表窗口必须用缩略图').toMatch(/class="preview-shot"[\s\S]{0,200}:src="thumbUrl\(/)
    expect(vue, '「查看大图」必须用原图').toMatch(/class="zoom-shot"[^>]*:src="fullUrl\(/)
    expect(vue, '列表图不能指到大图（那样等于没做优化）')
      .not.toMatch(/class="preview-shot"[\s\S]{0,200}:src="fullUrl\(/)

    // ② 规格只有一处来源：manifest 与脚本常量必须一致
    const combos = expectedCombos()
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const thumbWidth = manifest.thumbWidth as number
    expect(thumbWidth, 'manifest 缺 thumbWidth：改了规格却没重出图').toBe(460)
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toMatch(new RegExp(`THUMB_WIDTH\\s*=\\s*${thumbWidth}`))
    // 找不到 ImageMagick 必须直接失败，不能静默跳过缩略图
    expect(script, '缺 ImageMagick 时要显式报错，不能跳过缩略图')
      .toMatch(/raise SystemExit\(\s*"✗ 找不到 ImageMagick/)

    // ③ 36 张缩略图必须真的存在，尺寸按原图等比、体积真的更小
    for (const cid of Object.keys(combos)) {
      const full = readFileSync(join(PREVIEW_DIR, `${cid}.png`))
      const thumbPath = join(PREVIEW_DIR, `${cid}.thumb.png`)
      expect(existsSync(thumbPath), `${cid} 缺缩略图：后台会退回拉整张原图`).toBe(true)
      const thumb = readFileSync(thumbPath)
      expect(thumb.subarray(0, 8).toString('hex'), `${cid}.thumb 不是 PNG`).toBe('89504e470d0a1a0a')
      const fw = full.readUInt32BE(16)
      const fh = full.readUInt32BE(20)
      const tw = thumb.readUInt32BE(16)
      const th = thumb.readUInt32BE(20)
      expect(tw, `${cid} 缩略图宽度`).toBe(thumbWidth)
      expect(Math.abs(th - Math.round((fh * thumbWidth) / fw)), `${cid} 缩略图没按比例缩`)
        .toBeLessThanOrEqual(1)
      expect(thumb.length, `${cid} 缩略图疑似空白`).toBeGreaterThan(2000)
      expect(thumb.length, `${cid} 缩略图没比原图小多少（等于没做优化）`)
        .toBeLessThan(full.length * 0.6)
    }
  })

  // 2026-11-12 线上真实事故：管理员发版后切风格，预览图「看不到任何变化」，
  // 第一反应是「发布文件不全」。逐张比对 36 张图的字节数与本地完全一致、入口 JS 的
  // 内容 hash 也一致——文件是全的，是被缓存住了：图片名不含内容 hash（`ink--brand-crimson.png`
  // 这种组合键），源站又没发 Cache-Control，Cloudflare 按静态图片默认给了 max-age=14400。
  //
  // 第一版修法是 nginx 显式 no-cache，能防旧图但把 CDN 废掉了（每次刷新都回源校验，
  // 40MB 图集白烧带宽）。现行方案：**破缓存靠 URL 上的 `?v=<构建戳>`，因此可以长缓存**。
  // 这条用例钉三件事，缺任何一件都会退回「要么看旧图、要么烧带宽」：
  //   ① 前端 URL 必须带构建戳（否则同名不同内容被缓存住）；
  //   ② nginx 必须按状态码分流（`$preview_cache`），200 才 immutable、404 是 no-store
  //      （写死 immutable 会把「图不存在」锁一年，/uploads/ 踩过同款坑）；
  //   ③ 缺图必须真 404（不得 `@spa` 兜底）。
  it('预览图不会被缓存成旧版式：URL 带构建戳 + 按状态码分流 + 缺图真 404', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue, 'previewUrl 必须拼 ?v=<构建戳>，否则同名不同内容会被缓存住')
      .toMatch(/theme-previews\/.*\.png\?v=\$\{__APP_BUILD__\}/)
    // define 的注入点也要在，否则构建时 __APP_BUILD__ 是未定义变量
    const vite = readFileSync(resolve(REPO, 'frontend/vite.config.ts'), 'utf-8')
    expect(vite).toContain('__APP_BUILD__')
    expect(readFileSync(resolve(REPO, 'frontend/env.d.ts'), 'utf-8')).toContain('__APP_BUILD__')

    // 三份都要钉：两份参考版 + 线上实际在用的 live.conf（后者才是发版时真正生效的）
    for (const rel of [
      'frontend/nginx.conf',
      'deploy/baota/nginx-site.conf',
      'deploy/baota/nginx-site.live.conf',
    ]) {
      const conf = readFileSync(resolve(REPO, rel), 'utf-8')
      const at = conf.indexOf('location ^~ /theme-previews/')
      expect(at, `${rel} 缺 /theme-previews/ 专用 location`).toBeGreaterThan(-1)
      const body = conf.slice(at, conf.indexOf('}', at))
      // 不能写死字符串：add_header 区分不了状态码，写死 immutable 会让 404 也被锁一年
      expect(body, `${rel} 的 Cache-Control 必须走按状态码分流的变量`)
        .toContain('add_header Cache-Control $preview_cache always')
      // 反向：不能退回 no-cache（那样 CDN 每次回源，等于没缓存）
      expect(body, `${rel} 不能退回写死的 no-cache（CDN 会每次回源）`)
        .not.toContain('Cache-Control "no-cache"')
      // map 必须真的存在且在 server 块之前（http 级），否则变量未定义 nginx 起不来
      const mapAt = conf.indexOf('map $status $preview_cache {')
      expect(mapAt, `${rel} 缺 $preview_cache 的 map`).toBeGreaterThan(-1)
      expect(mapAt, `${rel} 的 map 必须在 server 块之前（http 级）`).toBeLessThan(at)
      const mapBody = conf.slice(mapAt, conf.indexOf('}', mapAt))
      expect(mapBody, `${rel} 的 200 必须 immutable 一年`)
        .toMatch(/200\s+"public, max-age=31536000, immutable"/)
      expect(mapBody, `${rel} 的 404 必须 no-store（否则「图不存在」被锁一年）`)
        .toMatch(/404\s+"no-store"/)
      // `try_files $uri =404` 是把「缺图真 404」钉死，别理解反：本 location 不写它其实
      // 也是 404（nginx 直接去 root 找文件），不会隐式落到 location / 的 SPA 兜底。
      // 真正会出事的写法是 `try_files $uri @spa`——那时缺图返回 index.html + 200，
      // <img> 解码失败 → 静默降级成示意图，Network 里看不到任何红字。
      expect(body, `${rel} 缺图必须显式 404，不得写成 @spa 兜底`).toContain('try_files $uri =404')
    }
  })

  it('生成脚本从 app.wxss 取 token，不手画第二套主题色', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toContain('miniprogram/app.wxss')
    expect(script).toContain('read_tokens()')
    expect(script).toMatch(/TOKEN_RE\s*=\s*re\.compile/)
    // 两层解析：布局层给全套 token、颜色层只给颜色，合并出 12 组完整 token
    expect(script).toContain('STYLE_BLOCK_RE')
    expect(script).toContain('BRAND_BLOCK_RE')
    expect(script, '不能退回单层 .wc-theme-* 解析').not.toContain('THEME_BLOCK_RE')
    // 模板 id 与顺序必须以 Go 白名单为准，脚本里不能自己再列一份
    expect(script).toContain('mptemplate.go')

    const html = script.slice(script.indexOf('HTML = Template("""'), script.indexOf('def find_chrome'))
    expect(html.length, '取不到预览 HTML 模板').toBeGreaterThan(1000)
    for (const id of THEME_IDS) {
      const primary = colorOf(readThemeVars()[id], '--wc-primary')
      expect(html.toLowerCase(), `${id} 主色被硬编码进预览 HTML`).not.toContain(primary.toLowerCase())
    }
    expect(html.split('var(--wc-').length - 1, '预览 HTML 没有真正用 token').toBeGreaterThanOrEqual(15)

    // P4：布局也必须同源。模块节点用 app.wxss 的真实钩子类名，规则由 ${style_css}
    // 注入 WXSS 原文；模板里自己写死列宽就会和端上分叉成第二套版式。
    expect(script).toContain('STYLE_RULE_RE')
    expect(script).toContain('read_layout_rules()')
    expect(script).toContain('MODULE_HOOKS')
    // P5：两轴都不许压扁。组合键、36 组合并 token（版式只来自风格层）、组合映射
    // 必须由脚本显式算出；退回 style_of() / 扁平 templateId 会让 36 → 12 塌缩。
    expect(script, '不能退回「色系全归 default」的扁平映射').not.toContain('def style_of(')
    expect(script).toContain('def combo_id(')
    expect(script).toContain('def all_combos(')
    expect(script).toContain('def combo_tokens(')
    expect(script).toContain('is_layout_token(k)')
    expect(script).toContain('"combos": {')
    expect(script).toContain('"comboTokens": combo_tok')
    expect(script).toContain('combo_tok[cid]')
    expect(script).toContain('layout_css(')
    expect(script).toContain('${style_css}')
    expect(script).toContain('"layouts": layouts')
    for (const hook of ['emp-card', 'mp-profile', 'mp-achievements', 'mp-services']) {
      expect(html, `预览 HTML 缺模块钩子 .${hook}`).toContain(hook)
    }
    expect(html, '预览 HTML 自己写了一套列宽，等于和 app.wxss 分叉').not.toMatch(
      /width:\s*(?:20|25|33\.33|50)%/
    )
  })

  // 第二十八轮之后用户实测：后台预览每款背景色不同，但小程序首页背景是写死蓝渐变，
  // 切模板背景不变——预览与真机对不上。首页背景必须跟随主题 token（与预览一致），
  // 且「首页背景」图仍由 page-bg 整幅覆盖优先。改回写死蓝会变红。
  it('小程序首页背景跟随主题 token，与后台预览一致（改回写死蓝会变红）', () => {
    const wxss = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.wxss'), 'utf-8')
    const page = wxss.match(/\.page\s*\{[^}]*\}/)?.[0] || ''
    expect(page, '取不到 .page 规则').not.toBe('')
    expect(page, '首页背景没用主题 token，切模板不会变色').toContain('var(--wc-primary-soft)')
    expect(page, '首页背景没用主题 token，切模板不会变色').toContain('var(--wc-primary-tint)')
    expect(page, '首页背景仍是写死蓝，与后台预览不一致').not.toContain('#d5e7fa')
    // 后台预览 .phone 同样用这两个 token，两端一致
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toContain('var(--wc-primary-soft), var(--wc-primary-tint)')
  })

  it('后台固定预览区是手机窗口、内部滚动看完整长图（比例不对 / 退回长条会变红）', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)!
    const parts = /CONTENT_BOTTOM,\s*TAIL_GAP,\s*BAR_HEIGHT\s*=\s*(\d+),\s*(\d+),\s*(\d+)/.exec(script)!
    // P4：长图按风格收口，脚本里的 HEIGHT 是四风格中的最大画布上界；
    // 每张图自己的高度写在 manifest.canvasByStyle，由上面的逐张尺寸守门对账。
    expect(Number(dim[1])).toBe(375)
    expect(Number(dim[2]), '最大画布仍由内容底部 + 呼吸 + 底栏算出').toBe(
      Number(parts[1]) + Number(parts[2]) + Number(parts[3])
    )
    expect(Number(dim[2]), '退回旧的单一 1210 长图会裁掉暖金棕的内容').toBeGreaterThan(1210)
    // 固定在一个位置，而不是十几张卡片各带一个预览
    expect(vue).toContain('class="preview-pane"')
    expect(vue).toContain('position: sticky')
    // 窗口是手机竖屏比例：窄于长图、靠滚动看完整页
    const ratio = /aspect-ratio:\s*(\d+)\s*\/\s*(\d+)/.exec(vue)
    expect(ratio, '后台预览容器缺 aspect-ratio，图没加载完会跳版').not.toBeNull()
    const r = Number(ratio![2]) / Number(ratio![1])
    expect(r, '卡片应接近手机竖屏比例，而不是把整张长图压扁').toBeGreaterThanOrEqual(1.6)
    expect(r, '卡片高度不应超过约 2.3 个屏宽，否则又变回长条').toBeLessThanOrEqual(2.3)
    // 长图在窗口内可滚动，并按原始比例显示（cover 会裁掉下半部、压扁会变形）
    expect(vue).toContain('class="preview-scroll"')
    expect(vue).toContain('overflow-y: auto')
    expect(vue).toContain('class="preview-shot"')
    expect(vue, '长图必须按原始比例显示才能滚动，cover 会裁掉下半部').toContain('height: auto')
  })

  it('后台确实渲染真实图并保留按张降级的兜底（改回色块示意会变红）', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('class="preview-shot"')
    // P4：预览区只有一个，src 跟随当前 (styleId, brandColorId) 组合，
    // 不再是遍历模板卡片时的 t.id。
    expect(vue).toContain(':src="thumbUrl(currentId)"')
    expect(vue).toContain('theme-previews/')
    expect(vue).toContain('encodeURIComponent(id)')
    expect(vue).toContain('import.meta.env.BASE_URL')
    expect(vue).toContain('@error="onPreviewError(currentId)"')
    expect(vue).toContain('class="preview-fallback"')
    expect(vue).toContain('class="fallback-tag"')
    expect(vue).toContain('failedPreviews.value = {}')
  })

  // 只查「PNG 都在」挡不住「app.wxss 改了配色却没重出图」：文件一个不少，
  // 客户看到的仍是旧样式，而且没人会报错。manifest 是出图时写下的 token 快照。
  it('token / 布局快照与 app.wxss 同版（改了配色 / 圆角 / 模块排布没重出图会变红）', () => {
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    expect(manifest.source).toBe('miniprogram/app.wxss')
    const ids = goTemplateIds()
    expect(Object.keys(manifest.tokens).sort()).toEqual([...ids].sort())

    const current = readThemeVars()
    for (const id of ids) {
      expect(manifest.tokens[id], `${id} 的 token 快照`).toEqual(current[id])
    }
    // P5：36 组合的映射与合并 token 也必须有快照。只查 12 组基础色板会放行
    // 「换色系时把风格版式一起覆盖」：图片一张不少、尺寸也对，客户却看到
    // 紧凑信息的近直角悄悄变回默认圆角。
    const combos = expectedCombos()
    expect(manifest.combos, '组合映射与 Go 白名单漂移，请重跑生成脚本').toEqual(combos)
    expect(Object.keys(manifest.comboTokens).sort()).toEqual(Object.keys(combos).sort())
    for (const [cid, c] of Object.entries(combos)) {
      expect(manifest.comboTokens[cid], `${cid} 的合并 token`).toEqual(
        comboTokens(c.styleId, c.brandColorId)
      )
      for (const key of LAYOUT_TOKENS) {
        expect(manifest.comboTokens[cid][key], `${cid} 的 ${key} 不该被色系覆盖`)
          .toBe(current[c.styleId][key])
      }
    }
    // 布局规则同样要有快照：只查 token 会放行「app.wxss 改了列数、预览图却还是旧版式」。
    // 图片一张不少、尺寸也对，客户却看到错的布局。
    expect(manifest.layouts, '布局快照与 app.wxss 当前规则漂移，请重跑生成脚本').toEqual(
      expectedLayoutRules()
    )
    expect(Object.keys(manifest.layouts).sort()).toEqual([...STYLE_IDS].sort())
    expect(new Set(Object.values(manifest.layouts)).size, '四风格复制同一套布局 = 没换风格')
      .toBe(STYLE_IDS.length)
    expect(Object.keys(manifest.canvasByStyle).sort()).toEqual([...STYLE_IDS].sort())
    expect(Object.keys(manifest.contentBottoms).sort()).toEqual([...STYLE_IDS].sort())

    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const dim = /WIDTH,\s*HEIGHT\s*=\s*(\d+),\s*(\d+)/.exec(script)!
    const scale = /SCALE\s*=\s*(\d+)/.exec(script)!
    const parts = /CONTENT_BOTTOM,\s*TAIL_GAP,\s*BAR_HEIGHT\s*=\s*(\d+),\s*(\d+),\s*(\d+)/.exec(script)!
    const [, gap, bar] = parts.slice(1).map(Number)
    expect(manifest.canvas).toEqual([Number(dim[1]), Number(dim[2])])
    expect(manifest.scale).toBe(Number(scale[1]))
    for (const sid of STYLE_IDS) {
      expect(manifest.canvasByStyle[sid], `${sid} 画布应由内容底部收口`).toBe(
        manifest.contentBottoms[sid] + gap + bar
      )
      expect(manifest.canvasByStyle[sid], `${sid} 不得超过最大画布`).toBeLessThanOrEqual(Number(dim[2]))
    }
  })

  it('两层读出的 12 组基础色板逐字等于预览快照（拆层零视觉突变）', () => {
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const merged = readThemeVars()
    expect(Object.keys(merged).sort(), '组合集合漂移').toEqual(Object.keys(manifest.tokens).sort())
    expect(merged, '合并口径与生成脚本漂移，或拆层时改了某个色值').toEqual(manifest.tokens)
  })
})

// ===== P5：36 组合预览图 + 后台两步 UI =====
//
// 最容易静默失败的一处：app.wxss 改了列数 / 封面高度，36 张 PNG 一张不少、
// token 快照也没变、尺寸也对，客户看到的却是旧版式。布局必须留快照并与 WXSS
// 对账；后台也必须真的按 (styleId, brandColorId) 组合键寻图、按两轴保存，
// 且 4 个风格下 9 档颜色全部可选。
describe('P5 36 组合预览图与后台两步选择', () => {
  it('36 张图按 4 风格分组：每风格 9 档颜色共用同一画布，四风格高度互不相同', () => {
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    const scale = Number(/SCALE\s*=\s*(\d+)/.exec(script)![1])
    const combos = expectedCombos()
    const grouped: Record<string, string[]> = {}
    for (const [cid, c] of Object.entries(combos)) {
      ;(grouped[c.styleId] ||= []).push(cid)
    }
    expect(Object.keys(grouped).sort()).toEqual([...STYLE_IDS].sort())
    // P5 全叉乘：四个风格**各**挂 1 款默认色 + 8 款品牌色，不再只有 default 有 9 张
    for (const sid of STYLE_IDS) {
      expect(grouped[sid], `${sid} 没能叠加全部 9 档颜色`).toHaveLength(9)
      for (const cid of grouped[sid]) {
        expect(styleOfCombo(cid)).toBe(sid)
        const buf = readFileSync(join(PREVIEW_DIR, `${cid}.png`))
        expect(buf.readUInt32BE(20), `${cid} 未按所属风格收口`).toBe(
          manifest.canvasByStyle[sid] * scale
        )
      }
    }
    expect(new Set(Object.values(manifest.canvasByStyle)).size, '四风格布局没拉开差异').toBe(
      STYLE_IDS.length
    )
  })

  it('布局规则来自 app.wxss 原文，脚本按每个组合的风格注入并留快照', () => {
    const script = readFileSync(PREVIEW_SCRIPT, 'utf-8')
    expect(script).toContain('STYLE_RULE_RE')
    expect(script).toContain('read_layout_rules()')
    expect(script).toContain('layout_css(layout_rules[sid])')
    expect(script).toContain('for cid, (sid, _brand) in combos.items():')
    expect(script, '不能把所有组合都按 default 布局出图').not.toContain('style_of(')
    expect(script).toContain('data.get("layouts") != layouts')
    expect(script).toContain('data.get("comboTokens") != combo_tok')
    expect(script).toContain('for sid in STYLE_IDS:')
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    expect(manifest.layouts).toEqual(expectedLayoutRules())
    for (const sid of STYLE_IDS) {
      for (const hook of ['emp-card', 'mp-profile', 'mp-achievements', 'mp-services']) {
        expect(manifest.layouts[sid], `${sid} 缺 .${hook} 布局规则`).toContain(hook)
      }
    }
  })

  it('后台按组合键寻图与两轴保存，固定预览区不退回「每款一张卡」', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('class="preview-pane"')
    expect(vue).toContain(':src="thumbUrl(currentId)"')
    expect(vue).toContain('const currentId = computed(() => comboId(styleId.value, brandColorId.value))')
    expect(vue).toContain('@click="pickStyle(t)"')
    expect(vue).toContain('@click="pickBrand(t)"')
    expect(vue).toContain('@click="pickBrand(null)"')
    expect(vue, '不能退回每款模板一张带预览的卡片').not.toContain('class="theme-card"')
    expect(vue, '不能退回单列 templateId 保存').not.toContain('saveHomeLayout({ templateId:')
    expect(vue, '不能退回切风格清空色系').not.toContain('if (t.id !== DEFAULT_STYLE) brandColorId.value = null')
    expect(vue).toContain('第 1 步')
    expect(vue).toContain('第 2 步')
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
    // P5：出图时必须把该组合的合并 token 与所属风格都传进去，才能注入该风格的布局规则
    expect(script).toContain('build_html(combo_tok[cid],')
    expect(script).toContain('${tokens}')
    expect(script).toContain('${style_css}')
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
    // P4：暖金棕是 2 列分类 + 单列精选 + 3 列服务，内容比旧版单一布局长得多，
    // HEIGHT 现在是「四风格最大画布上界」，不能再卡在 1250；真正的留白由
    // 每个风格自己的画布高度控制（见下面的 canvasByStyle 对账）。
    expect(Number(dim![2]), '最大画布不该无边界地膨胀').toBeLessThanOrEqual(2600)
    expect(Number(dim![2])).toBe(content + gap + bar)
    expect(gap, '呼吸超过 24px 就会让按钮和服务卡重新割裂').toBeLessThanOrEqual(24)
    // CSS 必须吃同一组常量，不能脚本算一套、样式写死另一套 60px
    expect(script).toContain('padding-bottom: ${tail_gap}px')
    expect(script).toContain('height: ${bar_h}px')
    // 改了占位文案 / 布局列数，absolute 底栏会静默盖住服务卡：出图前必须逐风格实测
    expect(script).toContain('measure_content_bottom(chrome, probe_html)')
    expect(script).toContain('bottom + TAIL_GAP > bar_top')
    expect(script).toContain('for sid in STYLE_IDS:')
    expect(script).toContain('MEASURE_HEIGHT')
    expect(script).toContain('canvasByStyle')
    expect(script).toContain('contentBottoms')
    const manifest = JSON.parse(readFileSync(join(PREVIEW_DIR, 'manifest.json'), 'utf-8'))
    for (const sid of STYLE_IDS) {
      const bottom = manifest.contentBottoms[sid]
      expect(bottom, `${sid} 缺实测内容底部`).toBeGreaterThan(0)
      expect(bottom, `${sid} 内容已超出 CONTENT_BOTTOM 上界`).toBeLessThanOrEqual(content)
      expect(bottom + gap, `${sid} 内容已顶到底栏`).toBeLessThanOrEqual(content + gap)
      expect(manifest.canvasByStyle[sid], `${sid} 画布`).toBe(bottom + gap + bar)
      expect(manifest.canvasByStyle[sid], `${sid} 画布不得超过最大画布`).toBeLessThanOrEqual(Number(dim![2]))
    }
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

  it('后台「非真机截图」角标与查看大图默认隐藏、悬停预览图才显示（改回常驻会变红）', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    expect(vue).toContain('class="preview-badge"')
    expect(vue).toContain('风格示意 · 非真机截图')
    expect(vue).toContain('class="preview-zoom-btn"')
    // P4：固定预览区只有一个，查看大图打开的是当前组合那张图
    expect(vue).toContain('@click.stop="openZoom(currentId)"')
    expect(vue).toContain('class="preview-zoom"')
    expect(vue).toContain('class="zoom-shot"')
    // 750x2600 长图不能再用 max-height 压成一条细线：固定面板宽度 + 内部纵向滚动
    expect(vue).toContain('class="zoom-scroll"')
    expect(vue).toContain('overflow-y: auto')
    expect(vue).toContain('width: min(420px, 92vw)')
    expect(vue).toContain('height: auto')
    expect(vue, '说明文案必须写明非真机截图').toContain('首页风格示意，不是真机截图')
    // 默认隐藏、悬停才显示：角标/按钮不再常驻遮挡示意图与选色对比，改回常驻会变红
    expect(vue).toContain('.theme-preview:hover .preview-badge')
    expect(vue).toContain('.theme-preview:hover .preview-zoom-btn')
    expect(vue, '默认态必须隐藏').toContain('opacity: 0')
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
    // 头像 / LOGO 用 CSS 画占位，不能退回真实素材。P4 起头像同时带
    // .ec-avatar（供 app.wxss 的布局规则改尺寸），故只查类名本身。
    expect(html).toContain('avatar-ph')
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
    expect(script).toContain('check_sources() + check_no_identity(build_html(first))')
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

describe('小程序风格模板 固定预览区与组合说明区的视觉分隔', () => {
  // P4 把「每款一张卡 + 卡片下方名称区」换成「一个固定预览区 + 下方当前组合说明」，
  // 但视觉问题一模一样：长图底部是白色操作栏，紧贴的白底说明会和图内按钮连成一片。
  // 所以硬边界与浅灰底必须保留，只是落点从 .theme-meta 搬到 .pane-foot。
  it('预览图底部有硬边界、组合说明区用浅灰底，图内按钮不再和说明文字连成一片', () => {
    const vue = readFileSync(MP_THEME_VUE, 'utf-8')
    const preview = vue.match(/\.theme-preview\s*\{[^}]*\}/)?.[0] || ''
    const foot = vue.match(/\.pane-foot\s*\{[^}]*\}/)?.[0] || ''
    expect(preview, '取不到 .theme-preview 规则').not.toBe('')
    expect(foot, '取不到 .pane-foot 规则').not.toBe('')
    expect(preview, '预览区与说明区没有分界线').toContain('border-bottom:')
    expect(foot, '说明区仍是纯白底，会和图内白色操作栏混在一起').toContain('background:')
    expect(foot).toContain('#f8fafc')
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
