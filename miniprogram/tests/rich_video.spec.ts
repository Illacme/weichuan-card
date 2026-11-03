// rich-video 组件（富文本视频块）：video_play 埋点 + 实例级去重。
//
// 为什么值得钉：mp4 / tencent 两档的 bindplay 在「暂停后继续播放」时会再次触发，
// 不去重则一次观看被记成多次；而去重标记若不在「换块」时重置，详情页复用组件
// 实例切换内容后第二段视频就永远计不到——两个方向都会让统计悄悄失真。
// channels 档官方 channel-video 无播放事件，不在本组件埋点范围内（见组件文件头）。
import { describe, it, expect, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  track: vi.fn(),
}))
vi.mock('../utils/track', () => ({
  track: mocks.track,
}))

let captured: any = null

beforeEach(() => {
  captured = null
  vi.resetModules()
  vi.stubGlobal('Component', (opts: any) => {
    captured = opts
  })
  mocks.track.mockClear()
})

async function loadComponent() {
  await import('../components/rich-video/index')
  if (!captured) throw new Error('组件未调用 Component() 注册')
  return captured
}

// 造组件实例：data 用组件声明初值；组件方法在 opts.methods 里（与 Page 顶层函数不同）；
// observers 是「键→函数」对象，手工绑定后按真机入参方式触发。
function instance(opts: any) {
  const inst: any = {
    data: JSON.parse(JSON.stringify(opts.data || {})),
    setData(patch: any) {
      Object.assign(this.data, patch)
    },
  }
  for (const k of Object.keys(opts)) {
    if (typeof opts[k] === 'function') inst[k] = opts[k].bind(inst)
  }
  if (opts.methods) {
    for (const k of Object.keys(opts.methods)) {
      inst[k] = opts.methods[k].bind(inst)
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
