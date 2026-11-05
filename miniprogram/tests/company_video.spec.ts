// company-video 组件：选档逻辑 + video_play 埋点（组件实例级去重）。
//
// 选档为什么值得钉：后台五档配置（none/channels/upload/url/tencent）与两种
// 视频号凭证的优先级一旦写反，表现是「配了却播不出来 / 拿错源播」，
// 而客户侧只会说「视频坏了」——选档是纯函数，最适合用测试固定语义。
import { describe, it, expect, beforeEach, vi } from 'vitest'
// 静态守门（txv-video 必须显式写 autoplay）要直接读 wxml
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))
vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) => u || '',
  apiBaseUrl: () => 'https://api.test',
}))

let captured: any = null
// requirePlugin 的返回值由各用例改写：默认给一个能 play() 的播放器上下文。
// 之所以要能改写——「取不到上下文」本身就是要测的分支（插件未声明 / 组件未渲染完）。
let requirePluginImpl: (name: string) => any = () => ({ getTxvContext: () => null })
let playSpy: any

beforeEach(() => {
  captured = null
  playSpy = vi.fn()
  requirePluginImpl = () => ({ getTxvContext: () => ({ play: playSpy }) })
  vi.resetModules()
  vi.stubGlobal('Component', (opts: any) => {
    captured = opts
  })
  vi.stubGlobal('requirePlugin', (name: string) => requirePluginImpl(name))
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
    setData(patch: any, cb?: () => void) {
      Object.assign(this.data, patch)
      // 微信的 setData 第二参是「渲染完成」回调，MP4 重试靠它把 src 设回来；
      // 桩必须真的调，否则「先清空再设回」这条路径测不到。
      if (cb) cb()
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

  // ---- MP4 加载失败：必须看得见、能重试 ----
  //
  // 2026-11-05 用户反馈「小程序里播不了新上传的视频」，截图是一块黑屏 + 00:00。
  // 旧实现只在控制台打一行 warn，访客与后台都无从判断是地址错、证书错还是文件不存在。
  // 这一组钉住：失败要有态、地址要打出来、重试要真的重建播放器。
  describe('MP4 加载失败态', () => {
    it('observer 把地址写进 mp4Src，并清掉上一次的失败态', async () => {
      const c = instance(await loadComponent())
      c.repick('upload', '', '', '  https://cdn.test/a.mp4  ')
      expect(c.data.mp4Src).toBe('https://cdn.test/a.mp4')
      expect(c.data.mp4Error).toBe(false)

      c.setData({ mp4Error: true })
      c.repick('url', '', '', 'https://cdn.test/b.mp4')
      expect(c.data.mp4Error).toBe(false)
      expect(c.data.mp4Src).toBe('https://cdn.test/b.mp4')
    })

    it.each(['upload', 'url'])('%s 档 binderror：显出失败态，档位不变也不回落', async (source) => {
      const c = instance(await loadComponent())
      c.repick(source, '', '', 'https://cdn.test/a.mp4')

      c.onVideoError({ detail: { errMsg: 'MEDIA_ERR_SRC_NOT_SUPPORTED' } })

      expect(c.data.mp4Error).toBe(true)
      expect(c.data.mode).toBe('mp4')
      expect(c.data.failed).toBe(false)
    })

    it('报错时把 src 打进日志：地址错才是这类失败的主因', async () => {
      const spy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      try {
        const c = instance(await loadComponent())
        c.repick('upload', '', '', 'https://cdn.test/bad.mp4')
        c.onVideoError({ detail: { errMsg: 'x' } })

        expect(spy).toHaveBeenCalled()
        expect(spy.mock.calls.flat().join(' ')).toContain('https://cdn.test/bad.mp4')
      } finally {
        spy.mockRestore()
      }
    })

    it('视频号主源失败仍按老规则回落兜底，不被 MP4 失败态拦下', async () => {
      const c = instance(await loadComponent())
      c.repick('channels', 'feed_1', '', 'https://cdn.test/fb.mp4', true)

      c.onVideoError()

      expect(c.data.mode).toBe('mp4')
      expect(c.data.failed).toBe(true)
      expect(c.data.mp4Error).toBe(false)
      expect(c.data.mp4Src).toBe('https://cdn.test/fb.mp4')
    })

    it('回落后的 MP4 又失败：照样显出失败态，不再沉默', async () => {
      const c = instance(await loadComponent())
      c.repick('channels', 'feed_1', '', 'https://cdn.test/fb.mp4', true)
      c.onVideoError() // 主源失败 → mp4
      c.onVideoError() // 兜底 MP4 也失败

      expect(c.data.mode).toBe('mp4')
      expect(c.data.mp4Error).toBe(true)
    })

    it('重试：先把 src 清空销毁播放器，再设回地址，并清掉失败态', async () => {
      const c = instance(await loadComponent())
      c.repick('upload', '', '', 'https://cdn.test/a.mp4')
      c.onVideoError()

      const seen: string[] = []
      const original = c.setData.bind(c)
      c.setData = (patch: any, cb?: () => void) => {
        seen.push(String(patch.mp4Src))
        original(patch, cb)
      }
      c.onRetryMp4()

      expect(seen).toEqual(['', 'https://cdn.test/a.mp4'])
      expect(c.data.mp4Error).toBe(false)
      expect(c.data.mp4Src).toBe('https://cdn.test/a.mp4')
    })
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

// ---- 静态守门：每个 <txv-video> 都必须显式写 autoplay ----
//
// 腾讯视频插件的 autoplay **默认值是 true**（官方参数表；与微信原生 video 的
// false 正好相反）。漏写这个属性不会报错、不会变丑，只会「一进页面就带声音
// 自己播起来」——2026-11-04 用户反馈的正是这个。
//
// 两个渲染点都要钉：名片首页的公司简介视频（值来自后台开关），
// 以及详情页富文本里的视频块（固定 false，正文里不该自动出声）。
describe('txv-video 必须显式声明 autoplay', () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const files = [
    'components/company-video/index.wxml',
    'components/rich-video/index.wxml',
  ]

  it.each(files)('%s 里的每个 <txv-video> 都写了 autoplay', (rel) => {
    const wxml = readFileSync(resolve(root, rel), 'utf8').replace(/<!--[\s\S]*?-->/g, '')
    const tags = [...wxml.matchAll(/<txv-video\b[^>]*\/?>/g)].map((m) => m[0])
    // 至少扫到一个标签：否则正则失效时这条用例会「假通过」
    expect(tags.length).toBeGreaterThanOrEqual(1)
    expect(tags.filter((t) => !/\bautoplay=/.test(t))).toEqual([])
  })
})

// ---- 腾讯视频档：能播、能停、能继续 ----
//
// 背景：插件自带的播放键默认在左下角，小到没人在意，访客点画面中央毫无反应
// （2026-11-04 第一轮反馈）→ 加了整块遮罩。
// 但第一版遮罩是**一次性**的：只认「还没播过」，播起来就永久撤掉，
// 于是暂停之后想继续，又得回去角落翻那个小按钮（同日第二轮反馈）。
// 现在的状态机分四件事，别再退回「一个 started 布尔走天下」：
//   txvStarted  曾经播过 → 决定用全屏遮罩还是中央遮罩
//   txvPlaying  当前在播 → 由 bindplay / bindpause / bindended 维护，遮罩跟着它走
//   txvMaskOff  遮罩彻底关掉 → 只在「根本点不动」的退化路径上置位
//   txvEnded    已播完 → 下次点击要先 seek(0)
//
// 这段逻辑有个特点：**失败全是静默的**。playerid 写错、插件名写错、
// 没有 getTxvContext 方法——表现都一样，就是「点了没反应」。
describe('腾讯视频档：能播、能停、能继续', () => {
  it('首次点击调 TxvContext.play()，先进等起播态', async () => {
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()

    expect(playSpy).toHaveBeenCalledTimes(1)
    // 撤遮罩要等 bindplay：起播有半秒以上延迟，先给个转圈反馈
    expect(c.data.txvStarting).toBe(true)
    expect(c.data.txvPlaying).toBe(false)
  })

  it('真正开播（bindplay）后撤遮罩，转圈状态收掉', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()
    c.onVideoPlay()

    expect(c.data.txvStarted).toBe(true)
    expect(c.data.txvPlaying).toBe(true)
    expect(c.data.txvStarting).toBe(false)
  })

  // 用户自己点插件那个按钮也能播：状态同样要跟上，否则遮罩会一直盖着
  it('没点遮罩、直接由插件起播时也撤遮罩', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onVideoPlay()

    expect(c.data.txvStarted).toBe(true)
    expect(c.data.txvPlaying).toBe(true)
  })

  // 起播延迟期间用户容易连点：重复 play() 会让播放器状态抖
  it('等起播期间再点不重复调 play()', async () => {
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()
    c.onTapTxvPlay()
    c.onTapTxvPlay()

    expect(playSpy).toHaveBeenCalledTimes(1)
  })

  // ↓↓↓ 本轮修的就是这一段：pause / ended 必须把 txvPlaying 拉回来，
  //     否则遮罩永远不重现，暂停后点画面就是死键。
  it('暂停（bindpause）后 txvPlaying 复位：中央遮罩才能重新出现', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()
    c.onVideoPlay()
    c.onVideoPause()

    expect(c.data.txvPlaying).toBe(false)
    // 播过这件事不能被抹掉：它决定用中央小遮罩（不影响拖进度）而不是全屏遮罩
    expect(c.data.txvStarted).toBe(true)
  })

  it('暂停后再点一下能继续播', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()
    c.onVideoPlay()
    c.onVideoPause()
    c.onTapTxvPlay()

    expect(playSpy).toHaveBeenCalledTimes(2)
    // 暂停态再点不能因为「已经 started」就被连点保护拦掉
    expect(c.data.txvStarting).toBe(true)
  })

  it('播完（bindended）后回到可重播状态', async () => {
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()
    c.onVideoPlay()
    c.onVideoEnded()

    expect(c.data.txvPlaying).toBe(false)
    expect(c.data.txvEnded).toBe(true)
  })

  // ended 之后直接 play() 在部分机型上没反应，必须先 seek(0) 回开头
  it('播完再点：先 seek(0) 再 play()', async () => {
    const seekSpy = vi.fn()
    requirePluginImpl = () => ({ getTxvContext: () => ({ play: playSpy, seek: seekSpy }) })
    const c = instance(await loadComponent())
    c.setData({ staffId: 42 })
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onVideoPlay()
    c.onVideoEnded()
    c.onTapTxvPlay()

    expect(seekSpy).toHaveBeenCalledWith(0)
    expect(playSpy).toHaveBeenCalledTimes(1)
    // 顺序不能反：先 seek 再 play，否则从头播的那一帧会被丢掉
    expect(seekSpy.mock.invocationCallOrder[0]).toBeLessThan(playSpy.mock.invocationCallOrder[0])
  })

  // seek 失败不能连累 play：定位失败还能从暂停点继续，比整个播不了强
  it('seek(0) 抛错不阻断 play()', async () => {
    requirePluginImpl = () => ({
      getTxvContext: () => ({
        play: playSpy,
        seek: () => {
          throw new Error('seek boom')
        },
      }),
    })
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onVideoEnded()
    expect(() => c.onTapTxvPlay()).not.toThrow()
    expect(playSpy).toHaveBeenCalledTimes(1)
  })

  // 关键退化路径：拿不到播放器上下文时，遮罩必须自己永久撤掉。
  // 否则用户对着一个点不动的遮罩，连插件原来的按钮也点不到——比不改还糟。
  it('取不到 TxvContext：永久撤遮罩退回插件按钮，且不调 play()', async () => {
    requirePluginImpl = () => ({ getTxvContext: () => null })
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    c.onTapTxvPlay()

    expect(playSpy).not.toHaveBeenCalled()
    expect(c.data.txvMaskOff).toBe(true)
    expect(c.data.txvStarting).toBe(false)
  })

  // 插件未在 app.json 声明时 requirePlugin 直接抛错。
  // 这条路径必须不冒泡——抛出去会把整个组件注册带崩，首页连封面都渲染不出来。
  it('插件未声明（requirePlugin 抛错）：不抛异常，遮罩照样撤掉', async () => {
    requirePluginImpl = () => {
      throw new Error('plugin not declared')
    }
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    expect(() => c.onTapTxvPlay()).not.toThrow()
    expect(c.data.txvMaskOff).toBe(true)
  })

  it('play() 自身抛错：撤遮罩退回插件按钮', async () => {
    requirePluginImpl = () => ({
      getTxvContext: () => ({
        play: () => {
          throw new Error('boom')
        },
      }),
    })
    const c = instance(await loadComponent())
    c.repick('tencent', '', '', '', false, 'n0041aa087e')

    expect(() => c.onTapTxvPlay()).not.toThrow()
    expect(c.data.txvMaskOff).toBe(true)
    expect(c.data.txvStarting).toBe(false)
  })

  // vid 未过审 / 断网时插件可能既不播也不报 error：
  // 没有超时兜底就是永久转圈，用户会以为小程序卡死
  it('起播超时：撤遮罩交回插件，不永远转圈', async () => {
    vi.useFakeTimers()
    try {
      const c = instance(await loadComponent())
      c.repick('tencent', '', '', '', false, 'n0041aa087e')

      c.onTapTxvPlay()
      expect(c.data.txvStarting).toBe(true)

      vi.advanceTimersByTime(10000)

      expect(c.data.txvMaskOff).toBe(true)
      expect(c.data.txvStarting).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  // 超时器要在开播时清掉：否则 10 秒后会对已经在播的实例再 setData 一次。
  // 这里用「开播后推进时间，maskOff 不被置位」间接证明它被清了。
  it('开播后清掉超时器：推进时间不会误关遮罩', async () => {
    vi.useFakeTimers()
    try {
      const c = instance(await loadComponent())
      c.setData({ staffId: 42 })
      c.repick('tencent', '', '', '', false, 'n0041aa087e')

      c.onTapTxvPlay()
      c.onVideoPlay()

      vi.advanceTimersByTime(10000)

      expect(c.data.txvMaskOff).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })
})

// ---- 静态守门：插件外部标识 + 遮罩的出现条件 ----
//
// 这些都**不会报错、不会变丑**，只会悄悄失效：
// playerid / 插件名对不上 → 点了没反应；遮罩条件写错 → 要么永远盖着挡死控制条，
// 要么收拾得太干净，暂停后再也点不动。只能靠测试钉住。
describe('腾讯视频插件的外部标识与遮罩条件', () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const wxml = readFileSync(resolve(root, 'components/company-video/index.wxml'), 'utf8').replace(
    /<!--[\s\S]*?-->/g,
    ''
  )
  const ts = readFileSync(resolve(root, 'components/company-video/index.ts'), 'utf8')

  it('wxml 的 playerid 与代码里的 TXV_PLAYER_ID 一致', () => {
    const inWxml = /<txv-video[\s\S]*?playerid="([^"]+)"/.exec(wxml)?.[1]
    const inTs = /const TXV_PLAYER_ID = '([^']+)'/.exec(ts)?.[1]
    expect(inWxml).toBeTruthy()
    expect(inTs).toBeTruthy()
    expect(inWxml).toBe(inTs)
  })

  it('requirePlugin 的插件名与 app.json 声明的 key 一致', () => {
    const app = JSON.parse(readFileSync(resolve(root, 'app.json'), 'utf8'))
    const inTs = /const TXV_PLUGIN_NAME = '([^']+)'/.exec(ts)?.[1]
    expect(inTs).toBeTruthy()
    expect(Object.keys(app.plugins || {})).toContain(inTs)
  })

  // 播放键从「左下角小按钮」挪到中央。插件里 playBtnPosition 默认是 bottom，
  // 不写回去就是访客找不到按钮——这正是最初的抱怨。
  it('播放键挪到中央，且打开双击播放/暂停手势', () => {
    const tag = /<txv-video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('play-btn-position="center"')
    expect(tag).toContain('enable-play-gesture="{{true}}"')
  })

  // 少了这两个回调，遮罩就退回「一次性」：暂停/播完后再也不出现。
  it('必须接住 pause / ended 事件，否则遮罩会变成一次性', () => {
    const tag = /<txv-video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('bindpause="onVideoPause"')
    expect(tag).toContain('bindended="onVideoEnded"')
    // 回调真在组件里实现了才算数（只是绑了个不存在的名字同样静默失败）
    expect(ts).toContain('onVideoPause()')
    expect(ts).toContain('onVideoEnded()')
  })

  it('首次遮罩：没播过、没开自动播放、遮罩没被永久关掉时才出现', () => {
    const mask = /<view\b[^>]*class="cv-tapstart"[^>]*>/.exec(wxml)?.[0] || ''
    expect(mask).toContain('bindtap="onTapTxvPlay"')
    expect(mask).toContain('!txvStarted')
    // 后台开了自动播放就不该有遮罩：一进页面就在播，盖一层只会挡住画面
    expect(mask).toContain('!autoplay')
  })

  // 暂停态的中央小遮罩：**只盖中央一块**，不能铺满。
  // 铺满就把底部进度条和全屏键挡死了——那正是「常驻全屏遮罩」这条路走不通的原因。
  it('暂停遮罩：跟着 txvPlaying 走，且只在中央一小块', () => {
    const mask = /<view\b[^>]*class="cv-tapresume"[^>]*>/.exec(wxml)?.[0] || ''
    expect(mask).toContain('bindtap="onTapTxvPlay"')
    // 关键条件：播完/暂停（!txvPlaying）就重现，不再依赖「没播过」
    expect(mask).toContain('!txvPlaying')
    expect(mask).toContain('txvStarted')
    expect(mask).toContain('!txvMaskOff')

    const wxss = readFileSync(resolve(root, 'app.wxss'), 'utf8')
    const block = /\.company-video \.cv-tapresume \{([^}]*)\}/.exec(wxss)?.[1] || ''
    // 尺寸小于容器（440rpx 高），四周留出控制条的位置
    expect(block).toMatch(/width:\s*240rpx/)
    expect(block).not.toMatch(/right:\s*0/)
    expect(block).not.toMatch(/bottom:\s*0/)
  })

  // 遮罩的 z-index 必须压过插件自己的 11。调小了不会报错、不会变丑，
  // 只是遮罩被原生组件盖住——「不显示、点不到」，和不做这个功能一模一样。
  it.each(['cv-tapstart', 'cv-tapresume'])('%s 的 z-index 高于插件的 11', (cls) => {
    const wxss = readFileSync(resolve(root, 'app.wxss'), 'utf8')
    const block = new RegExp(`\\.company-video \\.${cls} \\{([^}]*)\\}`).exec(wxss)?.[1] || ''
    const z = Number(/z-index:\s*(-?\d+)/.exec(block)?.[1])
    expect(Number.isFinite(z)).toBe(true)
    expect(z).toBeGreaterThan(11)
  })
})

