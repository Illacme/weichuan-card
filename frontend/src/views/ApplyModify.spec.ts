import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { mountOpts, flush, paged, rowOf, btnIn, dlgOf } from '@/test/elStubs'
import ApplyModify from './ApplyModify.vue'

// 名片修改审核页。
//
// 这个页面的失败模式跟「开通审核」不同：开通是建号（不可逆），这里是**把员工提交的新值
// 覆盖到员工档案上**。所以真正危险的是三件事：
//   1. 运营看不到「旧值 → 新值」，只能对着一串 JSON 猜——改错了无从察觉，
//      员工的旧手机号/旧头像就永久没了（没有回收站）。
//   2. 「通过」被点两次：第一次已应用，第二次要么重复写、要么报已处理，
//      界面如果不刷新，按钮就一直在那儿。
//   3. 驳回不给理由，或者理由前后串台（弹窗复用上一次的原因）。
// 另外本页的旧值要额外查一次员工表，这里钉住「按本页 staffId 精确取」而不是拉全量。

const mocks = vi.hoisted(() => ({
  listModifyApply: vi.fn(),
  approveModify: vi.fn(),
  rejectModify: vi.fn(),
  listEmployees: vi.fn(),
}))

vi.mock('@/api/apply', () => mocks)
vi.mock('@/api/employees', () => ({ listEmployees: mocks.listEmployees }))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))
// Right 是要真渲染的（明细里的旧→新箭头），给空渲染函数而不是空对象
vi.mock('@element-plus/icons-vue', () => ({
  Refresh: { render: () => null },
  Search: { render: () => null },
  Right: { render: () => null },
}))
// 桌面端为默认档；手机端单独一条用例翻 isPhone（员工ID 与提交时间两列会被隐藏）
const bp = vi.hoisted(() => ({ isPhone: null as any }))
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  bp.isPhone = ref(false)
  return { useBreakpoint: () => ({ isPhone: bp.isPhone }) }
})

import { ElMessage } from 'element-plus'

// 三条申请：待审核（可操作）/ 已通过 / 已驳回。
// 第 23 条的快照是**坏 JSON**（存量脏数据），用来钉住解析失败不留白屏。
function mkApplies() {
  return [
    {
      id: 21, staffId: 7, status: 'pending', rejectReason: '',
      createdAt: '2026-10-20T10:00:00',
      snapshot: JSON.stringify({
        name: '张新名', title: '销售总监', phone: '13900000009', landline: '021-6666', wechat: 'wxid-new',
      }),
    },
    {
      id: 22, staffId: 8, status: 'approved', rejectReason: '',
      createdAt: '2026-10-21T10:00:00',
      snapshot: JSON.stringify({ avatar: 'https://cdn/b.png' }),
    },
    {
      id: 23, staffId: 7, status: 'rejected', rejectReason: '头像不清晰',
      createdAt: '2026-10-22T10:00:00', snapshot: '不是 JSON',
    },
  ]
}

// 员工表当前值（旧值来源）。7 号刻意不给 landline、不给 wechat：
// 明细里这两项就要显示「（空）」，而不是 undefined 或一片空白
const employees = [
  { staffId: 7, name: '张旧名', title: '销售经理', phone: '13800000001', landline: '', avatar: 'https://cdn/a.png' },
  { staffId: 8, name: '李四', title: '技术', phone: '13800000003', landline: '021-1111', avatar: 'https://cdn/c.png' },
]

let applies: any[] = []

async function setup() {
  const w = mount(ApplyModify, mountOpts)
  await vi.waitFor(() => expect(mocks.listModifyApply).toHaveBeenCalled())
  await flush()
  return w
}

function resetAll() {
  applies = mkApplies()
  bp.isPhone.value = false
  mocks.listModifyApply.mockReset()
  mocks.approveModify.mockReset()
  mocks.rejectModify.mockReset()
  mocks.listEmployees.mockReset()
  mocks.listModifyApply.mockImplementation(async () => paged(applies))
  mocks.approveModify.mockImplementation(async () => ({ code: 0 }))
  mocks.rejectModify.mockImplementation(async () => ({ code: 0 }))
  mocks.listEmployees.mockImplementation(async () => paged(employees))
  vi.mocked(ElMessage.success).mockClear()
}

