<template>
  <el-card>
    <div class="page-bar">
      <h3>备份与恢复</h3>
      <div class="bar-right">
        <span class="bar-tip">自动备份跑在服务器 cron 上，这一页负责「看得见」和「临时补一份」</span>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button
          type="primary"
          :icon="FolderAdd"
          :loading="running"
          :disabled="!status.canRun || running"
          @click="onRun"
        >
          {{ running ? '备份中…' : '立即备份' }}
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="备份是三件套，缺一件就救不回来"
      description="数据库（全部业务数据）+ 上传文件（名片图片）+ 运行时密钥（PII_ENC_KEY 等）。少了密钥包，库里的姓名、手机是密文，恢复出来也读不出内容；少了图片，页面会成片空白。"
      style="margin-bottom: 14px"
    />

    <!-- 缺件、不可下载等「必须当面说清」的话。放在表格上方而不是折叠区：
         它们正是这个面板存在的理由，藏起来就等于没做。 -->
    <el-alert
      v-for="(n, i) in status.notes"
      :key="i"
      type="warning"
      :closable="false"
      show-icon
      :title="n"
      class="note-alert"
    />

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column label="类别" width="140">
        <template #default="{ row }">
          <div class="kind-cell">
            <span>{{ row.label }}</span>
            <el-tooltip :content="row.hint" placement="top">
              <el-icon class="kind-hint"><QuestionFilled /></el-icon>
            </el-tooltip>
          </div>
        </template>
      </el-table-column>

      <el-table-column label="最新备份" min-width="240" show-overflow-tooltip>
        <template #default="{ row }">
          <span v-if="row.item" class="mono">{{ row.item.file }}</span>
          <el-tag v-else type="danger" size="small" effect="plain">缺失</el-tag>
        </template>
      </el-table-column>

      <el-table-column label="大小" width="100" align="right" v-if="!isPhone">
        <template #default="{ row }">{{ row.item ? formatBytes(row.item.bytes) : '—' }}</template>
      </el-table-column>

      <el-table-column label="备份时间" width="170" align="center">
        <template #default="{ row }">
          {{ row.item ? formatDateTime(row.item.mtime) : '—' }}
        </template>
      </el-table-column>

      <el-table-column label="新鲜度" width="130" align="center">
        <template #default="{ row }">
          <el-tag v-if="!row.item" type="danger" size="small" effect="plain">无</el-tag>
          <el-tag v-else-if="row.item.fresh" type="success" size="small" effect="plain">
            {{ describeAge(row.item.ageHours) }}
          </el-tag>
          <el-tag v-else type="warning" size="small" effect="plain">
            {{ describeAge(row.item.ageHours) }}
          </el-tag>
        </template>
      </el-table-column>

      <el-table-column label="操作" width="120" align="center">
        <template #default="{ row }">
          <!-- 密钥包刻意不给下载按钮：界面上给了，就等于把密钥和数据放回同一个故障域 -->
          <el-tooltip v-if="row.kind === 'secrets'" content="密钥包请从服务器 scp/rsync 取走，与 BACKUP_ENC_KEY 分开保管" placement="top">
            <span class="no-download">不提供下载</span>
          </el-tooltip>
          <el-button
            v-else-if="row.item"
            link
            type="primary"
            :icon="Download"
            @click="onDownload(row.item)"
          >
            下载
          </el-button>
          <span v-else class="no-download">—</span>
        </template>
      </el-table-column>
    </el-table>

    <!-- 最近一次手动备份的结果。失败必须红着留在页面上，不能一闪而过的 toast：
         备份失败是「明天要恢复时才发现」的那类事故。 -->
    <el-alert
      v-if="status.lastRun"
      class="last-run"
      :type="status.lastRun.ok ? 'success' : 'error'"
      :closable="false"
      show-icon
      :title="`最近一次手动备份（${formatDateTime(status.lastRun.at)}）${status.lastRun.ok ? '成功' : '失败'}`"
      :description="status.lastRun.message"
    />

    <div class="foot">
      <div>
        备份目录：<span class="mono">{{ status.dir || '—' }}</span>
        <span class="sep">·</span>
        数据库形态：<span class="mono">{{ status.driver || '—' }}</span>
        <span class="sep">·</span>
        新鲜度阈值：{{ status.freshHours }} 小时
      </div>
      <div class="foot-tip">{{ status.automaticTip }}</div>
      <div v-if="!status.canRun" class="foot-tip">
        当前为 MySQL 形态，后台不代跑备份（一致性快照要靠 mysqldump）：请在服务器执行
        <span class="mono">scripts/backup.sh</span>，这里只负责查看与下载。
      </div>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Download, FolderAdd, QuestionFilled, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import {
  BACKUP_KINDS,
  describeAge,
  downloadBackup,
  formatBytes,
  getBackupStatus,
  runBackup,
  type BackupItem,
  type BackupStatus,
} from '@/api/backup'
import { formatDateTime } from '@/utils/datetime'
import { useBreakpoint } from '@/composables/useBreakpoint'

