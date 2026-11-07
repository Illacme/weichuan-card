// 小程序端「企业管理员」的独立请求通道。
//
// 为什么不复用 utils/request.ts 的 request()：它永远只注入 empToken（员工令牌），
// 401 时也只认 empRefreshToken 且只写回 empToken。管理员令牌是另一套身份，
// 混用会导致「员工刷新把管理员令牌冲掉」或「401 时用员工 refresh 去续管理员令牌」。
//
// 后端六个 /api/admin/apply/* 端点挂的 AuthMiddleware(db,"admin") 本来就接受
// Authorization: Bearer（ExtractToken 优先读头、其次才读 Cookie），所以这里只需要
// 换一个 token 来源，审核接口一行都不用改。
//
// 租户隔离靠 JWT 里的 companyId，故刻意不做 withCompanyId 自动补参——
// 那套白名单是给公开内容接口用的，给 /api/admin/* 补 companyId 毫无意义。
import { apiBaseUrl, type ApiResult } from './request'

export const ADMIN_TOKEN_KEY = 'adminToken'
export const ADMIN_REFRESH_KEY = 'adminRefreshToken'
// 本机是否用微信免密登过管理员：静默免密的准入门槛。
// 只在「免密登录成功」或「密码登录后绑定微信成功」时置位，显式退出管理员登录时清掉。
// 没有这道门槛，纯访客 / 只登过员工的微信每点一次「待审申请」都会打一次登录接口，
// 而它和账号密码登录共用 login:<IP> 限流桶（默认 10 次/分），办公网共享出口下很快 429
// ——真机上表现为「我还没登录，却提示登录尝试过于频繁」。
export const ADMIN_WX_BOUND_KEY = 'adminWxBound'

export function markAdminWxBound(): void {
  wx.setStorageSync(ADMIN_WX_BOUND_KEY, true)
}

export function hasAdminWxBound(): boolean {
  return !!wx.getStorageSync(ADMIN_WX_BOUND_KEY)
}

export function clearAdminWxBound(): void {
  wx.removeStorageSync(ADMIN_WX_BOUND_KEY)
}

// saveAdminAuth 保存登录响应里的令牌。刻意只在真的拿到 token 时返回 true：
// 登录接口业务码非 0 时也带 code/msg，页面若只看 code 就存，会把空令牌写进 Storage，
// 下次打开审核页以为已登录，结果每个请求都 401。
export function saveAdminAuth(r: { token?: string; refreshToken?: string } | null | undefined): boolean {
  if (!r || !r.token) return false
  wx.setStorageSync(ADMIN_TOKEN_KEY, r.token)
  if (r.refreshToken) wx.setStorageSync(ADMIN_REFRESH_KEY, r.refreshToken)
  return true
}

export function hasAdminToken(): boolean {
  return !!wx.getStorageSync(ADMIN_TOKEN_KEY)
}

export function clearAdminAuth(): void {
  wx.removeStorageSync(ADMIN_TOKEN_KEY)
  wx.removeStorageSync(ADMIN_REFRESH_KEY)
}

// 单飞锁：并发 401 只触发一次刷新，其余复用同一次结果。
// 与员工通道分开，避免两条身份链的刷新互相覆盖。
let adminRefreshing: Promise<string | null> | null = null

function rawRefresh(refreshToken: string): Promise<ApiResult<any>> {
  return new Promise((resolve, reject) => {
    wx.request({
      url: apiBaseUrl() + '/api/refresh',
      method: 'POST',
      data: { refreshToken },
      header: { 'content-type': 'application/json' },
      success: (res) => resolve(res.data as ApiResult<any>),
      fail: (err) => reject(err),
    })
  })
}

async function tryRefresh(): Promise<string | null> {
  const refreshToken = wx.getStorageSync(ADMIN_REFRESH_KEY)
  if (!refreshToken) return null
  try {
    const r = await rawRefresh(refreshToken)
    // /api/refresh 的 Body 流只回 {code:0, token, expiresIn}，不轮换 refreshToken
    if (r && r.code === 0 && r.token) {
      wx.setStorageSync(ADMIN_TOKEN_KEY, r.token)
      return r.token
    }
  } catch {
    // 刷新失败按失效处理，由页面引导重新登录
  }
  return null
}

export function adminRequest<T = any>(
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  data?: any
): Promise<ApiResult<T>> {
  return doAdminRequest<T>(method, path, data, wx.getStorageSync(ADMIN_TOKEN_KEY), false)
}

function doAdminRequest<T>(
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  data: any,
  token: string,
  retried: boolean
): Promise<ApiResult<T>> {
  return new Promise((resolve, reject) => {
    let url = apiBaseUrl() + path
    let body: any = data
    if (method === 'GET' && data) {
      const qs = Object.keys(data)
        .filter((k) => data[k] !== undefined && data[k] !== '')
        .map((k) => `${encodeURIComponent(k)}=${encodeURIComponent(data[k])}`)
        .join('&')
      if (qs) url += (url.indexOf('?') >= 0 ? '&' : '?') + qs
      body = undefined
    }
    wx.request({
      url,
      method,
      data: body,
      header: {
        'content-type': 'application/json',
        ...(token ? { Authorization: 'Bearer ' + token } : {}),
      },
      success: (res) => {
        const result = res.data as ApiResult<T>
        // 网关兜底页（nginx 404/502 的 HTML）不是接口信封，统一按网络错误 reject，
        // 否则页面会把「服务不可达」误判成业务态（见 utils/request.ts 同一段注释）。
        const badStatus = typeof res.statusCode === 'number' && res.statusCode >= 400
        const notEnvelope = typeof result !== 'object' || result === null
        if (badStatus || notEnvelope) {
          reject(new Error(`接口不可用（HTTP ${res.statusCode ?? '-'}）`))
          return
        }
        if (result && result.code === 401 && !retried) {
          if (!adminRefreshing) {
            adminRefreshing = tryRefresh().finally(() => {
              adminRefreshing = null
            })
          }
          adminRefreshing.then((newToken) => {
            if (newToken) {
              doAdminRequest<T>(method, path, data, newToken, true).then(resolve, reject)
            } else {
              clearAdminAuth()
              resolve(result)
            }
          })
          return
        }
        if (result && result.code === 401) clearAdminAuth()
        resolve(result)
      },
      fail: (err) => reject(err),
    })
  })
}
