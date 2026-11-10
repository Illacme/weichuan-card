import { describe, it, expect, vi } from 'vitest'
import { latestGuard, isSuperseded, SupersededError } from '../utils/latest'

describe('latestGuard（小程序请求竞态守门）', () => {
  it('next() 把上一轮标为 aborted 并调用在途 task.abort()，只认最后一轮', () => {
    const guard = latestGuard()
    const abort = vi.fn()
    const first: any = guard.next()
    first.task = { abort }
    expect(guard.isCurrent(first)).toBe(true)

    const second = guard.next()
    expect(first.aborted).toBe(true)
    expect(abort).toHaveBeenCalledTimes(1)
    expect(guard.isCurrent(first)).toBe(false)
    expect(guard.isCurrent(second)).toBe(true)
    expect(second.aborted).toBe(false)
  })

  it('重复取消同一轮不会重复 abort', () => {
    const guard = latestGuard()
    const abort = vi.fn()
    const first: any = guard.next()
    first.task = { abort }
    guard.next()
    guard.next()
    expect(abort).toHaveBeenCalledTimes(1)
  })

  it('task 缺失（单测桩）时取消只翻标记，不抛', () => {
    const guard = latestGuard()
    const first = guard.next()
    expect(() => guard.next()).not.toThrow()
    expect(first.aborted).toBe(true)
  })

  it('abort 抛错（请求已结束）时仍完成取消', () => {
    const guard = latestGuard()
    const first: any = guard.next()
    first.task = {
      abort: () => {
        throw new Error('already finished')
      },
    }
    expect(() => guard.next()).not.toThrow()
    expect(first.aborted).toBe(true)
  })

  it('cancel() 作废在飞请求，之后 next() 仍可用', () => {
    const guard = latestGuard()
    const first = guard.next()
    guard.cancel()
    expect(first.aborted).toBe(true)
    expect(guard.isCurrent(first)).toBe(false)
    const next = guard.next()
    expect(guard.isCurrent(next)).toBe(true)
  })
})

describe('isSuperseded', () => {
  it('认 SupersededError 与同名跨模块实例，不认真实错误', () => {
    expect(isSuperseded(new SupersededError())).toBe(true)
    expect(isSuperseded({ name: 'SupersededError' })).toBe(true)
    expect(isSuperseded(new Error('request:fail'))).toBe(false)
    expect(isSuperseded({ code: 1, msg: '参数错误' })).toBe(false)
    expect(isSuperseded(null)).toBe(false)
  })
})
