<template>
  <el-card>
    <div class="page-bar" :class="{ 'only-action': props.embedded }">
      <h3 v-if="!props.embedded">小程序整体风格</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">
      为小程序选择一套内置整体风格：主色、卡片圆角、按钮形态与投影质感一并切换。
      模板随小程序版本内置，后台只记录「选了哪一款」，
      保存后访客端即时生效，不需要重新上传代码或提交微信审核。
    </p>

    <!-- 授权到期 / 未开通时客户必须知道：不是保存失败，是这一档没买；
         且访客端会临时回落经典蓝，避免他看着后台的「使用中」以为已经生效。 -->
    <el-alert
      v-if="!templateEntitled"
      type="warning"
      :closable="false"
      show-icon
      title="当前授权未开通高级整体风格模板"
      class="entitle-alert"
    >
      高级款暂不可新选，访客端会临时回落到经典蓝；已经选中的高级款会为您保留，续费后自动恢复。
      已选中的款式仍可继续使用或切回经典蓝。如需开通请联系服务商升级授权。
    </el-alert>

    <div class="theme-grid">
      <div
        v-for="t in templates"
        :key="t.id"
        class="theme-card"
        :class="{ selected: selected === t.id, locked: isLocked(t) }"
        @click="pick(t)"
      >
        <!-- 真实预览图由 scripts/gen_theme_previews.py 从 miniprogram/app.wxss 的
             token 生成，和访客端共用同一套配色 / 圆角 / 投影，不是手画的第二套样式。
             底层 CSS 示意常驻：图片加载中、404、或新增模板忘了补图时都不会留空白；
             右下角「示意图」角标让降级可见，而不是悄悄退回旧样式。
             .layout-<id> 仍是示意路径与跨端守门的对账点。 -->
        <div class="theme-preview" :class="'layout-' + t.id">
          <div class="preview-fallback" :style="{ background: t.previewSoft }">
            <div class="preview-nav" :style="{ background: t.previewPrimary }" />
            <div class="preview-card">
              <div class="preview-line" :style="{ background: t.previewPrimary }" />
              <div class="preview-line short" />
              <div class="preview-btn" :style="{ background: t.previewPrimary }" />
            </div>
            <span class="fallback-tag">示意图</span>
          </div>
          <img
            v-if="!failedPreviews[t.id]"
            class="preview-shot"
            :src="previewUrl(t.id)"
            :alt="t.name + ' 小程序首页预览'"
            decoding="async"
            @error="onPreviewError(t.id)"
          />
        </div>
        <div class="theme-meta">
          <div class="theme-name">
            {{ t.name }}
            <el-tag v-if="t.advanced" size="small" type="warning" class="adv-tag">高级</el-tag>
            <span v-if="selected === t.id" class="current">使用中</span>
          </div>
          <div class="theme-desc">{{ t.description }}</div>
          <div v-if="isLocked(t)" class="theme-lock">未开通 · 暂不可选</div>
        </div>
      </div>
    </div>

    <div class="actions">
      <el-button type="primary" :loading="saving" :disabled="!templates.length" @click="save">
        保存风格
      </el-button>
      <span class="hint">仅提交模板选择，不改动首页背景、板块顺序与详情页时间显示。</span>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getHomeLayout, saveHomeLayout, type ThemeTemplate } from '@/api/content'

// embedded=true：被「小程序设置」页签内嵌时不再重复自己的标题。
const props = defineProps<{ embedded?: boolean }>()

const loading = ref(false)
const saving = ref(false)
const selected = ref('default')
const templates = ref<ThemeTemplate[]>([])
const templateEntitled = ref(true)

// 预览图是随前端版本发布的静态资源：加载失败时移除该张 <img>，露出底层示意。
// 用「id -> 失败」而不是一个全局开关，某一张图坏了不能把其余三张一起降级。
const failedPreviews = ref<Record<string, boolean>>({})

// public/ 下的文件由 Vite 原样拷到产物根目录；带 BASE_URL 前缀，部署到子路径时
// 不会误打到站点根下的 /theme-previews。encodeURIComponent 防 id 拼出非预期路径，
// 真出异常也就是这张图 404 → 走兜底，不会静默显示另一款模板的图。
function previewUrl(id: string): string {
  return `${import.meta.env.BASE_URL}theme-previews/${encodeURIComponent(id)}.png`
}

function onPreviewError(id: string) {
  failedPreviews.value = { ...failedPreviews.value, [id]: true }
}

// 未开通授权时禁止「新选」高级款；但**当前已在用**的那一款不能锁——
// 授权到期后客户仍要能带着它保存别处设置，或主动切回经典蓝（后端同值放行）。
function isLocked(t: ThemeTemplate): boolean {
  return t.advanced && !templateEntitled.value && t.id !== selected.value
}

function pick(t: ThemeTemplate) {
  if (isLocked(t)) return
  selected.value = t.id
}

