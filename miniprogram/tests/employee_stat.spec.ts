// 员工数据统计页（B10）：时间筛选 + 转化率 + 趋势柱状 + 内容浏览 TOP3 +
// 裂变 + 访客从哪来（来源分布）。
//
// 这页最容易错的三件事，各有专门的用例盯着：
//  1. 未登录 / 令牌失效必须回登录页——这页没有游客态，卡在空页对员工
//     来说就是「数据没了」。
//  2. 时间范围必须同时作用于汇总与趋势（同一个 from/to 发给后端），
//     切范围必须重新请求，而切指标不能重新请求（同一份响应里八项都有）。
//  3. 来源分布各行之和必须等于访问数：两者一旦漂移，「共 N 次访问」就和
//     转化卡对不上，而且不会报任何错。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({ code: 0, data: {} })),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) => u || '',
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  mocks.request.mockClear()
})

// 与页面同一套日期推导（本地时区），但**独立实现**：
// 若页面改成 UTC 或改用服务端日期，这里会立刻对不上。
function dayStr(offset: number): string {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

async function page() {
  const mod = await import('../pages/employee/stat/stat')
  return { p: makeInstance(capturedPage()), mod }
}

function lastToast(): string {
  const calls = wxStub.showToast.mock.calls
  return calls.length ? calls[calls.length - 1][0].title : ''
}

// 一份内容完整的后端响应（八计数 + 趋势 + 两个 TOP 榜），形状照
// /api/employee/me/stat 的真实回包。
function fullData(over: Record<string, any> = {}) {
  return {
    visit: 12,
    call: 3,
    save: 2,
    projectView: 5,
    serviceView: 2,
    videoPlay: 1,
    share: 1,
    posterSave: 1,
    trend: [
      { date: dayStr(-1), visit: 5, call: 1, save: 0 },
      { date: dayStr(0), visit: 7, call: 2, save: 2 },
    ],
    topProjects: [
      { targetId: 11, name: '热门案例', count: 5 },
      { targetId: 12, name: '冷门案例', count: 3 },
      { targetId: 13, name: '已删除', count: 1 },
    ],
    topServices: [{ targetId: 21, name: '服务一', count: 2 }],
    // 来源分布各行之和必须等于 visit（7+3+2=12）：这条对账关系由下面的
    // 用例守着——一旦后端改了过滤条件，页面上的「共 N 次访问」就会
    // 和转化卡里的数字对不上，且不报任何错。
    sourceDist: [
      { source: 'scan', count: 7 },
      { source: 'poster', count: 3 },
      { source: 'share_card', count: 2 },
    ],
    ...over,
  }
}

describe('employee/stat 页', () => {
  it('无令牌：直接跳登录页，不发请求（未登录没有数据可看）', async () => {
    const { p } = await page()

    await p.onShow()

    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/employee/login/login' })
    expect(mocks.request).not.toHaveBeenCalled()
  })

  // onShow 而非 onLoad：从后台切回来要刷新数字，挂在 onLoad 上只会显示进页面那一次
  it('有令牌：按默认「7 天」范围请求，并落位八项计数', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    // 默认 7 天：from = 今天前 6 天，to = 今天（闭区间，含今天共 7 天）
    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/employee/me/stat', {
      from: dayStr(-6),
      to: dayStr(0),
    })
    expect(p.data.stat).toEqual({
      visit: 12,
      call: 3,
      save: 2,
      projectView: 5,
      serviceView: 2,
      videoPlay: 1,
      share: 1,
      posterSave: 1,
    })
    expect(p.data.loading).toBe(false)
    expect(p.data.empty).toBe(false)
  })

  // 转化率 = 次数 ÷ 访问次数。3/12 = 25.0%，2/12 ≈ 16.7%
  it('转化率按「次数 ÷ 访问次数」算，保留一位小数', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.rateCall).toBe('25.0%')
    expect(p.data.rateSave).toBe('16.7%')
  })

  // 分母为 0 回 '—' 而不是 '0.0%'：「没人访问」和「访问了没人点」是两件事
  it('访问为 0 时转化率显示 —（不是 0.0%）', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({
      code: 0,
      data: fullData({ visit: 0, call: 0, save: 0, projectView: 0, serviceView: 0, videoPlay: 0, share: 0, posterSave: 0, trend: [] }),
    })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.rateCall).toBe('—')
    expect(p.data.rateSave).toBe('—')
    // 八项全 0 → 空态
    expect(p.data.empty).toBe(true)
  })

  // 同一访客可重复点击，率可能超过 100%：页面文案已明示，这里守住不被「修正」成上限 100%
  it('点击次数多于访问次数时率可超过 100%', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData({ visit: 2, call: 5, save: 0 }) })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.rateCall).toBe('250.0%')
    expect(p.data.rateSave).toBe('0.0%')
  })

  // 内容浏览与裂变分两组、**不相加**：海报保存的主体很可能是员工本人
  // （「我的 → 生成海报」只有本人进得去），混进浏览会让「访客在看什么」虚高。
  it('内容浏览三类与裂变两类分别汇总，两组不相加', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.browseItems).toEqual([
      { label: '案例浏览', value: 5 },
      { label: '服务浏览', value: 2 },
      { label: '视频播放', value: 1 },
    ])
    expect(p.data.browseTotal).toBe(8) // 5+2+1，不含转发/海报
    expect(p.data.fissionItems).toEqual([
      { label: '转发', value: 1 },
      { label: '海报保存', value: 1 },
    ])
    expect(p.data.fissionTotal).toBe(2) // 1+1，不含浏览
    expect(p.data.topProjects).toHaveLength(3)
    expect(p.data.topServices).toEqual([{ targetId: 21, name: '服务一', count: 2 }])
  })

  // 只有裂变、没有浏览时不能算空页：那也是一种真实的活动结果
  it('只有裂变事件时不算空态', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({
      code: 0,
      data: fullData({
        visit: 0, call: 0, save: 0,
        projectView: 0, serviceView: 0, videoPlay: 0,
        share: 2, posterSave: 1,
        trend: [], topProjects: [], topServices: [],
      }),
    })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.browseTotal).toBe(0)
    expect(p.data.fissionTotal).toBe(3)
    expect(p.data.empty).toBe(false)
  })

  // 后端只回有事件的日期，补零必须在前端做：漏了就会出现「昨天有 5 次访问，
  // 但图上只有一根柱子」——看着像数据丢了
  it('趋势按所选范围补零成完整按天序列（7 天 = 7 根柱子）', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    const trend = p.data.trend as any[]
    expect(trend).toHaveLength(7)
    expect(trend[6].date).toBe(dayStr(0))
    expect(trend[5].date).toBe(dayStr(-1))
    // 有事件的两天落位，其余补 0
    expect(trend[5].value).toBe(5)
    expect(trend[6].value).toBe(7)
    expect(trend[0].value).toBe(0)
    // 高度按峰值归一，且非零值有最小可见高度
    expect(trend[6].height).toBe(100)
    expect(trend[0].height).toBe(0)
    // 7 天内每天都标日期
    expect(trend.every((t) => t.showLabel)).toBe(true)
    expect(p.data.metricTotal).toBe(12)
    expect(p.data.trendMax).toBe(7)
  })

  // 切指标只换取值字段：不重新请求（同一份响应里八项都有，重发是白跑一趟）
  it('切换指标不重新请求，只重建趋势与合计', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()
    const callsBefore = mocks.request.mock.calls.length

    await p.onMetricTap({ currentTarget: { dataset: { key: 'call' } } })

    expect(mocks.request.mock.calls.length).toBe(callsBefore)
    expect(p.data.metricKey).toBe('call')
    const trend = p.data.trend as any[]
    expect(trend[5].value).toBe(1)
    expect(trend[6].value).toBe(2)
    expect(p.data.metricTotal).toBe(3)
    expect(p.data.trendMax).toBe(2)
  })

  it('点同一个指标：什么都不做', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()
    const before = JSON.stringify(p.data.trend)

    await p.onMetricTap({ currentTarget: { dataset: { key: 'visit' } } })

    expect(JSON.stringify(p.data.trend)).toBe(before)
  })

  // 范围变了就是另一份数据：必须带上新的 from/to 重新请求
  it('切到「今日」：按 1 天范围重新请求，趋势只有 1 根柱子', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData({ trend: [] }) })
    await p.onRangeTap({ currentTarget: { dataset: { key: 'today' } } })
    await flush()

    expect(mocks.request).toHaveBeenLastCalledWith('GET', '/api/employee/me/stat', {
      from: dayStr(0),
      to: dayStr(0),
    })
    expect(p.data.rangeKey).toBe('today')
    expect(p.data.trend).toHaveLength(1)
  })

  it('切到「30 天」：趋势 30 根柱子，标签每 5 天标一次以免叠成一团', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData({ trend: [] }) })
    await p.onRangeTap({ currentTarget: { dataset: { key: '30d' } } })
    await flush()

    expect(mocks.request).toHaveBeenLastCalledWith('GET', '/api/employee/me/stat', {
      from: dayStr(-29),
      to: dayStr(0),
    })
    const trend = p.data.trend as any[]
    expect(trend).toHaveLength(30)
    expect(trend[0].showLabel).toBe(true) // 第 0 天必标
    expect(trend[1].showLabel).toBe(false)
    expect(trend[29].showLabel).toBe(true) // 最后一天必标
  })

  it('点同一个时间档：不重复请求', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()
    const callsBefore = mocks.request.mock.calls.length

    await p.onRangeTap({ currentTarget: { dataset: { key: '7d' } } })

    expect(mocks.request.mock.calls.length).toBe(callsBefore)
  })

  // 令牌过期时 request 封装已清 storage，这里必须把人送回登录页，
  // 否则页面停在 loading 空白，用户以为「没数据」
  it('接口返回 401：跳登录页', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 401, msg: '登录失效' })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/employee/login/login' })
    expect(p.data.stat).toBeNull()
  })

  it('其它错误：提示后端 msg，loading 必须落下', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '统计服务不可用' })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(lastToast()).toBe('统计服务不可用')
    expect(p.data.loading).toBe(false)
  })

  it('错误且无 msg：回落「加载失败」', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 500 })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(lastToast()).toBe('加载失败')
  })

  it('加载中先置 loading=true（数字区显示骨架而不是 0）', async () => {
    storage.set('empToken', 'tk-1')
    let release: any
    mocks.request.mockImplementationOnce(() => new Promise((r) => (release = r)))
    const { p } = await page()

    p.onShow() // onShow 内部才 async，这里同步返回，靠后续 flush 推进
    expect(p.data.loading).toBe(true)

    release({ code: 0, data: fullData() })
    await flush()
    expect(p.data.loading).toBe(false)
  })

  // ===== 纯函数直测（页面用到的两个换算，出错的代价都是「数字全错但不报错」） =====
  it('rateText：分母为 0 回 —，否则一位小数的百分比', async () => {
    const { mod } = await page()
    expect(mod.rateText(3, 12)).toBe('25.0%')
    expect(mod.rateText(0, 12)).toBe('0.0%')
    expect(mod.rateText(3, 0)).toBe('—')
    expect(mod.rateText(5, 2)).toBe('250.0%')
  })

  // 来源分布是本页唯一能证明「海报有用」的一块：裂变里的海报保存主体可能是
  // 员工本人，只有「海报」渠道带回来的访问数才是外部证据。
  it('访客从哪来：转中文标签并按占比画条，各行之和等于访问数', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({ code: 0, data: fullData() })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.sourceDist).toEqual([
      { source: 'scan', label: '扫码', count: 7, pct: 58, width: 58 },
      { source: 'poster', label: '海报', count: 3, pct: 25, width: 25 },
      { source: 'share_card', label: '转发', count: 2, pct: 17, width: 17 },
    ])
    // 对账：分布各行之和 == 名片访问数（后端只按 visit 分组，不该多也不该少）
    expect(p.data.sourceTotal).toBe(p.data.stat.visit)
  })

  // 老后端（迁移 0022 前）不回 sourceDist：不能崩、也不能画出一堆 0 行
  it('响应里没有 sourceDist：落空数组，页面走空态文案而不是报错', async () => {
    storage.set('empToken', 'tk-1')
    mocks.request.mockResolvedValueOnce({
      code: 0,
      data: fullData({ sourceDist: undefined }),
    })
    const { p } = await page()

    await p.onShow()
    await flush()

    expect(p.data.sourceDist).toEqual([])
    expect(p.data.sourceTotal).toBe(0)
    expect(p.data.empty).toBe(false) // 只有来源缺失，其它块照常显示
  })

  it('buildSourceDist：空数组得空；空 source 归一未知；小额给最小可见条宽', async () => {
    const { mod } = await page()
    expect(mod.buildSourceDist([])).toEqual([])
    expect(mod.buildSourceDist(undefined)).toEqual([])

    // 老数据 source 为空串：显示成「未知」而不是留一个空白标签
    const one = mod.buildSourceDist([{ source: '', count: 5 }])
    expect(one[0].source).toBe('unknown')
    expect(one[0].label).toBe('未知')
    expect(one[0].pct).toBe(100)

    // 1 次海报访问在 200 次扫码旁边：占比四舍五入是 0，条会完全消失。
    // 而这几条恰恰是员工最想看到的，故给 4% 下限
    const two = mod.buildSourceDist([
      { source: 'scan', count: 200 },
      { source: 'poster', count: 1 },
    ])
    expect(two[0].width).toBe(100)
    expect(two[1].pct).toBe(0)
    expect(two[1].width).toBe(4)
  })

  it('buildTrend：未知指标按 0，缺日补 0，高度按峰值归一', async () => {
    const { mod } = await page()
    const trend = mod.buildTrend([{ date: dayStr(0), visit: 4, call: 3 }], 3, 'save')
    expect(trend).toHaveLength(3)
    expect(trend[2].value).toBe(0) // 该日没有 save 事件
    const t2 = mod.buildTrend([{ date: dayStr(0), visit: 4 }, { date: dayStr(-1), visit: 2 }], 3, 'visit')
    expect(t2.map((o: any) => o.value)).toEqual([0, 2, 4])
    expect(t2[2].height).toBe(100)
    expect(t2[1].height).toBe(50)
    expect(t2[0].height).toBe(0)
  })
})
