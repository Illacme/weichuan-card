// 名片卡的两处间距微调（2026-11-19 用户反馈）：
//   ① 姓名上方留白要更大；
//   ② 手机号列离卡片左缘要远一点；
//   ③ 右边那列（座机 / 邮箱）位置不动。
//
// 第 ③ 条是这组守门的重点：缩进若直接加在通用 .ec-col 上而不加 box-sizing，
// 50% / 40% 的 flex-basis 是内容盒宽度，缩进会把列撑宽、右列整体右移——
// 用户看到的正是「让你只动左边，结果右边也动了」。
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const SCRIPT = readFileSync(resolve(REPO, 'scripts/gen_theme_previews.py'), 'utf-8')

/**
 * 取某个选择器规则块里的声明体（跨行也算）。
 *
 * 两条锚定缺一不可，否则会抓到别的规则块，断言就在错误的样本上做判断：
 *   ① 选择器必须从行首开始（允许缩进）。不锚定的话 `.emp-card .ec-head`
 *      会先匹配到 `.wc-style-ink .emp-card .ec-head { align-items: flex-start; }`
 *      这类后缀变体——那条没有 margin-top，于是「留白加没加」永远读到空串。
 *   ② 收尾必须是 `}` + 换行。预览脚本的注释里也写着 `.ec-head{margin-top:16rpx}`
 *      （讲 1rpx=0.5px 的换算），不要求「规则整行收尾」就又会先抓到注释。
 *      行尾允许跟一条注释（`/* … *\/` 或 `// …`）：反向验证时真在行尾加过注释，
 *      装置当时就漏抓了——那是量尺的毛病，不能算产品的。
 */
function ruleBody(src: string, selector: string): string | undefined {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const tail = '(?:\\/\\*[\\s\\S]*?\\*\\/|\\/\\/[^\\n]*)?[ \\t]*(?:\\n|$)'
  const m = src.match(new RegExp('(?:^|\\n)[ \\t]*' + esc + '[ \\t]*\\{([^}]*)\\}[ \\t]*' + tail))
  return m ? m[1] : undefined
}

/** 从声明体里取某个属性的值（含单位） */
function decl(body: string | undefined, prop: string): string {
  if (!body) return ''
  const m = body.match(new RegExp('(?:^|;)\\s*' + prop + '\\s*:\\s*([^;]+)'))
  return m ? m[1].trim() : ''
}

describe('名片卡：姓名上方留白与手机号列缩进', () => {
  it('量尺自检：先证明 ruleBody 真能读到这两条基线规则', () => {
    expect(ruleBody(WXSS, '.emp-card .ec-head'), '抓不到 .ec-head 基线，下面会空转').toBeTruthy()
    expect(ruleBody(WXSS, '.emp-card .ec-contact .ec-col'), '抓不到 .ec-col 基线').toBeTruthy()
  })

  it('量尺自检：后缀变体与注释里的同名前缀都不能被当成目标规则', () => {
    // 真出现过：未锚定的正则会先命中 `.wc-style-ink .emp-card .ec-head`，
    // 那条没有 margin-top，于是「留白加没加」读到空串、断言在错误的样本上判红。
    const sample = [
      '.wc-style-ink .emp-card .ec-head { align-items: flex-start; }',
      '.ec-head{margin-top:99rpx} / .ec-col:not(.ec-col-r){padding-left:99rpx} 同值。',
      '.emp-card .ec-head { display: flex; margin-top: 16rpx; }',
    ].join('\n')
    expect(decl(ruleBody(sample, '.emp-card .ec-head'), 'margin-top')).toBe('16rpx')
    // 诱饵自身仍可被完整选择器读到，说明上一条不是「两条都读不到」的空转
    expect(decl(ruleBody(sample, '.wc-style-ink .emp-card .ec-head'), 'align-items')).toBe(
      'flex-start'
    )
  })

  it('姓名上方留白 16rpx，写在 .ec-head 上让四个风格都生效', () => {
    // 不能写进 .emp-card 的 padding：ink / warm / teal 各自覆盖了那条 padding，
    // 写在那里只有 default 会变（用户切风格就会觉得「设置没生效」）。
    expect(decl(ruleBody(WXSS, '.emp-card .ec-head'), 'margin-top')).toBe('16rpx')
  })

  it('手机号列左缩进 16rpx，且缩进算进列宽', () => {
    expect(decl(ruleBody(WXSS, '.emp-card .ec-contact .ec-col'), 'box-sizing')).toBe('border-box')
    const left = ruleBody(WXSS, '.emp-card .ec-contact .ec-col:not(.ec-col-r)')
    expect(left, '左列缩进规则不见了（或写成了通用 .ec-col）').toBeTruthy()
    expect(decl(left, 'padding-left')).toBe('16rpx')
  })

  it('右列（座机 / 邮箱）没有被一起缩进', () => {
    // 缩进写在通用 .ec-col 上会让左右两列都缩进，右列位置就变了
    expect(
      decl(ruleBody(WXSS, '.emp-card .ec-contact .ec-col'), 'padding-left'),
      '缩进不能写在通用 .ec-col 上'
    ).toBe('')
    // teal 给右列的分栏缩进（配左边框做表格感）保持原值，别被这次的改动抹掉
    expect(
      decl(ruleBody(WXSS, '.wc-style-teal .emp-card .ec-contact .ec-col-r'), 'padding-left')
    ).toBe('20rpx')
  })

  it('后台预览脚本同值（8px = 16rpx × 0.5），否则示意图与真机分叉', () => {
    // 预览按 1rpx = 0.5px 出图，两个间距必须在脚本里也跟上，否则客户照着示意图
    // 判断「留白够了」，真机上是另一回事。
    expect(decl(ruleBody(SCRIPT, '.ec-head'), 'margin-top')).toBe('8px')
    expect(decl(ruleBody(SCRIPT, '.ec-col'), 'box-sizing')).toBe('border-box')
    expect(decl(ruleBody(SCRIPT, '.ec-col:not(.ec-col-r)'), 'padding-left')).toBe('8px')
  })
})
