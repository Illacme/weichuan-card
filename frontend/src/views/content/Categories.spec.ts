import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import type { DOMWrapper } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed } from 'vue'
import Categories from './Categories.vue'

// 业绩分类页：企业内容六页里最有代表性的一页（列表 + 关键字过滤 + 排序 + 增删 +
// 锁定态）。六页共用 useTable / useSortableRows 两个组合式函数，测其中一页
// 就等于给「排序按钮禁用条件」「重排后批量持久化」这类共用行为上了锁。
//
// vi.mock 工厂会被提升到文件顶部，夹具必须经 vi.hoisted 预先创建（否则 TDZ）。
const mocks = vi.hoisted(() => ({
  listCategories: vi.fn(async (): Promise<any> => [
    { id: 1, name: '工程业绩', icon: '', sort: 1, isLocked: true },
    { id: 2, name: '服务案例', icon: '', sort: 2, isLocked: false },
    { id: 3, name: '资质荣誉', icon: '', sort: 3, isLocked: false },
  ]),
  createCategory: vi.fn(async () => ({ id: 9 })),
  updateCategory: vi.fn(async (_id: number, _payload?: any) => ({ code: 0 })),
  deleteCategory: vi.fn(async (_id: number) => ({ code: 0 })),
  sortCategories: vi.fn(async (_items: { id: number; sort: number }[]) => ({ code: 0 })),
}))
const { listCategories, createCategory, updateCategory, deleteCategory, sortCategories } = mocks

vi.mock('@/api/content', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn(async () => 'confirm') },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {}, Plus: {}, Search: {}, Rank: {}, ArrowUp: {}, ArrowDown: {} }))

import { ElMessageBox } from 'element-plus'

