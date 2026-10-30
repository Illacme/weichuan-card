import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import AdminLayout from './AdminLayout.vue'
import { routes } from '@/router'

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

// tenant 也用普通对象：需要断言「同步身份」「登出清视角」这些副作用，
// 真 store 还得额外挂 pinia，收益不成比例（store 自身的行为由 store/tenant.spec.ts 覆盖）。
const tenant = {
  currentId: 0,
  currentName: '',
  needPick: false,
  companies: [] as any[],
  loading: false,
  loaded: false,
  error: false,
  syncIdentity: vi.fn(),
  ensureLoaded: vi.fn(async () => {}),
  setCurrent: vi.fn(),
  search: vi.fn(async () => {}),
  clear: vi.fn(),
}
vi.mock('@/store/tenant', () => ({ useTenantStore: () => tenant }))
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
// 下拉：把子项的 command 真的回传到 el-dropdown 的 @command 上。
// 不接这一层的话「手机端点头像 → 切换企业」这条路径在测试里永远点不动，
// 只能直接调 vm 上的方法——那就等于没测事件接线。
const ElDropdown = defineComponent({
  name: 'ElDropdown',
  emits: ['command'],
  setup(_, { slots, emit }) {
    provide('dropdownCommand', (c: string) => emit('command', c))
    return () => h('div', { class: 'el-dropdown' }, [slots.default?.(), slots.dropdown?.()])
  },
})
const ElDropdownMenu = defineComponent({ name: 'ElDropdownMenu', template: `<div class="el-dropdown-menu"><slot /></div>` })
const ElDropdownItem = defineComponent({
  name: 'ElDropdownItem',
  props: ['command', 'divided', 'disabled'],
  setup(props, { slots }) {
    const fire = inject<((c: string) => void) | null>('dropdownCommand', null)
    return () =>
      h(
        'button',
        {
          class: 'el-dropdown-item',
          onClick: () => {
            if (props.command) fire?.(props.command as string)
          },
        },
        slots.default?.()
      )
  },
})
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue', 'placeholder', 'loading', 'filterable', 'clearable', 'remote', 'remoteMethod', 'fallbackPlacements'],
  template: `<div class="el-select" v-bind="$attrs"><slot /></div>`,
  inheritAttrs: true,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<div class="el-option" :data-value="value">{{ label }}</div>`,
})
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width', 'appendToBody'],
  template: `<div v-if="modelValue" class="el-dialog"><slot /><slot name="footer" /></div>`,
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
      'el-select': ElSelect,
      'el-option': ElOption,
      'el-dialog': ElDialog,
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

// 路由器里「应该出现在侧边栏」的条目：子路由带 meta.group 就说明它是菜单项
// （group 只用来分组，没有它的路由如 /login、父级布局本身不是菜单）。
function routeMenuEntries(): { path: string; title: string }[] {
  const out: { path: string; title: string }[] = []
  for (const r of routes as any[]) {
    for (const c of r.children ?? []) {
      if (!c.meta?.group) continue
      out.push({ path: ('/' + c.path).replace(/\/{2,}/g, '/'), title: c.meta.title })
    }
  }
  return out
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
    tenant.currentId = 0
    tenant.currentName = ''
    tenant.needPick = false
    tenant.companies = []
    tenant.loaded = false
    tenant.error = false
    tenant.syncIdentity.mockClear()
    tenant.ensureLoaded.mockClear()
    tenant.setCurrent.mockClear()
    tenant.clear.mockClear()
  })

  it('平台超管：菜单包含「平台管理」组的全部入口', async () => {
    auth.isPlatform = true
    const { w } = await mountLayout()

    const paths = menuPaths(w)
    expect(paths).toContain('/companies')
    expect(paths).toContain('/platform/settings')
    expect(paths).toContain('/platform/audit-logs')
  })

  // ── 守门：路由 ↔ 菜单必须一一对应 ──────────────────────────────
  // 「加了页面忘加菜单项」的后果是功能做完了、用户永远找不到入口（本轮真实踩过：
  // 「小程序入口」的页面和路由都加了，侧边栏却漏了这一项）；反过来「菜单指向不存在
  // 的路由」点进去是空白页。两份清单都只存在于源码里（router 的 meta.group / 菜单
  // 数组），所以拿真实 routes 断言，而不是在这里再抄一份期望列表——抄一份只会一起过时。
  it('守门：每个带 meta.group 的路由都能在侧边栏找到入口', async () => {
    auth.isPlatform = true // 平台超管视角：所有分组都可见
    const { w } = await mountLayout()

    const paths = menuPaths(w)
    const missing = routeMenuEntries().filter((r) => !paths.includes(r.path))
    expect(missing.map((m) => `${m.path}（${m.title}）`)).toEqual([])
  })

  it('守门：侧边栏每个菜单项都指向真实存在的路由', async () => {
    auth.isPlatform = true
    const { w } = await mountLayout()

    const router = createRouter({ history: createMemoryHistory(), routes: routes as any })
    // 判据用「匹配记录里有没有一条 path 正好等于它」：router 末尾有 catch-all
    // （`/:pathMatch(.*)*`），所以 `matched.length > 0` 对任何乱写的路径都成立——
    // 用那个判据这条守门永远绿，等于没写。
    const dead = menuPaths(w).filter(
      (p: string) => !router.resolve(p).matched.some((r: any) => r.path === p)
    )
    expect(dead).toEqual([])
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

// ===== 平台超管「切换当前企业」 =====
// 这是本功能的三条硬约束，缺一条就会静默出事故：
//   ① 未选企业时，企业内容/统计/审核/员工页必须挡住（后端会兜到 1 号企业或「全部企业」）；
//   ② 平台级页面（企业管理/系统设置/…）不受影响，选不选都能用；
//   ③ 企业管理员永远看不到选择器，也永远不进空态。
describe('layout/AdminLayout 企业作用域', () => {
  // 这个 describe 同样要逐例重置：mocks.isPhone / tenant.* 都是模块级单例，
  // 上一个用例改过就漏到下一个（曾经因为漏了这段，最后一条「退出」用例
  // 继承了手机端布局、找不到顶栏按钮而红）。
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
    tenant.currentId = 0
    tenant.currentName = ''
    tenant.needPick = false
    tenant.companies = []
    tenant.loaded = false
    tenant.error = false
    tenant.syncIdentity.mockClear()
    tenant.ensureLoaded.mockClear()
    tenant.setCurrent.mockClear()
    tenant.clear.mockClear()
  })

  it('挂载时把身份同步给租户作用域（子页面 onMounted 先发请求，必须早于它）', async () => {
    auth.isPlatform = true
    await mountLayout()
    // 第二个参数是 auth.companyId（本 mock 里未声明 → undefined），关键是「身份已同步」
    expect(tenant.syncIdentity).toHaveBeenCalledWith(auth.isPlatform, undefined)
  })

  it('平台超管未选企业：企业内容页显示空态，不渲染页面', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    // 有企业可选、只是还没选（零企业的情形是另一条用例）
    tenant.companies = [{ id: 1, name: '甲公司' }]
    const { w } = await mountLayout('/content/projects')

    expect(w.find('.pick-empty').exists()).toBe(true)
    expect(w.find('.router-view').exists()).toBe(false)
    expect(w.text()).toContain('请先选择要管理的企业')
  })

  // 新装且没勾「装载演示数据」：系统里一家企业都没有。此时把人留在「请先选择要管理的
  // 企业」是死路——点开选择器是空列表。直接送到「企业管理」，那里才有建企业的入口。
  it('平台超管一家企业都没有：自动跳到「企业管理」', async () => {
    auth.isPlatform = true
    tenant.companies = []
    const { router } = await mountLayout('/apply/open')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/companies')
  })

  // 线上现象：刷新后台，先闪一下「还没有创建任何企业」，再变成「请先选择要管理的企业」。
  // 成因是把「还没查」当成了「确实没有」——首帧 companies 是空数组、loading 还是 false，
  // 旧判断 `!loading && length===0` 立刻成立。空数组必须靠 loaded 区分「没查」和「没有」。
  it('首帧（还没开始拉列表）：显示「请先选择」，绝不谎称没有企业', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    tenant.companies = []
    tenant.loaded = false // 关键：列表还没查过
    tenant.loading = false
    const { w } = await mountLayout('/content/projects')

    expect(w.text()).toContain('请先选择要管理的企业')
    expect(w.text()).not.toContain('还没有创建任何企业')
    // 按钮也必须是「选择企业」而不是「去创建企业」，否则会把人带到一个空的选择器
    const btn = w.findAll('.el-button').find((b: any) => b.text() === '选择企业')
    expect(btn).toBeDefined()
  })

  // 反向：确实查过、没失败、一条都没有 —— 这时才敢说「还没有创建任何企业」
  it('确认一家企业都没有：显示「还没有创建任何企业」，按钮去建企业', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    tenant.companies = []
    tenant.loaded = true
    tenant.error = false
    const { w } = await mountLayout('/content/projects')

    expect(w.text()).toContain('还没有创建任何企业')
    const btn = w.findAll('.el-button').find((b: any) => b.text() === '去创建企业')
    expect(btn).toBeDefined()
  })

  // 拉取失败 ≠ 没有企业。旧实现把两者混为一谈，会把一家有十家客户企业的平台
  // 管理员送去「创建企业」。现在单列一档：说清是没取到，给「重试」。
  it('企业列表加载失败：空态说「加载失败」并给重试，不谎称没有企业', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    tenant.companies = []
    tenant.loaded = true
    tenant.error = true
    // 列表拉取失败时不自动跳转（不知道到底有没有企业），停在空态但给对出口
    tenant.ensureLoaded.mockRejectedValueOnce(new Error('网络异常'))
    const { w, router } = await mountLayout('/content/projects')
    await flushPromises()

    expect(w.text()).toContain('企业列表加载失败')
    expect(w.text()).not.toContain('还没有创建任何企业')
    expect(router.currentRoute.value.path).toBe('/content/projects')

    const btn = w.findAll('.el-button').find((b: any) => b.text() === '重试')
    expect(btn).toBeDefined()
    await btn!.trigger('click')
    await flushPromises()
    // 重试要真的重新发请求（force=true），否则按「已加载过」直接返回，按钮等于摆设
    expect(tenant.ensureLoaded).toHaveBeenCalledWith(true)
  })

  it('平台超管未选企业：审核 / 统计 / 员工页同样挡住', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    tenant.companies = [{ id: 1, name: '甲公司' }]
    for (const path of ['/', '/employees', '/stats', '/apply/modify']) {
      const { w } = await mountLayout(path)
      expect(w.find('.pick-empty').exists(), path).toBe(true)
    }
  })

  // 选不选企业跟「企业管理」「系统设置」这些平台级页面无关，不该把它们也挡住
  it('平台超管未选企业：平台级页面照常渲染', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    const { w } = await mountLayout('/companies')

    expect(w.find('.pick-empty').exists()).toBe(false)
    expect(w.find('.router-view').exists()).toBe(true)
  })

  it('平台超管已选企业：正常渲染页面，无空态', async () => {
    auth.isPlatform = true
    tenant.needPick = false
    tenant.currentId = 3
    const { w } = await mountLayout('/content/projects')

    expect(w.find('.pick-empty').exists()).toBe(false)
    expect(w.find('.router-view').exists()).toBe(true)
  })

  // 企业管理员只有一个企业，needPick 恒 false —— 空态绝不该出现在他身上
  it('企业管理员：不进空态', async () => {
    auth.isPlatform = false
    tenant.needPick = false
    const { w } = await mountLayout('/content/projects')

    expect(w.find('.pick-empty').exists()).toBe(false)
    expect(w.find('.router-view').exists()).toBe(true)
  })

  it('桌面端：平台超管顶栏有企业选择器，企业管理员没有', async () => {
    auth.isPlatform = true
    const { w: platformView } = await mountLayout()
    expect(platformView.find('.company-picker').exists()).toBe(true)

    auth.isPlatform = false
    const { w: tenantView } = await mountLayout()
    expect(tenantView.find('.company-picker').exists()).toBe(false)
  })

  it('空态里的「选择企业」按钮打开选择弹窗', async () => {
    auth.isPlatform = true
    tenant.needPick = true
    tenant.companies = [{ id: 1, name: '甲公司' }]
    const { w } = await mountLayout('/content/projects')
    expect(w.find('.el-dialog').exists()).toBe(false)

    await w.find('.pick-empty .el-button').trigger('click')
    await nextTick()

    expect(w.find('.el-dialog').exists()).toBe(true)
    expect(w.find('.el-dialog .company-picker').exists()).toBe(true)
  })

  // 手机端顶栏放不下选择器：收进头像下拉，点「选择企业」弹窗选
  it('手机端：下拉里的「选择企业」打开同一个弹窗', async () => {
    mocks.isPhone = true
    auth.isPlatform = true
    tenant.needPick = true
    const { w } = await mountLayout('/content/projects')

    const item = w.findAll('.el-dropdown-item').find((b: any) => b.text().includes('选择企业'))
    expect(item).toBeTruthy()

    await item!.trigger('click')
    await nextTick()
    expect(w.find('.el-dialog').exists()).toBe(true)
  })

  it('手机端：已选企业时下拉显示「切换企业：企业名」', async () => {
    mocks.isPhone = true
    auth.isPlatform = true
    tenant.needPick = false
    tenant.currentId = 2
    tenant.currentName = '朗汇机电'
    const { w } = await mountLayout('/content/projects')

    const item = w.findAll('.el-dropdown-item').find((b: any) => b.text().includes('切换企业'))
    expect(item?.text()).toContain('朗汇机电')
  })

  // 换人登录不该继承上一个人的视角（平台超管 → 企业管理员时尤其明显）
  it('退出：同时清掉当前企业视角', async () => {
    const { w } = await mountLayout()
    await w.findAll('.el-button').find((b: any) => b.text().includes('退出'))!.trigger('click')
    await flushPromises()

    expect(tenant.clear).toHaveBeenCalled()
  })
})
