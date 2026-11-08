<template>
  <el-card>
    <div class="page-bar">
      <h3>微信绑定管理</h3>
      <div class="bar-right">
        <span class="bar-tip">集中查看绑定关系，必要时解除免密入口</span>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="auth.isPlatform" type="primary" :icon="Search" @click="openLookup">
          按 openid 反查
        </el-button>
      </div>
    </div>

    <!-- 这页的边界必须写在最显眼的地方：解绑是安全处置动作，代绑则是绝不能开的口子。 -->
    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="只能解绑，不能代绑；解绑会立刻把该账号踢下线"
      description="清单只列已绑定的账号。解绑后该微信不能再免密进入，且该账号已签发的所有登录令牌立即失效（含手机端正在使用的会话），需要重新用账号密码登录。绑定只能由账号本人在小程序里完成，后台不提供代绑。列表里的 openid 均为掩码，原文不会下发。"
      style="margin-bottom: 14px"
    />

    <div class="filter-bar">
      <el-input
        v-model="keyword"
        class="filter-keyword"
        placeholder="搜索账号名 / 企业 / 掩码 / 编号"
        clearable
      />
      <el-select v-model="kind" class="filter-item" placeholder="全部类型">
        <el-option label="全部类型" value="" />
        <el-option label="管理员" value="admin" />
        <el-option label="员工" value="employee" />
      </el-select>
      <span class="scope-tip">
        {{ auth.isPlatform ? '右上角选定企业后按该企业收窄；未选时显示全部企业' : '仅显示本企业的账号' }}
      </span>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column prop="kind" label="类型" width="100" align="center">
        <template #default="{ row }">
          <el-tag :type="row.kind === 'admin' ? 'warning' : 'info'" size="small" effect="plain">
            {{ row.kind === 'admin' ? '管理员' : '员工' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="账号" min-width="170" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.name }}
          <span class="row-id">#{{ row.id }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="companyName" label="企业" min-width="140" show-overflow-tooltip />
      <el-table-column prop="wechatBound" label="微信绑定" width="170" align="center">
        <template #default="{ row }">
          <WechatBindingCell :bound="!!row.wechatBound" :masked="row.wxOpenidMasked" />
        </template>
      </el-table-column>
      <el-table-column prop="createdAt" label="创建时间" width="170" v-if="!isPhone">
        <template #default="{ row }">{{ formatDateTime(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="110" fixed="right" align="center">
        <template #default="{ row }">
          <el-button type="danger" link @click="unbind(row)">解绑</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div v-if="!loading && !rows.length" class="empty-hint">
      还没有账号绑定微信。绑定由账号本人在小程序「我的」页完成，后台不代绑。
    </div>

    <el-pagination
      class="table-pager"
      v-model:current-page="currentPage"
      v-model:page-size="size"
      :total="total"
      :page-sizes="[10, 20, 50]"
      layout="total, sizes, prev, pager, next"
      background
      @current-change="load"
      @size-change="load"
    />

    <!-- 按完整 openid 反查：平台超管专属。输入只进不出，结果里连掩码之外的原文都没有。 -->
    <el-dialog
      v-model="lookupVisible"
      title="按完整 openid 反查绑定"
      width="600px"
      @closed="onLookupClosed"
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="输入完整 openid，查出它绑到了哪些账号"
        description="跨企业关联查询，仅平台超管可用；每次查询都会记入操作日志（只记命中条数，不记 openid）。结果只显示掩码，原文不会被回显，也不会留在输入框里。"
        style="margin-bottom: 12px"
      />
      <div class="lookup-bar">
        <el-input
          v-model="lookupOpenid"
          class="lookup-input"
          placeholder="粘贴完整的 openid"
          autocomplete="off"
          clearable
        />
        <el-button type="primary" :loading="looking" @click="doLookup">查询</el-button>
      </div>
      <div v-if="lookupDone" class="lookup-result">
        <p v-if="!lookupRows.length" class="lookup-empty">没有账号绑定这个 openid</p>
        <div
          v-for="r in lookupRows"
          :key="r.kind + ':' + r.id"
          class="lookup-row"
          :data-kind="r.kind"
          :data-name="r.name"
          :data-company="r.companyName"
          :data-masked="r.wxOpenidMasked"
        >
          <div class="lookup-main">
            <el-tag :type="r.kind === 'admin' ? 'warning' : 'info'" size="small" effect="plain">
              {{ r.kind === 'admin' ? '管理员' : '员工' }}
            </el-tag>
            <b>{{ r.name }}</b>
            <span class="lookup-company">{{ r.companyName }}</span>
            <span class="lookup-mask">{{ r.wxOpenidMasked }}</span>
          </div>
          <el-button type="danger" link @click="unbind(r)">解绑</el-button>
        </div>
      </div>
      <template #footer>
        <el-button @click="lookupVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listWechatBindings,
  lookupWechatBindings,
  unbindWechat,
  type WechatBindingKind,
  type WechatBindingRow,
} from '@/api/wechatBindings'
import WechatBindingCell from '@/components/WechatBindingCell.vue'
import { useAuthStore } from '@/store/auth'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useServerTable } from '@/composables/useServerTable'
import { formatDateTime } from '@/utils/datetime'

