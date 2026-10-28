import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosResponse,
} from 'axios'
import router from '@/router'
import { ElMessage } from 'element-plus'
import { setLastRequestId } from './clientError'

/**
 * 统一响应体约定（见《三端联调与接口总表》§2）：
 *   { code: 0, msg, data?: ... }   成功
 *   { code: 1, msg }               业务失败（参数错误 / 密码错误 / 文件不支持）
 *   { code: 2, msg }               特殊状态（如名片已停用 / 微信凭据未配置）
 * 鉴权失败：后端一律返回 HTTP 200 + { code: 401 | 403, msg }（见 middleware/auth.go），
 * 因此拦截器以 res.code 为准，而非 HTTP 状态码。
 *
 * PC 管理后台为 httpOnly Cookie 鉴权：access / refresh 由浏览器随请求自动携带
 * （withCredentials），JS 全程不接触令牌，防 XSS 窃取。故不再有 Bearer 请求拦截。
 */
export interface ApiResp {
  code: number
  msg?: string
  [key: string]: any
}

/**
 * 服务端分页结果（管理端列表统一形状）。
 *
 * 后端回包信封：{ code: 0, data: [...当前页...], total, page, size }
 * ——data 仍是数组（与未分页时同构），总数/页码在顶层（见 backend/internal/handler/pagination.go）。
 * 分页必须落在服务端：员工/申请/租户这些列表随运营时间单调增长，
 * 前端拉全量再切片只是把问题藏起来（网络照样传了全部数据）。
 */
export interface Paged<T = any> {
  list: T[]
  total: number
  page: number
  size: number
}

/** 把分页接口的响应体收敛成 Paged；后端异常时退化为空列表而不是抛错。 */
export function toPaged<T = any>(r?: ApiResp | null): Paged<T> {
  const list = Array.isArray(r?.data) ? ((r?.data as unknown) as T[]) : []
  return {
    list,
    total: Number(r?.total ?? list.length) || 0,
    page: Number(r?.page ?? 1) || 1,
    size: Number(r?.size ?? list.length ?? 20) || 20,
  }
}

// 业务约定：响应拦截器把成功响应体（ApiResp）直接透传给调用方，
// 故把实例方法的返回类型收敛为 ApiResp（默认泛型参数），调用侧无需再取 .data。
type ApiHttp = Omit<
  AxiosInstance,
  'request' | 'get' | 'post' | 'put' | 'delete'
> & {
  request: <T = ApiResp>(config: AxiosRequestConfig) => Promise<T>
  get: <T = ApiResp>(url: string, config?: AxiosRequestConfig) => Promise<T>
  post: <T = ApiResp>(url: string, data?: unknown, config?: AxiosRequestConfig) => Promise<T>
  put: <T = ApiResp>(url: string, data?: unknown, config?: AxiosRequestConfig) => Promise<T>
  delete: <T = ApiResp>(url: string, config?: AxiosRequestConfig) => Promise<T>
}

const http = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '/api',
  timeout: 15000,
  withCredentials: true, // 携带 httpOnly Cookie（同源部署）
}) as ApiHttp

// 并发 401 时的单飞锁：多个请求同时 401 只触发一次刷新，其余复用同一次结果
let refreshing: Promise<boolean> | null = null

// 清除非敏感的登录态标记（令牌在 httpOnly Cookie 中，JS 无需/无法清理）
function clearSession() {
  localStorage.removeItem('admin_user')
  localStorage.removeItem('admin_loggedin')
}

// 用 refresh Cookie 换发新 access（服务端轮换下发新 Cookie）。__skipRefresh 防止刷新请求
// 自身再触发 401 刷新逻辑（避免递归）。
async function tryRefresh(): Promise<boolean> {
  try {
    const r: any = await http.post('/refresh', {}, { __skipRefresh: true } as AxiosRequestConfig)
    return !!(r && r.code === 0)
  } catch {
    return false
  }
}

