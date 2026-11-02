import { describe, expect, it } from 'vitest'
import { buildLicenseBanner } from './licenseBanner'
import type { LicenseView } from '@/api/license'

// 这条横幅决定「客户会不会在到期前知道」。组件测试覆盖了渲染与交互，
// 这里只钉最容易算错的一处：三个计数是**包含关系**（已过期也算 7 天内、30 天内），
// 直接拼会拼出「30 天内 3 家 · 7 天内 2 家 · 已过期 1 家」这种加起来对不上的句子。

function view(over: Partial<LicenseView> = {}, selfOver: Record<string, unknown> = {}): LicenseView {
  return {
    mode: 'platform',
    scope: 'tenant',
    self: {
      edition: 'trial',
      seats: 5,
      seatsUsed: 1,
      seatsFull: false,
      expiresAt: '2026-11-09',
      daysLeft: 7,
      expired: false,
      level: 'urgent',
      title: '授权 2026-11-09 到期，仅剩 7 天',
      consequence: '到期后后台自动切换为只读',
      renewHint: '请联系服务商续费，续费后自动恢复，无需重启',
      ...selfOver,
    } as LicenseView['self'],
    ...over,
  }
}

describe('buildLicenseBanner', () => {
  it('无数据 / 授权正常 / 不过期：都不显示', () => {
    expect(buildLicenseBanner(null)).toBeNull()
    expect(buildLicenseBanner(undefined)).toBeNull()
    expect(buildLicenseBanner(view({}, { level: 'ok', daysLeft: 180 }))).toBeNull()
    expect(buildLicenseBanner(view({}, { level: 'none', expiresAt: '', daysLeft: 0 }))).toBeNull()
  })

  it('临期：黄色、可关（还早，别挡着干活）', () => {
    // title 由后端按同一套口径算好下发，前端不重算天数（重算必然与服务端漂移一天）
    const b = buildLicenseBanner(view({}, { level: 'warn', daysLeft: 20, title: '授权 2026-11-22 到期，还剩 20 天' }))!
    expect(b.type).toBe('warning')
    expect(b.closable).toBe(true)
    expect(b.title).toContain('20 天')
  })

  it('最后一周与已过期：不可关——这两档正是最不该「关掉眼不见」的时刻', () => {
    expect(buildLicenseBanner(view({}, { level: 'urgent' }))!.closable).toBe(false)
    const expired = buildLicenseBanner(view({}, { level: 'expired', expired: true }))!
    expect(expired.closable).toBe(false)
    expect(expired.type).toBe('error')
  })

  it('后果与续费路径必须一起出现：只说「会只读」不说怎么办，等于吓人一跳', () => {
    const b = buildLicenseBanner(view())!
    expect(b.desc).toContain('只读')
    expect(b.desc).toContain('续费')
  })

  it('席位用满时补一句：否则「建不了员工」只能看到一个 5/5 的报错', () => {
    const b = buildLicenseBanner(view({}, { seats: 5, seatsUsed: 5, seatsFull: true }))!
    expect(b.desc).toContain('5/5')
    expect(b.desc).toContain('新增员工会被拒绝')
  })

  it('平台超管：三段互斥，加起来等于总数', () => {
    const b = buildLicenseBanner(
      view(
        {
          scope: 'platform',
          alerts: { expired: 1, within7: 2, within30: 4 },
          expiring: [],
        },
        { level: 'none', expiresAt: '', daysLeft: 0 }
      )
    )!
    // 4 = 1 已过期 + 1（7 天内但未过期）+ 2（30 天内但超过 7 天）
    expect(b.desc).toContain('1 家已过期')
    expect(b.desc).toContain('1 家 7 天内到期')
    expect(b.desc).toContain('2 家 30 天内到期')
    expect(b.title).toContain('4 家企业')
    // 有已过期的就该标红：那是事故，不是提醒
    expect(b.type).toBe('error')
  })

  it('平台超管：只有 30 天内、没有过期的，标黄而不是红', () => {
    const b = buildLicenseBanner(
      view({ scope: 'platform', alerts: { expired: 0, within7: 0, within30: 2 }, expiring: [] }, { level: 'none', expiresAt: '', daysLeft: 0 })
    )!
    expect(b.type).toBe('warning')
    expect(b.desc).not.toContain('已过期')
    expect(b.action).toBe('companies')
  })

  it('平台超管：名单最多列 5 条，但标题按总数说', () => {
    const expiring = Array.from({ length: 9 }, (_, i) => ({
      companyId: i + 1,
      companyName: `企业${i + 1}`,
      edition: 'trial',
      seats: 5,
      expiresAt: '2026-11-09',
      daysLeft: 7,
      expired: false,
      level: 'urgent' as const,
      title: '授权 2026-11-09 到期，仅剩 7 天',
    }))
    const b = buildLicenseBanner(
      view({ scope: 'platform', alerts: { expired: 0, within7: 9, within30: 9 }, expiring }, { level: 'none', expiresAt: '', daysLeft: 0 })
    )!
    expect(b.items).toHaveLength(5)
    expect(b.title).toContain('9 家企业')
  })

  it('企业管理员永远拿不到跨租户名单（哪怕后端误传了）', () => {
    const b = buildLicenseBanner(
      view(
        {
          scope: 'tenant',
          alerts: { expired: 1, within7: 1, within30: 1 },
          expiring: [
            { companyId: 9, companyName: '别家公司', edition: 'trial', seats: 5, expiresAt: '2026-11-01', daysLeft: -1, expired: true, level: 'expired', title: 'x' },
          ],
        },
        { level: 'ok', daysLeft: 200 }
      )
    )
    expect(b).toBeNull()
  })

  it('内容指纹随档位变化：改档后要能重新弹出来', () => {
    const warn = buildLicenseBanner(view({}, { level: 'warn', daysLeft: 20 }))!
    const urgent = buildLicenseBanner(view({}, { level: 'urgent', daysLeft: 5 }))!
    expect(warn.key).not.toBe(urgent.key)
  })
})
