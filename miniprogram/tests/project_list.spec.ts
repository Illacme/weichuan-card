// 项目案例列表页：分类切换条 + 列表 + 标题栏回显。
// 这页有三个「看起来能跑、实际错了没人发现」的点，全部钉住：
// 1) /api/achievements 的 categories 在响应**根级**（不是 data 包裹）；
// 2) /api/projects 的列表在 data 里，cover 是相对路径要端上拼绝对；
// 3) 标题回显依赖列表首行的 categoryName，分类被删/列表为空时必须回落固定标题。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  // 与真实现同语义：已是 http(s) 的原样返回，否则拼前缀
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  mocks.request.mockClear()
})

async function page(query: any = {}) {
  await import('../pages/project/list/list')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

// 按 path 分发：onLoad 会同时打 /api/achievements 与 /api/projects
function mockApis(opt: { cats?: any[]; list?: any[]; failProjects?: boolean } = {}) {
  mocks.request.mockImplementation(async (_method: string, path: string): Promise<any> => {
    if (path === '/api/achievements') return { code: 0, categories: opt.cats ?? [] }
    if (path === '/api/projects') {
      if (opt.failProjects) throw new Error('network down')
      return { code: 0, data: opt.list ?? [] }
    }
    return {}
  })
}

const project = (id: number, extra: any = {}) => ({
  id,
  name: `案例${id}`,
  cover: `/uploads/p${id}.jpg`,
  categoryName: '机电安装',
  ...extra,
})

describe('project/list 页', () => {
  it('onLoad 解析 categoryId / staffId（query 全是字符串）', async () => {
    mockApis()
    const p = await page({ categoryId: '3', staffId: '42' })

    expect(p.data.categoryId).toBe(3)
    expect(p.data.staffId).toBe(42)
  })

  it('缺参数时归 0，不产生 NaN（NaN 会让接口过滤条件变成非法值）', async () => {
    mockApis()
    const p = await page({})

    expect(p.data.categoryId).toBe(0)
    expect(p.data.staffId).toBe(0)
  })

  // 分类在响应根级：误读成 r.data.categories 会让切换条永远是空的
  it('分类取自响应根级的 categories', async () => {
    mockApis({ cats: [{ id: 1, name: '机电' }, { id: 3, name: '弱电' }] })
    const p = await page({ categoryId: '3' })

    expect(p.data.cats).toEqual([{ id: 1, name: '机电' }, { id: 3, name: '弱电' }])
  })

  // 从首页点靠右的分类进来时，不滚过去用户根本看不到选中项
  it('带 categoryId 进入时把对应分类滚进可视区', async () => {
    mockApis({ cats: [{ id: 3, name: '弱电' }] })
    const p = await page({ categoryId: '3' })

    expect(p.data.toView).toBe('cat-3')
  })

  it('「全部」进入时不设置 toView（没有选中项可滚）', async () => {
    mockApis({ cats: [{ id: 3, name: '弱电' }] })
    const p = await page({ categoryId: '0' })

    expect(p.data.toView).toBe('')
  })

  // 分类接口挂了不能连累列表：列表本身仍可用，只是没有切换条
  it('分类接口失败：列表照常加载', async () => {
    mocks.request.mockImplementation(async (_m: string, path: string): Promise<any> => {
      if (path === '/api/achievements') throw new Error('boom')
      return { code: 0, data: [project(1)] }
    })
    const p = await page({ categoryId: '1' })

    expect(p.data.cats).toEqual([])
    expect(p.data.list).toHaveLength(1)
    expect(p.data.loading).toBe(false)
  })

  it('列表请求带上分类过滤，cover 拼成绝对 URL', async () => {
    mockApis({ list: [project(7)] })
    const p = await page({ categoryId: '2' })

    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/projects', { categoryId: 2 }, expect.anything())
    expect(p.data.list[0].cover).toBe('https://cdn.test/uploads/p7.jpg')
    expect(p.data.loading).toBe(false)
  })

  // 标题栏回显分类名：列表接口已回填 categoryName，不必为标题多打一次接口
  it('标题回显分类名', async () => {
    mockApis({ list: [project(1, { categoryName: '暖通空调' })] })
    await page({ categoryId: '2' })

    expect(wxStub.setNavigationBarTitle).toHaveBeenCalledWith({ title: '暖通空调' })
  })

  it('「全部」不回显分类名，即使列表首行带 categoryName', async () => {
    mockApis({ list: [project(1)] })
    await page({ categoryId: '0' })

    expect(wxStub.setNavigationBarTitle).toHaveBeenCalledWith({ title: '项目案例' })
  })

  it('分类下没有案例：标题回落「项目案例」而不是空白', async () => {
    mockApis({ list: [] })
    await page({ categoryId: '9' })

    expect(wxStub.setNavigationBarTitle).toHaveBeenCalledWith({ title: '项目案例' })
  })

  it('点同一个分类不重复请求（避免列表闪一下）', async () => {
    mockApis({ list: [project(1)] })
    const p = await page({ categoryId: '2' })
    mocks.request.mockClear()

    p.onCat({ currentTarget: { dataset: { id: '2' } } })
    await flush()

    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('切换分类：更新 categoryId 并重新拉列表', async () => {
    mockApis({ list: [project(1)] })
    const p = await page({ categoryId: '0' })
    mocks.request.mockClear()

    p.onCat({ currentTarget: { dataset: { id: '5' } } })
    await flush()

    expect(p.data.categoryId).toBe(5)
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/projects', { categoryId: 5 }, expect.anything())
  })

  it('点击案例：带 staffId 时把名片链透传到详情页', async () => {
    mockApis({ list: [project(9)] })
    const p = await page({ staffId: '42' })

    p.onTap({ currentTarget: { dataset: { id: 9 } } })

    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      url: '/pages/project/detail/detail?id=9&staffId=42',
    })
  })

  it('没有 staffId（直接进列表）时不拼空参数', async () => {
    mockApis({ list: [project(9)] })
    const p = await page({})

    p.onTap({ currentTarget: { dataset: { id: 9 } } })

    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: '/pages/project/detail/detail?id=9' })
  })

  // 断网时不能停在加载中：必须落到错误态（可重试），否则页面像死了一样
  it('列表接口失败：error 置位且 loading 落下', async () => {
    mockApis({ failProjects: true })
    const p = await page({})

    expect(p.data.error).toBe(true)
    expect(p.data.loading).toBe(false)
    expect(p.data.list).toEqual([])
  })
})

function raceDeferred<T = any>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

describe('project/list 请求竞态守门', () => {
  it('快速切分类：旧分类响应后回来不覆盖新分类列表', async () => {
    const d1 = raceDeferred<any>()
    const d2 = raceDeferred<any>()
    let n = 0
    mocks.request.mockImplementation(async (_m: string, path: string) => {
      if (path === '/api/achievements') return { code: 0, categories: [] }
      n += 1
      return n === 1 ? d1.promise : d2.promise
    })

    const p = await page({ categoryId: '1' })
    p.onCat({ currentTarget: { dataset: { id: '2' } } })
    await flush()
    expect(mocks.request).toHaveBeenCalledTimes(3) // 1 次分类 + 2 次列表

    d2.resolve({ code: 0, data: [project(22)] })
    await flush()
    expect(p.data.list[0].id).toBe(22)

    d1.resolve({ code: 0, data: [project(11)] })
    await flush()
    expect(p.data.list[0].id).toBe(22)
    // 旧响应也不许再改一次标题栏
    expect(wxStub.setNavigationBarTitle).toHaveBeenCalledTimes(1)
  })
})
