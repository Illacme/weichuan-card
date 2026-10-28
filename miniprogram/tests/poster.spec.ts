// 小程序「分享海报」页逻辑测试。
//
// 为什么先测这一页：它是小程序码扫码链路的落点——scene 参数解析错，
// 扫码进来拿不到 staffId，整页就是空的，而且这类问题只在真机扫码时暴露，
// 本地开发（直接带 query 跳转）永远复现不了。
//
// 做法：桩掉全局 Page 捕获页面选项，再手工造一个「页面实例」调用生命周期，
// 与真机上的调用形态一致（onLoad → prepare → draw）。
import { describe, it, expect, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (_method: string, _url: string) => ({})),
  assetUrl: vi.fn((u: string) => (u ? `https://cdn.example.com${u.startsWith('/') ? '' : '/'}${u}` : '')),
  // 上传自定义背景要直接拼 URL（wx.uploadFile 不走 request 封装）
  apiBaseUrl: vi.fn(() => 'https://api.test'),
}))
vi.mock('../utils/request', () => mocks)

// 页面 staffId（用例统一用 42），也是「登录员工本人」的编号：
// 海报页据此判断这张名片归不归我编辑。
const MY_STAFF = 42

let captured: any = null

beforeEach(() => {
  captured = null
  vi.resetModules()
  // 登录员工的编号（globalData.myStaffId）：海报页判「是不是本人」的两个来源之一。
  // 个别用例（打开别人的名片 / 老后端）会覆盖成别的值。
  vi.stubGlobal('getApp', () => ({ globalData: { myStaffId: MY_STAFF } }))
  vi.stubGlobal('Page', (opts: any) => {
    captured = opts
  })
  // 选择器查询返回空 → draw() 走「取不到节点」分支（jsdom 里没有真 canvas）
  vi.stubGlobal('wx', {
    createSelectorQuery: () => ({
      select: () => ({ fields: () => ({ exec: (cb: any) => cb([]) }) }),
    }),
    getSystemInfoSync: () => ({ pixelRatio: 2 }),
    showToast: vi.fn(),
    showModal: vi.fn((o: any) => o && o.success && o.success({ confirm: true })),
    // 保存相册：默认走成功回调（个别用例改成 fail 覆盖）
    saveImageToPhotosAlbum: vi.fn((o: any) => o.success({})),
    // 分享图片面板（基础库 2.14.3+）：默认「已支持 + 打开成功」，低版本用例改 canIUse
    canIUse: vi.fn(() => true),
    showShareImageMenu: vi.fn((o: any) => o.success({})),
    // canvas 导出图片：默认成功（导出失败用例改 fail）
    canvasToTempFilePath: vi.fn((o: any) => o.success({ tempFilePath: 'wxfile://tmp.png' })),
    // 海报背景的本地记忆：默认什么都记不住（多数用例走「首次进入」）
    getStorageSync: vi.fn(() => ''),
    setStorageSync: vi.fn(),
    removeStorageSync: vi.fn(),
    // 上传自定义背景：默认「选了一张图 + 上传成功」
    chooseMedia: vi.fn((o: any) => o.success({ tempFiles: [{ tempFilePath: 'wxfile://pick.jpg' }] })),
    uploadFile: vi.fn((o: any) =>
      o.success({ data: JSON.stringify({ code: 0, data: { urls: ['/uploads/2026/10/my-bg.jpg'] } }) })
    ),
    showLoading: vi.fn(),
    hideLoading: vi.fn(),
  })
  mocks.request.mockClear()
  mocks.assetUrl.mockClear()
})

async function loadPage() {
  await import('../pages/poster/poster')
  if (!captured) throw new Error('页面未调用 Page() 注册')
  return captured
}

// 造一个页面实例：data 用页面声明的初值，方法来自 Page 选项，setData 落回 data
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
  return inst
}

// 让两个接口都返回可用数据（card 在响应根级，wxacode 在 data.url）。
// posters：给 /api/assets/posters 的返回；不传则视为「没拉到清单」（返回 {}），
// 页面应退化成原来的白底海报——这条兜底路径必须先于新功能保证不回归。
// prefs：给员工接口（/api/employee/poster-prefs）的返回；
// publicPrefs：给公开快照（/api/poster-prefs?staffId=）的返回，访客走这一支。
function mockApis(
  card: any = { employee: { name: '张伟', avatar: '/uploads/a.png' } },
  codeUrl = '/uploads/wxacode/42.jpg',
  posters: any = null,
  prefs: any = null,
  publicPrefs: any = null
) {
  mocks.request.mockImplementation(async (method: string, url: string) => {
    if (url.includes('/api/wechat/wxacode')) return { code: 0, data: { url: codeUrl } }
    if (url.includes('/api/card')) return card
    if (url.includes('/api/assets/posters')) return posters || {}
    // 默认「后端没存过」：客户端应沿用本机缓存（exists=false）
    if (url.includes('/api/employee/poster-prefs')) {
      if (method === 'PUT') return { code: 0, data: { staffId: MY_STAFF } }
      return prefs || { code: 0, data: { exists: false, staffId: MY_STAFF } }
    }
    // 公开快照（访客 / 打开别人的名片）：只回渲染这一张海报所需的最小集
    if (url.includes('/api/poster-prefs')) {
      return publicPrefs || { code: 0, data: { exists: false, staffId: MY_STAFF } }
    }
    return {}
  })
}

// 两张海报：一张商务款（默认）、一张节气款
const POSTER_LIST = [
  { id: 'poster-brand', name: '匠心品质', file: '/api/assets/file/posters/poster-brand.jpg', thumb: '/api/assets/file/thumbs/posters/poster-brand.jpg' },
  { id: 'poster-lichun', name: '立春', file: '/api/assets/file/posters/poster-lichun.jpg', thumb: '/api/assets/file/thumbs/posters/poster-lichun.jpg' },
]

// 让 draw() 真的画一遍：造一个假 canvas 节点（jsdom 没有 2d canvas）。
// 图片加载用「给 src 赋值即同步触发 onload」的假 img 驱动，
// 最后一步导出交给 wx.canvasToTempFilePath 桩（默认成功）。
function stubCanvasNode() {
  // 海报底版式会把头像裁成圆形（save/beginPath/arc/clip/restore），
  // 桩里没有这些方法会在真机上正常、在单测里直接抛错——补齐。
  const ctx: any = {
    scale: () => {}, fillRect: () => {}, fillText: () => {}, drawImage: () => {},
    save: () => {}, restore: () => {}, beginPath: () => {}, closePath: () => {},
    arc: () => {}, clip: () => {},
    // card 模板的圆角矩形要 moveTo/lineTo/fill；缺了会在真机上正常、单测里抛错
    moveTo: () => {}, lineTo: () => {}, fill: () => {}, stroke: () => {},
    // 自定义背景的渐变蒙层
    createLinearGradient: () => ({ addColorStop: () => {} }),
    // 文字截断靠它量宽；不实现也走 textWidth 的粗估兜底
    measureText: (t: string) => ({ width: (t || '').length * 12 }),
  }
  const canvas: any = {
    width: 0,
    height: 0,
    getContext: () => ctx,
    createImage() {
      const img: any = {}
      Object.defineProperty(img, 'src', {
        set() {
          if (img.onload) img.onload()
        },
        get() {
          return 'https://cdn.example.com/fake.png'
        },
      })
      return img
    },
  }
  ;(globalThis as any).wx.createSelectorQuery = vi.fn(() => ({
    select: () => ({ fields: () => ({ exec: (cb: any) => cb([{ node: canvas, width: 320, height: 460 }]) }) }),
  }))
  return canvas
}

// 内容右边界：与页面同一条量宽规则（桩里 measureText 恒为 12px/字），
// 超长文字按 maxWidth 封顶（真机上会被 clip 截断）。
// 用来断言「拖到最右，字也还在画布里」——这是夹紧唯一真正要保证的事。
function rightEdge(geo: any): number {
  let right = 0
  for (const it of geo.items) {
    if (!it.text) continue
    const w = Math.min(it.text.length * 12, it.maxWidth)
    right = Math.max(right, it.align === 'center' ? it.x + w / 2 : it.x + w)
  }
  if (geo.avatar) right = Math.max(right, geo.avatar.x + geo.avatar.size)
  return right
}

