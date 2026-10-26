<template>
  <el-card>
    <div class="page-bar">
      <h3>内容安全</h3>
      <div class="bar-right">
        <span class="bar-tip">图片违规由微信异步判定，确认后可一键下架</span>
        <el-button :icon="Refresh" :loading="loading" @click="reload">刷新</el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="图片检测是异步的：上传时送检，结论由微信推送回来"
      description="微信只提供异步接口，送出去只拿到一个追踪号，判定结果要等「消息推送」推回本系统。所以列表里的「待结论」是正常中间态；长时间停在待结论，通常是消息推送没配好（见下方配置状态）。命中违规后不会自动处理——覆盖是不可逆的，需要人工确认后点「下架」。"
      style="margin-bottom: 14px"
    />

    <!-- 配置状态：把「还缺什么」直接摆出来。这三项散在微信公众平台那边，
         只写在文档里没人看，放在界面上才真的配得起来。 -->
    <div class="cfg-box">
      <div class="cfg-head">
        <span class="cfg-title">推送配置</span>
        <el-tag v-if="cfg.enabled" size="small" type="success" effect="plain">检测已开启</el-tag>
        <el-tag v-else size="small" type="info" effect="plain">检测已关闭（系统设置 → 内容安全检测）</el-tag>
      </div>
      <div class="cfg-row">
        <span class="cfg-label">推送地址</span>
        <code class="cfg-code">{{ cfg.pushUrl || '未配置上传基址，无法推导推送地址' }}</code>
        <el-button v-if="cfg.pushUrl" size="small" text type="primary" @click="copy(cfg.pushUrl)">
          复制
        </el-button>
      </div>
      <div class="cfg-row">
        <span class="cfg-label">就绪情况</span>
        <span class="tag-row">
          <el-tag size="small" :type="cfg.credReady ? 'success' : 'danger'" effect="plain">
            小程序凭据 {{ cfg.credReady ? '已配' : '未配' }}
          </el-tag>
          <el-tag size="small" :type="cfg.tokenReady ? 'success' : 'danger'" effect="plain">
            推送 Token {{ cfg.tokenReady ? '已配' : '未配' }}
          </el-tag>
          <el-tag size="small" :type="cfg.aesReady ? 'success' : 'info'" effect="plain">
            EncodingAESKey {{ cfg.aesReady ? '已配' : '未配（仅安全模式需要）' }}
          </el-tag>
        </span>
      </div>
      <div v-if="!cfg.tokenReady" class="cfg-warn">
        没有推送 Token 时<strong>不会送检</strong>——送出去也收不到结论，只会造出一堆永远「待结论」的记录。
        请到「系统设置 → 微信」填 Token，再把上面的推送地址填到微信公众平台的消息推送配置里。
      </div>
      <div class="cfg-row">
        <span class="cfg-label">当前积压</span>
        <span class="tag-row">
          <el-tag size="small" type="warning" effect="plain">待结论 {{ cfg.pending }}</el-tag>
          <el-tag size="small" type="danger" effect="plain">违规未处置 {{ cfg.risky }}</el-tag>
        </span>
      </div>
    </div>

    <div class="filter-bar">
      <el-select v-model="status" clearable placeholder="全部状态" class="filter-item" @change="onFilter">
        <el-option v-for="(label, key) in STATUS_LABELS" :key="key" :label="label" :value="key" />
      </el-select>
      <el-checkbox v-model="onlyTakedownable" @change="onFilter">只看可下架</el-checkbox>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column label="上传时间" width="170" align="center">
        <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="图片" width="90" align="center">
        <template #default="{ row }">
          <ImageThumb v-if="row.url" :src="row.url" :width="44" :height="44" />
          <span v-else>—</span>
        </template>
      </el-table-column>
      <el-table-column label="来源" width="100">
        <template #default="{ row }">{{ SOURCE_LABELS[row.source] || row.source || '—' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="132">
        <template #default="{ row }">
          <el-tag :type="statusTag(row.status)" size="small" effect="plain">{{ statusLabel(row.status) }}</el-tag>
          <el-tag v-if="row.action === 'takedown'" size="small" type="danger" effect="dark" class="ml4">
            已下架
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="说明" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">
          <template v-if="row.status === 'risky'">违规标签 {{ row.label || '—' }}</template>
          <template v-else-if="row.note">{{ row.note }}</template>
          <template v-else-if="row.status === 'pending'">已送检，等微信推送结论</template>
          <template v-else>—</template>
        </template>
      </el-table-column>
      <el-table-column prop="traceId" label="追踪号" width="180" show-overflow-tooltip v-if="!isPhone" />
      <el-table-column label="操作" width="110" align="center" fixed="right">
        <template #default="{ row }">
          <el-button
            v-if="takedownable(asCheck(row))"
            size="small"
            type="danger"
            plain
            :loading="busyId === row.id"
            @click="onTakedown(asCheck(row))"
          >
            下架
          </el-button>
          <span v-else class="muted">—</span>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      class="table-pager"
      v-model:current-page="page"
      v-model:page-size="size"
      :total="total"
      :page-sizes="[20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      @current-change="load"
      @size-change="onFilter"
    />
  </el-card>
