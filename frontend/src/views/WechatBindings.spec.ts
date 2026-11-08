import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, provide, inject, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import WechatBindings from './WechatBindings.vue'
import { useAuthStore } from '@/store/auth'

// 微信绑定「可维护」层（P1）前端守门。
//
// 重点不是把表格渲染出来，而是四条边界：
//   - 后台只能解绑、绝不代绑（api 层没有 bind 函数，页面没有绑定入口）；
//   - 解绑的确认文案必须说清「会踢掉已签发令牌」，self=true 必须登出重登；
//   - openid 只进不出：完整值只出现在 lookup 请求体，回包与界面只有掩码；
//   - 反查是平台超管专属，企业管理员连入口都不该看到。
//
// 网络与 UI 反馈层全部 mock，分页用真实 useServerTable（顺带覆盖筛选联动）。
const mocks = vi.hoisted(() => ({
  listWechatBindings: vi.fn(async (): Promise<any> => ({
    list: [
      { kind: 'admin', id: 2, name: 'entboss', companyId: 1, companyName: '测试企业', wechatBound: true, wxOpenidMasked: 'o****7890', createdAt: '2026-01-01 10:00:00' },
      { kind: 'employee', id: 5, name: '张三', companyId: 1, companyName: '测试企业', wechatBound: true, wxOpenidMasked: 'o****cdef', createdAt: '2026-01-02 10:00:00' },
    ],
    total: 2,
    page: 1,
    size: 20,
  })),
  lookupWechatBindings: vi.fn(async (): Promise<any> => ({
    code: 0,
    data: [
      { kind: 'employee', id: 5, name: '张三', companyId: 1, companyName: '测试企业', wechatBound: true, wxOpenidMasked: 'o****cdef', createdAt: '2026-01-02 10:00:00' },
    ],
    total: 1,
  })),
  unbindWechat: vi.fn(async (): Promise<any> => ({ code: 0, self: false })),
}))

vi.mock('@/api/wechatBindings', () => mocks)
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  ElMessageBox: { confirm: vi.fn(async () => 'confirm') },
}))
vi.mock('@element-plus/icons-vue', () => ({ Refresh: {}, Search: {} }))

import { ElMessage, ElMessageBox } from 'element-plus'

