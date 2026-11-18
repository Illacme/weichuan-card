import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import MpTheme from './MpTheme.vue'

// 预览图 manifest：浓度遮罩高度按各风格实测高度反算，与生成脚本同源对账。
const MANIFEST = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../public/theme-previews/manifest.json'
)
const MASKS_DIR = resolve(dirname(MANIFEST), 'masks')

// 风格页 P5：核心约束是**只提交 (styleId, brandColorId, bgGradientStrength) 三值**，
// 4 风格 × 10 色档 = 44 个组合全部可选、全部按自己的组合键寻图；顶部浓度随这三值
// 一起保存，并在左侧预览图上按「背景遮罩图」叠白层即时模拟（0~12% 段与真机
// rgba 数学等价）。遮罩图逐像素排除卡片 / 照片 / 控件，所以低浓度时卡片内容
// 不受影响，顶部与卡片外侧背景连续，不会出现颜色硬分界。
// 三个页签共用 /admin/home-layout，但各存各的；整份表单回传会把别的页签刚改过
// 的值一起写回去，所以这里用精确断言钉死 payload，而不是 objectContaining。
// P4 的「非 default 风格清空并禁用色系」已废止：那会让客户对齐好的 VI 主色
// 在切风格时无声消失，而后端 P5 早已放开跨风格组合。
const STYLE_ITEMS = [
  { id: 'default', name: '标准均衡', description: '标准列数与间距、中等圆角', series: 'style', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
  { id: 'ink', name: '紧凑信息', description: '近直角细描边、高信息密度', series: 'style', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
  { id: 'warm', name: '宽松亲和', description: '大圆角胶囊按钮、单列精选大图', series: 'style', advanced: true, previewPrimary: '#8f6320', previewSoft: '#f7efdf' },
  { id: 'teal', name: '疏排透气', description: '中圆角轻投影、疏排网格', series: 'style', advanced: true, previewPrimary: '#0f766e', previewSoft: '#dff2ef' },
]
const BRAND_ITEMS = [
  { id: 'brand-crimson', name: '绛红', description: '绛红主色', series: 'brand', advanced: true, previewPrimary: '#a01f2e', previewSoft: '#f3e2e4' },
  { id: 'brand-coral', name: '朱橘', description: '朱橘主色', series: 'brand', advanced: true, previewPrimary: '#a33d16', previewSoft: '#f3e6e1' },
  { id: 'brand-amber', name: '琥珀', description: '琥珀金主色', series: 'brand', advanced: true, previewPrimary: '#8a5a08', previewSoft: '#f0eadf' },
  { id: 'brand-forest', name: '森绿', description: '森绿主色', series: 'brand', advanced: true, previewPrimary: '#12603a', previewSoft: '#e0eae5' },
  { id: 'brand-ocean', name: '海蓝', description: '海蓝主色', series: 'brand', advanced: true, previewPrimary: '#0b5585', previewSoft: '#dfe9ef' },
  { id: 'brand-indigo', name: '靛青', description: '靛青主色', series: 'brand', advanced: true, previewPrimary: '#38429c', previewSoft: '#e5e6f2' },
  { id: 'brand-violet', name: '紫罗兰', description: '紫罗兰主色', series: 'brand', advanced: true, previewPrimary: '#612a96', previewSoft: '#eae3f1' },
  { id: 'brand-magenta', name: '品红', description: '品红主色', series: 'brand', advanced: true, previewPrimary: '#8c2358', previewSoft: '#f0e2e9' },
  { id: 'brand-royal', name: '宝蓝', description: '宝蓝主色', series: 'brand', advanced: true, previewPrimary: '#0820d0', previewSoft: '#e6ebfa' },
  { id: 'brand-scarlet', name: '正红', description: '正红主色', series: 'brand', advanced: true, previewPrimary: '#d40c0a', previewSoft: '#fbecec' },
]
const STYLE_IDS = STYLE_ITEMS.map((t) => t.id)
const BRAND_IDS = BRAND_ITEMS.map((t) => t.id)
const comboId = (s: string, b: string | null) => `${s}--${b || 'default'}`

const DEFAULT_LAYOUT = {
  styleId: 'default',
  brandColorId: null,
  bgGradientStrength: 100,
  templateEntitled: true,
  templates: [...STYLE_ITEMS, ...BRAND_ITEMS],
}

const mocks = vi.hoisted(() => ({
  getHomeLayout: vi.fn(async (): Promise<any> => ({})),
  saveHomeLayout: vi.fn(async (_payload: any) => ({ code: 0 })),
}))

vi.mock('@/api/content', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {} }))

