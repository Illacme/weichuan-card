// 精选案例：featuredCount 决定「每页并排几张」，不是「总共显示几张」。
//   0 → 整块不渲染；1 → 一张铺满整行；2 → 两张各半；3 → 三张各三分之一。
// 精选多于每页张数时按页分组，多页才按 effect 做淡入 / 横移 / 翻转循环；
// 只有一页（含精选不足）时静态平铺，不做任何动画。
//
// WXML 只做纯渲染：要显示哪几页、每张图的宽度、图片在原始列表里的下标，
// 全部在 JS 里算好塞进 visiblePages。
// **不要把 wx:if 和 wx:for 写在同一个标签上**——小程序 wx:if 阶段拿不到
// wx:for-index 的变量，条件恒假会让整页被跳过，只剩一块空白。
// 精选案例专用回退：通用 onThumbError 按 data-arr 改的是外层同名数组，但本组件
// 真正渲染的是重排后的 visiblePages[].items[]，两者不是一份数据——种子/存量素材
// 没有 _thumb 兄弟图时缩略图必 404，回退打空就会让整块只剩占位撑出的空白
// （2026-11-18 真机实测：设置怎么改都没图，就是这个原因）。这里按 globalIdx
// 遍历 visiblePages 找到渲染项，直接改它，保证回退真的落到屏幕上那张 image。
// WXML 仍带 data-arr="featured" 只为满足 img_fallback 守门（它会检查 binderror 的
// image 必带 data-arr/data-fail）；本函数按 idx+full 定位，不依赖该属性。
function onFeaturedImgErr(this: any, e: any): void {
  const ds = (e && e.currentTarget && e.currentTarget.dataset) || {}
  const idx: number = ds.idx
  const full: string = ds.full
  if (idx == null || !full) return
  const pages = Array.isArray(this.data && this.data.visiblePages) ? this.data.visiblePages : []
  let touched = false
  // 整块重建再 setData，不用路径写法：路径语法在小程序脚手架/部分基础库上行为不一，
  // 而且整写让「改的就是屏幕上那份数据」这件事一目了然。
  const nextPages = pages.map((pg: any) => ({
    ...pg,
    items: (Array.isArray(pg.items) ? pg.items : []).map((it: any) => {
      if (it.globalIdx === idx && it.coverThumb !== full) {
        touched = true
        return { ...it, coverThumb: full }
      }
      return it
    }),
  }))
  if (!touched) return
  // 同步改回源数据 featured：否则 show / observer 再次 sync 时会用旧的 _thumb URL
  // 重建 visiblePages，回退就丢了，图会再裂一次、闪一下。改源数据让回退持久。
  const patch: Record<string, unknown> = { visiblePages: nextPages }
  const featured = Array.isArray(this.data && this.data.featured) ? this.data.featured : []
  if (featured[idx] && featured[idx].coverThumb !== full) {
    patch.featured = featured.map((it: any, i: number) =>
      i === idx ? { ...it, coverThumb: full } : it
    )
  }
  this.setData(patch)
}

