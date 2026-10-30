// 微信一键登录（登录页）+ 绑定微信（「我的」页）。
//
// 这条链路的产品价值是「员工不用记密码」，所以测试重点不在 happy path 一条，
// 而在那些会让用户「点了没反应」或「登进别人账号」的分支：
//   - 未绑定时必须说清「去哪绑定」，只说「登录失败」等于功能不可用；
//   - 已绑定时不重复绑定，改为说明怎么换微信；
//   - 绑定失败（这个微信已绑了别的账号）要用 modal 把原因说全，toast 会被截断；
//   - 连点只发一次请求（连点会连发两个 code，后一个必然失效并弹一个假错误）。
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
  request: vi.fn(async (_m: string, _u: string): Promise<any> => ({})),
  wxLoginCode: vi.fn(async (): Promise<string> => 'code-1'),
  getOpenid: vi.fn(async (): Promise<string> => 'openid-1'),
  companyId: 7,
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/wechat', () => ({
  wxLoginCode: mocks.wxLoginCode,
  getOpenid: mocks.getOpenid,
}))
vi.mock('../utils/tenant', () => ({
  getCompanyId: () => mocks.companyId,
  bindCompanyFromScene: vi.fn(),
}))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  installApp({ myStaffId: 0 })
  mocks.request.mockClear()
  mocks.wxLoginCode.mockClear()
  mocks.wxLoginCode.mockResolvedValue('code-1')
  mocks.getOpenid.mockClear()
  mocks.getOpenid.mockResolvedValue('openid-1')
  mocks.companyId = 7
})

async function loginPage() {
  await import('../pages/employee/login/login')
  return makeInstance(capturedPage())
}

async function mePage() {
  await import('../pages/employee/me/me')
  return makeInstance(capturedPage())
}

const loginOk = { code: 0, token: 'access-1', refreshToken: 'refresh-1', expiresIn: 900 }

describe('登录页 · 微信一键登录', () => {
  it('成功：拿 code 换令牌，带 companyId，存令牌并返回上一页', async () => {
    mocks.request.mockResolvedValueOnce(loginOk)
    const p = await loginPage()

    await p.onWechatLogin()

    // companyId 必须带：企业独立版各配自己的 appid，凭据按租户选，
    // 不带的话后端会按平台全局凭据换 openid，换出来的是另一个小程序的用户
    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/login/wechat', {
      code: 'code-1',
      companyId: 7,
    })
    expect(storage.get('empToken')).toBe('access-1')
    // refreshToken 也要存：只存 access 的话 15 分钟后被迫重新登录，
    // 「一键登录」省下的力气又还回去了
    expect(storage.get('empRefreshToken')).toBe('refresh-1')
    expect(wxStub.navigateBack).toHaveBeenCalled()
    expect(p.data.wxSubmitting).toBe(false)
  })

  it('未绑定（code=3）：弹 modal 说清去哪绑定，不存令牌也不跳转', async () => {
    mocks.request.mockResolvedValueOnce({ code: 3, msg: '该微信还没有绑定员工账号' })
    const p = await loginPage()

    await p.onWechatLogin()

    const m = wxStub.showModal.mock.calls[0][0]
    expect(m.title).toBe('还没有绑定微信')
    expect(m.content).toContain('密码登录')
    expect(m.content).toContain('「我的」页绑定')
    expect(storage.has('empToken')).toBe(false)
    expect(wxStub.navigateBack).not.toHaveBeenCalled()
    // 不能同时再弹一个 toast 说「登录失败」——两条提示叠在一起，用户只会更困惑
    expect(wxStub.showToast).not.toHaveBeenCalled()
  })

  it('wx.login 拿不到 code：提示重试，不发请求', async () => {
    mocks.wxLoginCode.mockResolvedValueOnce('')
    const p = await loginPage()

    await p.onWechatLogin()

    expect(mocks.request).not.toHaveBeenCalled()
    expect(wxStub.showToast).toHaveBeenCalledWith(
      expect.objectContaining({ title: '微信登录失败，请重试' })
    )
    expect(p.data.wxSubmitting).toBe(false)
  })

  it('其它业务失败（如停用）：用后端文案 toast，不吞成通用提示', async () => {
    mocks.request.mockResolvedValueOnce({ code: 2, msg: '账号已停用' })
    const p = await loginPage()

    await p.onWechatLogin()

    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '账号已停用' }))
    expect(storage.has('empToken')).toBe(false)
  })

  it('网络异常：toast 兜住，loading 收掉（不留一个永远转圈的按钮）', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await loginPage()

    await p.onWechatLogin()

    expect(wxStub.showToast).toHaveBeenCalledWith(
      expect.objectContaining({ title: '网络异常，请重试' })
    )
    expect(p.data.wxSubmitting).toBe(false)
  })

  it('连点只发一次请求（连发两个 code，后一个必然失效）', async () => {
    let release: (v: any) => void = () => {}
    mocks.request.mockImplementationOnce(
      () => new Promise<any>((r) => (release = r)) as any
    )
    const p = await loginPage()

    const first = p.onWechatLogin()
    // 第一次还没落地时再点一次
    await p.onWechatLogin()
    expect(mocks.request).toHaveBeenCalledTimes(1)

    release(loginOk)
    await first
    expect(mocks.request).toHaveBeenCalledTimes(1)
  })
})

