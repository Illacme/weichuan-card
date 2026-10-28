<template>
  <!-- 安装向导：首次部署时用表单完成「连库 → 建超管 → 写配置 → 自重启 → 进登录页」。
       与后端 /setup 内置页是同一套接口（/api/setup/test、/api/setup/install），
       区别只在外壳：后端页是纯静态自包含 HTML，前端页走 Element Plus 风格，
       生产由 nginx 托管时运维看到的是这一页。

       呈现形态：整页背景衬底 + 居中 dialog 浮层（聚焦、不分散），内部「真分步」
       —— 1 数据库 → 2 管理员 → 3 完成，每步一屏，比原「假三步长表单」认知负荷更低。 -->
  <div class="si-root">
    <div class="si-dialog" role="dialog" aria-label="安装向导">
      <header class="si-head">
        <div class="si-head-title">
          <img class="si-logo" src="/logo.png" alt="微传名片" />
          <span>安装向导</span>
        </div>
        <span class="si-head-tag">首次部署</span>
      </header>

      <ol class="si-steps">
        <li :class="stepClass(1)"><b>{{ 1 < step ? '✓' : 1 }}</b><span>数据库</span></li>
        <li :class="stepClass(2)"><b>{{ 2 < step ? '✓' : 2 }}</b><span>管理员</span></li>
        <li :class="stepClass(3)"><b>{{ 3 < step ? '✓' : 3 }}</b><span>完成</span></li>
      </ol>

      <div class="si-body">
      <!-- ---------- 步骤 1：数据库 ---------- -->
      <section v-show="step === 1 && !confirming" class="si-pane">
        <h2 class="si-h2">选择数据库</h2>
        <p class="si-lead">
          先选一种数据库，再填写连接信息。SQLite 零依赖，适合试用；MySQL 适合生产环境。
        </p>

        <div class="si-engines">
          <div
            class="si-engine"
            :class="{ active: engine === 'mysql' }"
            role="radio"
            :aria-checked="engine === 'mysql'"
            @click="engine = 'mysql'"
          >
            <span v-if="engine === 'mysql'" class="si-engine-check" aria-hidden="true"></span>
            <b>MySQL</b>
            <p>适合生产与多人使用。需要一台可访问的 MySQL 服务，docker compose 部署已内置。</p>
          </div>
          <div
            class="si-engine"
            :class="{ active: engine === 'sqlite' }"
            role="radio"
            :aria-checked="engine === 'sqlite'"
            @click="engine = 'sqlite'"
          >
            <span v-if="engine === 'sqlite'" class="si-engine-check" aria-hidden="true"></span>
            <b>SQLite</b>
            <p>零依赖单机版，数据即一个文件。适合试用与小规模内部使用；不支持多副本并发写，请勿放在网络盘。</p>
          </div>
        </div>

        <div v-show="engine === 'mysql'" class="si-grid">
          <label>主机<el-input v-model="form.dbHost" autocomplete="off" /></label>
          <label>端口<el-input v-model="form.dbPort" autocomplete="off" /></label>
          <label>用户名<el-input v-model="form.dbUser" autocomplete="off" /></label>
          <label>密码<el-input v-model="form.dbPassword" type="password" show-password autocomplete="new-password" /></label>
        </div>
        <div v-show="engine === 'mysql'" class="si-dbrow">
          <label class="si-field-flat"><span class="si-field-lbl">库名</span><el-input v-model="form.dbName" autocomplete="off" /></label>
          <el-checkbox v-model="form.createDatabase" class="si-chk">
            数据库不存在时自动创建
          </el-checkbox>
          <p v-if="mysqlNote" class="si-tip si-tip--hint si-dbnote">{{ mysqlNote }}</p>
        </div>

        <div v-show="engine === 'sqlite'" class="si-grid">
          <label class="wide">数据文件路径<el-input v-model="form.sqlitePath" autocomplete="off" /></label>
          <p class="si-tip">相对路径相对于服务进程的工作目录；目录不存在时会自动创建。</p>
        </div>

        <p class="si-dsn">连接串预览：<code>{{ dsnPreview }}</code></p>

        <details class="si-adv">
          <summary>高级选项（可选）</summary>
          <div class="si-grid">
            <label class="wide">
              <span class="si-field-cap">JWT 密钥（留空自动生成）
                <el-tooltip placement="top" popper-class="si-hint-popper">
                  <template #content>
                    <p class="si-hint-line"><b>登录签名密钥</b>：服务端用它给每次登录签发的身份凭证盖章并校验真伪。</p>
                    <p class="si-hint-line">留空即自动生成 32 字节随机值；手动填写至少 16 位，不能用内置缺省值（会被直接拒绝启动）。</p>
                    <p class="si-hint-line">安装后更换它，所有人已登录的状态立即失效，需重新登录。</p>
                  </template>
                  <span class="si-hint" tabindex="0" aria-label="JWT 密钥说明">?</span>
                </el-tooltip>
              </span>
              <el-input v-model="form.jwtKey" autocomplete="off" />
            </label>
            <el-checkbox v-model="form.cookieSecure" class="si-chk">
              Cookie 强制 HTTPS
              <el-tooltip placement="top" popper-class="si-hint-popper">
                <template #content>
                  <p class="si-hint-line"><b>登录 Cookie 加 Secure 标记</b>：浏览器只在 HTTPS 连接下发送它，防止登录凭证在 http 明文传输中被窃取。</p>
                  <p class="si-hint-line">站点正式上线（启用 HTTPS）后再勾选；本地 http 环境勾选会导致登录不上。</p>
                </template>
                <span class="si-hint" tabindex="0" aria-label="Cookie 强制 HTTPS 说明">?</span>
              </el-tooltip>
            </el-checkbox>
          </div>
        </details>

        <el-alert
          v-if="alertMsg"
          :type="alertType"
          :title="alertMsg"
          :closable="false"
          show-icon
          class="si-alert"
        />

        <div class="si-actions">
          <el-button :loading="testing" @click="onTest">测试连接</el-button>
          <el-button type="primary" :loading="nexting" @click="onNext">下一步</el-button>
        </div>
      </section>

      <!-- ---------- 步骤 2：管理员 ---------- -->
      <section v-show="step === 2 && !confirming" class="si-pane">
        <h2 class="si-h2">管理员账号</h2>
        <p class="si-lead">
          创建平台超级管理员账号。登录后可在后台右上角「修改密码」随时更换。
        </p>
        <div class="si-grid">
          <label>用户名<el-input v-model="form.adminUser" autocomplete="off" /></label>
          <div class="si-field">
            <label>密码<el-input v-model="form.adminPass" type="password" show-password autocomplete="new-password" /></label>
            <div class="si-strength" :data-level="pwLevel">
              <i v-for="i in 4" :key="i" :class="{ on: i <= pwLevel }" />
              <span class="si-strength-text">{{ pwText }}</span>
            </div>
          </div>
          <label class="wide">确认密码<el-input v-model="form.adminPass2" type="password" show-password autocomplete="new-password" /></label>
        </div>
        <p class="si-tip">建议 12 位以上，混合大小写字母、数字与符号；登录后可随时修改。</p>

        <!-- 初始化数据：放在管理员这一步（最后一步的确认页只做复核），
             与后端 /setup 内置页的选项同名同义（loadDemoData）。 -->
        <div class="si-demo">
          <el-checkbox v-model="form.loadDemoData">
            同时装载演示企业数据（朗汇机电样板）
          </el-checkbox>
          <p class="si-demo-desc">
            勾选后会自动创建一家样板企业，含 8 个业绩分类、10 个项目案例、4 项服务与配套图片，
            方便先把小程序跑起来看效果；正式上线前删掉这家企业即可。
            <b>不勾选则装完是空白系统</b>：登录后先到「企业管理」创建自己的企业，再录内容。
          </p>
        </div>

        <el-alert
          v-if="alertMsg"
          :type="alertType"
          :title="alertMsg"
          :closable="false"
          show-icon
          class="si-alert"
        />

        <div class="si-actions">
          <el-button @click="step = 1">上一步</el-button>
          <el-button type="primary" :loading="installing" @click="onInstall">开始安装</el-button>
        </div>
      </section>

      <!-- ---------- 步骤 3：进度 ---------- -->
      <section v-show="step === 3 && !confirming" class="si-pane">
        <h2 class="si-h2">安装进度</h2>
        <div class="si-progress-bar">
          <div class="si-progress-fill" :style="{ width: progressPct + '%' }"></div>
        </div>
        <ul class="si-psteps">
          <li :class="pClass(1)"><i></i><span>写入配置并初始化数据库</span></li>
          <li :class="pClass(2)"><i></i><span>重启服务并加载配置</span></li>
          <li :class="pClass(3)"><i></i><span>完成，进入登录页</span></li>
        </ul>
        <p class="si-tip">{{ progressTip }}</p>
        <div class="si-actions">
          <el-button v-if="showRecheck" @click="recheck">重新检测</el-button>
        </div>
      </section>

      <!-- ---------- 确认（温和，非红色 danger） ---------- -->
      <div v-if="confirming" class="si-confirm">
        <h3 class="si-confirm-title">确认按以下配置安装？</h3>
        <ul class="si-confirm-list">
          <li>写入配置文件并初始化数据库</li>
          <li>创建管理员账号 <b>{{ form.adminUser || 'admin' }}</b></li>
          <li>
            初始化数据
            <b>{{ form.loadDemoData ? '装载演示企业数据（朗汇机电样板）' : '空白（装完先创建企业）' }}</b>
          </li>
          <li>重启服务并加载配置</li>
          <li>完成后自动进入登录页</li>
        </ul>
        <p class="si-dsn"><code>{{ dsnPreview }}</code></p>
        <div class="si-actions">
          <el-button @click="confirming = false">取消</el-button>
          <el-button type="primary" :loading="installing" @click="doInstall">确认安装</el-button>
        </div>
      </div>

      <!-- ---------- 手动配置兜底（二级入口，默认收起） ---------- -->
      <div class="si-manual">
        <el-button text class="si-manual-entry" @click="manualOpen = !manualOpen">
          手动配置（不使用安装向导）
          <span class="si-manual-caret" :class="{ open: manualOpen }" aria-hidden="true">▾</span>
        </el-button>
        <transition name="si-expand">
          <div v-if="manualOpen" class="si-manual-panel">
            <p class="si-tip">
              把下面内容写进 <code>.env</code>（与 <code>docker-compose.yml</code> 同级）后重启。
              优先级：环境变量 &gt; <code>{{ configFile }}</code> &gt; 内置缺省值。
            </p>
            <pre class="si-pre"><code>{{ props.status.envText }}</code></pre>
            <el-button size="small" @click="copyEnv">复制配置</el-button>
          </div>
        </transition>
      </div>

      <p class="si-note">
        安全说明：安装向导仅在首次配置前可用，安装一次后即关闭；不会覆盖已有数据的数据库；
        配置以 0600 权限写入 <code>{{ configFile }}</code>；响应与日志中不出现明文口令。
      </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  fetchSetupStatus,
  install as runInstall,
  testConnection,
  type InstallPayload,
  type SetupStatus,
} from '@/api/setup'