describe('poster 页', () => {
  it('带 staffId 参数进入：解析出 staffId 并请求名片与小程序码', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0)) // 等 prepare 的 await 链

    expect(page.data.staffId).toBe(42)
    const urls = mocks.request.mock.calls.map((c) => c[1])
    expect(urls).toContain('/api/card?staffId=42')
    // 海报码必须带 from=poster：后端把它埋进 scene，扫这张码进来才能归到
    // poster 来源（普通码与海报码的微信 scene 同在扫码区间，只有这个标记能区分）。
    // 漏掉它就退化成「扫码」，海报渠道在来源分布里永远统计不到——静默失真。
    expect(urls).toContain('/api/wechat/wxacode?staffId=42&from=poster')
  })

  // 扫码进入：微信把参数塞进 scene（URL 编码），拿不到 staffId 就等于海报空白
  it('扫码进入（scene 参数）：解析出 staffId', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ scene: encodeURIComponent('staffId=42') })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.staffId).toBe(42)
  })

  it('scene 里混了其它参数也要能认出 staffId', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ scene: encodeURIComponent('a=1&staffId=7&b=2') })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.staffId).toBe(7)
  })

  it('既无 staffId 也无 scene：staffId 归 0 而不是 NaN/崩溃', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({})
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.staffId).toBe(0)
  })

  it('头像与小程序码都转成绝对 URL（canvas 的 img.src 必须绝对地址）', async () => {
    const opts = await loadPage()
    mockApis({ employee: { name: '张伟', avatar: '/uploads/a.png' } }, '/uploads/wxacode/42.jpg')
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.qrUrl).toBe('https://cdn.example.com/uploads/wxacode/42.jpg')
    expect(page.data.card.employee.avatar).toBe('https://cdn.example.com/uploads/a.png')
  })

  it('小程序码接口没有 data 时不崩（未配微信凭据时的降级路径）', async () => {
    const opts = await loadPage()
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 1, msg: '微信凭据未配置' }
      return { employee: { name: '张伟', avatar: '' } }
    })
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.qrUrl).toBe('')
    expect(page.data.card).toBeTruthy()
  })

  // ---- 海报保存埋点 ----

  // 保存成功才计 poster_save：用户愿意把海报存进相册，说明有线下发送的意图，
  // 比「打开过海报页」强得多的意向信号（后台意向指标卡之一）
  it('海报保存成功后上报 poster_save', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ tempFilePath: 'wxfile://tmp.png' })
    mocks.request.mockClear()

    // save 现在是 async：存相册（saveImageToPhotosAlbum，微信隐私接口）前
    // 要先过隐私授权检查，所以真正存的动作发生在 await 之后。
    await page.save()
    await new Promise((r) => setTimeout(r, 0))

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'poster_save' })
  })

  // 保存失败（权限拒绝 / 写入失败）不计：没做成不等于没意愿，
  // 计了会让「保存海报数」虚高、运营按这个数评估线下发放量就偏了
  it('海报保存失败：不上报 poster_save', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ tempFilePath: 'wxfile://tmp.png' })
    ;(globalThis as any).wx.saveImageToPhotosAlbum = vi.fn((o: any) =>
      o.fail({ errMsg: 'saveImageToPhotosAlbum:fail auth deny' })
    )
    mocks.request.mockClear()

    await page.save()
    await new Promise((r) => setTimeout(r, 0))

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  // 海报还没画完就点保存：连保存动作都没发生，更不该有埋点
  it('海报未生成时保存：直接提示，不上报', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()

    await page.save()
    await new Promise((r) => setTimeout(r, 0))

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
    expect((globalThis as any).wx.saveImageToPhotosAlbum).not.toHaveBeenCalled()
  })

  // ---- 加载 / 失败 / 重试 ----

  it('导出成功：加载态落下来并拿到临时图片', async () => {
    const opts = await loadPage()
    mockApis()
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.loading).toBe(false)
    expect(page.data.error).toBe(false)
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
  })

  // 断网 / 接口异常：loading 必须落下来并给重试入口。此前 Promise.all 没有 catch，
  // reject 直接冒泡出 async 函数 → 页面永远停在空白，用户只能退出重进
  it('名片接口失败：落到失败态而不是一直转圈', async () => {
    const opts = await loadPage()
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 0, data: { url: '/uploads/wxacode/42.jpg' } }
      throw new Error('network down')
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.loading).toBe(false)
    expect(page.data.error).toBe(true)
    expect(page.data.tempFilePath).toBe('')
  })

  // 取不到 canvas 节点（页面未渲染完 / 节点被销毁）也不能静默：
  // 否则按钮一直显示「生成中」，用户点了没反应也看不出原因
  it('取不到 canvas 节点：落到失败态而不是静默 return', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts) // beforeEach 的查询桩返回空数组
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.loading).toBe(false)
    expect(page.data.error).toBe(true)
  })

  it('canvas 导出失败：落到失败态并给出重试入口', async () => {
    const opts = await loadPage()
    mockApis()
    stubCanvasNode()
    ;(globalThis as any).wx.canvasToTempFilePath = vi.fn((o: any) => o.fail({ errMsg: 'canvasToTempFilePath:fail' }))
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.loading).toBe(false)
    expect(page.data.error).toBe(true)
    expect(page.data.tempFilePath).toBe('')
  })

  it('重试：重新请求并在成功前先清掉上一张海报', async () => {
    const opts = await loadPage()
    let n = 0
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 0, data: { url: '/uploads/wxacode/42.jpg' } }
      n++
      if (n === 1) throw new Error('network down')
      return { employee: { name: '张伟', avatar: '' } }
    })
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.error).toBe(true)

    stubCanvasNode() // 重试时才把 canvas 节点准备好（模拟第二次渲染到位）
    page.setData({ tempFilePath: 'wxfile://old.png' })
    const p = page.retry()
    // 重试必须先清掉旧图：否则导出失败后按钮仍拿着上一版海报，用户会拿到旧图且不报错
    expect(page.data.tempFilePath).toBe('')
    await p
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.error).toBe(false)
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
    const cards = mocks.request.mock.calls.filter((c) => c[1] === '/api/card?staffId=42')
    expect(cards).toHaveLength(2) // 首次失败 + 重试
  })

  // ---- 分享（裂变闭环：生成 → 分享 → 好友看海报 → 扫码记为 poster 来源）----

  it('分享海报：打开分享面板并上报 share', async () => {
    const opts = await loadPage()
    mockApis()
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()

    page.share()

    const menu = (globalThis as any).wx.showShareImageMenu
    expect(menu).toHaveBeenCalledTimes(1)
    expect(menu.mock.calls[0][0].path).toBe('wxfile://tmp.png')
    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    // targetId=0：名片级转发，与首页转发同一把尺子（区别于案例/服务详情的转发）
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'share' })
  })

  it('海报未生成时点分享：不弹面板也不上报', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()

    page.share()

    expect((globalThis as any).wx.showShareImageMenu).not.toHaveBeenCalled()
    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  // 低版本基础库（<2.14.3）没有 showShareImageMenu：直接调用会抛 unknown api，
  // 页面表现为「点了没反应」。必须降级提示先存相册，而不是把按钮做成死键
  it('低版本微信（无分享图片 API）：降级提示而不是点了没反应', async () => {
    const opts = await loadPage()
    mockApis()
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    ;(globalThis as any).wx.canIUse = vi.fn(() => false)
    mocks.request.mockClear()

    page.share()

    expect((globalThis as any).wx.showShareImageMenu).not.toHaveBeenCalled()
    expect((globalThis as any).wx.showModal).toHaveBeenCalled()
    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  it('用户取消分享面板：不上报 share', async () => {
    const opts = await loadPage()
    mockApis()
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    ;(globalThis as any).wx.showShareImageMenu = vi.fn((o: any) => o.fail({ errMsg: 'showShareImageMenu:fail cancel' }))
    mocks.request.mockClear()

    page.share()

    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(0)
  })

  // 右上角「转发」：转发出去的是海报页本身——好友打开即看到同一张海报并可继续
  // 保存/转发，是裂变链路的下一段（此前这一页没有转发出口）
  it('右上角转发：带 staffId 转发海报页并上报 share', async () => {
    const opts = await loadPage()
    mockApis()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()

    const share = page.onShareAppMessage()

    expect(share.path).toBe('/pages/poster/poster?staffId=42')
    expect(share.title).toBe('张伟的名片')
    const tracks = mocks.request.mock.calls.filter((c) => c[1] === '/api/stat/track')
    expect(tracks).toHaveLength(1)
    expect(tracks[0][2]).toEqual({ staffId: 42, eventType: 'share' })
  })

  // ---- 海报背景（二十四节气 / 商务场景）----

  it('拉到海报清单：带背景重画，默认选中商务款（节气会过时）', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.posters).toHaveLength(2)
    expect(page.data.bgId).toBe('poster-brand')
    // 缩略图必须转绝对 URL：<image src> 不认相对路径，真机上就是一排裂图
    expect(page.data.posters[0].thumbUrl).toBe(
      'https://cdn.example.com/api/assets/file/thumbs/posters/poster-brand.jpg'
    )
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
  })

  // 清单接口挂了（老后端 / 断流）不该连海报一起挂：退化成白底继续出图。
  // 这条是新增功能的兜底底线——主功能不能被可选能力拖垮。
  it('海报清单接口失败：退化成白底海报，不影响出图', async () => {
    const opts = await loadPage()
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 0, data: { url: '/uploads/wxacode/42.jpg' } }
      if (url.includes('/api/card')) return { employee: { name: '张伟', avatar: '' } }
      throw new Error('network down') // 海报清单挂了
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.error).toBe(false)
    expect(page.data.posters).toHaveLength(0)
    expect(page.data.bgId).toBe('')
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
  })

  it('切换背景：记住选择并整张重画（清掉上一张，避免分享出去的还是旧背景）', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')

    page.pickBg({ currentTarget: { dataset: { id: 'poster-lichun' } } })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.bgId).toBe('poster-lichun')
    // 重画前必须清掉旧图：否则「保存/分享」拿到的还是上一版背景的海报，且不报错
    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith('posterBg:42', 'poster-lichun')
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
  })

  // 「原样」= 白底卡片：选了海报要能退回去（有人就是想发一张干净的名片）
  it('切回「原样」：bgId 落空，走白底版式', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.pickBg({ currentTarget: { dataset: { id: '' } } })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.bgId).toBe('')
    // 存哨兵 'none' 而不是空串：空串在 wx.getStorageSync 里与「键不存在」同义，
    // 下次进来就分不清「用户选了白底」还是「这人从没选过」。
    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith('posterBg:42', 'none')
  })

  // 上次的选择要记住：员工每次发海报都重挑一遍太烦，节气/场景是有惯性的
  it('记住上次选过的背景', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    ;(globalThis as any).wx.getStorageSync = vi.fn(() => 'poster-lichun')
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect((globalThis as any).wx.getStorageSync).toHaveBeenCalledWith('posterBg:42')
    expect(page.data.bgId).toBe('poster-lichun')
  })

  // 素材库升级后旧 id 可能不存在（删款 / 改名）：不能让页面拿着一个画不出来的
  // 背景 id，必须回落到默认款
  it('上次选的背景已下架：回落到默认款而不是空白', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    ;(globalThis as any).wx.getStorageSync = vi.fn(() => 'poster-removed')
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.bgId).toBe('poster-brand')
  })

  // 背景加载失败（域名未配 downloadFile 白名单是小程序最常见的一坑）：
  // 不能卡在「生成中」，白底海报照样要出得来
  it('背景图加载失败：白底兜底继续出图而不是卡在生成中', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    const canvas = stubCanvasNode()
    // 背景图 onerror：createImage 返回的桩改为触发 onerror
    canvas.createImage = () => {
      const img: any = {}
      Object.defineProperty(img, 'src', {
        set() {
          if (img.onerror) img.onerror()
        },
        get() {
          return ''
        },
      })
      return img
    }
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.loading).toBe(false)
    expect(page.data.error).toBe(false)
    expect(page.data.tempFilePath).toBe('wxfile://tmp.png')
  })
})

// 按 key 精确控制本地存储：默认 getStorageSync 对所有键返回同一个值，
// 而 canUpload 要靠 'empToken'、背景记忆要靠 'posterBg:<id>'，必须分开。
function stubStorage(map: Record<string, string>) {
  ;(globalThis as any).wx.getStorageSync = vi.fn((k: string) => (k in map ? map[k] : ''))
}

const CUSTOM_BG = { id: 'c-abc123', name: '活动照', file: '/uploads/2026/10/my-bg.jpg' }

