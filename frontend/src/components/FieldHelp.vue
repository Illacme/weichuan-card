<template>
  <!-- 配置说明徽标：默认只占一个「?」的位置，指针停留 / 键盘聚焦 / 点击时才展开。
     Element Plus 没有 manual 这个 trigger 值，用空数组关掉它自带的触发方式，
     再由我们自己的 mouseenter / focus / click 控 visible：hover 看一眼、点击钉住（再点或点别处收起），
     这样长说明不必常驻版面，又不会像 popover 那样必须切走焦点才能关。 -->
  <el-tooltip
    :content="content"
    placement="top"
    :trigger="[]"
    popper-class="field-help-popper"
    :visible="visible"
    @update:visible="visible = $event"
  >
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
      @click.stop="toggle"
      @keydown.enter.prevent.stop="toggle"
      @keydown.space.prevent.stop="toggle"
    >?</span>
  </el-tooltip>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{
  /** 配置项名称，用于 aria-label 与弹层首行 */
  title: string
  /** 这个功能干什么 */
  usage: string
  /** 值从哪里取 */
  source?: string
  /** 填写规则 / 生效条件 */
  rules?: string
  /** 容易踩的坑、排查线索 */
  note?: string
}>()

// 四段固定顺序，缺哪段就不占位。用换行拼成一条 content，交给 popper 的
// white-space: pre-line 折行——比往每个页面塞一张说明表轻得多，
// 也保证每个配置项的结构完全一致（客户照着读就会填）。
const content = computed(() => {
  const lines = [`功能：${props.usage}`]
  if (props.source) lines.push(`值来源：${props.source}`)
  if (props.rules) lines.push(`规则限制：${props.rules}`)
  if (props.note) lines.push(`注意：${props.note}`)
  return lines.join('\n')
})

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
  width: 15px;
  height: 15px;
  margin-left: 6px;
  border-radius: 50%;
  background: #c0c4cc;
  color: #fff;
  font-size: 11px;
  line-height: 1;
  font-weight: 600;
  cursor: help;
  user-select: none;
  vertical-align: middle;
  transition: background 0.15s ease;
}
.field-help:hover,
.field-help:focus,
.field-help:focus-visible {
  background: #409eff;
  outline: none;
}
</style>

<!-- el-tooltip 的 popper 挂在 body 下，scoped 样式够不到，必须单独一个非 scoped 块 -->
<style>
.field-help-popper {
  max-width: 360px;
  padding: 10px 12px;
  text-align: left;
  line-height: 1.7;
  white-space: pre-line;
  font-size: 12px;
}
</style>
