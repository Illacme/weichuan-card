<template>
  <!-- 服务端路径选择器：在**服务器**文件系统上挑路径（Node / 模块目录 / 源码目录），
       而不是浏览器原生文件框——后者选的是客户端机器，对这些指向服务器的配置毫无意义。
       安全由后端 pathpick 包兜底：白名单目录、符号链接解析、敏感点目录屏蔽，
       前端只负责把结果陈列出来并回填选中的路径。 -->
  <el-dialog
    :model-value="visible"
    :title="title"
    width="620px"
    append-to-body
    class="pp-dialog"
    @update:model-value="(v: any) => emit('update:visible', !!v)"
  >
    <div class="pp">
      <!-- 面包屑：点任一段跳回该前缀。根 "/" 只作静态前缀（白名单里没有 "/"，
           点它必然报「不在允许浏览范围」），各段之间的分隔符只画一次，
           否则 /Users/eason 会显示成 //Users/eason（2026-10-25 用户实测截图）。 -->
      <div class="pp-crumbs">
        <span class="pp-sep pp-sep-root">/</span>
        <template v-for="(seg, i) in segments" :key="seg.path">
          <span v-if="i > 0" class="pp-sep">/</span>
          <span class="pp-crumb" @click="navigate(seg.path)">{{ seg.name }}</span>
        </template>
        <span v-if="loading" class="pp-loading">（读取中…）</span>
      </div>

      <!-- 直接输入一个绝对路径并跳转：方便直接粘贴已知路径 -->
      <div class="pp-go">
        <el-input
          v-model="gotoInput"
          placeholder="直接输入服务器绝对路径，如 /opt/weichuan/src"
          size="small"
          @keyup.enter="goGoto"
        />
        <el-button size="small" @click="goGoto">前往</el-button>
        <el-button v-if="parent" size="small" text @click="navigate(parent)">上一级</el-button>
        <el-button v-if="home" size="small" text @click="navigate(home)">回家目录</el-button>
      </div>

      <!-- 选中状态的实时校验（与后端 CheckPath 同源口径）。
           三态：ok 绿 = 可直接选；info 灰 = 还不能用但属于正常浏览过程（如 node 项正停在目录上）；
           error 红 = 请求失败 / 路径不在允许范围。 -->
      <div class="pp-status" :class="statusTone">
        <span class="pp-dot" />
        <span class="pp-status-text">{{ statusText }}</span>
      </div>

      <!-- 目录 / 文件列表 -->
      <div class="pp-list" v-loading="loading">
        <div v-if="!loading && !entries.length" class="pp-empty">
          {{ loadError ? '无法读取该目录' : '该目录下没有可选项' }}
        </div>
        <div
          v-for="e in entries"
          :key="e.path"
          class="pp-entry"
          :class="{ active: e.path === selected }"
          @click="navigate(e.path)"
        >
          <span class="pp-ico">{{ e.type === 'dir' ? '📁' : '📄' }}</span>
          <span class="pp-name">{{ e.name }}</span>
          <el-tag v-if="e.marker" size="small" type="success" effect="plain">{{ e.marker }}</el-tag>
        </div>
        <div v-if="truncated" class="pp-trunc">（结果过多，已截断，请用「前往」定位更深的目录）</div>
      </div>

      <!-- 自动探测到的候选：免去逐层翻目录。
           同一 basename 可能来自不同目录（PATH 里的 node 与 /usr/local/bin/node 都叫 node），
           所以每行都带上完整路径，否则两条候选长得一模一样、点哪个都不知道。 -->
      <div v-if="suggestions.length" class="pp-suggest">
        <div class="pp-suggest-title">自动探测到的候选：</div>
        <div
          v-for="s in suggestions"
          :key="s.path"
          class="pp-suggest-item"
          :class="{ active: s.path === selected }"
          @click="navigate(s.path)"
        >
          <span class="pp-suggest-main">
            <span class="pp-name">{{ s.label }}</span>
            <span class="pp-suggest-path">{{ s.path }}</span>
          </span>
          <span class="pp-reason">{{ s.reason }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <span class="pp-foot-msg" :class="statusTone">
        {{ current.ok ? '当前选中可用' : '请选择一个有效路径' }}
      </span>
      <el-button @click="emit('update:visible', false)">取消</el-button>
      <el-button type="primary" :disabled="!current.ok || loading" @click="confirm">
        选择此路径
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { browseServerPaths, type PathBrowseResult } from '@/api/settings'

const props = defineProps<{
  kind: string
  /** 当前已填写的值（绝对路径）：作为打开时的初始目录 */
  modelValue?: string
  visible: boolean
}>()

const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void
  (e: 'pick', path: string): void
}>()

