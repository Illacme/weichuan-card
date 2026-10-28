// 缩略图 URL 派生：在原始图 URL 的末位扩展名前插入 _thumb。
//
// 后端 storage 层（ProcessUpload）仅对经 Save 上传的 jpg/png 生成
// <hash>_thumb.<ext> 兄弟对象，列表页改用缩略图可大幅降低带宽与首屏耗时。
//
// 约定（与后端 storage.go / minio.go / oss.go 一致）：
//  - 仅对 jpg/jpeg/png 派生缩略图；gif/webp/mp4 等后端不生成，原样返回；
//  - 无法识别扩展名的地址（如微信头像 qlogo 路径）原样返回；
//  - 真实但无缩略图的情况（历史存量上传、SaveBytes 生成的二维码/素材、外链）
//    由调用方 image 的 binderror 回退到原图——本函数故意不感知这些分支。
export function thumbnailURL(src?: string | null): string {
  if (!src) return ''
  // 先切掉查询串 / 哈希，只对路径部分做扩展名判断
  const q = src.indexOf('?')
  const h = src.indexOf('#')
  let cut = -1
  if (q >= 0 && h >= 0) cut = Math.min(q, h)
  else if (q >= 0) cut = q
  else if (h >= 0) cut = h
  const base = cut >= 0 ? src.slice(0, cut) : src
  const suffix = cut >= 0 ? src.slice(cut) : ''
  const dot = base.lastIndexOf('.')
  if (dot < 0) return src
  const ext = base.slice(dot + 1).toLowerCase()
  if (ext !== 'jpg' && ext !== 'jpeg' && ext !== 'png') return src
  return base.slice(0, dot) + '_thumb.' + ext + suffix
}

// 列表图 binderror 通用回退：缩略图加载失败时改回原图，避免裂图。
// 配合 WXML 上的 data-arr / data-idx / data-key / data-full 使用；
// 适用于页面与组件（组件内数组名即其属性名）。
export function onThumbError(this: any, e: any): void {
  const ds = (e && e.currentTarget && e.currentTarget.dataset) || {}
  const arr: string = ds.arr
  const idx: number = ds.idx
  const key: string = ds.key
  const full: string = ds.full
  if (!arr || idx == null || !key || !full) return
  const list = this.data ? this.data[arr] : undefined
  if (!list || !list[idx]) return
  if (list[idx][key] === full) return // 已回退过，避免死循环
  const patch: Record<string, unknown> = {}
  patch[`${arr}[${idx}].${key}`] = full
  this.setData(patch)
}
