import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/api/settings', () => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(async () => ({ code: 0, data: { restartKeys: [] } })),
  getPublicSettings: vi.fn(async () => ({ code: 0, data: { brand_name: '微传名片' } })),
  // 路径选择器：三项 pathKind 配置各自会打开一个弹窗，点「浏览…」时拉一次目录
  browseServerPaths: vi.fn(async () => ({
    code: 0,
    data: {
      kind: 'source_dir',
      path: '/opt',
      selected: '/opt',
      selectedIsDir: true,
      parent: '/',
      home: '/home/dev',
      entries: [],
      truncated: false,
      current: { ok: false, message: '当前目录还不能用' },
      suggestions: [],
    },
  })),
}))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
  ElMessageBox: { confirm: vi.fn() },
}))

import { ElMessage, ElMessageBox } from 'element-plus'
import { browseServerPaths, getSettings, updateSettings } from '@/api/settings'
import { useSettingsStore } from '@/store/settings'
import Settings from './Settings.vue'
import type { SettingItem } from '@/api/settings'

// 与后端 setting.registry 一致的夹具（分组 / 类型 / 值 / 生效方式元数据 / 中文名）
const items: SettingItem[] = [
  { key: 'brand_name', name: '品牌名称', group: 'general', type: 'string', value: '微传名片', description: '品牌名', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'miniprogram_base_url', name: '小程序接口基址', group: 'miniprogram', type: 'string', value: '', description: '小程序基址', secret: false, restart: false, readOnly: false, envVar: '', configured: false },
  { key: 'pinned_company_id', name: '独立版锁定企业', group: 'miniprogram', type: 'int', value: '0', description: '锁定租户', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'upload_allow_ext', name: '允许上传的文件类型', group: 'upload', type: 'string_slice', value: 'png,jpg', description: '扩展名', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'upload_max_mb', name: '单张图片大小上限', group: 'upload', type: 'int', value: '10', description: '上传上限', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'upload_video_max_mb', name: '单个视频大小上限', group: 'upload', type: 'int', value: '100', description: '视频上限', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'presign_put_ttl_minutes', name: '上传链接有效期', group: 'upload', type: 'int', value: '15', description: '预签名时效', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'sign_get_ttl_minutes', name: '私有桶下载链接有效期', group: 'upload', type: 'int', value: '30', description: '签名下载时效', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'wx_appid', name: '小程序 AppID', group: 'wechat', type: 'string', value: '', description: 'AppID', secret: false, restart: false, readOnly: false, envVar: 'WX_APPID', configured: false },
  { key: 'wx_appsecret', name: '小程序 AppSecret', group: 'wechat', type: 'string', value: '******', description: 'AppSecret（敏感）', secret: true, restart: false, readOnly: false, envVar: 'WX_APPSECRET', configured: true },
  { key: 'wxacode_ttl_hours', name: '小程序码缓存时长', group: 'wechat', type: 'int', value: '720', description: '小程序码 TTL', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'wxacode_sign_ttl_minutes', name: '小程序码签名链接有效期', group: 'wechat', type: 'int', value: '60', description: '小程序码签名时效', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'wxacode_rate_limit_per_min', name: '小程序码生成频率限制', group: 'wechat', type: 'int', value: '30', description: '小程序码限流', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  // pathKind 三项：界面在文本框旁渲染「浏览…」按钮，点击弹出服务端目录选择器
  { key: 'mp_node_bin', name: 'Node 可执行文件', group: 'mpupload', type: 'string', value: '', description: 'Node 路径', secret: false, restart: false, readOnly: false, envVar: 'MP_NODE_BIN', pathKind: 'node_bin', configured: false },
  { key: 'mp_ci_module_dir', name: 'miniprogram-ci 安装目录', group: 'mpupload', type: 'string', value: '', description: 'ci 模块目录', secret: false, restart: false, readOnly: false, envVar: 'MP_CI_MODULE_DIR', pathKind: 'module_dir', configured: false },
  { key: 'mp_source_dir', name: '小程序源码目录', group: 'mpupload', type: 'string', value: '', description: '源码目录', secret: false, restart: false, readOnly: false, envVar: 'MP_SOURCE_DIR', pathKind: 'source_dir', configured: false },
  { key: 'mp_upload_timeout_sec', name: '上传超时（秒）', group: 'mpupload', type: 'int', value: '300', description: '超时', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'access_token_ttl_minutes', name: '登录凭证有效期', group: 'auth', type: 'int', value: '15', description: 'access 有效期', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'login_rate_limit_per_min', name: '登录尝试频率限制', group: 'auth', type: 'int', value: '10', description: '登录限流', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'cdn_enabled', name: '启用 CDN 刷新', group: 'storage', type: 'bool', value: '', description: 'CDN 刷新', secret: false, restart: true, readOnly: false, envVar: 'CDN_ENABLED', configured: false },
  { key: 'cdn_provider', name: 'CDN 厂商', group: 'storage', type: 'string', value: '', description: 'CDN 厂商', secret: false, restart: true, readOnly: false, envVar: 'CDN_PROVIDER', options: ['aliyun', 'tencent', 'qiniu', 'cloudflare', 'cloudfront'], configured: false },
  { key: 'cdn_access_key_id', name: 'CDN AccessKeyId', group: 'storage', type: 'string', value: '', description: 'CDN AK', secret: false, restart: true, readOnly: false, envVar: 'CDN_ACCESS_KEY_ID', configured: false },
  { key: 'cdn_access_key_secret', name: 'CDN AccessKeySecret', group: 'storage', type: 'string', value: '', description: 'CDN SK', secret: true, restart: true, readOnly: false, envVar: 'CDN_ACCESS_KEY_SECRET', configured: false },
  { key: 'cdn_resource_id', name: 'CDN 资源标识', group: 'storage', type: 'string', value: '', description: 'Zone ID / Distribution ID', secret: false, restart: true, readOnly: false, envVar: 'CDN_RESOURCE_ID', configured: false },
  { key: 'cdn_endpoint', name: 'CDN 服务端点', group: 'storage', type: 'string', value: '', description: 'CDN 端点', secret: false, restart: true, readOnly: false, envVar: 'CDN_ENDPOINT', configured: false },
  { key: 'storage_driver', name: '存储方式', group: 'storage', type: 'string', value: 'local', description: '存储驱动', secret: false, restart: true, readOnly: false, envVar: 'STORAGE_DRIVER', options: ['local', 'minio', 'oss', 's3'], configured: true },
  { key: 'storage_endpoint', name: '存储服务端点', group: 'storage', type: 'string', value: '', description: '对象存储端点', secret: false, restart: true, readOnly: false, envVar: 'MINIO_ENDPOINT', configured: false },
  { key: 'storage_bucket', name: '存储空间名称', group: 'storage', type: 'string', value: '', description: '存储桶', secret: false, restart: true, readOnly: false, envVar: 'MINIO_BUCKET', configured: false },
  { key: 'storage_access_key', name: '存储 AccessKey', group: 'storage', type: 'string', value: '', description: '存储 AK', secret: false, restart: true, readOnly: false, envVar: 'MINIO_ACCESS_KEY', configured: false },
  { key: 'storage_secret_key', name: '存储 SecretKey', group: 'storage', type: 'string', value: '', description: '存储 SecretKey', secret: true, restart: true, readOnly: false, envVar: 'MINIO_SECRET_KEY', configured: false },
  { key: 'storage_use_ssl', name: '存储启用 HTTPS', group: 'storage', type: 'bool', value: '', description: '存储 SSL', secret: false, restart: true, readOnly: false, envVar: 'MINIO_USE_SSL', configured: false },
  { key: 'storage_region', name: '存储区域', group: 'storage', type: 'string', value: '', description: 'S3 区域', secret: false, restart: true, readOnly: false, envVar: 'STORAGE_REGION', configured: false },
  { key: 'storage_public', name: '存储空间公开访问', group: 'storage', type: 'bool', value: 'true', description: '公开桶', secret: false, restart: true, readOnly: false, envVar: 'STORAGE_PUBLIC', configured: true },
  { key: 'upload_dir', name: '本地上传目录', group: 'storage', type: 'string', value: '', description: '本地落盘目录', secret: false, restart: true, readOnly: false, envVar: 'UPLOAD_DIR', configured: false },
  { key: 'upload_base_url', name: '文件访问基址', group: 'storage', type: 'string', value: '', description: '文件公开访问地址', secret: false, restart: true, readOnly: false, envVar: 'UPLOAD_BASE_URL', configured: false },
  { key: 'cookie_domain', name: '登录 Cookie 作用域', group: 'security', type: 'string', value: '', description: 'Cookie 作用域', secret: false, restart: true, readOnly: true, envVar: 'COOKIE_DOMAIN', configured: false },
  { key: 'cookie_secure', name: '登录 Cookie 强制 HTTPS', group: 'security', type: 'bool', value: 'false', description: 'Cookie Secure', secret: false, restart: true, readOnly: true, envVar: 'COOKIE_SECURE', configured: true },
  { key: 'db_dsn', name: '数据库连接串', group: 'security', type: 'string', value: '******', description: '数据库连接串', secret: true, restart: true, readOnly: true, envVar: 'DB_DSN', configured: true },
  { key: 'jwt_key', name: '登录签名密钥', group: 'security', type: 'string', value: '******', description: 'JWT 密钥', secret: true, restart: true, readOnly: true, envVar: 'JWT_KEY', configured: true },
  { key: 'independent_lock', name: '独立版企业锁定', group: 'feature', type: 'bool', value: 'false', description: '独立版锁定', secret: false, restart: false, readOnly: false, envVar: '', configured: true },
  { key: 'metrics_enabled', name: '开放监控指标', group: 'feature', type: 'bool', value: '', description: '指标开关', secret: false, restart: false, readOnly: false, envVar: 'METRICS_ENABLED', configured: false },
]

// 轻量 stub：仅渲染插槽 / 捕获 props，避免引入完整 Element Plus（jsdom 下过重易 OOM）。
const ElCard = defineComponent({
  name: 'ElCard',
  template: `<div class="el-card"><div class="el-card__header"><slot name="header" /></div><slot /></div>`,
})
// 渲染 title 与关闭按钮：真实 el-alert 会消费这两个 prop，
// stub 不渲染的话「待重启提示条」这类靠 title 传达的信息就断言不到。
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: {
    type: String,
    title: String,
    description: String,
    // 必须是 Boolean：模板里写无值属性 `closable` 时，只有声明为 Boolean 才会
    // 得到 true，字符串类型会拿到空串、v-if 判假，关闭按钮就渲染不出来
    closable: Boolean,
    showIcon: Boolean,
  },
  emits: ['close'],
  template: `<div class="el-alert"><div class="el-alert__title">{{ title }}</div><slot /><button v-if="closable" class="el-alert__close" @click="$emit('close')">×</button></div>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['size', 'effect', 'type'],
  template: `<span class="el-tag"><slot /></span>`,
})
// 开关改三态下拉：未设置（空串=回退环境变量）/ 开 / 关
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue', 'disabled', 'fallbackPlacements'],
  emits: ['update:modelValue', 'change'],
  template: `<select class="el-select" :disabled="disabled" :value="modelValue" @change="$emit('change', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
const ElInputNumber = defineComponent({
  name: 'ElInputNumber',
  props: ['modelValue', 'min', 'disabled', 'controlsPosition'],
  emits: ['update:modelValue', 'change'],
  template: `<input class="el-input-number" type="number" :disabled="disabled" :value="modelValue" @input="$emit('change', Number($event.target.value))" />`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  // size 必须声明：否则 size="small" 会作为属性落到 <input> 上（非法值 + 警告）
  props: ['modelValue', 'placeholder', 'type', 'showPassword', 'disabled', 'size'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :type="type || 'text'" :disabled="disabled" :placeholder="placeholder" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'disabled', 'text', 'size'],
  template: `<button class="el-button" :disabled="disabled"><slot /></button>`,
})
// 配置向导：对话框。stub 不传送门，直接内联渲染，便于在 wrapper 里断言。
// 步骤条是自绘 button chips（可点击回跳 + 待配置计数），不再用 el-steps。
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width', 'closeOnClickModal'],
  template: `<div class="el-dialog" v-if="modelValue"><div class="el-dialog__title">{{ title }}</div><slot /><div class="el-dialog__footer"><slot name="footer" /></div></div>`,
})
// tooltip 拍平：触发徽标 + 说明文本一起渲染，便于断言说明仍在 DOM
const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'placement'],
  template: `<span class="el-tooltip"><slot /><span class="el-tooltip__content">{{ content }}</span></span>`,
})