const { isPhone } = useBreakpoint()

const status = ref<BackupStatus>({
  dir: '',
  driver: '',
  canRun: false,
  running: false,
  freshHours: 26,
  items: [],
  lastRun: null,
  notes: [],
  automaticTip: '',
})
const loading = ref(false)
const running = ref(false)

// 三行固定顺序，缺的那件照样占一行并显示「缺失」——只渲染后端返回的 items
// 会让「少了密钥包」这种最该被看见的事从界面上消失。
const rows = computed(() =>
  BACKUP_KINDS.map((k) => ({
    ...k,
    item: status.value.items.find((i) => i.kind === k.kind) ?? null,
  })),
)

let pollTimer: ReturnType<typeof setTimeout> | undefined
let pollCount = 0
const POLL_INTERVAL = 2000
const POLL_MAX = 150 // 最多约 5 分钟：GB 级图片打包也就这个量级

function stopPoll() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = undefined
  pollCount = 0
}

// 轮询而不是等一次请求：备份在服务端异步跑（同步跑会拖到反代超时，用户只看到 504，
// 以为失败了，实际文件还在写）。这里等 running 变 false 再停。
async function poll() {
  pollCount++
  const s = await getBackupStatus()
  status.value = s
  running.value = s.running
  if (!s.running) {
    stopPoll()
    if (s.lastRun) {
      if (s.lastRun.ok) {
        ElMessage.success('备份完成')
      } else {
        ElMessage.error('备份失败：' + s.lastRun.message)
      }
    }
    return
  }
  if (pollCount >= POLL_MAX) {
    stopPoll()
    ElMessage.warning('备份仍在进行，可稍后点「刷新」查看结果')
    return
  }
  pollTimer = setTimeout(poll, POLL_INTERVAL)
}

async function load() {
  loading.value = true
  try {
    status.value = await getBackupStatus()
    // 别人（或上一次没关掉的轮询）触发的任务还在跑：接着盯
    running.value = status.value.running
    if (status.value.running && !pollTimer) {
      pollCount = 0
      pollTimer = setTimeout(poll, POLL_INTERVAL)
    }
  } finally {
    loading.value = false
  }
}

async function onRun() {
  if (!status.value.canRun) return
  running.value = true
  try {
    await runBackup()
    ElMessage.info('备份已开始，完成后本页会自动更新')
    pollCount = 0
    stopPoll()
    pollTimer = setTimeout(poll, POLL_INTERVAL)
  } catch {
    // 拦截器已提示（并发、MySQL 形态等）
    running.value = false
  }
}

async function onDownload(item: BackupItem) {
  try {
    await downloadBackup(item.file)
    ElMessage.success('已开始下载：' + item.file)
  } catch (e) {
    ElMessage.error((e as Error).message || '下载失败')
  }
}

onMounted(load)
onBeforeUnmount(stopPoll)
</script>

<style scoped>
.kind-cell {
  display: flex;
  align-items: center;
  gap: 4px;
}
.kind-hint {
  color: var(--el-text-color-placeholder);
  cursor: help;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  word-break: break-all;
}
.note-alert {
  margin-bottom: 10px;
}
.last-run {
  margin-top: 14px;
}
.foot {
  margin-top: 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.9;
}
.foot-tip {
  color: var(--el-text-color-placeholder);
}
.sep {
  margin: 0 6px;
  color: var(--el-text-color-placeholder);
}
.no-download {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  cursor: help;
}
</style>