const props = defineProps<{ status: SetupStatus }>()

const engine = ref<'mysql' | 'sqlite'>('mysql')
const step = ref<1 | 2 | 3>(1)
const testing = ref(false)
const installing = ref(false)
const confirming = ref(false)
const alertMsg = ref('')
const alertType = ref<'success' | 'error'>('error')
const progressTip = ref('')
const showRecheck = ref(false)
const manualOpen = ref(false)
const nexting = ref(false)

// 安装进度：0=未开始，1..3 已完成到哪一步；'fail' 记失败
const done = ref(0)
const running = ref(0)
const failed = ref(0)

const form = reactive({
  dbHost: '127.0.0.1',
  dbPort: '3306',
  dbName: 'weichuan',
  dbUser: 'root',
  dbPassword: '',
  createDatabase: true,
  sqlitePath: '',
  adminUser: 'admin',
  adminPass: '',
  adminPass2: '',
  jwtKey: '',
  // 是否装载演示企业数据（默认不勾：商用版装完就该是一张白纸，由客户自己录真实内容）。
  // 勾了才会建「朗汇机电」样板企业 + 分类/案例/服务 + 27 张配套素材。
  loadDemoData: false,
  // 默认按访问协议判定：HTTPS（生产域名，如 宝塔反代后的 weichuan.ziyuanxingqiu.cn）
  // 下自动勾选「Cookie 强制 HTTPS」，省去手动勾、实现安装即零配置；
  // HTTP（本地开发 / 尚未配 TLS）下保持不勾，避免 Secure Cookie 在 http 下无法落盘导致登录失效。
  cookieSecure: window.location.protocol === 'https:',
})

