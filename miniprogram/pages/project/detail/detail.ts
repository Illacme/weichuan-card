import { request, assetUrl } from '../../../utils/request'
import { parseRich } from '../../../utils/richtext'
import { formatDateTime } from '../../../utils/datetime'
import { getCompanyId } from '../../../utils/tenant'
import { track } from '../../../utils/track'

Page({
  data: {
    id: 0,
    staffId: 0,
    project: null,
    segments: [],
    updatedAtText: '',
    loading: true,
    error: false,
  },
  onLoad(query: any) {
    this.setData({
      id: parseInt(query.id || '0', 10),
      // 从首页 / 列表透传（跳转页文档 §5）：分享时带回对应员工名片
      staffId: parseInt(query.staffId || '0', 10),
    })
    this.load()
  },
  // 下拉刷新走 silent：不把整页切成「加载中」，否则内容会闪一下再回来。
  async load(opts?: { silent?: boolean }) {
    const patch: Record<string, unknown> = { error: false }
    if (!opts || !opts.silent) patch.loading = true
    this.setData(patch)
    try {
      const r = await request('GET', '/api/projects/' + this.data.id)
      if (!r.data) {
        wx.showToast({ title: '案例不存在或已下架', icon: 'none' })
        this.setData({ loading: false })
        return
      }
      // 封面与相册均为后端相对路径，端上拼绝对（<image> 与 previewImage 都需绝对 URL）
      r.data.cover = assetUrl(r.data.cover)
      if (Array.isArray(r.data.album)) {
        r.data.album = r.data.album.map((u: string) => assetUrl(u))
      }
      this.setData({
        project: r.data,
        segments: parseRich(r.data.detail || ''),
        // updatedAt 是后端 RFC3339，直接绑会把 T／时区后缀显示出来（同 PC 端 formatDateTime 口径）
        updatedAtText: r.data.updatedAt ? formatDateTime(r.data.updatedAt) : '',
        loading: false,
      })
      // 内容浏览埋点：案例详情加载成功才算一次「浏览」——加载失败/不存在不计，
      // 否则统计里的案例热度混进大量根本没看到内容的误报。targetId 必须带：
      // 后台「内容浏览 TOP 榜」靠它回答「哪个案例热」（后端校验存在+同租户）。
      track('project_view', this.data.staffId, this.data.id)
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
  // 分享带回 staffId 与 companyId：从分享入口进来的访客仍落在对应员工名片链、
  // 对应企业的内容上（不依赖本机粘滞）。
  // 转发埋点的 targetId = 案例 id（0 表示转名片）——转发不进 TOP 榜聚合，
  // 只在明细里可查「这个案例被转发了多少次」。
  onShareAppMessage() {
    const p = this.data.project
    const cid = getCompanyId()
    track('share', this.data.staffId, this.data.id)
    return {
      title: p ? p.name : '项目案例',
      path:
        '/pages/project/detail/detail?id=' +
        this.data.id +
        (this.data.staffId ? '&staffId=' + this.data.staffId : '') +
        (cid > 0 ? '&companyId=' + cid : ''),
    }
  },
  preview(e: any) {
    const album = (this.data.project && this.data.project.album) || []
    wx.previewImage({ urls: album, current: e.currentTarget.dataset.url })
  },
})
