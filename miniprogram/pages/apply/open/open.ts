import { request, apiBaseUrl, assetUrl } from '../../../utils/request'
import { getOpenid } from '../../../utils/wechat'
import { ensurePrivacy, needConsent } from '../../../utils/privacy'

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
//   2. 平台级隐私授权：chooseMedia 是微信隐私接口，
//      调用前须取得用户对隐私政策的同意（见 utils/privacy.ts 的 ensurePrivacy）。
//      头像的 chooseAvatar 属于官方列明的「无需主动调用也会自己弹授权窗」的
//      例外（另两个例外是剪切板读/写），所以代码里不拦；
//      但**昵称的 type="nickname" 恰恰相反**——见 onNicknameFocus 的说明，
//      它必须自己先拿到授权，否则静默降级成普通输入框。
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
    // 昵称输入框的聚焦态：需要「授权后重新聚焦」时才由代码驱动，见 onNicknameFocus
    nicknameFocus: false,
    // 昵称是否已处理过授权：只处理首次聚焦，避免每次点进来都重弹一次窗
    nicknameChecked: false,
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
  // 昵称聚焦：先补隐私授权，再重新聚焦。
  //
  // 为什么必须有这一段：<input type="nickname"> 是**唯一不会自己弹授权**的隐私
  // 组件。用户还没同意隐私政策时，它聚焦后既不触发 onNeedPrivacyAuthorization、
  // 也不弹官方授权窗，而是**静默降级成 input type="text"**——键盘上方不会
  // 出现「微信昵称」，页面不报错、不失败，用户只看到「填了没反应」。
  // 真机控制台会留一句 errno 104（degrade as input component when privacy
  // permission is unauthorized），界面上完全看不出来。
  //
  // 微信官方给的答案是二选一：等别的隐私接口（如 chooseAvatar）先拿到授权，
  // 或自己写授权弹窗。我们本来就有自绘弹窗（privacy-consent + open-type=
  // agreePrivacyAuthorization），所以走后者——聚焦时先补一次授权，
  // 同意后重新聚焦，让组件在授权态下重新初始化。
  async onNicknameFocus() {
    if (this.data.nicknameChecked) return
    // 已经同意过：直接放行，不做下面那次「失焦再聚焦」（否则会白闪一次键盘）
    if (!(await needConsent())) {
      this.setData({ nicknameChecked: true })
      return
    }
    if (!(await ensurePrivacy(this))) {
      wx.showToast({ title: '需同意隐私政策才能填入微信昵称', icon: 'none' })
      return
    }
    this.setData({ nicknameChecked: true, nicknameFocus: false })
    // 隔开一轮再置回 true：同一轮里 false→true 不会真的触发失焦，
    // 组件也就不会重新走一遍昵称初始化
    setTimeout(() => this.setData({ nicknameFocus: true }), 30)
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
    // openid 静默采集（wx.login 换取，无需弹窗）；昵称已由表单里的原生输入框
    // 填写/自动填充，提交时不再触发任何授权弹窗（避免提交瞬间盲弹）。
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
