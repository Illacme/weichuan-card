// 员工「我的」页：登录态总开关 + 改密入口。
// 改密这条链路尤其要紧——成功后旧令牌立即失效，必须真的把人踢回未登录态，
// 否则页面还显示着「已登录」，下一步操作全部 401。
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  installPageCapture,
  capturedPage,
  makeInstance,
  installWx,
  installApp,
  flush,
} from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any
let storage: Map<string, any>
let app: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  app = installApp({ myStaffId: 0 })
  mocks.request.mockClear()
})

afterEach(() => {
  vi.useRealTimers()
})

async function page() {
  await import('../pages/employee/me/me')
  return makeInstance(capturedPage())
}

function lastToast(): string {
  const calls = wxStub.showToast.mock.calls
  return calls.length ? calls[calls.length - 1][0].title : ''
}

const meData = {
  code: 0,
  pendingModify: true,
  data: { staffId: 42, name: '张伟', avatar: '/uploads/a.png', phone: '138' },
}

describe('employee/me 页', () => {
  // 未登录是常态（访客也会落到这页），不能因此发请求或报错
  it('无令牌：直接回未登录态，不发请求', async () => {
    const p = await page()

    await p.onShow()
    await flush()

    expect(mocks.request).not.toHaveBeenCalled()
    expect(p.data.me).toBeNull()
    expect(p.data.pendingModify).toBe(false)
  })

  // onShow 而非 onLoad：从登录页/编辑页返回必须刷新登录态与资料
  it('有令牌：onShow 拉资料，头像拼绝对，回写 globalData.myStaffId', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce(meData)
    const p = await page()

    p.onShow()
    await flush()

    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/employee/me')
    expect(p.data.me.name).toBe('张伟')
    expect(p.data.me.avatar).toBe('https://cdn.test/uploads/a.png')
    expect(p.data.pendingModify).toBe(true)
    // 名片页 switchTab 回来靠这个编号取数，漏写会显示别人的名片
    expect(app.globalData.myStaffId).toBe(42)
  })

  it('令牌失效：回到未登录态（不弹错，避免游客被报错骚扰）', async () => {
    storage.set('empToken', 'tk-expired')
    mocks.request.mockResolvedValueOnce({ code: 401, msg: '登录失效' })
    const p = await page()
    p.data.me = { staffId: 1, name: '旧数据' }

    p.onShow()
    await flush()

    expect(p.data.me).toBeNull()
    expect(p.data.pendingModify).toBe(false)
    expect(wxStub.showToast).not.toHaveBeenCalled()
  })

  it('生成海报：带上本人编号', async () => {
    const p = await page()
    p.data.me = { staffId: 42 }

    p.goPoster()

    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: '/pages/poster/poster?staffId=42' })
  })

  // 未登录时点「生成海报」不能跳到 staffId=undefined 的海报页
  it('未登录时不跳海报页', async () => {
    const p = await page()

    p.goPoster()

    expect(wxStub.navigateTo).not.toHaveBeenCalled()
  })

  it('返回名片页：栈内没有名片页时兜底 reLaunch 到首页', async () => {
    const p = await page()
    wxStub.navigateBack.mockImplementationOnce((opt: any) => opt.fail && opt.fail())

    p.goMyCard()

    expect(wxStub.reLaunch).toHaveBeenCalledWith({ url: '/pages/index/index' })
  })

  // ---- 改密 ----

  it('打开改密弹层时清空上次输入', async () => {
    const p = await page()
    p.data.pwd = { oldPwd: 'old', newPwd: 'new', confirmPwd: 'new' }

    p.openPwd()

    expect(p.data.pwdVisible).toBe(true)
    expect(p.data.pwd).toEqual({ oldPwd: '', newPwd: '', confirmPwd: '' })
  })

  it('原密码/新密码为空：提示且不发请求', async () => {
    const p = await page()
    p.data.pwd = { oldPwd: '', newPwd: 'newpass', confirmPwd: 'newpass' }

    await p.submitPwd()

    expect(lastToast()).toBe('请填写原密码与新密码')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('新密码短于 6 位：提示且不发请求', async () => {
    const p = await page()
    p.data.pwd = { oldPwd: 'old', newPwd: '12345', confirmPwd: '12345' }

    await p.submitPwd()

    expect(lastToast()).toBe('新密码至少 6 位')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('两次新密码不一致：提示且不发请求', async () => {
    const p = await page()
    p.data.pwd = { oldPwd: 'old', newPwd: 'newpass1', confirmPwd: 'newpass2' }

    await p.submitPwd()

    expect(lastToast()).toBe('两次输入的新密码不一致')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('改密失败：提示后端 msg，保持登录态', async () => {
    storage.set('empToken', 'tk-1')
    const p = await page()
    p.openPwd()
    p.data.pwd = { oldPwd: 'old', newPwd: 'newpass', confirmPwd: 'newpass' }
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '原密码不正确' })

    await p.submitPwd()

    expect(lastToast()).toBe('原密码不正确')
    expect(p.data.pwdSaving).toBe(false)
    // 失败不能顺手把人踢下线：令牌仍在，弹层也还开着供重试
    expect(storage.get('empToken')).toBe('tk-1')
    expect(p.data.pwdVisible).toBe(true)
  })

  // 改密后后端 token_version 自增，旧令牌立即失效：必须清本地态，
  // 否则页面仍显示已登录，用户点任何入口都 401
  it('改密成功：提示、关弹层、延时踢回未登录态', async () => {
    vi.useFakeTimers()
    storage.set('empToken', 'tk-1')
    storage.set('empRefreshToken', 'rt-1')
    app.globalData.myStaffId = 42
    const p = await page()
    p.data.pwd = { oldPwd: 'old', newPwd: 'newpass', confirmPwd: 'newpass' }
    mocks.request.mockResolvedValueOnce({ code: 0 })

    const task = p.submitPwd()
    await vi.advanceTimersByTimeAsync(0)
    await task

    expect(lastToast()).toBe('已修改，请重新登录')
    expect(p.data.pwdVisible).toBe(false)
    expect(p.data.pwdSaving).toBe(false)

    await vi.advanceTimersByTimeAsync(800)

    expect(storage.has('empToken')).toBe(false)
    expect(storage.has('empRefreshToken')).toBe(false)
    expect(app.globalData.myStaffId).toBe(0)
    expect(p.data.me).toBeNull()
  })

  it('主动退出：清两个令牌与 globalData，并提示', async () => {
    storage.set('empToken', 'tk-1')
    storage.set('empRefreshToken', 'rt-1')
    app.globalData.myStaffId = 42
    const p = await page()
    p.data.me = { staffId: 42 }

    p.logout()

    expect(storage.has('empToken')).toBe(false)
    expect(storage.has('empRefreshToken')).toBe(false)
    expect(app.globalData.myStaffId).toBe(0)
    expect(p.data.me).toBeNull()
    expect(lastToast()).toBe('已退出登录')
  })

  // bindtap 会把事件对象当首个实参传给 handler：若按钮直接绑 logout，
  // silent 会被事件对象（truthy）顶成「静默」，成功提示永远不弹。
  // onLogoutTap 必须显式走 logout(false)，这里用带事件对象的调用守住这条。
  it('点「退出登录」按钮（带事件对象）仍弹提示，不被误判为静默', async () => {
    storage.set('empToken', 'tk-1')
    const p = await page()
    p.data.me = { staffId: 42 }

    // 模拟真实 tap：事件对象作为首个实参传入
    ;(p as any).onLogoutTap({ type: 'tap', detail: {} })

    expect(storage.has('empToken')).toBe(false)
    expect(lastToast()).toBe('已退出登录')
  })

  // 改密后的静默登出不该再弹「已退出登录」，那时已经提示过「请重新登录」了
  it('静默登出不弹提示', async () => {
    const p = await page()

    p.logout(true)

    expect(wxStub.showToast).not.toHaveBeenCalled()
  })
})

// 注销账号（PIPL 删除权）：不可逆特权动作，必须两次强确认 + 成功后清本地态。
// 走两次 showModal（第一次讲后果、第二次红字「确认注销」），任一取消都不能真删。
describe('employee/me 注销账号', () => {
  let wxStub: any
  let storage: Map<string, any>
  let app: any

  beforeEach(() => {
    vi.resetModules()
    installPageCapture()
    const installed = installWx({
      // 默认两次 modal 都点「确认」
      showModal: vi.fn((o: any) => o && o.success && o.success({ confirm: true })),
    })
    wxStub = installed.wx
    storage = installed.storage
    app = installApp({ myStaffId: 42 })
    mocks.request.mockClear()
  })

  async function cancelPage() {
    await import('../pages/employee/me/me')
    return makeInstance(capturedPage())
  }

  it('两次确认后调用注销接口并清本地态', async () => {
    storage.set('empToken', 'tk-1')
    storage.set('empRefreshToken', 'rt-1')
    app.globalData.myStaffId = 42
    mocks.request.mockResolvedValueOnce({ code: 0 })
    const p = await cancelPage()

    p.cancelAccount()
    await flush()

    // 本人身份注销：无 id 参数，靠令牌鉴权（结构上杜绝删别人）
    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/me/cancel')
    expect(storage.has('empToken')).toBe(false)
    expect(storage.has('empRefreshToken')).toBe(false)
    expect(app.globalData.myStaffId).toBe(0)

    // 成功后 800ms 才 reLaunch（先把 toast 亮出来）。这个定时器必须在本文件内
    // 走完：本套件以「单 fork 顺序跑全部文件」执行，遗留的定时器会在后一个
    // 文件里触发，那时 wx 桩已换成没有 reLaunch 的最小桩 → 抛未捕获异常，
    // 表现为「某个毫不相干的文件莫名红了」（2026-10-27 实测抓到）。
    await new Promise((r) => setTimeout(r, 900))
    expect(wxStub.reLaunch).toHaveBeenCalledWith({ url: '/pages/index/index' })
  })

  it('第一次取消确认：不调注销接口、不动本地态', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValue({ code: 0 })
    const p = await cancelPage()
    // 第一个 modal 点「取消」
    wxStub.showModal.mockImplementationOnce((o: any) => o && o.success && o.success({ confirm: false }))

    p.cancelAccount()
    await flush()

    expect(mocks.request).not.toHaveBeenCalledWith('POST', '/api/employee/me/cancel')
    expect(storage.get('empToken')).toBe('tk-1')
  })

  it('接口失败：提示且保留本地登录态', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '注销失败：服务端异常' })
    const p = await cancelPage()

    p.cancelAccount()
    await flush()

    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/employee/me/cancel')
    expect(storage.get('empToken')).toBe('tk-1')
    const calls = wxStub.showToast.mock.calls
    expect(calls[calls.length - 1][0].title).toContain('注销失败')
  })
})
