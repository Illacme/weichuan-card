<template>
  <div class="storage-settings-panel">
    <!-- 顶部先回答管理员唯一关心的问题：文件现在到底存在哪。
         技术键名、类型、重启徽标都收进「高级设置」，默认不占版面。 -->
    <div class="hero-card">
      <div class="hero-copy">
        <div class="hero-kicker">图片 / 视频 / 小程序码存哪</div>
        <h3 class="hero-title">{{ locationTitle }}</h3>
        <p class="hero-desc">{{ locationDesc }}</p>
        <p class="hero-note">
          连接信息（驱动 / 桶 / 凭据 / 访问基址）在「存储档案 → 平台自身存储」里改；
          CDN 刷新的厂商与凭据在「分发档案 → 平台自身 CDN」里改。两处保存后都需要重启。
          这里只管服务器目录与本地备份。
        </p>
        <div class="hero-chips">
          <span class="chip" :class="{ 'chip-on': mirrorOn }">本地备份 {{ mirrorOn ? '已开启' : '未开启' }}</span>
          <span class="chip" :class="{ 'chip-on': cdnOn }">CDN 刷新 {{ cdnText }}</span>
        </div>
      </div>
      <div class="hero-action">
        <el-button type="primary" @click="emit('goto-profiles')">去存储档案修改</el-button>
        <el-button link type="primary" @click="emit('goto-deliveries')">改 CDN 刷新凭据</el-button>
        <span class="hero-action-note">改桶 / 密钥 / 访问地址</span>
      </div>
    </div>

    <div v-if="loading" class="panel-loading">正在读取配置…</div>
    <template v-else>
      <div class="simple-card">
        <div class="simple-card-head">
          <h4>服务器上的目录</h4>
          <p>只有用「服务器本地磁盘」时才需要关心；改完必须重启。</p>
        </div>
        <SettingRow
          v-for="item in basicItems"
          :key="item.key"
          :id="'set-' + item.key"
          :item="item"
          :model-value="draft[item.key] ?? ''"
          compact
          @change="(key: string, value: string) => draft[key] = value"
          @clear="clearSecret"
        />
      </div>

      <div class="advanced-card">
        <button
          type="button"
          class="advanced-toggle"
          :aria-expanded="advancedOpen"
          @click="advancedOpen = !advancedOpen"
        >
          <span>{{ advancedOpen ? '收起高级设置' : '高级设置（通常不用改）' }}</span>
          <span class="chev">{{ advancedOpen ? '−' : '+' }}</span>
        </button>
        <p v-if="!advancedOpen" class="advanced-hint">
          存储配额、上传大小与临时链接时效都在这里；不确定就保持默认。CDN 刷新凭据不在这里，见顶部「改 CDN 刷新凭据」。
        </p>
        <div v-else>
          <div v-for="sec in advancedSections" :key="sec.label" class="config-section">
            <div class="config-section-title">{{ sec.label }}</div>
            <SettingRow
              v-for="item in sec.items"
              :key="item.key"
              :id="'set-' + item.key"
              :item="item"
              :model-value="draft[item.key] ?? ''"
              compact
              @change="(key: string, value: string) => draft[key] = value"
              @clear="clearSecret"
            />
          </div>
        </div>
      </div>

      <div class="panel-actions">
        <el-button :disabled="!dirty" @click="reset">重置</el-button>
        <el-button type="primary" :loading="saving" :disabled="!dirty" @click="save">保存设置</el-button>
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
import { cdnProviderLabel } from '@/utils/cdn'

// 顶部摘要只需要回答「文件实际存在哪」，因此只要这四个字段；
// 父级直接传完整 StorageProfile 也兼容，测试里传最小对象也不必造 19 个字段。
interface PlatformLocation {
  driver: string
  endpoint?: string
  bucket?: string
  baseUrl?: string
}

const props = defineProps<{
  /** 平台自身存储档案；父级已加载时用来在顶部显示「实际存在哪」 */
  platformProfile?: PlatformLocation | null
  /** 平台自身 CDN 档案（分发档案里 purpose=platform_primary 那一行） */
  platformDelivery?: { provider?: string } | null
}>()
const emit = defineEmits<{
  (e: 'goto-profiles'): void
  (e: 'goto-deliveries'): void
}>()

// 本面板只接管系统设置里 storage / upload 两组，后端仍走 getSettings / updateSettings，
// 启动期语义不变（静态路由与上传器在 main.go 构造，改完必须重启）。
// 平台连接信息已进「存储档案」（迁移 0059），这里只剩部署期目录与策略项。
const PANEL_GROUPS = new Set(['storage', 'upload'])

