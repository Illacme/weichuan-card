<template>
  <div class="storage-overview">
    <!-- 结论先行：第一屏就回答"现在是什么状态"，不让人先读十个术语 -->
    <section class="ov-hero">
      <div>
        <h3>一页看清：文件存在哪、访客怎么读到</h3>
        <p class="ov-hero-sub">
          这个模块只管两件事——把文件存好、让访客读到。下面的状态是系统当前真实配置，
          不需要记字段名，看不懂的名词在页面最下方有解释。
        </p>
      </div>
      <el-tag :type="isDefaultLocal ? 'success' : 'primary'" effect="dark" size="large" class="ov-mode-tag">
        {{ isDefaultLocal ? '当前是默认配置' : '已接入云平台' }}
      </el-tag>
    </section>

    <section class="ov-status">
      <div class="ov-card">
        <span class="ov-card-label">文件存在哪</span>
        <strong class="ov-card-value">{{ storageLabel }}</strong>
        <span class="ov-card-note">{{ storageNote }}</span>
      </div>
      <div class="ov-card">
        <span class="ov-card-label">访客怎么读到</span>
        <strong class="ov-card-value">{{ cdnLabel }}</strong>
        <span class="ov-card-note">{{ cdnNote }}</span>
      </div>
      <div class="ov-card">
        <span class="ov-card-label">谁在用这套</span>
        <strong class="ov-card-value">{{ bindingLabel }}</strong>
        <span class="ov-card-note">{{ bindingNote }}</span>
      </div>
    </section>

    <el-alert
      class="ov-default-alert"
      type="success"
      :closable="false"
      show-icon
      title="默认就是本地配置：不开通任何云平台，也能完整使用"
      description="刚安装好的系统把文件写在服务器自己的磁盘上，CDN 处于关闭状态。只有磁盘不够、访客访问慢、或需要多企业隔离时，才需要往下走云平台流程。"
    />

    <!-- 架构图：三层关系用图形表达，避免只在导语里写一段话 -->
    <section class="ov-section">
      <h4 class="ov-section-title">整体架构：三层，各管各的</h4>
      <div class="ov-flow">
        <div class="ov-node ov-node-client">
          <span class="ov-node-kind">访客</span>
          <strong>手机 / 浏览器</strong>
          <span class="ov-node-desc">打开名片时读取图片和视频</span>
        </div>
        <div class="ov-flow-link">
          <span>↑ 后台上传 &nbsp; ↓ 访客读取</span>
        </div>
        <div class="ov-node" :class="cdnActive ? 'ov-node-on' : 'ov-node-off'">
          <span class="ov-node-kind">第二层 · 可选</span>
          <strong>分发（CDN）· {{ cdnLabel }}</strong>
          <span class="ov-node-desc">{{ cdnActive ? '把文件缓存到离访客更近的地方' : '未启用：访客直接从存储读取，功能一样正常' }}</span>
        </div>
        <div class="ov-flow-link">
          <span>↓ 缓存没命中时回源</span>
        </div>
        <div class="ov-node ov-node-storage">
          <span class="ov-node-kind">第一层 · 必有</span>
          <strong>存储 · {{ storageLabel }}</strong>
          <span class="ov-node-desc">所有文件的真正存放位置，默认本地磁盘</span>
        </div>
      </div>
      <ul class="ov-facts">
        <li v-for="fact in DEFAULT_FACTS" :key="fact">{{ fact }}</li>
      </ul>
    </section>

    <section class="ov-section">
      <h4 class="ov-section-title">三句话理解这套设计</h4>
      <div class="ov-points">
        <div v-for="point in ARCHITECTURE_POINTS" :key="point.title" class="ov-point">
          <strong>{{ point.title }}</strong>
          <p>{{ point.body }}</p>
        </div>
      </div>
    </section>

    <section class="ov-section">
      <h4 class="ov-section-title">完整配置流程：从默认本地到接入云平台</h4>
      <p class="ov-section-sub">按顺序走就行。带「可选」的步骤不影响其他步骤，随时可以跳过。</p>
      <div class="ov-steps">
        <div v-for="step in SETUP_STEPS" :key="step.no" class="ov-step">
          <span class="ov-step-no">{{ step.no }}</span>
          <div class="ov-step-body">
            <strong>{{ step.title }}</strong>
            <span class="ov-step-when">{{ step.when }}</span>
            <p>{{ step.body }}</p>
          </div>
          <el-button size="small" :type="step.tab === 'overview' ? undefined : 'primary'" @click="emit('goto', step.tab)">
            {{ step.action }}
          </el-button>
        </div>
      </div>
    </section>

    <section class="ov-section">
      <h4 class="ov-section-title">第三方平台官方入口</h4>
      <p class="ov-section-sub">
        所有支持的服务商都列在下面，点开即是官方控制台或文档。创建好桶和密钥后，回到对应页签填写即可；
        每个输入框旁边的「?」还会告诉你这一项去哪里取。
      </p>

      <h5 class="ov-vendor-title">对象存储 · 决定「文件存哪」</h5>
      <div class="ov-vendors">
        <div v-for="guide in storageVendors" :key="guide.key" class="ov-vendor">
          <div class="ov-vendor-head">
            <strong>{{ guide.label }}</strong>
            <span class="ov-vendor-when">{{ guide.when }}</span>
          </div>
          <p class="ov-vendor-summary">{{ guide.summary }}</p>
          <div class="ov-links">
            <a v-for="link in guide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">
              {{ link.label }} ↗
            </a>
          </div>
        </div>
      </div>

      <table class="ov-s3-table">
        <thead>
          <tr><th>兼容 S3 的服务商</th><th>服务地址示例</th><th>怎么填</th></tr>
        </thead>
        <tbody>
          <tr v-for="v in S3_VARIANTS" :key="v.label">
            <td>{{ v.label }}</td>
            <td><code>{{ v.endpoint }}</code></td>
            <td>{{ v.note }}</td>
          </tr>
        </tbody>
      </table>
      <p class="ov-table-note">上面的地址只是示例，实际以云厂商控制台给出的端点为准；不要带 <code>https://</code>。</p>

      <h5 class="ov-vendor-title">CDN 加速 · 决定「访客怎么更快读到」</h5>
      <div class="ov-vendors">
        <div v-for="guide in cdnVendors" :key="guide.key" class="ov-vendor">
          <div class="ov-vendor-head">
            <strong>{{ guide.label }}</strong>
            <span class="ov-vendor-when">{{ guide.when }}</span>
          </div>
          <p class="ov-vendor-summary">{{ guide.summary }}</p>
          <div class="ov-links">
            <a v-for="link in guide.links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">
              {{ link.label }} ↗
            </a>
          </div>
          <p class="ov-credential">{{ guide.credential }}</p>
        </div>
      </div>
    </section>

    <section class="ov-section">
      <h4 class="ov-section-title">名词解释</h4>
      <div class="ov-glossary">
        <div v-for="item in STORAGE_GLOSSARY" :key="item.term" class="ov-glossary-item">
          <strong>{{ item.term }}</strong>
          <span>{{ item.meaning }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { StorageProfile, DeliveryProfile, StorageBinding } from '@/api/storageProfiles'
import {
  ARCHITECTURE_POINTS, DEFAULT_FACTS, SETUP_STEPS, STORAGE_GLOSSARY,
  DRIVER_GUIDES, CDN_GUIDES, S3_VARIANTS, driverGuide, cdnGuide,
} from '@/utils/storageGuides'

const props = defineProps<{
  platformProfile?: StorageProfile | null
  platformDelivery?: DeliveryProfile | null
  globalBinding?: StorageBinding | null
}>()

const emit = defineEmits<{ (e: 'goto', tab: string): void }>()

const driver = computed(() => (props.platformProfile?.driver || '').trim())
const provider = computed(() => (props.platformDelivery?.provider || '').trim())
const isDefaultLocal = computed(() => !driver.value || driver.value === 'local')
const cdnActive = computed(() => !!provider.value && provider.value !== 'none')

const storageLabel = computed(() => {
  if (!driver.value) return '读取中…'
  if (driver.value === 'local') return '本地磁盘'
  return driverGuide(driver.value)?.label || driver.value
})
const storageNote = computed(() =>
  isDefaultLocal.value
    ? '服务器自带，不需要任何云账号。改这里需要重启服务。'
    : `平台自己的文件写在 ${storageLabel.value}；企业用哪套看「谁在用这套」。`,
)
const cdnLabel = computed(() => {
  if (!cdnActive.value) return '未启用 CDN'
  return cdnGuide(provider.value)?.label || provider.value
})
const cdnNote = computed(() =>
  cdnActive.value
    ? '替换同名文件会自动刷新缓存，也可以到「CDN 刷新」手动提交。'
    : '访客直接从存储读取，功能完整；需要加速时再接入。',
)
const bindingLabel = computed(() => (props.globalBinding ? '已设置全局默认' : '使用平台自带配置'))
const bindingNote = computed(() =>
  props.globalBinding
    ? '没有单独指定的企业，都按全局默认的档案走，改完立即生效。'
    : '所有企业共用平台自带的存储与分发配置。',
)

const storageVendors = Object.values(DRIVER_GUIDES).filter((g) => g.key !== 'local')
const cdnVendors = Object.values(CDN_GUIDES).filter((g) => g.key !== 'none')
</script>

<style scoped>
.storage-overview { color: #303133; }
.ov-hero { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 18px; }
.ov-hero h3 { margin: 0 0 8px; font-size: 19px; }
.ov-hero-sub { margin: 0; color: #606266; font-size: 13px; line-height: 1.8; max-width: 760px; }
.ov-mode-tag { flex: none; margin-top: 4px; }
.ov-status { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.ov-card { display: flex; flex-direction: column; gap: 6px; padding: 14px 16px; border: 1px solid #ebeef5; border-radius: 8px; background: #fafcff; }
.ov-card-label { color: #909399; font-size: 12px; }
.ov-card-value { font-size: 16px; color: #1f2d3d; }
.ov-card-note { color: #606266; font-size: 12px; line-height: 1.6; }
.ov-default-alert { margin: 16px 0 22px; }
.ov-section { margin-bottom: 28px; }
.ov-section-title { margin: 0 0 6px; font-size: 16px; padding-left: 9px; border-left: 3px solid #409eff; }
.ov-section-sub { margin: 6px 0 14px; color: #606266; font-size: 13px; line-height: 1.7; }
.ov-flow { display: flex; flex-direction: column; align-items: stretch; gap: 0; margin: 16px auto 12px; max-width: 640px; }
.ov-node { display: flex; flex-direction: column; gap: 5px; padding: 12px 18px; border: 1px solid #d9e8fb; border-radius: 8px; background: #f0f7ff; text-align: center; }
.ov-node strong { font-size: 15px; }
.ov-node-kind { color: #409eff; font-size: 11px; letter-spacing: 1px; }
.ov-node-desc { color: #606266; font-size: 12px; }
.ov-node-client { border-color: #e4e7ed; background: #f8f9fb; }
.ov-node-storage { border-color: #b7e0c3; background: #f2fbf5; }
.ov-node-storage .ov-node-kind { color: #4a9a63; }
.ov-node-off { opacity: 0.72; }
.ov-node-off .ov-node-kind { color: #909399; }
.ov-flow-link { text-align: center; padding: 5px 0; color: #909399; font-size: 11px; }
.ov-facts { margin: 14px 0 0; padding-left: 20px; color: #606266; font-size: 13px; line-height: 1.9; }
.ov-points { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-top: 14px; }
.ov-point { padding: 14px 16px; border: 1px solid #ebeef5; border-radius: 8px; }
.ov-point strong { font-size: 14px; }
.ov-point p { margin: 8px 0 0; color: #606266; font-size: 13px; line-height: 1.75; }
.ov-steps { margin-top: 14px; border: 1px solid #ebeef5; border-radius: 8px; overflow: hidden; }
.ov-step { display: flex; align-items: flex-start; gap: 14px; padding: 14px 16px; border-bottom: 1px solid #f0f2f5; }
.ov-step:last-child { border-bottom: 0; }
.ov-step-no { flex: none; width: 26px; height: 26px; border-radius: 50%; background: #409eff; color: #fff; font-size: 13px; line-height: 26px; text-align: center; }
.ov-step-body { flex: 1; min-width: 0; }
.ov-step-body strong { font-size: 14px; }
.ov-step-when { margin-left: 10px; color: #b88230; font-size: 12px; }
.ov-step-body p { margin: 6px 0 0; color: #606266; font-size: 13px; line-height: 1.75; }
.ov-step .el-button { flex: none; margin-top: 3px; }
.ov-vendor-title { margin: 20px 0 10px; font-size: 14px; }
.ov-vendors { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.ov-vendor { padding: 14px 16px; border: 1px solid #ebeef5; border-radius: 8px; }
.ov-vendor-head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.ov-vendor-head strong { font-size: 14px; }
.ov-vendor-when { color: #909399; font-size: 12px; text-align: right; }
.ov-vendor-summary { margin: 7px 0 9px; color: #606266; font-size: 12px; line-height: 1.65; }
.ov-links { display: flex; flex-wrap: wrap; gap: 8px 14px; }
.ov-links a { color: #409eff; font-size: 12px; text-decoration: none; }
.ov-links a:hover { text-decoration: underline; }
.ov-credential { margin: 10px 0 0; padding-top: 9px; border-top: 1px dashed #e4e7ed; color: #606266; font-size: 12px; line-height: 1.7; }
.ov-s3-table { width: 100%; margin-top: 14px; border-collapse: collapse; font-size: 12px; }
.ov-s3-table th, .ov-s3-table td { padding: 8px 10px; border: 1px solid #ebeef5; text-align: left; }
.ov-s3-table th { background: #f8f9fb; color: #606266; }
.ov-s3-table td { color: #606266; }
.ov-s3-table code { color: #303133; }
.ov-table-note { color: #909399; font-size: 12px; }
.ov-glossary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px 20px; margin-top: 14px; }
.ov-glossary-item { display: flex; gap: 10px; font-size: 13px; line-height: 1.7; }
.ov-glossary-item strong { flex: none; width: 72px; }
.ov-glossary-item span { color: #606266; }
@media (max-width: 900px) {
  .ov-status, .ov-points, .ov-vendors, .ov-glossary { grid-template-columns: 1fr; }
  .ov-hero { flex-direction: column; }
}
</style>
