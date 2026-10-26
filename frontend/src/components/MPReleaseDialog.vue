<template>
  <el-dialog
    v-model="visible"
    :title="`小程序版本 - ${company?.name || ''}`"
    width="900px"
    class="rel-dialog"
    @closed="reset"
  >
    <!-- 链路说明：把「为什么有这些按钮、点了会发生什么」讲在明处。
         提审扣额度 / 撤回不返还 / 发布改变线上行为，三条都是不可逆代价。 -->
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="版本发布链路：上传代码 → 提交审核 → 查审核状态 → 发布"
      description="提审消耗微信审核额度、撤回不返还；发布直接改变该企业小程序的线上行为，请谨慎操作。上传有两条路：「上传代码」由后台直接传到微信（不需要装微信开发者工具），或让 CI 传完后用「登记版本」记一笔。"
      style="margin-bottom: 12px"
    />

    <div class="rel-bar">
      <el-button type="primary" :icon="Upload" @click="openUpload">上传代码</el-button>
      <el-button :icon="Plus" @click="regVisible = true">登记版本</el-button>
      <el-button :icon="Refresh" :loading="loading" @click="reload">刷新</el-button>
      <span v-if="company" class="rel-tip">
        {{ company.wxConfigured ? '该企业使用独立小程序' : '未配独立凭据，代发走平台全局小程序' }}
      </span>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe max-height="420">
      <el-table-column prop="userVersion" label="版本号" width="110" align="center" />
      <el-table-column label="状态" width="110" align="center">
        <template #default="{ row }">
          <el-tag :type="mpReleaseTagType(row.status)" effect="light" size="small">
            {{ row.statusName }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="userDesc" label="说明" min-width="150" show-overflow-tooltip />
      <el-table-column label="审核单号" width="100" align="center">
        <template #default="{ row }">{{ row.auditId || '—' }}</template>
      </el-table-column>
      <!-- 驳回原因单独一列且危险色：被驳回的版本要能一眼看到为什么，
           不然运营只会反复点「重新提审」然后再次被同一个理由驳回 -->
      <el-table-column label="驳回原因" min-width="180">
        <template #default="{ row }">
          <span v-if="row.auditReason" class="rel-reason">{{ row.auditReason }}</span>
          <span v-else class="rel-muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="提交时间" width="160" show-overflow-tooltip>
        <template #default="{ row }">{{ row.submittedAt ? formatDateTime(row.submittedAt) : '—' }}</template>
      </el-table-column>
      <el-table-column label="上线时间" width="160" show-overflow-tooltip>
        <template #default="{ row }">{{ row.releasedAt ? formatDateTime(row.releasedAt) : '—' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right" align="center">
        <template #default="{ row }">
          <!-- 按钮显隐完全跟着状态机走（后端白名单是最终裁判，这里只是少让人点错） -->
          <template v-if="row.status === 'draft' || row.status === 'rejected' || row.status === 'withdrawn'">
            <el-button
              type="primary"
              link
              :loading="acting === `${row.id}:submit`"
              @click="submitAudit(row)"
              >{{ row.status === 'draft' ? '提交审核' : '重新提审' }}</el-button
            >
          </template>
          <template v-if="row.status === 'auditing'">
            <el-button type="primary" link :loading="acting === `${row.id}:status`" @click="checkStatus(row)"
              >查状态</el-button
            >
            <el-button
              type="danger"
              link
              :loading="acting === `${row.id}:undo`"
              @click="undoAudit(row)"
              >撤回</el-button
            >
          </template>
          <template v-if="row.status === 'audit_passed'">
            <el-button type="primary" link :loading="acting === `${row.id}:status`" @click="checkStatus(row)"
              >查状态</el-button
            >
            <el-button
              type="success"
              link
              :loading="acting === `${row.id}:release`"
              @click="release(row)"
              >发布</el-button
            >
          </template>
          <span v-if="row.status === 'released'" class="rel-muted">已上线</span>
        </template>
      </el-table-column>
      <template #empty><span class="rel-muted">还没有版本记录：点「上传代码」由后台传到微信，或用「登记版本」补记 CI 已传的版本</span></template>
    </el-table>

    <el-pagination
      class="rel-pager"
      v-model:current-page="page"
      v-model:page-size="size"
      :total="total"
      :page-sizes="[10, 20, 50]"
      layout="total, sizes, prev, pager, next"
      background
    />

    <!-- 登记版本：版本号即 CI 上传时填的那个号，后台无法验证，写错会在提审时暴露 -->
    <el-dialog v-model="regVisible" title="登记版本" width="480px" append-to-body>
      <el-form ref="regFormRef" :model="regForm" :rules="regRules" label-width="90px">
        <el-form-item label="版本号" prop="userVersion">
          <el-input v-model="regForm.userVersion" placeholder="如 1.2.0，须与 CI 上传时一致" clearable />
        </el-form-item>
        <el-form-item label="版本说明">
          <el-input
            v-model="regForm.userDesc"
            type="textarea"
            :rows="3"
            maxlength="255"
            placeholder="这版改了什么，如：补齐节日海报背景图"
            clearable
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="regVisible = false">取消</el-button>
        <el-button type="primary" :loading="registering" @click="submitRegister">登记</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="uploadVisible" title="上传小程序代码" width="600px" append-to-body>
      <el-alert
        v-if="uploadProblems.length > 0"
        type="error"
        :closable="false"
        show-icon
        title="上传环境未就绪，请先按下列项处理"
      >
        <ul class="problem-list">
          <li v-for="p in uploadProblems" :key="p">{{ p }}</li>
        </ul>
      </el-alert>
      <el-alert
        v-else
        type="success"
        :closable="false"
        show-icon
        title="上传环境已就绪：Node、miniprogram-ci、源码目录、上传密钥均已配置"
      />
      <el-form ref="uploadFormRef" :model="uploadForm" :rules="uploadRules" label-width="90px" class="upload-form">
        <el-form-item label="版本号" prop="userVersion">
          <el-input v-model="uploadForm.userVersion" placeholder="如 1.2.0，需与小程序源码中的版本一致" />
        </el-form-item>
        <el-form-item label="项目备注" prop="userDesc">
          <el-input v-model="uploadForm.userDesc" type="textarea" :rows="2" maxlength="200" show-word-limit placeholder="将展示在微信公众平台的审核备注中" />
        </el-form-item>
      </el-form>
      <el-alert type="warning" :closable="false" show-icon title="上传是同步执行的，通常需要数十秒到数分钟，请勿关闭或刷新页面。" />
      <template #footer>
        <el-button @click="uploadVisible = false">取消</el-button>
        <el-button type="primary" :loading="uploading" :disabled="uploadProblems.length > 0" @click="submitUpload">
          开始上传
        </el-button>
      </template>
    </el-dialog>

    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch, type Ref } from 'vue'
