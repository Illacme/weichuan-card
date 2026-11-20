<template>
  <div class="storage-profiles-page">
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="文件存储与分发：配置只存一份，谁用由绑定决定"
      description="三层关系：存储档案保存桶的连接信息；分发档案保存 CDN 域名；绑定决定谁用哪一套。tenant_id=0 是全局默认，企业没有自己的绑定行时自动继承全局。保存后下一个请求即生效，无需重启。档案与绑定都含云凭据或决定文件落点，只有平台超管能增删改；企业管理员看不到也改不了。"
    />

    <el-tabs v-model="tab" class="storage-tabs">
      <!-- 存储档案 -->
      <el-tab-pane label="存储档案" name="storage">
        <div class="tab-head">
          <span class="tab-tip">
            同一份 endpoint / AK / SK 只填一次，多家企业共享。改一次桶地址，所有绑定它的企业立刻跟着变。
            企业自有桶由平台超管在这里接入并绑定；下方「迁移任务」可把历史对象搬到新档案（只复制不删除）。
          </span>
          <el-button type="primary" @click="openStorageDialog(null)">新建存储档案</el-button>
        </div>
        <el-table :data="storages" v-loading="loading" size="small" border>
          <el-table-column label="档案名" min-width="150">
            <template #default="{ row }">
              {{ row.name }}
              <el-tag v-if="row.builtin" size="small" type="info">内置</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="driver" label="驱动" width="90" />
          <el-table-column prop="endpoint" label="Endpoint" min-width="160" />
          <el-table-column prop="bucket" label="Bucket" min-width="120" />
          <el-table-column prop="baseUrl" label="上传返回基址" min-width="180" />
          <el-table-column label="连通性" width="100">
            <template #default="{ row }">
              <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="accessKeyMasked" label="AK" min-width="120" />
          <el-table-column prop="version" label="版本" width="70" />
          <el-table-column label="操作" width="220" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openStorageDialog(row)">编辑</el-button>
              <el-button link type="primary" @click="checkStorage(row)">自检</el-button>
              <el-button link type="danger" :disabled="row.builtin" @click="removeStorage(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- 分发档案 -->
      <el-tab-pane label="分发档案" name="delivery">
        <div class="tab-head">
          <span class="tab-tip">
            分发与存储互相独立：同一个桶可以配国内 / 海外多套 CDN 域名。域名只影响访客侧 URL，不改变上传落点。
          </span>
          <el-button type="primary" @click="openDeliveryDialog(null)">新建分发档案</el-button>
        </div>
        <el-table :data="deliveries" v-loading="loading" size="small" border>
          <el-table-column prop="name" label="档案名" min-width="130" />
          <el-table-column prop="provider" label="厂商" width="120" />
          <el-table-column prop="domain" label="公开域名" min-width="220" />
          <el-table-column prop="pathPrefix" label="路径前缀" min-width="110" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="version" label="版本" width="70" />
          <el-table-column label="操作" width="220" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openDeliveryDialog(row)">编辑</el-button>
              <el-button link type="primary" @click="checkDelivery(row)">自检</el-button>
              <el-button link type="danger" @click="removeDelivery(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- 绑定关系 -->
      <el-tab-pane label="绑定关系" name="binding">
        <el-alert
          class="binding-alert"
          type="warning"
          :closable="false"
          show-icon
          title="切换全局默认是即时生效的高危操作"
          description="请先确认新档案已「自检通过」，并且历史对象仍在旧桶中可读；否则切换后访客会拿到裂图。需要搬运历史对象请用「迁移任务」。"
        />
        <div class="binding-card">
          <h4>全局默认（tenant_id = 0）</h4>
          <p v-if="!boundGlobal" class="muted">
            当前未配置档案绑定，继续沿用「系统设置」里的平台存储与 CDN（改动需重启）。
          </p>
          <div class="binding-form">
            <label>存储档案</label>
            <el-select v-model="globalStorageId" placeholder="选择存储档案" style="width: 260px">
              <el-option v-for="p in storages" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
            <label>分发档案</label>
            <el-select v-model="globalDeliveryId" placeholder="不启用 CDN" clearable style="width: 260px">
              <el-option v-for="p in deliveries" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
            <el-button type="primary" :loading="saving" @click="saveGlobal">保存全局默认</el-button>
          </div>
        </div>
        <h4 class="company-title">企业覆盖（没有绑定行则继承全局）</h4>
        <el-table :data="companyBindings" v-loading="loading" size="small" border>
          <el-table-column prop="tenantId" label="企业 ID" width="90" />
          <el-table-column label="企业名称" min-width="160">
            <template #default="{ row }">{{ companyNames[row.tenantId] || `#${row.tenantId}` }}</template>
          </el-table-column>
          <el-table-column label="存储档案" min-width="170">
            <template #default="{ row }">
              <el-select v-model="row.storageProfileId" size="small" style="width:100%">
                <el-option v-for="p in storages" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="分发档案" min-width="170">
            <template #default="{ row }">
              <el-select v-model="row.deliveryProfileId" size="small" clearable style="width:100%">
                <el-option v-for="p in deliveries" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="生效来源" width="100">
            <template #default="{ row }">{{ row.source === 'global' ? '全局' : row.source === 'inherit' ? '继承' : row.source }}</template>
          </el-table-column>
          <el-table-column label="操作" width="150">
            <template #default="{ row }">
              <el-button link type="primary" @click="saveCompany(row)">保存</el-button>
              <el-button link type="danger" @click="resetCompany(row)">恢复继承</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- 迁移任务 -->
      <el-tab-pane label="迁移任务" name="migration">
        <el-alert
          class="binding-alert"
          type="info"
          :closable="false"
          show-icon
          title="只复制，不删除"
          description="历史对象会完整复制到目标档案（含缩略图），源桶对象原样保留，方便回滚与对账。单对象失败会进失败清单，不影响其余对象；仅在零失败时才允许自动切换绑定。视频按流式搬运，不会整段进内存。"
        />
        <div class="migration-create">
          <label>迁移范围</label>
          <el-select v-model="newJob.tenantId" style="width: 240px">
            <el-option :value="0" label="全平台（整桶）" />
            <el-option v-for="c in companies" :key="c.id" :value="c.id" :label="`企业：${c.name}`" />
          </el-select>
          <label>源档案</label>
          <el-select v-model="newJob.sourceProfileId" style="width: 200px">
            <el-option v-for="p in storages" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
          <label>目标档案</label>
          <el-select v-model="newJob.targetProfileId" style="width: 200px">
            <el-option v-for="p in storages" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
          <el-checkbox v-model="newJob.dryRun">试运行（只统计不复制）</el-checkbox>
          <el-checkbox v-model="newJob.autoSwitch" :disabled="newJob.dryRun">成功后自动切换绑定</el-checkbox>
          <el-button type="primary" :loading="saving" @click="createJob">创建任务</el-button>
        </div>
        <el-table :data="jobs" v-loading="loading" size="small" border>
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column label="范围" min-width="130">
            <template #default="{ row }">{{ row.tenantId ? `企业 #${row.tenantId}` : '全平台' }}</template>
          </el-table-column>
          <el-table-column label="源 → 目标" min-width="190">
            <template #default="{ row }">{{ profileName(row.sourceProfileId) }} → {{ profileName(row.targetProfileId) }}</template>
          </el-table-column>
          <el-table-column prop="status" label="状态" width="100" />
          <el-table-column label="进度" min-width="140">
            <template #default="{ row }">
              <el-progress :percentage="percent(row)" :status="row.failed ? 'exception' : undefined" />
              <span class="muted">{{ row.done }}/{{ row.total }}，失败 {{ row.failed }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="error" label="错误" min-width="180" show-overflow-tooltip />
          <el-table-column label="操作" width="210">
            <template #default="{ row }">
              <el-button link type="primary" :disabled="row.status === 'running' || row.status === 'done'" @click="startJob(row)">启动 / 续跑</el-button>
              <el-button link type="warning" :disabled="row.status !== 'running'" @click="stopJob(row)">取消</el-button>
              <el-button link type="primary" @click="openErrors(row)">失败清单</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <!-- 存储档案表单 -->
    <el-dialog v-model="storageDialog" :title="storageForm.id ? '编辑存储档案' : '新建存储档案'" width="640px">
      <el-alert type="warning" :closable="false" show-icon class="dialog-alert"
        title="凭据以明文入库，回显只给掩码"
        description="请确保数据库备份与访问控制到位。SecretKey 一旦保存就不再回传，忘记只能重新填写。" />
      <el-form label-width="130px" size="small">
        <el-form-item label="档案名"><el-input v-model="storageForm.name" placeholder="例如：主站 OSS" /></el-form-item>
        <el-form-item label="驱动">
          <el-select v-model="storageForm.driver" style="width: 100%" :disabled="storageForm.builtin">
            <el-option v-for="d in drivers" :key="d.value" :label="d.label" :value="d.value" />
          </el-select>
          <span v-if="storageForm.builtin" class="field-note">
            内置本地档案的驱动锁定为「本地磁盘」：它是云存储不可用时的兜底，不可删除、不可改成云驱动。可修改展示名与本地目录。
          </span>
        </el-form-item>
        <template v-if="storageForm.driver !== 'local'">
          <el-form-item label="Endpoint"><el-input v-model="storageForm.endpoint" placeholder="host[:port]，不要带 http://" /></el-form-item>
          <el-form-item label="Bucket"><el-input v-model="storageForm.bucket" /></el-form-item>
          <el-form-item label="Region"><el-input v-model="storageForm.region" placeholder="S3 兼容桶可留空" /></el-form-item>
          <el-form-item label="上传返回基址"><el-input v-model="storageForm.baseUrl" placeholder="https://your-domain/uploads" /></el-form-item>
          <el-form-item label="Host Header"><el-input v-model="storageForm.hostHeader" placeholder="留空=路径风格；<bucket>.域名=虚拟主机风格" /></el-form-item>
          <el-form-item label="使用 HTTPS"><el-switch v-model="storageForm.useSsl" /></el-form-item>
          <el-form-item label="公开读桶">
            <el-switch v-model="storageForm.publicRead" disabled />
            <span class="field-note">当前只支持公开读桶：私有桶需要限时签名链路，后端会拒绝 publicRead=false</span>
          </el-form-item>
          <el-form-item label="AccessKey">
            <el-input v-model="storageForm.accessKey" :placeholder="storageForm.id ? '留空表示不修改' : '必填'" />
            <span v-if="storageForm.accessKeyMasked" class="field-note">当前：{{ storageForm.accessKeyMasked }}</span>
          </el-form-item>
          <el-form-item label="SecretKey">
            <el-input v-model="storageForm.secretKey" type="password" show-password :placeholder="storageForm.id ? '留空表示不修改' : '必填'" />
          </el-form-item>
        </template>
        <template v-else>
          <el-form-item label="本地根目录">
            <el-input v-model="storageForm.localDir" placeholder="留空使用平台 uploads 目录" />
            <span class="field-note">迁移到另一套本地档案时，两边要填不同目录，否则源对象会被同名覆盖。</span>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="storageDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitStorage">保存</el-button>
      </template>
    </el-dialog>

    <!-- 分发档案表单 -->
    <el-dialog v-model="deliveryDialog" :title="deliveryForm.id ? '编辑分发档案' : '新建分发档案'" width="600px">
      <el-form label-width="130px" size="small">
        <el-form-item label="档案名"><el-input v-model="deliveryForm.name" /></el-form-item>
        <el-form-item label="厂商">
          <el-select v-model="deliveryForm.provider" style="width: 100%">
            <el-option v-for="p in providers" :key="p.value" :label="p.label" :value="p.value" />
          </el-select>
        </el-form-item>
        <template v-if="deliveryForm.provider !== 'none'">
          <el-form-item label="公开域名"><el-input v-model="deliveryForm.domain" placeholder="https://cdn.example.com" /></el-form-item>
          <el-form-item label="路径前缀"><el-input v-model="deliveryForm.pathPrefix" placeholder="一般留空" /></el-form-item>
          <el-form-item label="刷新端点"><el-input v-model="deliveryForm.endpoint" placeholder="留空用厂商默认" /></el-form-item>
          <el-form-item label="资源标识"><el-input v-model="deliveryForm.resourceId" placeholder="Cloudflare Zone ID / CloudFront Distribution ID" /></el-form-item>
          <el-form-item label="AccessKey / Token"><el-input v-model="deliveryForm.accessKey" /></el-form-item>
          <el-form-item label="SecretKey"><el-input v-model="deliveryForm.secretKey" type="password" show-password /></el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="deliveryDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitDelivery">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="errorDialog" title="迁移失败对象" width="720px">
      <el-table :data="errorRows" size="small" border max-height="400">
        <el-table-column prop="objectKey" label="对象键" min-width="240" />
        <el-table-column prop="error" label="失败原因" min-width="260" />
      </el-table>
      <template #footer>
        <el-button @click="errorDialog = false">关闭</el-button>
        <el-button type="primary" @click="exportErrors">导出 CSV</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listStorageProfiles, saveStorageProfile, deleteStorageProfile, checkStorageProfile,
  listDeliveryProfiles, saveDeliveryProfile, deleteDeliveryProfile, checkDeliveryProfile,
  listStorageBindings, saveGlobalBinding, saveCompanyBinding, resetCompanyBinding,
  listMigrationJobs, createMigrationJob, runMigrationJob, cancelMigrationJob, listMigrationErrors,
  type StorageProfile, type DeliveryProfile, type StorageBinding, type MigrationJob, type MigrationError,
} from '@/api/storageProfiles'
import { listCompanies, type CompanyItem } from '@/api/tenants'

