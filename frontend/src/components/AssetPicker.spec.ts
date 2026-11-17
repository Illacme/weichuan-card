import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, provide, inject } from 'vue'
import AssetPicker from './AssetPicker.vue'

// 素材选择器：mock 掉素材 API（清单 / 采用）与 UI 反馈层；
// 预览图 URL 拼接逻辑原地保留（返回可断言的路由）。
const mocks = vi.hoisted(() => ({
  listLibrary: vi.fn(),
  adoptAsset: vi.fn(),
}))
const { listLibrary, adoptAsset } = mocks
const SIGNAL = expect.objectContaining({
  signal: expect.objectContaining({ aborted: expect.any(Boolean) }),
})

vi.mock('@/api/assets', () => ({
  listLibrary: mocks.listLibrary,
  adoptAsset: mocks.adoptAsset,
  libraryFileUrl: (rel: string) => '/api/assets/file/' + rel,
}))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
// 图标仅作为 prop 值传递（:prefix-icon / 卡片角标），不会真正渲染，桩成普通对象即可
vi.mock('@element-plus/icons-vue', () => ({ Check: {}, Loading: {}, Search: {} }))

import { ElMessage } from 'element-plus'

// ── 轻量 stub（jsdom 下完整 Element Plus 过重易 OOM） ──
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width', 'top', 'closeOnClickModal'],
  emits: ['update:modelValue'],
  template: `<div v-if="modelValue" class="el-dialog"><slot /><slot name="footer" /></div>`,
})
// radio-group / radio-button 通过 provide/inject 打通，让点击子按钮能触发
// group 的 update:modelValue + change——否则 tab / 风格切换在测试里点不动。
const ElRadioGroup = defineComponent({
  name: 'ElRadioGroup',
  props: ['modelValue'],
  emits: ['update:modelValue', 'change'],
  setup(_props, { emit }) {
    provide('radioGroup', {
      setValue(v: string) {
        emit('update:modelValue', v)
        emit('change', v)
      },
    })
    return {}
  },
  template: `<div class="el-radio-group"><slot /></div>`,
})
const ElRadioButton = defineComponent({
  name: 'ElRadioButton',
  props: ['label'],
  setup() {
    return { group: inject<any>('radioGroup') }
  },
  template: `<button class="el-radio-button" @click="group?.setValue(label)"><slot /></button>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'disabled'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElEmpty = defineComponent({
  name: 'ElEmpty',
  props: ['description', 'imageSize'],
  template: `<div class="el-empty" />`,
})
// 分页器：只关心 current-change 事件，桩成上一页/下一页两个按钮
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['layout', 'currentPage', 'pageSize', 'total', 'pagerCount'],
  emits: ['current-change'],
  template: `<div class="el-pagination">
    <button class="pg-prev" @click="$emit('current-change', currentPage - 1)">上一页</button>
    <button class="pg-next" @click="$emit('current-change', currentPage + 1)">下一页</button>
  </div>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-dialog': ElDialog,
      'el-radio-group': ElRadioGroup,
      'el-radio-button': ElRadioButton,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-empty': ElEmpty,
      'el-pagination': ElPagination,
    },
    directives: { loading: {} },
  },
}

function item(id: string, name: string, category: string, tags: string[] = [], style = 'clean') {
  return {
    id,
    name,
    category,
    style,
    file: `${category}s/${id}.png`,
    thumb: `${category}s/${id}.png`,
    width: 256,
    height: 256,
    tags,
  }
}

// 30 项（26 图标 + 封面/LOGO/背景各若干）→ size=15 时共 2 页，可测翻页。
// 非图标素材刻意分散到不同调性，用于验证风格筛选行。
const LIB_ITEMS = [
  ...Array.from({ length: 26 }, (_, i) =>
    item(`icon-${i + 1}`, `图标${i + 1}`, 'icon', ['图标'])
  ),
  item('cover-skyline', '天际线', 'cover', ['封面'], 'nature'),
  item('logo-building', '建筑', 'logo', ['LOGO'], 'luxury'),
  item('bg-deepspace', '深空', 'background', ['背景'], 'clean'),
  item('bg-glow', '光晕', 'background', ['背景'], 'vivid'),
]

const CATEGORIES = [
  { key: 'icon', name: '图标' },
  { key: 'cover', name: '封面' },
  { key: 'logo', name: 'LOGO' },
  { key: 'avatar', name: '头像' },
  { key: 'background', name: '背景' },
]

const STYLES = [
  { key: 'clean', name: '简约清晰' },
  { key: 'nature', name: '温暖自然' },
  { key: 'luxury', name: '轻奢尊贵' },
  { key: 'soft', name: '柔美清新' },
  { key: 'vivid', name: '光影流动' },
]

