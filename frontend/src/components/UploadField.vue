<template>
  <!-- 通用上传组件：优先直传预签名（绕开应用服务器），不支持时回退普通上传；
       另提供「从素材库选择」入口——内置常用开源素材，点一下即用 -->
  <div class="upload-field">
    <!-- el-upload 的 @preview 只在点击「文件名」或 picture-card 悬浮放大镜时触发，
         左侧缩略图本身不带预览 → 这里在容器上做一次事件委托：点到缩略图就开大图。 -->
    <div class="uf-list" @click="onListClick">
      <el-upload
        :action="action"
        :multiple="multiple"
        :accept="accept"
        :list-type="listType || (multiple ? 'picture-card' : 'picture')"
        :file-list="fileList"
        :limit="limit"
        :before-upload="beforeUpload"
        :http-request="customUpload"
        :on-success="onSuccess"
        :on-remove="onRemove"
        :on-exceed="onExceed"
        @preview="onPreview"
      >
        <el-button type="primary">选择文件</el-button>
        <template #tip>
          <div class="el-upload__tip">{{ tip }}<span v-if="!hideLibrary" class="upload-view-hint">，点击图片或文件名可放大预览</span></div>
        </template>
      </el-upload>
    </div>
    <!-- 上传进度条。大视频几十上百 MB，点完「选择文件」没有反馈只会被当成页面卡死。
         阶段也要写出来：直传失败会**静默回退**成经服务器中转，那一刻进度从头开始，
         不说原因就是「进度条莫名其妙倒退」。
         这里刻意不复用 el-upload 自带的进度：它只认一个百分比，表达不了「换了一条路」，
         两条进度条并排反而更乱。 -->
    <div v-if="progress" class="uf-progress" :class="{ 'is-server': progress.phase === 'server' }">
      <div class="uf-progress-label">
        <span>{{ phaseLabel }}</span>
        <span v-if="progress.phase !== 'presign' && progress.total > 0" class="uf-progress-pct">
          {{ progress.percent }}%
        </span>
      </div>
      <div class="uf-progress-track">
        <div
          class="uf-progress-bar"
          :class="{ 'is-waiting': progress.phase === 'presign' }"
          :style="{ width: progress.phase === 'presign' ? '30%' : progress.percent + '%' }"
        ></div>
      </div>
    </div>
    <!-- 编辑页大图确认：点击已回显/已上传的文件打开全屏查看器（带分辨率与文件大小） -->
    <ImageViewer v-if="viewerUrl" :url-list="[viewerUrl]" @close="viewerUrl = ''" />
    <div v-if="!hideLibrary" class="library-entry">
      <el-button :icon="Picture" @click="pickerOpen = true">从素材库选择</el-button>
      <span class="library-hint">内置常用开源素材，点击即用，无需自行找图和处理尺寸</span>
    </div>
    <AssetPicker
      v-model="pickerOpen"
      :multiple="multiple"
      :limit="limit"
      :default-category="libraryType"
      @picked="onLibraryPicked"
    />
  </div>
</template>

<script setup>
import { ElMessage } from 'element-plus'
import { Picture } from '@element-plus/icons-vue'
import { ref, watch, computed } from 'vue'
import AssetPicker from './AssetPicker.vue'
import ImageViewer from './ImageViewer.vue'
import { uploadFile } from '@/api/uploadFile'