import { Plus, Refresh, Upload } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import {
  createMPRelease,
  getMPReleaseAuditStatus,
  getMPReleaseUploadReadiness,
  listMPReleases,
  mpReleaseTagType,
  releaseMPRelease,
  submitMPReleaseAudit,
  undoMPReleaseAudit,
  uploadMPRelease,
  type MPReleaseItem,
} from '@/api/mpRelease'
import { formatDateTime } from '@/utils/datetime'

const props = defineProps<{
  company: { id: number; name: string; wxConfigured?: boolean } | null
}>()
const visible = defineModel<boolean>({ default: false })

// 表单校验包一层：el-form 的 ref 在「弹窗还没渲染出来」时取不到 validate
// （桩环境同样如此），双层可选调用兜底，省得为了跑测试把组件写成两套逻辑
async function validate(formRef: Ref<FormInstance | undefined>): Promise<boolean> {
  try {
    return (await formRef.value?.validate?.()) !== false
  } catch {
    return false
  }
}

// ===== 列表 =====
const rows = ref<MPReleaseItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(10)
const loading = ref(false)
// acting = `${记录id}:${动作}`：同一时刻只让一个按钮转圈，且能精确到是哪一行的哪个动作
const acting = ref('')

async function reload() {
  if (!props.company) return
  loading.value = true
  try {
    const r = await listMPReleases(props.company.id, { page: page.value, size: size.value })
    rows.value = r.list
    total.value = r.total
  } catch {
    // 拦截器已提示；保留旧数据，避免一次网络抖动把表格清空让人以为记录没了
  } finally {
    loading.value = false
  }
}

