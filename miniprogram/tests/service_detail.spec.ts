// 业务服务详情页：与项目详情同构，但正文取自 content 字段、空态文案与默认分享标题不同。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
  companyId: 0,
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/tenant', () => ({
  getCompanyId: () => mocks.companyId,
  bindCompanyFromScene: vi.fn(),
}))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  mocks.request.mockClear()
  mocks.companyId = 0
})

async function page(query: any = {}) {
  await import('../pages/service/detail/detail')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

const service = (extra: any = {}) => ({
  id: 3,
  title: '中央空调维保',
  content: '<p>季度保养</p>',
  updatedAt: '2026-10-16T15:34:12',
  ...extra,
})

describe('service/detail 页', () => {
  it('onLoad 解析 id / staffId 并按 id 拉详情', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    const p = await page({ id: '3', staffId: '42' })

    expect(p.data.id).toBe(3)
    expect(p.data.staffId).toBe(42)
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/services/3')
  })

  it('正文取自 content 字段并解析为片段', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    const p = await page({ id: '3' })

    expect(p.data.service.title).toBe('中央空调维保')
    expect(p.data.segments).toEqual([{ type: 'html', content: '<p>季度保养</p>' }])
  })

  it('content 为空：片段为空数组（不产出 [undefined]）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service({ content: '' }) })
    const p = await page({ id: '3' })

    expect(p.data.segments).toEqual([])
  })

  it('更新时间格式化（不含 T）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    const p = await page({ id: '3' })

    expect(p.data.updatedAtText).toBe('2026-10-16 15:34:12')
  })

  it('服务不存在：提示并结束加载', async () => {
    mocks.request.mockResolvedValueOnce({ code: 1 })
    const p = await page({ id: '3' })

    expect(wxStub.showToast).toHaveBeenCalledWith({ title: '服务不存在', icon: 'none' })
    expect(p.data.service).toBeNull()
    expect(p.data.loading).toBe(false)
  })

  it('请求异常：error 置位且 loading 落下', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await page({ id: '3' })

    expect(p.data.error).toBe(true)
    expect(p.data.loading).toBe(false)
  })

  it('分享：标题用服务名，路径回带 id / staffId / companyId', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    mocks.companyId = 5
    const p = await page({ id: '3', staffId: '42' })

    const share = p.onShareAppMessage()

    expect(share.title).toBe('中央空调维保')
    expect(share.path).toBe('/pages/service/detail/detail?id=3&staffId=42&companyId=5')
  })

  it('详情未加载时分享用默认标题「业务服务」', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await page({ id: '3' })

    expect(p.onShareAppMessage().title).toBe('业务服务')
  })

  // ---- 内容浏览埋点（后台「内容浏览 TOP 榜」的数据源） ----

  // 同案例详情口径：加载成功才计浏览，失败/不存在不计
  it('详情加载成功后上报 service_view（targetId=服务 id）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    const p = await page({ id: '3', staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'service_view', targetId: 3 })
  })

  it('服务不存在：不上报 service_view', async () => {
    mocks.request.mockResolvedValueOnce({ code: 1, msg: 'not found' })
    await page({ id: '3', staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  it('分享服务：上报 share（targetId=服务 id）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: service() })
    const p = await page({ id: '3', staffId: '42' })

    p.onShareAppMessage()

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(2) // 加载时的 service_view + 分享时的 share
    expect(tracks[1][2]).toEqual({ staffId: 42, eventType: 'share', targetId: 3 })
  })
})
