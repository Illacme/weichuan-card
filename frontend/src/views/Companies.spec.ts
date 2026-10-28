import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed, watch } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import Companies from './Companies.vue'

// 企业管理页 · 租户小程序凭据。
//
// 这个功能的失败模式很具体：企业员工扫码进的是**平台**小程序（因为该企业没配
// AppID/AppSecret，静默回落了），客诉到来时后台完全看不出哪家没配。所以这里钉住
// 三件事：列表能看出谁没配、点开能回填、保存时 Secret 留空有二次确认。
const mocks = vi.hoisted(() => ({
  listCompanies: vi.fn(),
  createCompany: vi.fn(async () => ({ code: 0 })),
  updateCompany: vi.fn(async () => ({ code: 0 })),
  setCompanyStatus: vi.fn(async () => ({ code: 0 })),
  // 显式标注 data 为 any[]：不标会被推成 never[]，之后任何一条用例想 mock 出
  // 真实管理员列表都会撞 TS2322（"真实数据不能赋给 never"）。
  listCompanyAdmins: vi.fn(async (): Promise<{ code: number; data: any[] }> => ({ code: 0, data: [] })),
  createCompanyAdmin: vi.fn(async () => ({ code: 0 })),
  resetCompanyAdminPassword: vi.fn(async () => 'abc12345'),
  getCompanyWXApp: vi.fn(),
  saveCompanyWXApp: vi.fn(async () => ({ code: 0 })),
  // B4 授权 / B3 存储配额：组件 setup 里就 import 了这四个，
  // 漏 mock 会让 openLicense 里调用 undefined 直接抛 TypeError
  getCompanyLicense: vi.fn(),
  saveCompanyLicense: vi.fn(async () => ({ code: 0 })),
  getStorageUsage: vi.fn(),
  saveStorageQuota: vi.fn(async () => ({ code: 0 })),
}))

vi.mock('@/api/tenants', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn(async () => 'confirm') },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {}, Plus: {}, Search: {} }))
// 固定桌面端：手机端会裁掉编号/员工数等列，断言按桌面列来
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  return { useBreakpoint: () => ({ isPhone: ref(false) }) }
})

import { ElMessage, ElMessageBox } from 'element-plus'

