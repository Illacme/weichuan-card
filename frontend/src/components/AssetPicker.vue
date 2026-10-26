<template>
  <!-- 常用素材库选择器：点一下即采用并填入（单选），或多选后批量添加。
       素材由后端内嵌（Lucide 图标 + 通用封面/LOGO/头像/背景/海报），采用后与普通上传同构。
       列表走服务端分页（page/size/type/style/keyword），素材扩充到百级也不会一次拉全量。
       两个筛选维度：分类 tab = 用在哪，风格行 = 什么气质（简约清晰/温暖自然/轻奢尊贵/
       柔美清新/光影流动）。风格行只显示当前条件下有素材的档位，只有一档时整行隐藏。 -->
  <el-dialog
    v-model="visible"
    title="素材库"
    width="880px"
    top="6vh"
    class="resp-dialog asset-dialog"
    :close-on-click-modal="false"
    @closed="onClosed"
  >
    <div class="asset-picker">
      <div class="picker-bar">
        <el-radio-group v-model="tab" size="default" @change="onTabChange">
          <el-radio-button label="all">全部</el-radio-button>
          <el-radio-button v-for="c in categories" :key="c.key" :label="c.key">{{ c.name }}</el-radio-button>
        </el-radio-group>
        <el-input
          v-model="keyword"
          placeholder="搜索素材名称"
          :prefix-icon="Search"
          clearable
          class="picker-search"
          @input="onKeywordInput"
          @clear="onKeywordInput"
        />
      </div>

      <div v-if="visibleStyles.length > 1" class="picker-styles">
        <span class="style-label">风格</span>
        <el-radio-group v-model="styleKey" size="small" @change="onStyleChange">
          <el-radio-button label="all">全部</el-radio-button>
          <el-radio-button v-for="s in visibleStyles" :key="s.key" :label="s.key">
            {{ s.name }} {{ s.count }}
          </el-radio-button>
        </el-radio-group>
      </div>

      <div v-loading="loading" class="picker-grid">
        <div
          v-for="it in items"
          :key="it.id"
          class="asset-card"
          :class="[it.category, { selected: isSelected(it.id) }]"
          @click="onCardClick(it)"
        >
          <img class="asset-img" :src="libraryFileUrl(it.thumb)" :alt="it.name" loading="lazy" />
          <div class="asset-name">{{ it.name }}</div>
          <div v-if="adoptingId === it.id" class="asset-mask">
            <el-icon class="is-loading"><Loading /></el-icon>
          </div>
          <div v-else-if="isSelected(it.id)" class="asset-check"><el-icon><Check /></el-icon></div>
        </div>
      </div>
      <el-empty v-if="!loading && !items.length" description="没有匹配的素材" :image-size="60" />

      <!-- 分页栏常驻固定在弹窗底部（居中紧凑 prev/pager/next，footer 已有「共 N 项」总数，
           不重复显示 total/sizes）。asset-picker 用 flex 布局约束整体高度，
           网格区内部滚动，分页栏不受数据量影响、永远不会被挤出可视区。 -->
      <div class="picker-pager">
        <el-pagination
          layout="prev, pager, next"
          :current-page="page"
          :page-size="size"
          :total="total"
          :pager-count="7"
          background
          @current-change="onPageChange"
        />
      </div>
    </div>

    <template #footer>
      <span class="picker-count">共 {{ total }} 项{{ multiple ? `，已选 ${selected.length} 个` : '' }}</span>
      <span class="picker-actions">
        <el-button @click="visible = false">取消</el-button>
        <el-button
          v-if="multiple"
          type="primary"
          :disabled="!selected.length"
          :loading="batchAdopting"
          @click="confirmMultiple"
        >
          添加所选{{ selected.length ? ` (${selected.length})` : '' }}
        </el-button>
      </span>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { Check, Loading, Search } from '@element-plus/icons-vue'
import {
  listLibrary,
  adoptAsset,
  libraryFileUrl,
  type LibraryCategory,
  type LibraryItem,
  type StyleCount,
} from '@/api/assets'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  multiple: { type: Boolean, default: false },
  limit: { type: Number, default: 9 },
  // 打开时定位到的分类（icon 图标 / cover 封面 / logo LOGO / avatar 头像 /
  // background 背景 / poster 海报），由调用方按字段语义传入
  defaultCategory: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue', 'picked'])

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const loading = ref(false)
const categories = ref<LibraryCategory[]>([])
const items = ref<LibraryItem[]>([])
const tab = ref('all')
// 视觉调性（第二筛选维度）。styleCounts 由服务端按当前 分类+关键字 回算，
// 前端只展示 count>0 的调性；只有一档可选时整行隐藏，避免无意义的筛选。
const styleKey = ref('all')
const styleCounts = ref<StyleCount[]>([])
const keyword = ref('')
const page = ref(1)
// 每页 15 条 = 网格 5 列 × 3 排，弹窗内一次看全、无需滚动
const size = ref(15)
const total = ref(0)
const selected = ref<string[]>([])
const adoptingId = ref('')
const batchAdopting = ref(false)

const visibleStyles = computed(() => styleCounts.value.filter((s) => s.count > 0))

let searchTimer: ReturnType<typeof setTimeout> | null = null

