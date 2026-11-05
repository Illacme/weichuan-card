// 「默认入口缓存」（utils/entry.ts）。
//
// 这个模块存在的唯一理由：让裸启动的下一次进站不必先等一次 /api/team 往返。
// 但它记的是「谁的名片」，记错了点进去就是别人家公司——所以两条边界必须钉死：
//
//  1. **按租户分键**：平台模式下本机可能粘滞着不同企业的 companyId。
//     不分键 = 把 A 企业的默认员工套到 B 企业头上。
//  2. **过期**：管理员换人 / 员工停用后，缓存要能自己退场，不能一直跳错。
//
// 时间全部走可注入的 now/at：涉及时间的用例必须被固定时钟完全驱动，
// 否则它只在「写这行代码的那天」成立（本项目踩过这个坑：一个用真实时钟的
// 边界用例，过了那天必红，而且看起来像新引入的 bug）。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installWx } from './helpers/harness'
import { readEntryStaff, writeEntryStaff, clearEntryStaff, ENTRY_TTL_MS } from '../utils/entry'

// 固定基准时刻（2023-11-14T22:13:20Z），与真实当前时间无关
const T0 = 1700000000000

let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  const installed = installWx()
  storage = installed.storage
})

describe('默认入口缓存', () => {
  it('没存过：返回 0（= 走网络问后端）', () => {
    expect(readEntryStaff(1, T0)).toBe(0)
  })

  it('写入后能读回，并带上写入时刻', () => {
    writeEntryStaff(1, 42, T0)

    expect(readEntryStaff(1, T0)).toBe(42)
    expect(JSON.parse(storage.get('entryStaff:1')).staffId).toBe(42)
  })

  // ↓ 按租户分键：这条漏了，扫过 A 家码的人再扫 B 家码，会被跳进 A 家的员工
  it('不同企业互不干扰', () => {
    writeEntryStaff(1, 42, T0)
    writeEntryStaff(2, 99, T0)

    expect(readEntryStaff(1, T0)).toBe(42)
    expect(readEntryStaff(2, T0)).toBe(99)
    // 没写过的第三个企业不受影响
    expect(readEntryStaff(3, T0)).toBe(0)
  })

  it('未绑定租户（companyId=0）与 1 号企业是两个键', () => {
    writeEntryStaff(0, 7, T0)

    expect(readEntryStaff(0, T0)).toBe(7)
    // 0 是「不传，走后端缺省」，不能和显式的租户 1 混为一谈
    expect(readEntryStaff(1, T0)).toBe(0)
  })

  it('恰好到期（等于 TTL）仍有效：边界按「超过才过期」', () => {
    writeEntryStaff(1, 42, T0)

    expect(readEntryStaff(1, T0 + ENTRY_TTL_MS)).toBe(42)
  })

  it('超过 TTL：过期，返回 0（让管理员改的配置最迟一天内生效）', () => {
    writeEntryStaff(1, 42, T0)

    expect(readEntryStaff(1, T0 + ENTRY_TTL_MS + 1)).toBe(0)
  })

  // 本机时钟被往回拨（换手机系统时间 / 时区调整）时，t - at 会是负数。
  // 负值当然不 > TTL，若不额外判 t < at，这条坏记录会永远有效。
  it('本机时钟回拨：按过期处理，不永久有效', () => {
    writeEntryStaff(1, 42, T0)

    expect(readEntryStaff(1, T0 - 1000)).toBe(0)
  })

  it('staffId 为 0 / 负数：不写入（否则会跳到一个 staffId=0 的空白名片）', () => {
    writeEntryStaff(1, 0, T0)
    writeEntryStaff(1, -3, T0)

    expect(storage.has('entryStaff:1')).toBe(false)
    expect(readEntryStaff(1, T0)).toBe(0)
  })

  // storage 可能被旧版本或人工写过别的形状：解析失败只当「没有缓存」，
  // 绝不能抛出去把名片墙的 onLoad 带崩（那样连名片墙都进不去）
  it('存储里是坏数据：当作没有缓存，不抛异常', () => {
    storage.set('entryStaff:1', '{不是 JSON')

    expect(() => readEntryStaff(1, T0)).not.toThrow()
    expect(readEntryStaff(1, T0)).toBe(0)
  })

  it('结构缺字段：当作没有缓存', () => {
    storage.set('entryStaff:1', JSON.stringify({ staffId: 42 })) // 缺 at
    expect(readEntryStaff(1, T0)).toBe(0)

    storage.set('entryStaff:1', JSON.stringify({ at: T0 })) // 缺 staffId
    expect(readEntryStaff(1, T0)).toBe(0)
  })

  it('清掉之后读不到（管理员取消默认入口 / 员工已失效时用）', () => {
    writeEntryStaff(1, 42, T0)
    clearEntryStaff(1)

    expect(readEntryStaff(1, T0)).toBe(0)
    // 只清自己那一份，其他租户不受影响
    writeEntryStaff(2, 99, T0)
    clearEntryStaff(1)
    expect(readEntryStaff(2, T0)).toBe(99)
  })
})
