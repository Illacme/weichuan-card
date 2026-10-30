// 守门：凡是挂了 <privacy-consent> 的页面，必须给它 id="privacy-consent"。
//
// 真实事故（2026-10-27）：「个人信息」页点「复制全部信息」提示「复制失败」。
// 根因不是剪贴板本身，而是 utils/privacy.ts 的 ensurePrivacy 用
// `page.selectComponent('#privacy-consent')` 按 **id** 找弹窗组件；四个页面
// （mydata / edit / open / poster）都只写了 `<privacy-consent visible=… />` 没写 id，
// 于是 selectComponent 恒返回 null → ensurePrivacy 走「找不到组件」兜底
// （markAgreed + 直接放行）→ 既没弹授权、也没真正取得微信侧同意就调隐私接口
// → setClipboardData 被微信拒 → fail。
//
// 第二次事故（2026-10-30）：首页「存入通讯录」必然弹「需要隐私授权」。
// 根因是 pages/index 既没在 index.json 注册、也没在 index.wxml 挂 <privacy-consent>，
// 而 action-bar 的「存入通讯录」调的 addPhoneContact 同样是隐私接口。
// 所以守门不能只查「已挂的页面 id 对不对」，还要覆盖「该挂的页面有没有挂」：
// 这里把首页也纳入清单，并额外校验 .json 的 usingComponents 里注册了该组件
// —— 只写 wxml 不注册组件，微信开发者工具直接报组件找不到。
//
// 这条用例读源码静态校验：每个用到的页面都要带 id，避免再次漏写。
// 注意：先剥掉注释再匹配，否则注释里提到 <privacy-consent> 会造成假通过/假失败。
// `import.meta.url` 在 vitest(jsdom) 下不是 file 协议，需用 fileURLToPath 转换
// （同 brand.spec.ts 的写法）。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const miniRoot = resolve(here, '..')

const PAGES = [
  'pages/index/index.wxml',
  'pages/mydata/mydata.wxml',
  'pages/employee/edit/edit.wxml',
  'pages/apply/open/open.wxml',
  'pages/poster/poster.wxml',
]

function stripComments(s: string): string {
  return s.replace(/<!--[\s\S]*?-->/g, '')
}

describe('privacy-consent 必须可被 selectComponent(#privacy-consent) 命中', () => {
  for (const page of PAGES) {
    it(`${page} 的 <privacy-consent> 带 id="privacy-consent"`, () => {
      const html = stripComments(readFileSync(resolve(miniRoot, page), 'utf8'))
      expect(html).toContain('<privacy-consent')
      // ensurePrivacy 用 #privacy-consent 选择，缺 id 会导致授权流程被静默跳过
      expect(html).toMatch(/<privacy-consent\b[^>]*\bid="privacy-consent"/)
    })

    it(`${page.replace(/\.wxml$/, '.json')} 已注册 privacy-consent 组件`, () => {
      const json = JSON.parse(readFileSync(resolve(miniRoot, page.replace(/\.wxml$/, '.json')), 'utf8'))
      // 只在 wxml 里写标签、不在 usingComponents 注册，开发者工具会报组件找不到
      expect(json.usingComponents?.['privacy-consent']).toBe('/components/privacy-consent/index')
    })
  }
})