describe('poster 页 · 自定义背景与版式', () => {
  // ---- 读取个性化设置 ----

  // 未登录也要拉设置——但拉的是**公开快照**（只回渲染这一张所需的最小集），
  // 不是员工接口（那要鉴权，且会带上他全部的私有背景）。
  // 没有这一支，访客看到的是「默认版式重新生成的一张」，员工排的版式传不出去。
  it('未登录：请求公开快照而不是员工接口', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubStorage({}) // 无 empToken
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.canUpload).toBe(false)
    const urls = mocks.request.mock.calls.map((c) => c[1])
    expect(urls.some((u: string) => u.indexOf('/api/employee/poster-prefs') >= 0)).toBe(false)
    expect(urls).toContain('/api/poster-prefs?staffId=42')
  })

  it('已登录：应用后端保存的版式与自定义背景', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, {
      code: 0,
      data: {
        exists: true,
        bgId: 'c-abc123',
        backgrounds: [CUSTOM_BG],
        layout: { template: 'center', showPhone: false, avatarSize: 88, offsetY: 20 },
      },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.canUpload).toBe(true)
    expect(page.data.customs).toHaveLength(1)
    expect(page.data.customs[0].thumbUrl).toBe('https://cdn.example.com/uploads/2026/10/my-bg.jpg')
    // 后端存的 id 是自定义背景：必须能选中，不能因为不在内置清单里就回落默认款
    expect(page.data.bgId).toBe('c-abc123')
    expect(page.data.layout.template).toBe('center')
    expect(page.data.layout.showPhone).toBe(false)
    expect(page.data.layout.avatarSize).toBe(88)
    expect(page.data.layout.offsetY).toBe(20)
  })

  // exists=false 是后端「没有可信记录」的信号：此时若拿后端的空值覆盖，
  // 员工换新账号第一次进页面，手挑的背景和调好的版式会被冲掉
  it('后端没存过（exists=false）：沿用本机缓存的选择', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1', 'posterBg:42': 'poster-lichun', 'posterLayout:42': '{"template":"minimal"}' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, {
      code: 0,
      data: { exists: false, bgId: '', backgrounds: [], layout: {} },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.bgId).toBe('poster-lichun')
    expect(page.data.layout.template).toBe('minimal')
  })

  // ---- 上传自定义背景 ----

  it('上传自定义背景：加入「我的背景」并立即选中', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    // uploadBg 现在是 async：选图（chooseMedia，微信隐私接口）前要先过隐私授权检查
    await page.uploadBg()
    await new Promise((r) => setTimeout(r, 0))

    const up = (globalThis as any).wx.uploadFile
    expect(up).toHaveBeenCalledTimes(1)
    // 必须走员工鉴权上传（/api/employee/upload），与员工改头像同一条链路
    expect(up.mock.calls[0][0].url).toBe('https://api.test/api/employee/upload')
    expect(up.mock.calls[0][0].header.Authorization).toBe('Bearer tk-1')

    expect(page.data.customs).toHaveLength(1)
    expect(page.data.customs[0].id.indexOf('c-')).toBe(0)
    expect(page.data.bgId).toBe(page.data.customs[0].id)
    // 选中后必须重画：否则「保存/分享」拿到的还是上一张背景的图
    expect((globalThis as any).wx.canvasToTempFilePath).toHaveBeenCalled()
  })

  it('上传返回非 JSON（网关 502 返回 HTML）：提示失败且不加入列表', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    ;(globalThis as any).wx.uploadFile = vi.fn((o: any) => o.success({ data: '<html>502</html>' }))
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    await page.uploadBg()
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.customs).toHaveLength(0)
    expect((globalThis as any).wx.hideLoading).toHaveBeenCalled()
    expect((globalThis as any).wx.showToast).toHaveBeenCalled()
  })

  it('未登录点上传：提示先登录，不打开相册', async () => {
    const opts = await loadPage()
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubStorage({})
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    await page.uploadBg()

    expect((globalThis as any).wx.chooseMedia).not.toHaveBeenCalled()
    expect((globalThis as any).wx.showModal).toHaveBeenCalled()
  })

  it('删除自定义背景：确认后从列表移除', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, {
      code: 0,
      data: { exists: true, bgId: 'poster-brand', backgrounds: [CUSTOM_BG], layout: {} },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.customs).toHaveLength(1)

    page.removeBg({ currentTarget: { dataset: { id: 'c-abc123' } } })

    expect(page.data.customs).toHaveLength(0)
    // 删的不是当前选中项：背景不该被换掉
    expect(page.data.bgId).toBe('poster-brand')
  })

  // 删掉的正好是当前背景：不回落的话画布会去加载一张已不在列表里的图，
  // 用户看到的是「背景突然变白」，而且没有任何提示
  it('删除当前选中的背景：回落到默认款并重画', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, {
      code: 0,
      data: { exists: true, bgId: 'c-abc123', backgrounds: [CUSTOM_BG], layout: {} },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.bgId).toBe('c-abc123')

    const before = (globalThis as any).wx.canvasToTempFilePath.mock.calls.length
    page.removeBg({ currentTarget: { dataset: { id: 'c-abc123' } } })

    expect(page.data.bgId).toBe('poster-brand')
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBeGreaterThan(before)
  })

  // ---- 版式调整 ----

  it('切换版式模板：更新版式并重画', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const before = (globalThis as any).wx.canvasToTempFilePath.mock.calls.length

    page.pickTemplate({ currentTarget: { dataset: { key: 'card' } } })

    expect(page.data.layout.template).toBe('card')
    // 版式变了但图没重画的话，用户点「分享」发出去的还是旧版式那张，且不报错
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBeGreaterThan(before)
  })

  it('切换版式模板：白名单外的值落回默认', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.pickTemplate({ currentTarget: { dataset: { key: 'evil' } } })

    expect(page.data.layout.template).toBe('classic')
  })

  it('元素显隐：点一下反转布尔', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.layout.showPhone).toBe(true)

    page.toggleOption({ currentTarget: { dataset: { field: 'showPhone' } } })

    expect(page.data.layout.showPhone).toBe(false)
  })

  it('位置上下移：推到画布边界就停住，不会把内容推出画布', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    // 可拖区间由内容包围盒算出（不再写死 ±60）：以页面自己算的为准
    const r = page.lastGeo().range

    for (let i = 0; i < 40; i++) page.offsetUp()
    expect(page.data.layout.offsetY).toBe(r.contentYMin)
    // 到边界后再点也不动：不夹紧的话数字会一直涨，而海报早就贴死了
    page.offsetUp()
    expect(page.data.layout.offsetY).toBe(r.contentYMin)

    for (let i = 0; i < 80; i++) page.offsetDown()
    expect(page.data.layout.offsetY).toBe(r.contentYMax)
  })

  // ---- 拖动调整位置 ----

  it('拖动海报：位移跟随手指，拖动中不导出、松手才导出', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const before = (globalThis as any).wx.canvasToTempFilePath.mock.calls.length

    page.onDragStart({ touches: [{ clientX: 100, clientY: 200 }] })
    page.onDragMove({ touches: [{ clientX: 110, clientY: 220 }] })

    expect(page.data.layout.offsetX).toBe(10)
    expect(page.data.layout.offsetY).toBe(20)
    // 拖动中每帧都导出会掉帧（canvasToTempFilePath 要编码整张 PNG）
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBe(before)

    page.onDragEnd()

    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBe(before + 1)
    // 松手才落本地存储
    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith(
      'posterLayout:42',
      JSON.stringify(page.data.layout)
    )
  })

  // 用户抱怨过「拖不动」：以前横向只给 ±16（拍脑袋定的安全区），拖两下就顶死。
  // 现在按内容包围盒算——两字姓名能横跨大半个画布，长公司名就只给一点点余量。
  it('拖动范围：横向远大于原来的 ±16（按内容包围盒算，不是拍脑袋的固定值）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    const r = page.lastGeo().range
    expect(r.contentXMax).toBeGreaterThan(16)
    // 左边余量天然小：内容本来就贴着左边距（20），往左只剩一点点——
    // 这正是「按包围盒算」的意义，固定区间给不出这种不对称
    expect(r.contentXMin).toBeLessThan(0)
    expect(r.contentXMax - r.contentXMin).toBeGreaterThan(60)
    // 纵向：内容还能往上让出二维码的位置，往下则到画布底为止
    expect(r.contentYMax).toBeGreaterThan(0)
    expect(r.contentYMin).toBeLessThan(0)
  })

  it('拖动夹紧：能拖到画布边缘，但字不会被推出画布', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const r = page.lastGeo().range

    page.onDragStart({ touches: [{ clientX: 0, clientY: 0 }] })
    page.onDragMove({ touches: [{ clientX: 400, clientY: 400 }] })
    expect(page.data.layout.offsetX).toBe(r.contentXMax)
    expect(page.data.layout.offsetY).toBe(r.contentYMax)
    page.onDragEnd()

    // 贴边之后右边缘正好落在安全边距上：多一个像素就会被裁掉尾字
    expect(rightEdge(page.lastGeo())).toBeLessThanOrEqual(320 - 8)

    page.onDragStart({ touches: [{ clientX: 400, clientY: 400 }] })
    page.onDragMove({ touches: [{ clientX: 0, clientY: 0 }] })
    expect(page.data.layout.offsetX).toBe(r.contentXMin)
    expect(page.data.layout.offsetY).toBe(r.contentYMin)
    page.onDragEnd()

    // 往反方向拖回来时不能有「死区」：拖到边界后再反向，画面必须立刻跟着动
    page.onDragStart({ touches: [{ clientX: 0, clientY: 0 }] })
    page.onDragMove({ touches: [{ clientX: 30, clientY: 30 }] })
    expect(page.data.layout.offsetX).toBe(r.contentXMin + 30)
    page.onDragEnd()
  })

  // 二维码以前钉死在底部居中、连位置都看不见，用户想把它挪开只能整张海报一起动，
  // 结果文字跟着歪。现在它有独立的一层热区（.qr-hit），按在码上就是挪码。
  it('拖动二维码：按在码上拖的是码，信息区一动不动', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const qrTarget = { currentTarget: { dataset: { target: 'qr' } } }

    page.onDragStart({ ...qrTarget, touches: [{ clientX: 100, clientY: 300 }] })
    page.onDragMove({ touches: [{ clientX: 100, clientY: 260 }] })
    expect(page.data.layout.qrOffsetY).toBe(-40)
    expect(page.data.layout.offsetY).toBe(0) // 信息区不受牵连
    page.onDragEnd()

    // 横向：码可以拖到左右任意位置（原来连拖都拖不了）
    page.onDragStart({ ...qrTarget, touches: [{ clientX: 100, clientY: 300 }] })
    page.onDragMove({ touches: [{ clientX: 190, clientY: 300 }] })
    expect(page.data.layout.qrOffsetX).toBe(90)
    page.onDragEnd()

    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith(
      'posterLayout:42',
      JSON.stringify(page.data.layout)
    )
  })

  it('拖动二维码：拖不出画布（默认贴在底部，所以再往下拖不动）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const r = page.lastGeo().range

    page.onDragStart({ currentTarget: { dataset: { target: 'qr' } }, touches: [{ clientX: 0, clientY: 0 }] })
    page.onDragMove({ touches: [{ clientX: 500, clientY: 500 }] })
    expect(page.data.layout.qrOffsetX).toBe(r.qrXMax)
    expect(page.data.layout.qrOffsetY).toBe(r.qrYMax)
    page.onDragEnd()
  })

  // 用户反馈「也没看到二维码位置」：热区要在版式里显形，让人知道这块能单独拖
  it('二维码热区：位置与几何一致（默认底部居中，尺寸随内容自适应）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    const geo = page.lastGeo()
    expect(page.data.qrRect).toEqual({
      left: Math.round(geo.qr.x),
      top: Math.round(geo.qr.y),
      size: Math.round(geo.qr.size),
    })
    // 水平居中、贴底（下方留出「长按识别名片」的高度）
    expect(page.data.qrRect.left + page.data.qrRect.size / 2).toBeCloseTo(160, 0)
    expect(page.data.qrRect.top + page.data.qrRect.size).toBeLessThanOrEqual(460)
  })

  // 手指按下几乎必然带 1px 抖动：不设死区的话「点一下海报」也会触发重画
  it('拖动死区：位移不足 2px 不重画也不导出', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const before = (globalThis as any).wx.canvasToTempFilePath.mock.calls.length

    page.onDragStart({ touches: [{ clientX: 100, clientY: 100 }] })
    page.onDragMove({ touches: [{ clientX: 101, clientY: 100 }] })
    page.onDragEnd()

    expect(page.data.layout.offsetX).toBe(0)
    expect(page.data.layout.offsetY).toBe(0)
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBe(before)
  })

  it('复位：内容和码各归各的，复位一个不会顺手挪另一个', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.setData({ layout: { ...page.data.layout, offsetX: 12, offsetY: -30, qrOffsetX: -40, qrOffsetY: -60 } })
    page.resetContent()

    expect(page.data.layout.offsetX).toBe(0)
    expect(page.data.layout.offsetY).toBe(0)
    // 码保持原样：用户拖歪的是内容，复位只该把内容拉回来
    expect(page.data.layout.qrOffsetX).toBe(-40)
    expect(page.data.layout.qrOffsetY).toBe(-60)

    page.resetQr()
    expect(page.data.layout.qrOffsetX).toBe(0)
    expect(page.data.layout.qrOffsetY).toBe(0)
  })

  // 「大小」工具里两行并排：头像和码各调各的，互不串档
  it('码也能用按钮微调，且不动内容区', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.qrUp()
    expect(page.data.layout.qrOffsetY).toBe(-10)
    expect(page.data.layout.offsetY).toBe(0)

    page.qrUp()
    expect(page.data.layout.qrOffsetY).toBe(-20)

    // 默认位置已经贴底（下方 32px 留给「长按识别名片」），所以只往上让得动
    page.qrDown()
    expect(page.data.layout.qrOffsetY).toBe(-10)
    expect(page.data.layout.offsetX).toBe(0)
  })

  // 拖动时每帧都重画：图片加载失败若不缓存，断网 / 域名未配时每帧都会发一次
  // 网络请求，越拖越卡。失败缓存 3 秒，兼顾「不刷请求」和「抖动后能自愈」。
  it('图片加载失败会短时缓存：拖动不会每帧重发请求', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    const canvas = stubCanvasNode()
    const spy = vi.fn(() => {
      const img: any = {}
      Object.defineProperty(img, 'src', {
        set() {
          if (img.onerror) img.onerror()
        },
        get() {
          return ''
        },
      })
      return img
    })
    canvas.createImage = spy

    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    const first = spy.mock.calls.length
    expect(first).toBeGreaterThan(0)

    page.onDragStart({ touches: [{ clientX: 0, clientY: 0 }] })
    page.onDragMove({ touches: [{ clientX: 20, clientY: 20 }] })
    page.onDragMove({ touches: [{ clientX: 30, clientY: 30 }] })

    expect(spy.mock.calls.length).toBe(first)
  })

  it('加载中不响应拖动（遮罩盖着，不该被拖走）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ loading: true })

    page.onDragStart({ touches: [{ clientX: 0, clientY: 0 }] })
    page.onDragMove({ touches: [{ clientX: 50, clientY: 50 }] })

    expect(page.data.layout.offsetX).toBe(0)
  })

  it('头像大小：三档之间切换', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.layout.avatarSize).toBe(72)

    page.pickAvatarSize({ currentTarget: { dataset: { size: '88' } } })
    expect(page.data.layout.avatarSize).toBe(88)

    page.pickAvatarSize({ currentTarget: { dataset: { size: '56' } } })
    expect(page.data.layout.avatarSize).toBe(56)
  })

  // ---- 保存到后端 ----

  it('版式改动同步后端：PUT 带 bgId / backgrounds / layout', async () => {
    const opts = await loadPage()
    stubStorage({ empToken: 'tk-1' })
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, {
      code: 0,
      data: { exists: true, bgId: 'poster-lichun', backgrounds: [CUSTOM_BG], layout: {} },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()
    // 清掉 mockClear 后仍要能响应 PUT
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })

    page.pickTemplate({ currentTarget: { dataset: { key: 'minimal' } } })
    await page.savePrefsNow()

    const puts = mocks.request.mock.calls.filter((c) => c[0] === 'PUT' && c[1] === '/api/employee/poster-prefs')
    expect(puts).toHaveLength(1)
    const body = puts[0][2]
    expect(body.bgId).toBe('poster-lichun')
    expect(body.layout.template).toBe('minimal')
    // 提交时必须还原成原始 file 路径：存的是绝对 URL 会被后端再拼一次 baseUrl
    expect(body.backgrounds[0].file).toBe('/uploads/2026/10/my-bg.jpg')
  })

  it('未登录：版式只存本地，不发 PUT', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    mocks.request.mockClear()

    page.pickTemplate({ currentTarget: { dataset: { key: 'center' } } })
    await page.savePrefsNow()

    const puts = mocks.request.mock.calls.filter((c) => c[0] === 'PUT')
    expect(puts).toHaveLength(0)
    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith(
      'posterLayout:42',
      JSON.stringify(page.data.layout)
    )
  })

  // ---- 版式计算（纯函数）----

  it('版式几何：内置素材内容从 150 开始（顶部烧了标题），自定义背景从 24 开始', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    // 内置素材：信息必须让开顶部标题区，否则会压在「立春」上
    page.setData({ bgId: 'poster-brand' })
    const builtin = page.computeGeometry(320, 460, true)
    expect(builtin.avatar.y).toBe(150)
    expect(builtin.light).toBe(true)

    // 自定义背景：用户自己的图，不能假设顶部有留白
    page.setData({ bgId: 'c-1', customs: [{ id: 'c-1', custom: true, fileUrl: 'https://x/a.jpg' }] })
    const custom = page.computeGeometry(320, 460, true)
    expect(custom.avatar.y).toBe(24)
  })

  it('版式几何：内容越高二维码越小，且不与内容重叠', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(
      { employee: { name: '张伟', title: '业务经理', phone: '13800000000', avatar: '' }, company: { fullName: '某某公司', address: '某某市某某路 1 号' } },
      undefined,
      { code: 0, data: { items: POSTER_LIST } }
    )
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand' })

    // 全显示 + 最大头像 → 内容最占地方
    page.setData({ layout: { ...page.data.layout, avatarSize: 88 } })
    const full = page.computeGeometry(320, 460, true)
    const lowest = Math.max.apply(null, full.items.map((i: any) => i.y))
    expect(full.qr.y).toBeGreaterThanOrEqual(lowest)
    expect(full.qr.y + full.qr.size).toBeLessThanOrEqual(460)
    expect(full.qr.size).toBeLessThan(104) // 被内容挤小了

    // 只留姓名 → 空间宽裕，二维码回到最大尺寸
    page.setData({
      layout: { ...page.data.layout, avatarSize: 56, showTitle: false, showCompany: false, showAddress: false, showPhone: false },
    })
    const slim = page.computeGeometry(320, 460, true)
    expect(slim.qr.size).toBe(104)
  })

  it('版式几何：文字配色 auto 跟随背景（有底图浅色字，白底深色字）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.computeGeometry(320, 460, true).light).toBe(true)
    expect(page.computeGeometry(320, 460, false).light).toBe(false)

    // 用户显式选深色：即使有底图也用深色字
    page.setData({ layout: { ...page.data.layout, textColor: 'dark' } })
    expect(page.computeGeometry(320, 460, true).light).toBe(false)
  })

  it('版式几何：内容整体位移跟随 offsetY', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand', layout: { ...page.data.layout, offsetY: 0 } })

    const base = page.computeGeometry(320, 460, true)
    page.setData({ layout: { ...page.data.layout, offsetY: 30 } })
    const moved = page.computeGeometry(320, 460, true)

    expect(moved.avatar.y - base.avatar.y).toBe(30)
    expect(moved.items[0].y - base.items[0].y).toBe(30)
  })

  // 信息区和二维码各有自己的位移，互不牵连
  it('版式几何：内容位移只平移内容，二维码位置独立', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand', layout: { ...page.data.layout, offsetX: 0 } })

    const base = page.computeGeometry(320, 460, true)
    page.setData({ layout: { ...page.data.layout, offsetX: 12 } })
    const moved = page.computeGeometry(320, 460, true)

    expect(moved.avatar.x - base.avatar.x).toBe(12)
    expect(moved.items[0].x - base.items[0].x).toBe(12)
    // 内容动，码不动
    expect(moved.qr.x).toBe(base.qr.x)
    expect(moved.qrOffsetX).toBe(0)

    // 反过来：码动，内容不动
    page.setData({ layout: { ...page.data.layout, offsetX: 0, qrOffsetX: -40, qrOffsetY: -80 } })
    const qrMoved = page.computeGeometry(320, 460, true)
    expect(qrMoved.qr.x).toBe(base.qr.x - 40)
    expect(qrMoved.qr.y).toBe(base.qr.y - 80)
    expect(qrMoved.items[0].x).toBe(base.items[0].x)
  })

  // 「长按识别名片」不能钉死在画布底部：码被拖到上面去时，提示得跟着走，
  // 否则码在上、提示孤零零留在底部，用户根本不知道那行字在说哪张图。
  it('版式几何：提示文字跟着二维码走', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand' })

    const base = page.computeGeometry(320, 460, true)
    page.setData({ layout: { ...page.data.layout, qrOffsetY: -100 } })
    const moved = page.computeGeometry(320, 460, true)

    expect(moved.qr.y).toBe(base.qr.y - 100)
    expect(moved.hint.y).toBe(base.hint.y - 100)
    // 水平上始终对齐码的中线
    expect(moved.hint.x).toBe(moved.qr.x + moved.qr.size / 2)
    // 提示永远不会被挤出画布
    expect(moved.hint.y).toBeLessThanOrEqual(460 - 6)
  })

  // 用户上传的是随手拍的图，底部可能是白墙 / 天空：不压暗的话白字直接糊掉，
  // 而内置素材的暗部是生成时烧好的，不需要再叠
  it('自定义背景：底部叠渐变蒙层；内置素材不叠', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    const canvas = stubCanvasNode()
    const ctx: any = canvas.getContext('2d')
    const gradient = vi.fn(() => ({ addColorStop: () => {} }))
    ctx.createLinearGradient = gradient

    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.setData({ bgId: 'poster-brand' })
    page.draw()
    expect(gradient).not.toHaveBeenCalled()

    page.setData({ customs: [{ id: 'c-1', custom: true, fileUrl: 'https://x/a.jpg', thumbUrl: '' }], bgId: 'c-1' })
    page.draw()
    expect(gradient).toHaveBeenCalled()
  })

  // ---- 小程序码缺失（真机上最常见的「海报是废图」）----

  // 后端没配 AppID 时 /api/wechat/wxacode 直接回 code:1。这条路径以前是静默的：
  // 海报上留一片空白、却仍然印着「长按识别名片」，用户发出去对方扫不出任何东西。
  it('小程序码取不到：置 qrError 并透出后端原因', async () => {
    const opts = await loadPage()
    stubStorage({})
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 1, msg: '未配置小程序 AppID / WX_APPSECRET' }
      if (url.includes('/api/card')) return { employee: { name: '张伟', avatar: '/uploads/a.png' } }
      if (url.includes('/api/assets/posters')) return { code: 0, data: { items: POSTER_LIST } }
      return {}
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.qrUrl).toBe('')
    expect(page.data.qrError).toBe(true)
    // 只说「失败」等于让用户去猜是不是网络问题，后端的原话最有用
    expect(page.data.qrMsg).toBe('未配置小程序 AppID / WX_APPSECRET')
  })

  it('没有码时不画「长按识别名片」，改画占位框（否则等于发一张废图）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) return { code: 1, msg: '未配置小程序 AppID / WX_APPSECRET' }
      if (url.includes('/api/card')) return { employee: { name: '张伟', avatar: '/uploads/a.png' } }
      if (url.includes('/api/assets/posters')) return { code: 0, data: { items: POSTER_LIST } }
      return {}
    })
    const canvas = stubCanvasNode()
    const ctx: any = canvas.getContext('2d')
    const texts: string[] = []
    ctx.fillText = (t: string) => texts.push(t)

    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(texts).not.toContain('长按识别名片')
    expect(texts).toContain('小程序码')
    expect(texts).toContain('未生成')
  })

  it('重试小程序码：成功后清掉 qrError 并重新出图', async () => {
    const opts = await loadPage()
    stubStorage({})
    let fail = true
    mocks.request.mockImplementation(async (_m: string, url: string) => {
      if (url.includes('/api/wechat/wxacode')) {
        return fail ? { code: 1, msg: '未配置小程序 AppID / WX_APPSECRET' } : { code: 0, data: { url: '/uploads/wxacode/42.jpg' } }
      }
      if (url.includes('/api/card')) return { employee: { name: '张伟', avatar: '/uploads/a.png' } }
      if (url.includes('/api/assets/posters')) return { code: 0, data: { items: POSTER_LIST } }
      return {}
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.qrError).toBe(true)

    fail = false
    page.retryQr()
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.qrError).toBe(false)
    expect(page.data.qrMsg).toBe('')
    expect(page.data.qrUrl).toBe('https://cdn.example.com/uploads/wxacode/42.jpg')
  })

  // ---- 底部工具条 ----

  it('工具切换：白名单外的 key 被忽略（面板不会空白）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    // 默认落在背景：换背景是最高频的操作
    expect(page.data.tool).toBe('bg')

    page.switchTool({ currentTarget: { dataset: { tool: 'pos' } } })
    expect(page.data.tool).toBe('pos')

    page.switchTool({ currentTarget: { dataset: { tool: 'nope' } } })
    expect(page.data.tool).toBe('pos')
  })

  // ---- 姓名可关 / 自定义文字 ----

  const FULL_CARD = {
    employee: { name: '张伟', title: '业务经理', phone: '13800000000', avatar: '' },
    company: { fullName: '某某公司', address: '某某市某某路 1 号' },
  }

  it('元素：姓名可以关掉，关掉后职位顶上来当标题', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(FULL_CARD, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    expect(page.data.layout.showName).toBe(true)

    page.toggleOption({ currentTarget: { dataset: { field: 'showName' } } })
    expect(page.data.layout.showName).toBe(false)

    page.setData({ bgId: 'poster-brand' })
    const geo = page.computeGeometry(320, 460, true)
    const texts = geo.items.map((i: any) => i.text)
    expect(texts).not.toContain('张伟')
    // 职位从 13px 灰字升成 17px 主字：否则头像旁只剩一条孤零零的小字，
    // 看着像「名字没加载出来」
    const title = geo.items.find((i: any) => i.text === '业务经理')
    expect(title.size).toBe(17)
    expect(title.dim).toBeFalsy()
  })

  it('自定义文字：输入时只重画不导出，失焦才归一落库并导出', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(FULL_CARD, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    page.switchTool({ currentTarget: { dataset: { tool: 'text' } } })
    expect(page.data.tool).toBe('text')

    const before = (globalThis as any).wx.canvasToTempFilePath.mock.calls.length
    page.onCustomText({ detail: { value: '24 小时上门维修' } })
    // 画布立刻跟着变，但不导出：导出要编码整张 PNG，一个字一次会明显卡顿
    expect(page.data.layout.customText).toBe('24 小时上门维修')
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBe(before)

    page.commitCustomText()
    expect((globalThis as any).wx.canvasToTempFilePath.mock.calls.length).toBe(before + 1)
    expect((globalThis as any).wx.setStorageSync).toHaveBeenCalledWith(
      'posterLayout:42',
      JSON.stringify(page.data.layout)
    )
  })

  it('二维码尺寸：可选档位，0 是自动（跟着内容高度自适应）', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand' })

    // 默认自动：内容不高，码顶到自动上限 104
    expect(page.data.layout.qrSize).toBe(0)
    const auto = page.computeGeometry(320, 460, true)
    expect(auto.qr.size).toBe(104)

    // 选「大」：码真的变大，底边不动（往上让），不会顶出画布。
    // 默认名片内容不算少，140 会被夹到「内容还能让出多少」——这是有意的：
    // 大码压住电话号码比小码难扫得多。放得下的情形见下一条用例。
    page.pickQrSize({ currentTarget: { dataset: { size: '140' } } })
    expect(page.data.layout.qrSize).toBe(140)
    const big = page.computeGeometry(320, 460, true)
    expect(big.qr.size).toBeGreaterThan(auto.qr.size)
    expect(big.qr.size).toBeLessThanOrEqual(140)
    expect(big.qr.y + big.qr.size).toBe(auto.qr.y + auto.qr.size)

    // 选「小」
    page.pickQrSize({ currentTarget: { dataset: { size: '76' } } })
    expect(page.computeGeometry(320, 460, true).qr.size).toBe(76)

    // 回「自动」：0 是合法值，不能被当成「没选」
    page.pickQrSize({ currentTarget: { dataset: { size: '0' } } })
    expect(page.data.layout.qrSize).toBe(0)
    expect(page.computeGeometry(320, 460, true).qr.size).toBe(104)
  })

  // 上一轮曾让「大」在内容多时自动缩到放得下——结果 fitSize 被压到 56，
  // 小 / 中 / 大一律变 56，用户看到的就是「大小调整失效」。
  // 现在档位严格生效，重叠交给白底衬保证可扫。
  it('二维码尺寸：内容多时档位照样生效，不被内容高度压回小码', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(FULL_CARD, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand', layout: { ...page.data.layout, avatarSize: 88 } })

    for (const q of [76, 104, 140]) {
      page.setData({ layout: { ...page.data.layout, avatarSize: 88, qrSize: q } })
      expect(page.computeGeometry(320, 460, true).qr.size).toBe(q)
    }

    // 再大也不能出画布
    const geo = page.computeGeometry(320, 460, true)
    expect(geo.qr.y + geo.qr.size).toBeLessThanOrEqual(460)
  })

  // 内容少的时候 140 才真放得下——档位不是摆设
  it('二维码尺寸：内容少时「大」档就是 140', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    // 只留姓名职位和头像：信息区矮了，底部自然腾得出地方
    page.setData({
      bgId: 'poster-brand',
      layout: {
        ...page.data.layout,
        showCompany: false,
        showAddress: false,
        showPhone: false,
        qrSize: 140,
      },
    })

    const geo = page.computeGeometry(320, 460, true)
    expect(geo.qr.size).toBe(140)
    // 底边仍钉在原来的位置，是往上长
    expect(geo.qr.y + geo.qr.size).toBe(428)
  })

  it('自定义文字：画在信息下方，用主色强调；清空后这一行消失', async () => {
    const opts = await loadPage()
    stubStorage({})
    mockApis(FULL_CARD, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    page.setData({ bgId: 'poster-brand', layout: { ...page.data.layout, customText: '24 小时上门维修' } })

    const geo = page.computeGeometry(320, 460, true)
    const slogan = geo.items.find((i: any) => i.accent)
    expect(slogan.text).toBe('24 小时上门维修')
    // 排在电话下面：自定义文案是补充信息，不该插到联系方式中间
    const phone = geo.items.find((i: any) => i.text.indexOf('手机') === 0)
    expect(slogan.y).toBeGreaterThan(phone.y)

    page.clearCustomText()
    expect(page.data.layout.customText).toBe('')
    const after = page.computeGeometry(320, 460, true)
    expect(after.items.some((i: any) => i.accent)).toBe(false)
  })
})

