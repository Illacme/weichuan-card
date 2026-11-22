// 系统设置的跨端契约自动对账（三条），全部读源码比对，不靠人记着同步：
//   ① backend setting.publicKeys      ↔ miniprogram/utils/config.ts 的 PublicSettings
//   ② backend setting.Item 的 json tag ↔ frontend/src/api/settings.ts 的 SettingItem
//   ③ backend setting.registry        ↔ frontend/src/views/platform/Settings.spec.ts 的视图测试夹具
//
// 为什么直到现在才补：这三条此前都只靠注释「声称一致」。典型坏法不是报错，
// 而是**静默漂移**——后端给 publicKeys 加一个键，小程序 PublicSettings 不认，
// 而 config.ts 里 `data.miniprogram_base_url` 这种点号访问连 TS 报错都不会有
// （小程序 tsconfig 是 strict:false 且 tsc 不是门禁），现象只是「后台改了配置、
// 小程序没反应」，谁也不会往字段没声明上想。同理，把某个 registry 项的 group
// 从 storage 挪到别的组，视图测试用的是自己那份 43 行硬编码夹具，照样全绿。
//
// 抓取型守门最容易「改个写法就一条也抓不到 → 于是全部通过」，所以每一组都先有
// 「量尺自己有读数」用例：不仅要求数量下限，还点名必须抓到具体锚点。解析不到
// 结构、或出现未识别的常量引用，一律 throw，绝不退化成空串蒙混过关。
//
// 本文件不 import 任何生产模块，只读磁盘源码，因此不会被 request / element-plus
// 那棵依赖链拖进 OOM（与 audit.spec.ts、types_contract.spec.ts 同一处理）。
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../../..')
const GO_SETTING = resolve(REPO, 'backend/internal/setting/setting.go')
const GO_PII = resolve(REPO, 'backend/internal/pii/pii.go')
const MP_CONFIG = resolve(REPO, 'miniprogram/utils/config.ts')
const FE_API = resolve(REPO, 'frontend/src/api/settings.ts')
const FE_SPEC = resolve(REPO, 'frontend/src/views/platform/Settings.spec.ts')

/**
 * 只剥「整行注释」，不碰行尾注释与字符串内容。
 * setting.go 的 Desc 里有 https://cdn.example.com 和 {{thing6.DATA}}，
 * 若按 `//` 一刀切会把配置文案截断；但整行注释里又确实有 `{Key:` 这类字样
 * 的解释，不剥就会把它当成真配置抓进去。
 */
function stripLineComments(src: string): string {
  return src
    .split('\n')
    .filter((line) => !/^\s*(\/\/|\/\*|\*\/|\*)/.test(line))
    .join('\n')
}

function readClean(path: string): string {
  return stripLineComments(readFileSync(path, 'utf8'))
}

/** 取 Go struct 的 json tag（含 omitempty 标记），找不到结构直接抛错而不是返回空数组。 */
function goJSONTags(structName: string): { all: string[]; optional: string[] } {
  const src = readClean(GO_SETTING)
  const m = src.match(new RegExp('type ' + structName + ' struct \\{([\\s\\S]*?)\\n\\}'))
  if (!m) throw new Error(`setting.go 里找不到 struct ${structName}`)
  const tags = [...m[1].matchAll(/json:"([^"]+)"/g)].map((x) => x[1])
  const all = tags.map((t) => t.split(',')[0]).filter((n) => n && n !== '-').sort()
  const optional = tags.filter((t) => t.includes('omitempty')).map((t) => t.split(',')[0]).sort()
  return { all, optional }
}

