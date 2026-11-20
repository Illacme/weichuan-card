<template>
  <!-- 配置项下面的即时说明统一用「!」：和帮助标识「?」分工明确。
       ? = 点开看完整规则；! = 当前这次填写需要马上知道的事（填错了、不生效、有风险）。 -->
  <div :class="['inline-notice', `is-${tone || 'warn'}`]">
    <span class="inline-notice-mark" aria-hidden="true">!</span>
    <span class="inline-notice-text"><slot /></span>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    /** warn=需要留意，info=操作提示，danger=会保存失败，good=已就绪 */
    tone?: 'warn' | 'info' | 'danger' | 'good' | 'ok'
  }>(),
  { tone: 'warn' }
)
</script>

<style scoped>
.inline-notice {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-top: 6px;
  padding: 6px 9px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.65;
  word-break: break-word;
}
.inline-notice-mark {
  flex: 0 0 14px;
  height: 14px;
  margin-top: 2px;
  border-radius: 50%;
  color: #fff;
  font-size: 11px;
  font-weight: 700;
  line-height: 14px;
  text-align: center;
}
.inline-notice-text {
  flex: 1;
  min-width: 0;
}
.is-warn {
  background: #fdf6ec;
  color: #8a5b13;
}
.is-warn .inline-notice-mark {
  background: #e6a23c;
}
.is-info {
  background: #ecf5ff;
  color: #2f6fb5;
}
.is-info .inline-notice-mark {
  background: #409eff;
}
.is-danger {
  background: #fef0f0;
  color: #b52745;
}
.is-danger .inline-notice-mark {
  background: #f56c6c;
}
.is-good,
.is-ok {
  background: #f0f9eb;
  color: #4d8b32;
}
.is-good .inline-notice-mark,
.is-ok .inline-notice-mark {
  background: #67c23a;
}
.inline-notice-text :deep(strong) {
  font-weight: 600;
}
.is-warn .inline-notice-text :deep(strong) {
  color: #a8620b;
}
.is-danger .inline-notice-text :deep(strong) {
  color: #c0392b;
}
.is-info .inline-notice-text :deep(strong) {
  color: #1f5fa6;
}
</style>
