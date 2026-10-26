import { request, assetUrl } from '../../../utils/request'

// 员工「我的」页（tabBar）：未登录给登录 / 申请开通入口，已登录给名片与账号操作。
// 数据在 onShow 拉取而非 onLoad——从登录页 / 编辑页返回时需要刷新登录态与资料。
Page({
  data: {
    me: null as any,
    // 有待审名片修改时为 true：「编辑名片」单元格显示「审核中」
    pendingModify: false,
    pwdVisible: false,
    pwdSaving: false,
    pwd: { oldPwd: '', newPwd: '', confirmPwd: '' },
  },
  onShow() {
    this.loadMe()
  },
  async loadMe() {
    const token = wx.getStorageSync('empToken')
    if (!token) {
      this.setData({ me: null, pendingModify: false })
      return
    }
    const r = await request('GET', '/api/employee/me')
    if (r.code === 0 && r.data) {
      // 记到 globalData：「我的名片」switchTab 回名片页时按这个编号取数
      const app = getApp() as any
      app.globalData.myStaffId = r.data.staffId
      // 头像为后端相对路径，端上拼绝对（小程序 <image src> 不认相对）
      r.data.avatar = assetUrl(r.data.avatar)
      this.setData({ me: r.data, pendingModify: !!r.pendingModify })
    } else {
      // token 失效（拦截器已清 storage）：回到未登录态
      this.setData({ me: null, pendingModify: false })
    }
  },
  goLogin() {
    wx.navigateTo({ url: '/pages/employee/login/login' })
  },
  goApply() {
    wx.navigateTo({ url: '/pages/apply/open/open' })
  },
  // 隐私政策页：微信审核要求用户可在应用内查阅隐私保护指引（未登录访客同样可达）
  goPrivacy() {
    wx.navigateTo({ url: '/pages/privacy/privacy' })
  },
  // 返回名片首页：底部 tabBar 已移除，「我的」页由 navigateTo 进入，返回用 navigateBack；
  // 若栈内已无名片页（极端入口），兜底 reLaunch 回名片页
  goMyCard() {
    wx.navigateBack({
      delta: 1,
      fail: () => wx.reLaunch({ url: '/pages/index/index' }),
    })
  },
  // 生成海报（B9）：带本人编号进海报页，canvas 合成后存相册
  goPoster() {
    if (!this.data.me) return
    wx.navigateTo({ url: '/pages/poster/poster?staffId=' + this.data.me.staffId })
  },
  goEdit() {
    wx.navigateTo({ url: '/pages/employee/edit/edit' })
  },
  goStat() {
    wx.navigateTo({ url: '/pages/employee/stat/stat' })
  },
  // 我的数据：查阅/复制本人个人信息（PIPL 查阅复制权）
  goMydata() {
    wx.navigateTo({ url: '/pages/mydata/mydata' })
  },
  openPwd() {
    this.setData({ pwdVisible: true, pwd: { oldPwd: '', newPwd: '', confirmPwd: '' } })
  },
  closePwd() {
    this.setData({ pwdVisible: false })
  },
  onPwdInput(e: any) {
    const field = e.currentTarget.dataset.field
    const pwd = this.data.pwd as any
    pwd[field] = e.detail.value
    this.setData({ pwd })
  },
  async submitPwd() {
    const { oldPwd, newPwd, confirmPwd } = this.data.pwd
    if (!oldPwd || !newPwd) {
      wx.showToast({ title: '请填写原密码与新密码', icon: 'none' })
      return
    }
    if (newPwd.length < 6) {
      wx.showToast({ title: '新密码至少 6 位', icon: 'none' })
      return
    }
    if (newPwd !== confirmPwd) {
      wx.showToast({ title: '两次输入的新密码不一致', icon: 'none' })
      return
    }
    this.setData({ pwdSaving: true })
    try {
      const r = await request('PUT', '/api/employee/password', { old: oldPwd, new: newPwd })
      if (r.code === 0) {
        wx.showToast({ title: '已修改，请重新登录' })
        this.setData({ pwdVisible: false })
        // 改密后安全戳自增，旧令牌已失效：直接清本地态回未登录
        setTimeout(() => this.logout(true), 800)
      } else {
        wx.showToast({ title: r.msg || '修改失败', icon: 'none' })
      }
    } finally {
      this.setData({ pwdSaving: false })
    }
  },
  // 退出登录：清令牌 + globalData.myStaffId，回到未登录态
  logout(silent = false) {
    const app = getApp() as any
    app.globalData.myStaffId = 0
    wx.removeStorageSync('empToken')
    wx.removeStorageSync('empRefreshToken')
    this.setData({ me: null, pendingModify: false, pwdVisible: false })
    if (!silent) wx.showToast({ title: '已退出登录' })
  },
  // 注销账号（PIPL 删除权）：删除本人员工记录及关联 PII。
  //
  // 这是**不可恢复**的特权动作——一旦注销，名片与管理后台账号都没了。
  // 因此走两次强确认：第一次讲清后果，第二次用「确认注销」红字按钮再次确认，
  // 避免手滑误删（一次确认在微信里太容易直接点掉）。
  cancelAccount() {
    wx.showModal({
      title: '注销账号',
      content: '注销后你的电子名片、账号与全部个人信息将被永久删除，且不可恢复。确定要继续吗？',
      confirmText: '继续注销',
      confirmColor: '#e64340',
      success: (r1) => {
        if (!r1.confirm) return
        wx.showModal({
          title: '确认注销',
          content: '最后确认：此操作不可撤销，你的所有数据将被清除。',
          confirmText: '确认注销',
          confirmColor: '#e64340',
          success: async (r2) => {
            if (!r2.confirm) return
            const res = await request('POST', '/api/employee/me/cancel')
            if (res.code === 0) {
              // 注销成功：清本地登录态并回名片首页（未登录态）
              wx.removeStorageSync('empToken')
              wx.removeStorageSync('empRefreshToken')
              const app = getApp() as any
              if (app) app.globalData.myStaffId = 0
              wx.showToast({ title: '账号已注销', icon: 'success' })
              setTimeout(() => wx.reLaunch({ url: '/pages/index/index' }), 800)
            } else {
              wx.showToast({ title: res.msg || '注销失败', icon: 'none' })
            }
          },
        })
      },
    })
  },
})
