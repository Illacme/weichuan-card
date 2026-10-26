<template>
  <div>
    <el-card>
      <div class="page-bar">
        <h3>企业管理</h3>
        <div class="bar-right">
          <span class="bar-tip">平台超级管理员：开通/停用企业、管理企业管理员</span>
          <el-input
            v-model="keyword"
            placeholder="搜索企业名称/简称"
            clearable
            :prefix-icon="Search"
            style="width: 200px"
          />
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="primary" :icon="Plus" :disabled="locked" @click="openCreate"
            >开通企业</el-button
          >
        </div>
      </div>

      <!-- 独立版企业锁定：整体只读，并说明在哪里能解除 -->
      <el-alert
        v-if="locked"
        type="warning"
        :closable="false"
        show-icon
        title="已开启独立版企业锁定，当前为只读"
        description="不能新建/编辑企业、启停企业或添加企业管理员。如需修改，请到「平台管理 → 系统设置 → 功能开关」关闭 independent_lock。"
        style="margin-bottom: 14px"
      />

      <el-table :data="rows" v-loading="loading" border stripe>
        <el-table-column prop="id" label="编号" width="70" align="center" v-if="!isPhone" />
        <el-table-column prop="name" label="企业名称" min-width="220" show-overflow-tooltip />
        <el-table-column label="简称" min-width="130" show-overflow-tooltip>
          <template #default="{ row }">{{ row.shortName || '微传名片' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'enabled' ? 'success' : 'info'" effect="light">
              {{ row.status === 'enabled' ? '运营中' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="employeeCount" label="员工数" width="90" align="center" v-if="!isPhone" />
        <el-table-column prop="adminCount" label="管理员数" width="100" align="center" v-if="!isPhone" />
        <el-table-column label="小程序" width="110" align="center">
          <template #default="{ row }">
            <el-tooltip :content="wxTip(row)" placement="top">
              <el-tag :type="row.wxConfigured ? 'success' : 'warning'" effect="light" size="small">
                {{ row.wxConfigured ? '已配置' : '未配置' }}
              </el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="授权" width="150" align="center">
          <template #default="{ row }">
            <el-tooltip :content="licTip(row)" placement="top">
              <el-tag :type="licTagType(row)" effect="light" size="small">{{ licText(row) }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="存储" width="100" align="center" v-if="!isPhone">
          <template #default="{ row }">{{ fmtBytes(row.storageBytes) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="180" show-overflow-tooltip v-if="!isPhone">
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="460" fixed="right" align="center">
          <template #default="{ row }">
            <el-button type="primary" link @click="openAdmins(row)">管理员</el-button>
            <el-button type="primary" link @click="openWX(row)">凭据</el-button>
            <el-button type="primary" link @click="openReleases(row)">版本</el-button>
            <el-button type="primary" link @click="openLicense(row)">授权</el-button>
            <el-button type="primary" link :disabled="locked" @click="openEdit(row)">编辑</el-button>
            <el-button
              v-if="row.status === 'enabled'"
              type="warning"
              link
              :disabled="locked"
              :loading="actingId === row.id"
              @click="toggleStatus(row)"
              >停用</el-button
            >
            <el-button
              v-else
              type="success"
              link
              :disabled="locked"
              :loading="actingId === row.id"
              @click="toggleStatus(row)"
              >启用</el-button
            >
          </template>
        </el-table-column>
      </el-table>

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

    <!-- 开通企业 -->
    <el-dialog v-model="createVisible" title="开通企业" width="480px" @closed="resetCreate">
      <el-form ref="createFormRef" :model="createForm" :rules="createRules" label-width="110px">
        <el-form-item label="企业名称" prop="name">
          <el-input v-model="createForm.name" placeholder="如：廊坊市XX科技有限公司" clearable />
        </el-form-item>
        <el-form-item label="企业简称">
          <el-input
            v-model="createForm.shortName"
            placeholder="侧边栏「简称 · 管理后台」品牌名，留空默认「微传名片」"
            clearable
          />
        </el-form-item>
        <el-divider content-position="left">
          <span class="divider-hint">企业管理员（选填，留空稍后添加）</span>
        </el-divider>
        <el-form-item label="管理员用户名" prop="adminUsername">
          <el-input v-model="createForm.adminUsername" placeholder="登录账号" clearable />
        </el-form-item>
        <el-form-item label="管理员密码" prop="adminPassword">
          <el-input v-model="createForm.adminPassword" placeholder="登录密码" clearable />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">开通</el-button>
      </template>
    </el-dialog>

    <!-- 编辑企业（名称 / 简称） -->
    <el-dialog v-model="editVisible" title="编辑企业" width="480px" @closed="resetEdit">
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="110px">
        <el-form-item label="企业名称" prop="name">
          <el-input v-model="editForm.name" placeholder="如：廊坊市XX科技有限公司" clearable />
        </el-form-item>
        <el-form-item label="企业简称">
          <el-input
            v-model="editForm.shortName"
            placeholder="侧边栏「简称 · 管理后台」品牌名，留空默认「微传名片」"
            clearable
          />
        </el-form-item>
        <div class="edit-tip">
          简称修改后同步后台侧边栏品牌名（留空默认「微传名片」）；公司信息里的简称（仅小程序展示）不受影响，由企业管理员在「公司信息」页维护。
        </div>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editing" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 企业管理员管理 -->
    <el-dialog v-model="adminsVisible" :title="`管理员管理 - ${currentCompany?.name || ''}`" width="560px">
      <div class="admin-add-bar">
        <el-input v-model="newAdmin.username" placeholder="用户名" style="width: 180px" clearable />
        <el-input v-model="newAdmin.password" placeholder="密码" style="width: 180px" clearable />
        <el-button
          type="primary"
          :icon="Plus"
          :disabled="locked"
          :loading="addingAdmin"
          @click="submitAddAdmin"
          >添加</el-button
        >
      </div>
      <el-table :data="admins" v-loading="adminsLoading" border stripe max-height="360">
        <el-table-column prop="id" label="编号" width="70" align="center" />
        <el-table-column prop="username" label="用户名" min-width="160" show-overflow-tooltip />
        <el-table-column label="创建时间" width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- 租户小程序凭据（企业独立版小程序：一个企业一个 appid） -->
    <el-dialog
      v-model="wxVisible"
      :title="`小程序凭据 - ${currentCompany?.name || ''}`"
      width="520px"
      @closed="resetWX"
    >
      <el-alert
        v-if="wxForm.configured"
        type="success"
        :closable="false"
        show-icon
        title="该企业已配置独立小程序"
        description="企业员工登录、生成海报小程序码，用的都是这份凭据。"
        style="margin-bottom: 14px"
      />
      <el-alert
        v-else-if="wxForm.appId"
        type="warning"
        :closable="false"
        show-icon
        title="只填了 AppID，AppSecret 为空，当前仍回落到平台全局小程序"
        description="半份凭据不能用。请在下面补填 AppSecret 后保存；若只想清掉这家企业的配置，把 AppID 也删掉再保存。"
        style="margin-bottom: 14px"
      />
      <el-alert
        v-else
        type="warning"
        :closable="false"
        show-icon
        title="未配置，当前回落到平台全局小程序"
        description="不配置也能用，但企业员工的小程序码会指向平台小程序，扫码进的是平台侧页面。企业有自己小程序时请填写下面两项。"
        style="margin-bottom: 14px"
      />
      <el-form ref="wxFormRef" v-loading="wxLoading" :model="wxForm" :rules="wxRules" label-width="96px">
        <el-form-item label="AppID" prop="appId">
          <el-input v-model="wxForm.appId" placeholder="该企业的小程序 AppID，以 wx 开头" clearable />
        </el-form-item>
        <el-form-item label="AppSecret" prop="appSecret">
          <el-input
            v-model="wxForm.appSecret"
            type="password"
            show-password
            :placeholder="wxSecretPlaceholder"
            clearable
          />
        </el-form-item>
        <!-- 上传密钥与 AppSecret 是两把不同的钥匙：AppSecret 换 token 调提审/发布接口，
             上传密钥（private.key）才能把代码传到微信。没配也不影响用「登记版本」走 CI 上传，
             只是后台的「上传代码」按钮会不可用。 -->
        <el-form-item label="上传密钥">
          <el-input
            v-model="wxForm.uploadKey"
            type="password"
            show-password
            :placeholder="wxUploadKeyPlaceholder"
            clearable
          />
        </el-form-item>
        <el-form-item label="平台代发">
          <el-switch v-model="wxForm.releaseEnabled" />
          <span class="wx-switch-tip">
            关闭后，平台不能再代这家企业提交审核 / 发布 / 撤回小程序版本（企业要自己登微信公众平台操作）。
            留空保存 AppSecret 会清空凭据，届时本开关随凭据一起重置。
          </span>
        </el-form-item>
        <div class="wx-tip">
          <p><b>AppSecret 不能单独保留</b>：保存时若此处为空，会连同清空该企业已存的 Secret。</p>
          <p>只想换 AppID 时，请把 AppSecret 一起重填一遍；确认不再用企业独立小程序，留空保存即可回落平台小程序。</p>
          <p>凭据从小程序公众平台 → 开发管理 → 开发设置里获取，需与后台配置的「小程序业务域名」一致。</p>
          <p><b>上传密钥同样「留空即清空」</b>：它只给后台的「上传代码」用，清空后将改用平台全局配置的那把（系统设置 → 小程序代码上传密钥）。</p>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="wxVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingWX" @click="submitWX">保存</el-button>
      </template>
    </el-dialog>

    <!-- 小程序版本发布（代租户上传代码 / 提审 / 发布 / 撤回） -->
    <MPReleaseDialog v-model="releasesVisible" :company="releasesCompany" />

    <!-- 授权与存储配额：试用转正 / 延期 / 改席位 / 单独放大存储上限 -->
    <el-dialog
      v-model="licVisible"
      :title="`授权与配额 - ${licCompany?.name || ''}`"
      width="560px"
      @closed="resetLicense"
    >
      <div v-loading="licLoading">
        <el-alert
          v-if="licInfo && !licInfo.exists"
          type="info"
          :closable="false"
          show-icon
          title="该企业尚未配置授权（不受席位与到期限制）"
          description="存量企业升级后都是这个状态。新开通的企业会自动拿到试用授权；要约束它，请在下面显式填写后保存。"
          style="margin-bottom: 14px"
        />
        <el-alert
          v-else-if="licInfo && licInfo.expired"
          type="error"
          :closable="false"
          show-icon
          title="授权已过期：该企业后台已降级为只读"
          description="员工与管理员仍能登录查看，但所有写操作会被拒绝。续费后把到期日改到未来再保存即可恢复。"
          style="margin-bottom: 14px"
        />
        <el-alert
          v-else-if="licInfo && licInfo.edition === 'trial'"
          type="warning"
          :closable="false"
          show-icon
          :title="`试用授权（${licInfo.trial.days} 天 · ${licInfo.trial.seats} 席）`"
          :description="`当前已建 ${licInfo.employeeCount} 个员工；到期日 ${licInfo.expiresAt || '未设置'}。到期后自动降级只读，续费后把到期日改到未来保存即可恢复。`"
          style="margin-bottom: 14px"
        />

        <el-form label-width="110px">
          <el-form-item label="版本标识">
            <el-select v-model="licForm.edition" style="width: 100%">
              <el-option label="试用" value="trial" />
              <el-option label="正式（付费）" value="platform" />
              <el-option label="独立部署" value="private" />
              <el-option label="未设置（不影响强制）" value="" />
            </el-select>
          </el-form-item>
          <el-form-item label="员工席位">
            <el-input-number v-model="licForm.seats" :min="0" :max="100000" :step="1" />
            <span class="lic-hint">0 = 不限制（含审批开通的申请）</span>
          </el-form-item>
          <el-form-item label="到期日">
            <el-date-picker
              v-model="licForm.expiresAt"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="留空 = 不过期"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="功能开关">
            <el-input v-model="licForm.features" placeholder="逗号分隔（如 mp_release）；留空 = 全部开通" />
          </el-form-item>
          <el-divider content-position="left">
            <span class="lic-hint">存储配额</span>
          </el-divider>
          <el-form-item label="存储上限">
            <el-input-number v-model="licForm.quotaMb" :min="0" :max="1048576" :step="100" />
            <span class="lic-hint">
              MB；0 = 跟随全局（{{ storage.quotaGlobal > 0 ? `${storage.quotaGlobal} MB` : '当前不限' }}）。
              本企业已用 {{ fmtBytes(storage.bytes) }}，统计自本功能启用起累计，不含历史文件。
            </span>
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="licVisible = false">取消</el-button>
        <el-button type="primary" :loading="licSaving" @click="submitLicense">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
const { isPhone } = useBreakpoint()
import { computed, onMounted, reactive, ref } from 'vue'
import { Plus, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import {
  createCompany,
  createCompanyAdmin,
  getCompanyLicense,
  getCompanyWXApp,
  getStorageUsage,
  listCompanies,
  listCompanyAdmins,
  saveCompanyLicense,
  saveCompanyWXApp,
  saveStorageQuota,
  setCompanyStatus,
  updateCompany,
  type CompanyItem,
  type CompanyLicense,
} from '@/api/tenants'
import MPReleaseDialog from '@/components/MPReleaseDialog.vue'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/store/auth'
import { useBreakpoint } from '@/composables/useBreakpoint'
// 服务端分页：租户数会随业务增长，关键字（名称/简称）也交给后端匹配
import { useServerTable } from '@/composables/useServerTable'

const auth = useAuthStore()
// 独立版企业锁定（后台「系统设置 → independent_lock」）：企业管理页整体只读。
// 写按钮已 disabled，这里再加一道入口守卫——防止快捷键 / 残留弹窗绕过界面禁用。
// 后端 writeLocked 才是最终防线，前端只是避免「点了才报错」。
const locked = computed(() => auth.independentLock)

const { rows, keyword, currentPage, size, total, loading, load } = useServerTable((q) =>
  listCompanies(q)
)
const actingId = ref<number | null>(null)

onMounted(load)

// ===== 开通企业 =====
const createVisible = ref(false)
const creating = ref(false)
const createFormRef = ref<FormInstance>()
const createForm = reactive({ name: '', shortName: '', adminUsername: '', adminPassword: '' })

const createRules: FormRules = {
  name: [{ required: true, message: '请输入企业名称', trigger: 'blur' }],
}

function openCreate() {
  if (locked.value) return
  createVisible.value = true
}

function resetCreate() {
  createForm.name = ''
  createForm.shortName = ''
  createForm.adminUsername = ''
  createForm.adminPassword = ''
  createFormRef.value?.clearValidate()
}

async function submitCreate() {
  if (!createFormRef.value) return
  const ok = await createFormRef.value.validate().catch(() => false)
  if (!ok) return
  creating.value = true
  try {
    await createCompany({
      name: createForm.name,
      shortName: createForm.shortName || undefined,
      adminUsername: createForm.adminUsername || undefined,
      adminPassword: createForm.adminPassword || undefined,
    })
    ElMessage.success('企业已开通')
    createVisible.value = false
    await load()
  } catch {
    // 拦截器已提示（如用户名已存在）
  } finally {
    creating.value = false
  }
}

// ===== 编辑企业（名称 / 简称） =====
const editVisible = ref(false)
const editing = ref(false)
const editFormRef = ref<FormInstance>()
const editForm = reactive({ id: 0, name: '', shortName: '' })

const editRules: FormRules = {
  name: [{ required: true, message: '请输入企业名称', trigger: 'blur' }],
}

function openEdit(row: any) {
  if (locked.value) return
  editForm.id = row.id
  editForm.name = row.name
  editForm.shortName = row.shortName || ''
  editVisible.value = true
}

function resetEdit() {
  editForm.id = 0
  editForm.name = ''
  editForm.shortName = ''
  editFormRef.value?.clearValidate()
}

async function submitEdit() {
  if (!editFormRef.value) return
  const ok = await editFormRef.value.validate().catch(() => false)
  if (!ok) return
  editing.value = true
  try {
    await updateCompany(editForm.id, { name: editForm.name, shortName: editForm.shortName })
    ElMessage.success('已保存')
    editVisible.value = false
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    editing.value = false
  }
}

// ===== 停用 / 启用 =====
async function toggleStatus(row: any) {
  if (locked.value) return
  const disabling = row.status === 'enabled'
  try {
    await ElMessageBox.confirm(
      disabling
        ? `停用后该企业的管理员与员工将不能登录，确定停用「${row.name}」？`
        : `确定恢复启用「${row.name}」？`,
      '确认操作',
      { type: 'warning' }
    )
  } catch {
    return // 用户取消
  }
  actingId.value = row.id
  try {
    await setCompanyStatus(row.id, disabling ? 'disabled' : 'enabled')
    ElMessage.success(disabling ? '已停用' : '已启用')
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    actingId.value = null
  }
}

// ===== 企业管理员管理 =====
const adminsVisible = ref(false)
const adminsLoading = ref(false)
const addingAdmin = ref(false)
const currentCompany = ref<CompanyItem | null>(null)
const admins = ref<any[]>([])
const newAdmin = reactive({ username: '', password: '' })

async function openAdmins(row: any) {
  currentCompany.value = row
  adminsVisible.value = true
  newAdmin.username = ''
  newAdmin.password = ''
  await reloadAdmins()
}

async function reloadAdmins() {
  if (!currentCompany.value) return
  adminsLoading.value = true
  try {
    const resp = await listCompanyAdmins(currentCompany.value.id)
    admins.value = resp.data || []
  } catch {
    // 拦截器已提示
  } finally {
    adminsLoading.value = false
  }
}

async function submitAddAdmin() {
  if (locked.value) return
  if (!currentCompany.value) return
  if (!newAdmin.username || !newAdmin.password) {
    ElMessage.warning('请输入用户名与密码')
    return
  }
  addingAdmin.value = true
  try {
    await createCompanyAdmin(currentCompany.value.id, newAdmin.username, newAdmin.password)
    ElMessage.success('管理员已添加')
    newAdmin.username = ''
    newAdmin.password = ''
    await reloadAdmins()
    await load()
  } catch {
    // 拦截器已提示（如用户名已存在）
  } finally {
    addingAdmin.value = false
  }
}

// ===== 租户小程序凭据（平台超管代填）=====
// 与「企业管理员管理」不同，这里**不受** independentLock 禁用：那是「独立版部署」
// 的只读开关，而凭据是阻塞性配置——真锁住了反而让独立版用户永远配不上小程序。
// 后端 RegisterCompanyWXApp 的 PUT 同样不走 writeLocked，两侧保持一致。
const wxVisible = ref(false)
const savingWX = ref(false)
const wxLoading = ref(false)
const wxFormRef = ref<FormInstance>()
const wxForm = reactive({
  id: 0,
  appId: '',
  appSecret: '',
  uploadKey: '',
  uploadKeyConfigured: false,
  configured: false,
  releaseEnabled: true,
})

const wxRules: FormRules = {
  appId: [
    { required: true, message: '请输入该企业的小程序 AppID', trigger: 'blur' },
    {
      validator: (_r: any, v: string, cb: any) => {
        // 只卡明显写错的（粘错成空格 / 少字符），其余交给微信侧当裁判：
        // 这里拦太严会让「换了个新形态 appid」时后台先拒一次
        if (!v || v.startsWith('wx')) cb()
        else cb(new Error('AppID 应以 wx 开头'))
      },
      trigger: 'blur',
    },
  ],
}

// 占位符要说清「留空即清空」——这是最容易误操作的一步，见 api/tenants.ts 的说明
const wxSecretPlaceholder = computed(() => {
  if (wxForm.configured) return '已配置。重填=覆盖；留空保存=清空并回落平台小程序'
  if (wxForm.appId) return 'AppSecret 为空，请补填（半份凭据无法使用）'
  return '该企业的小程序 AppSecret'
})

// 上传密钥的占位符：与 AppSecret 同口径（留空即清空），但要说清清的是哪份影响——
// 清的是「这家企业自己那把」，之后上传改用平台全局那把
const wxUploadKeyPlaceholder = computed(() =>
  wxForm.uploadKeyConfigured ? '已配置。重填=覆盖；留空保存=清空并改用平台全局密钥' : '该企业的小程序代码上传密钥（private.key 文件全文）'
)

// 表格列 tooltip：未配置时说明后果 + 指路，已配置时说明生效范围
function wxTip(row: any) {
  return row.wxConfigured
    ? '该企业员工登录与海报小程序码，均使用此独立小程序'
    : '未配置独立小程序，当前回落到平台全局小程序；点「凭据」可代填'
}

async function openWX(row: any) {
  currentCompany.value = row
  wxForm.id = row.id
  wxForm.appId = ''
  wxForm.appSecret = ''
  wxForm.uploadKey = ''
  wxForm.uploadKeyConfigured = false
  wxForm.configured = false
  // 没配过凭据的企业也没有「关代发」一说（走平台全局小程序），缺省允许
  wxForm.releaseEnabled = true
  wxVisible.value = true
  wxLoading.value = true
  try {
    const resp = await getCompanyWXApp(row.id)
    wxForm.appId = resp.data?.appId || ''
    wxForm.configured = !!resp.data?.configured
    wxForm.releaseEnabled = resp.data?.releaseEnabled !== false
    // 上传密钥只回「配没配」，不回内容；这里要的是「留空保存会不会清掉已有配置」
    wxForm.uploadKeyConfigured = !!resp.data?.uploadKeyConfigured
  } catch {
    // 拦截器已提示；保持空表单，用户仍可手工填写
  } finally {
    wxLoading.value = false
  }
}

function resetWX() {
  wxForm.id = 0
  wxForm.appId = ''
  wxForm.appSecret = ''
  wxForm.uploadKey = ''
  wxForm.uploadKeyConfigured = false
  wxForm.configured = false
  wxForm.releaseEnabled = true
  wxFormRef.value?.clearValidate()
}

async function submitWX() {
  if (!wxFormRef.value) return
  const ok = await wxFormRef.value.validate().catch(() => false)
  if (!ok) return
  // 二次确认：两个密钥都是「留空即清空」，且清完都不可逆（内容无法回读，
  // 只能去公众平台重新复制）。合并成一次确认——连弹两次窗只会让人机械点「确定」。
  const clearing: string[] = []
  if (wxForm.configured && !wxForm.appSecret) clearing.push('AppSecret（该企业将回落平台全局小程序）')
  if (wxForm.uploadKeyConfigured && !wxForm.uploadKey) clearing.push('上传密钥（上传代码时将改用平台全局那把）')
  if (clearing.length > 0) {
    try {
      await ElMessageBox.confirm(
        `以下已保存的配置会被清空：${clearing.join('；')}。密钥无法回读，之后需去微信公众平台重新复制。确定保存？`,
        '确认清空密钥',
        { type: 'warning', confirmButtonText: '留空并保存' }
      )
    } catch {
      return // 用户取消
    }
  }
  savingWX.value = true
  try {
    await saveCompanyWXApp(wxForm.id, {
      appId: wxForm.appId.trim(),
      appSecret: wxForm.appSecret.trim(),
      // 与 appSecret 同语义：一律带上，空串即清空（后端无法区分「没传」和「传空」）
      uploadKey: wxForm.uploadKey.trim(),
      releaseEnabled: wxForm.releaseEnabled,
    })
    ElMessage.success(wxForm.appSecret || wxForm.uploadKey ? '凭据已保存' : '已清空，回落到平台全局小程序')
    wxVisible.value = false
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    savingWX.value = false
  }
}

// ===== 小程序版本发布 =====
// 「版本」入口：打开某企业的版本发布弹窗。只读列表不受 independentLock 影响
// （查看历史版本不该被锁住），写动作由弹窗内的按钮各自判断后端返回。
const releasesVisible = ref(false)
const releasesCompany = ref<CompanyItem | null>(null)

function openReleases(row: any) {
  releasesCompany.value = row
  releasesVisible.value = true
}

// ===== 授权与存储配额（B4 试用转正 / B3 单租户存储上限）=====
// 这里把两件事放在同一个弹窗，是因为它们在运营动作上是连在一起的：
// 客户续费时「延期 + 顺带给够存储」是一句话的事，拆成两个弹窗反而要来回找入口。
// 但保存时仍是两个独立接口（授权 / 配额），没有跨表事务的假象——见 submitLicense。
const licVisible = ref(false)
const licLoading = ref(false)
const licSaving = ref(false)
const licCompany = ref<CompanyItem | null>(null)
const licInfo = ref<CompanyLicense | null>(null)
// 用量只用于展示（"当前已用 X"），配额本身以 licForm.quotaMb 为准；
// quotaGlobal 是全局兜底值，用来解释"跟随全局"到底跟到多少
const storage = reactive({
  bytes: 0,
  files: 0,
  quotaMb: 0,
  quotaGlobal: 0,
  humanBytes: '0 B',
  percent: 0,
  // 用量接口是否成功拿到过值。**这决定保存时敢不敢提交配额**：
  // 拉取失败时 licForm.quotaMb 还是 0，若照旧提交就等于把客户已设的上限
  // 悄悄改回「跟随全局」——一次接口抖动导致的行为变更，事后无从解释。
  loaded: false,
})
const licForm = reactive({ edition: '', seats: 0, expiresAt: '', features: '', quotaMb: 0 })

function resetLicense() {
  licCompany.value = null
  licInfo.value = null
  licForm.edition = ''
  licForm.seats = 0
  licForm.expiresAt = ''
  licForm.features = ''
  licForm.quotaMb = 0
  storage.bytes = 0
  storage.files = 0
  storage.quotaMb = 0
  storage.quotaGlobal = 0
  storage.humanBytes = '0 B'
  storage.percent = 0
  storage.loaded = false
}

// 列表行已带授权摘要（licenseEdition / licenseSeats / licenseExpiresAt），
// 先用它填表单：弹窗不会空白一闪，接口慢的时候也能直接看到当前值。
async function openLicense(row: any) {
  licCompany.value = row
  licInfo.value = null
  licForm.edition = row.licenseEdition || ''
  licForm.seats = row.licenseSeats || 0
  licForm.expiresAt = row.licenseExpiresAt || ''
  licForm.features = ''
  licForm.quotaMb = 0
  storage.bytes = row.storageBytes || 0
  storage.files = 0
  storage.quotaMb = 0
  storage.quotaGlobal = 0
  storage.humanBytes = fmtBytes(row.storageBytes)
  storage.percent = 0
  storage.loaded = false
  licVisible.value = true
  await reloadLicense(row.id)
}

// 两个接口一起拉，但**分开落地**：任一失败不该把另一份也丢掉。
// （用 Promise.all 会因为配额接口抖动而让授权区显示"未配置"——那是误导，
// 运营可能据此以为客户没有授权，直接给人转正。）
async function reloadLicense(id: number) {
  licLoading.value = true
  try {
    const [licRes, stRes] = await Promise.allSettled([getCompanyLicense(id), getStorageUsage(id)])
    if (licRes.status === 'fulfilled' && licRes.value.data) {
      const d = licRes.value.data
      licInfo.value = d
      licForm.edition = d.edition || ''
      licForm.seats = d.seats || 0
      licForm.expiresAt = d.expiresAt || ''
      licForm.features = d.features || ''
    }
    if (stRes.status === 'fulfilled' && stRes.value.data) {
      const s = stRes.value.data
      storage.bytes = s.bytes || 0
      storage.files = s.files || 0
      storage.quotaMb = s.quotaMb || 0
      storage.quotaGlobal = s.quotaGlobal || 0
      storage.humanBytes = s.humanBytes || fmtBytes(s.bytes)
      storage.percent = s.percent || 0
      storage.loaded = true
      // 覆盖值（0 = 跟随全局）才是这一栏该显示的东西：显示生效上限会让运营
      // 误以为该企业已经单独设过配额，改全局时以为没生效。
      licForm.quotaMb = s.quotaMb || 0
    }
  } finally {
    licLoading.value = false
  }
}

async function submitLicense() {
  if (!licCompany.value) return
  if (locked.value) {
    ElMessage.warning('已开启独立版企业锁定，当前为只读')
    return
  }
  // 到期填在过去 = 保存后立刻降级只读。这种"点一下就把客户停服"的操作必须二次确认，
  // 而且要说清后果（不是"确定保存吗"这种废话式确认）。
  const past = licForm.expiresAt && licForm.expiresAt < todayStr()
  if (past) {
    try {
      await ElMessageBox.confirm(
        `到期日 ${licForm.expiresAt} 已经过去，保存后「${licCompany.value.name}」会立刻降级为只读（能登录看，不能改）。确定保存？`,
        '到期日已过去',
        { type: 'warning', confirmButtonText: '仍然保存' }
      )
    } catch {
      return // 用户取消
    }
  }
  licSaving.value = true
  try {
    // 两个接口顺序发，任一失败都会停在错误上并保留弹窗：用户重试时前一个
    // 请求是幂等的（全量覆盖），不会产生"改了一半"的脏数据。
    await saveCompanyLicense(licCompany.value.id, {
      edition: licForm.edition,
      seats: licForm.seats,
      expiresAt: licForm.expiresAt,
      features: licForm.features,
    })
    // 用量没取到就不提交配额：那时 licForm.quotaMb 还是初值 0，提交等于把客户
    // 已设的上限悄悄改回「跟随全局」。宁可这次不改，也要说清为什么没改。
    if (storage.loaded) {
      await saveStorageQuota(licCompany.value.id, licForm.quotaMb)
    } else {
      ElMessage.warning('未取到存储用量，本次只保存授权，存储配额未改动')
    }
    ElMessage.success(storage.loaded ? '授权与配额已保存' : '授权已保存')
    licVisible.value = false
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    licSaving.value = false
  }
}

// 表格「授权」列的三种状态：未配置（不限制）/ 已过期（只读）/ 试用中
function licText(row: any): string {
  if (!row.licenseExists) return '未配置'
  if (row.licenseExpired) return '已过期'
  if (row.licenseEdition === 'trial') return '试用中'
  if (row.licenseEdition === 'platform') return '正式'
  if (row.licenseEdition === 'private') return '独立版'
  return '已授权'
}

function licTagType(row: any): 'success' | 'info' | 'warning' | 'danger' {
  if (!row.licenseExists) return 'info'
  if (row.licenseExpired) return 'danger'
  if (row.licenseEdition === 'trial') return 'warning'
  return 'success'
}

function licTip(row: any): string {
  if (!row.licenseExists) return '未配置授权 = 不受席位与到期限制（存量企业升级后就是这个状态）；点「授权」可显式约束'
  const seats = row.licenseSeats > 0 ? `${row.licenseSeats} 个席位` : '不限席位'
  const exp = row.licenseExpiresAt ? `${row.licenseExpiresAt} 到期` : '不过期'
  if (row.licenseExpired) return `授权已于 ${row.licenseExpiresAt} 过期，该企业后台已降级为只读；改到期日到未来再保存即可恢复`
  return `${seats}，${exp}；当前 ${row.employeeCount} 个员工`
}

// 字节数说成人话。单位口径**必须与后端 humanMB 一致**（<1GB 用 MB 一位小数、
// 否则 GB 两位小数）：同一份用量在两个位置显示成「614.4 KB」和「0.6 MB」，
// 运营会以为是两个数。0 是唯一例外——"还没传过"写 0 B 比 0.0 MB 直观。
function fmtBytes(n?: number): string {
  const v = Number(n || 0)
  if (!Number.isFinite(v) || v <= 0) return '0 B'
  const mb = v / (1024 * 1024)
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  return `${(mb / 1024).toFixed(2)} GB`
}

// 本地日期 YYYY-MM-DD（不能用 toISOString：那是 UTC，东八区下午会算成"昨天"）
function todayStr(): string {
  const d = new Date()
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const day = `${d.getDate()}`.padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}
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
  font-size: 16px;
}
.bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar-tip {
  font-size: 12px;
  color: #909399;
  margin-right: 4px;
}
.divider-hint {
  font-size: 12px;
  color: #909399;
}
.edit-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  padding: 0 4px;
}
.admin-add-bar {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.wx-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.7;
  padding: 0 4px 4px;
}
.wx-tip p {
  margin: 0 0 4px;
}
.wx-tip b {
  color: #e6a23c;
}
.wx-switch-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  margin-left: 10px;
}
/* 授权/配额表单里的单位与口径说明：跟在控件右边，不能换行挤掉控件 */
.lic-hint {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  margin-left: 10px;
}
</style>
