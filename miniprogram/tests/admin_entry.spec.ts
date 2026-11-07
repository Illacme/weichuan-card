// 管理员入口决策层：静默免密的准入门槛、冷却窗口、导航目标页、失败静默。
//
// 这一层是 2026-11-07 真机反馈的直接产物，测试必须钉住「不许再偷偷打登录接口」：
// 旧实现每次进审核页 onShow 都自动 POST /api/admin/login/wechat，
// 与账号密码登录共用 login:<IP> 限流桶，把用户真正点按钮时的额度挤没了。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { installWx } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  adminRequest: vi.fn(async (_m: string, _p: string, _d?: any): Promise<any> => ({})),
  saveAdminAuth: vi.fn(() => true),
  hasAdminToken: vi.fn(() => false),
  clearAdminAuth: vi.fn(),
  markAdminWxBound: vi.fn(),
  hasAdminWxBound: vi.fn(() => false),
  clearAdminWxBound: vi.fn(),
  wxLoginCode: vi.fn(async () => 'code-1'),
  companyId: 7,
}))

vi.mock('../utils/adminRequest', () => ({
  adminRequest: mocks.adminRequest,
  saveAdminAuth: mocks.saveAdminAuth,
  hasAdminToken: mocks.hasAdminToken,
  clearAdminAuth: mocks.clearAdminAuth,
  markAdminWxBound: mocks.markAdminWxBound,
  hasAdminWxBound: mocks.hasAdminWxBound,
  clearAdminWxBound: mocks.clearAdminWxBound,
}))
vi.mock('../utils/wechat', () => ({ wxLoginCode: mocks.wxLoginCode }))
vi.mock('../utils/tenant', () => ({ getCompanyId: () => mocks.companyId }))

import {
  REVIEW_PAGE,
  LOGIN_PAGE,
  SILENT_COOLDOWN_MS,
  canTrySilentLogin,
  clearSilentLoginCooldown,
  trySilentAdminLogin,
  endAdminSession,
  goAdminArea,
} from '../utils/adminEntry'

const HERE = dirname(fileURLToPath(import.meta.url))

// 源码守门只看真代码：注释里会写「旧版曾经在这里 POST xxx」这类追溯说明，
// 不剥注释就会把解释性文字误判成现存实现（项目里踩过）。
function code(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')
}

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  mocks.adminRequest.mockReset()
  mocks.adminRequest.mockResolvedValue({ code: 0, token: 'tk-1', refreshToken: 'rt-1' })
  mocks.saveAdminAuth.mockReset()
  mocks.saveAdminAuth.mockReturnValue(true)
  mocks.hasAdminToken.mockReset()
  mocks.hasAdminToken.mockReturnValue(false)
  mocks.clearAdminAuth.mockReset()
  mocks.markAdminWxBound.mockReset()
  mocks.hasAdminWxBound.mockReset()
  mocks.hasAdminWxBound.mockReturnValue(false)
  mocks.clearAdminWxBound.mockReset()
  mocks.wxLoginCode.mockReset()
  mocks.wxLoginCode.mockResolvedValue('code-1')
  mocks.companyId = 7
})

describe('静默免密的准入门槛', () => {
  it('从未探测过（升级后首次）：允许一次探测，让微信绑着管理员的人能零输入直达', () => {
    expect(canTrySilentLogin(1_000_000_000_000)).toBe(true)
  })

  it('探测过但没绑过管理员：此后恒 false（纯访客 / 只登过员工的微信不再打登录接口）', async () => {
    mocks.adminRequest.mockResolvedValue({ code: 3, msg: '该微信还没有绑定管理员账号' })
    const now = 1_700_000_000_000
    await trySilentAdminLogin(now)
    expect(mocks.markAdminWxBound).not.toHaveBeenCalled()
    expect(canTrySilentLogin(now + SILENT_COOLDOWN_MS * 10)).toBe(false)
  })

  it('从「我的」点待审申请：首次只探测一次，探测失败后连点零请求（限流误伤的根因）', async () => {
    mocks.adminRequest.mockResolvedValue({ code: 3, msg: '该微信还没有绑定管理员账号' })
    await goAdminArea()
    expect(mocks.adminRequest).toHaveBeenCalledTimes(1)
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: LOGIN_PAGE })

    wxStub.navigateTo.mockClear()
    await goAdminArea()
    await goAdminArea()
    expect(mocks.adminRequest).toHaveBeenCalledTimes(1)
    expect(wxStub.navigateTo).toHaveBeenCalledTimes(2)
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: LOGIN_PAGE })
  })

  it('已有 adminToken：直达审核页，不校验、不发请求', async () => {
    mocks.hasAdminToken.mockReturnValue(true)
    await goAdminArea()
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: REVIEW_PAGE })
    expect(mocks.adminRequest).not.toHaveBeenCalled()
  })
})

describe('冷却窗口', () => {
  it('绑过且从未尝试：允许；尝试一次后 60s 内不许再试', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    const now = 1_700_000_000_000
    expect(canTrySilentLogin(now)).toBe(true)

    await trySilentAdminLogin(now)
    expect(canTrySilentLogin(now + SILENT_COOLDOWN_MS - 1)).toBe(false)
    expect(canTrySilentLogin(now + SILENT_COOLDOWN_MS)).toBe(true)
  })

  it('失败也记账：429 / 未绑定 / 断网都不会引发连发', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValue({ code: 429, msg: '登录尝试过于频繁，请稍后再试' })
    const now = 1_700_000_000_000
    expect(await trySilentAdminLogin(now)).toBe(false)
    expect(canTrySilentLogin(now + 1000)).toBe(false)
    expect(mocks.adminRequest).toHaveBeenCalledTimes(1)
  })

  it('清掉冷却记录等于回到「从未探测」，会重新允许一次探测', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    const now = 1_700_000_000_000
    await trySilentAdminLogin(now)
    clearSilentLoginCooldown()
    expect(canTrySilentLogin(now + 1)).toBe(true)
  })
})

