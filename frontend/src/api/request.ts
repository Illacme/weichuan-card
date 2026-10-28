import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosResponse,
} from 'axios'
import router from '@/router'
import { ElMessage } from 'element-plus'
import { setLastRequestId } from './clientError'
import { getCompanyScope } from '@/store/companyScope'
import { useAuthStore } from '@/store/auth'

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

// 平台超管「当前管理的企业」自动透传：给 /admin/** 请求补 ?companyId=。
//
// 后端对平台超管的缺省行为是「不按企业过滤」（统计/审核）或「回落 1 号企业」（企业内容），
// 这两种在「切换企业」的语境下都是错的：前者把别家数据混进来，后者更糟——**改着 B 企业的
// 资料实际写进了 1 号企业**。逐个 api 函数加参数必漏（漏了不报错，只是静默串数据），
// 所以收在一处注入。
//
// 只对 `/admin/` 前缀生效：公开接口与安装向导不吃 companyId，不给他们添噪。
// 调用方显式传了 companyId（如 getStorageUsage(companyId)）时不覆盖。
http.interceptors.request.use((cfg) => {
  const { companyId, active } = getCompanyScope()
  if (!active || !cfg.url || !cfg.url.startsWith('/admin/')) return cfg
  const params = (cfg.params ?? {}) as Record<string, unknown>
  if (params.companyId != null) return cfg
  cfg.params = { ...params, companyId }
  return cfg
})

// 并发 401 时的单飞锁：多个请求同时 401 只触发一次刷新，其余复用同一次结果
let refreshing: Promise<boolean> | null = null

// 清除非敏感的登录态标记（令牌在 httpOnly Cookie 中，JS 无需/无法清理）
function clearSession() {
  localStorage.removeItem('admin_user')
  localStorage.removeItem('admin_loggedin')
  // 内存里的登录态必须一起清。只清 localStorage 时路由守卫仍然认为「已登录」，
  // 接着的 replace('/login') 会被守卫弹回后台——界面看起来一切正常（用户名、
  // 身份都在），实际上每个请求都在 401，用户看到的是「已登录」和「未登录」
  // 两条提示并存的荒谬画面。
  try {
    useAuthStore().logout()
  } catch {
    // pinia 尚未激活（安装页 / 单测直接拿 http 实例）：localStorage 已清，够了
  }
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
      // 刷新请求自身的 401：静默 reject，由发起刷新的一方统一提示。
      // 不静默的话，一次令牌失效会连弹两条（「刷新令牌无效」+「未登录」）——
      // 两句都是用户看不懂的内部术语，还挡住了界面。
      if (cfg && cfg.__skipRefresh) {
        return Promise.reject(new Error(data.msg || '未登录'))
      }
      if (cfg && !cfg.__retried) {
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
      // 不用后端的 msg：401 的文案在服务端是「未登录」「刷新令牌无效」这类内部说法，
      // 而此刻用户刚刚还在用，说「未登录」只会让人怀疑系统坏了。
      ElMessage.error('登录已失效，请重新登录')
      if (router.currentRoute.value.path !== '/login') {
        // 保留白标码：会话过期被打回登录页时，登录页品牌仍应是当前企业的，
        // 否则客户会以为被踢到了别家后台（与路由守卫的白标透传保持一致）。
        const company = router.currentRoute.value.query?.company
        if (typeof company === 'string' && company) {
          router.replace({ path: '/login', query: { company } })
        } else {
          router.replace('/login')
        }
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
