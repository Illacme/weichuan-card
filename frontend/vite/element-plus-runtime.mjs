// 裸 `element-plus` 值导入的构建期替身（只被 vite.config.ts 的 resolve.alias 命中）。
//
// 源码里 54 处 `import { ElMessage, ElMessageBox } from 'element-plus'` 如果直连包根，
// 会拉进 es/index.mjs → es/component.mjs 聚合的 111 个组件（约 823KB 进首屏）。
// 这里只 re-export 两个真正用到的命令式 API，且都指向各自组件的深路径。
//
// 为什么不直接改那 54 处源码：25 个 spec 用 vi.mock('element-plus', ...) 造消息桩，
// 逐个改深路径要连带改测试，改动面近 80 处且容易漏。Vitest 用的是独立的
// vitest.config.ts（不读 vite.config.ts，alias 也没有这一条），所以测试仍走原包 +
// mock，行为逐字不变；dev 与 build 则统一走这里，不存在两套运行时实现。
//
// 样式：message / message-box 的 CSS 仍由 src/main.ts 显式引入，不在这里重复。
// 新增任何「从裸 element-plus 取值」的符号都必须同步加到这里，
// src/vite/ep-resolver.spec.ts 会对账源码导入符号与本文件导出集合，漏了必红。
export { ElMessage } from 'element-plus/es/components/message/index'
export { ElMessageBox } from 'element-plus/es/components/message-box/index'
