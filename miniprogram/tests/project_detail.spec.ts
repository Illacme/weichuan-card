// 项目案例详情页：相册预览 + 富文本（含视频号片段）+ 分享回带名片链。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
  companyId: 0,
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  // 必须与真实现同语义：已是 http(s) 的原样返回（云存储返回绝对 CDN URL），
  // 无条件拼前缀会让「绝对 URL 被二次拼接」这个真实缺陷在测试里看不见
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
  await import('../pages/project/detail/detail')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

const detail = (extra: any = {}) => ({
  id: 7,
  name: '厂房配电改造',
  cover: '/uploads/cover7.jpg',
  album: ['/uploads/a1.jpg', 'https://cdn.other/a2.jpg'],
  detail: '<p>正文</p><wx-video feed-id="f1"></wx-video>',
  updatedAt: '2026-10-16T15:34:12',
  ...extra,
})

describe('project/detail 页', () => {
  it('onLoad 解析 id / staffId', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7', staffId: '42' })

    expect(p.data.id).toBe(7)
    expect(p.data.staffId).toBe(42)
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/projects/7')
  })

  // <image> 与 previewImage 都只认绝对 URL：漏拼就是整页裂图
  it('封面与相册都拼成绝对 URL（已是绝对的原样保留）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })

    expect(p.data.project.cover).toBe('https://cdn.test/uploads/cover7.jpg')
    expect(p.data.project.album).toEqual([
      'https://cdn.test/uploads/a1.jpg',
      'https://cdn.other/a2.jpg',
    ])
  })

  it('正文按 parseRich 切成 html / 视频号片段', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })

    expect(p.data.segments).toEqual([
      { type: 'html', content: '<p>正文</p>' },
      { type: 'video', content: 'f1', feedId: 'f1', feedToken: '', channelId: '' },
    ])
  })

  // 后端给 RFC3339，直接绑会把 T 和时区后缀显示出来
  it('更新时间格式化为可读文案（不含 T）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })

    expect(p.data.updatedAtText).toBe('2026-10-16 15:34:12')
  })

  it('没有 updatedAt 时留空，不显示「—」占位', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail({ updatedAt: '' }) })
    const p = await page({ id: '7' })

    expect(p.data.updatedAtText).toBe('')
  })

  // 已下架/被删：要明确告诉用户，而不是给一个空页面
  it('接口无 data（案例不存在）：提示并结束加载，project 保持 null', async () => {
    mocks.request.mockResolvedValueOnce({ code: 1, msg: 'not found' })
    const p = await page({ id: '7' })

    expect(wxStub.showToast).toHaveBeenCalledWith({
      title: '案例不存在或已下架',
      icon: 'none',
    })
    expect(p.data.project).toBeNull()
    expect(p.data.loading).toBe(false)
    expect(wxStub.showToast.mock.calls[0][0].title).not.toBe('not found')
  })

  it('请求异常：error 置位且 loading 落下', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await page({ id: '7' })

    expect(p.data.error).toBe(true)
    expect(p.data.loading).toBe(false)
  })

  it('预览相册：urls 传整册、current 传被点的那张', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })

    p.preview({ currentTarget: { dataset: { url: 'https://cdn.test/uploads/a1.jpg' } } })

    expect(wxStub.previewImage).toHaveBeenCalledWith({
      urls: ['https://cdn.test/uploads/a1.jpg', 'https://cdn.other/a2.jpg'],
      current: 'https://cdn.test/uploads/a1.jpg',
    })
  })

  it('没有相册时点图不调预览（不把空数组丢给微信）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail({ album: null }) })
    const p = await page({ id: '7' })

    p.preview({ currentTarget: { dataset: { url: '' } } })

    // 空列表调 previewImage 只会让微信打一条告警，用户看不到任何东西——
    // 干脆不调（见 detail.ts preview 的早退分支）
    expect(wxStub.previewImage).not.toHaveBeenCalled()
  })

  it('相册里有图挂了：预览列表剔掉坏图，点坏图本身不弹预览', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })
    // 模拟第 2 张加载失败（binderror → onImgFail 记下失败的 URL）
    p.onImgFail({
      currentTarget: {
        dataset: { fail: 'albumFail', idx: 1, src: 'https://cdn.other/a2.jpg' },
      },
    })

    p.preview({ currentTarget: { dataset: { url: 'https://cdn.test/uploads/a1.jpg' } } })
    expect(wxStub.previewImage).toHaveBeenLastCalledWith({
      urls: ['https://cdn.test/uploads/a1.jpg'],
      current: 'https://cdn.test/uploads/a1.jpg',
    })

    // 点坏图：current 不在可用列表里，不弹（否则打开就是同一张裂图）
    p.preview({ currentTarget: { dataset: { url: 'https://cdn.other/a2.jpg' } } })
    expect(wxStub.previewImage).toHaveBeenCalledTimes(1)
  })

  it('分享：标题用案例名，路径回带 id / staffId / companyId', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    mocks.companyId = 5
    const p = await page({ id: '7', staffId: '42' })

    const share = p.onShareAppMessage()

    expect(share.title).toBe('厂房配电改造')
    expect(share.path).toBe('/pages/project/detail/detail?id=7&staffId=42&companyId=5')
  })

  // 独立版 companyId 恒为 0：不传，避免后端把 0 当成有效租户
  it('companyId 为 0 时不拼该参数', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7' })

    const share = p.onShareAppMessage()

    expect(share.path).toBe('/pages/project/detail/detail?id=7')
  })

  // 详情还没加载完就点分享（微信允许）不能得到 undefined 标题
  it('详情未加载时分享用默认标题', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    const p = await page({ id: '7' })

    expect(p.onShareAppMessage().title).toBe('项目案例')
  })

  // ---- 内容浏览埋点（后台「内容浏览 TOP 榜」的数据源） ----

  // 加载成功才计一次浏览：失败/不存在不计，否则案例热度里混进
  // 根本没看到内容的误报——TOP 榜是运营调内容配比的依据，口径必须干净
  it('详情加载成功后上报 project_view（targetId=案例 id）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7', staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'project_view', targetId: 7 })
  })

  it('案例不存在：不上报 project_view', async () => {
    mocks.request.mockResolvedValueOnce({ code: 1, msg: 'not found' })
    await page({ id: '7', staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  it('请求异常：不上报 project_view', async () => {
    mocks.request.mockRejectedValueOnce(new Error('offline'))
    await page({ id: '7', staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  // 转发埋点 targetId=案例 id（转发不进 TOP 榜聚合，只在明细可查）。
  // 此时应有两条：加载时的 project_view + 分享时的 share
  it('分享案例：上报 share（targetId=案例 id）', async () => {
    mocks.request.mockResolvedValueOnce({ code: 0, data: detail() })
    const p = await page({ id: '7', staffId: '42' })

    p.onShareAppMessage()

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(2)
    expect(tracks[1][2]).toEqual({ staffId: 42, eventType: 'share', targetId: 7 })
  })
})
