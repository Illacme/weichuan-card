import { describe, it, expect, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'

vi.mock('@/api/storageProfiles', () => ({
  listStorageProfiles: vi.fn(),
  saveStorageProfile: vi.fn(),
  deleteStorageProfile: vi.fn(),
  checkStorageProfile: vi.fn(),
  listDeliveryProfiles: vi.fn(),
  saveDeliveryProfile: vi.fn(),
  deleteDeliveryProfile: vi.fn(),
  checkDeliveryProfile: vi.fn(),
  listStorageBindings: vi.fn(),
  saveGlobalBinding: vi.fn(),
  saveCompanyBinding: vi.fn(),
  resetCompanyBinding: vi.fn(),
  listMigrationJobs: vi.fn(),
  createMigrationJob: vi.fn(),
  runMigrationJob: vi.fn(),
  cancelMigrationJob: vi.fn(),
  listMigrationErrors: vi.fn(),
}))
vi.mock('@/api/tenants', () => ({ listCompanies: vi.fn() }))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), warning: vi.fn(), error: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) },
}))

import { ElMessage } from 'element-plus'
import * as api from '@/api/storageProfiles'
import { listCompanies } from '@/api/tenants'
import StorageProfiles from './StorageProfiles.vue'
import { elStubs, flush } from '@/test/elStubs'

const storage = (id: number, name: string) => ({
  id, name, driver: 'minio', endpoint: `s${id}.example.com`, bucket: 'b', region: '',
  baseUrl: 'https://x/uploads', pathStyle: false, hostHeader: '', useSsl: true,
  localDir: '', publicRead: true, status: 'ok', lastError: '', lastProbeAt: null,
  version: 1, builtin: false, accessKeyConfigured: true, secretKeyConfigured: true,
  accessKeyMasked: 'AKIA****MPLE',
})
const delivery = (id: number, name: string) => ({
  id, name, provider: 'aliyun', domain: 'https://cdn.example.com', pathPrefix: '',
  endpoint: '', resourceId: '', status: 'ok', lastError: '', version: 1,
  accessKeyConfigured: true, secretKeyConfigured: true, accessKeyMasked: 'AKIA****MPLE',
})

const ElTabs = defineComponent({ name: 'ElTabs', template: '<div class="el-tabs"><slot /></div>' })
const ElTabPane = defineComponent({ name: 'ElTabPane', props: ['label', 'name'], template: '<div class="el-tab-pane"><slot /></div>' })
const ElSelect = defineComponent({
  name: 'ElSelect', props: ['modelValue', 'clearable', 'size'], emits: ['update:modelValue'],
  template: `<select class="el-select" :value="modelValue ?? ''" @change="$emit('update:modelValue', $event.target.value ? Number($event.target.value) : 0)"><slot /></select>`,
})
const ElOption = defineComponent({ name: 'ElOption', props: ['label', 'value'], template: '<option :value="value">{{ label }}</option>' })
const ElAlert = defineComponent({ name: 'ElAlert', props: ['title', 'description', 'type'], template: '<div class="el-alert">{{ title }} {{ description }}<slot /></div>' })
const ElFormItem = defineComponent({ name: 'ElFormItem', props: ['label'], template: '<div class="el-form-item"><slot /></div>' })
const ElForm = defineComponent({ name: 'ElForm', template: '<div class="el-form"><slot /></div>' })
const ElSwitch = defineComponent({ name: 'ElSwitch', props: ['modelValue'], emits: ['update:modelValue'], template: '<button class="el-switch" />' })
const ElCheckbox = defineComponent({ name: 'ElCheckbox', props: ['modelValue', 'disabled'], template: '<label class="el-checkbox"><slot /></label>' })
const ElProgress = defineComponent({ name: 'ElProgress', props: ['percentage'], template: '<div class="el-progress">{{ percentage }}%</div>' })