const mountOpts = {
  global: {
    // v-loading 来自 PathPickerDialog 的列表加载态；这里不引完整 EP，给个空指令即可，
    // 否则每个用例都会刷一条「Failed to resolve directive: loading」。
    directives: { loading: {} },
    stubs: {
      'el-card': ElCard,
      'el-alert': ElAlert,
      'el-tag': ElTag,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-input-number': ElInputNumber,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-dialog': ElDialog,
      'el-tooltip': ElTooltip,
    },
  },
}

async function setup() {
  localStorage.clear()
  setActivePinia(createPinia())
  const w = mount(Settings, mountOpts)
  await vi.waitFor(() => expect(getSettings).toHaveBeenCalled())
  await nextTick()
  return { w, store: useSettingsStore() }
}

// 定位辅助：一律「先找 key 所在的行，再找行内控件」。
// 早先按控件类型全局索引断言打偏过（分组顺序一变就错位），按 key 定位对顺序不敏感。
function rowOf(w: any, key: string) {
  const r = w.findAll('.row').find((x: any) => x.text().includes(key))
  expect(r, `未找到配置项行: ${key}`).toBeTruthy()
  return r!
}
// 非抛出版本：断言某行「不在场」（被搜索过滤隐藏时用）
function rowExists(w: any, key: string) {
  return w.findAll('.row').some((x: any) => x.text().includes(key))
}
function textInputOf(w: any, key: string) {
  return rowOf(w, key).find('input.el-input')
}
function numberInputOf(w: any, key: string) {
  return rowOf(w, key).find('input.el-input-number')
}
// 断言组件 props（min 只存在于组件上，DOM 上没有对应属性）
function numberComponentOf(w: any, key: string) {
  return rowOf(w, key).findComponent({ name: 'ElInputNumber' })
}
function selectOf(w: any, key: string) {
  return rowOf(w, key).find('select.el-select')
}
function saveButton(w: any) {
  return w.findAll('button.el-button').find((b: any) => b.text().includes('保存'))!
}
// 向导对话框内的定位：主列表与向导共用 SettingRow，不限定作用域会一行两处打偏
function dialogOf(w: any) {
  const dlg = w.find('.el-dialog')
  expect(dlg.exists(), '配置向导对话框未打开').toBe(true)
  return dlg
}
function dialogRow(w: any, key: string) {
  const r = dialogOf(w)
    .findAll('.row')
    .find((x: any) => x.text().includes(key))
  expect(r, `向导中未找到配置项行: ${key}`).toBeTruthy()
  return r!
}
function dialogButton(w: any, text: string) {
  const b = dialogOf(w)
    .findAll('button.el-button')
    .find((x: any) => x.text() === text)
  expect(b, `向导中未找到按钮: ${text}`).toBeTruthy()
  return b!
}
// 非抛出版本：断言向导中某行「不在场」（被 depState 置灰隐藏时用）
function dialogRowExists(w: any, key: string) {
  return dialogOf(w)
    .findAll('.row')
    .some((x: any) => x.text().includes(key))
}
function wizardEntryButton(w: any) {
  return w.findAll('button.el-button').find((b: any) => b.text().includes('配置向导'))!
}