// 四家租户：已配置 / 完全没配 / 只填了 AppID 而 Secret 被清空（后端算未配置）/ 授权已过期
// 授权状态用四种：正式、试用中、未配置、已过期——列表标签要能一眼分清这四种
const companies = [
  {
    id: 1, name: '甲方公司', shortName: '甲方', status: 'enabled', createdAt: '2026-10-01T10:00:00',
    employeeCount: 12, adminCount: 2, wxConfigured: true, loginCode: 'jiufang',
    storageBytes: 1572864, licenseExists: true, licenseEdition: 'platform', licenseSeats: 0,
    licenseExpiresAt: '', licenseExpired: false,
  },
  {
    id: 2, name: '乙方公司', shortName: '', status: 'enabled', createdAt: '2026-10-02T10:00:00',
    employeeCount: 3, adminCount: 1, wxConfigured: false,
    storageBytes: 0, licenseExists: true, licenseEdition: 'trial', licenseSeats: 5,
    licenseExpiresAt: '2026-11-09', licenseExpired: false,
  },
  {
    id: 3, name: '丙方公司', shortName: '丙方', status: 'disabled', createdAt: '2026-10-03T10:00:00',
    employeeCount: 0, adminCount: 0, wxConfigured: false,
    storageBytes: 0, licenseExists: false, licenseEdition: '', licenseSeats: 0,
    licenseExpiresAt: '', licenseExpired: false,
  },
  {
    id: 4, name: '丁方公司', shortName: '', status: 'enabled', createdAt: '2026-10-04T10:00:00',
    employeeCount: 8, adminCount: 1, wxConfigured: false,
    storageBytes: 0, licenseExists: true, licenseEdition: 'trial', licenseSeats: 5,
    licenseExpiresAt: '2026-10-01', licenseExpired: true,
  },
]

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['type', 'title', 'description', 'closable', 'showIcon'],
  template: `<div class="el-alert" :data-type="type" :data-title="title">{{ description }}</div>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'type', 'placeholder', 'clearable', 'showPassword'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :class="[$attrs.class]" :type="type || 'text'" :placeholder="placeholder"
    :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'link', 'icon', 'disabled'],
  template: `<button class="el-button" :disabled="disabled" v-bind="$attrs"><slot /></button>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'effect', 'size'],
  template: `<span class="el-tag" :data-type="type"><slot /></span>`,
})
// tooltip 的文案是「为什么这家是未配置」的唯一解释位，必须能在 DOM 里断言到
const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'placement'],
  template: `<span class="el-tooltip" :data-content="content"><slot /></span>`,
})
const ElDivider = defineComponent({ name: 'ElDivider', template: `<div class="el-divider"><slot /></div>` })
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  template: `<div class="el-pagination" :data-total="total" />`,
})
const ElForm = defineComponent({
  name: 'ElForm',
  props: ['model', 'rules'],
  setup(props, { expose, slots }) {
    // 真跑一遍 rules（required + validator），不恒 resolve：
    // 「AppID 不以 wx 开头不许保存」必须真的在测试里被触发一次，
    // 否则桩的 validate 恒真，这条校验等于没测。
    async function validate(): Promise<boolean> {
      const model = (props.model || {}) as Record<string, any>
      const rules = (props.rules || {}) as Record<string, any[]>
      for (const key of Object.keys(rules)) {
        for (const rule of rules[key]) {
          if (rule.required && !String(model[key] ?? '').trim()) return false
          if (typeof rule.validator === 'function') {
            const ok = await new Promise<boolean>((res) =>
              rule.validator(null, model[key], (err: any) => res(!err))
            )
            if (!ok) return false
          }
        }
      }
      return true
    }
    expose({ validate, clearValidate: () => {} })
    // 必须渲染默认插槽，否则输入框不进 DOM，断言就是对着空气验证
    return () => h('form', { class: 'el-form' }, slots.default ? slots.default() : [])
  },
})
const ElFormItem = defineComponent({
  name: 'ElFormItem',
  props: ['label', 'prop'],
  template: `<div class="el-form-item" :data-prop="prop"><label>{{ label }}</label><slot /></div>`,
})
// 平台代发开关。桩把开关态暴露成 data-checked，点一下即反转并发 update:modelValue，
// 这样「关掉开关再保存」这条链路能在测试里真的走一遍
const ElSwitch = defineComponent({
  name: 'ElSwitch',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<div class="el-switch" :data-checked="String(!!modelValue)" @click="$emit('update:modelValue', !modelValue)" />`,
})
// ===== 授权/配额弹窗用到的控件 =====
// el-select 桩渲染成原生 <select>：这样"换版本标识"在测试里是真的选出来的，
// 而不是靠直接改组件内部状态（改内部状态等于测试自己给自己作弊）。
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<select class="el-select" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
// 开通企业的「复制分类模板」勾选框。同样把勾选态暴露成 data-checked、点一下即反转，
// 这样「取消勾选后开通」这条链路能真的走一遍，而不是直接改组件内部状态。
const ElCheckbox = defineComponent({
  name: 'ElCheckbox',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<label class="el-checkbox" :data-checked="String(!!modelValue)" @click="$emit('update:modelValue', !modelValue)"><slot /></label>`,
})
const ElInputNumber = defineComponent({
  name: 'ElInputNumber',
  props: ['modelValue', 'min', 'max', 'step'],
  emits: ['update:modelValue'],
  template: `<input class="el-input-number" type="number" :value="modelValue" @input="$emit('update:modelValue', Number($event.target.value))" />`,
})
// value-format="YYYY-MM-DD" 在桩里恒为字符串，正是被测代码假设的形状
const ElDatePicker = defineComponent({
  name: 'ElDatePicker',
  props: ['modelValue', 'type', 'valueFormat', 'placeholder'],
  emits: ['update:modelValue'],
  template: `<input class="el-date-picker" :placeholder="placeholder" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  emits: ['closed'],
  setup(props, { emit }) {
    // 真实 el-dialog 在关闭动画结束后抛 closed（页面靠它对齐表单状态）。
    // 桩原先只声明了 emit 却从不触发，于是「关掉弹窗再打开，勾选态是否复位」
    // 这类用例在桩上永远测不到——补上这个 watch，桩才和真组件行为一致。
    watch(
      () => props.modelValue,
      (v) => {
        if (!v) emit('closed')
      }
    )
  },
  // 真渲染两个插槽：弹窗里的输入框和底部按钮都要能点到
  template: `<div v-if="modelValue" class="el-dialog"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})
const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', computed(() => props.row))
    return () => h('div', { class: 'el-table-row', 'data-row-id': props.row?.id }, slots.default ? slots.default() : [])
  },
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots, expose }) {
    expose({})
    return () =>
      h(
        'div',
        { class: 'el-table' },
        (props.data || []).map((row: any, i: number) =>
          h(TableRow, { key: i, row }, () => (slots.default ? slots.default() : null))
        )
      )
  },
})
const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  props: ['prop', 'label', 'width', 'align', 'fixed', 'minWidth', 'showOverflowTooltip'],
  setup(props, { slots }) {
    const rowRef = inject('tableRow', null) as any
    return () => {
      const row = rowRef ? rowRef.value : null
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column', 'data-prop': props.prop }, content)
    }
  },
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-alert': ElAlert,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-tag': ElTag,
      'el-tooltip': ElTooltip,
      'el-divider': ElDivider,
      'el-pagination': ElPagination,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-switch': ElSwitch,
      'el-checkbox': ElCheckbox,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-input-number': ElInputNumber,
      'el-date-picker': ElDatePicker,
      'el-dialog': ElDialog,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
    },
    directives: { loading: {} },
  },
}

async function flush() {
  for (let i = 0; i < 6; i++) await nextTick()
}

