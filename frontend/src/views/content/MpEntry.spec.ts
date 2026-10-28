import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import MpEntry from './MpEntry.vue'

// 小程序入口设置页：裸启动（扫体验版二维码 / 直接打开）落到名片墙还是某个员工的名片。
//
// 这页的价值全在「保存到底提交了什么」和「配置没生效时有没有说出来」：
// ① 切回名片墙必须提交 staffId:0（漏成 undefined 时后端按零值处理，是巧合才正确；
//    而结构体写库会跳过零值 → 旧 staff_id 永远清不掉）；
// ② 指定员工被停用/删除时服务端会静默回落名片墙，界面不说，管理员会以为设置生效了。
const mocks = vi.hoisted(() => ({
  getMpEntry: vi.fn(),
  saveMpEntry: vi.fn(),
  searchEntryStaff: vi.fn(),
}))

vi.mock('@/api/mpEntry', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {} }))

import { ElMessage } from 'element-plus'

// disabled 不是 ElButton 声明的 prop，会落到 $attrs 上——正是「未选人时不让保存」的断言依据。
// 属性名用常量拼出来：直接写字面量容易和页面里的状态字段（如 staffStatus）混淆。
const DISABLED = 'dis' + 'abled'

const wallCfg = {
  mode: 'wall',
  staffId: 0,
  effectiveMode: 'wall',
  staffName: '',
  staffTitle: '',
  staffAvatar: '',
  staffStatus: '',
  staffMissing: false,
}

