// 小程序端「管理员登录」页：账号密码登录 + 首登自动绑定微信 + 免密进入。
//
// 重点分支：
//   - 账号不存在与密码错误都原样透出后端文案，前端不许自己拼「该账号不存在」；
//   - 绑定微信失败**不阻断**登录（企业没配小程序凭据、或微信号已绑别人），
//     否则管理员连活都干不了；
//   - 免密登录失败要说清「先用账号密码登录」，而不是笼统「登录失败」；
//   - 429 是「共享出口 IP 的登录桶满了」，不是「免密登录不可用」——
//     用户一下都没登却看到「登录尝试过于频繁」，真机反馈说这提示页不合理。
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
  markAdminWxBound: vi.fn(),
  wxLoginCode: vi.fn(async () => 'code-1'),
  companyId: 7,
  canTrySilentLogin: vi.fn(() => false),
  trySilentAdminLogin: vi.fn(async () => false),
}))

vi.mock('../utils/adminRequest', () => ({
  adminRequest: mocks.adminRequest,
  saveAdminAuth: mocks.saveAdminAuth,
  hasAdminToken: mocks.hasAdminToken,
  markAdminWxBound: mocks.markAdminWxBound,
}))
vi.mock('../utils/wechat', () => ({ wxLoginCode: mocks.wxLoginCode }))
vi.mock('../utils/tenant', () => ({ getCompanyId: () => mocks.companyId }))
vi.mock('../utils/adminEntry', () => ({
  REVIEW_PAGE: '/pages/admin/apply/apply',
  canTrySilentLogin: mocks.canTrySilentLogin,
  trySilentAdminLogin: mocks.trySilentAdminLogin,
}))

const HERE = dirname(fileURLToPath(import.meta.url))

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
  mocks.markAdminWxBound.mockReset()
  mocks.wxLoginCode.mockClear()
  mocks.wxLoginCode.mockResolvedValue('code-1')
  mocks.companyId = 7
  mocks.canTrySilentLogin.mockReset()
  mocks.canTrySilentLogin.mockReturnValue(false)
  mocks.trySilentAdminLogin.mockReset()
  mocks.trySilentAdminLogin.mockResolvedValue(false)
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

  it('登录成功：存令牌 → 绑定微信 → 标记可静默免密 → 进审核页', async () => {
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
    expect(mocks.markAdminWxBound).toHaveBeenCalled()
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
  })

  it('绑定微信失败不阻断登录：照常进审核页，也绝不把本机标成「已绑定」', async () => {
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
    expect(mocks.markAdminWxBound).not.toHaveBeenCalled()
  })

  it('密码登录撞上 429：说「请稍后再试」，不要甩一句 toast 就完事', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 429, msg: '登录尝试过于频繁，请稍后再试' })
    const p = await page()
    p.setData({ username: 'boss', password: 'right-pwd' })
    await p.onSubmit()
    await flush()

    const modal = wxStub.showModal.mock.calls[0][0]
    expect(modal.title).toBe('请稍后再试')
    expect(String(modal.content)).toContain('账号密码登录')
    expect(mocks.saveAdminAuth).not.toHaveBeenCalled()
  })
})

