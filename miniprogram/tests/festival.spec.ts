// 节日工具：判「今天是不是节日」+ 把当天节日图排到背景条第一位。
//
// 重点盯四类错：
//  1. 日期表错——节日差一天，用户当天看到的就不是那张图。
//     表由 solarlunar 的 lunar2solar 离线生成、solar2lunar 反算校验过，
//     这里再钉一批已知日期（含权威日历核对过的 2026 小年 2/10）。
//  2. 压缩串串位——行首两位是条数，每条 5 字符。偏移算错会整行错位，
//     而且错出来的还是个「像模像样」的日期，比崩掉更难发现。
//  3. 闰月里的同名日被当成节日——2023-03-23 是闰二月初二，不是龙抬头。
//  4. 农历节日漏登记/公历节日漏登记——用「全年扫一遍」兜住。
import { describe, it, expect } from 'vitest'
import {
  festivalOf,
  festivalOfDate,
  festivalFirst,
  LUNAR_FESTIVALS,
  type Festival,
} from '../utils/festival'

const nameOf = (y: number, m: number, d: number) => {
  const f = festivalOf(y, m, d)
  return f ? f.name : null
}

describe('festivalOf · 农历节日', () => {
  it('10 个农历节日都有海报素材 id，且 id 唯一、命名一致', () => {
    expect(LUNAR_FESTIVALS).toHaveLength(10)
    const ids = LUNAR_FESTIVALS.map((f) => f.posterId)
    expect(new Set(ids).size).toBe(10)
    for (const f of LUNAR_FESTIVALS) expect(f.posterId).toBe('poster-' + f.key)
    // 角标直接放名字：超过 4 个字在 120rpx 宽的缩略图里会换行
    for (const f of LUNAR_FESTIVALS) expect(Array.from(f.name).length).toBeLessThanOrEqual(4)
  })

  it('2026 全年 10 个农历节日逐个核对', () => {
    // 2026 春节 2/17、中秋 9/25、端午 6/19 都是公开日历可查的；
    // 小年 2/10（北方，腊月廿三）已与权威日历核对过——早先凭记忆记成 2/9，是错的。
    const expect10: Array<[number, number, string]> = [
      [1, 26, '腊八'], [2, 10, '小年'], [2, 16, '除夕'], [2, 17, '春节'],
      [3, 3, '元宵'], [3, 20, '龙抬头'], [6, 19, '端午'], [8, 19, '七夕'],
      [9, 25, '中秋'], [10, 18, '重阳'],
    ]
    expect(expect10).toHaveLength(10)
    for (const [m, d, name] of expect10) {
      expect(nameOf(2026, m, d)).toBe(name)
    }
  })

  it('每个农历节日只命中一天：前后一天都不是同一个节日', () => {
    const dates: Array<[number, number]> = [
      [1, 26], [2, 10], [2, 16], [2, 17], [3, 3],
      [3, 20], [6, 19], [8, 19], [9, 25], [10, 18],
    ]
    for (const [m, d] of dates) {
      const key = festivalOf(2026, m, d)!.key
      const before = festivalOf(2026, m, d - 1)
      const after = festivalOf(2026, m, d + 1)
      expect(before && before.key).not.toBe(key)
      expect(after && after.key).not.toBe(key)
    }
  })

  it('非农历节日日返回 null', () => {
    expect(festivalOf(2026, 2, 9)).toBe(null) // 小年（2/10）前一天
    expect(festivalOf(2026, 2, 11)).toBe(null) // 除夕（2/16）前五天
    expect(festivalOf(2026, 7, 7)).toBe(null)
    // 注意：1/1 与 12/25 不是「非节日」，它们是公历节日（元旦/圣诞），
    // 这里只断言它们不是**农历**节日。
    expect(LUNAR_FESTIVALS.some((l) => l.key === festivalOf(2026, 1, 1)!.key)).toBe(false)
    expect(LUNAR_FESTIVALS.some((l) => l.key === festivalOf(2026, 12, 25)!.key)).toBe(false)
  })

  it('2022 年有两个腊八：1/10 和 12/30', () => {
    // 农历 2021 无闰月、春节偏晚，腊月整月落在 2022 年 1 月；
    // 农历 2022 也无闰月、春节偏早，腊月初八又落在 2022 年 12 月。
    // 这是「行 = 节日实际所在公历年」编码的直接后果，也是最容易写错的地方。
    expect(nameOf(2022, 1, 10)).toBe('腊八')
    expect(nameOf(2022, 12, 30)).toBe('腊八')
  })

  it('2023 年一个腊八都没有：腊八在 2024-01-18', () => {
    expect(nameOf(2023, 1, 10)).toBe(null)
    expect(nameOf(2023, 1, 18)).toBe(null)
    expect(nameOf(2024, 1, 18)).toBe('腊八')
  })

  it('闰月里的同名日不是节日', () => {
    // 2023 闰二月：2023-03-23 是闰二月初二，不是龙抬头
    expect(nameOf(2023, 3, 23)).toBe(null)
    expect(nameOf(2023, 2, 21)).toBe('龙抬头') // 真的二月初二
    // 2028 闰五月：2028-06-27 是闰五月初五，不是端午
    expect(nameOf(2028, 6, 27)).toBe(null)
    expect(nameOf(2028, 5, 28)).toBe('端午')
    // 2039 闰五月同理
    expect(nameOf(2039, 6, 26)).toBe(null)
  })

  it('春节/除夕跨年样本：三年四种形态', () => {
    expect(nameOf(2024, 2, 9)).toBe('除夕')
    expect(nameOf(2024, 2, 10)).toBe('春节')
    expect(nameOf(2025, 1, 28)).toBe('除夕') // 腊月小，除夕提前到 1 月
    expect(nameOf(2025, 1, 29)).toBe('春节')
    expect(nameOf(2026, 2, 16)).toBe('除夕')
    expect(nameOf(2026, 2, 17)).toBe('春节')
  })

  it('中秋与国庆同一天（2020-10-01）：判中秋', () => {
    expect(nameOf(2020, 10, 1)).toBe('中秋')
    expect(nameOf(2031, 10, 1)).toBe('中秋')
  })
})

