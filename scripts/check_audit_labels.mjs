#!/usr/bin/env node
// 跨端守门：后端 internal/audit/audit.go 的审计动作常量，必须与前端
// frontend/src/api/audit.ts 的 ACTION_LABELS 中文名一一对应。
//
// 为什么要有这道独立脚本（而不是只靠 audit.spec.ts）：
//   audit.spec.ts 是 vitest 用例，只在 CI 跑；本仓库的 .githooks/pre-push 只在校验
//   frontend/* 改动时跑 vue-tsc，并不跑 vitest——于是「后端加了动作、前端漏了中文名」
//   会一路推到远端才被 CI 拦下（2026-11-14 真实发生：storage.config.rekey 缺中文名）。
//   这个脚本不依赖 vitest / node_modules，只用了 node 内置模块，接进 pre-push 后，
//   本地推送前就能拦下漏同步。与 audit.spec.ts 同一判据（读同一份 Go 源码比对）。
//
// 用法：
//   node scripts/check_audit_labels.mjs            # 真检查：两侧文件必须一一对应
//   node scripts/check_audit_labels.mjs --selftest # 自检：喂假样本，证明这道守门会红
//
// 为什么需要 --selftest（2026-11-18 补）：
//   本脚本是「抓取型守门」——两边都靠正则从源码抓键。最坏的坏法不是报错，而是
//   两边正则同时失配（例如有人重写了常量/字典写法），于是 missing=[]、extra=[]，
//   守门恒绿且无人察觉。自检在内存里喂「两侧都空」「两侧同步删锚点」等坏样本，
//   确认每一种都必须被判红；CI 与 pre-push 都会在真检查前先跑它。
//   同理，读文件失败不再跳过：本地文件都读不到时，这道守门没有任何存在意义，
//   必须 fail closed，而不是以「CI 仍会拦」为理由静默放过。

import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const AUDIT_GO = resolve(root, 'backend/internal/audit/audit.go')
const AUDIT_TS = resolve(root, 'frontend/src/api/audit.ts')

// 数量下限：只作为「正则整体失配」的兜底，不追着真实动作数跑。
// 当前两侧各 57 项，取 40 留足增删空间；低于它说明抓取规则坏了，而不是动作被删光了。
const MIN_BACKEND_ACTIONS = 40
const MIN_FRONTEND_LABELS = 40

// 锚点：从长期稳定、绝不删的核心动作里挑几个。
// 数量下限只能防「全空」，锚点能防「正则改抓了另一批键，数量仍然够」。
const ANCHOR_ACTIONS = ['setting.update', 'login.fail', 'employee.create', 'content.layout']

// 后端：形如 ActionEmployeeCreate = "employee.create"
function backendActions(goSrc) {
  const re = /Action\w+\s*=\s*"([^"]+)"/g
  const out = new Set()
  let m
  while ((m = re.exec(goSrc))) out.add(m[1])
  return [...out].sort()
}

// 前端：只取 ACTION_LABELS 对象块内的键，避免误抓文件里其它 'x': 结构。
// 键既可能是单引号串（'setting.update':）也可能是裸标识符（logout:）——两种都要抓。
function frontendLabelKeys(tsSrc) {
  const block = tsSrc.match(/export const ACTION_LABELS[^{]*\{([\s\S]*?)\n\}/)
  const body = block ? block[1] : ''
  const re = /^\s*(?:'([^']+)'|([A-Za-z_$][\w$]*))\s*:/gm
  const out = new Set()
  let m
  while ((m = re.exec(body))) out.add(m[1] || m[2])
  return out
}

// 唯一判据实现：selftest 与真检查共用，避免「自检测的是另一套逻辑」。
function check(goSrc, tsSrc) {
  const problems = []
  const backend = backendActions(goSrc)
  const labels = frontendLabelKeys(tsSrc)

  if (backend.length < MIN_BACKEND_ACTIONS) {
    problems.push(`后端只抓到 ${backend.length} 个审计动作（下限 ${MIN_BACKEND_ACTIONS}）：正则可能失配，或常量写法被改（读到的源码 ${goSrc.length} 字节）`)
  }
  if (labels.size < MIN_FRONTEND_LABELS) {
    problems.push(`前端只抓到 ${labels.size} 个中文名（下限 ${MIN_FRONTEND_LABELS}）：ACTION_LABELS 块或键写法可能变了（读到的源码 ${tsSrc.length} 字节）`)
  }
  for (const a of ANCHOR_ACTIONS) {
    if (!backend.includes(a)) problems.push(`量尺锚点失效：后端没抓到 ${a}（守门可能整体空转）`)
    if (!labels.has(a)) problems.push(`量尺锚点失效：前端没抓到 ${a}（守门可能整体空转）`)
  }

  const missing = backend.filter((a) => !labels.has(a))
  const extra = [...labels].filter((a) => !backend.includes(a))
  if (missing.length > 0) {
    problems.push(`后端已埋点但前端缺少中文名：${missing.join('、')}\n            请在 frontend/src/api/audit.ts 的 ACTION_LABELS 补上对应中文名。`)
  }
  if (extra.length > 0) {
    problems.push(`前端有、后端不存在的动作（永远筛不到结果）：${extra.join('、')}\n            请删掉 frontend/src/api/audit.ts 里多余的中文名键，或在后端补埋点。`)
  }
  return problems
}

