import { onThumbError } from '../../utils/thumb'

// 精选案例：featuredCount 决定「每页并排几张」，不是「总共显示几张」。
//   0 → 整块不渲染；1 → 一张铺满整行；2 → 两张各半；3 → 三张各三分之一。
// 精选多于每页张数时按页分组，多页才按 effect 做淡入 / 横移 / 翻转循环；
// 只有一页（含精选不足）时静态平铺，不做任何动画。
//
// WXML 只做纯渲染：要显示哪几页、每张图的宽度、图片在原始列表里的下标，
// 全部在 JS 里算好塞进 visiblePages / sizerItems。
// **不要把 wx:if 和 wx:for 写在同一个标签上**——小程序 wx:if 阶段拿不到
// wx:for-index 的变量，条件恒假会让整页被跳过，只剩隐藏占位撑出一块空白。
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
    sizerItems: [] as any[], // 第一页的副本，仅用于撑高度（visibility:hidden）
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
    onImgErr: onThumbError,
    sync() {
      this.stopTimer()
      const list = this.data.featured || []
      const raw = Number(this.data.featuredCount)
      const pageSize = Math.max(0, Math.min(3, isNaN(raw) ? 3 : raw))
      if (pageSize === 0 || list.length === 0) {
        this.setData({
          pageSize, pageCount: 0, current: 0, cycleOn: false,
          sizerItems: [], visiblePages: [], pageStyles: [],
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
      this.setData({
        pageSize, pageCount: pages.length, current, cycleOn,
        sizerItems: decorated[0], visiblePages,
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
