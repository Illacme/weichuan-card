import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import HomeLayout from './HomeLayout.vue'

// 首页布局页：板块顺序 + 背景图 + 遮罩浓度。
// 关键约束是「保存时提交的是完整顺序数组」——只提交 firstBlock 会让后端
// 无从拼出顺序；以及「清除背景必须连遮罩一起归零」，否则留下一个
// 对纯灰底生效的遮罩值，下次设背景时浓度莫名其妙。
const mocks = vi.hoisted(() => ({
  getHomeLayout: vi.fn(async (): Promise<any> => ({
    order: ['achievement', 'service'],
    bgImage: '',
    bgMask: 0,
  })),
  saveHomeLayout: vi.fn(async (_payload: any) => ({ code: 0 })),
}))

vi.mock('@/api/content', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {} }))

import { ElMessage } from 'element-plus'

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
// contentPosition 必须声明为 prop，否则会作为多余 attr 落在 fragment 根节点上报 warn
const ElDivider = defineComponent({
  name: 'ElDivider',
  props: ['contentPosition'],
  template: `<div class="el-divider"><slot /></div>`,
})
const ElTag = defineComponent({ name: 'ElTag', template: `<span class="el-tag"><slot /></span>` })

// radio-group 通过 provide 把「选中」事件转给子项，复刻真实组件的 v-model 语义
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
// disabled 不是声明的 prop，会落到 $attrs 上——正是要断言的那个
const ElSlider = defineComponent({
  name: 'ElSlider',
  props: ['modelValue', 'min', 'max', 'step'],
  emits: ['update:modelValue'],
  template: `<input class="el-slider" type="range" :value="modelValue"
    @input="$emit('update:modelValue', Number($event.target.value))" />`,
  inheritAttrs: true,
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

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-divider': ElDivider,
      'el-tag': ElTag,
      'el-radio-group': ElRadioGroup,
      'el-radio': ElRadio,
      'el-slider': ElSlider,
      UploadField,
      ImageViewer,
    },
    directives: { loading: {} },
  },
}

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage(layout?: any) {
  if (layout) mocks.getHomeLayout.mockResolvedValueOnce(layout)
  const w = mount(HomeLayout, mountOpts)
  await flush()
  return w
}

function orderTags(w: any): string[] {
  return w.findAll('.el-tag').map((t: any) => t.text())
}

function saveButton(w: any) {
  const btn = w.findAll('.el-button').find((b: any) => b.text() === '保存')
  if (!btn) throw new Error('找不到保存按钮')
  return btn
}

// 按表单项 label 定位某一组时间档位控件：页面里有两组（案例 / 服务），
// 直接 findAll('.el-radio') 会把两组合成一个列表，做不到「只改一组」的断言。
function timeItem(w: any, label: string) {
  const item = w.findAll('.el-form-item').find((i: any) => i.find('label').text() === label)
  if (!item) throw new Error('找不到表单项：' + label)
  return item
}

async function pickTimeMode(w: any, label: string, optionText: string) {
  const radio = timeItem(w, label)
    .findAll('.el-radio')
    .find((r: any) => r.text() === optionText)
  if (!radio) throw new Error(`「${label}」里找不到选项：${optionText}`)
  await radio.trigger('click')
  await flush()
}

