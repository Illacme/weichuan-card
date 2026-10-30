// 名片墙：企业全员名片列表，小程序**裸启动**时的落地页。
//
// 为什么需要这一页：扫「体验版二维码」或直接打开小程序属于裸启动——微信不会带任何
// 参数，页面拿不到 staffId。此前首页在这种情况下会弹一个「输入员工编号」的输入框
// （开发期兜底，注释里写着「联调取数 / 演示用」），真实访客看到的第一个界面就是
// 一个要求输入数字的弹窗。名片墙把落点换成「这家公司有谁」，点谁看谁的名片。
//
// 三条边界：
//  1. 只画公开字段（姓名/职位/头像/编号）——后端也做了白名单，两边都不能松；
//  2. 企业不可用（不存在/停用）与「公司还没人」是两种状态，文案必须分开：
//     前者要说「不可用」，后者要说「还没有人开通名片」，否则访客以为系统坏了；
//  3. 跳名片页要带上 companyId：平台模式下不传就会被后端兜到 1 号企业，
//     点进去看到的是别人家公司的人。
//
// 分页与搜索（2026-10-30）：
//   - 原来这一页一次拉全员（后端上限 300），500 人以上的企业只能看到前 300 位，
//     而且没有任何入口看到剩下的人——等于不能用。现在改成**首屏一页 60** +
//     「查看全部 N 位同事」进 pages/team/all 做滚动加载；
//   - 搜索框就地搜索（同一个接口带 keyword），只拉第一页；想看全部匹配结果
//     同样走「查看全部」并带上关键字。
//   - 首屏只拉 60 而不是 300：大企业首屏不必等 300 条 JSON 解析完才出画面。
import { request, assetUrl } from '../../utils/request'
import { thumbnailURL, onThumbError } from '../../utils/thumb'
import { FALLBACK_BRAND, applyBrandTitle } from '../../utils/brand'
import { mapTeamList, teamParams, hasMoreOf } from '../../utils/team'

