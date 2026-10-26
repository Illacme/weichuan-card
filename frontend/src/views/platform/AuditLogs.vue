<template>
  <el-card>
    <div class="page-bar">
      <h3>操作日志</h3>
      <div class="bar-right">
        <span class="bar-tip">后台写操作与登录失败留痕，只可查询、不可修改</span>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="记录谁在什么时候、对什么对象做了什么"
      description="覆盖系统设置变更（含密钥，但只记「已修改」不落明文）、管理员增删改、企业启停、员工增删改与申请审核，以及登录失败（连续失败是暴力破解的信号）。审计记录一旦可改就等于没有审计，因此本页只提供查询。"
      style="margin-bottom: 14px"
    />

    <div class="filter-bar">
      <el-select v-model="action" clearable placeholder="全部动作" class="filter-item" @change="onFilter">
        <el-option v-for="a in actionOptions" :key="a" :label="actionLabel(a)" :value="a" />
      </el-select>
      <el-select v-model="result" clearable placeholder="全部结果" class="filter-item" @change="onFilter">
        <el-option label="成功" value="success" />
        <el-option label="失败" value="fail" />
      </el-select>
      <el-select v-model="actorType" clearable placeholder="全部操作人" class="filter-item" @change="onFilter">
        <el-option label="平台/企业管理员" value="admin" />
        <el-option label="员工" value="employee" />
        <el-option label="未登录（登录失败）" value="anonymous" />
        <el-option label="系统（后台任务）" value="system" />
      </el-select>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column label="时间" width="170" align="center">
        <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="操作人" min-width="120" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.actorName || actorTypeText(row.actorType) }}
          <el-tag v-if="row.actorType === 'anonymous'" size="small" type="info" effect="plain">未登录</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="动作" min-width="130">
        <template #default="{ row }">
          <el-tag :type="row.result === 'fail' ? 'danger' : 'success'" size="small" effect="plain">
            {{ actionLabel(row.action) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="targetId" label="对象" min-width="130" show-overflow-tooltip />
      <el-table-column label="详情" min-width="200" show-overflow-tooltip v-if="!isPhone">
        <template #default="{ row }">{{ row.detail || '—' }}</template>
      </el-table-column>
      <el-table-column prop="ip" label="IP" width="130" align="center" v-if="!isPhone" />
    </el-table>

    <el-pagination
      class="table-pager"
      v-model:current-page="page"
      v-model:page-size="size"
      :total="total"
      :page-sizes="[20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      @current-change="load"
      @size-change="onFilter"
    />
  </el-card>
</template>

<script setup lang="ts">
import { Refresh } from '@element-plus/icons-vue'
import { onMounted, ref } from 'vue'
import { ACTION_LABELS, actionLabel, listAuditLogs, type AuditLog } from '@/api/audit'
import { formatDateTime } from '@/utils/datetime'
import { useBreakpoint } from '@/composables/useBreakpoint'

const { isPhone } = useBreakpoint()

const rows = ref<AuditLog[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const size = ref(20)

const action = ref('')
const result = ref('')
const actorType = ref('')

// 下拉直接取中文名表的键：后端新增动作时只要补一条 ACTION_LABELS，这里自动跟上。
// 手写清单曾漏掉 employee.* / apply.* / login.success / logout / company.admin.reset_password
// 共 9 个已埋点的动作——列表里看得见（有中文名）却筛不出来，等于功能缺了一块。
const actionOptions = Object.keys(ACTION_LABELS)

function actorTypeText(t: string) {
  return { admin: '管理员', employee: '员工', anonymous: '未登录', system: '系统' }[t] || t || '—'
}

function onFilter() {
  page.value = 1
  load()
}

async function load() {
  loading.value = true
  try {
    const p = await listAuditLogs({
      page: page.value,
      size: size.value,
      action: action.value || undefined,
      result: result.value || undefined,
      actorType: actorType.value || undefined,
    })
    rows.value = p?.list ?? []
    total.value = p?.total ?? 0
  } catch {
    // 拦截器已提示（如非平台超管越权）
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.filter-bar {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.filter-item {
  width: 180px;
}
/* 手机端筛选器占满一行，避免三个下拉挤在一排 */
@media (max-width: 767px) {
  .filter-item {
    width: 100%;
  }
}
</style>
