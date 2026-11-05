// 名片墙（pages/team/team）：小程序**裸启动**时的落地页。
//
// 这一页要解决的具体事故：扫「体验版二维码」进入小程序属于裸启动，微信不带任何
// 参数，首页拿不到 staffId，于是弹出一个「输入员工编号」的输入框（开发期兜底）。
// 客户第一次打开自家小程序，看到的就是一个要求输入数字的弹窗。
//
// 所以测试要钉住三件事：
//  1. 四种状态各自画对（有内容 / 公司没人 / 企业不可用 / 网络失败）——
//     混在一起会让访客误以为系统坏了；
//  2. 点某人跳名片页**必须带 companyId**，不传就落到平台缺省的 1 号企业，
//     点进去看到的是别家公司的人；
//  3. 后端只回公开字段，端上不得凭空补 phone 之类（这里只验证透传不改写）。
//
// 2026-10-30 增加分页与搜索后，另钉两件事（见下方「搜索」「查看全部」两组）：
//  4. 首屏只拉一页 60，hasMore 以后端为准（不是用 total - list.length 推）；
//  5. 输入时不发请求，只有确认搜索才查；搜索态下不走「默认进某个员工」的改道。
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
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  installApp({ myStaffId: 0 })
  mocks.request.mockClear()
})

async function page(query: any = {}) {
  await import('../pages/team/team')
  const p = makeInstance(capturedPage())
  p.onLoad(query)
  await flush()
  return p
}

const teamResp = (extra: any = {}) => ({
  code: 0,
  company: { id: 7, fullName: '廊坊市朗汇机电设备有限公司', shortName: '朗汇机电', logo: '/uploads/logo.png' },
  entry: { mode: 'wall', staffId: 0 },
  data: [
    { staffId: 11, name: '高立新', title: '销售经理', avatar: '/uploads/a1.png' },
    { staffId: 12, name: '张三', title: '', avatar: '' },
  ],
  total: 2,
  page: 1,
  size: 60,
  hasMore: false,
  ...extra,
})

describe('名片墙 · 状态与数据', () => {
  it('拉到第一页：名单、人数、企业抬头都落到 data 上', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    // 首屏只拉一页 60（不是全量 300）：大企业不必等几百条 JSON 解析完才出画面
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team', { page: 1, size: 60 })
    expect(p.data.list).toHaveLength(2)
    expect(p.data.list[0]).toMatchObject({ staffId: 11, name: '高立新', title: '销售经理' })
    expect(p.data.total).toBe(2)
    expect(p.data.hasMore).toBe(false)
    expect(p.data.title).toBe('朗汇机电')
    // 头像 / LOGO 必须是绝对地址：<image src> 不认相对路径，否则整屏裂图
    expect(p.data.list[0].avatar).toBe('https://cdn.test/uploads/a1.png')
    expect(p.data.logo).toBe('https://cdn.test/uploads/logo.png')
    expect(p.data.loading).toBe(false)
    // 导航栏标题跟随企业简称
    expect(wxStub.setNavigationBarTitle).toHaveBeenCalledWith({ title: '朗汇机电' })
  })

  it('企业信息还没填：标题回落品牌名，不显示空标题', async () => {
    mocks.request.mockResolvedValueOnce(
      teamResp({ company: { id: 7, fullName: '', shortName: '', logo: '' } })
    )
    const p = await page()

    expect(p.data.title).toBe('微传名片')
    expect(p.data.logo).toBe('')
  })

  it('公司还没人开通名片：list 为空且不算错误（页面画「还没有可展示的名片」）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ data: [], total: 0 }))
    const p = await page()

    expect(p.data.list).toEqual([])
    expect(p.data.error).toBe(false)
    expect(p.data.unavailable).toBe(false)
    expect(p.data.loading).toBe(false)
  })

  it('还有更多人：hasMore 为真，底部才显示「查看全部 N 位同事」', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ total: 500, hasMore: true }))
    const p = await page()

    expect(p.data.list).toHaveLength(2)
    expect(p.data.total).toBe(500)
    expect(p.data.hasMore).toBe(true)
  })

  // hasMore 不能用 total > list.length 推：搜索时 total 是匹配总数、
  // list 只有第一页，两者相减得到的是「没加载的匹配数」，语义不同。
  it('hasMore 以后端为准：total 大于本页条数但后端说没有了，就不该显示入口', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ total: 2, hasMore: false }))
    const p = await page()

    expect(p.data.hasMore).toBe(false)
  })

  it('企业不存在 / 已停用（code=1）：走 unavailable，不是网络错误', async () => {
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '企业不存在或已停用' })
    const p = await page()

    expect(p.data.unavailable).toBe(true)
    expect(p.data.error).toBe(false)
    expect(p.data.loading).toBe(false)
  })

  it('网络异常：走 error 且有重试入口', async () => {
    mocks.request.mockRejectedValueOnce(new Error('boom'))
    const p = await page()

    expect(p.data.error).toBe(true)
    expect(p.data.unavailable).toBe(false)
    expect(p.data.loading).toBe(false)
    // 重试必须复用同一个 load（不是把失败态永久钉住）
    mocks.request.mockResolvedValueOnce(teamResp())
    await p.load()
    expect(p.data.error).toBe(false)
    expect(p.data.list).toHaveLength(2)
  })
})

