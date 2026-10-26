import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed } from 'vue'
import Projects from './Projects.vue'

// 项目案例页：企业内容里字段最多的一页（分类 / 精选 / 上架 / 封面 / 相册 / 富文本）。
// 三处容易错的地方全部钉住：
// 1) 提交 payload 必须恰好九个字段（漏 enabled 会让下架状态莫名恢复）；
// 2) 编辑回填相册要拷贝，否则改表单会直接改到列表行上的数组（取消也回不去）；
// 3) 分类名靠 categories 反查，分类被删时要显示占位而不是空白。
const mocks = vi.hoisted(() => ({
  listProjects: vi.fn(async (): Promise<any[]> => [
    { id: 1, categoryId: 1, name: '厂房配电改造', summary: '简介', detail: '<p>详情</p>', cover: '/uploads/c1.jpg', album: ['/uploads/a1.jpg'], isHomeFeatured: true, enabled: true, sort: 1 },
    { id: 2, categoryId: 2, name: '中央空调工程', summary: '', detail: '', cover: '', album: [], isHomeFeatured: false, enabled: false, sort: 2 },
    { id: 3, categoryId: 99, name: '历史项目', summary: '', detail: '', cover: '', album: [], isHomeFeatured: false, enabled: true, sort: 3 },
  ]),
  listCategories: vi.fn(async (): Promise<any[]> => [
    { id: 1, name: '机电安装' },
    { id: 2, name: '暖通空调' },
  ]),
  createProject: vi.fn(async (_payload: any) => ({ id: 9 })),
  updateProject: vi.fn(async (_id: number, _payload?: any) => ({ code: 0 })),
  deleteProject: vi.fn(async (_id: number) => ({ code: 0 })),
  sortProjects: vi.fn(async (_items: { id: number; sort: number }[]) => ({ code: 0 })),
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
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  return { useBreakpoint: () => ({ isPhone: ref(false) }) }
})

import { ElMessage, ElMessageBox } from 'element-plus'

let valid = true

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable', 'prefixIcon', 'type', 'rows'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
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
      validate: () => (valid ? Promise.resolve(true) : Promise.reject(new Error('validate'))),
      clearValidate: () => {},
    })
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
  props: ['modelValue', 'title', 'width', 'appendToBody'],
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
// 两个下拉（筛选 / 弹窗内分类）共用；空选项对应 clearable 清空
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue', 'placeholder', 'clearable'],
  emits: ['update:modelValue'],
  template: `<select class="el-select" :placeholder="placeholder"
    @change="$emit('update:modelValue', $event.target.value === '' ? undefined : Number($event.target.value))">
    <slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'effect'],
  template: `<span class="el-tag"><slot /></span>`,
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
const ImageThumb = defineComponent({
  name: 'ImageThumb',
  props: ['src', 'width', 'height'],
  template: `<img class="thumb" />`,
})
const UploadField = defineComponent({
  name: 'UploadField',
  props: ['modelValue', 'multiple', 'limit', 'accept', 'libraryType'],
  emits: ['update:modelValue'],
  template: `<button class="upload-field" @click="$emit('update:modelValue', multiple ? ['/uploads/x1.jpg'] : '/uploads/x.jpg')" />`,
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
      'el-switch': ElSwitch,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-tag': ElTag,
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
  const w = mount(Projects, mountOpts)
  await flush()
  return w
}

function byText(w: any, sel: string, text: string) {
  const el = w.findAll(sel).find((b: any) => b.text() === text)
  if (!el) throw new Error(`找不到 ${sel} 文本为「${text}」的元素`)
  return el
}

function rowNames(w: any): string[] {
  return w.findAll('.el-table-row').map((r: any) => r.find('.el-table-column').text())
}

function columnTexts(w: any, rowIndex: number): string[] {
  return w.findAll('.el-table-row')[rowIndex].findAll('.el-table-column').map((c: any) => c.text())
}

// 页面上一共三个 el-input：工具栏「搜索案例名」、弹窗「名称」、弹窗「简介」。
// 名称框固定是第 2 个（简介是 textarea，stub 里也带同样的 class）。
const NAME_INPUT_INDEX = 1

function nameInput(w: any) {
  const el = w.findAll('.el-input')[NAME_INPUT_INDEX]
  if (!el) throw new Error('找不到「名称」输入框')
  return el
}

describe('content/Projects', () => {
  beforeEach(() => {
    valid = true
    mocks.listProjects.mockClear()
    mocks.listCategories.mockClear()
    mocks.createProject.mockClear()
    mocks.updateProject.mockClear()
    mocks.deleteProject.mockClear()
    mocks.sortProjects.mockClear()
    ;(ElMessage.success as any).mockClear()
    ;(ElMessageBox.confirm as any).mockImplementation(async () => 'confirm')
  })

  it('挂载并行加载案例与分类', async () => {
    const w = await mountPage()

    expect(mocks.listProjects).toHaveBeenCalledTimes(1)
    expect(mocks.listCategories).toHaveBeenCalledTimes(1)
    expect(rowNames(w)).toEqual(['厂房配电改造', '中央空调工程', '历史项目'])
  })

  // 列表只存 categoryId，展示要反查分类名；分类被删时不能空白
  it('分类列反查分类名，找不到时显示占位', async () => {
    const w = await mountPage()

    expect(columnTexts(w, 0)[1]).toBe('机电安装')
    expect(columnTexts(w, 1)[1]).toBe('暖通空调')
    expect(columnTexts(w, 2)[1]).toBe('—')
  })

  it('精选与上架两列按布尔值出文案', async () => {
    const w = await mountPage()

    expect(columnTexts(w, 0)).toContain('精选')
    expect(columnTexts(w, 0)).toContain('上架')
    expect(columnTexts(w, 1)).toContain('普通')
    expect(columnTexts(w, 1)).toContain('下架')
  })

  it('按分类筛选只留该分类的案例', async () => {
    const w = await mountPage()

    await w.find('.el-select').setValue(1)
    await flush()

    expect(rowNames(w)).toEqual(['厂房配电改造'])
  })

  it('关键字按案例名过滤', async () => {
    const w = await mountPage()

    await w.find('.el-input').setValue('空调')
    await flush()

    expect(rowNames(w)).toEqual(['中央空调工程'])
  })

  // 校验两道：名称必填 + 必须选分类（没分类的案例在小程序里无处归类）
  it('新增：校验不通过时不提交', async () => {
    valid = false
    const w = await mountPage()
    await byText(w, '.el-button', '新增案例').trigger('click')
    await flush()

    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.createProject).not.toHaveBeenCalled()
  })

  // 恰好九个字段：漏一个后端就按零值处理（enabled 漏掉会让下架案例莫名上架）
  it('新增成功：提交完整 payload 且提示、关窗、重载', async () => {
    const w = await mountPage()
    await byText(w, '.el-button', '新增案例').trigger('click')
    await flush()

    await nameInput(w).setValue('新案例')
    const selects = w.findAll('.el-select')
    await selects[selects.length - 1].setValue(2)
    await flush()
    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.createProject).toHaveBeenCalledTimes(1)
    const payload = mocks.createProject.mock.calls[0][0]
    expect(Object.keys(payload).sort()).toEqual([
      'album',
      'categoryId',
      'cover',
      'detail',
      'enabled',
      'isHomeFeatured',
      'name',
      'sort',
      'summary',
    ])
    expect(payload).toMatchObject({ name: '新案例', categoryId: 2, enabled: true, album: [] })
    expect(ElMessage.success).toHaveBeenCalledWith('已新增')
    expect(w.find('.el-dialog').exists()).toBe(false)
    expect(mocks.listProjects).toHaveBeenCalledTimes(2)
  })

  it('编辑：回填该行并走 updateProject', async () => {
    const w = await mountPage()
    await w.findAll('.el-table-row')[0].findAll('.el-button')[2].trigger('click')
    await flush()

    expect(w.find('.el-dialog h3').text()).toBe('编辑案例')

    expect((nameInput(w).element as HTMLInputElement).value).toBe('厂房配电改造')

    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    expect(mocks.updateProject).toHaveBeenCalledWith(
      1,
      expect.objectContaining({
        name: '厂房配电改造',
        categoryId: 1,
        isHomeFeatured: true,
        enabled: true,
        album: ['/uploads/a1.jpg'],
      })
    )
    expect(ElMessage.success).toHaveBeenCalledWith('已更新')
  })

  // 相册回填必须是拷贝：直接引用会让「改了表单再取消」永久改到列表行上的数组
  it('编辑回填相册是拷贝，改表单不污染列表行', async () => {
    const w = await mountPage()
    await w.findAll('.el-table-row')[0].findAll('.el-button')[2].trigger('click')
    await flush()

    // 弹窗里第二个 UploadField 是相册（multiple）
    const uploads = w.findAll('.upload-field')
    await uploads[uploads.length - 1].trigger('click')
    await flush()
    await byText(w, '.el-button', '保存').trigger('click')
    await flush()

    const sent = mocks.updateProject.mock.calls[0][1] as any
    expect(sent.album).toEqual(['/uploads/x1.jpg'])
    // 原始列表数据不能被改
    const row = (await mocks.listProjects())[0]
    expect(row.album).toEqual(['/uploads/a1.jpg'])
  })

  it('删除：取消确认则不调用删除接口', async () => {
    ;(ElMessageBox.confirm as any).mockRejectedValueOnce(new Error('cancel'))
    const w = await mountPage()

    await w.findAll('.el-table-row')[0].findAll('.el-button')[3].trigger('click')
    await flush()

    expect(mocks.deleteProject).not.toHaveBeenCalled()
  })

  it('删除：确认后调用接口并重载', async () => {
    const w = await mountPage()

    await w.findAll('.el-table-row')[0].findAll('.el-button')[3].trigger('click')
    await flush()

    expect(mocks.deleteProject).toHaveBeenCalledWith(1)
    expect(ElMessage.success).toHaveBeenCalledWith('已删除')
    expect(mocks.listProjects).toHaveBeenCalledTimes(2)
  })

  it('加载失败：页面仍可用（表格与刷新按钮都在）', async () => {
    mocks.listProjects.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    expect(w.find('.el-table').exists()).toBe(true)
    expect(byText(w, '.el-button', '刷新').exists()).toBe(true)
  })
})
