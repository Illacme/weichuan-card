import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import Employees from './Employees.vue'

// 员工管理页依赖 api/employees（列表 / 增 / 停启用 / 重置密码）与 element-plus 的消息 / 确认框，
// 全部 mock 掉网络与 UI 反馈层；分页用真实的 useServerTable（服务端分页组合式函数），
// 顺带覆盖其与视图的联动（翻页 / 改每页条数 / 操作后重载）。
// 注意：vi.mock 工厂会被提升到文件顶部，里面引用的 mock 必须经 vi.hoisted 预先创建，
// 否则访问 const 会撞 TDZ（Cannot access before initialization）。
const mocks = vi.hoisted(() => ({
  getWxacode: vi.fn(),
  // 列表接口返回服务端分页信封 { list, total, page, size }（见 api/request.ts 的 Paged）
  // 标注 any：夹具只填断言用到的字段，免去为每行补齐全部 Employee 字段
  listEmployees: vi.fn(async (): Promise<any> => ({
    list: [
      { staffId: 1, name: '张三', title: '工程师', phone: '13800000001', landline: '021-11111111', email: 'z@a.com', avatar: '', status: 'enabled', createdAt: '2026-01-01 10:00:00', wechatBound: true, openid: 'o****0001' },
      { staffId: 2, name: '李四', title: '销售', phone: '13900000002', landline: '', email: 'l@a.com', avatar: '', status: 'disabled', createdAt: '2026-01-02 10:00:00', wechatBound: false, openid: '' },
      { staffId: 3, name: '王五', title: '经理', phone: '13700000003', landline: '', email: 'w@a.com', avatar: '', status: 'enabled', createdAt: '2026-01-03 10:00:00', wechatBound: true, openid: 'o****0003' },
    ],
    total: 3,
    page: 1,
    size: 10,
  })),
  createEmployee: vi.fn(async () => ({ staffId: 99, initialPassword: 'init123' })),
  updateEmployee: vi.fn(async () => ({ code: 0 })),
  updateEmployeeEntries: vi.fn(async () => ({ code: 0 })),
  disableEmployee: vi.fn(async () => ({ code: 0 })),
  enableEmployee: vi.fn(async () => ({ code: 0 })),
  resetPassword: vi.fn(async () => 'reset123'),
  exportEmployees: vi.fn(async () => {}),
}))
const {
  listEmployees,
  createEmployee,
  updateEmployee,
  updateEmployeeEntries,
  disableEmployee,
  enableEmployee,
  resetPassword,
  exportEmployees,
} = mocks
import { getWxacode } from '@/api/wechat'

vi.mock('@/api/employees', () => mocks)
vi.mock('@/api/wechat', () => ({ getWxacode: mocks.getWxacode }))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn(async () => 'confirm') },
}))
// 图标仅作为 prop 值传递（:prefix-icon）或模板渲染（UploadField 的「从素材库选择」），桩成普通对象即可
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {}, Plus: {}, Search: {}, Download: {}, Picture: {} }))

import { ElMessageBox } from 'element-plus'

