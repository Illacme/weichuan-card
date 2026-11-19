// 名片卡右上角 LOGO 的尺寸 / 位置档位（0053）跨层守门。
//
// 这一轮客户的原话是「传一张正方形的图会相对偏左，要能调大小和位置」。所以真正要
// 拦住的是三类静默失败：
//   1) **位置档形同虚设**：只给 image 换 justify-content 是没用的——aspectFit 会把
//      图缩到框内并居中，窄图的位置由「框宽」决定，不由对齐方式决定。必须按原始宽高比
//      把框收到内容宽度，位置才真的动得了。
//   2) **后台预览与真机不一致**：预览写的是「真实比例」（1rpx = 0.5px）。若只改
//      app.wxss 不改预览，客户按预览调完、到真机上是另一个大小，谁都不会报错。
//   3) **缺省档不等于改造前**：medium/center 必须是 150×44 + 居中，否则存量租户
//      升级当天 LOGO 就变了样。旧后端不下发这两列时也必须落到同一个缺省。
//
// 分两部分：组件行为用真实 observer 驱动（不是读源码猜），样式 / 预览 / 后端 / 契约
// 用源码静态断言。静态部分带量尺自检 fixture，免得正则写错了一条也抓不到却全绿。
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const WXML = readFileSync(resolve(REPO, 'miniprogram/components/employee-card/index.wxml'), 'utf-8')
const PAGE_TS = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.ts'), 'utf-8')
const PAGE_WXML = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.wxml'), 'utf-8')
const MODEL = readFileSync(resolve(REPO, 'backend/internal/model/models.go'), 'utf-8')
const LAYOUT_VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/HomeLayout.vue'), 'utf-8')
const OPENAPI = readFileSync(resolve(REPO, 'docs/openapi.yaml'), 'utf-8')

