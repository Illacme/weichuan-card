import { request } from '../../../utils/request'
import { sourceLabel } from '../../../utils/track'

// 员工数据统计页（B10）：时间筛选 + 转化率 + 按天趋势 + 内容浏览 TOP3 +
// 裂变 + **访客从哪来**（来源分布）。
//
// 口径（页面底部有同样的说明，改这里也要改那里）：
//  - 八类事件全是「次数」。visit 是名片被打开的次数（按页面实例去重），
//    不是「多少个不同的客户」——系统没有访客标识，UV 现在算不出来。
//  - 「拨号率 / 保存率」= 次数 ÷ 访问次数。同一访客可重复点击，故可能 >100%；
//    它回答的是「每次访问带来多少行动」，不是「多少比例的访客行动了」。
//  - 时间范围统一由 from/to 交给后端，汇总 / 趋势 / TOP 榜共用同一个范围。
//    若某一处另算一套，就会出现「汇总 30 天、趋势画 7 天」这种页面上
//    完全看不出来、只能靠人工对账发现的错位。
//  - 八类事件分三组，界面上也分三块，**互相不相加**：
//      转化（访问/拨号/保存）→ 有没有人找上门
//      内容浏览（案例/服务/视频）→ 访客在看什么，只有这一组有 TOP 榜
//      裂变（转发/海报保存）→ 名片被传出去多少，主体可能是员工本人
//    后一条是关键：海报页唯一的入口是「我的 → 生成海报」，只有本人才进得去，
//    把员工自己保存自己的海报算成「访客对什么内容感兴趣」会把浏览总量抬高。
//  - 「访客从哪来」只统计 visit（进入名片的入口事件），故**各行相加 = 名片访问数**。
//    这一块是页面上唯一能证明海报有用的数字：裂变里的「海报保存」主体可能是本人，
//    「海报」渠道的访问数才是真凭实据。
//
// 进入方式：「我的」页 navigateTo；用 onShow 拉取而不是 onLoad——
// 从后台切回来、或从别的页面返回时要刷新数字。
// 401（令牌失效，request 封装已清 storage）时 redirect 回登录页，避免卡在空页。

type RangeKey = 'today' | '7d' | '30d'
type MetricKey = 'visit' | 'call' | 'save'

interface RangeDef {
  key: RangeKey
  label: string
  days: number
}

interface TrendItem {
  date: string
  label: string
  value: number
  height: number
  showLabel: boolean
}

// 三档时间范围。为什么不给「全部」：趋势图要按天画柱，全部时间可能是
// 上千根柱子，既画不出也没人看得清；而摘要用「全部」、图表用「近 30 天」
// 又会让同一屏里的数字互相对不上（口径必须一致）。
const RANGES: RangeDef[] = [
  { key: 'today', label: '今日', days: 1 },
  { key: '7d', label: '7 天', days: 7 },
  { key: '30d', label: '30 天', days: 30 },
]

// 趋势图可切换的三个指标。只给转化三项：内容浏览与裂变已经在下面的
// 两块清单里列全，图表再塞五项会让图例比图还长，手机上点不过来。
const METRICS: { key: MetricKey; label: string }[] = [
  { key: 'visit', label: '访问' },
  { key: 'call', label: '拨号' },
  { key: 'save', label: '保存' },
]

// 内容浏览三类：唯一带 targetId（案例/服务 id）的事件，也只有它们进 TOP 榜。
// 口径见后端 statEventTypes 与 stat.go 文件头的三组分工。
const BROWSE_ITEMS: { key: string; label: string }[] = [
  { key: 'projectView', label: '案例浏览' },
  { key: 'serviceView', label: '服务浏览' },
  { key: 'videoPlay', label: '视频播放' },
]

interface SourceItem {
  source: string
  label: string
  count: number
  pct: number
  width: number
}

// 裂变两类：不带 targetId、不指向内容对象，主体可能是员工本人
// （「我的 → 生成海报」只有本人进得去）。故单独成组，不与浏览相加。
const FISSION_ITEMS: { key: string; label: string }[] = [
  { key: 'share', label: '转发' },
  { key: 'posterSave', label: '海报保存' },
]

function pad2(n: number): string {
  return n < 10 ? '0' + n : '' + n
}

