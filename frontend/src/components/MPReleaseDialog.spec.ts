// MPReleaseDialog 的组件测试：按钮按状态机显隐 + 驳回原因展示 + 撤回/发布的二次确认。
//
// 两个刻意取舍：
//   1. el-* 全部打桩（同 ChangePasswordDialog.spec）：完整 Element Plus 在 jsdom 下
//      过重易 OOM，而这里要断言的是「哪个状态出哪些按钮」，不需要真组件；
//   2. ElMessage / ElMessageBox 用 spyOn 监视而非 mock 整个模块——mock 掉
//      element-plus 会连带打断 el-* 的按需引入。
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, inject, provide } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import MPReleaseDialog from './MPReleaseDialog.vue'
import type { MPReleaseItem } from '@/api/mpRelease'

const apiMocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  submit: vi.fn(),
  status: vi.fn(),
  release: vi.fn(),
  undo: vi.fn(),
  readiness: vi.fn(),
  upload: vi.fn(),
})
)
vi.mock('@/api/mpRelease', () => ({
  listMPReleases: apiMocks.list,
  createMPRelease: apiMocks.create,
  submitMPReleaseAudit: apiMocks.submit,
  getMPReleaseAuditStatus: apiMocks.status,
  releaseMPRelease: apiMocks.release,
  undoMPReleaseAudit: apiMocks.undo,
  getMPReleaseUploadReadiness: apiMocks.readiness,
  uploadMPRelease: apiMocks.upload,
  mpReleaseTagType: (s: string) =>
    s === 'auditing' ? 'primary' : s === 'audit_passed' ? 'warning' : s === 'released' ? 'success' : s === 'rejected' ? 'danger' : 'info',
}))