const configFile = computed(() => props.status.configFile || './data/runtime.env')
const mysqlNote = computed(() => props.status.mysqlHint?.note || '')
// 顶部进度条百分比：随已完成步数推进；全部完成（含倒计时进入登录页）done=3 → 100%
const progressPct = computed(() => Math.round((done.value / 3) * 100))

// 预览与服务端掩码口径一致：只用于给运维确认「填进去的是什么」，密码一律打星
const dsnPreview = computed(() => {
  if (engine.value === 'mysql') {
    const pw = form.dbPassword ? '******' : ''
    return `${form.dbUser}:${pw}@tcp(${form.dbHost}:${form.dbPort || '3306'})/${form.dbName}?charset=utf8mb4&parseTime=true`
  }
  return `sqlite://${form.sqlitePath}`
})

// 密码强度：长度 / 字母+数字 / 长度≥12 / 含特殊字符，四档
const pwLevel = computed(() => {
  const p = form.adminPass
  if (!p) return 0
  let s = 0
  if (p.length >= 8) s++
  if (/[A-Za-z]/.test(p) && /\d/.test(p)) s++
  if (p.length >= 12) s++
  if (/[^A-Za-z0-9]/.test(p)) s++
  return Math.min(s, 4)
})
const pwText = computed(() => ['', '弱', '弱', '中', '强'][pwLevel.value] || '')

