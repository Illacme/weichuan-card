// 前端报错上报：把浏览器侧异常对到后端 /api/client-error，服务端按 [CLIENT-ERROR] 前缀写日志，
// 并带上最近一次响应头的 X-Request-Id，与后端同一次请求的日志串起来排查。
//
// 设计要点：
// - 用原生 fetch，不依赖 axios 拦截器——拦截器在异常时可能自己也抛错，会形成上报死循环。
// - 用相对路径 /api/client-error：生产由 nginx 反代、开发由 vite 代理，都与业务接口同源，无需拼 baseURL。
// - DEV 下只 console.warn 不真正上报：本地开发报错本就一堆，打过去只会污染服务端日志，
//   真正的价值在 production。要本地验证上报可临时把 import.meta.env.DEV 当成 false 跑。
// - 上报本身包在 try/catch：万一 fetch 抛错（极端网络），绝不能反过来影响用户操作。

export interface ClientErrorPayload {
  message: string
  stack?: string
  url?: string
  line?: number
  column?: number
  level?: 'error' | 'warning' | 'unhandledrejection'
  env?: string
  requestId?: string
  userAgent?: string
  extra?: string
}

// 最近一次后端响应带回的 X-Request-Id（由 request.ts 拦截器写入），用于关联后端日志。
let lastRequestId = ''

export function setLastRequestId(id: string): void {
  lastRequestId = id || ''
}

export function reportClientError(p: ClientErrorPayload): void {
  const body = {
    message: p.message,
    stack: p.stack ?? '',
    url: p.url ?? location.href,
    line: p.line ?? 0,
    column: p.column ?? 0,
    level: p.level ?? 'error',
    env: import.meta.env.MODE,
    requestId: lastRequestId,
    userAgent: navigator.userAgent,
    extra: p.extra ?? '',
  }
  if (import.meta.env.DEV) {
    // 开发态：只本地留痕，不打到服务端（console 在这里是有意的；
    // 本仓库的 lint 配置不开 no-console，故不需要 disable 指令）
    console.warn('[CLIENT-ERROR][dev]', body)
    return
  }
  try {
    fetch('/api/client-error', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      keepalive: true, // 页面卸载（白屏崩溃）时也能尽力发出去
    }).catch(() => {
      /* 上报失败静默丢弃 */
    })
  } catch {
    /* 同上：绝不反向影响用户 */
  }
}
