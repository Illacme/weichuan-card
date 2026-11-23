<template>
  <div class="storage-overview">
    <!-- 默认视图只有三样东西：一句状态、一张三层小图、一个动作。
         流程 / 官方地址 / 名词解释全部收进按需展开区，避免把手册摊在首页。 -->
    <section class="ov-hero">
      <div class="ov-hero-text">
        <span class="ov-kicker">当前文件存放状态</span>
        <h3>{{ headline }}</h3>
        <p>{{ subline }}</p>
      </div>
      <div class="ov-hero-action">
        <el-button type="primary" size="large" @click="emit('goto', 'storage')">{{ actionText }}</el-button>
        <span v-if="isDefaultLocal" class="ov-hint">不接云也能正常使用</span>
      </div>
    </section>

    <section class="ov-flow" aria-label="整体架构">
      <div class="ov-node">
        <span>访客</span>
        <strong>手机 / 浏览器</strong>
      </div>
      <div class="ov-arrow">读取 ↓</div>
      <div class="ov-node" :class="cdnActive ? 'ov-node-on' : 'ov-node-off'">
        <span>CDN · 可选</span>
        <strong>{{ cdnLabel }}</strong>
      </div>
      <div class="ov-arrow">回源 ↓</div>
      <div class="ov-node ov-node-storage">
        <span>存储 · 必有</span>
        <strong>{{ storageLabel }}</strong>
      </div>
    </section>
    <p class="ov-flow-note">上传方向相反：后台写文件 → 存储；只有开了 CDN 才需要经过 CDN 到访客。</p>

    <div class="ov-help-bar">
      <span>要接云平台？展开看配置步骤、各家官方地址和名词解释。</span>
      <button class="ov-toggle" type="button" :aria-expanded="showHelp ? 'true' : 'false'" @click="showHelp = !showHelp">
        {{ showHelp ? '收起帮助 ↑' : '展开帮助 ↓' }}
      </button>
    </div>

    <div v-if="showHelp" class="ov-help">
      <section class="ov-help-section">
        <h4>配置步骤</h4>
        <div v-for="step in SETUP_STEPS" :key="step.no" class="ov-step">
          <span class="ov-step-no">{{ step.no }}</span>
          <span class="ov-step-name">{{ step.title }}</span>
          <span class="ov-step-when">{{ step.when }}</span>
          <button type="button" class="ov-step-link" @click="emit('goto', step.tab)">{{ step.action }} →</button>
        </div>
        <p class="ov-default-note">{{ DEFAULT_FACTS[0] }}</p>
      </section>

      <section class="ov-help-section">
        <h4>云平台官方地址</h4>
        <p class="ov-help-sub">点开是对应厂商的控制台或文档；在对应页签填配置项时，输入框旁的「?」还会告诉你这个值去哪拿。</p>
        <div v-for="guide in storageVendors" :key="guide.key" class="ov-vendor">
          <strong>{{ guide.label }}</strong>
          <span>{{ guide.summary }}</span>
          <a v-for="link in guide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a>
        </div>
        <div v-for="guide in cdnVendors" :key="guide.key" class="ov-vendor">
          <strong>{{ guide.label }}（CDN）</strong>
          <span>{{ guide.summary }}</span>
          <a v-for="link in guide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a>
        </div>
        <table class="ov-s3-table">
          <thead><tr><th>S3 兼容服务商</th><th>服务地址示例</th></tr></thead>
          <tbody>
            <tr v-for="v in S3_VARIANTS" :key="v.label"><td>{{ v.label }}</td><td><code>{{ v.endpoint }}</code></td></tr>
          </tbody>
        </table>
        <p class="ov-help-sub">示例地址以控制台显示为准，不带 <code>https://</code>。</p>
      </section>

      <section class="ov-help-section">
        <h4>名词解释</h4>
        <div v-for="item in STORAGE_GLOSSARY" :key="item.term" class="ov-glossary-item">
          <strong>{{ item.term }}</strong><span>{{ item.meaning }}</span>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { StorageProfile, DeliveryProfile, StorageBinding } from '@/api/storageProfiles'
import {
  DEFAULT_FACTS, SETUP_STEPS, STORAGE_GLOSSARY,
  DRIVER_GUIDES, CDN_GUIDES, S3_VARIANTS, driverGuide, cdnGuide,
} from '@/utils/storageGuides'

const props = defineProps<{
  platformProfile?: StorageProfile | null
  platformDelivery?: DeliveryProfile | null
  globalBinding?: StorageBinding | null
}>()

const emit = defineEmits<{ (e: 'goto', tab: string): void }>()

// 默认收起：需要细节的人自己展开，不需要的人第一屏就看完了
const showHelp = ref(false)

const driver = computed(() => (props.platformProfile?.driver || '').trim())
const provider = computed(() => (props.platformDelivery?.provider || '').trim())
const isDefaultLocal = computed(() => !driver.value || driver.value === 'local')
const cdnActive = computed(() => !!provider.value && provider.value !== 'none')