describe('Settings 系统设置', () => {
  beforeEach(() => {
    vi.mocked(getSettings).mockReset()
    // 必须返回深拷贝：save() 的乐观推进会就地改 item.configured，
    // 直接返回共享 fixture 会把改动泄漏到后续测试（清空敏感项的测试就中过招）。
    vi.mocked(getSettings).mockImplementation(async () => ({ code: 0, data: items.map((it) => ({ ...it })) }) as any)
    vi.mocked(updateSettings).mockClear()
    vi.mocked(updateSettings).mockImplementation(async () => ({ code: 0, data: { restartKeys: [] } }))
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessage.warning).mockClear()
    // 破坏性确认默认「同意」：只有专门测取消行为的用例才改写为拒绝
    vi.mocked(ElMessageBox.confirm).mockReset()
    vi.mocked(ElMessageBox.confirm).mockResolvedValue('confirm' as any)
    // localStorage 每个用例清空：就绪清单展开态与向导「已看过」都是持久化状态，
    // 不清会在用例间互相污染（前一个用例点开过，后一个用例的默认值就变了）
    try {
      localStorage.clear()
    } catch {
      // jsdom 一定能读写；真出异常时至少不影响其它断言
    }
  })

  it('按分组渲染全部配置项与说明（含存储与只读分组）', async () => {
    const { w } = await setup()
    const text = w.text()
    ;['通用', '小程序', '上传', '微信', '后台上传小程序代码', '登录与令牌', '文件存储与 CDN', '安全与基础设施', '功能开关'].forEach(
      (g) => expect(text).toContain(g),
    )
    expect(text).toContain('brand_name')
    expect(text).toContain('品牌名')
    expect(text).toContain('wxacode_ttl_hours')
    expect(text).toContain('小程序码 TTL')
    // 后台上传小程序代码组：四项均渲染（环境与密钥是两条不同配置路径，必须都能看到）
    expect(text).toContain('mp_node_bin')
    expect(text).toContain('Node 可执行文件')
    expect(text).toContain('mp_source_dir')
    expect(text).toContain('MP_SOURCE_DIR')
    // 需重启 / 只读 / 敏感 标记都渲染出来
    expect(rowOf(w, 'storage_driver').text()).toContain('需重启')
    expect(rowOf(w, 'db_dsn').text()).toContain('只读')
    expect(rowOf(w, 'jwt_key').text()).toContain('敏感')
    // 环境变量名提示（只读项只告诉改法）
    expect(rowOf(w, 'db_dsn').text()).toContain('DB_DSN')
  })

  it('无修改时保存按钮禁用，不提交（含敏感项的掩码不被回传）', async () => {
    const { w } = await setup()
    expect(saveButton(w).attributes('disabled')).toBeDefined()
    await saveButton(w).trigger('click')
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('敏感项输入框留空：已配置的显示占位提示，不把掩码当值提交', async () => {
    const { w } = await setup()
    // AppID 未填时 AppSecret 按联动不占版面——按真实配置顺序先填 AppID
    await textInputOf(w, 'wx_appid').setValue('wx123')
    await nextTick()
    const input = textInputOf(w, 'wx_appsecret')
    // 后端只给掩码，界面输入框必须是空的（否则一保存就把 '******' 写进库）
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(input.attributes('placeholder')).toContain('已配置')
    expect(input.attributes('type')).toBe('password')

    // 只改别的项，敏感项不应出现在载荷里
    await textInputOf(w, 'brand_name').setValue('朗汇机电')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ wx_appid: 'wx123', brand_name: '朗汇机电' })
  })

  it('敏感项填新值才会提交；点「清除」则提交空串回退环境变量', async () => {
    const { w } = await setup()
    await textInputOf(w, 'wx_appid').setValue('wx123')
    await nextTick()
    await textInputOf(w, 'wx_appsecret').setValue('new-secret')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ wx_appid: 'wx123', wx_appsecret: 'new-secret' })

    vi.mocked(updateSettings).mockClear()
    const clearBtn = rowOf(w, 'db_dsn').findAll('button.el-button')
    // db_dsn 是只读项，没有「清除」按钮；storage_secret_key 未配置，也没有；用 wx_appsecret 验证
    expect(clearBtn).toHaveLength(0)
    const secretClear = rowOf(w, 'wx_appsecret').findAll('button.el-button')
    expect(secretClear).toHaveLength(1)
    await secretClear[0].trigger('click')
    await nextTick()
    expect(saveButton(w).attributes('disabled')).toBeUndefined()
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    // 首次保存成功后 AppID 已推进为原值，二次保存只带清空项
    expect(updateSettings).toHaveBeenCalledWith({ wx_appsecret: '' })
  })

  it('只读项渲染为禁用控件，即使被改也不进提交载荷', async () => {
    const { w } = await setup()
    expect(textInputOf(w, 'db_dsn').attributes('disabled')).toBeDefined()
    expect(textInputOf(w, 'cookie_domain').attributes('disabled')).toBeDefined()
    expect(selectOf(w, 'cookie_secure').attributes('disabled')).toBeDefined()
    // 只读项仍展示当前值（敏感项展示掩码而非真实值）
    expect((textInputOf(w, 'db_dsn').element as HTMLInputElement).value).toBe('******')
    expect(selectOf(w, 'cookie_secure').element.value).toBe('false')

    // 绕过禁用直接触发 change，也不应产生变更
    await selectOf(w, 'cookie_secure').setValue('true')
    await nextTick()
    expect(saveButton(w).attributes('disabled')).toBeDefined()
  })

  it('开关三态：未设置 → 开，以 true/false 字符串提交', async () => {
    const { w } = await setup()
    const sel = selectOf(w, 'metrics_enabled')
    expect(sel.element.value).toBe('') // 未设置（跟随环境变量）
    await sel.setValue('true')
    await nextTick()
    expect(saveButton(w).attributes('disabled')).toBeUndefined()

    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ metrics_enabled: 'true' })
  })

  it('保存含「需重启」项后给出常驻待重启提示（含重启命令），可手动关闭', async () => {
    // 后端会点名哪些项需重启；这里模拟改了存储方式
    vi.mocked(updateSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: { restartKeys: ['storage_driver'] },
    }) as any)
    const { w } = await setup()
    await selectOf(w, 'storage_driver').setValue('minio')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(w.find('.restart-alert').exists()).toBe(true))
    const t = w.find('.restart-alert').text()
    // 显示中文名而不是 key，且给出可直接照做的重启命令
    expect(t).toContain('存储方式')
    expect(t).not.toContain('storage_driver')
    expect(t).toContain('docker compose restart backend')
    // 关掉后不再占用注意力
    await w.find('.restart-alert .el-alert__close').trigger('click')
    await nextTick()
    expect(w.find('.restart-alert').exists()).toBe(false)
  })

  it('没有需重启项时不显示待重启提示', async () => {
    const { w } = await setup()
    // 必须改成与当前值不同的内容，否则 dirty=false、保存按钮是禁用的
    await textInputOf(w, 'brand_name').setValue('朗汇机电')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(w.find('.restart-alert').exists()).toBe(false)
  })

  it('改整数项：以字符串提交，避免后端类型校验失败', async () => {
    const { w } = await setup()
    await numberInputOf(w, 'upload_max_mb').setValue('25')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ upload_max_mb: '25' })
  })

  it('改品牌名保存后即时同步侧边栏品牌（store）', async () => {
    const { w, store } = await setup()
    await textInputOf(w, 'brand_name').setValue('朗汇机电')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ brand_name: '朗汇机电' })
    expect(store.brandName).toBe('朗汇机电')
  })

  it('保存返回需重启项时给出告警提示，避免误以为没生效', async () => {
    vi.mocked(updateSettings).mockImplementation(async () => ({
      code: 0,
      msg: '保存成功；其中 1 项需重启服务后生效：storage_driver',
      data: { restartKeys: ['storage_driver'] },
    }))
    const { w } = await setup()
    await selectOf(w, 'storage_driver').setValue('oss')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(ElMessage.warning).mock.calls[0][0]).toContain('1 项需重启服务后才生效')
    expect(vi.mocked(ElMessage.success)).not.toHaveBeenCalled()
    // 具体是哪几项、怎么重启交给常驻提示条（toast 几秒就消失，说不完）
    await nextTick()
    const bar = w.find('.restart-alert')
    expect(bar.exists()).toBe(true)
    expect(bar.text()).toContain('存储方式')
    expect(bar.text()).toContain('docker compose restart backend')
  })

  it('重置：把改动改回服务端原值，不再有变更', async () => {
    const { w } = await setup()
    const input = textInputOf(w, 'brand_name')
    await input.setValue('临时品牌')
    await nextTick()
    expect(saveButton(w).attributes('disabled')).toBeUndefined()

    const resetBtn = w.findAll('button.el-button').find((b: any) => b.text().includes('重置'))!
    await resetBtn.trigger('click')
    await nextTick()
    expect((input.element as HTMLInputElement).value).toBe('微传名片')
    expect(saveButton(w).attributes('disabled')).toBeDefined()
  })

  // 只写「只读」不够：运营会以为功能没生效。必须同时说清「现在配了没」和「去哪改」。
  it('只读项标明当前是否已配置，并给出部署环境的改法', async () => {
    const { w } = await setup()
    expect(rowOf(w, 'db_dsn').text()).toContain('已配置')
    // cookie_domain 未配置 = 进程在用内置缺省值，属上线前要修的项
    expect(rowOf(w, 'cookie_domain').text()).toContain('未配置')
    expect(rowOf(w, 'cookie_domain').text()).toContain('COOKIE_DOMAIN')
    // 改法要指向部署环境，而不是让人在本页白找
    expect(rowOf(w, 'db_dsn').text()).toContain('部署环境')
    expect(rowOf(w, 'db_dsn').text()).toContain('重启')
  })

  it('有安全下限的整数项：上传上限最小为 1，界面就选不到 0', async () => {
    const { w } = await setup()
    expect(numberComponentOf(w, 'upload_max_mb').props('min')).toBe(1)
    // 视频上限同样有下限 1（后端 intRange(1,2048)）：2026-10-22 全量核查时补上，
    // 此前界面允许 0，保存才被后端拒
    expect(numberComponentOf(w, 'upload_video_max_mb').props('min')).toBe(1)
    // 其它整数项（如小程序码 TTL）仍允许 0：后端有回落逻辑，不该在界面硬拦
    expect(numberComponentOf(w, 'wxacode_ttl_hours').props('min')).toBe(0)
  })

  it('枚举项渲染为下拉：存储方式带 local / minio / oss / s3 候选，不再手打字符串', async () => {
    const { w } = await setup()
    const row = rowOf(w, 'storage_driver')
    // 后端 oneOf 校验的候选值通过 options 下发：界面渲染三态下拉
    // （未设置 + 4 候选），而不是文本框——手打候选值毫无意义，填错只能等保存时报错
    expect(row.find('input.el-input').exists()).toBe(false)
    expect(row.find('select.el-select').exists()).toBe(true)
    expect(row.findAll('option')).toHaveLength(5)
  })

  // ---- P0：商业产品可读性（中文名 / 分组导语 / 上线就绪面板）----
  it('配置项以中文名为主标签，key 降级为灰字仍可对照', async () => {
    const { w } = await setup()
    const row = rowOf(w, 'brand_name')
    expect(row.find('.label-text').text()).toBe('品牌名称')
    expect(row.find('.label-key').text()).toBe('brand_name')
    // 只读的中文名同样渲染（数据库连接串）
    expect(rowOf(w, 'db_dsn').find('.label-text').text()).toBe('数据库连接串')
  })

  it('旧部署响应缺 name：标签回落显示 key，不留空白', async () => {
    vi.mocked(getSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: items.map((it) => ({ ...it, name: undefined })),
    }) as any)
    const { w } = await setup()
    expect(rowOf(w, 'brand_name').find('.label-text').text()).toBe('brand_name')
    expect(rowOf(w, 'brand_name').find('.label-key').exists()).toBe(false)
  })

  it('每个分组展示导语：一句话说清管什么、什么时候动', async () => {
    const { w } = await setup()
    expect(w.text()).toContain('产品的基础展示信息') // general
    expect(w.text()).toContain('图片 / 视频 / 小程序码存哪') // storage
    expect(w.text()).toContain('首次部署必配') // miniprogram
    // 上传组导语要说清两条链接各自的适用条件：直传预签名公私桶都走，
    // 只有私有桶下载签名仅在公开访问关掉时才涉及（2026-10-22 核查 UploadField 总是先试预签名）
    expect(w.text()).toContain('公私桶都会走')
    // 9 个分组各一条导语（新增「后台上传小程序代码」）
    expect(w.findAll('.group-blurb')).toHaveLength(9)
  })

  it('上线就绪清单默认收起为摘要条：点「查看详情」才展开，并记住选择', async () => {
    const { w } = await setup()
    // 常驻摘要条：一眼给出「还差几项」及其归属（本页可配 / 环境变量）
    expect(w.find('.ready-bar').exists()).toBe(true)
    // local 驱动 6 项（文件访问基址仅对象存储需要，不进本地清单）
    expect(w.text()).toContain('3 / 6 项就绪')
    expect(w.text()).toContain('还有 3 项待处理')
    // 清单本身默认不展开：本页主任务是改配置，不能被清单推到屏幕外
    expect(w.find('.ready-card').exists()).toBe(false)
    await w.find('.ready-bar-toggle').trigger('click')
    await nextTick()
    expect(w.find('.ready-card').exists()).toBe(true)
    expect(w.find('.ready-bar-toggle').text()).toBe('收起')
    await w.find('.ready-bar-toggle').trigger('click')
    await nextTick()
    expect(w.find('.ready-card').exists()).toBe(false)
  })

  it('全新部署（0 项就绪）时清单默认展开：这时它就是首配的导航图', async () => {
    vi.mocked(getSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: items.map((it) => ({ ...it, value: '', configured: false })),
    }) as any)
    const { w } = await setup()
    // 0 就绪会自动拉起向导（stub 不传送门，页面本体仍在同一棵树上，可直接断言）
    expect(w.find('.el-dialog').exists()).toBe(true)
    expect(w.text()).toContain('0 / 6 项就绪')
    expect(w.find('.ready-card').exists()).toBe(true)
  })

  it('上线就绪面板：按「本页可配置 / 环境变量 / 配置文件」分组；只读项给可复制写法而非死路入口', async () => {
    const { w } = await setup()
    // 夹具已配置 jwt_key / db_dsn / storage_driver → 3/6；
    // cookie_secure=false、wx_appid 未配、miniprogram_base_url 未配；
    // upload_base_url 是云存储专属项，local 驱动下不进清单（假警报修复）
    expect(w.text()).toContain('上线就绪检查')
    expect(w.text()).toContain('3 / 6 项就绪')
    // 清单默认收起：点「查看详情」展开后再断言明细
    if (!w.find('.ready-card').exists()) {
      await w.find('.ready-bar-toggle').trigger('click')
      await nextTick()
    }
    const readyRows = w.findAll('.ready-row')
    expect(readyRows).toHaveLength(6)

    // 分两组呈现：本页可配 3 项（1 就绪） / 部署环境变量 3 项（2 就绪）
    expect(w.findAll('.ready-section-title')).toHaveLength(2)
    expect(w.text()).toContain('本页可配置')
    expect(w.text()).toContain('环境变量 / 配置文件')

    // 已就绪项：显示「已就绪」且无按钮
    const jwtRow = readyRows.find((r: any) => r.text().includes('登录签名密钥'))!
    expect(jwtRow.text()).toContain('已就绪')
    expect(jwtRow.findAll('button.el-button')).toHaveLength(0)

    // 未就绪的可配置项：「去配置」按钮 + 原因提示；点击走 jumpTo（jsdom 无 scrollIntoView，不应抛错）
    const wxRow = readyRows.find((r: any) => r.text().includes('微信 AppID'))!
    expect(wxRow.text()).toContain('小程序码与微信登录依赖')
    const goBtn = wxRow.findAll('button.el-button')
    expect(goBtn).toHaveLength(1)
    expect(goBtn[0].text()).toBe('去配置')
    await goBtn[0].trigger('click')

    // 未就绪的只读项：不再给「查看」（跳过去只是一行改不了的灰行，等于死路），
    // 改为直接给出可复制的环境变量示例 + 重启提示
    const cookieRow = readyRows.find((r: any) => r.text().includes('登录 Cookie 强制 HTTPS'))!
    expect(cookieRow.text()).toContain('COOKIE_SECURE=true')
    // 改法要同时给出环境变量与配置文件两条路（多数部署是安装向导装的，值落在
    // data/runtime.env 而非环境变量），只提环境变量会让人找不到地方改
    expect(cookieRow.text()).toContain('设为环境变量，或写进配置文件 data/runtime.env，重启后生效')
    const cookieBtns = cookieRow.findAll('button.el-button')
    expect(cookieBtns).toHaveLength(1)
    expect(cookieBtns[0].text()).toBe('复制')
  })

  it('cookie_secure 置为 true 后该项就绪（生产 HTTPS 标准）', async () => {
    vi.mocked(getSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: items.map((it) => (it.key === 'cookie_secure' ? { ...it, value: 'true' } : it)),
    }) as any)
    const { w } = await setup()
    expect(w.text()).toContain('4 / 6 项就绪')
  })

  // ---- P1：依赖联动（非搜索态按分支过滤） + 分组顺序 ----
  it('依赖联动：local 下云凭据不占版面，切 minio 后反之；搜索态全量可见', async () => {
    const { w } = await setup()
    // 非搜索态：local 只显示本地上传目录（待配置），云凭据整批不渲染——
    // 置灰保留是旧行为，半透明行仍占版面，与向导「切 minio 后 upload_dir 消失」也不一致
    expect(rowOf(w, 'upload_dir').text()).toContain('待配置')
    ;['storage_endpoint', 'storage_bucket', 'storage_access_key', 'storage_secret_key'].forEach((k) => {
      expect(rowExists(w, k), `${k} 在 local 下不应占版面`).toBe(false)
    })
    // CDN 未开：CDN 凭据同样不渲染（开关本身仍在，开了才出现凭据区）
    expect(rowExists(w, 'cdn_access_key_id')).toBe(false)
    expect(rowExists(w, 'cdn_enabled')).toBe(true)
    // AppID 未填：AppSecret 也不占版面（填了 AppID 才需要配 Secret）
    expect(rowExists(w, 'wx_appsecret')).toBe(false)

    // 切到 minio：云凭据转亮出现（待配置），本地上传目录不再占版面
    await selectOf(w, 'storage_driver').setValue('minio')
    await nextTick()
    expect(rowOf(w, 'storage_endpoint').text()).toContain('待配置')
    expect(rowExists(w, 'upload_dir')).toBe(false)
    // 填了 AppID：AppSecret 转亮出现
    await textInputOf(w, 'wx_appid').setValue('wx123')
    await nextTick()
    expect(rowOf(w, 'wx_appsecret').exists()).toBe(true)

    // 搜索态例外：两种方式的项都要能搜到（带置灰标记）——local 时搜 minio_endpoint
    // 必须命中，否则用户会以为系统里没有这项
    await w.find('input.head-search').setValue('minio_endpoint')
    await nextTick()
    expect(rowExists(w, 'storage_endpoint')).toBe(true)
    expect(rowExists(w, 'upload_dir')).toBe(false)
    await w.find('input.head-search').setValue('upload_dir')
    await nextTick()
    expect(rowExists(w, 'upload_dir')).toBe(true)
    expect(rowOf(w, 'upload_dir').text()).toContain('当前选项下不需要')
    await w.find('input.head-search').setValue('')
    await nextTick()
  })

  it('云存储专属项：local 下 upload_base_url 不占版面，切 minio / oss 后出现', async () => {
    const { w } = await setup()
    // 只有对象存储用 BaseURL 拼公开 URL（minio.go / oss.go 的 Save/Presign）；
    // local 驱动 Save 返回相对地址 /uploads/...，小程序端用 miniprogram_base_url
    // 拼、浏览器按页面源解析——local 下点亮这项是空操作（2026-10-22 全量核查）
    expect(rowExists(w, 'upload_base_url')).toBe(false)
    await selectOf(w, 'storage_driver').setValue('minio')
    await nextTick()
    expect(rowOf(w, 'upload_base_url').text()).toContain('待配置')
    // oss 同样需要：OSS 上传器也用 BaseURL 拼公开 URL
    await selectOf(w, 'storage_driver').setValue('oss')
    await nextTick()
    expect(rowExists(w, 'upload_base_url')).toBe(true)
  })

  it('storage_use_ssl / storage_region 是 S3 兼容驱动（minio / s3）专属：oss 驱动下不占版面', async () => {
    const { w } = await setup()
    await selectOf(w, 'storage_driver').setValue('minio')
    await nextTick()
    expect(rowExists(w, 'storage_use_ssl')).toBe(true)
    expect(rowExists(w, 'storage_region')).toBe(true)
    // 阿里云 OSS 客户端固定 https 且没有区域参数（NewOssUploader 两个都不收）：
    // oss 下这两项填了不产生任何效果，不能跟着端点那批一起亮
    await selectOf(w, 'storage_driver').setValue('oss')
    await nextTick()
    expect(rowExists(w, 'storage_use_ssl')).toBe(false)
    expect(rowExists(w, 'storage_region')).toBe(false)
    // s3 驱动与 minio 共用同一套 S3 客户端实现（main.go 的 case "minio","s3"）：
    // useSSL / region 两个入参对它俩都真正生效，必须亮
    await selectOf(w, 'storage_driver').setValue('s3')
    await nextTick()
    expect(rowExists(w, 'storage_use_ssl')).toBe(true)
    expect(rowOf(w, 'storage_region').text()).toContain('待配置')
    // 搜索态仍能搜到：置灰标记就是「当前选项下不需要」的答案
    await selectOf(w, 'storage_driver').setValue('oss')
    await nextTick()
    await w.find('input.head-search').setValue('storage_use_ssl')
    await nextTick()
    expect(rowExists(w, 'storage_use_ssl')).toBe(true)
    expect(rowOf(w, 'storage_use_ssl').text()).toContain('当前选项下不需要')
    await w.find('input.head-search').setValue('')
    await nextTick()
  })

  it('CDN 厂商联动：资源标识只给 Cloudflare / CloudFront，Cloudflare 用不到 AccessKeyId', async () => {
    const { w } = await setup()
    // 未启用 CDN：整区不占版面（开关本身仍在）
    expect(rowExists(w, 'cdn_provider')).toBe(false)
    expect(rowExists(w, 'cdn_resource_id')).toBe(false)
    await selectOf(w, 'cdn_enabled').setValue('true')
    await nextTick()

    // 默认 aliyun（留空回落）：AK/SK 三家用不到资源标识
    expect(rowExists(w, 'cdn_provider')).toBe(true)
    expect(rowExists(w, 'cdn_access_key_id')).toBe(true)
    expect(rowExists(w, 'cdn_resource_id')).toBe(false)

    // CloudFront：要 Distribution ID（资源标识），也要 AK/SK
    await selectOf(w, 'cdn_provider').setValue('cloudfront')
    await nextTick()
    expect(rowOf(w, 'cdn_resource_id').text()).toContain('待配置')
    expect(rowExists(w, 'cdn_access_key_id')).toBe(true)

    // Cloudflare：要 Zone ID，但只用 API Token——AccessKeyId 不占版面
    await selectOf(w, 'cdn_provider').setValue('cloudflare')
    await nextTick()
    expect(rowExists(w, 'cdn_resource_id')).toBe(true)
    expect(rowExists(w, 'cdn_access_key_id')).toBe(false)
    expect(rowExists(w, 'cdn_access_key_secret')).toBe(true)

    // 搜索态仍能搜到被收窄的项，并带「当前选项下不需要」标记
    await w.find('input.head-search').setValue('cdn_access_key_id')
    await nextTick()
    expect(rowOf(w, 'cdn_access_key_id').text()).toContain('当前选项下不需要')
    await w.find('input.head-search').setValue('')
    await nextTick()
  })

  it('就绪清单按存储方式驱动感知：local 无文件访问基址项，minio 出现且未配则不就绪', async () => {
    const { w } = await setup()
    const expandReady = async () => {
      if (!w.find('.ready-card').exists()) {
        await w.find('.ready-bar-toggle').trigger('click')
        await nextTick()
      }
    }
    await expandReady()
    // local：文件访问基址不进清单——本地存储用相对地址，要求它 = 永远划不掉的假警报
    expect(w.findAll('.ready-row').some((r: any) => r.text().includes('文件访问基址'))).toBe(false)

    // 切 minio：基址项出现（未配 → 不就绪）
    await selectOf(w, 'storage_driver').setValue('minio')
    await nextTick()
    await expandReady()
    const rows = w.findAll('.ready-row')
    expect(rows.some((r: any) => r.text().includes('文件访问基址'))).toBe(true)
    // 云驱动下凭据也纳入「存储方式与凭据」判定：缺端点/桶/密钥上传会在运行时才坏，
    // 就绪检查必须提前拦（提示语同时讲清要补哪几项）
    const credRow = rows.find((r: any) => r.text().includes('存储方式与凭据'))!
    expect(credRow.text()).toContain('端点 / 存储桶 / AccessKey / SecretKey')
  })

  it('分组按依赖链排序：存储在上传之前，小程序与微信相邻', async () => {
    const { w } = await setup()
    // 存储是上传的前提（限「文件多大」之前先定「存哪」），必须排在前；
    // 小程序与微信是同一条业务线，相邻配置不用跳组
    const order = w.findAll('.group-card').map((c: any) => c.find('.group-title').text())
    expect(order).toEqual([
      '通用',
      '文件存储与 CDN',
      '上传',
      '小程序',
      '微信',
      '后台上传小程序代码',
      '登录与令牌',
      '安全与基础设施（只读）',
      '功能开关',
    ])
  })

  it('组内分区：存储三分区、上传两分区、微信两分区，无分区组平铺', async () => {
    const { w } = await setup()
    const subs = (title: string) =>
      w
        .findAll('.group-card')
        .find((c: any) => c.find('.group-title').text() === title)!
        .findAll('.group-sub')
        .map((s: any) => s.text())
    // 存储组：位置 / 访问方式 / CDN（可选）——公私总闸与 CDN 不再混在主凭据里
    expect(subs('文件存储与 CDN')).toEqual(['存储位置', '访问方式', 'CDN 刷新（可选）'])
    // local 态：CDN 区只剩开关本身，凭据不占版面
    expect(rowExists(w, 'cdn_enabled')).toBe(true)
    expect(rowExists(w, 'cdn_access_key_id')).toBe(false)
    // 上传组：大小与类型 / 临时链接有效期（主列表与向导 steps 同结构）
    expect(subs('上传')).toEqual(['大小与类型', '临时链接有效期'])
    // 微信组：凭据 / 小程序码（缓存时长 + 签名时效 + 生成限流）
    expect(subs('微信')).toEqual(['小程序凭据', '小程序码'])
    // 无分区元数据的组（通用 / 小程序 / 令牌 / 只读 / 开关）不画小标题
    expect(subs('通用')).toEqual([])
    expect(subs('小程序')).toEqual([])
  })

  it('组内按逻辑顺序排列：存储方式 → 分支项 → 公私开关 → CDN', async () => {
    const { w } = await setup()
    const keys = w
      .findAll('.group-card')
      .find((c: any) => c.find('.group-title').text().includes('文件存储'))!
      .findAll('.row')
      // 取行内灰色 key 文本（.label-key）：整行 text() 会先吞到悬浮徽标里的
      // 「https://」示例串，按 key 定位才对
      .map((r: any) => r.find('.label-key').text())
    // local 态可见项的顺序：driver → upload_dir → public → cdn_enabled。
    // upload_base_url 是云存储专属（local 用相对地址不经过它），local 下不占版面
    expect(keys).toEqual(['storage_driver', 'upload_dir', 'storage_public', 'cdn_enabled'])
  })

  it('配置向导：默认落在第一个未完成步骤，条件显隐与主列表一致', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 品牌名已配置 → 跳过；存储步有未配置项（upload_dir）→ 落在第 2 步
    expect(dialogOf(w).text()).toContain('第 2 步 / 共 7 步')
    expect(dialogOf(w).text()).toContain('存储与上传')
    // local 驱动：向导里出现本地上传目录，不出现云凭据
    expect(dialogOf(w).text()).toContain('upload_dir')
    expect(dialogOf(w).text()).not.toContain('storage_endpoint')

    // 向导内切 minio：端点转亮出现、目录隐藏——联动在向导里同样生效
    await dialogRow(w, 'storage_driver').find('select.el-select').setValue('minio')
    await nextTick()
    expect(dialogRow(w, 'storage_endpoint').text()).toContain('待配置')
    expect(dialogOf(w).text()).not.toContain('upload_dir')
  })

  it('向导「稍后再说」：恢复打开前的草稿，不产生任何提交', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    await dialogRow(w, 'upload_dir').find('input.el-input').setValue('/data/uploads')
    await nextTick()
    // 草稿已脏（主列表保存按钮可用）
    expect(saveButton(w).attributes('disabled')).toBeUndefined()

    await dialogButton(w, '稍后再说').trigger('click')
    await nextTick()
    // 对话框关闭、草稿复原、不提交
    expect(w.find('.el-dialog').exists()).toBe(false)
    expect((textInputOf(w, 'upload_dir').element as HTMLInputElement).value).toBe('')
    expect(saveButton(w).attributes('disabled')).toBeDefined()
    expect(updateSettings).not.toHaveBeenCalled()
  })

  // ---- P1：快速开始预设 ----
  it('快速开始预设：演示 / 生产一键填入推荐值，只出现在第 1 步', async () => {
    // upload_max_mb 置空：验证「没设过才填」的分支；已有值的 upload_allow_ext 应保持原样
    vi.mocked(getSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: items.map((it) => (it.key === 'upload_max_mb' ? { ...it, value: '', configured: false } : it)),
    }) as any)
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 默认落在第 2 步（存储）：预设条不出现——已经看着存储项了，不需要再问一遍
    expect(dialogOf(w).find('.wizard-preset').exists()).toBe(false)

    // 回第 1 步：预设条出现
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('品牌与身份'))!
      .trigger('click')
    await nextTick()
    const preset = dialogOf(w).find('.wizard-preset')
    expect(preset.exists()).toBe(true)
    const presetBtn = (text: string) =>
      preset.findAllComponents({ name: 'ElButton' }).find((b: any) => b.text() === text)!

    // 演示模式：目录与上传限制落推荐值；已设过值的项不被覆盖
    await presetBtn('演示模式').trigger('click')
    await nextTick()
    // 说明还留在第 1 步（预设条是第 1 步专属），讲清填了什么、什么保持原样
    expect(dialogOf(w).text()).toContain('已按演示模式填入')
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('上传限制'))!
      .trigger('click')
    await nextTick()
    expect((dialogRow(w, 'upload_max_mb').find('input.el-input-number').element as HTMLInputElement).value).toBe('10')
    expect((dialogRow(w, 'upload_allow_ext').find('input.el-input').element as HTMLInputElement).value).toBe('png,jpg')

    // 预设条里直接跳到存储步，验证生产模式的填入结果
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('品牌与身份'))!
      .trigger('click')
    await nextTick()
    const preset2 = dialogOf(w).find('.wizard-preset')
    const jumpBtn = preset2.findAll('button.el-button').find((b: any) => b.text() === '去「存储与上传」步')
    expect(jumpBtn, '填入后应出现跳转按钮').toBeTruthy()
    await preset2
      .findAllComponents({ name: 'ElButton' })
      .find((b: any) => b.text() === '生产模式')!
      .trigger('click')
    await nextTick()
    // 当前模式高亮：生产模式按钮变主色
    expect(
      preset2.findAllComponents({ name: 'ElButton' }).find((b: any) => b.text() === '生产模式')!.props('type'),
    ).toBe('primary')
    expect(dialogOf(w).text()).toContain('需按你的对象存储手动填写')

    await jumpBtn!.trigger('click')
    await nextTick()
    expect(dialogOf(w).text()).toContain('第 2 步 / 共 7 步')
    // 生产模式：minio + HTTPS 开
    expect((dialogRow(w, 'storage_driver').find('select.el-select').element as HTMLSelectElement).value).toBe('minio')
    expect(dialogRow(w, 'storage_use_ssl').find('select.el-select').element.value).toBe('true')
  })

  // ---- P2：向导内联格式校验 ----
  it('向导内联校验：地址不合法不放行下一步，行内即时给出错误', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 第 2 步（存储）：先切 minio——文件访问基址是云存储专属项，local 下不出现
    await dialogRow(w, 'storage_driver').find('select.el-select').setValue('minio')
    await nextTick()
    await dialogRow(w, 'upload_base_url').find('input.el-input').setValue('cdn.example.com')
    await nextTick()
    // 行内错误即时出现（不用等点下一步），指出该填成什么样
    expect(dialogRow(w, 'upload_base_url').text()).toContain('不是完整地址')

    // 点下一步：被拦住，仍在第 2 步，并说明是哪一项
    await dialogButton(w, '下一步').trigger('click')
    await nextTick()
    expect(dialogOf(w).text()).toContain('第 2 步 / 共 7 步')
    expect(vi.mocked(ElMessage.warning).mock.calls[0][0]).toContain('文件访问基址')

    // 改成合法地址：错误消失、放行
    await dialogRow(w, 'upload_base_url').find('input.el-input').setValue('https://cdn.example.com')
    await nextTick()
    expect(dialogRow(w, 'upload_base_url').text()).not.toContain('不是完整地址')
    await dialogButton(w, '下一步').trigger('click')
    await nextTick()
    expect(dialogOf(w).text()).toContain('第 3 步 / 共 7 步')
  })

  it('向导内联校验：小程序基址只收 https；「完成」前全步骤兜底', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 去小程序步，填 http:// 地址：后端只校验前缀能过，但小程序连不上，这里更严一档
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('小程序接入'))!
      .trigger('click')
    await nextTick()
    await dialogRow(w, 'miniprogram_base_url').find('input.el-input').setValue('http://mp.example.com')
    await nextTick()
    expect(dialogRow(w, 'miniprogram_base_url').text()).toContain('只支持 https')

    // 步骤条允许自由跳转：直接跳到安全确认步，再点「保存并完成」
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('安全确认'))!
      .trigger('click')
    await nextTick()
    await dialogButton(w, '保存并完成').trigger('click')
    await nextTick()
    // 被全步骤校验兜住：跳回出错步骤、不提交、对话框不关
    expect(dialogOf(w).text()).toContain('第 5 步 / 共 7 步')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(w.find('.el-dialog').exists()).toBe(true)

    // 改成 https：校验放过，正常提交
    await dialogRow(w, 'miniprogram_base_url').find('input.el-input').setValue('https://mp.example.com')
    await nextTick()
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('安全确认'))!
      .trigger('click')
    await nextTick()
    await dialogButton(w, '保存并完成').trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ miniprogram_base_url: 'https://mp.example.com' })
  })

  // ---- P1：草稿暂存（刷新不丢） ----
  it('草稿暂存：刷新后恢复上次未保存的改动，可继续编辑', async () => {
    // 模拟「上次改到一半刷新了页面」：localStorage 里留着未保存草稿。
    // setup() 会先 clear，所以这里在挂载前重新种入
    localStorage.setItem(
      'settings_wizard_draft',
      JSON.stringify({ v: 1, draft: { brand_name: '改到一半的品牌' }, cleared: {} }),
    )
    setActivePinia(createPinia())
    const w = mount(Settings, mountOpts)
    await vi.waitFor(() => expect(getSettings).toHaveBeenCalled())
    await nextTick()
    // 草稿值回到输入框（而不是服务端原值），保存按钮可用
    expect((textInputOf(w, 'brand_name').element as HTMLInputElement).value).toBe('改到一半的品牌')
    expect(saveButton(w).attributes('disabled')).toBeUndefined()
    // 明确告知恢复了几项，避免「没改过却显示有变更」的困惑
    expect(
      vi.mocked(ElMessage.info).mock.calls.some((c: any[]) => String(c[0]).includes('已恢复上次未保存的 1 项改动')),
    ).toBe(true)
    // 暂存里没有的 key 不受影响
    expect((textInputOf(w, 'miniprogram_base_url').element as HTMLInputElement).value).toBe('')
  })

  it('草稿暂存：改动即写入；保存成功后清除，不恢复出已保存过的旧改动', async () => {
    const { w } = await setup()
    await textInputOf(w, 'brand_name').setValue('暂存测试')
    await nextTick()
    const raw = JSON.parse(localStorage.getItem('settings_wizard_draft')!)
    expect(raw.draft.brand_name).toBe('暂存测试')

    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    // 保存成功 = 草稿已落地，暂存必须清除（含 watch 在微任务里的兜底清除）
    await vi.waitFor(() => expect(localStorage.getItem('settings_wizard_draft')).toBeNull())
  })

  it('向导无改动时「完成」直接关闭，不发起空保存', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 从第 2 步一路点到安全确认（共 7 步）
    for (let i = 0; i < 5; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    expect(dialogOf(w).text()).toContain('第 7 步 / 共 7 步')
    expect(dialogOf(w).text()).toContain('安全确认')
    const finish = dialogButton(w, '完成')
    expect(finish.exists()).toBe(true)
    await finish.trigger('click')
    await nextTick()
    expect(w.find('.el-dialog').exists()).toBe(false)
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('向导有改动时「保存并完成」：提交变更、成功后关闭，且就绪统计即时推进', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    await dialogRow(w, 'upload_dir').find('input.el-input').setValue('/data/uploads')
    await nextTick()
    for (let i = 0; i < 5; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    // 最后一步点名「本次改动中需重启的项」（upload_dir 是 Restart 项）
    expect(dialogOf(w).text()).toContain('需重启服务后生效')
    const finish = dialogButton(w, '保存并完成')
    await finish.trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ upload_dir: '/data/uploads' })
    // 保存成功 → 对话框关闭；upload_dir 不属于上线就绪检查的 key，
    // 就绪数保持 3/6（不伪造进度），但该行「待配置」标记应随之消失
    expect(w.find('.el-dialog').exists()).toBe(false)
    expect(w.text()).toContain('3 / 6 项就绪')
    expect(rowOf(w, 'upload_dir').text()).not.toContain('待配置')
  })

  it('全新部署（一项都没就绪）进入页面自动拉起配置向导', async () => {
    vi.mocked(getSettings).mockImplementationOnce(async () => ({
      code: 0,
      data: items.map((it) => ({ ...it, value: '', configured: false })),
    }) as any)
    const { w } = await setup()
    await vi.waitFor(() => expect(w.find('.el-dialog').exists()).toBe(true))
    // 品牌名也未配置 → 落在第 1 步
    expect(dialogOf(w).text()).toContain('第 1 步 / 共 7 步')
    expect(dialogOf(w).text()).toContain('品牌与身份')
  })

  // ---- P0：向导易用性优化 ----
  it('步骤条可点击回跳：从第 2 步点回第 1 步', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    const chip = dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('品牌与身份'))
    expect(chip, '未找到品牌步骤 chip').toBeTruthy()
    await chip!.trigger('click')
    await nextTick()
    expect(dialogOf(w).text()).toContain('第 1 步 / 共 7 步')
  })

  it('步骤条带每步待配置数：local 下存储步亮 1', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    const chip = dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('存储与上传'))
    // local 驱动下可见项：storage_driver（已配）/ upload_dir（未配）。
    // upload_base_url 云存储专属，local 下不计入待配置
    expect(chip!.find('.wizard-stepchip-count').text()).toBe('1')
    expect(dialogOf(w).text()).toContain('本步 1 项待配置')
  })

  it('步内小组标题：项多的步骤按主题分堆，与主列表分区同结构', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 默认落在存储步：存储位置 / 访问方式两组
    expect(dialogOf(w).findAll('.wizard-group-title').map((t: any) => t.text())).toEqual([
      '存储位置',
      '访问方式',
    ])
    // local 下：本地上传目录在「存储位置」组可见；云凭据被置灰，整组不出现
    expect(dialogRow(w, 'upload_dir').exists()).toBe(true)
    expect(dialogRowExists(w, 'storage_endpoint')).toBe(false)
    // 公私总闸补进首配路径：local 态也在「访问方式」组
    expect(dialogRow(w, 'storage_public').exists()).toBe(true)

    // 上传限制步：大小与类型 / 临时链接有效期（与主列表上传组一致）
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('上传限制'))!
      .trigger('click')
    await nextTick()
    expect(dialogOf(w).findAll('.wizard-group-title').map((t: any) => t.text())).toEqual([
      '大小与类型',
      '临时链接有效期',
    ])
    expect(dialogRow(w, 'upload_max_mb').exists()).toBe(true)
    expect(dialogRow(w, 'upload_allow_ext').exists()).toBe(true)
    // 预签名时效补齐进向导（原缺口：14 项不在任何步骤）
    expect(dialogRow(w, 'presign_put_ttl_minutes').exists()).toBe(true)
    expect(dialogRow(w, 'sign_get_ttl_minutes').exists()).toBe(true)

    // 小程序步：接入地址 / 独立版锁定（可选）
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('小程序接入'))!
      .trigger('click')
    await nextTick()
    expect(dialogOf(w).findAll('.wizard-group-title').map((t: any) => t.text())).toEqual([
      '接入地址',
      '独立版锁定（可选）',
    ])

    // 微信步：小程序凭据 / 小程序码缓存（原只有两项，缓存时效补齐）
    // chip 上用短名「微信配置」——全名「微信与小程序码」在等宽格子里会折行（见文件末尾的步骤条用例）
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('微信配置'))!
      .trigger('click')
    await nextTick()
    expect(dialogOf(w).findAll('.wizard-group-title').map((t: any) => t.text())).toEqual([
      '小程序凭据',
      '小程序码',
    ])
    expect(dialogRow(w, 'wxacode_ttl_hours').exists()).toBe(true)
    expect(dialogRow(w, 'wxacode_sign_ttl_minutes').exists()).toBe(true)
    expect(dialogRow(w, 'wxacode_rate_limit_per_min').exists()).toBe(true)
    // AppID 未填：AppSecret 置灰不出现（填了 AppID 才需要配 Secret）
    expect(dialogRowExists(w, 'wx_appsecret')).toBe(false)

    // 无 groups 元数据的步骤（品牌）保持平铺，不画小标题
    await dialogOf(w)
      .findAll('.wizard-stepchip')
      .find((c: any) => c.text().includes('品牌与身份'))!
      .trigger('click')
    await nextTick()
    expect(dialogOf(w).findAll('.wizard-group-title')).toHaveLength(0)
    expect(dialogRow(w, 'brand_name').exists()).toBe(true)
  })

  it('格式关键项给示例占位与悬浮说明徽标', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    // 文件访问基址是云存储专属：local 下不出现，切 minio 后才该讲格式
    await dialogRow(w, 'storage_driver').find('select.el-select').setValue('minio')
    await nextTick()
    // minio 下就有 upload_base_url：示例占位讲格式、徽标讲填错的后果
    expect(dialogRow(w, 'upload_base_url').find('input.el-input').attributes('placeholder')).toContain('https://')
    expect(dialogRow(w, 'upload_base_url').text()).toContain('图片全部打不开')
    // 切到小程序步：miniprogram_base_url 也有徽标
    for (let i = 0; i < 3; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    expect(dialogRow(w, 'miniprogram_base_url').text()).toContain('小程序直接连不上')
  })

  it('安全确认步：就绪清单 + 去配置跳回对应步骤 + 只读项给可复制写法', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    for (let i = 0; i < 5; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    const dlg = dialogOf(w)
    // 复用主页 checks 数据：分组 + 就绪数（夹具 3/6，local 驱动无文件访问基址项）
    expect(dlg.text()).toContain('上线就绪')
    expect(dlg.text()).toContain('3 / 6')
    expect(dlg.text()).toContain('本页可配置')
    expect(dlg.text()).toContain('只读项')
    // 只读未就绪项（cookie_secure）给可复制的环境变量写法
    expect(dlg.text()).toContain('COOKIE_SECURE=true')
    // 「去配置」跳回拥有该配置项的步骤：小程序接口基址 → 第 5 步（小程序接入）
    const row = dlg.findAll('.wizard-final-row').find((r: any) => r.text().includes('小程序接口基址'))
    expect(row, '未找到小程序接口基址行').toBeTruthy()
    await row!.find('button.el-button').trigger('click')
    await nextTick()
    expect(dialogOf(w).text()).toContain('第 5 步 / 共 7 步')
  })

  it('安全确认步：说明未进向导的项去哪了（有缺省、可在本页调）', async () => {
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    for (let i = 0; i < 5; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    // 首配路径只走最小集：有缺省 / 可选增强的项不占步骤，但必须说清去哪了，
    // 否则用户会以为系统里只有这几项，该调的限流永远找不到
    expect(dialogOf(w).text()).toContain('未进向导的项')
    expect(dialogOf(w).text()).toContain('登录限流')
    expect(dialogOf(w).text()).toContain('可在本页对应分组按需调整')
  })

  // ---- P2：搜索过滤 + 破坏性更改二次确认 ----
  it('搜索过滤：环境变量名可搜，未命中分组整组隐藏，无匹配给空态提示', async () => {
    const { w } = await setup()
    const search = w.find('input.head-search')
    // 环境变量名搜索（大小写不敏感）：MINIO_ENDPOINT → 只命中存储服务端点
    await search.setValue('minio_endpoint')
    await nextTick()
    expect(w.text()).toContain('匹配 1 /')
    expect(rowExists(w, 'storage_endpoint')).toBe(true)
    expect(rowExists(w, 'brand_name')).toBe(false)
    // 分组中文名也参与匹配：搜「存储」出整个存储组
    await search.setValue('存储')
    await nextTick()
    expect(rowExists(w, 'storage_endpoint')).toBe(true)
    expect(rowExists(w, 'upload_dir')).toBe(true)
    expect(rowExists(w, 'brand_name')).toBe(false)
    // 无匹配：空态提示 + 分组全部隐藏
    await search.setValue('zzz-不存在')
    await nextTick()
    expect(w.find('.filter-empty').exists()).toBe(true)
    expect(w.findAll('.group-card')).toHaveLength(0)
    // 清空搜索恢复全量
    await search.setValue('')
    await nextTick()
    expect(w.findAll('.group-card')).toHaveLength(9)
    expect(rowExists(w, 'brand_name')).toBe(true)
  })

  it('过滤不影响草稿：被搜掉的行保存时仍进载荷（草稿按 key 持有）', async () => {
    const { w } = await setup()
    await textInputOf(w, 'brand_name').setValue('朗汇机电')
    await w.find('input.head-search').setValue('minio_endpoint')
    await nextTick()
    expect(rowExists(w, 'brand_name')).toBe(false)
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ brand_name: '朗汇机电' })
  })

  it('破坏性更改二次确认：存储方式变更弹确认，取消则不提交、草稿保留', async () => {
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    const { w } = await setup()
    await selectOf(w, 'storage_driver').setValue('minio')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1))
    const [msg, title] = vi.mocked(ElMessageBox.confirm).mock.calls[0]
    expect(title).toBe('破坏性更改确认')
    expect(msg).toContain('存储方式')
    expect(msg).toContain('历史文件')
    // 未确认前不提交；草稿保留（保存按钮仍可用，可改回去或再次确认）
    expect(updateSettings).not.toHaveBeenCalled()
    expect(saveButton(w).attributes('disabled')).toBeUndefined()
  })

  it('破坏性确认通过后正常提交（确认即保存）', async () => {
    const { w } = await setup()
    await selectOf(w, 'storage_driver').setValue('oss')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledTimes(1))
    expect(updateSettings).toHaveBeenCalledWith({ storage_driver: 'oss' })
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1)
  })

  it('无害改动不弹确认：品牌名随时可改，不该被二次确认骚扰', async () => {
    const { w } = await setup()
    await textInputOf(w, 'brand_name').setValue('朗汇机电')
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(updateSettings).toHaveBeenCalledWith({ brand_name: '朗汇机电' }))
    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
  })

  it('清空敏感项同样需确认：消息讲清回退环境变量的后果', async () => {
    const { w } = await setup()
    await textInputOf(w, 'wx_appid').setValue('wx123')
    await nextTick()
    const secretClear = rowOf(w, 'wx_appsecret').findAll('button.el-button')
    await secretClear[0].trigger('click')
    await nextTick()
    await saveButton(w).trigger('click')
    await vi.waitFor(() => expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1))
    expect(vi.mocked(ElMessageBox.confirm).mock.calls[0][0]).toContain('回退环境变量')
    // 默认确认 → 正常提交空串
    expect(updateSettings).toHaveBeenCalledWith({ wx_appid: 'wx123', wx_appsecret: '' })
  })

  it('向导内破坏性改动：确认被取消则对话框不关、不提交', async () => {
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    const { w } = await setup()
    await wizardEntryButton(w).trigger('click')
    await nextTick()
    await dialogRow(w, 'storage_driver').find('select.el-select').setValue('minio')
    await nextTick()
    for (let i = 0; i < 5; i++) {
      await dialogButton(w, '下一步').trigger('click')
      await nextTick()
    }
    await dialogButton(w, '保存并完成').trigger('click')
    await vi.waitFor(() => expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1))
    expect(updateSettings).not.toHaveBeenCalled()
    // 对话框保持打开，用户可改回来或重新确认
    expect(dialogOf(w).exists()).toBe(true)
  })

  // 三项路径配置各自要能弹出自己的选择器。
  // 2026-10-25 用户实测：只有「Node 可执行文件」能弹窗，另外两项点了没反应——
  // 根因是后端空切片序列化成 null，选择器渲染时对 null 取 .length 抛异常，
  // 弹窗体渲染失败。这里逐项验证「按钮存在 + 点开就是对应标题的弹窗」，防止回归。
  it('三项路径配置的「浏览…」都能打开各自的服务端选择器', async () => {
    const { w } = await setup()
    const cases = [
      ['mp_node_bin', 'node_bin', '选择 Node 可执行文件'],
      ['mp_ci_module_dir', 'module_dir', '选择 miniprogram-ci 安装目录'],
      ['mp_source_dir', 'source_dir', '选择小程序源码目录'],
    ]
    for (const [key, kind, title] of cases) {
      const row = rowOf(w, key)
      const btn = row.find('.path-browse')
      expect(btn.exists(), `${key} 缺少「浏览…」按钮`).toBe(true)

      await btn.trigger('click')
      await nextTick()
      // 弹窗按标题定位：三行同页，只按 .el-dialog 数量断言会分不清是谁打开的
      const dlg = w.findAll('.el-dialog').find((d) => d.text().includes(title))
      expect(dlg, `${key} 未打开「${title}」`).toBeTruthy()
      // 不只是壳子：弹窗挂载后要真的按对应 kind 去拉目录（pickerReady 是「首次点开才挂载」，
      // 少了这次请求就是空白弹窗）
      expect(browseServerPaths, `${key} 打开后未请求目录`).toHaveBeenCalledWith(kind, undefined)

      // 关掉再测下一项（取消按钮回写 visible=false）
      const cancel = dlg!.findAll('button.el-button').find((b) => b.text().includes('取消'))
      await cancel!.trigger('click')
      await nextTick()
      expect(
        w.findAll('.el-dialog').some((d) => d.text().includes(title)),
        `${key} 的弹窗关不掉`,
      ).toBe(false)
    }
  })
})

