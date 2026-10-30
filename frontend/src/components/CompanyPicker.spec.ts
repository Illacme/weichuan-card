import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

// 企业列表接口：本文件只关心「调了几次、带了什么关键字」，返回值用固定两家企业
const mocks = vi.hoisted(() => ({
  listCompanies: vi.fn(),
}))

vi.mock('@/api/tenants', () => ({ listCompanies: mocks.listCompanies }))

import CompanyPicker from './CompanyPicker.vue'
import { useTenantStore } from '@/store/tenant'
import { setCompanyScope } from '@/store/companyScope'

// 轻量 stub：只保留本组件真正用到的契约。
// 关键是 el-select 必须把 remote-method 作为 prop 暴露出来——测试要手动调用它，
// 模拟 Element Plus 展开下拉时的那次调用（这正是被回归的触发点）。
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: [
    'modelValue',
    'placeholder',
    'loading',
    'filterable',
    'clearable',
    'remote',
    'remoteMethod',
    'fallbackPlacements',
  ],
  emits: ['change', 'visible-change'],
  template: `<div class="el-select"><slot /><div class="el-select-empty"><slot name="empty" /></div></div>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<div class="el-option"><slot /></div>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-select': ElSelect,
      'el-option': ElOption,
    },
  },
}

function fixture() {
  return [
    { id: 1, name: '朗汇机电设备有限公司', shortName: '朗汇机电', status: 'enabled', employeeCount: 1, adminCount: 1, createdAt: '' },
    { id: 2, name: '某某科技有限公司', shortName: '', status: 'disabled', employeeCount: 0, adminCount: 0, createdAt: '' },
  ]
}

function pagedOf(list: unknown[]) {
  return { list, total: list.length, page: 1, size: 100 }
}

/** 模拟 Element Plus 展开下拉时对 remote-method 的调用（输入框内容通常是空串） */
async function openDropdown(w: ReturnType<typeof mount>, query = '') {
  const select = w.findComponent(ElSelect)
  select.props('remoteMethod')!(query)
  // 越过 250ms 防抖
  await vi.advanceTimersByTimeAsync(300)
  await nextTick()
}

describe('CompanyPicker 企业选择器', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    setCompanyScope(0, false)
    mocks.listCompanies.mockReset()
    mocks.listCompanies.mockResolvedValue(pagedOf(fixture()))
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  // 线上现象：点开下拉先闪一下「没有匹配的企业」，再变回列表。
  // 成因是展开时 Element Plus 会拿空串再调一次 remote-method → 又发一次请求 →
  // 请求期间选项容器被隐藏、改渲染 empty 插槽，而 loaded 早已为 true，
  // 于是空态文案是「没有匹配的企业」而不是「加载中…」。
  // 修法是在「已有全量候选」时直接不请求，空态压根没机会出现。
  it('展开下拉时不重复拉全量（避免闪一下「没有匹配的企业」）', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    expect(mocks.listCompanies).toHaveBeenCalledTimes(1)

    const w = mount(CompanyPicker, { props: { modelValue: 0 }, ...mountOpts })
    await openDropdown(w)

    // 候选已在手，展开不该再打接口——这一条挂了就说明闪烁会回来
    expect(mocks.listCompanies).toHaveBeenCalledTimes(1)
    expect(s.loading).toBe(false)
  })

  it('真的输入关键字时仍然远程搜索', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()

    const w = mount(CompanyPicker, { props: { modelValue: 0 }, ...mountOpts })
    await openDropdown(w, '朗汇')

    expect(mocks.listCompanies).toHaveBeenCalledTimes(2)
    expect(mocks.listCompanies).toHaveBeenLastCalledWith({ page: 1, size: 100, keyword: '朗汇' })
  })

  // 清空输入必须回到全量：按关键字搜过之后 companies 里只剩搜索结果，
  // 若把「空关键字」一律当成展开副作用跳过，用户就再也看不到完整列表了。
  it('按关键字搜过之后清空输入，必须重新拉全量', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    await s.search('朗汇')
    expect(s.keyword).toBe('朗汇')

    const w = mount(CompanyPicker, { props: { modelValue: 0 }, ...mountOpts })
    await openDropdown(w, '')

    expect(mocks.listCompanies).toHaveBeenCalledTimes(3)
    expect(mocks.listCompanies).toHaveBeenLastCalledWith({ page: 1, size: 100, keyword: undefined })
    expect(s.keyword).toBe('')
  })

  // 防回归：catch 里也会把 loaded 置 true（表示「别再自动拉」），
  // 若只用 loaded 判断，一次网络抖动就会让选择器永久停在空态、再也不重试。
  it('拉取失败后重新展开会重试，不会永久卡在空态', async () => {
    mocks.listCompanies.mockRejectedValueOnce(new Error('network'))
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()
    expect(s.error).toBe(true)
    expect(s.loaded).toBe(true)

    const w = mount(CompanyPicker, { props: { modelValue: 0 }, ...mountOpts })
    await openDropdown(w)

    expect(mocks.listCompanies).toHaveBeenCalledTimes(2)
    expect(s.error).toBe(false)
    expect(s.companies).toHaveLength(2)
  })

  it('请求在飞时空态显示「加载中…」，不谎称没有匹配', async () => {
    const s = useTenantStore()
    s.syncIdentity(true, 0)
    await s.ensureLoaded()

    // 挂起一个永不 resolve 的请求，把 loading 定格在 true
    mocks.listCompanies.mockImplementationOnce(() => new Promise(() => {}))
    const w = mount(CompanyPicker, { props: { modelValue: 0 }, ...mountOpts })
    void s.search('朗汇')
    await nextTick()

    expect(s.loading).toBe(true)
    expect(w.find('.el-select-empty').text()).toContain('加载中')
    expect(w.find('.el-select-empty').text()).not.toContain('没有匹配的企业')
  })
})