async function load() {
  loading.value = true
  // 「重新加载」也该给预览图一次重试机会：例如刚补上漏生成的 PNG，
  // 或上一轮只是临时网络抖动，不能让客户一直停在示意图上。
  failedPreviews.value = {}
  try {
    const d = await getHomeLayout()
    templates.value = Array.isArray(d?.templates) ? d.templates : []
    templateEntitled.value = d?.templateEntitled !== false
    // 后端已归一过未知 id；这里再兜一次空值，连老后端也不会出现「一个都没选中」
    selected.value = d?.templateId || 'default'
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 只提交 templateId：三个页签各存各的，后端 PUT 对未提供的字段保持原值。
    // 整份表单回传会把别的页签刚改过的值一起写回去，属于典型的跨页签覆盖。
    await saveHomeLayout({ templateId: selected.value })
    ElMessage.success('已保存小程序整体风格')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
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
.page-bar.only-action {
  justify-content: flex-end;
}
.desc {
  color: #606266;
  font-size: 13px;
  line-height: 1.7;
}
.entitle-alert {
  margin-bottom: 16px;
}
.theme-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-top: 8px;
}
.theme-card {
  width: 230px;
  border: 2px solid #e4e7ed;
  border-radius: 12px;
  overflow: hidden;
  cursor: pointer;
  background: #fff;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.theme-card.selected {
  border-color: #1a8cff;
  box-shadow: 0 0 0 2px rgba(26, 140, 255, 0.14);
}
/* 未开通的款式降透明度，但仍保留文字说明——置灰不解释会让客户以为页面坏了 */
.theme-card.locked {
  cursor: not-allowed;
  opacity: 0.62;
}
/* 预览容器比例与 gen_theme_previews.py 的 375x430 画布一致：图片下载完成前后
   都不跳版，四张卡保持对齐。生成脚本刻意把高度收到 430，就是为了让底部按钮
   （四款圆角差异最大的一处）完整可见，这里不能再裁切。 */
.theme-preview {
  position: relative;
  aspect-ratio: 375 / 430;
  overflow: hidden;
}
/* 底层示意常驻：img 还在下载 / 404 / 新模板漏补图时都有内容；
   「示意图」角标让降级是可见的，不会没人报错就没人发现。 */
.preview-fallback {
  position: absolute;
  inset: 0;
  padding: 12px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}
.fallback-tag {
  position: absolute;
  right: 8px;
  bottom: 8px;
  font-size: 11px;
  color: #606266;
  background: rgba(255, 255, 255, 0.92);
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 1px 6px;
}
.preview-shot {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.preview-nav {
  height: 14px;
  border-radius: 4px;
  opacity: 0.9;
  flex: none;
}
.preview-card {
  margin-top: 12px;
  padding: 12px;
  flex: 1;
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}
.preview-line {
  height: 8px;
  width: 78%;
  border-radius: 4px;
  opacity: 0.85;
}
.preview-line.short {
  margin-top: 8px;
  width: 52%;
  background: #dcdfe6;
  opacity: 1;
}
.preview-btn {
  margin-top: 14px;
  height: 12px;
  width: 44%;
  border-radius: 6px;
}
/* 版式示意（与端上 --wc-r-* token 同向，px 只是后台缩略图尺度）：
   沉稳墨 = 近直角 + 细描边代替投影；暖金棕 = 大圆角 + 胶囊按钮；
   松石青 = 中圆角 + 极轻投影。
   加新模板必须补一条 .layout-<id>，theme.spec.ts 会逐个对账。 */
.layout-ink .preview-nav,
.layout-ink .preview-line,
.layout-ink .preview-btn { border-radius: 2px; }
.layout-ink .preview-card {
  border-radius: 2px;
  box-shadow: 0 0 0 1px rgba(61, 75, 92, 0.22);
}
.layout-warm .preview-nav,
.layout-warm .preview-line { border-radius: 8px; }
.layout-warm .preview-card {
  border-radius: 16px;
  box-shadow: 0 6px 14px rgba(143, 99, 32, 0.16);
}
.layout-warm .preview-btn { border-radius: 999px; }
.layout-teal .preview-nav,
.layout-teal .preview-line { border-radius: 5px; }
.layout-teal .preview-card {
  border-radius: 6px;
  box-shadow: 0 1px 5px rgba(15, 118, 110, 0.14);
}
.layout-teal .preview-btn { border-radius: 10px; }
.theme-meta {
  padding: 12px 14px 14px;
}
.theme-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.adv-tag {
  transform: scale(0.9);
}
.current {
  font-size: 12px;
  font-weight: 400;
  color: #1a8cff;
}
.theme-desc {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  min-height: 38px;
}
.theme-lock {
  margin-top: 4px;
  font-size: 12px;
  color: #e6a23c;
}
.actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
}
.hint {
  font-size: 12px;
  color: #909399;
}
</style>
