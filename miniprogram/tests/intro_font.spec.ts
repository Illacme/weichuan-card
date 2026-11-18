// 首页「简介文字」四档展示（大/中/小/关闭）——跨层映射守门。
//
// 这档配置的链路横跨四层，任何一层漏改都是「后台选了、小程序没变」的静默失败：
//   后端 model.NormalizeIntroFontScale（缺省/脏值落 large）
//   → 后台 HomeLayout.vue「名片页面样式」页签的四档单选 + 提交（控件已从公司简介页搬来）
//   → 小程序 index.wxml 的 class 绑定 + || 'large' 兜底
//   → app.wxss 的 .intro-size-{large,medium,small} 三条 font-size
//
// 字号只在小程序首页「简介文字」这一段生效；公司名、联系表、章节标题都不受影响。
// large 从历史的 28rpx 提到 30rpx（+1px），是为了把三档区间拉开——旧档差只有 2rpx
// （每档 1px，真机几乎看不出），2026-11-18 用户反馈「调整幅度偏小」后改为 30/26/22。
//
// 这里有「量尺自检」：先钉死一个写死的黄金片段（fixture），证明正则真能抓到东西，
// 再断言真实源码。否则正则一旦写错，抓不到任何东西也会全绿（本项目栽过多次）。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const WXML = readFileSync(resolve(REPO, 'miniprogram/pages/index/index.wxml'), 'utf-8')
const WXSS = readFileSync(resolve(REPO, 'miniprogram/app.wxss'), 'utf-8')
const MODEL = readFileSync(resolve(REPO, 'backend/internal/model/models.go'), 'utf-8')
// 控件已迁到「小程序设置 → 名片页面样式」页签（HomeLayout.vue），不再在公司简介页。
const VUE = readFileSync(resolve(REPO, 'frontend/src/views/content/HomeLayout.vue'), 'utf-8')

/** 取 .intro-size-<tier> 规则块里的 font-size 值 */
function introFontSize(src: string, tier: string): string | undefined {
  const m = src.match(new RegExp('\\.intro-size-' + tier + '\\s*\\{([^}]*)\\}'))
  if (!m) return undefined
  const fm = m[1].match(/font-size\s*:\s*([\d.]+rpx)\s*;/)
  return fm ? fm[1] : undefined
}

describe('简介文字三档字号跨层映射', () => {
  it('量尺自检：黄金 fixture 能被解析出三档，防止正则空转', () => {
    const fixture = [
      '.intro-size-large { font-size: 30rpx; }',
      '.intro-size-medium { font-size: 26rpx; }',
      '.intro-size-small { font-size: 22rpx; }',
    ].join('\n')
    expect(introFontSize(fixture, 'large')).toBe('30rpx')
    expect(introFontSize(fixture, 'medium')).toBe('26rpx')
    expect(introFontSize(fixture, 'small')).toBe('22rpx')
  })

  it('WXSS 定义三档字号，档差 4rpx（大 30 / 中 26 / 小 22）', () => {
    expect(introFontSize(WXSS, 'large'), '缺 large 档').toBe('30rpx')
    expect(introFontSize(WXSS, 'medium'), '缺 medium 档').toBe('26rpx')
    expect(introFontSize(WXSS, 'small'), '缺 small 档').toBe('22rpx')
  })

  it('字号规则只写 font-size，不碰 line-height / margin（风格层覆盖要照旧生效）', () => {
    for (const tier of ['large', 'medium', 'small']) {
      const m = WXSS.match(new RegExp('\\.intro-size-' + tier + '\\s*\\{([^}]*)\\}'))
      expect(m, `缺 ${tier} 档规则`).not.toBeNull()
      expect(m![1]).not.toMatch(/line-height|margin|display|color/)
    }
  })

  it('WXML 把 introFontScale 绑到 intro-size-* class，并以 large 兜底', () => {
    // 必须同时出现字段绑定与兜底，缺一就是「老缓存下 class 落空 = 字号不生效」
    expect(WXML).toMatch(/class="intro intro-size-\{\{profile\.introFontScale[^}]*\}\}"/)
    expect(WXML).toMatch(/profile\.introFontScale\s*\|\|\s*'large'/)
  })

  it('后端归一函数四档齐全、缺省 large；后台四档单选齐全', () => {
    // 档位真值源已抽成 TextScale*，IntroFont* 只是别名——两边都要在
    expect(MODEL).toMatch(/TextScaleLarge\s*=\s*"large"/)
    expect(MODEL).toMatch(/TextScaleMedium\s*=\s*"medium"/)
    expect(MODEL).toMatch(/TextScaleSmall\s*=\s*"small"/)
    expect(MODEL).toMatch(/TextScaleHidden\s*=\s*"hidden"/)
    expect(MODEL).toMatch(/IntroFontLarge\s*=\s*TextScaleLarge/)
    expect(MODEL).toMatch(/DefaultIntroFontScale\s*=\s*DefaultTextScale/)
    for (const v of ['large', 'medium', 'small', 'hidden']) {
      expect(VUE, `后台缺 ${v} 档`).toContain(`value="${v}"`)
    }
  })
})
