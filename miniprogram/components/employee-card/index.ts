import { onImgFail } from '../../utils/thumb'

// 12px 字号（= 24rpx @375 视口）下 PingFang/-apple-system 的逐字符实测字宽。
// 由 outputs/gen_char_table.js 用 canvas measureText 在 375 视口下逐字符量出后回填，
// 之所以不用「按字符类别取平均值」：m/w 这类宽字母会把平均值拉偏，含它们的邮箱
// 会被低估 6px+，正好卡在档位阈值上时就会误判。邮箱/手机号都是 ASCII，逐字查表是精确的。
// 单位是 px；小程序里按 1rpx = 0.5px（375 视口）换算成 rpx 后与屏宽等比缩放，无需再取 windowWidth。
const CHAR_W: { [ch: string]: number } = {
  '0': 7.2, '1': 4.81, '2': 7.2, '3': 7.2, '4': 7.2, '5': 7.2, '6': 7.2, '7': 6.56, '8': 7.2, '9': 7.2,
  a: 6.71, b: 7.03, c: 6.56, d: 7.03, e: 6.66, f: 4.48, g: 7.09, h: 6.67, i: 3.07, j: 3.2,
  k: 6.35, l: 2.82, m: 10.26, n: 6.71, o: 7.03, p: 7.03, q: 7.03, r: 4.38, s: 6.06, t: 4.26,
  u: 6.72, v: 5.78, w: 9.06, x: 6.11, y: 5.95, z: 5.84,
  A: 7.88, B: 8.12, C: 8.74, D: 8.47, E: 7.64, F: 6.92, G: 8.98, H: 8.64, I: 2.84, J: 6.2,
  K: 8.28, L: 7.06, M: 10.58, N: 8.63, O: 9.2, P: 7.7, Q: 9.2, R: 8.11, S: 7.58, T: 7.43,
  U: 8.57, V: 7.67, W: 11.16, X: 7.64, Y: 7.94, Z: 7.49,
  '@': 10.3, '.': 3.17, '-': 7.26, '_': 6, '+': 7.26, '~': 6,
}

// 375 视口下 1rpx = 0.5px → px 字宽 ×2 即 rpx 字宽；rpx 随屏宽等比缩放，阈值可全程用 rpx 表述
const PX_TO_RPX = 2
// 名片卡内容宽 = 750 - 卡片左右外边距 20rpx×2 - 卡片左右内边距 20rpx×2
const CONTENT_RPX = 670
// 联系表标签定宽（app.wxss .ec-label），短邮箱判定要把它从半栏里扣掉
const LABEL_RPX = 56
// 10px 安全缓冲：估算与真机渲染可能有细微差异，宁可判成 long（右对齐、列更窄但不折行），
// 也不要判成 short 之后邮箱在真机上折行
const SAFETY_RPX = 20
const LONG_EMAIL_THRESHOLD_RPX = CONTENT_RPX / 2 - LABEL_RPX - SAFETY_RPX // 259rpx

// 估算一段 ASCII 文本的渲染宽度（rpx）；表里没有的字符（中文等全角）按一个字宽计
function estimateRpx(text: string): number {
  let w = 0
  for (const ch of text) w += (CHAR_W[ch] !== undefined ? CHAR_W[ch] : 12) * PX_TO_RPX
  return w
}

// 名片卡右上角 LOGO 的尺寸档 → 区块宽高（rpx）。三档必须与后台预览
// （frontend/views/content/HomeLayout.vue 的 .lp-slot-* / .lp-img-*，1rpx=0.5px）
// 以及 app.wxss 的 .ec-logo-slot-* / .ec-logo-* 逐值一致，否则客户按预览调完、
// 到真机上是另一个大小。改这里就要同时改那两处（有守门用例钉着）。
const LOGO_H: Record<string, number> = { small: 36, medium: 44, large: 52 }
const LOGO_W: Record<string, number> = { small: 110, medium: 150, large: 190 }
const LOGO_SIZES = ['small', 'medium', 'large']
const LOGO_ALIGNS = ['left', 'center', 'right']
// 宽高比的安全区间：极端图（超长横条 / 超细竖条）不该把行宽吃光或缩成一个点。
const LOGO_RATIO_MIN = 0.2
const LOGO_RATIO_MAX = 6

// logoWidth 按档位与原始宽高比算 LOGO 的渲染宽度（rpx），夹在区块宽以内。
// 抽成纯函数是为了能被用例直接盯住：它是「后台预览 / app.wxss / 组件」三处
// 尺寸一致的那一环，写错了三处会各说各话。
// ratio=0 表示还没拿到原始尺寸 → 返回 0，WXML 里就不写内联宽度，
// 让 CSS 的 width:100% 生效（= 改造前的观感），不猜比例。
function logoWidth(size: string, ratio: number): number {
  if (!ratio) return 0
  const s = LOGO_SIZES.indexOf(size) >= 0 ? size : 'medium'
  return Math.min(LOGO_W[s], Math.round(LOGO_H[s] * ratio))
}

