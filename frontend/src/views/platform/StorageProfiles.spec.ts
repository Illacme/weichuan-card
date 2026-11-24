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
  // 新「主存储」标签会挂载 StorageSettingsPanel，它自己拉系统设置；
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
    const createButton = w.findAll('button').find((b) => b.text() === '创建并启动')!
    await createButton.trigger('click')
    await flush()
    expect(api.createMigrationJob).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalledWith('源与目标不能相同')
  })

  it('内置本地档案标记为「内置」，编辑时驱动锁定并说明兜底用途', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '应急存储'), builtin: true, driver: 'local' },
    ] as any)
    const w = mountPage()
    await flush()
    expect(w.text()).toContain('应急存储')
    expect(w.text()).toContain('内置')
    // 说明移入标签悬停提示：列表保持清爽，但要钉死悬停会显示「系统自带、别删」
    const tip1 = w.findComponent({ name: 'ElTooltip' })
    expect(tip1.exists()).toBe(true)
    expect(tip1.props('content')).toContain('系统自带的应急存储')
    expect(tip1.props('content')).toContain('请勿删除')
    // 内置档案改不动目录/基址，必须给一条直达系统设置的链接，
    // 否则管理员只能在编辑弹窗里对着灰字段干瞪眼。
    expect(tip1.props('content')).toContain('服务器上传目录')
    expect(tip1.props('content')).toContain('这里改不了')
    const builtinLink = w.find('a.builtin-setting-link')
    expect(builtinLink.exists()).toBe(true)
    expect(builtinLink.attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=upload_dir')
    expect(builtinLink.text()).toContain('改上传目录')
    const edit = w.findAll('button').find((b) => b.text() === '编辑')!
    await edit.trigger('click')
    await flush()
    // 编辑弹窗顶部要有「应急存储 vs 备份存储」的对照说明，管理员一眼看懂两者区别
    expect(w.text()).toContain('这是系统自带的文件存储，通常不用改动')
    expect(w.text()).toContain('系统预留的应急存储')
    // 弹窗顶部要把「应急存储 vs 备份存储」一次讲清：名字已直白，别再用「保险柜/备用盘」另行造词
    expect(w.text()).toContain('备份存储')
    expect(w.text()).toContain('只进不出')
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
    // 内置档案不渲染删除按钮：不可删的意思已经由名称列的「内置」徽标及其
    // 悬停提示（上面已断言「请勿删除」）说清，操作列再重复一遍只是占地方。
    // 之前用 danger link + disabled，禁用态仍是红字，管理员以为能点却点不动。
    // 只看内置档案这一行：它不该有可点的删除按钮（别的企业档案仍有）
    const builtinRow = w.findAll('.el-table-row').find((r) => r.text().includes('应急存储'))!
    expect(builtinRow.text()).not.toContain('系统自带 · 不可删')
    expect(builtinRow.findAll('button').filter((b) => b.text() === '删除')).toHaveLength(0)
  })

  it('两套内置档案用途可辨，镜像档案不参与绑定与迁移', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '应急存储'), builtin: true, driver: 'local', purpose: 'tenant_fallback' },
      { ...storage(2, '备份存储'), builtin: true, driver: 'local', purpose: 'platform_mirror', publicRead: false },
    ] as any)
    const w = mountPage()
    await flush()
    // 角色徽标取代了原来「租户退路 / 平台镜像」两个和名字看不懂的标签
    expect(w.text()).toContain('应急')
    expect(w.text()).toContain('备份')
    // 两条内置档案的悬停提示各自可区分，管理员不会混为一谈
    const tips = w.findAllComponents({ name: 'ElTooltip' })
      // 列表里还有「连通性」的自检时间提示，这里只挑档案用途说明那一批。
      .filter((t) => !String(t.props('content')).includes('自检'))
    expect(tips).toHaveLength(2)
    expect(tips[0].props('content')).toContain('系统自带的应急存储')
    expect(tips[1].props('content')).toContain('系统自带的备份存储')
    expect(tips[1].props('content')).toContain('自动运行')
    expect(tips[1].props('content')).toContain('备份存储目录')
    // 悬停提示要点明目录真值源（配置项 key）与改后重启，否则管理员还来档案里找目录框
    expect(tips[0].props('content')).toContain('upload_dir')
    expect(tips[1].props('content')).toContain('mirror_dir')
    expect(tips[1].props('content')).toContain('重启')
    // 两套内置档案分别跳到各自的真值源，不能都指向 upload_dir
    const links = w.findAll('a.builtin-setting-link')
    expect(links).toHaveLength(2)
    expect(links[0].attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=upload_dir')
    expect(links[1].attributes('href')).toBe('/platform/storage-profiles?tab=platform&focus=mirror_dir')
    expect(links[0].text()).toContain('改上传目录')
    expect(links[1].text()).toContain('改备份目录')
    // 编辑镜像档案时说明的是「独立目录 + 不参与绑定」，不能沿用租户退路的
    // 「跟随服务器上传目录」文案——两者目录真值源不同，文案串了会误导换目录的方式。
    const buttons = w.findAll('button').filter((b) => b.text() === '编辑')
    await buttons[1].trigger('click')
    await flush()
    expect(w.text()).toContain('备份存储目录')
    expect(w.text()).toContain('不参与任何租户绑定')
    // 详情必写清目录继承自哪个配置项、改后要重启（链接名也要点名具体设置项）
    expect(w.text()).toContain('mirror_dir')
    expect(w.text()).toContain('重启')
    expect(w.text()).not.toContain('内置档案的目录跟随「系统设置 → 服务器上传目录」')

    // 下拉里不能出现镜像档案：绑上它等于把备份副本变成主写入目标。
    expect(w.findAll('option').filter((o) => o.text() === '备份存储')).toHaveLength(0)
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain("row.purpose === 'platform_mirror'")
  })

  it('三套内置档案职责可辨，主存储可改连接信息但不参与绑定与迁移', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '主存储'), builtin: true, driver: 'minio', purpose: 'platform_primary' },
      { ...storage(2, '应急存储'), builtin: true, driver: 'local', purpose: 'tenant_fallback' },
      { ...storage(3, '备份存储'), builtin: true, driver: 'local', purpose: 'platform_mirror' },
    ] as any)
    const w = mountPage()
    await flush()

    // 三套 builtin 都要能在列表里一眼认出来，不能混成「两个本地盘 + 一个普通档案」
    expect(w.text()).toContain('主存储')
    // 角色徽标取代了原来「租户退路 / 平台镜像」两个和名字看不懂的标签
    expect(w.text()).toContain('应急')
    expect(w.text()).toContain('备份')
    const tips = w.findAllComponents({ name: 'ElTooltip' })
      .filter((t) => !String(t.props('content')).includes('自检'))
    expect(tips).toHaveLength(3)
    expect(tips[0].props('content')).toContain('p/ 平台对象')
    expect(tips[0].props('content')).toContain('不能绑定给企业')
    expect(tips[0].props('content')).toContain('不能参与迁移')

    // 云驱动的主存储连接信息就在本行「编辑」里，upload_dir 与它无关：
    // 若也给它一条「改上传目录」，管理员照着改完会发现对象地址纹丝不动。
    const primaryRow = w.findAll('.el-table-row').find((r) => r.text().includes('主存储'))!
    expect(primaryRow.find('a.builtin-setting-link').exists()).toBe(false)
    // 它同样是内置行，不能因为「没有链接」就掉进 v-else 渲染出删除按钮。
    expect(primaryRow.findAll('button').filter((b) => b.text() === '删除')).toHaveLength(0)
    // 另外两套本地盘仍各有直达链接，文案点名到底改哪个设置项。
    const builtinLinks = w.findAll('a.builtin-setting-link')
    expect(builtinLinks).toHaveLength(2)
    expect(builtinLinks[0].text()).toContain('改上传目录')
    expect(builtinLinks[1].text()).toContain('改备份目录')

    // 主存储 / 备份存储都不能进绑定与迁移下拉；
    // 租户退路（tenant_fallback）本质就是可被绑定的本地盘，必须仍然可选。
    expect(w.findAll('option').filter((o) => o.text() === '应急存储').length).toBeGreaterThan(0)
    for (const name of ['主存储', '备份存储']) {
      expect(w.findAll('option').filter((o) => o.text() === name)).toHaveLength(0)
    }
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain("p.purpose !== 'platform_primary'")
    expect(src).toContain("p.purpose !== 'platform_mirror'")

    // 主存储的编辑弹窗保留驱动可改；连接信息保存即生效，不能再叫管理员重启。
    // 另外两套 builtin 仍锁 local；不能因为都是 builtin 就一刀切锁死。
    const edit = w.findAll('button').filter((b) => b.text() === '编辑')[0]
    await edit.trigger('click')
    await flush()
    expect(w.text()).toContain('连接信息在这里改')
    expect(w.text()).toContain('保存即生效')
    expect(w.text()).not.toContain('保存后必须重启')
    expect(w.text()).not.toContain('再重启后端')
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
    expect(w.text()).toContain('见「文件存储 → 主存储」的访问基址')
    expect(w.text()).toContain('https://cdn.example.com')

    // 内置行根本不渲染删除按钮：禁用态红字仍会引诱点击，点不动只会让人困惑；
    // 「不可删」由名称列的「内置」徽标及悬停提示说一次就够，操作列不再重复。
    const platformRow = w.findAll('.el-table-row').find((r) => r.text().includes('平台自身 CDN'))!
    expect(platformRow.findAll('button').filter((b) => b.text() === '删除')).toHaveLength(0)
    expect(platformRow.text()).not.toContain('系统自带 · 不可删')
    expect(platformRow.find('.builtin-tag').exists()).toBe(true)
    const tips = platformRow.findAllComponents({ name: 'ElTooltip' })
      .filter((t: { props: (key: string) => unknown }) => String(t.props('content')).includes('平台自身 CDN'))
    expect(tips).toHaveLength(1)
    expect(tips[0].props('content')).toContain('不可删除')

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
    // 私有桶现在可配：开关放开，只有平台自身那份仍锁死公开读。
    expect(profiles).toContain('v-model="storageForm.publicRead"')
    // 必须整段盯着开关：只断言 v-model 存在的话，贴回一个静态 disabled 仍会全绿。
    const sw = profiles.match(/<el-switch\s+v-model="storageForm\.publicRead"[\s\S]*?\/>/)?.[0] ?? ''
    expect(sw, '公开读开关块没找到，守门空转').toBeTruthy()
    expect(sw).not.toMatch(/\sdisabled(?:\s|=|>)/)
    expect(sw).toContain(':disabled=')
    expect(profiles).toContain(`storageForm.builtin && storageForm.purpose === 'platform_primary'`)
    expect(profiles).toContain('私有桶已开启：访客地址不变')
    expect(profiles).not.toContain('当前只支持公开读桶')
    expect(profiles).not.toContain('后端会拒绝 publicRead=false')
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

  it('新增「主存储」标签，把系统设置的存储/上传项收进同一模块', () => {
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
      // 后端 README 曾是术语漂移的漏网之鱼：它不在原清单里，界面改了它仍指
      // 「后台存储档案」这种不存在的菜单。守门必须覆盖后端文档。
      '../backend/README.md',
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
    // 手动清缓存是运维动作，排在五步配置流程之后，不打断「建档案→迁移→切绑定」
    expect(src.indexOf('name="binding"')).toBeLessThan(src.indexOf('name="refresh"'))
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
    await w.findAll('button').find((b) => b.text() === '新建 CDN 分发')!.trigger('click')
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

  it('档案列表默认只给业务列，技术列按需展开；并直接标出谁在用', async () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    // 默认隐藏靠 v-if 真渲染控制，不是把列做成空内容
    expect(src).toContain('v-if="showTechCols" prop="accessKeyMasked" label="AK 掩码"')
    expect(src).toContain('v-if="showTechCols" prop="version" label="版本"')
    // 使用中标签必须真渲染在档案行上，不能只留一个没人调用的函数
    expect(src).toContain('v-if="usageLabel(row.id, \'storage\')"')
    expect(src).toContain('v-if="usageLabel(row.id, \'delivery\')"')
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    expect(vm.showTechCols).toBe(false)
    // 使用中标签读已保存的绑定，不读表单草稿
    expect(vm.usageLabel(1, 'storage')).toBe('全局默认')
    expect(vm.usageLabel(3, 'delivery')).toBe('全局默认')
    expect(vm.usageLabel(999, 'storage')).toBe('')
    const toggle = w.findAll('button').find((b) => b.text() === '更多列')!
    await toggle.trigger('click')
    await flush()
    expect(toggle.text()).toBe('收起技术列')
    expect(vm.showTechCols).toBe(true)
  })

  it('迁移表单按三栏排布并先提示试运行，空任务表给出下一步', () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain('class="migration-fields"')
    // 创建即启动后，「先试运行核对数量」的指引必须跟着改，否则会让人去找已经不存在的第二步。
    expect(src).toContain('勾「试运行」后点「创建并启动」')
    expect(src).toContain('还没有迁移任务')
  })

  it('绑定来源显示中文语义，不把后端枚举直接抛给管理员', async () => {
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    expect(vm.sourceLabel('global')).toBe('全局')
    expect(vm.sourceLabel('inherit')).toBe('继承')
    expect(vm.sourceLabel('platform')).toBe('平台自带')
    expect(vm.sourceLabel('profile')).toBe('单独绑定')
  })

  it('两个列表的空表文案点明「空表＝没加载出来」，而不是「还没有」', () => {
    const src = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    expect(src).toContain('<template #empty>')
    // 系统自带 3 套文件存储 + 1 条平台 CDN 都是启动时必生成的。
    // 真为空只可能是没读到数据，写成「还没有…」会让管理员以为一切正常、继续等。
    expect(src).toContain('读不到文件存储列表')
    expect(src).toContain('读不到 CDN 分发列表')
    expect(src).not.toContain('还没有文件存储')
    expect(src).not.toContain('还没有 CDN 分发')
  })

  it('厂商列与迁移状态显示中文，不把后端枚举抛给管理员', async () => {
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    // 同一个模块里「CDN 缓存刷新」显示「阿里云」，列表不能显示 aliyun。
    expect(vm.providerLabel('aliyun')).toBe('阿里云')
    expect(vm.providerLabel('cloudfront')).toBe('AWS CloudFront')
    expect(vm.providerLabel('none')).toBe('不启用 CDN')
    expect(vm.providerLabel('')).toBe('不启用 CDN')
    // 迁移状态：running/done/failed 是给代码看的。
    expect(vm.migrationStatus('running').text).toBe('进行中')
    expect(vm.migrationStatus('done').text).toBe('已完成')
    expect(vm.migrationStatus('failed').text).toBe('失败')
    expect(vm.migrationStatus('canceled').text).toBe('已取消')
    // 后端将来加新状态：不许渲染成空标签，原样回显。
    expect(vm.migrationStatus('paused').text).toBe('paused')
    expect(vm.migrationStatus('').text).toBe('未知')
  })

  it('迁移进度自动刷新是真的：有 running 任务且在迁移页签才轮询，离开即停', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(api.listMigrationJobs).mockResolvedValue([
        { id: 1, tenantId: 0, sourceProfileId: 1, targetProfileId: 2, status: 'running', done: 1, total: 3, failed: 0, error: '' },
      ] as any)
      const w = mountPage()
      await flush()
      const vm: any = w.vm
      const calls = () => vi.mocked(api.listMigrationJobs).mock.calls.length

      // 不在迁移页签：不该有任何轮询，否则后台页签也在偷偷打接口。
      const base = calls()
      await vi.advanceTimersByTimeAsync(9000)
      expect(calls()).toBe(base)

      vm.tab = 'migration'
      await flush()
      await vi.advanceTimersByTimeAsync(3000)
      expect(calls(), '弹「进度会自动刷新」就必须真的刷新').toBeGreaterThan(base)

      // 离开页签立刻停：不能留着定时器继续打接口。
      vm.tab = 'storage'
      await flush()
      const afterLeave = calls()
      await vi.advanceTimersByTimeAsync(9000)
      expect(calls()).toBe(afterLeave)

      w.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  it('迁移任务全部结束时不轮询', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(api.listMigrationJobs).mockResolvedValue([
        { id: 2, tenantId: 0, sourceProfileId: 1, targetProfileId: 2, status: 'done', done: 3, total: 3, failed: 0, error: '' },
      ] as any)
      const w = mountPage()
      await flush()
      const vm: any = w.vm
      vm.tab = 'migration'
      await flush()
      const base = vi.mocked(api.listMigrationJobs).mock.calls.length
      await vi.advanceTimersByTimeAsync(9000)
      expect(vi.mocked(api.listMigrationJobs).mock.calls.length).toBe(base)
      w.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  it('企业覆盖表只显示真实企业，tenantId=0 的全局行不许混进来', async () => {
    // 后端把全局绑定也放进了 companies，前端不滤就会多出一行「企业 ID 0 / #0」。
    vi.mocked(api.listStorageBindings).mockResolvedValue({
      global: { storageProfileId: 1, deliveryProfileId: 3, source: 'profile' },
      companies: [
        { tenantId: 0, storageProfileId: 1, deliveryProfileId: 3, source: 'global' },
        { tenantId: 9, storageProfileId: 2, deliveryProfileId: 0, source: 'inherit' },
      ],
      storageProfiles: {}, deliveryProfiles: {},
    } as any)
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    expect(vm.companyBindings.map((r: any) => r.tenantId)).toEqual([9])
    // 总览的「谁在用」计数必须跟着只数真实企业。
    expect(vm.companyBindings.length).toBe(1)
    w.unmount()
  })

  it('页签名与页内说明标题必须同名，避免同一页出现两个名字', () => {
    const page = readFileSync('src/views/platform/StorageProfiles.vue', 'utf8')
    const m = page.match(/<el-tab-pane label="([^"]+)" name="platform">/)
    expect(m, '找不到 platform 页签').toBeTruthy()
    const tabLabel = m![1]
    const guides = readFileSync('src/utils/storageGuides.ts', 'utf8')
    const title = guides.match(/platform: \{ title: '([^']+)'/)![1]
    expect(title).toBe(tabLabel)
    // 访问基址在「文件存储」里改：说明必须把它指向那一页，不能写成自己的职责。
    const desc = guides.match(/platform: \{ title: '[^']+', desc: '([^']+)'/)![1]
    expect(desc).not.toContain('备份开关和访问基址')
    expect(desc).toContain('「文件存储」')
  })

  it('创建迁移任务即自动启动，不用再点一次「启动 / 续跑」', async () => {
    vi.mocked(api.createMigrationJob).mockResolvedValue({ id: 42 } as any)
    vi.mocked(api.runMigrationJob).mockResolvedValue({} as any)
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    vm.newJob.sourceProfileId = 1
    vm.newJob.targetProfileId = 2
    await vm.createJob()
    await flush()
    expect(api.createMigrationJob).toHaveBeenCalled()
    expect(api.runMigrationJob, '建完还让人再点一次是多余步骤').toHaveBeenCalledWith(42)
    expect(vi.mocked(ElMessage.success).mock.calls.some((c) => String(c[0]).includes('任务已启动'))).toBe(true)
    w.unmount()
  })

  it('启动失败时仍告诉用户「任务已创建」，不让人以为白做一次', async () => {
    vi.mocked(api.createMigrationJob).mockResolvedValue({ id: 43 } as any)
    vi.mocked(api.runMigrationJob).mockRejectedValue(new Error('boom') as never)
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    vm.newJob.sourceProfileId = 1
    vm.newJob.targetProfileId = 2
    await vm.createJob()
    await flush()
    const warned = vi.mocked(ElMessage.warning).mock.calls.map((c) => String(c[0])).join('|')
    expect(warned).toContain('任务已创建')
    expect(warned).toContain('启动失败')
    w.unmount()
  })

  it('连通性标签带「上次自检」时间：没有时间戳的「已验证」不可信', async () => {
    const w = mountPage()
    await flush()
    const vm: any = w.vm
    // 用本地时间字符串，避免时区把断言变成 flaky
    expect(vm.probeHint({ status: 'ok', lastProbeAt: '2026-11-23T12:05:00' })).toBe('上次自检：2026-11-23 12:05')
    expect(vm.probeHint({ status: 'ok', lastProbeAt: null })).toContain('没有记录到时间')
    expect(vm.probeHint({ status: 'unknown' })).toContain('还没自检过')
    // 后端建任务写的是 created，漏映射会直接显示英文。
    expect(vm.migrationStatus('created').text).toBe('待启动')
    w.unmount()
  })

  it('角色徽标 + 一句职责 + 位置列：三套内置存储不用看说明也能分清', async () => {
    vi.mocked(api.listStorageProfiles).mockResolvedValue([
      { ...storage(1, '主存储'), builtin: true, driver: 'local', purpose: 'platform_primary', localDir: '/data/upload' },
      { ...storage(2, '应急存储'), builtin: true, driver: 'local', purpose: 'tenant_fallback' },
      { ...storage(3, '备份存储'), builtin: true, driver: 'local', purpose: 'platform_mirror' },
      { ...storage(4, '主站 OSS'), driver: 'oss', bucket: 'my-bucket' },
    ] as any)
    const w = mountPage()
    await flush()
    const vm: any = w.vm

    // 业务存储排在内置机制前面：日常维护的是自己建的那几套。
    expect(vm.orderedStorages.map((r: any) => r.id)).toEqual([4, 1, 2, 3])
    // 名字本身就是角色，不再额外挂徽标（原来「名字 + 徽标」把同一件事说了两遍）。
    expect(vm.orderedStorages.map((r: any) => r.name)).toEqual(['主站 OSS', '主存储', '应急存储', '备份存储'])

    const text = w.text()
    // 每行都有一句「它替系统扛什么」，不用悬停也不用翻文档。
    expect(text).toContain('文件写在这里')
    expect(text).toContain('还能落到这里')
    expect(text).toContain('只写不读')

    // 本地盘没有 endpoint / bucket，三列全空；合成一列「位置」才说得清文件在哪。
    expect(vm.locationOf({ driver: 'local', localDir: '/data/upload' })).toBe('本地磁盘 · /data/upload')
    expect(vm.locationOf({ driver: 'local' })).toBe('本地磁盘 · 目录继承系统设置')
    expect(vm.locationOf({ driver: 'oss', bucket: 'my-bucket' })).toBe('阿里云 OSS · my-bucket')
    expect(text).toContain('本地磁盘 · /data/upload')
    // 技术列默认收起：默认视图不该被 Endpoint / Bucket 占掉半屏。
    expect(text).not.toContain('服务地址')
    w.unmount()
  })

  it('三个内置存储不再用旧默认名，避免「两个平台、两个本地」再次出现', () => {
    const files = [
      'src/views/platform/StorageProfiles.vue',
      'src/views/platform/Settings.vue',
      'src/views/Companies.vue',
      'src/components/StorageOverview.vue',
      'src/components/StorageSettingsPanel.vue',
      'src/utils/storageGuides.ts',
      'src/api/backup.ts',
      'src/api/audit.ts',
      'src/components/SettingRow.vue',
    ]
    // 三类旧名都要盯：① 两代内置存储默认名（第一代是三个近义地名，第二代多加了
    // 「盘」这层概念）；② 退役的设置项名；③ 面板里自己维护的那套「友好名」。
    // 第三类最容易漏：后端 registry 改了，FRIENDLY 映射还留着旧名，同一个 key
    // 在两个页面显示两个名字——这正是这轮体检抓到的实错。
    const legacy = [
      '平台自身存储', '平台本地存储', '平台本地镜像',
      '平台主存储', '本地应急盘', '本地备份盘',
      '本地镜像目录', '本地镜像总开关', '本地备份镜像',
      '本地备份开关', '本地备份目录', '自动本地备份',
      '本地镜像开关', '本地镜像总闸', '本地备份',
    ]
    // 只扫会渲染出去的代码，不扫注释：注释里解释「旧名叫什么」是必要的，
    // 朴素 Contains 会把这种解释判成违规（项目里踩过同样的坑）。
    const stripComments = (src: string) => src.split('\n')
      .filter((line) => {
        const t = line.trim()
        return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*') || t.startsWith('<!--'))
      })
      .join('\n')
    for (const f of files) {
      const s = stripComments(readFileSync(f, 'utf8'))
      for (const name of legacy) {
        expect(s, `${f} 又出现了旧默认名「${name}」`).not.toContain(name)
      }
    }
  })
})
