/// <reference types="vite/client" />

// 构建期注入的版本戳（vite.config.ts 的 define）：给不带内容 hash 的静态资源
// （public/theme-previews/*.png）拼 `?v=` 用，破了缓存才不会把旧版式当新图喂给客户。
declare const __APP_BUILD__: string

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface ImportMetaEnv {
  readonly VITE_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
