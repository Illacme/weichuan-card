<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>业务服务</h3>
        <div class="bar-right">
          <el-input
            v-model="keyword"
            placeholder="搜索服务标题"
            clearable
            :prefix-icon="Search"
            style="width: 200px"
          />
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="primary" :icon="Plus" @click="openCreate">新增服务</el-button>
        </div>
      </div>

      <el-table ref="tableRef" :data="paged" v-loading="loading" border stripe>
        <el-table-column prop="title" label="标题" min-width="200" />
        <el-table-column label="图标" width="90" align="center" v-if="!isPhone">
          <template #default="{ row }">
            <ImageThumb v-if="row.icon" :src="row.icon" :width="36" :height="64" />
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="内容(摘要)" min-width="280" show-overflow-tooltip v-if="!isPhone">
          <template #default="{ row }">
            <span class="muted">{{ stripHtml(row.content) }}</span>
          </template>
        </el-table-column>
        <!-- 显示更新时间而非创建时间：与项目案例列表同口径——运营要判断的是
             「哪些服务内容旧了」，创建时间回答不了这个问题。 -->
        <el-table-column label="更新时间" width="180" align="center" v-if="!isPhone">
          <template #default="{ row }">{{ formatDateTime(row.updatedAt) }}</template>
        </el-table-column>
        <el-table-column label="排序" width="120" align="center">
          <template #default="{ row }">
            <div class="sort-cell">
              <span class="drag-handle" draggable="true" title="按住拖拽调整顺序">
                <el-icon><Rank /></el-icon>
              </span>
              <el-button link :disabled="isFirstRow(row)" title="上移" @click="moveUp(row)">
                <el-icon><ArrowUp /></el-icon>
              </el-button>
              <el-button link :disabled="isLastRow(row)" title="下移" @click="moveDown(row)">
                <el-icon><ArrowDown /></el-icon>
              </el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right" align="center">
          <template #default="{ row }">
            <el-button type="primary" link @click="openEdit(row)">编辑</el-button>
            <el-button type="danger" link @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="table-pager"
        v-model:current-page="currentPage"
        v-model:page-size="size"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        background
      />
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? '编辑服务' : '新增服务'"
      width="640px"
      class="resp-dialog"
      @closed="resetForm"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" />
        </el-form-item>
        <el-form-item label="图标">
          <UploadField v-model="form.icon" :multiple="false" accept="image/*" library-type="icon" />
        </el-form-item>
        <el-form-item label="内容">
          <RichEditor v-model="form.content" :height="320" />
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort" :min="0" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
const { isPhone } = useBreakpoint()
import { ref, reactive, onMounted, defineAsyncComponent } from 'vue'
import { Refresh, Plus, Search, Rank, ArrowUp, ArrowDown } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listServices,
  createService,
  updateService,
  deleteService,
  sortServices,
} from '@/api/content'
import { useTable } from '@/composables/useTable'
import ImageThumb from '@/components/ImageThumb.vue'
import { useSortableRows } from '@/composables/useSortableRows'
import { formatDateTime } from '@/utils/datetime'
import UploadField from '@/components/UploadField.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
// 富文本编辑器体积较大（~800KB），按页面懒加载，避免拖累首屏
const RichEditor = defineAsyncComponent(() => import('@/components/RichEditor.vue'))

const rows = ref<any[]>([])
const { keyword, currentPage, size, total, paged } = useTable(
  rows,
  (r, kw) => (r.title || '').toLowerCase().includes(kw)
)
// 列表顺序调整：拖拽 + 上下箭头（与项目案例/业绩分类共用同一 composable）
const { tableRef, isFirstRow, isLastRow, moveUp, moveDown } = useSortableRows(
  rows,
  paged,
  sortServices
)
const loading = ref(false)
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({ title: '', icon: '', content: '', sort: 1 })
const rules: FormRules = {
  title: [{ required: true, message: '请输入标题', trigger: 'blur' }],
}

function stripHtml(html: string) {
  if (!html) return '（空）'
  return html
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 60)
}

async function load() {
  loading.value = true
  try {
    rows.value = await listServices()
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

function resetForm() {
  form.title = ''
  form.icon = ''
  form.content = ''
  form.sort = 1
  editingId.value = null
  formRef.value?.clearValidate()
}

function openCreate() {
  resetForm()
  dialogVisible.value = true
}

function openEdit(row: any) {
  editingId.value = row.id
  form.title = row.title
  form.icon = row.icon
  form.content = row.content
  form.sort = row.sort
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  saving.value = true
  try {
    if (editingId.value) {
      await updateService(editingId.value, { ...form })
      ElMessage.success('已更新')
    } else {
      await createService({ ...form })
      ElMessage.success('已新增')
    }
    dialogVisible.value = false
    await load()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function remove(row: any) {
  const confirmed = await ElMessageBox.confirm(`确认删除服务「${row.title}」？`, '提示', {
    type: 'warning',
  })
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  try {
    await deleteService(row.id)
    ElMessage.success('已删除')
    await load()
  } catch {
    /* 拦截器已提示 */
  }
}

onMounted(load)
</script>

<style scoped>
.page-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.page-bar h3 {
  margin: 0;
}
.bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.muted {
  color: #909399;
}
</style>