// ── 轻量 stub（jsdom 下完整 Element Plus 过重易 OOM） ──
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'link'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElForm = defineComponent({
  name: 'ElForm',
  methods: {
    validate: () => Promise.resolve(true),
    clearValidate: () => {},
  },
  template: `<form class="el-form"><slot /></form>`,
})
const ElFormItem = defineComponent({ name: 'ElFormItem', template: `<div class="el-form-item"><slot /></div>` })
const ElImage = defineComponent({ name: 'ElImage', props: ['src', 'fit'], template: `<img class="el-image" />` })
const ElTag = defineComponent({ name: 'ElTag', props: ['type', 'effect'], template: `<span class="el-tag"><slot /></span>` })
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  template: `<div v-if="modelValue" class="el-dialog"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})
const ElDescriptions = defineComponent({ name: 'ElDescriptions', props: ['column', 'border'], template: `<div class="el-descriptions"><slot /></div>` })
const ElDescriptionsItem = defineComponent({ name: 'ElDescriptionsItem', props: ['label'], template: `<div class="el-descriptions-item"><slot /></div>` })
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  emits: ['update:currentPage', 'update:pageSize'],
  template: `<div class="el-pagination" />`,
})

// 表格行上下文：ElTable 为每一行包一层 TableRow 并 provide 行数据，
// ElTableColumn 注入后转发给本列 #default 槽——复刻 Element Plus「列槽拿 { row }」的行为。
const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', props.row)
    return () => h('div', { class: 'el-table-row' }, slots.default ? slots.default() : [])
  },
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots }) {
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
    const row = inject('tableRow', null) as any
    return () => {
      // 复刻 Element Plus：无作用域槽时按 prop 直接取行字段渲染（如姓名 / 手机列）
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})

// el-switch：外层带 data-model-value，用例据此断言「开关当前是开还是关」，
// 并可以点击切换（复刻 Element Plus 的点击行为：翻转 modelValue）
const ElSwitch = defineComponent({
  name: 'ElSwitch',
  props: ['modelValue', 'activeText', 'inactiveText'],
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    return () =>
      h('button', {
        class: 'el-switch',
        'data-model-value': String(!!props.modelValue),
        onClick: () => emit('update:modelValue', !props.modelValue),
      }, props.modelValue ? props.activeText : props.inactiveText)
  },
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-image': ElImage,
      'el-tag': ElTag,
      'el-dialog': ElDialog,
      'el-descriptions': ElDescriptions,
      'el-descriptions-item': ElDescriptionsItem,
      'el-pagination': ElPagination,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      'el-switch': ElSwitch,
    },
    // el-table 上的 v-loading 指令在 stub 下无需行为，注册个空指令消除告警
    directives: { loading: {} },
  },
}

function mountEmployees() {
  return mount(Employees, mountOpts)
}

// 等挂载后的 onMounted → load() 的微任务链走完
async function flushLoad(_w: ReturnType<typeof mountEmployees>) {
  await nextTick()
  await nextTick()
  await nextTick()
}

describe('Employees', () => {
  beforeEach(() => {
    vi.mocked(listEmployees).mockClear()
    vi.mocked(createEmployee).mockClear()
    vi.mocked(updateEmployee).mockClear()
    vi.mocked(updateEmployeeEntries).mockClear()
    vi.mocked(disableEmployee).mockClear()
    vi.mocked(enableEmployee).mockClear()
    vi.mocked(resetPassword).mockClear()
    vi.mocked(getWxacode).mockClear()
    vi.mocked(getWxacode).mockResolvedValue('') // 默认拉不到小程序码（未配微信凭据的降级路径）
    vi.mocked(ElMessageBox.confirm).mockClear()
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => 'confirm' as any)
  })

  it('预览：弹窗展示名片摘要 + 小程序码；二维码拉取失败时静默降级为文字提示', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    // 第一行（张三，staffId=1）：getWxacode 返回空 → 弹窗显示降级文案，不抛错
    const previewBtns = () => w.findAll('button.el-button').filter((b) => b.text() === '预览')
    await previewBtns()[0].trigger('click')
    await vi.waitFor(() => expect(vi.mocked(getWxacode)).toHaveBeenCalledWith(1))
    expect(w.text()).toContain('访客视角的实际效果')
    expect(w.text()).toContain('小程序码暂不可用')

    // 关掉弹窗，换成有二维码的行（李四，staffId=2）
    await w.findAll('.el-dialog button.el-button').find((b) => b.text() === '知道了')!.trigger('click')
    vi.mocked(getWxacode).mockResolvedValue('http://qr/2.jpg')
    await previewBtns()[1].trigger('click')
    await vi.waitFor(() => expect(vi.mocked(getWxacode)).toHaveBeenCalledWith(2))
    expect(w.text()).toContain('编号 2')
    expect(w.text()).not.toContain('小程序码暂不可用')
  })

  it('挂载即加载列表：表格数据 / 分页总数 / 行内容正确', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const table = w.findComponent(ElTable)
    expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(1)
    expect(table.props('data')).toHaveLength(3)
    expect(w.findComponent(ElPagination).props('total')).toBe(3)
    // 行上下文已注入列槽：姓名 / 状态文案渲染出来
    expect(w.text()).toContain('张三')
    expect(w.text()).toContain('李四')
    expect(w.text()).toContain('已停用') // 李四 status=disabled
    expect(w.text()).toContain('正常') // 张三 / 王五
    // 「创建时间」列经 formatDateTime 渲染：解析与格式化都走本地时区，
    // 往返结果恒定，故此断言在任意时区（含 CI 的 UTC）下都成立。
    expect(w.text()).toContain('2026-01-01 10:00:00')
    expect(w.text()).not.toContain('T10:00') // 未把原始 RFC3339 串直接透出

    // 列定义：title 由「岗位」改名「职位」，并新增「座机」列（stub 不渲染 label，只能查 props）
    const labels = w.findAllComponents(ElTableColumn).map((c) => c.props('label'))
    expect(labels).toContain('职位')
    expect(labels).toContain('座机')
    expect(labels).not.toContain('岗位')
  })

  // 关键字筛选已下沉到服务端：界面只负责把关键字传进请求参数，
  // 「筛完剩几条」由后端回包的 list/total 决定（见 composables/useServerTable）。
  it('关键字输入后把关键字带进请求参数（服务端筛选）', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    // 命中手机：mock 只回 1 条，界面按回包渲染
    vi.mocked(listEmployees).mockImplementation(async (): Promise<any> => ({
      list: [{ staffId: 3, name: '王五', title: '经理', phone: '13700000003', status: 'enabled', createdAt: '2026-01-03 10:00:00' }],
      total: 1,
      page: 1,
      size: 10,
    }))
    await w.find('input.el-input').setValue('137')
    await vi.waitFor(() => expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(2))
    await vi.waitFor(() => expect(w.findComponent(ElTable).props('data')).toHaveLength(1))

    const calls = vi.mocked(listEmployees).mock.calls as any[]
    expect(calls[calls.length - 1][0]).toMatchObject({ keyword: '137' })
    expect(w.findComponent(ElTable).props('data')[0].name).toBe('王五')
    // 总数来自服务端回包（不是前端数出来的长度）
    expect(w.findComponent(ElPagination).props('total')).toBe(1)
  })

  it('停用：确认后调 disableEmployee 并重新加载', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const disableBtn = w.findAll('button.el-button').find((b) => b.text() === '停用')
    expect(disableBtn).toBeTruthy()
    await disableBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(ElMessageBox.confirm)).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(vi.mocked(disableEmployee)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(disableEmployee)).toHaveBeenCalledWith(1) // 张三（第一个 enabled 行）
    await vi.waitFor(() => expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(2)) // 停用后重载
  })

  it('停用被取消时不调用接口', async () => {
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => Promise.reject(new Error('cancel')))
    const w = mountEmployees()
    await flushLoad(w)

    const disableBtn = w.findAll('button.el-button').find((b) => b.text() === '停用')
    await disableBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(ElMessageBox.confirm)).toHaveBeenCalledTimes(1))
    await nextTick()
    await nextTick()
    expect(vi.mocked(disableEmployee)).not.toHaveBeenCalled()
  })

  it('启用：对已停用行调 enableEmployee 并重新加载', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const enableBtn = w.findAll('button.el-button').find((b) => b.text() === '启用')
    expect(enableBtn).toBeTruthy()
    await enableBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(enableEmployee)).toHaveBeenCalledWith(2)) // 李四
    await vi.waitFor(() => expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(2))
  })

  it('重置密码：确认后调 resetPassword 并弹窗展示新初始密码', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const resetBtn = w.findAll('button.el-button').find((b) => b.text() === '重置密码')
    await resetBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(ElMessageBox.confirm)).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(vi.mocked(resetPassword)).toHaveBeenCalledWith(1))
    await vi.waitFor(() => expect(w.text()).toContain('reset123'))
    expect(w.text()).toContain('重置密码成功')
  })

  it('重置密码取消时不调用接口', async () => {
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () =>
      Promise.reject(new Error('cancel'))
    )
    const w = mountEmployees()
    await flushLoad(w)

    const resetBtn = w.findAll('button.el-button').find((b) => b.text() === '重置密码')
    await resetBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(ElMessageBox.confirm)).toHaveBeenCalledTimes(1))
    await nextTick()
    await nextTick()
    expect(vi.mocked(resetPassword)).not.toHaveBeenCalled()
  })

  it('编辑员工：默认只回填一个手机号，点「+ 备用手机号」展开后保存副号', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    await w.findAll('button.el-button').find((b) => b.text() === '编辑')!.trigger('click')
    await nextTick()
    expect(w.text()).toContain('编辑员工') // 弹窗标题区分编辑 / 新增

    // 默认收起备用号：姓名 / 手机 / 职位 / 座机 / 邮箱 五个输入框（该员工无 phone2）
    let inputs = w.findAll('.el-dialog input.el-input')
    expect(inputs).toHaveLength(5)
    expect((inputs[0].element as HTMLInputElement).value).toBe('张三')
    expect((inputs[1].element as HTMLInputElement).value).toBe('13800000001')
    expect((inputs[2].element as HTMLInputElement).value).toBe('工程师')
    expect((inputs[3].element as HTMLInputElement).value).toBe('021-11111111')
    expect((inputs[4].element as HTMLInputElement).value).toBe('z@a.com')

    // 点右侧「+ 备用手机号」才展开第二个输入框（与小程序端同一套交互）
    await w.findAll('.el-dialog button.el-button').find((b) => b.text().includes('备用手机号'))!.trigger('click')
    await nextTick()
    inputs = w.findAll('.el-dialog input.el-input')
    expect(inputs).toHaveLength(6)
    // 展开后插在「手机」之后：姓名 / 手机 / 手机号2 / 职位 / 座机 / 邮箱
    expect((inputs[2].element as HTMLInputElement).value).toBe('')

    await inputs[4].setValue('021-99999999') // 座机
    await inputs[2].setValue('13900000009') // 手机号2
    await w.findAll('.el-dialog button.el-button').find((b) => b.text() === '保存')!.trigger('click')

    await vi.waitFor(() => expect(vi.mocked(updateEmployee)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(updateEmployee)).toHaveBeenCalledWith(
      1,
      expect.objectContaining({
        name: '张三',
        phone: '13800000001',
        phone2: '13900000009',
        landline: '021-99999999',
      })
    )
    await vi.waitFor(() => expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(2))
  })

  it('编辑员工：已有备用号时自动展开输入框', async () => {
    // 首屏列表就带一个有副号的员工（mockResolvedValueOnce 只作用于挂载后的第一次 load）
    vi.mocked(listEmployees).mockResolvedValueOnce({
      list: [
        {
          staffId: 9,
          name: '赵六',
          title: '总监',
          phone: '13600000009',
          phone2: '13500000008',
          landline: '',
          email: 'z@a.com',
          avatar: '',
          status: 'enabled',
          createdAt: '2026-01-04 10:00:00',
        },
      ],
      total: 1,
      page: 1,
      size: 10,
    } as any)
    const w = mountEmployees()
    await flushLoad(w)

    await w.findAll('button.el-button').find((b) => b.text() === '编辑')!.trigger('click')
    await nextTick()
    const inputs = w.findAll('.el-dialog input.el-input')
    // 有值就自动展开：姓名 / 手机 / 手机号2 / 职位 / 座机 / 邮箱
    expect(inputs).toHaveLength(6)
    expect((inputs[1].element as HTMLInputElement).value).toBe('13600000009')
    expect((inputs[2].element as HTMLInputElement).value).toBe('13500000008')
  })

  it('新增员工：校验通过后调 createEmployee、弹初始密码并重载', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    // 打开新增弹窗 → 填表 → 保存
    await w.findAll('button.el-button').find((b) => b.text() === '新增员工')!.trigger('click')
    await nextTick()
    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[0].setValue('赵六')
    await inputs[1].setValue('13600000004')
    await w.findAll('.el-dialog button.el-button').find((b) => b.text() === '保存')!.trigger('click')

    await vi.waitFor(() => expect(vi.mocked(createEmployee)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createEmployee)).toHaveBeenCalledWith(
      expect.objectContaining({ name: '赵六', phone: '13600000004' })
    )
    await vi.waitFor(() => expect(w.text()).toContain('init123'))
    await vi.waitFor(() => expect(vi.mocked(listEmployees)).toHaveBeenCalledTimes(2))
  })

  it('微信绑定列显示状态与掩码，未绑定的不留空 span、也不铺开 openid 原文', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const cells = w.findAll('.wx-bind-cell')
    expect(cells).toHaveLength(3)
    expect(cells[0].attributes('data-bound')).toBe('true')
    expect(cells[0].text()).toContain('已绑定')
    expect(cells[0].find('.wx-bind-mask').text()).toBe('o****0001')
    expect(cells[1].attributes('data-bound')).toBe('false')
    expect(cells[1].text()).toContain('未绑定')
    expect(cells[1].find('.wx-bind-mask').exists()).toBe(false)
    // 掩码是唯一的展示值；后端只回它，完整 openid 根本到不了这一层
    expect(cells[0].attributes('data-masked')).toBe('o****0001')
    expect(cells[2].attributes('data-masked')).toBe('o****0003')
    expect(cells[0].find('.wx-bind-mask').attributes('title')).toBe('o****0001')
  })

  it('批量导出：点击「批量导出」调 exportEmployees（带当前关键字）', async () => {
    const w = mountEmployees()
    await flushLoad(w)

    const exportBtn = w
      .findAll('button.el-button')
      .find((b) => b.text() === '批量导出')
    expect(exportBtn).toBeTruthy()
    await exportBtn!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(exportEmployees)).toHaveBeenCalledTimes(1))
    // 缺省无关键字 → 以空串下发（与列表页同口径，导出「当前看到的筛选结果」）
    expect(vi.mocked(exportEmployees)).toHaveBeenCalledWith({ keyword: '' })
  })
})

// 小程序菜单功能授权（2026-11-26）：列表「操作 → 功能授权」弹窗，四项按员工授权。
// 产品口径：海报/统计默认开（本职功能），团队/管理员登录默认关（额外能力）。
// 三条都必须钉住——漏任何一条都会变成「后台开了不生效」或「开了关不掉」：
//   1. 默认值两组相反，老数据缺字段按同样的默认处理；
//   2. 回填已有值，保存走**独立**接口（不能混进编辑资料）；
//   3. 关掉也要**显式传 false**（后端按地图更新，省略就等于保持原值）。
describe('Employees · 功能授权弹窗', () => {
  const rowWith = (extra: any = {}) => ({
    staffId: 9,
    name: '赵六',
    title: '总监',
    phone: '13600000009',
    phone2: '',
    landline: '',
    email: 'z@a.com',
    avatar: '',
    status: 'enabled',
    createdAt: '2026-01-04 10:00:00',
    ...extra,
  })

  async function openWith(row: any, buttonText: string) {
    vi.mocked(listEmployees).mockResolvedValueOnce({ list: [row], total: 1, page: 1, size: 10 } as any)
    const w = mountEmployees()
    await flushLoad(w)
    await w.findAll('button.el-button').find((b) => b.text() === buttonText)!.trigger('click')
    await nextTick()
    return w
  }

  // 弹窗里四项开关按模板顺序排列：海报 / 团队 / 统计 / 管理员。
  // stub 的 el-form-item 不渲染 label，所以按序取即可（源码级守门另有断言锁住顺序与文案）。
  const SWITCH_ORDER = ['生成海报', '团队成员', '数据统计', '管理员登录']
  function switchOf(w: any, label: string) {
    return w.findAll('.el-dialog .el-switch')[SWITCH_ORDER.indexOf(label)]
  }

  function switchValues(w: any) {
    return w.findAll('.el-dialog .el-switch').map((s: any) => s.attributes('data-model-value'))
  }

  it('操作列有「功能授权」按钮，点击后弹窗四项开关按产品默认回填', async () => {
    const w = await openWith(rowWith(), '功能授权')

    expect(w.find('.el-dialog').exists()).toBe(true)
    expect(w.find('.el-dialog h3').text()).toContain('功能授权')
    // 海报/统计开，团队/管理员关——两组默认值相反
    expect(switchValues(w)).toEqual(['true', 'false', 'true', 'false'])
  })

  it('编辑弹窗不再有功能开关：资料保存不会误改授权', async () => {
    const w = await openWith(rowWith({ showTeamEntry: true }), '编辑')
    expect(w.findAll('.el-dialog .el-switch')).toHaveLength(0)
  })

  it('保存四项授权走独立接口；关掉的项显式传 false，而不是省略', async () => {
    const w = await openWith(rowWith(), '功能授权')

    // 关「生成海报」（默认开 → 关），开「团队成员」+「管理员登录」（默认关 → 开）
    await switchOf(w, '生成海报').trigger('click')
    await switchOf(w, '团队成员').trigger('click')
    await switchOf(w, '管理员登录').trigger('click')
    await nextTick()
    expect(switchValues(w)).toEqual(['false', 'true', 'true', 'true'])

    await w.findAll('.el-dialog button.el-button').find((b) => b.text() === '保存授权')!.trigger('click')
    await vi.waitFor(() => expect(vi.mocked(updateEmployeeEntries)).toHaveBeenCalledTimes(1))
    const [id, payload] = vi.mocked(updateEmployeeEntries).mock.calls[0] as unknown as [number, any]
    expect(id).toBe(9)
    expect(payload).toEqual({
      showPosterEntry: false,
      showTeamEntry: true,
      showStatEntry: true,
      showAdminEntry: true,
    })
    // 不能误调编辑资料接口
    expect(vi.mocked(updateEmployee)).not.toHaveBeenCalled()
  })

  it('老数据缺字段：海报/统计仍按默认开，团队/管理员按默认关', async () => {
    const w = await openWith(rowWith(), '功能授权')
    expect(switchValues(w)).toEqual(['true', 'false', 'true', 'false'])
  })

  it('回填已有授权值：四项都按后端返回的状态显示', async () => {
    const w = await openWith(rowWith({
      showPosterEntry: false, showTeamEntry: true, showStatEntry: false, showAdminEntry: true,
    }), '功能授权')
    expect(switchValues(w)).toEqual(['false', 'true', 'false', 'true'])
  })

  it('关闭弹窗再开另一个员工：不残留上一个员工的授权状态', async () => {
    const w = await openWith(rowWith({ showTeamEntry: true, showAdminEntry: true }), '功能授权')
    expect(switchValues(w)).toEqual(['true', 'true', 'true', 'true'])
    const vm = w.vm as any

    vm.entriesVisible = false
    await nextTick()
    // 用另一个员工打开（openEntries 每次都从行数据重填，不沿用上次的表单值）
    vm.openEntries(rowWith({ staffId: 10, name: '新人' }))
    await nextTick()
    expect(switchValues(w)).toEqual(['true', 'false', 'true', 'false'])
  })
})


// 源码级守门：四项开关的文案与顺序是产品口径的一部分（默认两组相反）。
// 行为用例能测出默认值错了，但测不出「文案被改成别的」——那会让管理员勾错项。
describe('Employees · 功能授权弹窗源码守门', () => {
  const src = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), './Employees.vue'),
    'utf-8',
  )

  it('操作列（桌面与手机卡片）都要有「功能授权」按钮', () => {
    const hits = (src.match(/@click="openEntries\(row\)">功能授权</g) || []).length
    // 桌面表格 1 处 + 手机卡片 1 处
    expect(hits).toBe(2)
  })

  it('四项开关的标签与默认口径按固定顺序写在弹窗里', () => {
    const dialog = src.slice(src.indexOf('功能授权：四项小程序菜单入口'), src.indexOf('<!-- 名片预览'))
    const labels = ['生成海报', '团队成员', '数据统计', '管理员登录']
    let pos = 0
    for (const label of labels) {
      const at = dialog.indexOf(`label="${label}"`, pos)
      expect(at, `弹窗缺少或顺序错乱：${label}`).toBeGreaterThan(-1)
      pos = at
    }
  })

  it('entriesForm 初值：海报/统计 true，团队/管理员 false（与后端列默认一致）', () => {
    const block = src.slice(src.indexOf('const entriesForm = reactive'), src.indexOf('function openEntries'))
    expect(block).toMatch(/showPosterEntry:\s*true/)
    expect(block).toMatch(/showTeamEntry:\s*false/)
    expect(block).toMatch(/showStatEntry:\s*true/)
    expect(block).toMatch(/showAdminEntry:\s*false/)
  })

  it('编辑资料表单不再含任何菜单开关（授权独立于资料）', () => {
    const form = src.slice(src.indexOf('新增 / 编辑员工（同一表单'), src.indexOf('<!-- 功能授权'))
    expect(form).not.toContain('showTeamEntry')
    expect(form).not.toContain('el-switch')
  })
})

describe('Employees · 操作列宽度与授权弹窗提示文案', () => {
  const src = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), './Employees.vue'),
    'utf-8',
  )

  it('操作列收窄到 280px（从 320px 收 40px，避免压住创建时间列）', () => {
    expect(src).toMatch(/<el-table-column label="操作" width="280" fixed="right"/)
    expect(src).not.toMatch(/<el-table-column label="操作" width="320"/)
  })

  it('授权弹窗四项开关都带功能提示（生成海报 / 数据统计也要有）', () => {
    const dialog = src.slice(src.indexOf('<!-- 功能授权'), src.indexOf('<!-- 名片预览'))
    for (const label of ['生成海报', '团队成员', '数据统计', '管理员登录']) {
      const at = dialog.indexOf(`label="${label}"`)
      expect(at, `缺少开关：${label}`).toBeGreaterThan(-1)
      const next = dialog.indexOf('</el-form-item>', at)
      expect(dialog.slice(at, next), `${label} 缺少功能提示`).toContain('class="field-tip"')
    }
  })
})
