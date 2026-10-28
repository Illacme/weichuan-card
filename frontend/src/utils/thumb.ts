// 缩略图 URL 派生：在原始图 URL 的末位扩展名前插入 _thumb。
//
// 后端 storage 层（ProcessUpload）仅对经 Save 上传的 jpg/png 生成
// <hash>_thumb.<ext> 兄弟对象。列表缩略图组件优先加载它以降低带宽与首屏耗时。
//
// 约定（与后端 storage.go / minio.go / oss.go 一致）：
//  - 仅对 jpg/jpeg/png 派生缩略图；gif/webp/mp4 等后端不生成，原样返回；
//  - 无法识别扩展名的地址（如微信头像 qlogo 路径）原样返回；
//  - 真实但无缩略图的情况（历史存量上传、SaveBytes 生成的二维码/素材、外链）
//    由组件 el-image 的 @error 回退到原图——本函数故意不感知这些分支。
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