// listCompanies 在 api 层已把响应体收敛成 Paged（{list,total,page,size}），
// useServerTable 直接消费这个形状——所以 mock 必须回同样的形状，
// 回 {data:[...]} 会让 rows 恒为空，表格一行都不渲染。
function paged(list: any[]) {
  return { list, total: list.length, page: 1, size: 20 }
}

async function setup() {
  const w = mount(Companies, mountOpts)
  await vi.waitFor(() => expect(mocks.listCompanies).toHaveBeenCalled())
  await flush()
  return w
}

/** 找到某一行（按 data-row-id） */
function rowOf(w: any, id: number) {
  return w.findAll(`.el-table-row[data-row-id="${id}"]`)[0]
}

/** 在指定行里找按钮 */
function btnIn(row: any, text: string) {
  return row.findAll('button.el-button').find((b: any) => b.text().includes(text))
}

/** 按标题定位弹窗。页面上有六七个 el-dialog，直接 find('.el-dialog') 会命中
 *  当前正好打开的那个以外的东西（或压根命中空的），断言必须锁定到具体那一个。 */
function dialogByTitle(w: any, kw: string) {
  return w.findAll('.el-dialog').find((d: any) => d.find('h3').text().includes(kw))
}

/** 每个用例前重置全部 mock。两个 describe 共用这一份——只重置"自己关心的那几个"
 *  会让另一个 describe 的残留实现漏进来（比如某条用例把套餐改成已过期）。 */
function resetAll() {
  setActivePinia(createPinia())
  mocks.listCompanies.mockReset()
  mocks.getCompanyWXApp.mockReset()
  mocks.saveCompanyWXApp.mockReset()
  mocks.getCompanyLicense.mockReset()
  mocks.saveCompanyLicense.mockReset()
  mocks.getStorageUsage.mockReset()
  mocks.saveStorageQuota.mockReset()
  mocks.saveCompanyWXApp.mockImplementation(async () => ({ code: 0 }))
  mocks.saveCompanyLicense.mockImplementation(async () => ({ code: 0 }))
  mocks.saveStorageQuota.mockImplementation(async () => ({ code: 0 }))
  mocks.listCompanies.mockImplementation(async () => paged(companies))
  // 企业管理员相关：组件 setup 里 import 了这三个，漏 mock 会在点开弹窗时抛 TypeError
  mocks.listCompanyAdmins.mockReset()
  mocks.createCompanyAdmin.mockReset()
  mocks.resetCompanyAdminPassword.mockReset()
  mocks.listCompanyAdmins.mockImplementation(async () => ({ code: 0, data: [] }))
  mocks.createCompanyAdmin.mockImplementation(async () => ({ code: 0 }))
  mocks.resetCompanyAdminPassword.mockImplementation(async () => 'abc12345')
  mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: '', configured: false } }))
  mocks.getCompanyLicense.mockImplementation(async () => ({
    code: 0,
    data: {
      companyId: 3, exists: false, edition: '', seats: 0, expiresAt: '', features: '',
      expired: false, employeeCount: 0, trial: { enabled: true, days: 14, seats: 5 },
    },
  }))
  mocks.getStorageUsage.mockImplementation(async () => ({
    code: 0,
    data: {
      companyId: 3, bytes: 0, files: 0, quotaBytes: 0, quotaMb: 0, percent: 0,
      humanBytes: '0.0 MB', humanQuota: '0.0 MB', unlimited: true, quotaGlobal: 0,
    },
  }))
  vi.mocked(ElMessageBox.confirm).mockClear()
  vi.mocked(ElMessage.success).mockClear()
  vi.mocked(ElMessage.warning).mockClear()
  vi.mocked(ElMessage.error).mockClear()
}

