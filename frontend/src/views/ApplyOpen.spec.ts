import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { mountOpts, flush, paged, rowOf, btnIn, dlgOf } from '@/test/elStubs'
import ApplyOpen from './ApplyOpen.vue'

// 开通审核页。
//
// 这个页面的失败模式很具体，而且每一种都不可撤销：
//   1. 「通过」= 立刻建员工账号 + 生成一次性初始密码。密码只在弹窗里出现这一次，
//      弹窗没弹出来/没刷列表，运营就只能重置账号，员工那边干等。
//   2. 同一条申请被点两次「通过」= 建两个员工账号。列表若不就地刷新，
//      按钮就一直在那儿等人点第二下。
//   3. 驳回原因没填就提交，员工收到一条没有理由的驳回——必然再来问一次。
// 所以这里钉住：密码弹窗必现且带「仅展示一次」、审批后行就地变「已处理」、
// 驳回原因必填（空/纯空格都点不动）、关键字与页码下沉到后端。

const mocks = vi.hoisted(() => ({
  listOpenApply: vi.fn(),
  approveOpen: vi.fn(),
  rejectOpen: vi.fn(),
}))

vi.mock('@/api/apply', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({
  Refresh: { render: () => null },
  Search: { render: () => null },
}))
// 桌面端是默认档（手机端卡片列表单独一条用例翻 isPhone）：
// 两档共用同一份行数据，只有渲染容器不同，断言按桌面列写
const bp = vi.hoisted(() => ({ isPhone: null as any }))
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  bp.isPhone = ref(false)
  return { useBreakpoint: () => ({ isPhone: bp.isPhone }) }
})

import { ElMessage } from 'element-plus'

// 三条申请覆盖三种状态：待审核（可操作）/ 已通过 / 已驳回（都只能看）
function mkApplies() {
  return [
    {
      id: 11, name: '张三', phone: '13800000001', phone2: '13900000002', nickname: '小张',
      openid: 'o_abc123', landline: '021-8888', email: 'zhang@a.com', title: '销售经理',
      avatar: '', status: 'pending', rejectReason: '', createdAt: '2026-10-20T10:00:00',
    },
    {
      id: 12, name: '李四', phone: '13800000003', phone2: '', nickname: '', openid: '',
      landline: '', email: '', title: '技术', avatar: 'https://cdn/a.png',
      status: 'approved', rejectReason: '', createdAt: '2026-10-21T10:00:00',
    },
    {
      id: 13, name: '王五', phone: '13800000005', phone2: '', nickname: '', openid: '',
      landline: '', email: '', title: '售后', avatar: '',
      status: 'rejected', rejectReason: '手机号不实', createdAt: '2026-10-22T10:00:00',
    },
  ]
}

let applies: any[] = []

async function setup() {
  const w = mount(ApplyOpen, mountOpts)
  await vi.waitFor(() => expect(mocks.listOpenApply).toHaveBeenCalled())
  await flush()
  return w
}

function resetAll() {
  applies = mkApplies()
  bp.isPhone.value = false
  mocks.listOpenApply.mockReset()
  mocks.approveOpen.mockReset()
  mocks.rejectOpen.mockReset()
  mocks.listOpenApply.mockImplementation(async () => paged(applies))
  mocks.approveOpen.mockImplementation(async () => ({ staffId: 1001, initialPassword: 'Ab3!x9Qz' }))
  mocks.rejectOpen.mockImplementation(async () => ({ code: 0 }))
  vi.mocked(ElMessage.success).mockClear()
}

