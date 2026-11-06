// rich-video 组件（富文本视频块）：video_play 埋点 + 实例级去重 +
// MP4 画幅自适应 + 腾讯视频 playerid 实例唯一。
//
// 为什么值得钉：mp4 / tencent 两档的 bindplay 在「暂停后继续播放」时会再次触发，
// 不去重则一次观看被记成多次；而去重标记若不在「换块」时重置，详情页复用组件
// 实例切换内容后第二段视频就永远计不到——两个方向都会让统计悄悄失真。
// channels 档官方 channel-video 无播放事件，不在本组件埋点范围内（见组件文件头）。
//
// 画幅与 playerid 两组用例对应 2026-11-06 的两处修正：详情页 MP4 块此前固定
// 360rpx + contain（16:9 横屏左右各约 31rpx 黑边），以及一页多块时 txv-video
// 的 playerid 写死 richVideo 会互相串。静态守门尤其重要——把 contain 改回 cover
// 或漏掉 loadedmetadata 都不报错、不变丑，只会在真机上慢慢裁掉字幕。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const mocks = vi.hoisted(() => ({
  track: vi.fn(),
}))
vi.mock('../utils/track', () => ({
  track: mocks.track,
}))

let captured: any = null
// 画幅自适应要量 .rich-video 的实测宽度。用假的链式 selectorQuery 顶替 wx：
// 默认 360px，置 null 模拟量不到，queryThrows 模拟基础库不支持。
let boxWidth: number | null = 360
let querySelectors: string[] = []
let queryThrows = false

beforeEach(() => {
  captured = null
  boxWidth = 360
  querySelectors = []
  queryThrows = false
  vi.resetModules()
  vi.stubGlobal('Component', (opts: any) => {
    captured = opts
  })
  vi.stubGlobal('wx', {
    createSelectorQuery: () => {
      if (queryThrows) throw new Error('createSelectorQuery boom')
      let rectCb: ((rect: any) => void) | null = null
      const q: any = {
        in: () => q,
        select: (sel: string) => {
          querySelectors.push(sel)
          return q
        },
        boundingClientRect: (fn: any) => {
          rectCb = fn
          return q
        },
        exec: () => {
          if (rectCb) rectCb(boxWidth === null ? null : { width: boxWidth })
        },
      }
      return q
    },
  })
  mocks.track.mockClear()
})

async function loadComponent() {
  await import('../components/rich-video/index')
  if (!captured) throw new Error('组件未调用 Component() 注册')
  return captured
}

// 造组件实例：data 用组件声明初值；组件方法在 opts.methods 里（与 Page 顶层函数不同）；
// lifetimes / observers 是「键→函数」对象，手工绑定后按真机入参方式触发。
// setData 记录每次 patch，用来断言「同值不重复重渲染」。
function instance(opts: any) {
  const inst: any = {
    data: JSON.parse(JSON.stringify(opts.data || {})),
    setDataCalls: [] as any[],
  }
  inst.setData = (patch: any) => {
    Object.assign(inst.data, patch)
    inst.setDataCalls.push(patch)
  }
  for (const k of Object.keys(opts)) {
    if (typeof opts[k] === 'function') inst[k] = opts[k].bind(inst)
  }
  for (const group of ['methods', 'lifetimes']) {
    if (opts[group]) {
      for (const k of Object.keys(opts[group])) {
        inst[k] = opts[group][k].bind(inst)
      }
    }
  }
  const observerKey = 'videoType, vid, src, feedId, feedToken, channelId'
  inst.observers = {}
  inst.observers[observerKey] = opts.observers[observerKey].bind(inst)
  // 模拟真机「属性变化 → observers 触发」：properties 变化落到 data 后调观察者
  inst.repick = (patch: Record<string, any>) => {
    inst.setData(patch)
    inst.observers[observerKey](inst.data)
  }
  return inst
}

