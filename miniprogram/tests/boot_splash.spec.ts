// 启动遮罩的跨页守门（源码级）。
//
// 这类契约有两处最容易悄悄烂掉：
//   1. 两页各改一处样式，真机 reLaunch 时第一帧和第二帧大小/颜色不同，还是「闪一下」；
//   2. 为提速把缓存快路径挪回 bindTheme / getSystemInfoSync 之后，冷启动又多花一帧。
// WXML/WXSS 进不了页面单测，所以用逐字/源码断言钉住，且反向验证必须能变红。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const MP = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const read = (p: string) => readFileSync(resolve(MP, p), 'utf8')

// 剥注释后再扫代码：否则「注释里解释为什么不能用旧写法」会被当成违规。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '')
}

function block(src: string, selector: string): string {
  const at = src.indexOf(selector)
  if (at < 0) return ''
  const open = src.indexOf('{', at)
  const close = src.indexOf('}', open)
  return close > open ? src.slice(open + 1, close) : ''
}

const OVERLAY = `<view class="wc-boot wc-theme {{themeClass}} {{bootFade ? 'wc-boot-off' : ''}}" wx:if="{{bootVisible}}"
  bindtransitionend="onBootFadeEnd" catchtouchmove="noop">
  <view class="wc-boot-inner">
    <view class="wc-boot-title" wx:if="{{bootTitle}}">{{bootTitle}}</view>
    <view class="wc-boot-spinner"></view>
    <view class="wc-boot-text">正在打开…</view>
  </view>
</view>`

const TEAM_WXML = read('pages/team/team.wxml')
const INDEX_WXML = read('pages/index/index.wxml')
const APP_WXSS = read('app.wxss')
const TEAM_WXSS = read('pages/team/team.wxss')
const INDEX_WXSS = read('pages/index/index.wxss')
const TEAM_TS = stripComments(read('pages/team/team.ts'))
const INDEX_TS = stripComments(read('pages/index/index.ts'))

describe('启动遮罩 · 两页同源（源码级）', () => {
  it('两页首帧 markup 逐字相同，不再各写一套 boot 样式', () => {
    expect(TEAM_WXML).toContain(OVERLAY)
    expect(INDEX_WXML).toContain(OVERLAY)
    expect(TEAM_WXSS).not.toMatch(/\.team-boot/)
    expect(INDEX_WXSS).not.toMatch(/\.card-boot/)
    expect(APP_WXSS).not.toMatch(/\.team-boot|\.card-boot/)
  })

  it('首帧绝不再冒出裸的“加载中…”', () => {
    // 名片页已经没有任何加载中分支；真机第二帧那句裸文字就是它画的。
    expect(INDEX_WXML).not.toContain('加载中…')
    // 名片墙保留页内搜索重载提示，但必须排在遮罩之后且受 !booting 约束。
    const overlayAt = TEAM_WXML.indexOf('<view class="wc-boot')
    expect(TEAM_WXML.slice(0, overlayAt)).not.toContain('加载中…')
    expect(TEAM_WXML).toContain('wx:if="{{!booting && !redirecting && loading}}"')
  })

  it('遮罩全屏 fixed，底色与 window.backgroundColor 逐字一致，淡出有过渡', () => {
    const css = block(APP_WXSS, '.wc-boot')
    expect(css).toMatch(/position:\s*fixed/)
    expect(css).toMatch(/top:\s*0/)
    expect(css).toMatch(/right:\s*0/)
    expect(css).toMatch(/bottom:\s*0/)
    expect(css).toMatch(/left:\s*0/)
    expect(css).toMatch(/z-index:\s*9998/)
    expect(css).toMatch(/background:\s*#f5f5f5/)
    expect(css).toMatch(/transition:\s*opacity/)

    const appJson = JSON.parse(read('app.json'))
    expect(appJson.window.backgroundColor).toBe('#f5f5f5')
  })

  it('遮罩层级高于两页自绘导航，spacer 不再刷第二遍白底', () => {
    const teamNav = block(TEAM_WXSS, '.team-custom-nav')
    expect(teamNav).toMatch(/position:\s*fixed/)
    expect(teamNav).toMatch(/z-index:\s*100/)
    const spacer = block(TEAM_WXSS, '.team-nav-spacer')
    expect(spacer).not.toMatch(/background:\s*#fff/i)

    const indexNav = block(INDEX_WXSS, '.custom-nav')
    expect(indexNav).toMatch(/z-index:\s*100/)
  })

  it('缓存快路径在量状态栏 / 绑主题之前就改道', () => {
    const atCached = TEAM_TS.indexOf('const cached = kw || skipEntryCache')
    const atBindTheme = TEAM_TS.indexOf('bindTheme(this)')
    const atSystemInfo = TEAM_TS.indexOf('wx.getSystemInfoSync()')
    expect(atCached).toBeGreaterThanOrEqual(0)
    expect(atBindTheme).toBeGreaterThan(atCached)
    expect(atSystemInfo).toBeGreaterThan(atCached)
  })

  it('两页都在决策落定时淡出，且 transitionend 后才撤节点', () => {
    for (const [name, wxml, ts] of [
      ['team', TEAM_WXML, TEAM_TS],
      ['index', INDEX_WXML, INDEX_TS],
    ] as const) {
      expect(wxml, `${name} 缺少淡出类`).toContain('wc-boot-off')
      expect(wxml, `${name} 没有接 transitionend`).toContain('bindtransitionend="onBootFadeEnd"')
      expect(ts, `${name} 没有撤节点方法`).toContain('onBootFadeEnd()')
      // 必须是「booting 落 false 的同一帧就开始淡出」，不能只在某个错误分支里写 true。
      expect(ts, `${name} 决策落定时没有开始淡出`).toMatch(
        /booting:\s*false,\s*\n\s*bootFade:\s*true/
      )
    }
  })
})