function stepClass(n: number): string {
  if (step.value === n) return 'active'
  if (n < step.value) return 'done'
  return ''
}

function payload(): InstallPayload {
  const base = {
    adminUser: form.adminUser.trim(),
    adminPass: form.adminPass,
    adminPass2: form.adminPass2,
    jwtKey: form.jwtKey.trim(),
    cookieSecure: form.cookieSecure,
    loadDemoData: form.loadDemoData,
  }
  if (engine.value === 'mysql') {
    return {
      ...base,
      engine: 'mysql',
      dbHost: form.dbHost.trim(),
      dbPort: form.dbPort.trim(),
      dbUser: form.dbUser.trim(),
      dbPassword: form.dbPassword,
      dbName: form.dbName.trim(),
      createDatabase: form.createDatabase,
    }
  }
  return { ...base, engine: 'sqlite', sqlitePath: form.sqlitePath.trim() }
}

function setAlert(type: 'success' | 'error', msg: string) {
  alertType.value = type
  alertMsg.value = msg
}
function clearAlert() {
  alertMsg.value = ''
}

// 步骤 1 预检：只校验数据库侧，不过早暴露管理员规则
function precheckStep1(): string {
  if (engine.value === 'mysql') {
    if (!form.dbHost.trim() || !form.dbUser.trim() || !form.dbName.trim()) {
      return '请填写完整的 MySQL 连接信息（主机、用户名、库名）'
    }
  } else if (!form.sqlitePath.trim()) {
    return '请填写 SQLite 数据文件路径'
  }
  return ''
}

