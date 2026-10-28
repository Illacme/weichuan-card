import axios from 'axios'
import http from './request'

// 全局名片统计（超管）：汇总 + 按员工 + 按天趋势 + 内容浏览 TOP 榜。
// 与员工端 /api/employee/me/stat（仅本人）互补，见 backend/internal/handler/apply.go。
//
// from/to 为 YYYY-MM-DD（闭区间），均可缺省——缺省即「全部时间」口径。
export interface StatRange {
  from?: string
  to?: string
}

// 清掉空值再进 params：缺省日期不应变成 from=&to= 传给后端
// （后端虽也按空串处理，但请求 URL 干净些，日志与排查都少一层歧义）。
function cleanParams(range: StatRange): Record<string, string> {
  const out: Record<string, string> = {}
  if (range.from) out.from = range.from
  if (range.to) out.to = range.to
  return out
}

// 汇总口径与后端 backend/internal/handler/apply.go 的 /api/admin/stats 对齐：
// visit/call/save 是「转化」三指标；projectView/serviceView/videoPlay 是
// 「内容浏览」（migrations/0021 起带 target_id，回答「访客对什么内容感兴趣」，
// 也进 TOP 榜）；share/posterSave 是「裂变」（把名片传出去的动作，不带 target_id、
// 主体可能是员工本人，故不与内容浏览相加）。分组理由见后端 stat.go 文件头。
export interface StatSummary {
  visit: number
  call: number
  save: number
  projectView: number
  serviceView: number
  videoPlay: number
  share: number
  posterSave: number
}

// TOP 榜行：targetId 即对象 ID（案例/服务），name 为对象名（已删除对象显示
// 「已删除」——后端 COALESCE 保证计数不减，见 stat.go statTopTargets）。
export interface StatTopItem {
  targetId: number
  name: string
  count: number
}

// 来源分布行：来源类别 + 其 visit 事件数。source 取值与后端 statSourceTypes 一致：
// scan(扫码) / share_card(转发) / search(搜索) / poster(海报) / unknown(缺省) / other(脏值)。
export interface StatSourceItem {
  source: string
  count: number
}

// 裂变榜行：被转发的员工 + 其名片靠转发带来的新访客数（source=share_card 的 visit 聚合）。
export interface StatFissionItem {
  staffId: number
  name: string
  count: number
}

// source 中文标签映射（后端只回英文类别，前端做展示翻译）。
export const SOURCE_LABELS: Record<string, string> = {
  scan: '扫码',
  share_card: '转发',
  search: '搜索',
  poster: '海报',
  unknown: '未知',
  other: '其他',
}

const zeroSummary = (): StatSummary => ({
  visit: 0,
  call: 0,
  save: 0,
  projectView: 0,
  serviceView: 0,
  videoPlay: 0,
  share: 0,
  posterSave: 0,
})

export function adminStats(range: StatRange = {}) {
  // 响应拦截器回传整包信封 {code,msg,data}，汇总/按员工/趋势/TOP 榜均在 data 下
  return http.get('/admin/stats', { params: cleanParams(range) }).then((r: any) => {
    const s = r.data?.summary ?? {}
    // 逐项 Number 兜底：后端理论上八项都在，但老副本（未跑迁移 0021）会缺
    // 后五项——缺项按 0 渲染，而不是把 undefined 显示成「undefined」
    const summary = zeroSummary()
    for (const k of Object.keys(summary) as (keyof StatSummary)[]) {
      summary[k] = Number(s[k] ?? 0)
    }
    return {
      summary,
      byStaff: (r.data?.byStaff ?? []) as any[],
      trend: (r.data?.trend ?? []) as any[],
      topProjects: (r.data?.topProjects ?? []) as StatTopItem[],
      topServices: (r.data?.topServices ?? []) as StatTopItem[],
      // 来源分布 / 裂变榜：迁移 0022 之前的旧副本缺这两个字段，兜底空数组。
      // 老数据 source 全为 unknown，前端按英文类别翻译（SOURCE_LABELS）。
      sourceDist: (r.data?.sourceDist ?? []) as StatSourceItem[],
      fissionTop: (r.data?.fissionTop ?? []) as StatFissionItem[],
    }
  })
}

