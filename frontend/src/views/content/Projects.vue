<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>项目案例</h3>
        <div class="bar-right">
          <el-select v-model="catFilter" placeholder="全部分类" clearable style="width: 160px">
            <el-option v-for="c in categories" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <el-input
            v-model="keyword"
            placeholder="搜索案例名"
            clearable
            :prefix-icon="Search"
            style="width: 200px"
          />
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="primary" :icon="Plus" @click="openCreate">新增案例</el-button>
        </div>
      </div>

      <el-table ref="tableRef" :data="paged" v-loading="loading" border stripe>
        <el-table-column prop="name" label="名称" min-width="180" />
        <el-table-column label="分类" width="130" v-if="!isPhone">
          <template #default="{ row }">{{ categoryName(row.categoryId) }}</template>
        </el-table-column>
        <el-table-column label="封面" width="80" align="center" v-if="!isPhone">
          <template #default="{ row }">
            <ImageThumb v-if="row.cover" :src="row.cover" :width="40" :height="40" />
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="精选" width="80" align="center" v-if="!isPhone">
          <template #default="{ row }">
            <el-tag :type="row.isHomeFeatured ? 'danger' : 'info'" effect="plain">
              {{ row.isHomeFeatured ? '精选' : '普通' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="上架" width="80" align="center">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" effect="plain">
              {{ row.enabled ? '上架' : '下架' }}
            </el-tag>
          </template>
        </el-table-column>
        <!-- 显示更新时间而非创建时间：运营看列表要回答的是「哪些案例内容旧了该翻新」，
             创建时间回答不了这个问题（一年前建的上周刚改过，内容并不旧）。
             手机端隐藏（窄屏优先保住可操作列）；空值显示「—」。 -->
        <el-table-column label="更新时间" width="180" align="center" v-if="!isPhone">
          <template #default="{ row }">{{ formatDateTime(row.updatedAt) }}</template>
        </el-table-column>
        <el-table-column label="排序" width="120" align="center">
          <template #default="{ row }">
            <div class="sort-cell">
              <span class="drag-handle" draggable="true" title="按住拖拽调整顺序">
                <el-icon><Rank /></el-icon>
              </span>
              <el-button
                link
                :disabled="isFirstRow(row)"
                title="上移"
                @click="moveUp(row)"
              >
                <el-icon><ArrowUp /></el-icon>
              </el-button>
              <el-button
                link
                :disabled="isLastRow(row)"
                title="下移"
                @click="moveDown(row)"
              >
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
      :title="editingId ? '编辑案例' : '新增案例'"
      width="760px"
      class="project-dialog"
      :append-to-body="true"
      @closed="resetForm"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="分类" prop="categoryId">
          <el-select v-model="form.categoryId" placeholder="选择业绩分类" style="width: 240px">
            <el-option v-for="c in categories" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="简介">
          <el-input v-model="form.summary" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="详情">
          <RichEditor v-model="form.detail" :height="360" />
        </el-form-item>
        <el-form-item label="封面">
          <UploadField v-model="form.cover" :multiple="false" accept="image/*" library-type="cover" />
        </el-form-item>
        <el-form-item label="相册">
          <UploadField v-model="form.album" :multiple="true" :limit="9" accept="image/*" library-type="cover" />
        </el-form-item>
        <el-form-item label="首页精选">
          <el-switch v-model="form.isHomeFeatured" />
        </el-form-item>
        <el-form-item label="上架">
          <el-switch v-model="form.enabled" />
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
import { ref, reactive, computed, watch, onMounted, defineAsyncComponent } from 'vue'
import { Refresh, Plus, Search, Rank, ArrowUp, ArrowDown } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  sortProjects,
  listCategories,
} from '@/api/content'
import UploadField from '@/components/UploadField.vue'
import ImageThumb from '@/components/ImageThumb.vue'
import { formatDateTime } from '@/utils/datetime'
// 富文本编辑器体积较大（~800KB），按页面懒加载，避免拖累首屏
const RichEditor = defineAsyncComponent(() => import('@/components/RichEditor.vue'))
import { useTable } from '@/composables/useTable'
import { useSortableRows } from '@/composables/useSortableRows'
import { useBreakpoint } from '@/composables/useBreakpoint'

const rows = ref<any[]>([])
const categories = ref<any[]>([])
const catFilter = ref<number | undefined>(undefined)
const baseList = computed(() =>
  catFilter.value ? rows.value.filter((r) => r.categoryId === catFilter.value) : rows.value
)
const { keyword, currentPage, size, total, paged } = useTable(
  baseList,
  (r, kw) => (r.name || '').toLowerCase().includes(kw)
)
// 切换分类时回到第一页，避免停留在越界页
watch(catFilter, () => (currentPage.value = 1))
const loading = ref(false)
const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({
  categoryId: undefined as number | undefined,
  name: '',
  summary: '',
  detail: '',
  cover: '',
  album: [] as string[],
  isHomeFeatured: false,
  enabled: true,
  sort: 1,
})
const rules: FormRules = {
  name: [{ required: true, message: '请输入案例名称', trigger: 'blur' }],
  categoryId: [{ required: true, message: '请选择分类', trigger: 'change' }],
}

function categoryName(id: number) {
  const c = categories.value.find((x) => x.id === id)
  return c ? c.name : '—'
}

/* ===== 列表顺序调整：拖拽 + 上下箭头（与分类/服务页共用同一 composable） ===== */
const { tableRef, isFirstRow, isLastRow, moveUp, moveDown } = useSortableRows(
  rows,
  paged,
  sortProjects
)

async function load() {
  loading.value = true
  try {
    const [ps, cs] = await Promise.all([listProjects(), listCategories()])
    rows.value = ps
    categories.value = cs
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

function resetForm() {
  form.categoryId = undefined
  form.name = ''
  form.summary = ''
  form.detail = ''
  form.cover = ''
  form.album = []
  form.isHomeFeatured = false
  form.enabled = true
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
  form.categoryId = row.categoryId
  form.name = row.name
  form.summary = row.summary
  form.detail = row.detail
  form.cover = row.cover
  form.album = row.album ? [...row.album] : []
  form.isHomeFeatured = row.isHomeFeatured
  form.enabled = row.enabled
  form.sort = row.sort
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  saving.value = true
  try {
    const payload = {
      categoryId: form.categoryId,
      name: form.name,
      summary: form.summary,
      detail: form.detail,
      cover: form.cover,
      album: form.album,
      isHomeFeatured: form.isHomeFeatured,
      enabled: form.enabled,
      sort: form.sort,
    }
    if (editingId.value) {
      await updateProject(editingId.value, payload)
      ElMessage.success('已更新')
    } else {
      await createProject(payload)
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
  const confirmed = await ElMessageBox.confirm(`确认删除案例「${row.name}」？`, '提示', {
    type: 'warning',
  })
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  try {
    await deleteProject(row.id)
    ElMessage.success('已删除')
    await load()
  } catch {
    /* 拦截器已提示 */
  }
}

onMounted(load)
</script>

<style scoped>
/* 弹窗分辨率适配与视觉规范见全局 src/styles/admin.css（.project-dialog 类）。 */

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
  flex-wrap: wrap;
}
.muted {
  color: #c0c4cc;
}
/* 排序操作区样式见全局 src/styles/admin.css（.sort-cell / .drag-handle）。 */
</style>