// 常用区只留三件事：本地目录、备份目录、自动备份开关。
// 指针（platform_storage_profile_id）由系统自动维护，不给管理员看，也不允许改。
const BASIC_KEYS = ['upload_dir', 'mirror_dir', 'local_mirror_enabled']

// 高级区默认收起：CDN / 配额 / 上传限制 / 临时链接，都是「配一次就不动」的项。
const ADVANCED_SECTIONS: { label: string; keys: string[] }[] = [
  { label: '配额与上传限制', keys: ['storage_quota_mb', 'upload_max_mb', 'upload_video_max_mb', 'upload_allow_ext'] },
  { label: '临时链接有效期', keys: ['presign_put_ttl_minutes', 'sign_get_ttl_minutes'] },
]
const ADVANCED_KEYS = new Set(ADVANCED_SECTIONS.flatMap((s) => s.keys))

// 管理员语言：不解释 key、不解释类型，只回答「这项是干什么的、改了会怎样」。
const FRIENDLY: Record<string, { name: string; desc: string }> = {
  upload_dir: {
    name: '服务器上传目录',
    desc: '选「服务器本地磁盘」时，图片 / 视频 / 小程序码保存在这里。换目录后旧文件不会自动搬过去，必须重启。',
  },
  mirror_dir: {
    name: '本地备份目录',
    desc: '云端上传成功后，系统在这里留一份副本。不要放在上传目录下面；改完要重启。',
  },
  local_mirror_enabled: {
    name: '自动本地备份',
    desc: '打开后，云端文件会自动在服务器另存一份；每家企业还要在「企业管理」里单独打开。',
  },
  storage_quota_mb: {
    name: '企业存储配额',
    desc: '每家企业最多能存多少 MB；0 表示不限制。',
  },
  upload_max_mb: {
    name: '单张图片上限',
    desc: '单位 MB。超过这个大小的图片会被拒绝上传。',
  },
  upload_video_max_mb: {
    name: '单个视频上限',
    desc: '单位 MB。视频通常比图片大，单独设置。',
  },
  upload_allow_ext: {
    name: '允许上传的文件类型',
    desc: '逗号分隔，例如 png,jpg,webp；不在列表里的类型会被拒绝。',
  },
  presign_put_ttl_minutes: {
    name: '直传链接有效期',
    desc: '浏览器直接上传到云存储时，链接多少分钟内有效。',
  },
  sign_get_ttl_minutes: {
    name: '私有桶下载链接有效期',
    desc: '只有关闭公开读时才用到；填 0 用默认值。',
  },
}

const DRIVER_LABELS: Record<string, string> = {
  local: '服务器本地磁盘',
  minio: 'MinIO / 自建 S3',
  oss: '阿里云 OSS',
  s3: 'S3 兼容云',
}

const loading = ref(true)
const saving = ref(false)
const items = ref<SettingItem[]>([])
const original = reactive<Record<string, string>>({})
const draft = reactive<Record<string, string>>({})
const cleared = reactive<Record<string, boolean>>({})
const restartPending = ref<string[]>([])

// 从内置档案行的「跟随系统设置 →」跳来时带 ?focus=<key>，加载完滚到对应行。
// 如果目标在高级区，自动展开，避免「点了链接却看不到字段」。
const focusKey = (() => {
  if (typeof window === 'undefined') return ''
  try {
    return new URLSearchParams(window.location.search).get('focus')?.trim() || ''
  } catch {
    return ''
  }
})()
const advancedOpen = ref(ADVANCED_KEYS.has(focusKey))

function friendly(item: SettingItem): SettingItem {
  const f = FRIENDLY[item.key]
  return f ? { ...item, name: f.name, description: f.desc } : item
}

function pick(keys: string[]): SettingItem[] {
  return keys
    .map((k) => items.value.find((i) => i.key === k))
    .filter((it): it is SettingItem => !!it)
    .map(friendly)
}

const basicItems = computed(() => pick(BASIC_KEYS))
const advancedSections = computed(() => ADVANCED_SECTIONS
  .map((s) => ({ label: s.label, items: pick(s.keys) }))
  .filter((s) => s.items.length > 0))

const changedKeys = computed(() => items.value
  .filter((it) => !it.readOnly && (cleared[it.key] || draft[it.key] !== original[it.key]))
  .map((it) => it.key))
const dirty = computed(() => changedKeys.value.length > 0)
const changedCount = computed(() => changedKeys.value.length)

