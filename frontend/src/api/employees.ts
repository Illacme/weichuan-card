import http, { toPaged, type Paged, type ApiResp } from './request'
import { getCompanyScope } from '@/store/companyScope'

export interface EmployeePayload {
  name: string
  phone: string
  // 第二个手机号（选填）：名片「手机」下方并列展示，不参与登录
  phone2?: string
  email?: string
  title?: string
  landline?: string
  avatar?: string
}

// 员工列表（服务端分页）。
// keyword 在服务端匹配 姓名/手机/手机2/职位/座机/昵称/openid，与后台搜索框的范围一致；
// staffIds=1,2,3 用于按 id 精确取少数几条（名片修改申请页回显员工原值）。
export function listEmployees(
  params: { page?: number; size?: number; keyword?: string; status?: string; staffIds?: string } = {}
): Promise<Paged<any>> {
  return http.get('/admin/employees', { params }).then((r) => toPaged(r))
}

// 新增员工 → 返回 staffId + 一次性初始密码
export function createEmployee(payload: EmployeePayload) {
  return http.post('/admin/employees', payload).then((r: any) => ({
    staffId: r.staffId as number,
    initialPassword: r.initialPassword as string,
  }))
}

// 超管编辑员工资料（即时生效，不经过员工端审核流）
export function updateEmployee(id: number, payload: EmployeePayload) {
  return http.put(`/admin/employees/${id}`, payload)
}

export function disableEmployee(id: number) {
  return http.post(`/admin/employees/${id}/disable`)
}

export function enableEmployee(id: number) {
  return http.post(`/admin/employees/${id}/enable`)
}

// 重置密码 → 返回一次性初始密码
export function resetPassword(id: number) {
  return http.post(`/admin/employees/${id}/reset-password`).then((r: any) => r.initialPassword as string)
}

// 按员工导出个人信息（PIPL 查阅复制权协助义务）：拉取该员工完整 PII 的 JSON 文件并下载。
// 走统一 http 封装——后端回 {code,data:员工记录} 信封，拦截器已在 code!=0 时弹错并 reject，
// 这里只需在成功分支把 data（员工记录；已不含 json:"-" 标注的密码哈希等敏感字段）落盘为 .json 文件。
// 按租户隔离在服务端保证（scopeCond），此处只管下载。
export function exportEmployee(id: number) {
  return http.get(`/admin/employees/${id}/export`).then((r: ApiResp) => {
    const json = JSON.stringify(r.data, null, 2)
    const blob = new Blob([json], { type: 'application/json;charset=utf-8' })
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `employee-${id}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(url)
  })
}

// 批量导出员工（CSV，按当前企业范围）。后端直接返回 text/csv（带 BOM），
// 不走 JSON 信封——统一 http 封装的响应拦截会按 code 解析失败，故改用 fetch
// + credentials（同源携带 httpOnly Cookie），拿到 blob 后按 Content-Type 决定下载还是报错。
// 失败（如超管令牌失效被后端以 JSON 信封回 {code:1,msg}）时取 msg 提示，不静默吞掉。
// 企业范围与列表页一致：平台超管「在管企业」模式下补 ?companyId=，否则导出全部租户。
export async function exportEmployees(params: { keyword?: string } = {}): Promise<void> {
  const base = import.meta.env.VITE_API_BASE || '/api'
  const qs = new URLSearchParams()
  const { companyId, active } = getCompanyScope()
  if (active && companyId) qs.set('companyId', String(companyId))
  if (params.keyword) qs.set('keyword', params.keyword)
  const q = qs.toString()
  const url = `${base}/admin/employees/export${q ? '?' + q : ''}`
  const resp = await fetch(url, { method: 'GET', credentials: 'include' })
  const ct = resp.headers.get('Content-Type') || ''
  if (!resp.ok || !ct.includes('text/csv')) {
    let msg = '导出失败'
    try {
      const j = (await resp.json()) as ApiResp
      if (j && j.msg) msg = j.msg
    } catch {
      /* 非 JSON，用默认文案 */
    }
    throw new Error(msg)
  }
  const blob = await resp.blob()
  const durl = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.download = '员工数据_全部.csv'
  const disp = resp.headers.get('Content-Disposition') || ''
  const m = /filename\*=UTF-8''([^;]+)/i.exec(disp) || /filename="?([^";]+)"?/i.exec(disp)
  if (m) a.download = decodeURIComponent(m[1])
  a.href = durl
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(durl)
}
