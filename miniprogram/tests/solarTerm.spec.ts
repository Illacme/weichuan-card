// 二十四节气工具：判「今天是不是节气日」+ 把当天节气图排到背景条第一位。
//
// 重点盯两类错：
//  1. 日期表错——节气差一天，用户当天看到的就不是该节气的那张图。
//     表由 chinese-days 导出并用天文算法独立复算过（见 solarTerm.ts 头注释），
//     这里再钉一批已知日期，防止以后改表时手滑改错行。
//  2. 表外年份不猜——返回 null，让页面走「不自动选」而不是抓一个错值。
import { describe, it, expect } from 'vitest'
import { solarTermOf, solarTermOfDate, termFirst, SOLAR_TERMS } from '../utils/solarTerm'

describe('solarTermOf 已知日期', () => {
  it('24 个节气都有对应的海报素材 id，且 id 唯一', () => {
    expect(SOLAR_TERMS).toHaveLength(24)
    const ids = SOLAR_TERMS.map((t) => t.posterId)
    expect(new Set(ids).size).toBe(24)
    // 素材 id 必须是 poster-<key>：后台清单里就这个命名（manifest.json）
    for (const t of SOLAR_TERMS) expect(t.posterId).toBe('poster-' + t.key)
    // 节气名都是两个字：背景条角标直接放名字，放不下就会换行
    for (const t of SOLAR_TERMS) expect(Array.from(t.name).length).toBe(2)
  })

  it('立春：2024-02-04 / 2025-02-03 / 2026-02-04', () => {
    // 这三年立春日期都不同（4/3/4），是闰年错位的典型样本
    expect(solarTermOf(2024, 2, 4)!.name).toBe('立春')
    expect(solarTermOf(2025, 2, 3)!.name).toBe('立春')
    expect(solarTermOf(2026, 2, 4)!.name).toBe('立春')
  })

  it('清明与冬至：跨月交界也不串', () => {
    expect(solarTermOf(2024, 4, 4)!.name).toBe('清明')
    expect(solarTermOf(2025, 4, 4)!.name).toBe('清明')
    expect(solarTermOf(2026, 4, 5)!.name).toBe('清明')
    expect(solarTermOf(2024, 12, 21)!.name).toBe('冬至')
    expect(solarTermOf(2026, 12, 22)!.name).toBe('冬至')
  })

  it('1 月连着两个节气（小寒 + 大寒），不能互相吞', () => {
    expect(solarTermOf(2026, 1, 5)!.name).toBe('小寒')
    expect(solarTermOf(2026, 1, 20)!.name).toBe('大寒')
    // 小寒前一天 / 大寒后一天都不是节气
    expect(solarTermOf(2026, 1, 4)).toBe(null)
    expect(solarTermOf(2026, 1, 21)).toBe(null)
  })

  it('2026 全年 24 个节气日逐个核对', () => {
    // 表里 2026 = '052004180520052005210521072307230823082307220722'
    const expect24: Array<[number, number, string]> = [
      [1, 5, '小寒'], [1, 20, '大寒'], [2, 4, '立春'], [2, 18, '雨水'],
      [3, 5, '惊蛰'], [3, 20, '春分'], [4, 5, '清明'], [4, 20, '谷雨'],
      [5, 5, '立夏'], [5, 21, '小满'], [6, 5, '芒种'], [6, 21, '夏至'],
      [7, 7, '小暑'], [7, 23, '大暑'], [8, 7, '立秋'], [8, 23, '处暑'],
      [9, 7, '白露'], [9, 23, '秋分'], [10, 8, '寒露'], [10, 23, '霜降'],
      [11, 7, '立冬'], [11, 22, '小雪'], [12, 7, '大雪'], [12, 22, '冬至'],
    ]
    expect(expect24).toHaveLength(24)
    for (const [m, d, name] of expect24) {
      const t = solarTermOf(2026, m, d)
      expect(t && t.name).toBe(name)
    }
    // 每个节气只命中一天：前后各一天都必须是 null
    for (const [m, d] of expect24) {
      expect(solarTermOf(2026, m, d - 1)).toBe(null)
      expect(solarTermOf(2026, m, d + 1)).toBe(null)
    }
  })

  it('非节气日返回 null', () => {
    expect(solarTermOf(2026, 10, 24)).toBe(null) // 霜降（10-23）次日
    expect(solarTermOf(2026, 6, 15)).toBe(null)
    expect(solarTermOf(2026, 2, 14)).toBe(null)
  })

  it('表外年份返回 null，不猜不插值', () => {
    // 2019（表起点之前一天）与 2041（表终点之后一天）
    expect(solarTermOf(2019, 2, 4)).toBe(null)
    expect(solarTermOf(2041, 2, 4)).toBe(null)
    expect(solarTermOf(1999, 1, 1)).toBe(null)
    expect(solarTermOf(2100, 12, 31)).toBe(null)
  })

  it('月份越界不崩', () => {
    expect(solarTermOf(2026, 0, 10)).toBe(null)
    expect(solarTermOf(2026, 13, 10)).toBe(null)
    expect(solarTermOf(2026, -1, 10)).toBe(null)
  })

  it('solarTermOfDate 走本地时间年月日', () => {
    // 本地时区构造，与 solarTermOf 同参数——真机上 Date 就是本地时区
    expect(solarTermOfDate(new Date(2026, 9, 23))!.name).toBe('霜降')
    expect(solarTermOfDate(new Date(2026, 9, 24))).toBe(null)
  })
})

describe('termFirst 把当天节气图挪到第一位', () => {
  const list = [
    { id: 'poster-brand' },
    { id: 'poster-lichun' },
    { id: 'poster-shuangjiang' },
    { id: 'poster-zhongqiu' },
  ]

  it('节气图在中间时挪到最前，其余相对顺序不变', () => {
    const term = solarTermOf(2026, 10, 23)! // 霜降
    const out = termFirst(list, term)
    expect(out.map((p) => p.id)).toEqual([
      'poster-shuangjiang',
      'poster-brand',
      'poster-lichun',
      'poster-zhongqiu',
    ])
  })

  it('本来就是第一位：原样返回（同一个引用，不无谓重建）', () => {
    const term = solarTermOf(2026, 2, 4)! // 立春
    const l = [{ id: 'poster-lichun' }, { id: 'poster-brand' }]
    expect(termFirst(l, term)).toBe(l)
  })

  it('节气图不在清单里：原样返回，不插空位', () => {
    const term = solarTermOf(2026, 10, 23)! // 霜降，清单里没有
    expect(termFirst([{ id: 'poster-brand' }], term)).toEqual([{ id: 'poster-brand' }])
  })

  it('term 为 null（非节气日 / 表外年份）：原样返回', () => {
    expect(termFirst(list, null)).toBe(list)
  })
})