describe('trySilentAdminLogin：一次、失败静默', () => {
  it('成功：存令牌 + 标记已绑定，请求体带 companyId', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    expect(await trySilentAdminLogin()).toBe(true)
    expect(mocks.adminRequest).toHaveBeenCalledWith('POST', '/api/admin/login/wechat', {
      code: 'code-1',
      companyId: 7,
    })
    expect(mocks.saveAdminAuth).toHaveBeenCalled()
    expect(mocks.markAdminWxBound).toHaveBeenCalled()
  })

  it('拿不到 wx.login code 就不发请求（空 code 只会白白占限流额度）', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.wxLoginCode.mockResolvedValue('')
    expect(await trySilentAdminLogin()).toBe(false)
    expect(mocks.adminRequest).not.toHaveBeenCalled()
  })

  it('业务失败（未绑定 / 429）与网络异常都不抛错、不弹窗', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValueOnce({ code: 3, msg: '该微信还没有绑定管理员账号' })
    expect(await trySilentAdminLogin()).toBe(false)

    mocks.adminRequest.mockRejectedValueOnce(new Error('offline'))
    expect(await trySilentAdminLogin()).toBe(false)

    expect(wxStub.showModal).not.toHaveBeenCalled()
    expect(wxStub.showToast).not.toHaveBeenCalled()
    expect(mocks.markAdminWxBound).not.toHaveBeenCalled()
  })

  it('存令牌失败（响应没带 token）也按失败处理，不进审核页', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.saveAdminAuth.mockReturnValue(false)
    expect(await trySilentAdminLogin()).toBe(false)
    expect(mocks.markAdminWxBound).not.toHaveBeenCalled()
  })
})

describe('goAdminArea：目标页在导航前定好', () => {
  it('绑过且静默成功：loading 收起后直达审核页，全程不出现登录页', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    await goAdminArea()
    expect(wxStub.showLoading).toHaveBeenCalledWith(expect.objectContaining({ mask: true }))
    expect(wxStub.hideLoading).toHaveBeenCalled()
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: REVIEW_PAGE })
    expect(wxStub.navigateTo).toHaveBeenCalledTimes(1)
  })

  it('绑过但静默失败：安静落到登录页，loading 一定收起、不弹任何错误', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.adminRequest.mockResolvedValue({ code: 429, msg: '登录尝试过于频繁，请稍后再试' })
    await goAdminArea()
    expect(wxStub.hideLoading).toHaveBeenCalled()
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: LOGIN_PAGE })
    expect(wxStub.showModal).not.toHaveBeenCalled()
    expect(wxStub.showToast).not.toHaveBeenCalled()
  })

  it('静默过程抛异常也要收起 loading 并落到登录页', async () => {
    mocks.hasAdminWxBound.mockReturnValue(true)
    mocks.wxLoginCode.mockRejectedValue(new Error('wx.login fail'))
    await goAdminArea()
    expect(wxStub.hideLoading).toHaveBeenCalled()
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: LOGIN_PAGE })
  })
})

describe('退出管理员登录', () => {
  it('清令牌 + 清绑定标记 + 把探测时间写成当前值，退出后才不被静默免密带回', async () => {
    storage.set('adminWxBound', true)
    storage.set('adminSilentLoginAt', 123)
    endAdminSession()
    expect(mocks.clearAdminAuth).toHaveBeenCalled()
    expect(mocks.clearAdminWxBound).toHaveBeenCalled()
    // 关键：不能清成 0。清成 0 会被判成「从未探测」→ 立刻又发一次免密请求
    expect(storage.get('adminSilentLoginAt')).toBeGreaterThan(0)
    expect(canTrySilentLogin(Date.now() + SILENT_COOLDOWN_MS * 10)).toBe(false)

    // 退出后点入口应安静落在登录页，且一次登录请求都不发
    wxStub.navigateTo.mockClear()
    await goAdminArea()
    expect(mocks.adminRequest).not.toHaveBeenCalled()
    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: LOGIN_PAGE })
  })
})

describe('静态守门', () => {
  const applyTS = code(readFileSync(resolve(HERE, '../pages/admin/apply/apply.ts'), 'utf8'))
  const meTS = code(readFileSync(resolve(HERE, '../pages/employee/me/me.ts'), 'utf8'))
  const applyWXML = readFileSync(resolve(HERE, '../pages/admin/apply/apply.wxml'), 'utf8')

  it('审核页绝不自动发微信免密登录请求（限流误伤的根因）', () => {
    expect(applyTS).not.toContain('/api/admin/login/wechat')
    expect(applyTS).not.toContain('wxLoginCode')
    expect(applyTS).toContain('ensureAuth()')
  })

  it('审核页 WXML 在 ready 之前不渲染待处理列表（消除闪一下）', () => {
    expect(applyWXML).toContain('wx:if="{{ready}}"')
    expect(applyWXML).toContain('wx:else')
    const readyAt = applyWXML.indexOf('wx:if="{{ready}}"')
    const tabsAt = applyWXML.indexOf('apv-tabs')
    expect(readyAt).toBeGreaterThan(-1)
    expect(tabsAt).toBeGreaterThan(readyAt)
  })

  it('「我的」页不再无条件 navigateTo 审核页，改由 adminEntry 决定目标页', () => {
    expect(meTS).toContain('goAdminArea')
    expect(meTS).not.toContain("wx.navigateTo({ url: '/pages/admin/apply/apply' })")
  })
})
