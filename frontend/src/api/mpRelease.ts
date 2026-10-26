import http, { toPaged, type Paged } from './request'

// 小程序版本发布（平台超管代租户上传 / 提审 / 发布）。
//
// 与后端 internal/handler/mp_release.go 对应。上传代码有两条路：
//   1) CI 用 miniprogram-ci 传，后台只登记版本号（createMPRelease）；
//   2) 后台自己传（uploadMPRelease）——服务器上放着小程序源码 + Node +
//      miniprogram-ci，不需要任何人装微信开发者工具。
// 之后都是：提交审核 → 查审核状态 → 发布 → 撤回审核。
// 状态机与后端一致（model/mp_release.go 的 canTransition 白名单），
// 界面按状态显隐按钮只是「少让人点错」，真正的拦截在服务端。

export type MPReleaseStatus = 'draft' | 'auditing' | 'audit_passed' | 'released' | 'rejected' | 'withdrawn'

export interface MPReleaseItem {
  id: number
  companyId: number
  /** 登记时用的小程序 AppID（动作时重新解析凭据并比对，不一致会被拒） */
  appId: string
  userVersion: string
  userDesc: string
  status: MPReleaseStatus
  /** 后端给的中文状态名，前端不再维护一份字典 */
  statusName: string
  /** 微信审核单 id；未提审为 0 */
  auditId: number
  /** 驳回原因（微信原文）；重新提审后清空 */
  auditReason: string
  source: string
  operatorId: number
  /** 三个时间戳可空：没发生的动作是 null，不是 1970 哨兵值 */
  submittedAt: string | null
  releasedAt: string | null
  createdAt: string
  updatedAt: string
}

/** 查审核状态回包：记录快照 + 微信原始枚举 + 本次是否发生了状态变化 */
export interface MPReleaseAuditStatus {
  item: MPReleaseItem
  /** 微信 status：0 通过 / 1 拒绝 / 2 审核中 / 3 已撤回 / 4 延后 */
  wxStatus: number
  changed: boolean
}

/** 上传前探活回包：环境缺什么、钥匙配没配 */
export interface MPReleaseUploadReadiness {
  ready: boolean
  /** 缺什么就列什么，一次列全（后端刻意不「遇到第一个就返回」） */
  problems: string[]
  /** 只回「配没配」，永不明文回传密钥 */
  keyConfigured: boolean
}

// 某企业的版本发布记录（服务端分页 + 可选状态筛选）
export function listMPReleases(
  companyId: number,
  params: { page?: number; size?: number; status?: string } = {}
): Promise<Paged<MPReleaseItem>> {
  return http
    .get('/admin/companies/' + companyId + '/releases', { params })
    .then((r) => toPaged<MPReleaseItem>(r))
}

// 上传前探活。打开上传弹窗时调一次，把「缺 node / 没装 miniprogram-ci /
// 源码目录不存在 / 没配密钥」直接摆出来——点完上传才说不行最劝退。
// 与 getMPReleaseAuditStatus 一样在 api 层就把 data 拆出来，调用方不必记着隔一层。
export function getMPReleaseUploadReadiness(companyId: number): Promise<MPReleaseUploadReadiness> {
  return http
    .get<{ code: number; data?: MPReleaseUploadReadiness }>(
      '/admin/companies/' + companyId + '/releases/upload-readiness'
    )
    .then((r) => r.data ?? { ready: false, problems: [], keyConfigured: false })
}

// 上传代码到微信（成为体验版），成功后自动建一条 draft 记录。
// **同步执行、可能耗时数十秒到数分钟**（编译 + 上传），调用方要锁死按钮并提示。
export function uploadMPRelease(
  companyId: number,
  payload: { userVersion: string; userDesc?: string }
) {
  return http.post<{ code: number; data?: MPReleaseItem; msg?: string }>(
    '/admin/companies/' + companyId + '/releases/upload',
    payload
  )
}

// 登记一个版本 → draft。用于「CI 已经传过了，我手填版本号」。
// userVersion 必须与实际上传时填的一致（后台无法验证，写错会在提审时被微信发现）。
export function createMPRelease(companyId: number, payload: { userVersion: string; userDesc?: string }) {
  return http.post<{ code: number; data?: MPReleaseItem }>(
    '/admin/companies/' + companyId + '/releases',
    payload
  )
}

// 提交审核（draft / rejected / withdrawn → auditing）。**消耗微信审核额度**，
// 失败不扣——但也不该拿它当重试按钮使。
export function submitMPReleaseAudit(id: number) {
  return http.post<{ code: number; data?: MPReleaseItem }>('/admin/releases/' + id + '/submit-audit')
}

// 查审核状态：终局（通过 / 驳回 / 已撤回）会顺带同步进本地记录。
export function getMPReleaseAuditStatus(id: number) {
  return http
    .get<{ code: number; data?: Record<string, any> }>('/admin/releases/' + id + '/audit-status')
    .then((r) => ({
      item: (r.data ?? {}) as unknown as MPReleaseItem,
      wxStatus: Number(r.data?.wxStatus ?? -1),
      changed: r.data?.changed === true,
    }))
}

// 发布（audit_passed → released）。**直接改变租户小程序的线上行为**，需二次确认。
export function releaseMPRelease(id: number) {
  return http.post<{ code: number; data?: MPReleaseItem }>('/admin/releases/' + id + '/release')
}

// 撤回审核（auditing → withdrawn）。**不返还已消耗的审核额度**，
// 后端要求 confirm=true，前端同样必须先弹二次确认。
export function undoMPReleaseAudit(id: number) {
  return http.post<{ code: number; data?: MPReleaseItem }>('/admin/releases/' + id + '/undo-audit', {
    confirm: true,
  })
}

// 状态 → 标签色（element-plus tag type）。终态用灰色，进行中用主色，
// 出问题的（驳回）用危险色——扫一眼表格就知道哪版卡住了。
export function mpReleaseTagType(status: MPReleaseStatus): 'primary' | 'success' | 'warning' | 'info' | 'danger' {
  switch (status) {
    case 'auditing':
      return 'primary'
    case 'audit_passed':
      return 'warning'
    case 'released':
      return 'success'
    case 'rejected':
      return 'danger'
    default:
      return 'info' // draft / withdrawn
  }
}
