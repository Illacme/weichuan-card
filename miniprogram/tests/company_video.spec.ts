// company-video 组件：选档逻辑 + video_play 埋点（组件实例级去重）。
//
// 选档为什么值得钉：后台五档配置（none/channels/upload/url/tencent）与两种
// 视频号凭证的优先级一旦写反，表现是「配了却播不出来 / 拿错源播」，
// 而客户侧只会说「视频坏了」——选档是纯函数，最适合用测试固定语义。
import { describe, it, expect, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))
vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) => u || '',
  apiBaseUrl: () => 'https://api.test',
}))

let captured: any = null

beforeEach(() => {
  captured = null
  vi.resetModules()
  vi.stubGlobal('Component', (opts: any) => {
    captured = opts
  })
  mocks.request.mockClear()
})

async function loadComponent() {
  await import('../components/company-video/index')
  if (!captured) throw new Error('组件未调用 Component() 注册')
  return captured
}

// 造组件实例：data 用组件声明初值。注意 Component 与 Page 的选项结构不同——
// 组件的方法在 opts.methods 嵌套对象里（页面是顶层函数），两种都要绑。
// observers 是「键→函数」的对象，同样手工绑上，按真机框架的触发方式
// （入参变化）调用。
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
  inst.observers = {}
  const observerKey = 'videoSource, feedId, feedToken, vid, videoUrl, fallback'
  inst.observers[observerKey] = opts.observers[observerKey].bind(inst)
  // 简写：按真机入参顺序触发观察者。先把 properties 写进 data——
  // 真机框架会自动同步（组件的 properties 变化即落到 this.data），
  // 测试里手工补上，否则 onVideoError 读 this.data.videoSource 是 undefined。
  // vid 放最后：既有用例都用不到它，加在末尾不用改一堆调用点。
  inst.repick = (
    source: string,
    feedId: string,
    feedToken: string,
    videoUrl: string,
    fallback = false,
    vid = ''
  ) => {
    inst.setData({ videoSource: source, feedId, feedToken, vid, videoUrl, fallback })
    inst.observers[observerKey](source, feedId, feedToken, vid, videoUrl, fallback)
  }
  return inst
}

describe('company-video 组件', () => {
  // ---- 选档 ----

  it('upload 档有 url：走 mp4（标准 video 组件）', async () => {
    const c = instance(await loadComponent())

    c.repick('upload', '', '', 'https://cdn.test/a.mp4')

    expect(c.data.mode).toBe('mp4')
  })

  it('url 档（外部直链）：走 mp4', async () => {
    const c = instance(await loadComponent())

    c.repick('url', '', '', 'https://cdn.test/b.mp4')

    expect(c.data.mode).toBe('mp4')
  })

  it('channels 档：有 feedId 走 channels（同主体可自动播放）', async () => {
    const c = instance(await loadComponent())

    c.repick('channels', 'feed_1', 'tok_1', '', false)

    expect(c.data.mode).toBe('channels')
  })

  it('channels 档只有 feedToken：走 channels-token（跨主体强制手动播）', async () => {
    const c = instance(await loadComponent())

    c.repick('channels', '', 'tok_1', '', false)

    expect(c.data.mode).toBe('channels-token')
  })

  it('none 档：回封面占位，不渲染播放器', async () => {
    const c = instance(await loadComponent())

    c.repick('none', '', '', '', false)

    expect(c.data.mode).toBe('none')
  })

  // ---- 腾讯视频档（插件 txv-video）----
  // 这一档是「原位播放 + 画面干净」诉求的正解：不带视频号角标，也不跳视频号页。

  it('tencent 档有 vid：走 txv（插件组件，原位播放）', async () => {
    const c = instance(await loadComponent())

    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    expect(c.data.mode).toBe('txv')
  })

  it('tencent 档缺 vid：回封面占位（vid 是唯一取片凭证）', async () => {
    const c = instance(await loadComponent())

    c.repick('tencent', '', '', 'https://cdn.test/a.mp4', false, '   ')

    expect(c.data.mode).toBe('none')
  })

  it('tencent 主源失败且有兜底 url：回落 mp4', async () => {
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', 'https://cdn.test/fb.mp4', true, 'n0041aa087e')

    c.onVideoError()

    expect(c.data.mode).toBe('mp4')
    expect(c.data.failed).toBe(true)
  })

  it('tencent 主源失败没开兜底：回落封面占位', async () => {
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', 'https://cdn.test/fb.mp4', false, 'n0041aa087e')

    c.onVideoError()

    expect(c.data.mode).toBe('none')
    expect(c.data.failed).toBe(true)
  })

  // 播放埋点：插件透传了 video 的 bindplay，所以这一档也能记 video_play
  // （视频号档官方没有播放事件，记不了——这是选档时该知道的差别）
  it('tencent 档播放也上报 video_play', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 7 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onVideoPlay()

    const tracks = mocks.request.mock.calls.filter((x) => x[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 7, eventType: 'video_play' })
  })

  // 视频号主源失败 + 后台开了兜底 + 真填了 MP4：三条件缺一都不回落
  it('视频号主源失败且有兜底 url：回落 mp4', async () => {
    const c = instance(await loadComponent())
    c.repick('channels', 'feed_1', '', 'https://cdn.test/fb.mp4', true)

    c.onVideoError()

    expect(c.data.mode).toBe('mp4')
    expect(c.data.failed).toBe(true)
  })

  // 没开兜底开关：不能顶上坏链，但也不该留一个播不出来的播放器——
  // 回落到封面占位（none），至少「公司简介」区不破版
  it('没开兜底开关：视频号失败回落封面占位', async () => {
    const c = instance(await loadComponent())
    c.repick('channels', 'feed_1', '', 'https://cdn.test/fb.mp4', false)

    c.onVideoError()

    expect(c.data.mode).toBe('none')
    expect(c.data.failed).toBe(true)
  })

  it('mp4 档自身播失败：不二次回落（本来就是 MP4，回落没有意义）', async () => {
    const c = instance(await loadComponent())
    c.repick('upload', '', '', 'https://cdn.test/a.mp4', true)

    c.onVideoError()

    expect(c.data.mode).toBe('mp4')
    expect(c.data.failed).toBe(false)
  })

  // ---- video_play 埋点 ----

  it('播放上报 video_play（staffId 由父页面透传）', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })

    c.onVideoPlay()

    const tracks = mocks.request.mock.calls.filter((x) => x[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'video_play' })
  })

  // bindplay 在「暂停后继续播放」时会再次触发：不去重的话一次观看被记成
  // 多次——「反复看了几遍」与「手滑暂停再播」在统计里无法区分
  it('同一实例重复 play 只上报一次（组件级去重）', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })

    c.onVideoPlay()
    c.onVideoPlay()
    c.onVideoPlay()

    const tracks = mocks.request.mock.calls.filter((x) => x[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
  })

  // 档位重算（后台改配置 / 主源失败回落）等于换了一段视频：允许再计一次
  it('档位重算后允许再计一次播放', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })

    c.onVideoPlay()
    c.repick('url', '', '', 'https://cdn.test/another.mp4')
    c.onVideoPlay()

    const tracks = mocks.request.mock.calls.filter((x) => x[1] === '/api/stat/track')
    expect(tracks).toHaveLength(2)
  })

  // staffId=0（未携带员工编号的路径）丢弃：没有归属的事件进了后台
  // 也只是对不上账的噪声
  it('staffId 为 0：丢弃不发请求', async () => {
    const c = instance(await loadComponent())

    c.onVideoPlay()

    expect(mocks.request).not.toHaveBeenCalled()
  })
})
