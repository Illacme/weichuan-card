import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, resolve, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// 小程序端类型契约的对账守门。
//
// 为什么需要它：`miniprogram/tsconfig.json` 是 strict:false + 无 wx typings，
// `tsc --noEmit` 本来就有一堆基线错误、**不是门禁**，所以 `utils/types.ts` 里
// 写错字段名不会有任何人报错——上一版整整 12 个类型零引用、字段全部过时，
// 就是这样静默腐烂的。这个测试把 TS 字段与 Go 模型的 json tag 直接对账，
// 写错（比如把 `phone2` 写成 `phoneTwo`）立即变红。

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const GO_MODELS = resolve(REPO, 'backend/internal/model/models.go')
const GO_REPOSITORY = resolve(REPO, 'backend/internal/repository/repository.go')
const TS_TYPES = resolve(REPO, 'miniprogram/utils/types.ts')

/** 从 models.go 里取某个 struct 的 json 字段名（忽略 `json:"-"` 与 `,omitempty` 等后缀） */
function goJSONFields(structName: string): string[] {
  const src = readFileSync(GO_MODELS, 'utf-8')
  const m = src.match(new RegExp('type ' + structName + ' struct \\{([\\s\\S]*?)\\n\\}'))
  if (!m) throw new Error('models.go 里找不到 struct ' + structName)
  const out: string[] = []
  for (const raw of m[1].match(/json:"([^"]*)"/g) || []) {
    const name = raw.slice(6, -1).split(',')[0]
    if (name && name !== '-') out.push(name)
  }
  return out.sort()
}

/** 某个列名在 struct 上对应的 json 名（用于把 SQL 列清单翻译成出参字段名） */
function goColumnJSON(structName: string, column: string): string {
  const src = readFileSync(GO_MODELS, 'utf-8')
  const m = src.match(new RegExp('type ' + structName + ' struct \\{([\\s\\S]*?)\\n\\}'))
  if (!m) throw new Error('models.go 里找不到 struct ' + structName)
  for (const line of m[1].split('\n')) {
    // gorm 标签的写法是 column:name（不带引号），不是 column:"name"
    const col = line.match(/column:([^",;\s]+)/)
    const js = line.match(/json:"([^",]*)/)
    if (col && js && col[1].trim() === column) return js[1]
  }
  throw new Error(`struct ${structName} 上找不到列 ${column} 的 json 名`)
}

/**
 * 读 repository.go 里某个 `xxxBriefCols = "a, b, c"` 常量，翻译成出参 json 字段名。
 *
 * 为什么非要读这个常量：投影类型（FeaturedProject / ServiceBrief）的实际出参
 * 只由 Select(...) 这几列决定，跟模型有多少字段无关。只校验「TS 字段都在模型里」
 * 是个恒真的子集判断——后端往投影里加一列，端上不声明它照样绿，那个字段就静默
 * 用不上（反之端上多声明一个投影里没有的字段也绿，永远是 undefined）。
 */
function briefColsJSON(colsConst: string, structName: string): string[] {
  const src = readFileSync(GO_REPOSITORY, 'utf-8')
  const m = src.match(new RegExp(colsConst + '\\s*=\\s*"([^"]+)"'))
  if (!m) throw new Error('repository.go 里找不到 ' + colsConst)
  return m[1]
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean)
    .map((c) => goColumnJSON(structName, c))
    .sort()
}

/** 从 types.ts 里取某个 interface / type 字面量的字段名 */
function tsFields(typeName: string): string[] {
  const src = readFileSync(TS_TYPES, 'utf-8')
  // 兼容 `export interface X {` 与 `export type X = Envelope & {` 两种写法
  const m = src.match(
    new RegExp('export (?:interface|type) ' + typeName + '[^\\{]*\\{([\\s\\S]*?)\\n\\}')
  )
  if (!m) throw new Error('types.ts 里找不到类型 ' + typeName)
  const out: string[] = []
  for (const line of m[1].split('\n')) {
    const f = line.match(/^\s{2}([A-Za-z_][\w]*)\??:/)
    if (f) out.push(f[1])
  }
  return out.sort()
}

/** 递归收集小程序目录下的 .ts 源文件（跳过 tests 自身） */
function collectTs(dir: string, acc: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      if (name === 'tests' || name === 'node_modules') continue
      collectTs(p, acc)
    } else if (name.endsWith('.ts')) {
      acc.push(p)
    }
  }
  return acc
}

