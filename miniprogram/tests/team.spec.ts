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
  const installed = installWx()
  wxStub = installed.wx
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
  data: [
    { staffId: 11, name: '高立新', title: '销售经理', avatar: '/uploads/a1.png' },
    { staffId: 12, name: '张三', title: '', avatar: '' },
  ],
  total: 2,
  ...extra,
})

describe('名片墙 · 状态与数据', () => {
  it('拉到全员：名单、人数、企业抬头都落到 data 上', async () => {
    mocks.request.mockResolvedValueOnce(teamResp())
    const p = await page()

    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/team')
    expect(p.data.list).toHaveLength(2)
    expect(p.data.list[0]).toMatchObject({ staffId: 11, name: '高立新', title: '销售经理' })
    expect(p.data.total).toBe(2)
    expect(p.data.truncated).toBe(false)
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

  it('人数超过上限：truncated 为真，页面才能说清「只显示了前 N 位」', async () => {
    mocks.request.mockResolvedValueOnce(teamResp({ total: 500 }))
    const p = await page()

    expect(p.data.list).toHaveLength(2)
    expect(p.data.total).toBe(500)
    expect(p.data.truncated).toBe(true)
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
