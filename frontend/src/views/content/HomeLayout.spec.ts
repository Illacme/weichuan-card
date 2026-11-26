import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import HomeLayout from './HomeLayout.vue'

// 模块顺序是产品约定（首页背景 → 板块顺序 → 详情页时间显示）。
// 模板重排属于视觉要求，Vitest 挂载后看不出「谁在前」，只能读源码守门。
const PAGE_SRC = resolve(dirname(fileURLToPath(import.meta.url)), './HomeLayout.vue')

// 首页布局页：板块顺序 + 背景图 + 遮罩浓度。
// 关键约束是「保存时提交的是完整顺序数组」——只提交 firstBlock 会让后端
// 无从拼出顺序；以及「清除背景必须连遮罩一起归零」，否则留下一个
// 对纯灰底生效的遮罩值，下次设背景时浓度莫名其妙。
// 顶部主色浓度已移到「风格模板」页签（与品牌配色同页，调色即看即时预览），
// 本页不读写、不提交该字段；后端 PUT 是部分更新，保存本页不会冲掉它。
const mocks = vi.hoisted(() => ({
  getHomeLayout: vi.fn(async (): Promise<any> => ({
    order: ['achievement', 'service'],
    bgImage: '',
    bgMask: 0,
    categoryLimit: 20,
    featuredCount: 3,
    featuredEffect: 'static',
    featuredRatio: 'none',
    companyNameScale: 'large',
    introFontScale: 'large',
    styleId: 'default',
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

// radio-group 通过 provide 把「选中」事件转给子项，复刻真实组件的 v-model 语义。
// data-selected 暴露当前值：没有它就只能靠「点一下再存」间接验证回显，
// 而回显写错（比如把档位读成别的字段）恰好会被这种间接断言放过。
const ElRadioGroup = defineComponent({
  name: 'ElRadioGroup',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup(_props, { slots, emit }) {
    provide('radioGroup', { select: (v: string) => emit('update:modelValue', v) })
    return () =>
      h(
        'div',
        { class: 'el-radio-group', 'data-selected': String(_props.modelValue ?? '') },
        slots.default ? slots.default() : []
      )
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
// 两个数量输入是 el-input-number：测试里用原生 number input 复刻 v-model，
// 否则未注册的自定义元素不发射 update:modelValue，回显与保存都测不到。
const ElInputNumber = defineComponent({
  name: 'ElInputNumber',
  props: ['modelValue', 'min', 'max', 'step'],
  emits: ['update:modelValue'],
  template: `<input class="el-input-number" type="number" :min="min" :max="max" :value="modelValue"
    @input="$emit('update:modelValue', Number($event.target.value))" />`,
  inheritAttrs: true,
})
// el-switch（0050 两个名称开关）：同样必须复刻 v-model，否则未注册的自定义元素
// 不发射 update:modelValue——开关点了没反应、保存永远是 false，测试还会「绿」。
const ElSwitch = defineComponent({
  name: 'ElSwitch',
  props: ['modelValue', 'activeText', 'inactiveText'],
  emits: ['update:modelValue'],
  template: `<button class="el-switch" :data-on="String(!!modelValue)"
    @click="$emit('update:modelValue', !modelValue)">{{ modelValue ? activeText : inactiveText }}</button>`,
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
      'el-input-number': ElInputNumber,
      'el-switch': ElSwitch,
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
function formItemByLabel(w: any, label: string) {
  const item = w.findAll('.el-form-item').find((i: any) => i.find('label').text() === label)
  if (!item) throw new Error('找不到表单项：' + label)
  return item
}

async function pickTimeMode(w: any, label: string, optionText: string) {
  const radio = formItemByLabel(w, label)
    .findAll('.el-radio')
    .find((r: any) => r.text() === optionText)
  if (!radio) throw new Error(`「${label}」里找不到选项：${optionText}`)
  await radio.trigger('click')
  await flush()
}

// 通用版：按表单项 label + 选项文字点选。LOGO 的两组档位文案是「小 / 靠左」这类
// 短词，与时间档位共用一套定位逻辑，但错一个字就会静默点空，所以找不到直接抛。
async function pickRadio(w: any, label: string, optionText: string) {
  const radio = formItemByLabel(w, label)
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

  // 只挪表单项、把 divider 留在原地，会出现「标题说首页背景，底下是板块顺序」的错位，
  // 所以直接钉死六个模块标题在源码里的相对位置。首页文字展示（公司名/简介档位）
  // 必须夹在「首页背景」与「板块顺序」中间——这是产品约定的插入位置；
  // 名片 LOGO 紧随首页文字展示（两者都是「名片卡长什么样」），
  // 业绩与案例展示紧随板块顺序，且在详情页时间显示之前。
  it('配置模块顺序：首页背景 → 首页文字展示 → 名片 LOGO → 板块顺序 → 业绩与案例展示 → 详情页时间显示', () => {
    const src = readFileSync(PAGE_SRC, 'utf-8')
    const anchors = [
      '首页背景',
      '首页文字展示',
      '名片 LOGO',
      '板块顺序',
      '业绩与案例展示',
      '详情页时间显示',
    ].map((name) => `<el-divider content-position="left">${name}</el-divider>`)
    const idx = anchors.map((a) => {
      const i = src.indexOf(a)
      // 锚点不存在时 indexOf 返回 -1，后面的 > 比较会拿 -1 当「最靠前」，
      // 于是「删掉一个分组」照样全绿——必须显式断言存在。
      expect(i, `锚点 ${a} 不在模板里，顺序断言会空转`).toBeGreaterThan(-1)
      return i
    })
    expect(idx).toEqual([...idx].sort((a, b) => a - b))
  })

  // 顶部说明漏写分区时，运营只看第一眼会以为这个页签只有背景和顺序——
  // 后来加的「业绩与案例展示」「封面图名称」正是这样被漏掉的（2026-11-18 盘点）。
  it('顶部说明覆盖所有分区，不只列首页背景与板块顺序', async () => {
    const w = await mountPage()
    const desc = w.find('p.desc').text()
    for (const kw of ['首页背景', '公司名称与简介', 'LOGO 的大小与位置', '先后顺序', '业绩分类', '精选案例', '封面图名称', '更新于']) {
      expect(desc, `顶部说明漏了「${kw}」`).toContain(kw)
    }
  })

  // ---- 名片 LOGO 尺寸 / 位置（0053）----
  //
  // 客户的原话是「正方形图看起来偏左，要能调大小和位置」。这里守三件事：
  // ① 历史行没有这两列时回显不能落空（否则 radio 一个都不选中，看起来像配置丢了）；
  // ② 切档后保存提交的是新值（不是永远缺省）；
  // ③ 预览随档位联动——预览不动的控件等于让客户盲调。
  it('历史行没有这两列（0053 之前的存量）：回显成 中 / 居中', async () => {
    // 默认 mock 刻意不带 logoSize / logoAlign，模拟旧后端 / 旧行
    const w = await mountPage()

    expect(formItemByLabel(w, 'LOGO 大小').find('.el-radio-group').attributes('data-selected')).toBe(
      'medium'
    )
    expect(formItemByLabel(w, 'LOGO 位置').find('.el-radio-group').attributes('data-selected')).toBe(
      'center'
    )
  })

  it('后端下发 大 / 靠右：控件回显，预览同步换成大区块并贴右', async () => {
    const w = await mountPage({ order: [], logoSize: 'large', logoAlign: 'right' })

    expect(formItemByLabel(w, 'LOGO 大小').find('.el-radio-group').attributes('data-selected')).toBe(
      'large'
    )
    expect(formItemByLabel(w, 'LOGO 位置').find('.el-radio-group').attributes('data-selected')).toBe(
      'right'
    )
    const slot = w.find('.lp-slot')
    expect(slot.classes()).toContain('lp-slot-large')
    expect(slot.classes()).toContain('lp-align-right')
    expect(w.find('.lp-img').classes()).toContain('lp-img-large')
  })

  it('把 LOGO 调成 小 / 靠左 后保存：提交新档位，预览跟着变', async () => {
    const w = await mountPage()

    await pickRadio(w, 'LOGO 大小', '小')
    await pickRadio(w, 'LOGO 位置', '靠左')

    const slot = w.find('.lp-slot')
    expect(slot.classes()).toContain('lp-slot-small')
    expect(slot.classes()).toContain('lp-align-left')

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ logoSize: 'small', logoAlign: 'left' })
    )
  })

  // ---- 预览必须按「当前风格」摆姿态（否则预览会骗人）----
  //
  // LOGO 区块在卡片里的落点由风格决定：标准/浅墨贴右边缘、暖色换行到姓名下方居中、
  // 青色因头像在右而落在中部。只画一种姿态时，暖色 / 青色的客户就是对着一个真机上
  // 不存在的落点调档位——调得越认真偏得越多。
  const LAYOUT_FULL = {
    order: ['achievement', 'service'],
    bgImage: '',
    bgMask: 0,
    categoryLimit: 20,
    featuredCount: 3,
    featuredEffect: 'static',
    featuredRatio: 'none',
    companyNameScale: 'large',
    introFontScale: 'large',
    logoSize: 'medium',
    logoAlign: 'center',
  }

  it('风格是暖色：预览换成上下堆叠（LOGO 在姓名下方），并标出当前风格名', async () => {
    const w = await mountPage({ ...LAYOUT_FULL, styleId: 'warm' })

    expect(w.find('.lp-card').classes()).toContain('lp-layout-stack')
    expect(w.text()).toContain('当前风格')
    expect(w.text()).toContain('暖色')
  })

  it('风格是青色：预览用左右翻转（头像在右，LOGO 落在头像左侧）', async () => {
    const w = await mountPage({ ...LAYOUT_FULL, styleId: 'teal' })

    expect(w.find('.lp-card').classes()).toContain('lp-layout-reverse')
    expect(w.text()).toContain('青色')
  })

  it('标准 / 浅墨：预览是常规横排（LOGO 贴卡片右边缘）', async () => {
    for (const styleId of ['default', 'ink']) {
      const w = await mountPage({ ...LAYOUT_FULL, styleId })
      expect(w.find('.lp-card').classes(), `${styleId} 应该是横排姿态`).toContain('lp-layout-row')
    }
  })

  it('refreshStyle 只重读风格：客户在本页未保存的档位不会被冲掉', async () => {
    const w = await mountPage({ ...LAYOUT_FULL, styleId: 'default' })
    // 先把本页改到「小 / 靠左」，但刻意不保存
    await pickRadio(w, 'LOGO 大小', '小')
    await pickRadio(w, 'LOGO 位置', '靠左')

    mocks.getHomeLayout.mockResolvedValueOnce({ ...LAYOUT_FULL, styleId: 'warm' })
    await (w.vm as any).refreshStyle()
    await flush()

    expect(w.find('.lp-card').classes()).toContain('lp-layout-stack')
    expect(formItemByLabel(w, 'LOGO 大小').find('.el-radio-group').attributes('data-selected')).toBe(
      'small'
    )
    expect(w.find('.lp-slot').classes()).toContain('lp-align-left')
  })

  // order[0] 决定谁是「在前」的板块
  it('后端 order 以 service 开头时，「当前顺序」与选中项同步', async () => {
    const w = await mountPage({ order: ['service', 'achievement'], bgImage: '', bgMask: 0 })

    expect(orderTags(w)).toEqual(['业务服务', '经典业绩'])
  })

  it('切换板块顺序：当前顺序实时反过来', async () => {
    const w = await mountPage()
    // 页面里有多组 radio（文字档位 / 板块顺序 / 时间档位），按 label 限定到「谁在前」，
    // 不能用 findAll('.el-radio')[1]——新增控件会让下标漂移。
    const radios = formItemByLabel(w, '谁在前').findAll('.el-radio')

    await radios[1].trigger('click') // 「服务在前，业绩在后」
    await flush()

    expect(orderTags(w)).toEqual(['业务服务', '经典业绩'])
  })

  it('保存提交完整顺序数组（只交 firstBlock 后端拼不出顺序）', async () => {
    const w = await mountPage()
    await formItemByLabel(w, '谁在前').findAll('.el-radio')[1].trigger('click')
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveHomeLayout).toHaveBeenCalledWith({
      order: ['service', 'achievement'],
      bgImage: '',
      bgMask: 0,
      categoryLimit: 20,
      featuredCount: 3,
      featuredEffect: 'static',
      featuredRatio: 'none',
      showCategoryName: false,
      showFeaturedName: false,
      showServiceName: false,
      projectTimeMode: 'date',
      serviceTimeMode: 'date',
      companyNameScale: 'large',
      introFontScale: 'large',
      logoSize: 'medium',
      logoAlign: 'center',
    })
  })

  // 0049 三项展示配置：0 是合法值（隐藏分类 / 隐藏精选），回显不能用 || 兜底；改动要随同一份 payload 提交。
  it('业绩分类数量 / 精选案例数量 / 循环效果：回显后端值，修改后随保存提交', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'], bgImage: '', bgMask: 0,
      categoryLimit: 0, featuredCount: 0, featuredEffect: 'scroll',
    })

    expect(formItemByLabel(w, '业绩分类数量').find('.el-input-number').element.value).toBe('0')
    expect(formItemByLabel(w, '精选案例数量').find('.el-input-number').element.value).toBe('0')

    await formItemByLabel(w, '业绩分类数量').find('.el-input-number').setValue(6)
    await formItemByLabel(w, '精选案例数量').find('.el-input-number').setValue(3)
    const flip = formItemByLabel(w, '循环呈现效果')
      .findAll('.el-radio')
      .find((b: any) => b.text() === '立体翻转')
    expect(flip).toBeDefined()
    await flip.trigger('click')
    await flush()

    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ categoryLimit: 6, featuredCount: 3, featuredEffect: 'flip' })
    )
  })

  // 0066 裁切比例：单列枚举天然互斥，缺省 none 保持原图（升级零视觉突变）。
  it('精选案例裁切比例：缺省原图不裁切，四档可切且随保存提交', async () => {
    const w = await mountPage()
    const group = () => formItemByLabel(w, '精选案例裁切比例').find('.el-radio-group')
    expect(group().attributes('data-selected'), '缺省必须 none，否则存量租户升级当天封面就被裁').toBe('none')

    for (const [label, value] of [
      ['16:9', '16:9'], ['4:3', '4:3'], ['2:1', '2:1'], ['1:1', '1:1'], ['原图比例', 'none'],
    ]) {
      const radio = group().findAll('.el-radio').find((b: any) => b.text() === label)
      expect(radio, `缺少 ${label} 档位`).toBeDefined()
      await radio.trigger('click')
      await flush()
      expect(group().attributes('data-selected')).toBe(value)
    }

    await group().findAll('.el-radio').find((b: any) => b.text() === '2:1')!.trigger('click')
    await flush()
    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ featuredRatio: '2:1' })
    )
  })

  it('精选案例裁切比例：回显后端值（刷新后仍保持已选比例）', async () => {
    const w = await mountPage({ order: ['achievement', 'service'], featuredRatio: '4:3' })
    expect(formItemByLabel(w, '精选案例裁切比例').find('.el-radio-group').attributes('data-selected')).toBe('4:3')
  })

  // 0050：封面图里已经画了名称（业绩是带字方图、服务是带字竖版卡），
  // 所以默认关闭；客户换成素图后打开开关，端上才在图下补一行。
  // 两个开关必须独立——分类图与服务卡常常不是同一批素材风格。
  it('封面图名称开关：缺省关闭，打开后随保存提交 true，且互不影响', async () => {
    const w = await mountPage()

    const catSwitch = () => formItemByLabel(w, '业绩分类名称').find('.el-switch')
    const featSwitch = () => formItemByLabel(w, '精选案例名称').find('.el-switch')
    const svcSwitch = () => formItemByLabel(w, '业务服务名称').find('.el-switch')

    // 缺省 false —— 若这里变成 true，所有存量租户的首页会立刻多出一行重复文字
    expect(catSwitch().attributes('data-on')).toBe('false')
    expect(featSwitch().attributes('data-on')).toBe('false')
    expect(svcSwitch().attributes('data-on')).toBe('false')

    // 只打开业绩分类：案例与服务必须保持关闭（三列独立，不是同一个总开关）
    await catSwitch().trigger('click')
    expect(catSwitch().attributes('data-on')).toBe('true')
    expect(featSwitch().attributes('data-on')).toBe('false')
    expect(svcSwitch().attributes('data-on')).toBe('false')

    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ showCategoryName: true, showFeaturedName: false, showServiceName: false })
    )
  })

  it('封面图名称开关：回显后端值（打开过的开关刷新后仍是打开的）', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'],
      showCategoryName: false,
      showFeaturedName: true,
      showServiceName: true,
    })

    expect(formItemByLabel(w, '业绩分类名称').find('.el-switch').attributes('data-on')).toBe('false')
    expect(formItemByLabel(w, '精选案例名称').find('.el-switch').attributes('data-on')).toBe('true')
    expect(formItemByLabel(w, '业务服务名称').find('.el-switch').attributes('data-on')).toBe('true')
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

  // 2026-11-25 用户反馈后台预览太简陋（一张图 + 一个白方块），看不出深浅是否合适。
  // 改为按真机首页版式复刻：透明顶栏、名片卡、视频卡、按 order 排的板块网格、底部操作条。
  it('效果预览按真机版式复刻，遮罩浓度即时反映在预览上', async () => {
    const w = await mountPage({
      order: ['service', 'achievement'], bgImage: '/uploads/bg.jpg', bgMask: 25,
    })
    const p = w.find('.bg-preview')
    expect(p.find('.bp-nav').exists(), '缺透明顶栏，背景图铺不满顶部').toBe(true)
    expect(p.find('.bp-person').exists(), '缺名片卡').toBe(true)
    expect(p.find('.bp-video').exists(), '缺视频卡').toBe(true)
    expect(p.find('.bp-actions').exists(), '缺底部操作条').toBe(true)
    expect(p.findAll('.bp-grid i').length, '板块网格应有 8 个占位块').toBe(8)
    expect(p.find('.bp-sec-title').text(), '板块顺序没跟着 order 走').toBe('业务服务')
    expect(p.find('.bg-preview-mask').attributes('style')).toContain('0.25')
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
      categoryLimit: 20,
      featuredCount: 3,
      featuredEffect: 'static',
      featuredRatio: 'none',
      showCategoryName: false,
      showFeaturedName: false,
      showServiceName: false,
      projectTimeMode: 'date',
      serviceTimeMode: 'date',
      companyNameScale: 'large',
      introFontScale: 'large',
      logoSize: 'medium',
      logoAlign: 'center',
    })
  })

  // 只清图不清遮罩，会留下一个「对灰底生效」的浓度，下次设图时莫名其妙。
  it('清除背景：图与遮罩一起归零', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'], bgImage: '/uploads/bg.jpg', bgMask: 70,
    })

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

    expect(formItemByLabel(w, '项目案例详情').text()).toContain('小程序效果：不显示时间')
    expect(formItemByLabel(w, '业务服务详情').text()).toMatch(/小程序效果：更新于 \d{4}-\d{2}-\d{2}/)
  })

  // ---- 首页视频下方文字：公司名称 / 简介文字各四档（大/中/小/关闭）----
  // 控件从公司信息 / 公司简介页搬来，钉四件事：缺省 large（存量零视觉突变）、
  // 后端值能回显、选中档随 saveHomeLayout 提交、两处互不串。
  async function pickScale(w: any, label: string, optionText: string) {
    const radio = formItemByLabel(w, label)
      .findAll('.el-radio')
      .find((r: any) => r.text() === optionText)
    if (!radio) throw new Error(`「${label}」里找不到选项：${optionText}`)
    await radio.trigger('click')
    await flush()
  }

  it('两处文字档位缺省「大」，四档都可选', async () => {
    const w = await mountPage()
    for (const label of ['公司名称', '简介文字']) {
      const texts = formItemByLabel(w, label).findAll('.el-radio').map((r: any) => r.text())
      expect(texts).toHaveLength(4)
      expect(texts[0]).toBe('大')
      expect(texts[1]).toBe('中')
      expect(texts[2]).toBe('小')
      expect(texts[3]).toContain('关闭')
      // 选项文案不能带「当前」之类动态语义：它曾硬编码在「大」上，实际选中「小」时
      // 会自相矛盾（2026-11-18 用户截图）。当前值由单选选中态表达，默认值在说明里写。
      expect(texts.join('')).not.toContain('当前')
    }
  })

  it('后端回显两处档位，保存时原样提交', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'], bgImage: '', bgMask: 0,
      companyNameScale: 'small', introFontScale: 'hidden',
    })
    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ companyNameScale: 'small', introFontScale: 'hidden' })
    )
  })

  it('只改公司名称档位，简介档位保持原值（两处互不串）', async () => {
    const w = await mountPage({
      order: ['achievement', 'service'], bgImage: '', bgMask: 0,
      companyNameScale: 'large', introFontScale: 'medium',
    })
    await pickScale(w, '公司名称', '关闭（不显示公司名）')
    await saveButton(w).trigger('click')
    await flush()
    expect(mocks.saveHomeLayout).toHaveBeenCalledWith(
      expect.objectContaining({ companyNameScale: 'hidden', introFontScale: 'medium' })
    )
  })

  it('字段缺失时归一到「大」（历史行 / 老后端）', async () => {
    const w = await mountPage({ order: ['achievement', 'service'], bgImage: '', bgMask: 0 })
    expect((w.vm as any).companyNameScale).toBe('large')
    expect((w.vm as any).introFontScale).toBe('large')
  })

  it('内嵌在「小程序设置」时，隐藏自身的「保存 / 重新加载」按钮（由父切换栏统一提供）', async () => {
    const w = mount(HomeLayout, { ...mountOpts, props: { embedded: true } })
    await flush()

    // 自带页签栏（含「重新加载」）整体不渲染
    expect(w.find('.page-bar').exists()).toBe(false)
    // 表单底部的「保存」也不渲染；两个按钮改由父组件切换栏提供，避免一套按钮出现两处
    const selfActions = w
      .findAll('.el-button')
      .filter((b: any) => b.text() === '保存' || b.text() === '重新加载')
    expect(selfActions.length).toBe(0)
  })
})
