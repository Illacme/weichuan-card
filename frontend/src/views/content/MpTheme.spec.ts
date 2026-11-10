import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import MpTheme from './MpTheme.vue'

// 风格模板页：核心约束是**只提交 templateId**。
// 三个页签共用 /admin/home-layout，但各存各的；整份表单回传会把别的页签刚改过
// 的值一起写回去（后端 Order 是指针语义，多提交反而可能覆盖），所以这里用精确
// 断言钉死 payload，而不是只查 objectContaining。
const mocks = vi.hoisted(() => ({
  getHomeLayout: vi.fn(async (): Promise<any> => ({
    templateId: 'default',
    templateEntitled: true,
    templates: [
      { id: 'default', name: '经典蓝', description: '默认配色', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
      { id: 'ink', name: '沉稳墨', description: '低饱和深灰蓝', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
      { id: 'warm', name: '暖金棕', description: '温暖金棕', advanced: true, previewPrimary: '#8f6320', previewSoft: '#f7efdf' },
      { id: 'teal', name: '松石青', description: '清透青绿', advanced: true, previewPrimary: '#0f766e', previewSoft: '#dff2ef' },
    ],
  })),
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
  // disabled 必须真的落到 DOM 上：清单为空时「保存」按钮要靠它禁用，
  // 只声明 prop 而不输出 attribute 的话，attributes('disabled') 恒 undefined。
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
  if (layout) mocks.getHomeLayout.mockResolvedValueOnce(layout)
  const w = mount(MpTheme, mountOpts)
  await flush()
  return w
}

function cards(w: any) {
  return w.findAll('.theme-card')
}

function saveButton(w: any) {
  const btn = w.findAll('.el-button').find((b: any) => b.text().includes('保存风格'))
  if (!btn) throw new Error('找不到保存按钮')
  return btn
}

describe('content/MpTheme', () => {
  beforeEach(() => {
    mocks.getHomeLayout.mockClear()
    mocks.saveHomeLayout.mockClear()
    ;(ElMessage.success as any).mockClear()
  })

  it('挂载即拉取模板清单并按服务端顺序渲染', async () => {
    const w = await mountPage()

    expect(mocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(cards(w)).toHaveLength(4)
    expect(cards(w)[0].text()).toContain('经典蓝')
    expect(cards(w)[1].text()).toContain('沉稳墨')
    expect(cards(w)[2].text()).toContain('暖金棕')
    expect(cards(w)[3].text()).toContain('松石青')
    expect(cards(w)[0].find('.el-tag').exists()).toBe(false)
    expect(cards(w)[1].find('.el-tag').text()).toBe('高级')
  })

  it('每张预览卡按模板 id 套版式示意 class（换色之外也要看得出圆角差异）', async () => {
    const w = await mountPage()

    expect(cards(w)[0].find('.theme-preview').classes()).toContain('layout-default')
    expect(cards(w)[1].find('.theme-preview').classes()).toContain('layout-ink')
    expect(cards(w)[2].find('.theme-preview').classes()).toContain('layout-warm')
    expect(cards(w)[3].find('.theme-preview').classes()).toContain('layout-teal')
  })

  it('每张卡按 id 拼出真实预览图，底层示意常驻（图没加载完不留空白）', async () => {
    const w = await mountPage()

    const shots = cards(w).map((c: any) => c.find('img.preview-shot'))
    expect(shots).toHaveLength(4)
    expect(shots[0].attributes('src')).toContain('theme-previews/default.png')
    expect(shots[1].attributes('src')).toContain('theme-previews/ink.png')
    expect(shots[2].attributes('src')).toContain('theme-previews/warm.png')
    expect(shots[3].attributes('src')).toContain('theme-previews/teal.png')
    expect(shots[1].attributes('alt')).toContain('沉稳墨')
    for (const c of cards(w)) {
      expect(c.find('.preview-fallback').exists()).toBe(true)
      // 降级必须是可见的：静默退回色块会让「真实预览图没生效」没人察觉
      expect(c.find('.fallback-tag').text()).toBe('示意图')
    }
  })

  it('单张预览图加载失败：只降级这一张，其余三张不受影响', async () => {
    const w = await mountPage()

    await cards(w)[2].find('img.preview-shot').trigger('error')
    await flush()

    expect(cards(w)[2].find('img.preview-shot').exists()).toBe(false)
    expect(cards(w)[2].find('.preview-fallback').exists()).toBe(true)
    expect(cards(w)[2].find('.fallback-tag').exists()).toBe(true)
    expect(cards(w)[0].find('img.preview-shot').exists()).toBe(true)
    expect(cards(w)[1].find('img.preview-shot').exists()).toBe(true)
    expect(cards(w)[3].find('img.preview-shot').exists()).toBe(true)
  })

  it('点「重新加载」会重试预览图，不再一直停在示意图上', async () => {
    const w = await mountPage()

    await cards(w)[1].find('img.preview-shot').trigger('error')
    await flush()
    expect(cards(w)[1].find('img.preview-shot').exists()).toBe(false)

    const reload = w.findAll('.el-button').find((b: any) => b.text().includes('重新加载'))
    expect(reload).toBeDefined()
    await reload!.trigger('click')
    await flush()

    expect(cards(w)[1].find('img.preview-shot').exists()).toBe(true)
  })

  it('按服务端 series 分两组渲染，品牌配色组说明「只换主色」且可选中', async () => {
    const w = await mountPage({
      templateId: 'default',
      templateEntitled: true,
      templates: [
        { id: 'default', name: '经典蓝', description: '', series: 'style', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
        { id: 'ink', name: '沉稳墨', description: '', series: 'style', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
        { id: 'brand-crimson', name: '绛红', description: '', series: 'brand', advanced: true, previewPrimary: '#a01f2e', previewSoft: '#f3e2e4' },
      ],
    })

    const groups = w.findAll('.theme-group')
    expect(groups).toHaveLength(2)
    expect(groups[0].text()).toContain('整体风格')
    expect(groups[1].text()).toContain('品牌配色')
    expect(groups[1].text()).toContain('版式沿用经典蓝')
    expect(groups[0].findAll('.theme-card')).toHaveLength(2)
    expect(groups[1].findAll('.theme-card')).toHaveLength(1)
    // 分组不能把卡片弄丢，也不能打乱顺序（后台清单顺序仍有意义）
    expect(cards(w)).toHaveLength(3)
    expect(cards(w)[2].text()).toContain('绛红')
    expect(cards(w)[2].find('.theme-preview').classes()).toContain('layout-brand-crimson')

    await cards(w)[2].trigger('click')
    await flush()
    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ templateId: 'brand-crimson' })
  })

  // 后端是随二进制升级的：只换前端时旧接口不下发 series。分组逻辑不能因此
  // 让整组卡片消失——宁可都落在「整体风格」组，也不能白屏。
  it('旧后端不下发 series：全部归入整体风格组，卡片不丢失', async () => {
    const w = await mountPage({
      templateId: 'default',
      templateEntitled: true,
      templates: [
        { id: 'default', name: '经典蓝', description: '', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
        { id: 'ink', name: '沉稳墨', description: '', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
      ],
    })

    expect(w.findAll('.theme-group')).toHaveLength(1)
    expect(cards(w)).toHaveLength(2)
  })

  it('当前 templateId 回填为「使用中」', async () => {
    const w = await mountPage({
      templateId: 'ink', templateEntitled: true,
      templates: [
        { id: 'default', name: '经典蓝', description: '', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
        { id: 'ink', name: '沉稳墨', description: '', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
      ],
    })

    expect(cards(w)[1].classes()).toContain('selected')
    expect(cards(w)[1].text()).toContain('使用中')
  })

  it('选另一款后保存：只提交 templateId，不回传其它展示字段', async () => {
    const w = await mountPage()

    await cards(w)[1].trigger('click')
    await flush()
    await saveButton(w).trigger('click')
    await flush()

    expect(cards(w)[1].classes()).toContain('selected')
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ templateId: 'ink' })
    expect(ElMessage.success).toHaveBeenCalledWith('已保存小程序风格模板')
  })

  it('未开通高级模板：显示授权说明，高级款置灰且点不动', async () => {
    const w = await mountPage({
      templateId: 'default', templateEntitled: false,
      templates: [
        { id: 'default', name: '经典蓝', description: '', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
        { id: 'ink', name: '沉稳墨', description: '', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
      ],
    })

    expect(w.find('.el-alert').exists()).toBe(true)
    expect(w.text()).toContain('续费后自动恢复')
    expect(cards(w)[1].classes()).toContain('locked')
    expect(cards(w)[1].text()).toContain('暂不可选')

    await cards(w)[1].trigger('click')
    await flush()
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ templateId: 'default' })
  })

  // 授权到期时后端仍允许「带着同一个 id」保存别的设置，前端不能把已选中的
  // 那款也锁死，否则客户连切回经典蓝都做不到。
  it('授权到期但已选中高级款：该款不锁，仍可保存或切回默认款', async () => {
    const w = await mountPage({
      templateId: 'ink', templateEntitled: false,
      templates: [
        { id: 'default', name: '经典蓝', description: '', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
        { id: 'ink', name: '沉稳墨', description: '', advanced: true, previewPrimary: '#3d4b5c', previewSoft: '#e8edf2' },
      ],
    })

    expect(cards(w)[1].classes()).not.toContain('locked')
    expect(cards(w)[0].classes()).not.toContain('locked')

    await cards(w)[0].trigger('click')
    await flush()
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({ templateId: 'default' })
  })

  it('清单为空：禁用保存，不让客户提交一个空模板 id', async () => {
    const w = await mountPage({ templateId: 'default', templateEntitled: true, templates: [] })

    expect(cards(w)).toHaveLength(0)
    expect(saveButton(w).attributes('disabled')).toBeDefined()
  })

  it('拉取失败：不白屏，仍能重新加载', async () => {
    mocks.getHomeLayout.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    expect(saveButton(w).exists()).toBe(true)
    expect(w.find('.el-button').exists()).toBe(true)
  })

  it('保存失败：不提示成功', async () => {
    mocks.saveHomeLayout.mockRejectedValueOnce(new Error('403'))
    const w = await mountPage()

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
  })
})
