<template>
  <!--
    企业选择器：平台超管「当前正在管理哪家企业」的唯一入口。
    顶栏内联与手机端弹窗共用这一个实现——两处各写一遍必然漂移（一处加了「已停用」标记、
    另一处忘了），而这种漂移只会在客户现场被发现。
  -->
  <el-select
    class="company-picker"
    :model-value="modelValue || undefined"
    :placeholder="placeholder"
    :loading="tenant.loading"
    filterable
    clearable
    remote
    :remote-method="onSearch"
    :fallback-placements="['bottom-start', 'top-start']"
    @change="onChange"
    @visible-change="onVisible"
  >
    <el-option
      v-for="c in tenant.companies"
      :key="c.id"
      :label="c.shortName || c.name"
      :value="c.id"
    >
      <span class="opt-name">{{ c.shortName || c.name }}</span>
      <!-- 停用企业仍可被选中查看历史内容（后端内容接口不校验启用状态），
           但必须标出来，否则平台超管会以为这家还在正常运营 -->
      <span v-if="c.status === 'disabled'" class="opt-tag">已停用</span>
    </el-option>
    <template #empty>
      <div class="opt-empty">{{ tenant.loaded ? '没有匹配的企业' : '加载中…' }}</div>
    </template>
  </el-select>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useTenantStore } from '@/store/tenant'

const props = defineProps<{
  modelValue: number
  placeholder?: string
}>()
const emit = defineEmits<{ (e: 'change', id: number): void }>()

const tenant = useTenantStore()
const placeholder = computed(() => props.placeholder || '选择要管理的企业')

// 远程搜索防抖：每敲一个字就打一次接口，企业一多就是自造压力。
// 250ms 是「输入停顿」与「响应及时」的折中（与表格关键字筛选同一量级）。
let timer: ReturnType<typeof setTimeout> | null = null
function onSearch(kw: string) {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    tenant.search(kw).catch(() => {
      // search 内部已兜底（保留旧候选），这里只是不让未处理的 rejection 冒到控制台
    })
  }, 250)
}

// 展开时确保有候选：首次点开才拉，避免每个页面挂载都请求一次企业列表
function onVisible(open: boolean) {
  if (open) tenant.ensureLoaded().catch(() => {})
}

function onChange(v: unknown) {
  const id = Number(v) || 0
  tenant.setCurrent(id)
  emit('change', id)
}
</script>

<style scoped>
.company-picker {
  width: 190px;
}
.opt-name {
  float: left;
}
.opt-tag {
  float: right;
  color: #e6a23c;
  font-size: 12px;
  margin-left: 8px;
}
.opt-empty {
  padding: 8px 12px;
  color: #909399;
  font-size: 13px;
  text-align: center;
}
</style>
