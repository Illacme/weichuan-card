import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import MpSettings from './MpSettings.vue'
import MpEntry from './MpEntry.vue'
import HomeLayout from './HomeLayout.vue'
import MpTheme from './MpTheme.vue'
import { routes } from '@/router'

// 「小程序设置」= 一个菜单项 + 三个页签（访客先看到谁 / 名片页面样式 / 整体风格）。
//
// 拆开时客户得自己猜「首页布局」其实也是小程序的事，合并后菜单少一项、名字变成
// 客户语言。但合并有一个真风险：两页是**两个接口**，页签若同时挂载，进页面就会
// 多打一个请求（企业管理员第一次进这页会看到两个 loading、两次可能失败的提示）。
// 所以这里的核心用例是「未激活的页签不挂载」——它对应 el-tab-pane 的 lazy，
// 漏了这个属性，页面照样能用，只是白打一次接口，靠人眼看是看不出来的。
const mpMocks = vi.hoisted(() => ({
  getMpEntry: vi.fn(),
  saveMpEntry: vi.fn(async (_p: any) => ({ code: 0 })),
  searchEntryStaff: vi.fn(async (_k: string) => []),
}))
const contentMocks = vi.hoisted(() => ({
  getHomeLayout: vi.fn(),
  saveHomeLayout: vi.fn(async (_p: any) => ({ code: 0 })),
}))

vi.mock('@/api/mpEntry', () => mpMocks)
vi.mock('@/api/content', () => contentMocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {} }))

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

// ── 两套子页面的桩（合并自 MpEntry.spec / HomeLayout.spec） ──────────────
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
const ElDivider = defineComponent({
  name: 'ElDivider',
  props: ['contentPosition'],
  template: `<div class="el-divider"><slot /></div>`,
})
const ElTag = defineComponent({ name: 'ElTag', template: `<span class="el-tag"><slot /></span>` })
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
  setup(_props, { slots, emit }) {
    provide('radioGroup', { select: (v: string) => emit('update:modelValue', v) })
    return () => h('div', { class: 'el-radio-group' }, slots.default ? slots.default() : [])
  },
})
const ElRadio = defineComponent({
  name: 'ElRadio',
  props: ['value', 'label'],
  setup(props, { slots }) {
    const group = inject<any>('radioGroup', null)
    return () =>
      h(
        'button',
        { class: 'el-radio', onClick: () => group && group.select(props.value ?? props.label) },
        slots.default ? slots.default() : []
      )
  },
})
const ElSlider = defineComponent({
  name: 'ElSlider',
  props: ['modelValue', 'min', 'max', 'step'],
  emits: ['update:modelValue'],
  template: `<input class="el-slider" type="range" :value="modelValue"
    @input="$emit('update:modelValue', Number($event.target.value))" />`,
  inheritAttrs: true,
})
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
const UploadField = defineComponent({
  name: 'UploadField',
  props: ['modelValue', 'multiple', 'libraryType', 'tip'],
  emits: ['update:modelValue'],
  template: `<button class="upload-field" @click="$emit('update:modelValue', '/uploads/bg.jpg')">
    {{ modelValue }}</button>`,
})
const ImageViewer = defineComponent({
  name: 'ImageViewer',
  props: ['urlList'],
  emits: ['close'],
  template: `<div class="image-viewer" @click="$emit('close')">{{ urlList[0] }}</div>`,
})

