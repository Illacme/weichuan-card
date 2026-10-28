<template>
  <el-container class="admin-layout">
    <!-- 窄屏（<1200）侧栏抽屉遮罩：点遮罩或选菜单后自动收起 -->
    <div class="aside-backdrop" :class="{ show: asideOpen }" @click="asideOpen = false" />
    <el-aside width="220px" class="admin-aside" :class="{ open: asideOpen }">
      <div class="brand">
        <img class="brand-logo" src="/logo.png" alt="微传名片" />
        <span class="brand-name">{{ brandName }}</span>
      </div>
      <el-menu
        :default-active="activeMenu"
        router
        class="admin-menu"
        background-color="#1f2d3d"
        text-color="#c0c4cc"
        active-text-color="#ffffff"
      >
        <template v-for="g in menuGroups" :key="g.group">
          <el-sub-menu :index="g.group">
            <template #title>
              <el-icon><component :is="g.icon" /></el-icon>
              <span>{{ g.group }}</span>
            </template>
            <el-menu-item v-for="m in g.items" :key="m.path" :index="m.path">
              {{ m.title }}
            </el-menu-item>
          </el-sub-menu>
        </template>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="admin-header">
        <el-button class="menu-toggle" text aria-label="菜单" @click="asideOpen = !asideOpen">
          <el-icon><Menu /></el-icon>
        </el-button>
        <div class="header-title">{{ currentTitle }}</div>
        <div class="header-right">
          <template v-if="!isPhone">
            <!-- 平台超管：切换当前管理的企业（企业管理员没有这个选择器） -->
            <CompanyPicker v-if="auth.isPlatform" :model-value="tenant.currentId" />
            <span v-if="auth.companyName" class="header-company">
              <el-icon><OfficeBuilding /></el-icon>
              {{ auth.companyName }}
            </span>
            <span class="header-user">
              <el-icon><UserFilled /></el-icon>
              {{ auth.username || '管理员' }}
              <el-tag size="small" :type="auth.isPlatform ? 'warning' : 'info'" effect="plain">
                {{ auth.isPlatform ? '平台超管' : '企业管理员' }}
              </el-tag>
            </span>
            <el-button text @click="showPwd = true">
              <el-icon><Key /></el-icon> 修改密码
            </el-button>
            <el-button text type="danger" @click="onLogout">
              <el-icon><SwitchButton /></el-icon> 退出
            </el-button>
          </template>
          <el-dropdown v-else class="header-menu" @command="onCommand">
            <span class="header-avatar-trigger" aria-label="账户菜单">
              <el-avatar :size="32" class="header-avatar">{{ avatarText }}</el-avatar>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-if="auth.companyName" disabled>{{ auth.companyName }}</el-dropdown-item>
                <!-- 手机端顶栏放不下选择器：收进下拉，点开弹窗选（与内联选择器同一组件） -->
                <el-dropdown-item v-if="auth.isPlatform" command="company">
                  <el-icon><OfficeBuilding /></el-icon>
                  {{ tenant.currentId ? '切换企业：' + tenant.currentName : '选择企业' }}
                </el-dropdown-item>
                <el-dropdown-item command="pwd">
                  <el-icon><Key /></el-icon> 修改密码
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <el-icon><SwitchButton /></el-icon> 退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="admin-main">
        <!--
          平台超管未选企业时的空态：企业内容 / 统计 / 审核 / 员工这些页面一次只能对着
          一家企业看和改。不拦的话后端会兜到 1 号企业（零数据的商用版里可能不存在）或
          直接按「全部企业」出数——前者是**改着 A 家写进 B 家**，后者是把几家数据混一起，
          两种都比「让你先选一家」糟。
        -->
        <div v-if="needPickCompany" class="pick-empty">
          <el-icon class="pick-icon"><OfficeBuilding /></el-icon>
          <p class="pick-title">请先选择要管理的企业</p>
          <p class="pick-desc">
            当前是平台超管身份，可以管理所有企业。这个页面的内容与数据都归属于某一家企业，
            请先在
            <b>右上角选择企业</b>
            ，再查看或编辑。
          </p>
          <el-button type="primary" @click="pickerOpen = true">选择企业</el-button>
        </div>
        <router-view v-else v-slot="{ Component }">
          <!-- key 带上当前企业：切换企业必重建页面组件，让各页的 onMounted 重新取数。
               不重建的话页面仍是上一家的数据，而「保存」按钮却是活的——最坏的一种错。 -->
          <component :is="Component" :key="route.path + '#' + tenant.currentId" />
        </router-view>
      </el-main>
    </el-container>

    <ChangePasswordDialog v-model="showPwd" />

    <!-- 手机端 / 空态入口：与顶栏内联选择器共用 CompanyPicker -->
    <el-dialog v-model="pickerOpen" title="选择要管理的企业" width="420px" append-to-body>
      <CompanyPicker :model-value="tenant.currentId" @change="onPicked" />
      <template #footer>
        <span class="pick-tip">切换后当前页面会按所选企业重新加载。</span>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { UserFilled, Key, SwitchButton, Document, Van, DataLine, OfficeBuilding, School, Menu } from '@element-plus/icons-vue'