</template>

<script setup lang="ts">
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { onMounted, ref } from 'vue'
import ImageThumb from '@/components/ImageThumb.vue'
import {
  SOURCE_LABELS,
  STATUS_LABELS,
  getMediaCheckConfig,
  listMediaChecks,
  statusLabel,
  statusTag,
  takedownable,
  takedownMediaCheck,
  type MediaCheck,
  type MediaCheckConfig,
} from '@/api/mediaCheck'
import { formatDateTime } from '@/utils/datetime'
import { useBreakpoint } from '@/composables/useBreakpoint'

const { isPhone } = useBreakpoint()

const rows = ref<MediaCheck[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const size = ref(20)
const status = ref('')
const onlyTakedownable = ref(false)
const busyId = ref(0)

const cfg = ref<MediaCheckConfig>({
  enabled: true,
  tokenReady: false,
  aesReady: false,
  credReady: false,
  pushUrl: '',
  pending: 0,
  risky: 0,
})

// el-table 的 #default 插槽给的是 DefaultRow（无具体字段），
// 这里显式收敛成本页的行类型，避免每个表达式都写一遍 as。
function asCheck(row: unknown): MediaCheck {
  return row as MediaCheck
}

function onFilter() {
  page.value = 1
  load()
}

async function load() {
  loading.value = true
  try {
    const p = await listMediaChecks({
      page: page.value,
      size: size.value,
      status: status.value || undefined,
      onlyTakedownable: onlyTakedownable.value ? '1' : undefined,
    })
    rows.value = p?.list ?? []
    total.value = p?.total ?? 0
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

async function loadConfig() {
  try {
    cfg.value = await getMediaCheckConfig()
  } catch {
    // 配置状态拿不到不影响列表使用
  }
}

function reload() {
  load()
  loadConfig()
}

// 下架是不可逆的（对象被覆盖成占位图，原图当场丢失），所以必须先确认，
// 并且把「不可恢复」四个字写在按钮上，而不是藏在描述里。
async function onTakedown(row: MediaCheck) {
  try {
    await ElMessageBox.confirm(
      '下架会把这张图覆盖成「内容不可用」占位图，所有端立即生效，且无法恢复。确认这张图确实违规？',
      '确认下架',
      { type: 'warning', confirmButtonText: '确认下架（不可恢复）', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  busyId.value = row.id
  try {
    await takedownMediaCheck(row.id)
    ElMessage.success('已下架')
    reload()
  } catch {
    // 拦截器已提示
  } finally {
    busyId.value = 0
  }
}

async function copy(text: string) {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
    return
  }
  // 非 HTTPS / 无剪贴板权限时 navigator.clipboard 不存在：把内容显示出来供手动复制
  ElMessage.warning('当前环境不支持一键复制，请手动选中上面的地址复制')
}

onMounted(reload)
</script>

<style scoped>
.cfg-box {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  padding: 12px 14px;
  margin-bottom: 14px;
  background: var(--el-fill-color-blank);
}
.cfg-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.cfg-title {
  font-weight: 600;
  font-size: 14px;
}
.cfg-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.cfg-row:last-child {
  margin-bottom: 0;
}
.cfg-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  min-width: 68px;
}
.cfg-code {
  font-family: var(--el-font-family-mono, monospace);
  font-size: 12px;
  background: var(--el-fill-color-light);
  padding: 2px 8px;
  border-radius: 4px;
  word-break: break-all;
}
.cfg-warn {
  color: var(--el-color-danger);
  font-size: 12px;
  line-height: 1.6;
  margin: 0 0 10px;
}
.filter-bar {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  align-items: center;
  margin-bottom: 12px;
}
.filter-item {
  width: 180px;
}
.tag-row {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 8px;
}
.ml4 {
  margin-left: 4px;
}
.muted {
  color: var(--el-text-color-placeholder);
}
/* 手机端筛选器占满一行 */
@media (max-width: 767px) {
  .filter-item {
    width: 100%;
  }
}
</style>