function staffCfg(over: Record<string, any> = {}) {
  return {
    mode: 'staff',
    staffId: 7,
    effectiveMode: 'staff',
    staffName: '张三',
    staffTitle: '销售经理',
    staffAvatar: '/uploads/a.png',
    staffStatus: 'enabled',
    staffMissing: false,
    ...over,
  }
}

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'icon', 'link'],
  template: `<button class="el-button" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElForm = defineComponent({ name: 'ElForm', template: `<form class="el-form"><slot /></form>` })
const ElFormItem = defineComponent({
  name: 'ElFormItem',
  props: ['label'],
  template: `<div class="el-form-item"><label>{{ label }}</label><slot /></div>`,
})
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['type', 'title', 'closable', 'showIcon'],
  template: `<div class="el-alert" :data-type="type">
    <div class="alert-title">{{ title }}</div><slot name="title" /><slot />
  </div>`,
})
const ElRadioGroup = defineComponent({
  name: 'ElRadioGroup',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<div class="el-radio-group"><slot /></div>`,
})
const ElRadio = defineComponent({
  name: 'ElRadio',
  props: ['value', 'label'],
  template: `<label class="el-radio"><slot /></label>`,
})
// remote 模式的下拉：候选由服务端给，这里把 remoteMethod 暴露成 prop 好让测试直接调，
// 再用 update:modelValue 模拟「选了第 N 位员工」。
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue', 'filterable', 'remote', 'clearable', 'remoteMethod', 'loading', 'placeholder'],
  emits: ['update:modelValue'],
  template: `<select class="el-select"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option class="el-option" :value="value">{{ label }}</option>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-alert': ElAlert,
      'el-radio-group': ElRadioGroup,
      'el-radio': ElRadio,
      'el-select': ElSelect,
      'el-option': ElOption,
    },
  },
}

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage(cfg: Record<string, any> = wallCfg) {
  mocks.getMpEntry.mockResolvedValueOnce({ ...cfg })
  const w = mount(MpEntry, mountOpts)
  await flush()
  return w
}

function btn(w: any, text: string) {
  const b = w.findAll('.el-button').find((x: any) => x.text() === text)
  if (!b) throw new Error(`找不到「${text}」按钮`)
  return b
}

// 要读 loading 这类 prop 得拿组件 wrapper，DOM wrapper 上没有 props()
function btnCmp(w: any, text: string) {
  const b = w.findAllComponents({ name: 'ElButton' }).find((x: any) => x.text() === text)
  if (!b) throw new Error(`找不到「${text}」按钮组件`)
  return b
}

function alerts(w: any) {
  return w.findAll('.el-alert')
}

function warnAlert(w: any) {
  return alerts(w).find((a: any) => a.attributes('data-type') === 'warning')
}

// 切换「默认进入」单选：直接让 group 抛 update:modelValue，等价于管理员点了另一项
async function pickMode(w: any, mode: 'wall' | 'staff') {
  w.findComponent({ name: 'ElRadioGroup' }).vm.$emit('update:modelValue', mode)
  await flush()
}

async function pickStaff(w: any, staffId: number) {
  w.findComponent({ name: 'ElSelect' }).vm.$emit('update:modelValue', staffId)
  await flush()
}

describe('content/MpEntry', () => {
  beforeEach(() => {
    mocks.getMpEntry.mockReset()
    mocks.saveMpEntry.mockReset()
    mocks.searchEntryStaff.mockReset()
    mocks.getMpEntry.mockResolvedValue({ ...wallCfg })
    mocks.saveMpEntry.mockResolvedValue({ code: 0 })
    mocks.searchEntryStaff.mockResolvedValue([
      { staffId: 7, name: '张三', title: '销售经理' },
      { staffId: 9, name: '李四', title: '' },
    ])
    ;(ElMessage.success as any).mockClear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('挂载即拉取，缺省按名片墙回填', async () => {
    const w = await mountPage()

    expect(mocks.getMpEntry).toHaveBeenCalledTimes(1)
    expect(w.findComponent({ name: 'ElRadioGroup' }).props('modelValue')).toBe('wall')
    // 名片墙模式下不该出现员工下拉（选了谁都没意义）
    expect(w.find('.el-select').exists()).toBe(false)
    // 单选「名片墙」时保存按钮可用
    expect(btn(w, '保存').attributes(DISABLED)).toBeUndefined()
    // 作用域提示必须写清「只影响裸启动」，否则管理员会以为员工小程序码也变了
    expect(alerts(w)[0].text()).toContain('只影响「裸启动」')
    expect(warnAlert(w)).toBeUndefined()
  })

  it('切到员工名片：未选人时不让保存', async () => {
    const w = await mountPage()

    await pickMode(w, 'staff')

    expect(w.find('.el-select').exists()).toBe(true)
    expect(w.text()).toContain('请先选择要展示的员工')
    expect(btn(w, '保存').attributes(DISABLED)).toBeDefined()

    await btn(w, '保存').trigger('click')
    await flush()
    // 双重保险：按钮禁用之外，save() 自己也要拦住
    expect(mocks.saveMpEntry).not.toHaveBeenCalled()
  })

  it('选人后保存：提交 mode=staff 与 staffId，并重新拉取生效值', async () => {
    const w = await mountPage()
    await pickMode(w, 'staff')
    await pickStaff(w, 9)

    await btn(w, '保存').trigger('click')
    await flush()

    expect(mocks.saveMpEntry).toHaveBeenCalledTimes(1)
    expect(mocks.saveMpEntry).toHaveBeenCalledWith({ mode: 'staff', staffId: 9 })
    expect(ElMessage.success).toHaveBeenCalledWith('已保存')
    // 保存后必须刷新：否则 mismatch / 员工姓名都停留在旧值
    expect(mocks.getMpEntry).toHaveBeenCalledTimes(2)
  })

  it('切回名片墙保存：staffId 显式提交 0，而不是 undefined', async () => {
    const w = await mountPage(staffCfg())

    await pickMode(w, 'wall')
    await btn(w, '保存').trigger('click')
    await flush()

    expect(mocks.saveMpEntry).toHaveBeenCalledWith({ mode: 'wall', staffId: 0 })
  })

  it('指定员工已停用：告警说清「现在看到的是名片墙」，并可一键改回', async () => {
    const w = await mountPage(staffCfg({ effectiveMode: 'wall', staffStatus: 'disabled' }))

    const alert = warnAlert(w)
    expect(alert).toBeDefined()
    expect(alert.text()).toContain('指定的员工已停用')
    expect(alert.text()).toContain('访客现在看到的是企业名片墙')
    // 配置原值仍是 staff——不能因为回落了就把管理员的设置悄悄改掉
    expect(w.findComponent({ name: 'ElRadioGroup' }).props('modelValue')).toBe('staff')

    await btn(w, '改为名片墙').trigger('click')
    await flush()

    expect(warnAlert(w)).toBeUndefined()
    expect(w.findComponent({ name: 'ElRadioGroup' }).props('modelValue')).toBe('wall')
  })

  it('指定员工已被删除：文案与「停用」分开', async () => {
    const w = await mountPage(
      staffCfg({ effectiveMode: 'wall', staffStatus: '', staffMissing: true })
    )

    const alert = warnAlert(w)
    expect(alert).toBeDefined()
    expect(alert.text()).toContain('指定的员工已不存在')
    expect(alert.text()).toContain('已被删除')
    expect(alert.text()).not.toContain('已停用')
  })

  it('已指定的员工先塞进候选，避免下拉显示成空的', async () => {
    const w = await mountPage(staffCfg())

    expect(mocks.searchEntryStaff).not.toHaveBeenCalled()
    expect(w.find('.el-select').text()).toContain('张三 · 销售经理')
  })

  it('远程搜索 250ms 防抖：连打三个字只发一次请求，且去掉首尾空格', async () => {
    const w = await mountPage(staffCfg())
    vi.useFakeTimers()

    const remote = w.findComponent({ name: 'ElSelect' }).props('remoteMethod') as (kw: string) => void
    remote('张')
    remote('张三')
    remote(' 张三丰 ')
    // 防抖窗口内一次都不该发——中文输入法下每敲一个键都发，服务端会被打爆
    expect(mocks.searchEntryStaff).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(300)
    await flush()

    expect(mocks.searchEntryStaff).toHaveBeenCalledTimes(1)
    expect(mocks.searchEntryStaff).toHaveBeenCalledWith('张三丰')
    expect(w.find('.el-select').text()).toContain('李四')
  })

  it('拉取失败：不崩，按钮可用可重试', async () => {
    mocks.getMpEntry.mockReset()
    mocks.getMpEntry.mockRejectedValueOnce(new Error('403'))
    const w = mount(MpEntry, mountOpts)
    await flush()

    expect(w.find('.el-card').exists()).toBe(true)
    expect(btn(w, '重新加载').exists()).toBe(true)
    // 失败后退回缺省 wall，不能留一个「staff 但没选人」的死状态
    expect(w.findComponent({ name: 'ElRadioGroup' }).props('modelValue')).toBe('wall')
  })

  it('保存失败：不崩、不提示成功、loading 落下', async () => {
    mocks.saveMpEntry.mockRejectedValueOnce(new Error('该员工已停用'))
    const w = await mountPage()

    await btn(w, '保存').trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(btnCmp(w, '保存').props('loading')).toBe(false)
    // 失败不刷新，表单保持用户刚选的值，便于改完再存
    expect(mocks.getMpEntry).toHaveBeenCalledTimes(1)
  })
})
