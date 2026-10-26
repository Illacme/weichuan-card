import { describe, it, expect, vi, beforeEach } from 'vitest'

// 统一响应契约见 request.ts 注释：
//   成功 code=0；业务失败 code=1；特殊状态 code=2；鉴权失败 HTTP 200 + code 401/403。
// 拦截器以 res.code 为准。PC 后台为 httpOnly Cookie 鉴权：令牌由浏览器携带，JS 不接触，
// 故无 Bearer 请求拦截；401 时用 refresh Cookie 静默换发并重试一次。
// 这里直接驱动已注册的拦截器函数，并用 vi.spyOn 控制刷新/重试的内部请求，不真正发网络请求。
//
// mock 两个重依赖：
//   - element-plus 的 ElMessage（避免真实 toast）
//   - @/router 的 default 导出（避免加载 vue-router 全链路 + 真实跳转）
const { elMessage, routerReplace } = vi.hoisted(() => ({
  elMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn() },
  routerReplace: vi.fn(),
}))

vi.mock('element-plus', () => ({ ElMessage: elMessage }))
vi.mock('@/router', () => ({
  default: {
    currentRoute: { value: { path: '/apply/open' } },
    replace: routerReplace,
  },
}))

import http from '@/api/request'

// 取出已注册的响应拦截 fulfilled（axios 把 fulfilled/rejected 存在 handlers 数组）
function respFulfilled() {
  return (http.interceptors.response as any).handlers[0].fulfilled
}

describe('request 响应拦截：鉴权契约（httpOnly Cookie 流）', () => {
  beforeEach(() => {
    localStorage.clear()
    routerReplace.mockClear()
    elMessage.error.mockClear()
    elMessage.warning.mockClear()
    vi.restoreAllMocks()
  })

  it('code=0 成功：回传整包（交由各 api 函数取字段）', async () => {
    const data = await respFulfilled()({ data: { code: 0, msg: 'ok', data: { a: 1 } }, config: {} })
    expect(data).toEqual({ code: 0, msg: 'ok', data: { a: 1 } })
  })

  it('code=401 且刷新失败：清登录态、跳登录并 reject', async () => {
    localStorage.setItem('admin_user', 'admin')
    localStorage.setItem('admin_loggedin', '1')
    // /api/refresh 返回非 0 → 刷新失败
    vi.spyOn(http, 'post').mockResolvedValue({ code: 1 } as any)
    const p = respFulfilled()({ data: { code: 401, msg: '登录失效，请重新登录' }, config: {} })
    await expect(p).rejects.toThrow('登录失效，请重新登录')
    expect(localStorage.getItem('admin_user')).toBeNull()
    expect(localStorage.getItem('admin_loggedin')).toBeNull()
    expect(elMessage.error).toHaveBeenCalledWith('登录失效，请重新登录')
    expect(routerReplace).toHaveBeenCalledWith('/login')
  })

  it('code=401 且刷新成功：静默重试原请求并返回其结果，不跳登录', async () => {
    // 刷新成功：服务端轮换下发新 Cookie，返回 code:0
    vi.spyOn(http, 'post').mockResolvedValue({ code: 0 } as any)
    // 重试原请求成功
    vi.spyOn(http, 'request').mockResolvedValue({ code: 0, data: { ok: true } } as any)
    const cfg = { url: '/api/admin/employees', method: 'GET' }
    const result = await respFulfilled()({ data: { code: 401, msg: 'x' }, config: cfg })
    expect(result).toEqual({ code: 0, data: { ok: true } })
    expect(http.post).toHaveBeenCalledWith('/refresh', {}, expect.objectContaining({ __skipRefresh: true }))
    expect(routerReplace).not.toHaveBeenCalled()
  })

  it('code=401 刷新请求自身（__skipRefresh）不再递归刷新', async () => {
    // 刷新端点自身返回 401 时，应直接走清登录态，不再调用 http.post 去刷新
    const postSpy = vi.spyOn(http, 'post')
    localStorage.setItem('admin_user', 'admin')
    const p = respFulfilled()({
      data: { code: 401, msg: '刷新令牌无效' },
      config: { __skipRefresh: true },
    })
    await expect(p).rejects.toThrow('刷新令牌无效')
    expect(postSpy).not.toHaveBeenCalled()
    expect(localStorage.getItem('admin_user')).toBeNull()
    expect(routerReplace).toHaveBeenCalledWith('/login')
  })

  it('code=403：警告并 reject', async () => {
    const p = respFulfilled()({ data: { code: 403, msg: '无权限' }, config: {} })
    await expect(p).rejects.toThrow('无权限')
    expect(elMessage.warning).toHaveBeenCalledWith('无权限')
  })

  it('code!=0（业务失败）：报错并 reject', async () => {
    const p = respFulfilled()({ data: { code: 1, msg: '参数错误' }, config: {} })
    await expect(p).rejects.toThrow('参数错误')
    expect(elMessage.error).toHaveBeenCalledWith('参数错误')
  })

  // 请求号回传：后端每个响应都带 X-Request-Id，前端记下最后一个，白屏上报时一并带走，
  // 这样「用户说页面崩了」时能直接定位到后端同一条访问日志。
  it('响应头带 X-Request-Id 时记下来，前端报错上报会带走它', async () => {
    // 参数必须显式声明：fetch 调用有两个实参，不写形参时 vitest 推断出的是空元组，
    // 取 calls[0][1] 会直接编译不过（TS2493）
    const fetchMock = vi.fn(async (_url: any, _init?: any) => ({ ok: true }) as any)
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.stubGlobal('fetch', fetchMock)
    try {
      await respFulfilled()({
        data: { code: 0, msg: 'ok' },
        config: {},
        headers: { 'x-request-id': 'rid-abc123' },
      })
      const { reportClientError } = await import('@/api/clientError')
      reportClientError({ message: 'boom' })
      // vitest 下 `import.meta.env.DEV` 为 true → 走本地 console.warn 分支；生产构建走 fetch。
      // 两条路都只断言同一个 requestId：测的是「记没记住」，不是「怎么发」。
      const body = fetchMock.mock.calls.length
        ? JSON.parse((fetchMock.mock.calls[0][1] as any).body)
        : (warnSpy.mock.calls.find((c: any[]) => c[1] && c[1].requestId) || [])[1]
      expect(body?.requestId).toBe('rid-abc123')
    } finally {
      vi.unstubAllGlobals()
      warnSpy.mockRestore()
    }
  })

  // 记请求号是「锦上添花」，绝不能因为它崩掉整个拦截器：
  // 否则所有业务错误都会变成一句 Cannot read properties of undefined，真实原因全丢。
  it('响应对象没有 headers 时也不崩（只跳过记录）', async () => {
    const p = respFulfilled()({ data: { code: 1, msg: '参数错误' }, config: {} })
    await expect(p).rejects.toThrow('参数错误')
  })
})