Component({
  options: { addGlobalClass: true },
  properties: {
    featured: Array,
    featuredCount: { type: Number, value: 3 },
    effect: { type: String, value: 'static' },
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
  },
  data: {
    pageSize: 3, // 钳制后的每页张数
    pageCount: 0,
    current: 0,
    cycleOn: false,
    visiblePages: [] as any[], // [{ k: 原页码, items: [{...图, width, globalIdx}] }]
    pageStyles: [] as string[], // 按原页码索引的整页动画样式
  },
  observers: {
    'featured, featuredCount, effect': function () {
      ;(this as any).sync()
    },
  },
  lifetimes: {
    attached() {
      ;(this as any).sync()
    },
    detached() {
      ;(this as any).stopTimer()
    },
    hide() {
      ;(this as any).stopTimer()
    },
    show() {
      ;(this as any).sync()
    },
  },
  methods: {
    onImgErr: onFeaturedImgErr,
    sync() {
      this.stopTimer()
      const list = this.data.featured || []
      const raw = Number(this.data.featuredCount)
      const pageSize = Math.max(0, Math.min(3, isNaN(raw) ? 3 : raw))
      if (pageSize === 0 || list.length === 0) {
        this.setData({
          pageSize, pageCount: 0, current: 0, cycleOn: false,
          visiblePages: [], pageStyles: [],
        })
        return
      }
      const pages: any[][] = []
      for (let i = 0; i < list.length; i += pageSize) pages.push(list.slice(i, i + pageSize))
      const cycleOn = pages.length > 1 && this.data.effect !== 'static'
      const current = cycleOn && this.data.current >= pages.length ? 0 : this.data.current
      // 每张图带好自己的宽度与在原始列表里的下标（onImgErr 回退原图要用）：
      // 最后一页不足时按该页实际数量均分，1 张也铺满整行。
      const decorated = pages.map((page, pi) =>
        page.map((p, i) => ({
          ...p,
          width: `${100 / page.length}%`,
          globalIdx: pi * pageSize + i,
        }))
      )
      const visiblePages = cycleOn
        ? decorated.map((items, k) => ({ k, items }))
        : [{ k: 0, items: decorated[0] }]
      // 高度由 grid 里最高的页自然撑出，不再有隐藏占位。
      // 切页只改 transform / opacity，不碰 visiblePages 里的任何 src——iOS 上
      // widthFix 换 src 会让图片高度在加载间隙塌成 0，整页高度骤减后 WebView
      // 把 scrollTop 钳回 0（用户实测「切下一张就滚回页面顶部」，华为未复现）。
      this.setData({
        pageSize, pageCount: pages.length, current, cycleOn,
        visiblePages,
      })
      this.applyStyles()
      if (cycleOn) this.startTimer()
    },
    // 页容器的内联 transform/opacity 由这里按原页码算好，WXML 只负责绑。
    // 横移用「环形最短距离」：末页回首页保持同向从右侧滑入，不横穿整排。
    applyStyles() {
      const { pageCount, current, effect, cycleOn } = this.data as any
      if (!cycleOn) {
        this.setData({ pageStyles: Array.from({ length: pageCount }, () => '') })
        return
      }
      const half = pageCount / 2
      const styles = Array.from({ length: pageCount }, (_, i: number) => {
        let diff = i - current
        if (diff > half) diff -= pageCount
        if (diff < -half) diff += pageCount
        const active = diff === 0
        const layers = `z-index:${active ? 3 : 1};pointer-events:${active ? 'auto' : 'none'};`
        if (effect === 'fade') return `opacity:${active ? 1 : 0};${layers}`
        if (effect === 'flip') {
          return `transform:perspective(600px) rotateY(${active ? 0 : 180}deg) scale(${active ? 1 : 0.96});opacity:${active ? 1 : 0};${layers}`
        }
        if (effect === 'scroll') {
          return `transform:translateX(${diff * 100}%);opacity:${Math.abs(diff) <= 1 ? 1 : 0};z-index:${active ? 3 : 2};pointer-events:${active ? 'auto' : 'none'};`
        }
        return active ? layers : `opacity:0;${layers}`
      })
      this.setData({ pageStyles: styles })
    },
    startTimer() {
      this.stopTimer()
      if (!this.data.cycleOn) return
      ;(this as any)._timer = setInterval(() => this.next(), 3600)
    },
    stopTimer() {
      const timer = (this as any)._timer
      if (timer) {
        clearInterval(timer)
        ;(this as any)._timer = 0
      }
    },
    next() {
      const n = this.data.pageCount
      if (n < 2) return
      this.setCurrent((this.data.current + 1) % n)
    },
    setCurrent(current: number) {
      if (current < 0 || current >= this.data.pageCount) return
      // 只改页码与动画样式。绝不在这里 setData 图片数组 / src：iOS 的 WebView
      // 在 widthFix 图片换 src 时会先丢掉旧高度，页面高度骤减会把滚动位置钳回顶部。
      this.setData({ current })
      this.applyStyles()
    },
    onDot(e: any) {
      this.setCurrent(Number(e.currentTarget.dataset.index))
      // 手动点过之后重新计时，避免刚点完立刻被自动轮播切走
      this.startTimer()
    },
    onTap(e: any) {
      const url =
        '/pages/project/detail/detail?id=' +
        e.currentTarget.dataset.id +
        (this.data.staffId ? '&staffId=' + this.data.staffId : '')
      wx.navigateTo({ url })
    },
  },
})
