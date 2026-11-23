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
  rollbackGlobalBinding: vi.fn(),
  rollbackCompanyBinding: vi.fn(),
  listMigrationJobs: vi.fn(),
  createMigrationJob: vi.fn(),
  runMigrationJob: vi.fn(),
  cancelMigrationJob: vi.fn(),
  listMigrationErrors: vi.fn(),
}))
vi.mock('@/api/tenants', () => ({ listCompanies: vi.fn() }))
vi.mock('@/api/settings', () => ({
  getSettings: vi.fn().mockResolvedValue({ code: 0, data: [] }),
  updateSettings: vi.fn().mockResolvedValue({ code: 0, data: { restartKeys: [] } }),
}))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), warning: vi.fn(), error: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) },
}))

import { ElMessage } from 'element-plus'
import * as api from '@/api/storageProfiles'
import { listCompanies } from '@/api/tenants'
import { getSettings } from '@/api/settings'
import StorageProfiles from './StorageProfiles.vue'
import { elStubs, flush } from '@/test/elStubs'

const storage = (id: number, name: string) => ({
  id, name, driver: 'minio', endpoint: `s${id}.example.com`, bucket: 'b', region: '',
  baseUrl: 'https://x/uploads', hostHeader: '', useSsl: true,
  localDir: '', publicRead: true, status: 'ok', lastError: '', lastProbeAt: null,
  version: 1, builtin: false, accessKeyConfigured: true, secretKeyConfigured: true,
  accessKeyMasked: 'AKIA****MPLE',
})
const delivery = (id: number, name: string) => ({
  id, name, provider: 'aliyun', domain: 'https://cdn.example.com',
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
// label 插槽必须渲染：配置项旁边的「?」帮助挂在这里，不渲染就等于没测
const ElFormItem = defineComponent({ name: 'ElFormItem', props: ['label'], template: '<div class="el-form-item"><slot name="label">{{ label }}</slot><slot /></div>' })
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
  // 新「平台自身存储」标签会挂载 StorageSettingsPanel，它自己拉系统设置；
  // 这里显式给实现，避免 clearAllMocks 后拿不到返回值。
  vi.mocked(getSettings).mockResolvedValue({ code: 0, data: [] } as any)
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

describe('存储与分发模块', () => {
  it('加载五类数据并说明三层模型', async () => {
    const w = mountPage()
    await flush()
    expect(api.listStorageProfiles).toHaveBeenCalled()
    expect(api.listDeliveryProfiles).toHaveBeenCalled()
    expect(api.listStorageBindings).toHaveBeenCalled()
    expect(api.listMigrationJobs).toHaveBeenCalled()
    expect(w.text()).toContain('配置只存一份，谁用由绑定决定')
    // 管理员看不懂 tenant_id=0 这类内部术语，顶部说明要说人话
    expect(w.text()).not.toContain('tenant_id')
    expect(w.text()).toContain('所有人默认用同一套')
    expect(w.text()).toContain('主站 OSS')
  })

  it('全局默认提交两个档案 ID 与 force（默认 false），保存后即时生效', async () => {
    const w = mountPage()
    await flush()
    // 绑定页签的 el-select 按 id 顺序：全局存储、全局分发
    const selects = w.find('.binding-card').findAll('select')
    await selects[0].setValue('2')
    await flush()
    const saveButton = w.findAll('button').find((b) => b.text() === '保存全局默认')!
    await saveButton.trigger('click')
    await flush()
    expect(api.saveGlobalBinding).toHaveBeenCalledWith(2, 3, false)
    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('即生效'))
  })

  it('强制切换勾选后把 force=true 传给后端，供目标缺对象时逃生', async () => {
    vi.mocked(api.saveGlobalBinding).mockResolvedValue({} as any)
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    vm.forceGlobal = true
    const saveButton = w.findAll('button').find((b) => b.text() === '保存全局默认')!
    await saveButton.trigger('click')
    await flush()
    expect(api.saveGlobalBinding).toHaveBeenCalledWith(1, 3, true)
  })

  it('有上一套档案时显示回滚入口，点击调用回滚接口', async () => {
    vi.mocked(api.listStorageBindings).mockResolvedValue({
      global: { storageProfileId: 1, deliveryProfileId: 3, previousStorageProfileId: 2, source: 'profile' },
      companies: [{
        tenantId: 9, storageProfileId: 1, deliveryProfileId: 3,
        previousStorageProfileId: 2, previousDeliveryProfileId: 0,
        source: 'company', version: 2, updatedBy: 'x', updatedAt: '',
      }], storageProfiles: {}, deliveryProfiles: {},
    } as any)
    vi.mocked(api.rollbackGlobalBinding).mockResolvedValue({} as any)
    vi.mocked(api.rollbackCompanyBinding).mockResolvedValue({} as any)
    const w = mountPage()
    await flush()
    // 全局与企业两处回滚入口都要出现（切换前的文件存储是「备份桶」）
    expect(w.text()).toContain('回滚到上一套（备份桶）')
    // el-table-column 的 label 不在 stub 渲染的 text 里，用源码钉住「切换前的文件存储」列存在
    expect(readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')).toContain('label="切换前的文件存储"')
    const globalRollback = w.findAll('button').find((b) => b.text().includes('回滚到上一套'))!
    await globalRollback.trigger('click')
    await flush()
    expect(api.rollbackGlobalBinding).toHaveBeenCalled()
    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('回滚'))
    const rowRollback = w.findAll('button').find((b) => b.text() === '回滚')!
    await rowRollback.trigger('click')
    await flush()
    expect(api.rollbackCompanyBinding).toHaveBeenCalledWith(9)
  })

  it('没有上一套档案时不显示回滚按钮（避免空回滚）', async () => {
    const w = mountPage()
    await flush()
    expect(w.findAll('button').filter((b) => b.text() === '回滚')).toHaveLength(0)
    expect(w.findAll('button').filter((b) => b.text().includes('回滚到上一套'))).toHaveLength(0)
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

  it('内置本地档案标记为「内置」，编辑时驱动锁定并说明兜底用途', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '平台本地存储'), builtin: true, driver: 'local' },
    ] as any)
    const w = mountPage()
    await flush()
    expect(w.text()).toContain('平台本地存储')
    expect(w.text()).toContain('内置')
    // 说明移入标签悬停提示：列表保持清爽，但要钉死悬停会显示「系统自带、别删」
    const tip1 = w.findComponent({ name: 'ElTooltip' })
    expect(tip1.exists()).toBe(true)
    expect(tip1.props('content')).toContain('系统自带的备用盘')
    expect(tip1.props('content')).toContain('请勿删除')
    // 内置档案改不动目录/基址，必须给一条直达系统设置的链接，
    // 否则管理员只能在编辑弹窗里对着灰字段干瞪眼。
    expect(tip1.props('content')).toContain('本地上传目录')
    expect(tip1.props('content')).toContain('这里改不了')
    const builtinLink = w.find('a.builtin-setting-link')
    expect(builtinLink.exists()).toBe(true)
    expect(builtinLink.attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=upload_dir')
    expect(builtinLink.text()).toContain('跟随系统设置')
    const edit = w.findAll('button').find((b) => b.text() === '编辑')!
    await edit.trigger('click')
    await flush()
    // 编辑弹窗顶部要有「应急备用盘 vs 备份保险柜」的对照说明，管理员一眼看懂两者区别
    expect(w.text()).toContain('这是系统自带的文件存储，通常不用改动')
    expect(w.text()).toContain('应急备用盘')
    expect(w.text()).toContain('备份保险柜')
    // 开发者术语不能出现在给管理员看的说明里
    expect(w.text()).not.toContain('静态文件服务也绑在同一目录')
    expect(w.text()).not.toContain('会导致图片 404')
    expect(w.text()).toContain('驱动锁定为「本地磁盘」')
    expect(w.text()).toContain('不可删除')
    // 目录也必须锁死：它与静态路由共用一个真值源，单独改会让租户对象写进
    // /uploads 读不到的目录，全站裂图且不报错。
    expect(w.text()).toContain('系统自带文件存储的目录继承自')
    expect(w.text()).toContain('upload_dir')
    expect(w.text()).toContain('重启服务')
    const dirInput = w.find('input[placeholder="留空使用平台 uploads 目录"]')
    expect(dirInput.exists()).toBe(true)
    expect(dirInput.attributes('disabled')).toBeDefined()
    // 内置档案不渲染可点的删除按钮，只显示灰色「系统自带 · 不可删」提示：
    // 之前用 danger link + disabled，禁用态仍是红字，管理员以为能点却点不动。
    expect(w.text()).toContain('系统自带 · 不可删')
    // 只看内置档案这一行：它不该有可点的删除按钮（别的企业档案仍有）
    const builtinRow = w.findAll('.el-table-row').find((r) => r.text().includes('平台本地存储'))!
    expect(builtinRow.text()).toContain('系统自带 · 不可删')
    expect(builtinRow.findAll('button').filter((b) => b.text() === '删除')).toHaveLength(0)
  })

  it('两套内置档案用途可辨，镜像档案不参与绑定与迁移', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '平台本地存储'), builtin: true, driver: 'local', purpose: 'tenant_fallback' },
      { ...storage(2, '平台本地镜像'), builtin: true, driver: 'local', purpose: 'platform_mirror', publicRead: false },
    ] as any)
    const w = mountPage()
    await flush()
    expect(w.text()).toContain('租户退路')
    expect(w.text()).toContain('平台镜像')
    // 两条内置档案的悬停提示各自可区分，管理员不会混为一谈
    const tips = w.findAllComponents({ name: 'ElTooltip' })
    expect(tips).toHaveLength(2)
    expect(tips[0].props('content')).toContain('系统自带的备用盘')
    expect(tips[1].props('content')).toContain('系统自带的备份盘')
    expect(tips[1].props('content')).toContain('自动运行')
    expect(tips[1].props('content')).toContain('本地镜像目录')
    // 悬停提示要点明目录真值源（配置项 key）与改后重启，否则管理员还来档案里找目录框
    expect(tips[0].props('content')).toContain('upload_dir')
    expect(tips[1].props('content')).toContain('mirror_dir')
    expect(tips[1].props('content')).toContain('重启')
    // 两套内置档案分别跳到各自的真值源，不能都指向 upload_dir
    const links = w.findAll('a.builtin-setting-link')
    expect(links).toHaveLength(2)
    expect(links[0].attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=upload_dir')
    expect(links[1].attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=mirror_dir')
    // 编辑镜像档案时说明的是「独立目录 + 不参与绑定」，不能沿用租户退路的
    // 「跟随本地上传目录」文案——两者目录真值源不同，文案串了会误导换目录的方式。
    const buttons = w.findAll('button').filter((b) => b.text() === '编辑')
    await buttons[1].trigger('click')
    await flush()
    expect(w.text()).toContain('本地镜像目录')
    expect(w.text()).toContain('不参与任何租户绑定')
    // 详情必写清目录继承自哪个配置项、改后要重启（不是「跟随系统设置」这种含糊说法）
    expect(w.text()).toContain('mirror_dir')
    expect(w.text()).toContain('重启')
    expect(w.text()).not.toContain('内置档案的目录跟随「系统设置 → 本地上传目录」')

    // 下拉里不能出现镜像档案：绑上它等于把备份副本变成主写入目标。
    expect(w.findAll('option').filter((o) => o.text() === '平台本地镜像')).toHaveLength(0)
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain("row.purpose === 'platform_mirror'")
  })

  it('三套内置档案职责可辨，平台自身存储可改连接信息但不参与绑定与迁移', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '平台自身存储'), builtin: true, driver: 'minio', purpose: 'platform_primary' },
      { ...storage(2, '平台本地存储'), builtin: true, driver: 'local', purpose: 'tenant_fallback' },
      { ...storage(3, '平台本地镜像'), builtin: true, driver: 'local', purpose: 'platform_mirror' },
    ] as any)
    const w = mountPage()
    await flush()

    // 三套 builtin 都要能在列表里一眼认出来，不能混成「两个本地盘 + 一个普通档案」
    expect(w.text()).toContain('平台自身存储')
    expect(w.text()).toContain('租户退路')
    expect(w.text()).toContain('平台镜像')
    const tips = w.findAllComponents({ name: 'ElTooltip' })
    expect(tips).toHaveLength(3)
    expect(tips[0].props('content')).toContain('p/ 平台对象')
    expect(tips[0].props('content')).toContain('不能绑定给企业')
    expect(tips[0].props('content')).toContain('不能参与迁移')

    // 平台自身存储 / 平台本地镜像都不能进绑定与迁移下拉；
    // 租户退路（tenant_fallback）本质就是可被绑定的本地盘，必须仍然可选。
    expect(w.findAll('option').filter((o) => o.text() === '平台本地存储').length).toBeGreaterThan(0)
    for (const name of ['平台自身存储', '平台本地镜像']) {
      expect(w.findAll('option').filter((o) => o.text() === name)).toHaveLength(0)
    }
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain("p.purpose !== 'platform_primary'")
    expect(src).toContain("p.purpose !== 'platform_mirror'")

    // 平台自身存储的编辑弹窗保留驱动可改与「重启 + 自检」提示，
    // 另外两套 builtin 仍锁 local；不能因为都是 builtin 就一刀切锁死。
    const edit = w.findAll('button').filter((b) => b.text() === '编辑')[0]
    await edit.trigger('click')
    await flush()
    expect(w.text()).toContain('连接信息在这里改')
    expect(w.text()).toContain('重启')
    expect(w.text()).toContain('不可删除')
    expect(src).toContain(":disabled=\"storageForm.builtin && storageForm.purpose !== 'platform_primary'\"")
  })

  it('CDN 分发的内置行不可删、不进绑定下拉，域名指向文件存储', async () => {
    vi.mocked(api.listDeliveryProfiles).mockResolvedValue([
      { ...delivery(1, '平台自身 CDN'), builtin: true, purpose: 'platform_primary', provider: 'aliyun', domain: '' },
      delivery(2, '客户 CDN'),
    ] as any)
    const w = mountPage()
    await flush()

    // 平台行的域名列不能是空白（会像「没配」），要说清它跟文件存储走
    expect(w.text()).toContain('平台自身 CDN')
    expect(w.text()).toContain('见「文件存储 → 平台自身存储」的访问基址')
    expect(w.text()).toContain('https://cdn.example.com')

    // 删除按钮对内置行禁用：后端也会拒，但界面上不该给人点
    const platformRow = w.findAll('.el-table-row').find((r) => r.text().includes('平台自身 CDN'))!
    const del = platformRow.findAll('button').find((b) => b.text() === '删除')!
    expect(del.attributes('disabled')).toBeDefined()

    // 绑定下拉（全局 + 企业）都不能出现平台自身 CDN：
    // 它的 domain 为空，绑给企业会让企业自己的 CDN 域名失效
    const options = w.findAll('option').map((o) => o.text())
    expect(options).not.toContain('平台自身 CDN')
    expect(options).toContain('客户 CDN')
  })

  it('路由与菜单必须同时登记（新增页面不能漏一处）', () => {
    const router = readFileSync('src/router/index.ts', 'utf8')
    const menu = readFileSync('src/layout/AdminLayout.vue', 'utf8')
    expect(router).toContain("path: 'platform/storage-profiles'")
    expect(menu).toContain("path: '/platform/storage-profiles'")
    // 企业自选页已下线：绑定是平台级动作，企业管理员能改它就能把对象写进别家客户的桶。
    // 反向断言防止有人「顺手把页面加回来」——页面一旦存在，后端那组 403 就成了摆设。
    expect(router).not.toContain("path: 'company-storage'")
    expect(menu).not.toContain("path: '/company-storage'")
    // 系统设置里不再内嵌「默认档案」选择块：它与档案模块的绑定页重复，
    // 且少了切换前核对 / force / 回滚，管理员在两处各选一套还以为会同步。
    // 只保留一条指路；企业管理行内「文件存储」入口仍在。
    const settings = readFileSync('src/views/platform/Settings.vue', 'utf8')
    const companies = readFileSync('src/views/Companies.vue', 'utf8')
    expect(settings).not.toContain('默认档案（推荐）')
    expect(settings).not.toContain('saveGlobalBinding')
    expect(settings).toContain('class="storage-module-link"')
    expect(companies).toContain("openProfiles(row)")
    expect(companies).toContain('saveCompanyBinding')

    // 文案与开关不能回退到「企业自选」：该页与 options 接口已下线（平台超管专属）。
    expect(settings).not.toContain('让企业在管理页自选')
    // 存储组的措辞要按实际生效范围写：平台对象（p/）+ 无绑定企业始终走这组启动配置，
    // 不是「没绑档案时才生效」。旧文案会把人误导到「选了默认档案就不用管这里」。
    expect(settings).toContain('p/ 平台对象')
    expect(settings).toContain('启动期配置')
    expect(settings).not.toContain('兼容旧部署')
    expect(settings).not.toContain('已选默认档案时不再生效')
    expect(settings).not.toContain('企业管理 → 自有桶')
    const profiles = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(profiles).toMatch(/label="公开读桶">\s*<el-switch[^>]*disabled/)
    expect(profiles).toContain('后端会拒绝 publicRead=false')
    // 迁移入口已下线：不许文案再把运维指向不存在的「转为文件存储」按钮。
    expect(profiles).not.toContain('转为文件存储')
    expect(profiles).not.toContain('给自己选「用哪一套」')
  })

  it('迁移任务有失败对象时可重试，全部成功才禁用启动按钮', async () => {
    vi.mocked(api.listMigrationJobs).mockResolvedValue([
      { id: 1, tenantId: 7, sourceProfileId: 1, targetProfileId: 2, status: 'done', done: 1, total: 3, failed: 2, error: '' },
      { id: 2, tenantId: 0, sourceProfileId: 1, targetProfileId: 2, status: 'done', done: 3, total: 3, failed: 0, error: '' },
    ] as any)
    const w = mountPage()
    await flush()
    // 失败对象还在：按钮必须是可点的「重试失败」，否则失败清单里的对象永远没入口重试。
    const retryBtn = w.findAll('button').find((b) => b.text() === '重试失败')!
    expect(retryBtn).toBeTruthy()
    expect(retryBtn.attributes('disabled')).toBeUndefined()
    await retryBtn.trigger('click')
    await flush()
    expect(api.runMigrationJob).toHaveBeenCalledWith(1)
    // 全部成功：按钮禁用，文案仍是「启动 / 续跑」。
    const startBtn = w.findAll('button').find((b) => b.text() === '启动 / 续跑')!
    expect(startBtn).toBeTruthy()
    expect(startBtn.attributes('disabled')).toBeDefined()
  })

  it('新增「平台自身存储」标签，把系统设置的存储/上传项收进同一模块', () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain('label="平台自身配置"')
    expect(src).not.toContain('label="平台兜底配置"')
    expect(src).toContain('name="platform"')
    expect(src).toContain('StorageSettingsPanel')
    // 面板顶部的「文件存在哪」必须取 platform_primary 那一行，不能拿列表第一项凑；
    // 也不接受租户退路（tenant_fallback）——它只是可绑定的本地盘，跟平台实际落点无关。
    expect(src).toContain(":platform-profile=\"platformPrimaryProfile\"")
    expect(src).toContain("p.purpose === 'platform_primary'")
    expect(src).not.toContain("p.purpose === 'tenant_fallback'")
    // 顶部「去文件存储修改」必须切到文件存储标签，否则管理员点了没反应只能自己找。
    expect(src).toContain("@goto-profiles=\"tab = 'storage'\"")
  })

  it('术语统一：界面上只出现「文件存储 / CDN 分发」，不再出现旧名「存储档案 / 分发档案」', () => {
    const files = [
      'src/views/platform/StorageProfiles.vue',
      'src/components/StorageSettingsPanel.vue',
      'src/components/StorageOverview.vue',
      'src/views/platform/CdnRefresh.vue',
      'src/views/Companies.vue',
      'src/utils/storageGuides.ts',
      // 文档曾滞后于界面改名，守门必须一并覆盖，否则「界面改了、README 还指死链」
      '../README.md',
      '../docs/storage-user-guide.md',
      '../docs/launch-checklist.md',
    ]
    for (const f of files) {
      const src = readFileSync(f, 'utf8')
      expect(src, `${f} 又出现了旧名「存储档案」`).not.toContain('存储档案')
      expect(src, `${f} 又出现了旧名「分发档案」`).not.toContain('分发档案')
      expect(src, `${f} 的厂商名缺空格，应为「CDN 分发」`).not.toContain('CDN分发')
    }
    const view = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(view).toContain('label="文件存储"')
    expect(view).toContain('label="CDN 分发"')
    const guides = readFileSync('src/utils/storageGuides.ts', 'utf8')
    expect(guides).toContain("term: '文件存储'")
    expect(guides).toContain("term: 'CDN 分发'")
  })

  it('僵尸字段 pathPrefix 不再出现在类型、表单与帮助文案里', () => {
    const files = [
      'src/api/storageProfiles.ts',
      'src/views/platform/StorageProfiles.vue',
      'src/utils/storageGuides.ts',
      'src/views/platform/CdnRefresh.vue',
    ]
    for (const f of files) {
      expect(readFileSync(f, 'utf8'), `${f} 又出现了已删除的僵尸字段 pathPrefix`).not.toContain('pathPrefix')
    }
  })

  it('僵尸字段 pathStyle 不再出现在前端类型与表单里', () => {
    const types = readFileSync('src/api/storageProfiles.ts', 'utf8')
    const view = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(types).not.toContain('pathStyle')
    expect(view).not.toContain('pathStyle')
    // 唯一的路径风格入口是 Host Header：文案要说清留空就是路径风格
    expect(view).toContain('留空=路径风格')
  })

  it('默认落在总览页签，每个页签都有一句"这里管什么"的说明', async () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain('?tab=xxx 直达指定页签')
    expect(src).toContain("|| 'overview'")
    expect(src).toContain('name="overview"')
    // CDN 缓存刷新已并进模块；迁移在绑定之前，与五步引导「先搬后切」一致
    expect(src).toContain('label="CDN 缓存刷新"')
    expect(src).toContain('name="refresh"')
    expect(src.indexOf('name="migration"')).toBeLessThan(src.indexOf('name="binding"'))
    expect((src.match(/class="tab-guide"/g) || []).length).toBeGreaterThanOrEqual(5)
    const w = mountPage()
    await flush()
    expect(w.findComponent({ name: 'CdnRefresh' }).exists()).toBe(true)
    expect(w.find('.storage-overview').exists()).toBe(true)
    // 总览默认是极简视图：一句结论 + 三层小图 + 一个动作，细节收进按需展开区
    expect(w.find('.ov-hero').exists()).toBe(true)
    expect(w.find('.ov-toggle').exists()).toBe(true)
    expect(w.text()).toContain('文件存储')
    expect(w.text()).toContain('管「文件存哪」')
    expect(w.text()).toContain('管「老文件怎么搬到新桶」')
  })

  it('新建文件存储弹窗按当前驱动给出官方入口、凭据取法与逐字段说明', async () => {
    const w = mountPage()
    await flush()
    await w.findAll('button').find((b) => b.text() === '新建文件存储')!.trigger('click')
    await flush()
    const dialog = w.find('.el-dialog')
    const vm: any = w.vm

    const hrefs = () => dialog.findAll('a[href]').map((a) => a.attributes('href') || '')

    // 厂商小抄默认收起：已经知道去哪拿凭据的人不必先读一屏说明
    expect(dialog.find('.vendor-guide-toggle').exists()).toBe(true)
    expect(dialog.find('.vendor-guide-toggle').attributes('aria-expanded')).toBe('false')
    expect(hrefs()).toHaveLength(0)
    await dialog.find('.vendor-guide-toggle').trigger('click')
    await flush()

    // MinIO 自建：给官网与部署文档，不编造一个在线控制台
    expect(hrefs()).toContain('https://min.io/docs/minio/linux/index.html')
    expect(dialog.text()).toContain('Root 用户名 / 密码在部署 MinIO 时自己设定')

    vm.storageForm.driver = 'oss'
    await flush()
    expect(hrefs()).toContain('https://oss.console.aliyun.com/')
    expect(hrefs()).toContain('https://ram.console.aliyun.com/manage/ak')
    expect(dialog.text()).toContain('地域与 Endpoint 对照')
    expect(dialog.text()).toContain('密钥怎么拿')

    vm.storageForm.driver = 's3'
    await flush()
    expect(hrefs()).toContain('https://console.cloud.tencent.com/cos')
    expect(hrefs()).toContain('https://dash.cloudflare.com/')
    expect(hrefs()).toContain('https://console.huaweicloud.com/obs/')
    expect(dialog.text()).toContain('s3.ap-northeast-1.amazonaws.com')

    // 每个字段旁的「?」说明必须真渲染出来，且点名关键的几个，不能靠"总数够多"蒙混
    const helpTitles = dialog.findAllComponents({ name: 'FieldHelp' }).map((h: any) => h.props('title'))
    expect(helpTitles.length).toBeGreaterThanOrEqual(8)
    for (const key of ['服务地址（Endpoint）', '存储桶名（Bucket）', '访问基址', 'AccessKey', 'SecretKey']) {
      expect(helpTitles, `${key} 缺字段说明`).toContain(key)
    }
    expect(dialog.text()).toContain('值来源')

    // 关掉再开必须回到收起状态：不能把上一次的展开状态带过来，
    // 否则「默认收起」只在第一次打开时成立。
    vm.storageDialog = false
    await flush()
    await w.findAll('button').find((b) => b.text() === '新建文件存储')!.trigger('click')
    await flush()
    const reopened = w.find('.el-dialog')
    expect(reopened.find('.vendor-guide-toggle').attributes('aria-expanded')).toBe('false')
    expect(reopened.findAll('a[href]')).toHaveLength(0)
  })

  it('CDN 分发弹窗给出厂商控制台，Cloudflare 点名 API Token 与 Zone ID', async () => {
    const w = mountPage()
    await flush()
    await w.findAll('button').find((b) => b.text() === '新建CDN 分发')!.trigger('click')
    await flush()
    const dialog = w.find('.el-dialog')
    const hrefs = () => dialog.findAll('a[href]').map((a) => a.attributes('href') || '')
    // 与存储弹窗同口径：默认收起，点开才铺链接
    expect(hrefs()).toHaveLength(0)
    await dialog.find('.vendor-guide-toggle').trigger('click')
    await flush()
    expect(hrefs()).toContain('https://cdn.console.aliyun.com/')
    const vm: any = w.vm
    vm.deliveryForm.provider = 'cloudflare'
    await flush()
    expect(hrefs()).toContain('https://dash.cloudflare.com/profile/api-tokens')
    expect(dialog.text()).toContain('刷新缓存文档')
    expect(dialog.text()).toContain('Zone ID')
    expect(dialog.text()).toContain('API Token')
    const helpTitles = dialog.findAllComponents({ name: 'FieldHelp' }).map((h: any) => h.props('title'))
    expect(helpTitles.length).toBeGreaterThanOrEqual(6)
    expect(helpTitles).toContain('CDN 厂商')
    expect(helpTitles).toContain('资源标识')
  })

  it('空表给出引导文案，不让管理员对着白板猜下一步', () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain('<template #empty>')
    expect(src).toContain('还没有文件存储')
    expect(src).toContain('还没有CDN 分发')
  })
})