describe('企业管理 · 租户小程序凭据', () => {
  beforeEach(resetAll)

  it('列表把 wxConfigured 渲染成「已配置 / 未配置」标签', async () => {
    const w = await setup()
    // 只能取「小程序」列里那个标签：状态列也有 el-tag（运营中/已停用），
    // 直接 find('.el-tag') 会先命中状态列
    const tag = (id: number) => rowOf(w, id).find('.el-tooltip .el-tag')
    expect(tag(1).text()).toBe('已配置')
    expect(tag(2).text()).toBe('未配置')
    expect(tag(3).text()).toBe('未配置')
    // 未配置用警示色，已配置用成功色——扫一眼就能分出该补哪家
    expect(tag(1).attributes('data-type')).toBe('success')
    expect(tag(2).attributes('data-type')).toBe('warning')
  })

  it('未配置的 tooltip 说明会回落到平台全局小程序', async () => {
    const w = await setup()
    expect(rowOf(w, 2).find('.el-tooltip').attributes('data-content')).toContain('回落')
    expect(rowOf(w, 1).find('.el-tooltip').attributes('data-content')).toContain('独立小程序')
  })

  it('点「凭据」拉取该企业当前值并回填 AppID', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({
      code: 0,
      data: { appId: 'wxexisting0001', configured: true },
    }))
    const w = await setup()
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()

    expect(mocks.getCompanyWXApp).toHaveBeenCalledWith(2)
    const dlg = w.find('.el-dialog')
    expect(dlg.exists()).toBe(true)
    expect(dlg.find('h3').text()).toContain('乙方公司')
    expect((dlg.find('input.el-input').element as HTMLInputElement).value).toBe('wxexisting0001')
  })

  it('已配置时弹窗顶部是「已配置」提示，未配置时提示会回落', async () => {
    const w = await setup()

    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()
    expect(w.find('.el-dialog .el-alert').attributes('data-type')).toBe('success')

    await w.find('.el-dialog .el-button').trigger('click') // 取消关闭
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: '', configured: false } }))
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()
    expect(w.find('.el-dialog .el-alert').attributes('data-type')).toBe('warning')
  })

  it('半配置（有 AppID 但 Secret 被清空）单独提示，不与「从没配过」混为一谈', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({
      code: 0,
      data: { appId: 'wxhalf000001', configured: false },
    }))
    const w = await setup()
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()

    const alert = w.find('.el-dialog .el-alert')
    expect(alert.attributes('data-title')).toContain('只填了 AppID')
    // Secret 输入框提示去补填，而不是「已配置」那套说法
    const secretInput = w.findAll('.el-dialog input.el-input')[1]
    expect(secretInput.attributes('placeholder')).toContain('补填')
    // AppID 仍在输入框里，用户知道要补的是哪一份
    expect((w.findAll('.el-dialog input.el-input')[0].element as HTMLInputElement).value).toBe('wxhalf000001')
  })

  it('填了 AppSecret：直接保存，不弹二次确认', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()

    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[0].setValue('wxnew1234567')
    await inputs[1].setValue('secret-abc')
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
    expect(mocks.saveCompanyWXApp).toHaveBeenCalledWith(1, {
      appId: 'wxnew1234567',
      appSecret: 'secret-abc',
      uploadKey: '',
      releaseEnabled: true,
    })
  })

  // 上传密钥与 AppSecret 同语义（留空即清空），但清掉的后果不同：
  // AppSecret 清了整体回落平台小程序，上传密钥清了只是上传改用平台全局那把。
  // 所以确认文案要把两件事分开说，不能混成一句「凭据已保存」。
  it('上传密钥：填了就随保存一起提交', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()

    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[1].setValue('secret-abc')
    // 刻意不用真 PEM 串：测试要验证的是「输入框里的值原样进 payload」，
    // 密钥长什么样由后端 mpupload.KeyProblems 负责判断
    await inputs[2].setValue('fake-upload-key-body')
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
    expect(mocks.saveCompanyWXApp).toHaveBeenCalledWith(1, {
      appId: 'wxok12345678',
      appSecret: 'secret-abc',
      uploadKey: 'fake-upload-key-body',
      releaseEnabled: true,
    })
  })

  it('上传密钥：原本已配置却留空，确认文案里点名上传密钥', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({
      code: 0,
      data: { appId: 'wxok12345678', configured: true, uploadKeyConfigured: true },
    }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()
    // 已配置时的占位符要说清「留空保存 = 清空并改用平台全局密钥」
    expect(w.findAll('.el-dialog input.el-input')[2].attributes('placeholder')).toContain('平台全局密钥')

    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).toHaveBeenCalled()
    // 两个都空 → 一次确认里同时点名，别连弹两次窗让人机械点确定
    const msg = String(vi.mocked(ElMessageBox.confirm).mock.calls[0][0])
    expect(msg).toContain('AppSecret')
    expect(msg).toContain('上传密钥')
  })

  it('Secret 留空且原本已配置：弹确认框，取消则不保存', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    vi.mocked(ElMessageBox.confirm).mockImplementationOnce(async () => {
      throw new Error('cancel') // 用户点了取消
    })
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()

    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(mocks.saveCompanyWXApp).not.toHaveBeenCalled()
  })

  it('Secret 留空且原本已配置：确认后按空串保存（清空并回落平台小程序）', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()

    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(mocks.saveCompanyWXApp).toHaveBeenCalledWith(1, {
      appId: 'wxok12345678',
      appSecret: '',
      uploadKey: '',
      releaseEnabled: true,
    })
  })

  // 平台代发开关是「能不能替这家企业提审/发布」的总闸。关掉后后端会拒绝一切
  // 代发动作（handler 侧 releaseAllowed），所以前端必须原样把开关值带上去。
  it('平台代发开关：缺省允许，关掉后随保存提交 false', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()

    // 回包没带 releaseEnabled 字段（存量数据）→ 视为允许
    expect(w.find('.el-dialog .el-switch').attributes('data-checked')).toBe('true')
    await w.find('.el-dialog .el-switch').trigger('click')
    expect(w.find('.el-dialog .el-switch').attributes('data-checked')).toBe('false')

    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[1].setValue('secret-abc')
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyWXApp).toHaveBeenCalledWith(1, {
      appId: 'wxok12345678',
      appSecret: 'secret-abc',
      uploadKey: '',
      releaseEnabled: false,
    })
  })

  it('平台代发开关：回包 releaseEnabled=false 时显示关闭态', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({
      code: 0,
      data: { appId: 'wxok12345678', configured: true, releaseEnabled: false },
    }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()
    expect(w.find('.el-dialog .el-switch').attributes('data-checked')).toBe('false')
  })

  it('AppID 不以 wx 开头：校验不通过，不发保存请求', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: '', configured: false } }))
    const w = await setup()
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()

    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[0].setValue('gh_1234567890')
    await inputs[1].setValue('whatever')
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyWXApp).not.toHaveBeenCalled()
  })

  it('AppID 为空：必填校验不通过，不发保存请求', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: '', configured: false } }))
    const w = await setup()
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()

    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyWXApp).not.toHaveBeenCalled()
  })

  it('保存成功后重新拉列表，让「小程序」列反映新状态', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: '', configured: false } }))
    const w = await setup()
    await btnIn(rowOf(w, 2), '凭据').trigger('click')
    await flush()

    const inputs = w.findAll('.el-dialog input.el-input')
    await inputs[0].setValue('wxbrand000002')
    await inputs[1].setValue('secret-xyz')
    mocks.listCompanies.mockClear()
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.listCompanies).toHaveBeenCalled()
    expect(ElMessage.success).toHaveBeenCalledWith('凭据已保存')
  })

  it('清空 Secret 保存后提示「已回落平台全局小程序」', async () => {
    mocks.getCompanyWXApp.mockImplementation(async () => ({ code: 0, data: { appId: 'wxok12345678', configured: true } }))
    const w = await setup()
    await btnIn(rowOf(w, 1), '凭据').trigger('click')
    await flush()
    await w.findAll('.el-dialog .el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('回落'))
  })

  it('凭据入口不受独立版企业锁定禁用（凭据是阻塞性配置）', async () => {
    localStorage.setItem('admin_independent_lock', '1')
    setActivePinia(createPinia())
    const w = await setup()
    // 编辑按钮被锁，凭据按钮仍可点
    expect(btnIn(rowOf(w, 1), '编辑').attributes('disabled')).toBeDefined()
    expect(btnIn(rowOf(w, 1), '凭据').attributes('disabled')).toBeUndefined()
    localStorage.removeItem('admin_independent_lock')
  })
})

