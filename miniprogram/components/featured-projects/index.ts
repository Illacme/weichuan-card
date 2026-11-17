import { onThumbError } from '../../utils/thumb'

// 精选案例：featuredCount 决定「每页并排几张」，不是「总共显示几张」。
//   0 → 整块不渲染；1 → 一张铺满整行；2 → 两张各半；3 → 三张各三分之一。
// 精选多于每页张数时按页分组，多页才按 effect 做淡入 / 横移 / 翻转循环；
// 只有一页（含精选不足）时静态平铺，不做任何动画。
// 小程序不能动态加载 WXSS，效果全部随包内置，由后台下发的白名单 id 选择。
Component({
  options: { addGlobalClass: true },
  properties: {
    featured: Array,
    featuredCount: { type: Number, value: 3 },
    effect: { type: String, value: 'static' },
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
  },
  data: {
    pageSize: 3, // 钳制后的每页张数，WXML 用它算图片在原始列表里的索引
    pages: [] as any[][],
    pageCount: 0,
    current: 0,
    cycleOn: false,
    pageStyles: [] as string[],
    itemWidths: [] as string[][],
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
          pageSize, pages: [], pageCount: 0, current: 0, cycleOn: false,
          pageStyles: [], itemWidths: [],
        })
        return
      }
      const pages: any[][] = []
      for (let i = 0; i < list.length; i += pageSize) pages.push(list.slice(i, i + pageSize))
      const cycleOn = pages.length > 1 && this.data.effect !== 'static'
      const current = cycleOn && this.data.current >= pages.length ? 0 : this.data.current
      // 每张图按「该页实际数量」均分整行：最后一页不足时也会铺满（1 张=100%）
      const itemWidths = pages.map((page) => page.map(() => `${100 / page.length}%`))
      this.setData({ pageSize, pages, pageCount: pages.length, current, cycleOn, itemWidths })
      this.applyStyles()
      if (cycleOn) this.startTimer()
    },
    // 页容器的内联 transform/opacity 由这里算好，WXML 只负责绑。
    // 横移用「环形最短距离」：末页回首页保持同向从右侧滑入，不横穿整排。
    applyStyles() {
      const { pageCount, current, effect, cycleOn } = this.data as any
      if (!cycleOn) {
        this.setData({ pageStyles: Array.from({ length: this.data.pageCount }, () => '') })
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
        // static 或未知效果兜底：不做动画
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
