// 精选案例切页动画带 transform（会创建层叠上下文），iOS 上向上滚动时
// 曾盖住底部固定的「一键拨号 / 存入通讯录」按钮——静默失败：不会报错，
// 只是客户点不到按钮。
//
// 修法是把两端都显式钉住：正文里的精选区 z-index:0，底部操作栏 z-index:8。
// 8 的区间有讲究：必须 > 正文 / .pcat-bar 的 5，必须 < .mask 的 10、
// .pwd-dialog 的 11 和顶栏 .custom-nav 的 100，否则弹窗会被按钮压住。
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const FEATURED = readFileSync(
  resolve(REPO, 'miniprogram/components/featured-projects/index.wxss'),
  'utf-8'
)
const NAVWXSS = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.wxss'), 'utf-8')

function ruleBody(src: string, selector: string): string | undefined {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const tail = '(?:\\/\\*[\\s\\S]*?\\*\\/|\\/\\/[^\\n]*)?[ \\t]*(?:\\n|$)'
  const m = src.match(new RegExp('(?:^|\\n)[ \\t]*' + esc + '[ \\t]*\\{([^}]*)\\}[ \\t]*' + tail))
  return m ? m[1] : undefined
}
function decl(body: string | undefined, prop: string): string {
  if (!body) return ''
  const m = body.match(new RegExp('(?:^|;)\\s*' + prop + '\\s*:\\s*([^;]+)'))
  return m ? m[1].trim() : ''
}
function z(src: string, selector: string): number {
  const raw = decl(ruleBody(src, selector), 'z-index')
  // Number('') 也是 0：不先判空的话「z-index 被删掉」会伪装成「z-index 为 0」
  if (!raw) throw new Error(`${selector} 没写 z-index`)
  return Number(raw)
}

describe('底部操作栏与精选案例的层级', () => {
  it('量尺自检：四条基线规则都读得到，不是在空串上比大小', () => {
    for (const sel of ['.action-bar', '.pcat-bar', '.mask', '.pwd-dialog']) {
      expect(ruleBody(WXSS, sel), `${sel} 抓不到`).toBeTruthy()
    }
    expect(ruleBody(FEATURED, '.featured-paged'), '精选容器规则抓不到').toBeTruthy()
    expect(ruleBody(NAVWXSS, '.custom-nav'), '顶栏规则抓不到').toBeTruthy()
  })

  it('操作栏是 fixed 且显式 z-index，不再与正文比绘制顺序', () => {
    expect(decl(ruleBody(WXSS, '.action-bar'), 'position')).toBe('fixed')
    expect(z(WXSS, '.action-bar')).toBe(8)
  })

  it('精选区钉在正文层 0，压不过操作栏', () => {
    expect(z(FEATURED, '.featured-paged')).toBe(0)
    expect(z(WXSS, '.action-bar')).toBeGreaterThan(z(FEATURED, '.featured-paged'))
    // 风险来源仍在：切页动画的 transform 会创建层叠上下文，所以上面两条不能省
    expect(FEATURED).toContain('transform-origin')
    expect(FEATURED).toContain('transition: transform')
  })

  it('8 落在正确区间：高过吸顶分类条，低于各种弹窗与顶栏', () => {
    const bar = z(WXSS, '.action-bar')
    expect(bar).toBeGreaterThan(z(WXSS, '.pcat-bar'))
    expect(bar).toBeLessThan(z(WXSS, '.mask'))
    expect(bar).toBeLessThan(z(WXSS, '.pwd-dialog'))
    expect(bar).toBeLessThan(z(NAVWXSS, '.custom-nav'))
  })
})
