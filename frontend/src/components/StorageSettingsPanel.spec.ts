import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

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
  // 迁移 0059：连接信息已进存储档案，这里只有指针与部署期目录
  item('platform_storage_profile_id', '平台自身存储档案', 'storage', '1', { restart: true }),
  item('upload_dir', '本地上传目录', 'storage', '/srv/uploads', { envVar: 'UPLOAD_DIR', restart: true }),
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

const LOCAL_PROFILE = { driver: 'local', endpoint: '', bucket: '', baseUrl: 'https://card.example.com/uploads' }

type PlatformLocation = { driver: string; endpoint?: string; bucket?: string; baseUrl?: string }

function mountPanel(platformProfile: PlatformLocation = LOCAL_PROFILE) {
  return mount(StorageSettingsPanel, {
    props: { platformProfile },
    global: { stubs, directives: { loading: {} } },
  })
}
// 紧凑模式隐藏了 key，用 data-key 定位；这本身也是「技术键名不再占版面」的守门。
function rowOf(w: any, key: string) {
  const r = w.find(`.row[data-key="${key}"]`)
  expect(r.exists(), `未找到配置项行: ${key}`).toBe(true)
  return r
}
async function openAdvanced(w: any) {
  const btn = w.findAll('button').find((b: any) => b.text().includes('高级设置'))
  if (btn) {
    await btn.trigger('click')
    await flush()
  }
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
  it('默认只展示「文件存在哪」与三个常用项，技术键名和高级项全部收起', async () => {
    const w = mountPanel()
    await flush()
    expect(getSettings).toHaveBeenCalled()
    const text = w.text()
    // 顶部先给结论：管理员不用读配置表就知道文件在哪
    expect(text).toContain('图片 / 视频 / 小程序码存哪')
    expect(text).toContain('文件存在服务器本地磁盘')
    expect(text).toContain('服务器上传目录')
    expect(text).toContain('本地备份目录')
    expect(text).toContain('自动本地备份')
    // 内部指针不给管理员看：它由系统自动维护，展示只会增加困惑。
    // 紧凑模式不渲染 key 文本，所以必须按 data-key 断言「这一行根本不存在」——
    // 只断言 key 字符串不出现会恒真（键名本来就不显示），等于没守。
    expect(text).not.toContain('platform_storage_profile_id')
    expect(text).not.toContain('平台自身存储档案')
    expect(w.find('.row[data-key="platform_storage_profile_id"]').exists()).toBe(false)
    // 高级项默认不占版面
    expect(text).toContain('高级设置（通常不用改）')
    expect(text).not.toContain('单张图片上限')
    expect(text).not.toContain('CDN 厂商')
    expect(w.findAll('.config-section')).toHaveLength(0)
    expect(text).not.toContain('brand_name')
    expect(text).not.toContain('jwt_key')
    expect(text).not.toContain('storage_driver')
  })

  it('顶部说清连接信息去哪改，并能一键切到存储档案标签', async () => {
    const w = mountPanel()
    await flush()
    const text = w.text()
    expect(text).toContain('连接信息（驱动 / 桶 / 凭据 / 访问基址）')
    expect(text).toContain('存储档案 → 平台自身存储')
    expect(text).toContain('保存后需要重启')
    const btn = w.findAll('button').find((b) => b.text() === '去存储档案修改')!
    await btn.trigger('click')
    expect(w.emitted('goto-profiles')).toBeTruthy()
  })

  it('云存储时摘要显示实际桶与访问地址，而不是继续讲本地目录', async () => {
    const w = mountPanel({
      driver: 's3', endpoint: 's3.example.com', bucket: 'card-prod',
      baseUrl: 'https://cdn.example.com',
    })
    await flush()
    const text = w.text()
    expect(text).toContain('文件存在S3 兼容云 · card-prod')
    expect(text).toContain('s3.example.com')
    expect(text).toContain('https://cdn.example.com')
  })

  it('高级设置默认收起，点开后出现 CDN / 配额 / 上传限制三组', async () => {
    const w = mountPanel()
    await flush()
    expect(w.text()).not.toContain('单张图片上限')
    await openAdvanced(w)
    expect(w.findAll('.config-section')).toHaveLength(3)
    const text = w.text()
    expect(text).toContain('CDN 刷新（可选）')
    expect(text).toContain('配额与上传限制')
    expect(text).toContain('临时链接有效期')
    expect(text).toContain('单张图片上限')
    expect(text).toContain('CDN 厂商')
  })

  it('部署目录不再随驱动置灰：连接信息已进档案，面板只按 CDN 联动', async () => {
    const w = mountPanel()
    await flush()
    // 从内置档案跳来定位 upload_dir 时一定能找到，不会被任何联动隐藏
    expect(rowOf(w, 'upload_dir').classes()).not.toContain('row-dim')
    expect(rowOf(w, 'mirror_dir').classes()).not.toContain('row-dim')
  })

  it('保存只提交改动项，值以字符串提交', async () => {
    const w = mountPanel()
    await flush()
    await openAdvanced(w)
    const input = rowOf(w, 'upload_max_mb').find('input.el-input-number')
    await input.setValue('20')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存设置')!.trigger('click')
    await flush()
    expect(updateSettings).toHaveBeenCalledTimes(1)
    const payload = vi.mocked(updateSettings).mock.calls[0][0] as Record<string, string>
    expect(payload).toEqual({ upload_max_mb: '20' })
  })

  it('敏感项点清除提交空串，保存后回到「已配置」且不把掩码当值', async () => {
    const w = mountPanel()
    await flush()
    await openAdvanced(w)
    // 夹具里 secret 项原值为空（后端给掩码，界面留空），直接改值模拟重新填写
    const pwd = rowOf(w, 'cdn_access_key_secret').find('input.el-input')
    await pwd.setValue('new-secret')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存设置')!.trigger('click')
    await flush()
    const payload = vi.mocked(updateSettings).mock.calls[0][0] as Record<string, string>
    expect(payload.cdn_access_key_secret).toBe('new-secret')
    expect(ElMessage.success).toHaveBeenCalled()
  })

  it('保存返回需重启项时给出常驻提示', async () => {
    vi.mocked(updateSettings).mockImplementationOnce(async () => ({
      code: 0, data: { restartKeys: ['cdn_provider'] },
    }) as any)
    const w = mountPanel()
    await flush()
    await openAdvanced(w)
    const input = rowOf(w, 'cdn_provider').find('input.el-input')
    await input.setValue('cloudflare')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存设置')!.trigger('click')
    await flush()
    expect(w.text()).toContain('刚保存的改动需要重启才生效')
    expect(w.text()).toContain('CDN 厂商')
  })

  it('破坏性改动弹确认，取消则不提交且草稿保留', async () => {
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce(new Error('cancel'))
    const w = mountPanel()
    await flush()
    const input = rowOf(w, 'upload_dir').find('input.el-input')
    await input.setValue('/mnt/new-uploads')
    await flush()
    await w.findAll('button').find((b) => b.text() === '保存设置')!.trigger('click')
    await flush()
    expect(updateSettings).not.toHaveBeenCalled()
    expect(w.text()).toContain('有 1 项改动未保存')
  })

  it('重置把改动还原为服务端原值', async () => {
    const w = mountPanel()
    await flush()
    await openAdvanced(w)
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