const router = useRouter()
const auth = useAuthStore()
const { isPhone } = useBreakpoint()

// 类型筛选在闭包里读，loader 签名仍是 useServerTable 约定的 {page,size,keyword}。
// 后端在分页之前做类型过滤，total 才是筛后的真实条数。
const kind = ref<'' | WechatBindingKind>('')
const { rows, keyword, currentPage, size, total, loading, load } = useServerTable((q) =>
  listWechatBindings({ ...q, kind: kind.value || undefined })
)

watch(kind, () => {
  // 与 useServerTable 的关键字 watcher 同口径：不在第 1 页时改页码，由它自己触发一次 load，
  // 否则显式重载。两条分支都不能重复发请求。
  if (currentPage.value !== 1) currentPage.value = 1
  else void load()
})

onMounted(load)

// ===== 解绑 =====
// 确认文案必须把两个副作用说清：免密入口没了，且**已签发的令牌全失效**。
// 只写「确定解绑吗」的话，运营会以为只是关掉一个开关，直到有人被踢下线才发现。
// el-table 插槽的 row 是 Element Plus 内部 DefaultRow（索引签名），与业务类型不兼容；
// 反查结果里的行则是真实 WechatBindingRow。统一在入口处收窄一次，
// 与 Accounts.vue / Companies.vue 的表格槽处理保持同一口径。
async function unbind(row: any) {
  const target = row as WechatBindingRow
  try {
    await ElMessageBox.confirm(
      `确定解除「${target.name}」的微信绑定？\n\n` +
        '· 这个微信将不能再免密进入该账号；\n' +
        '· 该账号所有已签发的登录令牌立即失效（含手机端正在使用的会话），需要重新登录；\n' +
        '· 重新绑定只能由账号本人在小程序里完成，后台无法代绑。',
      '确认解绑',
      { type: 'warning', confirmButtonText: '确认解绑' }
    )
  } catch {
    return // 用户取消
  }
  try {
    const r = await unbindWechat(target.kind, target.id)
    if (r.code !== 0) return
    if (r.self) {
      // 不调 adminLogout()：安全戳已自增，当前令牌本来就是无效的，
      // 再发一次 logout 只会换来拦截器弹「登录已失效」。直接清本地登录态即可。
      auth.logout()
      ElMessage.success('已解绑当前账号，请重新登录')
      router.replace('/login')
      return
    }
    ElMessage.success('已解绑')
    // 反查结果里若正好有这一行，就地移除：不重新发一次 lookup，
    // 也避免完整 openid 再次出网。
    lookupRows.value = lookupRows.value.filter(
      (x) => !(x.kind === target.kind && x.id === target.id)
    )
    await load()
  } catch {
    // 拦截器已提示（如「该账号未绑定微信」「账号不存在」）
  }
}

// ===== 按 openid 反查（平台超管）=====
const lookupVisible = ref(false)
const lookupOpenid = ref('')
const lookupRows = ref<WechatBindingRow[]>([])
const lookupDone = ref(false)
const looking = ref(false)

function openLookup() {
  lookupVisible.value = true
}

function onLookupClosed() {
  lookupOpenid.value = ''
  lookupRows.value = []
  lookupDone.value = false
}

async function doLookup() {
  const openid = lookupOpenid.value.trim()
  if (!openid) {
    ElMessage.warning('请填写完整的 openid')
    return
  }
  if (openid.length > 128) {
    ElMessage.warning('openid 过长，请核对后重试')
    return
  }
  looking.value = true
  try {
    const r = await lookupWechatBindings(openid)
    if (r.code !== 0) return
    lookupRows.value = r.data ?? []
    lookupDone.value = true
    // 完整 openid 只在这次请求里用，不留在输入框、不写本地存储、不进日志
    lookupOpenid.value = ''
  } catch {
    // 拦截器已提示（如 403 越权）
  } finally {
    looking.value = false
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
.filter-bar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.filter-keyword {
  width: 260px;
}
.filter-item {
  width: 140px;
}
.scope-tip {
  color: #909399;
  font-size: 12px;
}
.row-id,
.lookup-company,
.lookup-mask {
  color: #909399;
  font-size: 12px;
}
.row-id {
  margin-left: 6px;
}
.lookup-mask {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.empty-hint,
.lookup-empty {
  padding: 18px 0;
  text-align: center;
  color: #909399;
  font-size: 13px;
}
.lookup-bar {
  display: flex;
  gap: 8px;
  align-items: center;
}
.lookup-input {
  flex: 1;
}
.lookup-result {
  margin-top: 14px;
}
.lookup-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  margin-bottom: 8px;
}
.lookup-main {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
@media (max-width: 767px) {
  .page-bar {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  .bar-tip,
  .scope-tip {
    display: none;
  }
  .filter-keyword,
  .filter-item {
    width: 100%;
  }
  .lookup-bar {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