// 授权与存储配额。
//
// 失败模式：客户到期后后台自动只读，而界面看不出"这家已过期"——运营只能等客户
// 打电话来问；或者反方向：本来让客户续费三年，结果把到期日填成当天零点，
// 客户上午就被停服。所以这里钉住四件事：四种授权状态分得清、点开能回填、
// 保存把授权与配额各发一次、到期日填在过去必须二次确认。
describe('企业管理 · 授权与存储配额', () => {
  beforeEach(resetAll)

  /** 某行「授权」列里的标签。每行有两个 tooltip（小程序 / 授权），取第二个 */
  function licTag(row: any) {
    return row.findAll('.el-tooltip .el-tag')[1]
  }

  async function openLic(w: any, id: number) {
    await btnIn(rowOf(w, id), '授权').trigger('click')
    await flush()
    return w.find('.el-dialog')
  }

  it('列表把四种授权状态渲染成不同标签：正式 / 试用中 / 未配置 / 已过期', async () => {
    const w = await setup()
    expect(licTag(rowOf(w, 1)).text()).toBe('正式')
    expect(licTag(rowOf(w, 1)).attributes('data-type')).toBe('success')
    expect(licTag(rowOf(w, 2)).text()).toBe('试用中')
    expect(licTag(rowOf(w, 2)).attributes('data-type')).toBe('warning')
    // 未配置不是"过期"，绝不能用同一个红色——那会让运营去催一个根本没约束的客户
    expect(licTag(rowOf(w, 3)).text()).toBe('未配置')
    expect(licTag(rowOf(w, 3)).attributes('data-type')).toBe('info')
    expect(licTag(rowOf(w, 4)).text()).toBe('已过期')
    expect(licTag(rowOf(w, 4)).attributes('data-type')).toBe('danger')
  })

  it('已过期的 tooltip 说清后果与恢复方式', async () => {
    const w = await setup()
    const tip = rowOf(w, 4).findAll('.el-tooltip')[1].attributes('data-content')
    expect(tip).toContain('只读')
    expect(tip).toContain('恢复')
  })

  it('存储列按人话显示用量（字节数直接显示没人看得懂）', async () => {
    const w = await setup()
    expect(rowOf(w, 1).text()).toContain('1.5 MB')
    expect(rowOf(w, 2).text()).toContain('0 B')
  })

  it('点「授权」同时拉授权与用量，并把当前值回填到表单', async () => {
    mocks.getCompanyLicense.mockImplementation(async () => ({
      code: 0,
      data: {
        companyId: 2, exists: true, edition: 'trial', seats: 5, expiresAt: '2026-11-09',
        features: '', expired: false, employeeCount: 3, trial: { enabled: true, days: 14, seats: 5 },
      },
    }))
    mocks.getStorageUsage.mockImplementation(async () => ({
      code: 0,
      data: {
        companyId: 2, bytes: 629145, files: 3, quotaBytes: 104857600, quotaMb: 100,
        percent: 1, humanBytes: '0.6 MB', humanQuota: '100.0 MB', unlimited: false, quotaGlobal: 2048,
      },
    }))
    const w = await setup()
    const dlg = await openLic(w, 2)

    expect(mocks.getCompanyLicense).toHaveBeenCalledWith(2)
    expect(mocks.getStorageUsage).toHaveBeenCalledWith(2)
    expect(dlg.find('h3').text()).toContain('乙方公司')
    expect((dlg.find('select.el-select').element as HTMLSelectElement).value).toBe('trial')
    const nums = dlg.findAll('input.el-input-number')
    expect((nums[0].element as HTMLInputElement).value).toBe('5') // 席位
    // 配额栏回填的是**覆盖值**（100），不是生效上限：显示生效值会让运营以为
    // 这家已经单独设过配额，之后改全局却"没生效"时查不出来
    expect((nums[1].element as HTMLInputElement).value).toBe('100')
    expect((dlg.find('input.el-date-picker').element as HTMLInputElement).value).toBe('2026-11-09')
    // 全局兜底值要在提示里说明，不然"0 = 跟随全局"跟到多少没人知道
    expect(dlg.text()).toContain('2048 MB')
    expect(dlg.text()).toContain('0.6 MB')
  })

  it('未配置授权的企业顶部提示「不受限制」，而不是红着脸说已过期', async () => {
    const w = await setup()
    const dlg = await openLic(w, 3)
    expect(dlg.find('.el-alert').attributes('data-type')).toBe('info')
    expect(dlg.find('.el-alert').attributes('data-title')).toContain('尚未配置授权')
  })

  it('试用中显示天数与席位口径', async () => {
    // 默认 mock 回的是"未配置"，这条用例要的是试用行——不显式给就会断言到另一档提示上
    mocks.getCompanyLicense.mockImplementation(async () => ({
      code: 0,
      data: {
        companyId: 2, exists: true, edition: 'trial', seats: 5, expiresAt: '2026-11-09',
        features: '', expired: false, employeeCount: 3, trial: { enabled: true, days: 14, seats: 5 },
      },
    }))
    const w = await setup()
    const dlg = await openLic(w, 2)
    expect(dlg.find('.el-alert').attributes('data-title')).toContain('14 天')
    expect(dlg.find('.el-alert').attributes('data-title')).toContain('5 席')
  })

  it('保存：授权与配额各发一次，payload 原样，顺序先是授权后是配额', async () => {
    const w = await setup()
    const dlg = await openLic(w, 2)

    await dlg.find('select.el-select').setValue('platform')
    const nums = dlg.findAll('input.el-input-number')
    await nums[0].setValue('8')
    await nums[1].setValue('500')
    await dlg.find('input.el-date-picker').setValue('2027-01-31')
    await dlg.find('input.el-input').setValue('mp_release') // 功能开关（整弹窗唯一的普通 input）
    mocks.listCompanies.mockClear()
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyLicense).toHaveBeenCalledWith(2, {
      edition: 'platform',
      seats: 8,
      expiresAt: '2027-01-31',
      features: 'mp_release',
    })
    expect(mocks.saveStorageQuota).toHaveBeenCalledWith(2, 500)
    expect(mocks.saveCompanyLicense.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.saveStorageQuota.mock.invocationCallOrder[0]
    )
    expect(ElMessage.success).toHaveBeenCalledWith('授权与配额已保存')
    expect(mocks.listCompanies).toHaveBeenCalled()
  })

  it('到期日填在过去：二次确认里说清会立刻降级只读；取消则一个请求都不发', async () => {
    vi.mocked(ElMessageBox.confirm).mockImplementationOnce(async () => {
      throw new Error('cancel')
    })
    const w = await setup()
    const dlg = await openLic(w, 1)
    await dlg.find('input.el-date-picker').setValue('2020-01-01')
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).toHaveBeenCalled()
    const msg = String(vi.mocked(ElMessageBox.confirm).mock.calls[0][0])
    expect(msg).toContain('2020-01-01')
    expect(msg).toContain('只读')
    expect(mocks.saveCompanyLicense).not.toHaveBeenCalled()
    expect(mocks.saveStorageQuota).not.toHaveBeenCalled()
  })

  it('到期日在未来：不弹确认，直接保存', async () => {
    const w = await setup()
    const dlg = await openLic(w, 1)
    await dlg.find('input.el-date-picker').setValue('2030-01-01')
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
    expect(mocks.saveCompanyLicense).toHaveBeenCalled()
  })

  it('清空到期日 = 不过期（空串原样提交，不是"没填"）', async () => {
    const w = await setup()
    const dlg = await openLic(w, 2)
    await dlg.find('input.el-date-picker').setValue('')
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyLicense).toHaveBeenCalledWith(2, expect.objectContaining({ expiresAt: '' }))
  })

  it('独立版锁定：能点开看，但保存被拒且不发请求', async () => {
    localStorage.setItem('admin_independent_lock', '1')
    const w = await setup()
    // 查看授权不算写操作，锁定下仍要能看（否则运营没法回答"客户什么时候到期"）
    const dlg = await openLic(w, 1)
    expect(dlg.exists()).toBe(true)
    // 编辑按钮被锁，授权按钮仍可点
    expect(btnIn(rowOf(w, 1), '授权').attributes('disabled')).toBeUndefined()

    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()
    expect(ElMessage.warning).toHaveBeenCalledWith('已开启独立版企业锁定，当前为只读')
    expect(mocks.saveCompanyLicense).not.toHaveBeenCalled()
    localStorage.removeItem('admin_independent_lock')
  })

  it('配额接口失败时授权也不该白丢：保存请求仍按顺序发出（幂等可重试）', async () => {
    mocks.saveStorageQuota.mockImplementationOnce(async () => {
      throw new Error('boom')
    })
    const w = await setup()
    const dlg = await openLic(w, 1)
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyLicense).toHaveBeenCalled()
    expect(mocks.saveStorageQuota).toHaveBeenCalled()
    // 失败就别报成功，也别关弹窗——用户要能原地重试
    expect(ElMessage.success).not.toHaveBeenCalledWith('授权与配额已保存')
  })

  // 用量没拉到时 licForm.quotaMb 还是初值 0。若照旧提交，等于把客户已设的上限
  // 悄悄改回「跟随全局」——一次接口抖动造成的行为变更，事后根本无从解释。
  it('用量接口失败时只保存授权，绝不把配额悄悄清零', async () => {
    mocks.getStorageUsage.mockImplementationOnce(async () => {
      throw new Error('boom')
    })
    const w = await setup()
    const dlg = await openLic(w, 2)
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.saveCompanyLicense).toHaveBeenCalled()
    expect(mocks.saveStorageQuota).not.toHaveBeenCalled()
    // 而且要说清为什么没改，不能让运营以为配额也一起保存了
    expect(ElMessage.warning).toHaveBeenCalledWith(expect.stringContaining('存储配额未改动'))
    expect(ElMessage.success).toHaveBeenCalledWith('授权已保存')
  })
})

