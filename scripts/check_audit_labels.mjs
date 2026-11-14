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
// 用法：node scripts/check_audit_labels.mjs
//   退出码 0 = 一一对应；非 0 = 有缺失/多余，并打印修复指引。

import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')
const AUDIT_GO = resolve(root, 'backend/internal/audit/audit.go')
const AUDIT_TS = resolve(root, 'frontend/src/api/audit.ts')

// 后端：形如 ActionEmployeeCreate = "employee.create"
function backendActions() {
  const re = /Action\w+\s*=\s*"([^"]+)"/g
  const out = new Set()
  let m
  while ((m = re.exec(goSrc))) out.add(m[1])
  return [...out].sort()
}

// 前端：只取 ACTION_LABELS 对象块内的键，避免误抓文件里其它 'x': 结构。
// 键既可能是单引号串（'setting.update':）也可能是裸标识符（logout:）——两种都要抓。
function frontendLabelKeys() {
  const block = tsSrc.match(/export const ACTION_LABELS[^{]*\{([\s\S]*?)\n\}/)
  const body = block ? block[1] : ''
  const re = /^\s*(?:'([^']+)'|([A-Za-z_$][\w$]*))\s*:/gm
  const out = new Set()
  let m
  while ((m = re.exec(body))) out.add(m[1] || m[2])
  return out
}

let goSrc, tsSrc
try {
  goSrc = readFileSync(AUDIT_GO, 'utf8')
  tsSrc = readFileSync(AUDIT_TS, 'utf8')
} catch (e) {
  // 本地环境问题（文件缺失）不阻断推送，交给 CI；真正的门禁仍在 CI。
  console.error(`[audit-labels] 读取审计文件失败（${e.code}），跳过检查（CI 仍会拦）：${AUDIT_GO} / ${AUDIT_TS}`)
  process.exit(0)
}

const backend = backendActions()
const labels = frontendLabelKeys()

const missing = backend.filter((a) => !labels.has(a))
const extra = [...labels].filter((a) => !backend.includes(a))

if (missing.length === 0 && extra.length === 0) {
  console.log('[audit-labels] 后端审计动作与前端中文名一一对应，通过。')
  process.exit(0)
}

let failed = false
if (missing.length > 0) {
  failed = true
  console.error(`[audit-labels] 后端已埋点但前端缺少中文名的动作：${missing.join('、')}`)
  console.error('            请在 frontend/src/api/audit.ts 的 ACTION_LABELS 补上对应中文名。')
}
if (extra.length > 0) {
  failed = true
  console.error(`[audit-labels] 前端有、后端不存在的动作（永远筛不到结果）：${extra.join('、')}`)
  console.error('            请删掉 frontend/src/api/audit.ts 里多余的中文名键，或在后端补埋点。')
}
process.exit(failed ? 1 : 0)