describe('名片墙 · 搜索', () => {
  it('输入不发请求（每敲一个字打一次接口既费流量又会让宫格闪烁）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()
    mocks.request.mockClear()

    p.onKeywordInput({ detail: { value: '张' } })

    expect(p.data.keyword).toBe('张')
    expect(p.data.activeKeyword).toBe('')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('确认搜索：带 keyword 从第一页重查', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()
    mocks.request.mockClear()
    mocks.request.mockResolvedValueOnce(teamResp({ data: [], total: 0 }))

    p.onKeywordInput({ detail: { value: '  张  ' } })
    p.onSearch()
    await flush()

    // 两端空格必须吃掉，否则后端会拿着「  张  」去匹配
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team', { page: 1, size: 60, keyword: '张' })
    expect(p.data.activeKeyword).toBe('张')
  })

  it('关键字没变：不重复请求', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()
    p.onKeywordInput({ detail: { value: '张' } })
    p.onSearch()
    await flush()
    mocks.request.mockClear()

    p.onSearch()

    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('清空搜索：恢复全员列表', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()
    p.onKeywordInput({ detail: { value: '张' } })
    p.onSearch()
    await flush()
    mocks.request.mockClear()
    mocks.request.mockResolvedValueOnce(teamResp())

    p.onClearSearch()
    await flush()

    expect(p.data.keyword).toBe('')
    expect(p.data.activeKeyword).toBe('')
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team', { page: 1, size: 60 })
  })

  // 用户在墙上搜人时突然被弹进某个人的名片，会觉得「我搜的人呢」
  it('搜索态下不走「默认进入指定员工」的改道', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 11 } }))

    p.onKeywordInput({ detail: { value: '张' } })
    p.onSearch()
    await flush()

    expect(wxStub.reLaunch).not.toHaveBeenCalled()
  })
})

describe('名片墙 · 查看全部', () => {
  it('带 companyId 与当前关键字进全部页', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ total: 500, hasMore: true }))
    const p = await page()
    p.onKeywordInput({ detail: { value: '张 三' } })
    p.onSearch()
    await flush()

    p.goAll()

    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      // 关键字要 encodeURIComponent：中文直接拼进 URL 会被截断
      url: '/pages/team/all?companyId=7&keyword=' + encodeURIComponent('张 三'),
    })
  })

  it('没有关键字时只带 companyId', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ total: 500, hasMore: true }))
    const p = await page()

    p.goAll()

    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: '/pages/team/all?companyId=7' })
  })
})

describe('名片墙 · 企业配置的默认入口', () => {
  it('entry.mode=staff：直接改道到该员工名片，不渲染名片墙', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 11 } }))
    const p = await page()

    expect(wxStub.reLaunch).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=11&companyId=7',
    })
    // 关键：不能先把名片墙画出来再跳——那样访客会看到宫格闪一下
    expect(p.data.list).toEqual([])
  })

  it('entry.mode=staff 但 staffId 为 0：按名片墙处理（后端已回落，端上再兜一层）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 0 } }))
    const p = await page()

    expect(wxStub.reLaunch).not.toHaveBeenCalled()
    expect(p.data.list).toHaveLength(2)
  })

  it('entry.mode=wall：正常渲染名片墙', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    expect(wxStub.reLaunch).not.toHaveBeenCalled()
    expect(p.data.list).toHaveLength(2)
  })
})