describe('poster 页 · 纯函数', () => {
  it('normalizeLayout：白名单外的值落默认，数值夹紧', async () => {
    const mod: any = await import('../pages/poster/poster')
    const { normalizeLayout } = mod

    const d = normalizeLayout(null)
    expect(d.template).toBe('classic')
    expect(d.avatarSize).toBe(72)
    expect(d.qrOffsetX).toBe(0)
    expect(d.qrOffsetY).toBe(0)

    const bad = normalizeLayout({ template: 'nope', textColor: 'rainbow', offsetX: 99999, offsetY: 99999, avatarSize: 200 })
    expect(bad.template).toBe('classic')
    expect(bad.textColor).toBe('auto')
    // 上限只是挡脏数据的粗框（±480，画布长边级别），不是「可拖范围」——
    // 真正的可拖范围按内容包围盒算，见 computeGeometry 的 range
    expect(bad.offsetX).toBe(480)
    expect(bad.offsetY).toBe(480)
    expect(bad.avatarSize).toBe(88)

    expect(normalizeLayout({ offsetX: -99999 }).offsetX).toBe(-480)
    expect(normalizeLayout({ qrOffsetY: -99999 }).qrOffsetY).toBe(-480)

    // 区间内的值原样保留：以前横向只给 ±16，拖一下就被截断
    expect(normalizeLayout({ offsetX: 120, qrOffsetX: -200 }).offsetX).toBe(120)
    expect(normalizeLayout({ qrOffsetX: -200 }).qrOffsetX).toBe(-200)

    // 明确传 false 才是关掉（与后端 patch 语义一致）
    const off = normalizeLayout({ showPhone: false })
    expect(off.showPhone).toBe(false)
    expect(off.showAvatar).toBe(true)
  })

  it('shiftRange：按内容包围盒反推可拖区间，内容比画布还宽时钉死不动', async () => {
    const mod: any = await import('../pages/poster/poster')
    const { shiftRange } = mod

    // 内容占 [20, 130]，画布 320、边距 8 → 左可挪 -12，右可挪 182
    expect(shiftRange(20, 130, 320, 8)).toEqual([-12, 182])
    // 正好铺满：一点都挪不动
    expect(shiftRange(8, 312, 320, 8)).toEqual([0, 0])
    // 内容比画布还宽（理论上不该发生）：宁可不动，也不要推到更糟的位置
    expect(shiftRange(-50, 400, 320, 8)).toEqual([0, 0])
  })

  it('normalizeCustomText：压平空白、剔控制字符、按字符截断', async () => {
    const mod: any = await import('../pages/poster/poster')
    const { normalizeCustomText } = mod

    // 换行折成空格而不是直接删：英文 "hello\nworld" 直接删会粘成 helloworld
    expect(normalizeCustomText('  a\n\nb  ')).toBe('a b')
    expect(normalizeCustomText('A\u0000B')).toBe('A B')
    expect(normalizeCustomText('   ')).toBe('')
    expect(normalizeCustomText('')).toBe('')

    expect(Array.from(normalizeCustomText('字'.repeat(50))).length).toBe(30)
    // 按码点截断：emoji 不能被劈成半个（半个代理对在 canvas 上是方块）
    expect(Array.from(normalizeCustomText('🎉'.repeat(40))).length).toBe(30)
  })

  it('resolveBgId：从没选过 / 选了原样 / 已下架 三种情况分开处理', async () => {
    const mod: any = await import('../pages/poster/poster')
    const { resolveBgId } = mod
    const posters = [{ id: 'poster-brand' }, { id: 'poster-lichun' }]
    const customs = [{ id: 'c-1' }]

    // 从没选过 → 默认商务款（节气会过时）
    expect(resolveBgId(null, posters, customs)).toBe('poster-brand')
    // 明确选了原样 → 白底，不能被默认款顶掉
    expect(resolveBgId('', posters, customs)).toBe('')
    // 选过的还在
    expect(resolveBgId('poster-lichun', posters, customs)).toBe('poster-lichun')
    expect(resolveBgId('c-1', posters, customs)).toBe('c-1')
    // 已下架 → 回落默认款
    expect(resolveBgId('poster-removed', posters, customs)).toBe('poster-brand')
    expect(resolveBgId('c-gone', posters, customs)).toBe('poster-brand')
    // 连默认款都没有（老后端只返回零星几张）→ 白底
    expect(resolveBgId('poster-removed', [{ id: 'x' }], [])).toBe('')
  })

  it('nearestAvatarSize：取最近档位，等距取较小档', async () => {
    const mod: any = await import('../pages/poster/poster')
    const { nearestAvatarSize } = mod
    expect(nearestAvatarSize(0)).toBe(56)
    expect(nearestAvatarSize(70)).toBe(72)
    expect(nearestAvatarSize(80)).toBe(72)
    expect(nearestAvatarSize(84)).toBe(88)
    expect(nearestAvatarSize(999)).toBe(88)
  })
})