// el-tabs / el-tab-pane：复刻「lazy = 首次激活才渲染，之后保留」。
// 桩必须严格按 lazy 语义走，否则「漏写 lazy」这条缺陷会被桩吞掉（桩替代码兜底了）。
// lazy 必须声明成 Boolean：模板里裸写 `lazy` 在没声明类型的桩上是空字符串（假值），
// 桩会退化成「全都渲染」，缺陷就测不出来了。
const ElTabs = defineComponent({
  name: 'ElTabs',
  props: ['modelValue'],
  emits: ['update:modelValue', 'tab-change'],
  setup(props, { slots }) {
    provide('tabsActive', computed(() => props.modelValue))
    return () => h('div', { class: 'el-tabs' }, slots.default ? slots.default() : [])
  },
})
const ElTabPane = defineComponent({
  name: 'ElTabPane',
  props: { label: String, name: String, lazy: Boolean },
  setup(props, { slots }) {
    const active = inject<any>('tabsActive')
    let seen = false
    return () => {
      const isActive = active?.value === props.name
      if (isActive) seen = true
      if (!isActive && props.lazy && !seen) return null
      return h(
        'div',
        { class: 'el-tab-pane', 'data-name': props.name, 'data-active': String(isActive) },
        slots.default ? slots.default() : []
      )
    }
  },
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-divider': ElDivider,
      'el-tag': ElTag,
      'el-alert': ElAlert,
      'el-radio-group': ElRadioGroup,
      'el-radio': ElRadio,
      'el-slider': ElSlider,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-tabs': ElTabs,
      'el-tab-pane': ElTabPane,
      UploadField,
      ImageViewer,
    },
    directives: { loading: {} },
  },
}

const Blank = defineComponent({ template: `<div />` })

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage(query = '') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/content/mp-settings', component: Blank }],
  })
  await router.push(`/content/mp-settings${query}`)
  await router.isReady()
  const w = mount(MpSettings, { ...mountOpts, global: { ...mountOpts.global, plugins: [router] } })
  await flush()
  return { w, router }
}

async function pickTab(w: any, name: string) {
  const tabs = w.findComponent({ name: 'ElTabs' })
  tabs.vm.$emit('update:modelValue', name)
  tabs.vm.$emit('tab-change', name)
  await flush()
}

function pane(w: any, name: string) {
  return w.find(`.el-tab-pane[data-name="${name}"]`)
}

