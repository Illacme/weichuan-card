// getOpenid 必须把当前企业 companyId 一起发给后端：本项目支持「企业独立小程序」
// （每企业可配自己的 appid），换 openid 要用该企业自己的小程序凭据。不带的话后端
// 一律按 company_id=0（平台全局应用）换，企业独立版下 openid 会与申请人实际所用
// 小程序不一致，后台审核列表里看到的就是空/错值。
//
// 这里锁死两件事：① 请求体含 companyId（与 tenant.getCompanyId() 一致）；
// ② 缓存按企业分键——切换企业不会把 A 企业的 openid 串到 B 企业。
import { describe, it, expect, vi, beforeEach } from 'vitest'

interface WxRequestOpts {
  url: string
  method: string
  data?: any
  header: Record<string, string>
  success: (res: { data: any; statusCode?: number }) => void
  fail: (err: any) => void
}

function installWx() {
  const store: Record<string, string> = {}
  const calls: WxRequestOpts[] = []
  const script = new Map<number, any>()
  const waiting = new Map<number, WxRequestOpts>()

  const deliver = (idx: number, opts: WxRequestOpts) => {
    const s = script.get(idx)
    if (s === undefined) {
      waiting.set(idx, opts)
      return
    }
    waiting.delete(idx)
    if (s === 'fail') opts.fail(new Error('network'))
    else opts.success({ data: s, statusCode: 200 })
  }

  vi.stubGlobal('getApp', () => ({ globalData: { baseUrl: 'http://test' } }))
  vi.stubGlobal('wx', {
    login: vi.fn((opts: any) => opts.success({ code: 'the-code' })),
    request: vi.fn((opts: WxRequestOpts) => {
      const idx = calls.length
      calls.push(opts)
      deliver(idx, opts)
    }),
    getStorageSync: (k: string) => (k in store ? store[k] : ''),
    setStorageSync: (k: string, v: string) => {
      store[k] = v
    },
    removeStorageSync: (k: string) => {
      delete store[k]
    },
  })

  const respond = (idx: number, v: any) => {
    script.set(idx, v)
    const opts = waiting.get(idx)
    if (opts) deliver(idx, opts)
  }

  return { store, calls, respond }
}

// 每个用例独立指定当前企业，避免跨用例缓存串扰
const mocks = vi.hoisted(() => ({ getCompanyId: vi.fn(() => 7) }))
vi.mock('../utils/tenant', () => ({ getCompanyId: mocks.getCompanyId }))

async function loadWechat() {
  vi.resetModules()
  return import('../utils/wechat')
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getCompanyId.mockReturnValue(7)
})

describe('getOpenid 携带企业 companyId', () => {
  it('请求 /api/wechat/openid 的 body 含当前 companyId', async () => {
    const { calls, respond } = installWx()
    const { getOpenid } = await loadWechat()

    const p = getOpenid()
    // wx.login 已同步返回 code → request 已发出；应答换取
    respond(calls.length - 1, { code: 0, openid: 'o-biz7' })
    const openid = await p

    expect(openid).toBe('o-biz7')
    const req = calls[calls.length - 1]
    expect(req.url).toBe('http://test/api/wechat/openid')
    expect(req.data.companyId).toBe(7)
    expect(req.data.code).toBe('the-code')
  })

  it('不同企业用不同的缓存键（不串号）', async () => {
    const { store, respond } = installWx()
    const { getOpenid } = await loadWechat()

    // 企业 7
    mocks.getCompanyId.mockReturnValue(7)
    let p = getOpenid()
    respond(0, { code: 0, openid: 'o-biz7' })
    await p
    expect(store['wxOpenid_7']).toBe('o-biz7')

    // 切到企业 9：缓存里没有 9 的键，应重新发请求
    mocks.getCompanyId.mockReturnValue(9)
    p = getOpenid()
    respond(1, { code: 0, openid: 'o-biz9' })
    await p
    expect(store['wxOpenid_9']).toBe('o-biz9')
    // 企业 7 的键未被覆盖
    expect(store['wxOpenid_7']).toBe('o-biz7')
  })

  it('未配置凭据（code!=0）时降级为空串，不抛错', async () => {
    const { respond } = installWx()
    const { getOpenid } = await loadWechat()

    const p = getOpenid()
    respond(0, { code: 1, msg: '未配置小程序 AppID / WX_APPSECRET' })
    const openid = await p
    expect(openid).toBe('')
  })
})