// ── 轻量 stub（jsdom 下完整 Element Plus 过重易 OOM）──
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'description', 'type', 'closable', 'showIcon'],
  template: `<div class="el-alert">{{ title }}{{ description }}<slot /></div>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable', 'autocomplete'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'icon', 'link'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElSelect = defineComponent({
  name: 'ElSelect',
  props: ['modelValue', 'placeholder'],
  emits: ['update:modelValue'],
  template: `<select class="el-select" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)"><slot /></select>`,
})
const ElOption = defineComponent({
  name: 'ElOption',
  props: ['label', 'value'],
  template: `<option :value="value">{{ label }}</option>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'size', 'effect'],
  template: `<span class="el-tag"><slot /></span>`,
})
const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  template: `<div v-if="modelValue" class="el-dialog"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>`,
})
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  emits: ['update:currentPage', 'update:pageSize'],
  template: `<div class="el-pagination" />`,
})

// 复刻 Element Plus「列槽拿 { row }」：ElTable 逐行 provide，ElTableColumn inject 后转发。
const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', props.row)
    return () => h('div', { class: 'el-table-row' }, slots.default ? slots.default() : [])
  },
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  setup(props, { slots }) {
    return () =>
      h(
        'div',
        { class: 'el-table' },
        (props.data || []).map((row: any, i: number) =>
          h(TableRow, { key: i, row }, () => (slots.default ? slots.default() : null))
        )
      )
  },
})
const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  props: ['prop', 'label', 'width', 'align', 'fixed', 'minWidth', 'showOverflowTooltip'],
  setup(props, { slots }) {
    const row = inject('tableRow', null) as any
    return () => {
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})

const stubs = {
  'el-card': ElCard,
  'el-alert': ElAlert,
  'el-input': ElInput,
  'el-button': ElButton,
  'el-select': ElSelect,
  'el-option': ElOption,
  'el-tag': ElTag,
  'el-dialog': ElDialog,
  'el-pagination': ElPagination,
  'el-table': ElTable,
  'el-table-column': ElTableColumn,
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/wechat-bindings', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
    ],
  })
}

function mountPage(isPlatform = true) {
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.isPlatform = isPlatform
  auth.loggedIn = true // 让「解绑自己 → 登出」的断言真的能观察到状态变化
  const router = makeRouter()
  const w = mount(WechatBindings, {
    global: { plugins: [router], stubs, directives: { loading: {} } },
  })
  return { w, router, auth }
}

async function flush(w: ReturnType<typeof mount>) {
  await nextTick()
  await nextTick()
  await nextTick()
  // 等到行真的渲染出来（或空态出现），否则后面断言的是首屏 loading 中的空表格
  await vi.waitFor(() =>
    expect(w.text()).toMatch(/entboss|还没有账号绑定微信/)
  )
}

function unbindButton(w: ReturnType<typeof mount>) {
  return w.findAll('button.el-button').find((b: any) => b.text().includes('解绑'))!
}

describe('WechatBindings · 清单与筛选', () => {
  beforeEach(() => {
    vi.mocked(mocks.listWechatBindings).mockClear()
    vi.mocked(mocks.lookupWechatBindings).mockClear()
    vi.mocked(mocks.unbindWechat).mockClear()
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessage.warning).mockClear()
    vi.mocked(ElMessageBox.confirm).mockClear()
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => 'confirm' as any)
    vi.mocked(mocks.unbindWechat).mockResolvedValue({ code: 0, self: false } as never)
  })

  it('清单渲染两类账号 + 企业 + 掩码，且每列都写了 prop', async () => {
    const { w } = mountPage()
    await flush(w)
    expect(w.findAll('.el-table-row')).toHaveLength(2)
    const text = w.text()
    expect(text).toContain('管理员')
    expect(text).toContain('员工')
    expect(text).toContain('entboss')
    expect(text).toContain('张三')
    expect(text).toContain('测试企业')
    expect(text).toContain('o****7890')
    expect(text).toContain('o****cdef')
    const cells = w.findAll('.wx-bind-cell')
    expect(cells).toHaveLength(2)
    expect(cells[0].attributes('data-bound')).toBe('true')
  })

  it('首屏按服务端分页参数拉清单（后端负责切片）', async () => {
    const { w } = mountPage()
    await flush(w)
    expect(mocks.listWechatBindings).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, size: 20, keyword: '' })
    )
  })

  it('边界提示写清「只能解绑、不能代绑」与「会踢掉令牌」', async () => {
    const { w } = mountPage()
    await flush(w)
    const alert = w.find('.el-alert').text()
    expect(alert).toContain('只能解绑，不能代绑')
    expect(alert).toContain('登录令牌立即失效')
  })

  it('切换账号类型时带上 kind 重新拉清单（服务端在分页前过滤）', async () => {
    const { w } = mountPage()
    await flush(w)
    await w.find('select.el-select').setValue('admin')
    await nextTick()
    await vi.waitFor(() =>
      expect(mocks.listWechatBindings).toHaveBeenLastCalledWith(
        expect.objectContaining({ kind: 'admin' })
      )
    )
  })

  it('空清单给出「由本人到小程序绑定」的明确出路', async () => {
    vi.mocked(mocks.listWechatBindings).mockResolvedValueOnce({ list: [], total: 0, page: 1, size: 20 } as never)
    const { w } = mountPage()
    await flush(w)
    expect(w.text()).toContain('还没有账号绑定微信')
    expect(w.text()).toContain('后台不代绑')
  })
})

describe('WechatBindings · 解绑', () => {
  beforeEach(() => {
    vi.mocked(mocks.listWechatBindings).mockClear()
    vi.mocked(mocks.lookupWechatBindings).mockClear()
    vi.mocked(mocks.unbindWechat).mockClear()
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessageBox.confirm).mockClear()
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => 'confirm' as any)
    vi.mocked(mocks.unbindWechat).mockResolvedValue({ code: 0, self: false } as never)
  })

  it('二次确认把免密入口失效 / 令牌失效 / 不能代绑三个后果说清', async () => {
    const { w } = mountPage()
    await flush(w)
    await unbindButton(w).trigger('click')
    await nextTick()
    expect(ElMessageBox.confirm).toHaveBeenCalled()
    const msg = String(vi.mocked(ElMessageBox.confirm).mock.calls[0][0])
    expect(msg).toContain('不能再免密进入')
    expect(msg).toContain('登录令牌立即失效')
    expect(msg).toContain('后台无法代绑')
  })

  it('取消确认不发解绑请求', async () => {
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce(new Error('cancel') as never)
    const { w } = mountPage()
    await flush(w)
    await unbindButton(w).trigger('click')
    await nextTick()
    expect(mocks.unbindWechat).not.toHaveBeenCalled()
  })

  it('确认后用 kind + id 定位账号，成功后重载清单', async () => {
    const { w } = mountPage()
    await flush(w)
    const before = vi.mocked(mocks.listWechatBindings).mock.calls.length
    await unbindButton(w).trigger('click')
    await nextTick()
    await vi.waitFor(() => expect(mocks.unbindWechat).toHaveBeenCalledWith('admin', 2))
    await vi.waitFor(() =>
      expect(vi.mocked(mocks.listWechatBindings).mock.calls.length).toBeGreaterThan(before)
    )
    expect(ElMessage.success).toHaveBeenCalledWith('已解绑')
  })

  it('解绑自己（self=true）→ 清登录态并跳登录页，不再发 logout 请求', async () => {
    vi.mocked(mocks.unbindWechat).mockResolvedValue({ code: 0, self: true } as never)
    const { w, router, auth } = mountPage()
    await flush(w)
    await unbindButton(w).trigger('click')
    await nextTick()
    await vi.waitFor(() => expect(auth.loggedIn).toBe(false))
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/login'))
    expect(ElMessage.success).toHaveBeenCalledWith('已解绑当前账号，请重新登录')
  })
})

describe('WechatBindings · 按 openid 反查（平台超管专属）', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.mocked(mocks.listWechatBindings).mockClear()
    vi.mocked(mocks.lookupWechatBindings).mockClear()
    vi.mocked(mocks.unbindWechat).mockClear()
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessage.warning).mockClear()
    vi.mocked(ElMessageBox.confirm).mockClear()
    vi.mocked(ElMessageBox.confirm).mockImplementation(async () => 'confirm' as any)
    vi.mocked(mocks.unbindWechat).mockResolvedValue({ code: 0, self: false } as never)
  })

  it('企业管理员看不到反查入口（后端是 403，前端不该先让人撞上去）', async () => {
    const { w } = mountPage(false)
    await flush(w)
    expect(w.text()).not.toContain('按 openid 反查')
    expect(w.find('input[placeholder="粘贴完整的 openid"]').exists()).toBe(false)
  })

  it('空 openid 不发请求，先提示', async () => {
    const { w } = mountPage()
    await flush(w)
    await w.findAll('button.el-button').find((b: any) => b.text().includes('按 openid 反查'))!.trigger('click')
    await nextTick()
    await w.findAll('button.el-button').find((b: any) => b.text() === '查询')!.trigger('click')
    await nextTick()
    expect(mocks.lookupWechatBindings).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalledWith('请填写完整的 openid')
  })

  it('完整 openid 只进请求体；结果只显示掩码，界面不回显原文且输入框清空', async () => {
    const full = 'oFULL-emp-openid-abcdef'
    const { w } = mountPage()
    await flush(w)
    await w.findAll('button.el-button').find((b: any) => b.text().includes('按 openid 反查'))!.trigger('click')
    await nextTick()
    await w.find('input[placeholder="粘贴完整的 openid"]').setValue(full)
    await w.findAll('button.el-button').find((b: any) => b.text() === '查询')!.trigger('click')
    await vi.waitFor(() => expect(mocks.lookupWechatBindings).toHaveBeenCalledWith(full))
    await nextTick()
    const row = w.find('.lookup-row')
    expect(row.exists()).toBe(true)
    expect(row.attributes('data-masked')).toBe('o****cdef')
    expect(row.attributes('data-name')).toBe('张三')
    expect(w.text()).not.toContain(full)
    expect((w.find('input[placeholder="粘贴完整的 openid"]').element as HTMLInputElement).value).toBe('')
  })

  it('反查结果里直接解绑：该行就地移除，不重新发一次 lookup', async () => {
    const { w } = mountPage()
    await flush(w)
    await w.findAll('button.el-button').find((b: any) => b.text().includes('按 openid 反查'))!.trigger('click')
    await nextTick()
    await w.find('input[placeholder="粘贴完整的 openid"]').setValue('oFULL-emp-openid-abcdef')
    await w.findAll('button.el-button').find((b: any) => b.text() === '查询')!.trigger('click')
    await vi.waitFor(() => expect(mocks.lookupWechatBindings).toHaveBeenCalledTimes(1))
    await nextTick()

    const rowUnbind = w.find('.lookup-row').find('button.el-button')
    await rowUnbind.trigger('click')
    await vi.waitFor(() => expect(mocks.unbindWechat).toHaveBeenCalledWith('employee', 5))
    await vi.waitFor(() => expect(w.find('.lookup-row').exists()).toBe(false))
    expect(mocks.lookupWechatBindings).toHaveBeenCalledTimes(1)
  })
})

// 静态守门：页面层用读源码而非给每个分支造全套 mount。
// 纯字符串断言会被注释骗过，所以先剥注释。
function code(path: string): string {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

describe('WechatBindings · 安全底线（静态守门）', () => {
  it('列表每一列都写了 prop（漏写时 elStubs 的 data-prop 断言恒 0 = 假绿）', () => {
    const src = code('src/views/WechatBindings.vue')
    for (const p of ['prop="kind"', 'prop="name"', 'prop="companyName"', 'prop="wechatBound"', 'prop="createdAt"']) {
      expect(src, `缺少 ${p}`).toContain(p)
    }
  })

  it('api 层没有代绑函数：绑定只能本人在小程序完成', () => {
    const src = code('src/api/wechatBindings.ts')
    expect(src).toContain("'/admin/wechat/unbind'")
    expect(src).not.toContain("'/admin/wechat/bind'")
    expect(src).not.toContain("'/api/admin/wechat/bind'")
    expect(src).toContain("'/admin/wechat/lookup'")
  })

  it('页面不把完整 openid 写进本地存储，也没有任何绑定端点', () => {
    const src = code('src/views/WechatBindings.vue')
    expect(src).not.toContain('localStorage')
    expect(src).not.toContain('sessionStorage')
    expect(src).not.toContain('wechat/bind')
    // 解绑确认后才发请求，且只有 unbindWechat 一个写操作
    expect(src).toContain('await ElMessageBox.confirm(')
    expect(src).toContain('await unbindWechat(')
  })

  it('反查入口按平台超管身份收窄，路由不误标 platformOnly', () => {
    const page = code('src/views/WechatBindings.vue')
    expect(page).toContain('v-if="auth.isPlatform"')
    const routerSrc = code('src/router/index.ts')
    const start = routerSrc.indexOf("path: 'wechat-bindings'")
    // 只截这一条路由记录（到下一条路由的 path 为止），别把后面企业管理的 platformOnly 算进来
    const block = routerSrc.slice(start, routerSrc.indexOf("path: 'content-sec'", start))
    expect(block).not.toContain('platformOnly')
    expect(block).toContain("group: '审核管理'")
    const layout = code('src/layout/AdminLayout.vue')
    expect(layout).toContain("path: '/wechat-bindings'")
  })

  it('后端三个端点挂在 RegisterAdmins 内部，避免三处装配漏改静默 404', () => {
    const admins = code('../backend/internal/handler/admins.go')
    expect(admins).toContain('registerWechatAdmin(r, db)')
    const impl = code('../backend/internal/handler/wechat_admin.go')
    for (const p of ['/api/admin/wechat/bindings', '/api/admin/wechat/lookup', '/api/admin/wechat/unbind']) {
      expect(impl).toContain(p)
    }
    expect(impl).toContain('token_version')
  })
})