import { useAuthStore } from '@/store/auth'
import { useSettingsStore } from '@/store/settings'
import { useTenantStore } from '@/store/tenant'
import { adminLogout, getMe } from '@/api/auth'
import ChangePasswordDialog from '@/components/ChangePasswordDialog.vue'
import CompanyPicker from '@/components/CompanyPicker.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'

const auth = useAuthStore()
const settings = useSettingsStore()
const tenant = useTenantStore()
const route = useRoute()
const router = useRouter()
const showPwd = ref(false)
// 企业选择弹窗（手机端下拉入口 + 空态里的「选择企业」按钮共用）
const pickerOpen = ref(false)
// 窄屏抽屉：<1200 时侧栏离场为抽屉，由汉堡按钮/遮罩/路由切换控制开合
const asideOpen = ref(false)
// 手机端（<768）把顶栏右侧的公司名/用户名/改密/退出收进头像下拉，避免顶栏拥挤
const { isPhone } = useBreakpoint()
const avatarText = computed(() => (auth.username || '管理员').charAt(0))

// 身份 → 租户作用域同步。**必须放在 setup 而不是 onMounted**：子页面的 onMounted
// 先于父组件执行，它们一挂载就发请求；晚一步同步，首屏那几个请求就漏了 ?companyId=，
// 拿回的是「全部企业」或 1 号企业的数据（这正是本功能要消灭的静默串数据）。
tenant.syncIdentity(auth.isPlatform, auth.companyId)

// 页面刷新后恢复身份：令牌在 httpOnly Cookie 中，JS 读不到 claims，
// 用 /api/admin/me 取回「平台超管 / 企业管理员」与当前企业（服务端为准，覆盖本地缓存）。
onMounted(async () => {
  // 品牌名等公开运行参数：先渲染本地缓存值，再静默刷新（失败回落内置默认）
  settings.load()
  if (auth.loggedIn && !auth.profileLoaded) {
    try {
      const resp = await getMe()
      if (resp.code === 0 && resp.data) {
        auth.hydrate(resp.data)
        // 服务端为准再同步一次：localStorage 里的身份可能已过期（被改成企业管理员、
        // 或所属企业被换掉），而它决定要不要注入 companyId
        tenant.syncIdentity(resp.data.isPlatform, resp.data.companyId)
        if (resp.data.isPlatform) tenant.ensureLoaded().catch(() => {})
      }
    } catch {
      // 网络异常时保留本地缓存身份，不阻塞界面
    }
  }
  // 平台超管：预取企业候选，顶栏选择器点开即有内容
  if (auth.isPlatform) tenant.ensureLoaded().catch(() => {})
})

/**
 * 哪些页面「必须选定一家企业」。
 * 平台级页面（企业管理 / 系统设置 / 账号 / 操作日志 / CDN / 内容安全）不在其列——
 * 它们是跨企业的，选不选都能用。
 * `/` 是开通审核（ApplyOpen）的落地路径，同样属于企业数据，别忘了它。
 */
