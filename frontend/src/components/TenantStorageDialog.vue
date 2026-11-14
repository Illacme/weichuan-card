<template>
  <el-dialog
    v-model="visible"
    :title="`自有存储 - ${company?.name || ''}`"
    width="660px"
    class="tenant-storage-dialog"
    @closed="reset"
  >
    <!-- 生效状态必须写在最上面，且要说清是哪一种「没生效」。
         写死一句话会在开关打开后继续骗人；只回一个 bool 则会让超管拿着
         "未生效"去猜是没填凭据、没勾启用，还是运维没开路由。 -->
    <el-alert
      :type="alertView.type"
      :closable="false"
      show-icon
      :title="alertView.title"
      :description="alertView.desc"
      style="margin-bottom: 12px"
    />

    <el-form v-loading="loading" :model="form" label-width="120px" label-position="right">
      <el-form-item label="驱动">
        <el-select v-model="form.driver" style="width: 100%">
          <el-option label="MinIO / 自建 S3 兼容" value="minio" />
          <el-option label="阿里云 OSS" value="oss" />
          <el-option label="S3 兼容云（COS / OBS / 七牛 / AWS / R2）" value="s3" />
        </el-select>
        <div class="hint">
          客户想「文件不落在平台机器上」时选这里。要让这家企业**回到平台存储**，用底部的「切回平台存储」。
        </div>
      </el-form-item>

      <el-form-item label="访问域名">
        <el-input v-model="form.endpoint" placeholder="play.min.io 或 oss-cn-hangzhou.aliyuncs.com" />
        <div class="hint">
          只填 <b>host[:port]</b>，<b>不要带 https://</b>。带 scheme 是最常见的填错方式，
          会让接入层拼出 http://https://xxx。
        </div>
      </el-form-item>

      <el-form-item label="Bucket">
        <el-input v-model="form.bucket" placeholder="客户桶名" />
      </el-form-item>

      <el-form-item label="Region">
        <el-input v-model="form.region" placeholder="oss-cn-hangzhou / ap-guangzhou（MinIO 可留空）" />
        <div class="hint">仅 OSS / S3 需要。填错时报的是签名错误，看着像密钥不对，实际是区域不对。</div>
      </el-form-item>

      <el-form-item label="Host 头">
        <el-input v-model="form.hostHeader" placeholder="如 wc-bucket.oss-cn-hangzhou.aliyuncs.com" />
        <div class="hint">
          virtual-hosted-style 的桶名在 Host 里，且它是 TLS SNI 的取值——漏了会被桶以 421 拒绝。
          照控制台抄（逐厂商不同，桶名在前还是在后没有通用规则）。
        </div>
      </el-form-item>

      <el-form-item label="HTTPS">
        <el-switch v-model="form.useSsl" active-text="走 443" inactive-text="走 80" />
      </el-form-item>

      <el-form-item label="自有访问域名">
        <el-input v-model="form.publicBaseUrl" placeholder="如 https://img.cust.com，留空 = 走平台统一入口" />
        <div class="hint">
          只填 <b>纯域名</b>（http(s)://host[:port]），不带路径 / 查询。
          它是<b>访客侧 origin</b>，与上面的「Host 头」（回源侧 Host / TLS SNI）是两件事，互不影响；
          客户自有域名不要填进 Host 头，那里只照桶方控制台抄。
        </div>
        <!-- 「填了以为生效」比「不填」更坏：只要表单里有值而后端回报未生效，
             当场把这句话摆出来，不等保存后才发现。 -->
        <el-alert
          v-if="publicBaseUrlPending"
          type="warning"
          :closable="false"
          show-icon
          title="已登记，但当前不会改变出参 URL"
          :description="cfg?.publicBaseUrlNote || '出参侧按租户选 origin 尚未接入：对象 URL 仍走平台统一入口。'"
          style="margin-top: 8px"
        />
      </el-form-item>

      <el-form-item label="公开读">
        <el-switch v-model="form.publicRead" />
        <div class="hint">
          第一阶段只支持公开读桶。私有桶要走预签名，出参契约会跟着变（存裸键还是存 URL），
          本期没做；写入侧与上传侧都会显式拒绝关闭它。
        </div>
      </el-form-item>

      <el-form-item label="启用">
        <el-switch v-model="form.enabled" />
        <div class="hint">
          启用前建议先点「连通性自检」。停用不会搬走已有对象，只是让新配置不再指向该桶。
        </div>
      </el-form-item>

      <el-divider content-position="left">凭据（只写不读）</el-divider>

      <el-alert
        v-if="cfg?.credentialError"
        type="error"
        :closable="false"
        show-icon
        :title="cfg.credentialError"
        style="margin-bottom: 12px"
      />

      <el-form-item label="AccessKey">
        <el-input
          v-model="form.accessKey"
          :placeholder="akPlaceholder"
          autocomplete="off"
          spellcheck="false"
        />
        <div class="hint">
          已保存的是密文，这里只显示掩码供你核对「现在生效的是不是新那把」。
          <b>留空 = 保持原值不变</b>；要换成新密钥，直接覆盖填写。
        </div>
      </el-form-item>

      <el-form-item label="SecretKey">
        <el-input
          v-model="form.secretKey"
          type="password"
          show-password
          :placeholder="skPlaceholder"
          autocomplete="new-password"
          spellcheck="false"
        />
        <div class="hint">SecretKey 永不回显（掩码也不给）：它没有核对价值，泄漏等于把桶交出去。</div>
      </el-form-item>

      <el-form-item v-if="cfg?.accessKeyConfigured || cfg?.secretKeyConfigured" label=" ">
        <el-button link type="danger" @click="clearCredentialsPending = true">
          清空已存凭据（切回平台账号 / 换账号时用）
        </el-button>
        <span v-if="clearCredentialsPending" class="clear-tip">保存后凭据会被清空</span>
      </el-form-item>
    </el-form>

    <el-divider content-position="left">连通性自检</el-divider>

    <div class="check-bar">
      <el-tag :type="statusTagType" effect="light" size="small">{{ statusText }}</el-tag>
      <span class="check-time">{{ cfg?.lastCheckedAt ? `最近一次：${cfg.lastCheckedAt}` : '尚未自检' }}</span>
      <el-button
        size="small"
        :loading="checking"
        :disabled="!cfg?.configured"
        @click="onCheck"
      >
        立即自检
      </el-button>
    </div>
    <div v-if="!cfg?.configured" class="hint" style="margin-top: 6px">
      先保存配置才能自检——自检是拿**库里已保存的**凭据去探桶的。
    </div>
    <div v-if="!checking && cfg?.lastError" class="check-error">{{ cfg.lastError }}</div>
    <div v-if="!checking && checkResult?.ok" class="check-ok">
      {{ checkResult.detail }}
      <div class="hint">本次自检范围：{{ checkResult.checkedScope }}</div>
    </div>

    <template #footer>
      <div class="footer">
        <el-button
          v-if="cfg?.configured"
          type="danger"
          plain
          :loading="resetting"
          @click="onReset"
        >
          切回平台存储
        </el-button>
        <span class="spacer" />
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  checkTenantStorage,
  getTenantStorage,
  resetTenantStorage,
  saveTenantStorage,
  type StorageDriver,
  type TenantStorageCheckResult,
  type TenantStorageConfig,
} from '@/api/tenantStorage'

