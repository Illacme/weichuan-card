import { describe, it, expect } from 'vitest'
import axios from 'axios'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { latestOnly, isSuperseded } from './latest'

// 源码级守门：这些页面都会因筛选 / 翻页 / 下拉 / 换关键字而连续重查，
// 任何一个漏接守门都会退回「旧响应盖掉新结果」。
//
// 这里曾只有 6 条硬编码路径，靠人肉「新增同类页面时把路径补进数组」——
// 结果 pages/team/team.ts（有 onSearch / onClearSearch / onPullDownRefresh 三个
// 连查入口）一直漏在清单外且不接守门，CI 全绿。下面的自动发现 + 双向断言
// 就是把「清单本身漏页」也变成红灯。
const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const RACY_PAGES = [
  'frontend/src/views/Stats.vue',
  'frontend/src/components/StatDetail.vue',
  'frontend/src/components/MPReleaseDialog.vue',
  'miniprogram/pages/project/list/list.ts',
  'miniprogram/pages/team/all.ts',
  'miniprogram/pages/team/team.ts',
  'miniprogram/pages/employee/stat/stat.ts',
]
const GUARD_TOOL_FILE = 'frontend/src/api/latest.ts' // 守门工具自身的定义，不算消费方

function walk(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = resolve(dir, name)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (/\.(ts|vue)$/.test(name) && !name.endsWith('.spec.ts')) out.push(p)
  }
  return out
}

// 剥掉整行注释：守门扫的是代码，不是注释里解释「为什么不能用某写法」的文字。
function stripLineComments(src: string): string {
  return src.split('\n').filter((l) => !/^\s*\/\//.test(l)).join('\n')
}

const PROD_FILES = [
  ...walk(resolve(ROOT, 'frontend/src')),
  ...walk(resolve(ROOT, 'miniprogram/pages')),
].filter((p) => !p.includes(`${sep}tests${sep}`))

const SRC = new Map(PROD_FILES.map((p) => [p, stripLineComments(readFileSync(p, 'utf8'))] as const))
const rel = (p: string) => relative(ROOT, p).split(sep).join('/')

describe.each(RACY_PAGES)('%s 竞态守门静态检查', (file) => {
  it('接入了守门工具，且响应写回前有 isCurrent 判定', () => {
    const src = readFileSync(resolve(ROOT, file), 'utf8')
    // 「至少读到东西」兜底：路径写错 readFileSync 直接抛错，这里再挡一次空文件。
    // 不判「是否调用了 request」——前端页面走 http / 封装 api，不叫这个名。
    expect(src.length).toBeGreaterThan(100)
    expect(/latestOnly|latestGuard/.test(src)).toBe(true)
    expect(/isCurrent\(/.test(src)).toBe(true)
  })
})

describe('竞态守门清单的完整性（防清单外漏页）', () => {
  it('量尺自己有读数：扫描确实抓到了生产文件与关键字连查页', () => {
    // 抓取型守门最坏的坏法是正则写错、一条也抓不到 → 于是「全部通过」。
    expect(PROD_FILES.length).toBeGreaterThan(20)
    expect(RACY_PAGES.length).toBeGreaterThanOrEqual(7)
    const keywordPages = PROD_FILES.filter((p) => {
      const s = SRC.get(p) || ''
      return /onSearch\s*\(|onClearSearch\s*\(/.test(s) && /request\s*[<(]/.test(s)
    })
    expect(keywordPages.length).toBeGreaterThanOrEqual(2)
  })

  it('每个「关键字连查页」都登记在清单里，且真的接了守门', () => {
    // 换关键字会让同一接口返回完全不同的结果，旧响应盖回来是访客能看见的错误
    // （搜 A 却显示 B 的人）。下拉刷新同一资源不在这一档：文档明确不做全站取消。
    const keywordPages = PROD_FILES.filter((p) => {
      const s = SRC.get(p) || ''
      return /onSearch\s*\(|onClearSearch\s*\(/.test(s) && /request\s*[<(]/.test(s)
    })
    for (const p of keywordPages) {
      const f = rel(p)
      expect(RACY_PAGES, `关键字连查页 ${f} 漏登记进 RACY_PAGES`).toContain(f)
      const s = SRC.get(p) || ''
      expect(/latestOnly|latestGuard/.test(s), `${f} 未接入守门工具`).toBe(true)
      expect(/isCurrent\(/.test(s), `${f} 响应写回前没有 isCurrent 判定`).toBe(true)
    }
  })

  it('反向：接了守门的生产页面必须都在清单里（接了没登记 = 清单漂移）', () => {
    const guarded = PROD_FILES.filter((p) => rel(p) !== GUARD_TOOL_FILE)
      .filter((p) => {
        const s = SRC.get(p) || ''
        return /isCurrent\(/.test(s) && /(latestGuard|latestOnly)\s*\(/.test(s)
      })
      .map(rel)
      .sort()
    expect(guarded.length).toBeGreaterThanOrEqual(7)
    // 双向相等：既防「接了没登记」，也防「登记了一堆却没接」的摆设清单
    expect(guarded).toEqual([...RACY_PAGES].sort())
  })
})

describe('latestOnly（前端请求竞态守门）', () => {
  it('next() 真取消上一轮 signal，且只认最后一轮为当前', () => {
    const guard = latestOnly()
    const first = guard.next()
    expect(first.aborted).toBe(false)
    expect(guard.isCurrent(first)).toBe(true)

    const second = guard.next()
    // 取消必须是「真 abort」而不是只翻个布尔：axios 会把在途请求断掉，
    // 否则旧响应仍会跑完整条拦截器链，浪费带宽还可能触发错误提示。
    expect(first.aborted).toBe(true)
    expect(second.aborted).toBe(false)
    expect(guard.isCurrent(first)).toBe(false)
    expect(guard.isCurrent(second)).toBe(true)
  })

  it('cancel() 取消在途请求，并让所有旧 signal 都不再是当前', () => {
    const guard = latestOnly()
    const signal = guard.next()
    guard.cancel()
    expect(signal.aborted).toBe(true)
    expect(guard.isCurrent(signal)).toBe(false)
    // 卸载后即使又有响应回来，fake signal 也不能冒充当前轮
    expect(guard.isCurrent(new AbortController().signal)).toBe(false)
  })

  it('cancel() 可重复调用，之后再 next() 仍能正常工作', () => {
    const guard = latestOnly()
    guard.cancel()
    guard.cancel()
    const signal = guard.next()
    expect(guard.isCurrent(signal)).toBe(true)
    expect(signal.aborted).toBe(false)
  })
})

describe('isSuperseded', () => {
  it('认 axios 的 CanceledError / ERR_CANCELED 与小程序同名错误', () => {
    expect(isSuperseded(new axios.CanceledError('canceled'))).toBe(true)
    expect(isSuperseded({ code: 'ERR_CANCELED' })).toBe(true)
    expect(isSuperseded({ name: 'SupersededError' })).toBe(true)
  })

  it('不把真实网络错误 / 业务失败误判成取消（否则错误会被静默吞掉）', () => {
    expect(isSuperseded(new Error('Network Error'))).toBe(false)
    expect(isSuperseded({ code: 1, msg: '参数错误' })).toBe(false)
    expect(isSuperseded(null)).toBe(false)
    expect(isSuperseded(undefined)).toBe(false)
  })
})
