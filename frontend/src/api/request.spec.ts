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
const { elMessage, routerReplace, authLogout } = vi.hoisted(() => ({
  elMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn() },
  routerReplace: vi.fn(),
  authLogout: vi.fn(),
}))

vi.mock('element-plus', () => ({ ElMessage: elMessage }))
vi.mock('@/router', () => ({
  default: {
    currentRoute: { value: { path: '/apply/open' } },
    replace: routerReplace,
  },
}))
// 401 后必须连内存里的登录态一起清（store 不清 → 守卫仍认为已登录 →
// replace('/login') 被弹回后台 → 界面「已登录」但每个请求都 401）。
vi.mock('@/store/auth', () => ({ useAuthStore: () => ({ logout: authLogout }) }))

import http from '@/api/request'
import { setCompanyScope } from '@/store/companyScope'

// 取出已注册的响应拦截 fulfilled（axios 把 fulfilled/rejected 存在 handlers 数组）
function respFulfilled() {
  return (http.interceptors.response as any).handlers[0].fulfilled
}

// 请求拦截同理（本文件只有一个请求拦截器）
function reqFulfilled() {
  return (http.interceptors.request as any).handlers[0].fulfilled
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

  it('code=401 且刷新失败：清登录态（含内存 store）、跳登录并 reject', async () => {
    localStorage.setItem('admin_user', 'admin')
    localStorage.setItem('admin_loggedin', '1')
    // /api/refresh 返回非 0 → 刷新失败
    vi.spyOn(http, 'post').mockResolvedValue({ code: 1 } as any)
    const p = respFulfilled()({ data: { code: 401, msg: '未登录' }, config: {} })
    await expect(p).rejects.toThrow('未登录')
    expect(localStorage.getItem('admin_user')).toBeNull()
    expect(localStorage.getItem('admin_loggedin')).toBeNull()
    // 只清 localStorage 不够：store 里 loggedIn 仍为 true 时守卫会把用户弹回后台
    expect(authLogout).toHaveBeenCalled()
    // 提示用「登录已失效」而不是后端那两句内部术语（未登录 / 刷新令牌无效）
    expect(elMessage.error).toHaveBeenCalledWith('登录已失效，请重新登录')
    expect(routerReplace).toHaveBeenCalledWith('/login')
  })

  it('code=401 且当前页带白标码：跳登录时保留 company（品牌不丢）', async () => {
    localStorage.setItem('admin_loggedin', '1')
    vi.spyOn(http, 'post').mockResolvedValue({ code: 1 } as any)
    const routerMod = (await import('@/router')).default as any
    const prev = routerMod.currentRoute.value
    routerMod.currentRoute.value = { path: '/apply/open', query: { company: 'langhui' } }
    try {
      const p = respFulfilled()({ data: { code: 401, msg: 'x' }, config: {} })
      await expect(p).rejects.toThrow()
      expect(routerReplace).toHaveBeenCalledWith({ path: '/login', query: { company: 'langhui' } })
    } finally {
      routerMod.currentRoute.value = prev
    }
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

  it('code=401 刷新请求自身（__skipRefresh）：静默 reject，不递归刷新也不弹提示', async () => {
    // 刷新端点自身返回 401 时：既不重复刷新，也不清场/跳转/弹提示——
    // 发起刷新的那个原请求会自己走 clearSession + 「登录已失效」。
    // 两边都处理的结果是一次失效弹两条（「刷新令牌无效」+「未登录」）。
    const postSpy = vi.spyOn(http, 'post')
    localStorage.setItem('admin_user', 'admin')
    const p = respFulfilled()({
      data: { code: 401, msg: '刷新令牌无效' },
      config: { __skipRefresh: true },
    })
    await expect(p).rejects.toThrow('刷新令牌无效')
    expect(postSpy).not.toHaveBeenCalled()
    expect(localStorage.getItem('admin_user')).toBe('admin')
    expect(elMessage.error).not.toHaveBeenCalled()
    expect(routerReplace).not.toHaveBeenCalled()
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

// 网络层错误（HTTP 非 2xx）。
//
// 背景：业务失败走的是 HTTP 200 + 信封里的 code，但有一类错误走的是真 HTTP 状态码
// ——典型就是安装向导的同源检查（403）。2026-10-28 线上：反代没透传 Host，
// 「测试连接」返回 403，后端响应体里已经写了「反代没透传 Host，加
// proxy_set_header Host $host」，但拦截器把这个 body 整个丢掉，界面只显示 axios 的
// "Request failed with status code 403"，运维完全无从下手。
describe('request 响应拦截：HTTP 非 2xx 时的错误文案', () => {
  beforeEach(() => {
    elMessage.error.mockClear()
    vi.restoreAllMocks()
  })

  /** 取出已注册的响应拦截 rejected（axios 把 handlers 存成 [fulfilled, rejected]） */
  function respRejected() {
    return (http.interceptors.response as any).handlers[0].rejected
  }

  /** 造一个 axios 风格的错误：有 response.data 信封 */
  function httpError(status: number, data: any, config: any = {}) {
    return { isAxiosError: true, message: `Request failed with status code ${status}`, response: { status, data }, config }
  }

  it('HTTP 403 带后端 msg：抛出的错误就是后端那句话，不再出现 axios 英文', async () => {
    const backendMsg =
      '跨源请求被拒绝：浏览器来源 card.example.com，后端收到的 Host 是 127.0.0.1:8080'
    const err = await respRejected()(httpError(403, { code: 403, msg: backendMsg })).catch(
      (e: any) => e
    )

    expect(err.message).toBe(backendMsg)
    expect(err.message).not.toMatch(/status code/)
    // 状态码与原始 response 不能丢：调用方可能按 HTTP 状态分支
    expect(err.status).toBe(403)
    expect(err.code).toBe(403)
    expect(err.response.status).toBe(403)
    // 非 __silent 时也要弹出来（这条以前弹的是「网络异常」，等于什么也没说）
    expect(elMessage.error).toHaveBeenCalledWith(backendMsg)
  })

  it('HTTP 非 2xx 但 __silent：不弹 toast，但错误文案仍然后端优先', async () => {
    const backendMsg = '数据库连接失败：Access denied for user'
    const err = await respRejected()(
      httpError(500, { code: 1, msg: backendMsg }, { __silent: true })
    ).catch((e: any) => e)

    expect(err.message).toBe(backendMsg)
    expect(elMessage.error).not.toHaveBeenCalled()
  })

  it('响应体不是信封（如 nginx 的 HTML 错误页）：不崩，回落「网络异常」', async () => {
    const raw = httpError(502, '<html><body>502 Bad Gateway</body></html>')
    const err = await respRejected()(raw).catch((e: any) => e)

    // 拿不到 msg 就原样抛出 axios 错误，至少保留 status / response 供排障
    expect(err).toBe(raw)
    expect(elMessage.error).toHaveBeenCalledWith('网络异常，请稍后重试')
  })
})

// 平台超管「当前管理的企业」由拦截器统一透传成 ?companyId=。
//
// 为什么必须收在一处：后端对平台超管的缺省行为是「不按企业过滤」（统计/审核）或
// 「回落 1 号企业」（企业内容）。逐个 api 函数加参数必漏，而漏了不报错——
// 页面会静默显示别家企业的数据，最坏的一种是「改着 A 家写进 B 家」。
describe('request 请求拦截：当前企业自动透传', () => {
  beforeEach(() => {
    setCompanyScope(0, false)
  })

  it('平台超管已选企业：/admin/** 自动带 companyId', () => {
    setCompanyScope(3, true)
    const cfg = reqFulfilled()({ url: '/admin/categories', params: { page: 1 } })
    expect(cfg.params).toEqual({ page: 1, companyId: 3 })
  })

  it('原本没有 params：也要补上', () => {
    setCompanyScope(7, true)
    const cfg = reqFulfilled()({ url: '/admin/company-info' })
    expect(cfg.params).toEqual({ companyId: 7 })
  })

  // 企业管理员 / 平台超管未选企业：一个参数都不加，由后端按身份自行收窄
  it('作用域未激活：不加任何参数', () => {
    setCompanyScope(0, false)
    const cfg = reqFulfilled()({ url: '/admin/categories' })
    expect(cfg.params).toBeUndefined()
  })

  // 公开接口与安装向导不吃 companyId，不该被塞进一个来路不明的查询参数
  it('非 /admin 前缀：不加参数', () => {
    setCompanyScope(3, true)
    for (const url of ['/card', '/achievements', '/refresh', '/setup/status']) {
      expect(reqFulfilled()({ url }).params).toBeUndefined()
    }
  })

  // 调用方显式传了 companyId（如按企业查存储用量）就以调用方为准
  it('调用方已显式指定 companyId：不覆盖', () => {
    setCompanyScope(3, true)
    const cfg = reqFulfilled()({ url: '/admin/companies/9/storage', params: { companyId: 9 } })
    expect(cfg.params).toEqual({ companyId: 9 })
  })
})