describe('名片墙 · 默认入口的本地缓存（2026-11-04 提速）', () => {
  // 背景：企业配了「默认进某位员工名片」时，这一页只是个中转站。
  // 原来每一轮冷启动都得先等 /api/team 往返（弱网 1~3 秒），
  // 访客盯着「微传名片 + 加载中」看半天才被 reLaunch 走。
  // 记住上次的结果，下次直接跳。

  const cacheKey = 'entryStaff:0' // companyId=0（未绑定租户）

  it('缓存还在有效期内：直接跳名片页，一次请求都不发', async () => {
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 11, at: Date.now() }))

    const p = await page()

    expect(mocks.request).not.toHaveBeenCalled()
    expect(wxStub.reLaunch).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=11&fromEntryCache=1',
    })
    // 跳转中整页不渲染，否则会先闪一下名片墙
    expect(p.data.redirecting).toBe(true)
  })

  // fromEntryCache=1 是给名片页的自愈线索：那位员工要是已经停用了，
  // 名片页负责清缓存并把访客送回来。这一格漏了，缓存就会把人锁死在死名片上。
  it('凭缓存跳转时必须带上 fromEntryCache 标记', async () => {
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 11, at: Date.now() }))

    await page()

    const url = wxStub.reLaunch.mock.calls[0][0].url
    expect(url).toContain('fromEntryCache=1')
  })

  it('缓存已过期：照常请求后端，不跳', async () => {
    // at=1 → 1970 年，必然过期
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 11, at: 1 }))
    mocks.request.mockResolvedValueOnce(teamResp())

    const p = await page()

    expect(mocks.request).toHaveBeenCalled()
    expect(wxStub.reLaunch).not.toHaveBeenCalled()
    expect(p.data.list).toHaveLength(2)
  })

  it('带关键字进入（搜索）：不凭缓存跳，直接查', async () => {
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 11, at: Date.now() }))
    mocks.request.mockResolvedValueOnce(teamResp())

    const p = await page({ keyword: '张' })

    expect(mocks.request).toHaveBeenCalled()
    expect(wxStub.reLaunch).not.toHaveBeenCalled()
    // 关键字要带回搜索框，不能因为跳转逻辑把它吃掉
    expect(p.data.activeKeyword).toBe('张')
  })

  it('后端确认要改道：把结果记进缓存，下次才能直接跳', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 11 } }))

    await page()

    const rec = JSON.parse(wxStub.getStorageSync(cacheKey))
    expect(rec.staffId).toBe(11)
  })

  // 管理员取消「默认入口」后，旧缓存必须清掉——不清的话访客还会被默默跳到那位员工
  it('后端说不需要改道：清掉残留的旧缓存', async () => {
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 99, at: 1 })) // 已过期，会走网络
    mocks.request.mockResolvedValueOnce(teamResp()) // entry.mode = wall

    await page()

    expect(wxStub.getStorageSync(cacheKey)).toBe('')
  })

  // 读写必须落在同一个键上。这条踩过一次：读用 query 的 companyId、
  // 写用 getCompanyId()，两者不同时缓存「写得进、读不到」，而且完全不报错。
  it('query 带 companyId 时按它分键写入', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 11 } }))

    await page({ companyId: '7' })

    // 分开断言：键写错时 getStorageSync 回空串，直接 JSON.parse 只会抛
    // 「Unexpected end of JSON input」，看不出是哪儿错了
    const raw = wxStub.getStorageSync('entryStaff:7')
    expect(raw).toBeTruthy()
    expect(JSON.parse(raw).staffId).toBe(11)
    expect(wxStub.getStorageSync('entryStaff:0')).toBe('')
  })

  it('下次带同一个 companyId 裸启动：命中同一份缓存直接跳', async () => {
    wxStub.setStorageSync('entryStaff:7', JSON.stringify({ staffId: 11, at: Date.now() }))

    await page({ companyId: '7' })

    expect(mocks.request).not.toHaveBeenCalled()
    expect(wxStub.reLaunch).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=11&companyId=7&fromEntryCache=1',
    })
  })

  // 缓存按租户分键，A 企业的默认员工不会被套到 B 企业头上
  it('别的租户的缓存不会串过来', async () => {
    wxStub.setStorageSync('entryStaff:7', JSON.stringify({ staffId: 11, at: Date.now() }))
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'wall', staffId: 0 } }))

    const p = await page({ companyId: '8' })

    expect(mocks.request).toHaveBeenCalled()
    expect(wxStub.reLaunch).not.toHaveBeenCalled()
    expect(p.data.list).toHaveLength(2)
  })

  // 名片页发现缓存里的员工已失效，会带 skipEntryCache=1 把人送回来。
  // 这一次必须清缓存 + 老实走网络 + 不再改道，否则会两边来回跳成死循环。
  it('skipEntryCache：清缓存、走网络、这次不再改道', async () => {
    wxStub.setStorageSync(cacheKey, JSON.stringify({ staffId: 11, at: Date.now() }))
    // 后端仍然说「要改道」——正是这种矛盾才会形成死循环
    mocks.request.mockResolvedValueOnce(teamResp({ entry: { mode: 'staff', staffId: 11 } }))

    const p = await page({ skipEntryCache: '1' })

    expect(wxStub.getStorageSync(cacheKey)).toBe('') // 缓存已清
    expect(mocks.request).toHaveBeenCalled()
    expect(wxStub.reLaunch).not.toHaveBeenCalled() // 不再跳
  })
})

describe('名片墙 · 点人进名片', () => {
  it('跳名片页带 staffId 与 companyId（不带 companyId 会落到别家公司）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    p.onTap({ currentTarget: { dataset: { id: '11' } } })

    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=11&companyId=7',
    })
  })

  it('列表接口回传的 companyId 比 query 更权威（后端按实际生效租户回）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page({ companyId: '3' })

    expect(p.data.companyId).toBe(7)
    p.onTap({ currentTarget: { dataset: { id: '12' } } })
    expect(wxStub.navigateTo).toHaveBeenCalledWith({
      url: '/pages/index/index?staffId=12&companyId=7',
    })
  })

  it('非法 staffId：不跳转（避免打开一个 staffId=0 的空白名片页）', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    p.onTap({ currentTarget: { dataset: { id: 'abc' } } })

    expect(wxStub.navigateTo).not.toHaveBeenCalled()
  })

  it('转发名片墙：落点回到名片墙本身', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    const share = p.onShareAppMessage()
    expect(share.path).toBe('/pages/team/team')
    expect(share.title).toBe('朗汇机电')
  })
})
