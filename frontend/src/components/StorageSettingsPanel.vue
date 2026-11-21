<template>
  <div class="storage-settings-panel">
    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="这里是平台兜底的启动配置，保存后需重启服务"
      :description="`决定 p/ 平台对象（素材采用 / 小程序码 / 访客头像）与尚未绑定档案的企业用什么存储，改动写进系统设置但需要重启才生效。已绑定档案的企业走「存储档案 / 绑定关系」标签里的配置，不受这里影响。多套档案、全局默认、企业绑定与历史对象迁移也都在本页其它标签。`"
    />

    <div v-if="loading" class="panel-loading">正在读取配置…</div>
    <template v-else>
      <div v-for="sec in sections" :key="sec.label" class="config-section">
        <div class="config-section-title">{{ sec.label }}</div>
        <SettingRow
          v-for="item in sec.items"
          :key="item.key"
          :id="'set-' + item.key"
          :item="item"
          :model-value="draft[item.key] ?? ''"
          :focus="depState(item.key).focus"
          :dim="depState(item.key).dim"
          @change="(key: string, value: string) => draft[key] = value"
          @clear="clearSecret"
        />
      </div>

      <div class="panel-actions">
        <el-button :disabled="!dirty" @click="reset">重置</el-button>
        <el-button type="primary" :loading="saving" :disabled="!dirty" @click="save">保存存储配置</el-button>
        <span v-if="dirty" class="dirty-tip">有 {{ changedCount }} 项改动未保存</span>
      </div>

      <el-alert
        v-if="restartPending.length"
        type="warning"
        :closable="false"
        show-icon
        title="刚保存的改动需要重启才生效"
        :description="`待生效：${restartPending.join('、')}。按你的部署方式重启后端服务（docker compose restart backend 或 systemctl restart weichuan）。`"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getSettings, updateSettings, type SettingItem } from '@/api/settings'
import SettingRow from '@/components/SettingRow.vue'
import { CDN_DEFAULT_PROVIDER, CDN_RESOURCE_PROVIDERS } from '@/utils/cdn'

// 本面板只接管系统设置里 storage / upload 两组，后端仍走 getSettings / updateSettings，
// 启动期语义不变（静态路由与上传器在 main.go 构造，改完必须重启）。
// 配置向导仍留在系统设置页里，首装时照常能配这些项——两边共用同一份 SettingRow。
const PANEL_GROUPS = new Set(['storage', 'upload'])

// 分区元数据与 Settings.vue 的 GROUP_SECTIONS.storage / .upload 同源。
// 那边还服务向导，本面板单独维护一份；改分区时两边都要改。
const SECTIONS: { label: string; keys: string[] }[] = [
  { label: '平台存储 · 存储位置', keys: ['storage_driver', 'upload_dir', 'storage_endpoint', 'storage_bucket', 'storage_access_key', 'storage_secret_key', 'storage_use_ssl', 'storage_region'] },
  { label: '平台存储 · 访问方式', keys: ['storage_public', 'upload_base_url'] },
  { label: '平台存储 · CDN 刷新（可选）', keys: ['cdn_enabled', 'cdn_provider', 'cdn_access_key_id', 'cdn_access_key_secret', 'cdn_resource_id', 'cdn_endpoint'] },
  { label: '本地镜像备份（可选）', keys: ['local_mirror_enabled', 'mirror_dir'] },
  { label: '存储配额（可选）', keys: ['storage_quota_mb'] },
  { label: '大小与类型', keys: ['upload_max_mb', 'upload_video_max_mb', 'upload_allow_ext'] },
  { label: '临时链接有效期', keys: ['presign_put_ttl_minutes', 'sign_get_ttl_minutes'] },
]

const loading = ref(true)
const saving = ref(false)
const items = ref<SettingItem[]>([])
const original = reactive<Record<string, string>>({})
const draft = reactive<Record<string, string>>({})
const cleared = reactive<Record<string, boolean>>({})
const restartPending = ref<string[]>([])

// 从内置档案行的「跟随系统设置 →」跳来时带 ?focus=<key>，加载完滚到对应行。
// 面板不按 depState 隐藏项（只置灰），所以云驱动下 upload_dir 也找得到，不会扑空。
const focusKey = (() => {
  if (typeof window === 'undefined') return ''
  try {
    return new URLSearchParams(window.location.search).get('focus')?.trim() || ''
  } catch {
    return ''
  }
})()

const sections = computed(() => SECTIONS
  .map((s) => ({
    label: s.label,
    items: s.keys
      .map((k) => items.value.find((i) => i.key === k))
      .filter((it): it is SettingItem => !!it),
  }))
  .filter((s) => s.items.length > 0))

const changedKeys = computed(() => items.value
  .filter((it) => !it.readOnly && (cleared[it.key] || draft[it.key] !== original[it.key]))
  .map((it) => it.key))
const dirty = computed(() => changedKeys.value.length > 0)
const changedCount = computed(() => changedKeys.value.length)

// 依赖联动：与 Settings.vue 的 depState 同规则，向导和本面板切驱动时行为一致。
const driverVal = computed(() => (draft.storage_driver || '').trim() || 'local')
const cdnOn = computed(() => (draft.cdn_enabled || '') === 'true')
const cdnProvider = computed(() => (draft.cdn_provider || '').trim() || CDN_DEFAULT_PROVIDER)
const cdnNeedsResource = computed(() => CDN_RESOURCE_PROVIDERS.includes(cdnProvider.value))
const S3_COMPAT_ONLY = ['storage_use_ssl', 'storage_region']
const CLOUD_ONLY = ['storage_endpoint', 'storage_bucket', 'storage_access_key', 'storage_secret_key', 'upload_base_url']
const CDN_ONLY = ['cdn_provider', 'cdn_access_key_id', 'cdn_access_key_secret', 'cdn_resource_id', 'cdn_endpoint']

