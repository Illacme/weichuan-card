// stats.ts 会 import ./request（HTTP 客户端 + 401 跳转），而它牵出 router / store /
// element-plus 一整条依赖链——本机内存跑 vitest 会直接被 kill（exit 137），
// 故把请求层打桩（同 audit.spec.ts 的取舍）；axios 只用到 create/get，一并桩掉。
const mocks = vi.hoisted(() => ({
  httpGet: vi.fn(),
  rawGet: vi.fn(),
}))
vi.mock('./request', () => ({ default: { get: mocks.httpGet } }))
vi.mock('axios', () => ({ default: { create: () => ({ get: mocks.rawGet }) } }))

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { adminStats, adminStatsDetail, exportStatsCsv, parseCsvFilename } from './stats'

describe('parseCsvFilename', () => {
  it('优先取 filename*（RFC 5987，中文名在这里）', () => {
    const disp = "attachment; filename=\"stats.csv\"; filename*=UTF-8''%E5%90%8D%E7%89%87%E7%BB%9F%E8%AE%A1_2026-10-01_2026-10-07.csv"
    expect(parseCsvFilename(disp)).toBe('名片统计_2026-10-01_2026-10-07.csv')
  })

  it('回落 filename（老客户端不认 filename*）', () => {
    expect(parseCsvFilename('attachment; filename="stats.csv"')).toBe('stats.csv')
  })

  it('没有头 / 空值 → 兜底名', () => {
    expect(parseCsvFilename(undefined)).toBe('stats.csv')
    expect(parseCsvFilename(null)).toBe('stats.csv')
    expect(parseCsvFilename('attachment')).toBe('stats.csv')
  })

  it('filename* 百分号编码坏掉时不当崩，回落兜底名', () => {
    expect(parseCsvFilename("attachment; filename*=UTF-8''%E4%B8")).toBe('stats.csv')
  })
})

describe('adminStats', () => {
  beforeEach(() => {
    mocks.httpGet.mockReset()
  })

  it('把日期范围原样带给后端', async () => {
    mocks.httpGet.mockResolvedValue({ data: { summary: {}, byStaff: [], trend: [] } })
    await adminStats({ from: '2026-10-01', to: '2026-10-07' })
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/stats', {
      params: { from: '2026-10-01', to: '2026-10-07' },
    })
  })

  it('空范围不发 from/to（全部时间口径）', async () => {
    mocks.httpGet.mockResolvedValue({ data: { summary: {}, byStaff: [], trend: [] } })
    await adminStats({})
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/stats', { params: {} })
  })

  it('回包缺字段时回落空集合，不抛', async () => {
    mocks.httpGet.mockResolvedValue({ data: {} })
    const r = await adminStats()
    // 八项全在：缺项补 0（后端未跑迁移 0021 时后五项就没有）
    expect(r.summary).toEqual({
      visit: 0,
      call: 0,
      save: 0,
      projectView: 0,
      serviceView: 0,
      videoPlay: 0,
      share: 0,
      posterSave: 0,
    })
    expect(r.byStaff).toEqual([])
    expect(r.trend).toEqual([])
    expect(r.topProjects).toEqual([])
    expect(r.topServices).toEqual([])
  })

  it('解包内容浏览：TOP 榜与意向计数原样透出，并解包来源分布与裂变榜', async () => {
    mocks.httpGet.mockResolvedValue({
      data: {
        summary: { visit: 10, call: 2, save: 3, projectView: 7, serviceView: 5, videoPlay: 4, share: 6, posterSave: 1 },
        byStaff: [],
        trend: [],
        topProjects: [{ targetId: 3, name: '朗汇车间改造', count: 5 }],
        topServices: [{ targetId: 9, name: '机电维保', count: 4 }],
        // 迁移 0022 起：来源分布 + 裂变榜
        sourceDist: [
          { source: 'scan', count: 4 },
          { source: 'share_card', count: 5 },
        ],
        fissionTop: [
          { staffId: 1, name: '张三', count: 3 },
          { staffId: 2, name: '李四', count: 2 },
        ],
      },
    })
    const r = await adminStats()
    expect(r.summary.projectView).toBe(7)
    expect(r.summary.posterSave).toBe(1)
    expect(r.topProjects).toEqual([{ targetId: 3, name: '朗汇车间改造', count: 5 }])
    expect(r.topServices[0].name).toBe('机电维保')
    expect(r.sourceDist).toEqual([
      { source: 'scan', count: 4 },
      { source: 'share_card', count: 5 },
    ])
    expect(r.fissionTop[0]).toEqual({ staffId: 1, name: '张三', count: 3 })
  })

  it('来源分布 / 裂变榜缺字段时回落空数组（老副本未跑迁移 0022）', async () => {
    mocks.httpGet.mockResolvedValue({ data: { summary: { visit: 1, call: 2, save: 3 } } })
    const r = await adminStats()
    expect(r.sourceDist).toEqual([])
    expect(r.fissionTop).toEqual([])
  })

  it('老副本只回三项时，意向项按 0 渲染（不出现 undefined / NaN）', async () => {
    mocks.httpGet.mockResolvedValue({ data: { summary: { visit: 1, call: 2, save: 3 } } })
    const r = await adminStats()
    expect(r.summary.projectView).toBe(0)
    expect(r.summary.share).toBe(0)
    expect(Number.isNaN(r.summary.videoPlay)).toBe(false)
  })
})

