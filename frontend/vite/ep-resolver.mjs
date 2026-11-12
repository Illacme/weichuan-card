// Element Plus 深路径按需解析器（构建期模块，只被 vite.config.ts 与守门测试引用）。
//
// 为什么不用官方 ElementPlusResolver：
//   它给每个 SFC 模板里的 <el-*> 注入的都是同一个 `element-plus/es` 桶入口
//   （见 unplugin-vue-components/dist/resolvers.mjs 的 resolveComponent$1）。
//   es/component.mjs 把 111 个组件聚合在一张表里，App.vue 用到
//   <el-config-provider>，初始图一旦引用桶入口，整库 ~823KB 就进首屏。
//   另一条独立来源是源码里 54 处 `import { ElMessage } from 'element-plus'`，
//   同样指向桶入口。**只修其中一条，另一条仍会把整库拉回来**——这正是此前
//   「五条瘦身路径实测 0 收益」的原因（见 docs/commercial-readiness.md）。
//
// 本模块负责模板组件 + 指令两条；裸 `element-plus` 值导入由 vite.config.ts 的
// resolve.alias 转到 ./element-plus-runtime.mjs 处理，两条必须同时存在。
//
// 设计要点：
//   1. 映射不手工维护：只读 EP 官方 es/component.mjs 的文本，解析
//      「组件名 → 组件目录」。升级 EP 自动跟随，也不会把桶入口 import 进构建图。
//   2. 复合组件要「组件目录 + 样式目录」双映射：ElFormItem / ElOption /
//      ElTabPane 等 14 个组件的导出在父目录，CSS 却只在同名子目录的 style/ 下
//      （子目录没有 index.mjs）。只引父目录会静默丢样式——实测少 83 条
//      .el-form-item 选择器，页面一个错都不报。
//   3. v-loading 是指令不是组件，24 处模板在用；漏了 loading 态会静默消失。
//   4. 任何解析失败都**抛错**，不返回 null：返回 null 会让模板渲染成不存在的
//      自定义标签，构建全绿、页面空白，属于典型静默失败。

import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)

export function elementPlusRoot() {
  return path.dirname(require.resolve('element-plus/package.json'))
}

export function componentPath(...parts) {
  return path.join(elementPlusRoot(), 'es', 'components', ...parts)
}

/** 解析官方导出表。读文本而非 import，避免把桶入口带进构建图。 */
export function componentDirectories() {
  const source = fs.readFileSync(path.join(elementPlusRoot(), 'es', 'component.mjs'), 'utf8')
  const dirs = {}
  const pattern = /import\s*\{([^}]+)\}\s*from\s*["']\.\/components\/([^/"']+)\/index\.mjs["']/g
  for (const match of source.matchAll(pattern)) {
    for (const raw of match[1].split(',')) {
      const name = raw.trim()
      if (name) dirs[name] = match[2]
    }
  }
  // 版本换了格式（比如改成 export * from）就立刻炸，别让空表静默产出一堆空组件。
  if (Object.keys(dirs).length < 50) {
    throw new Error('[ep-resolver] 无法从 element-plus/es/component.mjs 解析组件导出表，请核对 EP 版本')
  }
  return dirs
}

export function hasComponentIndex(dir) {
  return fs.existsSync(componentPath(dir, 'index.mjs'))
}

export function hasStyle(dir) {
  return fs.existsSync(componentPath(dir, 'style', 'css.mjs'))
}

/** ElDescriptionsItem -> descriptions-item */
export function kebabName(name) {
  return name.replace(/^El/, '').replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase()
}

/**
 * 组件需要的全部样式目录：父目录 + （若存在）同名子样式目录。
 * 子目录没有 index.mjs，所以组件导出只能从父目录取，样式两份都要引。
 */
export function styleDirectories(name, dirs = componentDirectories()) {
  const dir = dirs[name]
  if (!dir) throw new Error(`[ep-resolver] 未知 Element Plus 组件：${name}（官方导出表里没有）`)
  const out = [dir]
  const child = kebabName(name)
  if (child !== dir && hasStyle(child)) out.push(child)
  for (const d of out) {
    if (!hasStyle(d)) {
      throw new Error(`[ep-resolver] ${name} 的样式目录 ${d}/style/css 不存在，会静默丢样式`)
    }
  }
  return out
}

/** 模板组件：深路径 index + 全部样式 sideEffects */
function componentResolver(dirs) {
  return {
    type: 'component',
    resolve(name) {
      if (!/^El[A-Z]/.test(name)) return null
      const dir = dirs[name]
      if (!dir) {
        throw new Error(`[ep-resolver] 模板用到了官方导出表里没有的组件 ${name}，请核对拼写或 EP 版本`)
      }
      if (!hasComponentIndex(dir)) {
        throw new Error(`[ep-resolver] ${name} 的组件目录 ${dir}/index.mjs 不存在`)
      }
      return {
        name,
        from: `element-plus/es/components/${dir}/index`,
        sideEffects: styleDirectories(name, dirs).map(
          (d) => `element-plus/es/components/${d}/style/css`,
        ),
      }
    },
  }
}

/** 指令：目前只用到 v-loading；官方 resolver 也是把它解析成 ElLoadingDirective。 */
function directiveResolver() {
  return {
    type: 'directive',
    resolve(name) {
      if (name !== 'Loading') return null
      if (!hasComponentIndex('loading') || !hasStyle('loading')) {
        throw new Error('[ep-resolver] loading 指令的深路径或样式不存在')
      }
      return {
        name: 'ElLoadingDirective',
        from: 'element-plus/es/components/loading/index',
        sideEffects: ['element-plus/es/components/loading/style/css'],
      }
    },
  }
}

export function createEPResolvers() {
  const dirs = componentDirectories()
  return [componentResolver(dirs), directiveResolver()]
}
