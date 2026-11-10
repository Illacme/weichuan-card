import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import MpTheme from './MpTheme.vue'

// 「整体风格」页：核心约束是**只提交 templateId**。
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
      { id: 'warm', name: '暖金棕', description: '温暖金棕', advanced: true, previewPrimary: '#a5742a', previewSoft: '#f7efdf' },
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
    expect(cards(w)).toHaveLength(3)
    expect(cards(w)[0].text()).toContain('经典蓝')
    expect(cards(w)[1].text()).toContain('沉稳墨')
    expect(cards(w)[2].text()).toContain('暖金棕')
    expect(cards(w)[0].find('.el-tag').exists()).toBe(false)
    expect(cards(w)[1].find('.el-tag').text()).toBe('高级')
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
    expect(ElMessage.success).toHaveBeenCalledWith('已保存小程序整体风格')
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