describe('adminStatsDetail', () => {
  beforeEach(() => {
    mocks.httpGet.mockReset()
  })

  it('筛选参数一起下沉：staffId/eventType/targetId 转成字符串参数', async () => {
    mocks.httpGet.mockResolvedValue({ data: [], total: 0, page: 1, size: 20 })
    await adminStatsDetail({
      from: '2026-10-01',
      to: '2026-10-07',
      staffId: 3,
      eventType: 'project_view',
      targetId: 12,
      page: 2,
      size: 50,
    })
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/stats/detail', {
      params: {
        from: '2026-10-01',
        to: '2026-10-07',
        staffId: '3',
        eventType: 'project_view',
        targetId: '12',
        page: '2',
        size: '50',
      },
    })
  })

  it('空筛选不发 staffId/eventType/targetId（缺省即不过滤）', async () => {
    mocks.httpGet.mockResolvedValue({ data: [], total: 0, page: 1, size: 20 })
    await adminStatsDetail()
    expect(mocks.httpGet).toHaveBeenCalledWith('/admin/stats/detail', {
      params: { page: '1', size: '20' },
    })
  })

  it('对象名与 targetId 随明细行透出（TOP 榜下钻的「对象」列靠它）', async () => {
    mocks.httpGet.mockResolvedValue({
      data: [{ id: 7, staffId: 1, name: '张三', eventType: 'project_view', targetId: 12, targetName: '车间改造' }],
      total: 1,
      page: 1,
      size: 20,
    })
    const r = await adminStatsDetail({ targetId: 12 })
    expect(r.list[0].targetId).toBe(12)
    expect(r.list[0].targetName).toBe('车间改造')
  })

  it('解包分页信封：list/total/page/size，缺字段回落', async () => {
    mocks.httpGet.mockResolvedValue({
      data: [{ id: 7, staffId: 1, name: '张三', eventType: 'visit' }],
      total: 42,
      page: 2,
      size: 10,
    })
    const r = await adminStatsDetail({ page: 2, size: 10 })
    expect(r.list).toHaveLength(1)
    expect(r.list[0].name).toBe('张三')
    expect(r.total).toBe(42)
    expect(r.page).toBe(2)
    expect(r.size).toBe(10)

    // 后端异常回包（字段缺失）不能把界面打崩：回落空集合 + 第 1 页
    mocks.httpGet.mockResolvedValue({})
    const r2 = await adminStatsDetail()
    expect(r2.list).toEqual([])
    expect(r2.total).toBe(0)
    expect(r2.page).toBe(1)
    expect(r2.size).toBe(20)
  })
})

describe('exportStatsCsv', () => {
  beforeEach(() => {
    mocks.rawGet.mockReset()
    // jsdom 不实现 URL.createObjectURL，桩掉才能走下载分支
    ;(globalThis as any).URL.createObjectURL = vi.fn(() => 'blob:fake')
    ;(globalThis as any).URL.revokeObjectURL = vi.fn()
  })

  it('CSV 响应正常下载（不抛）', async () => {
    mocks.rawGet.mockResolvedValue({
      headers: {
        'content-type': 'text/csv; charset=utf-8',
        'content-disposition': "attachment; filename*=UTF-8''%E5%90%8D%E7%89%87.csv",
      },
      data: new Blob(['\xEF\xBB\xBF编号,姓名'], { type: 'text/csv' }),
    })
    await expect(exportStatsCsv({ from: '2026-10-01' })).resolves.toBeUndefined()
    expect(mocks.rawGet).toHaveBeenCalledWith('/admin/stats/export', {
      params: { from: '2026-10-01' },
      responseType: 'blob',
    })
  })

  it('业务失败信封不当 CSV 下载，而是抛出口袋里的 msg', async () => {
    mocks.rawGet.mockResolvedValue({
      headers: { 'content-type': 'application/json' },
      data: { text: async () => JSON.stringify({ code: 401, msg: '未登录' }) },
    })
    await expect(exportStatsCsv()).rejects.toThrow('未登录')
  })

  it('失败信封不是合法 JSON 时用兜底文案', async () => {
    mocks.rawGet.mockResolvedValue({
      headers: { 'content-type': 'application/json' },
      data: { text: async () => '<html>502</html>' },
    })
    await expect(exportStatsCsv()).rejects.toThrow('导出失败')
  })
})