/** 取 TS interface 的字段名与可选标记。 */
function tsInterface(path: string, name: string): { all: string[]; optional: string[] } {
  const src = readClean(path)
  const m = src.match(new RegExp('export interface ' + name + '[^\\{]*\\{([\\s\\S]*?)\\n\\}'))
  if (!m) throw new Error(`${path} 里找不到 interface ${name}`)
  const all: string[] = []
  const optional: string[] = []
  for (const line of m[1].split('\n')) {
    const f = line.match(/^\s*([A-Za-z_$][\w$]*)(\?)?\s*:/)
    if (!f) continue
    all.push(f[1])
    if (f[2]) optional.push(f[1])
  }
  return { all: all.sort(), optional: optional.sort() }
}

/** 公开下发的非敏感键（/api/public/settings 的白名单）。 */
function goPublicKeys(): string[] {
  const src = readClean(GO_SETTING)
  const m = src.match(/var publicKeys = map\[string\]bool\s*\{([\s\S]*?)\n\}/)
  if (!m) throw new Error('setting.go 里找不到 publicKeys 声明')
  return [...m[1].matchAll(/"([^"]+)"\s*:\s*true/g)].map((x) => x[1]).sort()
}

interface RegistryDef {
  key: string
  group: string
  type: string
  envVar: string
  secret: boolean
  restart: boolean
  readOnly: boolean
  pathKind: string
  options: string[]
}

/** 把 registry 的每条 def 解成结构化数据，供视图测试夹具逐字段对账。 */
function goRegistry(): RegistryDef[] {
  const src = readClean(GO_SETTING)
  const start = src.indexOf('var registry = []def{')
  if (start < 0) throw new Error('setting.go 里找不到 registry 声明')
  const end = src.indexOf('\n}', start)
  if (end < 0) throw new Error('setting.go 里找不到 registry 结束的 }')
  const body = src.slice(start, end)
  const starts = [...body.matchAll(/^[ \t]*\{Key:\s*"([^"]+)"/gm)]
  if (!starts.length) throw new Error('registry 一条 def 也没解析到')

  // registry 里有一项写的是 EnvVar: pii.KeyEnvVar（常量引用，不是字符串字面量）。
  // 早期版本把「解析不到」当成空串，结果夹具里正确的 'PII_ENC_KEY' 反而被判成漂移。
  // 现在按引用名去 pii 包取真值；遇到没见过的引用直接抛错，逼着补解析而不是静默放行。
  const piiSrc = readClean(GO_PII)
  const piiConst = piiSrc.match(/const KeyEnvVar = "([^"]+)"/)
  if (!piiConst) throw new Error('pii.go 里找不到 KeyEnvVar 常量')

  return starts.map((m, i) => {
    const stop = i + 1 < starts.length ? starts[i + 1].index : body.length
    const block = body.slice(m.index, stop)
    const grab = (field: string): string => {
      const x = block.match(new RegExp('\\b' + field + ':\\s*"([^"]*)"'))
      return x ? x[1] : ''
    }
    const env = block.match(/\bEnvVar:\s*(?:"([^"]*)"|([A-Za-z_][\w.]*))/)
    let envVar = ''
    if (env) {
      if (env[1] !== undefined) envVar = env[1]
      else if (env[2] === 'pii.KeyEnvVar') envVar = piiConst[1]
      else throw new Error(`registry 出现未识别的 EnvVar 常量引用：${env[2]}`)
    }
    const opt = block.match(/Options:\s*\[\]string\{([^}]*)\}/)
    return {
      key: m[1],
      group: grab('Group'),
      type: grab('Type'),
      envVar,
      secret: /\bSecret:\s*true/.test(block),
      restart: /\bRestart:\s*true/.test(block),
      readOnly: /\bReadOnly:\s*true/.test(block),
      pathKind: grab('PathKind'),
      options: opt ? [...opt[1].matchAll(/"([^"]+)"/g)].map((x) => x[1]) : [],
    }
  })
}

interface FixtureItem {
  key: string
  group: string
  type: string
  envVar: string
  secret: boolean
  restart: boolean
  readOnly: boolean
  pathKind: string
  options: string[]
}

