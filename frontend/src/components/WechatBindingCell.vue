<script setup lang="ts">
/**
 * 微信绑定状态单元格：平台超管 / 企业管理员 / 员工三处列表共用一个口径。
 *
 * 为什么抽成组件而不各写一份模板：这三处展示的是同一件事（这个账号能不能用微信免密
 * 进系统），文案 / 颜色 / 「掩码怎么露」一旦各写各的，后台就会出现三种「已绑定」，
 * 运营核对时反而要重新学一遍。P1 若加解绑入口，也只需在这一处扩展。
 *
 * 只展示**布尔状态 + 脱敏后的掩码**：openid 原文绝不出网（见后端
 * handler/wechat_binding.go 的口径）。掩码仅用于「大概是哪一个」的肉眼核对，
 * 精确定位走按 openid 反查，方向正好反过来。
 */
const props = withDefaults(
  defineProps<{
    bound: boolean
    /** 已脱敏的 openid；空串表示未绑定或后端没给 */
    masked?: string
    /** 窄表格（企业管理员弹窗）只显示状态标签，省掉掩码一列的空间 */
    compact?: boolean
  }>(),
  { masked: '', compact: false }
)
</script>

<template>
  <div
    class="wx-bind-cell"
    :class="{ 'is-compact': props.compact }"
    :data-bound="props.bound ? 'true' : 'false'"
    :data-masked="props.bound && !props.compact ? props.masked : ''"
  >
    <el-tag :type="props.bound ? 'success' : 'info'" effect="light" size="small">
      {{ props.bound ? '已绑定' : '未绑定' }}
    </el-tag>
    <span
      v-if="props.bound && props.masked && !props.compact"
      class="wx-bind-mask"
      :title="props.masked"
    >{{ props.masked }}</span>
  </div>
</template>

<style scoped>
.wx-bind-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}

.wx-bind-cell.is-compact {
  align-items: center;
}

.wx-bind-mask {
  font-size: 12px;
  color: #909399;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  white-space: nowrap;
}
</style>
