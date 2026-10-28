<template>
  <el-card>
    <div class="page-bar" :class="{ 'only-action': props.embedded }">
      <h3 v-if="!props.embedded">首页布局</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">控制小程序首页「经典业绩」与「业务服务」的先后顺序，并可设置首页背景图做品牌化包装。</p>

    <!-- 表单宽度与「访客先看到谁」对齐（720）：560 时「从素材库选择」按钮的文字会被
         挤成两行，撑满卡片后这个窄表单也显得孤零零 -->
    <el-form label-width="120px" style="max-width: 720px">
      <el-form-item label="板块顺序">
        <el-radio-group v-model="firstBlock">
          <el-radio value="achievement">业绩在前，服务在后</el-radio>
          <el-radio value="service">服务在前，业绩在后</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="当前顺序">
        <el-tag v-for="b in order" :key="b" class="order-tag">{{ blockLabel(b) }}</el-tag>
      </el-form-item>

      <el-divider content-position="left">首页背景（可选）</el-divider>

      <el-form-item label="背景图">
        <UploadField v-model="bgImage" library-type="background" :multiple="false" tip="建议竖幅 750×1334 以上，JPG/PNG，≤10MB" />
        <div class="bg-hint">
          从素材库「背景」分类可直接选用内置高端商务背景；留空则保持小程序默认灰底。
        </div>
      </el-form-item>

      <el-form-item label="遮罩浓度">
        <div class="mask-row">
          <el-slider v-model="bgMask" :min="0" :max="100" :step="5" :disabled="!bgImage" style="flex: 1" />
          <span class="mask-val">{{ bgMask }}%</span>
        </div>
        <div class="bg-hint">背景图较花时调高遮罩压暗，保证白色卡片与文字清晰可读。</div>
      </el-form-item>

      <el-form-item v-if="bgImage" label="效果预览">
        <!-- 点击整块预览可放大查看原图（遮罩/卡片只是叠加效果层，原图可能较花看不清细节） -->
        <div class="bg-preview clickable" @click="bgViewer = true">
          <img class="bg-preview-img" :src="bgImage" alt="背景预览" />
          <div class="bg-preview-mask" :style="{ opacity: bgMask / 100 }" />
          <div class="bg-preview-card">白色内容卡片</div>
        </div>
      </el-form-item>

      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
        <el-button v-if="bgImage" link @click="clearBg">清除背景</el-button>
      </el-form-item>
    </el-form>
    <ImageViewer v-if="bgViewer && bgImage" :url-list="[bgImage]" @close="bgViewer = false" />
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, computed, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getHomeLayout, saveHomeLayout } from '@/api/content'
import UploadField from '@/components/UploadField.vue'
import ImageViewer from '@/components/ImageViewer.vue'

// embedded=true：被「小程序设置」内嵌在「名片首页样式」页签里时，不再重复自己的标题。
const props = defineProps<{ embedded?: boolean }>()

const BLOCKS = ['achievement', 'service']
const loading = ref(false)
const saving = ref(false)
const firstBlock = ref<string>('achievement')
// 首页背景（可选增强）：空串=不设背景；遮罩 0~100
const bgImage = ref('')
// 背景原图全屏查看器（点击效果预览块打开）
const bgViewer = ref(false)
const bgMask = ref(0)

const order = computed(() => {
  const rest = BLOCKS.filter((b) => b !== firstBlock.value)
  return [firstBlock.value, ...rest]
})

function blockLabel(b: string) {
  return b === 'achievement' ? '经典业绩' : '业务服务'
}

async function load() {
  loading.value = true
  try {
    const d = await getHomeLayout()
    if (d && d.order && d.order.length) firstBlock.value = d.order[0]
    bgImage.value = d?.bgImage || ''
    bgMask.value = d?.bgMask || 0
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 背景与板块顺序一起提交：bgImage 传空串即清空（后端区分「未提供」与「空串」）
    await saveHomeLayout({ order: order.value, bgImage: bgImage.value, bgMask: bgMask.value })
    ElMessage.success('已保存首页布局')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

function clearBg() {
  bgImage.value = ''
  bgMask.value = 0
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
.desc {
  color: #606266;
  font-size: 13px;
}
/* 内嵌时只剩「重新加载」按钮，别让它贴左 */
.page-bar.only-action {
  justify-content: flex-end;
}
.order-tag {
  margin-right: 8px;
}
.bg-hint {
  margin-top: 6px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}
.mask-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
.mask-val {
  width: 44px;
  flex-shrink: 0;
  color: #606266;
  font-size: 13px;
  text-align: right;
}
/* 效果预览：模拟小程序首页——背景铺底 + 遮罩 + 白色卡片浮起 */
.bg-preview {
  position: relative;
  width: 160px;
  height: 284px;
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid #ebeef5;
}
.bg-preview.clickable {
  cursor: zoom-in;
}
.bg-preview-img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.bg-preview-mask {
  position: absolute;
  inset: 0;
  background: #000;
}
.bg-preview-card {
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: 14px;
  padding: 10px 0;
  text-align: center;
  font-size: 12px;
  color: #333;
  background: rgba(255, 255, 255, 0.95);
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}
</style>