const mirrorOn = computed(() => (draft.local_mirror_enabled || '') === 'true')
// CDN 状态来自分发档案，不再来自本面板的 draft：凭据只有一处真值源。
const cdnProvider = computed(() => (props.platformDelivery?.provider || '').trim())
const cdnOn = computed(() => cdnProvider.value !== '' && cdnProvider.value !== 'none')
const cdnText = computed(() => {
  if (!props.platformDelivery) return '未配置'
  if (!cdnOn.value) return '已关闭'
  return `已开启 · ${cdnProviderLabel(cdnProvider.value)}`
})

const locationTitle = computed(() => {
  const p = props.platformProfile
  // 父级档案列表与设置项各自加载：设置项已回来却仍没有档案，说明档案列表没读到，
  // 不能一直显示「正在读取」，否则管理员会以为页面卡死。
  if (!p) return loading.value ? '正在读取存储位置…' : '暂时读不到存储位置'
  if (p.driver === 'local') return '文件存在服务器本地磁盘'
  const label = DRIVER_LABELS[p.driver] || p.driver || '云存储'
  return `文件存在${label}${p.bucket ? ` · ${p.bucket}` : ''}`
})

const locationDesc = computed(() => {
  const p = props.platformProfile
  if (!p) return '请到「存储档案 → 平台自身存储」核对驱动、桶与访问基址。'
  if (p.driver === 'local') {
    return `实际目录是 ${draft.upload_dir || '（未设置）'}；网站通过 /uploads 直接读取。`
  }
  return `写入 ${p.endpoint || '未填端点'} 的 ${p.bucket || '未填桶名'}，对外地址 ${p.baseUrl || '未填访问基址'}。`
})

// 与 Settings.vue 的 DESTRUCTIVE 同源：改错会让已存文件失联。
const DESTRUCTIVE: Record<string, string> = {
  upload_dir: '本地上传目录变更后，旧目录中的已有文件将不再可见',
  mirror_dir: '本地镜像目录变更后，旧目录中的备份副本将不再被自动纳入',
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
      `已保存 ${savedCount} 项设置${restartPending.value.length ? '，需重启服务后生效' : ''}`,
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

/* 顶部摘要：先给结论，再给入口。不要一上来就是 15 行配置。 */
.hero-card {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 18px 20px;
  border: 1px solid #d9ecff;
  border-radius: 10px;
  background: linear-gradient(135deg, #f4f9ff 0%, #ffffff 70%);
}
.hero-copy { min-width: 0; flex: 1; }
.hero-kicker { font-size: 12px; color: #409eff; font-weight: 600; letter-spacing: 0.02em; }
.hero-title { margin: 6px 0 6px; font-size: 19px; line-height: 1.35; color: #1f2d3d; }
.hero-desc { margin: 0; font-size: 13px; line-height: 1.7; color: #303133; word-break: break-all; }
.hero-note { margin: 8px 0 0; font-size: 12px; line-height: 1.6; color: #909399; }
.hero-chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.chip {
  display: inline-flex; align-items: center;
  padding: 3px 9px; border-radius: 999px;
  background: #f4f4f5; color: #909399; font-size: 12px;
}
.chip-on { background: #ecf5ff; color: #409eff; }
.hero-action { flex: none; display: flex; flex-direction: column; align-items: flex-end; gap: 6px; }
.hero-action-note { font-size: 12px; color: #c0c4cc; white-space: nowrap; }

.simple-card {
  margin-top: 14px;
  padding: 14px 16px 6px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #fff;
}
.simple-card-head h4 { margin: 0; font-size: 14px; color: #303133; }
.simple-card-head p { margin: 4px 0 6px; font-size: 12px; line-height: 1.6; color: #909399; }

.advanced-card { margin-top: 14px; padding-top: 4px; }
.advanced-toggle {
  display: flex; align-items: center; justify-content: space-between;
  width: 100%; padding: 10px 2px;
  border: none; border-top: 1px solid #ebeef5;
  background: transparent; color: #606266;
  font-size: 13px; cursor: pointer;
}
.advanced-toggle:hover { color: #409eff; }
.chev { font-size: 16px; line-height: 1; color: #c0c4cc; }
.advanced-hint { margin: 0 0 6px; font-size: 12px; line-height: 1.6; color: #c0c4cc; }
.config-section { margin-top: 10px; }
.config-section-title {
  font-size: 13px; font-weight: 600; color: #303133;
  padding: 6px 0 4px; border-bottom: 1px solid #f2f6fc;
}

.panel-actions {
  display: flex; align-items: center; gap: 12px;
  position: sticky; bottom: 0; margin-top: 18px; padding: 12px 0;
  background: #fff; border-top: 1px solid #ebeef5;
}
.dirty-tip { font-size: 12px; color: #e6a23c; }

@media (max-width: 767px) {
  .hero-card { flex-direction: column; }
  .hero-action { align-items: flex-start; }
}
</style>
