import http from './request'

// 常用开源素材库（后端内嵌，见 backend/internal/assets）：
// 所有需要图片的位置都能直接选内置素材，免去四处找图、按尺寸处理图。
// 采用（adopt）= 后端把素材复制进上传存储，返回与普通上传完全同构的 URL，
// 因此下游（小程序 <image> / CDN）零感知。

export interface LibraryCategory {
  key: string
  name: string
}

// 视觉调性（第二筛选维度）：与 category「用在哪」正交，描述素材的「气质」。
// 命名用用户能直接感知的形容词，不暴露 mesh / grain 等实现术语。
export interface LibraryStyle {
  key: string // clean / nature / luxury / soft / vivid
  name: string // 简约清晰 / 温暖自然 / 轻奢尊贵 / 柔美清新 / 光影流动
}

// 某调性在当前筛选条件下的素材数（前端据此隐藏 0 结果的调性）
export interface StyleCount extends LibraryStyle {
  count: number
}

export interface LibraryItem {
  id: string
  name: string
  category: string // icon / cover / logo / avatar / background / poster
  style: string // 视觉调性，见 LibraryStyle.key
  file: string
  thumb: string // 预览图相对路径（封面/背景为缩略版）
  width: number
  height: number
  tags: string[]
}

// 分页查询条件（全部可选，服务端有安全默认值）
export interface LibraryQuery {
  page?: number // 1-based，缺省 1
  size?: number // 每页条数，缺省 24，上限 60
  type?: string // 分类过滤：空/'all' = 全部
  style?: string // 调性过滤：空/'all' = 全部
  keyword?: string // 名称或标签匹配
}

// 分页结果：items 为本页条目，total 为过滤后的总条数
export interface LibraryData {
  version: number
  categories: LibraryCategory[]
  styles: LibraryStyle[]
  items: LibraryItem[]
  total: number
  page: number
  size: number
  pages: number
  styleCounts?: StyleCount[]
}

// 素材清单（分页，管理员登录态）
export function listLibrary(params: LibraryQuery = {}) {
  const q: Record<string, string | number> = {}
  if (params.page) q.page = params.page
  if (params.size) q.size = params.size
  // 前端 tab 用 'all' 表示全部，服务端按空值处理
  if (params.type && params.type !== 'all') q.type = params.type
  if (params.style && params.style !== 'all') q.style = params.style
  if (params.keyword) q.keyword = params.keyword
  return http.get<{ code: number; data?: LibraryData }>('/admin/assets/library', {
    params: q,
  })
}

// 采用素材：返回可直接填入表单的 URL
export function adoptAsset(id: string) {
  return http.post<{
    code: number
    data?: { url: string; item?: { id: string; name: string; category: string } }
  }>('/admin/assets/adopt', { id })
}

// 预览图 URL：公开端点（/api/assets/file/...），长缓存，与登录态无关
export function libraryFileUrl(rel: string) {
  const base = import.meta.env.VITE_API_BASE || '/api'
  return `${base.replace(/\/$/, '')}/assets/file/${rel}`
}
