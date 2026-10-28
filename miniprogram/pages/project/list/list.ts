import { request, assetUrl } from '../../../utils/request'
import { thumbnailURL, onThumbError } from '../../../utils/thumb'

Page({
  data: {
    categoryId: 0,
    staffId: 0,
    list: [],
    cats: [],
    // 进入时把当前分类滚进可视区（从首页点靠右的分类进来时，否则用户看不到选中项）。
    // 只在「页面进入」时设置一次，之后手动点分类不再滚动——避免刚修好的「弹回/跳动」问题。
    toView: '',
    loading: true,
    error: false,
  },
  onLoad(query: any) {
    this.setData({
      categoryId: parseInt(query.categoryId || '0', 10),
      // 从首页透传（跳转页文档 §5）：子页分享 / 返回名片用
      staffId: parseInt(query.staffId || '0', 10),
    })
    this.loadCats()
    this.load()
  },
  // 业绩分类切换条：/api/achievements 的 categories 在响应根级（非 data 包裹）
  async loadCats() {
    try {
      const r: any = await request('GET', '/api/achievements')
      const cats = r.categories || []
      const target = this.data.categoryId > 0 ? 'cat-' + this.data.categoryId : ''
      // scroll-into-view 必须在目标节点渲染完成后设置，否则找不到元素不生效
      this.setData({ cats }, () => {
        if (target) this.setData({ toView: target })
      })
    } catch {
      // 分类加载失败不阻塞列表：列表本身仍可用，只是没有分类切换
    }
  },
  onCat(e: any) {
    const id = parseInt(e.currentTarget.dataset.id, 10) || 0
    if (id === this.data.categoryId) return
    this.setData({ categoryId: id })
    this.load()
  },
  async load() {
    this.setData({ loading: true, error: false })
    try {
      const r = await request('GET', '/api/projects', { categoryId: this.data.categoryId })
      const list = (r.data || []).map((p: any) => {
        const cover = assetUrl(p.cover)
        return { ...p, cover, coverThumb: thumbnailURL(cover) }
      })
      this.setData({ list, loading: false })
      // 标题栏回显分类名（跳转页文档 §1.2）：列表接口已回填 categoryName，无需二次查询；
      // 分类被删导致空名时退化为固定标题；「全部」（categoryId=0）不回显分类名
      const name = this.data.categoryId > 0 && list.length ? list[0].categoryName : ''
      wx.setNavigationBarTitle({ title: name || '项目案例' })
    } catch {
      this.setData({ loading: false, error: true })
    }
  },
  onTap(e: any) {
    const url =
      '/pages/project/detail/detail?id=' +
      e.currentTarget.dataset.id +
      (this.data.staffId ? '&staffId=' + this.data.staffId : '')
    wx.navigateTo({ url })
  },

  onImgErr: onThumbError,
})