// 登录页白标码（login_code）。
//
// 失败模式：客户多了以后，登录页「微传名片 · 管理后台」对所有企业一模一样，
// 客户分不清是不是自己的后台入口；且运营在列表里看不出哪家配了白标码。
// 钉住三件事：列表能看出谁配了（?company= 是什么）、开通时能填并随请求提交、
// 编辑时能回填并带新值提交。
describe('企业管理 · 登录页白标码', () => {
  beforeEach(resetAll)

  it('列表白标码列：已配置显示 ?company= 码，未配置显示「未配置」', async () => {
    const w = await setup()
    expect(rowOf(w, 1).find('.login-code-cell').text()).toContain('?company=jiufang')
    expect(rowOf(w, 2).find('.muted').text()).toBe('未配置')
  })

  it('开通企业：填写白标码后随创建请求一起提交', async () => {
    const w = await setup()
    await w.findAll('button.el-button').find((b: any) => b.text().includes('开通企业'))!.trigger('click')
    await flush()
    const dlg = w.find('.el-dialog')
    expect(dlg.exists()).toBe(true)
    // 开通弹窗输入顺序：企业名称 / 企业简称 / 登录白标码 / 管理员用户名 / 管理员密码
    const inputs = dlg.findAll('input.el-input')
    await inputs[0].setValue('新公司') // name 必填
    await inputs[1].setValue('新简称')
    await inputs[2].setValue('newcode') // 登录白标码
    await dlg.findAll('.el-button').find((b: any) => b.text().includes('开通'))!.trigger('click')
    await flush()

    expect(mocks.createCompany).toHaveBeenCalledWith(
      expect.objectContaining({ name: '新公司', loginCode: 'newcode' })
    )
  })

  it('编辑企业：回填空白标码，保存时带新值提交（允许清空）', async () => {
    const w = await setup()
    await btnIn(rowOf(w, 1), '编辑').trigger('click')
    await flush()
    const dlg = w.find('.el-dialog')
    // 编辑弹窗输入顺序：企业名称 / 企业简称 / 登录白标码
    expect((dlg.findAll('input.el-input')[2].element as HTMLInputElement).value).toBe('jiufang')
    await dlg.findAll('input.el-input')[2].setValue('jiufang2')
    await dlg.findAll('.el-button').find((b: any) => b.text() === '保存')!.trigger('click')
    await flush()

    expect(mocks.updateCompany).toHaveBeenCalledWith(1, expect.objectContaining({ loginCode: 'jiufang2' }))
  })
})