describe('小程序类型契约与 Go 模型对账', () => {
  it('Employee 字段与 model.Employee 的 json tag 完全一致', () => {
    expect(tsFields('Employee')).toEqual(goJSONFields('Employee'))
  })

  it('CompanyInfo 字段与 model.CompanyInfo 完全一致（含 mpEntryMode / mpEntryStaffId）', () => {
    expect(tsFields('CompanyInfo')).toEqual(goJSONFields('CompanyInfo'))
  })

  it('CompanyProfile 字段与 model.CompanyProfile 完全一致（含 videoAutoplay / videoBackupUrl）', () => {
    expect(tsFields('CompanyProfile')).toEqual(goJSONFields('CompanyProfile'))
  })

  it('Category 字段与 model.AchievementCategory 完全一致', () => {
    expect(tsFields('Category')).toEqual(goJSONFields('AchievementCategory'))
  })

  // 下面两个是**裁剪后的投影**，不是模型全量：后端用 projectBriefCols /
  // serviceBriefCols 只选了几列，端上就不该声明正文类字段。
  //
  // 断言必须与「投影列」严格相等，不能只查「字段都在模型里」：
  // 后者是恒真的子集判断，后端往投影里加一列（或端上多写一个投影里没有的字段）
  // 都不会红，那个字段就永远静默是 undefined。
  it('FeaturedProject 与 projectBriefCols 逐列相等（不是「在模型里就算」）', () => {
    expect(tsFields('FeaturedProject')).toEqual(briefColsJSON('projectBriefCols', 'ProjectCase'))
    // 正文类字段必须在投影外，否则列表页会白拉富文本
    expect(tsFields('FeaturedProject')).not.toContain('detail')
    expect(tsFields('FeaturedProject')).not.toContain('album')
  })

  it('ServiceBrief 与 serviceBriefCols 逐列相等', () => {
    expect(tsFields('ServiceBrief')).toEqual(briefColsJSON('serviceBriefCols', 'BizService'))
    expect(tsFields('ServiceBrief')).not.toContain('content')
  })

  it('四个根级字段信封声明了根级字段（不在 data 里）', () => {
    expect(tsFields('CardResp')).toEqual(['company', 'employee', 'profile'])
    expect(tsFields('AchievementsResp')).toEqual(['categories', 'featured'])
    expect(tsFields('HomeLayoutResp')).toEqual([
      'bgGradientStrength', 'bgImage', 'bgMask', 'brandColorId', 'categoryLimit',
      'featuredCount', 'featuredEffect', 'featuredRatio', 'logoAlign', 'logoSize', 'order',
      'showCategoryName', 'showFeaturedName', 'showServiceName', 'styleId',
    ])
    // HomeLayoutResp 是根级信封：主题两轴 (styleId / brandColorId) 必须真实存在于
    // Go 模型的 json tag 里，否则端上会读到后端根本不下发的字段，主题功能静默失效。
    // bgGradientStrength（顶部主色浓度）同属后端真实下发的展示配置，一并强约束。
    // 0049 三项（categoryLimit / featuredCount / featuredEffect）也必须真实存在于
    // Go 模型 json tag 且由公开接口下发，否则后台改了小程序却收不到，静默失效。
    // 0050 两个名称开关同理：端上按 showName 决定渲不渲染标题，后端不下发就永远是
    // undefined → 开关看起来「没反应」，而图片与版式完全正常，最难查的一类。
    // 旧 templateId 兼容列已随 0043 删除，端上不再保留可选字段。
    for (const key of [
      'styleId', 'brandColorId', 'bgGradientStrength',
      'categoryLimit', 'featuredCount', 'featuredEffect', 'featuredRatio',
      'showCategoryName', 'showFeaturedName', 'showServiceName',
      // 0053 两个 LOGO 档位：后端模型必须真的有这两列，否则后台保存成功而端上
      // 永远读到 undefined → 位置档三档看起来完全一样，且没有任何报错。
      'logoSize', 'logoAlign',
    ]) {
      expect(goJSONFields('HomeLayout'), key).toContain(key)
    }
  })

  it('types.ts 至少被一个业务文件 import（否则就是又一份零引用的死契约）', () => {
    const consumers = collectTs(resolve(REPO, 'miniprogram')).filter((p) =>
      /from\s+'[^']*\/utils\/types'/.test(readFileSync(p, 'utf-8'))
    )
    expect(consumers.length).toBeGreaterThan(0)
  })

  it('types.ts 里每个导出类型都被真实引用过（声明 + 至少 1 处使用）', () => {
    const src = readFileSync(TS_TYPES, 'utf-8')
    const names = Array.from(src.matchAll(/export (?:interface|type) ([A-Za-z_]\w*)/g)).map(
      (m) => m[1]
    )
    expect(names.length).toBeGreaterThan(0)
    const corpus = collectTs(resolve(REPO, 'miniprogram'))
      .map((p) => readFileSync(p, 'utf-8'))
      .join('\n')
    const dead = names.filter((n) => {
      const hits = corpus.match(new RegExp('\\b' + n + '\\b', 'g')) || []
      return hits.length < 2
    })
    expect(dead).toEqual([])
  })
})
