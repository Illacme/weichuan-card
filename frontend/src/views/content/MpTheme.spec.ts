import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import MpTheme from './MpTheme.vue'

// 风格页 P4：核心约束是**只提交两轴 (styleId, brandColorId)**。
// 三个页签共用 /admin/home-layout，但各存各的；整份表单回传会把别的页签刚改过
// 的值一起写回去，所以这里用精确断言钉死 payload，而不是 objectContaining。
// 另外两步选择必须守住树形归属：非 default 风格下清空并禁用色系，否则后端
// BelongsToStyle 会 code=1，客户只看到「保存失败」却不知道为什么。
const STYLE_ITEMS = [
  { id: 'default', name: '经典蓝', description: '默认配色', series: 'style', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
  { id: 'ink', name: '沉稳墨', description: '低饱和深灰蓝', series: 'style', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
  { id: 'warm', name: '暖金棕', description: '温暖金棕', series: 'style', advanced: true, previewPrimary: '#8f6320', previewSoft: '#f7efdf' },
  { id: 'teal', name: '松石青', description: '清透青绿', series: 'style', advanced: true, previewPrimary: '#0f766e', previewSoft: '#dff2ef' },
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
]
const DEFAULT_LAYOUT = {
  styleId: 'default',
  brandColorId: null,
  templateId: 'default',
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

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-alert': ElAlert,
      'el-tag': ElTag,
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

describe('content/MpTheme（P4 固定预览区 + 两步选择）', () => {
  beforeEach(() => {
    mocks.getHomeLayout.mockReset()
    mocks.saveHomeLayout.mockClear()
    ;(ElMessage.success as any).mockClear()
  })

  it('挂载即拉取清单，按服务端 series 渲染两步选择', async () => {
    const w = await mountPage()

    expect(mocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(w.findAll('.picker-step')).toHaveLength(2)
    expect(w.text()).toContain('整体风格')
    expect(w.text()).toContain('品牌配色')
    // 4 张风格卡 + 「默认色」+ 8 款品牌色
    expect(styleCards(w)).toHaveLength(4)
    expect(colorOptions(w)).toHaveLength(9)
    expect(styleCards(w)[0].text()).toContain('经典蓝')
    expect(styleCards(w)[1].find('.el-tag').text()).toBe('高级')
    expect(colorOptions(w)[0].text()).toContain('默认色')
    expect(colorOptions(w)[1].text()).toContain('绛红')
  })

  it('整页只有一个固定预览区，默认落在当前组合对应的 PNG', async () => {
    const w = await mountPage()

    expect(w.findAll('.theme-preview')).toHaveLength(1)
    expect(w.find('.preview-pane').exists()).toBe(true)
    expect(previewSrc(w)).toContain('theme-previews/default.png')
    expect(previewShot(w).attributes('alt')).toContain('经典蓝')
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

  it('第 1 步选风格：固定预览区随风格切换到对应 PNG，并带上版式示意 class', async () => {
    const w = await mountPage()

    await styleCards(w)[1].trigger('click')
    await flush()

    expect(styleCards(w)[1].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('theme-previews/ink.png')
    expect(w.find('.theme-preview').classes()).toContain('layout-ink')
    expect(w.text()).toContain('暂无可用品牌配色')
    // 未保存时不能显示「使用中」，否则客户以为已经生效
    expect(w.find('.pane-combo-line').text()).not.toContain('使用中')
  })

  it('第 2 步选品牌色：只在 default 风格上叠加，预览切到该色系 PNG 且风格仍是经典蓝', async () => {
    const w = await mountPage()

    await colorOptions(w)[1].trigger('click')
    await flush()

    expect(colorOptions(w)[1].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('theme-previews/brand-crimson.png')
    expect(styleCards(w)[0].classes()).toContain('selected')
    expect(w.find('.theme-preview').classes()).toContain('layout-default')
  })

  it('树形归属：切到非 default 风格自动清空色系，保存不会带出跨风格组合', async () => {
    const w = await mountPage()

    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('brand-crimson.png')

    await styleCards(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('ink.png')
    expect(colorOptions(w)[0].classes()).toContain('selected')
    for (const c of colorOptions(w)) expect(c.classes()).toContain('disabled')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'ink', brandColorId: null })
  })

  it('非 default 风格下点色卡无效（不能绕过后端 BelongsToStyle）', async () => {
    const w = await mountPage()
    await styleCards(w)[3].trigger('click')
    await flush()

    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('teal.png')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'teal', brandColorId: null })
  })

  it('点回「默认色」清掉已叠加的色系', async () => {
    const w = await mountPage()
    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('brand-crimson.png')

    await colorOptions(w)[0].trigger('click')
    await flush()
    expect(previewSrc(w)).toContain('default.png')
    expect(colorOptions(w)[0].classes()).toContain('selected')
  })

  it('保存只提交两轴，不回传其它展示字段；成功后才显示「使用中」', async () => {
    const w = await mountPage()

    await colorOptions(w)[2].trigger('click')
    await flush()
    await (w.vm as any).save()
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: 'brand-coral',
    })
    expect(ElMessage.success).toHaveBeenCalledWith('已保存小程序风格模板')
    expect(w.find('.pane-combo-line').text()).toContain('使用中')
  })

  it('load 优先读两轴，忽略旧的 templateId 派生列', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: 'teal', brandColorId: null, templateId: 'default',
    })

    expect(styleCards(w)[3].classes()).toContain('selected')
    expect(previewSrc(w)).toContain('teal.png')
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'teal', brandColorId: null })
  })

  // 旧后端（P1 之前）只下发兼容派生列 templateId：brand-* 必须拆回
  // (default, brand-*)，否则客户已选的品牌色会在升级后被静默冲掉。
  it('旧后端只下发 templateId：按前缀拆回两轴，预览与保存都不丢色系', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: undefined, brandColorId: undefined, templateId: 'brand-ocean',
    })

    expect(previewSrc(w)).toContain('brand-ocean.png')
    expect(styleCards(w)[0].classes()).toContain('selected')
    expect(colorOptions(w)[5].classes()).toContain('selected')
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: 'brand-ocean',
    })
  })

  it('旧后端不下发 series：卡片不丢失，色系区给出升级提示', async () => {
    const w = await mountPage({
      styleId: 'default', brandColorId: null, templateId: 'default', templateEntitled: true,
      templates: STYLE_ITEMS.map(({ id, name, description, advanced, previewPrimary, previewSoft }) =>
        ({ id, name, description, advanced, previewPrimary, previewSoft })),
    })

    expect(styleCards(w)).toHaveLength(4)
    expect(w.text()).toContain('当前服务端版本未提供品牌配色清单')
    expect(previewSrc(w)).toContain('default.png')
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
    expect(w.find('.zoom-shot').attributes('src')).toContain('default.png')
    expect(w.find('.zoom-note').text()).toContain('非真机截图')
    expect(w.find('.zoom-scroll').find('img.zoom-shot').exists()).toBe(true)

    await colorOptions(w)[1].trigger('click')
    await flush()
    expect(w.find('.zoom-shot').attributes('src')).toContain('brand-crimson.png')

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
    expect(previewSrc(w)).toContain('default.png')

    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'default', brandColorId: null })
  })

  // 授权到期时后端仍允许「带着同一个组合」保存别的设置，前端不能把已选中的
  // 那款也锁死，否则客户连切回经典蓝都做不到。
  it('授权到期但已选中高级风格：该款不锁，仍可保存或切回默认款', async () => {
    const w = await mountPage({
      ...DEFAULT_LAYOUT,
      styleId: 'ink', brandColorId: null, templateId: 'ink', templateEntitled: false,
    })

    expect(styleCards(w)[1].classes()).not.toContain('locked')
    expect(styleCards(w)[0].classes()).not.toContain('locked')

    await styleCards(w)[0].trigger('click')
    await flush()
    await (w.vm as any).save()
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ styleId: 'default', brandColorId: null })
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

  it('页面说明写明「通用平台 / 不含真实客户资料 / 非真机截图 / 固定预览区」', async () => {
    const w = await mountPage()
    expect(w.text()).toContain('本产品是通用平台')
    expect(w.text()).toContain('不含任何真实企业客户资料')
    expect(w.text()).toContain('不是真机截图')
    expect(w.text()).toContain('固定在一个位置')
  })
})
