// 企业管理员审核页（小程序端）：开通申请 / 名片修改申请，就地通过或驳回。
//
// 身份：adminToken（见 utils/adminRequest.ts）。进页面先看有没有令牌，没有就试一次
// 微信免密登录（openid 已绑定）；都不行才跳登录页——多数管理员第二次进来是零输入的。
//
// 与 PC 后台的差异（刻意为之）：
//   1) 只做「待处理 / 全部」两档筛选，不做关键字搜索——手机上打字慢，审核的动作是
//      「按时间顺序清队列」，而不是检索。
//   2) 通过后如果是开通申请，后端会回员工初始密码：必须弹出来给人看清（PC 也是这么做的），
//      否则审核人根本没办法把账号交给新员工。
//   3) 驳回理由用页内输入框而不是 wx.showModal 的 editable（后者要基础库 2.17.1，
//      低版本会静默变普通弹窗，理由根本填不进去）。
import { adminRequest, saveAdminAuth, hasAdminToken, clearAdminAuth } from '../../../utils/adminRequest'
import { wxLoginCode } from '../../../utils/wechat'
import { getCompanyId } from '../../../utils/tenant'

const LOGIN_PAGE = '/pages/admin/login/login'

const STATUS_TEXT: Record<string, string> = {
  pending: '待审核',
  approved: '已通过',
  rejected: '已驳回',
}

const MODIFY_LABELS: Record<string, string> = {
  name: '姓名',
  phone: '手机',
  phone2: '手机2',
  email: '邮箱',
  title: '职位',
  landline: '座机',
  avatar: '头像',
}

function pad2(n: number): string {
  return n < 10 ? '0' + n : '' + n
}