// 打开即加载：版本记录是「点进来就要看最新状态」的场景，不该再让人点一次刷新。
// immediate 是给「挂载时就已是打开态」兜底（测试直接以 modelValue: true 挂载、
// 或父组件把弹窗常驻时），关闭态下不会触发，与正常点开路径行为一致。
watch(
  visible,
  (v) => {
    if (v) {
      page.value = 1
      reload()
    }
  },
  { immediate: true }
)

watch([page, size], reload)

function reset() {
  rows.value = []
  total.value = 0
  acting.value = ''
}

// ===== 登记版本 =====
const regVisible = ref(false)
const registering = ref(false)
const regFormRef = ref<FormInstance>()
const regForm = reactive({ userVersion: '', userDesc: '' })

// 与后端 releaseVersionRe 同口径（后端才是裁判，这里只为少一次无效请求）
const regRules: FormRules = {
  userVersion: [
    { required: true, message: '请输入版本号', trigger: 'blur' },
    {
      pattern: /^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$/,
      message: '1-64 位字母/数字/点/横线/下划线，如 1.0.0',
      trigger: 'blur',
    },
  ],
}

async function submitRegister() {
  if (!props.company) return
  const ok = await validate(regFormRef)
  if (!ok) return
  registering.value = true
  try {
    await createMPRelease(props.company.id, {
      userVersion: regForm.userVersion.trim(),
      userDesc: regForm.userDesc.trim(),
    })
    ElMessage.success('已登记，等待提交审核')
    regVisible.value = false
    regForm.userVersion = ''
    regForm.userDesc = ''
    page.value = 1
    await reload()
  } catch {
    // 拦截器已提示（重复版本号 / 未配凭据等）
  } finally {
    registering.value = false
  }
}

// ===== 上传代码 =====
// 与登记版本的区别：登记只是「记一笔」，版本号错了要到提审才暴露；
// 上传是后台真的把源码传到微信，成功后由后端按同一个版本号建 draft 记录，
// 从根上杜绝「填的号」和「传的号」不是同一个。
const uploadVisible = ref(false)
const uploading = ref(false)
const uploadProblems = ref<string[]>([])
const uploadFormRef = ref<FormInstance>()
const uploadForm = reactive({ userVersion: '', userDesc: '' })

// 同 regRules：与后端 uploadVersionRe 对齐
const uploadRules: FormRules = {
  userVersion: [
    { required: true, message: '请输入版本号', trigger: 'blur' },
    {
      pattern: /^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$/,
      message: '1-64 位字母/数字/点/横线/下划线，如 1.0.0',
      trigger: 'blur',
    },
  ],
}

// 打开弹窗先探活：缺 node / 没装 miniprogram-ci / 源码目录不在 / 没配上传密钥，
// 这四件事任缺一件上传必然失败，早说清楚比让人白等几分钟再报错好。
async function openUpload() {
  uploadVisible.value = true
  uploadProblems.value = []
  if (!props.company) return
  try {
    const r = await getMPReleaseUploadReadiness(props.company.id)
    uploadProblems.value = r.problems || []
  } catch {
    // 拦截器已提示；problems 留空，提交时后端会再拦一次
  }
}

