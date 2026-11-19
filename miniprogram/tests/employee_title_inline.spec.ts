// 名片卡职位挪到姓名右侧（2026-11-19 用户反馈）：
//   <姓名> <职位>            [LOGO]
//   而不是原来的两行：
//   <姓名>            [LOGO]
//   <职位>
//
// 三处必须同构，缺一处客户就会对着一个真机上不存在的版式调档位：
//   ① miniprogram/components/employee-card/index.wxml + app.wxss（真机）
//   ② scripts/gen_theme_previews.py（主题预览 PNG）
//   ③ frontend/src/views/content/HomeLayout.vue（后台 LOGO 位置小示意图）
//
// 另有一条硬约束：职位变长时只能自己省略，不能把右上角 LOGO 挤走。
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXML = readFileSync(resolve(REPO, 'miniprogram/components/employee-card/index.wxml'), 'utf-8')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const SCRIPT = readFileSync(resolve(REPO, 'scripts/gen_theme_previews.py'), 'utf-8')
const VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/HomeLayout.vue'), 'utf-8')

/**
 * 取某个选择器规则块里的声明体。两条锚定缺一不可：
 * ① 选择器从行首开始，否则 `.ec-name-group` 会先命中 `.wc-style-xx .ec-name-group`；
 * ② `}` 后整行收尾（允许行尾注释），否则会先抓到注释里提到的同名选择器。
 */
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

/** 取 WXML / 模板里两个标记之间的片段，用来证明「职位确实在那一层里」 */
function between(src: string, from: string, to: string): string {
  const a = src.indexOf(from)
  const b = src.indexOf(to, a + from.length)
  if (a < 0 || b < 0) return ''
  return src.slice(a + from.length, b)
}

