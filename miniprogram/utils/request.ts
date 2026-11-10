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
import { SupersededError, type RequestSignal } from './latest'

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

// 裸请求（不经过本封装，避免递归）：用 refresh 令牌换新的 access。
// 刻意**不接受 signal**：刷新是全局单飞，一个页面切走就把刷新 abort 掉，
// 会把同时 401 的其它页面一起拖下水；被取消的查询在 refreshing.then 里自行退场。
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

// method 支持 GET/POST/PUT；GET 的 data 会拼成 query string。
//
// 第四个参数 signal 是**可选**的取消信号（见 utils/latest.ts）：不传时行为与从前完全一致，
// 全站几十个调用点一个都不用改；只有会连续重查的页面才传，用来取消在途的旧查询。
//
// 泛型 R 是**整个响应信封**，默认 ApiResult<any>（不传时与改动前等价）：
//   - 标准 data 数组/对象接口：`request<ApiResult<T>>(...)` 或 `request()` 不传；
//   - 字段在**根级**的接口（/api/card、/api/achievements、/api/home-layout）：
//     用 utils/types.ts 里的 CardResp / AchievementsResp / HomeLayoutResp。
// 上一版泛型是 `T = data 类型`，全站零使用、通道空转；改成信封后才撑得住根级字段。
export function request<R = ApiResult<any>>(
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  data?: any,
  signal?: RequestSignal
): Promise<R> {
  return doRequest<R>(method, path, data, wx.getStorageSync('empToken'), false, signal)
}

function doRequest<R = ApiResult<any>>(
  method: 'GET' | 'POST' | 'PUT',
  path: string,
  data: any,
  token: string,
  retried: boolean,
  signal?: RequestSignal
): Promise<R> {
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
    const task = wx.request({
      url,
      method,
      data: body,
      header: {
        'content-type': 'application/json',
        ...(token ? { Authorization: 'Bearer ' + token } : {}),
      },
      success: (res) => {
        // 被后一次查询取消：abort() 可能来不及阻止回调（尤其同步应答的桩），
        // 这里再挡一道，旧响应绝不能进调用方的成功分支。
        if (signal?.aborted) {
          reject(new SupersededError())
          return
        }
        // 运行时只保证是信封形状（下方 badStatus / notEnvelope 校验）；
        // 具体字段由调用点用泛型 R 声明，这里不做校验也不做转换。
        const result = res.data as ApiResult<any>
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
            // 刷新链跨多轮微任务，期间页面可能已经发了新查询。
            // 不在这里挡，就会为一次已被取消的旧查询再发一个请求。
            if (signal?.aborted) {
              reject(new SupersededError())
              return
            }
            if (newToken) {
              doRequest<R>(method, path, data, newToken, true, signal).then(resolve, reject)
            } else {
              clearAuth()
              resolve(result as unknown as R)
            }
          })
          return
        }
        if (result && result.code === 401) {
          // 已重试仍 401：彻底清理
          clearAuth()
        }
        resolve(result as unknown as R)
      },
      fail: (err) => {
        // 真 wx.request 的 abort() 会触发 fail("request:fail abort")：
        // 这是主动取消，不能当成网络错误把页面打成错误态。
        if (signal?.aborted) {
          reject(new SupersededError())
          return
        }
        reject(err)
      },
    })
    // RequestTask.abort() 才能真正断掉在途请求，省下弱网下的带宽。
    // 单测桩的 wx.request 通常返回 undefined，故一律可选判断。
    if (signal) {
      signal.task = task
      // 同步应答的桩在拿到 task 之前就跑完了 success，那时候 abort 还没发；
      // 若此刻已被取消就补一次（真环境异步，next() 里已经发过，这里不会重复生效）。
      if (signal.aborted && task && typeof task.abort === 'function') {
        try {
          task.abort()
        } catch {
          // 请求已结束，忽略
        }
      }
    }
  })
}
