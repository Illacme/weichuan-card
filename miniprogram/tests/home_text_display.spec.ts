// 首页「视频下方文字区」四档展示守门（公司名 + 简介文字，各 large/medium/small/hidden）。
//
// 这一轮客户要求「关闭后把该区域撤销显示，不继续挤占屏幕空间」，所以最要紧的不是
// 字号数字，而是**关闭到底有没有真的把节点拿掉**。三类静默失败都要拦：
//   1) 用 display:none / visibility:hidden 假关闭——节点还在、ink 双栏空槽还在；
//   2) 关掉其中一块后 ink 的 grid 没塌，空出的 34% 列继续占屏；
//   3) 后端没归一 / 没下发，端上拿到空串就按「非 hidden」显示，客户关不掉。
//
// 与其它守门一样带**量尺自检 fixture**：先证明正则抓得到东西，再断言真实源码，
// 免得写错的正则一条也抓不到却全绿。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXML = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.wxml'), 'utf-8')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const MODEL = readFileSync(resolve(REPO, 'backend/internal/model/models.go'), 'utf-8')
// 两处档位控件都已迁到「小程序设置 → 名片页面样式」页签（HomeLayout.vue），
// 与首页背景 / 板块顺序放在一起；原公司信息 / 公司简介页不再渲染它们。
const LAYOUT_VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/HomeLayout.vue'), 'utf-8')
const CI_VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/CompanyInfo.vue'), 'utf-8')
const CP_VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/CompanyProfile.vue'), 'utf-8')
const OPENAPI = readFileSync(resolve(REPO, 'docs/openapi.yaml'), 'utf-8')

