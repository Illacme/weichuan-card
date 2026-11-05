import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import CompanyProfile from './CompanyProfile.vue'

// 公司简介页：视频来源五档 + 动态表单 + 存储能力探测。
// 这页的价值全在「按来源切换的字段」和「保存前的三道拦截」——
// 漏一条拦截就会存下一条永远播不出视频的配置，而界面上看不出任何异常。
const mocks = vi.hoisted(() => ({
  getCompanyProfile: vi.fn(async (): Promise<any> => ({
    id: 1,
    introText: '朗汇机电成立于 2003 年',
    videoSource: 'none',
    videoChannelId: '',
    videoFeedId: '',
    videoFeedToken: '',
    videoVid: '',
    videoUrl: '',
    videoFallbackUrl: false,
    cover: '/uploads/cover.png',
  })),
  saveCompanyProfile: vi.fn(async (_payload: any) => ({ code: 0 })),
  getUploadCapability: vi.fn(async (): Promise<any> => ({
    driver: 'oss',
    presign: true,
    videoUpload: true,
    videoMaxMb: 200,
    reason: '',
  })),
  // 腾讯视频档默认不可用（部署未开 MP_TENCENT_VIDEO）：默认就该是「选不了 + 有原因」
  getVideoCapability: vi.fn(async (): Promise<any> => ({
    tencentVideo: false,
    tencentReason: '腾讯视频档未启用：请在「系统设置 → 功能开关」开启「腾讯视频档」',
  })),
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
  props: ['type', 'loading', 'icon'],
  template: `<button class="el-button" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElForm = defineComponent({
  name: 'ElForm',
  setup(_p, { slots }) {
    return () => h('form', { class: 'el-form' }, slots.default ? slots.default() : [])
  },
})
const ElFormItem = defineComponent({
  name: 'ElFormItem',
  props: ['label'],
  template: `<div class="el-form-item"><label>{{ label }}</label><slot /></div>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'type', 'rows'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<select class="el-select" :value="modelValue"
    @change="$emit('update:modelValue', $event.target.value)"><slot /></select>`,
})
// disabled 不是声明的 prop，会落到 $attrs 上——正是「存储不支持时禁用上传档」的断言依据
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value" v-bind="$attrs">{{ label }}</option>`,
  inheritAttrs: true,
})
const ElCheckbox = defineComponent({
  name: 'ElCheckbox',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<label class="el-checkbox"><input type="checkbox" :checked="modelValue"
    @change="$emit('update:modelValue', $event.target.checked)" /><slot /></label>`,
})
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['type', 'closable', 'showIcon'],
  template: `<div class="el-alert"><slot name="title" /></div>`,
})
// el-switch：腾讯视频档的「自动播放」开关。
// 插件 autoplay **默认 true**，所以这里既要做「默认关」的断言，也要做「打开后真提交」的断言。
const ElSwitch = defineComponent({
  name: 'ElSwitch',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<div class="el-switch"><input type="checkbox" :checked="modelValue"
    @change="$emit('update:modelValue', $event.target.checked)" /></div>`,
})
// value / maxMB 都透到 DOM 上：本页最要命的两个 bug 都发生在「上传组件收到了什么」——
// ① 直链档的地址被当成「已上传的文件」回显出来；② 容量提示与真正的校验上限不同源。
// 只在页面上找得到组件是不够的，必须能读到它拿到的 props。
const UploadField = defineComponent({
  name: 'UploadField',
  props: ['modelValue', 'multiple', 'accept', 'listType', 'hideLibrary', 'maxMB', 'tip', 'libraryType'],
  emits: ['update:modelValue'],
  template: `<button class="upload-field" :data-tip="tip" :data-value="modelValue || ''"
    :data-max-mb="maxMB"
    @click="$emit('update:modelValue', '/uploads/new-cover.png')" />`,
})

// el-radio-group / el-radio：「自托管 MP4」档内部的「上传 / 直链」二选一。
// 子组件要能真的把选择写回父级，所以用 provide/inject 搭一条最小链路，
// 而不是只渲染两个不可点的 label（那样「选不了」的 bug 根本测不出来）。
const ElRadioGroup = defineComponent({
  name: 'ElRadioGroup',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup(props, { slots, emit }) {
    provide('radioGroup', {
      pick: (v: string) => emit('update:modelValue', v),
    })
    return () => h('div', { class: 'el-radio-group' }, slots.default ? slots.default() : [])
  },
})
const ElRadio = defineComponent({
  name: 'ElRadio',
  props: ['value'],
  inheritAttrs: false,
  setup(props, { slots, attrs }) {
    const g: any = inject('radioGroup')
    return () =>
      h(
        'label',
        {
          class: ['el-radio', attrs.disabled ? 'is-disabled' : ''],
          'data-value': props.value,
          'data-disabled': attrs.disabled ? '1' : '0',
          onClick: () => {
            if (!attrs.disabled && g) g.pick(props.value)
          },
        },
        slots.default ? slots.default() : []
      )
  },
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-input': ElInput,
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-checkbox': ElCheckbox,
      'el-alert': ElAlert,
      'el-switch': ElSwitch,
      'el-radio-group': ElRadioGroup,
      'el-radio': ElRadio,
      UploadField,
    },
    directives: { loading: {} },
  },
}

