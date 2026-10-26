import { request, apiBaseUrl, assetUrl } from '../../../utils/request'
import { getOpenid, getWechatNickname } from '../../../utils/wechat'
import { ensurePrivacy } from '../../../utils/privacy'

// 访客自助提交开通申请（POST /api/apply/open）。
// 头像：默认读取用户微信头像（按钮 open-type=chooseAvatar 会直接调起微信头像选择器，
// 默认展示用户当前微信头像），同时支持从相册/拍照上传——两者都拿到本地临时文件后，
// 经专用公开端点 /api/apply/open/avatar 上传拿回永久 URL 再随表单提交。
// 多租户：请求层按 utils/tenant.ts 自动带上 companyId（独立版锁定值 / 平台模式粘滞值），
// 申请即归属该企业，由该企业的管理员审核。
//
// 隐私合规（本页是 UGC 采集最重的页面，2026-10-26 补）：
//   1. 表单级明示同意（PIPL「告知—同意」）：采集姓名/手机/邮箱等属个人信息，
//      必须有用户主动勾选的同意项，未勾选不得提交（见 form.agreed 与 onSubmit）。
//   2. 平台级隐私授权：chooseMedia / getUserProfile 是微信隐私接口，
//      调用前须取得用户对隐私政策的同意（见 utils/privacy.ts 的 ensurePrivacy）。
Page({
  data: {
    form: { name: '', phone: '', phone2: '', title: '', landline: '', email: '', avatar: '', nickname: '' },
    // 备用手机号输入框是否展开：默认收起（多数人只有一个号），
    // 由「手机」行右侧「+ 备用手机号」按钮切换；编辑/回填场景见 loadMe
    showPhone2: false,
    // 申请目标企业名（GET /api/company）：让访客知道在申请加入哪家企业
    companyName: '',
    saving: false,
    submitted: false,
    // 隐私政策同意勾选状态：默认未勾选，提交前必须勾选。
    // 单独放顶层而不是塞进 form —— 它不是要提交给后端的字段，
    // 塞进去会被 request 一并带上（后端模型里没有这个字段，属于无意义噪声）。
    agreed: false,
    // 微信隐私授权弹窗是否显示（由 ensurePrivacy 控制）
    privacyVisible: false,
  },
  onLoad() {
    this.loadCompany()
  },
  // 「+ 备用手机号」/「×」：展开或收起第二个手机号输入框。
  // 收起时顺手清空已填内容——否则用户会以为「没显示就是没提交」，留下暗数据
  togglePhone2() {
    const show = !this.data.showPhone2
    const form = this.data.form as any
    if (!show) form.phone2 = ''
    this.setData({ showPhone2: show, form })
  },
  // 隐私政策勾选：点了就切换。勾选框本身用 label 包住 checkbox，
  // 点文字也能勾上（小程序里 checkbox 必须放进 checkbox-group 才能这样）。
  toggleAgree() {
    this.setData({ agreed: !this.data.agreed })
  },
  // 「查看隐私政策」：跳到应用内的隐私政策页
  goPrivacy() {
    wx.navigateTo({ url: '/pages/privacy/privacy' })
  },
  // 拉取申请目标企业名；失败（网络/企业停用）时隐藏该行，不阻断申请
  loadCompany() {
    request('GET', '/api/company')
      .then((r: any) => {
        if (r && r.code === 0 && r.data) {
          this.setData({ companyName: r.data.shortName || r.data.name || '' })
        }
      })
      .catch(() => {})
  },
  onInput(e: any) {
    const field = e.currentTarget.dataset.field
    const form = this.data.form as any
    form[field] = e.detail.value
    this.setData({ form })
  },
  // 「使用微信头像」：open-type=chooseAvatar 回调拿到本地临时文件，直接上传
  onChooseAvatar(e: any) {
    const url = e.detail && e.detail.avatarUrl
    if (url) this.uploadAvatar(url)
  },
  // 「上传头像」：从相册/拍照选图后上传。
  // chooseMedia 是微信隐私接口：调用前须取得用户对隐私政策的同意，
  // 未同意则不打开选图器（用户不同意就不能采集照片，直接放弃本次操作）。
  async chooseImage() {
    if (!(await ensurePrivacy(this))) {
      wx.showToast({ title: '需同意隐私政策才能使用', icon: 'none' })
      return
    }
    const that = this
    wx.chooseMedia({
      count: 1,
      mediaType: ['image'],
      success(res: any) {
        const filePath = res.tempFiles[0].tempFilePath
        that.uploadAvatar(filePath)
      },
    })
  },
  // 头像上传：调公开端点（无需登录），返回永久 URL 写回表单。
  //
  // 顺带把 openid 一起带上（wx.login 静默换取，不需用户授权）：后端据此把图
  // 送微信内容安全检测——这张图上传成功即返回永久 URL，是立刻公开可访问的，
  // 不送检就等于「违规图在人工审核之前已经挂在网上」。
  uploadAvatar(filePath: string) {
    // wx.uploadFile 的 success 是普通函数回调，this 不是页面实例——
    // 这里必须先存好引用（漏了会让「头像已设置」永远不生效，且抛错无声）
    const that = this
    wx.showLoading({ title: '上传中' })
    // 先取 openid 再发：拿到 openid 才能送检。取不到也要继续上传
    // （老客户端 / 网络异常），后端会退回人工审核兜底。
    getOpenid()
      .catch(() => '')
      .then((openid: string) => {
        wx.uploadFile({
          url: apiBaseUrl() + '/api/apply/open/avatar',
          filePath,
          name: 'file',
          formData: openid ? { openid } : {},
          success(r: any) {
            wx.hideLoading()
            let body: any = {}
            try {
              body = JSON.parse(r.data)
            } catch {}
            if (body.code === 0 && body.data && body.data.urls && body.data.urls.length) {
              const form = that.data.form as any
              form.avatar = assetUrl(body.data.urls[0])
              that.setData({ form })
              wx.showToast({ title: '头像已设置' })
            } else {
              wx.showToast({ title: body.msg || '上传失败', icon: 'none' })
            }
          },
          fail() {
            wx.hideLoading()
            wx.showToast({ title: '上传失败', icon: 'none' })
          },
        })
      })
  },
  // 「授权获取」微信昵称：getUserProfile 弹窗（需在点击手势内调用），成功后展示在「微信昵称」行，
  // 提交时随表单带上。用户可跳过——昵称仅作后台核实用途，不填也能提交。
  //
  // getUserProfile 是微信隐私接口，调用前须取得隐私政策同意。
  async authorizeNickname() {
    if (!(await ensurePrivacy(this))) {
      wx.showToast({ title: '需同意隐私政策才能使用', icon: 'none' })
      return
    }
    wx.showLoading({ title: '获取中' })
    getWechatNickname().then((nickname) => {
      wx.hideLoading()
      if (nickname) {
        const form = this.data.form as any
        form.nickname = nickname
        this.setData({ form })
      } else {
        wx.showToast({ title: '未获取到昵称（可跳过）', icon: 'none' })
      }
    })
  },
  async onSubmit() {
    const { name, phone } = this.data.form
    if (!name || !phone) {
      wx.showToast({ title: '请填写姓名与手机', icon: 'none' })
      return
    }
    // 隐私政策明示同意（表单级，PIPL「告知—同意」）：本表单采集姓名/手机/邮箱/
    // 座机等个人信息，属受告知—同意约束的收集行为。未勾选不得提交——
    // 这条不能靠后端拦：后端拿不到「用户是否真的看过并勾了」这个事实。
    if (!this.data.agreed) {
      wx.showToast({ title: '请先阅读并同意隐私政策', icon: 'none' })
      return
    }
    this.setData({ saving: true })
    // openid 静默采集（wx.login 换取，无需弹窗）；昵称由用户点「授权获取」显式授权后已在 form.nickname，
    // 此处只补 openid，不再触发昵称弹窗（避免提交时盲弹）。
    const openid = await getOpenid()
    try {
      const r = await request('POST', '/api/apply/open', { ...this.data.form, openid })
      if (r.code === 0) {
        this.setData({ submitted: true })
      } else {
        wx.showToast({ title: r.msg || '提交失败', icon: 'none' })
      }
    } finally {
      this.setData({ saving: false })
    }
  },
  backMe() {
    wx.navigateBack({
      delta: 1,
      fail: () => wx.reLaunch({ url: '/pages/employee/me/me' }),
    })
  },
})
