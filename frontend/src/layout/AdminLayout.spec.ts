import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import AdminLayout from './AdminLayout.vue'

// 后台外壳：菜单分组与权限过滤、品牌名回落、登出、手机端分流。
// 「企业管理员看不到平台管理组」是这里的核心安全约束——菜单藏不住就等于没权限，
// 但把入口露出来会让企业管理员反复点进去吃 403。
const mocks = vi.hoisted(() => ({
  getMe: vi.fn(async (): Promise<any> => ({ code: 0, data: { isPlatform: true } })),
  adminLogout: vi.fn(async () => ({ code: 0 })),
  settingsLoad: vi.fn(),
  isPhone: false,
}))

// auth 用普通对象：computed 在 mount 时求值，改属性后重新 mount 即可（无需响应式）
const auth = {
  loggedIn: true,
  profileLoaded: true,
  isPlatform: false,
  companyName: '',
  companyShortName: '',
  username: 'admin',
  hydrate: vi.fn(),
  logout: vi.fn(),
}

vi.mock('@/store/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/store/settings', () => ({
  useSettingsStore: () => ({ load: mocks.settingsLoad, displayBrandName: '微传名片' }),
}))
vi.mock('@/api/auth', () => ({ getMe: mocks.getMe, adminLogout: mocks.adminLogout }))
vi.mock('@/composables/useBreakpoint', async () => {
  const { ref } = await import('vue')
  return { useBreakpoint: () => ({ isPhone: ref(mocks.isPhone) }) }
})

const ElContainer = defineComponent({ name: 'ElContainer', template: `<div class="el-container"><slot /></div>` })
const ElAside = defineComponent({
  name: 'ElAside',
  props: ['width'],
  template: `<aside class="el-aside" v-bind="$attrs"><slot /></aside>`,
  inheritAttrs: true,
})
const ElHeader = defineComponent({ name: 'ElHeader', template: `<header class="el-header"><slot /></header>` })
const ElMain = defineComponent({ name: 'ElMain', template: `<main class="el-main"><slot /></main>` })
const ElMenu = defineComponent({
  name: 'ElMenu',
  props: ['defaultActive', 'router', 'backgroundColor', 'textColor', 'activeTextColor'],
  template: `<nav class="el-menu" :data-active="defaultActive"><slot /></nav>`,
})
const ElSubMenu = defineComponent({
  name: 'ElSubMenu',
  props: ['index'],
  template: `<div class="el-sub-menu" :data-index="index"><slot name="title" /><slot /></div>`,
})
const ElMenuItem = defineComponent({
  name: 'ElMenuItem',
  props: ['index'],
  template: `<a class="el-menu-item" :data-path="index"><slot /></a>`,
})
// 不要在这里 emit('click')：stub 根元素是 <button>，同时 emit 会让一次点击触发两次
// （原生事件冒泡到组件根 + 组件自定义事件），登出接口就会被调两遍。
// 靠 inheritAttrs 把 onClick 落到真实 DOM 上即可，与生产 ElButton 的行为一致。
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['text', 'type'],
  template: `<button class="el-button" v-bind="$attrs"><slot /></button>`,
  inheritAttrs: true,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'size', 'effect'],
  template: `<span class="el-tag"><slot /></span>`,
})
const ElIcon = defineComponent({ name: 'ElIcon', template: `<i><slot /></i>` })
const ElAvatar = defineComponent({ name: 'ElAvatar', props: ['size'], template: `<span class="el-avatar"><slot /></span>` })
const ElDropdown = defineComponent({
  name: 'ElDropdown',
  emits: ['command'],
  template: `<div class="el-dropdown"><slot /><slot name="dropdown" /></div>`,
})
const ElDropdownMenu = defineComponent({ name: 'ElDropdownMenu', template: `<div class="el-dropdown-menu"><slot /></div>` })
const ElDropdownItem = defineComponent({
  name: 'ElDropdownItem',
  props: ['command', 'divided', 'disabled'],
  template: `<button class="el-dropdown-item" @click="$emit('command')"><slot /></button>`,
})
const ChangePasswordDialog = defineComponent({
  name: 'ChangePasswordDialog',
  props: ['modelValue'],
  template: `<div v-if="modelValue" class="pwd-dialog" />`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-container': ElContainer,
      'el-aside': ElAside,
      'el-header': ElHeader,
      'el-main': ElMain,
      'el-menu': ElMenu,
      'el-sub-menu': ElSubMenu,
      'el-menu-item': ElMenuItem,
      'el-button': ElButton,
      'el-tag': ElTag,
      'el-icon': ElIcon,
      'el-avatar': ElAvatar,
      'el-dropdown': ElDropdown,
      'el-dropdown-menu': ElDropdownMenu,
      'el-dropdown-item': ElDropdownItem,
      ChangePasswordDialog,
      RouterView: defineComponent({ name: 'RouterView', template: `<div class="router-view" />` }),
    },
    directives: { loading: {} },
  },
}