function depState(key: string) {
  if (key === 'upload_dir') {
    return { focus: driverVal.value === 'local', dim: driverVal.value !== 'local' }
  }
  if (S3_COMPAT_ONLY.includes(key)) {
    return { focus: driverVal.value === 'minio' || driverVal.value === 's3', dim: driverVal.value !== 'minio' && driverVal.value !== 's3' }
  }
  if (CLOUD_ONLY.includes(key)) {
    return { focus: driverVal.value !== 'local', dim: driverVal.value === 'local' }
  }
  if (CDN_ONLY.includes(key)) {
    if (!cdnOn.value) return { focus: false, dim: true }
    if (key === 'cdn_access_key_id') {
      return { focus: cdnProvider.value !== 'cloudflare', dim: cdnProvider.value === 'cloudflare' }
    }
    if (key === 'cdn_resource_id') {
      return { focus: cdnNeedsResource.value, dim: !cdnNeedsResource.value }
    }
    return { focus: true, dim: false }
  }
  return { focus: false, dim: false }
}

// 与 Settings.vue 的 DESTRUCTIVE 中存储相关三项同源：改错会让已存文件失联。
const DESTRUCTIVE: Record<string, string> = {
  storage_driver: '存储方式变更后，按原方式上传的历史文件可能无法访问（请先确认历史文件仍可访问）',
  upload_dir: '本地上传目录变更后，旧目录中的已有文件将不再可见',
  storage_public: '存储空间公开访问变更会影响所有已生成文件链接的可访问范围',
}

function destructiveWarnings(): string[] {
  const warns: string[] = []
  changedKeys.value.forEach((k) => {
    const it = items.value.find((i) => i.key === k)
    if (!it) return
    const label = it.name || k
    if (it.secret && cleared[k]) {
      warns.push(`「${label}」将被清空并回退环境变量；若部署环境没有对应变量，相关功能会直接失效`)
    } else if (DESTRUCTIVE[k]) {
      warns.push(`「${label}」：${DESTRUCTIVE[k]}`)
    }
  })
  return warns
}

async function load() {
  loading.value = true
  try {
    const r = await getSettings()
    if (r.code === 0 && r.data) {
      items.value = r.data.filter((it) => PANEL_GROUPS.has(it.group))
      items.value.forEach((it) => {
        // 敏感项界面留空 = 不修改；只读项直接显示掩码，让管理员看得到「已配置」。
        original[it.key] = it.secret && !it.readOnly ? '' : it.value
        draft[it.key] = original[it.key]
        cleared[it.key] = false
      })
    }
  } finally {
    loading.value = false
    if (focusKey) {
      nextTick(() => {
        const el = document.getElementById('set-' + focusKey)
        if (el && typeof el.scrollIntoView === 'function') {
          el.scrollIntoView({ behavior: 'smooth', block: 'center' })
        }
      })
    }
  }
}

function clearSecret(k: string) {
  draft[k] = ''
  cleared[k] = true
}

function reset() {
  items.value.forEach((it) => {
    draft[it.key] = original[it.key]
    cleared[it.key] = false
  })
  restartPending.value = []
}

async function save() {
  if (!dirty.value) return
  const warns = destructiveWarnings()
  if (warns.length > 0) {
    try {
      await ElMessageBox.confirm(
        `以下更改可能影响已有数据：${warns.join('；')}。确认保存吗？`,
        '破坏性更改确认',
        { type: 'warning', confirmButtonText: '确认保存', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }
  saving.value = true
  const payload: Record<string, string> = {}
  changedKeys.value.forEach((k) => { payload[k] = cleared[k] ? '' : draft[k] })
  const savedCount = Object.keys(payload).length
  try {
    const r = await updateSettings(payload)
    Object.keys(payload).forEach((k) => {
      const wasCleared = cleared[k]
      cleared[k] = false
      const it = items.value.find((x) => x.key === k)
      if (it) it.configured = !wasCleared
      original[k] = it?.secret ? '' : payload[k]
      draft[k] = original[k]
    })
    restartPending.value = (r?.data?.restartKeys || [])
      .map((k: string) => items.value.find((i) => i.key === k)?.name || k)
    ElMessage.success(
      `已保存 ${savedCount} 项存储配置${restartPending.value.length ? '，需重启服务后生效' : ''}`,
    )
  } catch {
    // 失败保留草稿，方便修正后重试
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.storage-settings-panel { padding: 4px 0 8px; }
.panel-loading { padding: 24px 0; color: #909399; font-size: 13px; }
.config-section { margin-top: 14px; }
.config-section-title {
  font-size: 13px; font-weight: 600; color: #303133;
  padding: 6px 0 8px; border-bottom: 1px solid #ebeef5;
}
.panel-actions {
  display: flex; align-items: center; gap: 12px;
  position: sticky; bottom: 0; margin-top: 18px; padding: 12px 0;
  background: #fff; border-top: 1px solid #ebeef5;
}
.dirty-tip { font-size: 12px; color: #e6a23c; }
</style>
