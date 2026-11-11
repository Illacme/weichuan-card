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
  it('FeaturedProject 是 projectBriefCols 投影：字段都在 ProjectCase 里，且不含 detail / album', () => {
    const model = goJSONFields('ProjectCase')
    const ts = tsFields('FeaturedProject')
    expect(ts.filter((f) => !model.includes(f))).toEqual([])
    expect(ts).not.toContain('detail')
    expect(ts).not.toContain('album')
  })

  it('ServiceBrief 是 serviceBriefCols 投影：字段都在 BizService 里，且不含 content', () => {
    const model = goJSONFields('BizService')
    const ts = tsFields('ServiceBrief')
    expect(ts.filter((f) => !model.includes(f))).toEqual([])
    expect(ts).not.toContain('content')
  })

  it('四个根级字段信封声明了根级字段（不在 data 里）', () => {
    expect(tsFields('CardResp')).toEqual(['company', 'employee', 'profile'])
    expect(tsFields('AchievementsResp')).toEqual(['categories', 'featured'])
    expect(tsFields('HomeLayoutResp')).toEqual([
      'bgImage', 'bgMask', 'brandColorId', 'order', 'styleId', 'templateId',
    ])
    // HomeLayoutResp 是根级信封：三个主题字段必须真实存在于 Go 模型的 json tag 里，
    // 否则端上会读到后端根本不下发的字段，主题功能静默失效。
    // P1 起后端根级下发两轴，templateId 保留作兼容派生列（端上 resolveTheme 会拆它）。
    for (const key of ['styleId', 'brandColorId', 'templateId']) {
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
