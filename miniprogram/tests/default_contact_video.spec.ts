// 默认标准风格的两处还原（2026-11-19 客户反馈）：
//   ① 手机 / 座机 / 邮箱联系方式字体偏小，各 +2rpx（24→26rpx）；
//   ② 原设计稿里宣传视频与下方公司名称之间有一条阴影分割条，需要还原。
//
// 两条都只加在 default。阴影条挂在视频容器的 ::after 上、top:100%，
// 不占布局高度——否则公司名称会被往下推，那就不是「分割条」而是多了一行间距。
// 预览脚本必须与真机同构：它把 app.wxss 的 .wc-style-* 规则原文注入出图 CSS，
// 所以 HTML 里要有同名 .cv-wrap 容器，阴影条才会出现在示意图上。
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const SCRIPT = readFileSync(resolve(REPO, 'scripts/gen_theme_previews.py'), 'utf-8')

/** 行首锚定 + 整行收尾，避免抓到后缀变体或注释里的同名选择器 */
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

describe('默认标准风格：联系方式字号与视频底部分割条', () => {
  it('量尺自检：四条目标规则都抓得到，不会在别的样本上空判', () => {
    for (const sel of [
      '.emp-card .ec-label',
      '.wc-style-default .emp-card .ec-label',
      '.wc-style-default .emp-card .ec-val',
      '.wc-style-default .emp-card .ec-val-email',
      '.wc-style-default .mp-profile .company-video .cv-wrap::after',
    ]) {
      expect(ruleBody(WXSS, sel), `${sel} 抓不到`).toBeTruthy()
    }
  })

  it('联系方式三处 24→26rpx，且只改 default', () => {
    expect(decl(ruleBody(WXSS, '.emp-card .ec-label'), 'font-size')).toBe('24rpx')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-val'), 'font-size')).toBe('24rpx')
    expect(decl(ruleBody(WXSS, '.emp-card .ec-val-email'), 'font-size')).toBe('24rpx')
    for (const sel of [
      '.wc-style-default .emp-card .ec-label',
      '.wc-style-default .emp-card .ec-val',
      '.wc-style-default .emp-card .ec-val-email',
    ]) {
      expect(decl(ruleBody(WXSS, sel), 'font-size'), `${sel} 漏改`).toBe('26rpx')
    }
    // 其他三款没有任何联系方式字号覆盖，继续吃 24rpx 基线
    expect(WXSS).not.toMatch(
      /\.(?:wc-style-ink|wc-style-warm|wc-style-teal)[\s\S]{0,80}\.ec-(?:label|val|val-email)\s*\{[^}]*font-size/
    )
  })

  it('阴影分割条：贴在视频下缘外侧，不占高度、不挡跳转点击', () => {
    const shade = ruleBody(WXSS, '.wc-style-default .mp-profile .company-video .cv-wrap::after')
    expect(decl(shade, 'content')).toBe('""')
    expect(decl(shade, 'position')).toBe('absolute')
    expect(decl(shade, 'top')).toBe('100%')
    expect(decl(shade, 'height')).toBe('18rpx')
    expect(decl(shade, 'left')).toBe('8rpx')
    expect(decl(shade, 'right')).toBe('8rpx')
    expect(decl(shade, 'pointer-events')).toBe('none')
    expect(shade, '渐变阴影条而不是一条实色线').toContain('linear-gradient')
    expect(shade, '阴影是中性灰，不随品牌主色变蓝').toContain('rgba(30, 42, 53, 0.22)')
    expect(shade, '别把阴影染成主题色').not.toContain('--wc-primary-rgb')
    // 容器必须可定位，否则 ::after 会跑到页面上而不是视频下缘
    expect(decl(ruleBody(WXSS, '.company-video .cv-wrap'), 'position')).toBe('relative')
  })

  it('另外三款没有这条阴影条，也没有联系方式字号覆盖', () => {
    expect(WXSS).not.toMatch(/\.wc-style-(?:ink|warm|teal)[\s\S]{0,100}\.cv-wrap::after\s*\{/)
    // 更不能写成全局选择器——那样四款都会出现阴影条，只是没人说「其他风格不变」
    expect(WXSS).not.toMatch(/(?:^|\n)[ \t]*\.company-video \.cv-wrap::after\s*\{/)
  })

  it('预览出图脚本同构：有同名容器，风格规则原文在通用 CSS 之后注入', () => {
    expect(SCRIPT).toContain('<div class="cv-wrap">')
    expect(SCRIPT).toContain('.company-video .cv-wrap { position: relative; }')
    expect(SCRIPT).toContain('def read_layout_rules()')
    // 注释里也提到 ${style_css}，取最后一次才是真正的注入点
    expect(SCRIPT.lastIndexOf('${style_css}')).toBeGreaterThan(
      SCRIPT.indexOf('.company-video .cv-wrap { position: relative; }')
    )
  })
})
