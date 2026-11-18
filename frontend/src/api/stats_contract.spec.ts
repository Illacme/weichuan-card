// 埋点字典三端对账：后端白名单 ↔ 后台中文名 ↔ 小程序上报类型/来源标签。
//
// 这三处取值分散在三个目录，当前一致，但没有任何自动守门：后端加一个事件而后台
// 没加中文名，明细表就显示裸英文；小程序上报一个后端不认的类型，只会静默丢数据。
// 与其靠人工记得同步，不如读源码做集合比对。
//
// 与 audit.spec.ts 同一条纪律：抓取型守门必须自带「至少抓到 N 个」量尺，
// 否则两边正则一起失配时会 missing=[]、extra=[] 恒绿。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const GO = readFileSync(resolve(root, 'backend/internal/handler/stat.go'), 'utf8')
const TRACK_TS = readFileSync(resolve(root, 'miniprogram/utils/track.ts'), 'utf8')
const STATS_TS = readFileSync(resolve(root, 'frontend/src/api/stats.ts'), 'utf8')
const DETAIL_VUE = readFileSync(resolve(root, 'frontend/src/components/StatDetail.vue'), 'utf8')

function block(src: string, start: RegExp, openChar: string, closeChar: string): string {
  const m = src.match(start)
  if (!m) return ''
  const from = (m.index ?? 0) + m[0].length
  const at = src.indexOf(openChar, from)
  if (at < 0) return ''
  const close = src.indexOf(closeChar, at)
  return close < 0 ? '' : src.slice(at, close + 1)
}

function quotedKeys(src: string): string[] {
  return [...src.matchAll(/['"]([\w]+)['"]/g)].map((m) => m[1])
}

function bareKeys(src: string): string[] {
  return [...src.matchAll(/^\s*([a-zA-Z_]\w*)\s*:/gm)].map((m) => m[1])
}

const goEvents = quotedKeys(block(GO, /var statEventTypes = map\[string\]bool/, '{', '}'))
const goSources = quotedKeys(block(GO, /var statSourceTypes = map\[string\]bool/, '{', '}'))
const mpEvents = quotedKeys(block(TRACK_TS, /const TRACK_EVENTS[^=]*=/, '[', ']'))
const mpSources = bareKeys(block(TRACK_TS, /export const SOURCE_LABELS[^=]*=/, '{', '}'))
const adminEvents = [...block(DETAIL_VUE, /const eventTypes =/, '[', ']').matchAll(/value:\s*'([^']+)'/g)].map((m) => m[1])
const adminSources = bareKeys(block(STATS_TS, /export const SOURCE_LABELS[^=]*=/, '{', '}'))

describe('埋点事件 / 来源字典三端一致', () => {
  it('量尺自检：六处字典都真的抓到了（防正则整体失配）', () => {
    for (const [name, keys] of Object.entries({
      goEvents, goSources, mpEvents, mpSources, adminEvents, adminSources,
    })) {
      expect(keys.length, `${name} 抓不到任何键，守门会空转`).toBeGreaterThan(0)
    }
    expect(goEvents).toHaveLength(8)
    expect(goSources).toHaveLength(6)
  })

  it('事件类型：后端白名单 = 小程序上报 = 后台明细中文名', () => {
    expect([...goEvents].sort()).toEqual([...mpEvents].sort())
    expect([...goEvents].sort()).toEqual([...adminEvents].sort())
  })

  it('来源类别：后端白名单 = 小程序标签 = 后台分布标签', () => {
    expect([...goSources].sort()).toEqual([...mpSources].sort())
    expect([...goSources].sort()).toEqual([...adminSources].sort())
  })
})