// ---- 元素开关（座机 / 邮箱 / 小程序码）与版式扩展 ----
describe('poster 页 · 元素开关与版式扩展', () => {
  const CARD = {
    employee: {
      name: '张伟', title: '业务经理', phone: '13800000000',
      landline: '0571-88886666', email: 'zhangwei@example.com', avatar: '',
    },
    company: { fullName: '某某公司', address: '某某市某某路 1 号' },
  }
  const texts = (geo: any) => geo.items.map((i: any) => i.text)

  async function boot(card: any = CARD) {
    const opts = await loadPage()
    stubStorage({})
    mockApis(card, undefined, { code: 0, data: { items: POSTER_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    return page
  }

  it('码能向下拖到贴底，贴底时「长按识别名片」不画', async () => {
    const page = await boot()
    const base = page.computeGeometry(320, 460, true)
    // 修复前这里是 0：下界扣掉了提示文字那 32px，而默认位置正好用满它，
    // 于是「下移」永远按不动，码也永远贴不到底边
    expect(base.range.qrYMax).toBeGreaterThan(0)
    expect(base.hint.show).toBe(true)

    page.setData({ layout: { ...page.data.layout, qrOffsetY: 24 } })
    const down = page.computeGeometry(320, 460, true)
    expect(down.qr.y + down.qr.size).toBe(460 - 8)
    // 贴底后提示没地方了：宁可不画，也不能压在码上
    expect(down.hint.show).toBe(false)
  })

  it('码向上仍能顶到画布上沿（上下对称，不再只往上放行）', async () => {
    const page = await boot()
    page.setData({ layout: { ...page.data.layout, qrOffsetY: -9999 } })
    expect(page.computeGeometry(320, 460, true).qr.y).toBe(8)
  })

  it('元素：座机 / 邮箱默认关，打开后才出现在海报上', async () => {
    const page = await boot()
    expect(page.data.layout.showLandline).toBe(false)
    expect(page.data.layout.showEmail).toBe(false)

    const before = texts(page.computeGeometry(320, 460, true))
    expect(before.some((t: string) => t.indexOf('座机') >= 0)).toBe(false)
    expect(before.some((t: string) => t.indexOf('@') >= 0)).toBe(false)

    page.toggleOption({ currentTarget: { dataset: { field: 'showLandline' } } })
    page.toggleOption({ currentTarget: { dataset: { field: 'showEmail' } } })
    const after = texts(page.computeGeometry(320, 460, true))
    expect(after).toContain('座机 0571-88886666')
    expect(after).toContain('zhangwei@example.com')
  })

  it('元素：关掉二维码后不画、不占位、位移区间归零', async () => {
    const page = await boot()
    expect(page.data.layout.showQr).toBe(true)

    page.toggleOption({ currentTarget: { dataset: { field: 'showQr' } } })
    const geo = page.computeGeometry(320, 460, true)
    expect(geo.qr.size).toBe(0)
    expect(geo.hint).toBe(null)
    // 免得「位置」里还能拖一个看不见的东西
    expect(geo.range.qrXMin).toBe(0)
    expect(geo.range.qrYMax).toBe(0)
  })

  it('版式：八套模板都能排出画布内、码不小于下限的版式', async () => {
    const page = await boot()
    const keys = page.data.templates.map((t: any) => t.key)
    expect(keys.length).toBe(8)
    for (const k of ['split', 'left', 'stack', 'poster']) expect(keys).toContain(k)

    for (const key of keys) {
      page.setData({ layout: { ...page.data.layout, template: key } })
      const geo = page.computeGeometry(320, 460, true)
      expect(geo.items.length).toBeGreaterThan(0)
      expect(geo.qr.size).toBeGreaterThanOrEqual(56)
      expect(geo.qr.y + geo.qr.size).toBeLessThanOrEqual(460)
      for (const it of geo.items) {
        expect(it.y).toBeGreaterThan(0)
        expect(it.y).toBeLessThan(460)
        expect(it.x).toBeGreaterThanOrEqual(0)
        expect(it.x).toBeLessThanOrEqual(320)
      }
    }
  })

  it('版式：大字 / 画报带主色分隔线，其余模板不带', async () => {
    const page = await boot()
    page.setData({ layout: { ...page.data.layout, template: 'left' } })
    expect(page.computeGeometry(320, 460, true).rule).not.toBe(null)
    page.setData({ layout: { ...page.data.layout, template: 'poster' } })
    expect(page.computeGeometry(320, 460, true).rule).not.toBe(null)
    page.setData({ layout: { ...page.data.layout, template: 'classic' } })
    expect(page.computeGeometry(320, 460, true).rule).toBe(null)
  })

  it('版式：模板白名单外的 key 落回经典', async () => {
    const mod: any = await import('../pages/poster/poster')
    for (const k of ['split', 'left', 'stack', 'poster']) {
      expect(mod.normalizeLayout({ template: k }).template).toBe(k)
    }
    expect(mod.normalizeLayout({ template: 'nope' }).template).toBe('classic')
  })

  it('版式：九项元素全开时内容仍在画布内，自动档缩到最小可用尺寸', async () => {
    const page = await boot()
    page.setData({
      layout: {
        ...page.data.layout,
        showLandline: true,
        showEmail: true,
        customText: '24 小时上门维修',
      },
    })
    const geo = page.computeGeometry(320, 460, true)
    const lowest = Math.max.apply(null, geo.items.map((i: any) => i.y))
    expect(lowest).toBeLessThan(460)
    expect(geo.qr.size).toBeGreaterThanOrEqual(56)
    // 内容多时「自动」会缩到最小可用尺寸；这时想要大码就显式选档位，
    // 选了就一定生效（见「内容多时档位照样生效」），重叠由白底衬兜住
    expect(geo.qr.size).toBe(56)
  })
})

// ---- 节气日自动选背景 ----
//
// 需求：进入海报页，当天是节气 → 自动选中该节气的背景图，并排到背景条第一位。
//
// 两个必须钉住的点：
//  1. 「当天是节气」是真按公历判的，不是按月份猜——所以要把系统时间钉到
//     具体某一天（2026-10-23 霜降）再进页面，否则测的是「今天」这个变量。
//  2. 自动选中的那张**不能落库**：用户在节气日顺手调下版式，PUT 会把 bgId
//     一起写走。写成节气 id 就把偏好固化了，一个月后进页面还在发上个月的图。
describe('poster 页 · 节气日自动选背景', () => {
  // 只假造 Date，不动 setTimeout：prepare 的 await 链和 syncPrefs 的防抖
  // 都靠真定时器驱动，全量 fake 会让它们永远不触发，测试直接挂死。
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  // 把「今天」钉到指定公历日（m 从 1 起）
  function at(y: number, m: number, d: number) {
    vi.setSystemTime(new Date(y, m - 1, d))
  }

  // 背景清单故意打乱：商务款在最前，两张节气图垫底。
  // 这样「排第一」必须真的发生重排才算过，而不是碰巧就在第一位。
  const TERM_LIST = [
    { id: 'poster-brand', name: '匠心品质', file: '/api/assets/file/posters/poster-brand.jpg', thumb: '/t/brand.jpg' },
    { id: 'poster-lichun', name: '立春', file: '/api/assets/file/posters/poster-lichun.jpg', thumb: '/t/lichun.jpg' },
    { id: 'poster-shuangjiang', name: '霜降', file: '/api/assets/file/posters/poster-shuangjiang.jpg', thumb: '/t/shuangjiang.jpg' },
  ]
  const CARD_SIMPLE = { employee: { name: '张伟', title: '业务经理', phone: '13800000000', avatar: '' } }

  async function bootAt(y: number, m: number, d: number, storage: Record<string, string> = {}) {
    at(y, m, d)
    const opts = await loadPage()
    // 默认登录：本机缓存（posterBg/posterLayout）只对**本人**生效——
    // 访客手机上的那份跟员工排的版式无关，读它就会让「别人看到的」和
    // 「员工排的」对不上（公开快照 exists=false 时一律从默认重算）。
    stubStorage({ empToken: 'tk-1', ...storage })
    mockApis(CARD_SIMPLE, undefined, { code: 0, data: { items: TERM_LIST } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    return page
  }

  it('节气日 + 从没选过：自动选中节气图，并排到背景条第一位', async () => {
    const page = await bootAt(2026, 10, 23) // 霜降
    expect(page.data.bgId).toBe('poster-shuangjiang')
    expect(page.data.bgAuto).toBe(true)
    expect(page.data.todayId).toBe('poster-shuangjiang')
    expect(page.data.todayName).toBe('霜降')
    expect(page.data.todayKind).toBe('term')
    expect(page.data.posters.map((p: any) => p.id)).toEqual([
      'poster-shuangjiang',
      'poster-brand',
      'poster-lichun',
    ])
  })

  it('节气日 + 存了别的背景：不自动选（尊重用户选择），但仍排第一', async () => {
    const page = await bootAt(2026, 10, 23, { 'posterBg:42': 'poster-lichun' })
    expect(page.data.bgId).toBe('poster-lichun')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.posters[0].id).toBe('poster-shuangjiang')
  })

  it('节气日 + 明确选了「原样」：白底不被节气顶掉', async () => {
    // BG_NONE_STORE = 'none' 是「用户明确选了白底」的哨兵，与「从没选过」区分
    const page = await bootAt(2026, 10, 23, { 'posterBg:42': 'none' })
    expect(page.data.bgId).toBe('')
    expect(page.data.bgAuto).toBe(false)
  })

  it('节气日 + 存的正好是默认商务款：仍然自动选', async () => {
    // 关键：默认款是兜底值而不是用户的审美表态。不这么判的话，
    // 第一次在节气日调了下版式就会把偏好固化成默认款，
    // 之后每个节气日都不再自动换了——而节气图的意义就在于「当天」。
    const page = await bootAt(2026, 10, 23, { 'posterBg:42': 'poster-brand' })
    expect(page.data.bgId).toBe('poster-shuangjiang')
    expect(page.data.bgAuto).toBe(true)
  })

  it('既非节日也非节气：与改造前完全一致（默认商务款、无角标、不重排）', async () => {
    const page = await bootAt(2026, 10, 24) // 霜降次日，也不是节日
    expect(page.data.bgId).toBe('poster-brand')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.todayId).toBe('')
    expect(page.data.todayName).toBe('')
    expect(page.data.todayKind).toBe('')
    expect(page.data.posters.map((p: any) => p.id)).toEqual([
      'poster-brand',
      'poster-lichun',
      'poster-shuangjiang',
    ])
  })

  it('节气图不在清单里（素材调整 / 老后端）：回落商务款，不插空位', async () => {
    at(2026, 10, 23)
    const opts = await loadPage()
    stubStorage({})
    mockApis(CARD_SIMPLE, undefined, {
      code: 0,
      data: { items: [{ id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t.jpg' }] },
    })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))

    expect(page.data.bgId).toBe('poster-brand')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.posters).toHaveLength(1)
  })

  it('表外年份（2050）：不猜不插值，退回默认商务款', async () => {
    const page = await bootAt(2050, 10, 23)
    expect(page.data.bgId).toBe('poster-brand')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.todayId).toBe('')
  })

  it('自动选中的节气图不落库：PUT 写的是默认款而不是节气 id', async () => {
    // 用户在节气日只调了下版式（没碰背景），syncPrefs 会把 bgId 一起写走。
    // 写节气 id 就把偏好固化了，一个月后进页面还在发上个月的节气图。
    const page = await bootAt(2026, 10, 23, { empToken: 't' })
    expect(page.data.bgAuto).toBe(true)
    mocks.request.mockClear()

    await page.savePrefsNow()

    const puts = mocks.request.mock.calls.filter((c) => c[0] === 'PUT')
    expect(puts).toHaveLength(1)
    expect(puts[0][2].bgId).toBe('poster-brand')
  })

  it('用户点一下背景就接管：bgAuto 清零，之后落用户选的那张', async () => {
    const page = await bootAt(2026, 10, 23, { empToken: 't' })
    page.pickBg({ currentTarget: { dataset: { id: 'poster-lichun' } } })
    expect(page.data.bgId).toBe('poster-lichun')
    expect(page.data.bgAuto).toBe(false)
    mocks.request.mockClear()

    await page.savePrefsNow()

    const puts = mocks.request.mock.calls.filter((c) => c[0] === 'PUT')
    expect(puts[0][2].bgId).toBe('poster-lichun')
  })

  it('buildState：后端有记录时以后端为准，节气只影响没记录的人', async () => {
    at(2026, 10, 23)
    const opts = await loadPage()
    stubStorage({})
    const page = instance(opts)
    const st = page.buildState({ code: 0, data: { items: TERM_LIST } }, {
      code: 0,
      data: { exists: true, bgId: 'poster-lichun', backgrounds: [], layout: {} },
    })
    expect(st.bgId).toBe('poster-lichun')
    expect(st.bgAuto).toBe(false)
    // 但排序仍然生效：当天是节气，那张就在最前
    expect(st.posters[0].id).toBe('poster-shuangjiang')
  })
})

// ---- 节日日自动选背景（优先于节气） ----
//
// 需求：管理后台素材库补齐节日海报后，小程序端**优先**校验当前日期是否节日，
// 节日时优先自动选节日海报图。
//
// 两个必须钉住的点：
//  1. 「节日优先于节气」是真按公历判的——2026-03-20 同时是龙抬头（农历节日）
//     和春分（节气），必须选龙抬头。同一天两撞的样本 2020-2040 有 14 处，
//     这里挑两个有海报素材的钉住。
//  2. 节日图不在清单里时要**退到节气图**，不能一路退到商务默认款：
//     老后端没有新加的节日图，不能因此连春分那张都不给了。
describe('poster 页 · 节日日自动选背景（优先于节气）', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  function at(y: number, m: number, d: number) {
    vi.setSystemTime(new Date(y, m - 1, d))
  }

  // 故意打乱：商务款在最前，节日图和节气图都垫底。
  const FEST_LIST = [
    { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
    { id: 'poster-chunfen', name: '春分', file: '/c.jpg', thumb: '/t/c.jpg' },
    { id: 'poster-longtaitou', name: '龙抬头', file: '/l.jpg', thumb: '/t/l.jpg' },
    { id: 'poster-chunjie', name: '春节', file: '/j.jpg', thumb: '/t/j.jpg' },
    { id: 'poster-lichun', name: '立春', file: '/i.jpg', thumb: '/t/i.jpg' },
  ]
  const CARD_SIMPLE = { employee: { name: '张伟', title: '业务经理', phone: '13800000000', avatar: '' } }

  async function bootAt(
    y: number,
    m: number,
    d: number,
    storage: Record<string, string> = {},
    list: any[] = FEST_LIST
  ) {
    at(y, m, d)
    const opts = await loadPage()
    // 同节气那组：默认登录，本机缓存只对本人生效
    stubStorage({ empToken: 'tk-1', ...storage })
    mockApis(CARD_SIMPLE, undefined, { code: 0, data: { items: list } })
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId: '42' })
    await new Promise((r) => setTimeout(r, 0))
    return page
  }

  it('节日日 + 从没选过：自动选中节日图，排第一，角标是节日', async () => {
    const page = await bootAt(2026, 2, 17) // 春节
    expect(page.data.bgId).toBe('poster-chunjie')
    expect(page.data.bgAuto).toBe(true)
    expect(page.data.todayId).toBe('poster-chunjie')
    expect(page.data.todayName).toBe('春节')
    expect(page.data.todayKind).toBe('festival')
    expect(page.data.posters.map((p: any) => p.id)).toEqual([
      'poster-chunjie',
      'poster-brand',
      'poster-chunfen',
      'poster-longtaitou',
      'poster-lichun',
    ])
  })

  it('公历节日同样生效：2026-10-01 国庆', async () => {
    // 国庆不撞节气，走的是「日期固定」那条分支。单独给清单：
    // FEST_LIST 里没有 poster-guoqing，用它会走到「节日图不在清单里」的分支。
    const page = await bootAt(2026, 10, 1, {}, [
      { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
      { id: 'poster-guoqing', name: '国庆', file: '/g.jpg', thumb: '/t/g.jpg' },
    ])
    expect(page.data.bgId).toBe('poster-guoqing')
    expect(page.data.todayKind).toBe('festival')
    expect(page.data.todayName).toBe('国庆')
  })

  it('节日优先于节气：2026-03-20 龙抬头（节日）赢春分（节气）', async () => {
    // 同一天既是农历节日又是节气，必须选节日那张
    const page = await bootAt(2026, 3, 20)
    expect(page.data.bgId).toBe('poster-longtaitou')
    expect(page.data.bgAuto).toBe(true)
    expect(page.data.todayId).toBe('poster-longtaitou')
    expect(page.data.todayName).toBe('龙抬头')
    expect(page.data.todayKind).toBe('festival')
    // 节日图第一，节气图第二——两个都还在，只是让位
    expect(page.data.posters.map((p: any) => p.id)).toEqual([
      'poster-longtaitou',
      'poster-chunfen',
      'poster-brand',
      'poster-chunjie',
      'poster-lichun',
    ])
  })

  it('节日优先于节气：2038-02-04 春节（节日）赢立春（节气）', async () => {
    // 换一个样本，防止只在 2026 碰巧对
    const page = await bootAt(2038, 2, 4)
    expect(page.data.bgId).toBe('poster-chunjie')
    expect(page.data.todayName).toBe('春节')
    expect(page.data.todayKind).toBe('festival')
  })

  it('节日图不在清单里：退到节气图，不退到商务款', async () => {
    // 老后端没有 poster-longtaitou，但有 poster-chunfen。
    // 2026-03-20 仍是龙抬头+春分，此时应选春分而不是商务默认款。
    const page = await bootAt(2026, 3, 20, {}, [
      { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
      { id: 'poster-chunfen', name: '春分', file: '/c.jpg', thumb: '/t/c.jpg' },
    ])
    expect(page.data.bgId).toBe('poster-chunfen')
    expect(page.data.bgAuto).toBe(true)
    expect(page.data.todayId).toBe('poster-chunfen')
    expect(page.data.todayName).toBe('春分')
    expect(page.data.todayKind).toBe('term')
  })

  it('节日图与节气图都不在清单里：回落商务款，不插空位', async () => {
    const page = await bootAt(2026, 3, 20, {}, [
      { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
    ])
    expect(page.data.bgId).toBe('poster-brand')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.todayId).toBe('')
    expect(page.data.todayKind).toBe('')
    expect(page.data.posters).toHaveLength(1)
  })

  it('节日日 + 存了别的背景：不自动选（尊重用户选择），但仍排第一', async () => {
    // 存的必须是「非默认款」才算用户表过态；存 poster-brand 会被当成没选过
    // （见 buildState 里 noChoice 的说明），那条路径由下一个用例覆盖。
    const page = await bootAt(2026, 2, 17, { 'posterBg:42': 'poster-lichun' })
    expect(page.data.bgId).toBe('poster-lichun')
    expect(page.data.bgAuto).toBe(false)
    expect(page.data.posters[0].id).toBe('poster-chunjie')
  })

  it('节日日 + 明确选了「原样」：白底不被节日顶掉', async () => {
    const page = await bootAt(2026, 2, 17, { 'posterBg:42': 'none' })
    expect(page.data.bgId).toBe('')
    expect(page.data.bgAuto).toBe(false)
  })

  it('节日日 + 存的正好是默认商务款：仍然自动选', async () => {
    const page = await bootAt(2026, 2, 17, { 'posterBg:42': 'poster-brand' })
    expect(page.data.bgId).toBe('poster-chunjie')
    expect(page.data.bgAuto).toBe(true)
  })

  it('自动选中的节日图不落库：PUT 写的是默认款而不是节日 id', async () => {
    // 与节气同理：写成 poster-chunjie 就把偏好固化了，
    // 明年春节后进页面还在发今年的春节图。
    const page = await bootAt(2026, 2, 17, { empToken: 't' })
    expect(page.data.bgAuto).toBe(true)
    mocks.request.mockClear()

    await page.savePrefsNow()

    const puts = mocks.request.mock.calls.filter((c) => c[0] === 'PUT')
    expect(puts).toHaveLength(1)
    expect(puts[0][2].bgId).toBe('poster-brand')
  })

  it('用户点一下背景就接管：bgAuto 清零', async () => {
    const page = await bootAt(2026, 2, 17, { empToken: 't' })
    page.pickBg({ currentTarget: { dataset: { id: 'poster-lichun' } } })
    expect(page.data.bgId).toBe('poster-lichun')
    expect(page.data.bgAuto).toBe(false)
  })

  it('农历节日撞公历节日：农历赢（2033-02-14 元宵赢情人节）', async () => {
    const page = await bootAt(2033, 2, 14, {}, [
      { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
      { id: 'poster-yuanxiao', name: '元宵', file: '/y.jpg', thumb: '/t/y.jpg' },
      { id: 'poster-qingrenjie', name: '情人节', file: '/q.jpg', thumb: '/t/q.jpg' },
    ])
    expect(page.data.bgId).toBe('poster-yuanxiao')
    expect(page.data.todayName).toBe('元宵')
  })

  it('表外年份（2050）：农历节日不猜，公历节日仍生效', async () => {
    // 2050-01-01 是元旦（公历，不依赖年份表）→ 应有元旦图
    const page = await bootAt(2050, 1, 1, {}, [
      { id: 'poster-brand', name: '匠心品质', file: '/b.jpg', thumb: '/t/b.jpg' },
      { id: 'poster-yuandan', name: '元旦', file: '/y.jpg', thumb: '/t/y.jpg' },
    ])
    expect(page.data.bgId).toBe('poster-yuandan')
    expect(page.data.todayName).toBe('元旦')
    expect(page.data.todayKind).toBe('festival')
  })
})

// ===== 公开快照与只读态（2026-10-28）=====
//
// 背景：转发链接只带 staffId，访客打开海报页是**当场重新生成**一张。
// 原先访客拿不到员工的版式，看到的是默认版式——员工调的排版、挑的背景、
// 写的 slogan 全部传不出去，海报作为「可传播的物料」其实没被传递。
// 这一组用例守两件事：① 访客能拿到员工的版式；② 但拿不到编辑权。

describe('poster 页 · 公开快照与只读态', () => {
  // 员工排好的一份快照：居中模板 + 自定义背景 + 一句 slogan
  const SNAPSHOT = {
    code: 0,
    data: {
      exists: true,
      staffId: MY_STAFF,
      bgId: 'c-abc123',
      backgrounds: [CUSTOM_BG],
      // 座机默认关：显式打开过，正好用来验证「访客拿到的是员工的设置」
      layout: { template: 'center', showLandline: true, customText: '24 小时上门维修' },
    },
  }

  async function bootVisitor(
    storage: Record<string, string> = {},
    publicPrefs: any = SNAPSHOT,
    staffId = '42',
    prefs: any = null
  ) {
    const opts = await loadPage()
    stubStorage(storage)
    mockApis(undefined, undefined, { code: 0, data: { items: POSTER_LIST } }, prefs, publicPrefs)
    stubCanvasNode()
    const page = instance(opts)
    await page.onLoad({ staffId })
    await new Promise((r) => setTimeout(r, 0))
    return page
  }

  it('访客：用公开快照里的版式与背景渲染，且拿不到编辑权', async () => {
    const page = await bootVisitor() // 无 empToken
    expect(page.data.canEdit).toBe(false)
    // 关键：员工排的版式真的传过来了
    expect(page.data.layout.template).toBe('center')
    expect(page.data.layout.customText).toBe('24 小时上门维修')
    expect(page.data.layout.showLandline).toBe(true)
    expect(page.data.bgId).toBe('c-abc123')
    expect(page.data.customs).toHaveLength(1)
    // 说明这行字：访客得知道这是谁的、他能做什么
    expect(page.data.visitorTip).toContain('张伟')
  })

  // 访客手机上的本机缓存跟员工排的版式无关：读它会让「别人看到的」
  // 和「员工排的」对不上（员工自己那份存在他自己手机 + 服务端账号里）
  it('访客：本机缓存不生效（别人手机上的偏好与我无关）', async () => {
    const page = await bootVisitor(
      { 'posterBg:42': 'poster-lichun', 'posterLayout:42': '{"template":"minimal"}' },
      { code: 0, data: { exists: false } }
    )
    expect(page.data.layout.template).toBe('classic') // 默认，不是本机的 minimal
    expect(page.data.canEdit).toBe(false)
  })

  it('访客：一次 PUT 都不发（改不动，也不该往服务端写）', async () => {
    const page = await bootVisitor()
    mocks.request.mockClear()
    await page.savePrefsNow()
    expect(mocks.request.mock.calls.filter((c) => c[0] === 'PUT')).toHaveLength(0)
  })

  it('访客：上传背景被挡（那是员工自己的素材库）', async () => {
    const page = await bootVisitor()
    await page.uploadBg()
    expect((globalThis as any).wx.showModal).toHaveBeenCalled()
    expect((globalThis as any).wx.chooseMedia).not.toHaveBeenCalled()
  })

  // 登录员工也会被转发到同事的名片：那时他不该拿到编辑入口。
  // 判据是「我的 staffId == 页面 staffId」，不是「有没有登录」。
  it('登录员工打开别人的海报页：只读', async () => {
    const page = await bootVisitor({ empToken: 'tk-1' }, SNAPSHOT, '99')
    expect(page.data.canUpload).toBe(true) // 确实登录了
    expect(page.data.canEdit).toBe(false) // 但这张不是我的
    mocks.request.mockClear()
    await page.savePrefsNow()
    expect(mocks.request.mock.calls.filter((c) => c[0] === 'PUT')).toHaveLength(0)
  })

  // 防回归：后端 staffId 是新增字段，老后端不回。若只认它，员工升级后
  // 会突然改不动自己的海报——本机 globalData 那份必须能兜住。
  it('老后端不回 staffId：靠本机的员工编号认定本人，仍可编辑', async () => {
    // 后端 staffId 是 2026-10-28 新增字段。若只认它，员工升级小程序后
    // 会突然改不动自己的海报——本机 globalData 那份必须能兜住。
    const legacy = {
      code: 0,
      data: { exists: true, bgId: 'poster-brand', backgrounds: [], layout: { template: 'stack' } }, // 无 staffId
    }
    const page = await bootVisitor({ empToken: 'tk-1' }, null, '42', legacy)

    expect(page.data.canEdit).toBe(true)
    expect(page.data.layout.template).toBe('stack')
  })

  it('本人：可编辑，且没有只读说明', async () => {
    const page = await bootVisitor({ empToken: 'tk-1' }, null, '42', SNAPSHOT)

    expect(page.data.canEdit).toBe(true)
    expect(page.data.visitorTip).toBe('')
    expect(page.data.layout.template).toBe('center')
  })
})
