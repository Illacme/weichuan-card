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
import { request, assetUrl } from '../../utils/request'
import { thumbnailURL, onThumbError } from '../../utils/thumb'
import { FALLBACK_BRAND, applyBrandTitle } from '../../utils/brand'

Page({
  data: {
    list: [],
    total: 0,
    // 人数超过后端上限（repository.MaxTeamList）时为真：页面底部要说清
    // 「只显示了前 N 位」，否则访客以为公司就这么点人。
    truncated: false,
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
    if (cid > 0) this.setData({ companyId: cid })
    this.load()
  },

  async load() {
    this.setData({ loading: true, error: false, unavailable: false })
    try {
      const r: any = await request('GET', '/api/team')
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
      const entry = r.entry || {}
      const entryStaff = Number(entry.staffId) || 0
      if (entry.mode === 'staff' && entryStaff > 0) {
        const cid = Number(comp.id) || this.data.companyId
        wx.reLaunch({
          url:
            '/pages/index/index?staffId=' +
            entryStaff +
            (cid > 0 ? '&companyId=' + cid : ''),
        })
        return
      }
      const list = (r.data || []).map((e: any) => {
        const avatar = assetUrl(e.avatar)
        return {
          staffId: e.staffId,
          name: e.name || '未命名',
          title: e.title || '',
          avatar,
          avatarThumb: thumbnailURL(avatar),
        }
      })
      const total = Number(r.total) || 0
      const title = comp.shortName || comp.fullName || FALLBACK_BRAND
      this.setData({
        list,
        total,
        truncated: total > list.length,
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