const props = defineProps<{ company: { id: number; name: string } | null }>()
const visible = defineModel<boolean>({ default: false })

const loading = ref(false)
const saving = ref(false)
const checking = ref(false)
const resetting = ref(false)
const cfg = ref<TenantStorageConfig | null>(null)
const checkResult = ref<TenantStorageCheckResult | null>(null)
// 「清空已存凭据」不是立即生效的动作，要跟表单一起保存。
// 独立一个标记而不是直接把输入框清空：输入框为空本身就表示「不改」，
// 两种意图必须能分开表达，否则「清空」会被当成「不改」而静默失败。
const clearCredentialsPending = ref(false)
// 顶部提示三态，各自对应完全不同的下一步动作：
//   已生效 → 什么都不用做
//   路由开了但这家没生效 → 按 inactiveReason 去改（没勾启用 / 驱动是 local / 凭据缺）
//   路由没开 → 这是部署决策，改配置没用，要找运维
const alertView = computed(() => {
  if (cfg.value?.activeForTenant) {
    return {
      type: 'success' as const,
      title: '该企业正在使用自有桶',
      desc: '新上传写入客户桶，出参 URL 形态不变（已登记的自有访问域名当前也不会改变它）；存量对象仍在平台侧。若这里填错，该企业的上传会直接失败，不会退回平台存储。',
    }
  }
  if (cfg.value?.trafficEnabled) {
    return {
      type: 'warning' as const,
      title: '租户存储路由已开启，但这家尚未生效',
      desc: cfg.value?.inactiveReason || '该企业仍写入平台存储。',
    }
  }
  return {
    type: 'warning' as const,
    title: '本期只保存配置，尚未承载流量',
    desc: '本进程未开启租户存储路由（TENANT_STORAGE_ROUTE），上传与出参仍走平台存储。这份配置的价值是：先把客户的桶信息与凭据登记好、当场验通，避免切量那天才发现填错。',
  }
})

