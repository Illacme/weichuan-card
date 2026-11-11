<template>
    <el-card>
    <!-- 内嵌在「小程序设置」页签里时，重新加载由父组件切换栏右侧的按钮统一提供，
         本页不再重复渲染自己的页签栏。独立访问时才需要它。 -->
    <div v-if="!props.embedded" class="page-bar">
      <h3>小程序页面样式</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">设置小程序首页背景、首页「经典业绩」与「业务服务」的先后顺序，以及两个详情页是否显示「更新于」。</p>

    <!-- 表单宽度与「访客先看到谁」对齐（720）：560 时「从素材库选择」按钮的文字会被
         挤成两行，撑满卡片后这个窄表单也显得孤零零 -->
    <el-form label-width="120px" style="max-width: 720px">
      <!-- 模块顺序是产品约定：首页背景 → 板块顺序 → 详情页时间显示。
           三块都带标题，调整顺序时别只挪表单项、把标题留在原地。
           HomeLayout.spec.ts 有源码级守门钉死这个顺序。 -->
      <el-divider content-position="left">首页背景</el-divider>

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

      <el-divider content-position="left">板块顺序</el-divider>

      <el-form-item label="谁在前">
        <el-radio-group v-model="firstBlock">
          <el-radio value="achievement">业绩在前，服务在后</el-radio>
          <el-radio value="service">服务在前，业绩在后</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="当前顺序">
        <el-tag v-for="b in order" :key="b" class="order-tag">{{ blockLabel(b) }}</el-tag>
      </el-form-item>

      <el-divider content-position="left">详情页时间显示</el-divider>

      <p class="desc desc-tight">
        详情页原本固定显示「更新于 + 最后一次保存时间」。这个时间是运营保存内容的时刻，
        不是案例交付时间——批量整理内容时全站案例会显示同一天，精确到秒对访客也偏噪音。
        可以调成只到日，或直接关掉。
      </p>

      <!-- 案例与服务分开控制：案例带「交付年份」语义、服务更像常青内容，
           两者该不该显示时间往往不是一个答案，共用一个开关会逼客户二选一。 -->
      <el-form-item label="项目案例详情">
        <el-radio-group v-model="projectTimeMode">
          <el-radio value="hidden">不显示</el-radio>
          <el-radio value="date">只显示日期</el-radio>
          <el-radio value="minute">显示到分</el-radio>
        </el-radio-group>
        <div class="bg-hint">小程序效果：{{ modePreview(projectTimeMode) }}</div>
      </el-form-item>

      <el-form-item label="业务服务详情">
        <el-radio-group v-model="serviceTimeMode">
          <el-radio value="hidden">不显示</el-radio>
          <el-radio value="date">只显示日期</el-radio>
          <el-radio value="minute">显示到分</el-radio>
        </el-radio-group>
        <div class="bg-hint">小程序效果：{{ modePreview(serviceTimeMode) }}</div>
      </el-form-item>

      <el-form-item>
        <!-- 内嵌时保存由父组件切换栏右侧按钮统一提供；独立访问才在本页渲染。
             「清除背景」是跟背景字段绑定的便捷操作，留在表单内不动。 -->
        <el-button v-if="!props.embedded" type="primary" :loading="saving" @click="save">保存</el-button>
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
import { getHomeLayout, saveHomeLayout, type DetailTimeMode } from '@/api/content'
import UploadField from '@/components/UploadField.vue'
import ImageViewer from '@/components/ImageViewer.vue'

// embedded=true：被「小程序设置」内嵌在「名片页面样式」页签里时，不再重复自己的标题。
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
// 详情页「更新于」档位：案例与服务各一个，互不影响
const projectTimeMode = ref<DetailTimeMode>('date')
const serviceTimeMode = ref<DetailTimeMode>('date')

// 把档位翻译成「小程序上会看到什么」，比让客户理解 hidden/date/minute 直观。
// 时间取当下（不是真实内容时间，只演示格式），够用且不会误导成「这是某条内容的时间」。
function modePreview(mode: string): string {
  if (mode === 'hidden') return '不显示时间'
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  if (mode === 'minute') return `更新于 ${date} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `更新于 ${date}`
}

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
    projectTimeMode.value = d?.projectTimeMode || 'date'
    serviceTimeMode.value = d?.serviceTimeMode || 'date'
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 背景与板块顺序一起提交：bgImage 传空串即清空（后端区分「未提供」与「空串」）。
    // 两个时间档位一起带上：不传后端会保持原值，但这里本就把两者都渲染出来了，
    // 全量提交更符合「所见即所存」。
    await saveHomeLayout({
      order: order.value,
      bgImage: bgImage.value,
      bgMask: bgMask.value,
      projectTimeMode: projectTimeMode.value,
      serviceTimeMode: serviceTimeMode.value,
    })
    ElMessage.success('已保存小程序展示设置')
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

// 内嵌在「小程序设置」时，父组件用 ref 调这里的 load/save 来驱动切换栏右侧按钮的
// loading 态；首页样式页无「未填完不可保存」的限制，故不暴露 disabled 判定。
defineExpose({ load, save, loading, saving })
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
/* 夹在两组表单之间的说明段：上下留白交给 el-form-item，自身别再撑开 */
.desc-tight {
  margin: 0 0 8px;
  line-height: 1.7;
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
