// Element Plus 深路径按需引入的源码级 / 行为级守门。
//
// 这一轮踩过的坑决定了守门必须查「行为」而不只是「文件在不在」：
//   - 上一轮五条瘦身路径实测 0 收益，根因是模板组件与命令式 API **两条独立来源**
//     都指向同一个 `element-plus/es` 桶入口，只掐一条另一条照样把整库拉回来；
//   - 只引「组件目录」的样式会静默丢掉 14 个复合组件的子样式（实测少 83 条
//     .el-form-item 选择器），构建全绿、页面零报错；
//   - 解析器遇到未知组件若返回 null，模板会渲染成不存在的自定义标签（空白块）。
//
// 所以这里直接 import 构建期模块、调用真实解析函数，并对账产物源头的每一个环节：
// 实际用到的 el-* 组件 → 官方导出表 → 组件深路径 → 全部样式目录 → 全局类型声明，
// 以及裸包导入符号 → 薄封装导出集合、vite 配置禁止回潮。
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import * as ep from '../../vite/ep-resolver.mjs'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf8')

function sourceFiles(dir: string, ext: RegExp, out: string[] = []): string[] {
  for (const name of readdirSync(resolve(ROOT, dir))) {
    const rel = join(dir, name)
    if (statSync(resolve(ROOT, rel)).isDirectory()) sourceFiles(rel, ext, out)
    else if (ext.test(name)) out.push(rel)
  }
  return out
}

const VUE_FILES = sourceFiles('src', /\.vue$/)
const TS_FILES = sourceFiles('src', /\.ts$/)
const ALL_FILES = [...VUE_FILES, ...TS_FILES]

function pascal(tag: string): string {
  return tag.split('-').map((s) => s.charAt(0).toUpperCase() + s.slice(1)).join('')
}

/** 从 SFC 模板里真实扫出用过的 <el-*>（别拿 components.d.ts 当真值，那是产物）。 */
function usedComponents(): string[] {
  const set = new Set<string>()
  for (const file of VUE_FILES) {
    for (const m of read(file).matchAll(/<el-([a-z0-9-]+)/g)) set.add(pascal(`el-${m[1]}`))
  }
  return [...set].sort()
}

/** 源码里 `from 'element-plus'` 的**运行时**值导入符号（type-only 不算）。 */
function runtimeSymbols(): string[] {
  const set = new Set<string>()
  for (const file of ALL_FILES) {
    const src = read(file)
    const re = /import\s+(type\s+)?\{([^}]+)\}\s+from\s+['"]element-plus['"]/g
    for (const m of src.matchAll(re)) {
      if (m[1]) continue // import type { ... } 整行都是类型，构建期会被擦除
      for (const raw of m[2].split(',')) {
        const sym = raw.trim()
        if (sym && !sym.startsWith('type ')) set.add(sym)
      }
    }
  }
  return [...set].sort()
}

interface ShimExport { name: string; from: string }
function shimExports(): ShimExport[] {
  return [...read('vite/element-plus-runtime.mjs').matchAll(
    /export\s*\{\s*([^}]+?)\s*\}\s*from\s*['"]([^'"]+)['"]/g,
  )].map((m) => ({ name: m[1].trim(), from: m[2] }))
}

const USED = usedComponents()

describe('Element Plus 深路径解析：组件与样式', () => {
  it('确实从 SFC 扫到了组件（防止空集合让后面全假绿）', () => {
    expect(USED.length).toBeGreaterThanOrEqual(40)
    expect(USED).toContain('ElConfigProvider') // App.vue 根组件，桶入口的触发点
    expect(USED).toContain('ElFormItem') // 复合组件代表
  })

  it('导出表可读且每个用到的组件都能定位到真实深路径', () => {
    const dirs = ep.componentDirectories()
    expect(Object.keys(dirs).length).toBeGreaterThan(100)
    for (const name of USED) {
      expect(dirs[name], `${name} 不在官方导出表里`).toBeTruthy()
      expect(ep.hasComponentIndex(dirs[name]), `${name} 的 index.mjs 不存在`).toBe(true)
    }
  })

  it('复合组件必须同时引「组件目录 + 子样式目录」，否则静默丢 CSS', () => {
    const dirs = ep.componentDirectories()
    const composite = USED.filter((name) => {
      const child = ep.kebabName(name)
      return child !== dirs[name] && ep.hasStyle(child)
    })
    // 14 个：aside/header/main、descriptions-item、dropdown-item/menu、
    // form-item、menu-item/sub-menu、option、radio-button/group、tab-pane、table-column
    expect(composite.length).toBe(14)
    for (const name of composite) {
      const styles = ep.styleDirectories(name, dirs)
      expect(styles, `${name} 漏了组件目录样式`).toContain(dirs[name])
      expect(styles, `${name} 漏了子样式目录 ${ep.kebabName(name)}（会丢 .el-* 选择器）`)
        .toContain(ep.kebabName(name))
      for (const d of styles) expect(ep.hasStyle(d), `${name} 的样式目录 ${d} 不存在`).toBe(true)
    }
  })

  it('组件 resolver 返回深路径且带齐样式，绝不回桶入口', () => {
    const resolvers = ep.createEPResolvers()
    const component = resolvers.find((r) => r.type === 'component')!
    const dirs = ep.componentDirectories()
    for (const name of USED) {
      const hit = component.resolve(name)
      expect(hit, `${name} 没被解析`).toBeTruthy()
      expect(hit!.name).toBe(name)
      expect(hit!.from).toBe(`element-plus/es/components/${dirs[name]}/index`)
      expect(hit!.from).not.toBe('element-plus/es')
      for (const d of ep.styleDirectories(name, dirs)) {
        expect(hit!.sideEffects, `${name} 缺 ${d} 的样式`).toContain(
          `element-plus/es/components/${d}/style/css`,
        )
      }
    }
  })

  it('未知组件必须抛错，不能静默返回 null 让模板渲染成空白', () => {
    const component = ep.createEPResolvers().find((r) => r.type === 'component')!
    expect(() => component.resolve('ElNotARealComponent')).toThrow(/未知|没有/)
    expect(component.resolve('RouterLink')).toBeNull()
  })
})