/** 取某个类规则块（单行）里的声明体 */
function ruleBody(src: string, selector: string): string | undefined {
  const m = src.match(new RegExp(selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\s*\\{([^}]*)\\}'))
  return m ? m[1] : undefined
}

/** 按 4 空格缩进的 schema 标题切块，取到下一个同级 schema 之前 */
function schemaBlock(src: string, name: string): string {
  const lines = src.split('\n')
  const start = lines.findIndex((l) => new RegExp('^    ' + name + ':\\s*$').test(l))
  if (start < 0) return ''
  let end = lines.length
  for (let i = start + 1; i < lines.length; i++) {
    if (/^    [A-Za-z][A-Za-z0-9_]*:\s*$/.test(lines[i])) { end = i; break }
  }
  return lines.slice(start, end).join('\n')
}

describe('首页文字区四档展示跨层映射', () => {
  it('量尺自检：黄金 fixture 能被解析，证明正则不是空转', () => {
    const fixture = '.pf-text .company-name.company-name-size-small { font-size: 26rpx; }'
    expect(ruleBody(fixture, '.pf-text .company-name.company-name-size-small')).toContain('26rpx')
    expect(ruleBody(fixture, '.not-present')).toBeUndefined()
    // schemaBlock 也要自证：切得对、不越界串到下一个 schema
    const yamlFixture = '    CompanyInfo:\n      fullName:\n        type: string\n    Next:\n      other: 1'
    expect(schemaBlock(yamlFixture, 'CompanyInfo')).toContain('fullName:')
    expect(schemaBlock(yamlFixture, 'CompanyInfo')).not.toContain('other:')
    expect(schemaBlock(yamlFixture, 'NotThere')).toBe('')
  })

  it('公司名两档小字号规则存在，large 刻意不写（各风格基线零突变）', () => {
    expect(ruleBody(WXSS, '.pf-text .company-name.company-name-size-medium'), '缺中档')
      .toContain('font-size: 30rpx')
    expect(ruleBody(WXSS, '.pf-text .company-name.company-name-size-small'), '缺小档')
      .toContain('font-size: 26rpx')
    // large 若也写死 font-size，ink/teal 的 30rpx 基线会被改成 34rpx——升级即突变
    expect(WXSS).not.toMatch(/\.company-name-size-large\s*\{[^}]*font-size/)
  })

  it('关闭靠 wx:if 移除节点，不许用 display:none / visibility:hidden 假关', () => {
    // 必须找到「带 wx:if 且判 hidden」的同一行，不能只 anywhere 匹配两个片段
    const nameLine = WXML.split('\n').find(
      (l) => l.includes('wx:if') && l.includes("company.companyNameScale !== 'hidden'")
    )
    const introLine = WXML.split('\n').find(
      (l) => l.includes('wx:if') && l.includes("profile.introFontScale !== 'hidden'")
    )
    expect(nameLine, '公司名缺 wx:if ... !== hidden 判定（或不在同一行）').toBeTruthy()
    expect(introLine, '简介缺 wx:if ... !== hidden 判定（或不在同一行）').toBeTruthy()
    // 不许用隐藏式关闭。先剥 WXML 注释——注释里解释「为什么不用 display:none」
    // 不该被判成违规（本项目踩过多次扫代码连注释一起扫的坑）。
    const codeOnly = WXML.replace(/<!--[\s\S]*?-->/g, '')
    expect(codeOnly).not.toMatch(/display\s*:\s*none|visibility\s*:\s*hidden/)
  })

  it('只剩一块时 .pf-text 带收缩修饰，ink grid 塌成单栏并撤销 grid-area', () => {
    expect(WXML).toMatch(/pf-text--only-intro/)
    expect(WXML).toMatch(/pf-text--only-name/)
    for (const only of ['only-name', 'only-intro']) {
      expect(ruleBody(WXSS, `.wc-theme.wc-style-ink .pf-text.pf-text--${only}`), `ink ${only} 未塌单栏`)
        .toContain('grid-template-columns: 1fr')
    }
    // 子项仍指着第 2 列会隐式造列，必须一律 auto
    expect(WXSS).toMatch(/\.pf-text--only-name \.company-name-line \{ grid-area: auto; \}/)
    expect(WXSS).toMatch(/\.pf-text--only-intro \.intro \{ grid-area: auto; \}/)
    // 这些规则刻意不以 .wc-style- 开头，避免被预览快照脚本抓走（示意图两块都在）
    const collapseLines = WXSS.split('\n').filter((l) => l.includes('pf-text--only-') && l.includes('{'))
    expect(collapseLines.length, '收缩规则不能一条都没有').toBeGreaterThan(0)
    for (const l of collapseLines) expect(l.trim().startsWith('.wc-style-')).toBe(false)
  })

  it('四档共用一套归一与取值，hidden 是真值源；样式页两组四档单选齐全', () => {
    for (const v of ['large', 'medium', 'small', 'hidden']) {
      expect(MODEL, `模型缺 ${v} 档`).toMatch(new RegExp('TextScale' + v[0].toUpperCase() + v.slice(1) + '\\s*=\\s*"' + v + '"'))
      // 两组单选都在 HomeLayout.vue（同一文件里至少各出现一次，不能用出现总数≥4 糊弄）
      expect(LAYOUT_VUE, `样式页缺 ${v} 档`).toContain(`value="${v}"`)
    }
    expect(MODEL).toMatch(/case TextScaleLarge, TextScaleMedium, TextScaleSmall, TextScaleHidden:/)
    // 样式页必须同时绑定 companyNameScale 与 introFontScale 两个 v-model，缺一组就少一处控件
    expect(LAYOUT_VUE).toMatch(/v-model="companyNameScale"/)
    expect(LAYOUT_VUE).toMatch(/v-model="introFontScale"/)
  })

  it('控件只留在样式页：公司信息 / 公司简介页不再渲染四档单选', () => {
    // 防止搬迁时旧控件没删干净——两处都能改同一档位会造成「改了这页、那页保存又冲掉」。
    // 剥注释再判：页面里有说明「已迁到样式页」的注释，不能把注释当控件。
    const ciCode = CI_VUE.replace(/<!--[\s\S]*?-->/g, '')
    const cpCode = CP_VUE.replace(/<!--[\s\S]*?-->/g, '')
    expect(ciCode).not.toMatch(/v-model="form\.companyNameScale"/)
    expect(cpCode).not.toMatch(/v-model="form\.introFontScale"/)
  })

  it('OpenAPI 四处契约都齐全四档，hidden 语义不漂移', () => {
    const cases = [
      ['CompanyInfo', 'companyNameScale'],
      ['CompanyInfoInput', 'companyNameScale'],
      ['CompanyProfile', 'introFontScale'],
      ['CompanyProfileInput', 'introFontScale'],
    ] as const
    for (const [schema, field] of cases) {
      const block = schemaBlock(OPENAPI, schema)
      expect(block, `OpenAPI 缺 ${schema} schema`).not.toBe('')
      expect(block, `${schema} 缺 ${field} 字段`).toContain(`${field}:`)
      // 精确取该字段的 enum 行，不用全文 contains（description 里也提到 hidden）
      const fieldTail = block.slice(block.indexOf(`${field}:`))
      const enumLine = fieldTail.split('\n').find((l) => l.includes('enum:'))
      expect(enumLine, `${schema}.${field} 缺 enum 行`).toBeTruthy()
      for (const v of ['large', 'medium', 'small', 'hidden']) {
        expect(enumLine, `${schema}.${field} enum 缺 ${v}`).toContain(v)
      }
    }
  })
})
