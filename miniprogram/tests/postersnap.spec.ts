// 「海报页图片地址快照」（utils/postersnap.ts）。
//
// 这个模块存在的唯一理由：把海报页的「接口波」和「图片波」从串行变并行。
// 它记的只是三个地址，**不参与出图**——真正画什么永远以接口返回为准。
// 所以它的失败面很窄：最坏是白跑一次下载，绝不会画出错的海报。
// 但「白跑」也要防，几条边界必须钉死：
//
//  1. **按员工分键**：同一台手机可能看不同同事的名片，串了就是预热别人的头像。
//  2. **过期**：地址可能失效（对象被删、码重新生成），过期就不预热。
//  3. **只认绝对地址**：相对路径喂给 canvas.createImage 会静默失败并且占住
//     3 秒失败锁，反而拖累真正该加载的那次。
//
// 时间全部走可注入的 now/at：涉及时间的用例必须被固定时钟完全驱动，
// 否则它只在「写这行代码的那天」成立（本项目踩过这个坑）。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installWx } from './helpers/harness'
import {
  readPosterSnap,
  writePosterSnap,
  clearPosterSnap,
  POSTER_SNAP_TTL_MS,
} from '../utils/postersnap'

// 固定基准时刻（2023-11-14T22:13:20Z），与真实当前时间无关
const T0 = 1700000000000

const BG = 'https://cdn.example.com/api/assets/file/posters/poster-brand.jpg'
const AV = 'https://cdn.example.com/uploads/avatar-a.png'
const QR = 'https://cdn.example.com/uploads/wxacode/1_poster.jpg'

let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  const installed = installWx()
  storage = installed.storage
})

describe('海报图片快照', () => {
  it('没存过：返回 null（= 不预热，行为与改造前一致）', () => {
    expect(readPosterSnap(1, T0)).toBeNull()
  })

  it('写入后能读回三个地址', () => {
    writePosterSnap(1, { bg: BG, avatar: AV, qr: QR }, T0)

    const snap = readPosterSnap(1, T0)
    expect(snap).not.toBeNull()
    expect(snap!.bg).toBe(BG)
    expect(snap!.avatar).toBe(AV)
    expect(snap!.qr).toBe(QR)
    expect(snap!.at).toBe(T0)
  })

  // ↓ 漏了这条，看过 A 同事名片的手机再进 B 同事的海报页，
  //   会先拿 A 的头像去预热（画面上不会错，但白跑一次下载）
  it('不同员工互不干扰', () => {
    writePosterSnap(1, { bg: BG, avatar: AV, qr: QR }, T0)
    writePosterSnap(2, { bg: BG }, T0)

    expect(readPosterSnap(1, T0)!.avatar).toBe(AV)
    // 2 号只存了背景，不能沾上 1 号的头像
    expect(readPosterSnap(2, T0)!.avatar).toBe('')
    expect(readPosterSnap(2, T0)!.bg).toBe(BG)
  })

  it('恰好到期（等于 TTL）仍有效：边界按「超过才过期」', () => {
    writePosterSnap(1, { bg: BG }, T0)

    expect(readPosterSnap(1, T0 + POSTER_SNAP_TTL_MS)).not.toBeNull()
    expect(readPosterSnap(1, T0 + POSTER_SNAP_TTL_MS + 1)).toBeNull()
  })

  // 时钟被往回拨过：t < at 时如果照旧返回，快照会「永不过期」
  it('时钟回拨按过期处理', () => {
    writePosterSnap(1, { bg: BG }, T0)

    expect(readPosterSnap(1, T0 - 1)).toBeNull()
  })

  // 相对路径喂给 createImage 不报错、不回调，却会把失败锁写进去，
  // 让 draw() 在半秒后跳过这张图 —— 比不预热更糟
  it('非绝对地址一律丢弃', () => {
    writePosterSnap(1, { bg: '/api/assets/file/posters/poster-brand.jpg', avatar: 'wxfile://tmp/a.png' }, T0)

    // 三项全被判掉 = 没有快照
    expect(readPosterSnap(1, T0)).toBeNull()
    // 且不会往 storage 里留一条空壳
    expect(storage.get('posterSnap:1')).toBeUndefined()
  })

  it('三项全空时不落库（避免每次进页面白建一个离屏 canvas）', () => {
    writePosterSnap(1, { bg: '', avatar: '', qr: '' }, T0)

    expect(storage.get('posterSnap:1')).toBeUndefined()
    expect(readPosterSnap(1, T0)).toBeNull()
  })

  it('staffId 非法（<=0）不读也不写', () => {
    writePosterSnap(0, { bg: BG }, T0)
    writePosterSnap(-3, { bg: BG }, T0)

    expect(readPosterSnap(0, T0)).toBeNull()
    expect(readPosterSnap(-3, T0)).toBeNull()
    expect(storage.get('posterSnap:0')).toBeUndefined()
  })

  it('storage 里是脏数据：当没有，不抛', () => {
    storage.set('posterSnap:1', '{ 这不是 json')

    expect(() => readPosterSnap(1, T0)).not.toThrow()
    expect(readPosterSnap(1, T0)).toBeNull()
  })

  it('at 缺失或非正：当没有（无法判断是否过期）', () => {
    storage.set('posterSnap:1', JSON.stringify({ bg: BG, avatar: AV, qr: QR }))
    expect(readPosterSnap(1, T0)).toBeNull()

    storage.set('posterSnap:1', JSON.stringify({ bg: BG, at: 0 }))
    expect(readPosterSnap(1, T0)).toBeNull()
  })

  it('clear 之后不再命中（自愈路径）', () => {
    writePosterSnap(1, { bg: BG }, T0)
    clearPosterSnap(1)

    expect(readPosterSnap(1, T0)).toBeNull()
    expect(storage.has('posterSnap:1')).toBe(false)
  })

  // 存储满 / 隐私模式下 setStorageSync 会抛：不能把进页面主流程带崩
  it('写存储抛异常时不影响调用方', () => {
    const spy = vi.spyOn(wx, 'setStorageSync').mockImplementation(() => {
      throw new Error('quota exceeded')
    })

    expect(() => writePosterSnap(1, { bg: BG }, T0)).not.toThrow()
    spy.mockRestore()
  })
})