describe('开通审核 · 列表与状态', () => {
  beforeEach(resetAll)

  it('首屏按服务端分页拉列表（page/size/keyword 一起下沉）', async () => {
    await setup()
    expect(mocks.listOpenApply).toHaveBeenCalledWith({ page: 1, size: 20, keyword: '' })
  })

  it('三种状态渲染成不同文案与颜色的标签', async () => {
    const w = await setup()
    const tag = (id: number) => rowOf(w, id).find('.el-tag')
    expect(tag(11).text()).toBe('待审核')
    expect(tag(12).text()).toBe('已通过')
    expect(tag(13).text()).toBe('已驳回')
    expect(tag(11).attributes('data-type')).toBe('success')
    expect(tag(12).attributes('data-type')).toBe('primary')
    expect(tag(13).attributes('data-type')).toBe('danger')
  })

  it('已处理的行只显示「已处理」，不再有通过/驳回按钮', async () => {
    const w = await setup()
    expect(rowOf(w, 12).text()).toContain('已处理')
    expect(rowOf(w, 13).text()).toContain('已处理')
    expect(btnIn(rowOf(w, 12), '通过')).toBeUndefined()
    expect(btnIn(rowOf(w, 13), '驳回')).toBeUndefined()
    // 待审核行两个按钮都在
    expect(btnIn(rowOf(w, 11), '通过')).toBeDefined()
    expect(btnIn(rowOf(w, 11), '驳回')).toBeDefined()
  })

  it('主号下方带出副号，缺失的昵称/openid 显示占位符而不是空白', async () => {
    const w = await setup()
    const r = rowOf(w, 11)
    expect(r.find('.sub-phone').text()).toBe('13900000002')
    expect(r.text()).toContain('小张')
    // 李四昵称/openid 都为空 → 破折号占位，运营一眼看出是「没授权」而不是「加载失败」
    expect(rowOf(w, 12).text()).toContain('—')
  })

  it('关键字输入后防抖再查，且回到第 1 页（不在第 3 页查新词）', async () => {
    const w = await setup()
    await w.find('input.el-input').setValue('张三')
    await vi.waitFor(() =>
      expect(mocks.listOpenApply).toHaveBeenCalledWith(expect.objectContaining({ keyword: '张三', page: 1 }))
    )
  })

  it('手机端改用卡片列表，通过/驳回与「已处理」照旧可辨', async () => {
    bp.isPhone.value = true
    const w = await setup()
    expect(w.find('.el-table').exists()).toBe(false)
    const cards = w.findAll('.apply-card')
    expect(cards).toHaveLength(3)
    expect(cards[0].text()).toContain('张三')
    expect(cards[0].findAll('button.el-button')).toHaveLength(2)
    // 已处理的一张：无按钮、有文案
    expect(cards[1].findAll('button.el-button')).toHaveLength(0)
    expect(cards[1].text()).toContain('已处理')
  })
})

describe('开通审核 · 通过（建号 + 一次性初始密码）', () => {
  beforeEach(resetAll)

  it('通过后弹出开通成功窗，展示员工编号与初始密码，并说明仅展示一次', async () => {
    const w = await setup()
    await btnIn(rowOf(w, 11), '通过').trigger('click')
    await flush()

    expect(mocks.approveOpen).toHaveBeenCalledWith(11)
    const dlg = dlgOf(w, '开通成功')
    expect(dlg.exists()).toBe(true)
    expect(dlg.text()).toContain('1001')
    expect(dlg.text()).toContain('Ab3!x9Qz')
    // 密码不再二次出现（没有「再次查看」这类入口），文案必须把这件事说清
    expect(dlg.text()).toContain('仅展示一次')
  })

  it('通过后立刻刷新列表，该行变「已处理」——不给第二次点击的机会', async () => {
    const w = await setup()
    // 后端在建号后会把这条置为 approved；列表不刷新的话按钮就还在
    mocks.approveOpen.mockImplementation(async (id: number) => {
      const r = applies.find((a) => a.id === id)
      if (r) r.status = 'approved'
      return { staffId: 1001, initialPassword: 'Ab3!x9Qz' }
    })
    await btnIn(rowOf(w, 11), '通过').trigger('click')
    await flush()

    expect(mocks.listOpenApply.mock.calls.length).toBeGreaterThan(1)
    expect(rowOf(w, 11).text()).toContain('已处理')
    expect(btnIn(rowOf(w, 11), '通过')).toBeUndefined()
  })

  it('点「知道了」关闭密码窗', async () => {
    const w = await setup()
    await btnIn(rowOf(w, 11), '通过').trigger('click')
    await flush()
    await dlgOf(w, '开通成功')
      .findAll('button.el-button')
      .find((b: any) => b.text() === '知道了')!
      .trigger('click')
    await flush()
    expect(dlgOf(w, '开通成功').exists()).toBe(false)
  })

  it('审批失败（后端已处理/并发）：不弹密码窗，不给没有密码的空弹窗', async () => {
    mocks.approveOpen.mockImplementation(async () => {
      throw new Error('该申请已处理')
    })
    const w = await setup()
    await btnIn(rowOf(w, 11), '通过').trigger('click')
    await flush()

    expect(dlgOf(w, '开通成功').exists()).toBe(false)
    expect(ElMessage.success).not.toHaveBeenCalled()
    // 失败也不能把按钮卡在 loading（actingId 在 finally 里复位）
    expect(btnIn(rowOf(w, 11), '通过').attributes('data-loading')).toBe('false')
  })
})

