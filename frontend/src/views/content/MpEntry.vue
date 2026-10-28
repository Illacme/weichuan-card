<template>
  <el-card>
    <div class="page-bar" :class="{ 'only-action': props.embedded }">
      <h3 v-if="!props.embedded">小程序入口</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <el-alert type="info" :closable="false" show-icon class="scope-tip">
      <template #title>只影响「裸启动」</template>
      <div class="scope-tip-body">
        扫微信「体验版二维码」、或在微信里直接打开小程序时，微信不带任何参数，本页决定访客先看到什么。
        员工自己的小程序码（<code>scene=staffId=</code>）与员工转发的名片卡片不受此设置影响，
        永远进对应员工的名片。
      </div>
    </el-alert>

    <!-- 配置与实际不一致：最常见是指定员工停用了。不说出来的话，管理员会以为设置生效了。 -->
    <el-alert
      v-if="mismatch"
      type="warning"
      :closable="false"
      show-icon
      class="scope-tip"
      :title="mismatchTitle"
    >
      <div class="scope-tip-body">
        {{ mismatchBody }}
        <el-button link type="primary" @click="fixMismatch">改为名片墙</el-button>
      </div>
    </el-alert>

    <el-form label-width="120px" style="max-width: 720px">
      <el-form-item label="默认进入">
        <el-radio-group v-model="form.mode" :disabled="saving">
          <el-radio value="wall">企业名片墙</el-radio>
          <el-radio value="staff">直接进入指定员工的名片</el-radio>
        </el-radio-group>
        <div class="field-tip">
          {{ form.mode === 'wall'
            ? '访客先看到公司抬头 + 全员名片宫格，点谁看谁。适合有多名销售的企业。'
            : '访客扫码/打开就直接进这位员工的名片，省掉一层宫格。适合「一张名片打天下」的用法。' }}
        </div>
      </el-form-item>

      <el-form-item v-if="form.mode === 'staff'" label="展示员工">
        <el-select
          v-model="form.staffId"
          filterable
          remote
          clearable
          :remote-method="onSearch"
          :loading="searching"
          placeholder="输入姓名搜索员工"
          style="width: 100%"
          :fallback-placements="['bottom-start', 'top-start']"
        >
          <el-option
            v-for="e in candidates"
            :key="e.staffId"
            :label="e.name + (e.title ? ' · ' + e.title : '')"
            :value="e.staffId"
          />
        </el-select>
        <div class="field-tip">
          只能选本企业启用中的员工。员工停用后，访客会自动回到名片墙（不会看到一张停用的废页）。
        </div>
      </el-form-item>

      <el-form-item>
        <el-button type="primary" :loading="saving" :disabled="!canSave" @click="save">保存</el-button>
        <span v-if="form.mode === 'staff' && !form.staffId" class="field-tip inline">
          请先选择要展示的员工
        </span>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { computed, onMounted, reactive, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getMpEntry, saveMpEntry, searchEntryStaff, type MpEntryMode } from '@/api/mpEntry'

// embedded=true：被「小程序设置」内嵌在页签里时，不再重复自己的页面标题
// （页签名已经是「访客先看到谁」）。独立访问（旧链接 /content/mp-entry 重定向前）仍用它。
const props = defineProps<{ embedded?: boolean }>()

const loading = ref(false)
const saving = ref(false)
const searching = ref(false)

// form 只放「可编辑的两项」。其余（staffName / effectiveMode）是服务端算出来的，
// 不放进表单，避免误以为它们能提交。
const form = reactive<{ mode: MpEntryMode; staffId: number }>({ mode: 'wall', staffId: 0 })
const snap = reactive({
  effectiveMode: 'wall' as MpEntryMode,
  staffName: '',
  staffTitle: '',
  staffStatus: '',
  staffMissing: false,
})
const candidates = ref<any[]>([])

const canSave = computed(() => form.mode === 'wall' || form.staffId > 0)

// 配置的 staff 没能生效（员工被停用 / 被删除）
const mismatch = computed(() => form.mode === 'staff' && snap.effectiveMode !== 'staff')

const mismatchTitle = computed(() => {
  if (!mismatch.value) return ''
  if (snap.staffMissing) return '指定的员工已不存在'
  if (snap.staffStatus === 'disabled') return '指定的员工已停用'
  return '该设置当前未生效'
})

const mismatchBody = computed(() => {
  if (!mismatch.value) return ''
  if (snap.staffMissing) {
    return '这位员工已被删除，访客现在看到的是企业名片墙。请重新选择一位员工，或改回名片墙。'
  }
  if (snap.staffStatus === 'disabled') {
    return '这位员工已停用，访客现在看到的是企业名片墙。可以启用该员工，或换一位、或改回名片墙。'
  }
  return '访客现在看到的是企业名片墙。'
})

async function load() {
  loading.value = true
  try {
    const d = await getMpEntry()
    form.mode = d.mode === 'staff' ? 'staff' : 'wall'
    form.staffId = Number(d.staffId) || 0
    snap.effectiveMode = d.effectiveMode === 'staff' ? 'staff' : 'wall'
    snap.staffName = d.staffName || ''
    snap.staffTitle = d.staffTitle || ''
    snap.staffStatus = d.staffStatus || ''
    snap.staffMissing = !!d.staffMissing
    // 已指定的员工可能不在第一批候选里（关键字搜索才有他），先塞进去，
    // 否则下拉框会显示成一个空的「请选择」——管理员以为配置丢了。
    candidates.value = form.staffId > 0 && d.staffName
      ? [{ staffId: form.staffId, name: d.staffName, title: d.staffTitle }]
      : []
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

// 远程搜索：不做本地过滤（员工可能上千人，一次只取 20 条由服务端筛）
let searchTimer: ReturnType<typeof setTimeout> | null = null
function onSearch(kw: string) {
  if (searchTimer) clearTimeout(searchTimer)
  // 250ms 防抖：每敲一个字就发一次请求，中文输入法下会打出好几次无效请求
  searchTimer = setTimeout(async () => {
    searching.value = true
    try {
      candidates.value = await searchEntryStaff((kw || '').trim())
    } catch {
      /* 拦截器已提示；保留旧候选，避免下拉瞬间清空 */
    } finally {
      searching.value = false
    }
  }, 250)
}

async function save() {
  if (!canSave.value) return
  saving.value = true
  try {
    await saveMpEntry(
      form.mode === 'staff' ? { mode: 'staff', staffId: form.staffId } : { mode: 'wall', staffId: 0 }
    )
    ElMessage.success('已保存')
    // 重新拉一次：生效值 / 员工状态都要刷新（保存后 mismatch 可能已消失）
    await load()
  } catch {
    /* 拦截器已提示（如「该员工已停用」） */
  } finally {
    saving.value = false
  }
}

function fixMismatch() {
  form.mode = 'wall'
}

onMounted(load)
</script>

<style scoped>
/* .page-bar 不是全局类：每个页面各自在 scoped 样式里定义一次（见其他内容页）。
   漏了这段就没有 flex，标题与按钮会各占一行、按钮贴左。 */
.page-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.page-bar h3 {
  margin: 0;
}
/* 内嵌在「小程序设置」页签里时没有标题，只剩「重新加载」——不要让它贴左 */
.page-bar.only-action {
  justify-content: flex-end;
}
.scope-tip {
  margin-bottom: 16px;
}
.scope-tip-body {
  font-size: 12px;
  line-height: 1.7;
  margin-top: 4px;
}
.field-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
.field-tip.inline {
  margin-left: 12px;
}
</style>
