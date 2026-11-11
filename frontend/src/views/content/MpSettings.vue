<template>
  <div class="mp-settings">
    <!-- 自有的标签页切换栏：① 复用 el-tabs 的页签切换 / lazy 挂载能力，但隐藏其原生
         header（hidden-header），改用这里的 .tabs-bar 渲染页签，方便把「风格模板」页专属的
         「保存风格 / 重新加载」按钮放在同一行最右侧；
         ② .tabs-bar 在页面滚动时固定顶部（sticky），管理员翻阅 12 张风格卡时仍能随时点
         页签或右侧按钮，不必滚回顶部。tab 同时驱动 el-tabs 的 v-model 与这里的选中态。 -->
    <div class="tabs-bar">
      <div class="tabs" role="tablist">
        <button
          v-for="t in TABS_META"
          :key="t.name"
          type="button"
          class="tab"
          :class="{ active: tab === t.name }"
          role="tab"
          :aria-selected="tab === t.name"
          @click="tab = t.name"
        >{{ t.label }}</button>
      </div>
      <!-- 重新加载 / 保存风格只在「风格模板」页有意义（操作该页的模板清单与选中态），
           所以只在切到该页时显示；切走即随切换栏一起消失，不会出现在不相关的页签上。 -->
      <div v-if="tab === 'theme'" class="tab-actions">
        <el-button
          type="primary"
          :loading="themeSaving"
          :disabled="themeDisabled"
          title="仅提交模板选择，不改动首页背景、板块顺序与详情页时间显示。"
          @click="onSaveTheme"
        >保存风格</el-button>
        <el-button :icon="Refresh" :loading="themeLoading" @click="onReloadTheme">重新加载</el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="hidden-header" @tab-change="syncQuery">
      <el-tab-pane label="访客先看到谁" name="entry" lazy>
        <MpEntry embedded />
      </el-tab-pane>
      <el-tab-pane label="名片页面样式" name="appearance" lazy>
        <HomeLayout embedded />
      </el-tab-pane>
      <!-- 风格模板独立成第三个页签，不塞进「名片页面样式」里当第四块：
           那一页的三块顺序（首页背景 → 板块顺序 → 详情页时间显示）是产品约定，
           HomeLayout.spec.ts 用源码 indexOf 钉死了，插一块就得改约定。
           页内再按服务端下发的 series 分「整体风格」与「品牌配色」两组。 -->
      <el-tab-pane label="风格模板" name="theme" lazy>
        <MpTheme ref="themeRef" embedded />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import MpEntry from './MpEntry.vue'
import HomeLayout from './HomeLayout.vue'
import MpTheme from './MpTheme.vue'

// 小程序端「访客第一眼」的三件事：
//   ① 入口：扫体验版二维码 / 直接打开（裸启动）时，先看到名片墙还是某位员工的名片；
//   ② 样式：名片首页的板块顺序与背景，以及两个详情页是否显示「更新于」；
//   ③ 风格：整套内置风格模板（整体风格 + 品牌配色，按授权开关的增值能力）。
// 三者各自保存（/admin/mp-entry 与 /admin/home-layout 的不同写入子集），所以是页签
// 而不是一个表单；但菜单只留一项——客户配的是「我的小程序长什么样」，不该由他去猜
// 「首页布局」其实也是小程序的事。

const TABS = ['entry', 'appearance', 'theme'] as const
type TabName = (typeof TABS)[number]

const TABS_META: { name: TabName; label: string }[] = [
  { name: 'entry', label: '访客先看到谁' },
  { name: 'appearance', label: '名片页面样式' },
  { name: 'theme', label: '风格模板' },
]

const route = useRoute()
const router = useRouter()

// 页签写进 URL（?tab=appearance）：
// ① 旧路由 /content/home-layout 与 /content/mp-entry 重定向进来时能落到对应页签；
// ② 同事之间照文档说的「小程序设置 → 名片首页样式」发链接，打开就是那一页。
function fromQuery(): TabName {
  const q = String(route.query.tab || '')
  return (TABS as readonly string[]).includes(q) ? (q as TabName) : TABS[0]
}

const tab = ref<TabName>(fromQuery())

