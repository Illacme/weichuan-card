// 守门：所有 <image> 必须挂 binderror，且失败处理要真的能换掉那张图。
//
// 为什么值得用一条静态用例守着：图片挂了不会报错，只会安静地留一块白或裂图。
// 列表和宫格早就接了 onThumbError（缩略图 → 原图），但 13 处「单图」
// （名片头像 / 企业 LOGO / 首页背景 / 案例封面 / 相册 / 表单头像预览 / 海报缩略图…）
// 一直漏着——它们恰好都在第一眼位置，坏一张就像整页没加载完。
//
// 这条用例做两件事：
//  1. 扫全部 .wxml，任何 <image> 缺 binderror 直接失败（新增页面也会被兜住）；
//  2. 单测 utils/thumb.ts 的 onImgFail：单图 / 图集两种 data 形态都要能改对，
//     缺参数时不能瞎 setData（否则一次误绑就把整页 data 写坏）。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, resolve, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { onImgFail, onThumbError } from '../utils/thumb'

const here = dirname(fileURLToPath(import.meta.url))
const miniRoot = resolve(here, '..')

// 递归收集 .wxml（跳过 node_modules / 编译产物）
function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'miniprogram_npm') continue
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, out)
    else if (name.endsWith('.wxml')) out.push(p)
  }
  return out
}

function stripComments(s: string): string {
  return s.replace(/<!--[\s\S]*?-->/g, '')
}

describe('<image> 必须挂 binderror', () => {
  const files = walk(miniRoot).filter((p) => !p.includes(`${'/'}tests${'/'}`))

  it('至少扫到 15 个 wxml（防止 walk 静默返回空、用例假通过）', () => {
    expect(files.length).toBeGreaterThanOrEqual(15)
  })

  it('没有遗漏的 <image>', () => {
    const missing: string[] = []
    for (const file of files) {
      const html = stripComments(readFileSync(file, 'utf8'))
      // 自闭合标签：<image ... />；[^>]* 不会跨过 '>'，所以不会把后一个标签吃进来
      for (const m of html.matchAll(/<image\b[^>]*\/?>/g)) {
        const tag = m[0]
        if (/\bbinderror=/.test(tag)) continue
        const src = (tag.match(/\bsrc="([^"]*)"/) || [])[1] || '(无 src)'
        missing.push(`${file.slice(miniRoot.length + 1)} → ${src}`)
      }
    }
    // 报错信息要能直接定位：文件 + 那个 src 的绑定表达式
    expect(missing).toEqual([])
  })

  it('挂了 binderror 的必须带 data-fail 或 data-arr（否则处理器无从下手）', () => {
    const bad: string[] = []
    for (const file of files) {
      const html = stripComments(readFileSync(file, 'utf8'))
      for (const m of html.matchAll(/<image\b[^>]*\/?>/g)) {
        const tag = m[0]
        if (!/\bbinderror=/.test(tag)) continue
        // 视频组件用 binderror 接播放错误，不需要 data-fail
        if (/binderror="onVideoError"/.test(tag)) continue
        if (/\bdata-(fail|arr)=/.test(tag)) continue
        bad.push(`${file.slice(miniRoot.length + 1)} → ${(tag.match(/\bsrc="([^"]*)"/) || [])[1]}`)
      }
    }
    expect(bad).toEqual([])
  })
})

describe('onImgFail：单图与图集', () => {
  let ctx: any
  beforeEach(() => {
    ctx = { data: { avatarFail: '', albumFail: [] as string[] }, setData: vi.fn() }
  })

  it('单图：记下失败的 URL', () => {
    onImgFail.call(ctx, { currentTarget: { dataset: { fail: 'avatarFail', src: 'https://x/a.png' } } })
    expect(ctx.setData).toHaveBeenCalledWith({ avatarFail: 'https://x/a.png' })
  })

  it('图集：按位记下失败的 URL（整数组重写，不依赖 setData 的路径语法）', () => {
    ctx.data.albumFail = ['https://x/0.png']
    onImgFail.call(ctx, {
      currentTarget: { dataset: { fail: 'albumFail', idx: 2, src: 'https://x/b.png' } },
    })
    expect(ctx.setData).toHaveBeenCalledWith({
      albumFail: ['https://x/0.png', undefined, 'https://x/b.png'],
    })
  })

  it('idx=0 也要走图集分支（0 是 falsy，用 != null 判断才对）', () => {
    onImgFail.call(ctx, {
      currentTarget: { dataset: { fail: 'albumFail', idx: 0, src: 'https://x/c.png' } },
    })
    expect(ctx.setData).toHaveBeenCalledWith({ albumFail: ['https://x/c.png'] })
  })

  it('图集字段还不是数组时按空数组起手，不抛错', () => {
    const c: any = { data: {}, setData: vi.fn() }
    onImgFail.call(c, {
      currentTarget: { dataset: { fail: 'albumFail', idx: 1, src: 'https://x/d.png' } },
    })
    expect(c.setData).toHaveBeenCalledWith({ albumFail: [undefined, 'https://x/d.png'] })
  })

  it('缺 data-fail 或 data-src：什么都不做，不能把空值写进 data', () => {
    onImgFail.call(ctx, { currentTarget: { dataset: { src: 'https://x/a.png' } } })
    onImgFail.call(ctx, { currentTarget: { dataset: { fail: 'avatarFail' } } })
    onImgFail.call(ctx, {})
    onImgFail.call(ctx, undefined)
    expect(ctx.setData).not.toHaveBeenCalled()
  })
})

describe('onThumbError：列表缩略图回退原图', () => {
  it('缩略图挂了改回原图', () => {
    const ctx: any = {
      data: { list: [{ coverThumb: 'https://x/a_thumb.jpg', cover: 'https://x/a.jpg' }] },
      setData: vi.fn(),
    }
    onThumbError.call(ctx, {
      currentTarget: { dataset: { arr: 'list', idx: 0, key: 'coverThumb', full: 'https://x/a.jpg' } },
    })
    expect(ctx.setData).toHaveBeenCalledWith({ 'list[0].coverThumb': 'https://x/a.jpg' })
  })

  it('已经在原图上：不再 setData（否则 binderror 会无限循环）', () => {
    const ctx: any = {
      data: { list: [{ coverThumb: 'https://x/a.jpg', cover: 'https://x/a.jpg' }] },
      setData: vi.fn(),
    }
    onThumbError.call(ctx, {
      currentTarget: { dataset: { arr: 'list', idx: 0, key: 'coverThumb', full: 'https://x/a.jpg' } },
    })
    expect(ctx.setData).not.toHaveBeenCalled()
  })
})