// 全量预检：与服务端同规则，只为少一次往返；真正的判据在服务端
function precheck(): string {
  const e = precheckStep1()
  if (e) return e
  if (!form.adminUser.trim()) return '请填写管理员用户名'
  if (form.adminPass.length < 8) return '管理员密码至少 8 位'
  if (form.adminPass !== form.adminPass2) return '两次输入的管理员密码不一致'
  return ''
}

async function onTest() {
  clearAlert()
  const err = precheckStep1()
  if (err) {
    setAlert('error', err)
    return
  }
  testing.value = true
  try {
    const r = await testConnection(payload())
    setAlert('success', `连接正常（${r.driver}）${r.note ? '：' + r.note : ''}`)
  } catch (e: any) {
    setAlert('error', e?.message || '连接失败')
  } finally {
    testing.value = false
  }
}

async function onNext() {
  clearAlert()
  const err = precheckStep1()
  if (err) {
    setAlert('error', err)
    return
  }
  // 进入下一步前强制验证数据库连通性：避免带着连不上的配置走到安装才报错。
  // 后端安装时也会再测一次（双保险），这里给的是即时前端反馈。
  nexting.value = true
  try {
    await testConnection(payload())
    step.value = 2
  } catch (e: any) {
    setAlert('error', e?.message || '数据库连通性校验未通过，请检查连接信息')
  } finally {
    nexting.value = false
  }
}

function onInstall() {
  clearAlert()
  const err = precheck()
  if (err) {
    setAlert('error', err)
    return
  }
  confirming.value = true
}

async function doInstall() {
  clearAlert()
  installing.value = true
  try {
    const r = await runInstall(payload())
    confirming.value = false
    startProgress(r.restarted)
  } catch (e: any) {
    setAlert('error', e?.message || '安装失败')
  } finally {
    installing.value = false
  }
}

function pClass(n: number): string {
  if (failed.value === n) return 'fail'
  if (done.value >= n) return 'done'
  if (running.value === n) return 'run'
  return ''
}

function startProgress(restarted: boolean) {
  step.value = 3
  done.value = 1
  if (restarted) {
    running.value = 2
    progressTip.value = '服务正在重启，请稍候…'
    poll()
  } else {
    failed.value = 2
    progressTip.value = '服务未能自动重启（Windows 或容器托管环境），请手动重启服务后点击下方按钮重新检测。'
    showRecheck.value = true
  }
}

// 轮询直到后端说 configured=true。请求失败（重启瞬间连接被拒）属预期，继续等：
// 这里必须用会抛错的 fetchSetupStatus，用折叠成 null 的 getSetupStatus 会把
// 「重启中」误判成「装好了」。
function poll() {
  let tries = 0
  const timer = window.setInterval(async () => {
    tries++
    try {
      const s = await fetchSetupStatus()
      if (s.configured) {
        window.clearInterval(timer)
        done.value = 3
        countdown()
      }
    } catch {
      /* 重启瞬间连不上是正常的，继续轮询 */
    }
    if (tries >= 50) {
      window.clearInterval(timer)
      failed.value = 2
      progressTip.value = '等待服务重启超时，请手动刷新页面或重启服务。'
      showRecheck.value = true
    }
  }, 1200)
}

function countdown() {
  let n = 3
  // 装完就把下一步说清楚：零数据安装最大的困惑是「登进去什么都没有、也不知道去哪」。
  const next = form.loadDemoData
    ? '已装载演示企业数据，登录后可在「企业内容」里替换成自己的内容'
    : '系统是空白的，登录后请先到「企业管理」创建一家企业'
  progressTip.value = `安装完成——${next}。${n} 秒后进入登录页…`
  const t = window.setInterval(() => {
    n--
    if (n <= 0) {
      window.clearInterval(t)
      // 整页重载：main.ts 会重新查状态，这次 configured=true → 挂载正常应用
      window.location.href = '/'
      return
    }
    progressTip.value = `安装完成——${next}。${n} 秒后进入登录页…`
  }, 1000)
}