// ---- 静态守门：MP4 失败态的 WXML / WXSS ----
//
// 这些同样「不会报错、只会悄悄失效」：src 绑回 videoUrl 就没法靠清空重建播放器；
// 错误层没绑 onRetryMp4 就是一块点不动的板；z-index 小了会被原生 video 盖住，
// 结果又回到用户看到的那块黑屏。
describe('MP4 失败态的静态守门', () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const wxml = readFileSync(resolve(root, 'components/company-video/index.wxml'), 'utf8').replace(
    /<!--[\s\S]*?-->/g,
    ''
  )
  const wxss = readFileSync(resolve(root, 'app.wxss'), 'utf8')
  const ts = readFileSync(resolve(root, 'components/company-video/index.ts'), 'utf8')

  it('video 的 src 绑 mp4Src（重试要能先清空再设回）', () => {
    const tag = /<video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('src="{{mp4Src}}"')
    expect(tag).not.toContain('src="{{videoUrl}}"')
    expect(tag).toContain('binderror="onVideoError"')
  })

  it('失败态有可点重试，且代码里真的实现了 onRetryMp4', () => {
    const err = /<view\b[^>]*class="cv-mp4err"[^>]*>/.exec(wxml)?.[0] || ''
    expect(err).toContain('wx:if="{{mp4Error}}"')
    expect(wxml).toContain('bindtap="onRetryMp4"')
    expect(ts).toContain('onRetryMp4()')
  })

  it('失败层能盖住播放器：父容器 relative + z-index 高于 video', () => {
    const wrap = /\.company-video \.cv-mp4 \{([^}]*)\}/.exec(wxss)?.[1] || ''
    expect(wrap).toContain('position: relative')
    const err = /\.company-video \.cv-mp4err \{([^}]*)\}/.exec(wxss)?.[1] || ''
    expect(err).toContain('position: absolute')
    const z = Number(/z-index:\s*(-?\d+)/.exec(err)?.[1])
    expect(Number.isFinite(z)).toBe(true)
    expect(z).toBeGreaterThan(11)
  })
})

