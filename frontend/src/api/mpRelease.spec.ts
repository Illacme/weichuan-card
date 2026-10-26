// mpRelease.ts 只做「URL + 信封」的接线，真正要盯的是三件事：
//   1. 路径与后端路由一字不差（写错段就是 404，而 404 在后台表现为「点了没反应」）；
//   2. 撤回必须带 confirm=true（后端硬要求，漏了字段整个功能不可用）；
//   3. 查状态回包要把 wxStatus / changed 摊平出来（界面提示语依赖它）。
// 请求层打桩（同 stats.spec.ts 的取舍：真实 request.ts 会牵出 router/store 一整条链）。
const mocks = vi.hoisted(() => ({
  httpGet: vi.fn(),
  httpPost: vi.fn(),
  toPaged: vi.fn(),
}))
vi.mock('./request', () => ({
  default: { get: mocks.httpGet, post: mocks.httpPost },
  toPaged: mocks.toPaged,
}))
vi.mock('axios', () => ({ default: { create: () => ({ get: vi.fn() }) } }))

import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  createMPRelease,
  getMPReleaseAuditStatus,
  getMPReleaseUploadReadiness,
  listMPReleases,
  mpReleaseTagType,
  releaseMPRelease,
  submitMPReleaseAudit,
  undoMPReleaseAudit,
  uploadMPRelease,
} from './mpRelease'

describe('listMPReleases', () => {
  beforeEach(() => mocks.httpGet.mockReset())

  it('按企业查列表并交给 toPaged 收敛分页', async () => {
    mocks.httpGet.mockResolvedValue({ data: [], total: 0, page: 1, size: 10 })
    mocks.toPaged.mockReturnValue({ list: [], total: 0, page: 1, size: 10 })
    await listMPReleases(2, { page: 2, size: 20 })
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/companies/2/releases', {
      params: { page: 2, size: 20 },
    })
    expect(mocks.toPaged).toHaveBeenCalled()
  })
})

describe('版本发布动作', () => {
  beforeEach(() => {
    mocks.httpPost.mockReset()
    mocks.httpPost.mockResolvedValue({ code: 0 })
  })

  it('登记版本：POST 到企业下的 releases', async () => {
    await createMPRelease(2, { userVersion: '1.2.0', userDesc: '节日海报' })
    expect(mocks.httpPost).toHaveBeenCalledWith('/admin/companies/2/releases', {
      userVersion: '1.2.0',
      userDesc: '节日海报',
    })
  })

  it('提交审核 / 发布：路径带记录 id', async () => {
    await submitMPReleaseAudit(7)
    expect(mocks.httpPost).toHaveBeenCalledWith('/admin/releases/7/submit-audit')
    await releaseMPRelease(7)
    expect(mocks.httpPost).toHaveBeenCalledWith('/admin/releases/7/release')
  })

  it('撤回审核：必须带 confirm=true（后端硬要求，漏了字段直接被拒）', async () => {
    await undoMPReleaseAudit(7)
    expect(mocks.httpPost).toHaveBeenCalledWith('/admin/releases/7/undo-audit', { confirm: true })
  })
})

// 上传是「后台自己把源码传到微信」那条路，与登记版本走不同的路径，
// 且要原样带上版本号 + 备注（备注会出现在微信审核备注里）。
describe('uploadMPRelease', () => {
  beforeEach(() => {
    mocks.httpPost.mockReset()
    mocks.httpPost.mockResolvedValue({ code: 0 })
  })

  it('POST 到企业下的 releases/upload，带版本号与备注', async () => {
    await uploadMPRelease(2, { userVersion: '2.0.0', userDesc: '补齐节日海报背景图' })
    expect(mocks.httpPost).toHaveBeenCalledWith('/admin/companies/2/releases/upload', {
      userVersion: '2.0.0',
      userDesc: '补齐节日海报背景图',
    })
  })
})

describe('getMPReleaseUploadReadiness', () => {
  beforeEach(() => mocks.httpGet.mockReset())

  it('把 data 摊平出来，调用方直接读 problems / keyConfigured', async () => {
    mocks.httpGet.mockResolvedValue({
      code: 0,
      data: { ready: false, problems: ['未找到 node'], keyConfigured: false },
    })
    const r = await getMPReleaseUploadReadiness(2)
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/companies/2/releases/upload-readiness')
    expect(r.ready).toBe(false)
    expect(r.problems).toEqual(['未找到 node'])
    expect(r.keyConfigured).toBe(false)
  })

  it('回包缺 data 时兜底成「没就绪 + 无缺失项」，不当崩', async () => {
    mocks.httpGet.mockResolvedValue({ code: 0 })
    const r = await getMPReleaseUploadReadiness(2)
    expect(r.ready).toBe(false)
    expect(r.problems).toEqual([])
  })
})

describe('getMPReleaseAuditStatus', () => {
  beforeEach(() => mocks.httpGet.mockReset())

  it('把回包摊平成 { item, wxStatus, changed }', async () => {
    mocks.httpGet.mockResolvedValue({
      code: 0,
      data: { id: 7, status: 'audit_passed', statusName: '审过待发布', wxStatus: 0, changed: true },
    })
    const r = await getMPReleaseAuditStatus(7)
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/releases/7/audit-status')
    expect(r.item.id).toBe(7)
    expect(r.wxStatus).toBe(0)
    expect(r.changed).toBe(true)
  })

  it('缺字段时兜底：wxStatus=-1、changed=false，不当崩', async () => {
    mocks.httpGet.mockResolvedValue({ code: 0, data: { id: 7 } })
    const r = await getMPReleaseAuditStatus(7)
    expect(r.wxStatus).toBe(-1)
    expect(r.changed).toBe(false)
  })
})

describe('mpReleaseTagType', () => {
  it('终态与异常态分色：扫一眼表格就知道哪版卡住了', () => {
    expect(mpReleaseTagType('auditing')).toBe('primary')
    expect(mpReleaseTagType('audit_passed')).toBe('warning')
    expect(mpReleaseTagType('released')).toBe('success')
    expect(mpReleaseTagType('rejected')).toBe('danger')
    expect(mpReleaseTagType('draft')).toBe('info')
    expect(mpReleaseTagType('withdrawn')).toBe('info')
  })
})
