import { request } from '../../../utils/request'
import { parseRich } from '../../../utils/richtext'
import { formatDateTime } from '../../../utils/datetime'
import { getCompanyId } from '../../../utils/tenant'
import { track } from '../../../utils/track'

Page({
  data: {
    id: 0,
    staffId: 0,
    service: null,
    segments: [],
    updatedAtText: '',
    loading: true,
    error: false,
  },
  onLoad(query: any) {
    this.setData({
      id: parseInt(query.id || '0', 10),
      // 从首页透传（跳转页文档 §5）：分享时带回对应员工名片
      staffId: parseInt(query.staffId || '0', 10),
    })
    this.load()
  },
  async load() {
    this.setData({ loading: true, error: false })
    try {
      const r = await request('GET', '/api/services/' + this.data.id)
      if (!r.data) {
        wx.showToast({ title: '服务不存在', icon: 'none' })
        this.setData({ loading: false })
        return
      }
      this.setData({
        service: r.data,
        segments: parseRich(r.data.content || ''),
        updatedAtText: r.data.updatedAt ? formatDateTime(r.data.updatedAt) : '',
        loading: false,
      })
      // 内容浏览埋点：服务详情加载成功才算一次「浏览」（同案例详情口径）。
      // targetId = 服务 id，后台 TOP 服务榜靠它聚合（后端校验存在+同租户）。
      track('service_view', this.data.staffId, this.data.id)
    } catch {
      this.setData({ loading: false, error: true })
    }
  },
  // 分享带回 staffId 与 companyId：从分享入口进来的访客仍落在对应员工名片链、
  // 对应企业的内容上（不依赖本机粘滞）。
  // 转发埋点的 targetId = 服务 id（0 表示转名片）；转发不进 TOP 榜聚合，
  // 只在明细里可查「这个服务被转发了多少次」。
  onShareAppMessage() {
    const s = this.data.service
    const cid = getCompanyId()
    track('share', this.data.staffId, this.data.id)
    return {
      title: s ? s.title : '业务服务',
      path:
        '/pages/service/detail/detail?id=' +
        this.data.id +
        (this.data.staffId ? '&staffId=' + this.data.staffId : '') +
        (cid > 0 ? '&companyId=' + cid : ''),
    }
  },
})