Component({
  options: { addGlobalClass: true },
  properties: {
    employee: Object,
    company: Object,
    // 方向箭头（黑色实心小三角）：▾ 表示滚动到下方业绩区，▸ 表示跳转到案例页；
    // 由页面按首页布局（业绩区是否展示）下发，让用户点前就知道会发生什么
    caseArrow: {
      type: String,
      value: '▸',
    },
    // 名片卡右上角 LOGO 的档位（0053），由页面按 home-layout 下发。
    // 缺省值 = 加配置前的 150×44 + aspectFit 居中，旧后端不下发这两项时也不突变。
    logoSize: {
      type: String,
      value: 'medium',
    },
    logoAlign: {
      type: String,
      value: 'center',
    },
  },
  data: {
    // 邮箱是否「长」：超过半栏可用宽度 → 走 long 档（右列≤60% + 邮箱地址右对齐）
    emailLong: false,
    // 加载失败的图片地址（头像 / 企业 LOGO）：WXML 用 `src && xxxFail !== src` 判断，
    // 记 URL 而不是布尔标记，换图后自动重试（见 utils/thumb.ts onImgFail）
    avatarFail: '',
    logoFail: '',
    // LOGO 的原始宽高比与据此算出的渲染宽度（rpx）。0 = 还没拿到（用 CSS 的 100%）。
    logoRatio: 0,
    logoW: 0,
  },
  observers: {
    // 员工数据是首页异步加载后 setData 进来的，observer 里拿不到渲染后的 DOM，
    // 所以用字宽估算而不是 SelectorQuery 实测（实测要在渲染后、且文字已被折断时量不准）
    'employee.email'(email: string) {
      this.setData({ emailLong: estimateRpx(email || '') > LONG_EMAIL_THRESHOLD_RPX })
    },
    // 换尺寸档或换了图（原始比例变了）都要重算渲染宽度：只算一次的话，
    // 客户把「中」改成「大」之后 LOGO 宽度会停在旧值、看不出变大。
    'logoSize, logoRatio'(_size: string, ratio: number) {
      this.setData({ logoW: logoWidth(_size, ratio) })
    },
  },
  methods: {
    // LOGO 加载完拿到**原始像素宽高**，据此把图片框收到内容宽度：
    // aspectFit 在「框宽 == 内容宽」时才不会留白，位置档才真的能左/中/右挪。
    // 拿不到就保持 logoW=0，走 CSS 的 100%（= 改造前的观感），不猜比例。
    onLogoLoad(e: any) {
      const d = (e && e.detail) || {}
      const w = Number(d.width)
      const h = Number(d.height)
      if (!w || !h) return
      const ratio = Math.min(LOGO_RATIO_MAX, Math.max(LOGO_RATIO_MIN, w / h))
      this.setData({ logoRatio: ratio })
    },
    // 头像 / LOGO 加载失败：记下失败的地址，WXML 据此不再画这张图（见 utils/thumb.ts）
    onImgFail,
    // 「查看案例」：只上报意图，具体行为（滚动业绩区 / 跳第一个分类 / 提示）由页面决定
    onEnterCase() {
      this.triggerEvent('entercase')
    },
    // 手机/座机/邮箱点击复制：名片主要给客户看，存号、发邮件是最高频动作
    //
    // setClipboardData 属微信隐私接口（读取你的剪切板）。访客复制的是名片上
    // 本就公开的联系信息，不涉及收集访客个人信息，故不弹自绘授权窗——由微信
    // 官方弹窗兜底即可（未声明对应隐私类型时平台会直接放行）。
    // 但**必须有 fail 回调**：接口被拒（未声明隐私类型 / 未授权）时若不接
    // fail，用户点了复制却毫无反应——「复制」是本页最高频动作，静默失效最致命。
    onCopy(e: any) {
      const ds = e.currentTarget.dataset
      const value = ds.copy
      const type = ds.type || ''
      if (!value) return
      wx.setClipboardData({
        data: value,
        // 提示必须明确复制的是什么：手机/座机/邮箱在名片上并排，只弹「已复制」
        // 用户分不清刚存的是哪个号；带上类型（已复制手机号/座机号/邮箱）一目了然
        success: () => wx.showToast({ title: type ? `已复制${type}` : '已复制', icon: 'success' }),
        fail: (err: any) => {
          const msg = (err && err.errMsg) || ''
          if (msg.indexOf('privacy') >= 0) {
            wx.showToast({ title: '需同意隐私政策才能复制', icon: 'none' })
          } else {
            wx.showToast({ title: '复制失败', icon: 'none' })
          }
        },
      })
    },
  },
})