// ===== 埋点明细（聚合的下钻）=====
// GET /api/admin/stats/detail：与 adminStats 同租户隔离、同日期口径，
// 分页信封 {code:0,data,total,page,size}（全站约定，见 backend pagination.go）。
// staffId/eventType/targetId 筛选一起下沉到服务端——明细表随运营时间单调增长，
// 拉全表前端切片只会把「网络传了 1 万条」的代价藏起来。
export interface StatDetailItem {
  id: number
  staffId: number
  name: string
  eventType: string // visit / call / save / project_view / service_view / video_play / share / poster_save
  statDate: string
  /** "YYYY-MM-DD HH:mm:ss"，已按服务端 TZ 格式化——前端不再按时区换算，
   *  否则跨时区浏览时明细时间会与趋势图的日期边界对不上 */
  createdAt: string
  /** 关联对象 ID（0 = 无对象）；配 eventType=project_view/service_view 下钻 TOP 榜 */
  targetId?: number
  /** 对象名（案例名/服务名）；已删除对象为「已删除」，无对象为「—」 */
  targetName?: string
}

export interface StatDetailQuery {
  from?: string
  to?: string
  staffId?: number
  eventType?: string
  /** 内容对象筛选：TOP 榜行点击下钻时带（配 eventType=project_view/service_view） */
  targetId?: number
  page?: number
  size?: number
}

export function adminStatsDetail(query: StatDetailQuery = {}) {
  const params: Record<string, string> = { ...cleanParams(query) }
  if (query.staffId) params.staffId = String(query.staffId)
  if (query.eventType) params.eventType = query.eventType
  if (query.targetId) params.targetId = String(query.targetId)
  params.page = String(query.page ?? 1)
  params.size = String(query.size ?? 20)
  // 响应拦截器回传整包信封：data 是当前页数组，分页元信息在顶层
  return http.get('/admin/stats/detail', { params }).then((r: any) => ({
    list: (r.data ?? []) as StatDetailItem[],
    total: Number(r.total ?? 0),
    page: Number(r.page ?? 1),
    size: Number(r.size ?? 20),
  }))
}

// 导出走独立 axios 实例（无业务拦截器）：拦截器按 {code,msg} 信封解析，
// 而 CSV 响应体不是 JSON——走拦截器会被误判成「业务失败」弹红条并 reject。
// withCredentials 照旧带上（超管是 httpOnly Cookie 鉴权）。
const rawHttp = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '/api',
  withCredentials: true,
})

/** 导出统计 CSV 并触发浏览器下载（文件名取自 Content-Disposition）。 */
export async function exportStatsCsv(range: StatRange = {}): Promise<void> {
  const resp = await rawHttp.get('/admin/stats/export', {
    params: cleanParams(range),
    responseType: 'blob',
  })
  // 后端业务失败（code:1 / 401 等）走的是 HTTP 200 + JSON 信封，
  // 在 responseType:'blob' 下会变成一个装着 JSON 文本的 Blob——
  // 不拦就会把错误信封当 CSV 下载给用户（打开是一串 JSON，还以 .csv 命名）。
  const ct = String(resp.headers['content-type'] || '')
  if (ct.includes('json')) {
    const text = await (resp.data as Blob).text()
    let msg = '导出失败'
    try {
      msg = JSON.parse(text).msg || msg
    } catch {
      /* 非标准信封：用兜底文案 */
    }
    throw new Error(msg)
  }
  const filename = parseCsvFilename(resp.headers['content-disposition'])
  const url = URL.createObjectURL(new Blob([resp.data], { type: 'text/csv;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // 立即 revoke 在部分浏览器会取消尚未开始的下载，故延后一拍再回收
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/**
 * 从 Content-Disposition 解析下载文件名。
 * 优先 filename*（RFC 5987，服务端中文名走这里，百分号编码），
 * 回落 filename，再兜底 stats.csv。解析失败不抛——下载本身已成功，
 * 文件名只是个体验问题，抛错反而会让调用方以为导出失败。
 */
export function parseCsvFilename(disposition: string | undefined | null): string {
  if (!disposition) return 'stats.csv'
  const star = /filename\*=UTF-8''([^;]+)/i.exec(disposition)
  if (star) {
    try {
      return decodeURIComponent(star[1])
    } catch {
      return 'stats.csv'
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(disposition)
  return plain ? plain[1] : 'stats.csv'
}
