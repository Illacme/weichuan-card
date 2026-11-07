// 小程序端管理员请求通道（utils/adminRequest.ts）。
//
// 这条通道与员工通道（utils/request.ts）是两套身份：管理员令牌绝不能和 empToken 混写，
// 401 刷新也不能拿员工的 refresh 去续。测试重点在：
//   - 带的是 adminToken，且不注入 companyId（后端按 JWT 里的租户隔离）；
//   - 401 用 adminRefreshToken 单飞刷新后重试一次；
//   - 刷新失败彻底清令牌（否则下次进审核页会以为已登录、每条请求都 401）；
//   - 网关兜底页（HTML）不能被当成业务信封。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installWx } from './helpers/harness'

vi.mock('../utils/request', () => ({
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
})

async function mod() {
  return import('../utils/adminRequest')
}

describe('管理员请求通道', () => {
  it('带 adminToken（不是 empToken），GET 参数拼成 query', async () => {
    const { adminRequest } = await mod()
    storage.set('adminToken', 'admin-tk')
    storage.set('empToken', 'emp-tk')
    const calls: any[] = []
    wxStub.request = vi.fn((o: any) => {
      calls.push(o)
      o.success({ statusCode: 200, data: { code: 0, data: [], total: 0, page: 1, size: 20 } })
    })

    const r = await adminRequest('GET', '/api/admin/apply/open', {
      page: 1,
      size: 20,
      status: 'pending',
    })

    expect(r.code).toBe(0)
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('https://api.test/api/admin/apply/open?page=1&size=20&status=pending')
    expect(calls[0].header.Authorization).toBe('Bearer admin-tk')
  })

  it('不自动注入 companyId：租户隔离靠 JWT，不靠入参', async () => {
    const { adminRequest } = await mod()
    storage.set('adminToken', 'admin-tk')
    const calls: any[] = []
    wxStub.request = vi.fn((o: any) => {
      calls.push(o)
      o.success({ statusCode: 200, data: { code: 0 } })
    })
    await adminRequest('POST', '/api/admin/apply/open/1/approve', {})
    expect(calls[0].data).toEqual({})
  })

  it('401 时用 adminRefreshToken 刷新并重试一次（并发只刷新一次）', async () => {
    const { adminRequest } = await mod()
    storage.set('adminToken', 'old-tk')
    storage.set('adminRefreshToken', 'admin-rt')

    let refreshCalls = 0
    wxStub.request = vi.fn((o: any) => {
      if (o.url.endsWith('/api/refresh')) {
        refreshCalls += 1
        expect(o.data).toEqual({ refreshToken: 'admin-rt' })
        o.success({ statusCode: 200, data: { code: 0, token: 'new-tk' } })
        return
      }
      const auth = o.header.Authorization
      if (auth === 'Bearer old-tk') {
        o.success({ statusCode: 200, data: { code: 401, msg: '登录失效' } })
        return
      }
      o.success({ statusCode: 200, data: { code: 0, data: [{ id: 1 }] } })
    })

    const [a, b] = await Promise.all([
      adminRequest('GET', '/api/admin/apply/open'),
      adminRequest('GET', '/api/admin/apply/modify'),
    ])

    expect(refreshCalls).toBe(1)
    expect(a.code).toBe(0)
    expect(b.code).toBe(0)
    expect(storage.get('adminToken')).toBe('new-tk')
  })

  it('刷新失败就清掉管理员令牌，并把 401 交给页面去引导登录', async () => {
    const { adminRequest, hasAdminToken } = await mod()
    storage.set('adminToken', 'old-tk')
    storage.set('adminRefreshToken', 'bad-rt')

    wxStub.request = vi.fn((o: any) => {
      if (o.url.endsWith('/api/refresh')) {
        o.success({ statusCode: 200, data: { code: 401, msg: '刷新令牌无效' } })
        return
      }
      o.success({ statusCode: 200, data: { code: 401, msg: '登录失效' } })
    })

    const r = await adminRequest('GET', '/api/admin/apply/open')
    expect(r.code).toBe(401)
    expect(hasAdminToken()).toBe(false)
    expect(storage.has('adminRefreshToken')).toBe(false)
  })

  it('网关兜底页（HTML）按网络错误 reject，不当成业务信封', async () => {
    const { adminRequest } = await mod()
    storage.set('adminToken', 'admin-tk')
    wxStub.request = vi.fn((o: any) => {
      o.success({ statusCode: 502, data: '<html>502 Bad Gateway</html>' })
    })
    await expect(adminRequest('GET', '/api/admin/apply/open')).rejects.toThrow('HTTP 502')
  })

  it('saveAdminAuth 拒绝空令牌；clearAdminAuth 两个键都清', async () => {
    const { saveAdminAuth, clearAdminAuth, hasAdminToken } = await mod()
    expect(saveAdminAuth(null)).toBe(false)
    expect(saveAdminAuth({ token: '' })).toBe(false)
    expect(hasAdminToken()).toBe(false)

    expect(saveAdminAuth({ token: 'tk', refreshToken: 'rt' })).toBe(true)
    expect(storage.get('adminToken')).toBe('tk')
    expect(storage.get('adminRefreshToken')).toBe('rt')

    clearAdminAuth()
    expect(hasAdminToken()).toBe(false)
    expect(storage.has('adminRefreshToken')).toBe(false)
  })

  it('微信绑定标记独立于令牌：401 只清令牌，主动退出才清绑定', async () => {
    const {
      markAdminWxBound, hasAdminWxBound, clearAdminWxBound,
      ADMIN_WX_BOUND_KEY, clearAdminAuth,
    } = await mod()

    expect(hasAdminWxBound()).toBe(false)
    markAdminWxBound()
    expect(storage.get(ADMIN_WX_BOUND_KEY)).toBe(true)
    expect(hasAdminWxBound()).toBe(true)

    // 令牌失效不能把「这台设备绑过管理员」抹掉，否则静默免密永远不再触发
    clearAdminAuth()
    expect(hasAdminWxBound()).toBe(true)

    clearAdminWxBound()
    expect(hasAdminWxBound()).toBe(false)
  })
})