const props = defineProps({
  modelValue: { type: [String, Array], default: '' }, // 单选为 string，多选为 string[]
  multiple: { type: Boolean, default: false },
  accept: { type: String, default: 'image/*' },
  limit: { type: Number, default: 9 },
  action: { type: String, default: '/api/admin/upload' }, // 普通上传地址；预签名地址由 action 推导
  tip: { type: String, default: '支持图片，单文件 ≤10MB' },
  maxMB: { type: Number, default: 10 },
  // 列表形态：空串时按 multiple 推导（多选 picture-card / 单选 picture）。
  // 非图片文件（如视频）需显式传 'text'——picture 会把 URL 当图片渲染成裂图。
  listType: { type: String, default: '' },
  // 非图片文件用：隐藏「从素材库选择」入口与放大预览提示。
  // 素材库里全是图片，视频点进去选不出东西；text 形态也没有缩略图可放大。
  hideLibrary: { type: Boolean, default: false },
  // 素材库打开时定位的分类：icon（分类/服务图标）/ cover（封面/相册）/
  // logo（LOGO）/ avatar（员工头像）/ background（背景）
  libraryType: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

// 素材库选择器
const pickerOpen = ref(false)

// 大图查看器：el-upload 的 preview 事件（点击文件名/缩略图）触发
const viewerUrl = ref('')
function onPreview(file) {
  if (file && file.url) viewerUrl.value = file.url
}
// 事件委托：点击列表里的缩略图（picture / picture-card 同为
// .el-upload-list__item-thumbnail）也打开大图。src 可能是回显的远程 URL，
// 也可能是刚上传尚未提交的 blob: URL——两种都能直接预览。
function onListClick(e) {
  const t = e.target
  if (!(t instanceof HTMLElement)) return
  const thumb = t.closest('.el-upload-list__item-thumbnail')
  if (!thumb) return
  const src = thumb.getAttribute('src')
  if (src) viewerUrl.value = src
}

// 素材库回调：单选直接替换，多选追加（受 limit 约束）
function onLibraryPicked(picked) {
  const arr = Array.isArray(picked) ? picked : picked ? [picked] : []
  if (!arr.length) return
  if (props.multiple) {
    const cur = Array.isArray(props.modelValue) ? [...props.modelValue] : []
    const room = Math.max(0, props.limit - cur.length)
    if (arr.length > room) ElMessage.warning(`最多上传 ${props.limit} 个文件`)
    emit('update:modelValue', [...cur, ...arr.slice(0, room)])
  } else {
    emit('update:modelValue', arr[0])
  }
}

// PC 后台为 httpOnly Cookie 鉴权（withCredentials 自动携带，JS 不接触令牌）；
// 上传走同源请求，浏览器会自动附带 Cookie，无需手动拼 Bearer。
// 早期 Bearer 方案已迁移，localStorage 不保存 token，拼空 Bearer 属误导性死代码，已移除。

const fileList = ref([])

// 上传进度（null = 不在上传）。XHR 的 progress 事件很密，
// 只在「阶段或百分比真的变了」时才写响应式状态，别每个 64KB 都触发一次渲染。
const progress = ref(null)
function applyProgress(p) {
  const cur = progress.value
  if (cur && cur.phase === p.phase && cur.percent === p.percent) return
  progress.value = p
}
const phaseLabel = computed(() => {
  const p = progress.value
  if (!p) return ''
  if (p.phase === 'presign') return '正在申请上传链接…'
  if (p.phase === 'direct') return '正在直传到云存储（不经应用服务器）…'
  // 这一句不能省：大文件走中转会明显变慢，用户有权知道是配置没配对
  return '直传不可用，已改用服务器中转（大文件会占服务器带宽）…'
})

// 外部值 → 内部 file-list（用于回显已保存 URL）
watch(
  () => props.modelValue,
  (val) => {
    const arr = Array.isArray(val) ? val : val ? [val] : []
    fileList.value = arr.map((u, i) => ({ name: u.split('/').pop(), url: u, uid: i }))
  },
  { immediate: true }
)

function beforeUpload(file) {
  // 用 <= 而不是 <：提示文案写的是「≤N MB」，服务端 checkFile 也是 size > max 才拒，
  // 两边对「正好 N MB」的判断必须一致，否则卡在边界上的文件会出现
  // 「界面说不能超过、后端其实收」的矛盾提示。
  // maxMB<=0 视为「不限制」：设置项被写坏时不能反过来把所有文件都拒掉。
  if (props.maxMB > 0 && file.size / 1024 / 1024 > props.maxMB) {
    ElMessage.error(`文件不能超过 ${props.maxMB}MB`)
    return false
  }
  return true
}

// 自定义上传：实现细节（预签名直传 → 回退 multipart）抽在 api/uploadFile.ts，
// 富文本编辑器的图片菜单用的是同一份——两条链路各写一遍必然漂移
// （此前就出现过「这里能把图传上去、编辑器里传不上去」）。
// el-upload 的成功契约是 onSuccess 收一个 { code, data } 信封，这里把拿到的 URL 包回去，
// 就仍由下面的 onSuccess 统一处理多选追加 / 单选替换。
//
// **故意不写成 async**：el-upload 对「返回 Promise 的 http-request」会再接一次
// `request.then(options.onSuccess, options.onError)`（见 upload-content），于是
// 成功回调跑两遍，第二遍拿到 undefined，我们的 onSuccess 读 res.code 直接抛
// TypeError → 控制台一条 Uncaught (in promise)，却看不出是哪来的。
// 返回 undefined 就只走我们自己这一条 onSuccess / onError。
function customUpload(option) {
  progress.value = null
  uploadFile(option.file, {
    action: option.action || props.action,
    onProgress: applyProgress,
  }).then(
    (url) => {
      progress.value = null
      option.onSuccess({ code: 0, data: { urls: [url] } })
    },
    (e) => {
      progress.value = null
      ElMessage.error((e && e.message) || '上传失败')
      option.onError(e)
    }
  )
}

function onSuccess(res) {
  if (res.code !== 0) {
    ElMessage.error(res.msg || '上传失败')
    return
  }
  const url = (res.data.urls || [])[0]
  if (!url) return
  if (props.multiple) {
    const cur = Array.isArray(props.modelValue) ? [...props.modelValue] : []
    cur.push(url)
    emit('update:modelValue', cur)
  } else {
    emit('update:modelValue', url)
  }
}

function onRemove(file) {
  if (props.multiple) {
    const cur = (Array.isArray(props.modelValue) ? props.modelValue : []).filter((u) => u !== file.url)
    emit('update:modelValue', cur)
  } else {
    emit('update:modelValue', '')
  }
}

function onExceed() {
  ElMessage.warning(`最多上传 ${props.limit} 个文件`)
}
</script>

<style scoped>
/* 上传进度：置于文件列表与素材库入口之间，避免把「选择文件」按钮推得跳动 */
.uf-progress {
  margin: 8px 0 2px;
  max-width: 420px;
}
.uf-progress-label {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  color: #606266;
  line-height: 1.5;
}
.uf-progress.is-server .uf-progress-label {
  color: #b8820a;
}
.uf-progress-pct {
  flex: none;
  font-variant-numeric: tabular-nums;
  color: #409eff;
}
.uf-progress.is-server .uf-progress-pct {
  color: #b8820a;
}
.uf-progress-track {
  height: 6px;
  margin-top: 5px;
  border-radius: 3px;
  background: #ebeef5;
  overflow: hidden;
}
.uf-progress-bar {
  height: 100%;
  border-radius: 3px;
  background: #409eff;
  transition: width 0.15s linear;
}
.uf-progress.is-server .uf-progress-bar {
  background: #e6a23c;
}
/* 申请签名这步没有字节进度，用一条来回滑动的短条表示「还在动」 */
.uf-progress-bar.is-waiting {
  animation: uf-wait 1.2s ease-in-out infinite;
}
@keyframes uf-wait {
  0% { transform: translateX(-60%); }
  50% { transform: translateX(180%); }
  100% { transform: translateX(-60%); }
}
/* 缩略图可点开大图（事件委托在 .uf-list 上），指针提示 + 避免被当成普通图片 */
.uf-list :deep(.el-upload-list__item-thumbnail) {
  cursor: zoom-in;
}
.library-entry {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
}
.library-hint {
  font-size: 12px;
  color: #909399;
}
.upload-view-hint {
  color: #909399;
}
</style>
