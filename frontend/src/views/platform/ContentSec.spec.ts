import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'

vi.mock('@/api/mediaCheck', async (orig) => {
  const actual = (await orig()) as Record<string, unknown>
  return { ...actual, listMediaChecks: vi.fn(), getMediaCheckConfig: vi.fn(), takedownMediaCheck: vi.fn() }
})
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {} }))

import {
  getMediaCheckConfig,
  listMediaChecks,
  statusLabel,
  statusTag,
  takedownable,
  type MediaCheck,
} from '@/api/mediaCheck'
import ContentSec from './ContentSec.vue'

// 夹具：覆盖「违规未处置（可下架）」「违规已下架」「待结论」「通过」四种形态
function row(p: Partial<MediaCheck>): MediaCheck {
  return {
    id: 1,
    companyId: 1,
    url: 'https://card.example.com/uploads/a.jpg',
    objectKey: 'a.jpg',
    mediaType: 2,
    scene: 1,
    source: 'employee',
    traceId: 't1',
    status: 'pending',
    suggest: '',
    label: 0,
    raw: '',
    note: '',
    action: '',
    handledAt: null,
    handlerId: 0,
    createdAt: '2026-10-25 10:00:00',
    updatedAt: '2026-10-25 10:00:00',
    ...p,
  }
}

const rows = [
  row({ id: 3, status: 'risky', suggest: 'risky', label: 20001 }),
  row({ id: 2, status: 'risky', suggest: 'risky', label: 20001, action: 'takedown' }),
  row({ id: 1, status: 'pending' }),
]

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'description'],
  template: `<div class="el-alert">{{ title }} {{ description }}</div>`,
})
const ElTable = defineComponent({ name: 'ElTable', props: ['data'], template: `<div class="el-table"><slot /></div>` })
const ElTableColumn = defineComponent({ name: 'ElTableColumn', template: `<div class="el-table-column" />` })
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue'],
  emits: ['update:modelValue', 'change'],
  template: `<select class="el-select" :value="modelValue"
    @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
const ElTag = defineComponent({ name: 'ElTag', props: ['type'], template: `<span class="el-tag"><slot /></span>` })
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElPagination = defineComponent({ name: 'ElPagination', template: `<div class="el-pagination" />` })
const ElCheckbox = defineComponent({
  name: 'ElCheckbox',
  props: ['modelValue'],
  emits: ['update:modelValue', 'change'],
  template: `<label class="el-checkbox"><input type="checkbox" :checked="modelValue"
    @change="$emit('update:modelValue', $event.target.checked); $emit('change', $event.target.checked)" /><slot /></label>`,
})
const ImageThumb = defineComponent({ name: 'ImageThumb', props: ['src'], template: `<img class="thumb" />` })

const mountOpts = {
  global: {
    // v-loading 是 Element Plus 的指令，测试里没有注册；给个空实现消掉告警
    directives: { loading: {} },
    stubs: {
      'el-card': ElCard,
      'el-alert': ElAlert,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-tag': ElTag,
      'el-button': ElButton,
      'el-pagination': ElPagination,
      'el-checkbox': ElCheckbox,
      ImageThumb,
    },
  },
}

function mockConfig(over: Record<string, unknown> = {}) {
  vi.mocked(getMediaCheckConfig).mockImplementation(async () => ({
    enabled: true,
    tokenReady: true,
    aesReady: false,
    credReady: true,
    pushUrl: 'https://card.example.com/api/wechat/msgpush',
    pending: 1,
    risky: 1,
    ...over,
  }) as any)
}

async function setup() {
  const w = mount(ContentSec, mountOpts)
  await vi.waitFor(() => expect(listMediaChecks).toHaveBeenCalled())
  await nextTick()
  return w
}

describe('ContentSec 内容安全', () => {
  beforeEach(() => {
    vi.mocked(listMediaChecks).mockReset()
    vi.mocked(getMediaCheckConfig).mockReset()
    vi.mocked(listMediaChecks).mockImplementation(async () => ({
      code: 0,
      data: rows,
      total: rows.length,
      page: 1,
      size: 20,
    }) as any)
    mockConfig()
  })

  it('挂载即按默认分页查询，并同时拉配置状态', async () => {
    await setup()
    expect(listMediaChecks).toHaveBeenCalledWith({
      page: 1,
      size: 20,
      status: undefined,
      onlyTakedownable: undefined,
    })
    expect(getMediaCheckConfig).toHaveBeenCalled()
  })

  it('按状态筛选：带上 status 并回到第 1 页', async () => {
    const w = await setup()
    vi.mocked(listMediaChecks).mockClear()
    await w.find('select.el-select').setValue('risky')
    await nextTick()
    expect(listMediaChecks).toHaveBeenCalledWith(expect.objectContaining({ status: 'risky', page: 1 }))
  })

  it('勾「只看可下架」时按后端约定的 onlyTakedownable=1 传参', async () => {
    const w = await setup()
    vi.mocked(listMediaChecks).mockClear()
    await w.find('input[type="checkbox"]').setValue(true)
    await nextTick()
    expect(listMediaChecks).toHaveBeenCalledWith(expect.objectContaining({ onlyTakedownable: '1' }))
  })

  it('没配推送 Token 时明确提示「不会送检」', async () => {
    mockConfig({ tokenReady: false })
    const w = await setup()
    // 这是最容易被误解的一条：送检能成功、结论永远收不到，
    // 界面上必须直说「不会送检」，否则运营会以为图片在检。
    expect(w.text()).toContain('不会送检')
    expect(w.text()).toContain('待结论')
  })

  it('推送地址显示出来供复制（运维要抄到微信后台）', async () => {
    const w = await setup()
    expect(w.text()).toContain('https://card.example.com/api/wechat/msgpush')
  })
})

describe('内容安全的判定规则（与后端同口径）', () => {
  it('只有「命中违规 + 未处置 + 推得出对象键」才允许下架', () => {
    expect(takedownable(row({ status: 'risky' }))).toBe(true)
    // 已下架的不允许重复操作：覆盖不可逆，重复点只会多一条审计
    expect(takedownable(row({ status: 'risky', action: 'takedown' }))).toBe(false)
    // pass / review 下架等于用机器结论误伤正常内容
    expect(takedownable(row({ status: 'pass' }))).toBe(false)
    expect(takedownable(row({ status: 'review' }))).toBe(false)
    // 对象键推不出来时不下架：宁可人工处理，也不能拿猜出来的键覆盖别人的对象
    expect(takedownable(row({ status: 'risky', objectKey: '' }))).toBe(false)
  })

  it('状态与颜色：违规红、待结论黄、通过绿', () => {
    expect(statusLabel('risky')).toBe('违规')
    expect(statusTag('risky')).toBe('danger')
    expect(statusTag('pending')).toBe('warning')
    expect(statusTag('pass')).toBe('success')
    // 未登记的状态回退显示原值而不是空白——界面宁可丑也不能丢信息
    expect(statusLabel('weird')).toBe('weird')
    expect(statusTag('weird')).toBe('info')
  })
})