describe('rich-video 组件', () => {
  it('mp4 档播放上报 video_play（staffId 由详情页透传）', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42, videoType: 'mp4', src: 'https://cdn.test/a.mp4' })

    c.onVideoPlay()

    expect(mocks.track).toHaveBeenCalledTimes(1)
    expect(mocks.track).toHaveBeenCalledWith('video_play', 42)
  })

  it('暂停恢复再播不重复计（实例级去重）', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42, videoType: 'tencent', vid: 'v_1' })

    c.onVideoPlay()
    c.onVideoPlay()
    c.onVideoPlay()

    expect(mocks.track).toHaveBeenCalledTimes(1)
  })

  it('换块后重置去重：详情页复用实例切内容允许再计一次', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick({ videoType: 'mp4', src: 'https://cdn.test/a.mp4' })

    c.onVideoPlay()
    // 切到另一段视频（哪怕同类型）：观察者键含 src，触发即重置
    c.repick({ src: 'https://cdn.test/b.mp4' })
    c.onVideoPlay()

    expect(mocks.track).toHaveBeenCalledTimes(2)
  })

  it('观察者键覆盖三档凭证：改 channels 的 feedId 同样重置', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick({ videoType: 'channels', feedId: 'f_1' })

    c.onVideoPlay()
    c.repick({ feedId: 'f_2' })
    c.onVideoPlay()

    expect(mocks.track).toHaveBeenCalledTimes(2)
  })

  it('onVideoError 只打日志不抛错（块渲染失败的排查线索不能吞）', async () => {
    const c = instance(await loadComponent())
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    expect(() => c.onVideoError({ detail: { errMsg: 'x' } })).not.toThrow()
    expect(() => c.onVideoError()).not.toThrow()
    expect(warn).toHaveBeenCalledTimes(2)

    warn.mockRestore()
  })
})

// ---- 详情页 MP4 画幅：按源文件分辨率调整窗口（2026-11-06）----
//
// 容器原本固定 width:100% + height:360rpx（详情页内容区约 702rpx 宽，
// ≈1.95:1），16:9 横屏 contain 会左右各留约 31rpx 黑边。与首页共用
// utils/mp4box.ts 的等比算法，覆盖默认高度后既不裁也不留黑边。
describe('详情页 MP4 块画幅自适应', () => {
  it('16:9 源片：按实测宽度算高度，覆盖 360rpx 默认画幅', async () => {
    const c = instance(await loadComponent())

    c.onVideoMeta({ detail: { width: 854, height: 480 } })

    // 360px * 480/854 = 202.34 → 202；量的是当前播放器而不是首页的 .cv-mp4
    expect(c.data.mp4BoxStyle).toBe('height:202px')
    expect(querySelectors).toContain('.rich-video')
  })

  it('4:3 源片按 0.75 算，不是写死 16:9', async () => {
    const c = instance(await loadComponent())

    c.onVideoMeta({ detail: { width: 640, height: 480 } })

    expect(c.data.mp4BoxStyle).toBe('height:270px')
  })

  it('容器宽度不同就跟着变：不能用 750rpx 硬算', async () => {
    const c = instance(await loadComponent())
    boxWidth = 320

    c.onVideoMeta({ detail: { width: 1600, height: 900 } })

    expect(c.data.mp4BoxStyle).toBe('height:180px')
  })

  it('极端竖屏钳到 4/3：与首页同一套比例安全区间', async () => {
    const c = instance(await loadComponent())
    c.onVideoMeta({ detail: { width: 540, height: 960 } })
    expect(c.data.mp4BoxStyle).toBe('height:480px')

    // 共享纯函数的边界：两处渲染点必须同源，不能各写一份公式
    const box: any = await import('../utils/mp4box')
    expect(box.mp4BoxStyleFor(100, 540, 960)).toBe('height:133px')
    expect(box.mp4BoxStyleFor(100, 3000, 300)).toBe('height:40px')
    expect(box.MAX_MP4_RATIO).toBe(4 / 3)
  })

  it.each([
    ['detail 缺失', undefined],
    ['宽高为 0', { width: 0, height: 0 }],
    ['只有宽', { width: 854 }],
    ['非数字', { width: 'x', height: 'y' }],
  ])('metadata 无效（%s）时保持默认画幅，不猜也不裁', async (_label, detail) => {
    const c = instance(await loadComponent())

    expect(() => c.onVideoMeta(detail ? { detail } as any : undefined)).not.toThrow()

    expect(c.data.mp4BoxStyle).toBe('')
    expect(querySelectors).toEqual([])
  })

  it('量不到容器宽度时维持默认高度（contain 留黑边），不猜', async () => {
    const c = instance(await loadComponent())
    boxWidth = null

    c.onVideoMeta({ detail: { width: 854, height: 480 } })

    expect(c.data.mp4BoxStyle).toBe('')
  })

  it('createSelectorQuery 抛错时只打日志，不抛异常也不改画幅', async () => {
    const c = instance(await loadComponent())
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    queryThrows = true

    expect(() => c.onVideoMeta({ detail: { width: 854, height: 480 } })).not.toThrow()

    expect(warn).toHaveBeenCalledTimes(1)
    expect(c.data.mp4BoxStyle).toBe('')
    warn.mockRestore()
  })

  it('同一高度不重复 setData（metadata 多次回调不会反复重渲染正文）', async () => {
    const c = instance(await loadComponent())

    c.onVideoMeta({ detail: { width: 854, height: 480 } })
    c.onVideoMeta({ detail: { width: 854, height: 480 } })

    expect(c.setDataCalls.filter((p: any) => 'mp4BoxStyle' in p)).toHaveLength(1)
  })

  it('换块清掉上一段视频算出的高度，等新 metadata 重算', async () => {
    const c = instance(await loadComponent())
    c.onVideoMeta({ detail: { width: 854, height: 480 } })
    expect(c.data.mp4BoxStyle).toBe('height:202px')

    c.repick({ src: 'https://cdn.test/b.mp4' })

    expect(c.data.mp4BoxStyle).toBe('')
  })
})