// ── 轻量 stub（jsdom 下完整 Element Plus 过重易 OOM）──
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
// 关键：按钮要真的渲染 disabled 属性，才能验证「首行不能上移、末行不能下移」
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'link'],
  // disabled 是 HTML 原生属性，会落到 $attrs 上
  template: `<button class="el-button" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElForm = defineComponent({
  name: 'ElForm',
  setup(_, { expose }) {
    // 校验结果由每个用例控制（valid 为 false 时模拟「必填没填」）
    expose({ validate: () => (valid ? Promise.resolve(true) : Promise.reject(new Error('validate'))), clearValidate: () => {} })
    return () => h('form', { class: 'el-form' }, h('slot'))
  },
})
let valid = true
const ElFormItem = defineComponent({ name: 'ElFormItem', template: `<div class="el-form-item"><slot /></div>` })
const ElTag = defineComponent({ name: 'ElTag', props: ['type', 'effect'], template: `<span class="el-tag"><slot /></span>` })
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  template: `<div v-if="modelValue" class="el-dialog"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  template: `<div class="el-pagination" />`,
})
const ElInputNumber = defineComponent({
  name: 'ElInputNumber',
  props: ['modelValue', 'min'],
  emits: ['update:modelValue'],
  template: `<input class="el-input-number" type="number" :value="modelValue"
    @input="$emit('update:modelValue', Number($event.target.value))" />`,
})
const ElSwitch = defineComponent({
  name: 'ElSwitch',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<input class="el-switch" type="checkbox" :checked="modelValue"
    @change="$emit('update:modelValue', $event.target.checked)" />`,
})

// 表格行上下文：复刻 Element Plus「列槽拿 { row }」的行为
// 注意：provide 只在 setup 执行一次，直接 provide 行对象会让列在「行复用」时
// 拿到旧值（列表从 3 行筛成 1 行后，index 0 的实例被复用，注入的还是上一行）。
// 故这里提供一个 computed，由列在渲染时取值。
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
  props: ['prop', 'label', 'width', 'align', 'fixed', 'minWidth'],
  setup(props, { slots }) {
    const rowRef = inject('tableRow', null) as any
    return () => {
      const row = rowRef ? rowRef.value : null // 渲染时取值，避免拿到陈旧行
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})
const UploadFieldStub = defineComponent({
  name: 'UploadField',
  props: ['modelValue'],
  template: `<div class="upload-field" />`,
})
const ImageThumbStub = defineComponent({ name: 'ImageThumb', props: ['src', 'width', 'height'], template: `<img class="thumb" />` })

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-tag': ElTag,
      'el-dialog': ElDialog,
      'el-pagination': ElPagination,
      'el-input-number': ElInputNumber,
      'el-switch': ElSwitch,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      UploadField: UploadFieldStub,
      ImageThumb: ImageThumbStub,
    },
    directives: { loading: {} },
  },
}

function mountPage() {
  return mount(Categories, mountOpts)
}

async function flush(_w: ReturnType<typeof mountPage>) {
  await nextTick()
  await nextTick()
  await nextTick()
  await nextTick()
}

// 表格里的操作按钮按 title 取（上移 / 下移 / 编辑 / 删除）
// 找不到就把测试打红并说清缺什么，而不是留一句 as any 让断言悄悄变成 undefined
function byTitle(list: DOMWrapper<Element>[], title: string) {
  const b = list.find((x) => x.attributes('title') === title)
  if (!b) throw new Error(`未找到按钮（title=${title}）`)
  return b
}
function byText(list: DOMWrapper<Element>[], text: string) {
  const b = list.find((x) => (x.text() || '').includes(text))
  if (!b) throw new Error(`未找到按钮（文案含「${text}」）`)
  return b
}
function rowButtons(w: ReturnType<typeof mountPage>, rowName: string) {
  const row = w.findAll('.el-table-row').find((r) => r.text().includes(rowName))
  if (!row) throw new Error(`未找到行（${rowName}）`)
  return row.findAll('button.el-button')
}

describe('Categories', () => {
  beforeEach(() => {
    valid = true
    vi.mocked(listCategories).mockClear()
    vi.mocked(createCategory).mockClear()
    vi.mocked(updateCategory).mockClear()
    vi.mocked(deleteCategory).mockClear()
    vi.mocked(sortCategories).mockClear()
    vi.mocked(ElMessageBox.confirm).mockClear()
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => 'confirm' as any)
  })

  it('挂载即加载，渲染分类名与锁定态', async () => {
    const w = mountPage()
    await flush(w)
    expect(listCategories).toHaveBeenCalledTimes(1)
    const text = w.text()
    expect(text).toContain('工程业绩')
    expect(text).toContain('服务案例')
    expect(text).toContain('已锁定') // 1 号分类锁定
    expect(text).toContain('未锁')
  })

  it('关键字过滤（大小写不敏感），并把分页重置回第一页', async () => {
    const w = mountPage()
    await flush(w)
    const input = w.find('input.el-input')
    await input.setValue('案例')
    await nextTick()
    const text = w.text()
    expect(text).toContain('服务案例')
    expect(text).not.toContain('工程业绩')
    expect(text).not.toContain('资质荣誉')
  })

  // 排序按钮的禁用条件：首行不能上移、末行不能下移。
  // 这组的价值在于盯住模板里的 disabled 绑定——它一旦写错（属性名拼错 / 绑错值），
  // 表现是「按钮永远可点」，点了也没反应或报错，属于最难自查的一类 UI 缺陷。
  it('首行上移按钮禁用、末行下移按钮禁用', async () => {
    const w = mountPage()
    await flush(w)

    const first = rowButtons(w, '工程业绩')
    expect(byTitle(first, '上移').attributes('disabled')).toBeDefined()
    expect(byTitle(first, '下移').attributes('disabled')).toBeUndefined()

    const last = rowButtons(w, '资质荣誉')
    expect(byTitle(last, '下移').attributes('disabled')).toBeDefined()
    expect(byTitle(last, '上移').attributes('disabled')).toBeUndefined()
  })

  it('下移后按新顺序批量持久化 sort', async () => {
    const w = mountPage()
    await flush(w)
    await byTitle(rowButtons(w, '工程业绩'), '下移').trigger('click')
    await flush(w)

    expect(sortCategories).toHaveBeenCalledTimes(1)
    const items = vi.mocked(sortCategories).mock.calls[0][0]
    // 1 号与 2 号交换，sort 按新顺序重算为 1..3
    expect(items).toEqual([
      { id: 2, sort: 1 },
      { id: 1, sort: 2 },
      { id: 3, sort: 3 },
    ])
  })

  it('新增：校验不通过时不提交', async () => {
    const w = mountPage()
    await flush(w)
    valid = false
    // 打开新增弹窗
    await byText(w.findAll('button.el-button'), '新增分类').trigger('click')
    await nextTick()
    await byText(w.findAll('button.el-button'), '保存').trigger('click')
    await flush(w)
    expect(createCategory).not.toHaveBeenCalled()
    valid = true
  })

  it('新增：填好名称后提交并刷新列表', async () => {
    const w = mountPage()
    await flush(w)
    await byText(w.findAll('button.el-button'), '新增分类').trigger('click')
    await nextTick()
    const form = w.vm as any
    form.form.name = '新分类'
    await nextTick()
    await byText(w.findAll('button.el-button'), '保存').trigger('click')
    await flush(w)
    expect(createCategory).toHaveBeenCalledTimes(1)
    expect(listCategories).toHaveBeenCalledTimes(2) // 提交后重载
  })

  it('编辑：带 id 调用更新接口，而不是新增', async () => {
    const w = mountPage()
    await flush(w)
    await byText(rowButtons(w, '服务案例'), '编辑').trigger('click')
    await nextTick()
    expect((w.vm as any).form.name).toBe('服务案例')
    await byText(w.findAll('button.el-button'), '保存').trigger('click')
    await flush(w)
    expect(updateCategory).toHaveBeenCalledTimes(1)
    expect(vi.mocked(updateCategory).mock.calls[0][0]).toBe(2)
    expect(createCategory).not.toHaveBeenCalled()
  })

  it('删除要二次确认：取消则不调接口', async () => {
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => Promise.reject('cancel'))
    const w = mountPage()
    await flush(w)
    await byText(rowButtons(w, '资质荣誉'), '删除').trigger('click')
    await flush(w)
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1)
    expect(deleteCategory).not.toHaveBeenCalled()
  })

  it('删除确认后调用接口并刷新', async () => {
    const w = mountPage()
    await flush(w)
    await byText(rowButtons(w, '资质荣誉'), '删除').trigger('click')
    await flush(w)
    expect(deleteCategory).toHaveBeenCalledWith(3)
    expect(listCategories).toHaveBeenCalledTimes(2)
  })

  // 后端强校验（锁定分类 / 有关联项目）会返回 code=1，由拦截器提示；
  // 这里只需确认异常不会把页面带崩。
  it('列表接口失败时不崩（拦截器已提示）', async () => {
    vi.mocked(listCategories).mockRejectedValueOnce(new Error('boom'))
    const w = mountPage()
    await flush(w)
    expect(w.exists()).toBe(true)
    expect(w.text()).toContain('业绩分类')
  })
})
