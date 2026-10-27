// 员工登录页：小程序端唯一的口令入口。
// 它的失败形态都很隐蔽——校验漏一条就是「空密码也能提交」，令牌漏存就是
// 「登录成功但下一页仍是未登录」，所以每条支线都钉住。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
  getOpenid: vi.fn(async () => 'openid-from-wx'),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) => u || '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/wechat', () => ({
  getOpenid: mocks.getOpenid,
}))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  mocks.request.mockClear()
  mocks.getOpenid.mockClear()
})

async function page() {
  await import('../pages/employee/login/login')
  return makeInstance(capturedPage())
}

function fill(p: any, phone: string, password: string) {
  p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: phone } })
  p.onInput({ currentTarget: { dataset: { field: 'password' } }, detail: { value: password } })
}

function lastToast(): string {
  const calls = wxStub.showToast.mock.calls
  return calls.length ? calls[calls.length - 1][0].title : ''
}

describe('employee/login 页', () => {
  it('输入框按 data-field 写回 data（手机号与密码各一个）', async () => {
    const p = await page()
    fill(p, '13800000000', 'secret')
    expect(p.data.phone).toBe('13800000000')
    expect(p.data.password).toBe('secret')
  })

  it('手机号或密码为空：只提示、不发请求（空密码也能登录是越权入口）', async () => {
    const p = await page()

    await p.onSubmit()
    expect(lastToast()).toBe('请输入手机号和密码')
    expect(mocks.request).not.toHaveBeenCalled()

    fill(p, '13800000000', '')
    await p.onSubmit()
    expect(mocks.request).not.toHaveBeenCalled()

    fill(p, '', 'secret')
    await p.onSubmit()
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('登录成功：存 access + refresh 令牌，并返回上一页', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 0, token: 'tk-1', refreshToken: 'rt-1' })
    fill(p, '13800000000', 'secret')

    await p.onSubmit()

    expect(storage.get('empToken')).toBe('tk-1')
    expect(storage.get('empRefreshToken')).toBe('rt-1')
    expect(wxStub.navigateBack).toHaveBeenCalledWith(expect.objectContaining({ delta: 1 }))
  })

  it('请求里带上 openid（后台据此核实身份）', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 0, token: 'tk-1' })
    fill(p, '13800000000', 'secret')

    await p.onSubmit()

    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/login', {
      phone: '13800000000',
      password: 'secret',
      openid: 'openid-from-wx',
    })
  })

  // 后端不一定下发 refreshToken（历史账号 / 开关关闭）：此时不能写入空串，
  // 否则请求层会拿 '' 去换令牌，把一个可用会话变成不可刷新
  it('后端没给 refreshToken 时不写入空值', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 0, token: 'tk-1' })
    fill(p, '13800000000', 'secret')

    await p.onSubmit()

    expect(storage.has('empRefreshToken')).toBe(false)
  })

  it('没有上一页（分享直进登录页）时兜底 reLaunch 到「我的」', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 0, token: 'tk-1' })
    wxStub.navigateBack.mockImplementationOnce((opt: any) => opt.fail && opt.fail())
    fill(p, '13800000000', 'secret')

    await p.onSubmit()

    expect(wxStub.reLaunch).toHaveBeenCalledWith({ url: '/pages/employee/me/me' })
  })

  it('登录失败：提示后端 msg，且不写任何令牌', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '手机号或密码错误' })
    fill(p, '13800000000', 'wrong')

    await p.onSubmit()

    expect(lastToast()).toBe('手机号或密码错误')
    expect(storage.has('empToken')).toBe(false)
  })

  // 后端只回 code 不回 msg 时也要有话说，不能弹空白 toast
  it('失败但无 msg：回落固定文案「登录失败」', async () => {
    const p = await page()
    mocks.request.mockResolvedValueOnce({ code: 1 })
    fill(p, '13800000000', 'wrong')

    await p.onSubmit()

    expect(lastToast()).toBe('登录失败')
  })

  // submitting 是按钮的 loading 态：不复位会让按钮永久转圈（用户只能杀进程）
  it('提交期间 submitting 为 true，结束后复位', async () => {
    const p = await page()
    let release: any
    mocks.request.mockImplementationOnce(() => new Promise((r) => (release = r)))
    fill(p, '13800000000', 'secret')

    const task = p.onSubmit()
    expect(p.data.submitting).toBe(true)

    await flush() // 让 await getOpenid() 放行，进入 request
    release({ code: 1, msg: 'x' })
    await task

    expect(p.data.submitting).toBe(false)
  })
})
