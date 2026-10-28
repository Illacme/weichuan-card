<template>
  <div class="mp-settings">
    <!-- 两页签分开而不是上下堆两块：两页各有自己的「保存」，同屏放两个保存按钮
         很容易改完上一段去点下一段的保存（而这两处是不同接口、不同数据）。 -->
    <el-tabs v-model="tab" @tab-change="syncQuery">
      <el-tab-pane label="访客先看到谁" name="entry" lazy>
        <MpEntry embedded />
      </el-tab-pane>
      <el-tab-pane label="名片首页样式" name="appearance" lazy>
        <HomeLayout embedded />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MpEntry from './MpEntry.vue'
import HomeLayout from './HomeLayout.vue'

// 小程序端「访客第一眼」的两件事：
//   ① 入口：扫体验版二维码 / 直接打开（裸启动）时，先看到名片墙还是某位员工的名片；
//   ② 样式：名片首页的板块顺序与背景（品牌化包装）。
// 两者接口不同（/admin/mp-entry 与 /admin/home-layout）、各自保存，所以是页签而不是
// 一个表单；但菜单只留一项——客户配的是「我的小程序长什么样」，不该由他去猜
// 「首页布局」其实也是小程序的事。

const TABS = ['entry', 'appearance'] as const
type TabName = (typeof TABS)[number]

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
</script>

<style scoped>
.mp-settings {
  max-width: 960px;
}
</style>