import { ElMessage } from 'element-plus'

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'icon', 'link', 'disabled'],
  template: `<button class="el-button" :disabled="disabled" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['type', 'title', 'closable', 'showIcon'],
  template: `<div class="el-alert" :data-type="type"><div class="alert-title">{{ title }}</div><slot /></div>`,
})
const ElTag = defineComponent({ name: 'ElTag', template: `<span class="el-tag"><slot /></span>` })
// 浓度滑杆 stub：渲染原生 range，方便 setValue 驱动 v-model，验证即时预览与保存。
const ElSlider = defineComponent({
  name: 'ElSlider',
  props: ['modelValue', 'min', 'max', 'step', 'disabled'],
  emits: ['update:modelValue'],
  template: `<input type="range" class="el-slider" :value="modelValue" :min="min" :max="max" :step="step" :disabled="disabled" @input="$emit('update:modelValue', Number($event.target.value))" />`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-alert': ElAlert,
      'el-tag': ElTag,
      'el-slider': ElSlider,
    },
    directives: { loading: {} },
  },
}

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage(layout?: any) {
  mocks.getHomeLayout.mockResolvedValueOnce(layout ?? { ...DEFAULT_LAYOUT })
  const w = mount(MpTheme, mountOpts)
  await flush()
  return w
}

const styleCards = (w: any) => w.findAll('.option-card')
const colorOptions = (w: any) => w.findAll('.color-option')
const previewShot = (w: any) => w.find('img.preview-shot')
const previewSrc = (w: any) => previewShot(w).attributes('src') || ''

describe('content/MpTheme（P5 固定预览区 + 两轴组合三步选择 + 顶部浓度即时预览）', () => {
  beforeEach(() => {
    mocks.getHomeLayout.mockReset()
    mocks.saveHomeLayout.mockClear()
    ;(ElMessage.success as any).mockClear()
  })

  it('挂载即拉取清单，按服务端 series 渲染三步选择（含顶部浓度）', async () => {
    const w = await mountPage()

    expect(mocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(w.findAll('.picker-step')).toHaveLength(3)
    expect(w.text()).toContain('整体风格')
    expect(w.text()).toContain('品牌配色')
    expect(w.text()).toContain('顶部主色浓度')
    // 4 张风格卡 + 「默认色」+ 10 款品牌色
    expect(styleCards(w)).toHaveLength(4)
    expect(colorOptions(w)).toHaveLength(11)
    expect(styleCards(w)[0].text()).toContain('标准均衡')
    expect(styleCards(w)[1].find('.el-tag').text()).toBe('高级')
    expect(colorOptions(w)[0].text()).toContain('默认色')
    expect(colorOptions(w)[1].text()).toContain('绛红')
  })

  it('整页只有一个固定预览区，默认落在当前组合对应的 PNG', async () => {
    const w = await mountPage()

    expect(w.findAll('.theme-preview')).toHaveLength(1)
    expect(w.find('.preview-pane').exists()).toBe(true)
    expect(previewSrc(w)).toContain('theme-previews/default--default.thumb.png')
    expect(previewShot(w).attributes('alt')).toContain('标准均衡')
    expect(w.find('.theme-preview').classes()).toContain('layout-default')
    expect(w.find('.pane-combo-line').text()).toContain('使用中')
  })

  it('预览区有底层示意 + 非真机角标 + 查看大图入口（悬停才显示）', async () => {
    const w = await mountPage()

    expect(w.find('.preview-fallback').exists()).toBe(true)
    expect(w.find('.fallback-tag').text()).toBe('示意图')
    expect(w.find('.preview-badge').text()).toBe('风格示意 · 非真机截图')
    expect(w.find('.preview-zoom-btn').exists()).toBe(true)
    expect(w.find('.preview-scroll').exists()).toBe(true)
  })

  it('第 1 步选风格：预览切到该风格默认色 PNG，色系区不再被禁用', async () => {
    const w = await mountPage()

    await styleCards(w)[1].trigger('click')
    await flush()

    expect(styleCards(w)[1].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('theme-previews/ink--default.thumb.png')
    expect(w.find('.theme-preview').classes()).toContain('layout-ink')
    expect(w.text()).not.toContain('暂无可用品牌配色')
    for (const c of colorOptions(w)) expect(c.classes()).not.toContain('disabled')
    // 未保存时不能显示「使用中」，否则客户以为已经生效
    expect(w.find('.pane-combo-line').text()).not.toContain('使用中')
  })

  it('第 2 步选品牌色：default 风格下叠色，预览切到该组合 PNG', async () => {
    const w = await mountPage()

    await colorOptions(w)[1].trigger('click')
    await flush()

    expect(colorOptions(w)[1].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('theme-previews/default--brand-crimson.thumb.png')
    expect(styleCards(w)[0].classes()).toContain('selected')
    expect(w.find('.theme-preview').classes()).toContain('layout-default')
  })

  it('P5 全叉乘：非 default 风格下照样能选色系，布局仍是该风格', async () => {
    const w = await mountPage()
    await styleCards(w)[3].trigger('click') // 疏排透气
    await flush()

    await colorOptions(w)[1].trigger('click') // 绛红
    await flush()

    expect(previewSrc(w)).toContain('theme-previews/teal--brand-crimson.thumb.png')
    expect(w.find('.theme-preview').classes()).toContain('layout-teal')
    expect(colorOptions(w)[1].classes()).toContain('selected')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'teal', brandColorId: 'brand-crimson', bgGradientStrength: 100,
    })
  })

  it('P5：切风格保留已选色系，不再清空，也不会带回跨风格旧限制', async () => {
    const w = await mountPage()

    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('default--brand-crimson.thumb.png')

    await styleCards(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('ink--brand-crimson.thumb.png')
    expect(colorOptions(w)[1].classes()).toContain('selected')
    expect(colorOptions(w)[0].classes()).not.toContain('selected')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'ink', brandColorId: 'brand-crimson', bgGradientStrength: 100,
    })
  })

  it('P5 组合矩阵：4 风格 × 10 色档 = 44 个组合，每个都按自己的组合键寻图', async () => {
    const w = await mountPage()
    let seen = 0
    for (let si = 0; si < STYLE_IDS.length; si++) {
      await styleCards(w)[si].trigger('click')
      await flush()
      for (let bi = 0; bi <= BRAND_IDS.length; bi++) {
        await colorOptions(w)[bi].trigger('click')
        await flush()
        const brand = bi === 0 ? null : BRAND_IDS[bi - 1]
        expect(previewSrc(w), `${STYLE_IDS[si]} + ${brand}`)
          .toContain(`theme-previews/${comboId(STYLE_IDS[si], brand)}.thumb.png`)
        expect(w.find('.theme-preview').classes()).toContain(`layout-${STYLE_IDS[si]}`)
        seen++
      }
    }
    expect(seen).toBe(44)
  })

  it('P5：默认色色卡跟随当前风格（紧凑信息的默认色不是全局蓝）', async () => {
    const w = await mountPage()
    const swatch = () => colorOptions(w)[0].find('.color-swatch').attributes('style') || ''
    expect(swatch()).toMatch(/#1a8cff|rgb\(26,\s*140,\s*255\)/)

    await styleCards(w)[1].trigger('click')
    await flush()
    expect(swatch()).toMatch(/#3d4b5c|rgb\(61,\s*75,\s*92\)/)
  })

  it('点回「默认色」清掉已叠加的色系', async () => {
    const w = await mountPage()
    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('default--brand-crimson.thumb.png')

    await colorOptions(w)[0].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('default--default.thumb.png')
    expect(colorOptions(w)[0].classes()).toContain('selected')
  })

  it('保存只提交两轴，不回传其它展示字段；成功后才显示「使用中」', async () => {
    const w = await mountPage()

    await colorOptions(w)[2].trigger('click')
    await flush()
    await (w.vm as any).save()
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: 'brand-coral', bgGradientStrength: 100,
    })
    expect(ElMessage.success).toHaveBeenCalledWith('已保存小程序风格模板')
    expect(w.find('.pane-combo-line').text()).toContain('使用中')
  })

  // ===== 顶部主色浓度（第 3 步）：滑杆 → 遮罩即时预览 → 保存三值 =====
  const veil = (w: any) => w.find('.preview-scroll .strength-veil')
  const veilStyle = (w: any) => veil(w).attributes('style') || ''
  // 遮罩是 SVG：白层透明度在第一个 rect 的 fill-opacity。rect 必须带 mask，
  // 否则白层铺满整图会把卡片内文字一起冲淡；只盖到卡片顶的矩形又会让卡片外侧
  // 仍是原色深蓝、与混白后的顶栏割裂。两种回归都由这里的断言钉死。
  const veilAlphaAttr = (w: any) => veil(w).find('rect').attributes('fill-opacity')
  const veilMaskRef = (w: any) => veil(w).find('rect').attributes('mask')
  const veilMaskSrc = (w: any) => (veil(w).find('mask image').attributes('href') || '')

  it('第 3 步有浓度滑杆，默认 100% 原色：遮罩 opacity=0（不遮），白层按背景遮罩取区域', async () => {
    const w = await mountPage()

    expect(w.find('.strength-row').exists()).toBe(true)
    const slider = w.find('.strength-row input.el-slider')
    expect(slider.exists()).toBe(true)
    expect(slider.attributes('value')).toBe('100')
    expect(w.find('.strength-val').text()).toBe('100%')
    // 100% 时白层 fill-opacity=0，遮罩不显；但结构必须在：铺满整图 + mask 限定背景区域
    expect(veil(w).exists()).toBe(true)
    expect(veilAlphaAttr(w)).toBe('0')
    expect(veilStyle(w)).toContain('height: 100%')
    // 高度从 manifest 反算，不写死数字：换服务图会让内容底部整体变矮，
    // 硬编码会像 2026-11-18 那样悄悄红掉。
    const defaultH = JSON.parse(readFileSync(MANIFEST, 'utf-8')).contentBottoms.default + 80
    expect(veil(w).attributes('viewBox')).toBe(`0 0 375 ${defaultH}`)
    expect(veil(w).find('rect').attributes('height')).toBe(String(defaultH))
    expect(veilMaskRef(w)).toBe('url(#wc-veil-mask-preview)')
    expect(veilMaskSrc(w)).toContain('/theme-previews/masks/default.png')
  })

  it('拖低浓度：遮罩 opacity 实时变大、数值联动，保存带上新浓度', async () => {
    const w = await mountPage()

    await w.find('.strength-row input.el-slider').setValue('40')
    await flush()

    expect(w.find('.strength-val').text()).toBe('40%')
    // alpha = (100-40)/100 = 0.6，滑杆一动遮罩即时更新，不必重新切图
    expect(veilAlphaAttr(w)).toBe('0.6')
    expect(veilStyle(w)).toContain('height: 100%')
    // 大图浮层的遮罩必须同步（同一 viewBox / alpha，但 mask id 不能撞车）
    await w.find('.preview-zoom-btn').trigger('click')
    await flush()
    const zoomVeil = w.find('.zoom-scroll .strength-veil')
    expect(zoomVeil.attributes('style')).toBe(veilStyle(w))
    expect(zoomVeil.attributes('viewBox')).toBe(veil(w).attributes('viewBox'))
    expect(zoomVeil.find('rect').attributes('fill-opacity')).toBe('0.6')
    expect(zoomVeil.find('rect').attributes('mask')).toBe('url(#wc-veil-mask-zoom)')
    expect(veilMaskRef(w)).toBe('url(#wc-veil-mask-preview)')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: null, bgGradientStrength: 40,
    })
  })

  it('浓度滑到 0：值不被 || 吞掉，遮罩 opacity=1（顶部近白），保存的仍是 0', async () => {
    const w = await mountPage()

    await w.find('.strength-row input.el-slider').setValue('0')
    await flush()

    expect(w.find('.strength-val').text()).toBe('0%')
    expect(veilAlphaAttr(w)).toBe('1')
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: null, bgGradientStrength: 0,
    })
  })

  it('浓度<50 时重绘深色公司名（PNG 白字会被遮罩冲淡），≥50 让原白字透出', async () => {
    const w = await mountPage()
    // 100%：白字原样透出，不重绘
    expect(veil(w).find('text').exists()).toBe(false)

    await w.find('.strength-row input.el-slider').setValue('40')
    await flush()
    const text = veil(w).find('text')
    expect(text.exists()).toBe(true)
    expect(text.text()).toBe('示例企业')
    expect(text.attributes('fill')).toBe('#333')
    // 背景块用主色混白（default 默认色 #1a8cff、alpha 0.6），盖住原白字避免双影
    const rects = veil(w).findAll('rect')
    expect(rects).toHaveLength(2)
    expect(rects[1].attributes('fill')).toBe('rgb(163, 209, 255)')

    // 阈值与真机 wxml 的 `< 50` 一致：50 仍是白字（不重绘）
    await w.find('.strength-row input.el-slider').setValue('50')
    await flush()
    expect(veil(w).find('text').exists()).toBe(false)
  })

  it('浓度遮罩按风格与 manifest 对账：画布高度、背景遮罩图四款都不漏不串', async () => {
    // 整图高 = contentBottom + 呼吸 20 + 底栏 60；遮罩图必须和预览图同尺寸，
    // 否则白层与背景错位，要么盖到卡片上，要么卡片外侧漏出原色深蓝。
    const manifest = JSON.parse(readFileSync(MANIFEST, 'utf-8'))
    // 组件里重绘公司名用的占位文案必须与生成脚本一致，否则遮罩里会叠出两种公司名
    expect(manifest.placeholders?.short_name).toBe('示例企业')
    const bottoms: Record<string, number> = manifest.contentBottoms
    const w = await mountPage()
    for (const sid of STYLE_IDS) {
      await styleCards(w)[STYLE_IDS.indexOf(sid)].trigger('click')
      await flush()
      const h = bottoms[sid] + 20 + 60
      expect(veil(w).attributes('viewBox'), `${sid} 画布高度应与 manifest 实测一致`)
        .toBe(`0 0 375 ${h}`)
      expect(veil(w).find('rect').attributes('height')).toBe(String(h))
      expect(veilMaskSrc(w), `${sid} 必须用自己风格的遮罩图`).toContain(`/theme-previews/masks/${sid}.png`)
      expect(existsSync(resolve(MASKS_DIR, `${sid}.png`)), `${sid} 遮罩图必须真实存在`)
        .toBe(true)
      expect(veilMaskRef(w)).toBe('url(#wc-veil-mask-preview)')
    }
  })

  it('load 读回非默认浓度；只改浓度也进「未保存」态，保存后回「使用中」', async () => {
    const w = await mountPage({ ...DEFAULT_LAYOUT, bgGradientStrength: 65 })

    expect(w.find('.strength-val').text()).toBe('65%')
    expect(veilAlphaAttr(w)).toBe('0.35')
    expect(w.find('.pane-combo-line').text()).toContain('使用中')

    await w.find('.strength-row input.el-slider').setValue('30')
    await flush()
    expect(w.find('.pane-combo-line').text()).not.toContain('使用中')
    await (w.vm as any).save()
    await flush()
    expect(w.find('.pane-combo-line').text()).toContain('使用中')
  })

  it('load 读回两轴，预览与保存都只用两轴', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: 'teal', brandColorId: null,
    })

    expect(styleCards(w)[3].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('teal--default.thumb.png')
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'teal', brandColorId: null, bgGradientStrength: 100 })
  })

  it('P5：load 读回跨风格组合，预览与保存都不丢两轴中的任何一维', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: 'ink', brandColorId: 'brand-ocean',
    })

    expect(styleCards(w)[1].classes()).toContain('selected')
    expect(colorOptions(w)[5].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('ink--brand-ocean.thumb.png')
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'ink', brandColorId: 'brand-ocean', bgGradientStrength: 100,
    })
  })

  it('旧后端不下发 series：卡片不丢失，色系区给出升级提示', async () => {
    const w = await mountPage({
      styleId: 'default', brandColorId: null, templateEntitled: true,
      templates: STYLE_ITEMS.map(({ id, name, description, advanced, previewPrimary, previewSoft }) =>
        ({ id, name, description, advanced, previewPrimary, previewSoft })),
    })

    expect(styleCards(w)).toHaveLength(4)
    expect(w.text()).toContain('当前服务端版本未提供品牌配色清单')
    expect(previewSrc(w)).toContain('default--default.thumb.png')
  })

  it('单张预览图加载失败：只降级当前组合，重新加载会重试', async () => {
    const w = await mountPage()

    await previewShot(w).trigger('error')
    await flush()
    expect(previewShot(w).exists()).toBe(false)
    expect(w.find('.preview-fallback').exists()).toBe(true)
    expect(w.find('.preview-zoom-btn').exists()).toBe(false)

    await (w.vm as any).load()
    await flush()
    expect(previewShot(w).exists()).toBe(true)
  })

  it('查看大图：打开后随组合切换，可关闭，图降级时自动收起', async () => {
    const w = await mountPage()
    expect(w.find('.preview-zoom').exists()).toBe(false)

    await w.find('.preview-zoom-btn').trigger('click')
    await flush()
    expect(w.find('.preview-zoom').exists()).toBe(true)
    // 大图必须指回原图（750px 宽）：列表窗口用 460px 缩略图，浮层才是看清细节的地方。
    // `.thumb` 不能出现在这里，否则「查看大图」等于放大一张缩略图。
    const zoomSrc = w.find('.zoom-shot').attributes('src') || ''
    expect(zoomSrc).toContain('theme-previews/default--default.png')
    expect(zoomSrc).not.toContain('.thumb')
    expect(w.find('.zoom-note').text()).toContain('非真机截图')
    expect(w.find('.zoom-scroll').find('img.zoom-shot').exists()).toBe(true)

    await colorOptions(w)[1].trigger('click')
    await flush()
    const zoomSrc2 = w.find('.zoom-shot').attributes('src') || ''
    expect(zoomSrc2).toContain('theme-previews/default--brand-crimson.png')
    expect(zoomSrc2).not.toContain('.thumb')

    const closeBtn = w.findAll('.el-button').find((b: any) => b.text().includes('关闭'))
    expect(closeBtn).toBeDefined()
    await closeBtn!.trigger('click')
    await flush()
    expect(w.find('.preview-zoom').exists()).toBe(false)

    await w.find('.preview-zoom-btn').trigger('click')
    await flush()
    await w.find('.preview-zoom').trigger('click')
    await flush()
    expect(w.find('.preview-zoom').exists()).toBe(false)

    await w.find('.preview-zoom-btn').trigger('click')
    await flush()
    await previewShot(w).trigger('error')
    await flush()
    expect(w.find('.preview-zoom').exists()).toBe(false)
  })

  it('未开通高级模板：高级风格与色系置灰且点不动，切回默认仍放行', async () => {
    const w = await mountPage({ ...DEFAULT_LAYOUT, templateEntitled: false })

    expect(w.find('.el-alert').exists()).toBe(true)
    expect(w.text()).toContain('续费后自动恢复')
    expect(styleCards(w)[1].classes()).toContain('locked')
    expect(colorOptions(w)[1].classes()).toContain('locked')

    await styleCards(w)[1].trigger('click')
    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('default--default.thumb.png')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'default', brandColorId: null, bgGradientStrength: 100 })
  })

  // 授权到期时后端仍允许「带着同一个组合」保存别的设置，前端不能把已选中的
  // 那款也锁死，否则客户连切回标准均衡都做不到。
  it('授权到期但已选中高级风格：该款不锁，仍可保存或切回默认款', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: 'ink', brandColorId: null, templateEntitled: false,
    })

    expect(styleCards(w)[1].classes()).not.toContain('locked')
    expect(styleCards(w)[0].classes()).not.toContain('locked')

    await styleCards(w)[0].trigger('click')
    await flush()
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'default', brandColorId: null, bgGradientStrength: 100 })
  })

  it('清单为空：暴露的 templates 为空，父组件据此禁用保存', async () => {
    const w = await mountPage({ ...DEFAULT_LAYOUT, templates: [] })

    expect(styleCards(w)).toHaveLength(0)
    expect((w.vm as any).templates.length).toBe(0)
  })

  it('拉取失败：不白屏，仍能重新加载', async () => {
    mocks.getHomeLayout.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()
    expect(w.find('.desc').exists()).toBe(true)
    expect(w.find('.theme-preview').exists()).toBe(true)
  })

  it('保存失败：不提示成功，也不把未保存的组合标成「使用中」', async () => {
    mocks.saveHomeLayout.mockRejectedValueOnce(new Error('403'))
    const w = await mountPage()

    await styleCards(w)[1].trigger('click')
    await flush()
    await (w.vm as any).save()
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(w.find('.pane-combo-line').text()).not.toContain('使用中')
  })

  it('页面说明写明「通用平台 / 不含真实客户资料 / 非真机截图 / 固定预览区 / 44 组合」', async () => {
    const w = await mountPage()
    expect(w.text()).toContain('本产品是通用平台')
    expect(w.text()).toContain('不含任何真实企业客户资料')
    expect(w.text()).toContain('不是真机截图')
    expect(w.text()).toContain('固定在一个位置')
    expect(w.text()).toContain('44 种组合')
  })
})