// dayStr 取相对今天的日期串（YYYY-MM-DD），按设备本地日期算——
// 与管理端 Stats.vue 的日期快捷选项同口径（页面上有说明）。
// 为什么不向服务端要日期列表：趋势接口只回「有事件的那几天」，
// 补零必须有一端来做；放前端能保证「今日」一定包含今天。
function dayStr(offset: number): string {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate())
}

// rateText 转化率文案。分母为 0 时回 '—' 而不是 '0.0%'：
// 「没有访问」和「访问了但没人点击」是两件事，都显示 0.0% 会把前者藏起来。
export function rateText(numerator: number, denominator: number): string {
  if (!denominator) return '—'
  return ((numerator / denominator) * 100).toFixed(1) + '%'
}

// buildTrend 把接口返回的稀疏趋势行补成完整的按天序列。
// 指标切换只换取值字段、不重新请求：同一份响应里八项都有（后端 statTrend 全给）。
export function buildTrend(raw: any[], days: number, metric: MetricKey): TrendItem[] {
  const map: Record<string, any> = {}
  for (const r of raw || []) map[String(r.date)] = r
  const out: TrendItem[] = []
  for (let i = 0; i < days; i++) {
    const date = dayStr(i - (days - 1))
    const row = map[date] || {}
    out.push({
      date,
      label: date.slice(5),
      value: Number(row[metric]) || 0,
      height: 0,
      showLabel: false,
    })
  }
  let max = 0
  for (const o of out) max = Math.max(max, o.value)
  const scale = Math.max(1, max)
  out.forEach((o, i) => {
    // 有值就至少给 6% 高度：否则「1 次访问」在峰值 50 的那几天旁边
    // 是一条几乎看不见的线，看着像 0
    o.height = o.value > 0 ? Math.max(6, Math.round((o.value / scale) * 100)) : 0
    // 30 天时逐日标签会叠成一团：每 5 天标一次，最后一天必标
    o.showLabel = days <= 7 || i % 5 === 0 || i === days - 1
  })
  return out
}

// buildSourceDist 把接口回的来源分布转成可直接渲染的行（后端已按 cnt DESC 排好，
// 端上不再重排——重排就得再定一套并列规则，两端顺序不一致会引来对账疑问）。
//
// 两个细节：
//  - 分母取「各行之和」而不是访问总数：后端口径下两者必然相等（分布只统计 visit、
//    空 source 归一 unknown，不会漏行），但用分布自身求和能保证这一屏的百分比
//    加起来不超过 100%，将来后端哪怕加了过滤条件也不会画出溢出容器的条。
//  - 条宽给 4% 下限：1 次海报访问在 200 次扫码旁边是一条几乎看不见的线，
//    看着像没数据——而恰恰这几条才是员工最想看到的。
export function buildSourceDist(raw: any[]): SourceItem[] {
  const rows = Array.isArray(raw) ? raw : []
  let total = 0
  for (const r of rows) total += Number(r && r.count) || 0
  const out: SourceItem[] = []
  for (const r of rows) {
    const count = Number(r && r.count) || 0
    // source 空串兜底 unknown：后端已归一，但老版本接口（0022 迁移前）会回空，
    // 直接渲染会留一个空白标签，看着像数据缺了一行
    const source = String((r && r.source) || 'unknown')
    const pct = total > 0 ? Math.round((count / total) * 100) : 0
    out.push({ source, label: sourceLabel(source), count, pct, width: count > 0 ? Math.max(4, pct) : 0 })
  }
  return out
}

