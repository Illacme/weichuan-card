import { createRouter, createWebHistory } from 'vue-router'
import type { Router, RouteLocation } from 'vue-router'
import { useAuthStore } from '@/store/auth'

export const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/Login.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    component: () => import('@/layout/AdminLayout.vue'),
    // 保留白标码：/?company=xxx 命中后仍能以 /login?company=xxx 呈现该企业品牌。
    // 根路径必须先落 /apply/open（未登录再由守卫踢到 /login）；若 redirect 写成字符串，
    // query 会在这一跳被丢掉，白标链接一分享就"变回总品牌"。
    redirect: (to: RouteLocation) => ({
      path: '/apply/open',
      query: typeof to.query.company === 'string' ? { company: to.query.company } : {},
    }),
    children: [
      {
        path: 'apply/open',
        name: 'apply-open',
        component: () => import('@/views/ApplyOpen.vue'),
        meta: { title: '开通审核', group: '审核管理' },
      },
      {
        path: 'apply/modify',
        name: 'apply-modify',
        component: () => import('@/views/ApplyModify.vue'),
        meta: { title: '名片修改审核', group: '审核管理' },
      },
      {
        path: 'employees',
        name: 'employees',
        component: () => import('@/views/Employees.vue'),
        meta: { title: '员工管理', group: '审核管理' },
      },
      {
        // 内容安全：图片违规的检测记录与处置（异步检测 → 消息推送回调 → 人工下架）。
        // 放在「审核管理」组而不是「平台管理」：企业管理员也要处置自己企业的图，
        // 而平台管理组是 platformOnly（企业管理员看不到）。
        path: 'content-sec',
        name: 'content-sec',
        component: () => import('@/views/platform/ContentSec.vue'),
        meta: { title: '内容安全', group: '审核管理' },
      },
      {
        path: 'stats',
        name: 'stats',
        component: () => import('@/views/Stats.vue'),
        meta: { title: '名片统计', group: '数据统计' },
      },
      {
        path: 'content/company-info',
        name: 'company-info',
        component: () => import('@/views/content/CompanyInfo.vue'),
        meta: { title: '公司信息', group: '企业内容' },
      },
      {
        path: 'content/company-profile',
        name: 'company-profile',
        component: () => import('@/views/content/CompanyProfile.vue'),
        meta: { title: '公司简介', group: '企业内容' },
      },
      {
        path: 'content/categories',
        name: 'categories',
        component: () => import('@/views/content/Categories.vue'),
        meta: { title: '业绩分类', group: '企业内容' },
      },
      {
        path: 'content/projects',
        name: 'projects',
        component: () => import('@/views/content/Projects.vue'),
        meta: { title: '项目案例', group: '企业内容' },
      },
      {
        path: 'content/services',
        name: 'services',
        component: () => import('@/views/content/Services.vue'),
        meta: { title: '业务服务', group: '企业内容' },
      },
      {
        // 小程序端的「访客第一眼」：入口（先落到哪一页）+ 首页样式（那一页怎么排）。
        // 两者是不同接口、不同保存动作，但同属一个心智模型——企业管理员配的是
        // 「客户打开小程序看到什么」，拆成两个菜单项时客户要自己去猜哪项影响哪里。
        path: 'content/mp-settings',
        name: 'mp-settings',
        component: () => import('@/views/content/MpSettings.vue'),
        meta: { title: '小程序设置', group: '企业内容' },
      },
      // 旧路径保留重定向：文档、收藏、聊天记录里的链接不能失效。
      // 不带 meta.group —— 它们不在侧边栏，加了会让「路由↔菜单」守门用例误判。
      {
        path: 'content/home-layout',
        name: 'home-layout',
        redirect: { path: '/content/mp-settings', query: { tab: 'appearance' } },
      },
      {
        path: 'content/mp-entry',
        name: 'mp-entry',
        redirect: { path: '/content/mp-settings', query: { tab: 'entry' } },
      },
      {
        // 企业管理（多租户）：仅平台超级管理员可见可访问
        path: 'companies',
        name: 'companies',
        component: () => import('@/views/Companies.vue'),
        meta: { title: '企业管理', group: '平台管理', platformOnly: true },
      },
      {
        // CDN 刷新：平台级基础设施操作（覆盖同名对象的场景已自动刷新，此处为手动兜底），
        // 仅平台超级管理员可见可访问
        path: 'platform/cdn-refresh',
        name: 'cdn-refresh',
        component: () => import('@/views/platform/CdnRefresh.vue'),
        meta: { title: 'CDN 刷新', group: '平台管理', platformOnly: true },
      },
      {
        // 系统设置：把产品运行参数（品牌名 / 上传限制 / 小程序码 TTL / 特性开关等）
        // 从「代码写死」收敛为后台可维护项，保存即时生效、无需重启。
        // 密钥类仍留环境变量，仅平台超级管理员可见可访问
        path: 'platform/settings',
        name: 'settings',
        component: () => import('@/views/platform/Settings.vue'),
        meta: { title: '系统设置', group: '平台管理', platformOnly: true },
      },
      {
        // 账号管理：平台超管账号的改名 / 新增 / 重置密码 / 删除。
        // 初始超管由后端按 ADMIN_USER 一次性 seed，此前没有改用户名的入口。
        path: 'platform/accounts',
        name: 'accounts',
        component: () => import('@/views/platform/Accounts.vue'),
        meta: { title: '账号管理', group: '平台管理', platformOnly: true },
      },
      {
        // 操作日志：后台写操作与登录失败留痕（只读查询）。
        path: 'platform/audit-logs',
        name: 'audit-logs',
        component: () => import('@/views/platform/AuditLogs.vue'),
        meta: { title: '操作日志', group: '平台管理', platformOnly: true },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    // 未知路径同样保留白标码，避免分享链接拼错一段就丢掉品牌。
    redirect: (to: RouteLocation) => ({
      path: '/apply/open',
      query: typeof to.query.company === 'string' ? { company: to.query.company } : {},
    }),
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

// registerGuards 挂路由守卫，抽成函数是为了单测能用内存 history + 桩组件
// 单独验证门禁（不必真的加载每一个页面组件，也不依赖浏览器 history）。
// 门禁是后台的第一道防线：未登录 / 越权访问必须在前端就被挡住
// （后端还有 AuthMiddleware / PlatformAdminMiddleware 兜底，双保险）。
export function registerGuards(r: Router) {
  r.beforeEach((to) => {
    const auth = useAuthStore()
    if (!to.meta.public && !auth.loggedIn) {
      // 保留白标码（?company=xxx）：用户从任意入口（根路径 / 未知路径 / 受保护页）进来，
      // 落到登录页时品牌仍应属于该企业；否则白标链接刷新一次就退回总品牌。
      const company = typeof to.query.company === 'string' ? to.query.company : undefined
      return { path: '/login', query: company ? { company } : {} }
    }
    if (to.path === '/login' && auth.loggedIn) {
      return { path: '/' }
    }
    if (to.meta.platformOnly && !auth.isPlatform) {
      return { path: '/apply/open' }
    }
  })
}

registerGuards(router)

export default router
