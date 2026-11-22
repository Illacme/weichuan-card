<template>
  <el-card>
    <div class="page-bar">
      <h3>CDN 刷新</h3>
      <!-- 当前生效厂商：值来自「分发档案 → 平台自身 CDN」（迁移 0060 之前是系统设置的
           cdn_provider）。没配时后端按 aliyun 回落，这里如实标注，避免「没配过怎么会
           是阿里云」的困惑 -->
      <el-tag v-if="providerTag" size="small" effect="plain">{{ providerTag }}</el-tag>
    </div>

    <!-- 配不全就提交只会得到一句「CDN 刷新未配置」——先在这儿点名缺口，省一次往返 -->
    <el-alert
      v-if="blockReason"
      type="warning"
      :closable="false"
      :title="blockReason"
      style="margin-bottom: 14px"
    >
      <el-button size="small" @click="goSettings">去设置</el-button>
    </el-alert>

    <el-alert
      type="info"
      :closable="false"
      title="覆盖同名文件的操作（素材采用、小程序码重生成）会自动刷新，这里是手动兜底：直接在对象存储里换了文件、或想让边缘立刻失效时用。支持阿里云 / 腾讯云 / 七牛云 / Cloudflare / AWS CloudFront，平台对象的厂商与凭据在「存储与分发 → 分发档案 → 平台自身 CDN」里配（首次部署也可用环境变量播种），改完需重启服务。条数超过厂商单次上限时会自动分批提交。"
      style="margin-bottom: 14px"
    />

    <!-- 当前平台的官方入口：刷新失败时第一反应应该是"去控制台看状态"，而不是无从下手 -->
    <div v-if="guideInfo" class="refresh-guide">
      <strong>{{ guideInfo.label }} · 官方入口</strong>
      <div class="refresh-links">
        <a v-for="link in guideInfo.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a>
      </div>
      <p>{{ guideInfo.credential }}</p>
    </div>

    <el-input
      v-model="urls"
      type="textarea"
      :rows="8"
      placeholder="每行一个待刷新的 URL（文件或目录），例如：&#10;https://cdn.example.com/uploads/abc.png&#10;https://cdn.example.com/uploads/def.jpg"
    />

    <div class="actions">
      <el-button type="primary" :loading="loading" @click="refresh">提交刷新</el-button>
      <span class="count">共 {{ urlList.length }} 条</span>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { cdnRefresh } from '@/api/storage'
import { listDeliveryProfiles, type DeliveryProfile } from '@/api/storageProfiles'
import {
  CDN_NO_AK_PROVIDERS,
  CDN_RESOURCE_PROVIDERS,
  cdnProviderLabel,
} from '@/utils/cdn'
import { cdnGuide } from '@/utils/storageGuides'

const router = useRouter()
const urls = ref('')
const loading = ref(false)
const profiles = ref<DeliveryProfile[]>([])
const loaded = ref(false)

onMounted(async () => {
  try {
    profiles.value = (await listDeliveryProfiles()) ?? []
  } catch {
    // 状态条是锦上添花：拿不到档案就只留通用说明，不能因此挡住刷新入口
  } finally {
    loaded.value = true
  }
})

// 平台对象的刷新凭据只有一处真值源：分发档案里 purpose=platform_primary 那一行
// （迁移 0060 之前是系统设置的 cdn_* 一组）。这里只读，改去档案页。
const platform = computed<DeliveryProfile | null>(
  () => profiles.value.find((p) => p.purpose === 'platform_primary') || null,
)
const provider = computed(() => (platform.value?.provider || '').trim())
const providerTag = computed(() => {
  // 档案还没拉到时不显示：否则会先闪一下「按默认（aliyun）」，像是配过
  if (!loaded.value || !platform.value) return ''
  if (!provider.value) return '未配置（不刷新）'
  return `${provider.value}（${cdnProviderLabel(provider.value)}）`
})
const enabled = computed(() => !!provider.value && provider.value !== 'none')
const guideInfo = computed(() => (enabled.value ? cdnGuide(provider.value) : null))
// 还缺哪几项：与后端 storage.CDNProviderNeeds* 同源（utils/cdn 是界面侧同一份事实）
const missingKeys = computed(() => {
  const dp = platform.value
  if (!dp) return []
  const need: string[] = []
  if (!CDN_NO_AK_PROVIDERS.includes(provider.value) && !dp.accessKeyConfigured) need.push('AccessKey')
  if (!dp.secretKeyConfigured) need.push(provider.value === 'cloudflare' ? 'API Token' : 'SecretKey')
  if (CDN_RESOURCE_PROVIDERS.includes(provider.value) && !dp.resourceId) need.push('资源标识')
  return need
})
const blockReason = computed(() => {
  // 设置还没拉到（或拉取失败）时不下判断：宁可不提示，也不误报「未启用」
  if (!loaded.value) return ''
  if (!platform.value) {
    return '还没读到「平台自身 CDN」档案（旧版本可能尚未生成）。请到「存储与分发 → 分发档案」确认那一行存在，保存后重启服务。'
  }
  if (!enabled.value) {
    return 'CDN 刷新当前未启用，提交会被拒绝。请到「存储与分发 → 分发档案 → 平台自身 CDN」选好厂商并填全它要求的凭据，保存后重启服务。'
  }
  if (missingKeys.value.length) {
    return `当前厂商 ${provider.value} 还缺 ${missingKeys.value.join('、')}，提交会被拒绝。请到「存储与分发 → 分发档案 → 平台自身 CDN」补齐后重启服务。`
  }
  return ''
})

function goSettings() {
  // 直达档案页的「分发档案」标签：CDN 凭据只在那里配
  router.push('/platform/storage-profiles?tab=delivery')
}

const urlList = computed(() =>
  urls.value
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
)

async function refresh() {
  if (!urlList.value.length) {
    ElMessage.warning('请至少填写一个 URL')
    return
  }
  loading.value = true
  try {
    await cdnRefresh(urlList.value)
    ElMessage.success(`已提交刷新 ${urlList.value.length} 条`)
  } catch {
    // 拦截器已提示（如 CDN 未配置）
  } finally {
    loading.value = false
  }
}
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
.actions {
  margin-top: 14px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.count {
  color: #909399;
  font-size: 13px;
}
.refresh-guide { margin-bottom: 14px; padding: 12px 14px; border: 1px solid #d9e8fb; border-radius: 6px; background: #f5f9ff; }
.refresh-guide strong { font-size: 13px; }
.refresh-links { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 8px; }
.refresh-links a { color: #409eff; font-size: 12px; text-decoration: none; }
.refresh-links a:hover { text-decoration: underline; }
.refresh-guide p { margin: 9px 0 0; color: #606266; font-size: 12px; line-height: 1.7; }
</style>
