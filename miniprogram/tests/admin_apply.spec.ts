// 小程序端「企业审核」页：身份兜底、列表、通过/驳回、错误态。
//
// 这条链路的价值是「管理员在手机上两下点完」，所以测试要钉住的是那些会让管理员
// 「看到空列表以为没活」「点了没反应」「通过后拿不到初始密码」的分支。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  installPageCapture,
  capturedPage,
  makeInstance,
  installWx,
  installApp,
  flush,
} from './helpers/harness'

const mocks = vi.hoisted(() => ({
  adminRequest: vi.fn(async (_m: string, _p: string, _d?: any): Promise<any> => ({})),
  saveAdminAuth: vi.fn(() => true),
  hasAdminToken: vi.fn(() => false),
  clearAdminAuth: vi.fn(),
  wxLoginCode: vi.fn(async () => 'code-1'),
  companyId: 7,
}))

vi.mock('../utils/adminRequest', () => ({
  adminRequest: mocks.adminRequest,
  saveAdminAuth: mocks.saveAdminAuth,
  hasAdminToken: mocks.hasAdminToken,
  clearAdminAuth: mocks.clearAdminAuth,
}))
vi.mock('../utils/wechat', () => ({ wxLoginCode: mocks.wxLoginCode }))
vi.mock('../utils/tenant', () => ({ getCompanyId: () => mocks.companyId }))

const HERE = dirname(fileURLToPath(import.meta.url))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  installApp({ myStaffId: 0 })
  // showModal 默认按「用户点了确定」应答；需要验证取消的用例自行覆写
  wxStub.showModal = vi.fn((o: any) => o && o.success && o.success({ confirm: true }))
  mocks.adminRequest.mockClear()
  mocks.adminRequest.mockResolvedValue({ code: 0, data: [], total: 0, page: 1, size: 20 })
  mocks.saveAdminAuth.mockClear()
  mocks.saveAdminAuth.mockReturnValue(true)
  mocks.hasAdminToken.mockReset()
  mocks.hasAdminToken.mockReturnValue(false)
  mocks.clearAdminAuth.mockClear()
  mocks.wxLoginCode.mockClear()
  mocks.wxLoginCode.mockResolvedValue('code-1')
  mocks.companyId = 7
})

async function page() {
  await import('../pages/admin/apply/apply')
  return makeInstance(capturedPage())
}

describe('身份兜底', () => {
  it('已有 adminToken：直接拉列表，不再走微信登录', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValueOnce({ code: 0, data: [{ id: 1, name: '张三', status: 'pending', createdAt: '2026-11-06T10:00:00+08:00' }], total: 1 })
    const p = await page()
    p.onShow()
    await flush()

    expect(mocks.wxLoginCode).not.toHaveBeenCalled()
    expect(mocks.adminRequest).toHaveBeenCalledWith('GET', '/api/admin/apply/open', { page: 1, size: 20, status: 'pending' })
    expect(p.data.list).toHaveLength(1)
  })

  it('无令牌但微信已绑定：免密登录后自动进列表（管理员第二次进来零输入）', async () => {
    mocks.adminRequest.mockImplementation(async (m: string, path: string) => {
      if (path === '/api/admin/login/wechat') {
        return { code: 0, token: 'tk-1', refreshToken: 'rt-1', username: 'boss' }
      }
      return { code: 0, data: [], total: 0 }
    })
    const p = await page()
    p.onShow()
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/login/wechat', { code: 'code-1', companyId: 7 })
    expect(mocks.saveAdminAuth).toHaveBeenCalledWith({ token: 'tk-1', refreshToken: 'rt-1' })
    expect(wxStub.redirectTo).not.toHaveBeenCalled()
  })

  it('无令牌且未绑定：跳管理员登录页（不说「登录失败」就完事）', async () => {
    mocks.adminRequest.mockResolvedValue({ code: 3, msg: '该微信还没有绑定管理员账号，请先用账号密码登录' })
    const p = await page()
    p.onShow()
    await flush()

    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/login/login' })
  })

  it('列表返回 401：清令牌并跳登录页，而不是停在空列表', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValue({ code: 401, msg: '登录失效' })
    const p = await page()
    p.onShow()
    await flush()

    expect(mocks.clearAdminAuth).toHaveBeenCalled()
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/login/login' })
  })
})

