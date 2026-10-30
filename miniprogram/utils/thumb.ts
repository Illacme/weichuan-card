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

// 单图 binderror 通用处理：记下「加载失败的那个 URL」，WXML 用
//   src && xxxFail !== src
// 决定还要不要画它——不画就露出同位置的占位（默认图标 / 容器自带的纯色底 /
// 表单里的「未设置」），避免白块或裂图。
//
// 为什么记 URL 而不是布尔标记：布尔标记必须在每次换图时手动清掉（换头像、
// 下拉刷新换了地址、图集重排……），漏一处就是「新图永远不显示」这种难查的 bug。
// 记 URL 则天然自愈——地址一变就重新尝试；同一张坏图也不会无限重试。
// 顺带解决了图集按下标标记的隐患：列表增删导致下标错位时，比较的是 URL，
// 错位只会让标记失效（多画一次图），不会把别的图误藏起来。
//
// 用法：
//   单图：binderror="onImgFail" data-fail="avatarFail" data-src="{{employee.avatar}}"
//   图集：binderror="onImgFail" data-fail="albumFail" data-idx="{{index}}" data-src="{{item}}"
//         （data 里放同名空数组，如 albumFail: []）
export function onImgFail(this: any, e: any): void {
  const ds = (e && e.currentTarget && e.currentTarget.dataset) || {}
  const key: string = ds.fail
  const src: string = ds.src
  if (!key || !src) return
  const patch: Record<string, unknown> = {}
  if (ds.idx != null) {
    // 整数组重写而不是 setData('albumFail[2]')：路径写法在微信里合法，但数组只有
    // 几项到几十项（相册 ≤9、海报素材 ≤44），整写省掉一个「路径解析」依赖，
    // 也让测试脚手架不必模拟路径语义——少一处只在真机上才暴露的差异。
    const cur: string[] = Array.isArray(this.data && this.data[key]) ? this.data[key].slice() : []
    cur[ds.idx] = src
    patch[key] = cur
  } else {
    patch[key] = src
  }
  this.setData(patch)
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