// 开通企业时自动复制业绩分类模板（经典业绩 8 类）。
// 失败模式：零数据版新企业一个分类都没有 → 企业管理员登录后业绩页空白、小程序业绩 Tab
// 渲染不出来，得先自建分类才能录案例。但这件事必须**可取消**：客户行业完全对不上时，
// 与其建完再一条条删，不如开的时候就不要。
describe('企业管理 · 开通企业复制分类模板', () => {
  beforeEach(resetAll)

  async function openCreate() {
    const w = await setup()
    await w.findAll('button.el-button').find((b: any) => b.text().includes('开通企业'))!.trigger('click')
    await flush()
    return w
  }

  it('默认勾选，开通时显式提交 seedCategories=true', async () => {
    const w = await openCreate()
    const dlg = w.find('.el-dialog')
    const box = dlg.find('.el-checkbox')
    expect(box.exists()).toBe(true)
    expect(box.attributes('data-checked')).toBe('true')

    await dlg.findAll('input.el-input')[0].setValue('新公司')
    await dlg.findAll('.el-button').find((b: any) => b.text().includes('开通'))!.trigger('click')
    await flush()

    expect(mocks.createCompany).toHaveBeenCalledWith(
      expect.objectContaining({ name: '新公司', seedCategories: true })
    )
  })

  it('取消勾选后开通：提交 seedCategories=false（明确不要，不是"没填"）', async () => {
    const w = await openCreate()
    const dlg = w.find('.el-dialog')
    await dlg.find('.el-checkbox').trigger('click')
    await flush()
    expect(dlg.find('.el-checkbox').attributes('data-checked')).toBe('false')

    await dlg.findAll('input.el-input')[0].setValue('新公司')
    await dlg.findAll('.el-button').find((b: any) => b.text().includes('开通'))!.trigger('click')
    await flush()

    // 必须是 false 而不是 undefined：后端用指针区分「没传」与「传了 false」，
    // 传 undefined 会被当成没表态 → 照样复制模板，用户取消了个寂寞。
    expect(mocks.createCompany).toHaveBeenCalledWith(
      expect.objectContaining({ name: '新公司', seedCategories: false })
    )
  })

  it('面板关闭后重新打开：勾选态回到默认（不继承上一次的取消）', async () => {
    const w = await openCreate()
    await w.find('.el-dialog .el-checkbox').trigger('click')
    await flush()
    expect(w.find('.el-dialog .el-checkbox').attributes('data-checked')).toBe('false')

    // 取消按钮关闭弹窗 → @closed 触发 resetCreate
    await w.find('.el-dialog').findAll('.el-button').find((b: any) => b.text() === '取消')!.trigger('click')
    await flush()
    await w.findAll('button.el-button').find((b: any) => b.text().includes('开通企业'))!.trigger('click')
    await flush()

    expect(w.find('.el-dialog .el-checkbox').attributes('data-checked')).toBe('true')
  })
})

