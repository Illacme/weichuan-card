import { describe, it, expect } from 'vitest'
import {
  formatDate,
  formatMinute,
  formatDateTime,
  detailTimeText,
} from '../utils/datetime'

// 详情页「更新于」的三档展示，以及三个格式化函数的口径一致性。
//
// 输入故意用不带时区后缀的 RFC3339（'2026-10-16T15:34:12'）：JS 按**本地时间**
// 解析，因此断言里的钟点在任何 TZ 的机器上都成立。带 `+08:00` 的字面量在 UTC
// runner 上会变成 07:34，是项目踩过的假红坑。
const AT = '2026-10-16T15:34:12'

describe('utils/datetime', () => {
  it('formatDate 只到日', () => {
    expect(formatDate(AT)).toBe('2026-10-16')
  })

  it('formatMinute 到分', () => {
    expect(formatMinute(AT)).toBe('2026-10-16 15:34')
  })

  it('formatDateTime 到秒', () => {
    expect(formatDateTime(AT)).toBe('2026-10-16 15:34:12')
  })

  // 空值与脏值口径必须三者一致：空→「—」、脏→原样回显（不静默吞掉，便于发现脏数据）
  it('空值统一显示「—」，非法值原样回显', () => {
    for (const fn of [formatDate, formatMinute, formatDateTime]) {
      expect(fn('')).toBe('—')
      expect(fn(null)).toBe('—')
      expect(fn(undefined)).toBe('—')
      expect(fn('not-a-date')).toBe('not-a-date')
    }
  })

  it('接受 Date 实例', () => {
    expect(formatDate(new Date(2026, 9, 16, 15, 34, 12))).toBe('2026-10-16')
  })
})

describe('detailTimeText（后台下发的详情页时间档位）', () => {
  it('date：只到日', () => {
    expect(detailTimeText('date', AT)).toBe('2026-10-16')
  })

  it('minute：到分', () => {
    expect(detailTimeText('minute', AT)).toBe('2026-10-16 15:34')
  })

  // 空串是「不显示」的信号：wxml 用 wx:if 整行隐藏，不能返回「—」占位
  it('hidden：空串，整行不显示', () => {
    expect(detailTimeText('hidden', AT)).toBe('')
  })

  // 端上比后端新、或库里被手工改坏：按 date 兜底而不是让这一行凭空消失
  it('未知档位按 date 兜底', () => {
    expect(detailTimeText('', AT)).toBe('2026-10-16')
    expect(detailTimeText(undefined, AT)).toBe('2026-10-16')
    expect(detailTimeText('weird', AT)).toBe('2026-10-16')
  })

  it('没有时间值：无论什么档位都返回空串', () => {
    expect(detailTimeText('date', '')).toBe('')
    expect(detailTimeText('minute', null)).toBe('')
    expect(detailTimeText('hidden', AT)).toBe('')
  })
})
