// 「查看案例」按钮下移（2026-11-19 客户反馈，标准均衡款）：
//   用 position:relative + top 做纯视觉位移，不写 margin、不动 .ec-line3 的
//   align-items——公司名那一行与卡片高度都保持原样。
//
// 为什么不用 margin-top：.ec-line3 是 align-items:center 的 flex 行，
// 给按钮加 margin 会把它算进交叉轴尺寸，可能连带把公司名也挤上去，
// 客户要的是「按钮单独往下一点」，不是整行重排。
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const SCRIPT = readFileSync(resolve(REPO, 'scripts/gen_theme_previews.py'), 'utf-8')

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

describe('标准均衡：「查看案例」按钮相对公司名下移', () => {
  it('量尺自检：基线行与按钮规则都抓得到', () => {
    expect(ruleBody(WXSS, '.emp-card .ec-line3'), '基线行抓不到').toBeTruthy()
    expect(ruleBody(WXSS, '.emp-card .case-btn'), '按钮基线抓不到').toBeTruthy()
    expect(ruleBody(WXSS, '.wc-style-default .emp-card .case-btn'), 'default 位移规则抓不到').toBeTruthy()
  })

  it('default 按钮 relative 下移 8rpx，且没写 margin 去推动公司名', () => {
    const off = ruleBody(WXSS, '.wc-style-default .emp-card .case-btn')
    expect(decl(off, 'position')).toBe('relative')
    expect(decl(off, 'top'), '要落到公司名与地址的中点').toBe('20rpx')
    expect(decl(off, 'margin-top'), '不能靠 margin 把整行撑高').toBe('')
    expect(decl(off, 'margin'), '不能靠 margin 把整行撑高').toBe('')
    // 基线行仍是居中对齐、原来的 margin-top，未被这次改动波及
    expect(decl(ruleBody(WXSS, '.emp-card .ec-line3'), 'align-items')).toBe('center')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-line3'), 'margin-top')).toBe('10rpx')
  })

  it('只有 default 下移；暖色的按钮本就在公司名下方，不动', () => {
    // 逐风格精确抓规则块，别用跨行正则——那会从别的选择器一路match到无关的 position
    for (const sid of ['ink', 'warm', 'teal']) {
      const off = ruleBody(WXSS, `.wc-style-${sid} .emp-card .case-btn`)
      expect(decl(off, 'position'), `${sid} 不该有位移`).toBe('')
      expect(decl(off, 'top'), `${sid} 不该有位移`).toBe('')
    }
    // 暖色原有的「下方独立一行 + 14rpx 间距」保持原值
    expect(decl(ruleBody(WXSS, '.wc-style-warm .emp-card .case-btn'), 'margin-top')).toBe('14rpx')
  })

  it('预览图同构：default 位移规则由风格 CSS 原文注入', () => {
    expect(SCRIPT).toContain('def read_layout_rules()')
    expect(SCRIPT.lastIndexOf('${style_css}')).toBeGreaterThan(
      SCRIPT.indexOf('.case-btn { flex: none')
    )
  })
})