describe('列表与筛选', () => {
  it('开通申请卡片：时间被格式化，联系方式进 lines', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValueOnce({
      code: 0,
      total: 1,
      data: [{ id: 9, name: '李四', phone: '13800000000', email: 'l@a.com', title: '销售', status: 'pending', createdAt: '2026-11-06T09:05:00+08:00' }],
    })
    const p = await page()
    p.onShow()
    await flush()

    const row = p.data.list[0]
    expect(row.name).toBe('李四')
    expect(row.time).toBe('2026-11-06 09:05')
    expect(row.lines).toEqual(['手机：13800000000', '邮箱：l@a.com', '职位：销售'])
    expect(row.statusText).toBe('待审核')
    expect(row.pending).toBe(true)
  })

  it('修改申请卡片：snapshot 解析成可读行，头像不摊开对象键', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    const p = await page()
    p.setData({ tab: 'modify' })
    mocks.adminRequest.mockResolvedValueOnce({
      code: 0,
      total: 1,
      data: [{
        id: 3, staffId: 12, status: 'pending', createdAt: '2026-11-06T08:00:00+08:00',
        snapshot: JSON.stringify({ name: '王五', phone: '', avatar: 'uploads/a.jpg' }),
      }],
    })
    p.onShow()
    await flush()

    const row = p.data.list[0]
    expect(row.name).toBe('员工编号 12')
    expect(row.lines).toContain('姓名：王五')
    expect(row.lines).toContain('手机：（清空）')
    expect(row.lines).toContain('头像：已更换')
  })

  it('切页签 / 切筛选都会重置到第一页并带对参数', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValue({ code: 0, data: [], total: 0 })
    const p = await page()

    p.onTabChange({ currentTarget: { dataset: { tab: 'modify' } } })
    await flush()
    expect(mocks.adminRequest).toHaveBeenLastCalledWith('GET', '/api/admin/apply/modify', { page: 1, size: 20, status: 'pending' })

    p.onStatusChange({ currentTarget: { dataset: { status: 'all' } } })
    await flush()
    expect(mocks.adminRequest).toHaveBeenLastCalledWith('GET', '/api/admin/apply/modify', { page: 1, size: 20 })
  })

  it('加载更多按 total 判定是否还有下一页', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValueOnce({ code: 0, total: 3, data: [{ id: 1, status: 'pending' }, { id: 2, status: 'pending' }] })
    const p = await page()
    p.onShow()
    await flush()
    expect(p.data.hasMore).toBe(true)

    mocks.adminRequest.mockResolvedValueOnce({ code: 0, total: 3, data: [{ id: 3, status: 'pending' }] })
    p.onReachBottom()
    await flush()
    expect(p.data.list).toHaveLength(3)
    expect(p.data.hasMore).toBe(false)
  })

  it('接口失败落到可重试的错误态，而不是空列表', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValueOnce({ code: 1, msg: '加载失败' })
    const p = await page()
    p.onShow()
    await flush()
    expect(p.data.error).toBe('加载失败')
    expect(p.data.list).toHaveLength(0)

    mocks.adminRequest.mockResolvedValueOnce({ code: 0, total: 1, data: [{ id: 1, status: 'pending' }] })
    p.onRetry()
    await flush()
    expect(p.data.error).toBe('')
    expect(p.data.list).toHaveLength(1)
  })
})

