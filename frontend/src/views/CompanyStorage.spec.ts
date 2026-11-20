import { describe, it, expect, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'

vi.mock('@/api/storageProfiles', () => ({
  listStorageBindings: vi.fn(),
  saveCompanyBinding: vi.fn(),
  resetCompanyBinding: vi.fn(),
}))
vi.mock('@/api/request', () => ({ default: { get: vi.fn() } }))
vi.mock('@/store/auth', () => ({ useAuthStore: () => ({ companyId: 7 }) }))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
  ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) },
}))

import http from '@/api/request'
import * as api from '@/api/storageProfiles'
import CompanyStorage from './CompanyStorage.vue'
import { elStubs, flush } from '@/test/elStubs'

const ElAlert = defineComponent({ name: 'ElAlert', props: ['title', 'description'], template: '<div class="el-alert">{{ title }} {{ description }}</div>' })
const ElSelect = defineComponent({
  name: 'ElSelect', props: ['modelValue'], emits: ['update:modelValue'],
  template: `<select :value="modelValue ?? ''" @change="$emit('update:modelValue', Number($event.target.value))"><slot /></select>`,
})
const ElOption = defineComponent({ name: 'ElOption', props: ['label', 'value'], template: '<option :value="value">{{ label }}</option>' })
const ElForm = defineComponent({ name: 'ElForm', template: '<div><slot /></div>' })
const ElFormItem = defineComponent({ name: 'ElFormItem', props: ['label'], template: '<div><slot /></div>' })
const stubs = { ...elStubs, 'el-alert': ElAlert, 'el-select': ElSelect, 'el-option': ElOption, 'el-form': ElForm, 'el-form-item': ElFormItem }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(http.get).mockResolvedValue({ data: { storageProfiles: [{ id: 1, name: '主站 OSS' }], deliveryProfiles: [] } } as any)
  vi.mocked(api.listStorageBindings).mockResolvedValue({ companies: [], global: {} } as any)
  vi.mocked(api.saveCompanyBinding).mockResolvedValue({} as any)
  vi.mocked(api.resetCompanyBinding).mockResolvedValue({} as any)
})

describe('企业自助选择存储档案', () => {
  it('只展示本企业绑定，保存时带当前企业 ID', async () => {
    const w = mount(CompanyStorage, { global: { stubs } })
    await flush()
    expect(http.get).toHaveBeenCalledWith('/admin/storage-bindings/options')
    expect(w.text()).toContain('继承全局默认')
    const select = w.find('select')
    await select.setValue('1')
    const save = w.findAll('button').find((b) => b.text() === '保存本企业绑定')!
    await save.trigger('click')
    await flush()
    expect(api.saveCompanyBinding).toHaveBeenCalledWith(7, 1, 0)
  })

  it('明确说明看不到凭据、历史对象不会自动搬迁', async () => {
    const w = mount(CompanyStorage, { global: { stubs } })
    await flush()
    expect(w.text()).toContain('看不到也改不了任何云凭据')
    expect(w.text()).toContain('历史对象不会自动搬迁')
  })
})