async function submitUpload() {
  if (!props.company) return
  const ok = await validate(uploadFormRef)
  if (!ok) return
  if (uploadProblems.value.length > 0) {
    ElMessage.warning('上传环境未就绪，请先处理上面列出的问题')
    return
  }
  uploading.value = true
  try {
    await uploadMPRelease(props.company.id, {
      userVersion: uploadForm.userVersion.trim(),
      userDesc: uploadForm.userDesc.trim(),
    })
    ElMessage.success('代码已上传到微信，并生成待审核记录')
    uploadVisible.value = false
    uploadForm.userVersion = ''
    uploadForm.userDesc = ''
    page.value = 1
    await reload()
  } catch {
    // 拦截器已提示（重复版本号 / 环境未就绪 / 上传失败等）
  } finally {
    uploading.value = false
  }
}

// ===== 提审 / 查状态 / 发布 / 撤回 =====
// 四个动作的骨架一致：置 loading → 调接口 → 用回包的最新记录替换行 → 刷新列表。
// 统一在 catch 里保持原状（后端失败时状态不变，界面也不该乱）。

async function submitAudit(row: any) {
  const resubmit = row.status !== 'draft'
  if (resubmit) {
    // 重新提审会**再扣一次**审核额度，必须说清楚
    try {
      await ElMessageBox.confirm(
        `重新提审版本 ${row.userVersion} 会再次消耗一次审核额度，确定继续？`,
        '确认重新提审',
        { type: 'warning' }
      )
    } catch {
      return
    }
  }
  acting.value = `${row.id}:submit`
  try {
    await submitMPReleaseAudit(row.id)
    ElMessage.success('已提交审核，微信审核时长小时到天级，不可催')
    await reload()
  } catch {
    // 拦截器已提示
  } finally {
    acting.value = ''
  }
}

async function checkStatus(row: any) {
  acting.value = `${row.id}:status`
  try {
    const r = await getMPReleaseAuditStatus(row.id)
    if (r.changed) {
      ElMessage.success(`状态已更新：${r.item.statusName}`)
    } else if (r.wxStatus === 2) {
      ElMessage.info('微信仍在审核中，稍后再查')
    } else {
      ElMessage.info(`当前状态：${r.item.statusName}`)
    }
    await reload()
  } catch {
    // 拦截器已提示
  } finally {
    acting.value = ''
  }
}

async function release(row: any) {
  try {
    await ElMessageBox.confirm(
      `发布后版本 ${row.userVersion} 将立即对该企业小程序的全部用户生效，确定发布？`,
      '确认发布',
      { type: 'warning', confirmButtonText: '确定发布' }
    )
  } catch {
    return
  }
  acting.value = `${row.id}:release`
  try {
    await releaseMPRelease(row.id)
    ElMessage.success('已发布上线')
    await reload()
  } catch {
    // 拦截器已提示
  } finally {
    acting.value = ''
  }
}

async function undoAudit(row: any) {
  try {
    await ElMessageBox.confirm(
      `撤回审核不返还已消耗的审核额度，且撤回后需重新提审（再扣一次）。确定撤回版本 ${row.userVersion}？`,
      '确认撤回审核',
      { type: 'warning', confirmButtonText: '仍要撤回' }
    )
  } catch {
    return
  }
  acting.value = `${row.id}:undo`
  try {
    await undoMPReleaseAudit(row.id)
    ElMessage.success('已撤回审核')
    await reload()
  } catch {
    // 拦截器已提示
  } finally {
    acting.value = ''
  }
}
</script>

<style scoped>
.rel-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.rel-tip {
  font-size: 12px;
  color: #909399;
}
.rel-reason {
  color: #f56c6c;
  font-size: 12px;
}
.rel-muted {
  color: #909399;
  font-size: 12px;
}
.rel-pager {
  margin-top: 12px;
  justify-content: flex-end;
}
.problem-list {
  margin: 0;
  padding-left: 18px;
  line-height: 1.8;
}
.upload-form {
  margin-top: 12px;
}
</style>