async function flush() {
  await nextTick()
  await nextTick()
  await nextTick()
}

async function mountPage(profile?: any, cap?: any, vcap?: any) {
  if (profile) mocks.getCompanyProfile.mockResolvedValueOnce(profile)
  if (cap) mocks.getUploadCapability.mockResolvedValueOnce(cap)
  if (vcap) mocks.getVideoCapability.mockResolvedValueOnce(vcap)
  const w = mount(CompanyProfile, mountOpts)
  await flush()
  return w
}

function byPlaceholder(w: any, placeholder: string) {
  const el = w.findAll('.el-input').find((i: any) => i.attributes('placeholder') === placeholder)
  if (!el) throw new Error(`找不到 placeholder=${placeholder} 的输入框`)
  return el
}

function saveButton(w: any) {
  const btn = w.findAll('.el-button').find((b: any) => b.text() === '保存')
  if (!btn) throw new Error('找不到保存按钮')
  return btn
}

async function pickSource(w: any, value: string) {
  await w.find('.el-select').setValue(value)
  await flush()
}

// 「自托管 MP4」档内部的二选一（upload / url）
async function pickMp4Mode(w: any, value: string) {
  await w.find(`.el-radio[data-value="${value}"]`).trigger('click')
  await flush()
}

// 本页有两个 UploadField（视频 / 封面），用提示文案区分：视频那个带 mp4
function videoUpload(w: any) {
  const c = w
    .findAllComponents(UploadField)
    .find((x: any) => String(x.props('tip') || '').includes('mp4'))
  if (!c) throw new Error('找不到视频上传组件')
  return c
}

// 造一份「公司简介」接口返回：只关心视频这几个字段，其余给能过校验的最小值
function profile(videoSource: string, videoUrl: string) {
  return {
    id: 1,
    introText: '简介',
    videoSource,
    videoChannelId: '',
    videoFeedId: '',
    videoFeedToken: '',
    videoVid: '',
    videoUrl,
    videoFallbackUrl: false,
    videoAutoplay: false,
    cover: '',
  }
}

