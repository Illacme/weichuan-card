import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'
import { fileURLToPath, URL } from 'node:url'

// PC 管理后台 Vite 配置
// 开发期通过 /api 代理把请求转发到本地 Go 后端（默认 8080），避免跨域。
//
// 代理目标统一由 VITE_API_BASE 推导：VITE_API_BASE 是「含 /api 前缀的完整基址」
// （运行时 axios / 素材预览图都以此为 base），取其去掉末尾 /api 后的源站作为代理目标，
// 这样仅维护一个环境变量即可，避免再保留一个 VITE_BACKEND_URL 造成双命名混淆。
// VITE_API_BASE 缺省 /api（相对当前源）时回落本机 8080。
const apiBase = process.env.VITE_API_BASE || 'http://127.0.0.1:8080/api'
const proxyTarget = apiBase.replace(/\/api\/?$/, '') || 'http://127.0.0.1:8080'

// 开发代理把 Origin 改写为目标源站：后端 setup 接口的 sameOrigin 同源检查会比对
// 请求的 Origin/Referer 与 Host（端口不同即不同源），而 vite 仅转发、浏览器对同源
// 5173 的请求不发 Origin、却带 5173 的 Referer，导致开发期所有 /api/setup/* 被误判跨源 403。
// 这里把 Origin 显式设为 proxyTarget（如 http://127.0.0.1:8080），让开发环境的请求对后端
// 显得同源，仅影响开发期，不动生产安全语义，也不改后端代码。
function rewriteOriginForProxy(proxy: any) {
  proxy.on('proxyReq', (proxyReq: any) => {
    proxyReq.setHeader('origin', proxyTarget)
  })
}

// 构建戳：注入给前端当静态资源的反缓存版本号（见 MpTheme.vue 的 previewUrl）。
//
// 为什么需要它：`public/theme-previews/` 下 36 张预览图的文件名是「风格--色系」组合键
// （`ink--brand-crimson.png`），**不含内容 hash**。Vite 只能给 /assets/ 下的产物加 hash，
// public/ 里的文件是原样拷贝。于是重出图后 URL 一点没变，浏览器与 CDN 按各自的默认
// 静态缓存（实测 Cloudflare 给的是 max-age=14400）继续喂旧图——客户在后台切换风格，
// 看到的是上一版的版式，而控制台、Network、日志里**一个错都不报**，属于典型静默失败。
// 每次构建换一个 `?v=`，URL 变了，任何一级缓存都自然失效。
// 一次只下发当前选中那一张（切换时再拉下一张），所以不存在「发版后全量重下 41MB」。
const buildStamp = new Date().toISOString().replace(/[-:TZ.]/g, '').slice(0, 12)

export default defineConfig({
  // 构建期常量：静态预览图的版本戳
  define: {
    __APP_BUILD__: JSON.stringify(buildStamp),
  },
  plugins: [
    vue(),
    // Element Plus 按需引入：自动扫描模板中的 el-* 组件并按需注册、注入对应 CSS，
    // 取代全量 app.use(ElementPlus) 与 import 'element-plus/dist/index.css'（约 361KB），
    // 首屏 CSS 体积显著下降。ElMessage / ElMessageBox 等命令式 API 改为「显式 import」
    // （见各 .vue 与 src/api/request.ts），其样式在 src/main.ts 中手动引入，避免依赖
    // AutoImport 的运行时注入（实测该注入在构建产物中未生效，会导致线上 ReferenceError）。
    Components({
      resolvers: [ElementPlusResolver({ importStyle: 'css' })],
      dts: 'src/components.d.ts',
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': {
        target: proxyTarget,
        changeOrigin: true,
        configure: rewriteOriginForProxy,
      },
      // 上传/采用接口返回相对路径 /uploads/<name>，开发态由本代理转发到后端静态目录，
      // 保证管理后台 <img src="/uploads/..."> 在 vite 源下也能取到图（生产态由同源/反代保证）。
      '/uploads': {
        target: proxyTarget,
        changeOrigin: true,
        configure: rewriteOriginForProxy,
      },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // 拆分体积较大的第三方依赖，提升缓存命中率与首屏加载速度：
        //   - wangeditor 富文本编辑器（约 800KB）单独成块，仅在编辑页按需加载
        //   - element-plus / vue 生态各自成块
        manualChunks(id) {
          if (id.includes('@wangeditor')) return 'wangeditor'
          if (id.includes('element-plus')) return 'element-plus'
          if (id.includes('node_modules')) return 'vendor'
        },
      },
    },
  },
})
