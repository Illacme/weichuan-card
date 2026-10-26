import http, { toPaged, type Paged, type ApiResp } from './request'

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
