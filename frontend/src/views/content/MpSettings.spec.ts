import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject, computed } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import MpSettings from './MpSettings.vue'
import MpEntry from './MpEntry.vue'
import HomeLayout from './HomeLayout.vue'
import { routes } from '@/router'

// 「小程序设置」= 一个菜单项 + 两个页签（访客先看到谁 / 名片首页样式）。
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
})
