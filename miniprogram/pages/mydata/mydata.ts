// 我的数据页（pages/mydata）：员工查阅/复制本人个人信息（PIPL 查阅复制权）。
//
// 为什么有这一页：PIPL 赋予个人信息主体「查阅、复制」其个人信息的权利。
// 此前员工只能在「我的」页看零散几项，没有一处能完整看到系统到底存了自己的哪些信息，
// 也没有一键复制的出口。本页把系统持有的本人 PII 一次性、带标签地列出来，
// 并提供「复制全部」满足复制权。
//
// 数据来自已有的 GET /api/employee/me（已按员工身份鉴权，返回不含 password_hash /
// token_version 等敏感内部字段）。这里只是把它**呈现**出来，不新增任何采集。
//
// 复制用 setClipboardData，属微信隐私接口：复制前先走 ensurePrivacy 取得授权。
import { request, assetUrl } from '../../utils/request'
import { ensurePrivacy } from '../../utils/privacy'

// 标签映射：模型字段 → 中文标签。顺序即展示顺序。
const LABELS: Record<string, string> = {
  staffId: '员工编号',
  name: '姓名',
  title: '职位',
  phone: '手机号',
  phone2: '备用手机号',
  landline: '座机',
  email: '邮箱',
  nickname: '微信昵称',
  openid: '微信 OpenID',
  companyId: '所属企业编号',
  status: '账号状态',
  createdAt: '注册时间',
}

Page({
  data: {
    // 加载态
    loading: true,
    // 微信隐私授权弹窗是否显示（由 ensurePrivacy 控制）
    privacyVisible: false,
    // 头像（后端相对路径，端上拼绝对）
    avatar: '',
    // 待展示的键值对（已过滤掉空值，保持页面干净）
    items: [] as Array<{ key: string; label: string; value: string }>,
  },
  onShow() {
    this.loadMe()
  },
  async loadMe() {
    this.setData({ loading: true })
    const r = await request('GET', '/api/employee/me')
    if (r.code === 0 && r.data) {
      const d = r.data as Record<string, any>
      const items: Array<{ key: string; label: string; value: string }> = []
      for (const key of Object.keys(LABELS)) {
        let v = d[key]
        if (v === undefined || v === null || v === '') continue
        if (key === 'status') v = v === 'enabled' ? '正常' : '已停用'
        items.push({ key, label: LABELS[key], value: String(v) })
      }
      this.setData({
        loading: false,
        avatar: d.avatar ? assetUrl(d.avatar) : '',
        items,
      })
    } else {
      // token 失效：回未登录态
      this.setData({ loading: false, items: [] })
    }
  },
  // 「复制全部」：把全部信息拼成多行文本写入剪贴板（PIPL 复制权）。
  // setClipboardData 是隐私接口，复制前先经 ensurePrivacy 取得授权——
  // 用户不同意就放弃复制，不报错但也不把信息外送。
  async copyAll() {
    const ok = await ensurePrivacy(this)
    if (!ok) {
      wx.showToast({ title: '需同意隐私政策才能复制', icon: 'none' })
      return
    }
    const lines = (this.data.items as any[]).map((it) => `${it.label}：${it.value}`)
    if (this.data.avatar) lines.push(`头像：${this.data.avatar}`)
    wx.setClipboardData({
      data: lines.join('\n'),
      success: () => wx.showToast({ title: '已复制全部信息', icon: 'success' }),
      fail: () => wx.showToast({ title: '复制失败', icon: 'none' }),
    })
  },
})
