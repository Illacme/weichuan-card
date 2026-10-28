import { createRouter, createWebHistory } from 'vue-router'
import type { Router } from 'vue-router'
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
    redirect: '/apply/open',
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
        path: 'content/home-layout',
        name: 'home-layout',
        component: () => import('@/views/content/HomeLayout.vue'),
        meta: { title: '首页布局', group: '企业内容' },
      },
      {
        // 小程序入口：裸启动（扫体验版二维码 / 直接打开）落到名片墙还是某个员工的名片。
        // 放在「企业内容」组而不是「系统设置」：后者是 platformOnly，而这一项
        // 本来就该由企业管理员自己决定（他才知道自家是「全员名片」还是「一张名片打天下」）。
        path: 'content/mp-entry',
        name: 'mp-entry',
        component: () => import('@/views/content/MpEntry.vue'),
        meta: { title: '小程序入口', group: '企业内容' },
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
  { path: '/:pathMatch(.*)*', redirect: '/apply/open' },
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
      return { path: '/login' }
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
