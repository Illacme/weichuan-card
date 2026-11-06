// 小程序端「管理员登录」页：账号密码登录 + 首登自动绑定微信 + 免密进入。
//
// 重点分支：
//   - 账号不存在与密码错误都原样透出后端文案，前端不许自己拼「该账号不存在」；
//   - 绑定微信失败**不阻断**登录（企业没配小程序凭据、或微信号已绑别人），
//     否则管理员连活都干不了；
//   - 免密登录失败要说清「先用账号密码登录」，而不是笼统「登录失败」。
import { describe, it, expect, beforeEach, vi } from 'vitest'
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
  wxLoginCode: vi.fn(async () => 'code-1'),
  companyId: 7,
}))

vi.mock('../utils/adminRequest', () => ({
  adminRequest: mocks.adminRequest,
  saveAdminAuth: mocks.saveAdminAuth,
  hasAdminToken: mocks.hasAdminToken,
}))
vi.mock('../utils/wechat', () => ({ wxLoginCode: mocks.wxLoginCode }))
vi.mock('../utils/tenant', () => ({ getCompanyId: () => mocks.companyId }))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  installApp({ myStaffId: 0 })
  mocks.adminRequest.mockClear()
  mocks.adminRequest.mockResolvedValue({ code: 0 })
  mocks.saveAdminAuth.mockClear()
  mocks.saveAdminAuth.mockReturnValue(true)
  mocks.hasAdminToken.mockReset()
  mocks.hasAdminToken.mockReturnValue(false)
  mocks.wxLoginCode.mockClear()
  mocks.wxLoginCode.mockResolvedValue('code-1')
  mocks.companyId = 7
})

async function page() {
  await import('../pages/admin/login/login')
  return makeInstance(capturedPage())
}

describe('账号密码登录', () => {
  it('账号或密码为空只提示，不发请求', async () => {
    const p = await page()
    p.setData({ username: '', password: '' })
    await p.onSubmit()
    await flush()
    expect(mocks.adminRequest).not.toHaveBeenCalled()
    expect(wxStub.showToast).toHaveBeenCalled()
  })

  it('密码错误：原样透出后端文案，且不存令牌', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 1, msg: '用户名或密码错误' })
    const p = await page()
    p.setData({ username: 'boss', password: 'bad' })
    await p.onSubmit()
    await flush()

    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '用户名或密码错误' }))
    expect(mocks.saveAdminAuth).not.toHaveBeenCalled()
    expect(wxStub.redirectTo).not.toHaveBeenCalled()
  })

  it('登录成功：存令牌 → 绑定微信 → 进审核页', async () => {
    mocks.adminRequest.mockImplementation(async (m: string, path: string) => {
      if (path === '/api/admin/login/mobile') return { code: 0, token: 'tk-1', refreshToken: 'rt-1' }
      if (path === '/api/admin/wechat/bind') return { code: 0 }
      return { code: 0 }
    })
    const p = await page()
    p.setData({ username: 'boss', password: 'pass123456' })
    await p.onSubmit()
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/login/mobile', {
      username: 'boss', password: 'pass123456',
    })
    expect(mocks.saveAdminAuth).toHaveBeenCalledWith({ token: 'tk-1', refreshToken: 'rt-1' })
    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/wechat/bind', { code: 'code-1' })
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
  })

  it('绑定微信失败不阻断登录：照常进审核页，只提示下次仍需输密码', async () => {
    mocks.adminRequest.mockImplementation(async (m: string, path: string) => {
      if (path === '/api/admin/login/mobile') return { code: 0, token: 'tk-1', refreshToken: 'rt-1' }
      if (path === '/api/admin/wechat/bind') return { code: 1, msg: '未配置小程序 AppID' }
      return { code: 0 }
    })
    const p = await page()
    p.setData({ username: 'boss', password: 'pass123456' })
    await p.onSubmit()
    await flush()

    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '未配置小程序 AppID' }))
  })
})

describe('微信免密登录', () => {
  it('成功：存令牌并进审核页', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 0, token: 'tk-wx', refreshToken: 'rt-wx' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/login/wechat', { code: 'code-1', companyId: 7 })
    expect(mocks.saveAdminAuth).toHaveBeenCalledWith({ token: 'tk-wx', refreshToken: 'rt-wx' })
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
  })

  it('未绑定（code:3）：modal 说清先用账号密码登录，不跳转', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 3, msg: '该微信还没有绑定管理员账号，请先用账号密码登录' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    const modal = wxStub.showModal.mock.calls[0][0]
    expect(String(modal.content)).toContain('账号密码登录')
    expect(wxStub.redirectTo).not.toHaveBeenCalled()
  })
})

describe('已登录', () => {
  it('带令牌进登录页直接跳到审核页', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    const p = await page()
    p.onLoad()
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
  })
})
