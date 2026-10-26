import http, { toPaged, type Paged } from './request'

// 图片内容安全检测记录。
//
// 微信的图片检测只有**异步**接口：上传时送出去只拿到一个 trace_id，
// 判定结果由「消息推送」在几秒到几十秒后推回来。所以这张表里同时存在
// 「已送检但还没结论」（pending）和「已有结论」（pass / review / risky / failed）。
export interface MediaCheck {
  id: number
  companyId: number
  url: string // 送检时的绝对地址，也是下架时覆盖的对象定位依据
  objectKey: string // 存储对象键；为空表示推不出来，不允许下架
  mediaType: number // 1 音频 / 2 图片
  scene: number
  source: string // employee / admin / apply
  traceId: string // 微信侧追踪号（申诉与排障都要它）
  status: MediaCheckStatus
  suggest: string // 微信原始 suggest
  label: number // 违规标签
  raw: string // 微信回调原文（申诉证据）
  note: string // 跳过 / 失败原因
  action: string // '' 未处置 / takedown 已下架
  handledAt: string | null
  handlerId: number
  createdAt: string
  updatedAt: string
}

export type MediaCheckStatus = 'pending' | 'pass' | 'review' | 'risky' | 'failed'

// 后台要显示「要让图片检测跑起来还缺什么」，所以除了列表还要这份配置状态。
export interface MediaCheckConfig {
  enabled: boolean
  tokenReady: boolean // 消息推送 Token（验签用，必填）
  aesReady: boolean // EncodingAESKey（安全模式才需要）
  credReady: boolean // 小程序 AppID / AppSecret
  pushUrl: string // 要填到微信后台的推送地址
  pending: number
  risky: number
}

export const STATUS_LABELS: Record<MediaCheckStatus, string> = {
  pending: '待结论',
  pass: '通过',
  review: '待复核',
  risky: '违规',
  failed: '检测失败',
}

// 状态标签的颜色：违规红、待结论黄、通过绿、失败灰。
// 与小程序端的红涨绿跌无关——这里是「风险等级」，不是行情。
export const STATUS_TAGS: Record<MediaCheckStatus, 'danger' | 'warning' | 'success' | 'info'> = {
  pending: 'warning',
  pass: 'success',
  review: 'warning',
  risky: 'danger',
  failed: 'info',
}

export const SOURCE_LABELS: Record<string, string> = {
  employee: '员工上传',
  admin: '后台上传',
  apply: '访客申请',
}

export function statusLabel(s: string): string {
  return STATUS_LABELS[s as MediaCheckStatus] || s || '—'
}

export function statusTag(s: string): 'danger' | 'warning' | 'success' | 'info' {
  return STATUS_TAGS[s as MediaCheckStatus] || 'info'
}

// takedownable 与后端 model.MediaCheck.Takedownable 保持同一口径：
// 只有「命中违规 + 未处置 + 推得出对象键」的才允许下架。
// 前端先判一次是为了不给出一个点了必然报错的按钮，真正的门禁在后端。
export function takedownable(row: MediaCheck): boolean {
  return row.status === 'risky' && row.action !== 'takedown' && !!row.objectKey
}

export function listMediaChecks(params: {
  page?: number
  size?: number
  status?: string
  onlyTakedownable?: string
}): Promise<Paged<MediaCheck>> {
  return http.get('/admin/media-checks', { params }).then((r) => toPaged<MediaCheck>(r))
}

export function getMediaCheckConfig(): Promise<MediaCheckConfig> {
  return http.get('/admin/media-checks/config').then((r) => (r.data || {}) as MediaCheckConfig)
}

// 下架 = 把存储里的对象覆盖成「内容不可用」占位图。**不可逆**（原图当场丢失），
// 所以界面上必须先确认；后端也只允许对「命中违规且未处置」的记录执行。
export function takedownMediaCheck(id: number): Promise<void> {
  return http.post(`/admin/media-checks/${id}/takedown`).then(() => undefined)
}
