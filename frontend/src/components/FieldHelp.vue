<template>
  <!-- 帮助标识固定放在配置项名称的右侧：看到名称就知道「这项有说明可查」。
     Element Plus 的 tooltip trigger 没有 manual，用空数组关掉自带触发，
     由 mouseenter / focus / click 自己控 visible：停留看一眼，点击钉住方便细读。 -->
  <el-tooltip
    effect="light"
    placement="top"
    :trigger="[]"
    popper-class="field-help-popper"
    :visible="visible"
    @update:visible="visible = $event"
  >
    <template #content>
      <div class="fh-pop">
        <div class="fh-title">{{ title }}</div>
        <div class="fh-row">
          <span class="fh-key fh-key-usage">功能</span>
          <span class="fh-text">{{ usage }}</span>
        </div>
        <div v-if="source" class="fh-row">
          <span class="fh-key fh-key-source">值来源</span>
          <span class="fh-text">{{ source }}</span>
        </div>
        <div v-if="rules" class="fh-row">
          <span class="fh-key fh-key-rules">规则限制</span>
          <span class="fh-text">{{ rules }}</span>
        </div>
        <div v-if="note" class="fh-row">
          <span class="fh-key fh-key-note">注意</span>
          <span class="fh-text">{{ note }}</span>
        </div>
      </div>
    </template>
    <span
      ref="root"
      class="field-help"
      tabindex="0"
      role="button"
      :aria-label="`查看${title}配置说明`"
      @mouseenter="onEnter"
      @mouseleave="onLeave"
      @focus="onEnter"
      @blur="onLeave"
      @click.prevent.stop="toggle"
      @keydown.enter.prevent.stop="toggle"
      @keydown.space.prevent.stop="toggle"
    >?</span>
  </el-tooltip>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

defineProps<{
  /** 配置项名称，作为弹层标题与 aria-label */
  title: string
  /** 这项是干什么的：先用日常话讲，再补技术名 */
  usage: string
  /** 值从哪里来：去哪个后台、点哪几步 */
  source?: string
  /** 什么情况下才能生效、有什么填法限制 */
  rules?: string
  /** 容易踩的坑 / 出问题先看哪里 */
  note?: string
}>()

const visible = ref(false)
const pinned = ref(false)
const root = ref<HTMLElement | null>(null)

function onEnter() {
  visible.value = true
}
function onLeave() {
  if (!pinned.value) visible.value = false
}
function toggle() {
  pinned.value = !pinned.value
  visible.value = pinned.value
}
function onDocumentClick(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) {
    pinned.value = false
    visible.value = false
  }
}
onMounted(() => document.addEventListener('click', onDocumentClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocumentClick))
</script>

<style scoped>
.field-help {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 5px;
  border-radius: 50%;
  background: #d5d9e0;
  color: #606266;
  font-size: 11px;
  line-height: 1;
  font-weight: 700;
  cursor: help;
  user-select: none;
  vertical-align: middle;
  transition: background 0.15s ease, color 0.15s ease;
}
.field-help:hover,
.field-help:focus,
.field-help:focus-visible {
  background: #409eff;
  color: #fff;
  outline: none;
}
</style>

<!-- 弹层挂在 body 下，scoped 样式够不到，必须单独写非 scoped 块。
     四段颜色分工：蓝色=这项干什么，绿色=去哪取值，橙色=填法限制，红色=坑。
     长文按段染色 + 固定行距，比一大段灰字好扫得多。 -->
<style>
/* 之前没设 effect，Element Plus 默认给的是深色 popper，
   而我们正文用的是深色字 —— 深底深字正是「浮窗看不清」的根因。
   effect="light" 加上这里显式兜底，保证白底深字 + 足够字号。 */
.field-help-popper.el-popper {
  max-width: 420px;
  padding: 12px 14px;
  background: #fff !important;
  color: #303133 !important;
  border: 1px solid #e4e7ed;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.14);
}
.field-help-popper.el-popper .el-popper__arrow::before {
  background: #fff !important;
  border-color: #e4e7ed !important;
}
.fh-pop {
  font-size: 13px;
  line-height: 1.75;
  color: #303133;
}
.fh-title {
  margin-bottom: 6px;
  padding-bottom: 5px;
  border-bottom: 1px solid #ebeef5;
  font-size: 13px;
  font-weight: 600;
  color: #303133;
}
.fh-row {
  display: flex;
  gap: 8px;
  margin-top: 5px;
  align-items: flex-start;
}
.fh-key {
  flex: 0 0 58px;
  text-align: center;
  border-radius: 3px;
  font-size: 12px;
  font-weight: 600;
  line-height: 20px;
}
.fh-key-usage {
  color: #409eff;
  background: #ecf5ff;
}
.fh-key-source {
  color: #67a34a;
  background: #f0f9eb;
}
.fh-key-rules {
  color: #b88230;
  background: #fdf6ec;
}
.fh-key-note {
  color: #d03050;
  background: #fef0f0;
}
.fh-text {
  flex: 1;
  min-width: 0;
  color: #303133;
  word-break: break-word;
}
</style>