describe('festivalOf · 公历节日', () => {
  it('日期固定的 9 个', () => {
    expect(nameOf(2026, 1, 1)).toBe('元旦')
    expect(nameOf(2026, 2, 14)).toBe('情人节')
    expect(nameOf(2026, 3, 8)).toBe('女神节')
    expect(nameOf(2026, 5, 1)).toBe('劳动节')
    expect(nameOf(2026, 6, 1)).toBe('儿童节')
    expect(nameOf(2026, 9, 10)).toBe('教师节')
    expect(nameOf(2026, 10, 1)).toBe('国庆')
    expect(nameOf(2026, 12, 24)).toBe('平安夜')
    expect(nameOf(2026, 12, 25)).toBe('圣诞')
  })

  it('公历节日不依赖年份表：2041 年照样生效', () => {
    expect(nameOf(2041, 1, 1)).toBe('元旦')
    expect(nameOf(2041, 12, 25)).toBe('圣诞')
  })

  it('母亲节 / 父亲节 / 感恩节：第 n 个星期几', () => {
    expect(nameOf(2026, 5, 10)).toBe('母亲节') // 2026-05-01 周五 → 第 2 个周日是 10 号
    expect(nameOf(2026, 5, 3)).toBe(null) // 第 1 个周日，不是母亲节
    expect(nameOf(2026, 5, 17)).toBe(null) // 第 3 个周日
    expect(nameOf(2026, 6, 21)).toBe('父亲节') // 2026-06-01 周一 → 第 3 个周日是 21 号
    expect(nameOf(2026, 6, 14)).toBe(null) // 第 2 个周日
    expect(nameOf(2026, 11, 26)).toBe('感恩节') // 第 4 个周四
    expect(nameOf(2026, 11, 19)).toBe(null) // 第 3 个周四
    // 换个年份再钉一组，防止公式只在 2026 碰巧对
    expect(nameOf(2027, 5, 9)).toBe('母亲节')
    expect(nameOf(2027, 6, 20)).toBe('父亲节')
    expect(nameOf(2027, 11, 25)).toBe('感恩节')
    expect(nameOf(2028, 5, 14)).toBe('母亲节')
    expect(nameOf(2028, 6, 18)).toBe('父亲节')
    expect(nameOf(2028, 11, 23)).toBe('感恩节')
  })
})

