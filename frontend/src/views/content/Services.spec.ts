import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed } from 'vue'
import Services from './Services.vue'

// 业务服务页：列表 + 关键字过滤 + 排序 + 增删改。
// 与业绩分类页共用 useTable / useSortableRows，这里重点测本页特有的三处：
// 摘要渲染（stripHtml）、排序后批量持久化、删除二次确认。
const mocks = vi.hoisted(() => ({
  listServices: vi.fn(async (): Promise<any[]> => [
    { id: 1, title: '中央空调维保', icon: '/uploads/i1.png', content: '<p>季度保养</p>', sort: 1, createdAt: '2026-10-16T15:34:12' },
    { id: 2, title: '配电改造', icon: '', content: '', sort: 2, createdAt: '2026-10-17T09:00:00' },
    { id: 3, title: '电梯年检', icon: '', content: '<p>法定检验</p>', sort: 3, createdAt: '2026-10-18T09:00:00' },
  ]),
  createService: vi.fn(async (_payload: any) => ({ id: 9 })),
  updateService: vi.fn(async (_id: number, _payload?: any) => ({ code: 0 })),
  deleteService: vi.fn(async (_id: number) => ({ code: 0 })),
  sortServices: vi.fn(async (_items: { id: number; sort: number }[]) => ({ code: 0 })),
}))

vi.mock('@/api/content', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn(async () => 'confirm') },
}))
vi.mock('@element-plus/icons-vue', () => ({
  Refresh: {},
  Plus: {},
  Search: {},
  Rank: {},
  ArrowUp: {},
  ArrowDown: {},
}))
// 判断手机端：这里固定为「不是手机」，让桌面端列全部渲染
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  return { useBreakpoint: () => ({ isPhone: ref(false) }) }
})

import { ElMessage, ElMessageBox } from 'element-plus'

let valid = true

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable', 'prefixIcon'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
// disabled 会作为原生属性落到 DOM 上，是断言「首行不能上移 / 末行不能下移」的依据
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'link', 'icon'],
  template: `<button class="el-button" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElForm = defineComponent({
  name: 'ElForm',
  setup(_p, { expose, slots }) {
    expose({
      validate: () =>
        valid ? Promise.resolve(true) : Promise.reject(new Error('validate')),
      clearValidate: () => {},
    })
    // 必须真的渲染默认插槽：写成 h('slot') 会得到一个无效元素，
    // 表单里的输入框全都不进 DOM，断言就变成「对着空气验证」
    return () => h('form', { class: 'el-form' }, slots.default ? slots.default() : [])
  },
})
const ElFormItem = defineComponent({
  name: 'ElFormItem',
  props: ['label', 'prop'],
  template: `<div class="el-form-item"><label>{{ label }}</label><slot /></div>`,
})
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  emits: ['closed'],
  template: `<div v-if="modelValue" class="el-dialog"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  template: `<div class="el-pagination" :data-total="total" />`,
})
const ElInputNumber = defineComponent({
  name: 'ElInputNumber',
  props: ['modelValue', 'min'],
  emits: ['update:modelValue'],
  template: `<input class="el-input-number" type="number" :value="modelValue"
    @input="$emit('update:modelValue', Number($event.target.value))" />`,
})
const ElIcon = defineComponent({ name: 'ElIcon', template: `<i><slot /></i>` })