describe('开通审核 · 驳回（原因必填）', () => {
  beforeEach(resetAll)

  async function openReject(w: any, id = 11) {
    await btnIn(rowOf(w, id), '驳回').trigger('click')
    await flush()
    return dlgOf(w, '驳回申请')
  }

  it('打开驳回窗时原因是空的——不带上一条的原因', async () => {
    const w = await setup()
    // 先驳回一次填了原因，再打开另一条：残留原因会造成「驳回理由张冠李戴」
    let dlg = await openReject(w, 11)
    await dlg.find('textarea.el-input').setValue('资料不全')
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!.trigger('click')
    await flush()
    dlg = await openReject(w, 11)
    expect((dlg.find('textarea.el-input').element as HTMLTextAreaElement).value).toBe('')
  })

  it('原因没填时「确定驳回」点不动；纯空格同样点不动', async () => {
    const w = await setup()
    const dlg = await openReject(w)
    const ok = () => dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!
    expect(ok().attributes('disabled')).toBeDefined()

    await dlg.find('textarea.el-input').setValue('   ')
    await flush()
    expect(ok().attributes('disabled')).toBeDefined()
    expect(mocks.rejectOpen).not.toHaveBeenCalled()
  })

  it('填了原因后可提交：trim 后原样发出', async () => {
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('  手机号与微信不一致  ')
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!.trigger('click')
    await flush()

    expect(mocks.rejectOpen).toHaveBeenCalledWith(11, '手机号与微信不一致')
    expect(ElMessage.success).toHaveBeenCalledWith('已驳回')
  })

  it('驳回成功后关窗并刷新列表：该行变「已处理」且带上驳回原因', async () => {
    const w = await setup()
    mocks.rejectOpen.mockImplementation(async (id: number, reason: string) => {
      const r = applies.find((a) => a.id === id)
      if (r) {
        r.status = 'rejected'
        r.rejectReason = reason
      }
      return { code: 0 }
    })
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('资料不全')
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!.trigger('click')
    await flush()

    expect(dlgOf(w, '驳回申请').exists()).toBe(false)
    expect(rowOf(w, 11).text()).toContain('已处理')
    expect(rowOf(w, 11).text()).toContain('资料不全')
  })

  it('驳回失败：不关窗、不报成功，运营能原地改原因重试', async () => {
    mocks.rejectOpen.mockImplementation(async () => {
      throw new Error('boom')
    })
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('资料不全')
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '确定驳回')!.trigger('click')
    await flush()

    expect(dlgOf(w, '驳回申请').exists()).toBe(true)
    expect(ElMessage.success).not.toHaveBeenCalled()
  })

  it('取消关闭弹窗不发请求', async () => {
    const w = await setup()
    const dlg = await openReject(w)
    await dlg.find('textarea.el-input').setValue('资料不全')
    await dlg.findAll('button.el-button').find((b: any) => b.text() === '取消')!.trigger('click')
    await flush()
    expect(dlgOf(w, '驳回申请').exists()).toBe(false)
    expect(mocks.rejectOpen).not.toHaveBeenCalled()
  })
})
