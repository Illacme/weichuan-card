import { createApp } from 'vue'
import { createPinia } from 'pinia'

import './styles/admin.css'
import './styles/responsive.css'

// Element Plus 按需引入后，组件 CSS 由 Components 插件按模板用量注入；
// 但 ElMessage / ElMessageBox 这类「命令式 API」不会出现在模板里，
// 必须在此手动引入其样式，否则弹窗无样式。每个组件的 style/css 已包含 base 变量。
import 'element-plus/es/components/message/style/css'
import 'element-plus/es/components/message-box/style/css'

import App from './App.vue'
import router from './router'
import { getSetupStatus, type SetupStatus } from './api/setup'
import { reportClientError } from './api/clientError'

// 首次部署引导 / 安装向导：后端在 DB_DSN 未提供时会进入「初始配置模式」（进程起来但不加载业务）。
// 生产环境前端由 nginx 托管，运维访问 80 端口看到的是本前端而非后端 /setup 页，
// 所以这里在挂载前先问一次状态：未配置就直接渲染向导页，避免「打开是登录页、
// 但接口全部报错、不知道差什么」。
//
// 两个组件都动态载入：正常登录后不该为「一辈子只用一次」的页面付首屏体积。
//   - installable=true（新后端）→ 安装向导：填表 → 测试连接 → 一键安装 → 自重启 → 进登录页；
//   - 否则（旧后端还没有安装接口）→ 只读引导页：只生成可复制的配置文本。
// 请求失败 / 接口不存在一律按「已配置」处理——不能因为一次网络抖动就挡住正常登录。
async function loadSetupComponent(status: SetupStatus) {
  if (status.installable) {
    return (await import('./components/SetupInstaller.vue')).default
  }
  return (await import('./components/SetupGuide.vue')).default
}

async function bootstrap() {
  const status = await getSetupStatus()
  const unconfigured = !!status && !status.configured
  // 说明：Element Plus 已改为「按需引入」（见 vite.config.ts 的 Components 插件），
  // 组件与样式在编译期按模板实际用量注入，不再全量 app.use(ElementPlus) 或引入整包 CSS。
  // 命令式 API（ElMessage / ElMessageBox）改为在各 .vue 与 src/api/request.ts 中显式 import，
  // 其样式已在本文件顶部手动引入。图标此前通过全局 barrel 注册；现改为各组件显式 import，
  // 因此此处不再注册全局图标，避免打入未使用的图标。
  const app = unconfigured
    ? createApp(await loadSetupComponent(status as SetupStatus), { status })
    : createApp(App)

  // 全局错误边界：捕获渲染/生命周期中的异常并上报但不重抛，
  // 避免 dev 模式重抛单个子组件异常从而冻结整棵响应式树
  // （#53 崩溃正是经此路径放大为关闭/下拉/开关失灵）。
  app.config.errorHandler = (err, instance, info) => {
    console.error('[Vue 运行时异常]', info, err)
    reportClientError({
      message: err instanceof Error ? err.message : String(err),
      stack: err instanceof Error ? err.stack : '',
      level: 'error',
      extra: typeof info === 'string' ? info : '',
    })
  }

  // 全局兜底：Vue errorHandler 抓不到的原生错误（脚本运行异常、资源加载失败）
  // 与未捕获的 Promise 拒绝（async 函数 / 接口调用链）。Vue 边界之外崩了也要报。
  window.onerror = (msg, src, line, col, err) => {
    reportClientError({
      message: typeof msg === 'string' ? msg : String(msg),
      stack: err?.stack,
      url: typeof src === 'string' ? src : location.href,
      line: typeof line === 'number' ? line : 0,
      column: typeof col === 'number' ? col : 0,
      level: 'error',
    })
  }
  window.addEventListener('unhandledrejection', (e: PromiseRejectionEvent) => {
    const r = e.reason
    reportClientError({
      message: r instanceof Error ? r.message : String(r),
      stack: r instanceof Error ? r.stack : '',
      level: 'unhandledrejection',
    })
  })

  app.use(createPinia())
  app.use(router)
  app.mount('#app')
}

bootstrap()
