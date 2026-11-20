<template>
  <div class="company-storage-page">
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="选择本企业的文件存储与分发"
      description="这里只能选择平台已经准备好的档案，看不到也改不了任何云凭据。保存后本企业新上传的文件立刻写入所选存储；历史对象不会自动搬迁，如需搬运请联系平台管理员执行「一键迁移」。不单独设置时自动继承平台全局默认。"
    />
    <el-card class="binding-card">
      <h4>当前生效配置</h4>
      <div class="current">
        <span>存储档案：<b>{{ currentStorageName }}</b></span>
        <span>分发档案：<b>{{ currentDeliveryName }}</b></span>
        <span>来源：{{ currentSource }}</span>
      </div>
      <el-form label-width="120px" class="form">
        <el-form-item label="存储档案">
          <el-select v-model="storageId" placeholder="请选择" style="width: 320px">
            <el-option v-for="p in storages" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="分发档案（CDN）">
          <el-select v-model="deliveryId" placeholder="不启用 CDN" clearable style="width: 320px">
            <el-option v-for="p in deliveries" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <el-alert
        v-if="savingWarning"
        type="warning"
        :closable="false"
        show-icon
        :title="savingWarning"
        class="warn"
      />
      <div class="actions">
        <el-button type="primary" :loading="saving" @click="save">保存本企业绑定</el-button>
        <el-button :disabled="!bound" :loading="saving" @click="reset">恢复继承全局</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listStorageBindings, saveCompanyBinding, resetCompanyBinding,
} from '@/api/storageProfiles'
import http from '@/api/request'
import { useAuthStore } from '@/store/auth'

type Option = { id: number; name: string }

const auth = useAuthStore()
const storages = ref<Option[]>([])
const deliveries = ref<Option[]>([])
const currentStorageId = ref(0)
const currentDeliveryId = ref(0)
const currentSource = ref('继承全局')
const storageId = ref<number | undefined>(undefined)
const deliveryId = ref<number | undefined>(undefined)
const bound = ref(false)
const saving = ref(false)

const currentStorageName = computed(() => storages.value.find((p) => p.id === currentStorageId.value)?.name || '（未选择）')
const currentDeliveryName = computed(() => deliveries.value.find((p) => p.id === currentDeliveryId.value)?.name || '不启用 CDN')
const savingWarning = computed(() =>
  bound.value ? '本企业已有单独绑定：保存后会立即改变新文件的落点，请先确认历史对象仍可访问。' : ''
)

async function load() {
  const [options, bindings]: [any, any] = await Promise.all([
    http.get('/admin/storage-bindings/options').then((r: any) => r.data ?? {}),
    listStorageBindings(),
  ])
  storages.value = options.storageProfiles || []
  deliveries.value = options.deliveryProfiles || []
  const own = (bindings.companies || [])[0]
  if (own) {
    bound.value = true
    currentStorageId.value = own.storageProfileId
    currentDeliveryId.value = own.deliveryProfileId || 0
    currentSource.value = '本企业单独绑定'
    storageId.value = own.storageProfileId
    deliveryId.value = own.deliveryProfileId || undefined
  } else {
    bound.value = false
    currentStorageId.value = 0
    currentDeliveryId.value = 0
    currentSource.value = '继承全局默认'
    storageId.value = undefined
    deliveryId.value = undefined
  }
}

onMounted(load)

async function save() {
  if (!storageId.value) return ElMessage.warning('请选择存储档案')
  saving.value = true
  try {
    await saveCompanyBinding(auth.companyId, storageId.value, deliveryId.value || 0)
    ElMessage.success('已保存，新上传立即生效')
    await load()
  } finally {
    saving.value = false
  }
}

async function reset() {
  await ElMessageBox.confirm('恢复后会继承平台全局默认，本企业已上传的对象不会被搬走。确认继续？', '恢复继承', { type: 'warning' })
  saving.value = true
  try {
    await resetCompanyBinding(auth.companyId)
    ElMessage.success('已恢复继承全局默认')
    await load()
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.company-storage-page { padding: 4px 0; }
.binding-card { margin-top: 14px; }
.binding-card h4 { margin: 0 0 12px; font-size: 14px; }
.current { display: flex; gap: 24px; flex-wrap: wrap; font-size: 13px; color: #606266; margin-bottom: 16px; }
.actions { display: flex; gap: 10px; }
.warn { margin-bottom: 14px; }
</style>