// 步骤条排版回归：弹窗宽度固定 720px，7 步挤在等宽网格里，标题一长就折行，
// 极端情况下（「后台上传小程序代码」9 字）会被逐字断成一列竖排——截图里出现过。
// 约束：chip 只用短名（≤6 字），全名靠进度行交代，两边都不丢信息。
describe('配置向导步骤条', () => {
  it('每个步骤 chip 的标题不超过 6 字，长名步骤在进度行仍显示全名', async () => {
    const { w } = await setup()
    const openBtn = w.findAll('button').find((b: any) => b.text() === '配置向导')
    expect(openBtn, '未找到「配置向导」按钮').toBeTruthy()
    await openBtn!.trigger('click')
    await nextTick()

    const chips = w.findAll('.wizard-stepchip')
    expect(chips.length, '向导步骤数变了？同步检查短名是否够用').toBe(7)
    for (const c of chips) {
      const title = c.find('.wizard-stepchip-title').text()
      expect(title.length, `步骤条标题「${title}」超过 6 字，等宽格子里会折行`).toBeLessThanOrEqual(6)
      expect(title.trim()).not.toBe('')
    }

    // 点进「代码上传」步：chip 上是短名，进度行必须给出全名，否则用户看不出这步到底在配什么
    const mpChip = chips.find((c: any) => c.text().includes('代码上传'))
    expect(mpChip, '「代码上传」步骤 chip 缺失').toBeTruthy()
    await mpChip!.trigger('click')
    await nextTick()
    expect(w.find('.wizard-progress').text()).toContain('后台上传小程序代码')
  })
})
