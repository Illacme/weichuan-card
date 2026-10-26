<template>
  <el-card>
    <div class="page-bar">
      <h3>账号管理</h3>
      <div class="bar-right">
        <span class="bar-tip">平台超级管理员账号：改名 / 新增 / 重置密码 / 删除</span>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">新增管理员</el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="改自己的密码请用右上角「修改密码」"
      description="这里的「重置密码」用于给其他管理员重置（不需要原密码），重置后对方所有登录会话立即失效。改名不会影响登录态。"
      style="margin-bottom: 14px"
    />

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column prop="id" label="编号" width="80" align="center" v-if="!isPhone" />
      <el-table-column prop="username" label="用户名" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.username }}
          <el-tag v-if="row.id === myId" size="small" effect="plain" type="success">当前登录</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="创建时间" min-width="180" show-overflow-tooltip v-if="!isPhone">
        <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="260" fixed="right" align="center">
        <template #default="{ row }">
          <el-button type="primary" link @click="rename(row)">改名</el-button>
          <el-button type="warning" link @click="resetPwd(row)">重置密码</el-button>
          <el-button
            type="danger"
            link
            :disabled="row.id === myId"
            :title="row.id === myId ? '不能删除当前登录的账号' : ''"
            @click="remove(row)"
            >删除</el-button
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

    <!-- 新增管理员 -->
    <el-dialog v-model="createVisible" title="新增平台管理员" width="440px" @closed="resetCreate">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" placeholder="登录用户名（全平台唯一）" clearable />
        </el-form-item>
        <el-form-item label="初始密码" prop="password">
          <el-input v-model="form.password" type="password" show-password placeholder="初始密码" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">确定</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
const { isPhone } = useBreakpoint()
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import {
  createPlatformAdmin,
  deletePlatformAdmin,
  listPlatformAdmins,
  renamePlatformAdmin,
  resetPlatformAdminPassword,
} from '@/api/admins'
import { adminLogout, getMe } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { formatDateTime } from '@/utils/datetime'
import { useBreakpoint } from '@/composables/useBreakpoint'
// 服务端分页：与其它管理端列表同构（平台超管数量本就不多，但保持契约一致）
import { useServerTable } from '@/composables/useServerTable'

const router = useRouter()
const auth = useAuthStore()

const { rows, currentPage, size, total, loading, load } = useServerTable((q) =>
  listPlatformAdmins(q)
)
// 当前登录账号 id：用于标注「当前登录」并禁用自删按钮（后端也有护栏，这里是界面层）
const myId = ref<number>(0)

// 当前登录账号 id 从 /admin/me 取（列表接口不返回，避免多余耦合）
async function loadMe() {
  try {
    const r = await getMe()
    if (r.code === 0 && r.data) myId.value = r.data.adminId
  } catch {
    // 取不到就不标注，不影响列表使用
  }
}

onMounted(async () => {
  await loadMe()
  await load()
})

// ===== 新增 =====
const createVisible = ref(false)
const creating = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({ username: '', password: '' })

const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入初始密码', trigger: 'blur' }],
}

function openCreate() {
  createVisible.value = true
}

function resetCreate() {
  form.username = ''
  form.password = ''
  formRef.value?.clearValidate()
}

async function submitCreate() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  creating.value = true
  try {
    const r = await createPlatformAdmin(form.username.trim(), form.password)
    if (r.code === 0) {
      ElMessage.success('已创建')
      createVisible.value = false
      await load()
    }
  } catch {
    // 拦截器已提示（如「用户名已存在」）
  } finally {
    creating.value = false
  }
}

// ===== 改名 =====
// 参数用 any：el-table 插槽的 row 是其内部 DefaultRow 类型，与业务类型不兼容
// （与 Companies.vue 等页面一致，不为此处额外做类型断言）
async function rename(row: any) {
  try {
    const { value } = await ElMessageBox.prompt('新的登录用户名', `改名 - ${row.username}`, {
      inputValue: row.username,
      inputValidator: (v: string) => (v && v.trim() ? true : '用户名不能为空'),
    })
    const r = await renamePlatformAdmin(row.id, String(value).trim())
    if (r.code !== 0) return
    ElMessage.success('已改名')
    // 改的是自己 → 刷新顶栏展示的用户名（令牌不含用户名，无需重新登录）
    if (r.self) {
      try {
        const me = await getMe()
        if (me.code === 0 && me.data) auth.hydrate(me.data)
      } catch {
        // 刷新失败不影响改名结果
      }
    }
    await load()
  } catch {
    // 用户取消或请求失败（拦截器已提示），均不做处理
  }
}

// ===== 重置密码 =====
async function resetPwd(row: any) {
  try {
    const { value } = await ElMessageBox.prompt(
      `重置后「${row.username}」的所有登录会话将立即失效`,
      '重置密码',
      {
        inputType: 'password',
        inputPlaceholder: '输入新密码',
        inputValidator: (v: string) => (v ? true : '新密码不能为空'),
      }
    )
    const r = await resetPlatformAdminPassword(row.id, String(value))
    if (r.code !== 0) return
    // 重置的是自己 → 安全戳自增，当前令牌已失效，必须重新登录
    if (r.self) {
      try {
        await adminLogout()
      } catch {
        // 忽略：本地照常登出
      }
      auth.logout()
      ElMessage.success('密码已重置，请重新登录')
      router.replace('/login')
      return
    }
    ElMessage.success('已重置密码')
  } catch {
    // 用户取消或请求失败
  }
}

// ===== 删除 =====
async function remove(row: any) {
  if (row.id === myId.value) return
  try {
    await ElMessageBox.confirm(
      `确定删除管理员「${row.username}」？删除后该账号立即无法登录。`,
      '确认删除',
      { type: 'warning' }
    )
  } catch {
    return // 用户取消
  }
  try {
    const r = await deletePlatformAdmin(row.id)
    if (r.code === 0) {
      ElMessage.success('已删除')
      await load()
    }
  } catch {
    // 拦截器已提示（如「至少保留一个平台超管」）
  }
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
}
.bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar-tip {
  color: #909399;
  font-size: 13px;
}
/* 手机端：提示语换行，避免把按钮挤出屏幕 */
@media (max-width: 767px) {
  .page-bar {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  .bar-tip {
    display: none;
  }
}
</style>