// fmtTime 把后端 RFC3339 时间裁成「YYYY-MM-DD HH:mm」。
// 解析失败原样返回：宁可显示原始串，也不要显示 Invalid Date。
export function fmtTime(raw?: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (isNaN(d.getTime())) return raw
  return (
    `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ` +
    `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
  )
}

// decorateOpen 把开通申请压成卡片模型：首行是谁，第二行是联系方式。
export function decorateOpen(row: any): any {
  const lines: string[] = []
  if (row.phone) lines.push('手机：' + row.phone)
  if (row.email) lines.push('邮箱：' + row.email)
  if (row.title) lines.push('职位：' + row.title)
  if (row.nickname) lines.push('昵称：' + row.nickname)
  return {
    id: row.id,
    time: fmtTime(row.createdAt),
    name: row.name || '（未填姓名）',
    lines,
    status: row.status,
    statusText: STATUS_TEXT[row.status] || row.status,
    pending: row.status === 'pending',
    rejectReason: row.rejectReason || '',
  }
}

// decorateModify 把名片修改申请压成卡片模型。
// snapshot 是后端存的 JSON 字符串（只含被改动字段）；解析失败时退成一句提示，
// 绝不让整个列表因为一条脏数据崩掉。
export function decorateModify(row: any): any {
  let lines: string[] = []
  try {
    const patch = JSON.parse(row.snapshot || '{}')
    lines = Object.keys(patch).map((k) => {
      const label = MODIFY_LABELS[k] || k
      // 头像是对象键，直接摊开一串 uploads/xxx 对审核没有意义
      if (k === 'avatar') return '头像：已更换'
      return `${label}：${patch[k] === '' ? '（清空）' : patch[k]}`
    })
  } catch (err) {
    lines = ['变更内容无法解析，请在电脑端查看']
  }
  return {
    id: row.id,
    time: fmtTime(row.createdAt),
    name: '员工编号 ' + row.staffId,
    lines,
    status: row.status,
    statusText: STATUS_TEXT[row.status] || row.status,
    pending: row.status === 'pending',
    rejectReason: row.rejectReason || '',
  }
}

Page({
  data: {
    tab: 'open', // open | modify
    status: 'pending', // pending | all
    list: [] as any[],
    page: 1,
    size: 20,
    total: 0,
    hasMore: false,
    loading: false,
    loadingMore: false,
    error: '',
    moreError: '',
    adminName: '',
    acting: 0, // 正在提交操作的行 id，用于禁用该行按钮
    rejectVisible: false,
    rejectReason: '',
    rejectId: 0,
  },

  onShow() {
    this.ensureAuth()
  },

  // ensureAuth 有令牌就直接用；没有就试一次微信免密登录；都不行才去登录页。
  async ensureAuth() {
    if (hasAdminToken()) {
      this.load(1, true)
      return
    }
    let ok = false
    try {
      const code = await wxLoginCode()
      const r = await adminRequest('POST', '/api/admin/login/wechat', {
        code,
        companyId: getCompanyId(),
      })
      ok = r.code === 0 && saveAdminAuth({ token: r.token, refreshToken: r.refreshToken } as any)
      if (ok) this.setData({ adminName: (r as any).username || '' })
    } catch (err) {
      ok = false
    }
    if (!ok) {
      wx.redirectTo({ url: LOGIN_PAGE })
      return
    }
    this.load(1, true)
  },

  // load 拉一页数据。reset=true 时从第一页重来（切页签 / 切筛选 / 下拉刷新 / 操作后）。
  async load(page: number, reset: boolean) {
    if (reset) {
      this.setData({ loading: true, error: '', moreError: '' })
    } else {
      this.setData({ loadingMore: true, moreError: '' })
    }
    const query: any = { page, size: this.data.size }
    if (this.data.status !== 'all') query.status = this.data.status
    const kind = this.data.tab
    try {
      const r = await adminRequest('GET', '/api/admin/apply/' + kind, query)
      if (r.code === 401) {
        clearAdminAuth()
        wx.redirectTo({ url: LOGIN_PAGE })
        return
      }
      if (r.code !== 0) {
        // 列表失败要落成可见的错误态（带重试），不能停在「空列表」——
        // 空列表会被管理员误读成「没有待审」，直接把工作漏掉。
        const msg = r.msg || '加载失败'
        this.setData(reset ? { error: msg, list: [], total: 0 } : { moreError: msg })
        return
      }
      const rows = Array.isArray(r.data) ? (r.data as any[]) : []
      const view = rows.map(kind === 'open' ? decorateOpen : decorateModify)
      const total = Number((r as any).total) || 0
      const merged = reset ? view : this.data.list.concat(view)
      this.setData({
        list: merged,
        total,
        page,
        hasMore: merged.length < total,
      })
    } catch (err) {
      this.setData(reset ? { error: '网络异常，请下拉刷新重试' } : { moreError: '加载更多失败，点击重试' })
    } finally {
      this.setData({ loading: false, loadingMore: false })
    }
  },

  onTabChange(e: any) {
    const tab = e.currentTarget.dataset.tab
    if (tab === this.data.tab) return
    this.setData({ tab, list: [], total: 0, error: '' })
    this.load(1, true)
  },

  onStatusChange(e: any) {
    const status = e.currentTarget.dataset.status
    if (status === this.data.status) return
    this.setData({ status, list: [], total: 0, error: '' })
    this.load(1, true)
  },

  onRetry() {
    this.load(1, true)
  },

  onRetryMore() {
    this.load(this.data.page + 1, false)
  },

  onPullDownRefresh() {
    this.load(1, true).finally(() => wx.stopPullDownRefresh())
  },

  onReachBottom() {
    if (!this.data.hasMore || this.data.loadingMore || this.data.loading) return
    this.load(this.data.page + 1, false)
  },

  // onApprove 通过。开通申请通过后后端会建员工账号并回初始密码，必须展示出来。
  async onApprove(e: any) {
    const id = Number(e.currentTarget.dataset.id)
    if (!id || this.data.acting) return
    const kind = this.data.tab
    const tip = kind === 'open' ? '通过后将自动创建员工账号' : '通过后新资料立即生效'
    const confirmed = await new Promise<boolean>((resolve) => {
      wx.showModal({
        title: '确认通过',
        content: tip,
        success: (res: any) => resolve(!!res.confirm),
        fail: () => resolve(false),
      })
    })
    if (!confirmed) return
    this.setData({ acting: id })
    try {
      const r = await adminRequest('POST', `/api/admin/apply/${kind}/${id}/approve`)
      if (r.code !== 0) {
        wx.showToast({ title: r.msg || '操作失败', icon: 'none' })
        return
      }
      const staffId = (r as any).staffId
      const pwd = (r as any).initialPassword
      if (kind === 'open' && staffId) {
        wx.showModal({
          title: '已通过，账号已创建',
          content: `员工编号 ${staffId}，初始密码 ${pwd || '（未生成）'}。请转告本人首次登录后修改密码。`,
          showCancel: false,
        })
      } else {
        wx.showToast({ title: '已通过', icon: 'success' })
      }
      this.load(1, true)
    } catch (err) {
      wx.showToast({ title: '操作失败，请重试', icon: 'none' })
    } finally {
      this.setData({ acting: 0 })
    }
  },

  onRejectTap(e: any) {
    const id = Number(e.currentTarget.dataset.id)
    if (!id) return
    this.setData({ rejectVisible: true, rejectId: id, rejectReason: '' })
  },

  onRejectInput(e: any) {
    this.setData({ rejectReason: e.detail.value })
  },

  onRejectCancel() {
    this.setData({ rejectVisible: false, rejectId: 0, rejectReason: '' })
  },

  async onRejectConfirm() {
    const id = this.data.rejectId
    if (!id || this.data.acting) return
    const kind = this.data.tab
    this.setData({ acting: id })
    try {
      const r = await adminRequest('POST', `/api/admin/apply/${kind}/${id}/reject`, {
        reason: (this.data.rejectReason || '').trim(),
      })
      if (r.code !== 0) {
        wx.showToast({ title: r.msg || '驳回失败', icon: 'none' })
        return
      }
      this.setData({ rejectVisible: false, rejectId: 0, rejectReason: '' })
      wx.showToast({ title: '已驳回', icon: 'success' })
      this.load(1, true)
    } catch (err) {
      wx.showToast({ title: '驳回失败，请重试', icon: 'none' })
    } finally {
      this.setData({ acting: 0 })
    }
  },

  onLogout() {
    wx.showModal({
      title: '退出管理员登录',
      content: '退出后需要重新输入后台账号密码',
      success: (res: any) => {
        if (!res.confirm) return
        clearAdminAuth()
        wx.redirectTo({ url: LOGIN_PAGE })
      },
    })
  },
})