const stubs = {
  ...elStubs,
  'el-tabs': ElTabs, 'el-tab-pane': ElTabPane, 'el-select': ElSelect, 'el-option': ElOption,
  'el-alert': ElAlert, 'el-form': ElForm, 'el-form-item': ElFormItem,
  'el-switch': ElSwitch, 'el-checkbox': ElCheckbox, 'el-progress': ElProgress,
  'router-link': defineComponent({ template: '<a><slot /></a>' }),
}

function mountPage() {
  return mount(StorageProfiles, { global: { stubs, directives: { loading: {} } } })
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.listStorageProfiles).mockResolvedValue([storage(1, '主站 OSS'), storage(2, '备份桶')] as any)
  vi.mocked(api.listDeliveryProfiles).mockResolvedValue([delivery(3, '国内 CDN')] as any)
  vi.mocked(api.listStorageBindings).mockResolvedValue({
    global: { storageProfileId: 1, deliveryProfileId: 3, source: 'profile' },
    companies: [], storageProfiles: {}, deliveryProfiles: {},
  } as any)
  vi.mocked(api.listMigrationJobs).mockResolvedValue([] as any)
  vi.mocked(listCompanies).mockResolvedValue({ list: [{ id: 9, name: '甲公司' }], total: 1, page: 1, size: 200 } as any)
  vi.mocked(api.saveGlobalBinding).mockResolvedValue({} as any)
  vi.mocked(api.createMigrationJob).mockResolvedValue({} as any)
})

describe('文件存储与分发模块', () => {
  it('加载五类数据并说明三层模型', async () => {
    const w = mountPage()
    await flush()
    expect(api.listStorageProfiles).toHaveBeenCalled()
    expect(api.listDeliveryProfiles).toHaveBeenCalled()
    expect(api.listStorageBindings).toHaveBeenCalled()
    expect(api.listMigrationJobs).toHaveBeenCalled()
    expect(w.text()).toContain('配置只存一份，谁用由绑定决定')
    expect(w.text()).toContain('主站 OSS')
  })

  it('全局默认只提交两个档案 ID，保存后即时生效', async () => {
    const w = mountPage()
    await flush()
    // 绑定页签的 el-select 按 id 顺序：全局存储、全局分发
    const selects = w.findAll('select')
    await selects[0].setValue('2')
    await flush()
    const saveButton = w.findAll('button').find((b) => b.text() === '保存全局默认')!
    await saveButton.trigger('click')
    await flush()
    expect(api.saveGlobalBinding).toHaveBeenCalledWith(2, 3)
    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('即生效'))
  })

  it('迁移任务拒绝源与目标相同，避免无意义复制', async () => {
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    vm.newJob.sourceProfileId = 1
    vm.newJob.targetProfileId = 1
    const createButton = w.findAll('button').find((b) => b.text() === '创建任务')!
    await createButton.trigger('click')
    await flush()
    expect(api.createMigrationJob).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalledWith('源与目标不能相同')
  })

  it('路由与菜单必须同时登记（新增页面不能漏一处）', () => {
    const router = readFileSync('src/router/index.ts', 'utf8')
    const menu = readFileSync('src/layout/AdminLayout.vue', 'utf8')
    expect(router).toContain("path: 'platform/storage-profiles'")
    expect(router).toContain("path: 'company-storage'")
    expect(menu).toContain("path: '/platform/storage-profiles'")
    expect(menu).toContain("path: '/company-storage'")
    // 系统设置里必须有「默认档案」选择块（不是只留一条指路链接），
    // 企业管理行内必须有「存储档案」入口——这是用户明确要求的两个操作位置。
    const settings = readFileSync('src/views/platform/Settings.vue', 'utf8')
    const companies = readFileSync('src/views/Companies.vue', 'utf8')
    expect(settings).toContain('默认档案（推荐）')
    expect(settings).toContain('saveGlobalBinding')
    expect(companies).toContain("openProfiles(row)")
    expect(companies).toContain('saveCompanyBinding')
  })
})
