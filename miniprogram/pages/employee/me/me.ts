import { request, assetUrl } from '../../../utils/request'
import { onImgFail } from '../../../utils/thumb'
import { wxLoginCode } from '../../../utils/wechat'

// 员工「我的」页（tabBar）：未登录给登录 / 申请开通入口，已登录给名片与账号操作。
// 数据在 onShow 拉取而非 onLoad——从登录页 / 编辑页返回时需要刷新登录态与资料。
Page({
  data: {
    me: null as any,
    // 有待审名片修改时为 true：「编辑名片」单元格显示「审核中」
    pendingModify: false,
    // 是否已绑定微信：由 /api/employee/me 根级下发，「微信一键登录」单元格据此显示状态。
    // 不用 me.openid 是否为空来判断——那是后端的承载字段，产品语义不该由前端猜。
    wechatBound: false,
    // 绑定请求在途：防连点（连点会连发两个 code，后一个必然失效并报错）
    bindingWechat: false,
    pwdVisible: false,
    pwdSaving: false,
    pwd: { oldPwd: '', newPwd: '', confirmPwd: '' },
    // 头像加载失败的地址：WXML 用 `me.avatar && avatarFail !== me.avatar` 判断
    // （见 utils/thumb.ts onImgFail——记 URL 而不是布尔标记，换头像自动重试）
    avatarFail: '',
  },
  onShow() {
    this.loadMe()
  },
  async loadMe() {
    const token = wx.getStorageSync('empToken')
    if (!token) {
      this.setData({ me: null, pendingModify: false, wechatBound: false })
      return
    }
    const r = await request('GET', '/api/employee/me')
    if (r.code === 0 && r.data) {
      // 记到 globalData：「我的名片」switchTab 回名片页时按这个编号取数
      const app = getApp() as any
      app.globalData.myStaffId = r.data.staffId
      // 头像为后端相对路径，端上拼绝对（小程序 <image src> 不认相对）
      r.data.avatar = assetUrl(r.data.avatar)
      this.setData({
        me: r.data,
        pendingModify: !!r.pendingModify,
        wechatBound: !!r.wechatBound,
      })
    } else {
      // token 失效（拦截器已清 storage）：回到未登录态
      this.setData({ me: null, pendingModify: false, wechatBound: false })
    }
  },
  // 绑定微信：绑定后下次可直接「微信一键登录」，不用再记密码。
  //
  // 已绑定时不再重复绑定（重复绑定只是把同一个 openid 又写一遍，没有意义），
  // 改为说明「怎么换微信」——换微信的正确做法是用新微信登录 → 提示未绑定 →
  // 密码登录 → 在这里重新绑定，新 openid 覆盖旧的。所以不需要「解绑」按钮。
  async onBindWechat() {
    if (this.data.wechatBound) {
      wx.showModal({
        title: '已绑定微信',
        content: '当前微信已绑定本账号，下次登录可直接点「微信一键登录」。换微信时：用新微信进入本页重新绑定即可，旧微信会自动失效。',
        showCancel: false,
      })
      return
    }
    if (this.data.bindingWechat) return
    this.setData({ bindingWechat: true })
    try {
      const code = await wxLoginCode()
      if (!code) {
        wx.showToast({ title: '获取微信信息失败，请重试', icon: 'none' })
        return
      }
      const r = await request('POST', '/api/employee/wechat/bind', { code })
      if (r.code === 0) {
        this.setData({ wechatBound: true })
        wx.showToast({ title: '已绑定', icon: 'success' })
        return
      }
      // 用 modal 而不是 toast：绑定失败的原因（这个微信已绑了别的账号）需要完整说明，
      // toast 单行会被截断，用户只看到半句
      wx.showModal({ title: '绑定失败', content: r.msg || '请稍后重试', showCancel: false })
    } catch {
      wx.showToast({ title: '网络异常，请重试', icon: 'none' })
    } finally {
      this.setData({ bindingWechat: false })
    }
  },
  // 下拉刷新：重拉登录态与资料。loadMe 没有整页 loading 态（页内各块各自兜底），
  // 因此不需要 silent；无论成败都收掉顶部指示器。
  async onPullDownRefresh() {
    try {
      await this.loadMe()
    } catch {
      // loadMe 没有内部 try/catch，网络异常会抛到这里；兜住避免未捕获的
      // Promise rejection，错误提示沿用原 onShow 的静默口径
    } finally {
      wx.stopPullDownRefresh()
    }
  },
  // 头像加载失败：记下失败的地址，WXML 据此退到浅底占位（见 utils/thumb.ts）
  onImgFail,
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
  // 个人信息（原「我的数据」）：查阅/复制本人个人信息（PIPL 查阅复制权）。
  // 改名的原因：原「我的数据」与「数据统计」名字几乎一样，用户分不清；此页实为合规查阅页，非分析报表。
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
  // 「退出登录」按钮的 tap 入口：bindtap 会把事件对象作为首个实参传给 handler，
  // 若直接把按钮绑到 logout，silent 会被这个事件对象（truthy）顶成「静默」，
  // 成功提示就永远不弹。这里显式转成 logout(false)，把「用户主动退出」与「改密后的静默登出」区分干净。
  onLogoutTap() {
    this.logout(false)
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
