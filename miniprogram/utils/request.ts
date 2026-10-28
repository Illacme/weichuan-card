// 统一的 API 请求封装：自动注入员工 token、统一错误码处理。
// 401 时先用 refresh 令牌静默换发新 access 并重试原请求一次（单飞锁），
// 无 refreshToken 或刷新失败则清 token 由调用方引导重新登录。
//
// 多租户（见 utils/tenant.ts）：企业内容类 GET 与自助申请 POST 自动带上
// companyId——企业独立版恒带锁定值，平台模式带扫码/分享粘滞值，
// 都没有则不传（后端缺省落 1 号租户朗汇）。/api/card 按员工归属企业取，
// /api/employee/*、/api/wechat/*、登录刷新等与租户无关，一律不注入。
import { getCompanyId } from './tenant'
import { DEFAULT_BASE_URL } from './config'

const app = getApp() as any

// 基址优先取 globalData（冷启动已由后台下发的 miniprogram_base_url 覆盖，见 utils/config.ts）；
// 拿不到（如单测环境 / 未初始化）时回落到内置引导种子，不再在请求层散落写死地址。
function baseUrl(): string {
  return (app && app.globalData && app.globalData.baseUrl) || DEFAULT_BASE_URL
}

// apiBaseUrl 暴露给文件上传等需要直接拼 URL 的场景（如 wx.uploadFile）
export function apiBaseUrl(): string {
  return baseUrl()
}

// assetUrl 把后端返回的资源路径统一修正为「当前端可访问的绝对 URL」。
// 后端本地存储返回相对路径（/uploads/...）或库内相对（/api/assets/file/...）：
// 浏览器能按页面源解析，但小程序 <image src> / canvas img.src 不认相对路径，
// 必须拼上 baseUrl 才能加载——否则手机端图片裂图。
// 云存储（MinIO/OSS）返回的是绝对 CDN URL，原样返回即可（already absolute）。
export function assetUrl(p?: string | null): string {
  if (!p) return ''
  if (/^https?:\/\//i.test(p)) return p
  return baseUrl() + (p.charAt(0) === '/' ? '' : '/') + p
}

export interface ApiResult<T> {
  code: number
  msg?: string
  data?: T
  // 登录接口在根级返回 token / refreshToken（{ code:0, token, refreshToken }）
  token?: string
  refreshToken?: string
}

// 单飞锁：并发 401 只触发一次刷新，其余复用同一次结果
let refreshing: Promise<string | null> | null = null

// 裸请求（不经过本封装，避免递归）：用 refresh 令牌换新的 access
function rawRefresh(refreshToken: string): Promise<ApiResult<any>> {
  return new Promise((resolve, reject) => {
    wx.request({
      url: baseUrl() + '/api/refresh',
      method: 'POST',
      data: { refreshToken },
      header: { 'content-type': 'application/json' },
      success: (res) => resolve(res.data as ApiResult<any>),
      fail: (err) => reject(err),
    })
  })
}

async function tryRefresh(): Promise<string | null> {
  const refreshToken = wx.getStorageSync('empRefreshToken')
  if (!refreshToken) return null
  try {
    const r = await rawRefresh(refreshToken)
    if (r && r.code === 0 && r.token) {
      wx.setStorageSync('empToken', r.token)
      return r.token
    }
  } catch {
    // 刷新失败：按失效处理
  }
  return null
}

function clearAuth() {
  wx.removeStorageSync('empToken')
  wx.removeStorageSync('empRefreshToken')
}

// 需要带 companyId 的内容类 GET 前缀（业绩/服务/项目/布局/企业公开信息/名片墙）。
// 白名单式而非黑名单式：新增与租户无关的公开端点时不会意外带上参数。
function tenantAwarePath(path: string): boolean {
  return (
    path.indexOf('/api/achievements') === 0 ||
    path.indexOf('/api/services') === 0 ||
    path.indexOf('/api/projects') === 0 ||
    path.indexOf('/api/home-layout') === 0 ||
    path.indexOf('/api/company') === 0 ||
    // 名片墙：漏了它的话，平台模式下访客会把「平台缺省企业（1 号）」的
    // 全员名单当成眼前这家公司的——一头雾水，而且看不出错在哪。
    path.indexOf('/api/team') === 0
  )
}

// 按租户语义为请求补 companyId（>0 才补；0 = 不传，走后端缺省）。
// 独立版恒为锁定值；平台模式为粘滞值；申请提交按 body 归属租户。
function withCompanyId(method: 'GET' | 'POST' | 'PUT', path: string, data: any): any {
  const isApply = path.indexOf('/api/apply/open') === 0
  if (!(method === 'GET' && tenantAwarePath(path)) && !(method !== 'GET' && isApply)) {
    return data
  }
  const cid = getCompanyId()
  if (cid <= 0) return data
  return { ...(data || {}), companyId: cid }
}

// method 支持 GET/POST/PUT；GET 的 data 会拼成 query string
export function request<T = any>(method: 'GET' | 'POST' | 'PUT', path: string, data?: any): Promise<ApiResult<T>> {
  return doRequest<T>(method, path, data, wx.getStorageSync('empToken'), false)
}

function doRequest<T>(
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  data: any,
  token: string,
  retried: boolean
): Promise<ApiResult<T>> {
  return new Promise((resolve, reject) => {
    data = withCompanyId(method, path, data)
    let url = baseUrl() + path
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
        // 网关/反代兜底页（nginx 404/502 的 HTML）不是接口信封：resolve 出去会让
        // 调用方把「服务不可达」误判成业务态（如员工不存在 → 引导用户改编号，
        // 真实原因却是后端没通）。统一按网络错误 reject，让页面走 error 态。
        // 后端约定业务错误恒为 HTTP 200 + {code}（401/403 也在 200 信封里），
        // 因此 statusCode>=400 或响应体不是对象都只可能来自代理/网关。
        // statusCode 不判定 undefined（单测桩不传该字段，缺省视为 200）。
        const badStatus = typeof res.statusCode === 'number' && res.statusCode >= 400
        const notEnvelope = typeof result !== 'object' || result === null
        if (badStatus || notEnvelope) {
          reject(new Error(`接口不可用（HTTP ${res.statusCode ?? '-'}）`))
          return
        }
        if (result && result.code === 401 && !retried) {
          // 先用 refresh 令牌换新 access，再重试一次
          if (!refreshing) {
            refreshing = tryRefresh().finally(() => {
              refreshing = null
            })
          }
          refreshing.then((newToken) => {
            if (newToken) {
              doRequest<T>(method, path, data, newToken, true).then(resolve, reject)
            } else {
              clearAuth()
              resolve(result)
            }
          })
          return
        }
        if (result && result.code === 401) {
          // 已重试仍 401：彻底清理
          clearAuth()
        }
        resolve(result)
      },
      fail: (err) => reject(err),
    })
  })
}
