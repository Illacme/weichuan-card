// 打包配置守门：**测试代码与文档不得进包，运行时文件不得被排掉**。
//
// 为什么值得单独一条用例：`miniprogram/tests/` 有 470 KB 左右，`scripts/` 还有脚本，
// 它们被打进包不会报错、不会变丑，只会让主包白白胖一圈——而且每次新增一个 spec
// 都在继续长，没人会注意到。主包上限只有 2 MB，这部分占掉的是真金白银的额度。
//
// 这条配置第一次被发现是空的（`packOptions.ignore: []`），也就是说此前的上传
// 一直把 35 个 spec 一起发了出去。删掉 ignore 不会让任何功能坏掉，所以必须钉住。
//
// 2026-11-15 补充两个反向风险（同样都是「不会报错、只会静默出错」）：
//  ① 有人图省事往 ignore 里加 `pages` / `utils` / `sitemap.json` → 线上直接缺代码或
//     丢搜索索引，上传时照样提示「N 个文件未被打包」，看起来一切正常；
//  ② 某个运行时页面不小心 import 了 `tests/helpers/harness` → 本地测试全绿，
//     上传后该文件被 ignore 掉，真机上直接白屏。
// 这两条此前都靠人工 grep 核对，这里改成自动守门。
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')

function packIgnore(): Array<{ type: string; value: string }> {
  const cfg = JSON.parse(readFileSync(resolve(root, 'project.config.json'), 'utf8'))
  return (cfg.packOptions && cfg.packOptions.ignore) || []
}

function values(): string[] {
  return packIgnore().map((r) => r.type + ':' + r.value)
}

// 递归列出会被小程序打包的运行时源码目录（排除 tests / scripts 本身）。
function runtimeFiles(dir: string): string[] {
  const out: string[] = []
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = resolve(dir, e.name)
    if (e.isDirectory()) out.push(...runtimeFiles(p))
    else if (/\.(ts|js|wxml|wxss|json)$/.test(e.name)) out.push(p)
  }
  return out
}

describe('小程序打包配置', () => {
  // 量尺自己有读数：下面多条 not.toContain / toEqual([]) 型断言，若 packIgnore()
  // 解析失败返回空数组会全部恒绿（项目里踩过「抓取型守门一条都抓不到于是全绿」）。
  // 先证明规则真的被读到了，且至少 6 条（tests / scripts / README / 两挡 .DS_Store / 私有配置）。
  it('能读到 ignore 规则本身（守门不空转）', () => {
    const rules = packIgnore()
    expect(rules.length).toBeGreaterThanOrEqual(6)
    expect(rules).toContainEqual({ type: 'folder', value: 'tests' })
    expect(rules).toContainEqual({ type: 'folder', value: 'scripts' })
  })

  it('tests 目录不进包（470 KB 的 spec 不该发给用户）', () => {
    const hit = packIgnore().some(
      (r) => r.type === 'folder' && String(r.value).replace(/\/+$/, '') === 'tests'
    )
    expect(hit).toBe(true)
  })

  it('scripts 目录、README 与 macOS 隐藏文件不进包', () => {
    const vals = values()
    expect(vals).toContain('folder:scripts')
    expect(vals).toContain('file:README.md')
    // .gitignore 早就忽略 .DS_Store（第 2 行），但那只管入库、管不到「本地文件
    // 是否进小程序包」。miniprogram 根目录那个 6 KB 的 .DS_Store 要靠这条挡。
    expect(vals).toContain('file:.DS_Store')
    // 子目录里的 .DS_Store 同样进包；官方文档确认 packOptions.ignore 的 type
    // 支持 glob（工具 1.02.1809260+），所以用 **/ 递归匹配而不是逐层加 file 规则。
    expect(vals).toContain('glob:**/.DS_Store')
    // 开发者工具的个人配置：每台机器不同，不该随包发给客户。
    expect(vals).toContain('file:project.private.config.json')
  })

  // sitemap.json 是微信搜索索引配置，app.* 是入口，pages/utils/components 是全部运行时代码。
  // 这些一旦出现在 ignore 里，上传不会报错、只会静默缺功能，必须自动拦。
  it('ignore 不得排除运行时文件 / 目录（否则线上静默缺代码）', () => {
    const ignored = packIgnore().map((r) => String(r.value).replace(/\/+$/, ''))
    for (const must of ['pages', 'utils', 'components', 'app.json', 'app.ts', 'app.wxss', 'sitemap.json']) {
      expect(ignored.includes(must), `运行时文件 ${must} 被 packOptions.ignore 排除了`).toBe(false)
    }
  })

  it('运行时源码零引用 tests/ scripts/（被排除的东西不能是运行依赖）', () => {
    const files: string[] = [resolve(root, 'app.ts')]
    for (const d of ['pages', 'utils', 'components']) {
      files.push(...runtimeFiles(resolve(root, d)))
    }
    // 量尺自己有读数：目录没读到时下面的 filter 会返回空，恒绿
    expect(files.length).toBeGreaterThan(20)
    const refRE = /(?:from\s*['"]|require\s*\(\s*['"]|import\s*['"])[^'"]*(?:tests|scripts)\//
    const bad = files.filter((f) => refRE.test(readFileSync(f, 'utf8')))
    expect(bad.map((f) => f.slice(root.length + 1))).toEqual([])
  })
})