describe('content/CompanyProfile', () => {
  beforeEach(() => {
    mocks.getCompanyProfile.mockClear()
    mocks.saveCompanyProfile.mockClear()
    mocks.getUploadCapability.mockClear()
    ;(ElMessage.success as any).mockClear()
    ;(ElMessage.warning as any).mockClear()
  })

  const VCAP_ON = { tencentVideo: true, tencentReason: '' }
  const VID_PLACEHOLDER = '如 n0041aa087e（腾讯视频播放页地址末段）'

  it('挂载即拉取简介与存储能力', async () => {
    const w = await mountPage()

    expect(mocks.getCompanyProfile).toHaveBeenCalledTimes(1)
    expect(mocks.getUploadCapability).toHaveBeenCalledTimes(1)
    expect(mocks.getVideoCapability).toHaveBeenCalledTimes(1)
    expect(byPlaceholder(w, '首页简介区文字').element.value).toBe('朗汇机电成立于 2003 年')
  })

  // 该列是后加的，历史行可能是空串：空串必须归一到 none，否则下拉显示空白
  it('videoSource 为空串时归一到「不展示视频」', async () => {
    const w = await mountPage({ ...(await mocks.getCompanyProfile()), videoSource: '' })

    expect((w.find('.el-select').element as HTMLSelectElement).value).toBe('none')
  })

  it('视频号档：展开 channelId / feedId / feed-token 三项', async () => {
    const w = await mountPage()
    await pickSource(w, 'channels')

    expect(byPlaceholder(w, 'sph 开头，视频号助手首页可查看').exists()).toBe(true)
    expect(byPlaceholder(w, '视频号助手 → 动态管理 → 复制 feedId').exists()).toBe(true)
    expect(byPlaceholder(w, '跨主体才填：MP 平台开权限后移动端复制').exists()).toBe(true)
  })

  // upload 与 url 在小程序端是**同一个分支**（都渲染原生 <video src>），
  // 所以界面上合成一档「自托管 MP4」，内部再选「上传 / 直链」。
  // 这条钉的是默认落点：存储能力可用时默认走上传（客户最省事），
  // 并且把容量上限摆在明面上。
  it('自托管 MP4 档：默认落到上传，显示上传组件与容量提示', async () => {
    const w = await mountPage()
    await pickSource(w, 'mp4')

    const upload = w.findAll('.upload-field').find((u: any) => u.attributes('data-tip')?.includes('mp4'))
    expect(upload).toBeTruthy()
    expect(upload!.attributes('data-tip')).toContain('200MB')
  })

  // 存储不支持视频时「上传」必须点不动并给出原因，而不是让用户选完才发现传不了。
  // 同时**默认落到直链**：不能上传还默认选上传，等于一进页面就摆一个灰掉的选项。
  it('存储不支持视频：上传项禁用、给出原因，并默认落到直链', async () => {
    const w = await mountPage(undefined, {
      driver: 'local',
      presign: false,
      videoUpload: false,
      videoMaxMb: 0,
      reason: '本地存储不支持视频直传，请改用对象存储',
    })

    await pickSource(w, 'mp4')

    expect(w.find('.el-radio[data-value="upload"]').attributes('data-disabled')).toBe('1')
    expect(w.text()).toContain('本地存储不支持视频直传，请改用对象存储')
    expect(byPlaceholder(w, 'https:// 开头的 mp4 地址').exists()).toBe(true)
  })

  it('自托管 MP4 档 · 直链：显示 MP4 地址输入框', async () => {
    const w = await mountPage()
    await pickSource(w, 'mp4')
    await pickMp4Mode(w, 'url')

    expect(byPlaceholder(w, 'https:// 开头的 mp4 地址').exists()).toBe(true)
  })

  // 兜底只对「主源可能播不出来」的档位有意义：视频号（组件可能报错）与腾讯视频
  // （插件可能未授权 / vid 未过审）；MP4 两档本身就是 MP4，不需要兜底
  it('兜底开关只在视频号 / 腾讯视频档出现', async () => {
    const w = await mountPage()
    expect(w.find('.el-checkbox').exists()).toBe(false)

    await pickSource(w, 'channels')
    expect(w.find('.el-checkbox').exists()).toBe(true)

    await pickSource(w, 'tencent')
    expect(w.find('.el-checkbox').exists()).toBe(true)

    await pickSource(w, 'mp4')
    expect(w.find('.el-checkbox').exists()).toBe(false)

    await pickMp4Mode(w, 'url')
    expect(w.find('.el-checkbox').exists()).toBe(false)
  })

  // ---- 保存前的三道拦截 ----

  it('视频号档两个标识都没填：拦下且不提交', async () => {
    const w = await mountPage()
    await pickSource(w, 'channels')

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.warning).toHaveBeenCalledWith(
      '视频号档需要填写 feedId（同主体）或 feed-token（跨主体）'
    )
    expect(mocks.saveCompanyProfile).not.toHaveBeenCalled()
  })

  it('填了 feedId 即可通过校验', async () => {
    const w = await mountPage()
    await pickSource(w, 'channels')
    await byPlaceholder(w, '视频号助手 → 动态管理 → 复制 feedId').setValue('feed-123')
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveCompanyProfile).toHaveBeenCalledTimes(1)
    expect(mocks.saveCompanyProfile.mock.calls[0][0]).toMatchObject({
      videoSource: 'channels',
      videoFeedId: 'feed-123',
    })
  })

  it('外部直链档没填地址：拦下且不提交', async () => {
    const w = await mountPage()
    await pickSource(w, 'mp4')
    await pickMp4Mode(w, 'url')

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.warning).toHaveBeenCalledWith('请填写 MP4 地址')
    expect(mocks.saveCompanyProfile).not.toHaveBeenCalled()
  })

  // 开了兜底却不填地址 = 存了一个永远不生效的开关，必须在保存时拦住
  it('开了兜底却没填地址：拦下且不提交', async () => {
    const w = await mountPage()
    await pickSource(w, 'channels')
    await byPlaceholder(w, '视频号助手 → 动态管理 → 复制 feedId').setValue('feed-1')
    await w.find('.el-checkbox input').setValue(true)
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.warning).toHaveBeenCalledWith('已开启兜底，请填写兜底 MP4 地址')
    expect(mocks.saveCompanyProfile).not.toHaveBeenCalled()
  })

  it('兜底开启并填了地址：正常提交', async () => {
    const w = await mountPage()
    await pickSource(w, 'channels')
    await byPlaceholder(w, '视频号助手 → 动态管理 → 复制 feedId').setValue('feed-1')
    await w.find('.el-checkbox input').setValue(true)
    await flush()
    await byPlaceholder(w, '兜底用的 mp4 地址（云存储或任意 CDN 均可）').setValue('https://x/a.mp4')
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
      expect.objectContaining({ videoFallbackUrl: true, videoUrl: 'https://x/a.mp4' })
    )
  })

  // ---- 保存 payload ----

  it('保存提交十个字段，不带 id', async () => {
    const w = await mountPage()

    await saveButton(w).trigger('click')
    await flush()

    const payload = mocks.saveCompanyProfile.mock.calls[0][0]
    expect(Object.keys(payload).sort()).toEqual([
      'cover',
      'introText',
      'videoAutoplay',
      'videoChannelId',
      'videoFallbackUrl',
      'videoFeedId',
      'videoFeedToken',
      'videoSource',
      'videoUrl',
      'videoVid',
    ])
    expect(payload).not.toHaveProperty('id')
    expect(ElMessage.success).toHaveBeenCalledWith('已保存')
  })

  it('选中封面随保存提交', async () => {
    const w = await mountPage()
    const covers = w.findAll('.upload-field')
    await covers[covers.length - 1].trigger('click') // 最后一个是「封面 / 占位图」
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
      expect.objectContaining({ cover: '/uploads/new-cover.png' })
    )
  })

  it('保存失败：不提示成功', async () => {
    mocks.saveCompanyProfile.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
  })

  // 存储能力探测失败不能把整页拖垮，也不能把 rejection 漏成 Uncaught (in promise)：
  // 页面降级为「不可用」并给出可读原因，其余字段照常可编辑
  // ---- 腾讯视频档（部署级开关）----

  // 未启用时必须「选不了 + 说清为什么」：只置灰不给原因，客户会以为是产品 bug
  it('部署未启用腾讯视频：选项禁用且给出原因', async () => {
    const w = await mountPage()

    const opt = w.findAll('option').find((o: any) => o.text().includes('腾讯视频'))
    expect(opt!.attributes('disabled')).toBeDefined()

    await pickSource(w, 'tencent')
    expect(w.find('.el-alert').text()).toContain('系统设置')
    // 注意不能用 byPlaceholder：它找不到就抛错，断言「不存在」得自己扫一遍
    expect(
      w.findAll('.el-input').some((i: any) => i.attributes('placeholder') === VID_PLACEHOLDER)
    ).toBe(false)
  })

  it('部署已启用：显示 vid 输入框', async () => {
    const w = await mountPage(undefined, undefined, VCAP_ON)
    await pickSource(w, 'tencent')

    expect(byPlaceholder(w, VID_PLACEHOLDER).exists()).toBe(true)
    expect(w.find('.el-alert').exists()).toBe(false)
  })

  it('腾讯视频档没填 vid：拦下且不提交', async () => {
    const w = await mountPage(undefined, undefined, VCAP_ON)
    await pickSource(w, 'tencent')

    await saveButton(w).trigger('click')
    await flush()

    expect(ElMessage.warning).toHaveBeenCalledWith('腾讯视频档需要填写 vid')
    expect(mocks.saveCompanyProfile).not.toHaveBeenCalled()
  })

  it('腾讯视频档填了 vid：提交 videoVid', async () => {
    const w = await mountPage(undefined, undefined, VCAP_ON)
    await pickSource(w, 'tencent')
    await byPlaceholder(w, VID_PLACEHOLDER).setValue('n0041aa087e')
    await flush()

    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
      expect.objectContaining({ videoSource: 'tencent', videoVid: 'n0041aa087e' })
    )
  })

  // 自动播放：腾讯视频插件的 autoplay **默认 true**（与原生 video 相反），
  // 所以「默认关」与「打开后真的提交 true」两件事都必须钉住——
  // 只钉一件的话，漏掉的那一半表现是「开关在那儿摆着却不生效」，看不出异常。
  it('腾讯视频档：自动播放默认关，打开后随保存提交', async () => {
    const w = await mountPage(undefined, undefined, VCAP_ON)
    await pickSource(w, 'tencent')
    await byPlaceholder(w, VID_PLACEHOLDER).setValue('n0041aa087e')
    await flush()

    const sw = w.find('.el-switch input')
    expect(sw.exists()).toBe(true)
    expect((sw.element as HTMLInputElement).checked).toBe(false)

    await sw.setValue(true)
    await flush()
    await saveButton(w).trigger('click')
    await flush()

    expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
      expect.objectContaining({ videoSource: 'tencent', videoAutoplay: true })
    )
  })

  it('回填：后端说 videoAutoplay=true 时开关是打开的', async () => {
    const w = await mountPage(
      {
        id: 1,
        introText: '简介',
        videoSource: 'tencent',
        videoChannelId: '',
        videoFeedId: '',
        videoFeedToken: '',
        videoVid: 'n0041aa087e',
        videoUrl: '',
        videoFallbackUrl: false,
        videoAutoplay: true,
        cover: '',
      },
      undefined,
      VCAP_ON
    )
    await flush()

    expect((w.find('.el-switch input').element as HTMLInputElement).checked).toBe(true)
  })

  it('能力探测失败：页面仍可用，且上传档给出可读原因', async () => {
    mocks.getUploadCapability.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    await pickSource(w, 'mp4')

    expect(w.text()).toContain('存储能力探测失败')
    expect(byPlaceholder(w, '首页简介区文字').exists()).toBe(true)
    expect(saveButton(w).exists()).toBe(true)
  })

  // ---- 「自托管 MP4」：upload 与 url 在界面合成一档 ----
  //
  // 合成的动机：小程序端 company-video 里两者是**同一个分支**，
  // 都返回 'mp4' 并渲染原生 <video src>，访客看不出任何区别；
  // 差别只在「文件怎么放上去」。并列两个选项只会让客户对着两个效果
  // 一样的选项猜。
  //
  // 但**存库必须仍然写 upload / url**：后端 IsValidVideoSource 不认 'mp4'，
  // 历史数据、开放接口、小程序组件也都按这两个值分支。这一组用例的最主要
  // 目的就是把这条卡死——界面上引入一个叫 'mp4' 的取值太顺手了。
  describe('自托管 MP4 合并', () => {
    it('历史数据 upload：下拉显示「自托管 MP4」，子选择停在「上传」', async () => {
      const w = await mountPage(profile('upload', '/uploads/a.mp4'))
      await flush()

      // 下拉的界面值是合成档
      expect((w.find('.el-select').element as HTMLSelectElement).value).toBe('mp4')
      // 子选择回显成上传（否则老用户一进来看到的是「填直链」，以为配置丢了）
      expect(w.find('.upload-field').exists()).toBe(true)
    })

    it('历史数据 url：下拉显示「自托管 MP4」，子选择停在「直链」', async () => {
      const w = await mountPage(profile('url', 'https://cdn.example.com/a.mp4'))
      await flush()

      expect((w.find('.el-select').element as HTMLSelectElement).value).toBe('mp4')
      expect(byPlaceholder(w, 'https:// 开头的 mp4 地址').element.value).toBe(
        'https://cdn.example.com/a.mp4'
      )
    })

    // ↓ 最关键的一条：界面上的 'mp4' 绝不能泄漏到请求体
    it('保存时写的仍是 upload / url，不会把界面值 mp4 提交上去', async () => {
      const w = await mountPage(profile('upload', '/uploads/a.mp4'))
      await flush()

      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
        // videoUrl 必须把回显的地址原样带回去：拆槽之后如果从错的那个槽取值，
        // 就会存成空串——视频悄悄没了，而界面上一直显示着它
        expect.objectContaining({ videoSource: 'upload', videoUrl: '/uploads/a.mp4' })
      )
    })

    it('切到直链再保存：提交的是 url', async () => {
      const w = await mountPage(profile('upload', '/uploads/a.mp4'))
      await flush()

      await pickSource(w, 'mp4')
      await pickMp4Mode(w, 'url')
      // 切到直链后直链槽是空的（上传得到的地址不会被搬过来，这正是拆槽的目的），
      // 所以这里必须自己填一个地址——否则会被「请填写 MP4 地址」拦下，
      // 这条用例就变成在测拦截而不是在测取值映射
      await byPlaceholder(w, 'https:// 开头的 mp4 地址').setValue('https://cdn.example.com/b.mp4')
      await flush()
      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
        expect.objectContaining({ videoSource: 'url', videoUrl: 'https://cdn.example.com/b.mp4' })
      )
    })

    it('切到其它档再切回：不会残留 mp4 这个假取值', async () => {
      // 起始档位是 none，视频地址不落在任何一个上传/直链槽里，
      // 所以切回「自托管 MP4」后得自己传一个文件，否则会被空值拦截挡住
      const w = await mountPage(profile('none', '/uploads/a.mp4'), {
        driver: 'oss',
        presign: true,
        videoUpload: true,
        videoMaxMb: 100,
        reason: '',
      })
      await flush()

      await pickSource(w, 'mp4')
      await pickSource(w, 'none')
      await pickSource(w, 'mp4')
      videoUpload(w).vm.$emit('update:modelValue', '/uploads/c.mp4')
      await flush()
      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile).toHaveBeenCalledWith(
        expect.objectContaining({ videoSource: 'upload', videoUrl: '/uploads/c.mp4' })
      )
    })

    // 选了上传却没传文件：存下去就是一张永远播不出视频的名片，界面还看不出异常
    it('上传档没传文件：拦下且不提交', async () => {
      const w = await mountPage(profile('none', ''))
      await flush()

      await pickSource(w, 'mp4')
      await saveButton(w).trigger('click')
      await flush()

      expect(ElMessage.warning).toHaveBeenCalledWith(
        '请先上传视频文件，或改用「填写 MP4 地址」'
      )
      expect(mocks.saveCompanyProfile).not.toHaveBeenCalled()
    })
  })

  // ---- 「上传 / 直链」两个槽必须彼此隔离 ----
  //
  // 用户报的现象：从「填写 MP4 地址」切到「上传到云存储」，上传区里已经躺着一条
  // 带着绿色对勾的 video-langhui.mp4——那个文件根本不在云存储里，
  // 只是手填的直链被上传组件当成了「已选中的文件」
  //（UploadField 的 watch 会把 modelValue 映射成 file-list，文件名取 URL 末段）。
  // 反过来也成立：上传得到的地址会跑进直链输入框。
  //
  // 根因是三个来源共用一个 form.videoUrl。这一组守的就是「别再把它们合回一个字段」。
  describe('上传槽与直链槽互不串味', () => {
    const MP4_CAP = { driver: 'oss', presign: true, videoUpload: true, videoMaxMb: 200, reason: '' }

    it('从直链切到上传：上传组件拿到的是空值，不回显直链文件名', async () => {
      const w = await mountPage(profile('url', 'https://cdn.example.com/video-langhui.mp4'), MP4_CAP)
      await flush()
      // 先确认直链确实回填进了直链输入框（否则下面「上传组件是空的」就不成立）
      expect(byPlaceholder(w, 'https:// 开头的 mp4 地址').element.value).toBe(
        'https://cdn.example.com/video-langhui.mp4'
      )

      await pickMp4Mode(w, 'upload')

      // 拿到直链就会显示成「已上传的文件」——这正是用户看到的那一幕
      expect(videoUpload(w).props('modelValue')).toBe('')
    })

    it('从上传切到直链：直链输入框是空的，不搬运上传得到的地址', async () => {
      const w = await mountPage(profile('upload', '/uploads/up.mp4'), MP4_CAP)
      await flush()

      await pickMp4Mode(w, 'url')

      expect(byPlaceholder(w, 'https:// 开头的 mp4 地址').element.value).toBe('')
    })

    // 注意这两条必须「改过值再保存」：只回显不修改的话，错取的槽恰好也等于原值，
    // 用例照样绿——反向验证（把 payload 改回 form.videoUrl）时才发现这一点。
    it('上传档改传了新文件：提交新地址，不是回显时的旧地址', async () => {
      const w = await mountPage(profile('upload', '/uploads/old.mp4'), MP4_CAP)
      await flush()

      videoUpload(w).vm.$emit('update:modelValue', '/uploads/new.mp4')
      await flush()
      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile.mock.calls[0][0].videoUrl).toBe('/uploads/new.mp4')
    })

    it('直链档改了地址：提交新地址，不是回显时的旧地址', async () => {
      const w = await mountPage(profile('url', 'https://cdn.example.com/old.mp4'), MP4_CAP)
      await flush()

      await byPlaceholder(w, 'https:// 开头的 mp4 地址').setValue('https://cdn.example.com/new.mp4')
      await flush()
      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile.mock.calls[0][0].videoUrl).toBe(
        'https://cdn.example.com/new.mp4'
      )
    })

    // 上面两条钉「取当前档位的槽」，这条钉「另一个槽里的值不许串过来」
    it('两个槽都有值时，上传档不会把直链槽的值提交上去', async () => {
      const w = await mountPage(profile('upload', '/uploads/up.mp4'), MP4_CAP)
      await flush()

      // 先在直链槽里留一个值，再切回上传档
      await pickMp4Mode(w, 'url')
      await byPlaceholder(w, 'https:// 开头的 mp4 地址').setValue('https://other.example.com/x.mp4')
      await flush()
      await pickMp4Mode(w, 'upload')
      await saveButton(w).trigger('click')
      await flush()

      expect(mocks.saveCompanyProfile.mock.calls[0][0].videoUrl).toBe('/uploads/up.mp4')
    })

    // 来回切换不能丢值：客户传完视频想去看一眼直链长什么样，回来发现文件没了
    it('来回切换不丢值：切回上传档时之前上传的地址还在', async () => {
      const w = await mountPage(profile('upload', '/uploads/up.mp4'), MP4_CAP)
      await flush()

      await pickMp4Mode(w, 'url')
      await pickMp4Mode(w, 'upload')

      expect(videoUpload(w).props('modelValue')).toBe('/uploads/up.mp4')
    })

    // 用户报的第二个现象的另一半：提示写着「≤100MB」，选完文件却被告知超限。
    // 提示与校验上限必须来自同一个值（cap.videoMaxMb = 后台「单个视频大小上限」），
    // 只把它们写在两处、各取一个来源，就还会漂移
    it('容量提示与校验上限同源，都取后端能力探测值', async () => {
      const w = await mountPage(profile('none', ''), { ...MP4_CAP, videoMaxMb: 777 })
      await flush()

      await pickSource(w, 'mp4')

      const up = videoUpload(w)
      expect(up.props('maxMB')).toBe(777)
      expect(String(up.props('tip'))).toContain('777MB')
    })

    // 「最终播放地址」是排查小程序黑屏的第一现场：地址在浏览器里都打不开，
    // 小程序里一定播不了。展示值必须和 save() 写库的值同源，
    // 否则就会出现「后台看着是 A、库里存的是 B」这种最难查的情况。
    it('最终播放地址随交付方式切换，且可点开验证', async () => {
      const w = await mountPage(profile('url', 'https://cdn.example.com/a.mp4'), MP4_CAP)
      await flush()

      const link = () => w.find('.video-url-check a')
      expect(link().exists()).toBe(true)
      expect(link().attributes('href')).toBe('https://cdn.example.com/a.mp4')

      await pickMp4Mode(w, 'upload')
      videoUpload(w).vm.$emit('update:modelValue', '/uploads/up.mp4')
      await flush()

      expect(link().attributes('href')).toBe('/uploads/up.mp4')
      expect(w.find('.video-url-check').text()).toContain('相对路径')
    })

    it('非自托管档不把兜底地址当主视频源展示', async () => {
      const w = await mountPage(profile('channels', 'https://cdn.example.com/fallback.mp4'), MP4_CAP)
      await flush()

      expect(w.find('.video-url-check').exists()).toBe(false)
    })

    it('地址不是 http(s)/站内路径时只作纯文本，不生成可点链接', async () => {
      const w = await mountPage(profile('url', 'javascript:alert(1)'), MP4_CAP)
      await flush()

      expect(w.find('.video-url-check a').exists()).toBe(false)
      expect(w.find('.video-url-check').text()).toContain('javascript:alert(1)')
    })
  })
})
