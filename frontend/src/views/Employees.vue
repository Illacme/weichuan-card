<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>员工管理</h3>
        <div class="bar-right">
          <el-input
            v-model="keyword"
            placeholder="搜索姓名/手机/手机2/职位/座机/昵称/openid"
            clearable
            :prefix-icon="Search"
            style="width: 280px"
          />
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button :icon="Download" :loading="exporting" @click="exportAll">批量导出</el-button>
          <el-button type="primary" :icon="Plus" @click="openCreate">新增员工</el-button>
        </div>
      </div>

      <!--
        列宽是「算」出来的，不是拍的：固定 width 之和 + min-width 之和 ≈ 1116px，
        需小于后台内容区宽度（约 1170px）。历史上操作列曾设 280px，加上其余列
        合计 1380px 超出容器，fixed="right" 的操作列直接压住「创建时间」列。
        改动列宽时请按同一口径重算，宁紧勿松。操作列现为 260px（预览/编辑/启用
        或停用/重置密码/导出 五个 link 按钮，超窄会折行或挤占创建时间列）。
      -->
      <el-table v-if="!isPhone" :data="rows" v-loading="loading" border stripe>
        <el-table-column prop="staffId" label="编号" width="62" align="center" />
        <el-table-column prop="name" label="姓名" min-width="90" class-name="cell-nowrap" show-overflow-tooltip />
        <el-table-column prop="title" label="职位" width="96" class-name="cell-nowrap" show-overflow-tooltip />
        <!-- 副号直接并在主号下方（muted 小字）：联系表是「姓名/手机」两列布局，
             再加一列会顶破列宽预算（见上方列宽核算注释），故同行双行展示 -->
        <el-table-column label="手机" width="120">
          <template #default="{ row }">
            <div>{{ row.phone }}</div>
            <div v-if="row.phone2" class="sub-phone">{{ row.phone2 }}</div>
          </template>
        </el-table-column>
        <el-table-column label="座机" width="132" v-if="!isPhone" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="landline-cell" :class="{ muted: !row.landline }">{{ row.landline || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="email" label="邮箱" min-width="120" show-overflow-tooltip />
        <el-table-column prop="nickname" label="微信昵称" width="100" v-if="!isPhone" show-overflow-tooltip>
          <template #default="{ row }">
            <span :class="{ muted: !row.nickname }">{{ row.nickname || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="openid" label="openid" width="210" v-if="!isPhone" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="openid-cell" :class="{ muted: !row.openid }">{{ row.openid || '—' }}</span>
          </template>
        </el-table-column>
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
            <el-tag :type="row.status === 'enabled' ? 'success' : 'info'" effect="light">
              {{ row.status === 'enabled' ? '正常' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="180" v-if="!isPhone" show-overflow-tooltip>
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right" align="center">
          <template #default="{ row }">
            <el-button type="primary" link @click="preview(row)">预览</el-button>
            <el-button type="primary" link @click="openEdit(row)">编辑</el-button>
            <el-button
              v-if="row.status === 'disabled'"
              type="success"
              link
              :loading="actingId === row.staffId"
              @click="enable(row)"
              >启用</el-button
            >
            <el-button v-else type="warning" link :loading="actingId === row.staffId" @click="disable(row)"
              >停用</el-button
            >
            <el-button
              type="primary"
              link
              :loading="actingId === row.staffId"
              @click="reset(row)"
              >重置密码</el-button
            >
            <el-button type="info" link @click="exportInfo(row)">导出</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 手机端（<768）卡片列表：复用 rows 与既有操作函数，桌面仍走 el-table。
           卡片只保留核心字段 + 操作，避免窄屏横向滚动。 -->
      <div v-else class="emp-cards" v-loading="loading">
        <div class="emp-card" v-for="row in rows" :key="row.staffId">
          <div class="emp-card-head">
            <ImageThumb v-if="row.avatar" :src="row.avatar" :width="44" :height="44" />
            <div v-else class="emp-avatar ph">—</div>
            <div class="emp-card-id">
              <div class="emp-name">{{ row.name }} <span class="emp-staff">#{{ row.staffId }}</span></div>
              <div class="emp-title">{{ row.title || '未填写职位' }}</div>
            </div>
            <el-tag :type="row.status === 'enabled' ? 'success' : 'info'" effect="light" size="small">
              {{ row.status === 'enabled' ? '正常' : '已停用' }}
            </el-tag>
          </div>
          <div class="emp-card-row"><span class="emp-k">手机</span><span class="emp-v">{{ row.phone }}</span></div>
          <div class="emp-card-row" v-if="row.phone2"><span class="emp-k">手机号2</span><span class="emp-v">{{ row.phone2 }}</span></div>
          <div class="emp-card-row" v-if="row.landline"><span class="emp-k">座机</span><span class="emp-v">{{ row.landline }}</span></div>
          <div class="emp-card-row" v-if="row.email"><span class="emp-k">邮箱</span><span class="emp-v">{{ row.email }}</span></div>
          <div class="emp-card-actions">
            <el-button size="small" @click="preview(row)">预览</el-button>
            <el-button size="small" type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button
              v-if="row.status === 'disabled'"
              size="small"
              type="success"
              :loading="actingId === row.staffId"
              @click="enable(row)"
              >启用</el-button
            >
            <el-button v-else size="small" type="warning" :loading="actingId === row.staffId" @click="disable(row)"
              >停用</el-button
            >
            <el-button
              size="small"
              type="primary"
              plain
              :loading="actingId === row.staffId"
              @click="reset(row)"
              >重置密码</el-button
            >
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

    <!-- 新增 / 编辑员工（同一表单：新增走创建接口并弹初始密码，编辑直接覆盖资料） -->
    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? '编辑员工' : '新增员工'"
      width="520px"
      class="resp-dialog"
      @closed="resetForm"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
        <el-form-item label="姓名" prop="name">
          <el-input v-model="form.name" placeholder="必填" />
        </el-form-item>
        <el-form-item label="手机" prop="phone">
          <div class="phone-row">
            <el-input v-model="form.phone" type="tel" placeholder="必填，作为登录账号" />
            <!-- 与小程序端同一套交互：默认只露一个手机号输入框（多数人只有一个号），
                 需要第二个时点右侧「+」展开，避免一上来两栏空框让人以为都要填 -->
            <el-button v-if="!showPhone2" class="add-phone-btn" size="small" round @click="showPhone2 = true">
              <span class="add-plus">+</span> 备用手机号
            </el-button>
          </div>
        </el-form-item>
        <!-- 备用手机号：不设标签（用户 2026-10-20 要求），用输入框占位提示「备用手机号」。
             label 给一个空格占位：el-form-item 无 label 时内容会顶到 x=0，与其它行的输入框错位 -->
        <el-form-item v-if="showPhone2" label=" ">
          <div class="phone-row">
            <el-input v-model="form.phone2" type="tel" placeholder="备用手机号" />
            <el-button class="del-phone-btn" size="small" round @click="removePhone2">移除</el-button>
          </div>
          <p class="field-tip">选填。填写后与「手机」在名片上并列展示；主号仍是登录账号</p>
        </el-form-item>
        <el-form-item label="职位">
          <el-input v-model="form.title" />
        </el-form-item>
        <el-form-item label="座机">
          <el-input v-model="form.landline" type="tel" placeholder="选填，如 021-88888888" />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="form.email" type="email" />
        </el-form-item>
        <el-form-item label="头像">
          <UploadField v-model="form.avatar" :multiple="false" accept="image/*" library-type="avatar" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 名片预览（C2 要求「预览」）：PC 端打不开小程序，用名片摘要 + 小程序码近似。
         小程序码拉取失败（典型：本地开发未配 WX_APPID/WX_APPSECRET）时静默降级——
         api/wechat.ts 走 __silent，这里只把二维码位置换成文字提示，不弹红条。 -->
    <el-dialog v-model="previewVisible" title="名片预览" width="440px" class="resp-dialog">
      <div class="preview-card" v-if="previewRow">
        <div class="preview-head">
          <ImageThumb
            v-if="previewRow.avatar"
            :src="previewRow.avatar"
            :width="56"
            :height="56"
            radius="50%"
          />
          <div class="preview-avatar ph" v-else>—</div>
          <div>
            <div class="preview-name">{{ previewRow.name }}</div>
            <div class="preview-title">{{ previewRow.title || '未填写职位' }} · 编号 {{ previewRow.staffId }}</div>
          </div>
        </div>
        <el-descriptions :column="1" border>
          <!-- 副号与主号同格展示（跟列表列、小程序名片卡一致：不再单列「手机号 2」） -->
          <el-descriptions-item label="手机">
            <div>{{ previewRow.phone }}</div>
            <div v-if="previewRow.phone2" class="sub-phone">{{ previewRow.phone2 }}</div>
          </el-descriptions-item>
          <el-descriptions-item label="座机">{{ previewRow.landline || '—' }}</el-descriptions-item>
          <el-descriptions-item label="邮箱">{{ previewRow.email || '—' }}</el-descriptions-item>
          <el-descriptions-item label="微信昵称">
            <span :class="{ muted: !previewRow.nickname }">{{ previewRow.nickname || '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="openid">
            <span class="openid-cell" :class="{ muted: !previewRow.openid }">{{ previewRow.openid || '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ formatDateTime(previewRow.createdAt) }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="previewRow.status === 'enabled' ? 'success' : 'info'" effect="light">
              {{ previewRow.status === 'enabled' ? '正常' : '已停用' }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>
        <div class="preview-qr">
          <ImageThumb v-if="previewQr" :src="previewQr" :width="180" :height="180" fit="contain" />
          <span v-else class="muted">小程序码暂不可用（需配置微信凭据）</span>
        </div>
        <p class="tip">扫码，或在微信小程序首页输入编号 {{ previewRow.staffId }}，可查看访客视角的实际效果。</p>
      </div>
      <template #footer>
        <el-button type="primary" @click="previewVisible = false">知道了</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码结果 -->
    <el-dialog v-model="pwdVisible" title="重置密码成功" width="420px" class="resp-dialog">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="员工编号">{{ pwdResult.staffId }}</el-descriptions-item>
        <el-descriptions-item label="新初始密码">
          <span class="pwd">{{ pwdResult.initialPassword }}</span>
        </el-descriptions-item>
      </el-descriptions>
      <p class="tip">初始密码仅展示一次，请通知员工登录后尽快修改。</p>
      <template #footer>
        <el-button type="primary" @click="pwdVisible = false">知道了</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Refresh, Plus, Search, Download } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listEmployees,
  createEmployee,
  updateEmployee,
  disableEmployee,
  enableEmployee,
  resetPassword,
  exportEmployee,
  exportEmployees,
} from '@/api/employees'
import { getWxacode } from '@/api/wechat'
import UploadField from '@/components/UploadField.vue'
import ImageThumb from '@/components/ImageThumb.vue'
// 服务端分页：员工数会随运营增长，关键字/页码都交给后端（详见 composables/useServerTable）
import { useServerTable } from '@/composables/useServerTable'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { formatDateTime } from '@/utils/datetime'

const {
  rows,
  keyword,
  currentPage,
  size,
  total,
  loading,
  load,
} = useServerTable((q) => listEmployees(q), { pageSize: 10 })
const actingId = ref<number | null>(null)
const exporting = ref(false)
// 手机端（<768）隐藏次要列，避免表格过宽；平板/桌面保留全部列并横向滚动
const { isPhone } = useBreakpoint()

const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
// 备用手机号输入框是否展开：默认收起，由「手机」行右侧「+ 备用手机号」按钮切换；
// 编辑回填到已有 phone2 时自动展开（见 openEdit），避免「存了值却看不到输入框」
const showPhone2 = ref(false)
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({
  name: '',
  phone: '',
  phone2: '',
  title: '',
  landline: '',
  email: '',
  avatar: '',
})
const rules: FormRules = {
  name: [{ required: true, message: '请输入姓名', trigger: 'blur' }],
  phone: [{ required: true, message: '请输入手机', trigger: 'blur' }],
}

const pwdVisible = ref(false)
const pwdResult = ref({ staffId: 0, initialPassword: '' })

// 名片预览：先开弹窗展示资料（不等二维码），再异步拉小程序码
const previewVisible = ref(false)
const previewRow = ref<any>(null)
const previewQr = ref('')

async function preview(row: any) {
  previewRow.value = row
  previewQr.value = ''
  previewVisible.value = true
  previewQr.value = await getWxacode(row.staffId)
}

function openCreate() {
  resetForm()
  dialogVisible.value = true
}

function openEdit(row: any) {
  editingId.value = row.staffId
  form.name = row.name
  form.phone = row.phone
  form.phone2 = row.phone2 || ''
  showPhone2.value = !!row.phone2
  form.title = row.title
  form.landline = row.landline || ''
  form.email = row.email
  form.avatar = row.avatar
  formRef.value?.clearValidate()
  dialogVisible.value = true
}

// 「移除」备用手机号：收起输入框并清空。
// 必须清空——否则用户会以为「没显示就是没提交」，留下暗数据被一起保存
function removePhone2() {
  showPhone2.value = false
  form.phone2 = ''
}

function resetForm() {
  form.name = ''
  form.phone = ''
  form.phone2 = ''
  showPhone2.value = false
  form.title = ''
  form.landline = ''
  form.email = ''
  form.avatar = ''
  editingId.value = null
  formRef.value?.clearValidate()
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  saving.value = true
  try {
    if (editingId.value) {
      // 超管编辑即时生效，不经过员工端审核流
      await updateEmployee(editingId.value, { ...form })
      ElMessage.success('已更新')
      dialogVisible.value = false
      await load()
    } else {
      const r = await createEmployee({ ...form })
      pwdResult.value = { staffId: r.staffId, initialPassword: r.initialPassword }
      pwdVisible.value = true
      dialogVisible.value = false
      await load()
    }
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function disable(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `确认停用「${row.name}」？停用后其小程序名片将整页提示已停用。`,
    '提示',
    { type: 'warning' }
  )
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  actingId.value = row.staffId
  try {
    await disableEmployee(row.staffId)
    ElMessage.success('已停用')
    await load()
  } catch {
    /* 拦截器已提示 */
  } finally {
    actingId.value = null
  }
}

async function enable(row: any) {
  actingId.value = row.staffId
  try {
    await enableEmployee(row.staffId)
    ElMessage.success('已启用')
    await load()
  } catch {
    /* 拦截器已提示 */
  } finally {
    actingId.value = null
  }
}

async function reset(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `确认重置「${row.name}」的登录密码？重置后该员工旧密码立即失效，需使用新初始密码重新登录。`,
    '提示',
    { type: 'warning' }
  )
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  actingId.value = row.staffId
  try {
    const pwd = await resetPassword(row.staffId)
    pwdResult.value = { staffId: row.staffId, initialPassword: pwd }
    pwdVisible.value = true
  } catch {
    /* 拦截器已提示 */
  } finally {
    actingId.value = null
  }
}

// 导出某员工个人信息（PIPL 查阅复制权协助义务）：拉取该员工完整 PII 的 JSON 文件并下载。
async function exportInfo(row: any) {
  actingId.value = row.staffId
  try {
    await exportEmployee(row.staffId)
  } catch {
    /* 拦截器已提示 */
  } finally {
    actingId.value = null
  }
}

// 批量导出本租户员工（CSV）：走独立 fetch 通道（后端回 text/csv，非 JSON 信封）。
// 与列表页同口径——带上当前关键字，导出「当前看到的筛选结果」。
async function exportAll() {
  exporting.value = true
  try {
    await exportEmployees({ keyword: keyword.value })
    ElMessage.success('已开始下载员工数据')
  } catch (e: any) {
    ElMessage.error(e?.message || '导出失败')
  } finally {
    exporting.value = false
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
.openid-cell {
  font-family: monospace;
  font-size: 12px;
}
/* 姓名/职位等短文本列：禁止换行，超出省略并悬浮显示全文（避免被压宽后折行） */
.cell-nowrap {
  white-space: nowrap;
}
.landline-cell {
  white-space: nowrap;
}
/* 副号：主号下方的次要小字，视觉上从属于「手机」列 */
.sub-phone {
  color: #a8abb2;
  font-size: 12px;
  line-height: 1.4;
}
/* 带说明文案的输入项：说明紧跟输入框，避免用户不知道第二个号是干嘛的 */
.field-tip {
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
  margin: 4px 0 0;
}
/* 手机号行：输入框 + 右侧「+ 备用手机号」/「移除」按钮同一行 */
.phone-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.phone-row .el-input {
  flex: 1;
  min-width: 0;
}
.add-phone-btn {
  flex-shrink: 0;
  color: #1a8cff;
  border-color: #1a8cff;
}
.add-plus {
  font-size: 15px;
  line-height: 1;
  margin-right: 2px;
}
.del-phone-btn {
  flex-shrink: 0;
  color: #909399;
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

/* 名片预览弹窗 */
.preview-card .preview-head {
  display: flex;
  align-items: center;
  margin-bottom: 16px;
}
.preview-avatar {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  margin-right: 14px;
  flex-shrink: 0;
}
.preview-avatar.ph {
  background: #f0f0f0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #c0c4cc;
}
.preview-name {
  font-size: 16px;
  font-weight: 600;
}
.preview-title {
  color: #909399;
  font-size: 13px;
  margin-top: 4px;
}
.preview-qr {
  margin-top: 16px;
  text-align: center;
}
.qr-img {
  width: 180px;
  height: 180px;
}

/* ===== 手机端员工卡片列表 ===== */
.emp-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.emp-card {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 12px 14px;
  background: #fff;
}
.emp-card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.emp-avatar {
  width: 44px;
  height: 44px;
  border-radius: 6px;
  flex-shrink: 0;
}
.emp-avatar.ph {
  background: #f0f0f0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #c0c4cc;
}
.emp-card-id {
  flex: 1;
  min-width: 0;
}
.emp-name {
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.emp-staff {
  font-size: 12px;
  font-weight: 400;
  color: #909399;
}
.emp-title {
  color: #909399;
  font-size: 13px;
  margin-top: 2px;
}
.emp-card-row {
  display: flex;
  font-size: 13px;
  line-height: 1.8;
}
.emp-k {
  width: 48px;
  flex-shrink: 0;
  color: #909399;
}
.emp-v {
  color: #303133;
  word-break: break-all;
}
.emp-card-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid #f2f4f7;
}
</style>
