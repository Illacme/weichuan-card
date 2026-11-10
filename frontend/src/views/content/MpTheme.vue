<template>
  <el-card>
    <div class="page-bar" :class="{ 'only-action': props.embedded }">
      <h3 v-if="!props.embedded">小程序整体风格</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">
      为小程序选择一套内置配色。模板随小程序版本内置，后台只记录「选了哪一款」，
      保存后访客端即时生效，不需要重新上传代码或提交微信审核。
    </p>

    <!-- 授权到期 / 未开通时客户必须知道：不是保存失败，是这一档没买；
         且访客端会临时回落经典蓝，避免他看着后台的「使用中」以为已经生效。 -->
    <el-alert
      v-if="!templateEntitled"
      type="warning"
      :closable="false"
      show-icon
      title="当前授权未开通高级配色模板"
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
        <div class="theme-preview" :style="{ background: t.previewSoft }">
          <div class="preview-nav" :style="{ background: t.previewPrimary }" />
          <div class="preview-card">
            <div class="preview-line" :style="{ background: t.previewPrimary }" />
            <div class="preview-line short" />
            <div class="preview-btn" :style="{ background: t.previewPrimary }" />
          </div>
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
  width: 210px;
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
.theme-preview {
  position: relative;
  height: 128px;
  padding: 12px;
  box-sizing: border-box;
}
.preview-nav {
  height: 14px;
  border-radius: 4px;
  opacity: 0.9;
}
.preview-card {
  margin-top: 12px;
  padding: 12px;
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