/** 解 Settings.spec.ts 里的 `const items: SettingItem[] = [...]` 夹具（每项一行）。 */
function fixtureItems(): FixtureItem[] {
  const src = readClean(FE_SPEC)
  const m = src.match(/const items: SettingItem\[\] = \[([\s\S]*?)\n\]/)
  if (!m) throw new Error('Settings.spec.ts 里找不到 items 夹具')
  const out: FixtureItem[] = []
  for (const line of m[1].split('\n')) {
    if (!/^\s*\{\s*key:/.test(line)) continue
    const grab = (field: string): string => {
      const x = line.match(new RegExp("\\b" + field + ":\\s*'([^']*)'"))
      return x ? x[1] : ''
    }
    const flag = (field: string): boolean => new RegExp('\\b' + field + ':\\s*true').test(line)
    const opt = line.match(/\boptions:\s*\[([^\]]*)\]/)
    out.push({
      key: grab('key'),
      group: grab('group'),
      type: grab('type'),
      envVar: grab('envVar'),
      secret: flag('secret'),
      restart: flag('restart'),
      readOnly: flag('readOnly'),
      pathKind: grab('pathKind'),
      options: opt ? [...opt[1].matchAll(/'([^']*)'/g)].map((x) => x[1]) : [],
    })
  }
  return out
}

// ─────────────────────────────────────────────────────────────
describe('① 公开配置白名单：setting.publicKeys ↔ 小程序 PublicSettings', () => {
  const backend = goPublicKeys()
  const mini = tsInterface(MP_CONFIG, 'PublicSettings')
  const registry = goRegistry()

  it('量尺自己有读数：两侧都抓到键，且后端 registry 不是空壳', () => {
    expect(backend.length).toBeGreaterThanOrEqual(3)
    expect(backend).toEqual(['brand_name', 'miniprogram_base_url', 'pinned_company_id'])
    expect(mini.all.length).toBeGreaterThanOrEqual(3)
    expect(mini.all).toContain('miniprogram_base_url')
    expect(registry.length).toBeGreaterThan(40)
  })

  it('后端每个公开键都在小程序 PublicSettings 里声明（否则端上读不到）', () => {
    const missing = backend.filter((k) => !mini.all.includes(k))
    expect(missing, `小程序漏声明：${missing.join('、')}`).toEqual([])
  })

  it('小程序没有多声明后端不下发的字段（否则是永远读不到的死字段）', () => {
    const extra = mini.all.filter((f) => !backend.includes(f))
    expect(extra, `后端不下发：${extra.join('、')}`).toEqual([])
  })

  it('公开键全部在 registry 登记（Public() 直接 GetString，没登记就没有缺省值、后台也维护不了）', () => {
    const known = new Set(registry.map((d) => d.key))
    const unknown = backend.filter((k) => !known.has(k))
    expect(unknown, `registry 未登记：${unknown.join('、')}`).toEqual([])
  })

  it('公开键不得是敏感项或只读项（/api/public/settings 无需登录，泄的是全局配置）', () => {
    const byKey = new Map(registry.map((d) => [d.key, d]))
    const bad = backend.filter((k) => {
      const d = byKey.get(k)
      return d && (d.secret || d.readOnly)
    })
    expect(bad, `公开了敏感/只读项：${bad.join('、')}`).toEqual([])
  })
})

