// 下拉刷新（横切特性）：9 个内容页都要「下拉能重拉、指示器一定收掉」。
//
// 为什么单独一个 spec：这不是某一页的业务逻辑，而是横跨 8 个页面的同一份约定。
// 最容易漏的是 finally —— 加载失败、或未登录被 redirectTo 时若不 stopPullDownRefresh，
// 顶部指示器会一直转，用户以为小程序卡死。所以每页都同时钉「成功」与「抛错」两条。
//
// 另一条约定是 silent：下拉时不能把整页切成「加载中」，否则内容先消失再回来，
// 比不刷新还难受。silent 语义在 team 上做精确断言（其余页同一写法，靠入参断言保证）。
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
  request: vi.fn(async (..._a: any[]): Promise<any> => ({ code: 0, data: [] })),
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

/** 下拉时该被调用的一个加载函数，以及期望的入参 */
type Loader = { m: string; args: any[] }

type Case = { name: string; path: string; loaders: Loader[] }

// silent 的页面传 { silent: true }（不闪整页 loading）；
// me 的 loadMe 无整页 loading 态，故无参；project/list 的分类条不需要 silent。
const CASES: Case[] = [
  { name: '名片墙 team', path: '../pages/team/team', loaders: [{ m: 'load', args: [{ silent: true }] }] },
  {
    name: '查看全部 team/all',
    path: '../pages/team/all',
    loaders: [{ m: 'load', args: [{ silent: true }] }],
  },
  {
    name: '名片首页 index',
    path: '../pages/index/index',
    loaders: [{ m: 'loadAll', args: [{ silent: true }] }],
  },
  {
    name: '项目列表 project/list',
    path: '../pages/project/list/list',
    loaders: [
      { m: 'loadCats', args: [] },
      { m: 'load', args: [{ silent: true }] },
    ],
  },
  {
    name: '项目详情 project/detail',
    path: '../pages/project/detail/detail',
    loaders: [{ m: 'load', args: [{ silent: true }] }],
  },
  {
    name: '服务详情 service/detail',
    path: '../pages/service/detail/detail',
    loaders: [{ m: 'load', args: [{ silent: true }] }],
  },
  { name: '我的 employee/me', path: '../pages/employee/me/me', loaders: [{ m: 'loadMe', args: [] }] },
  {
    name: '统计 employee/stat',
    path: '../pages/employee/stat/stat',
    loaders: [{ m: 'load', args: [{ silent: true }] }],
  },
  { name: '个人信息 mydata', path: '../pages/mydata/mydata', loaders: [{ m: 'loadMe', args: [{ silent: true }] }] },
]

async function loadPage(path: string) {
  await import(path)
  return makeInstance(capturedPage()) as any
}

describe('下拉刷新：注册与编排', () => {
  for (const c of CASES) {
    it(`${c.name}：注册了 onPullDownRefresh`, async () => {
      await loadPage(c.path)
      expect(typeof capturedPage().onPullDownRefresh).toBe('function')
    })

    it(`${c.name}：下拉触发加载并收掉指示器`, async () => {
      const inst = await loadPage(c.path)
      const spies: Record<string, any> = {}
      for (const l of c.loaders) {
        spies[l.m] = vi.fn(async () => {})
        inst[l.m] = spies[l.m]
      }

      await inst.onPullDownRefresh()

      for (const l of c.loaders) {
        expect(spies[l.m]).toHaveBeenCalledTimes(1)
        expect(spies[l.m]).toHaveBeenCalledWith(...l.args)
      }
      expect(wxStub.stopPullDownRefresh).toHaveBeenCalledTimes(1)
    })

    it(`${c.name}：加载抛错也要收掉指示器（否则顶部一直转）`, async () => {
      const inst = await loadPage(c.path)
      for (const l of c.loaders) {
        inst[l.m] = vi.fn(async () => {
          throw new Error('boom')
        })
      }

      // 不得把异常抛给框架：各页 load 自己落错误态，这里只要求「不崩 + 收指示器」
      await expect(inst.onPullDownRefresh()).resolves.toBeUndefined()
      expect(wxStub.stopPullDownRefresh).toHaveBeenCalledTimes(1)
    })
  }
})

describe('下拉刷新：silent 不把整页切成「加载中」', () => {
  const okResp = {
    code: 0,
    company: { id: 7, fullName: '廊坊市朗汇机电设备有限公司', shortName: '朗汇机电', logo: '' },
    entry: { mode: 'wall', staffId: 0 },
    data: [],
    total: 0,
  }

  it('silent：请求在途时 loading 保持 false，内容不消失', async () => {
    let release: any
    mocks.request.mockImplementation(
      () =>
        new Promise((r) => {
          release = r
        })
    )
    const inst = await loadPage('../pages/team/team')
    // 模拟「上一次失败留下了 error 横幅」
    inst.setData({ loading: false, error: true })

    const p = inst.load({ silent: true })
    await flush()

    expect(inst.data.loading).toBe(false) // 没有切成「加载中…」
    expect(inst.data.error).toBe(false) // 但错误横幅必须清掉，否则旧错误留在页面上

    release(okResp)
    await p
  })

  it('非 silent（首次进入 / 点重新加载）：请求在途时 loading 为 true', async () => {
    let release: any
    mocks.request.mockImplementation(
      () =>
        new Promise((r) => {
          release = r
        })
    )
    const inst = await loadPage('../pages/team/team')

    const p = inst.load()
    await flush()

    expect(inst.data.loading).toBe(true)

    release(okResp)
    await p
  })
})
