import { describe, it, expect, beforeEach } from 'vitest'
import { createRouter, createMemoryHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import { routes, registerGuards } from '@/router/index'

// 路由守卫是后台的第一道门：未登录不得进、企业管理员不得进平台级页面。
// 后端还有中间件兜底，但「能点进去再被 403」和「压根进不去」是两种体验，
// 所以这里把每一条门禁都钉死。
//
// 做法：用真实 routes（保留 meta）+ 桩组件 + 内存 history 组装一个测试路由，
// 再把生产同一个 registerGuards 挂上去——测的就是线上那段逻辑，不是复制品。

const stub = { template: '<div />' }

function stubComponents(list: RouteRecordRaw[]): RouteRecordRaw[] {
  return list.map((r) => ({
    ...r,
    component: stub,
    children: r.children ? stubComponents(r.children as RouteRecordRaw[]) : undefined,
  })) as RouteRecordRaw[]
}

function makeRouter() {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: stubComponents(routes as unknown as RouteRecordRaw[]),
  })
  registerGuards(r)
  return r
}

// 登录态按 localStorage 初始化（store 的 state 直接读它）
function setSession(opt: { loggedIn: boolean; isPlatform?: boolean; companyId?: number }) {
  localStorage.clear()
  if (opt.loggedIn) {
    localStorage.setItem('admin_loggedin', '1')
    localStorage.setItem('admin_user', 'tester')
    localStorage.setItem('admin_is_platform', opt.isPlatform ? '1' : '0')
    localStorage.setItem('admin_company_id', String(opt.companyId ?? 0))
  }
}

async function goto(r: ReturnType<typeof makeRouter>, path: string) {
  await r.push(path)
  return r.currentRoute.value.path
}

describe('路由守卫', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('未登录访问后台页面 → 跳登录页', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    expect(await goto(r, '/employees')).toBe('/login')
    expect(await goto(r, '/content/categories')).toBe('/login')
  })

  it('未登录访问登录页 → 放行（否则永远登不进去）', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    expect(await goto(r, '/login')).toBe('/login')
  })

  it('已登录访问登录页 → 回后台首页（/ 再重定向到默认业务页）', async () => {
    setSession({ loggedIn: true, isPlatform: true })
    const r = makeRouter()
    expect(await goto(r, '/login')).toBe('/apply/open')
  })

  it('已登录访问普通页面 → 放行', async () => {
    setSession({ loggedIn: true, isPlatform: false, companyId: 5 })
    const r = makeRouter()
    expect(await goto(r, '/employees')).toBe('/employees')
    expect(await goto(r, '/content/projects')).toBe('/content/projects')
  })

  it('企业管理员访问平台级页面 → 挡回审核页', async () => {
    setSession({ loggedIn: true, isPlatform: false, companyId: 5 })
    const r = makeRouter()
    for (const p of ['/companies', '/platform/settings', '/platform/accounts', '/platform/audit-logs', '/platform/cdn-refresh']) {
      expect(await goto(r, p)).toBe('/apply/open')
    }
  })

  it('平台超管访问平台级页面 → 放行', async () => {
    setSession({ loggedIn: true, isPlatform: true, companyId: 0 })
    const r = makeRouter()
    expect(await goto(r, '/companies')).toBe('/companies')
    expect(await goto(r, '/platform/settings')).toBe('/platform/settings')
  })

  it('未知路径 → 落到默认页而不是白屏', async () => {
    setSession({ loggedIn: true, isPlatform: true })
    const r = makeRouter()
    expect(await goto(r, '/no/such/page')).toBe('/apply/open')
  })

  it('退出登录后立刻重新生效（store 状态与门禁同步）', async () => {
    setSession({ loggedIn: true, isPlatform: true })
    const r = makeRouter()
    expect(await goto(r, '/companies')).toBe('/companies')

    const { useAuthStore } = await import('@/store/auth')
    useAuthStore().logout()
    // 换一个路径 push：跳到当前所在路由时 vue-router 视为重复导航、不会再跑守卫
    expect(await goto(r, '/employees')).toBe('/login')
  })
})

// 白标码透传：?company=xxx 是「登录页白标」（只改品牌，不切租户）。
// 运营分享的可能是「根路径+白标码」，也可能在任意链接后拼白标码；无论从哪个入口
// 进到 /login 都必须保留它，否则刷新/跳转一次品牌就退回总品牌（客户会以为进错后台）。
describe('白标码 ?company= 透传', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('未登录访问根路径 ?company= → 落到登录页且保留 company', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    await r.push('/?company=langhui')
    expect(r.currentRoute.value.path).toBe('/login')
    expect(r.currentRoute.value.query.company).toBe('langhui')
  })

  it('未登录访问受保护页 ?company= → 登录页保留 company', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    await r.push('/employees?company=langhui')
    expect(r.currentRoute.value.path).toBe('/login')
    expect(r.currentRoute.value.query.company).toBe('langhui')
  })

  it('未知路径 ?company= → 登录页保留 company（兜底路由也不丢）', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    await r.push('/no/such/page?company=langhui')
    expect(r.currentRoute.value.path).toBe('/login')
    expect(r.currentRoute.value.query.company).toBe('langhui')
  })

  it('直接访问登录页 ?company= → 原样放行', async () => {
    setSession({ loggedIn: false })
    const r = makeRouter()
    await r.push('/login?company=langhui')
    expect(r.currentRoute.value.path).toBe('/login')
    expect(r.currentRoute.value.query.company).toBe('langhui')
  })

  it('已登录访问根路径 ?company= → 进默认业务页（company 不影响门禁）', async () => {
    setSession({ loggedIn: true, isPlatform: true })
    const r = makeRouter()
    await r.push('/?company=langhui')
    expect(r.currentRoute.value.path).toBe('/apply/open')
  })
})

// 路由表本身也要守：新增平台级页面时忘了标 platformOnly，
// 企业管理员就会在侧边栏看到并点进去（前端放行、后端才拦，体验割裂）。
describe('路由表 meta', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('平台管理分组的页面全部标了 platformOnly', () => {
    const flat: RouteRecordRaw[] = []
    const walk = (list: any[]) => {
      for (const r of list) {
        flat.push(r)
        if (r.children) walk(r.children)
      }
    }
    walk(routes as any[])
    const platformPages = flat.filter((r) => r.meta && r.meta.group === '平台管理')
    expect(platformPages.length).toBeGreaterThan(0)
    for (const p of platformPages) {
      expect(p.meta!.platformOnly, `${String(p.path)} 缺 platformOnly`).toBe(true)
    }
  })

  it('登录页标记为公开，其余页面都不公开', () => {
    const flat: RouteRecordRaw[] = []
    const walk = (list: any[]) => {
      for (const r of list) {
        flat.push(r)
        if (r.children) walk(r.children)
      }
    }
    walk(routes as any[])
    const withPath = flat.filter((r) => typeof r.path === 'string' && r.path)
    expect(withPath.filter((r) => r.meta?.public).map((r) => r.path)).toEqual(['/login'])
  })

  it('每个业务页都有标题（侧边栏与面包屑靠它）', () => {
    const missing: string[] = []
    const walk = (list: any[]) => {
      for (const r of list) {
        // 只校验业务页（有分组的才是侧边栏里的页面）；'/' 是布局壳、pathMatch 是兜底路由
        const p = String(r.path || '')
        if (p && p !== '/' && !p.includes('pathMatch') && r.meta?.group && !r.meta?.title) {
          missing.push(p)
        }
        if (r.children) walk(r.children)
      }
    }
    walk(routes as any[])
    expect(missing).toEqual([])
  })
})