const TITLE: Record<string, string> = {
  node_bin: '选择 Node 可执行文件',
  module_dir: '选择 miniprogram-ci 安装目录',
  source_dir: '选择小程序源码目录',
}
const title = computed(() => TITLE[props.kind] || '选择服务器路径')

// dirPath = 正在列举的目录；selected = 当前待选中的路径（node 项可能是目录里的某个文件）。
// 两者分开：点一个 node 文件时列表仍停在该文件所在目录，但「选中」已经是那个文件。
const dirPath = ref('')
const selected = ref('')
const selectedIsDir = ref(false)
const parent = ref('')
const home = ref('')
const entries = ref<PathBrowseResult['entries']>([])
const suggestions = ref<PathBrowseResult['suggestions']>([])
const current = ref<PathBrowseResult['current']>({ ok: false, message: '' })
const truncated = ref(false)
const loading = ref(false)
const gotoInput = ref('')
const loadError = ref('')

// 面包屑：把当前目录拆成可点击的前缀段
const segments = computed(() => {
  const p = dirPath.value
  if (!p || p === '/') return []
  const parts = p.split('/').filter(Boolean)
  const segs: { name: string; path: string }[] = []
  let acc = ''
  for (const part of parts) {
    acc += '/' + part
    segs.push({ name: part, path: acc })
  }
  return segs
})

// 三态提示：ok（可选）/ info（正常浏览中还不能选）/ error（请求失败或越界）
const statusTone = computed(() => {
  if (loadError.value) return 'error'
  return current.value.ok ? 'ok' : 'info'
})
const statusText = computed(() => {
  if (loadError.value) return loadError.value
  if (current.value.ok) return current.value.message
  // node 项停在目录上是常态（还没点选文件），给一句「下一步做什么」，而不是把
  // 「找不到 node 可执行文件（/Users/eason）」这种红字甩给用户（截图里就是这么吓人的）。
  if (props.kind === 'node_bin' && selectedIsDir.value) {
    return `当前是目录：请在下方列表点选 node 可执行文件（带「node 可执行文件」徽标的那个），再点「选择此路径」`
  }
  return current.value.message || '当前路径不能用于该项配置'
})

// 打开弹窗时初始化：以已填的绝对路径为起点（若填的是命令名 node 则回落后端给的默认起点）
function onOpen() {
  const v = String(props.modelValue || '')
  gotoInput.value = v.startsWith('/') ? v : ''
  dirPath.value = ''
  selected.value = gotoInput.value
  selectedIsDir.value = false
  entries.value = []
  suggestions.value = []
  loadError.value = ''
  void load(gotoInput.value)
}

// dir 缺省时后端给一个合理起点；请求静默失败（错误就地展示，不弹全局红条）
async function load(dir: string) {
  loading.value = true
  loadError.value = ''
  try {
    const r = await browseServerPaths(props.kind, dir || undefined)
    if (r.code === 0 && r.data) {
      const d = r.data
      dirPath.value = d.path || ''
      parent.value = d.parent || ''
      home.value = d.home || ''
      // 数组字段一律兜成 []：后端老版本 / 异常分支可能给 null，
      // 而模板里按数组用（entries.length）。null 会让渲染函数抛异常、
      // 弹窗整个渲染失败（「点了没反应」就是这么来的）。
      entries.value = Array.isArray(d.entries) ? d.entries : []
      suggestions.value = Array.isArray(d.suggestions) ? d.suggestions : []
      current.value = d.current && typeof d.current.ok === 'boolean' ? d.current : { ok: false, message: '' }
      selected.value = d.selected || dirPath.value
      selectedIsDir.value = !!d.selectedIsDir
      truncated.value = !!d.truncated
      gotoInput.value = selected.value
    } else {
      failWith(r.msg)
    }
  } catch (e: any) {
    failWith(e?.response?.data?.msg)
  } finally {
    loading.value = false
  }
}

