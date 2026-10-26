import { request, apiBaseUrl, assetUrl } from '../../../utils/request'
import { ensurePrivacy } from '../../../utils/privacy'

Page({
  data: {
    form: { avatar: '', name: '', title: '', phone: '', phone2: '', landline: '', email: '' },
    // 备用手机号输入框是否展开：回填到已有 phone2 时自动展开（见 loadMe），
    // 之后由「手机」行右侧「+ 备用手机号」按钮切换
    showPhone2: false,
    loading: true,
    // 有待审修改申请时为 true：页面展示「审核中」标签（需求 B9）。
    // 审核期对外仍显示旧信息——后端仅在超管 approve 时写回。
    pendingModify: false,
    // 微信隐私授权弹窗是否显示（由 ensurePrivacy 控制）
    privacyVisible: false,
  },
  onLoad() {
    this.loadMe()
  },
  async loadMe() {
    const r = await request('GET', '/api/employee/me')
    if (r.code === 401) {
      wx.redirectTo({ url: '/pages/employee/login/login' })
      return
    }
    if (r.data) {
      // 头像为后端相对路径，端上拼绝对——<image src> 不认相对路径，
      // 直接绑会是裂图（「我的」页一直是这么处理的，本页曾漏）
      const data = r.data as any
      data.avatar = assetUrl(data.avatar)
      this.setData({
        form: data,
        loading: false,
        pendingModify: !!r.pendingModify,
        // 已有备用号就展开，避免「存了值却看不到输入框」的困惑
        showPhone2: !!data.phone2,
      })
    } else {
      this.setData({ loading: false })
    }
  },
  // 「+ 备用手机号」/「×」：展开或收起第二个手机号输入框。
  // 收起时顺手清空已填内容——否则用户会以为「没显示就是没提交」，留下暗数据
  togglePhone2() {
    const show = !this.data.showPhone2
    const form = this.data.form as any
    if (!show) form.phone2 = ''
    this.setData({ showPhone2: show, form })
  },
  onInput(e: any) {
    const field = e.currentTarget.dataset.field
    const form = this.data.form as any
    form[field] = e.detail.value
    this.setData({ form })
  },
  // 选图并上传头像（沿用 PC 端 UploadField 思路：调后端上传接口拿回 URL）
  //
  // chooseMedia 是微信隐私接口：调用前须取得用户对隐私政策的同意，
  // 未同意则不打开选图器（用户不同意就不能采集照片，直接放弃本次操作）。
  async chooseAvatar() {
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
        wx.showLoading({ title: '上传中' })
        wx.uploadFile({
          url: apiBaseUrl() + '/api/employee/upload',
          filePath,
          name: 'file',
          header: { Authorization: 'Bearer ' + (wx.getStorageSync('empToken') || '') },
          success(r: any) {
            wx.hideLoading()
            let body: any = {}
            try {
              body = JSON.parse(r.data)
            } catch {
              // 网关 502 / 反代返回 HTML 时响应体不是 JSON：先兜住再提示。
              // 让 JSON.parse 的异常冒泡出 success 回调，用户只会看到卡住的
              // 「上传中」——不知道发生了什么，也不知道能不能重试
              wx.showToast({ title: '上传失败', icon: 'none' })
              return
            }
            if (body.code === 0 && body.data && body.data.urls && body.data.urls.length) {
              const form = that.data.form as any
              form.avatar = assetUrl(body.data.urls[0])
              that.setData({ form })
              wx.showToast({ title: '上传成功' })
            } else {
              wx.showToast({ title: body.msg || '上传失败', icon: 'none' })
            }
          },
          fail() {
            wx.hideLoading()
            wx.showToast({ title: '上传失败', icon: 'none' })
          },
        })
      },
    })
  },
  async onSubmit() {
    const r = await request('PUT', '/api/employee/me', this.data.form)
    if (r.code === 0) {
      // 本地立即置位：后端此时只落待审申请，再次进入本页时由 loadMe 回读同样为 true
      this.setData({ pendingModify: true })
      wx.showToast({ title: '已提交，等待审核' })
    } else {
      wx.showToast({ title: r.msg || '提交失败', icon: 'none' })
    }
  },
})
