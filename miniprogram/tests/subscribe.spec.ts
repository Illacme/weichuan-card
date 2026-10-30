// 审核结果订阅消息（一次性订阅）的端上部分。
//
// 这条链路的价值全在「时机」上，最容易做错的两处：
//  1. 把 wx.requestSubscribeMessage 放在 await 之后 —— 微信要求由用户手势
//     同步触发，异步再调会被拒，现象是「点了提交但从来没弹过授权窗」；
//  2. 用户拒绝订阅就拦住提交 —— 通知是可选增强，不能拿它要挟主流程。
// 所以下面的用例既测「发不发」，也测「先后顺序」。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._a: any[]): Promise<any> => ({})),
  getOpenid: vi.fn(async (): Promise<string> => 'openid-x'),
  ensurePrivacy: vi.fn(async (..._a: any[]): Promise<boolean> => true),
  needConsent: vi.fn(async (..._a: any[]): Promise<boolean> => false),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/wechat', () => ({ getOpenid: mocks.getOpenid }))
vi.mock('../utils/privacy', () => ({
  ensurePrivacy: mocks.ensurePrivacy,
  needConsent: mocks.needConsent,
}))

let wxStub: any

beforeEach(() => {
  // 清掉 subscribe.ts 的模块级模板缓存，否则用例之间互相污染
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({})
})

// ---- 模板预取与授权请求 ----

describe('utils/subscribe', () => {
  it('预取模板 ID 后缓存，不重复请求', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: { auditTemplateId: 'TPL-1' } })
    const { loadAuditTemplate } = await import('../utils/subscribe')

    expect(await loadAuditTemplate()).toBe('TPL-1')
    expect(await loadAuditTemplate()).toBe('TPL-1')
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/subscribe/config')
  })

  // 未配置模板时不能拿空 tmplIds 去调微信：那会直接 fail，
  // 用户看到的是「点了提交但什么都没发生」。
  it('未配置模板：不弹授权窗', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: { auditTemplateId: '' } })
    const { loadAuditTemplate, requestAuditSubscribe } = await import('../utils/subscribe')

    expect(await loadAuditTemplate()).toBe('')
    requestAuditSubscribe()
    expect(wxStub.requestSubscribeMessage).not.toHaveBeenCalled()
  })

  it('有模板：tmplIds 传数组', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: { auditTemplateId: 'TPL-9' } })
    const { loadAuditTemplate, requestAuditSubscribe } = await import('../utils/subscribe')

    await loadAuditTemplate()
    requestAuditSubscribe()
    expect(wxStub.requestSubscribeMessage).toHaveBeenCalledWith(
      expect.objectContaining({ tmplIds: ['TPL-9'] })
    )
  })

  // 网络异常时**不能缓存空串**：缓存了会让整个会话都收不到通知，
  // 而用户完全无从察觉（审批通过了但手机不响）。
  it('预取失败不缓存，下次还能再试', async () => {
    mocks.request.mockRejectedValueOnce(new Error('boom'))
    const { loadAuditTemplate } = await import('../utils/subscribe')

    expect(await loadAuditTemplate()).toBe('')
    mocks.request.mockResolvedValueOnce({ code: 0, data: { auditTemplateId: 'TPL-2' } })
    expect(await loadAuditTemplate()).toBe('TPL-2')
  })

  it('用户拒绝订阅：不抛错、不重试', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: { auditTemplateId: 'TPL-1' } })
    const { loadAuditTemplate, requestAuditSubscribe } = await import('../utils/subscribe')
    await loadAuditTemplate()
    wxStub.requestSubscribeMessage.mockImplementationOnce((o: any) =>
      o.fail({ errMsg: 'requestSubscribeMessage:fail auth deny' })
    )

    expect(() => requestAuditSubscribe()).not.toThrow()
    expect(wxStub.requestSubscribeMessage).toHaveBeenCalledTimes(1)
  })
})

// ---- 页面接线：时机与顺序 ----

// routeRequest 按路径分发桩响应，并把「提交」记进 order（用来断言先后顺序）。
function routeRequest(order: string[], tmplId = 'TPL-1') {
  mocks.request.mockImplementation(async (method: string, path: string) => {
    if (path === '/api/company') return { code: 0, data: { name: '甲公司', shortName: '甲' } }
    if (path === '/api/subscribe/config') return { code: 0, data: { auditTemplateId: tmplId } }
    if (path === '/api/apply/open') {
      order.push('submit')
      return { code: 0 }
    }
    if (path === '/api/employee/me') {
      // GET 是 onLoad 回填资料，PUT 才是提交——只有提交记进 order
      if (method === 'PUT') order.push('submit')
      return { code: 0 }
    }
    return {}
  })
}

function fillApply(p: any) {
  p.onInput({ currentTarget: { dataset: { field: 'name' } }, detail: { value: '张伟' } })
  p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: '13800000000' } })
  p.toggleAgree()
}

describe('申请页提交时订阅审核结果', () => {
  async function applyPage(order: string[], tmplId = 'TPL-1') {
    routeRequest(order, tmplId)
    await import('../pages/apply/open/open')
    const p = makeInstance(capturedPage())
    p.onLoad()
    await flush()
    return p
  }

  // 顺序错了微信会直接拒绝授权（它靠手势判断「用户主动同意」），
  // 而现象只是「从来没弹过窗」，代码看起来完全正常。
  it('先请求订阅，再发提交请求', async () => {
    const order: string[] = []
    const p = await applyPage(order)
    fillApply(p)
    wxStub.requestSubscribeMessage.mockImplementationOnce((o: any) => {
      order.push('subscribe')
      if (o.success) o.success({})
    })

    await p.onSubmit()

    expect(order).toEqual(['subscribe', 'submit'])
    expect(p.data.submitted).toBe(true)
  })

  it('用户拒绝订阅：提交照样发出去', async () => {
    const order: string[] = []
    const p = await applyPage(order)
    fillApply(p)
    wxStub.requestSubscribeMessage.mockImplementationOnce((o: any) =>
      o.fail({ errMsg: 'requestSubscribeMessage:fail auth deny' })
    )

    await p.onSubmit()

    expect(order).toEqual(['submit'])
    expect(p.data.submitted).toBe(true)
  })

  // 未配置模板时不该弹窗，也不该拦住提交
  it('后端没配模板：不弹窗，提交照常', async () => {
    const order: string[] = []
    const p = await applyPage(order, '')
    fillApply(p)

    await p.onSubmit()

    expect(wxStub.requestSubscribeMessage).not.toHaveBeenCalled()
    expect(p.data.submitted).toBe(true)
  })
})

describe('编辑名片页提交时订阅审核结果', () => {
  it('提交前请求订阅（审核在后台做，员工不在小程序里）', async () => {
    const order: string[] = []
    routeRequest(order)
    await import('../pages/employee/edit/edit')
    const p = makeInstance(capturedPage())
    p.onLoad()
    await flush()
    wxStub.requestSubscribeMessage.mockImplementationOnce((o: any) => {
      order.push('subscribe')
      if (o.success) o.success({})
    })

    await p.onSubmit()

    expect(order).toEqual(['subscribe', 'submit'])
  })
})