describe('微信免密登录', () => {
  it('成功：存令牌 + 标记已绑定，并进审核页', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 0, token: 'tk-wx', refreshToken: 'rt-wx' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/login/wechat', { code: 'code-1', companyId: 7 })
    expect(mocks.saveAdminAuth).toHaveBeenCalledWith({ token: 'tk-wx', refreshToken: 'rt-wx' })
    expect(mocks.markAdminWxBound).toHaveBeenCalled()
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
  })

  it('未绑定（code:3）：modal 说清先用账号密码登录，不跳转', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 3, msg: '该微信还没有绑定管理员账号，请先用账号密码登录' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    const modal = wxStub.showModal.mock.calls[0][0]
    expect(modal.title).toBe('免密登录不可用')
    expect(String(modal.content)).toContain('账号密码登录')
    expect(wxStub.redirectTo).not.toHaveBeenCalled()
  })

  it('限流（code:429）：不再套「免密登录不可用」，改成让用户稍后或走账号密码', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 429, msg: '登录尝试过于频繁，请稍后再试' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    const modal = wxStub.showModal.mock.calls[0][0]
    expect(modal.title).toBe('请稍后再试')
    expect(modal.title).not.toBe('免密登录不可用')
    expect(String(modal.content)).toContain('稍等一分钟')
    expect(String(modal.content)).toContain('账号密码登录')
    expect(String(modal.content)).not.toContain('登录尝试过于频繁')
  })

  it('其他失败：标题回归「登录失败」，原样透出后端文案', async () => {
    mocks.adminRequest.mockResolvedValueOnce({ code: 1, msg: '企业已停用' })
    const p = await page()
    await p.onWechatLogin()
    await flush()

    const modal = wxStub.showModal.mock.calls[0][0]
    expect(modal.title).toBe('登录失败')
    expect(modal.content).toBe('企业已停用')
  })
})

describe('登录页的一次性静默免密', () => {
  it('onLoad 允许静默且成功：直接进审核页，checking 归位', async () => {
    mocks.canTrySilentLogin.mockReturnValue(true)
    mocks.trySilentAdminLogin.mockResolvedValue(true)
    const p = await page()
    await p.onLoad()
    await flush()

    expect(mocks.trySilentAdminLogin).toHaveBeenCalledTimes(1)
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
    expect(p.data.checking).toBe(false)
  })

  it('静默失败：安静留在登录页，不弹窗、不 toast', async () => {
    mocks.canTrySilentLogin.mockReturnValue(true)
    mocks.trySilentAdminLogin.mockResolvedValue(false)
    const p = await page()
    await p.onLoad()
    await flush()

    expect(wxStub.redirectTo).not.toHaveBeenCalled()
    expect(wxStub.showModal).not.toHaveBeenCalled()
    expect(wxStub.showToast).not.toHaveBeenCalled()
    expect(p.data.checking).toBe(false)
  })

  it('未过准入门槛：一次登录请求都不发', async () => {
    const p = await page()
    await p.onLoad()
    await flush()
    expect(mocks.trySilentAdminLogin).not.toHaveBeenCalled()
    expect(mocks.adminRequest).not.toHaveBeenCalled()
  })

  it('静默校验在途时，两个按钮都不响应（避免撞成两次登录请求）', async () => {
    const p = await page()
    p.setData({ checking: true, username: 'boss', password: 'pwd' })
    await p.onSubmit()
    await p.onWechatLogin()
    await flush()
    expect(mocks.adminRequest).not.toHaveBeenCalled()
  })
})

describe('已登录', () => {
  it('带令牌进登录页直接跳到审核页，且不触发静默尝试', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    const p = await page()
    p.onLoad()
    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/admin/apply/apply' })
    expect(mocks.canTrySilentLogin).not.toHaveBeenCalled()
  })
})

describe('静态守门', () => {
  const loginWXML = readFileSync(resolve(HERE, '../pages/admin/login/login.wxml'), 'utf8')
  const loginWXSS = readFileSync(resolve(HERE, '../pages/admin/login/login.wxss'), 'utf8')

  it('按钮显式给定宽度、居中和单行，不再吃 button 默认的收缩宽度', () => {
    expect(loginWXSS).toContain('width: 686rpx')
    expect(loginWXSS).toContain('text-align: center')
    expect(loginWXSS).toContain('white-space: nowrap')
    expect(loginWXSS).toContain('line-height: 88rpx')
  })

  it('两个按钮在 checking 期间一起禁用，并给出可见的校验中提示', () => {
    expect(loginWXML).toContain('{{checking || submitting}}')
    expect(loginWXML).toContain('{{checking || wxSubmitting}}')
    expect(loginWXML).toContain('正在验证管理员身份')
  })
})