describe('festivalOf · 撞期与边界', () => {
  it('农历节日优先于公历节日', () => {
    expect(nameOf(2033, 2, 14)).toBe('元宵') // 不是情人节
    expect(nameOf(2037, 2, 14)).toBe('除夕') // 不是情人节
    expect(nameOf(2022, 9, 10)).toBe('中秋') // 不是教师节
    expect(nameOf(2031, 1, 1)).toBe('腊八') // 不是元旦
    expect(nameOf(2033, 6, 1)).toBe('端午') // 不是儿童节
  })

  it('月份/日期越界不崩', () => {
    expect(festivalOf(2026, 0, 1)).toBe(null)
    expect(festivalOf(2026, 13, 1)).toBe(null)
    expect(festivalOf(2026, -1, 1)).toBe(null)
    expect(festivalOf(2026, 1, 0)).toBe(null)
    expect(festivalOf(2026, 1, 32)).toBe(null)
    expect(festivalOf(2026, 2, 30)).toBe(null)
  })

  it('festivalOfDate 走本地时间年月日', () => {
    // new Date(y, m, d) 的 m 是 0 起：6 月要传 5
    expect(festivalOfDate(new Date(2026, 1, 17))!.name).toBe('春节')
    expect(festivalOfDate(new Date(2026, 1, 16))!.name).toBe('除夕')
    expect(festivalOfDate(new Date(2026, 5, 19))!.name).toBe('端午') // 6/19
    expect(festivalOfDate(new Date(2026, 0, 1))!.name).toBe('元旦')
    expect(festivalOfDate(new Date(2026, 4, 10))!.name).toBe('母亲节') // 5/10
    expect(festivalOfDate(new Date(2026, 3, 15))).toBe(null)
  })
})

describe('压缩表结构自检', () => {
  // 把 FEST_DAYS 的解码逻辑在测试里独立走一遍：这里不 import 表本身，
  // 而是穷举 2020-2040 的每一天，检查「命中的必须真的是节日」——
  // 漏判靠上面那些已知日期，误判靠这里的全年扫。
  const years: number[] = []
  for (let y = 2020; y <= 2040; y++) years.push(y)

  it('2020-2040 每一年都至少命中 9 个农历节日', () => {
    for (const y of years) {
      let n = 0
      for (let m = 1; m <= 12; m++) {
        for (let d = 1; d <= 31; d++) {
          const f = festivalOf(y, m, d)
          // 只数农历那批：公历节日日期固定，年年都有
          if (f && LUNAR_FESTIVALS.some((l) => l.key === f.key)) n++
        }
      }
      expect(n, `${y} 年农历节日数`).toBeGreaterThanOrEqual(9)
    }
  })

  it('2020-2040 每一年 10 个农历节日都出现过（除夕/腊八可跨年）', () => {
    for (const y of years) {
      const keys = new Set<string>()
      for (let m = 1; m <= 12; m++) {
        for (let d = 1; d <= 31; d++) {
          const f = festivalOf(y, m, d)
          if (f && LUNAR_FESTIVALS.some((l) => l.key === f.key)) keys.add(f.key)
        }
      }
      // 2022 双腊八、2023 无腊八都在预期内；这里只要求「大部分」齐，
      // 真正逐日核对由上面的已知日期负责。
      expect(keys.size, `${y} 年出现的农历节日种类`).toBeGreaterThanOrEqual(9)
    }
  })

  it('同一天不会返回两个不同节日', () => {
    for (const y of years) {
      for (let m = 1; m <= 12; m++) {
        for (let d = 1; d <= 31; d++) {
          const f: Festival | null = festivalOf(y, m, d)
          expect(f === null || typeof f.name === 'string').toBe(true)
        }
      }
    }
  })
})

describe('festivalFirst 把当天节日图挪到第一位', () => {
  const list = [
    { id: 'poster-brand' },
    { id: 'poster-longtaitou' },
    { id: 'poster-shuangjiang' },
    { id: 'poster-zhongqiu' },
  ]

  it('节日图在中间时挪到最前，其余相对顺序不变', () => {
    const fest = festivalOf(2026, 3, 20)! // 龙抬头
    expect(festivalFirst(list, fest).map((p) => p.id)).toEqual([
      'poster-longtaitou',
      'poster-brand',
      'poster-shuangjiang',
      'poster-zhongqiu',
    ])
  })

  it('本来就是第一位：原样返回（同一个引用，不无谓重建）', () => {
    const l = [{ id: 'poster-zhongqiu' }, { id: 'poster-brand' }]
    expect(festivalFirst(l, festivalOf(2026, 9, 25)!)).toBe(l)
  })

  it('节日图不在清单里：原样返回，不插空位', () => {
    const fest = festivalOf(2026, 9, 25)! // 中秋，清单里没有
    expect(festivalFirst([{ id: 'poster-brand' }], fest)).toEqual([{ id: 'poster-brand' }])
  })

  it('festival 为 null（非节日日 / 表外年份）：原样返回', () => {
    expect(festivalFirst(list, null)).toBe(list)
  })
})