describe('v-loading 指令', () => {
  it('模板里确实用了 v-loading（防止用例随页面改版假绿）', () => {
    const hits = VUE_FILES.filter((f) => /\bv-loading=/.test(read(f)))
    expect(hits.length).toBeGreaterThanOrEqual(10)
  })

  it('指令解析到 loading 深路径并带样式', () => {
    const directive = ep.createEPResolvers().find((r) => r.type === 'directive')!
    expect(directive).toBeTruthy()
    const hit = directive.resolve('Loading')
    expect(hit).toBeTruthy()
    expect(hit!.name).toBe('ElLoadingDirective')
    expect(hit!.from).toBe('element-plus/es/components/loading/index')
    expect(hit!.sideEffects).toContain('element-plus/es/components/loading/style/css')
    expect(directive.resolve('NotADirective')).toBeNull()
  })
})

describe('裸 `element-plus` 导入与薄封装', () => {
  it('shim 导出集合与源码运行时导入符号逐字对账', () => {
    const runtime = runtimeSymbols()
    expect(runtime.length).toBeGreaterThan(0)
    const exports = shimExports().map((e) => e.name).sort()
    expect(runtime).toEqual(exports)
  })

  it('shim 每个导出都走组件深路径，绝不 re-export 桶入口', () => {
    const exports = shimExports()
    expect(exports.length).toBeGreaterThan(0)
    for (const e of exports) {
      expect(e.from).toMatch(/^element-plus\/es\/components\/[a-z-]+\/index$/)
    }
  })

  it('命令式 API 的 CSS 仍由 main.ts 显式引入', () => {
    const main = read('src/main.ts')
    expect(main).toContain('element-plus/es/components/message/style/css')
    expect(main).toContain('element-plus/es/components/message-box/style/css')
  })
})

describe('全局组件声明（dts 关闭后手工维护）', () => {
  it('实际用到的每个 el-* 组件都在 components.d.ts 里声明', () => {
    const dts = read('src/components.d.ts')
    for (const name of USED) {
      expect(dts, `${name} 缺全局声明，vue-tsc / IDE 会认不出模板组件`)
        .toMatch(new RegExp(`^\\s*${name}:\\s*typeof import`, 'm'))
    }
    expect(dts).toMatch(/vLoading:\s*typeof import/)
  })
})

describe('vite 配置禁止回潮', () => {
  it('vite.config.ts 用自定义 resolver、关 dts、alias 精确匹配裸包名', () => {
    const cfg = read('vite.config.ts')
    expect(cfg, '恢复官方解析器会把 823KB 桶入口拉回首屏').not.toContain('ElementPlusResolver')
    expect(cfg).toContain('resolvers: createEPResolvers()')
    expect(cfg, '自动 dts 写回会在受限构建环境超时，且会反复改写声明文件').toContain('dts: false')
    expect(cfg).toContain('epRuntimeShim')
    expect(cfg).toMatch(/find:\s*\/\^element-plus\$\//)
  })

  it('vitest 配置不给 element-plus 加 alias，25 个 vi.mock 才能照常生效', () => {
    const cfg = read('vitest.config.ts')
    expect(cfg).not.toContain('element-plus')
    const specFiles = TS_FILES.filter((f) => f.endsWith('.spec.ts'))
    const mocked = specFiles.filter((f) => /vi\.mock\(['"]element-plus['"]/.test(read(f)))
    expect(mocked.length, '这条守门依赖 mock 仍存在，全删了要重新设计').toBeGreaterThanOrEqual(20)
  })
})
