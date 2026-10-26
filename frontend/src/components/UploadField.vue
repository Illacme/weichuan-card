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

// 由 action 推导预签名地址：/api/admin/upload -> /api/admin/upload/presign
const presignAction = computed(() => props.action.replace('/upload', '/upload/presign'))

const fileList = ref([])

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
  const ok = file.size / 1024 / 1024 < props.maxMB
  if (!ok) ElMessage.error(`文件不能超过 ${props.maxMB}MB`)
  return ok
}

// 自定义上传：先尝试直传预签名，失败则回退普通上传
async function customUpload(option) {
  const file = option.file
  const ext = (file.name.split('.').pop() || '').toLowerCase()
  const contentType = file.type || 'application/octet-stream'
  try {
    const p = await presign(ext, contentType)
    if (p && p.code === 0 && p.data && p.data.putUrl) {
      await putDirect(p.data.putUrl, file, contentType)
      option.onSuccess({ code: 0, data: { urls: [p.data.url] } })
      return
    }
  } catch {
    // 预签名异常，落入回退分支
  }
  await serverUpload(option)
}

async function presign(ext, contentType) {
  const url =
    presignAction.value +
    '?ext=' + encodeURIComponent(ext) +
    '&contentType=' + encodeURIComponent(contentType)
  // 同源请求自动携带 httpOnly Cookie；不附加 Bearer（无 token 可附）
  const resp = await fetch(url)
  return resp.json()
}

function putDirect(putUrl, file, contentType) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', putUrl)
    xhr.setRequestHeader('Content-Type', contentType)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve(xhr)
      else reject(new Error('put failed: ' + xhr.status))
    }
    xhr.onerror = () => reject(new Error('put error'))
    xhr.send(file)
  })
}

function serverUpload(option) {
  return new Promise((resolve) => {
    const form = new FormData()
    form.append('file', option.file)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', props.action)
    // 同源请求自动携带 httpOnly Cookie；不附加 Bearer（无 token 可附）
    xhr.onload = () => {
      try {
        option.onSuccess(JSON.parse(xhr.responseText))
      } catch (e) {
        option.onError(e)
      }
      resolve()
    }
    xhr.onerror = (e) => {
      option.onError(e)
      resolve()
    }
    xhr.send(form)
  })
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