describe('content/MpSettings', () => {
  beforeEach(() => {
    mpMocks.getMpEntry.mockReset()
    mpMocks.getMpEntry.mockResolvedValue({ ...wallCfg })
    mpMocks.saveMpEntry.mockClear()
    mpMocks.searchEntryStaff.mockClear()
    contentMocks.getHomeLayout.mockReset()
    contentMocks.getHomeLayout.mockResolvedValue({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      styleId: 'default',
      brandColorId: null,
      templateEntitled: true,
      templates: [
        { id: 'default', name: '经典蓝', description: '', advanced: false, previewPrimary: '#1a8cff', previewSoft: '#e8f1fb' },
      ],
    })
    contentMocks.saveHomeLayout.mockClear()
  })

  it('默认进「访客先看到谁」：只拉入口配置，不碰首页布局', async () => {
    const { w } = await mountPage()

    expect(mpMocks.getMpEntry).toHaveBeenCalledTimes(1)
    // 未激活的页签必须不挂载（lazy）：否则企业管理员一进这页就打两个接口
    expect(contentMocks.getHomeLayout).not.toHaveBeenCalled()
    expect(pane(w, 'entry').exists()).toBe(true)
    expect(pane(w, 'appearance').exists()).toBe(false)
  })

  it('两个子页面都按内嵌模式渲染（不重复自己的页面标题）', async () => {
    const { w } = await mountPage()

    expect(w.findComponent(MpEntry).props('embedded')).toBe(true)
    await pickTab(w, 'appearance')
    expect(w.findComponent(HomeLayout).props('embedded')).toBe(true)
  })

  it('切到「名片首页样式」才挂载首页布局，且只拉一次', async () => {
    const { w } = await mountPage()

    await pickTab(w, 'appearance')

    expect(contentMocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(pane(w, 'appearance').attributes('data-active')).toBe('true')
    expect(pane(w, 'appearance').text()).toContain('板块顺序')
  })

  it('切走再切回不重复请求（页签已渲染过就保留，不是每次切换都重新拉）', async () => {
    const { w } = await mountPage()
    await pickTab(w, 'appearance')
    await pickTab(w, 'entry')
    await pickTab(w, 'appearance')

    expect(mpMocks.getMpEntry).toHaveBeenCalledTimes(1)
    expect(contentMocks.getHomeLayout).toHaveBeenCalledTimes(1)
  })

  it('?tab=appearance 直接落在首页样式（旧路由 /content/home-layout 重定向的目标）', async () => {
    const { w } = await mountPage('?tab=appearance')

    expect(contentMocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(mpMocks.getMpEntry).not.toHaveBeenCalled()
    expect(pane(w, 'entry').exists()).toBe(false)
  })

  it('tab 值不认识时回落第一个页签（旧链接带脏参数不该白屏）', async () => {
    const { w } = await mountPage('?tab=nonsense')

    expect(pane(w, 'entry').exists()).toBe(true)
    expect(mpMocks.getMpEntry).toHaveBeenCalledTimes(1)
    expect(contentMocks.getHomeLayout).not.toHaveBeenCalled()
  })

  // 合并后旧路径继续存在：文档、收藏、群聊天记录里的链接不能变成空白页
  it('旧的 /content/home-layout 与 /content/mp-entry 重定向到对应页签', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: routes as any })

    await router.push('/content/home-layout')
    expect(router.currentRoute.value.path).toBe('/content/mp-settings')
    expect(router.currentRoute.value.query.tab).toBe('appearance')

    await router.push('/content/mp-entry')
    expect(router.currentRoute.value.path).toBe('/content/mp-settings')
    expect(router.currentRoute.value.query.tab).toBe('entry')
  })

  it('切页签把 tab 写进 URL：照文档发链接给同事，打开就是那一页', async () => {
    const { w, router } = await mountPage()
    expect(router.currentRoute.value.query.tab).toBeUndefined()

    await pickTab(w, 'appearance')
    // 导航是异步的：只 flush 微任务不够，用 waitFor 等 replace 落地
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('appearance'))

    await pickTab(w, 'entry')
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('entry'))
  })

  // 「风格模板」是第三个页签，同样必须 lazy：它和首页样式共用 home-layout 接口，
  // 一进「小程序设置」就把三页全挂载会多打两次请求，弱网下三个 loading 一起转。
  it('默认不挂载「风格模板」页签', async () => {
    const { w } = await mountPage()

    expect(pane(w, 'theme').exists()).toBe(false)
    expect(contentMocks.getHomeLayout).not.toHaveBeenCalled()
  })

  it('?tab=theme 直接落在风格模板，且按内嵌模式渲染', async () => {
    const { w } = await mountPage('?tab=theme')

    expect(pane(w, 'theme').exists()).toBe(true)
    expect(pane(w, 'theme').attributes('data-active')).toBe('true')
    expect(w.findComponent(MpTheme).props('embedded')).toBe(true)
    expect(contentMocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(mpMocks.getMpEntry).not.toHaveBeenCalled()
  })

  it('风格页与样式页各自拉一次；切回已渲染的页签不重复请求', async () => {
    const { w } = await mountPage()
    await pickTab(w, 'theme')
    await pickTab(w, 'appearance')
    await pickTab(w, 'theme')

    expect(contentMocks.getHomeLayout).toHaveBeenCalledTimes(2)
  })

  // 「重新加载 / 保存」按钮上移到父组件「小程序设置」的页签切换栏，三个页签共用同一组：
  // 切到哪个页就操作哪个内嵌子组件的 load/save，所以每个页签右侧都该出现这组按钮
  // （风格页的保存文案为「保存风格」，其余为「保存」），不再只有风格页才有。
  it('每个页签的切换栏右侧都出现「保存 / 重新加载」，风格页的保存叫「保存风格」', async () => {
    const { w } = await mountPage('?tab=entry')
    let actions = w.find('.tab-actions')
    expect(actions.exists()).toBe(true)
    expect(actions.text()).toContain('保存')
    expect(actions.text()).toContain('重新加载')

    await pickTab(w, 'appearance')
    actions = w.find('.tab-actions')
    expect(actions.exists()).toBe(true)
    expect(actions.text()).toContain('保存')
    expect(actions.text()).toContain('重新加载')

    await pickTab(w, 'theme')
    actions = w.find('.tab-actions')
    expect(actions.exists()).toBe(true)
    expect(actions.text()).toContain('保存风格')
    expect(actions.text()).toContain('重新加载')
  })

  it('点切换栏上的「重新加载」会重新拉取模板清单（风格页，重试预览图）', async () => {
    const { w } = await mountPage('?tab=theme')
    const before = contentMocks.getHomeLayout.mock.calls.length
    const reload = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('重新加载'))
    expect(reload).toBeDefined()
    await reload!.trigger('click')
    await flush()
    expect(contentMocks.getHomeLayout.mock.calls.length).toBeGreaterThan(before)
  })

  it('点切换栏上的「保存风格」只提交两轴 styleId/brandColorId（风格页）', async () => {
    const { w } = await mountPage('?tab=theme')
    const save = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('保存风格'))
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flush()
    expect(contentMocks.saveHomeLayout).toHaveBeenCalledWith({
      styleId: 'default', brandColorId: null,
    })
  })

  it('点切换栏上的「重新加载」会重新拉取入口配置（访客先看到谁页）', async () => {
    const { w } = await mountPage('?tab=entry')
    const before = mpMocks.getMpEntry.mock.calls.length
    const reload = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('重新加载'))
    expect(reload).toBeDefined()
    await reload!.trigger('click')
    await flush()
    expect(mpMocks.getMpEntry.mock.calls.length).toBeGreaterThan(before)
  })

  it('点切换栏上的「保存」只提交入口配置（访客先看到谁页）', async () => {
    const { w } = await mountPage('?tab=entry')
    const save = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('保存') && !b.text().includes('保存风格'))
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flush()
    expect(mpMocks.saveMpEntry).toHaveBeenCalledWith({ mode: 'wall', staffId: 0 })
  })

  it('点切换栏上的「重新加载」会重新拉取首页样式（名片页面样式页）', async () => {
    const { w } = await mountPage('?tab=appearance')
    const before = contentMocks.getHomeLayout.mock.calls.length
    const reload = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('重新加载'))
    expect(reload).toBeDefined()
    await reload!.trigger('click')
    await flush()
    expect(contentMocks.getHomeLayout.mock.calls.length).toBeGreaterThan(before)
  })

  it('点切换栏上的「保存」提交完整首页样式（名片页面样式页）', async () => {
    const { w } = await mountPage('?tab=appearance')
    const save = w
      .find('.tab-actions')
      .findAll('.el-button')
      .find((b: any) => b.text().includes('保存') && !b.text().includes('保存风格'))
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flush()
    expect(contentMocks.saveHomeLayout).toHaveBeenCalledWith({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'date',
      serviceTimeMode: 'date',
    })
  })

  // 源码级守门：防止「切换栏不再固定顶部」或「按钮又退回卡片里」这类只改样式/位置的回归。
  it('标签页切换栏在滚动时固定顶部，且两个按钮与页签在同一行（自有 sticky 栏）', async () => {
    const src = readFileSync(resolve(process.cwd(), 'src/views/content/MpSettings.vue'), 'utf-8')
    expect(src).toMatch(/position:\s*sticky/)
    expect(src).toMatch(/top:\s*-16px/)
    expect(src).toContain('.tabs-bar')
    expect(src).toContain('class="tab-actions"')
    expect(src).toContain('hidden-header')
    // 吸顶要贴到顶栏底边：.mp-settings 出血抵消 .admin-main 的 16px padding，
    // 且顶部不给内边距，否则吸顶栏仍比顶栏低一截、上方漏卡片
    expect(src).toContain('margin: -16px')
    expect(src).toContain('padding: 0 16px 16px')
    // 页签文字上下留白对称：.tabs-bar 用 stretch 让页签撑满栏高，
    // .tab 上下各 12px padding，使文字上沿与栏顶、下沿（含蓝下划线）与栏底等距，
    // 不再只在下方堆 12px 而上方紧贴
    expect(src).toMatch(/align-items:\s*stretch/)
    expect(src).toMatch(/\.tab\s*\{[^}]*padding:\s*12px 0/)
    // 保存/重新加载按钮对所有页签共用、始终显示：.tab-actions 不再用
    // v-if="tab === 'theme'" 限定只在风格页出现（否则另两页的按钮会退回卡片里）
    expect(src).toContain('class="tab-actions"')
    expect(src).not.toContain("v-if=\"tab === 'theme'\"")
    // 三个内嵌子组件都通过 ref 暴露给父组件，驱动切换栏右侧按钮
    expect(src).toContain('ref="entryRef"')
    expect(src).toContain('ref="appearanceRef"')
    expect(src).toContain('ref="themeRef"')
  })
})