describe('名片修改审核 · 列表与变更摘要', () => {
  beforeEach(resetAll)

  it('首屏按服务端分页拉列表（page/size/keyword 一起下沉）', async () => {
    await setup()
    expect(mocks.listModifyApply).toHaveBeenCalledWith({ page: 1, size: 20, keyword: '' })
  })

  it('变更摘要把快照渲染成「字段→新值」，未知字段用键名兜底', async () => {
    const w = await setup()
    const tags = rowOf(w, 21).findAll('.sum-tag')
    expect(tags.map((t: any) => t.text())).toEqual([
      '姓名→张新名',
      '职位→销售总监',
      '手机→13900000009',
      '座机→021-6666',
      // 后端哪天加字段而前端没同步 FIELD_LABELS：显示键名也比显示空白强——
      // 运营能照着键名问「这个 wechat 是什么」
      'wechat→wxid-new',
    ])
  })

  it('快照不是合法 JSON：显示「无」而不是白屏或整页报错', async () => {
    const w = await setup()
    // 第 23 条是脏数据；列表照常渲染、摘要位显示「无」
    expect(rowOf(w, 23).text()).toContain('无')
    expect(rowOf(w, 23).findAll('.sum-tag')).toHaveLength(0)
    expect(rowOf(w, 21).text()).not.toContain('未填写')
  })

  // 变更摘要列也是 el-tag（.sum-tag），直接 find('.el-tag') 会先命中它
  function statusTag(w: any, id: number) {
    return rowOf(w, id).findAll('.el-tag').find((t: any) => !t.classes().includes('sum-tag'))
  }

  it('三种状态渲染成不同文案与颜色的标签', async () => {
    const w = await setup()
    expect(statusTag(w, 21)!.text()).toBe('待审核')
    expect(statusTag(w, 22)!.text()).toBe('已通过')
    expect(statusTag(w, 23)!.text()).toBe('已驳回')
    expect(statusTag(w, 21)!.attributes('data-type')).toBe('success')
    expect(statusTag(w, 22)!.attributes('data-type')).toBe('primary')
    expect(statusTag(w, 23)!.attributes('data-type')).toBe('danger')
  })

  it('已处理的行只显示「已处理」，不再有明细/通过/驳回按钮', async () => {
    const w = await setup()
    expect(rowOf(w, 22).text()).toContain('已处理')
    expect(rowOf(w, 23).text()).toContain('已处理')
    expect(btnIn(rowOf(w, 22), '通过')).toBeUndefined()
    expect(btnIn(rowOf(w, 23), '驳回')).toBeUndefined()
    // 待审核行三个按钮都在
    expect(btnIn(rowOf(w, 21), '明细')).toBeDefined()
    expect(btnIn(rowOf(w, 21), '通过')).toBeDefined()
    expect(btnIn(rowOf(w, 21), '驳回')).toBeDefined()
  })

  // 分页改造后，为「显示旧值」拉全量员工表会把省下的传输又吃回去
  it('旧值只按本页涉及的 staffId 精确取，且去重', async () => {
    await setup()
    expect(mocks.listEmployees).toHaveBeenCalledWith({ staffIds: '7,8' })
  })

  it('手机端隐藏员工ID与提交时间两列', async () => {
    bp.isPhone.value = true
    const w = await setup()
    expect(w.find('.el-table-column[data-prop="staffId"]').exists()).toBe(false)
    expect(w.findAll('.el-table-column[data-prop="createdAt"]')).toHaveLength(0)
    // 桌面端这两列在
    bp.isPhone.value = false
    await flush()
    expect(w.find('.el-table-column[data-prop="staffId"]').exists()).toBe(true)
  })
})

describe('名片修改审核 · 变更明细（旧值 → 新值）', () => {
  beforeEach(resetAll)

  async function openDetail(w: any, id = 21) {
    await btnIn(rowOf(w, id), '明细').trigger('click')
    await flush()
    return dlgOf(w, '变更明细')
  }

  it('明细逐字段给出旧值与新值，取不到的旧值显示「（空）」', async () => {
    const w = await setup()
    const dlg = await openDetail(w)

    const items = dlg.findAll('.el-descriptions-item')
    expect(items.map((i: any) => i.attributes('data-label'))).toEqual(['姓名', '职位', '手机', '座机', 'wechat'])
    // 旧值来自员工表、新值来自快照：审批前必须能对照着看
    expect(items[0].text()).toContain('张旧名')
    expect(items[0].text()).toContain('张新名')
    expect(items[1].text()).toContain('销售经理')
    expect(items[1].text()).toContain('销售总监')
    // 员工表里没有 landline / wechat → 占位，不能渲染成 undefined 或空白
    expect(items[3].text()).toContain('（空）')
    expect(items[4].text()).toContain('（空）')
  })

  it('员工表取不到时明细仍能打开（旧值为空），不阻断审批', async () => {
    mocks.listEmployees.mockImplementation(async () => {
      throw new Error('boom')
    })
    const w = await setup()
    // 列表照常渲染：旧值只是展示增强，不该让整页打不开
    expect(w.findAll('.el-table-row')).toHaveLength(3)
    const dlg = await openDetail(w)
    expect(dlg.exists()).toBe(true)
    expect(dlg.findAll('.el-descriptions-item')).toHaveLength(5)
    expect(dlg.text()).toContain('（空）')
    // 新值仍来自快照，不受影响
    expect(dlg.text()).toContain('张新名')
  })

  it('在明细里点「通过此申请」：应用到员工并关闭明细', async () => {
    const w = await setup()
    const dlg = await openDetail(w)
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '通过此申请')!.trigger('click')
    await flush()

    expect(mocks.approveModify).toHaveBeenCalledWith(21)
    expect(ElMessage.success).toHaveBeenCalledWith('已通过，变更已应用到该员工')
    expect(dlgOf(w, '变更明细').exists()).toBe(false)
  })

  it('列表里点「通过」后立刻刷新：该行变「已处理」，不给第二次覆盖的机会', async () => {
    const w = await setup()
    mocks.approveModify.mockImplementation(async (id: number) => {
      const r = applies.find((a) => a.id === id)
      if (r) r.status = 'approved'
      return { code: 0 }
    })
    await btnIn(rowOf(w, 21), '通过').trigger('click')
    await flush()

    expect(mocks.listModifyApply.mock.calls.length).toBeGreaterThan(1)
    expect(rowOf(w, 21).text()).toContain('已处理')
    expect(btnIn(rowOf(w, 21), '通过')).toBeUndefined()
  })

  it('审批失败（已处理/并发）：不报成功、不关明细，运营能原地重试', async () => {
    mocks.approveModify.mockImplementation(async () => {
      throw new Error('该申请已处理')
    })
    const w = await setup()
    const dlg = await openDetail(w)
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '通过此申请')!.trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(dlgOf(w, '变更明细').exists()).toBe(true)
  })
})