const form = reactive({
  driver: 'minio' as StorageDriver,
  endpoint: '',
  bucket: '',
  region: '',
  useSsl: true,
  hostHeader: '',
  publicBaseUrl: '',
  publicRead: true,
  enabled: false,
  accessKey: '',
  secretKey: '',
})

// 表单里有值、但后端如实回报「尚未接入」时，当场显示未生效提示。
// 用 form 而不是 cfg.publicBaseUrl：超管刚敲进去、还没点保存的新值也要被提醒，
// 否则提示只在「保存完再打开一次对话框」才出现。
const publicBaseUrlPending = computed(
  () => form.publicBaseUrl.trim() !== '' && cfg.value?.publicBaseUrlActive === false
)

const akPlaceholder = computed(() => {
  if (!cfg.value?.accessKeyConfigured) return '未配置，填写 AccessKey'
  return cfg.value.accessKeyMasked ? `已配置（${cfg.value.accessKeyMasked}），留空不改` : '已配置，留空不改'
})
const skPlaceholder = computed(() =>
  cfg.value?.secretKeyConfigured ? '已配置，留空不改' : '未配置，填写 SecretKey'
)

const statusText = computed(() => {
  switch (cfg.value?.status) {
    case 'ok':
      return '自检通过'
    case 'error':
      return '自检未通过'
    default:
      return cfg.value?.configured ? '尚未自检' : '未配置'
  }
})
const statusTagType = computed(() => {
  switch (cfg.value?.status) {
    case 'ok':
      return 'success'
    case 'error':
      return 'danger'
    default:
      return 'info'
  }
})

watch(visible, async (v) => {
  if (v) await load()
})

async function load() {
  if (!props.company) return
  loading.value = true
  try {
    const c = await getTenantStorage(props.company.id)
    cfg.value = c
    form.driver = c.driver === 'local' ? 'minio' : c.driver
    form.endpoint = c.endpoint
    form.bucket = c.bucket
    form.region = c.region
    form.useSsl = c.useSsl
    form.hostHeader = c.hostHeader
    form.publicBaseUrl = c.publicBaseUrl ?? ''
    form.publicRead = c.publicRead
    form.enabled = c.enabled
    // 凭据永远留空：留空 = 保持原值。这是「只写不读」在前端的落点。
    form.accessKey = ''
    form.secretKey = ''
    clearCredentialsPending.value = false
    checkResult.value = null
  } finally {
    loading.value = false
  }
}

function reset() {
  cfg.value = null
  checkResult.value = null
  clearCredentialsPending.value = false
}