describe('企业管理 · 平台超管重置企业管理员密码', () => {
  beforeEach(resetAll)

  // 企业管理员忘了密码时唯一的救济入口就是平台超管代重置（自己改密要原密码，
  // 库里只存哈希谁也读不回来）。这个动作的代价是**把对方当前会话全踢下线**，
  // 所以测试重点不是"接口调了没"，而是"确认框弹了没、新密码回显了没"。
  const admins = [{ id: 11, username: 'entadmin', createdAt: '2026-10-01T10:00:00' }]

  async function openAdminsDialog(w: any) {
    mocks.listCompanyAdmins.mockImplementation(async () => ({ code: 0, data: admins }))
    await btnIn(rowOf(w, 1), '管理员').trigger('click')
    await flush()
    return dialogByTitle(w, '管理员管理')
  }

  it('确认后调接口，并回显服务端生成的新初始密码', async () => {
    mocks.resetCompanyAdminPassword.mockImplementation(async () => 'k7m2p9xq')
    const w = await setup()

    const dlg = await openAdminsDialog(w)
    expect(mocks.listCompanyAdmins).toHaveBeenCalledWith(1)
    expect(dlg.text()).toContain('entadmin')

    await btnIn(rowOf(w, 11), '重置密码').trigger('click')
    await flush()

    // 会把对方踢下线，点之前必须确认过
    expect(ElMessageBox.confirm).toHaveBeenCalled()
    expect(mocks.resetCompanyAdminPassword).toHaveBeenCalledWith(1, 11)

    // 新密码只在这一次响应里出现，必须当场回显——关掉就看不到了
    const pwdDlg = dialogByTitle(w, '密码已重置')
    expect(pwdDlg).toBeTruthy()
    expect(pwdDlg!.text()).toContain('k7m2p9xq')
    expect(pwdDlg!.text()).toContain('entadmin')
  })

  it('确认框点取消则不调接口（避免误点把人踢下线）', async () => {
    const w = await setup()
    await openAdminsDialog(w)

    // Element Plus 的 confirm 在用户取消时是 reject，捕获后应静默返回
    vi.mocked(ElMessageBox.confirm).mockImplementationOnce(async () => {
      throw new Error('cancel')
    })
    await btnIn(rowOf(w, 11), '重置密码').trigger('click')
    await flush()

    expect(mocks.resetCompanyAdminPassword).not.toHaveBeenCalled()
    expect(dialogByTitle(w, '密码已重置')).toBeUndefined()
  })
})