// ---- 腾讯视频 playerid：一页多块不能串 ----
describe('txv-video 的 playerid 实例唯一', () => {
  it('首次渲染有非空保底值，避免插件拿到空 playerid', async () => {
    const c = instance(await loadComponent())
    expect(c.data.playerId).toBeTruthy()
  })

  it('同一次加载里两个组件实例 attached 后 id 不同', async () => {
    const opts = await loadComponent()
    const a = instance(opts)
    const b = instance(opts)

    a.attached()
    b.attached()

    expect(a.data.playerId).not.toBe(b.data.playerId)
    expect(a.data.playerId).toMatch(/^richVideo_\d+$/)
    expect(b.data.playerId).toMatch(/^richVideo_\d+$/)
    expect(a.data.playerId).not.toBe('richVideo')
  })
})

// ---- 静态守门：WXML / WXSS / 源码接线不能被悄悄改回去 ----
describe('详情页视频块的静态守门', () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const strip = (s: string) => s.replace(/<!--[\s\S]*?-->/g, '')
  const wxml = strip(readFileSync(resolve(root, 'components/rich-video/index.wxml'), 'utf8'))
  const ts = readFileSync(resolve(root, 'components/rich-video/index.ts'), 'utf8')
  const companyTs = readFileSync(resolve(root, 'components/company-video/index.ts'), 'utf8')
  const wxss = readFileSync(resolve(root, 'app.wxss'), 'utf8')

  it('mp4 原生 video 用 contain，绝不出现 cover', () => {
    const tag = /<video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('object-fit="contain"')
    expect(tag).not.toContain('object-fit="cover"')
  })

  it('绑定 loadedmetadata 与行内高度，代码里真的实现了', () => {
    const tag = /<video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('bindloadedmetadata="onVideoMeta"')
    expect(tag).toContain('style="{{mp4BoxStyle}}"')
    expect(ts).toContain('onVideoMeta(')
    expect(ts).toContain('mp4BoxStyleFor(')
    expect(ts).toContain("select('.rich-video')")
  })

  it('两处渲染点共用 utils/mp4box.ts，不再各写一份公式', () => {
    expect(ts).toContain("from '../../utils/mp4box'")
    expect(companyTs).toContain("from '../../utils/mp4box'")
    expect(companyTs).not.toContain('const MIN_MP4_RATIO')
  })

  it('保留 360rpx 默认高度作为元数据未到时的降级', () => {
    const block = /\.rich-video \{([^}]*)\}/.exec(wxss)?.[1] || ''
    expect(block).toMatch(/height:\s*360rpx/)
  })

  it('插件两档保持原样：没有 loadedmetadata，也没被改成 cover', () => {
    const channels = [...wxml.matchAll(/<channel-video\b[^>]*\/?>/g)].map((m) => m[0])
    expect(channels.length).toBe(3)
    channels.forEach((tag) => {
      expect(tag).not.toContain('bindloadedmetadata')
      expect(tag).not.toContain('object-fit=')
    })
    const txv = /<txv-video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(txv).toContain('object-fit="contain"')
    expect(txv).not.toContain('object-fit="cover"')
    expect(txv).not.toContain('bindloadedmetadata')
  })

  it('txv-video 的 playerid 绑实例值，不再写死 richVideo', () => {
    const txv = /<txv-video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(txv).toContain('playerid="{{playerId}}"')
    expect(txv).not.toContain('playerid="richVideo"')
    expect(ts).toContain('playerSeq')
  })

  it('两个详情页都把 src 透传给视频块（mp4 内容能到组件）', () => {
    for (const rel of [
      'pages/project/detail/detail.wxml',
      'pages/service/detail/detail.wxml',
    ]) {
      const page = strip(readFileSync(resolve(root, rel), 'utf8'))
      expect(page).toContain('<rich-video')
      expect(page).toContain('src="{{item.src}}"')
    }
  })
})