// 模拟服务端：按 type / style / keyword 过滤后分页（与 backend assets.List 行为一致）。
// styleCounts 在「分类+关键字」结果上统计（不含 style 本身），供前端隐藏 0 结果的调性。
function fakeList(params: Record<string, any> = {}) {
  const page = Number(params?.page) || 1
  const size = Number(params?.size) || 15
  const type = params?.type && params.type !== 'all' ? params.type : ''
  const style = params?.style && params.style !== 'all' ? params.style : ''
  const kw = String(params?.keyword || '').trim().toLowerCase()
  const base = LIB_ITEMS.filter((it) => {
    if (type && it.category !== type) return false
    if (kw) {
      const hay = [it.name, ...it.tags].join(' ').toLowerCase()
      if (!hay.includes(kw)) return false
    }
    return true
  })
  const styleCounts = STYLES.map((s) => ({
    ...s,
    count: base.filter((it) => it.style === s.key).length,
  }))
  const matched = style ? base.filter((it) => it.style === style) : base
  const total = matched.length
  const pages = Math.max(1, Math.ceil(total / size))
  const start = (page - 1) * size
  return Promise.resolve({
    code: 0,
    data: {
      version: 5,
      categories: CATEGORIES,
      styles: STYLES,
      items: matched.slice(start, start + size),
      total,
      page,
      size,
      pages,
      styleCounts,
    },
  })
}