describe('② 后台设置 DTO：setting.Item 的 json tag ↔ SettingItem', () => {
  const backend = goJSONTags('Item')
  const frontend = tsInterface(FE_API, 'SettingItem')

  it('量尺自己有读数：两侧都抓到 13 个字段且含关键锚点', () => {
    expect(backend.all.length).toBeGreaterThanOrEqual(10)
    expect(frontend.all.length).toBeGreaterThanOrEqual(10)
    for (const anchor of ['key', 'value', 'type', 'group', 'readOnly', 'configured']) {
      expect(backend.all, `后端缺 ${anchor}`).toContain(anchor)
      expect(frontend.all, `前端缺 ${anchor}`).toContain(anchor)
    }
    expect(backend.optional).toContain('pathKind')
  })

  it('字段名双向一致（写错一个 json tag，界面就会静默拿到 undefined）', () => {
    expect(frontend.all).toEqual(backend.all)
  })

  it('omitempty ↔ 可选标记一致；name 是唯一被允许的额外例外', () => {
    // 后端 Name 没标 omitempty，但注释明确说「旧响应可能缺省，界面回落显示 key」，
    // 这是前端为兼容旧版后端保留的宽容，唯一允许比后端多出来的可选字段。
    // 其余字段若有人手滑标成 `?`，界面就会把必填元数据当可缺省处理。
    const allowedExtra = ['name']
    const extra = frontend.optional.filter((f) => !backend.optional.includes(f) && !allowedExtra.includes(f))
    expect(extra, `前端多标了可选：${extra.join('、')}`).toEqual([])
    const missing = backend.optional.filter((f) => !frontend.optional.includes(f))
    expect(missing, `后端 omitempty 但前端标成必填：${missing.join('、')}`).toEqual([])
    expect(frontend.optional).toContain('name')
  })
})

describe('③ 视图测试夹具：Settings.spec.ts 的 items ↔ setting.registry', () => {
  const registry = goRegistry()
  const fixture = fixtureItems()
  const byKey = new Map(registry.map((d) => [d.key, d]))

  it('量尺自己有读数：夹具不是三行玩具，且覆盖了大部分分组', () => {
    expect(registry.length).toBeGreaterThan(40)
    expect(fixture.length).toBeGreaterThanOrEqual(30)
    expect(fixture.map((f) => f.key)).toContain('platform_storage_profile_id')
    expect(fixture.map((f) => f.key)).toContain('db_dsn')
    expect(new Set(fixture.map((f) => f.group)).size).toBeGreaterThanOrEqual(8)
  })

  it('夹具里每个 key 都在 registry 登记（否则测试在测一个后端压根不存在的项）', () => {
    const unknown = fixture.map((f) => f.key).filter((k) => !byKey.has(k))
    expect(unknown, `夹具凭空造了：${unknown.join('、')}`).toEqual([])
  })

  it('夹具的 group/type/envVar/secret/restart/readOnly/pathKind 与后端逐项一致', () => {
    const fields: (keyof Omit<FixtureItem, 'key' | 'options'>)[] = [
      'group', 'type', 'envVar', 'secret', 'restart', 'readOnly', 'pathKind',
    ]
    const diffs: string[] = []
    for (const f of fixture) {
      const d = byKey.get(f.key)
      if (!d) continue // 上一条已经报过，别在这里重复刷屏
      for (const k of fields) {
        if (f[k] !== d[k]) diffs.push(`${f.key}.${k}: 夹具=${JSON.stringify(f[k])} 后端=${JSON.stringify(d[k])}`)
      }
    }
    expect(diffs, `夹具与后端漂移：\n${diffs.join('\n')}`).toEqual([])
  })

  it('夹具里的枚举候选值与后端 Options 同源（下拉能选但保存被拒是最难查的那类错）', () => {
    const withOptions = fixture.filter((f) => f.options.length > 0)
    expect(withOptions.length).toBeGreaterThan(0) // 量尺自己有读数
    const diffs: string[] = []
    for (const f of withOptions) {
      const d = byKey.get(f.key)
      if (!d || f.options.join(',') !== d.options.join(',')) {
        diffs.push(`${f.key}: 夹具=[${f.options.join(',')}] 后端=[${d ? d.options.join(',') : '无此项'}]`)
      }
    }
    // 反向也盯：后端有 Options 的项若夹具一项都没带，说明这条对账可能空转
    const backendEnums = registry.filter((d) => d.options.length > 0).map((d) => d.key)
    expect(backendEnums.length).toBeGreaterThan(0)
    expect(diffs, `枚举不同源：\n${diffs.join('\n')}`).toEqual([])
  })
})