/** 前端只挡明显写错的，格式的最终裁判是后端 nginxsnippet.Target.Validate（单点）。 */
function validate(): string {
  if (!form.endpoint.trim()) return '请填写访问域名'
  if (form.endpoint.includes('://')) return '访问域名不要带 https://，只填 host[:port]'
  if (!form.bucket.trim()) return '请填写 Bucket'
  // 自有访问域名：前端只挡一眼能看出的写错，最终裁判是后端
  // storage.ValidatePublicOrigin（它与出参 ResolveRef 的 origin 校验同源）。
  // 这里若另写一套严规则，会出现「前端不让存、后端其实能存」的口径漂移。
  const base = form.publicBaseUrl.trim().replace(/\/+$/, '')
  if (base && !/^https?:\/\/[^/?#\s"']+$/i.test(base)) {
    return '自有访问域名必须是 http(s)://host[:port]，不要带路径、查询或用户名密码'
  }
  if (form.enabled && !form.accessKey.trim() && !cfg.value?.accessKeyConfigured) {
    return '启用前必须填写 AccessKey'
  }
  if (form.enabled && !form.secretKey.trim() && !cfg.value?.secretKeyConfigured) {
    return '启用前必须填写 SecretKey'
  }
  return ''
}

async function onSave() {
  if (!props.company) return
  const err = validate()
  if (err) {
    ElMessage.warning(err)
    return
  }
  saving.value = true
  try {
    const payload: Parameters<typeof saveTenantStorage>[1] = {
      driver: form.driver,
      endpoint: form.endpoint.trim(),
      bucket: form.bucket.trim(),
      region: form.region.trim(),
      useSsl: form.useSsl,
      hostHeader: form.hostHeader.trim(),
      // 表单回显的就是库里的原值，所以这里总是带上：不改时是「有值=覆盖同一个值」，
      // 清空时是空串=清空。不要改成「留空就不传」——那会让清空永远无法生效。
      publicBaseUrl: form.publicBaseUrl.trim(),
      publicRead: form.publicRead,
      enabled: form.enabled,
    }
    if (clearCredentialsPending.value) {
      // 空串 = 清空（后端三态里的第二态）
      payload.accessKey = ''
      payload.secretKey = ''
    } else {
      // 有值才传。**不传 ≠ 清空**：留空表示「不改」，这是三态的第三态。
      if (form.accessKey.trim()) payload.accessKey = form.accessKey.trim()
      if (form.secretKey.trim()) payload.secretKey = form.secretKey.trim()
    }
    cfg.value = await saveTenantStorage(props.company.id, payload)
    form.accessKey = ''
    form.secretKey = ''
    clearCredentialsPending.value = false
    ElMessage.success('已保存。记得点「立即自检」确认能连上')
  } finally {
    saving.value = false
  }
}

async function onCheck() {
  if (!props.company) return
  checking.value = true
  checkResult.value = null
  try {
    checkResult.value = await checkTenantStorage(props.company.id)
    // 重新拉一次：status / lastCheckedAt / lastError 由服务端回写，本地别自己拼
    cfg.value = await getTenantStorage(props.company.id)
  } finally {
    checking.value = false
  }
}

async function onReset() {
  if (!props.company) return
  try {
    await ElMessageBox.confirm(
      '切回平台存储后：这家企业的新上传会回到平台盘，但**已经传进客户桶的对象不会被搬回来**（那属于存量搬迁，另一步）。确定继续？',
      '切回平台存储',
      { type: 'warning', confirmButtonText: '确定切回', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  resetting.value = true
  try {
    await resetTenantStorage(props.company.id)
    ElMessage.success('已切回平台存储')
    visible.value = false
  } finally {
    resetting.value = false
  }
}
</script>

<style scoped>
.hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
  margin-top: 4px;
}
.check-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.check-time {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.check-error {
  margin-top: 8px;
  font-size: 13px;
  color: var(--el-color-danger);
  word-break: break-all;
}
.check-ok {
  margin-top: 8px;
  font-size: 13px;
  color: var(--el-color-success);
}
.clear-tip {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-color-danger);
}
.footer {
  display: flex;
  align-items: center;
}
.spacer {
  flex: 1;
}
</style>