const COMPANY_SCOPED = [/^\/$/, /^\/content\//, /^\/apply\//, /^\/stats$/, /^\/employees$/]

const needPickCompany = computed(
  () => tenant.needPick && COMPANY_SCOPED.some((re) => re.test(route.path))
)

function onPicked() {
  pickerOpen.value = false
}

const allMenuGroups = [
  {
    group: '审核管理',
    icon: Van,
    items: [
      { path: '/apply/open', title: '开通审核' },
      { path: '/apply/modify', title: '名片修改审核' },
      { path: '/employees', title: '员工管理' },
      // 内容安全：图片违规记录与一键下架。企业管理员也要用（管自己企业的图），
      // 所以放在这个非 platformOnly 的组里，而不是「平台管理」。
      { path: '/content-sec', title: '内容安全' },
    ],
  },
  {
    group: '数据统计',
    icon: DataLine,
    items: [{ path: '/stats', title: '名片统计' }],
  },
  {
    group: '企业内容',
    icon: Document,
    items: [
      { path: '/content/company-info', title: '公司信息' },
      { path: '/content/company-profile', title: '公司简介' },
      { path: '/content/categories', title: '业绩分类' },
      { path: '/content/projects', title: '项目案例' },
      { path: '/content/services', title: '业务服务' },
      { path: '/content/home-layout', title: '首页布局' },
    ],
  },
  {
    // 仅平台超级管理员可见：企业管理（开通/停用企业、企业管理员管理）
    // + CDN 刷新（平台级基础设施操作，覆盖同名对象的场景已自动刷新，此处为手动兜底）
    group: '平台管理',
    icon: School,
    platformOnly: true,
    items: [
      { path: '/companies', title: '企业管理' },
      { path: '/platform/cdn-refresh', title: 'CDN 刷新' },
      // 系统设置：运行参数（品牌名 / 上传限制 / 小程序码 TTL / 特性开关）后台维护
      { path: '/platform/settings', title: '系统设置' },
      // 账号管理：平台超管账号改名 / 新增 / 重置密码 / 删除
      { path: '/platform/accounts', title: '账号管理' },
      // 操作日志：后台写操作与登录失败审计（只读）
      { path: '/platform/audit-logs', title: '操作日志' },
    ],
  },
]

// 企业管理员不显示平台管理组
const menuGroups = computed(() =>
  allMenuGroups.filter((g) => !(g as { platformOnly?: boolean }).platformOnly || auth.isPlatform)
)

// 切换页面时把当前菜单项滚进可视区：侧边栏可滚动后（菜单组展开会超出一屏），
// 选中项可能藏在滚动区外（如底部的「企业管理」），这里自动 scrollIntoView。
// nextTick 等 el-menu 的 is-active 类落到 DOM 上之后再定位。
watch(
  () => route.path,
  () => {
    // 窄屏进入新页面时收起抽屉（选完菜单即关）
    asideOpen.value = false
    nextTick(() => {
      const el = document.querySelector('.admin-menu .is-active')
      if (el && typeof el.scrollIntoView === 'function') {
        el.scrollIntoView({ block: 'nearest' })
      }
    })
  }
)

// 侧边栏品牌名：优先「企业简称」（在「企业管理」里设置）；
// 平台超管无归属企业、或企业未设简称时，回落到后台「系统设置 → 通用 → brand_name」
// （未配置再由 store 回落内置默认「微传名片」）——品牌名不再写死在前端代码里。
// 注意：公司信息页里的简称只作为小程序展示值，与本标识没有关联。
const brandName = computed(() => {
  const fallback = settings.displayBrandName
  return auth.isPlatform || !auth.companyName
    ? `${fallback} · 管理后台`
    : `${auth.companyShortName || fallback} · 管理后台`
})

const activeMenu = computed(() => route.path)
const currentTitle = computed(
  () => (route.meta?.title as string) || `${settings.displayBrandName}管理后台`
)

async function onLogout() {
  try {
    await adminLogout()
  } catch {
    // 忽略网络异常，本地照常登出
  }
  auth.logout()
  // 清掉「当前管理的企业」：换人登录不该继承上一个人的视角（尤其平台超管切到企业管理员时）
  tenant.clear()
  router.replace('/login')
}

// 手机端头像下拉指令：修改密码 / 退出 / 切换企业（与桌面端行为一致）
function onCommand(cmd: string) {
  if (cmd === 'pwd') showPwd.value = true
  else if (cmd === 'company') pickerOpen.value = true
  else if (cmd === 'logout') onLogout()
}
</script>

<style scoped>
.admin-layout {
  height: 100vh;
}
.admin-aside {
  background: #1f2d3d;
  /* 侧边栏改为「品牌头固定 + 菜单区独立滚动」：
     菜单组全部展开（企业内容 7 项 + 审核管理 3 项 + 平台管理…）会超出视口高度，
     此前 overflow:hidden 把底部「平台管理」组直接裁掉且无法滚动查看。
     这里让 aside 成为纵向 flex：brand 固定 60px，菜单 flex:1 内部滚动。 */
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.brand {
  flex: none;
  height: 60px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 16px;
  color: #fff;
  font-weight: 600;
  background: #18222e;
}
.brand-logo {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  background: #fff;
  display: block;
  object-fit: cover;
  flex-shrink: 0;
}
.brand-name {
  font-size: 15px;
}
.admin-menu {
  border-right: none;
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
}
/* 深色细滚动条（WebKit）：默认亮色滚动条在深色侧栏上很突兀 */
.admin-menu::-webkit-scrollbar {
  width: 6px;
}
.admin-menu::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.18);
  border-radius: 3px;
}
.admin-menu::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.3);
}
.admin-menu::-webkit-scrollbar-track {
  background: transparent;
}
.admin-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #ebeef5;
}
.header-title {
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.header-user {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #606266;
  margin-right: 8px;
}
.header-company {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #409eff;
  font-weight: 600;
  margin-right: 8px;
  padding-right: 12px;
  border-right: 1px solid #ebeef5;
}
/* 手机端头像下拉触发器 */
.header-avatar-trigger {
  display: inline-flex;
  cursor: pointer;
  outline: none;
}
.header-avatar {
  background: #409eff;
  user-select: none;
}
.admin-main {
  background: #f5f7fa;
  padding: 16px;
}

/* 平台超管未选企业的空态：居中卡片，视觉上等同于「这页暂时没内容」，
   而不是报错——它不是错误，只是还没选上下文。 */
.pick-empty {
  max-width: 460px;
  margin: 12vh auto 0;
  padding: 32px 28px;
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 10px;
  text-align: center;
}
.pick-icon {
  font-size: 40px;
  color: #c0c4cc;
}
.pick-title {
  margin: 12px 0 8px;
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}
.pick-desc {
  margin: 0 0 18px;
  font-size: 13px;
  line-height: 1.8;
  color: #909399;
}
.pick-tip {
  font-size: 12px;
  color: #909399;
}

/* ===== 窄屏响应式：<1200 侧栏变抽屉 =====
   桌面（>=1200）保持原 220px 固定侧栏不变；窄屏下侧栏脱离文档流改为
   左滑抽屉，由 .open 控制显隐，遮罩 .aside-backdrop 覆盖内容区。 */
.menu-toggle {
  display: none;
  margin-right: 8px;
}
@media (max-width: 1199px) {
  .menu-toggle {
    display: inline-flex;
  }
  .admin-aside {
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    z-index: 1001;
    transform: translateX(-100%);
    transition: transform 0.25s ease;
    box-shadow: 2px 0 12px rgba(0, 0, 0, 0.18);
  }
  .admin-aside.open {
    transform: translateX(0);
  }
  .aside-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.35);
    z-index: 1000;
    opacity: 0;
    visibility: hidden;
    transition: opacity 0.25s ease;
  }
  .aside-backdrop.show {
    opacity: 1;
    visibility: visible;
  }
}
@media (min-width: 1200px) {
  .aside-backdrop {
    display: none;
  }
}
</style>