const Blank = defineComponent({ template: `<div />` })

async function makeRouter(path = '/') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: Blank, meta: { title: '开通审核' } },
      { path: '/employees', component: Blank, meta: { title: '员工管理' } },
      { path: '/content/projects', component: Blank, meta: { title: '项目案例' } },
      { path: '/companies', component: Blank, meta: { title: '企业管理' } },
      { path: '/login', component: Blank },
    ],
  })
  await router.push(path)
  await router.isReady()
  return router
}

async function mountLayout(path = '/') {
  const router = await makeRouter(path)
  const w = mount(AdminLayout, { ...mountOpts, global: { ...mountOpts.global, plugins: [router] } })
  await nextTick()
  await nextTick()
  return { w, router }
}

function groupNames(w: any): string[] {
  return w.findAll('.el-sub-menu').map((g: any) => g.find('.brand-name, span').text())
}

function menuPaths(w: any): string[] {
  return w.findAll('.el-menu-item').map((i: any) => i.attributes('data-path'))
}

describe('layout/AdminLayout', () => {
  beforeEach(() => {
    auth.loggedIn = true
    auth.profileLoaded = true
    auth.isPlatform = false
    auth.companyName = ''
    auth.companyShortName = ''
    auth.username = 'admin'
    auth.hydrate.mockClear()
    auth.logout.mockClear()
    mocks.getMe.mockClear()
    mocks.adminLogout.mockClear()
    mocks.settingsLoad.mockClear()
    mocks.isPhone = false
  })

  it('平台超管：菜单包含「平台管理」组的全部入口', async () => {
    auth.isPlatform = true
    const { w } = await mountLayout()

    const paths = menuPaths(w)
    expect(paths).toContain('/companies')
    expect(paths).toContain('/platform/settings')
    expect(paths).toContain('/platform/audit-logs')
  })

  // 企业管理员不得看到平台级入口（后端也会挡，但露出入口只会制造 403）
  it('企业管理员：平台管理组整体不渲染', async () => {
    auth.isPlatform = false
    auth.companyName = '朗汇机电'
    const { w } = await mountLayout()

    const paths = menuPaths(w)
    expect(paths).not.toContain('/companies')
    expect(paths).not.toContain('/platform/settings')
    // 业务菜单照常在
    expect(paths).toContain('/employees')
    expect(paths).toContain('/content/projects')
    expect(groupNames(w).some((t: string) => t.includes('平台管理'))).toBe(false)
  })

  // 品牌名：平台超管无归属企业 → 用系统设置里的品牌名
  it('品牌名：平台超管回落系统设置品牌', async () => {
    auth.isPlatform = true
    const { w } = await mountLayout()

    expect(w.find('.brand-name').text()).toBe('微传名片 · 管理后台')
  })

  it('品牌名：企业管理员优先用企业简称', async () => {
    auth.isPlatform = false
    auth.companyName = '朗汇机电设备有限公司'
    auth.companyShortName = '朗汇机电'
    const { w } = await mountLayout()

    expect(w.find('.brand-name').text()).toBe('朗汇机电 · 管理后台')
  })

  it('品牌名：企业没设简称时回落系统品牌', async () => {
    auth.isPlatform = false
    auth.companyName = '朗汇机电设备有限公司'
    auth.companyShortName = ''
    const { w } = await mountLayout()

    expect(w.find('.brand-name').text()).toBe('微传名片 · 管理后台')
  })

  it('顶部标题取路由 meta.title', async () => {
    const { w } = await mountLayout('/content/projects')

    expect(w.find('.header-title').text()).toBe('项目案例')
  })

  it('当前菜单项跟随路由', async () => {
    const { w } = await mountLayout('/employees')

    expect(w.find('.el-menu').attributes('data-active')).toBe('/employees')
  })

  // 刷新后 JS 读不到 httpOnly Cookie 里的 claims，必须用 /api/admin/me 取回身份
  it('已登录但身份未恢复：挂载时调 getMe 并 hydrate', async () => {
    auth.loggedIn = true
    auth.profileLoaded = false
    await mountLayout()
    await nextTick()

    expect(mocks.getMe).toHaveBeenCalledTimes(1)
    expect(auth.hydrate).toHaveBeenCalledWith({ isPlatform: true })
  })

  it('身份已恢复：不重复请求', async () => {
    auth.profileLoaded = true
    await mountLayout()
    await nextTick()

    expect(mocks.getMe).not.toHaveBeenCalled()
  })

  // 网络异常时保留本地缓存身份，不能把用户挡在登录页
  it('取身份失败：保留本地身份不阻塞界面', async () => {
    auth.loggedIn = true
    auth.profileLoaded = false
    mocks.getMe.mockRejectedValueOnce(new Error('network'))
    const { w } = await mountLayout()
    await nextTick()

    expect(auth.hydrate).not.toHaveBeenCalled()
    expect(w.find('.admin-layout').exists()).toBe(true)
  })

  it('挂载时加载运行参数（品牌名等）', async () => {
    await mountLayout()
    await nextTick()

    expect(mocks.settingsLoad).toHaveBeenCalled()
  })

  it('退出：调登出接口、清本地身份并回登录页', async () => {
    const { w, router } = await mountLayout()
    const logoutBtn = w.findAll('.el-button').find((b: any) => b.text().includes('退出'))!

    await logoutBtn.trigger('click')
    await flushPromises()

    expect(mocks.adminLogout).toHaveBeenCalledTimes(1)
    expect(auth.logout).toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  // 登出接口挂了也必须本地登出：否则用户被困在后台退不出去
  it('登出接口失败：本地照常登出', async () => {
    mocks.adminLogout.mockRejectedValueOnce(new Error('network'))
    const { w, router } = await mountLayout()
    const logoutBtn = w.findAll('.el-button').find((b: any) => b.text().includes('退出'))!

    await logoutBtn.trigger('click')
    await flushPromises()

    expect(auth.logout).toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('点「修改密码」打开弹层', async () => {
    const { w } = await mountLayout()
    const btn = w.findAll('.el-button').find((b: any) => b.text().includes('修改密码'))!

    await btn.trigger('click')
    await nextTick()

    expect(w.find('.pwd-dialog').exists()).toBe(true)
  })

  // 手机端把公司名/用户名/改密/退出收进头像下拉，避免顶栏拥挤
  it('手机端：不渲染顶栏按钮，改走头像下拉', async () => {
    mocks.isPhone = true
    auth.username = '张伟'
    const { w } = await mountLayout()

    expect(w.find('.el-dropdown').exists()).toBe(true)
    expect(w.findAll('.el-button').some((b: any) => b.text().includes('退出'))).toBe(false)
    expect(w.find('.header-avatar').text()).toBe('张')
  })

  // 窄屏选了菜单就该收起抽屉（否则遮罩挡住刚打开的内容页）
  it('路由变化时收起窄屏抽屉', async () => {
    const { w, router } = await mountLayout()

    ;(w.vm as any).asideOpen = true
    await router.push('/employees')
    await nextTick()
    await nextTick()

    expect((w.vm as any).asideOpen).toBe(false)
  })
})