// 服务端分页：条件（分类/关键字/页码）变化即重新拉取；分类清单随响应带回，无需单独接口
async function fetchPage() {
  loading.value = true
  try {
    const resp = await listLibrary({
      page: page.value,
      size: size.value,
      type: tab.value,
      style: styleKey.value,
      keyword: keyword.value.trim(),
    })
    if (resp.code === 0 && resp.data) {
      categories.value = resp.data.categories || []
      styleCounts.value = resp.data.styleCounts || []
      items.value = resp.data.items || []
      total.value = resp.data.total || 0
      page.value = resp.data.page || page.value
      size.value = resp.data.size || size.value
      // 当前调性在新条件下无素材（例如切到图标分类、或搜索命中面很窄）：
      // 回落「全部」重拉一次，避免停在空白结果页。all 必然可用，不会递归。
      if (styleKey.value !== 'all' && !visibleStyles.value.some((s) => s.key === styleKey.value)) {
        styleKey.value = 'all'
        return fetchPage()
      }
    }
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

// 打开时定位到调用方指定的分类，重置检索条件后拉第一页。
// immediate：挂载时即以 modelValue=true 打开的场景（父组件直接 v-model=true）也要拉数据。
watch(
  visible,
  (v) => {
    if (!v) return
    tab.value = props.defaultCategory || 'all'
    styleKey.value = 'all'
    keyword.value = ''
    page.value = 1
    selected.value = []
    fetchPage()
  },
  { immediate: true }
)

function onTabChange() {
  page.value = 1
  fetchPage()
}

function onStyleChange() {
  page.value = 1
  fetchPage()
}

// 搜索防抖：避免逐字请求
function onKeywordInput() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    page.value = 1
    fetchPage()
  }, 300)
}

function onPageChange(p: number) {
  page.value = p
  fetchPage()
}

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
})

function isSelected(id: string) {
  return selected.value.includes(id)
}

// 单选：点卡片 = 采用 + 填入 + 关闭（一步到位）；多选：点卡片累计选择（跨页保留）
async function onCardClick(it: LibraryItem) {
  if (props.multiple) {
    if (isSelected(it.id)) {
      selected.value = selected.value.filter((x) => x !== it.id)
    } else if (selected.value.length >= props.limit) {
      ElMessage.warning(`最多选择 ${props.limit} 个`)
    } else {
      selected.value.push(it.id)
    }
    return
  }
  adoptingId.value = it.id
  try {
    const resp = await adoptAsset(it.id)
    if (resp.code === 0 && resp.data?.url) {
      emit('picked', resp.data.url)
      visible.value = false
    }
  } catch {
    // 拦截器已提示
  } finally {
    adoptingId.value = ''
  }
}

// 多选：逐项采用（顺序执行，避免并发写同一存储），全部成功后回传 URL 数组
async function confirmMultiple() {
  if (!selected.value.length) return
  batchAdopting.value = true
  const urls: string[] = []
  try {
    for (const id of selected.value) {
      const resp = await adoptAsset(id)
      if (resp.code === 0 && resp.data?.url) urls.push(resp.data.url)
    }
    if (urls.length) {
      emit('picked', urls)
      visible.value = false
    }
  } catch {
    // 拦截器已提示
  } finally {
    batchAdopting.value = false
  }
}

function onClosed() {
  selected.value = []
}
</script>

<style scoped>
/* 弹窗 body（.asset-dialog）已是 flex 容器且自身不滚动：这里占满剩余高度，
   工具条与分页栏固定，网格区吃剩余高度并内部滚动 —— 分页栏因此钉在底部，
   数据多少都不会把它挤出可视区。 */
.asset-picker {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.picker-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.picker-search {
  width: 220px;
}
.picker-styles {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
.style-label {
  font-size: 13px;
  color: #909399;
  flex-shrink: 0;
}
.picker-grid {
  flex: 1 1 auto;
  min-height: 0;
  display: grid;
  align-content: start;
  /* 行高锁内容高：容器变矮时行不压缩（否则卡片图片会被裁），改为内部滚动 */
  grid-auto-rows: max-content;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 12px;
  overflow-y: auto;
  padding: 2px;
}
.asset-card {
  position: relative;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  overflow: hidden;
  cursor: pointer;
  background: #fff;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.asset-card:hover {
  border-color: #409eff;
  box-shadow: 0 2px 8px rgba(64, 158, 255, 0.15);
}
.asset-card.selected {
  border-color: #409eff;
  box-shadow: 0 0 0 1px #409eff inset;
}
.asset-img {
  display: block;
  width: 100%;
  height: 110px;
  object-fit: contain;
  background: #f7f8fa;
}
/* 首页背景为竖幅（750×1334）：卡片内放大并裁切，避免大片留白看不清样式 */
.asset-card.background .asset-img {
  height: 190px;
  object-fit: cover;
}
/* 海报是 640×920 竖幅且顶部烧着标题：按原比例整张显示，裁掉顶部就等于盲选
   （选的时候看不到「立春」还是「合作共赢」） */
.asset-card.poster .asset-img {
  height: auto;
  aspect-ratio: 640 / 920;
  object-fit: cover;
}
.asset-name {
  padding: 6px 8px;
  font-size: 12px;
  color: #606266;
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.asset-mask {
  position: absolute;
  inset: 0;
  background: rgba(255, 255, 255, 0.75);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  color: #409eff;
}
.asset-check {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #409eff;
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
}
/* 分页容器：居中紧凑，钉在网格下方（asset-picker 的 flex 保证不被挤出弹窗） */
.picker-pager {
  flex-shrink: 0;
  display: flex;
  justify-content: center;
  margin-top: 10px;
}
.picker-count {
  font-size: 13px;
  color: #909399;
  margin-right: 12px;
}
.picker-actions {
  float: right;
}
</style>
