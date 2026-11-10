import axios from 'axios'

/**
 * 「同一页面内连续发多次查询」的竞态守门。
 *
 * 为什么放在 api 层而不是让每个页面自己数序号：
 *  - axios 原生支持 `signal`，传进去能**真取消**在途请求，不只是让旧响应被忽略
 *    （旧响应仍会占满带宽、仍会跑完拦截器，序号法只挡住了最后一步写回）；
 *  - 序号 + isCurrent 仍保留：取消是异步的，旧请求可能在 abort 之前就已经 resolve，
 *    finally 也必须判断谁有权清 loading，否则新请求会被旧请求提前摘掉转圈。
 *
 * 用法（每个页面一个实例，不要跨页面共享）：
 * ```ts
 * const guard = latestOnly()
 * onUnmounted(() => guard.cancel())
 * async function load() {
 *   const signal = guard.next()
 *   loading.value = true
 *   try {
 *     const r = await adminStats(range, { signal })
 *     if (!guard.isCurrent(signal)) return
 *     // 写回数据
 *   } catch (e) {
 *     if (isSuperseded(e)) return  // 被下一次查询取消：不是错误，不提示
 *   } finally {
 *     if (guard.isCurrent(signal)) loading.value = false
 *   }
 * }
 * ```
 *
 * 不传 signal 的请求行为完全不变——现有调用点一个都不用改。
 */
export interface LatestGuard {
  /** 开启新一轮：取消上一轮在途请求，返回本轮的 signal。 */
  next(): AbortSignal
  /** 本轮是否仍是最新一轮。写回数据与清 loading 之前都要判。 */
  isCurrent(signal: AbortSignal): boolean
  /** 组件卸载 / 登出时调用，取消在途请求并作废所有在飞回调。 */
  cancel(): void
}

export function latestOnly(): LatestGuard {
  let controller: AbortController | null = null
  return {
    next() {
      if (controller) controller.abort()
      const current = new AbortController()
      controller = current
      return current.signal
    },
    isCurrent(signal: AbortSignal) {
      return controller !== null && controller.signal === signal
    },
    cancel() {
      if (!controller) return
      controller.abort()
      // 置空后 isCurrent 对所有旧 signal 都为 false：
      // 卸载后才回来的响应不能再写回已销毁组件的状态。
      controller = null
    },
  }
}

/**
 * 被「后一次查询」取消的错误。也认小程序的 SupersededError（同名字符串），
 * 让两端页面能写同一句 `if (isSuperseded(e)) return`。
 * 用 name/code 判定而非 instanceof：小程序测试会 resetModules，
 * 跨模块实例的 instanceof 会失效。
 */
export function isSuperseded(e: unknown): boolean {
  if (axios.isCancel(e)) return true
  const anyErr = e as { code?: string; name?: string } | null
  return anyErr?.code === 'ERR_CANCELED' || anyErr?.name === 'SupersededError'
}
