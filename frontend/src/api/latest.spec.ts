import { describe, it, expect } from 'vitest'
import axios from 'axios'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { latestOnly, isSuperseded } from './latest'

// 源码级守门：这 6 个页面都会因筛选 / 翻页 / 下拉而连续重查，
// 任何一个漏接守门都会退回「旧响应盖掉新结果」。新增同类页面时把路径补进数组。
const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const RACY_PAGES = [
  'frontend/src/views/Stats.vue',
  'frontend/src/components/StatDetail.vue',
  'frontend/src/components/MPReleaseDialog.vue',
  'miniprogram/pages/project/list/list.ts',
  'miniprogram/pages/team/all.ts',
  'miniprogram/pages/employee/stat/stat.ts',
]

describe.each(RACY_PAGES)('%s 竞态守门静态检查', (file) => {
  it('接入了守门工具，且响应写回前有 isCurrent 判定', () => {
    const src = readFileSync(resolve(ROOT, file), 'utf8')
    expect(/latestOnly|latestGuard/.test(src)).toBe(true)
    expect(/isCurrent\(/.test(src)).toBe(true)
  })
})

describe('latestOnly（前端请求竞态守门）', () => {
  it('next() 真取消上一轮 signal，且只认最后一轮为当前', () => {
    const guard = latestOnly()
    const first = guard.next()
    expect(first.aborted).toBe(false)
    expect(guard.isCurrent(first)).toBe(true)

    const second = guard.next()
    // 取消必须是「真 abort」而不是只翻个布尔：axios 会把在途请求断掉，
    // 否则旧响应仍会跑完整条拦截器链，浪费带宽还可能触发错误提示。
    expect(first.aborted).toBe(true)
    expect(second.aborted).toBe(false)
    expect(guard.isCurrent(first)).toBe(false)
    expect(guard.isCurrent(second)).toBe(true)
  })

  it('cancel() 取消在途请求，并让所有旧 signal 都不再是当前', () => {
    const guard = latestOnly()
    const signal = guard.next()
    guard.cancel()
    expect(signal.aborted).toBe(true)
    expect(guard.isCurrent(signal)).toBe(false)
    // 卸载后即使又有响应回来，fake signal 也不能冒充当前轮
    expect(guard.isCurrent(new AbortController().signal)).toBe(false)
  })

  it('cancel() 可重复调用，之后再 next() 仍能正常工作', () => {
    const guard = latestOnly()
    guard.cancel()
    guard.cancel()
    const signal = guard.next()
    expect(guard.isCurrent(signal)).toBe(true)
    expect(signal.aborted).toBe(false)
  })
})

describe('isSuperseded', () => {
  it('认 axios 的 CanceledError / ERR_CANCELED 与小程序同名错误', () => {
    expect(isSuperseded(new axios.CanceledError('canceled'))).toBe(true)
    expect(isSuperseded({ code: 'ERR_CANCELED' })).toBe(true)
    expect(isSuperseded({ name: 'SupersededError' })).toBe(true)
  })

  it('不把真实网络错误 / 业务失败误判成取消（否则错误会被静默吞掉）', () => {
    expect(isSuperseded(new Error('Network Error'))).toBe(false)
    expect(isSuperseded({ code: 1, msg: '参数错误' })).toBe(false)
    expect(isSuperseded(null)).toBe(false)
    expect(isSuperseded(undefined)).toBe(false)
  })
})
