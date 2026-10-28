// 员工「我的数据」页（pages/mydata）：PIPL 查阅复制权。
// 这页把系统持有的本人 PII 一次性带标签列出，并提供「复制全部」满足复制权。
// 测三件事：
//   1. onShow 拉 /api/employee/me，按 LABELS 渲染、过滤空值、头像拼绝对；
//   2. 复制前必须经 ensurePrivacy 授权，授权后把信息写入剪贴板；
//   3. 用户拒绝隐私授权时，既不复制也不把信息外送（只提示）。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  installPageCapture,
  capturedPage,
  makeInstance,
  installWx,
  installApp,
  flush,
} from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (_m: string, _u: string): Promise<any> => ({})),
  // ensurePrivacy 默认放行；个别用例改成拒绝以覆盖分支
  ensurePrivacy: vi.fn(async (): Promise<boolean> => true),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/privacy', () => ({
  ensurePrivacy: mocks.ensurePrivacy,
}))

let wxStub: any
let storage: Map<string, any>

const meData = {
  code: 0,
  // 所属企业由后端在根级下发（不在 data 内）
  companyName: '测试公司全称',
  companyShortName: '测试公司',
  data: {
    staffId: 42,
    name: '张伟',
    title: '销售经理',
    phone: '13800000000',
    phone2: '', // 空值：应被过滤
    landline: '', // 空值：应被过滤
    email: 'z@a.com',
    nickname: '小伟',
    openid: 'oXxx',
    companyId: 1,
    status: 'enabled',
    createdAt: '2026-01-01T00:00:00Z',
    avatar: '/uploads/a.png',
  },
}

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  installApp({ myStaffId: 42 })
  mocks.request.mockClear()
  mocks.ensurePrivacy.mockClear()
  mocks.ensurePrivacy.mockResolvedValue(true)
})

async function page() {
  await import('../pages/mydata/mydata')
  return makeInstance(capturedPage())
}

// 空值被过滤：phone2 / landline 不应出现在 items 里；
// 全部非空字段按 LABELS 顺序出现，status 的 enabled 应转成「正常」。
it('onShow 拉资料并渲染带标签键值对，过滤空值，头像拼绝对', async () => {
  mocks.request.mockResolvedValueOnce(meData)
  const p = await page()

  p.onShow()
  await flush()

  expect(mocks.request).toHaveBeenCalledWith('GET', '/api/employee/me')
  const items = p.data.items as any[]
  const labels = items.map((i) => i.label)
  expect(labels).toContain('姓名')
  expect(labels).toContain('手机号')
  expect(labels).toContain('邮箱')
  expect(labels).toContain('账号状态')
  // 空值字段不出现
  expect(labels).not.toContain('备用手机号')
  expect(labels).not.toContain('座机')
  // 具体值
  const byLabel = Object.fromEntries(items.map((i) => [i.label, i.value]))
  expect(byLabel['姓名']).toBe('张伟')
  expect(byLabel['账号状态']).toBe('正常')
  expect(byLabel['员工编号']).toBe('42')
  // 注册时间必须格式化掉 RFC3339 的 T/毫秒/偏移（原值 2026-01-01T00:00:00Z），否则页面又长又折行
  expect(byLabel['注册时间']).toBe('2026-01-01 00:00')
  expect(byLabel['注册时间']).not.toContain('T')
  // 内部租户外键（companyId）不是「关于我本人的信息」，不应出现在个人信息页
  expect(labels).not.toContain('所属企业编号')
  // 所属企业：根级下发、优先简称，且插在「职位」之后
  expect(byLabel['所属企业']).toBe('测试公司')
  expect(labels[labels.indexOf('职位') + 1]).toBe('所属企业')
  // 头像拼成绝对
  expect(p.data.avatar).toBe('https://cdn.test/uploads/a.png')
})

it('复制全部：授权后把信息写入剪贴板', async () => {
  mocks.request.mockResolvedValueOnce(meData)
  const p = await page()
  p.onShow()
  await flush()

  await p.copyAll()

  expect(mocks.ensurePrivacy).toHaveBeenCalled()
  expect(wxStub.setClipboardData).toHaveBeenCalled()
  const arg = wxStub.setClipboardData.mock.calls[0][0]
  expect(arg.data).toContain('姓名：张伟')
  expect(arg.data).toContain('手机号：13800000000')
  // 头像作为补充行也带进去
  expect(arg.data).toContain('头像：https://cdn.test/uploads/a.png')
  expect(arg.success).toBeTypeOf('function')
})

it('复制被拒：不复制、提示且不外送', async () => {
  mocks.request.mockResolvedValueOnce(meData)
  mocks.ensurePrivacy.mockResolvedValue(false)
  const p = await page()
  p.onShow()
  await flush()

  await p.copyAll()

  expect(wxStub.setClipboardData).not.toHaveBeenCalled()
  const calls = wxStub.showToast.mock.calls
  const last = calls[calls.length - 1][0].title
  expect(last).toContain('隐私')
})