function syncQuery() {
  router.replace({ query: { ...route.query, tab: tab.value } })
}

// 「风格模板」页的操作按钮（重新加载 / 保存风格）渲染在父组件的页签切换栏右侧，
// 通过 ref 调用 MpTheme 暴露的 load / save；loading / saving / templates 也来自它，
// 用于驱动按钮的 loading 与 disabled 态。其它页签激活时 MpTheme 未挂载，ref 为 null，
// 此时按钮因 v-if="tab==='theme'" 不渲染，点击不会发生。
const themeRef = ref<InstanceType<typeof MpTheme> | null>(null)
const themeLoading = computed(() => (themeRef.value as any)?.loading ?? false)
const themeSaving = computed(() => (themeRef.value as any)?.saving ?? false)
const themeDisabled = computed(() => ((themeRef.value as any)?.templates?.length ?? 0) === 0)
function onReloadTheme() {
  themeRef.value?.load()
}
function onSaveTheme() {
  themeRef.value?.save()
}
</script>

<style scoped>
/* 不设 max-width：这页和其他内容页一样撑满内容区（早期写死 960px 会让卡片在
   宽屏下缩在左边、右边一大片空白）。
   el-tabs 的 .el-tabs__content 自带 15px padding，会把卡片左右各缩进一格，
   与页面上其他卡片对不齐——这里清掉，只在页签与卡片之间留间距。 */
/* 整体出血到 .admin-main 的 padding 边缘（抵消其 16px padding），让本页紧贴内容区最顶端，
   这样 .tabs-bar 静止时直接贴到顶栏底边。吸顶位置再由 .tabs-bar 的 top:-16px 补偿
   .admin-main 的 padding-top（sticky 的 top 参照是滚动容器「内容区上沿」，比顶栏低 16px），
   确保滚动吸顶时白底也紧贴顶栏底边、上方不留空隙。内部只补左右 / 底部 16px（顶部不给），
   内容与后台其它页面对齐、又不把吸顶栏往下挤。 */
.mp-settings {
  margin: -16px;
  padding: 0 16px 16px;
}
.mp-settings :deep(.el-tabs__content) {
  padding: 0;
}
/* 原生 el-tabs 的 header 隐藏：页签由上面的 .tabs-bar 渲染，避免两套页签并存。 */
.hidden-header :deep(.el-tabs__header) {
  display: none;
}
/* 自有的标签页切换栏：滚动时固定顶部（sticky），按钮与页签在同一行、一起固定。
   白底 + z-index 保证内容滚到下方时被遮住而不是透出来。flex 让页签占满左侧、
   操作按钮推到最右（.tab-actions 用 margin-left:auto）。 */
.tabs-bar {
  position: sticky;
  /* 抵消 .admin-main 的 16px padding-top：sticky 的 top 参照是滚动容器「内容区上沿」
     （比顶栏低 16px），top:0 会让吸顶位比顶栏底边低一截、上方留空隙；
     top 取 -16px 正好补回，吸顶时白底直接贴到顶栏底边。
     再配合 .mp-settings 出血，静止（未滚动）时也贴顶。 */
  top: -16px;
  z-index: 10;
  display: flex;
  /* stretch 让 .tab 撑满整条栏高，这样 .tab.active::after 的 bottom:0 才能贴到
     栏的底边框（而不是悬浮在文字下方），且三种页签下栏高一致。 */
  align-items: stretch;
  margin-bottom: 12px;
  background: #fff;
  border-bottom: 1px solid #ebeef5;
}
.tabs {
  display: flex;
  gap: 28px;
}
.tab {
  appearance: none;
  background: none;
  border: none;
  cursor: pointer;
  /* 上下对称留白：文字在页签里垂直居中，上沿与栏顶、下沿（含 2px 蓝下划线）与栏底等距，
     不再像旧版那样只在下方堆 12px 而上方紧贴。 */
  padding: 12px 0;
  font-size: 14px;
  color: #909399;
  position: relative;
}
.tab:hover {
  color: #606266;
}
.tab.active {
  color: #1a8cff;
  font-weight: 500;
}
.tab.active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 2px;
  background: #1a8cff;
}
.tab-actions {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  padding-left: 16px;
}
</style>
