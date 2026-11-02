import type { LicenseView } from '@/api/license'

/**
 * 把授权视图折叠成「一条横幅」或「不显示」。
 *
 * 抽成纯函数而不是写在组件里：这段判断决定**客户会不会在到期前知道**，
 * 是最该被单测钉住的地方（可关闭条件、优先级、文案拼装），
 * 而挂在 el-alert 上的组件测试既慢又难覆盖分支。
 */

export interface BannerCompany {
  id: number
  name: string
  text: string
}

export interface LicenseBanner {
  /** 内容指纹：内容变了（跳档、家数变化）就该重新弹出来，即使用户关过 */
  key: string
  type: 'warning' | 'error'
  title: string
  desc: string
  items: BannerCompany[]
  /** 需要跳转的出口；空串表示没有可点的动作 */
  action: '' | 'companies'
  closable: boolean
}

const NEEDS_ATTENTION = ['warn', 'urgent', 'expired']

export function buildLicenseBanner(v: LicenseView | null | undefined): LicenseBanner | null {
  if (!v || !v.self) return null

  const self = v.self
  // ① 自己这份临期/过期：优先级最高——这是「你自己马上就不能用了」。
  if (NEEDS_ATTENTION.includes(self.level)) {
    const extra: string[] = []
    if (self.seatsFull && self.seats > 0) {
      // 席位满了会让人「建不了员工」，而报错只说 5/5——顺手在这里说清是哪里来的
      extra.push(`席位已用满（${self.seatsUsed}/${self.seats}），新增员工会被拒绝`)
    }
    return {
      key: `self:${self.level}:${self.expiresAt}:${self.seatsUsed}/${self.seats}`,
      type: self.level === 'expired' ? 'error' : 'warning',
      title: self.title,
      desc: [self.consequence, self.renewHint, ...extra].filter(Boolean).join('；'),
      items: [],
      action: '',
      // 只剩 30 天可以关（还早，别挡着干活）；最后一周与已过期不给关闭按钮——
      // 这两档正是最不该被「关掉眼不见」的时刻。
      closable: self.level === 'warn',
    }
  }

  // ② 平台超管：哪些客户快到期了。这是运营每天唯一需要看的那一屏。
  const a = v.alerts
  if (v.scope !== 'platform' || !a || a.within30 === 0) return null

  // 三个数是包含关系（已过期也算 7 天内、30 天内），这里拆成互斥的三段再拼，
  // 否则会出现「30 天内 3 家 · 7 天内 2 家 · 已过期 1 家」这种加起来对不上的句子。
  const parts: string[] = []
  if (a.expired > 0) parts.push(`${a.expired} 家已过期（后台已只读）`)
  const within7 = Math.max(0, a.within7 - a.expired)
  if (within7 > 0) parts.push(`${within7} 家 7 天内到期`)
  const within30 = Math.max(0, a.within30 - a.within7)
  if (within30 > 0) parts.push(`${within30} 家 30 天内到期`)

  const items = (v.expiring ?? []).slice(0, 5).map((it) => ({
    id: it.companyId,
    name: it.companyName || `企业 ${it.companyId}`,
    text: it.title,
  }))

  return {
    key: `platform:${a.expired}:${a.within7}:${a.within30}`,
    type: a.expired > 0 ? 'error' : 'warning',
    title: `${a.within30} 家企业的授权将在 30 天内到期`,
    desc: `${parts.join(' · ')}。到期后该企业后台会自动变只读——客户会先于我们发现问题，续费要在到期前谈。`,
    items,
    action: 'companies',
    closable: true,
  }
}
