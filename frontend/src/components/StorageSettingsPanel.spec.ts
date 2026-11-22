import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'

vi.mock('@/api/settings', () => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(async () => ({ code: 0, data: { restartKeys: [] } })),
}))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn() },
}))

import { getSettings, updateSettings } from '@/api/settings'
import { ElMessage, ElMessageBox } from 'element-plus'
import StorageSettingsPanel from '@/components/StorageSettingsPanel.vue'
import { flush } from '@/test/elStubs'

// SettingRow 内部控件的最小桩。与 Settings.spec.ts 同构：
// el-select 不能声明 emits:click，否则 change 打到空气上。
const ElAlert = defineComponent({
  name: 'ElAlert', props: ['title', 'description', 'type'],
  template: `<div class="el-alert">{{ title }} {{ description }}<slot /></div>`,
})
const ElInput = defineComponent({
  name: 'ElInput', props: ['modelValue', 'placeholder', 'type', 'disabled'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :type="type || 'text'" :disabled="disabled" :placeholder="placeholder" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton', props: ['type', 'loading', 'disabled'],
  template: `<button class="el-button" :disabled="disabled"><slot /></button>`,
})
const ElTag = defineComponent({ name: 'ElTag', template: `<span class="el-tag"><slot /></span>` })
const ElSelect = defineComponent({
  name: 'ElSelect', props: ['modelValue', 'disabled'], emits: ['update:modelValue', 'change'],
  template: `<select class="el-select" :disabled="disabled" :value="modelValue" @change="$emit('change', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption', props: ['label', 'value'], template: `<option :value="value">{{ label }}</option>`,
})
const ElInputNumber = defineComponent({
  name: 'ElInputNumber', props: ['modelValue', 'min', 'disabled'], emits: ['update:modelValue', 'change'],
  template: `<input class="el-input-number" type="number" :disabled="disabled" :value="modelValue" @input="$emit('change', Number($event.target.value))" />`,
})
const ElSwitch = defineComponent({
  name: 'ElSwitch', props: ['modelValue'], template: `<button class="el-switch" />`,
})
const ElTooltip = defineComponent({
  name: 'ElTooltip', props: ['content'], template: `<span class="el-tooltip"><slot /><span class="el-tooltip__content">{{ content }}</span></span>`,
})
const stubs = {
  'el-alert': ElAlert, 'el-input': ElInput, 'el-button': ElButton, 'el-tag': ElTag,
  'el-select': ElSelect, 'el-option': ElOption, 'el-input-number': ElInputNumber,
  'el-switch': ElSwitch, 'el-tooltip': ElTooltip,
}

const item = (key: string, name: string, group: string, value: string, extra: Record<string, any> = {}) => ({
  key, name, group, type: 'string', value, description: name, secret: false,
  restart: false, readOnly: false, envVar: '', options: [], configured: !!value, ...extra,
})

const ITEMS = [
  item('storage_driver', '存储方式', 'storage', 'local', { options: ['local', 'minio', 'oss', 's3'], restart: true }),
  item('upload_dir', '本地上传目录', 'storage', '/srv/uploads', { envVar: 'UPLOAD_DIR', restart: true }),
  item('storage_endpoint', '对象存储端点', 'storage', '', { envVar: 'MINIO_ENDPOINT', restart: true }),
  item('storage_bucket', 'Bucket', 'storage', '', { restart: true }),
  item('storage_access_key', 'AccessKey', 'storage', '', { restart: true }),
  item('storage_secret_key', 'SecretKey', 'storage', '', { secret: true, restart: true }),
  item('storage_use_ssl', '使用 HTTPS', 'storage', 'true', { type: 'bool', restart: true }),
  item('storage_region', '区域', 'storage', '', { restart: true }),
  item('storage_public', '公开访问', 'storage', 'true', { type: 'bool', restart: true }),
  item('upload_base_url', '上传基址', 'storage', '', { restart: true }),
  item('cdn_enabled', '启用 CDN', 'storage', 'false', { type: 'bool', restart: true }),
  item('cdn_provider', 'CDN 厂商', 'storage', 'aliyun', { restart: true }),
  item('cdn_access_key_id', 'CDN AK', 'storage', '', { restart: true }),
  item('cdn_access_key_secret', 'CDN SK', 'storage', '', { secret: true, restart: true }),
  item('cdn_resource_id', '资源标识', 'storage', '', { restart: true }),
  item('cdn_endpoint', '刷新端点', 'storage', '', { restart: true }),
  item('local_mirror_enabled', '镜像总开关', 'storage', 'true', { type: 'bool' }),
  item('mirror_dir', '镜像目录', 'storage', '/srv/mirror', { restart: true }),
  item('storage_quota_mb', '租户配额', 'storage', '0', { type: 'int' }),
  item('upload_max_mb', '单张上限', 'upload', '10', { type: 'int' }),
  item('upload_allow_ext', '允许类型', 'upload', 'png,jpg', { type: 'string_slice' }),
  item('upload_video_max_mb', '视频上限', 'upload', '100', { type: 'int' }),
  item('presign_put_ttl_minutes', '预签名时效', 'upload', '15', { type: 'int' }),
  item('sign_get_ttl_minutes', '签名时效', 'upload', '30', { type: 'int' }),
]

function mountPanel() {
  return mount(StorageSettingsPanel, { global: { stubs, directives: { loading: {} } } })
}
function rowOf(w: any, key: string) {
  const r = w.findAll('.row').find((x: any) => x.text().includes(key))
  expect(r, `未找到配置项行: ${key}`).toBeTruthy()
  return r!
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSettings).mockImplementation(async () => ({
    code: 0, data: ITEMS.map((it) => ({ ...it })),
  }) as any)
  vi.mocked(updateSettings).mockImplementation(async () => ({ code: 0, data: { restartKeys: [] } }) as any)
  vi.mocked(ElMessageBox.confirm).mockResolvedValue('confirm' as any)
})

describe('平台自身存储配置面板', () => {
  it('集中展示 storage / upload 两组全部配置项，不混入其它组', async () => {
    const w = mountPanel()
    await flush()
    expect(getSettings).toHaveBeenCalled()
    const text = w.text()
    expect(text).toContain('存储方式')
    expect(text).toContain('本地上传目录')
    expect(text).toContain('镜像目录')
    expect(text).toContain('租户配额') // 原页面的未分区项也不能丢
    expect(text).toContain('单张上限')
    expect(text).toContain('签名时效')
    expect(text).not.toContain('brand_name')
    expect(text).not.toContain('jwt_key')
    // 六个分区齐全
    expect(w.findAll('.config-section')).toHaveLength(7)
  })

  it('顶部说明讲清谁用这组配置、改动要重启，并指回本模块其它标签', async () => {
    const w = mountPanel()
    await flush()
    const text = w.text()
    expect(text).toContain('平台自身存储的启动配置')
    expect(text).toContain('需重启服务')
    expect(text).toContain('尚未绑定档案的企业')
    expect(text).toContain('存储档案 / 绑定关系')
    // 本地模式下平台实例与「平台本地存储」档案是同一块盘——这句是用户那次疑问的根因，
    // 文案必须点明，否则管理员仍会以为这是两份配置
    expect(text).toContain('与「存储档案」里的「平台本地存储」是同一块盘')
  })

  it('依赖联动：切到 minio 后本地目录置灰，云凭据转亮', async () => {
    const w = mountPanel()
    await flush()
    expect(rowOf(w, 'upload_dir').classes()).not.toContain('row-dim')
    const sel = rowOf(w, 'storage_driver').find('select.el-select')
    await sel.setValue('minio')
    await nextTick()
    // 面板只置灰不隐藏：从内置档案跳来定位 upload_dir 时不会扑空
    expect(rowOf(w, 'upload_dir').classes()).toContain('row-dim')
    expect(rowOf(w, 'storage_endpoint').classes()).not.toContain('row-dim')
  })

  it('保存只提交改动项，值以字符串提交', async () => {
    const w = mountPanel()
    await flush()
    const input = rowOf(w, 'upload_max_mb').find('input.el-input-number')
    await input.setValue('20')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存存储配置')!.trigger('click')
    await flush()
    expect(updateSettings).toHaveBeenCalledTimes(1)
    const payload = vi.mocked(updateSettings).mock.calls[0][0] as Record<string, string>
    expect(payload).toEqual({ upload_max_mb: '20' })
  })

  it('敏感项点清除提交空串，保存后回到「已配置」且不把掩码当值', async () => {
    const w = mountPanel()
    await flush()
    // 夹具里 secret 项原值为空（后端给掩码，界面留空），直接改值模拟重新填写
    const pwd = rowOf(w, 'storage_secret_key').find('input.el-input')
    await pwd.setValue('new-secret')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存存储配置')!.trigger('click')
    await flush()
    const payload = vi.mocked(updateSettings).mock.calls[0][0] as Record<string, string>
    expect(payload.storage_secret_key).toBe('new-secret')
    expect(ElMessage.success).toHaveBeenCalled()
  })

  it('保存返回需重启项时给出常驻提示', async () => {
    vi.mocked(updateSettings).mockImplementationOnce(async () => ({
      code: 0, data: { restartKeys: ['storage_driver'] },
    }) as any)
    const w = mountPanel()
    await flush()
    const sel = rowOf(w, 'storage_driver').find('select.el-select')
    await sel.setValue('minio')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存存储配置')!.trigger('click')
    await flush()
    expect(w.text()).toContain('刚保存的改动需要重启才生效')
    expect(w.text()).toContain('存储方式')
  })

  it('破坏性改动弹确认，取消则不提交且草稿保留', async () => {
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce(new Error('cancel'))
    const w = mountPanel()
    await flush()
    const sel = rowOf(w, 'storage_driver').find('select.el-select')
    await sel.setValue('minio')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存存储配置')!.trigger('click')
    await flush()
    expect(updateSettings).not.toHaveBeenCalled()
    expect(w.text()).toContain('有 1 项改动未保存')
  })

  it('重置把改动还原为服务端原值', async () => {
    const w = mountPanel()
    await flush()
    const input = rowOf(w, 'upload_max_mb').find('input.el-input-number')
    await input.setValue('20')
    await flush()
    const resetBtn = w.findAll('button.el-button').find((b) => b.text() === '重置')!
    await resetBtn.trigger('click')
    await flush()
    expect(w.text()).not.toContain('有 1 项改动未保存')
    expect((rowOf(w, 'upload_max_mb').find('input.el-input-number').element as HTMLInputElement).value).toBe('10')
  })

  it('从内置档案跳来时按 focus 滚到对应行', async () => {
    const oldHref = window.location.href
    window.history.replaceState({}, '', '/platform/storage-profiles?tab=platform&focus=mirror_dir')
    let anchor: HTMLDivElement | null = null
    const spy = vi.fn()
    try {
      anchor = document.createElement('div')
      anchor.id = 'set-mirror_dir'
      Object.defineProperty(anchor, 'scrollIntoView', { value: spy, configurable: true })
      document.body.appendChild(anchor)
      mountPanel()
      await flush()
      expect(spy).toHaveBeenCalled()
    } finally {
      anchor?.remove()
      window.history.replaceState({}, '', oldHref)
    }
  })
})
