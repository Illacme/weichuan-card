import { onThumbError } from '../../utils/thumb'

// 精选案例循环呈现：static 平铺 / fade 淡入 / scroll 横移 / flip 立体翻转。
// 小程序不能动态加载 WXSS，四套效果全部随包内置，由首页透传的 effect id 选择；
// 后台只写下发白名单内的 id（见后端 model.FeaturedEffect*）。
//
// 只有一张案例时始终静态：轮播一张图没有意义，还会让人以为卡住了。
// 定时器在 attached/show 启动、detached/hide 停止，页面切后台不空转。
Component({
  options: { addGlobalClass: true },
  properties: {
    featured: Array,
    effect: { type: String, value: 'static' },
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
  },
  data: {
    current: 0,
    cycleOn: false,
    cycleStyles: [] as string[],
  },
  observers: {
    'effect, featured': function () {
      ;(this as any).syncCycle()
    },
  },
  lifetimes: {
    attached() {
      ;(this as any).syncCycle()
    },
    detached() {
      ;(this as any).stopTimer()
    },
    hide() {
      ;(this as any).stopTimer()
    },
    show() {
      ;(this as any).syncCycle()
    },
  },
  methods: {
    onImgErr: onThumbError,
    syncCycle() {
      const n = this.data.featured.length
      const cycleOn = this.data.effect !== 'static' && n > 1
      const current = cycleOn && this.data.current >= n ? 0 : this.data.current
      this.setData({ cycleOn, current })
      this.applyStyles()
      if (cycleOn) this.startTimer()
      else this.stopTimer()
    },
    // 每张图的内联 transform/opacity 由这里算好，WXML 只负责绑。
    // 横移用「环形最短距离」：最后一张切回第一张时不会横穿整排卡片。
    applyStyles() {
      const { featured, current, effect } = this.data as any
      const n = featured.length
      const half = n / 2
      const styles = featured.map((_: any, i: number) => {
        let diff = i - current
        if (diff > half) diff -= n
        if (diff < -half) diff += n
        const active = diff === 0
        const layers = `z-index:${active ? 3 : 1};pointer-events:${active ? 'auto' : 'none'};`
        if (effect === 'fade') {
          return `opacity:${active ? 1 : 0};${layers}`
        }
        if (effect === 'flip') {
          // 非当前页翻到背面；scale 让翻转有一点纵深，不会像纸片平移
          return `transform:perspective(600px) rotateY(${active ? 0 : 180}deg) scale(${active ? 1 : 0.96});opacity:${active ? 1 : 0};${layers}`
        }
        if (effect === 'scroll') {
          const visible = Math.abs(diff) <= 1
          return `transform:translateX(${diff * 100}%);opacity:${visible ? 1 : 0};z-index:${active ? 3 : 2};pointer-events:${active ? 'auto' : 'none'};`
        }
        // 到这里只可能是 static（首页已把未知值归一），保持内联样式为空即可。
        return ''
      })
      this.setData({ cycleStyles: styles })
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
      const n = this.data.featured.length
      if (n < 2) return
      this.setCurrent((this.data.current + 1) % n)
    },
    setCurrent(current: number) {
      if (current < 0 || current >= this.data.featured.length) return
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
