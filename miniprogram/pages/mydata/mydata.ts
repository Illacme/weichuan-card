// 个人信息页（原「我的数据」，pages/mydata）：员工查阅/复制本人个人信息（PIPL 查阅复制权）。
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
// 刻意**不列 companyId**：它是内部租户外键，员工看到「所属企业编号 1」既看不懂也无意义
// （PIPL 查阅的是「关于我本人的信息」，企业外键不是）。要显示企业名需后端在 /api/employee/me
// 额外下发 companyName，属另一件事。
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
  status: '账号状态',
  createdAt: '注册时间',
}

// ISO/RFC3339 → 「YYYY-MM-DD HH:mm」。后端 CreatedAt 是 Go time.Time，JSON 出来是
// 带纳秒的 `2026-10-27T21:19:21.419153+08:00`，直接展示既难读又会折行。
// 取字符串字面量而非换算时区：偏移量就是服务端本地时（TZ=Asia/Shanghai），
// 这样不同设备时区下看到的时间一致，不会有人显示 21:19、有人显示 13:19。
function fmtTime(v: string): string {
  const m = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2})/.exec(v)
  return m ? m[1] + ' ' + m[2] : v
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
        // 注册时间：格式化掉 RFC3339 的 T / 毫秒 / 偏移，变成可读的「2026-10-27 21:19」。
        if (key === 'createdAt') v = fmtTime(String(v))
        items.push({ key, label: LABELS[key], value: String(v) })
      }
      // 所属企业：后端在**根级**下发（不在 data 里，故不进上面的 LABELS 循环）。
      // 优先简称（更紧凑，正式全称太长会在窄屏折行），无简称回落全称；
      // 两者都空则不展示该行（平台模式下企业可能没配简称）。
      // 注意：这里不能举例写死某一家的名字——brand.spec 的源码守门用例
      // 禁止演示租户的名字出现在小程序源码里（含注释）。
      const root = r as any
      const company = String(root.companyShortName || root.companyName || '').trim()
      if (company) {
        // 插在「职位」之后：编号 / 姓名 / 职位 → 所属企业 → 联系方式，符合认知顺序
        const at = items.findIndex((it) => it.key === 'title')
        items.splice(at >= 0 ? at + 1 : items.length, 0, {
          key: 'company',
          label: '所属企业',
          value: company,
        })
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
      fail: (err: any) => {
        // 失败原因对排查很关键：隐私未授权是 "require privacy authorize"，
        // MP 后台未声明「剪切板」隐私类型则是 errno 112（接口被平台禁用）。
        // 打到控制台供开发者定位，用户侧只给一句通用提示。
        console.warn('[mydata] setClipboardData 失败', err)
        wx.showToast({ title: '复制失败', icon: 'none' })
      },
    })
  },
})
