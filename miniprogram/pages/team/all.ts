// 查看全部同事（pages/team/all）——名片墙的「第二层」。
//
// 为什么单独一页：名片墙首屏只拉一页（60）就出画面，500 人的企业不必等
// 300 条 JSON 解析完；剩下的人在这个页面滚动加载。
//
// 与落地页的分工：
//   · pages/team/team  抬头 + 搜索 + 首屏一页 + 「查看全部 N 位同事」入口
//   · pages/team/all   搜索 + 滚动加载（onReachBottom）+ 到底提示
// 两页共用 utils/team.ts 的参数与整形，避免「一页 60、另一页 20」这种漂移。
//
// 三个必须做对的细节：
//  1. 滚动加载要**防重入**：onReachBottom 在惯性滚动中会连触发多次，
//     不锁的话同一页会被请求好几遍，列表里出现重复宫格；
//  2. 追加时按 staffId 去重（mergeTeam）：翻页期间管理员可能新增/停用员工，
//     同一个人会同时出现在相邻两页；
//  3. 加载更多失败不能把整页打成错误态——已经看到的人不该消失，
//     只在底部给一行「加载失败，点击重试」。
import { request } from '../../utils/request'
import { onThumbError } from '../../utils/thumb'
import { latestGuard, isSuperseded } from '../../utils/latest'
import { applyBrandTitle } from '../../utils/brand'
import { mapTeamList, mergeTeam, teamParams, hasMoreOf } from '../../utils/team'
import { bindTheme } from '../../utils/theme'

Page({
  data: {
    themeClass: '',
    list: [],
    total: 0,
    hasMore: false,
    // 已加载到第几页（后端回传的实际页码，越界时会被夹到末页）
    page: 0,
    keyword: '',
    activeKeyword: '',
    companyId: 0,
    loading: true,
    error: false,
    unavailable: false,
    // 加载更多中 / 加载更多失败（底部提示用，不影响已渲染内容）
    loadingMore: false,
    moreError: false,
  },

  // 搜索 / 清空 / 下拉刷新 / 滚动追加共用一个守门：换关键字时若旧请求还在途，
  // 旧响应会把 list 重新填回旧关键字的人（用户点中的人反而不在列表里）。
  guard: null as any,
  // 主查询（非 more）在途时不允许发追加：否则追加会 guard.next() 取消主查询，
  // 第 1 页的结果永远回不来，用户看到的是「越滑越多的第 2 页」。
  mainInFlight: false,
  onLoad(query: any) {
    bindTheme(this)
    if (!this.guard) this.guard = latestGuard()
    const cid = parseInt((query && query.companyId) || '0', 10)
    const kw = ((query && query.keyword) || '').trim()
    const patch: Record<string, unknown> = {}
    if (cid > 0) patch.companyId = cid
    if (kw) patch.keyword = patch.activeKeyword = kw
    if (Object.keys(patch).length) this.setData(patch)
    this.load()
  },

  onUnload() {
    this.guard?.cancel()
  },

  // load 拉数据。more=true 表示「追加下一页」，false 表示「从第一页重来」。
  async load(opts?: { silent?: boolean; more?: boolean }) {
    const more = !!(opts && opts.more)
    // 防重入：加载更多时若已有请求在途、主查询在途，或后端说没有了，直接忽略
    if (more && (this.data.loadingMore || !this.data.hasMore || this.mainInFlight)) return

    if (!this.guard) this.guard = latestGuard()
    const guard = this.guard
    const sig = guard.next()
    // 新轮开始：上一轮的主查询要么已结束，要么刚被 next() 取消，标志统一复位。
    // 旧轮的 finally 判 isCurrent=false，不会回来把新轮的标志改错。
    this.mainInFlight = !more
    const nextPage = more ? this.data.page + 1 : 1
    // 上一轮若是在途的「加载更多」并被本次取消，loadingMore 会永远为 true，
    // 之后所有 onReachBottom 都被防重入挡住——开新轮时先复位这把锁。
    const patch: Record<string, unknown> = { error: false, moreError: false, loadingMore: false }
    if (more) {
      patch.loadingMore = true
    } else if (!opts || !opts.silent) {
      patch.loading = true
    }
    this.setData(patch)

    try {
      const r: any = await request('GET', '/api/team', teamParams(nextPage, this.data.activeKeyword), sig)
      if (!guard.isCurrent(sig)) return
      if (r.code !== 0) {
        // 企业不存在 / 停用：不是网络问题，重试也没用
        this.setData({ loading: false, loadingMore: false, unavailable: true })
        return
      }
      const items = mapTeamList(r.data)
      const comp = r.company || {}
      this.setData({
        list: more ? mergeTeam(this.data.list, items) : items,
        total: Number(r.total) || 0,
        hasMore: hasMoreOf(r),
        page: Number(r.page) || nextPage,
        companyId: Number(comp.id) || this.data.companyId,
        loading: false,
        loadingMore: false,
      })
      applyBrandTitle({ shortName: comp.shortName, fullName: comp.fullName })
    } catch (e) {
      if (isSuperseded(e) || !guard.isCurrent(sig)) return
      // 追加失败：保留已加载的人，只在底部提示可重试
      this.setData({
        loading: false,
        loadingMore: false,
        error: !more,
        moreError: more,
      })
    } finally {
      // 旧轮（已被取消）不许清掉新轮正在转的圈，也不许把追加锁一直留着
      if (guard.isCurrent(sig)) {
        this.mainInFlight = false
        this.setData({ loading: false, loadingMore: false })
      }
    }
  },

  // 滚动到底：追加下一页。惯性滚动会连触发，靠 load 里的防重入挡住。
  onReachBottom() {
    this.load({ more: true })
  },

  // 加载更多失败后点提示重试
  onRetryMore() {
    this.load({ more: true })
  },

  async onPullDownRefresh() {
    try {
      await this.load({ silent: true })
    } catch {
      // 错误态由 load 自己落
    } finally {
      wx.stopPullDownRefresh()
    }
  },

  // ---- 搜索 ----
  onKeywordInput(e: any) {
    this.setData({ keyword: (e && e.detail && e.detail.value) || '' })
  },

  onSearch() {
    const kw = (this.data.keyword || '').trim()
    if (kw === this.data.activeKeyword) return
    // 换关键字必须从第一页重来，否则会把新搜索结果追加到旧结果后面
    this.setData({ activeKeyword: kw, list: [], page: 0, hasMore: false })
    this.load()
  },

  onClearSearch() {
    if (!this.data.activeKeyword && !this.data.keyword) return
    this.setData({ keyword: '', activeKeyword: '', list: [], page: 0, hasMore: false })
    this.load()
  },

  onTap(e: any) {
    const staffId = parseInt(e.currentTarget.dataset.id, 10) || 0
    if (staffId <= 0) return
    const cid = this.data.companyId
    wx.navigateTo({
      url:
        '/pages/index/index?staffId=' +
        staffId +
        (cid > 0 ? '&companyId=' + cid : ''),
    })
  },

  onShareAppMessage() {
    return { title: this.data.activeKeyword ? '团队成员' : '我们的团队', path: '/pages/team/team' }
  },

  onImgErr: onThumbError,
})
