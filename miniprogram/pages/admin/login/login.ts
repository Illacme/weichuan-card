// 企业管理员登录（小程序端）。
//
// 账号密码直接复用后台 /api/admin/login/mobile：同一套 bcrypt 校验、同一个登录限流器、
// 同一套企业停用闸门与审计。与 PC 的唯一区别是令牌进响应体（手机没有 Cookie 容器）。
//
// 登录成功后顺手做一次微信绑定，之后打开审核页就能免密进入。
// 绑定失败**不阻断登录**：可能是企业没配小程序凭据、或该微信号已绑了别的管理员——
// 这两种情况都该让管理员先把活干了，绑定问题单独提示。
import { adminRequest, saveAdminAuth, hasAdminToken } from '../../../utils/adminRequest'
import { wxLoginCode } from '../../../utils/wechat'
import { getCompanyId } from '../../../utils/tenant'

const REVIEW_PAGE = '/pages/admin/apply/apply'

Page({
  data: {
    username: '',
    password: '',
    submitting: false,
    wxSubmitting: false,
    tip: '首次登录会自动把当前微信绑定到该账号，下次可直接免密进入',
  },

  onLoad() {
    // 已有管理员令牌就直接进审核页：从「我的」点进来的人不该再输一次密码
    if (hasAdminToken()) {
      wx.redirectTo({ url: REVIEW_PAGE })
    }
  },

  onInput(e: any) {
    const field = e.currentTarget.dataset.field
    this.setData({ [field]: e.detail.value })
  },

  async onSubmit() {
    if (this.data.submitting) return
    const username = (this.data.username || '').trim()
    const password = this.data.password || ''
    if (!username || !password) {
      wx.showToast({ title: '请输入账号和密码', icon: 'none' })
      return
    }
    this.setData({ submitting: true })
    try {
      const r = await adminRequest('POST', '/api/admin/login/mobile', { username, password })
      if (r.code !== 0) {
        // 后端对「账号不存在」与「密码错误」是同一句文案，这里原样透出，
        // 不要自己拼「该账号不存在」——那等于把枚举接口写在界面上。
        wx.showToast({ title: r.msg || '登录失败', icon: 'none' })
        return
      }
      saveAdminAuth({ token: r.token, refreshToken: r.refreshToken } as any)
      await this.bindWechat()
      wx.redirectTo({ url: REVIEW_PAGE })
    } catch (err) {
      wx.showToast({ title: '网络异常，请重试', icon: 'none' })
    } finally {
      this.setData({ submitting: false })
    }
  },

  async onWechatLogin() {
    if (this.data.wxSubmitting) return
    this.setData({ wxSubmitting: true })
    try {
      const code = await wxLoginCode()
      const r = await adminRequest('POST', '/api/admin/login/wechat', {
        code,
        companyId: getCompanyId(),
      })
      if (r.code === 0 && saveAdminAuth({ token: r.token, refreshToken: r.refreshToken } as any)) {
        wx.redirectTo({ url: REVIEW_PAGE })
        return
      }
      // code:3 是「这个微信还没绑管理员」，必须说清去哪绑，只说「登录失败」等于功能不可用
      wx.showModal({
        title: '免密登录不可用',
        content: r.code === 3 ? r.msg || '该微信还没有绑定管理员账号，请先用账号密码登录' : r.msg || '登录失败，请重试',
        showCancel: false,
      })
    } catch (err) {
      wx.showToast({ title: '微信授权失败，请重试', icon: 'none' })
    } finally {
      this.setData({ wxSubmitting: false })
    }
  },

  // bindWechat 绑定失败只提示、不抛错：登录本身已经成功，账号密码这条路始终可用。
  async bindWechat() {
    try {
      const code = await wxLoginCode()
      const r = await adminRequest('POST', '/api/admin/wechat/bind', { code })
      if (r.code !== 0) {
        wx.showToast({ title: r.msg || '微信绑定未完成，下次仍需输密码', icon: 'none', duration: 3000 })
      }
    } catch (err) {
      wx.showToast({ title: '微信绑定未完成，下次仍需输密码', icon: 'none', duration: 3000 })
    }
  },
})
