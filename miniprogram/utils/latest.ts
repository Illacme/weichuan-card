// 「同一页面内连续发多次查询」的竞态守门（小程序端）。
//
// 刻意与 utils/request.ts 分开成纯逻辑模块：
//  - 页面 spec 普遍把 utils/request 整个 vi.mock 掉（只给 request/assetUrl/apiBaseUrl），
//    守门若放在那里，mock 里缺导出会直接炸；纯逻辑模块不依赖 wx，可在测试中直接用真实现，
//    乱序用例才能真正复现「旧响应后回来」。
//  - 请求封装只需要 RequestSignal 的形状，用 type-only 引用，不引入运行时环。

/** 一次查询的取消信号。由 latestGuard.next() 产生，传给 request 的第四个参数。 */
export interface RequestSignal {
  aborted: boolean
  /** wx.request 返回的 RequestTask；桩环境里可能没有，故可空。 */
  task?: any
}

/**
 * 被后一次查询取消。
 * 必须 reject 而不能让 Promise 永远悬着：onPullDownRefresh 里 await load()，
 * 悬挂会让 wx.stopPullDownRefresh() 永远不执行，下拉指示器卡在屏幕上。
 */
export class SupersededError extends Error {
  constructor() {
    super('superseded')
    // 用 name 而不是 instanceof 判定：小程序测试会 resetModules，
    // 跨模块实例的 instanceof 会失效（-target ES5 时继承链也会断）。
    this.name = 'SupersededError'
  }
}

export function isSuperseded(e: unknown): boolean {
  return !!e && (e as { name?: string }).name === 'SupersededError'
}

export interface LatestGuard {
  /** 开启新一轮：取消上一轮在途请求，返回本轮 signal。 */
  next(): RequestSignal
  /** 本轮是否仍是最新一轮。写回数据、清 loading 之前都要判。 */
  isCurrent(signal: RequestSignal): boolean
  /** 页面卸载 / 退出登录时调用。小程序没有 onUnmounted，供 onUnload 使用。 */
  cancel(): void
}

export function latestGuard(): LatestGuard {
  let current: RequestSignal | null = null

  function abort(signal: RequestSignal) {
    if (signal.aborted) return
    signal.aborted = true
    const task = signal.task
    if (task && typeof task.abort === 'function') {
      try {
        task.abort()
      } catch {
        // 请求已经结束、或桩环境不支持 abort：置了 aborted 就够了，
        // 旧响应回来时 success/fail 会被挡住。
      }
    }
  }

  return {
    next() {
      if (current) abort(current)
      current = { aborted: false }
      return current
    },
    isCurrent(signal: RequestSignal) {
      return current === signal && !signal.aborted
    },
    cancel() {
      if (current) abort(current)
      // 置空后旧 signal 一律不是当前轮：页面卸载后回来的响应不能再 setData
      current = null
    },
  }
}
