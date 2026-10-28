import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import SetupInstaller from './SetupInstaller.vue'
import type { SetupStatus } from '@/api/setup'

// 安装向导的接口层整体 mock：这里验证的是「表单校验 → 确认 → 调安装 → 进度状态」
// 这条链路，不碰真实后端。element-plus 只用到 ElMessage（命令式），mock 掉即可。
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}))
vi.mock('@/api/setup', () => ({
  testConnection: vi.fn(),
  install: vi.fn(),
  fetchSetupStatus: vi.fn(),
  getSetupStatus: vi.fn(),
}))

import { testConnection, install as runInstall } from '@/api/setup'

// 轻量 stub：jsdom 下挂真实 Element Plus 太重（且会 OOM），只保留「v-model + 点击」语义。
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'type', 'showPassword', 'autocomplete'],
  emits: ['update:modelValue'],
  // 注意：stub 的 template 是运行时编译的，不能写 TS 断言（`as HTMLInputElement`）
  template: `<input class="stub-input" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['loading', 'type', 'size'],
  emits: ['click'],
  template: `<button class="stub-btn" @click="$emit('click')"><slot /></button>`,
})
const ElCheckbox = defineComponent({
  name: 'ElCheckbox',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  // 支持点击切换：v-model 靠 update:modelValue 回写，stub 不实现这个事件
  // 就没法在测试里勾选（曾经漏了，结果「勾选演示数据」这类用例根本点不动）。
  template: `<label class="stub-chk" @click="$emit('update:modelValue', !modelValue)"><slot /></label>`,
})
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['type', 'title', 'closable', 'showIcon'],
  template: `<div class="stub-alert" :data-type="type">{{ title }}</div>`,
})
// 测试里把 el-tooltip 拍平：触发徽标 + 说明文本一起渲染，便于断言说明仍在 DOM。
const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'placement'],
  template: `<span class="stub-tip"><slot /><span class="stub-tip-content"><slot name="content">{{ content }}</slot></span></span>`,
})

const stubs = {
  'el-input': ElInput,
  'el-button': ElButton,
  'el-checkbox': ElCheckbox,
  'el-alert': ElAlert,
  'el-tooltip': ElTooltip,
}

function status(over: Partial<SetupStatus> = {}): SetupStatus {
  return {
    configured: false,
    missing: [],
    envText: 'DB_DSN=sqlite:///data/weichuan.db\nJWT_KEY=generated',
    hint: '',
    installable: true,
    engines: ['mysql', 'sqlite'],
    sqlitePath: './data/weichuan.db',
    configFile: './data/runtime.env',
    adminUser: 'admin',
    mysqlHint: {
      host: 'mysql',
      port: '3306',
      user: 'root',
      database: 'weichuan',
      note: '检测到编排环境',
    },
    ...over,
  }
}

function mountInstaller(s: SetupStatus = status()) {
  return mount(SetupInstaller, { props: { status: s }, global: { stubs } })
}

// 表单字段靠 label 文本定位：模板结构是 <label>文本<el-input/></label>，
// 用索引取输入框会在增删字段时静默错位。
// 必须限定在某个 section 内查找：「数据库」与「管理员账号」两块都有叫「密码」的字段，
// 全局找会命中 MySQL 密码而不是管理员密码（这曾让测试静默测错对象）。
function block(w: ReturnType<typeof mountInstaller>, title: string) {
  const s = w.findAll('section').find((x) => x.text().includes(title))
  if (!s) throw new Error(`未找到区块：${title}`)
  return s
}

function field(root: ReturnType<typeof block>, label: string) {
  const l = root.findAll('label').find((x) => x.text().trim().startsWith(label))
  if (!l) throw new Error(`未找到字段：${label}`)
  return l.find('input')
}

function button(w: ReturnType<typeof mountInstaller>, text: string) {
  const b = w.findAll('button').find((x) => x.text().includes(text))
  if (!b) throw new Error(`未找到按钮：${text}`)
  return b
}

describe('SetupInstaller', () => {
  beforeEach(() => {
    vi.mocked(runInstall).mockReset()
    vi.mocked(testConnection).mockReset()
  })

  it('渲染安装向导骨架与编排环境预填值', async () => {
    const w = mountInstaller()
    // 预填发生在 onMounted，DOM 更新在下一拍才落地
    await nextTick()
    expect(w.text()).toContain('安装向导')
    expect(w.text()).toContain('选择数据库')
    expect(w.text()).toContain('管理员账号')
    // compose 环境（mysqlHint）预填：主机是服务名 mysql
    const db = block(w, '选择数据库')
    expect((field(db, '主机').element as HTMLInputElement).value).toBe('mysql')
    expect((field(db, '用户名').element as HTMLInputElement).value).toBe('root')
  })

  it('管理员密码不足 8 位时拦在本地，不发安装请求', async () => {
    const w = mountInstaller()
    await field(block(w, '管理员账号'), '密码').setValue('short')
    await button(w, '开始安装').trigger('click')
    expect(w.text()).toContain('管理员密码至少 8 位')
    expect(w.find('.si-confirm').exists()).toBe(false)
    expect(runInstall).not.toHaveBeenCalled()
  })

  it('两次密码不一致时拦在本地', async () => {
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-124')
    await button(w, '开始安装').trigger('click')
    expect(w.text()).toContain('两次输入的管理员密码不一致')
    expect(runInstall).not.toHaveBeenCalled()
  })

  it('校验通过后先出现确认条，确认才真正调安装接口', async () => {
    vi.mocked(runInstall).mockResolvedValue({
      ok: true,
      driver: 'mysql',
      dsn: 'root:******@tcp(mysql:3306)/weichuan',
      configFile: './data/runtime.env',
      restarted: false,
      message: '',
    })
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-123')
    await button(w, '开始安装').trigger('click')

    expect(w.find('.si-confirm').exists()).toBe(true)
    expect(runInstall).not.toHaveBeenCalled() // 还没确认

    await button(w, '确认安装').trigger('click')
    await vi.waitFor(() => expect(runInstall).toHaveBeenCalledTimes(1))

    const payload = vi.mocked(runInstall).mock.calls[0][0]
    expect(payload.engine).toBe('mysql')
    expect(payload.dbHost).toBe('mysql')
    expect(payload.dbName).toBe('weichuan')
    expect(payload.adminPass).toBe('installer-pass-123')

    // 进入进度区；restarted=false（Windows / 托管环境）要给人工重启提示
    expect(w.text()).toContain('安装进度')
    expect(w.text()).toContain('请手动重启服务')
  })

  it('安装接口报错时回显错误且停留在表单', async () => {
    vi.mocked(runInstall).mockRejectedValue(new Error('目标数据库已初始化（已有 1 个管理员账号）'))
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-123')
    await button(w, '开始安装').trigger('click')
    await button(w, '确认安装').trigger('click')

    await vi.waitFor(() => expect(w.find('.stub-alert').exists()).toBe(true))
    expect(w.text()).toContain('目标数据库已初始化')
    // 进度区在模板里常驻（v-show），不能靠 text() 判断是否切走了表单
    expect(w.find('.si-psteps').isVisible()).toBe(false)
  })

  it('测试连接成功展示 driver 与备注', async () => {
    vi.mocked(testConnection).mockResolvedValue({
      driver: 'mysql',
      dsn: 'root:******@tcp(mysql:3306)/weichuan',
      willCreate: true,
      note: '数据库不存在，安装时将自动创建',
    })
    const w = mountInstaller()
    await field(block(w, '选择数据库'), '密码').setValue('db-secret')
    await button(w, '测试连接').trigger('click')

    await vi.waitFor(() => expect(w.find('.stub-alert').exists()).toBe(true))
    expect(w.find('.stub-alert').attributes('data-type')).toBe('success')
    expect(w.text()).toContain('连接正常（mysql）')
  })

  it('手动配置入口展开后可复制兜底配置文本', async () => {
    const w = mountInstaller()
    await button(w, '手动配置（不使用安装向导）').trigger('click')
    await nextTick()
    expect(w.text()).toContain('手动配置（不使用安装向导）')
    expect(w.text()).toContain('DB_DSN=sqlite:///data/weichuan.db')
  })

  it('高级选项用悬浮徽标承载说明，默认不占版面', () => {
    const w = mountInstaller()
    // 说明以悬浮「?」徽标形式存在，文本仍在 DOM（stub 拍平）可断言
    const badges = w.findAll('.stub-tip .si-hint')
    expect(badges.length).toBeGreaterThanOrEqual(2)
    expect(w.text()).toContain('登录签名密钥')
    expect(w.text()).toContain('至少 16 位')
    expect(w.text()).toContain('所有人已登录的状态立即失效')
    expect(w.text()).toContain('登录 Cookie 加 Secure 标记')
    expect(w.text()).toContain('浏览器只在 HTTPS 连接下发送')
  })

  it('管理员步骤含「装载演示企业数据」勾选项，默认不勾（商用零数据基线）', async () => {
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    const chk = admin.find('.si-demo .stub-chk')
    expect(chk.exists()).toBe(true)
    expect(w.text()).toContain('同时装载演示企业数据（朗汇机电样板）')
    expect(w.text()).toContain('不勾选则装完是空白系统')
    // 填合法密码才能越过本地预检、进入确认条
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-123')
    // 默认不勾：确认条列出的是「空白（装完先创建企业）」
    await button(w, '开始安装').trigger('click')
    expect(w.find('.si-confirm').exists()).toBe(true)
    // 仅看确认块：勾选项的 label 还在隐藏的管理员步骤里，整体 text() 会误命中
    const confirm = w.find('.si-confirm')
    expect(confirm.text()).toContain('空白（装完先创建企业）')
    expect(confirm.text()).not.toContain('装载演示企业数据（朗汇机电样板）')
  })

  it('勾选演示数据：确认条列出样板企业，安装 payload 带上 loadDemoData=true', async () => {
    vi.mocked(runInstall).mockResolvedValue({
      ok: true,
      driver: 'mysql',
      dsn: 'root:******@tcp(mysql:3306)/weichuan',
      configFile: './data/runtime.env',
      restarted: false,
      message: '',
    })
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-123')
    // 勾选演示数据
    await w.find('.si-demo .stub-chk').trigger('click')
    await nextTick()
    await button(w, '开始安装').trigger('click')
    expect(w.find('.si-confirm').exists()).toBe(true)
    expect(w.text()).toContain('装载演示企业数据（朗汇机电样板）')
    await button(w, '确认安装').trigger('click')
    await vi.waitFor(() => expect(runInstall).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(runInstall).mock.calls[0][0]
    expect(payload.loadDemoData).toBe(true)
  })

  it('不勾演示数据：payload 明确带 loadDemoData=false（零数据基线，不静默漏掉字段）', async () => {
    vi.mocked(runInstall).mockResolvedValue({
      ok: true,
      driver: 'mysql',
      dsn: 'root:******@tcp(mysql:3306)/weichuan',
      configFile: './data/runtime.env',
      restarted: false,
      message: '',
    })
    const w = mountInstaller()
    const admin = block(w, '管理员账号')
    await field(admin, '密码').setValue('installer-pass-123')
    await field(admin, '确认密码').setValue('installer-pass-123')
    await button(w, '开始安装').trigger('click')
    await button(w, '确认安装').trigger('click')
    await vi.waitFor(() => expect(runInstall).toHaveBeenCalledTimes(1))
    const payload = vi.mocked(runInstall).mock.calls[0][0]
    expect(payload.loadDemoData).toBe(false)
  })
})