function recheck() {
  window.location.reload()
}

async function copyEnv() {
  const text = props.status.envText || ''
  if (!text) return
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      ElMessage.success('已复制，粘贴进 .env 即可')
    } else {
      ElMessage.info(text)
    }
  } catch {
    ElMessage.info(text)
  }
}

onMounted(() => {
  const s = props.status
  if (s.mysqlHint) {
    form.dbHost = s.mysqlHint.host || form.dbHost
    form.dbPort = s.mysqlHint.port || form.dbPort
    form.dbUser = s.mysqlHint.user || form.dbUser
    form.dbName = s.mysqlHint.database || form.dbName
  }
  if (s.sqlitePath) form.sqlitePath = s.sqlitePath
  if (s.adminUser) form.adminUser = s.adminUser
  // 编排环境预填了 MySQL 连接信息，说明 compose 里就带 MySQL，默认选中它
  if (s.mysqlHint) engine.value = 'mysql'
})
</script>

<style scoped>
.si-root {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  box-sizing: border-box;
  background: linear-gradient(160deg, #eef3f9 0%, #f7f9fc 100%);
}
.si-dialog {
  width: 100%;
  max-width: 680px;
  max-height: 100%;
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.04);
  border-radius: 16px;
  box-shadow: 0 24px 60px rgba(15, 23, 42, 0.12);
  overflow: hidden;
  color: #303133;
}
.si-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 24px;
  border-bottom: 1px solid #eef0f3;
}
.si-head-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 16px;
  font-weight: 600;
}
.si-logo {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: #fff;
  border: 1px solid #ebeef5;
  display: inline-block;
  object-fit: cover;
  flex-shrink: 0;
}
.si-head-tag {
  font-size: 12px;
  color: #909399;
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 999px;
  padding: 2px 10px;
}
.si-steps {
  list-style: none;
  display: flex;
  gap: 8px;
  margin: 0;
  padding: 12px 24px;
}
/* 弹窗内容滚动区：header 与步骤条常驻，仅此区域滚动 */
.si-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.si-steps li {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid #ebeef5;
  border-radius: 10px;
  color: #909399;
  font-size: 13px;
}
.si-steps li b {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #f0f2f5;
  color: #909399;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  flex: none;
}
.si-steps li.active {
  border-color: #409eff;
  color: #303133;
}
.si-steps li.active b {
  background: #409eff;
  color: #fff;
}
.si-steps li.done {
  border-color: #c2e7b0;
  color: #303133;
}
.si-steps li.done b {
  background: #67c23a;
  color: #fff;
}
.si-pane {
  padding: 4px 24px 8px;
}
.si-h2 {
  margin: 8px 0 4px;
  font-size: 16px;
  font-weight: 600;
}
.si-lead {
  margin: 0 0 12px;
  color: #606266;
  font-size: 13px;
  line-height: 1.7;
}
.si-engines {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 16px;
}
.si-engine {
  position: relative;
  border: 1px solid #ebeef5;
  border-radius: 12px;
  padding: 14px 16px;
  cursor: pointer;
  background: #fff;
  transition: border-color 0.15s, background 0.15s;
}
.si-engine:hover {
  border-color: #c6e2ff;
}
.si-engine b {
  display: block;
  font-size: 15px;
  margin-bottom: 6px;
  font-weight: 600;
}
.si-engine p {
  margin: 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
.si-engine.active {
  border-color: #409eff;
  background: #ecf5ff;
  box-shadow: 0 0 0 1px #409eff inset;
}
.si-engine-check {
  position: absolute;
  top: 10px;
  right: 10px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #409eff;
}
.si-engine-check::after {
  content: '';
  position: absolute;
  left: 6px;
  top: 3px;
  width: 4px;
  height: 8px;
  border: solid #fff;
  border-width: 0 2px 2px 0;
  transform: rotate(45deg);
}
.si-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px 16px;
  align-items: start;
}
.si-grid label:not(.el-checkbox) {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: #606266;
}
.si-grid .wide {
  grid-column: 1 / -1;
}
.si-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.si-strength {
  display: flex;
  align-items: center;
  gap: 6px;
}
.si-strength i {
  flex: 1;
  height: 4px;
  border-radius: 2px;
  background: #ebeef5;
  transition: background 0.15s;
}
.si-strength i.on[data-level='1'],
.si-strength i.on[data-level='2'] {
  background: #f56c6c;
}
.si-strength[data-level='3'] i.on {
  background: #409eff;
}
.si-strength[data-level='4'] i.on {
  background: #67c23a;
}
.si-strength-text {
  font-size: 12px;
  color: #909399;
  flex: none;
  min-width: 18px;
}
.si-chk {
  height: auto;
  font-size: 13px;
  color: #606266;
}
/* 高级选项标签：文字与悬浮徽标同行，输入框另起一行 */
.si-field-cap {
  display: inline-flex;
  align-items: center;
}
/* 悬浮说明徽标：常驻不占版面，指针停留/聚焦时由 el-tooltip 弹出说明 */
.si-hint {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 6px;
  border-radius: 50%;
  background: #c0c4cc;
  color: #fff;
  font-size: 10px;
  line-height: 1;
  cursor: help;
  user-select: none;
  transition: background 0.15s ease;
}
.si-hint:hover,
.si-hint:focus {
  background: #409eff;
  outline: none;
}
.si-dbrow {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
  margin-top: 18px;
}
.si-field-flat {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 220px;
}
.si-field-lbl {
  position: absolute;
  top: -18px;
  left: 0;
  font-size: 13px;
  color: #606266;
}
.si-dbnote {
  flex-basis: 100%;
  margin: 2px 0 0;
}
.si-tip {
  margin: 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.7;
  grid-column: 1 / -1;
}
.si-tip--hint {
  color: #e6a23c;
}
/* 初始化数据选项：浅底卡片，与上面的账号字段分开——它是「装完里面有什么」，
   不是账号信息的一部分，贴在一起容易被当成必填项。 */
