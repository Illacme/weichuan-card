import { assetUrl, request } from '../../../utils/request'
import { getOpenid, wxLoginCode } from '../../../utils/wechat'
import { getCompanyId } from '../../../utils/tenant'
import { onImgFail } from '../../../utils/thumb'

// 登录成功后的收尾：存令牌 + 回到「我的」页。
// 两个登录方式（微信一键 / 手机号密码）共用，避免一处改了另一处忘了。
function afterLogin(r: any) {
  wx.setStorageSync('empToken', r.token)
  if (r.refreshToken) wx.setStorageSync('empRefreshToken', r.refreshToken)
  // 登录页由「我的」页 navigateTo 打开：登录成功后返回上一页（即「我的」），
  // 其 onShow 会重新拉取员工资料并显示已登录态。兜底 reLaunch 到「我的」。
  wx.navigateBack({
    delta: 1,
    fail: () => wx.reLaunch({ url: '/pages/employee/me/me' }),
  })
}

Page({
  data: {
    phone: '',
    password: '',
    submitting: false,
    // 一键登录单独的 loading：两种登录方式互斥，各自一个标记，
    // 免得多点一下把另一个按钮也变成 loading。
    wxSubmitting: false,
    // 品牌区：企业简称（优先）/ 全称。为空时页面显示中性的「员工登录」，
    // **绝不回落到产品名**——登录页顶着平台产品名，客户会觉得「没改干净」。
    brand: '',
    logo: '',
    // 企业 LOGO 加载失败的地址（utils/thumb.ts onImgFail 写入）：失败即回落到
    // 渐变磁贴，避免页头正中挂一块裂图——比没有 LOGO 更难看。
    logoFail: '',
  },
  onLoad() {
    this.loadBrand()
  },
  // 拉当前企业的公开信息（/api/company 会自动带 companyId）：简称优先、全称次之；
  // 两者都空时保持空串，由页面显示「员工登录」。失败不阻断登录——品牌只是装饰。
  // 返回 Promise 方便测试 await（onLoad 里不 await，失败也不该拖慢页面）。
  loadBrand() {
    return request('GET', '/api/company')
      .then((r: any) => {
        const d = (r && r.code === 0 && r.data) || {}
        const short = String(d.shortName || '').trim()
        const full = String(d.name || '').trim()
        this.setData({ brand: short || full, logo: assetUrl(d.logo) })
      })
      .catch(() => {})
  },
  // 企业 LOGO 加载失败：记下失败地址（见 utils/thumb.ts），WXML 据此回落渐变磁贴
  onImgFail,
  onInput(e: any) {
    this.setData({ [e.currentTarget.dataset.field]: e.detail.value })
  },
  // 微信一键登录：wx.login 拿一次性 code → 后端换 openid → 找已绑定的员工 → 发令牌。
  // 用户全程不需要输入任何东西，也不需要授权弹窗（wx.login 是静默的）。
  async onWechatLogin() {
    if (this.data.wxSubmitting) return // 防连点：连点会连发两个 code，后一个必然失效
    this.setData({ wxSubmitting: true })
    try {
      const code = await wxLoginCode()
      if (!code) {
        wx.showToast({ title: '微信登录失败，请重试', icon: 'none' })
        return
      }
      const r = await request('POST', '/api/employee/login/wechat', {
        code,
        // 企业独立版各配自己的 appid，凭据要按租户选，否则换出来的 openid 属于别的小程序
        companyId: getCompanyId(),
      })
      if (r.code === 0 && r.token) {
        afterLogin(r)
        return
      }
      if (r.code === 3) {
        // 未绑定是唯一一个「用户自己就能解决」的失败，用 modal 把解法说全：
        // 只弹一句「登录失败」的话，用户不知道该去哪绑定，等于功能不可用。
        wx.showModal({
          title: '还没有绑定微信',
          content: '请先用手机号密码登录，然后在「我的」页绑定微信，之后就能一键登录了',
          showCancel: false,
        })
        return
      }
      wx.showToast({ title: r.msg || '登录失败', icon: 'none' })
    } catch {
      // 网络异常：request 会 reject，不接住就是未捕获的 Promise rejection
      wx.showToast({ title: '网络异常，请重试', icon: 'none' })
    } finally {
      this.setData({ wxSubmitting: false })
    }
  },
  async onSubmit() {
    const { phone, password } = this.data
    if (!phone || !password) {
      wx.showToast({ title: '请输入手机号和密码', icon: 'none' })
      return
    }
    // 只静默采集 openid（wx.login 换取，无需弹窗）供后台核实身份；
    // 不再弹昵称授权框——昵称在申请开通时由用户显式授权填写，登录不重复索取。
    this.setData({ submitting: true })
    try {
      const openid = await getOpenid()
      const r = await request('POST', '/api/employee/login', { phone, password, openid })
      if (r.code === 0 && r.token) {
        afterLogin(r)
        return
      }
      wx.showToast({ title: r.msg || '登录失败', icon: 'none' })
    } catch {
      // 原来这里没有 catch：断网时按钮会永远停在 loading，用户只能杀进程重进
      wx.showToast({ title: '网络异常，请重试', icon: 'none' })
    } finally {
      this.setData({ submitting: false })
    }
  },
})
