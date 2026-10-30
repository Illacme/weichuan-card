// 名片墙的数据层：分页与搜索的请求参数、结果整形、跨页合并。
//
// 为什么单独抽一层：名片墙现在有**两个页面**共用同一套语义——
//   · pages/team/team      裸启动落地页（抬头 + 搜索 + 首屏一页 + 「查看全部」）
//   · pages/team/all       查看全部（搜索 + 滚动加载）
// 两处各写一遍，迟早会漂移成「一页 60、另一页 20」「一页能搜、一页不能」。
//
// 另外这里集中处理三件容易写错的事：
//  1. 请求参数：page 从 1 开始（不是 0）、keyword 必须 trim 且为空时不传；
//  2. 字段整形：头像要拼绝对地址 + 缩略图（<image> 不认相对路径，否则整屏裂图）；
//  3. 跨页合并：按 staffId 去重——翻页期间管理员可能新增/停用员工，
//     同一个人会同时出现在第 1 页和第 2 页，直接 concat 会出现重复宫格。
import { assetUrl } from './request'
import { thumbnailURL } from './thumb'

// TEAM_PAGE_SIZE 每页条数。三列宫格一屏约 12 个，60 是 5 屏，
// 滚动加载的节奏刚好；再大首屏白等，再小则一次滚动要连发好几个请求。
// 必须与后端 repository.TeamPageDefault 保持一致（那边只是缺省值，端上始终显式传）。
export const TEAM_PAGE_SIZE = 60

export interface TeamItem {
  staffId: number
  name: string
  title: string
  avatar: string
  avatarThumb: string
}

// teamParams 生成请求参数。
// keyword 为空时**不传该字段**：request.ts 会过滤空值，但显式不传更明确，
// 也避免后端拿到一个空串还要 TrimSpace 一遍。
export function teamParams(page: number, keyword: string): Record<string, unknown> {
  const p = Math.floor(Number(page))
  const kw = (keyword || '').trim()
  const out: Record<string, unknown> = { page: p > 0 ? p : 1, size: TEAM_PAGE_SIZE }
  if (kw) out.keyword = kw
  return out
}

// mapTeamList 把接口返回的原始数组整形成宫格要用的形状。
// 只做「补全 + 拼绝对地址」，不做字段裁剪——后端已经白名单过了，
// 这里再裁一遍只会让「后端多给了个字段」这种问题在端上被掩盖。
export function mapTeamList(raw: any): TeamItem[] {
  const arr = Array.isArray(raw) ? raw : []
  return arr.map((e: any) => {
    const avatar = assetUrl(e && e.avatar)
    return {
      staffId: Number(e && e.staffId) || 0,
      name: (e && e.name) || '未命名',
      title: (e && e.title) || '',
      avatar,
      avatarThumb: thumbnailURL(avatar),
    }
  })
}

// mergeTeam 追加一页，按 staffId 去重（保留先出现的那条，顺序稳定）。
export function mergeTeam(prev: TeamItem[], next: TeamItem[]): TeamItem[] {
  const seen = new Set<number>()
  const out: TeamItem[] = []
  for (const it of prev.concat(next)) {
    if (seen.has(it.staffId)) continue
    seen.add(it.staffId)
    out.push(it)
  }
  return out
}

// hasMoreOf 由后端回包的 hasMore 决定，不要用 total - list.length 去推：
// 搜索场景下 total 是「匹配总数」、data 是当前页，两者相减得到的是
// 「还没加载的匹配数」，但翻页过程中 list 只含已加载部分，推出来的值会失真。
export function hasMoreOf(res: any): boolean {
  if (!res) return false
  if (typeof res.hasMore === 'boolean') return res.hasMore
  // 老后端没有 hasMore：按「本页是否满页」保守判断
  const n = Array.isArray(res.data) ? res.data.length : 0
  const size = Number(res.size) || TEAM_PAGE_SIZE
  return n >= size && Number(res.total) > n
}
