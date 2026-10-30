import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

// 企业列表接口：默认返回两家企业，个别用例改写返回值
const mocks = vi.hoisted(() => ({
  listCompanies: vi.fn(),
}))

vi.mock('@/api/tenants', () => ({ listCompanies: mocks.listCompanies }))

import { useTenantStore } from '@/store/tenant'
import { getCompanyScope, setCompanyScope } from '@/store/companyScope'

// 「当前管理的企业」是平台超管跨租户视角的唯一来源，它的三个输出都要钉死：
//   ① needPick（决定页面是否显示「请先选择企业」）；
//   ② currentId 持久化（刷新后不丢）；
//   ③ companyScope（决定请求是否带 ?companyId=）。
// ③ 是最关键的：它错了不会报错，只会静默展示/写入别家企业的数据。
function companiesFixture() {
  return [
    { id: 1, name: '朗汇机电设备有限公司', shortName: '朗汇机电', status: 'enabled', employeeCount: 1, adminCount: 1, createdAt: '' },
    { id: 2, name: '某某科技有限公司', shortName: '', status: 'disabled', employeeCount: 0, adminCount: 0, createdAt: '' },
  ]
}

function pagedOf(list: any[]) {
  return { list, total: list.length, page: 1, size: 100 }
}

describe('useTenantStore 当前企业', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    setCompanyScope(0, false)
    mocks.listCompanies.mockReset()
    mocks.listCompanies.mockResolvedValue(pagedOf(companiesFixture()))
  })

  it('企业管理员：固定本企业，且不注入 companyId', () => {
    const s = useTenantStore()
    s.syncIdentity(false, 9)

    expect(s.currentId).toBe(9)
    expect(s.needPick).toBe(false)
    // 后端本来就只认他这家，多传参数只会让「谁在收窄」变含糊
    expect(getCompanyScope()).toEqual({ companyId: 0, active: false })
  })

  it('平台超管未选企业：needPick 为真，且不注入 companyId', () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)

    expect(s.currentId).toBe(0)
    expect(s.needPick).toBe(true)
    expect(getCompanyScope()).toEqual({ companyId: 0, active: false })
  })

  it('选中企业：写入 localStorage 并激活请求作用域', () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    s.setCurrent(2)

    expect(s.currentId).toBe(2)
    expect(s.needPick).toBe(false)
    expect(localStorage.getItem('admin_scope_company_id')).toBe('2')
    expect(getCompanyScope()).toEqual({ companyId: 2, active: true })
  })

  it('取消选择：清掉持久化并关闭作用域', () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    s.setCurrent(2)
    s.setCurrent(0)

    expect(s.currentId).toBe(0)
    expect(localStorage.getItem('admin_scope_company_id')).toBeNull()
    expect(getCompanyScope().active).toBe(false)
  })

  it('刷新页面：从 localStorage 恢复当前企业', () => {
    localStorage.setItem('admin_scope_company_id', '2')
    setActivePinia(createPinia()) // 模拟新会话

    const s = useTenantStore()
    expect(s.currentId).toBe(2)
  })

  it('ensureLoaded 只对平台超管发请求，且只发一次', async () => {
    const s = useTenantStore()
    s.syncIdentity(false, 3)
    await s.ensureLoaded()
    expect(mocks.listCompanies).not.toHaveBeenCalled()

    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    await s.ensureLoaded()
    expect(mocks.listCompanies).toHaveBeenCalledTimes(1)
    expect(s.companies.map((c) => c.id)).toEqual([1, 2])
  })

  // AdminLayout 的 onMounted 里有两处 ensureLoaded（/api/admin/me 回来后一处、
  // 紧随其后的兜底一处），两次落在同一帧。此时 loaded 还是 false，
  // 只看 loaded 就会发出两个完全相同的请求——刷新一次后台白跑一次往返。
  it('并发调用 ensureLoaded 只发一次请求（刷新后台不重复拉）', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)

    await Promise.all([s.ensureLoaded(), s.ensureLoaded()])

    expect(mocks.listCompanies).toHaveBeenCalledTimes(1)
    expect(s.companies).toHaveLength(2)
  })

  it('拉取失败后记下 error，供选择器判断「是没拉到」而不是「真的没有」', async () => {
    mocks.listCompanies.mockRejectedValueOnce(new Error('network'))
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()

    expect(s.error).toBe(true)
    // 失败后允许重试：force=true 必须能绕过 inflight/loaded 的判断
    await s.ensureLoaded(true)
    expect(s.error).toBe(false)
    expect(s.companies).toHaveLength(2)
  })

  it('接口失败：保留已有候选，不把列表清空', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    expect(s.companies).toHaveLength(2)

    mocks.listCompanies.mockRejectedValueOnce(new Error('network'))
    await s.ensureLoaded(true)
    // 顶栏选择器宁可显示旧列表，也不要突然变空让人以为企业都没了
    expect(s.companies).toHaveLength(2)
    expect(s.loading).toBe(false)
  })

  // 企业被删 / 换了数据库：存下来的 id 已不存在，必须自动清掉，
  // 否则页面会带着一个不存在的 companyId 去请求，拿到空数据却没有任何提示
  it('存下来的企业已不存在：拉取后自动清空选择', async () => {
    localStorage.setItem('admin_scope_company_id', '99')
    setActivePinia(createPinia())
    const s = useTenantStore()
    s.syncIdentity(true, 0)

    await s.ensureLoaded()
    expect(s.currentId).toBe(0)
    expect(getCompanyScope().active).toBe(false)
  })

  it('关键字搜索：不因结果里没有当前企业就清空选择', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    s.setCurrent(2)

    mocks.listCompanies.mockResolvedValueOnce(pagedOf([companiesFixture()[0]]))
    await s.search('朗汇')
    expect(s.keyword).toBe('朗汇')
    // 搜索只是换候选列表，不该把「我已经选好的企业」弄丢
    expect(s.currentId).toBe(2)
  })

  it('clear：登出后不继承上一个人的视角', () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    s.setCurrent(2)

    s.clear()
    expect(s.currentId).toBe(0)
    expect(s.isPlatform).toBe(false)
    expect(s.companies).toEqual([])
    expect(localStorage.getItem('admin_scope_company_id')).toBeNull()
    expect(getCompanyScope().active).toBe(false)
  })
})