Page({
  data: {
    loading: true,
    stat: null as any,
    ranges: RANGES,
    rangeKey: '7d' as RangeKey,
    metrics: METRICS,
    metricKey: 'visit' as MetricKey,
    // 转化率（字符串，分母为 0 时是 '—'）
    rateCall: '—',
    rateSave: '—',
    // 趋势图
    trend: [] as TrendItem[],
    metricTotal: 0,
    trendMax: 0,
    // 内容浏览：合计 + 三类明细；裂变：合计 + 两类明细（两组不相加）
    browseTotal: 0,
    browseItems: [] as { label: string; value: number }[],
    fissionTotal: 0,
    fissionItems: [] as { label: string; value: number }[],
    // 内容浏览 TOP3（案例 / 服务各一组）
    topProjects: [] as any[],
    topServices: [] as any[],
    // 访客从哪来：visit 按进入渠道拆分（各行之和 == 名片访问数）
    sourceDist: [] as SourceItem[],
    sourceTotal: 0,
    // 八项全 0 → 空态
    empty: false,
  },

  // 原始趋势行（接口原样）。放实例属性而不是 data：
  // 它只是指标切换的来源，进 data 会让每一行都参与一次 setData 序列化，
  // 30 天 × 8 项白跑一趟渲染层。
  rawTrend: [] as any[],

  onShow() {
    this.load()
  },

  // 时间范围：切了就重新拉——范围不同就是另一份数据，不能拿旧响应凑
  onRangeTap(e: any) {
    const key = (e.currentTarget.dataset.key || '') as RangeKey
    if (key === this.data.rangeKey) return
    const def = RANGES.find((r) => r.key === key)
    if (!def) return
    this.setData({ rangeKey: key })
    this.load()
  },

  // 指标切换：只换取值字段，原始趋势留在实例上，不重复请求
  onMetricTap(e: any) {
    const key = (e.currentTarget.dataset.key || '') as MetricKey
    if (key === this.data.metricKey || !METRICS.some((m) => m.key === key)) return
    const days = this.rangeDays()
    const trend = buildTrend(this.rawTrend || [], days, key)
    let total = 0
    let max = 0
    for (const o of trend) {
      total += o.value
      max = Math.max(max, o.value)
    }
    this.setData({ metricKey: key, trend, metricTotal: total, trendMax: max })
  },

  rangeDays(): number {
    const def = RANGES.find((r) => r.key === this.data.rangeKey)
    return def ? def.days : 7
  },

  async load() {
    if (!wx.getStorageSync('empToken')) {
      wx.redirectTo({ url: '/pages/employee/login/login' })
      return
    }
    const days = this.rangeDays()
    this.setData({ loading: true })
    const r = await request('GET', '/api/employee/me/stat', {
      from: dayStr(-(days - 1)),
      to: dayStr(0),
    })
    this.setData({ loading: false })
    if (r.code === 0 && r.data) {
      this.apply(r.data, days)
    } else if (r.code === 401) {
      wx.redirectTo({ url: '/pages/employee/login/login' })
    } else {
      wx.showToast({ title: r.msg || '加载失败', icon: 'none' })
    }
  },

  apply(d: any, days: number) {
    const num = (v: any) => Number(v) || 0
    const stat = {
      visit: num(d.visit),
      call: num(d.call),
      save: num(d.save),
      projectView: num(d.projectView),
      serviceView: num(d.serviceView),
      videoPlay: num(d.videoPlay),
      share: num(d.share),
      posterSave: num(d.posterSave),
    }
    // 两组各自算合计、互不相加：内容浏览答「访客在看什么」，
    // 裂变答「名片被传出去多少」，加在一起两个问题都答不准。
    const sumOf = (items: { key: string; label: string }[]) => {
      let total = 0
      const list = items.map((it) => {
        const value = num((stat as any)[it.key])
        total += value
        return { label: it.label, value }
      })
      return { total, list }
    }
    const browse = sumOf(BROWSE_ITEMS)
    const fission = sumOf(FISSION_ITEMS)
    const sourceDist = buildSourceDist(d.sourceDist)
    let sourceTotal = 0
    for (const s of sourceDist) sourceTotal += s.count
    // 原始趋势留在实例上，指标切换时不必再打一次接口
    this.rawTrend = Array.isArray(d.trend) ? d.trend : []
    const trend = buildTrend(this.rawTrend, days, this.data.metricKey)
    let metricTotal = 0
    let trendMax = 0
    for (const o of trend) {
      metricTotal += o.value
      trendMax = Math.max(trendMax, o.value)
    }
    this.setData({
      stat,
      rateCall: rateText(stat.call, stat.visit),
      rateSave: rateText(stat.save, stat.visit),
      browseTotal: browse.total,
      browseItems: browse.list,
      fissionTotal: fission.total,
      fissionItems: fission.list,
      trend,
      metricTotal,
      trendMax,
      topProjects: Array.isArray(d.topProjects) ? d.topProjects : [],
      topServices: Array.isArray(d.topServices) ? d.topServices : [],
      sourceDist,
      sourceTotal,
      // 八项全 0 才算空：只看访问的话，「昨天有 3 个人看了案例、
      // 今天只筛今天」会被误判成空页
      empty: stat.visit + stat.call + stat.save + browse.total + fission.total === 0,
    })
  },
})
