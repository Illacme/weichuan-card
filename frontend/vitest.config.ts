import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// 单元测试配置（Vitest）。
// - jsdom 环境：前端逻辑（localStorage / DOM 占位）需要在类浏览器环境跑。
// - 复用 vite 的 '@' -> src 别名，保证测试里的 import 与生产代码一致。
// - 同时纳入 miniprogram/tests 下的小程序端纯逻辑测试（richtext 解析 / request
//   刷新重试单飞锁）：这些是不依赖 wx / DOM 的纯 TS 模块，挂在这里统一运行，
//   避免为小程序包再引入一套 Node 工具链（miniprogram 保持零依赖）。
export default defineConfig({
  plugins: [vue()],
  // 与 vite.config.ts 的 define 对齐：MpTheme.vue 的 previewUrl 会读这个构建戳拼 `?v=`，
  // 单测里没有真实的「构建」动作，给一个固定值即可（断言只看路径前缀）。
  define: {
    __APP_BUILD__: JSON.stringify('test'),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // 允许加载项目根之外的文件：小程序端测试（../miniprogram/tests）在本配置下运行
  server: {
    fs: {
      strict: false,
      allow: ['..'],
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.spec.ts', '../miniprogram/tests/**/*.spec.ts'],
    // 测试不依赖真实后端，统一关掉网络与副作用
    clearMocks: true,
    restoreMocks: true,
  },
})