/** 取某个类规则块（单行）里的声明体 */
function ruleBody(src: string, selector: string): string | undefined {
  const m = src.match(new RegExp(selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\s*\\{([^}]*)\\}'))
  return m ? m[1] : undefined
}

/** 从声明体里取某个属性的数值（含单位） */
function decl(body: string | undefined, prop: string): string {
  if (!body) return ''
  const m = body.match(new RegExp('(?:^|;)\\s*' + prop + '\\s*:\\s*([^;]+)'))
  return m ? m[1].trim() : ''
}

// ---- 组件行为：用真实 Component 选项驱动 observer ----
let opts: any = null
vi.stubGlobal('Component', (o: any) => {
  opts = o
})
vi.stubGlobal('wx', {})

beforeAll(async () => {
  await import('../components/employee-card/index')
})

function newInstance() {
  const inst: any = {
    data: JSON.parse(JSON.stringify(opts.data || {})),
    setData(patch: any) {
      Object.assign(this.data, patch)
    },
  }
  for (const k of Object.keys(opts.methods || {})) inst[k] = opts.methods[k].bind(inst)
  return inst
}

/** 触发「档位或比例变了」的那个 observer（键名是两个字段的组合，别写死顺序） */
function fireSizeObserver(inst: any, size: string, ratio: number) {
  const key = Object.keys(opts.observers).find((k) => k.indexOf('logoRatio') >= 0)
  if (!key) throw new Error('找不到 logoRatio 的 observer，断言会空转')
  opts.observers[key].apply(inst, [size, ratio])
}

describe('名片 LOGO 档位（0053）', () => {
  it('量尺自检：ruleBody / decl 能解析，证明静态断言不是空转', () => {
    const fixture = '.emp-card .ec-logo-slot-small { width: 110rpx; height: 36rpx; }'
    expect(decl(ruleBody(fixture, '.emp-card .ec-logo-slot-small'), 'width')).toBe('110rpx')
    expect(ruleBody(fixture, '.not-present')).toBeUndefined()
    expect(decl(undefined, 'width')).toBe('')
  })

  // ---- 组件：位置档必须真的能挪，前提是把框收到内容宽度 ----
  it('方块 LOGO（1:1）：三档高度就是区块高度，宽度也是内容宽度', () => {
    const inst = newInstance()
    const cases: [string, number][] = [
      ['small', 36],
      ['medium', 44],
      ['large', 52],
    ]
    for (const [size, h] of cases) {
      fireSizeObserver(inst, size, 1)
      expect(inst.data.logoW, `${size} 档方块 LOGO 宽应等于高`).toBe(h)
    }
  })

  it('横版长条（超过区块宽）：宽度夹在区块宽，三档位置都看不出差别', () => {
    const inst = newInstance()
    const caps: [string, number][] = [
      ['small', 110],
      ['medium', 150],
      ['large', 190],
    ]
    for (const [size, cap] of caps) {
      fireSizeObserver(inst, size, 5) // 5:1 的横条，按高度算早就超宽
      expect(inst.data.logoW, `${size} 档横条必须夹在区块宽以内`).toBe(cap)
    }
  })

  it('方块图在 150 宽的区块里居中的成因可复现：内容宽 44 < 区块宽 150', () => {
    // 这条是「为什么看起来偏左」的量化依据：44 宽的图放进 150 宽的区块，
    // 居中后左右各留 53rpx。把它钉住，免得以后有人把区块宽改成和内容一样宽、
    // 又顺手删掉位置档。
    const inst = newInstance()
    fireSizeObserver(inst, 'medium', 1)
    const slot = Number(decl(ruleBody(WXSS, '.emp-card .ec-logo-slot-medium'), 'width').replace('rpx', ''))
    expect(slot).toBe(150)
    expect(inst.data.logoW).toBe(44)
    expect((slot - inst.data.logoW) / 2).toBe(53)
  })

  it('未知尺寸档回落 medium，不落成「靠左」那种静默错位', () => {
    const inst = newInstance()
    fireSizeObserver(inst, 'huge', 1)
    expect(inst.data.logoW).toBe(44)
    // 非法/缺省比例：宽度回 0，WXML 不写内联宽度 → CSS width:100%（= 改造前的观感）
    fireSizeObserver(inst, 'medium', 0)
    expect(inst.data.logoW).toBe(0)
  })

  it('bindload 拿到的原始宽高比会夹在安全区间内', () => {
    const inst = newInstance()
    inst.onLogoLoad({ detail: { width: 400, height: 1 } }) // 极端横条
    expect(inst.data.logoRatio).toBeLessThanOrEqual(6)
    inst.onLogoLoad({ detail: { width: 1, height: 400 } }) // 极端竖条
    expect(inst.data.logoRatio).toBeGreaterThanOrEqual(0.2)
    // 没有尺寸的 load 事件不能把比例写成 NaN（会把 width 变成 "NaN rpx"）
    inst.onLogoLoad({ detail: {} })
    expect(inst.data.logoRatio).toBeLessThanOrEqual(6)
  })

  // ---- 样式表：三档尺寸 + 三档对齐 ----
  it('app.wxss 三档区块尺寸齐备，且 medium 就是改造前的 150×44', () => {
    const want: [string, string, string][] = [
      ['small', '110rpx', '36rpx'],
      ['medium', '150rpx', '44rpx'],
      ['large', '190rpx', '52rpx'],
    ]
    for (const [size, w, h] of want) {
      const body = ruleBody(WXSS, `.emp-card .ec-logo-slot-${size}`)
      expect(body, `缺 .ec-logo-slot-${size}`).toBeTruthy()
      expect(decl(body, 'width'), `${size} 档区块宽`).toBe(w)
      expect(decl(body, 'height'), `${size} 档区块高`).toBe(h)
      expect(decl(ruleBody(WXSS, `.emp-card .ec-logo-${size}`), 'height'), `${size} 档图高`).toBe(h)
    }
  })

  it('三档对齐类齐备，缺省 center 是居中（改造前的观感）', () => {
    expect(decl(ruleBody(WXSS, '.emp-card .ec-logo-align-left'), 'justify-content')).toBe('flex-start')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-logo-align-center'), 'justify-content')).toBe('center')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-logo-align-right'), 'justify-content')).toBe('flex-end')
  })

  // ---- WXML：区块包图 + 挂 bindload + 区块也判空 ----
  it('LOGO 在区块内渲染，且区块自身也为空时不渲染（不能凭空占掉行宽）', () => {
    const slot = WXML.slice(WXML.indexOf('ec-logo-slot'))
    expect(slot, '找不到 ec-logo-slot').not.toBe('')
    const tagStart = WXML.lastIndexOf('<view', WXML.indexOf('ec-logo-slot'))
    const tag = WXML.slice(tagStart, WXML.indexOf('>', tagStart))
    expect(tag, '区块必须自己挂 wx:if，否则没 LOGO 时也占 150rpx 行宽').toContain('company.logo')
    expect(WXML.slice(WXML.indexOf('ec-logo-slot'), WXML.indexOf('</view>', WXML.indexOf('ec-logo-slot'))))
      .toContain('class="ec-logo')
  })

  it('image 挂 bindload 才能拿到原始宽高比——没有它位置档永远不动', () => {
    const imgStart = WXML.indexOf('class="ec-logo ')
    expect(imgStart, '找不到 LOGO 的 image').toBeGreaterThan(-1)
    const imgTag = WXML.slice(imgStart, WXML.indexOf('/>', imgStart))
    expect(imgTag).toContain('bindload="onLogoLoad"')
    // 内联宽度是「框收到内容宽」的唯一落地点：漏了它，位置档三档完全一样
    expect(imgTag).toContain('logoW')
    // 保住失败态：图挂了要整块收掉，不能留半截裂图
    expect(imgTag).toContain('binderror="onImgFail"')
  })

  // ---- 页面：下发 + 端上归一 ----
  it('首页把两个档位透传给名片组件', () => {
    expect(PAGE_WXML).toContain('logo-size="{{logoSize}}"')
    expect(PAGE_WXML).toContain('logo-align="{{logoAlign}}"')
  })

  it('端上按白名单归一：旧后端不下发 / 脏值都落缺省 medium + center', () => {
    expect(PAGE_TS).toMatch(/LOGO_SIZES\s*=\s*\[\s*'small'\s*,\s*'medium'\s*,\s*'large'\s*\]/)
    expect(PAGE_TS).toMatch(/LOGO_ALIGNS\s*=\s*\[\s*'left'\s*,\s*'center'\s*,\s*'right'\s*\]/)
    // 兜底值必须写死在调用点，不能从 layout 里取——旧后端就是没有这两个字段
    expect(PAGE_TS).toContain("normalizeLogo(layout.logoSize, LOGO_SIZES, 'medium')")
    expect(PAGE_TS).toContain("normalizeLogo(layout.logoAlign, LOGO_ALIGNS, 'center')")
  })

  // ---- 后端模型：常量 / 归一 / json tag ----
  it('后端有对应常量与归一函数，缺省档是改造前的表现', () => {
    expect(MODEL).toContain('LogoSizeSmall  = "small"')
    expect(MODEL).toContain('LogoSizeLarge  = "large"')
    expect(MODEL).toContain('LogoAlignLeft   = "left"')
    expect(MODEL).toContain('LogoAlignRight  = "right"')
    expect(MODEL).toContain('DefaultLogoSize  = LogoSizeMedium')
    expect(MODEL).toContain('DefaultLogoAlign = LogoAlignCenter')
    // json tag 必须与端上 types.ts 对齐
    expect(MODEL).toContain('json:"logoSize"')
    expect(MODEL).toContain('json:"logoAlign"')
  })

  // ---- 后台：两组三档 + 预览像素与真机 rpx 严格 1:2 ----
  it('后台两组各三档，缺省文案指向「加设置前的样子」', () => {
    for (const v of ['small', 'medium', 'large']) {
      expect(LAYOUT_VUE, `后台缺 LOGO 大小档 ${v}`).toContain(`<el-radio value="${v}">`)
    }
    for (const v of ['left', 'center', 'right']) {
      expect(LAYOUT_VUE, `后台缺 LOGO 位置档 ${v}`).toContain(`<el-radio value="${v}">`)
    }
    expect(LAYOUT_VUE).toContain('中（默认）')
    expect(LAYOUT_VUE).toContain('居中（默认）')
  })

  it('后台预览的像素值 = 真机 rpx / 2（1rpx = 0.5px @375），三档逐值对齐', () => {
    const sizes: [string, number, number][] = [
      ['small', 110, 36],
      ['medium', 150, 44],
      ['large', 190, 52],
    ]
    for (const [size, wRpx, hRpx] of sizes) {
      const slot = ruleBody(LAYOUT_VUE, `.lp-slot-${size}`)
      expect(slot, `预览缺 .lp-slot-${size}`).toBeTruthy()
      expect(Number(decl(slot, 'width').replace('px', '')), `${size} 预览区块宽与真机不一致`).toBe(wRpx / 2)
      expect(Number(decl(slot, 'height').replace('px', '')), `${size} 预览区块高与真机不一致`).toBe(hRpx / 2)
      const img = ruleBody(LAYOUT_VUE, `.lp-img-${size}`)
      expect(img, `预览缺 .lp-img-${size}`).toBeTruthy()
      // 预览用方块图模拟「正方形 LOGO」——正是会被压缩、最能看出位置差别的那种
      expect(Number(decl(img, 'width').replace('px', ''))).toBe(hRpx / 2)
      expect(Number(decl(img, 'height').replace('px', ''))).toBe(hRpx / 2)
    }
  })

  it('OpenAPI 两处（公开 GET / 后台 PUT）都声明了这两个枚举', () => {
    const hits = OPENAPI.match(/logoSize:/g) || []
    // 公开 GET、后台 GET、后台 PUT 三处
    expect(hits.length).toBeGreaterThanOrEqual(3)
    expect(OPENAPI).toContain('logoAlign:')
    expect(OPENAPI).toContain('enum: [small, medium, large]')
    expect(OPENAPI).toContain('enum: [left, center, right]')
  })
})