describe('登录页 · 手机号密码（顺带修的 loading 卡死）', () => {
  it('断网时 submitting 也要落下来，并提示重试', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await loginPage()
    p.setData({ phone: '13800000000', password: 'pwd123' })

    await p.onSubmit()

    expect(p.data.submitting).toBe(false)
    expect(wxStub.showToast).toHaveBeenCalledWith(
      expect.objectContaining({ title: '网络异常，请重试' })
    )
  })

  it('成功：仍然按老路子带 openid 登录并存令牌', async () => {
    mocks.request.mockResolvedValueOnce(loginOk)
    const p = await loginPage()
    p.setData({ phone: '13800000000', password: 'pwd123' })

    await p.onSubmit()

    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/login', {
      phone: '13800000000',
      password: 'pwd123',
      openid: 'openid-1',
    })
    expect(storage.get('empToken')).toBe('access-1')
  })
})

describe('「我的」页 · 绑定微信', () => {
  const meResp = (extra: any = {}) => ({
    code: 0,
    companyName: '测试公司',
    companyShortName: '测试',
    pendingModify: false,
    wechatBound: false,
    data: { staffId: 42, name: '张伟', title: '销售', avatar: '' },
    ...extra,
  })

  it('loadMe 把 wechatBound 落到 data（页面据此显示「已绑定 / 未绑定」）', async () => {
    mocks.request.mockResolvedValueOnce(meResp({ wechatBound: true }))
    storage.set('empToken', 't')
    const p = await mePage()

    await p.loadMe()

    expect(p.data.wechatBound).toBe(true)
  })

  it('未登录：wechatBound 必须归位为 false（否则换账号后还显示已绑定）', async () => {
    const p = await mePage()
    p.setData({ wechatBound: true })

    await p.loadMe()

    expect(p.data.wechatBound).toBe(false)
  })

  it('未绑定 → 点一下完成绑定', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, msg: '已绑定' })
    const p = await mePage()

    await p.onBindWechat()

    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/wechat/bind', {
      code: 'code-1',
    })
    expect(p.data.wechatBound).toBe(true)
    expect(p.data.bindingWechat).toBe(false)
    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '已绑定' }))
  })

  it('已绑定 → 不发请求，只说明怎么换微信', async () => {
    const p = await mePage()
    p.setData({ wechatBound: true })

    await p.onBindWechat()

    expect(mocks.request).not.toHaveBeenCalled()
    const m = wxStub.showModal.mock.calls[0][0]
    expect(m.title).toBe('已绑定微信')
    // 换微信的正确做法要写清：重新绑定即覆盖，不需要「解绑」按钮
    expect(m.content).toContain('重新绑定')
  })

  it('绑定失败（微信已绑别的账号）：用 modal 把后端原因说全，不截断', async () => {
    mocks.request.mockResolvedValueOnce({
      code: 1,
      msg: '该微信已绑定本企业的其他员工账号，请联系管理员处理',
    })
    const p = await mePage()

    await p.onBindWechat()

    expect(p.data.wechatBound).toBe(false)
    const m = wxStub.showModal.mock.calls[0][0]
    expect(m.title).toBe('绑定失败')
    expect(m.content).toContain('其他员工账号')
  })

  it('网络异常：绑定态不变，提示重试', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await mePage()

    await p.onBindWechat()

    expect(p.data.wechatBound).toBe(false)
    expect(p.data.bindingWechat).toBe(false)
    expect(wxStub.showToast).toHaveBeenCalledWith(
      expect.objectContaining({ title: '网络异常，请重试' })
    )
  })

  it('绑定请求在途时连点：只发一次', async () => {
    let release: (v: any) => void = () => {}
    mocks.request.mockImplementationOnce(() => new Promise<any>((r) => (release = r)) as any)
    const p = await mePage()

    const first = p.onBindWechat()
    await p.onBindWechat()
    expect(mocks.request).toHaveBeenCalledTimes(1)

    release({ code: 0 })
    await first
  })
})
