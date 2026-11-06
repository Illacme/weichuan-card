<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>开通审核</h3>
        <div class="bar-right">
          <el-input
            v-model="keyword"
            placeholder="搜索姓名/手机/邮箱/昵称/openid"
            clearable
            :prefix-icon="Search"
            style="width: 260px"
          />
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        </div>
      </div>

      <!--
        列宽口径同员工管理：固定 width 之和 + min-width 之和 ≈ 1024px < 内容区约 1170px。
        本表历史上曾达 1200px（头像 80 / 状态 100 / 邮箱与驳回原因 min 160）超出容器，
        与 Employees 同款遮挡风险。改动列宽请按同一口径重算，宁紧勿松。
      -->
      <el-table v-if="!isPhone" :data="rows" v-loading="loading" border stripe>
        <!-- 提交时间放第一列：列表按 created_at desc 排，审核先要看到「谁等得最久」。
             手机端卡片没有「列」的概念，同样把这一行放到卡片最上面。 -->
        <el-table-column prop="createdAt" label="提交时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column prop="name" label="姓名" width="100" />
        <!-- 副号并在主号下方（muted 小字）：不加列，沿用员工管理页同款写法 -->
        <el-table-column label="手机" width="120">
          <template #default="{ row }">
            <div>{{ row.phone }}</div>
            <div v-if="row.phone2" class="sub-phone">{{ row.phone2 }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="nickname" label="微信昵称" width="100" show-overflow-tooltip>
          <template #default="{ row }">
            <span :class="{ muted: !row.nickname }">{{ row.nickname || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="openid" label="openid" width="210" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="openid-cell" :class="{ muted: !row.openid }">{{ row.openid || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="landline" label="座机" width="120" show-overflow-tooltip />
        <el-table-column prop="email" label="邮箱" min-width="130" show-overflow-tooltip />
        <el-table-column prop="title" label="职位" width="110" />
        <el-table-column label="头像" width="62" align="center">
          <template #default="{ row }">
            <ImageThumb
              v-if="row.avatar"
              :src="row.avatar"
              :width="36"
              :height="36"
            />
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="72" align="center">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" effect="light">{{ statusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="rejectReason" label="驳回原因" min-width="130" show-overflow-tooltip />
        <el-table-column label="操作" width="120" fixed="right" align="center">
          <template #default="{ row }">
            <template v-if="row.status === 'pending'">
              <el-button type="success" link :loading="actingId === row.id" @click="approve(row)">通过</el-button>
              <el-button type="danger" link @click="openReject(row)">驳回</el-button>
            </template>
            <span v-else class="muted">已处理</span>
          </template>
        </el-table-column>
      </el-table>

      <!-- 手机端（<768）卡片列表：复用 rows 与 approve/openReject，桌面仍走 el-table -->
      <div v-else class="apply-cards" v-loading="loading">
        <div class="apply-card" v-for="row in rows" :key="row.id">
          <div class="apply-card-row"><span class="apply-k">提交时间</span><span class="apply-v">{{ formatDateTime(row.createdAt) }}</span></div>
          <div class="apply-card-head">
            <div class="apply-card-id">
              <div class="apply-name">{{ row.name }}</div>
              <div class="apply-sub">{{ row.title || '未填写职位' }}</div>
            </div>
            <el-tag :type="statusTag(row.status)" effect="light" size="small">
              {{ statusText(row.status) }}
            </el-tag>
          </div>
          <div class="apply-card-row"><span class="apply-k">手机</span><span class="apply-v">{{ row.phone }}</span></div>
          <div class="apply-card-row" v-if="row.phone2"><span class="apply-k">手机2</span><span class="apply-v">{{ row.phone2 }}</span></div>
          <div class="apply-card-row" v-if="row.landline"><span class="apply-k">座机</span><span class="apply-v">{{ row.landline }}</span></div>
          <div class="apply-card-row" v-if="row.email"><span class="apply-k">邮箱</span><span class="apply-v">{{ row.email }}</span></div>
          <div class="apply-card-row" v-if="row.nickname"><span class="apply-k">昵称</span><span class="apply-v">{{ row.nickname }}</span></div>
          <div class="apply-card-row" v-if="row.rejectReason"><span class="apply-k">驳回</span><span class="apply-v">{{ row.rejectReason }}</span></div>
          <div class="apply-card-actions">
            <template v-if="row.status === 'pending'">
              <el-button size="small" type="success" :loading="actingId === row.id" @click="approve(row)">通过</el-button>
              <el-button size="small" type="danger" @click="openReject(row)">驳回</el-button>
            </template>
            <span v-else class="muted">已处理</span>
          </div>
        </div>
      </div>

      <el-pagination
        class="table-pager"
        v-model:current-page="currentPage"
        v-model:page-size="size"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        background
      />
    </el-card>

    <!-- 驳回原因 -->
    <el-dialog v-model="rejectVisible" title="驳回申请" width="440px" class="resp-dialog">
      <el-input
        v-model="rejectReason"
        type="textarea"
        :rows="3"
        placeholder="请填写驳回原因（必填）"
      />
      <template #footer>
        <el-button @click="rejectVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!rejectReason.trim()" @click="submitReject">确定驳回</el-button>
      </template>
    </el-dialog>

    <!-- 通过结果：一次性初始密码 -->
    <el-dialog v-model="okVisible" title="开通成功" width="420px" class="resp-dialog">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="员工编号">{{ okResult.staffId }}</el-descriptions-item>
        <el-descriptions-item label="初始密码">
          <span class="pwd">{{ okResult.initialPassword }}</span>
        </el-descriptions-item>
      </el-descriptions>
      <p class="tip">初始密码仅展示一次，请线下通知员工登录后尽快修改。</p>
      <template #footer>
        <el-button type="primary" @click="okVisible = false">知道了</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, onMounted } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { listOpenApply, approveOpen, rejectOpen } from '@/api/apply'
// 服务端分页：申请记录只增不减（已处理的也留档），关键字/页码都交给后端
import { useServerTable } from '@/composables/useServerTable'
import ImageThumb from '@/components/ImageThumb.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { formatDateTime } from '@/utils/datetime'

const { rows, keyword, currentPage, size, total, loading, load } = useServerTable((q) =>
  listOpenApply(q)
)
const actingId = ref<number | null>(null)
// 手机端（<768）隐藏表格、改走卡片列表；平板/桌面保留 el-table + 横滚
const { isPhone } = useBreakpoint()

const rejectVisible = ref(false)
const rejectReason = ref('')
const rejectId = ref<number | null>(null)

const okVisible = ref(false)
const okResult = ref({ staffId: 0, initialPassword: '' })

function statusText(s: string) {
  return s === 'pending' ? '待审核' : s === 'approved' ? '已通过' : '已驳回'
}
function statusTag(s: string) {
  return s === 'pending' ? 'success' : s === 'approved' ? 'primary' : 'danger'
}

async function approve(row: any) {
  actingId.value = row.id
  try {
    const r = await approveOpen(row.id)
    okResult.value = { staffId: r.staffId, initialPassword: r.initialPassword }
    okVisible.value = true
    await load()
  } catch {
    /* 拦截器已提示（已处理等） */
  } finally {
    actingId.value = null
  }
}

function openReject(row: any) {
  rejectId.value = row.id
  rejectReason.value = ''
  rejectVisible.value = true
}

async function submitReject() {
  if (!rejectId.value) return
  try {
    await rejectOpen(rejectId.value, rejectReason.value.trim())
    ElMessage.success('已驳回')
    rejectVisible.value = false
    await load()
  } catch {
    /* 拦截器已提示 */
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
.bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.muted {
  color: #c0c4cc;
}
/* 副号：主号下方的次要小字，视觉上从属于「手机」列（同员工管理页） */
.sub-phone {
  color: #a8abb2;
  font-size: 12px;
  line-height: 1.4;
}
.openid-cell {
  font-family: monospace;
  font-size: 12px;
}
.pwd {
  font-family: monospace;
  font-size: 16px;
  letter-spacing: 1px;
  color: #e6a23c;
}
.tip {
  color: #909399;
  font-size: 12px;
  margin: 12px 0 0;
}

/* ===== 手机端开通审核卡片列表 ===== */
.apply-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.apply-card {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 12px 14px;
  background: #fff;
}
.apply-card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.apply-card-id {
  flex: 1;
  min-width: 0;
}
.apply-name {
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.apply-sub {
  color: #909399;
  font-size: 13px;
  margin-top: 2px;
}
.apply-card-row {
  display: flex;
  font-size: 13px;
  line-height: 1.8;
}
.apply-k {
  width: 48px;
  flex-shrink: 0;
  color: #909399;
}
.apply-v {
  color: #303133;
  word-break: break-all;
}
.apply-card-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid #f2f4f7;
}
</style>