// ---- 后台预览的姿态必须跟着风格走 ----
//
// 这一段是实拍截图推翻初版后补的：预览原来只画「姓名 …… LOGO 贴右」一种姿态，
// 而暖色风格把 LOGO 换到姓名下方居中、青色风格因为头像在右把 LOGO 落在中部。
// 客户选暖色时看到的预览，在真机上根本不存在——他会照着调，越调越偏。
// 守两件事：三种姿态后台都实现了；姿态分类与 app.wxss 的真实规则一致
// （app.wxss 改了头部布局而预览没跟，必须红）。
describe('后台 LOGO 预览按风格摆姿态', () => {
  const MP_SETTINGS = readFileSync(resolve(REPO, 'frontend/src/views/content/MpSettings.vue'), 'utf-8')

  it('量尺自检：先证明 ruleBody 真能读到 app.wxss 的规则', () => {
    expect(ruleBody(WXSS, '.emp-card .ec-head'), '抓不到基线头部规则，下面的断言会空转').toBeTruthy()
    expect(ruleBody(WXSS, '.wc-style-warm .emp-card .ec-head')).toBeTruthy()
    expect(ruleBody(WXSS, '.wc-style-teal .emp-card .ec-head')).toBeTruthy()
  })

  it('暖色风格在真机上是列向居中，LOGO 换行到姓名下方', () => {
    const head = ruleBody(WXSS, '.wc-style-warm .emp-card .ec-head')
    expect(decl(head, 'flex-direction')).toBe('column')
    const slot = ruleBody(WXSS, '.wc-style-warm .emp-card .ec-logo-slot')
    expect(decl(slot, 'margin-left'), '暖色下区块要取消左间距（换行到下一行）').toBe('0')
    expect(decl(slot, 'margin-top'), '暖色下区块靠 margin-top 与姓名拉开').toBe('12rpx')
  })

  it('青色风格在真机上是左右翻转（头像在右），LOGO 落在头像左侧', () => {
    expect(decl(ruleBody(WXSS, '.wc-style-teal .emp-card .ec-head'), 'flex-direction')).toBe(
      'row-reverse'
    )
    expect(decl(ruleBody(WXSS, '.wc-style-teal .emp-card .ec-info'), 'margin-right')).toBe('24rpx')
  })

  it('标准 / 浅墨是基线横排：没有方向的覆盖', () => {
    expect(decl(ruleBody(WXSS, '.emp-card .ec-head'), 'flex-direction')).toBe('')
    // 2026-11-19 二次调整后标准款允许另写 margin-top（加留白），但方向仍走基线
    const def = ruleBody(WXSS, '.wc-style-default .emp-card .ec-head')
    expect(decl(def, 'flex-direction'), '标准款只加留白，不改头部方向').toBe('')
    expect(decl(def, 'margin-top'), '二次调整后的标准款留白').toBe('24rpx')
    const ink = ruleBody(WXSS, '.wc-style-ink .emp-card .ec-head')
    expect(ink, '浅墨只改 align-items，不改方向').toContain('align-items')
    expect(decl(ink, 'flex-direction')).toBe('')
  })

  it('后台的三种姿态在 CSS 里真的会改变排布', () => {
    expect(decl(ruleBody(LAYOUT_VUE, '.lp-layout-stack'), 'flex-direction')).toBe('column')
    expect(decl(ruleBody(LAYOUT_VUE, '.lp-layout-reverse'), 'flex-direction')).toBe('row-reverse')
    expect(
      decl(ruleBody(LAYOUT_VUE, '.lp-layout-stack .lp-slot'), 'margin'),
      '堆叠时区块要换行，不能还挂着 16rpx 的左间距'
    ).toContain('6px')
    expect(
      decl(ruleBody(LAYOUT_VUE, '.lp-layout-reverse .lp-info'), 'padding-right'),
      '翻转后要让出与头像的间距'
    ).toBe('12px')
  })

  it('风格 → 姿态的映射与 app.wxss 对齐，且模板用动态 class', () => {
    expect(LAYOUT_VUE).toContain("default: 'row'")
    expect(LAYOUT_VUE).toContain("ink: 'row'")
    expect(LAYOUT_VUE).toContain("warm: 'stack'")
    expect(LAYOUT_VUE).toContain("teal: 'reverse'")
    expect(LAYOUT_VUE, '模板必须用动态姿态类，写死就退回「只画一种」').toContain(
      '`lp-layout-${logoLayout}`'
    )
    expect(LAYOUT_VUE, '不能再有写死 class="lp-card"> 的旧结构').not.toMatch(/<div class="lp-card">/)
  })

  it('保存风格后样式页的预览要跟着换（否则停在旧姿态）', () => {
    // 别只断言「文件里出现过 refreshStyle」——注释里写一句也会过，那是空转。
    // 要的是「theme 分支里真的调了它」这个调用形态。
    expect(MP_SETTINGS, '风格页保存后没通知样式页，预览会停在旧姿态').toMatch(
      /tab\.value === 'theme'[\s\S]{0,40}\?\.refreshStyle\?\.\(\)/
    )
    expect(LAYOUT_VUE).toContain('refreshStyle')
    expect(LAYOUT_VUE).toMatch(/defineExpose\(\{[^}]*refreshStyle/)
  })

  it('文案写明「区块在卡片里的落点由风格决定」，别让客户以为本页能挪整块', () => {
    expect(LAYOUT_VUE).toContain('当前风格')
    expect(LAYOUT_VUE).toContain('由「风格模板」决定')
    expect(LAYOUT_VUE, '旧文案「右侧空白就是卡片右边缘」在暖色 / 青色下是错的').not.toContain(
      '右侧空白就是卡片右边缘'
    )
  })
})