// ---- 静态守门：MP4 的画幅填充方式必须和视频号一致 ----
//
// 容器是 width:100% + height:440rpx（约 1.52:1），上传的 MP4 绝大多数是 16:9。
// 用 contain 会「宽度撑满、高度不足」，播放器底色 #000 就在上下各露一条黑边
// （2026-11-05 真机反馈）。改回 contain 不会报错、不会有任何日志，只会悄悄变丑，
// 所以只能靠静态守门钉住「两档同一个 object-fit」。
describe('MP4 画幅的静态守门', () => {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
  const wxml = readFileSync(resolve(root, 'components/company-video/index.wxml'), 'utf8').replace(
    /<!--[\s\S]*?-->/g,
    ''
  )

  it('原生 video 用 cover，与两个 channel-video 保持同一套满屏语义', () => {
    const tag = /<video\b[^>]*\/?>/.exec(wxml)?.[0] || ''
    expect(tag).toContain('object-fit="cover"')
    expect(tag).not.toContain('object-fit="contain"')

    const channels = [...wxml.matchAll(/<channel-video\b[^>]*\/?>/g)].map((m) => m[0])
    expect(channels.length).toBeGreaterThan(0)
    channels.forEach((c) => expect(c).toContain('object-fit="cover"'))
  })
})