function failWith(msg?: string) {
  entries.value = []
  suggestions.value = []
  current.value = { ok: false, message: '' }
  loadError.value = msg || '无法读取该目录（路径不存在或不在允许浏览的范围内）'
}

function navigate(path: string) {
  gotoInput.value = path
  void load(path)
}

function goGoto() {
  const p = gotoInput.value.trim()
  if (p) navigate(p)
}

function confirm() {
  if (!current.value.ok || loading.value || !selected.value) return
  emit('pick', selected.value)
  emit('update:visible', false)
}

// 打开时加载目录。只在 visible 为 true 时拉一次：
// - immediate 是必须的：SettingRow 里本组件是「首次点开才挂载」（pickerReady），
//   挂载那一刻 visible 已经是 true，没有 immediate 就永远不会触发加载 → 空白弹窗。
// - 回调里的 if (v) 保证「未打开就挂载」的场景（如果将来改成常驻挂载）不发请求，
//   否则设置页 6 个 pathKind 项会各发一次白跑请求。
// - 不用 el-dialog 的 @open：它会与 watch 各触发一次 → 两个请求、两条错误提示。
watch(
  () => props.visible,
  (v) => {
    if (v) onOpen()
  },
  { immediate: true },
)
</script>

<style scoped>
.pp {
  font-size: 13px;
}
.pp-crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  padding: 6px 8px;
  background: #f5f7fa;
  border-radius: 4px;
  color: #606266;
  min-height: 30px;
}
.pp-crumb {
  cursor: pointer;
  color: #409eff;
}
.pp-crumb-root {
  cursor: default;
  color: #909399;
}
.pp-crumb:hover {
  text-decoration: underline;
}
.pp-sep {
  color: #c0c4cc;
}
.pp-loading {
  color: #909399;
  margin-left: 6px;
}
.pp-go {
  display: flex;
  gap: 6px;
  margin: 10px 0;
}
.pp-go .el-input {
  flex: 1;
}
.pp-status {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-bottom: 8px;
  padding: 6px 8px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.6;
}
.pp-status .pp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
  margin-top: 5px;
}
.pp-status.ok {
  background: #f0f9eb;
  color: #67c23a;
}
.pp-status.ok .pp-dot {
  background: #67c23a;
}
.pp-status.info {
  background: #f4f4f5;
  color: #909399;
}
.pp-status.info .pp-dot {
  background: #c0c4cc;
}
.pp-status.error {
  background: #fef0f0;
  color: #f56c6c;
}
.pp-status.error .pp-dot {
  background: #f56c6c;
}
.pp-list {
  min-height: 180px;
  max-height: 260px;
  overflow: auto;
  border: 1px solid #ebeef5;
  border-radius: 4px;
}
.pp-empty {
  padding: 40px 0;
  text-align: center;
  color: #c0c4cc;
}
.pp-entry {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  cursor: pointer;
  border-bottom: 1px solid #f5f7fa;
}
.pp-entry:hover {
  background: #ecf5ff;
}
.pp-entry.active {
  background: #d9ecff;
}
.pp-ico {
  flex: none;
}
.pp-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pp-trunc {
  padding: 6px 10px;
  color: #e6a23c;
  font-size: 12px;
}
.pp-suggest {
  margin-top: 10px;
}
.pp-suggest-title {
  color: #909399;
  margin-bottom: 4px;
}
.pp-suggest-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 5px 8px;
  cursor: pointer;
  border-radius: 4px;
}
.pp-suggest-item:hover {
  background: #ecf5ff;
}
.pp-suggest-item.active {
  background: #d9ecff;
}
.pp-suggest-main {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  flex: 1;
}
.pp-suggest-path {
  color: #c0c4cc;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pp-reason {
  color: #c0c4cc;
  font-size: 12px;
  flex: none;
}
.pp-foot-msg {
  margin-right: auto;
  font-size: 12px;
}
.pp-foot-msg.ok {
  color: #67c23a;
}
.pp-foot-msg.info,
.pp-foot-msg.error {
  color: #909399;
}
</style>