describe('名片修改审核 · 驳回（原因必填 + 防重入）', () => {
  beforeEach(resetAll)

  async function openReject(w: any, id = 21) {
    await btnIn(rowOf(w, id), '驳回').trigger('click')
    await flush()
    return dlgOf(w, '驳回申请')
  }
  function okBtn(dlg: any) {
    return dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!
  }

  it('打开驳回窗时原因是空的——不带上一条的原因', async () => {
    const w = await setup()
    let dlg = await openReject(w, 21)
    await dlg.find('textarea.el-input').setValue('头像不清晰')
    await okBtn(dlg).trigger('click')
    await flush()
    // 再开一次：残留的原因会造成「驳回理由张冠李戴」
    dlg = await openReject(w, 21)
    expect((dlg.find('textarea.el-input').element as HTMLTextAreaElement).value).toBe('')
  })

  it('原因没填时「确定驳回」点不动；纯空格同样点不动', async () => {
    const w = await setup()
    const dlg = await openReject(w)
    expect(okBtn(dlg).attributes('disabled')).toBeDefined()

    await dlg.find('textarea.el-input').setValue('   ')
    await flush()
    expect(okBtn(dlg).attributes('disabled')).toBeDefined()
    expect(mocks.rejectModify).not.toHaveBeenCalled()
  })

  it('填了原因后可提交：trim 后原样发出', async () => {
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('  头像不清晰，请重传  ')
    await okBtn(dlg).trigger('click')
    await flush()

    expect(mocks.rejectModify).toHaveBeenCalledWith(21, '头像不清晰，请重传')
    expect(ElMessage.success).toHaveBeenCalledWith('已驳回')
  })

  it('驳回成功后关窗并刷新列表：该行变「已处理」且带上驳回原因', async () => {
    const w = await setup()
    mocks.rejectModify.mockImplementation(async (id: number, reason: string) => {
      const r = applies.find((a) => a.id === id)
      if (r) {
        r.status = 'rejected'
        r.rejectReason = reason
      }
      return { code: 0 }
    })
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('头像不清晰')
    await okBtn(dlg).trigger('click')
    await flush()

    expect(dlgOf(w, '驳回申请').exists()).toBe(false)
    expect(rowOf(w, 21).text()).toContain('已处理')
    expect(rowOf(w, 21).text()).toContain('头像不清晰')
  })

  // 驳回会写库并通知员工。手抖连点两下 = 两条驳回记录 + 员工收到两遍通知，
  // 而且第二次往往带着「申请已处理」的报错，看起来像系统坏了。
  it('连点两次「确定驳回」只发一次请求（rejecting 防重入）', async () => {
    let release: () => void = () => {}
    mocks.rejectModify.mockImplementation(
      () => new Promise<void>((res) => { release = () => res() })
    )
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('头像不清晰')
    await okBtn(dlg).trigger('click')
    await okBtn(dlg).trigger('click')

    expect(mocks.rejectModify).toHaveBeenCalledTimes(1)
    // 请求悬着的时候按钮进入 loading，用户看得出正在提交
    expect(okBtn(dlg).attributes('data-loading')).toBe('true')

    // 放开后走完：成功即关窗提示（按钮随弹窗卸载，不必再断 loading）
    release()
    await vi.waitFor(() => expect(dlgOf(w, '驳回申请').exists()).toBe(false))
    expect(ElMessage.success).toHaveBeenCalledWith('已驳回')
  })

  it('驳回失败：不关窗、不报成功，运营能原地改原因重试', async () => {
    mocks.rejectModify.mockImplementation(async () => {
      throw new Error('boom')
    })
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('头像不清晰')
    await okBtn(dlg).trigger('click')
    await flush()

    expect(dlgOf(w, '驳回申请').exists()).toBe(true)
    expect(ElMessage.success).not.toHaveBeenCalled()
    // 失败后按钮必须恢复可点（rejecting 在 finally 里复位），否则弹窗就死了
    expect(okBtn(dlgOf(w, '驳回申请')).attributes('data-loading')).toBe('false')
  })
})