const tab = ref('storage')
const loading = ref(false)
const saving = ref(false)
const storages = ref<StorageProfile[]>([])
const deliveries = ref<DeliveryProfile[]>([])
const jobs = ref<MigrationJob[]>([])
const companies = ref<CompanyItem[]>([])
const companyBindings = ref<StorageBinding[]>([])
const globalStorageId = ref<number | undefined>(undefined)
const globalDeliveryId = ref<number | undefined>(undefined)
const boundGlobal = computed(() => !!globalStorageId.value)
const companyNames = ref<Record<number, string>>({})

const drivers = [
  { value: 'minio', label: 'MinIO / S3 兼容' },
  { value: 'oss', label: '阿里云 OSS' },
  { value: 's3', label: 'S3（腾讯云 COS 等）' },
  { value: 'local', label: '本地磁盘' },
]
const providers = [
  { value: 'none', label: '不启用 CDN' },
  { value: 'aliyun', label: '阿里云 CDN' },
  { value: 'tencent', label: '腾讯云 CDN' },
  { value: 'qiniu', label: '七牛云 CDN' },
  { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'cloudfront', label: 'AWS CloudFront' },
]

const emptyStorage = () => ({
  id: 0, name: '', driver: 'minio', endpoint: '', bucket: '', region: '', baseUrl: '',
  pathStyle: false, hostHeader: '', useSsl: true, localDir: '', publicRead: true,
  accessKey: '', secretKey: '', accessKeyMasked: '', builtin: false,
})
const emptyDelivery = () => ({
  id: 0, name: '', provider: 'aliyun', domain: '', pathPrefix: '', endpoint: '',
  resourceId: '', accessKey: '', secretKey: '', accessKeyMasked: '',
})

