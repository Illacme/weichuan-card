// 查看全部同事（pages/team/all）——名片墙的第二层，滚动加载。
//
// 这一页是「500 人以上企业能不能用」的关键：名片墙首屏只拉一页 60，
// 剩下的人在这里 onReachBottom 续页。
//
// 滚动加载有三类典型 bug，全部在用例里钉住：
//  1. **重入**：惯性滚动会连触发 onReachBottom，不锁的话同一页被请求多次，
//     列表里出现重复宫格（而且用户看到的是「怎么越滑越多重复的人」）；
//  2. **重复**：翻页期间管理员新增/停用员工会让同一个人出现在相邻两页，
//     直接 concat 就重复了，必须按 staffId 去重；
//  3. **失败即清空**：续页失败把整页打成错误态，已经看到的人全消失——
//     这比「加载不出来」更糟，用户会以为数据没了。
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
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  installApp({ myStaffId: 0 })
  mocks.request.mockClear()
})

async function page(query: any = {}) {
  await import('../pages/team/all')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

// people 造 n 个人，staffId 从 start 开始
function people(start: number, n: number) {
  return Array.from({ length: n }, (_, i) => ({
    staffId: start + i,
    name: '同事' + (start + i),
    title: '',
    avatar: '',
  }))
}

const resp = (extra: any = {}) => ({
  code: 0,
  company: { id: 7, shortName: '朗汇机电', fullName: '朗汇', logo: '' },
  data: people(1, 60),
  total: 130,
  page: 1,
  size: 60,
  hasMore: true,
  ...extra,
})

describe('查看全部 · 首屏与分页', () => {
  it('进页面拉第一页（60）', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page({ companyId: '7' })

    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team', { page: 1, size: 60 })
    expect(p.data.list).toHaveLength(60)
    expect(p.data.total).toBe(130)
    expect(p.data.hasMore).toBe(true)
    expect(p.data.page).toBe(1)
    expect(p.data.loading).toBe(false)
  })

  it('滚到底：请求第 2 页并追加', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockResolvedValueOnce(resp({ data: people(61, 60), page: 2, hasMore: true }))

    p.onReachBottom()
    await flush()

    expect(mocks.request).toHaveBeenLastCalledWith('GET', '/api/team', { page: 2, size: 60 })
    expect(p.data.list).toHaveLength(120)
    expect(p.data.page).toBe(2)
  })

  // 惯性滚动会连触发 onReachBottom，不锁就会把同一页请求好几遍
  it('防重入：请求在途时再滚到底不会重复发请求', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockClear()

    let release: (v: any) => void = () => {}
    mocks.request.mockImplementationOnce(
      () => new Promise((resolve) => { release = resolve })
    )

    p.onReachBottom()
    await flush()
    p.onReachBottom() // 第二次：应该被 loadingMore 挡住
    await flush()

    expect(mocks.request).toHaveBeenCalledTimes(1)

    release(resp({ data: people(61, 60), page: 2 }))
    await flush()
    expect(p.data.list).toHaveLength(120)
  })

  it('后端说没有了：滚到底不再发请求', async () => {
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 60), total: 60, hasMore: false }))
    const p = await page()
    mocks.request.mockClear()

    p.onReachBottom()
    await flush()

    expect(mocks.request).not.toHaveBeenCalled()
  })

  // 翻页期间管理员新增/停用员工 → 同一个人出现在相邻两页
  it('按 staffId 去重：相邻两页有重复时只保留一条', async () => {
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 60) }))
    const p = await page()
    // 第二页故意与第一页重叠 2 个人
    mocks.request.mockResolvedValueOnce(resp({ data: people(59, 60), page: 2 }))

    p.onReachBottom()
    await flush()

    const ids = p.data.list.map((x: any) => x.staffId)
    expect(new Set(ids).size).toBe(ids.length)
    expect(p.data.list).toHaveLength(118) // 60 + 60 - 2 重复
  })

  it('越界页码：用后端回传的实际页码，不用自己算的', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    // 数据被删光后后端把 page 夹回 1
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 60), page: 1, hasMore: false }))

    p.onReachBottom()
    await flush()

    expect(p.data.page).toBe(1)
  })
})

describe('查看全部 · 失败处理', () => {
  it('首屏失败：走错误态，有重试入口', async () => {
    mocks.request.mockRejectedValueOnce(new Error('boom'))
    const p = await page()

    expect(p.data.error).toBe(true)
    expect(p.data.loading).toBe(false)
  })

  // 续页失败不能把整页打成错误态——已经看到的人全消失比「加载不出来」更糟
  it('续页失败：保留已加载列表，只在底部标记可重试', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockRejectedValueOnce(new Error('boom'))

    p.onReachBottom()
    await flush()

    expect(p.data.error).toBe(false)
    expect(p.data.moreError).toBe(true)
    expect(p.data.loadingMore).toBe(false)
    expect(p.data.list).toHaveLength(60)
  })

  it('点「加载失败，点击重试」能重新续页', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockRejectedValueOnce(new Error('boom'))
    p.onReachBottom()
    await flush()

    mocks.request.mockResolvedValueOnce(resp({ data: people(61, 60), page: 2 }))
    p.onRetryMore()
    await flush()

    expect(p.data.moreError).toBe(false)
    expect(p.data.list).toHaveLength(120)
  })
})

describe('查看全部 · 搜索', () => {
  it('带关键字进页面：直接按关键字查第一页', async () => {
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 3), total: 3, hasMore: false }))
    const p = await page({ companyId: '7', keyword: '张' })

    expect(p.data.keyword).toBe('张')
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team', { page: 1, size: 60, keyword: '张' })
  })

  // 换关键字必须从第一页重来，否则会把新结果追加到旧结果后面
  it('换关键字：清空旧列表并从第一页重查', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 2), total: 2, hasMore: false }))

    p.onKeywordInput({ detail: { value: '销售' } })
    p.onSearch()
    await flush()

    expect(p.data.list).toHaveLength(2)
    expect(p.data.page).toBe(1)
    expect(mocks.request).toHaveBeenLastCalledWith('GET', '/api/team', {
      page: 1, size: 60, keyword: '销售',
    })
  })

  it('清空关键字：回到全员列表', async () => {
    mocks.request.mockResolvedValueOnce(resp({ data: people(1, 2), total: 2, keyword: '张' }))
    const p = await page({ keyword: '张' })
    mocks.request.mockResolvedValueOnce(resp())

    p.onClearSearch()
    await flush()

    expect(p.data.activeKeyword).toBe('')
    expect(mocks.request).toHaveBeenLastCalledWith('GET', '/api/team', { page: 1, size: 60 })
  })
})

describe('查看全部 · 交互', () => {
  it('点人进名片带 companyId（不带会落到别家公司）', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()

    p.onTap({ currentTarget: { dataset: { id: '11' } } })

    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=11&companyId=7',
    })
  })

  it('下拉刷新：重拉第一页并收掉指示器', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockResolvedValueOnce(resp())

    await p.onPullDownRefresh()

    expect(wxStub.stopPullDownRefresh).toHaveBeenCalled()
    expect(p.data.list).toHaveLength(60)
  })

  it('下拉刷新失败也要收掉指示器（否则顶部一直转）', async () => {
    mocks.request.mockResolvedValueOnce(resp())
    const p = await page()
    mocks.request.mockRejectedValueOnce(new Error('boom'))

    await p.onPullDownRefresh()

    expect(wxStub.stopPullDownRefresh).toHaveBeenCalled()
  })
})