describe('名片卡：职位紧挨姓名右侧', () => {
  it('量尺自检：ruleBody 能读到目标块，且不会被后缀变体/注释诱饵带偏', () => {
    expect(ruleBody(WXSS, '.emp-card .ec-name-group'), '抓不到姓名组基线').toBeTruthy()
    expect(ruleBody(WXSS, '.emp-card .ec-title'), '抓不到职位基线').toBeTruthy()
    const sample = [
      '.wc-style-warm .ec-name-group { flex-direction: column; }',
      '.ec-title{margin-top:99px} / .ec-name-group{gap:99px} 换算说明',
      '.emp-card .ec-name-group { display: flex; gap: 12rpx; }',
    ].join('\n')
    expect(decl(ruleBody(sample, '.emp-card .ec-name-group'), 'gap')).toBe('12rpx')
    expect(decl(ruleBody(sample, '.wc-style-warm .ec-name-group'), 'flex-direction')).toBe('column')
  })

  it('真机 WXML：职位包在姓名组里、紧跟姓名，LOGO 仍是行内另一项', () => {
    const iLine1 = WXML.indexOf('class="ec-line1"')
    const iGroup = WXML.indexOf('class="ec-name-group"')
    const iName = WXML.indexOf('class="ec-name"')
    const iTitle = WXML.indexOf('class="ec-title"')
    const iSlot = WXML.indexOf('class="ec-logo-slot ')
    const iLine3 = WXML.indexOf('class="ec-line3"')
    expect(Math.min(iLine1, iGroup, iName, iTitle, iSlot, iLine3) >= 0).toBe(true)
    expect(iLine1).toBeLessThan(iGroup)
    expect(iGroup).toBeLessThan(iName)
    expect(iName).toBeLessThan(iTitle)
    expect(iTitle).toBeLessThan(iSlot)
    expect(iSlot).toBeLessThan(iLine3)
    // 职位在 WXML 里只此一处：原来那行单独渲染的节点必须真删掉，不能留下两条
    expect(WXML.match(/class="ec-title"/g)?.length).toBe(1)
    expect(WXML, '没有职位的人不该渲染出一块空白').toContain('wx:if="{{employee.title}}"')
    // 姓名组在 LOGO 区块开始前闭合
    expect(between(WXML, 'class="ec-name-group"', 'class="ec-logo-slot ')).toContain('ec-title')
  })

  it('姓名与职位同基线，长职位省略号而不挤走 LOGO', () => {
    const group = ruleBody(WXSS, '.emp-card .ec-name-group')
    expect(decl(group, 'display')).toBe('flex')
    expect(decl(group, 'align-items')).toBe('baseline')
    expect(decl(group, 'gap')).toBe('12rpx')
    expect(decl(group, 'min-width')).toBe('0')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-name'), 'flex')).toBe('0 0 auto')
    const title = ruleBody(WXSS, '.emp-card .ec-title')
    expect(decl(title, 'flex')).toBe('0 1 auto')
    expect(decl(title, 'min-width')).toBe('0')
    expect(decl(title, 'overflow')).toBe('hidden')
    expect(decl(title, 'text-overflow')).toBe('ellipsis')
    expect(decl(title, 'white-space')).toBe('nowrap')
    // 原来职位在下一行，才需要这 2rpx；留着会把同行的职位顶下去
    expect(decl(title, 'margin-top')).toBe('')
    // LOGO 仍靠 flex-shrink:0 待在行尾
    expect(decl(ruleBody(WXSS, '.emp-card .ec-logo-slot'), 'flex-shrink')).toBe('0')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-line1'), 'justify-content')).toBe('space-between')
  })

  it('四个风格只改外层姿态，没有谁把姓名组改成竖排', () => {
    // warm 的 .ec-line1 是 column：那是「整组 vs LOGO」的排列，组内必须仍横排
    expect(decl(ruleBody(WXSS, '.wc-style-warm .emp-card .ec-line1'), 'flex-direction')).toBe('column')
    expect(
      decl(ruleBody(WXSS, '.wc-style-warm .emp-card .ec-line1'), 'align-items'),
      '暖色靠它把姓名组居中'
    ).toBe('center')
    expect(WXSS).not.toMatch(/(?:^|\n)\s*\.wc-style-\w+ [\s\S]{0,80}\.ec-name-group\s*\{[^}]*flex-direction/)
    // 历史遗留：某风格若还留着给 ec-title 的 margin-top，那一行就又冒出来了
    expect(WXSS).not.toMatch(/(?:^|\n)\s*\.wc-style-\w+ [\s\S]{0,80}\.ec-title\s*\{[^}]*margin-top/)
  })

  it('主题预览脚本：职位也在姓名右侧，且字号 / 间距按 1rpx=0.5px 换算', () => {
    const html = between(SCRIPT, '<div class="ec-line1">', '</div>')
    expect(html).toContain('ec-name-group')
    expect(html).toContain('ec-name')
    expect(html).toContain('ec-title')
    expect(SCRIPT.match(/class="ec-title"/g)?.length).toBe(1)
    expect(decl(ruleBody(SCRIPT, '.ec-name-group'), 'gap')).toBe('6px')
    expect(decl(ruleBody(SCRIPT, '.ec-name-group'), 'align-items')).toBe('baseline')
    expect(decl(ruleBody(SCRIPT, '.ec-title'), 'font-size')).toBe('12px')
    expect(decl(ruleBody(SCRIPT, '.ec-title'), 'text-overflow')).toBe('ellipsis')
    expect(decl(ruleBody(SCRIPT, '.ec-title'), 'margin-top')).toBe('')
  })

  it('后台 LOGO 小预览也是同构的：灰字职位与姓名同一行', () => {
    const line1 = between(VUE, 'class="lp-line1"', 'class="lp-slot"')
    expect(line1).toContain('lp-name-group')
    expect(line1).toContain('lp-name')
    expect(line1).toContain('lp-title')
    expect(VUE.match(/class="lp-title"/g)?.length).toBe(1)
    expect(decl(ruleBody(VUE, '.lp-name-group'), 'display')).toBe('flex')
    expect(decl(ruleBody(VUE, '.lp-name-group'), 'align-items')).toBe('baseline')
    expect(decl(ruleBody(VUE, '.lp-name-group'), 'gap')).toBe('6px')
    expect(decl(ruleBody(VUE, '.lp-title'), 'font-size')).toBe('12px')
    // 暖色只把「组 vs LOGO」竖排，组内仍横排（没有 .lp-layout-stack .lp-name-group 规则）
    expect(VUE).not.toMatch(/\.lp-layout-stack\s+\.lp-name-group\s*\{/)
    expect(
      decl(ruleBody(VUE, '.lp-layout-stack .lp-line1'), 'align-items'),
      '暖色下姓名组要整组居中，不能被拉伸成左对齐'
    ).toBe('center')
    expect(VUE).toContain('灰字「职位」与姓名同一行')
  })
})