describe('content/HomeLayout', () => {
  beforeEach(() => {
    mocks.getHomeLayout.mockClear()
    mocks.saveHomeLayout.mockClear()
    ;(ElMessage.success as any).mockClear()
  })

  it('挂载即拉取布局并按顺序渲染标签', async () => {
    const w = await mountPage()

    expect(mocks.getHomeLayout).toHaveBeenCalledTimes(1)
    expect(orderTags(w)).toEqual(['经典业绩', '业务服务'])
  })

  // order[0] 决定谁是「在前」的板块
  it('后端 order 以 service 开头时，「当前顺序」与选中项同步', async () => {
    const w = await mountPage({ order: ['service', 'achievement'], bgImage: '', bgMask: 0 })

    expect(orderTags(w)).toEqual(['业务服务', '经典业绩'])
  })

  it('切换板块顺序：当前顺序实时反过来', async () => {
    const w = await mountPage()
    const radios = w.findAll('.el-radio')

    await radios[1].trigger('click') // 「服务在前，业绩在后」
    await flush()

    expect(orderTags(w)).toEqual(['业务服务', '经典业绩'])
  })

  it('保存提交完整顺序数组（只交 firstBlock 后端拼不出顺序）', async () => {
    const w = await mountPage()
    await w.findAll('.el-radio')[1].trigger('click')
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      order: ['service', 'achievement'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'date',
      serviceTimeMode: 'date',
    })
  })

  // 没有背景图时遮罩没有意义：滑块要禁用，否则用户拖了半天看不到任何变化
  it('没有背景图：滑块禁用，且不显示预览与「清除背景」', async () => {
    const w = await mountPage()

    expect(w.find('.el-slider').attributes('disabled')).toBeDefined()
    expect(w.find('.bg-preview').exists()).toBe(false)
    expect(w.findAll('.el-button').some((b: any) => b.text() === '清除背景')).toBe(false)
  })

  it('有背景图：滑块可用、显示预览与「清除背景」', async () => {
    const w = await mountPage({ order: ['achievement', 'service'], bgImage: '/uploads/bg.jpg', bgMask: 40 })

    expect(w.find('.el-slider').attributes('disabled')).toBeUndefined()
    expect(w.find('.bg-preview').exists()).toBe(true)
    expect(w.find('.mask-val').text()).toBe('40%')
  })

  it('选中的背景图随保存提交，遮罩浓度一起带上', async () => {
    const w = await mountPage()

    await w.find('.upload-field').trigger('click')
    await flush()
    await w.find('.el-slider').setValue(60)
    await flush()
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      order: ['achievement', 'service'],
      bgImage: '/uploads/bg.jpg',
      bgMask: 60,
      projectTimeMode: 'date',
      serviceTimeMode: 'date',
    })
  })

  // 只清图不清遮罩，会留下一个「对灰底生效」的浓度，下次设图时莫名其妙
  it('清除背景：图与遮罩一起归零', async () => {
    const w = await mountPage({ order: ['achievement', 'service'], bgImage: '/uploads/bg.jpg', bgMask: 70 })

    const clear = w.findAll('.el-button').find((b: any) => b.text() === '清除背景')!
    await clear.trigger('click')
    await flush()

    expect(w.find('.bg-preview').exists()).toBe(false)
    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ bgImage: '', bgMask: 0 })
    )
  })

  it('点击效果预览打开原图查看器，关闭后移除', async () => {
    const w = await mountPage({ order: ['achievement', 'service'], bgImage: '/uploads/bg.jpg', bgMask: 10 })

    await w.find('.bg-preview').trigger('click')
    await flush()
    expect(w.find('.image-viewer').exists()).toBe(true)

    await w.find('.image-viewer').trigger('click')
    await flush()
    expect(w.find('.image-viewer').exists()).toBe(false)
  })

  it('保存成功提示', async () => {
    const w = await mountPage()

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.success).toHaveBeenCalledWith('已保存小程序展示设置')
  })

  it('保存失败：不提示成功，按钮可从 loading 复位', async () => {
    mocks.saveHomeLayout.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(saveButton(w).exists()).toBe(true)
  })

  // 接口失败时保留默认布局，页面仍可操作（不能白屏）
  it('拉取失败：保留默认顺序并继续可用', async () => {
    mocks.getHomeLayout.mockRejectedValueOnce(new Error('403'))
    const w = await mountPage()

    expect(orderTags(w)).toEqual(['经典业绩', '业务服务'])
    expect(saveButton(w).exists()).toBe(true)
  })

  // ---- 详情页时间显示：案例与服务分开控制 ----
  // 两列独立是这次需求的核心：改案例那一栏不能顺手把服务也改掉。
  it('拉取到的两个档位分别回填到各自的控件，保存时原样提交', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'hidden',
      serviceTimeMode: 'minute',
    })

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ projectTimeMode: 'hidden', serviceTimeMode: 'minute' })
    )
  })

  it('只切换项目案例档位：业务服务那一栏保持原值', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'date',
      serviceTimeMode: 'minute',
    })

    await pickTimeMode(w, '项目案例详情', '不显示')
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ projectTimeMode: 'hidden', serviceTimeMode: 'minute' })
    )
  })

  it('只切换业务服务档位：项目案例那一栏保持原值', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'hidden',
      serviceTimeMode: 'date',
    })

    await pickTimeMode(w, '业务服务详情', '显示到分')
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ projectTimeMode: 'hidden', serviceTimeMode: 'minute' })
    )
  })

  // 客户不该靠猜 hidden/date/minute 的含义，控件下方直接给出效果文案
  it('档位下方给出「小程序效果」预览，选不显示时不带「更新于」', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'],
      bgImage: '',
      bgMask: 0,
      projectTimeMode: 'hidden',
      serviceTimeMode: 'date',
    })

    expect(timeItem(w, '项目案例详情').text()).toContain('小程序效果：不显示时间')
    expect(timeItem(w, '业务服务详情').text()).toMatch(/小程序效果：更新于 \d{4}-\d{2}-\d{2}/)
  })
})