describe('AssetPicker', () => {
  beforeEach(() => {
    mocks.listLibrary.mockReset()
    mocks.adoptAsset.mockReset()
    mocks.listLibrary.mockImplementation(fakeList as any)
    mocks.adoptAsset.mockResolvedValue({
      code: 0,
      data: { url: 'http://cdn/uploads/library/icon-1.png' },
    })
  })

  it('打开时拉取第一页并渲染素材卡片（页码与总数回填）', async () => {
    const w = mount(AssetPicker, { props: { modelValue: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    // 首页按 size=15 截断（5 列 × 3 排），footer 显示总数 30
    expect(w.findAll('.asset-card')).toHaveLength(15)
    expect(w.text()).toContain('共 30 项')
    expect(listLibrary).toHaveBeenCalledWith(expect.objectContaining({ page: 1 }), SIGNAL)
  })

  it('defaultCategory 定位到对应分类（请求带 type，渲染该分类素材）', async () => {
    const w = mount(AssetPicker, {
      props: { modelValue: true, defaultCategory: 'background' },
      ...mountOpts,
    })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    expect(listLibrary).toHaveBeenCalledWith(expect.objectContaining({ type: 'background' }), SIGNAL)
    expect(w.findAll('.asset-card')).toHaveLength(2)
    expect(w.text()).toContain('深空')
    expect(w.text()).toContain('光晕')
  })

  it('单选：点卡片即采用并回传 URL、关闭弹窗', async () => {
    const w = mount(AssetPicker, { props: { modelValue: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    await w.findAll('.asset-card')[0].trigger('click')
    await vi.waitFor(() => expect(adoptAsset).toHaveBeenCalledWith('icon-1'))
    expect(w.emitted('picked')![0]).toEqual(['http://cdn/uploads/library/icon-1.png'])
    expect(w.emitted('update:modelValue')![0]).toEqual([false])
  })

  it('多选：点卡片累计选择，footer 批量采用后回传 URL 数组', async () => {
    adoptAsset
      .mockResolvedValueOnce({ code: 0, data: { url: 'http://cdn/a.png' } })
      .mockResolvedValueOnce({ code: 0, data: { url: 'http://cdn/b.png' } })
    const w = mount(AssetPicker, { props: { modelValue: true, multiple: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    await w.findAll('.asset-card')[0].trigger('click')
    await w.findAll('.asset-card')[1].trigger('click')
    await nextTick()
    expect(w.findAll('.asset-card.selected')).toHaveLength(2)
    expect(adoptAsset).not.toHaveBeenCalled()
    const addBtn = w.findAll('.el-button').find((b) => b.text().includes('添加所选'))
    expect(addBtn).toBeTruthy()
    await addBtn!.trigger('click')
    await vi.waitFor(() => expect(adoptAsset).toHaveBeenCalledTimes(2))
    expect(adoptAsset).toHaveBeenCalledWith('icon-1')
    expect(adoptAsset).toHaveBeenCalledWith('icon-2')
    expect(w.emitted('picked')![0]).toEqual([['http://cdn/a.png', 'http://cdn/b.png']])
  })

  it('多选超限：第 limit+1 次点选被拦截并提示', async () => {
    const w = mount(AssetPicker, {
      props: { modelValue: true, multiple: true, limit: 1 },
      ...mountOpts,
    })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    await w.findAll('.asset-card')[0].trigger('click')
    await w.findAll('.asset-card')[1].trigger('click')
    await nextTick()
    expect(w.findAll('.asset-card.selected')).toHaveLength(1)
    expect(ElMessage.warning).toHaveBeenCalledWith('最多选择 1 个')
  })

  it('翻页：触发分页器后带新页码重新请求', async () => {
    const w = mount(AssetPicker, { props: { modelValue: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    await w.find('.pg-next').trigger('click')
    await vi.waitFor(() =>
      expect(listLibrary).toHaveBeenCalledWith(expect.objectContaining({ page: 2 }), SIGNAL)
    )
    await nextTick()
    await nextTick()
    // 第二页 15 项（30 - 15）
    expect(w.findAll('.asset-card')).toHaveLength(15)
  })

  it('搜索关键字（防抖）走服务端过滤：请求带 keyword 并回填结果', async () => {
    const w = mount(AssetPicker, { props: { modelValue: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    await w.find('.el-input').setValue('深空')
    // 300ms 防抖：等待期间不应立即请求，防抖后再断言
    await vi.waitFor(
      () =>
        expect(listLibrary).toHaveBeenCalledWith(
          expect.objectContaining({ keyword: '深空', page: 1 }),
          SIGNAL,
        ),
      { timeout: 2000 }
    )
    await nextTick()
    await nextTick()
    expect(w.findAll('.asset-card')).toHaveLength(1)
    expect(w.text()).toContain('深空')
  })

  it('风格行只渲染当前条件下有素材的调性（0 项的不显示）', async () => {
    const w = mount(AssetPicker, { props: { modelValue: true }, ...mountOpts })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    const bar = w.find('.picker-styles')
    expect(bar.exists()).toBe(true)
    const txt = bar.text()
    expect(txt).toContain('简约清晰')
    expect(txt).toContain('温暖自然')
    expect(txt).toContain('轻奢尊贵')
    expect(txt).toContain('光影流动')
    expect(txt).not.toContain('柔美清新') // 全库 0 项，不应占一个档位
  })

  it('切换风格：请求带 style 且只回填该调性的素材', async () => {
    const w = mount(AssetPicker, {
      props: { modelValue: true, defaultCategory: 'background' },
      ...mountOpts,
    })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    const btn = w.findAll('.el-radio-button').find((b) => b.text().includes('光影流动'))
    expect(btn).toBeTruthy()
    await btn!.trigger('click')
    await vi.waitFor(() =>
      expect(listLibrary).toHaveBeenCalledWith(
        expect.objectContaining({ style: 'vivid', page: 1 }),
        SIGNAL,
      )
    )
    await nextTick()
    await nextTick()
    expect(w.findAll('.asset-card')).toHaveLength(1)
    expect(w.text()).toContain('光晕')
    expect(w.text()).not.toContain('深空')
  })

  it('单一调性的分类隐藏风格行（图标全部为简约清晰）', async () => {
    const w = mount(AssetPicker, {
      props: { modelValue: true, defaultCategory: 'icon' },
      ...mountOpts,
    })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    expect(w.find('.picker-styles').exists()).toBe(false)
  })

  it('已选调性在新条件下无素材时自动回落「全部」，不停在空结果页', async () => {
    const w = mount(AssetPicker, {
      props: { modelValue: true, defaultCategory: 'background' },
      ...mountOpts,
    })
    await vi.waitFor(() => expect(listLibrary).toHaveBeenCalled())
    await nextTick()
    await nextTick()
    const vividBtn = w.findAll('.el-radio-button').find((b) => b.text().includes('光影流动'))
    await vividBtn!.trigger('click')
    await vi.waitFor(() =>
      expect(listLibrary).toHaveBeenCalledWith(expect.objectContaining({ style: 'vivid' }), SIGNAL)
    )
    await nextTick()
    await nextTick()
    // 切到图标分类：该调性下无素材 → 自动回落 all 并重新拉取
    const iconBtn = w.findAll('.el-radio-button').find((b) => b.text().trim() === '图标')
    await iconBtn!.trigger('click')
    await vi.waitFor(() =>
      expect(listLibrary).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'icon', style: 'all' }),
        SIGNAL,
      )
    )
    await nextTick()
    await nextTick()
    expect(w.findAll('.asset-card').length).toBeGreaterThan(0)
  })
})
