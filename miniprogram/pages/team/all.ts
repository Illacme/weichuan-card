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
import { applyBrandTitle } from '../../utils/brand'
import { mapTeamList, mergeTeam, teamParams, hasMoreOf } from '../../utils/team'

Page({
  data: {
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

  onLoad(query: any) {
    const cid = parseInt((query && query.companyId) || '0', 10)
    const kw = ((query && query.keyword) || '').trim()
    const patch: Record<string, unknown> = {}
    if (cid > 0) patch.companyId = cid
    if (kw) patch.keyword = patch.activeKeyword = kw
    if (Object.keys(patch).length) this.setData(patch)
    this.load()
  },

  // load 拉数据。more=true 表示「追加下一页」，false 表示「从第一页重来」。
  async load(opts?: { silent?: boolean; more?: boolean }) {
    const more = !!(opts && opts.more)
    // 防重入：加载更多时若已有请求在途，或后端说没有了，直接忽略
    if (more && (this.data.loadingMore || !this.data.hasMore)) return

    const nextPage = more ? this.data.page + 1 : 1
    const patch: Record<string, unknown> = { error: false, moreError: false }
    if (more) {
      patch.loadingMore = true
    } else if (!opts || !opts.silent) {
      patch.loading = true
    }
    this.setData(patch)

    try {
      const r: any = await request('GET', '/api/team', teamParams(nextPage, this.data.activeKeyword))
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
    } catch {
      // 追加失败：保留已加载的人，只在底部提示可重试
      this.setData({
        loading: false,
        loadingMore: false,
        error: !more,
        moreError: more,
      })
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
