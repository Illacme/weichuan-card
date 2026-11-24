// audit.ts 会 import ./request（HTTP 客户端 + 401 跳转），而它又牵出 router / store /
// element-plus 一整条依赖链——本机内存跑 vitest 会直接被杀（exit 137）。
// 本用例只关心动作常量表这张静态数据，把请求层打桩即可，不必拉起整棵依赖树。
vi.mock('./request', () => ({
  default: { get: vi.fn(), post: vi.fn() },
  toPaged: vi.fn(),
}))

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { ACTION_LABELS, actionLabel } from './audit'

// 跨端守门：后端 internal/audit 的动作常量必须与前端中文名表一一对应。
// 后端新增埋点而前端没补中文名 → 界面上会冒出 setting.update 这样的原始键，
// 运营看不懂；反过来前端多出后端没有的键，则是筛了永远不会有结果的选项。
// 与其靠人记着同步，不如让测试去读 Go 源码比对。
// cwd 是 frontend（vitest 项目根），跨到 backend 读 Go 源码。
// 不用 __dirname：ESM 里没有它。
const AUDIT_GO = resolve(process.cwd(), '../backend/internal/audit/audit.go')

function backendActions(): string[] {
  const src = readFileSync(AUDIT_GO, 'utf8')
  const out = new Set<string>()
  // 形如 ActionEmployeeCreate = "employee.create"
  const re = /Action\w+\s*=\s*"([^"]+)"/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) out.add(m[1])
  return [...out].sort()
}

describe('审计动作中文名与后端埋点对齐', () => {
  const backend = backendActions()

  it('能读到后端动作常量（守门本身有效，避免解析失败后空跑通过）', () => {
    expect(backend.length).toBeGreaterThan(10)
    expect(backend).toContain('setting.update')
  })

  it('后端每个已埋点的动作都有中文名', () => {
    const missing = backend.filter((a) => !ACTION_LABELS[a])
    expect(missing, `缺少中文名：${missing.join('、')}`).toEqual([])
  })

  it('前端没有后端不存在的动作（否则是永远筛不到结果的选项）', () => {
    const extra = Object.keys(ACTION_LABELS).filter((a) => !backend.includes(a))
    expect(extra, `后端没有该动作：${extra.join('、')}`).toEqual([])
  })

  // 键对齐只是承诺的一半。`'setting.update': 'setting.update'` 能让上面两条全绿，
  // 但审计页会直接把英文动作名摆给运营看——"有中文名" 没兑现。
  // 与 scripts/check_audit_labels.mjs 的校验同一判据（那边有 --selftest 证明会红）。
  it('每个中文名都含汉字（不能拿英文键值兜底冒充）', () => {
    const notChinese = Object.entries(ACTION_LABELS)
      .filter(([, v]) => !/[\u4e00-\u9fff]/.test(v))
      .map(([k, v]) => `${k} → ${JSON.stringify(v)}`)
    expect(notChinese, `这些动作名不含汉字：${notChinese.join('、')}`).toEqual([])
  })

  it('未知动作回落原始键而不是空白/崩溃', () => {
    expect(actionLabel('login.fail')).toBe('登录失败')
    expect(actionLabel('some.new.action')).toBe('some.new.action')
  })
})