// 读文件失败必须阻断：这里不是「环境问题」，是守门失去输入。
function load(goPath, tsPath) {
  return {
    goSrc: readFileSync(goPath, 'utf8'),
    tsSrc: readFileSync(tsPath, 'utf8'),
  }
}

function fail(lines) {
  for (const line of lines) console.error(`[audit-labels] ${line}`)
  process.exit(1)
}

// ── 自检：内存样本，绝不碰真实文件 ────────────────────────────────────
function selftest() {
  const fillerGo = Array.from({ length: 45 }, (_, i) => `  ActionFiller${i} = "filler.${i}"`).join('\n')
  const fillerTs = Array.from({ length: 45 }, (_, i) => `  'filler.${i}': '填充${i}',`).join('\n')
  const healthyGo = `const (\n  ActionSettingUpdate = "setting.update"\n  ActionLoginFail = "login.fail"\n  ActionEmployeeCreate = "employee.create"\n  ActionContentLayout = "content.layout"\n  ActionLogout = "logout"\n${fillerGo}\n)`
  const healthyTs = `export const ACTION_LABELS: Record<string, string> = {\n  'setting.update': '修改系统设置',\n  'login.fail': '登录失败',\n  'employee.create': '新增员工',\n  'content.layout': '修改首页布局',\n  logout: '登出',\n${fillerTs}\n}`

  const cases = [
    ['健康样本必须全绿', check(healthyGo, healthyTs), false],
    ['两侧同时解析为空必须红', check('', ''), true],
    ['后端一侧空必须红', check('', healthyTs), true],
    ['前端 ACTION_LABELS 改写法导致抓空必须红', check(healthyGo, healthyTs.replace('ACTION_LABELS', 'OTHER_LABELS')), true],
    ['前端漏一个中文名必须红', check(healthyGo, healthyTs.replace("  'employee.create': '新增员工',\n", '')), true],
    // 这条专测锚点：两侧同步删掉同一动作时 missing/extra 都是空，
    // 只有数量下限与锚点能拦住「两边一起漂移」的静默放行。
    ['两侧同步删掉锚点动作必须红', check(
      healthyGo.replace('  ActionEmployeeCreate = "employee.create"\n', ''),
      healthyTs.replace("  'employee.create': '新增员工',\n", ''),
    ), true],
  ]

  // 读文件失败也必须红：用不存在的路径，不创建任何临时文件。
  let loadFailed = false
  try {
    load(resolve(root, '__no_such_audit__.go'), resolve(root, '__no_such_audit__.ts'))
  } catch {
    loadFailed = true
  }
  cases.push(['读不到源文件必须红', loadFailed ? ['读取未抛错'] : [], true])

  const failures = cases.filter(([, problems, wantRed]) => wantRed !== (problems.length > 0))
  if (failures.length > 0) {
    fail(failures.map(([name]) => `自检失败：${name}（应当红却绿，或应当绿却红）——守门已不可信，禁止推送`))
  }
  console.log(`[audit-labels] 自检通过：${cases.length} 个样本全部按预期判定（含双空、锚点漂移、读文件失败）。`)
}

if (process.argv.includes('--selftest')) {
  selftest()
  process.exit(0)
}

let sources
try {
  sources = load(AUDIT_GO, AUDIT_TS)
} catch (e) {
  fail([`读取审计文件失败（${e.code || e.message}）： ${AUDIT_GO} / ${AUDIT_TS}`, '      守门失去输入，必须停下；请检查工作区是否完整。'])
}

const problems = check(sources.goSrc, sources.tsSrc)
if (problems.length > 0) fail(problems)
console.log('[audit-labels] 后端审计动作与前端中文名一一对应，通过。')