Page({
  data: {
    list: [],
    total: 0,
    // 还有更多人（后端 hasMore）：底部据此显示「查看全部 N 位同事」。
    // 不能用 total > list.length 推——搜索时 total 是匹配总数，list 只有第一页，
    // 两者相减得到的是「没加载的匹配数」，不是「还有更多人」这个布尔语义。
    hasMore: false,
    // 搜索关键字（input 的 value 双向绑定）
    keyword: '',
    // 已提交给接口的关键字：与 keyword 分开，避免用户边打字边发请求
    // （输入框里正在输入的字符还没确认，不该触发网络请求）
    activeKeyword: '',
    // 企业抬头：企业信息还没填时后端给空串，标题回落品牌名
    title: FALLBACK_BRAND,
    logo: '',
    companyId: 0,
    loading: true,
    error: false,
    // 企业不存在 / 已停用（后端 code=1），与「公司暂时没有员工」区分开
    unavailable: false,
  },

  onLoad(query: any) {
    // 平台模式下企业可能由扫码 scene 带进来；query 里的 companyId 只用于
    // 兜底展示，实际请求的 companyId 由 utils/request 按租户语义统一注入。
    const cid = parseInt((query && query.companyId) || '0', 10)
    const kw = ((query && query.keyword) || '').trim()
    const patch: Record<string, unknown> = {}
    if (cid > 0) patch.companyId = cid
    // 从「查看全部」返回时把关键字带回来，否则用户会以为自己搜的东西丢了
    if (kw) patch.keyword = patch.activeKeyword = kw
    if (Object.keys(patch).length) this.setData(patch)
    this.load()
  },

  // 下拉刷新走 silent：不把整页切成「加载中」，否则内容会闪一下再回来。
  async load(opts?: { silent?: boolean }) {
    const patch: Record<string, unknown> = { error: false, unavailable: false }
    if (!opts || !opts.silent) patch.loading = true
    this.setData(patch)
    try {
      const r: any = await request('GET', '/api/team', teamParams(1, this.data.activeKeyword))
      if (r.code !== 0) {
        // 企业不存在 / 停用：这不是网络问题，重试也没用，给明确说法
        this.setData({ loading: false, unavailable: true })
        return
      }
      const comp = r.company || {}
      // 企业在后台配了「默认进入指定员工的名片」（一张名片打天下的用法）→ 直接改道，
      // 不渲染名片墙。后端已经把「指定员工是否还有效」校验过并回落成 wall，
      // 所以这里放心按 mode 走。
      //
      // 用 reLaunch 而不是 redirectTo：名片墙是 entryPagePath，栈里只有它自己；
      // reLaunch 清栈重建，访客按返回键直接退出小程序，而不是退回一张他从没看过的墙。
      //
      // 只在**没有搜索**时改道：用户在墙上搜人时突然被弹进某个人的名片，
      // 会觉得「我搜的人呢」。
      const entry = r.entry || {}
      const entryStaff = Number(entry.staffId) || 0
      if (entry.mode === 'staff' && entryStaff > 0 && !this.data.activeKeyword) {
        const cid = Number(comp.id) || this.data.companyId
        wx.reLaunch({
          url:
            '/pages/index/index?staffId=' +
            entryStaff +
            (cid > 0 ? '&companyId=' + cid : ''),
        })
        return
      }
      const list = mapTeamList(r.data)
      const total = Number(r.total) || 0
      const title = comp.shortName || comp.fullName || FALLBACK_BRAND
      this.setData({
        list,
        total,
        hasMore: hasMoreOf(r),
        title,
        logo: assetUrl(comp.logo),
        // 抬头里的 id 比 query 更权威（后端按实际生效租户回传）
        companyId: Number(comp.id) || this.data.companyId,
        loading: false,
      })
      // 导航栏标题与页面抬头一致；企业信息为空时回落品牌名
      applyBrandTitle({ shortName: comp.shortName, fullName: comp.fullName })
    } catch {
      this.setData({ loading: false, error: true })
    }
  },

  // 下拉刷新：静默重拉，无论成败都收掉顶部指示器（不收会一直挂着转）。
  async onPullDownRefresh() {
    try {
      await this.load({ silent: true })
    } catch {
      // 错误态由 load 自己落（error 标记）；这里只兜网络异常，
      // 避免 onPullDownRefresh 抛出未捕获的 Promise rejection
    } finally {
      wx.stopPullDownRefresh()
    }
  },

  // ---- 搜索 ----
  //
  // 输入时不发请求（每敲一个字就打一次接口，既费流量又会让宫格闪烁）；
  // 只在键盘「搜索」键 / 点搜索按钮时提交。
  onKeywordInput(e: any) {
    this.setData({ keyword: (e && e.detail && e.detail.value) || '' })
  },

  onSearch() {
    const kw = (this.data.keyword || '').trim()
    if (kw === this.data.activeKeyword) return
    this.setData({ activeKeyword: kw })
    this.load()
  },

  onClearSearch() {
    if (!this.data.activeKeyword && !this.data.keyword) return
    this.setData({ keyword: '', activeKeyword: '' })
    this.load()
  },

  // 进「查看全部」：带上当前企业与已提交的关键字，让全部页接着搜同一个词
  goAll() {
    const cid = this.data.companyId
    const kw = this.data.activeKeyword
    const qs =
      (cid > 0 ? 'companyId=' + cid : '') +
      (kw ? (cid > 0 ? '&' : '') + 'keyword=' + encodeURIComponent(kw) : '')
    wx.navigateTo({ url: '/pages/team/all' + (qs ? '?' + qs : '') })
  },

  onTap(e: any) {
    const staffId = parseInt(e.currentTarget.dataset.id, 10) || 0
    if (staffId <= 0) return
    // 带上 companyId：不传的话平台模式下后端会兜到 1 号企业，
    // 点进去看到的是别家公司的人（见 utils/request.ts 的租户注入约定）
    const cid = this.data.companyId
    wx.navigateTo({
      url:
        '/pages/index/index?staffId=' +
        staffId +
        (cid > 0 ? '&companyId=' + cid : ''),
    })
  },

  onShareAppMessage() {
    // 名片墙本身适合整包转发（「看我们团队」），落点就回到名片墙
    return { title: this.data.title, path: '/pages/team/team' }
  },

  onImgErr: onThumbError,
})