describe('审批动作', () => {
  it('通过开通申请：确认后调 approve，并把员工编号与初始密码弹给管理员', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockImplementation(async (m: string, path: string) => {
      if (path === '/api/admin/apply/open/9/approve') {
        return { code: 0, staffId: 1024, initialPassword: 'pwd-888' }
      }
      return { code: 0, data: [], total: 0 }
    })
    const p = await page()
    p.onShow()
    await flush()
    wxStub.showModal.mockClear()

    await p.onApprove({ currentTarget: { dataset: { id: 9 } } })
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/apply/open/9/approve')
    const created = wxStub.showModal.mock.calls.find((c: any[]) => String(c[0].title).indexOf('已通过') === 0)
    expect(created, '必须把初始密码展示出来，否则审核人没法把账号交给员工').toBeTruthy()
    expect(String(created[0].content)).toContain('1024')
    expect(String(created[0].content)).toContain('pwd-888')
  })

  it('取消确认就不发请求（通过是不可逆动作）', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    const p = await page()
    p.onShow()
    await flush()
    mocks.adminRequest.mockClear()
    wxStub.showModal = vi.fn((o: any) => o && o.success && o.success({ confirm: false }))

    await p.onApprove({ currentTarget: { dataset: { id: 9 } } })
    await flush()
    expect(mocks.adminRequest).not.toHaveBeenCalled()
  })

  it('驳回带理由：理由原样进请求体，成功后关弹窗并刷新', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValue({ code: 0, data: [], total: 0 })
    const p = await page()
    p.onShow()
    await flush()

    p.onRejectTap({ currentTarget: { dataset: { id: 5 } } })
    expect(p.data.rejectVisible).toBe(true)
    p.onRejectInput({ detail: { value: '  照片不符合要求  ' } })
    await p.onRejectConfirm()
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/apply/open/5/reject', { reason: '照片不符合要求' })
    expect(p.data.rejectVisible).toBe(false)
  })

  it('驳回失败时保留弹窗与理由，管理员不用重新输入', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    const p = await page()
    p.setData({ tab: 'modify' })
    p.onShow()
    await flush()

    p.onRejectTap({ currentTarget: { dataset: { id: 7 } } })
    p.onRejectInput({ detail: { value: '资料与工牌不符' } })
    mocks.adminRequest.mockResolvedValueOnce({ code: 1, msg: '未找到或已处理' })
    await p.onRejectConfirm()
    await flush()

    expect(p.data.rejectVisible).toBe(true)
    expect(p.data.rejectReason).toBe('资料与工牌不符')
  })
})

describe('静态守门', () => {
  const appJSON = JSON.parse(readFileSync(resolve(HERE, '../app.json'), 'utf8'))
  const applyTS = readFileSync(resolve(HERE, '../pages/admin/apply/apply.ts'), 'utf8')
  const applyWXML = readFileSync(resolve(HERE, '../pages/admin/apply/apply.wxml'), 'utf8').replace(/<!--[\s\S]*?-->/g, '')
  const loginTS = readFileSync(resolve(HERE, '../pages/admin/login/login.ts'), 'utf8')
  const meWXML = readFileSync(resolve(HERE, '../pages/employee/me/me.wxml'), 'utf8')

  it('两个新页面已注册，且默认入口没被改动', () => {
    expect(appJSON.pages).toContain('pages/admin/login/login')
    expect(appJSON.pages).toContain('pages/admin/apply/apply')
    expect(appJSON.entryPagePath).toBe('pages/team/team')
  })

  it('审核页用管理员通道，绝不混用员工 request（否则带的是 empToken，每条都 403）', () => {
    expect(applyTS).toContain("from '../../../utils/adminRequest'")
    expect(applyTS).not.toMatch(/from '\.\.\/\.\.\/\.\.\/utils\/request'/)
    expect(applyTS).toContain("'/api/admin/apply/'")
  })

  it('驳回理由用页内 textarea，不用 wx.showModal 的 editable（低版本会吞输入）', () => {
    expect(applyWXML).toContain('<textarea')
    expect(applyWXML).toContain('onRejectInput')
    expect(applyTS).not.toContain('editable: true')
  })

  it('登录页走移动端登录端点，且不把密码写进日志/Storage', () => {
    expect(loginTS).toContain("'/api/admin/login/mobile'")
    expect(loginTS).toContain('saveAdminAuth')
    expect(loginTS).not.toContain('console.log')
    expect(loginTS).not.toContain('setStorageSync(\'adminPassword\'')
  })

  it('「我的」页有审核入口，且挂在登录态判断之外', () => {
    expect(meWXML).toContain('bindtap="goAdminApply"')
    const guestStart = meWXML.indexOf('wx:if="{{!me}}"')
    const entry = meWXML.indexOf('goAdminApply')
    const blockEnd = meWXML.indexOf('</block>')
    expect(guestStart).toBeGreaterThan(-1)
    expect(entry).toBeGreaterThan(blockEnd)
  })
})
