// 名片首页：整个小程序的落地页，把「员工卡片 + 业绩分类 + 精选案例 + 服务 + 首页布局」
// 四次请求并行拼出来。
//
// 这页历史上出过两个只在真机上才看得见的错，测试专门钉住：
// 1) /api/card 的 employee/company/profile 在响应**根级**（不是 data 包裹）——
//    误读会让卡片页恒走 notFound，扫码进来的访客一律看到「员工不存在」；
// 2) loadAll 里 reject 直接冒泡出 async 函数会让页面永久停在「加载中…」。
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
  companyId: 0,
  bindScene: vi.fn(),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/tenant', () => ({
  getCompanyId: () => mocks.companyId,
  bindCompanyFromScene: (...args: any[]) => mocks.bindScene(...args),
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
  mocks.bindScene.mockClear()
  mocks.companyId = 0
})

async function page(query: any = { staffId: '42' }) {
  await import('../pages/index/index')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

const card = (extra: any = {}) => ({
  code: 0,
  employee: { staffId: 42, name: '张伟', avatar: '/uploads/a.png', title: '工程师' },
  company: { name: '朗汇机电有限公司', shortName: '朗汇机电', logo: '/uploads/logo.png' },
  profile: { cover: '/uploads/cover.png' },
  ...extra,
})

const ach = (extra: any = {}) => ({
  code: 0,
  categories: [{ id: 1, name: '机电安装', icon: '/uploads/c1.png' }],
  featured: [{ id: 9, name: '厂房配电', cover: '/uploads/p9.jpg' }],
  ...extra,
})

const services = { code: 0, data: [{ id: 3, title: '维保', icon: '/uploads/s3.png' }] }
const layout = { code: 0, order: ['achievement', 'service'], bgImage: '/uploads/bg.jpg', bgMask: 60 }

// 按 path 分发四次并行请求（loadAll 用 Promise.all 一次性打出去）
function mockApis(opt: { card?: any; ach?: any; svc?: any; layout?: any; fail?: boolean } = {}) {
  mocks.request.mockImplementation(async (_m: string, path: string): Promise<any> => {
    if (opt.fail) throw new Error('offline')
    if (path.startsWith('/api/card')) return opt.card ?? card()
    if (path === '/api/achievements') return opt.ach ?? ach()
    if (path === '/api/services') return opt.svc ?? services
    if (path === '/api/home-layout') return opt.layout ?? layout
    if (path === '/api/stat/track') return { code: 0 }
    if (path === '/api/employee/me') return { code: 0, data: { staffId: 42, avatar: '/uploads/a.png' } }
    return {}
  })
}

describe('index 首页', () => {
  // ---- 进入参数 ----

  it('带 staffId：直接加载，不弹输入框', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    expect(p.data.staffId).toBe(42)
    expect(wxStub.showModal).not.toHaveBeenCalled()
    expect(p.data.employee.name).toBe('张伟')
  })

  // 扫小程序码进来，微信把参数塞进 scene（形如 staffId=12）
  it('从 scene 解析 staffId', async () => {
    mockApis()
    const p = await page({ scene: encodeURIComponent('staffId=12') })

    expect(p.data.staffId).toBe(12)
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/card?staffId=12')
  })

  // 从「我的」页切回名片页：已登录员工用自己的编号，不必再输
  it('无参数时用 globalData.myStaffId 兜底', async () => {
    mockApis()
    app.globalData.myStaffId = 42
    const p = await page({})

    expect(p.data.staffId).toBe(42)
    expect(wxStub.showModal).not.toHaveBeenCalled()
  })

  // 裸启动（扫体验版二维码 / 直接打开）：微信不带任何参数。这里**绝不能**弹
  // 「输入员工编号」——那是开发期联调兜底，访客第一次打开这家公司的小程序
  // 看到的会是一个要他输入数字的弹窗。落点应该是名片墙。
  it('既无参数也无登录态：跳名片墙，不弹输入框', async () => {
    mockApis()
    const p = await page({})

    expect(p.data.loading).toBe(false)
    expect(wxStub.reLaunch).toHaveBeenCalledWith({ url: '/pages/team/team' })
    expect(wxStub.showModal).not.toHaveBeenCalled()
    // 没有 staffId 就不该去拉名片（否则会打一个 staffId=0 的请求回来）
    expect(mocks.request).not.toHaveBeenCalled()
  })

  // 平台模式下扫码/分享带 companyId 要粘滞到本机，企业内容才跟着走
  it('把 scene / companyId 交给租户绑定（平台模式粘滞）', async () => {
    mockApis()
    await page({ staffId: '42', scene: 'c=1', companyId: '7' })

    expect(mocks.bindScene).toHaveBeenCalledWith('c=1', '7')
  })

  // ---- 自定义顶栏 ----

  it('按状态栏高度算顶栏避让（44 + 44）', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    expect(p.data.statusBarHeight).toBe(44)
    expect(p.data.navHeight).toBe(88)
  })

  // ---- 四次请求的拼装 ----

  it('卡片字段取自响应根级，图片一律拼绝对 URL', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    expect(p.data.employee.avatar).toBe('https://cdn.test/uploads/a.png')
    expect(p.data.company.logo).toBe('https://cdn.test/uploads/logo.png')
    expect(p.data.profile.cover).toBe('https://cdn.test/uploads/cover.png')
    expect(p.data.categories[0].icon).toBe('https://cdn.test/uploads/c1.png')
    expect(p.data.featured[0].cover).toBe('https://cdn.test/uploads/p9.jpg')
    expect(p.data.services[0].icon).toBe('https://cdn.test/uploads/s3.png')
    expect(p.data.loading).toBe(false)
  })

  it('首页布局：order 生效，背景图与遮罩浓度换算成 0~1', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    expect(p.data.homeOrder).toEqual(['achievement', 'service'])
    expect(p.data.bgImage).toBe('https://cdn.test/uploads/bg.jpg')
    expect(p.data.bgMask).toBe(60)
    expect(p.data.bgMaskAlpha).toBeCloseTo(0.6)
  })

  // 业绩区在首页 → 箭头朝下（滚过去）；被布局隐藏 → 朝右（跳案例页）
  it('业绩区在首页时箭头朝下', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    expect(p.data.caseArrow).toBe('▾')
  })

  it('业绩区被隐藏时箭头朝右', async () => {
    mockApis({ layout: { code: 0, order: ['service'] } })
    const p = await page({ staffId: '42' })

    expect(p.data.caseArrow).toBe('▸')
  })

  it('布局接口没给 order 时回落默认顺序', async () => {
    mockApis({ layout: { code: 0 } })
    const p = await page({ staffId: '42' })

    expect(p.data.homeOrder).toEqual(['achievement', 'service'])
  })

  it('员工已停用（code=2）：显示停用态而不是「不存在」', async () => {
    mockApis({ card: { code: 2 } })
    const p = await page({ staffId: '42' })

    expect(p.data.disabled).toBe(true)
    expect(p.data.notFound).toBe(false)
    expect(p.data.loading).toBe(false)
  })

  it('员工不存在（code=1）：notFound 并提示', async () => {
    mockApis({ card: { code: 1 } })
    const p = await page({ staffId: '42' })

    expect(p.data.notFound).toBe(true)
    expect(wxStub.showToast).toHaveBeenCalledWith({ title: '员工不存在', icon: 'none' })
    expect(p.data.loading).toBe(false)
  })

  it('card 里没有 employee 字段也按不存在处理', async () => {
    mockApis({ card: { code: 0 } })
    const p = await page({ staffId: '42' })

    expect(p.data.notFound).toBe(true)
  })

  // 回归：reject 冒泡出 async 函数会让页面永远停在「加载中…」（真实缺陷）
  it('请求异常：落到错误态且 loading 必须落下', async () => {
    mockApis({ fail: true })
    const p = await page({ staffId: '42' })

    expect(p.data.error).toBe(true)
    expect(p.data.loading).toBe(false)
  })

  // ---- 访问埋点 ----

  it('加载成功后上报一次访问', async () => {
    mockApis()
    await page({ staffId: '42' })

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'visit' })
  })

  // onShow 每次显示都会触发；不去重的话「我的」↔名片来回切几下访问量就虚高
  it('同一员工多次 onShow 只上报一次访问', async () => {
    mockApis()
    const p = await page({ staffId: '42' })
    const before = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track').length

    p.onShow()
    p.onShow()
    await flush()

    const after = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track').length
    expect(after).toBe(before)
  })

  // 时序兜底：真机上 onLoad 之后立刻会 onShow，而 staffId 可能要等解析完成才就绪。
  // 只挂 onShow 会漏计，所以 loadAll 成功后要再补一次 trackVisit（内部按 staffId 去重）。
  //
  // 这条用例原先是「弹窗输入编号进入时也计入访问」——弹窗那条路径已随「裸启动改跳
  // 名片墙」删掉了，但**时序不变量本身还在**，所以按真机顺序手工驱动一遍来守它。
  it('onShow 早于 staffId 就绪时，加载完成后补报访问', async () => {
    mockApis()
    const p = await page({ staffId: '42' })
    const tracks = () => mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    const before = tracks().length

    // 回到「staffId 尚未解析出来」的时刻，模拟 onShow 先到
    p.data.staffId = 0
    p.data.lastVisitStaffId = 0
    p.onShow()
    await flush()
    expect(tracks().length).toBe(before) // 没有 staffId 就什么都不报

    // staffId 就绪 → loadAll 末尾那次 trackVisit 补上
    p.setData({ staffId: 42 })
    p.trackVisit()
    expect(tracks().length).toBe(before + 1)
    expect(tracks()[before][2]).toEqual({ staffId: 42, eventType: 'visit' })
  })

  // ---- 顶栏「我的」入口 ----

  it('未登录：顶栏不显示头像', async () => {
    mockApis()
    const p = await page({ staffId: '42' })
    p.onShow()
    await flush()

    expect(p.data.meAvatar).toBe('')
    expect(mocks.request.mock.calls.some((c) => c[1] === '/api/employee/me')).toBe(false)
  })

  it('已登录：顶栏显示员工头像（绝对 URL）', async () => {
    mockApis()
    storage.set('empToken', 'tk-1')
    const p = await page({ staffId: '42' })
    p.onShow()
    await flush()

    expect(p.data.meAvatar).toBe('https://cdn.test/uploads/a.png')
  })

  it('取资料失败：静默清空头像，不弹错（游客不该被报错骚扰）', async () => {
    mockApis()
    storage.set('empToken', 'tk-1')
    const p = await page({ staffId: '42' })
    wxStub.showToast.mockClear()
    mocks.request.mockImplementation(async (_m: string, path: string): Promise<any> => {
      if (path === '/api/employee/me') throw new Error('401')
      return { code: 0 }
    })

    p.onShow()
    await flush()

    expect(p.data.meAvatar).toBe('')
    expect(wxStub.showToast).not.toHaveBeenCalled()
  })

  // ---- 「查看案例」 ----

  it('没有分类：提示暂无案例，不跳转', async () => {
    mockApis({ ach: { code: 0, categories: [], featured: [] } })
    const p = await page({ staffId: '42' })

    p.onEnterCase()

    expect(wxStub.showToast).toHaveBeenCalledWith({ title: '暂无案例', icon: 'none' })
    expect(wxStub.pageScrollTo).not.toHaveBeenCalled()
    expect(wxStub.navigateTo).not.toHaveBeenCalled()
  })

  it('业绩区在首页：滚动到该区块（不离开名片页）', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    p.onEnterCase()

    expect(wxStub.pageScrollTo).toHaveBeenCalledWith(
      expect.objectContaining({ selector: '#sec-achievement' })
    )
    expect(wxStub.navigateTo).not.toHaveBeenCalled()
  })

  it('业绩区被隐藏：跳全部案例列表', async () => {
    mockApis({ layout: { code: 0, order: ['service'] } })
    const p = await page({ staffId: '42' })

    p.onEnterCase()

    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      url: '/pages/project/list/list?categoryId=0&staffId=42',
    })
  })

  // ---- 分享 ----

  it('分享：标题优先简称，路径带 staffId 与 companyId', async () => {
    mockApis()
    mocks.companyId = 5
    const p = await page({ staffId: '42' })

    const share = p.onShareAppMessage()

    expect(share.title).toBe('朗汇机电')
    expect(share.path).toBe('/pages/index/index?staffId=42&companyId=5')
  })

  it('没有简称时用全称', async () => {
    mockApis({
      card: card({ company: { fullName: '朗汇机电设备有限公司', logo: '' } }),
    })
    const p = await page({ staffId: '42' })

    expect(p.onShareAppMessage().title).toBe('朗汇机电设备有限公司')
  })

  it('名片未加载时分享用产品名兜底（不是任何具体企业名）', async () => {
    mockApis({ fail: true })
    const p = await page({ staffId: '42' })

    // 兜底必须是产品名：一套代码多租户，写死某个客户名对第二个客户就是错的
    expect(p.onShareAppMessage().title).toBe('微传名片')
    expect(p.onShareAppMessage().path).toBe('/pages/index/index?staffId=42')
  })

  // 转发名片埋点（targetId=0，区别于案例/服务详情的转发）：
  // 转发量是「员工愿不愿意把这张名片推出去」的直接信号
  it('分享名片：上报 share（不带 targetId）', async () => {
    mockApis()
    const p = await page({ staffId: '42' })
    mocks.request.mockClear() // 清掉加载过程的访问埋点，只看分享这一条

    p.onShareAppMessage()

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'share' })
  })

  // 每次转发都计一次（不去重）：转发意愿的频次本身就是信号，
  // 与 visit 的页面实例级去重不是一回事
  it('多次分享各计一次 share', async () => {
    mockApis()
    const p = await page({ staffId: '42' })
    mocks.request.mockClear()

    p.onShareAppMessage()
    p.onShareAppMessage()

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(2)
  })

  // ---- 顶部滚动遮罩 ----

  it('滚动遮罩随 scrollTop 渐显，并裁到 0~1', async () => {
    mockApis()
    const p = await page({ staffId: '42' }) // navHeight = 88

    p.onPageScroll({ scrollTop: 44 })
    expect(p.data.navMask).toBe(0.5)

    p.onPageScroll({ scrollTop: 176 }) // 2.0 → 裁到 1
    expect(p.data.navMask).toBe(1)

    p.onPageScroll({ scrollTop: -10 }) // 负值 → 0
    expect(p.data.navMask).toBe(0)
  })

  // 0.01 死区：每帧都 setData 会让滚动发涩（这是刻意的性能取舍）
  it('小于 0.01 的变化不触发更新', async () => {
    mockApis()
    const p = await page({ staffId: '42' })

    p.onPageScroll({ scrollTop: 44 }) // 0.5
    p.onPageScroll({ scrollTop: 44.44 }) // 0.505，差 0.005
    expect(p.data.navMask).toBe(0.5)

    p.onPageScroll({ scrollTop: 45 }) // 0.511，超过死区
    expect(p.data.navMask).toBeCloseTo(0.511, 3)
  })
})