.si-demo {
  margin-top: 16px;
  padding: 12px 14px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #fafcff;
}
.si-demo-desc {
  margin: 6px 0 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.7;
}
.si-demo-desc b {
  color: #606266;
}
.si-dsn {
  margin: 14px 0 0;
  font-size: 12px;
  color: #606266;
  word-break: break-all;
}
code {
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 12px;
}
.si-adv {
  margin-top: 16px;
  border: 1px solid #ebeef5;
  border-radius: 12px;
  padding: 0;
}
.si-adv summary {
  cursor: pointer;
  padding: 14px 20px;
  font-size: 14px;
  font-weight: 500;
  list-style: none;
}
.si-adv summary::before {
  content: '▸';
  margin-right: 8px;
  color: #909399;
}
.si-adv[open] summary::before {
  content: '▾';
}
.si-adv > *:not(summary) {
  margin-left: 20px;
  margin-right: 20px;
  margin-bottom: 14px;
}
.si-adv > summary {
  margin-bottom: 0;
}
.si-manual {
  margin: 16px 24px 0;
  padding-top: 14px;
  border-top: 1px dashed #ebeef5;
  text-align: center;
}
.si-manual-entry {
  font-size: 12px;
  color: #909399;
}
.si-manual-caret {
  display: inline-block;
  margin-left: 4px;
  transition: transform 0.2s ease;
}
.si-manual-caret.open {
  transform: rotate(180deg);
}
.si-manual-panel {
  margin-top: 12px;
  padding: 14px 16px;
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 12px;
  text-align: left;
}
.si-expand-enter-active,
.si-expand-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}
.si-expand-enter-from,
.si-expand-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
.si-pre {
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  padding: 14px 16px;
  overflow-x: auto;
}
.si-pre code {
  background: none;
  border: none;
  padding: 0;
  white-space: pre;
}
.si-alert {
  margin-top: 4px;
}
.si-actions {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
  margin-top: 14px;
}
.si-confirm {
  margin: 16px 24px;
  padding: 16px 18px;
  border: 1px solid #d9ecff;
  background: #f5f9ff;
  border-radius: 12px;
}
.si-confirm-title {
  margin: 0 0 10px;
  font-size: 14px;
  font-weight: 600;
}
.si-confirm-list {
  margin: 0 0 8px;
  padding-left: 20px;
  font-size: 13px;
  line-height: 1.9;
  color: #606266;
}
.si-confirm-list b {
  color: #303133;
}
.si-progress-bar {
  height: 6px;
  background: #ebeef5;
  border-radius: 3px;
  overflow: hidden;
  margin: 4px 0 18px;
}
.si-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, #409eff 0%, #67c23a 100%);
  border-radius: 3px;
  transition: width 0.5s ease;
}
.si-psteps {
  list-style: none;
  margin: 0;
  padding: 0;
}
.si-psteps li {
  display: flex;
  align-items: center;
  padding: 12px;
  font-size: 14px;
  color: #909399;
  border-radius: 8px;
  transition: background 0.3s ease, color 0.3s ease;
}
.si-psteps li + li {
  margin-top: 6px;
}
.si-psteps li.run {
  color: #303133;
  background: #ecf5ff;
}
.si-psteps li.done {
  color: #303133;
  background: #f0f9eb;
}
.si-psteps li.fail {
  color: #303133;
  background: #fef0f0;
}
.si-psteps i {
  display: inline-block;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 2px solid #dcdfe6;
  margin-right: 10px;
  flex: none;
  position: relative;
  box-sizing: border-box;
}
.si-psteps li.done i {
  background: #67c23a;
  border-color: #67c23a;
}
.si-psteps li.done i::after {
  content: '';
  position: absolute;
  left: 4px;
  top: 1px;
  width: 4px;
  height: 8px;
  border: solid #fff;
  border-width: 0 2px 2px 0;
  transform: rotate(45deg);
  animation: si-check 0.35s ease both;
}
@keyframes si-check {
  from {
    transform: rotate(45deg) scale(0.4);
    opacity: 0;
  }
  to {
    transform: rotate(45deg) scale(1);
    opacity: 1;
  }
}
.si-psteps li.run i {
  border-color: #409eff;
  border-top-color: transparent;
  animation: si-spin 0.8s linear infinite;
}
.si-psteps li.fail i {
  background: #f56c6c;
  border-color: #f56c6c;
}
@keyframes si-spin {
  to {
    transform: rotate(360deg);
  }
}
.si-note {
  margin: 12px 24px 16px;
  font-size: 12px;
  color: #909399;
  line-height: 1.8;
}
@media (max-width: 767px) {
  .si-root {
    padding: 16px 12px;
  }
  .si-dialog {
    border-radius: 14px;
  }
  .si-steps {
    flex-direction: column;
  }
  .si-engines,
  .si-grid {
    grid-template-columns: 1fr;
  }
  .si-actions {
    flex-direction: column;
  }
}
</style>

<!-- 说明弹层样式：el-tooltip 的 popper 挂在 body 下，scoped 样式够不到，必须用非 scoped 块。
     限宽让长说明折行成短段落，而不是横穿全屏的一长行。 -->
<style>
.si-hint-popper {
  max-width: 300px;
  text-align: left;
  line-height: 1.7;
}
.si-hint-popper .si-hint-line {
  margin: 0;
  font-size: 12px;
}
.si-hint-popper .si-hint-line + .si-hint-line {
  margin-top: 6px;
}
</style>