const storageDialog = ref(false)
const storageForm = reactive(emptyStorage())
const deliveryDialog = ref(false)
const deliveryForm = reactive(emptyDelivery())
const errorDialog = ref(false)
const errorRows = ref<MigrationError[]>([])
const newJob = reactive({ tenantId: 0, sourceProfileId: 0, targetProfileId: 0, dryRun: false, autoSwitch: false })

function statusTag(s: string) {
  return s === 'ok' ? 'success' : s === 'error' ? 'danger' : 'info'
}
function statusText(s: string) {
  return s === 'ok' ? '已验证' : s === 'error' ? '失败' : '未自检'
}
function profileName(id: number) {
  return storages.value.find((s) => s.id === id)?.name || `#${id}`
}
function percent(row: any) {
  return row.total ? Math.min(100, Math.round((row.done / row.total) * 100)) : 0
}

async function load() {
  loading.value = true
  try {
    const [ss, ds, bs, js, cs] = await Promise.all([
      listStorageProfiles(), listDeliveryProfiles(), listStorageBindings(),
      listMigrationJobs(), listCompanies({ page: 1, size: 200 }),
    ])
    storages.value = ss
    deliveries.value = ds
    jobs.value = js
    companies.value = cs.list || []
    companyNames.value = companies.value.reduce((m, c) => {
      m[c.id] = c.name
      return m
    }, {} as Record<number, string>)
    const g: any = bs.global
    globalStorageId.value = g?.storageProfileId || undefined
    globalDeliveryId.value = g?.deliveryProfileId || undefined
    companyBindings.value = bs.companies || []
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openStorageDialog(row: any | null) {
  Object.assign(storageForm, emptyStorage(), row || {})
  storageForm.accessKey = ''
  storageForm.secretKey = ''
  storageDialog.value = true
}
async function submitStorage() {
  if (!storageForm.name.trim()) return ElMessage.warning('请填写档案名')
  saving.value = true
  try {
    await saveStorageProfile(storageForm.id || null, { ...storageForm })
    ElMessage.success('已保存')
    storageDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}
async function checkStorage(row: any) {
  const r = await checkStorageProfile(row.id)
  ElMessage[r.ok ? 'success' : 'warning'](r.ok ? r.detail : r.error)
  await load()
}
async function removeStorage(row: any) {
  await ElMessageBox.confirm(`删除「${row.name}」？仍被绑定时会被拒绝。`, '确认删除', { type: 'warning' })
  await deleteStorageProfile(row.id)
  ElMessage.success('已删除')
  await load()
}

function openDeliveryDialog(row: any | null) {
  Object.assign(deliveryForm, emptyDelivery(), row || {})
  deliveryForm.accessKey = ''
  deliveryForm.secretKey = ''
  deliveryDialog.value = true
}
async function submitDelivery() {
  if (!deliveryForm.name.trim()) return ElMessage.warning('请填写档案名')
  saving.value = true
  try {
    await saveDeliveryProfile(deliveryForm.id || null, { ...deliveryForm })
    ElMessage.success('已保存')
    deliveryDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}
async function checkDelivery(row: any) {
  const r = await checkDeliveryProfile(row.id)
  ElMessage[r.ok ? 'success' : 'warning'](r.ok ? r.detail : r.error)
  await load()
}
async function removeDelivery(row: any) {
  await ElMessageBox.confirm(`删除「${row.name}」？仍被绑定时会被拒绝。`, '确认删除', { type: 'warning' })
  await deleteDeliveryProfile(row.id)
  ElMessage.success('已删除')
  await load()
}

async function saveGlobal() {
  if (!globalStorageId.value) return ElMessage.warning('请选择存储档案')
  saving.value = true
  try {
    await saveGlobalBinding(globalStorageId.value, globalDeliveryId.value || 0)
    ElMessage.success('全局默认已切换，下一个请求即生效')
    await load()
  } finally {
    saving.value = false
  }
}
async function saveCompany(row: any) {
  if (!row.storageProfileId) return ElMessage.warning('请选择存储档案')
  await saveCompanyBinding(row.tenantId, row.storageProfileId, row.deliveryProfileId || 0)
  ElMessage.success('已保存')
  await load()
}
async function resetCompany(row: any) {
  await resetCompanyBinding(row.tenantId)
  ElMessage.success('已恢复继承全局')
  await load()
}

async function createJob() {
  if (!newJob.sourceProfileId || !newJob.targetProfileId) return ElMessage.warning('请选择源与目标档案')
  if (newJob.sourceProfileId === newJob.targetProfileId) return ElMessage.warning('源与目标不能相同')
  saving.value = true
  try {
    await createMigrationJob({ ...newJob })
    ElMessage.success('任务已创建，点「启动 / 续跑」开始')
    await load()
  } finally {
    saving.value = false
  }
}
async function startJob(row: any) {
  await runMigrationJob(row.id)
  ElMessage.success('已启动，进度会自动刷新')
  await load()
}
async function stopJob(row: any) {
  await cancelMigrationJob(row.id)
  ElMessage.success('已取消')
  await load()
}
async function openErrors(row: any) {
  errorRows.value = await listMigrationErrors(row.id)
  errorDialog.value = true
}
function exportErrors() {
  const lines = ['objectKey,error', ...errorRows.value.map((e) => `"${e.objectKey.replace(/"/g, '""')}","${e.error.replace(/"/g, '""')}"`)]
  const blob = new Blob(['\ufeff' + lines.join('\n')], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'storage-migration-errors.csv'
  a.click()
  URL.revokeObjectURL(a.href)
}
</script>

<style scoped>
.storage-profiles-page { padding: 4px 0; }
.storage-tabs { margin-top: 14px; }
.tab-head { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.tab-tip { color: #606266; font-size: 13px; line-height: 1.6; }
.binding-alert, .dialog-alert { margin-bottom: 14px; }
.binding-card { border: 1px solid #ebeef5; border-radius: 6px; padding: 14px 16px; margin-bottom: 18px; }
.binding-card h4 { margin: 0 0 10px; font-size: 14px; }
.binding-form { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.binding-form label, .migration-create label { font-size: 13px; color: #606266; }
.company-title { font-size: 14px; margin: 0 0 10px; }
.migration-create { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 16px; }
.muted { color: #909399; font-size: 12px; }
.field-note { display: block; color: #909399; font-size: 12px; line-height: 1.5; }
</style>
