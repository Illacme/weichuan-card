import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'

vi.mock('@/api/audit', async (orig) => {
  const actual = (await orig()) as Record<string, unknown>
  return { ...actual, listAuditLogs: vi.fn() }
})
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {}, Plus: {}, Search: {} }))

import { ACTION_LABELS, listAuditLogs, actionLabel } from '@/api/audit'
import AuditLogs from './AuditLogs.vue'

// 竞态守门会给每次查询传 signal；只断言「aborted 是布尔」，
// 不钉死具体 signal 实例（每轮都会新建）。
const SIGNAL = expect.objectContaining({
  signal: expect.objectContaining({ aborted: expect.any(Boolean) }),
})

// 夹具：覆盖「成功 / 失败」与「未登录」三种形态
const page1 = {
  total: 2,
  page: 1,
  size: 20,
  list: [
    {
      id: 2,
      actorType: 'admin',
      actorId: 1,
      actorName: 'admin',
      companyId: 0,
      action: 'setting.update',
      targetType: 'setting',
      targetId: 'wx_appsecret',
      detail: '{"wx_appsecret":"******"}',
      ip: '127.0.0.1',
      result: 'success',
      createdAt: '2026-10-19 06:00:00',
    },
    {
      id: 1,
      actorType: 'anonymous',
      actorId: 0,
      actorName: 'admin',
      companyId: 0,
      action: 'login.fail',
      targetType: 'admin',
      targetId: 'admin',
      detail: '{"reason":"密码错误"}',
      ip: '10.0.0.9',
      result: 'fail',
      createdAt: '2026-10-19 05:59:00',
    },
  ],
}