// ---- 轻量桩：只渲染插槽。表格用 StatDetail.spec.ts 同款的 provide/inject 行上下文：
//      ElTable 为每行包一层 TableRow 并 provide 行数据，ElTableColumn 注入后转发给 #default 槽 ----
const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', props.row)
    return () => h('div', { class: 'el-table-row' }, slots.default ? slots.default() : [])
  },
})
// 桩要认 modelValue：否则嵌套的「登记版本」弹窗（v-model="regVisible"，初始 false）
// 会被无条件渲染出来，把「取消 / 登记」两个按钮混进行内按钮断言里
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue'],
  setup(props, { slots }) {
    return () =>
      props.modelValue
        ? h('div', { class: 'el-dialog' }, [slots.default ? slots.default() : null, slots.footer ? slots.footer() : null])
        : null
  },
})
// 桩要渲染 title/description：上传弹窗的就绪/未就绪提示全在 title 上，
// 只渲 slot 的话那些文案在测试里根本看不见
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'description'],
  template: `<div class="el-alert"><div class="el-alert__title">{{ title }}</div><div class="el-alert__description">{{ description }}</div><slot /></div>`,
})
// disabled 也要声明成 prop：否则它会以 attr 形式落到 <button> 上，
// 而 VTU 的 trigger 会跳过 disabled 元素——那样「环境不齐时点了也不发请求」
// 就永远点不中，测不到组件里那道 guard
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['loading', 'disabled'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots }) {
    return () =>
      h(
        'div',
        { class: 'el-table' },
        (props.data || []).map((r: any, i: number) =>
          h(TableRow, { key: i, row: r }, () => (slots.default ? slots.default() : null))
        )
      )
  },
})
const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  props: ['prop', 'label', 'width', 'align'],
  setup(props, { slots }) {
    const row = inject('tableRow', null) as any
    return () => {
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})
const ElTag = defineComponent({ name: 'ElTag', template: `<span class="el-tag"><slot /></span>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  // 桩要认 v-model：上传/登记弹窗里的输入框要能在测试里 setValue，
  // 否则 submitUpload 只会带着空版本号去打接口，等于没测
  template: `<input class="el-input" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElForm = defineComponent({ name: 'ElForm', template: `<form><slot /></form>` })
const ElFormItem = defineComponent({ name: 'ElFormItem', template: `<div><slot /></div>` })
const ElPagination = defineComponent({ name: 'ElPagination', template: `<div class="el-pagination" />` })
const ElSwitch = defineComponent({ name: 'ElSwitch', template: `<div class="el-switch" />` })

const stubs = {
  'el-dialog': ElDialog,
  'el-alert': ElAlert,
  'el-button': ElButton,
  'el-table': ElTable,
  'el-table-column': ElTableColumn,
  'el-tag': ElTag,
  'el-input': ElInput,
  'el-form': ElForm,
  'el-form-item': ElFormItem,
  'el-pagination': ElPagination,
  'el-switch': ElSwitch,
}

function row(over: Partial<MPReleaseItem>): MPReleaseItem {
  return {
    id: 1,
    companyId: 2,
    appId: 'wx-global-appid',
    userVersion: '1.0.0',
    userDesc: '',
    status: 'draft',
    statusName: '待提审',
    auditId: 0,
    auditReason: '',
    source: 'manual',
    operatorId: 1,
    submittedAt: null,
    releasedAt: null,
    createdAt: '2026-10-24 10:00:00',
    updatedAt: '2026-10-24 10:00:00',
    ...over,
  }
}

// el-table 上的 v-loading 是 Element Plus 指令，桩环境里未注册会刷一屏 warn；
// 这里断言的是按钮显隐，loading 态无意义，给个空指令即可
const vLoading = { mounted() {}, updated() {} }

function mountDialog(rows: MPReleaseItem[]) {
  apiMocks.list.mockResolvedValue({ list: rows, total: rows.length, page: 1, size: 10 })
  return mount(MPReleaseDialog, {
    props: { company: { id: 2, name: '乙公司', wxConfigured: true }, modelValue: true },
    global: { stubs, directives: { loading: vLoading } },
  })
}

// 行内按钮文案（el-button 桩渲染 <button>，直接按文本找）
function buttons(w: ReturnType<typeof mountDialog>) {
  return w.findAll('button').map((b) => b.text())
}

describe('MPReleaseDialog', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    // 命令式 API 用 spyOn 监视而非 mock 整个 element-plus 模块（mock 掉会打断
    // el-* 的按需引入）。as any：这里只关心「被调用过 / 返回值决定走哪条分支」，
    // MessageHandler.close 这类真实句柄形状与断言无关
    vi.spyOn(ElMessage, 'success').mockImplementation((() => ({})) as any)
    vi.spyOn(ElMessage, 'info').mockImplementation((() => ({})) as any)
    vi.spyOn(ElMessage, 'warning').mockImplementation((() => ({})) as any)
    vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as any)
  })

  it('打开即按企业拉列表', async () => {
    mountDialog([row({})])
    await nextTick()
    await nextTick()
    expect(apiMocks.list).toHaveBeenCalledWith(2, { page: 1, size: 10 })
  })

  it('draft：只有「提交审核」，点击即提审', async () => {
    const w = mountDialog([row({ id: 11, status: 'draft' })])
    await nextTick()
    await nextTick()
    expect(buttons(w)).toEqual(['上传代码', '登记版本', '刷新', '提交审核', '关闭'])
    await w.findAll('button').find((b) => b.text() === '提交审核')!.trigger('click')
    expect(apiMocks.submit).toHaveBeenCalledWith(11)
  })

  it('auditing：查状态 + 撤回，撤回要先过二次确认', async () => {
    const w = mountDialog([row({ id: 12, status: 'auditing', auditId: 999 })])
    await nextTick()
    await nextTick()
    expect(buttons(w)).toContain('查状态')
    expect(buttons(w)).toContain('撤回')
    await w.findAll('button').find((b) => b.text() === '撤回')!.trigger('click')
    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(apiMocks.undo).toHaveBeenCalledWith(12)
  })

  it('撤回的二次确认被用户取消时，不调接口', async () => {
    vi.spyOn(ElMessageBox, 'confirm').mockRejectedValueOnce('cancel')
    const w = mountDialog([row({ id: 13, status: 'auditing' })])
    await nextTick()
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '撤回')!.trigger('click')
    expect(apiMocks.undo).not.toHaveBeenCalled()
  })

  it('audit_passed：发布按钮，点击先确认再发布', async () => {
    const w = mountDialog([row({ id: 14, status: 'audit_passed', statusName: '审过待发布' })])
    await nextTick()
    await nextTick()
    expect(buttons(w)).toContain('发布')
    await w.findAll('button').find((b) => b.text() === '发布')!.trigger('click')
    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(apiMocks.release).toHaveBeenCalledWith(14)
  })

  it('rejected：展示驳回原因，按钮是「重新提审」', async () => {
    const w = mountDialog([
      row({ id: 15, status: 'rejected', statusName: '已驳回', auditReason: '首页按钮点击无响应' }),
    ])
    await nextTick()
    await nextTick()
    expect(w.text()).toContain('首页按钮点击无响应')
    expect(buttons(w)).toContain('重新提审')
    expect(buttons(w)).not.toContain('提交审核')
  })

  it('released：终态没有任何操作按钮', async () => {
    const w = mountDialog([row({ id: 16, status: 'released', statusName: '已上线' })])
    await nextTick()
    await nextTick()
    expect(w.text()).toContain('已上线')
    // 只剩工具栏 + 弹窗底栏，行内不应再出现可点的动作
    expect(buttons(w)).toEqual(['上传代码', '登记版本', '刷新', '关闭'])  })

  it('多行混排：每行按钮各随各的状态', async () => {
    const w = mountDialog([
      row({ id: 21, status: 'draft' }),
      row({ id: 22, status: 'auditing' }),
      row({ id: 23, status: 'audit_passed' }),
    ])
    await nextTick()
    await nextTick()
    const text = w.text()
    expect(text).toContain('提交审核')
    expect(text).toContain('撤回')
    expect(text).toContain('发布')
  })

  // ===== 上传代码 =====
  // 上传与登记的本质差别：登记只是记一笔，上传是后台真的把源码传到微信。
  // 所以这里重点守两件事——打开就探活（别让人白等几分钟）、环境不齐时拦在提交前。

  it('上传代码：打开即探活，把缺失项逐条列出来', async () => {
    apiMocks.readiness.mockResolvedValue({
      ready: false,
      problems: ['未找到 node，请在服务器安装 Node.js', '未安装 miniprogram-ci，请在 MP_CI_MODULE_DIR 指向的目录执行 npm install miniprogram-ci'],
      keyConfigured: false,
    })
    const w = mountDialog([row({})])
    await nextTick()
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '上传代码')!.trigger('click')
    await nextTick()
    expect(apiMocks.readiness).toHaveBeenCalledWith(2)
    expect(w.text()).toContain('上传环境未就绪')
    expect(w.text()).toContain('未找到 node')
    expect(w.text()).toContain('未安装 miniprogram-ci')
  })

  it('上传代码：环境就绪时显示就绪提示，不带缺失项', async () => {
    apiMocks.readiness.mockResolvedValue({ ready: true, problems: [], keyConfigured: true })
    const w = mountDialog([row({})])
    await nextTick()
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '上传代码')!.trigger('click')
    await nextTick()
    expect(w.text()).toContain('上传环境已就绪')
    expect(w.text()).not.toContain('上传环境未就绪')
  })

  it('上传代码：环境不齐时点了「开始上传」也不发请求', async () => {
    apiMocks.readiness.mockResolvedValue({ ready: false, problems: ['没配上传密钥'], keyConfigured: false })
    const w = mountDialog([row({})])
    await nextTick()
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '上传代码')!.trigger('click')
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '开始上传')!.trigger('click')
    await nextTick()
    expect(apiMocks.upload).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalled()
  })

  it('上传代码：就绪后提交，成功后刷新列表', async () => {
    apiMocks.readiness.mockResolvedValue({ ready: true, problems: [], keyConfigured: true })
    apiMocks.upload.mockResolvedValue({ code: 0 })
    const w = mountDialog([row({})])
    await nextTick()
    await nextTick()
    await w.findAll('button').find((b) => b.text() === '上传代码')!.trigger('click')
    await nextTick()
    const inputs = w.findAll('.el-input')
    await inputs[0].setValue('2.0.0')
    await inputs[1].setValue('补齐节日海报背景图')
    await w.findAll('button').find((b) => b.text() === '开始上传')!.trigger('click')
    await nextTick()
    await nextTick()
    expect(apiMocks.upload).toHaveBeenCalledWith(2, { userVersion: '2.0.0', userDesc: '补齐节日海报背景图' })
    // 上传成功后弹窗关掉、列表重新拉过一次（1 次首拉 + 1 次刷新）
    expect(apiMocks.list).toHaveBeenCalledTimes(2)
    expect(ElMessage.success).toHaveBeenCalled()
  })
})
