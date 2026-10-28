import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import Login from './Login.vue'

// 登录页走 api/auth.adminLogin（httpOnly Cookie 流，不再持有 token），
// 这里 mock 掉网络层；element-plus 的 ElMessage 由请求拦截器负责，登录页只用到 success。
vi.mock('@/api/auth', () => ({
  adminLogin: vi.fn(async () => ({ code: 0 })),
}))
// 登录页标题品牌名走公开设置接口（无需登录），此处 mock 掉网络层
vi.mock('@/api/settings', () => ({
  getPublicSettings: vi.fn(async () => ({ code: 0, data: { brand_name: '微传名片' } })),
  getSettings: vi.fn(async () => ({ code: 0, data: [] })),
  updateSettings: vi.fn(async () => true),
}))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

import { adminLogin } from '@/api/auth'
import { getPublicSettings } from '@/api/settings'
import { useAuthStore } from '@/store/auth'

// 轻量 stub：仅渲染插槽 / 捕获 props，避免引入完整 Element Plus（jsdom 下过重易 OOM）。
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElForm = defineComponent({
  name: 'ElForm',
  // 表单校验在 stub 中直接放行（rules 声明式校验由 Element Plus 运行时负责，此处不重复测）
  methods: {
    validate: () => Promise.resolve(true),
    clearValidate: () => {},
  },
  template: `<form class="el-form"><slot /></form>`,
})
const ElFormItem = defineComponent({ name: 'ElFormItem', template: `<div class="el-form-item"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'type', 'placeholder'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :type="type || 'text'" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'disabled'],
  template: `<button class="el-button"><slot /></button>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-form': ElForm,
      'el-form-item': ElFormItem,
      'el-input': ElInput,
      'el-button': ElButton,
    },
  },
}

// 每个用例独立 pinia + 内存 router；router 需先导航到目标路由再挂载（useRoute 才能拿到 query）。
// 注意：auth store 的初始 state 从 localStorage 读取（httpOnly Cookie 改造后仅存非敏感标记），
// jsdom 的 localStorage 跨用例残留，故每个用例前清掉，保证「未登录」初始态。
async function setup(initialPath = '/login') {
  localStorage.clear()
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes: [] })
  router.replace(initialPath)
  await router.isReady()
  const w = mount(Login, { ...mountOpts, global: { ...mountOpts.global, plugins: [router] } })
  return { w, router, auth: useAuthStore() }
}

describe('Login', () => {
  beforeEach(() => {
    vi.mocked(adminLogin).mockClear()
    vi.mocked(adminLogin).mockImplementation(async () => ({ code: 0 }) as any)
  })

  it('渲染用户名 / 密码输入框与登录按钮', async () => {
    const { w } = await setup()
    expect(w.findAll('input.el-input')).toHaveLength(2)
    expect(w.findAll('input[type="password"]')).toHaveLength(1)
    expect(w.text()).toContain('登录')
  })

  it('提交成功：调 adminLogin、写登录态、跳转默认落地页', async () => {
    const { w, router, auth } = await setup()
    const inputs = w.findAll('input.el-input')
    await inputs[0].setValue('admin')
    await inputs[1].setValue('admin123456')
    await w.find('button.el-button').trigger('click')
    await vi.waitFor(() => expect(vi.mocked(adminLogin)).toHaveBeenCalledWith('admin', 'admin123456'))
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/apply/open'))

    expect(auth.loggedIn).toBe(true)
    expect(auth.username).toBe('admin')
  })

  it('带 redirect query 时登录后跳到指定页', async () => {
    const { w, router } = await setup('/login?redirect=%2Femployees')
    const inputs = w.findAll('input.el-input')
    await inputs[0].setValue('admin')
    await inputs[1].setValue('pwd')
    await w.find('button.el-button').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/employees'))
  })

  it('登录失败：不写登录态、不跳转', async () => {
    vi.mocked(adminLogin).mockClear()
    vi.mocked(adminLogin).mockImplementation(async () => {
      throw new Error('密码错误')
    })
    const { w, router, auth } = await setup()
    const inputs = w.findAll('input.el-input')
    await inputs[0].setValue('admin')
    await inputs[1].setValue('wrong')
    await w.find('button.el-button').trigger('click')
    await vi.waitFor(() => expect(vi.mocked(adminLogin)).toHaveBeenCalledTimes(1))
    await nextTick()
    await nextTick()

    expect(auth.loggedIn).toBe(false)
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('带 company 白标码命中时，登录页标题显示该企业简称', async () => {
    vi.mocked(getPublicSettings).mockImplementation(async (company?: string) =>
      (company === 'langhui'
        ? { code: 0, data: { brand_name: '微传名片', login_brand_name: '朗汇机电' } }
        : { code: 0, data: { brand_name: '微传名片' } }) as { code: number; data: Record<string, string> }
    )
    const { w } = await setup('/login?company=langhui')
    await vi.waitFor(() => expect(w.text()).toContain('朗汇机电'))
    expect(w.text()).toContain('朗汇机电 · 管理后台')
  })

  it('白标码不匹配时回落全局品牌名', async () => {
    vi.mocked(getPublicSettings).mockImplementation(async () => ({
      code: 0,
      data: { brand_name: '微传名片' },
    }))
    const { w } = await setup('/login?company=nope')
    await vi.waitFor(() => expect(w.text()).toContain('微传名片'))
    expect(w.text()).toContain('微传名片 · 管理后台')
  })
})