const storageLabel = computed(() => {
  if (!driver.value) return '读取中…'
  if (driver.value === 'local') return '服务器本地磁盘'
  return driverGuide(driver.value)?.label || driver.value
})
const cdnLabel = computed(() =>
  cdnActive.value ? (cdnGuide(provider.value)?.label || provider.value) : '未启用',
)
const headline = computed(() =>
  `文件存在${storageLabel.value}，CDN ${cdnLabel.value}`,
)
const subline = computed(() =>
  isDefaultLocal.value
    ? '这是系统默认配置，不接任何云账号也能正常使用。只有磁盘不够或访客访问慢时才需要改动。'
    : '后台上传的文件写到这里，访客按上面的路径读取。要换存储或加速方式，去「文件存储」或「CDN 分发」页改。',
)
const actionText = computed(() => (isDefaultLocal.value ? '我要接入云平台' : '管理文件存储'))

const storageVendors = Object.values(DRIVER_GUIDES).filter((g) => g.key !== 'local')
const cdnVendors = Object.values(CDN_GUIDES).filter((g) => g.key !== 'none')
</script>

<style scoped>
.storage-overview { color: #303133; }
.ov-hero { display: flex; align-items: center; justify-content: space-between; gap: 24px; padding: 22px 24px; border: 1px solid #d9e8fb; border-radius: 10px; background: linear-gradient(180deg, #f5f9ff, #fff); }
.ov-kicker { color: #909399; font-size: 12px; letter-spacing: 1px; }
.ov-hero h3 { margin: 6px 0; font-size: 20px; line-height: 1.45; }
.ov-hero p { margin: 0; color: #606266; font-size: 13px; line-height: 1.7; max-width: 620px; }
.ov-hero-action { display: flex; flex-direction: column; align-items: center; gap: 8px; flex: none; }
.ov-hint { color: #909399; font-size: 12px; }
.ov-flow { display: flex; align-items: center; justify-content: center; gap: 10px; margin: 22px 0 6px; flex-wrap: wrap; }
.ov-node { display: flex; flex-direction: column; align-items: center; gap: 4px; min-width: 128px; padding: 12px 16px; border: 1px solid #e4e7ed; border-radius: 8px; background: #f8f9fb; text-align: center; }
.ov-node span { color: #909399; font-size: 11px; }
.ov-node strong { font-size: 14px; }
.ov-node-on { border-color: #b6d8fb; background: #f0f7ff; }
.ov-node-on span { color: #409eff; }
.ov-node-off { opacity: 0.75; }
.ov-node-storage { border-color: #b7e0c3; background: #f2fbf5; }
.ov-node-storage span { color: #4a9a63; }
.ov-arrow { color: #a8abb2; font-size: 12px; }
.ov-flow-note { margin: 0; text-align: center; color: #909399; font-size: 12px; }
.ov-help-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 24px; padding: 10px 14px; border: 1px solid #ebeef5; border-radius: 6px; color: #606266; font-size: 13px; }
.ov-toggle { border: 0; background: transparent; color: #409eff; font-size: 13px; cursor: help; }
.ov-help { margin-top: 10px; padding: 4px 18px 18px; border: 1px solid #ebeef5; border-radius: 6px; }
.ov-help-section { padding-top: 14px; }
.ov-help-section h4 { margin: 0 0 10px; font-size: 14px; }
.ov-help-sub { margin: 0 0 10px; color: #909399; font-size: 12px; line-height: 1.7; }
.ov-step { display: flex; align-items: center; gap: 10px; padding: 7px 0; border-bottom: 1px solid #f2f4f7; font-size: 13px; }
.ov-step-no { flex: none; width: 20px; height: 20px; border-radius: 50%; background: #409eff; color: #fff; font-size: 11px; line-height: 20px; text-align: center; }
.ov-step-name { font-weight: 600; }
.ov-step-when { color: #909399; font-size: 12px; }
.ov-step-link { margin-left: auto; border: 0; background: transparent; color: #409eff; font-size: 12px; cursor: pointer; white-space: nowrap; }
.ov-default-note { margin: 10px 0 0; color: #606266; font-size: 12px; }
.ov-vendor { padding: 8px 0; border-bottom: 1px dashed #ebeef5; font-size: 12px; }
.ov-vendor strong { margin-right: 10px; }
.ov-vendor span { margin-right: 10px; color: #606266; }
.ov-vendor a { margin-right: 12px; color: #409eff; text-decoration: none; white-space: nowrap; }
.ov-vendor a:hover { text-decoration: underline; }
.ov-s3-table { width: 100%; margin: 12px 0 6px; border-collapse: collapse; font-size: 12px; }
.ov-s3-table th, .ov-s3-table td { padding: 6px 8px; border: 1px solid #ebeef5; text-align: left; color: #606266; }
.ov-s3-table th { background: #f8f9fb; }
.ov-glossary-item { display: flex; gap: 10px; padding: 4px 0; font-size: 12px; line-height: 1.7; }
.ov-glossary-item strong { flex: none; width: 68px; }
.ov-glossary-item span { color: #606266; }
@media (max-width: 900px) {
  .ov-hero { flex-direction: column; align-items: flex-start; }
  .ov-hero-action { align-items: flex-start; }
}
</style>
