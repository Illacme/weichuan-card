import { request, assetUrl } from '../../utils/request'
import { thumbnailURL } from '../../utils/thumb'
import { bindCompanyFromScene, getCompanyId } from '../../utils/tenant'
import { track, deriveSource } from '../../utils/track'
import { FALLBACK_BRAND } from '../../utils/brand'

Page({
  data: {
    staffId: 0,
    employee: null,
    company: null,
    profile: null,
    categories: [],
    featured: [],
    services: [],
    homeOrder: ['achievement', 'service'],
    // 「查看案例」按钮方向箭头（黑色实心小三角）：业绩区在首页 → 滚动过去（▾），被隐藏 → 跳案例页（▸）。
    // 用 U+25BE/U+25B8 而非 U+25BC/U+25B6：后者的右向三角在部分客户端有 emoji 变体会被渲染成彩色
    caseArrow: '▾',
    // 首页背景（商业化美化包装，可选）：bgImage 为空即保持灰底布局。
    // bgMask 为遮罩浓度 0~100，转成 0~1 的黑色蒙层透明度，压暗背景突出白卡片。
    bgImage: '',
    bgMask: 0,
    bgMaskAlpha: 0,
    disabled: false,
    notFound: false,
    loading: true,
    error: false,
    // 自定义顶栏（navigationStyle=custom）：状态栏高度与整体高度，用于避让刘海/状态栏
    statusBarHeight: 20,
    navHeight: 64,
    // 左上角「我的」入口：已登录显示头像，未登录显示默认用户图标；文字固定「我的」
    meAvatar: '',
    // 顶部滚动遮罩透明度：随 scrollTop 连续渐显，滚过顶栏下沿（navHeight）时恰好为 1；
    // 初始 0 让顶栏透明融入背景，滚动后平滑浮现（见 onPageScroll）
    navMask: 0,
    // 上一帧遮罩值（带 0.01 死区，避免 onPageScroll 每帧无谓 setData）
    _lastMask: 0,
    // 访问埋点去重标记：同一 staffId 在本页面实例内只上报一次。
    // onShow 语义（首页文档 §4）是「每次显示 +1」，但从「我的」switchTab 回名片页
    // 会再次触发 onShow——不去重的话切两个 tab 来回几下访问量就虚高。
    // 页面被销毁重建（重新进小程序）时 data 重置，会正常再计一次。
    lastVisitStaffId: 0,
    // 本次进入的流量来源（由微信 scene / 海报标记推导）：仅 visit 埋点携带，
    // 后端按它归「来源分布」与「裂变榜」。空串=端上未识别（后端归一为 unknown）。
    source: '',
  },
  onLoad(query: any) {
    // 自定义顶栏：先按设备状态栏高度算出避让高度，避免内容被刘海/状态栏遮挡
    let sb = 20
    try {
      const info = wx.getSystemInfoSync()
      if (info && info.statusBarHeight) sb = info.statusBarHeight
    } catch {}
    this.setData({ statusBarHeight: sb, navHeight: sb + 44 })

    // 租户绑定（平台模式）：扫企业小程序码 / 分享链接带 companyId 时粘滞本机，
    // 企业内容（业绩/服务/布局）随之落该企业。独立版在此调用内被忽略（锁定）。
    // app.ts onLaunch 已做过一次（冷启动），这里覆盖热启动直接进页面的路径，幂等。
    bindCompanyFromScene(query.scene, query.companyId)

    let staffId = parseInt(query.staffId || '0', 10)
    // 从小程序码 scene 解析（形如 "staffId=12"）
    if (!staffId && query.scene) {
      const params = decodeURIComponent(query.scene)
      const m = /staffId=(\d+)/.exec(params)
      if (m) staffId = parseInt(m[1], 10)
    }
    // 仍无参数（直接打开 / 从「我的」页返回）：已登录员工用自己的编号，
    // 免去每次手输；未登录才弹窗输入（联调取数 / 演示用）
    if (!staffId) {
      const app = getApp() as any
      staffId = (app && app.globalData && app.globalData.myStaffId) || 0
    }
    // 推导流量来源：优先海报标记（from=poster），否则按微信启动场景值归类。
    // wx.getEnterOptionsSync 在基础库 2.12+ 可用，老版本回退 getLaunchOptionsSync；
    // 均不可用（如测试桩未实现）时 scene=0 → unknown，不影响后续流程。
    let sceneNum = 0
    try {
      const enter = (wx.getEnterOptionsSync ? wx.getEnterOptionsSync() : wx.getLaunchOptionsSync ? wx.getLaunchOptionsSync() : {}) as any
      sceneNum = Number(enter && enter.scene) || 0
    } catch {
      sceneNum = 0
    }
    if (staffId) {
      this.setData({ staffId, source: deriveSource(sceneNum, query) })
      this.loadAll()
    } else {
      // 既没有 staffId 参数、也没有登录态：**不弹输入框**，直接去名片墙。
      //
      // 这里原先是一个 wx.showModal 让访客「输入员工编号」——它是开发期联调用的
      // 兜底，但裸启动（扫体验版二维码 / 直接打开小程序）恰好就是这条路径，
      // 于是真实访客打开这家公司的小程序，看到的第一个界面是一个要他输入数字的弹窗。
      // 名片墙（pages/team/team）才是自然的落点：先看「这家公司有谁」，点谁看谁。
      //
      // 为什么是 reLaunch 而不是 redirectTo：index 有可能是页面栈里唯一一页
      // （直接打开），也可能是从名片墙 navigateTo 过来的（那种一定带 staffId，
      // 走不到这里）；reLaunch 一律清栈重建，不会留下一个「返回还是空名片页」的栈。
      this.setData({ loading: false })
      wx.reLaunch({ url: '/pages/team/team' })
    }
  },
  onShow() {
    // 埋点挂 onShow（首页文档 §4 明确要求）：名片每次显示记一次访问
    this.trackVisit()
    // 左上角「我的」入口跟随登录态刷新（登录 / 退出后回名片页时即时反映）
    this.loadMeInfo()
  },
  // 页面滚动：把 scrollTop 映射到顶部遮罩透明度，滚过顶栏下沿（navHeight）时恰好浮现。
  // 带 0.01 死区，仅在视觉有变化时 setData，避免高频无效渲染。
  onPageScroll(e: { scrollTop: number }) {
    const navH = this.data.navHeight || 64
    let o = e.scrollTop / navH
    if (o < 0) o = 0
    if (o > 1) o = 1
    if (Math.abs(o - this.data._lastMask) >= 0.01) {
      this.setData({ navMask: o })
      this.data._lastMask = o
    }
  },
  // 读取当前登录员工，用于在自定义顶栏的「我的」入口展示头像；
  // 文字固定「我的」不显示姓名（与名片卡信息重复，用户 2026-10-20 反馈）。
  // 无头像时显示默认用户图标（见 nav-me-user-icon），无 token 时仅图标。
  // 失败（令牌失效）静默回退，不弹错。
  loadMeInfo() {
    const token = wx.getStorageSync('empToken')
    if (!token) {
      this.setData({ meAvatar: '' })
      return
    }
    request('GET', '/api/employee/me')
      .then((r: any) => {
        if (r && r.code === 0 && r.data) {
          this.setData({ meAvatar: assetUrl(r.data.avatar) || '' })
        } else {
          this.setData({ meAvatar: '' })
        }
      })
      .catch(() => this.setData({ meAvatar: '' }))
  },
  // 访问埋点：带 lastVisitStaffId 去重（见 data 注释）。
  // loadAll 成功后也会调一次——onLoad 走弹窗输编号时，onShow 早于 staffId 就绪，
  // 只挂 onShow 会漏掉这条路径。
  trackVisit() {
    const sid = this.data.staffId
    if (!sid || sid === this.data.lastVisitStaffId) return
    this.setData({ lastVisitStaffId: sid })
    // 入口事件携带推导出的来源（后端按 source 归「来源分布」与「裂变榜」）
    track('visit', sid, 0, this.data.source)
  },
  // 输入员工编号进入名片（生产环境入口一般为分享链接 / 扫码，此处作为缺省兜底）
  // 已移除：它是开发期联调用的兜底，但裸启动（扫体验版二维码 / 直接打开）走的正是
  // 这条路径，真实访客会被一个要数字的弹窗挡在门外。现在无 staffId 时跳名片墙
  // （见 onLoad），名片墙为空也能告诉访客「这家公司还没人开通名片」。
  async loadAll() {
    const sid = this.data.staffId
    this.setData({ loading: true, error: false })
    try {
      const [card, ach, svc, layout] = await Promise.all([
        request('GET', '/api/card?staffId=' + sid),
        request('GET', '/api/achievements'),
        request('GET', '/api/services'),
        request('GET', '/api/home-layout'),
      ])
      // 停用：后端返回 code=2（无 employee）
      if (card.code === 2) {
        this.setData({ disabled: true, loading: false })
        return
      }
      // 不存在：后端返回 code=1（无 employee）。
      // 注意：/api/card 的 employee/company/profile 在响应根级（非 data 包裹），
      // /api/achievements 的 categories/featured、/api/home-layout 的 order 同样在根级，
      // 切勿误读成 card.data.* / ach.data.* / layout.data.*（此前 bug：卡片页恒走 notFound）。
      if (card.code === 1 || !card.employee) {
        this.setData({ notFound: true, loading: false })
        wx.showToast({ title: '员工不存在', icon: 'none' })
        return
      }
      // 后端资源返回相对路径（/uploads/... 等），小程序 <image src> 不认相对，
      // 必须在端上拼绝对——统一经 assetUrl 修正后再下发（employee-card /
      // featured-projects / achievement-grid / service-grid / company-video 组件直接消费）。
      const cardData = card as any
      if (cardData.employee) cardData.employee.avatar = assetUrl(cardData.employee.avatar)
      if (cardData.company) cardData.company.logo = assetUrl(cardData.company.logo)
      if (cardData.profile) cardData.profile.cover = assetUrl(cardData.profile.cover)
      // 列表/宫格用缩略图降低带宽与首屏耗时；缩略图缺失时由 image binderror 回退原图。
      const categories = (ach.categories || []).map((c: any) => {
        const icon = assetUrl(c.icon)
        return { ...c, icon, iconThumb: thumbnailURL(icon) }
      })
      const featured = (ach.featured || []).map((p: any) => {
        const cover = assetUrl(p.cover)
        return { ...p, cover, coverThumb: thumbnailURL(cover) }
      })
      const services = (svc.data || []).map((s: any) => {
        const icon = assetUrl(s.icon)
        return { ...s, icon, iconThumb: thumbnailURL(icon) }
      })
      const order = layout.order && layout.order.length ? layout.order : ['achievement', 'service']
      this.setData({
        employee: cardData.employee,
        company: cardData.company,
        profile: cardData.profile,
        categories,
        featured,
        services,
        homeOrder: order,
        caseArrow: order.indexOf('achievement') >= 0 ? '▾' : '▸',
        // 首页背景：后端在响应根级下发（与 order 同级），缺省即不设背景
        bgImage: assetUrl(layout.bgImage || ''),
        bgMask: layout.bgMask || 0,
        bgMaskAlpha: (layout.bgMask || 0) / 100,
        disabled: false,
        notFound: false,
        loading: false,
      })
      this.trackVisit()
    } catch {
      // 断网 / 接口异常：loading 必须落下来。此前 reject 直接冒泡出 async 函数，
      // 页面永远停在「加载中…」——这是真实缺陷，不是体验问题。
      this.setData({ loading: false, error: true })
    }
  },
  reload() {
    this.loadAll()
  },
  // 「查看案例」（首页文档 §2 名片区右上按钮）：业绩区在首页布局里 → 滚动过去（不离开名片页）；
  // 被布局隐藏 → 跳第一个分类的项目列表；一个分类都没有 → 提示
  // 「更多案例」：进入全部项目案例列表（categoryId=0 不过滤），列表页内有业绩分类切换
  goAllCases() {
    const url =
      '/pages/project/list/list?categoryId=0' +
      (this.data.staffId ? '&staffId=' + this.data.staffId : '')
    wx.navigateTo({ url })
  },
  onEnterCase() {
    const cats = this.data.categories || []
    if (!cats.length) {
      wx.showToast({ title: '暂无案例', icon: 'none' })
      return
    }
    const hasAchievement = (this.data.homeOrder || []).indexOf('achievement') >= 0
    if (hasAchievement) {
      wx.pageScrollTo({ selector: '#sec-achievement', duration: 300 })
      return
    }
    // 业绩区被隐藏时才跳转：进「全部」而非第一个分类（列表页已有分类切换，
    // 让用户自己挑比替他选第一个更合理）
    this.goAllCases()
  },
  onShareAppMessage() {
    const c = this.data.company
    // 分享链接带上 companyId（平台模式）：对方设备首次打开即落到同一企业，
    // 不依赖本机粘滞。独立版恒带锁定值，无害。
    const cid = getCompanyId()
    // 转发名片埋点（targetId=0，区别于案例/服务详情的转发）。
    // 转发量是「员工愿不愿意把这张名片推出去」的直接信号——销售动力指标。
    track('share', this.data.staffId)
    return {
      // 名片还没加载出来时的兜底标题：用产品名，**不能**用任何具体企业名——
      // 一套代码多租户，写死某个客户名对第二个客户就是错的。
      title: (c && (c.shortName || c.fullName)) || FALLBACK_BRAND,
      path:
        '/pages/index/index?staffId=' +
        this.data.staffId +
        (cid > 0 ? '&companyId=' + cid : ''),
    }
  },
  // 左上角「我的」入口：底部 tabBar 已移除，改由 navigateTo 进入「我的」页
  goMe() {
    wx.navigateTo({ url: '/pages/employee/me/me' })
  },
  goPoster() {
    wx.navigateTo({ url: '/pages/poster/poster?staffId=' + this.data.staffId })
  },
})
