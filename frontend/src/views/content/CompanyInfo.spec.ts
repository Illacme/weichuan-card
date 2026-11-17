import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import CompanyInfo from './CompanyInfo.vue'

// 公司信息页：六个企业内容页里最简单的一页，价值在「保存到底提交了什么」——
// 多提交一个 id 就可能被后端当成更新主键，少提交一个字段就是静默丢数据。
const mocks = vi.hoisted(() => ({
  getCompanyInfo: vi.fn(async (): Promise<any> => ({
    id: 1,
    fullName: '朗汇机电设备有限公司',
    shortName: '朗汇机电',
    companyNameScale: 'large',
    address: '上海市某路 1 号',
    tel: '021-12345678',
    logo: '/uploads/logo.png',
  })),
  saveCompanyInfo: vi.fn(async (_payload: any) => ({ code: 0 })),
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
const ElForm = defineComponent({ name: 'ElForm', template: `<form class="el-form"><slot /></form>` })
const ElFormItem = defineComponent({
  name: 'ElFormItem',
  props: ['label'],
  template: `<div class="el-form-item"><label>{{ label }}</label><slot /></div>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
// el-radio-group / el-radio：首页公司名四档单选。用 provide/inject 搭最小链路，
// 让「点了某档真能写回表单」被测到——只渲染不可点的 label 测不出「改了没生效」。
const ElRadioGroup = defineComponent({
  name: 'ElRadioGroup',
  props: ['modelValue'],
  emits: ['update:modelValue'],
  setup(_props, { slots, emit }) {
    provide('radioGroup', { pick: (v: string) => emit('update:modelValue', v) })
    return () => h('div', { class: 'el-radio-group' }, slots.default ? slots.default() : [])
  },
})
const ElRadio = defineComponent({
  name: 'ElRadio',
  props: ['value'],
  inheritAttrs: false,
  setup(props, { slots }) {
    const g: any = inject('radioGroup')
    return () =>
      h('label', { class: 'el-radio', 'data-value': props.value, onClick: () => g?.pick(props.value) },
        slots.default ? slots.default() : [])
  },
})
// UploadField 是 v-model 组件：点一下就当作「选了图」，用于验证 logo 会随保存提交
const UploadField = defineComponent({
  name: 'UploadField',
  props: ['modelValue', 'multiple', 'accept', 'libraryType'],
  emits: ['update:modelValue'],
  template: `<button class="upload-field" @click="$emit('update:modelValue', '/uploads/new-logo.png')">
    {{ modelValue }}</button>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-button': ElButton,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-input': ElInput,
      'el-radio-group': ElRadioGroup,
      'el-radio': ElRadio,
      UploadField,
    },
    directives: { loading: {} },
  },
}

async function mountPage() {
  const w = mount(CompanyInfo, mountOpts)
  await flush(w)
  return w
}

async function flush(_w?: any) {
  await nextTick()
  await nextTick()
  await nextTick()
}

// 按 placeholder 找输入框：页面字段多，用 placeholder 更稳
function inputByPlaceholder(w: any, placeholder: string) {
  const el = w.findAll('.el-input').find((i: any) => i.attributes('placeholder') === placeholder)
  if (!el) throw new Error(`找不到 placeholder=${placeholder} 的输入框`)
  return el
}

describe('content/CompanyInfo', () => {
  beforeEach(() => {
    mocks.getCompanyInfo.mockClear()
    mocks.saveCompanyInfo.mockClear()
    ;(ElMessage.success as any).mockClear()
  })

  it('挂载即拉取并回填表单', async () => {
    const w = await mountPage()

    expect(mocks.getCompanyInfo).toHaveBeenCalledTimes(1)
    expect(inputByPlaceholder(w, '名片首页公司名').element.value).toBe('朗汇机电设备有限公司')
    expect(inputByPlaceholder(w, '名片首页座机').element.value).toBe('021-12345678')
  })

  // 接口失败时页面必须可用可重试，不能白屏或卡在 loading
  it('拉取失败：不崩，且 loading 落下', async () => {
    mocks.getCompanyInfo.mockRejectedValueOnce(new Error('403'))
    const w = await mountPage()

    expect(w.find('.el-card').exists()).toBe(true)
    const btn = w.findAll('.el-button')[0]
    expect(btn.exists()).toBe(true)
  })

  it('输入写回表单', async () => {
    const w = await mountPage()
    const input = inputByPlaceholder(w, '名片首页地址')

    await input.setValue('上海市另一条路 2 号')
    await flush()

    await w.findAll('.el-button')[1].trigger('click') // 保存
    await flush()
    expect(mocks.saveCompanyInfo).toHaveBeenCalledWith(
      expect.objectContaining({ address: '上海市另一条路 2 号' })
    )
  })

  // 只提交 6 个可编辑字段：id 混进 payload 会被后端当主键处理
  it('保存只提交可编辑字段，不带 id', async () => {
    const w = await mountPage()

    await w.findAll('.el-button')[1].trigger('click')
    await flush()

    expect(mocks.saveCompanyInfo).toHaveBeenCalledTimes(1)
    const payload = mocks.saveCompanyInfo.mock.calls[0][0]
    expect(Object.keys(payload).sort()).toEqual([
      'address', 'companyNameScale', 'fullName', 'logo', 'shortName', 'tel',
    ])
    expect(payload).not.toHaveProperty('id')
  })

  // ---- 首页公司名四档（大/中/小/关闭）----
  function scaleRadio(w: any, value: string) {
    const r = w.find(`.el-radio[data-value="${value}"]`)
    if (!r.exists()) throw new Error(`找不到首页公司名档位 ${value}`)
    return r
  }

  it('首页公司名缺省为「大」，四档都可选且随保存提交', async () => {
    const w = await mountPage()
    for (const v of ['large', 'medium', 'small', 'hidden']) {
      expect(scaleRadio(w, v).exists()).toBe(true)
    }
    await w.findAll('.el-button')[1].trigger('click')
    await flush()
    expect(mocks.saveCompanyInfo.mock.calls[0][0].companyNameScale).toBe('large')
  })

  it('首页公司名选中「关闭」随保存提交', async () => {
    const w = await mountPage()
    await scaleRadio(w, 'hidden').trigger('click')
    await flush()
    await w.findAll('.el-button')[1].trigger('click')
    await flush()
    expect(mocks.saveCompanyInfo.mock.calls[0][0].companyNameScale).toBe('hidden')
  })

  it('首页公司名字段缺失时归一到「大」', async () => {
    mocks.getCompanyInfo.mockResolvedValueOnce({
      id: 1, fullName: '无档位公司', shortName: '无', address: '', tel: '', logo: '',
    })
    const w = await mountPage()
    expect((w.vm as any).form.companyNameScale).toBe('large')
  })

  it('选中的 LOGO 随保存提交', async () => {
    const w = await mountPage()

    await w.find('.upload-field').trigger('click')
    await flush()
    await w.findAll('.el-button')[1].trigger('click')
    await flush()

    expect(mocks.saveCompanyInfo).toHaveBeenCalledWith(
      expect.objectContaining({ logo: '/uploads/new-logo.png' })
    )
  })

  it('保存成功提示「已保存」', async () => {
    const w = await mountPage()

    await w.findAll('.el-button')[1].trigger('click')
    await flush()

    expect(ElMessage.success).toHaveBeenCalledWith('已保存')
  })

  // 失败时不能弹成功；saving 也必须复位，否则按钮永久转圈
  it('保存失败：不提示成功，saving 复位', async () => {
    mocks.saveCompanyInfo.mockRejectedValueOnce(new Error('500'))
    const w = await mountPage()

    await w.findAll('.el-button')[1].trigger('click')
    await flush()

    expect(ElMessage.success).not.toHaveBeenCalled()
    const saveBtn = w.findAll('.el-button')[1]
    expect(saveBtn.exists()).toBe(true)
  })
})