// 轻量 stub：表格与分页器只需渲染不报错，断言走请求参数与纯函数
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElAlert = defineComponent({ name: 'ElAlert', template: `<div class="el-alert"><slot /></div>` })
const ElTable = defineComponent({ name: 'ElTable', props: ['data'], template: `<div class="el-table"><slot /></div>` })
const ElTableColumn = defineComponent({ name: 'ElTableColumn', template: `<div class="el-table-column" />` })
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue'],
  emits: ['update:modelValue', 'change'],
  // 注意：v-model 靠 update:modelValue 生效，只 emit change 的话页面里的值不会变，
  // 筛选断言就会拿到旧值（这里两个事件都要发）。
  template: `<select class="el-select" :value="modelValue"
    @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
const ElTag = defineComponent({ name: 'ElTag', props: ['type'], template: `<span class="el-tag"><slot /></span>` })
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElPagination = defineComponent({ name: 'ElPagination', template: `<div class="el-pagination" />` })

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-alert': ElAlert,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-tag': ElTag,
      'el-button': ElButton,
      'el-pagination': ElPagination,
    },
  },
}

async function setup() {
  const w = mount(AuditLogs, mountOpts)
  await vi.waitFor(() => expect(listAuditLogs).toHaveBeenCalled())
  await nextTick()
  return w
}

describe('AuditLogs 操作日志', () => {
  beforeEach(() => {
    vi.mocked(listAuditLogs).mockReset()
    // 分页信封：data 是当前页数组，total/page/size 在顶层（见 api/request.ts 的 Paged）
    vi.mocked(listAuditLogs).mockImplementation(async () => ({
      code: 0,
      data: page1.list,
      total: page1.total,
      page: page1.page,
      size: page1.size,
    }) as any)
  })

  it('挂载即按默认分页查询一次', async () => {
    await setup()
    expect(listAuditLogs).toHaveBeenCalledWith({ page: 1, size: 20 }, SIGNAL)
  })

  it('连续筛选时旧响应后到也不盖掉新结果', async () => {
    const w = await setup()
    const pending: Array<(v: unknown) => void> = []
    vi.mocked(listAuditLogs).mockClear()
    vi.mocked(listAuditLogs).mockImplementation(
      () => new Promise((resolve) => { pending.push(resolve) }) as any
    )

    await w.findAll('select.el-select')[0].setValue('setting.update')
    await nextTick()
    await w.findAll('select.el-select')[1].setValue('fail')
    await nextTick()
    expect(pending, '两次筛选应各发一次请求，量尺不能空转').toHaveLength(2)
    // 第二轮真的取消了第一轮（不只是忽略写回）
    const firstSignal = vi.mocked(listAuditLogs).mock.calls[0][1]?.signal
    expect(firstSignal?.aborted).toBe(true)

    const paged = (targetId: string) => ({
      list: [{ ...page1.list[0], id: 1, targetId }],
      total: 1, page: 1, size: 20,
    })
    const rowsOf = () => (w.vm as any).$.setupState.rows as Array<{ targetId: string }>
    // 新响应先回，旧响应后回：旧的那次必须被 isCurrent 丢弃。
    // 表格组件在测试里是桩，不渲染插槽，所以直接看页面自己的 rows 状态。
    pending[1](paged('NEW-TARGET'))
    await flushPromises()
    expect(rowsOf()[0]?.targetId).toBe('NEW-TARGET')

    pending[0](paged('OLD-TARGET'))
    await flushPromises()
    expect(rowsOf()[0]?.targetId).toBe('NEW-TARGET')
  })

  it('按动作筛选：带上 action 并回到第 1 页', async () => {
    const w = await setup()
    vi.mocked(listAuditLogs).mockClear()
    await w.findAll('select.el-select')[0].setValue('setting.update')
    await nextTick()
    expect(listAuditLogs).toHaveBeenCalledWith(
      expect.objectContaining({ action: 'setting.update', page: 1 }),
      SIGNAL,
    )
  })

  it('动作下拉覆盖全部已埋点动作，且都显示中文名而不是原始键', async () => {
    const w = await setup()
    const opts = w.findAll('select.el-select')[0].findAll('option')
    // 下拉曾手写清单、漏掉 employee.* / apply.* / login.success / logout 等 8 个动作：
    // 列表里看得见（有中文名）却筛不出来。这里钉住「选项数 = 中文名表条目数」。
    expect(opts).toHaveLength(Object.keys(ACTION_LABELS).length)
    const labels = opts.map((o: any) => o.text())
    // 每个动作都显示中文名
    for (const label of Object.values(ACTION_LABELS)) {
      expect(labels).toContain(label)
    }
    // 且没有一个选项直接把英文键摊开
    for (const key of Object.keys(ACTION_LABELS)) {
      expect(labels).not.toContain(key)
    }
  })

  it('按结果筛选：只带 result，未选的筛选项不传', async () => {
    const w = await setup()
    vi.mocked(listAuditLogs).mockClear()
    await w.findAll('select.el-select')[1].setValue('fail')
    await nextTick()
    const arg = vi.mocked(listAuditLogs).mock.calls[0][0]
    expect(arg.result).toBe('fail')
    expect(arg.action).toBeUndefined()
    expect(arg.actorType).toBeUndefined()
  })

  it('刷新按钮重新拉取', async () => {
    const w = await setup()
    vi.mocked(listAuditLogs).mockClear()
    const btn = w.findAll('button.el-button').find((b) => b.text().includes('刷新'))!
    await btn.trigger('click')
    await vi.waitFor(() => expect(listAuditLogs).toHaveBeenCalled())
  })
})

describe('actionLabel 动作中文名', () => {
  it('已登记的动作显示中文，未登记的回退原键', () => {
    expect(actionLabel('setting.update')).toBe('修改系统设置')
    expect(actionLabel('admin.reset_password')).toBe('重置管理员密码')
    expect(actionLabel('login.fail')).toBe('登录失败')
    // 后端新增动作而前端没同步时，回退显示键名而不是空白——界面宁可丑也不能丢信息
    expect(actionLabel('some.new.action')).toBe('some.new.action')
  })
})
