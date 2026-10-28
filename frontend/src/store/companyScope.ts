/**
 * 后台「当前正在管理的企业」——请求级租户作用域。
 *
 * 为什么是这样一个只有两个变量的叶子模块，而不是直接读 Pinia store：
 * `api/request.ts` 的请求拦截器需要它，而 store 又要 import `api/tenants.ts` 去拉企业列表，
 * request 再 import store 就成环（store → api → request → store）。
 * 这个文件不 import 任何东西，谁都能安全引用。
 *
 * 语义只有一条：
 *   - `active = true` 且 `companyId > 0` → 给 `/admin/**` 请求自动带上 `?companyId=`；
 *   - 其余情况（企业管理员 / 平台超管还没选企业）→ 一个参数都不加，后端按身份自行收窄。
 *
 * 为什么要「自动加」而不是每个 api 函数加个参数：企业内容有 6 个页面、审核 2 个、
 * 统计 3 个端点、员工 5 个操作，逐个传参会漏——漏了不报错，页面会静默展示**别的企业**
 * 的数据（后端对平台超管的缺省行为是「不按企业过滤」或「回落 1 号企业」）。
 * 一处注入，遗漏面从「N 个调用点」收敛成「1 个拦截器」。
 */
let active = false
let companyId = 0

/** 更新作用域（由 tenant store 在身份变化 / 切换企业时调用）。 */
export function setCompanyScope(id: number, enabled: boolean) {
  companyId = enabled && id > 0 ? id : 0
  active = enabled && companyId > 0
}

/** 读取当前作用域。companyId 为 0 表示「不注入」。 */
export function getCompanyScope(): { companyId: number; active: boolean } {
  return { companyId, active }
}
