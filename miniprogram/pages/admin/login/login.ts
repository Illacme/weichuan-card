// 企业管理员登录（小程序端）。
//
// 账号密码直接复用后台 /api/admin/login/mobile：同一套 bcrypt 校验、同一个登录限流器、
// 同一套企业停用闸门与审计。与 PC 的唯一区别是令牌进响应体（手机没有 Cookie 容器）。
//
// 登录成功后顺手做一次微信绑定，之后打开审核页就能免密进入。
// 绑定失败**不阻断登录**：可能是企业没配小程序凭据、或该微信号已绑了别的管理员——
// 这两种情况都该让管理员先把活干了，绑定问题单独提示。
import {
  adminRequest,
  saveAdminAuth,
  hasAdminToken,
  markAdminWxBound,
} from '../../../utils/adminRequest'
import { wxLoginCode } from '../../../utils/wechat'
import { getCompanyId } from '../../../utils/tenant'
import { REVIEW_PAGE, canTrySilentLogin, trySilentAdminLogin } from '../../../utils/adminEntry'

// 把后端信封翻成人话。限流是「共享出口 IP 的登录桶被占满了」，不是「这个微信没绑管理员」，
// 更不是「账号密码错了」。旧版把所有失败都塞进标题「免密登录不可用」，
// 用户明明一下都没登，却看到「登录尝试过于频繁」，反馈说这提示页不合理。
export function loginFailure(code: number, msg?: string): { title: string; content: string } {
  if (code === 429) {
    return {
      title: '请稍后再试',
      content: '登录请求太频繁了，请稍等一分钟再试；也可以直接用账号密码登录。',
    }
  }
  if (code === 3) {
    return {
      title: '免密登录不可用',
      content: msg || '该微信还没有绑定管理员账号，请先用账号密码登录',
    }
  }
  return { title: '登录失败', content: msg || '登录失败，请重试' }
}

Page({
  data: {
    username: '',
    password: '',
    submitting: false,
    wxSubmitting: false,
    // 静默校验在途：期间两个按钮都不该响应，避免与 onLoad 的自动尝试撞成两次登录请求
    checking: false,
    tip: '首次登录会自动把当前微信绑定到该账号，下次可直接免密进入',
  },

  onLoad() {
    // 已有管理员令牌就直接进审核页：从「我的」点进来的人不该再输一次密码
    if (hasAdminToken()) {
      wx.redirectTo({ url: REVIEW_PAGE })
      return
    }
    void this.trySilentLogin()
  },

  // 从审核页 401（令牌过期）或直接落到本页时，给「这台设备绑过管理员微信」的人
  // 一次零输入机会。与「我的」页入口共用同一个冷却窗口，绝不会连发两次登录请求；
  // 失败一律静默——没主动发起的登录不该弹窗打断用户。
  async trySilentLogin() {
    if (!canTrySilentLogin()) return
    this.setData({ checking: true })
    try {
      if (await trySilentAdminLogin()) {
        wx.redirectTo({ url: REVIEW_PAGE })
      }
    } finally {
      this.setData({ checking: false })
    }
  },

  onInput(e: any) {
    const field = e.currentTarget.dataset.field
    this.setData({ [field]: e.detail.value })
  },

  async onSubmit() {
    if (this.data.submitting || this.data.checking) return
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
        if (r.code === 429) {
          const f = loginFailure(r.code, r.msg)
          wx.showModal({ title: f.title, content: f.content, showCancel: false })
          return
        }
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
    if (this.data.wxSubmitting || this.data.checking) return
    this.setData({ wxSubmitting: true })
    try {
      const code = await wxLoginCode()
      const r = await adminRequest('POST', '/api/admin/login/wechat', {
        code,
        companyId: getCompanyId(),
      })
      if (r.code === 0 && saveAdminAuth({ token: r.token, refreshToken: r.refreshToken } as any)) {
        // 免密成功 = 这台设备的微信确实绑着管理员，之后可走静默免密
        markAdminWxBound()
        wx.redirectTo({ url: REVIEW_PAGE })
        return
      }
      const f = loginFailure(r.code, r.msg)
      wx.showModal({ title: f.title, content: f.content, showCancel: false })
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
        return
      }
      markAdminWxBound()
    } catch (err) {
      wx.showToast({ title: '微信绑定未完成，下次仍需输密码', icon: 'none', duration: 3000 })
    }
  },
})
