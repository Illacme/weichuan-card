import http from './request'

/**
 * 授权到期视图（后端 handler/license_view.go）。
 *
 * 为什么要有它：授权到期会**自动**降级只读（私有部署甚至起不来），但此前没有任何
 * 东西会**主动**告诉任何人——客户是「某天上午突然改不了内容」才知道的。
 * 这个接口就是「让客户自己看见」的那一层：登录后台即见顶部横幅。
 *
 * 一个接口服务两种视角：企业管理员看自己那份，平台超管额外拿到「哪些客户快到期」。
 */
export type LicenseLevel = 'none' | 'ok' | 'warn' | 'urgent' | 'expired'

export interface LicenseSelf {
  edition: string
  seats: number // 0 = 不限
  seatsUsed: number
  seatsFull: boolean
  expiresAt: string // YYYY-MM-DD；空串 = 不过期
  daysLeft: number
  expired: boolean
  level: LicenseLevel
  title: string
  consequence: string // 到期后会怎样（仅临期/过期时下发）
  renewHint: string // 怎么恢复（仅临期/过期时下发）
}

export interface LicenseExpiringItem {
  companyId: number
  companyName: string
  edition: string
  seats: number
  expiresAt: string
  daysLeft: number
  expired: boolean
  level: LicenseLevel
  title: string
}

export interface LicenseView {
  mode: 'platform' | 'private'
  scope: 'tenant' | 'platform'
  self: LicenseSelf
  // 以下仅平台超管（scope=platform）才有
  alerts?: { expired: number; within7: number; within30: number }
  expiring?: LicenseExpiringItem[]
}

/**
 * 取当前登录者的授权视图。
 *
 * 失败返回 null 而不是抛错：横幅是**增强**，拿不到就什么都不显示。
 * 让一个提醒接口把整个后台布局打崩（或弹一条红条），比不提醒更糟。
 */
export function getLicense(): Promise<LicenseView | null> {
  return http
    .get('/admin/license')
    .then((r) => ((r?.data ?? null) as LicenseView | null))
    .catch(() => null)
}