// 响应拦截：统一处理 401/403/业务失败，并回传整包（api 函数按需取字段）
http.interceptors.response.use(
  async (resp: AxiosResponse) => {
    const data = resp.data as ApiResp
    if (!data || typeof data !== 'object') {
      return data as unknown as AxiosResponse
    }

    // 把后端回传的 X-Request-Id 记下来，供前端报错上报时带上，
    // 把「这次请求」和后端同一条访问日志关联起来（见 ./clientError.ts）。
    // 这里必须用可选链：真实 axios 一定有 headers，但测试替身 / 自定义 adapter 返回的
    // 响应可能没有——为了让「记个请求号」崩掉整个响应拦截（进而把所有业务错误都变成
    // 一句「Cannot read properties of undefined」）是不值当的。
    const rid = resp.headers?.['x-request-id'] || resp.headers?.['X-Request-Id']
    if (rid) setLastRequestId(rid as string)

    // 401：access 过期 / 登录失效 → 先用 refresh Cookie 静默换发并重试原请求一次；
    //      刷新失败再清登录态跳登录。已重试过（__retried）或刷新请求自身（__skipRefresh）不再刷新。
    if (data.code === 401) {
      const cfg = resp.config as any
      if (cfg && !cfg.__skipRefresh && !cfg.__retried) {
        if (!refreshing) {
          refreshing = tryRefresh().finally(() => {
            refreshing = null
          })
        }
        const ok = await refreshing
        if (ok) {
          cfg.__retried = true
          return http.request(cfg)
        }
      }
      clearSession()
      ElMessage.error(data.msg || '登录失效，请重新登录')
      if (router.currentRoute.value.path !== '/login') {
        router.replace('/login')
      }
      return Promise.reject(new Error(data.msg || '未登录'))
    }
    // 403：角色不符（本后台仅超管，正常不应出现）
    if (data.code === 403) {
      ElMessage.warning(data.msg || '无权限')
      return Promise.reject(new Error(data.msg || '无权限'))
    }
    // 业务失败（code=1 及其他非 0）。__silent：调用方自行降级（如预览弹窗拉小程序码，
    // 未配微信凭据时 code=1 属预期，不该弹红条惊动运营）——仍然 reject，由调用方 catch。
    if (data.code !== 0) {
      const cfg0 = resp.config as any
      if (!cfg0 || !cfg0.__silent) ElMessage.error(data.msg || '请求失败')
      return Promise.reject(new Error(data.msg || '业务失败'))
    }
    // 成功：回传响应体，由各 api 函数提取 data / staffId 等
    return data as unknown as AxiosResponse
  },
  (error) => {
    // HTTP 非 2xx（403 / 500 / 404…）时后端同样回 { code, msg } 信封，必须把 msg
    // 透出来。丢掉它，用户只会看到 axios 的英文原文
    // （"Request failed with status code 403"）——安装向导第一步 403 就是这么被
    // 糊住的：后端已经说清「反代没透传 Host，加 proxy_set_header Host $host」，
    // 界面却在说天书，运维只能干瞪眼。
    const body = error && error.response ? error.response.data : null
    const backendMsg =
      body && typeof body === 'object' && typeof (body as ApiResp).msg === 'string'
        ? (body as ApiResp).msg
        : ''
    // __silent 同样放过网络层失败（同上：调用方自行降级）
    const cfg = (error && (error as any).config) as any
    if (!cfg || !cfg.__silent) ElMessage.error(backendMsg || '网络异常，请稍后重试')
    if (backendMsg) {
      // 用后端那句话替换 axios 的英文，并把状态码/原始 response 挂回去 ——
      // 调用方若需要按 HTTP 状态分支（如 401 跳登录），信息一点没少。
      const e = new Error(backendMsg) as any
      e.code = (body as ApiResp).code
      e.status = error.response.status
      e.response = error.response
      return Promise.reject(e)
    }
    return Promise.reject(error)
  }
)

export default http