const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', computed(() => props.row))
    return () => h('div', { class: 'el-table-row' }, slots.default ? slots.default() : [])
  },
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots, expose }) {
    expose({})
    return () =>
      h(
        'div',
        { class: 'el-table' },
        (props.data || []).map((row: any, i: number) =>
          h(TableRow, { key: i, row }, () => (slots.default ? slots.default() : null))
        )
      )
  },
})
const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  props: ['prop', 'label', 'width', 'align', 'fixed', 'minWidth', 'showOverflowTooltip'],
  setup(props, { slots }) {
    const rowRef = inject('tableRow', null) as any
    return () => {
      const row = rowRef ? rowRef.value : null
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})
const ImageThumb = defineComponent({ name: 'ImageThumb', props: ['src', 'width', 'height'], template: `<img class="thumb" />` })
const UploadField = defineComponent({
  name: 'UploadField',
  props: ['modelValue', 'multiple', 'accept', 'libraryType'],
  emits: ['update:modelValue'],
  template: `<button class="upload-field" @click="$emit('update:modelValue', '/uploads/icon.png')" />`,
})
const RichEditor = defineComponent({
  name: 'RichEditor',
  props: ['modelValue', 'height'],
  emits: ['update:modelValue'],
  template: `<textarea class="rich-editor" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-dialog': ElDialog,
      'el-pagination': ElPagination,
      'el-input-number': ElInputNumber,
      ElInputNumber,
      'el-icon': ElIcon,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      ImageThumb,
      UploadField,
      RichEditor,
    },
    directives: { loading: {} },
  },
}

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage() {
  const w = mount(Services, mountOpts)
  await flush()
  return w
}

function byText(w: any, sel: string, text: string) {
  const el = w.findAll(sel).find((b: any) => b.text() === text)
  if (!el) throw new Error(`找不到 ${sel} 文本为「${text}」的元素`)
  return el
}

function rowTitles(w: any): string[] {
  return w.findAll('.el-table-row').map((r: any) => r.find('.el-table-column').text())
}

describe('content/Services', () => {
  beforeEach(() => {
    valid = true
    mocks.listServices.mockClear()
    mocks.createService.mockClear()
    mocks.updateService.mockClear()
    mocks.deleteService.mockClear()
    mocks.sortServices.mockClear()
    ;(ElMessage.success as any).mockClear()
    ;(ElMessageBox.confirm as any).mockClear()
    ;(ElMessageBox.confirm as any).mockImplementation(async () => 'confirm')
  })

  it('挂载即加载列表并按 title 列渲染', async () => {
    const w = await mountPage()

    expect(mocks.listServices).toHaveBeenCalledTimes(1)
    expect(rowTitles(w)).toEqual(['中央空调维保', '配电改造', '电梯年检'])
  })

  it('关键字按标题过滤（大小写不敏感）', async () => {
    const w = await mountPage()

    await w.find('.el-input').setValue('空调')
    await flush()

    expect(rowTitles(w)).toEqual(['中央空调维保'])
  })

  // 摘要要去标签、压空白、截断——直接把 HTML 丢进单元格会撑破表格
  it('内容摘要去掉 HTML 标签并截断', async () => {
    const w = await mountPage()

    expect(w.findAll('.muted').map((m: any) => m.text())).toContain('季度保养')
  })

  it('内容为空时摘要显示占位而不是空白单元格', async () => {
    const w = await mountPage()

    expect(w.findAll('.muted').map((m: any) => m.text())).toContain('（空）')
  })

  it('首行不能上移、末行不能下移', async () => {
    const w = await mountPage()
    const rows = w.findAll('.el-table-row')

    const firstUp = rows[0].findAll('.el-button')[0]
    const firstDown = rows[0].findAll('.el-button')[1]
    const lastUp = rows[rows.length - 1].findAll('.el-button')[0]
    const lastDown = rows[rows.length - 1].findAll('.el-button')[1]

    expect(firstUp.attributes('disabled')).toBeDefined()
    expect(firstDown.attributes('disabled')).toBeUndefined()
    expect(lastUp.attributes('disabled')).toBeUndefined()
    expect(lastDown.attributes('disabled')).toBeDefined()
  })

  // 重排后必须按新顺序批量重算 sort 再落库，否则刷新又回到旧顺序
  it('下移一行：重排并批量持久化新 sort', async () => {
    const w = await mountPage()
    const rows = w.findAll('.el-table-row')

    await rows[0].findAll('.el-button')[1].trigger('click') // 首行下移
    await flush()

    expect(mocks.sortServices).toHaveBeenCalledWith([
      { id: 2, sort: 1 },
      { id: 1, sort: 2 },
      { id: 3, sort: 3 },
    ])
    expect(rowTitles(w)).toEqual(['配电改造', '中央空调维保', '电梯年检'])
  })

  it('新增：打开空表单', async () => {
    const w = await mountPage()
    await byText(w, '.el-button', '新增服务').trigger('click')
    await flush()

    expect(w.find('.el-dialog').exists()).toBe(true)
    expect(w.find('.el-dialog h3').text()).toBe('新增服务')
    // 弹窗内的标题输入框是 DOM 里最后一个 .el-input（搜索框在工具栏，先渲染）
    const inputs = w.findAll('.el-input')
    expect((inputs[inputs.length - 1].element as HTMLInputElement).value).toBe('')
  })

  // 校验不过绝不能提交：空标题的服务在小程序里是一张空白卡
  it('新增：校验不通过时不提交', async () => {
    valid = false
    const w = await mountPage()
    await byText(w, '.el-button', '新增服务').trigger('click')
    await flush()

    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.createService).not.toHaveBeenCalled()
  })

  it('新增成功：提交表单、提示、关闭弹窗并重新加载', async () => {
    const w = await mountPage()
    await byText(w, '.el-button', '新增服务').trigger('click')
    await flush()

    const inputs = w.findAll('.el-input')
    await inputs[inputs.length - 1].setValue('新服务')
    await w.find('input[type="number"]').setValue(5)
    await flush()
    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.createService).toHaveBeenCalledWith(
      expect.objectContaining({ title: '新服务', sort: 5 })
    )
    expect(ElMessage.success).toHaveBeenCalledWith('已新增')
    expect(w.find('.el-dialog').exists()).toBe(false)
    expect(mocks.listServices).toHaveBeenCalledTimes(2)
  })

  it('编辑：回填该行并走 updateService', async () => {
    const w = await mountPage()
    await w.findAll('.el-table-row')[0].findAll('.el-button')[2].trigger('click') // 「编辑」
    await flush()

    expect(w.find('.el-dialog h3').text()).toBe('编辑服务')

    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.updateService).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ title: '中央空调维保', content: '<p>季度保养</p>' })
    )
    expect(ElMessage.success).toHaveBeenCalledWith('已更新')
  })

  // 删除是不可逆动作，二次确认点「取消」必须什么都不做
  it('删除：取消确认框则不调用删除接口', async () => {
    ;(ElMessageBox.confirm as any).mockRejectedValueOnce(new Error('cancel'))
    const w = await mountPage()

    await w.findAll('.el-table-row')[0].findAll('.el-button')[3].trigger('click') // 「删除」
    await flush()

    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(mocks.deleteService).not.toHaveBeenCalled()
  })

  it('删除：确认后调用接口、提示并重新加载', async () => {
    const w = await mountPage()

    await w.findAll('.el-table-row')[0].findAll('.el-button')[3].trigger('click')
    await flush()

    expect(mocks.deleteService).toHaveBeenCalledWith(1)
    expect(ElMessage.success).toHaveBeenCalledWith('已删除')
    expect(mocks.listServices).toHaveBeenCalledTimes(2)
  })

  it('列表加载失败：不崩，表格仍在（可点刷新重试）', async () => {
    mocks.listServices.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    expect(w.find('.el-table').exists()).toBe(true)
    expect(byText(w, '.el-button', '刷新').exists()).toBe(true)
  })
})
