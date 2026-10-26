<template>
  <el-card class="settings-shell" :style="{ '--toolbar-h': toolbarH + 'px' }">
    <!-- 吸顶头：标题 + 搜索 + 操作按钮 + 模块锚点整块常驻。搜索框放标题栏
         （配置向导左侧）是常规后台布局；整块吸顶后，搜不搜索、滚不滚动，
         标题与操作都可见，行为一致 -->
    <div ref="toolbarRef" class="sticky-head" :class="{ pinned }">
      <div class="page-bar">
        <h3>系统设置</h3>
        <div class="bar-actions">
          <el-input
            v-model="keyword"
            clearable
            placeholder="搜索配置项：中文名 / key / 环境变量名"
            class="head-search"
          />
          <!-- 配置向导：给第一次部署的人一条明确顺序（品牌→存储→上传→微信→小程序→确认），
               不必在 35 个输入框里自己猜该从哪开始、按什么顺序配 -->
          <el-button :disabled="loading" @click="openWizard">配置向导</el-button>
          <el-button :disabled="!dirty || saving" @click="reset">重置</el-button>
          <el-button type="primary" :loading="saving" :disabled="!dirty" @click="save">
            保存{{ dirty ? `（${changedCount} 项）` : '' }}
          </el-button>
        </div>
      </div>
      <div v-if="!loading" class="module-nav">
        <span v-if="keyword.trim()" class="filter-count">匹配 {{ matchCount }} / {{ items.length }} 项</span>
        <button
          v-for="g in filteredGroups"
          :key="g.key"
          type="button"
          class="module-pill"
          :class="{ active: g.key === activeKey }"
          @click="jumpToGroup(g.key)"
        >{{ g.label }}</button>
      </div>
    </div>

    <!-- 搜索态收起说明与就绪面板：聚焦结果。否则关键词命中少时内容比面板还短，
         无滚动可吸，工具条被面板「拉」到页面中部，看起来像吸顶失效 -->
    <!-- 页面说明条：与审计日志 / 账号管理等页保持同一形态（info + 图标 + 标题 + 说明，
         下间距 14px）——同类提示长得不一样会让人怀疑是不是进了另一个产品 -->
    <el-alert
      v-if="!keyword.trim()"
      type="info"
      :closable="false"
      show-icon
      title="在此维护产品运行参数。常规项保存后即时生效；标注「需重启」的项要重启服务后才生效；标注「只读」的项只能改环境变量或配置文件（默认 data/runtime.env）后重启。"
      description="取值优先级：本页保存的值最优先；某项留空时，凡是标注了「部署环境配置名」的会依次取环境变量、配置文件（默认 data/runtime.env），都没有才用内置缺省值，没标注的则直接用内置缺省值。可修改的敏感项（密钥 / 连接串）不回传真实值，界面只显示「已配置」，输入框留空表示不修改、填新值即覆盖；只读项界面不能改，会显示可辨认的预览（保留首尾、隐去中段）供你核对。带范围 / 格式约束的项（上传上限、接口基址、存储方式、锁定企业）保存时会校验，填错当场拦住并说明原因——与其让你保存后排查半天，不如改的时候就告诉你。"
      style="margin-bottom: 14px"
    />

    <!-- 待重启提示：保存含「需重启」项后常驻显示。
         只靠 toast 提示是不够的——它几秒就消失，人一走神就错过，
         接下来就是「明明改了为什么不生效」的排查。这里常驻到用户手动关掉，
         并直接给出重启命令（不同部署方式各一条），把「知道要重启」和
         「知道怎么重启」一次给全。 -->
    <el-alert
      v-if="restartPending.length && !restartDismissed"
      type="warning"
      show-icon
      closable
      title="刚保存的改动里有需要重启才生效的项"
      class="restart-alert"
      @close="restartDismissed = true"
    >
      <div class="restart-body">
        <div class="restart-keys">待生效：{{ restartPending.join('、') }}</div>
        <div class="restart-cmd">
          按你的部署方式重启服务，任选其一：
          <code>docker compose restart backend</code>
          <code>systemctl restart weichuan</code>
          <code>kill -HUP &lt;进程号&gt;</code>
        </div>
      </div>
    </el-alert>

    <!-- 上线就绪检查：默认收起成一条状态栏，点「查看详情」再展开清单。
         本页主任务是改配置，整份清单常驻会把它推到屏幕外；但完全藏起来又丢掉
         「还差几项」这个一眼可见的信号——所以留一行摘要：未就绪时给出待处理数与归属，
         全就绪时只留一句「已全部就绪」，不再占用注意力。
         清单按存储方式驱动感知（local 6 项 / 对象存储 7 项，见 checks 注释）。
         清单内仍分两类，避免误导：
         - 本页可配置：能点「去配置」直接跳到对应行改；
         - 部署环境配置（环境变量 / 配置文件）：只读项，界面改不了，直接给出要设的
           写法（可复制）+ 重启提示。注意多数部署是安装向导装的，DB_DSN / JWT_KEY
           落在配置文件 data/runtime.env 里而不是环境变量，只能改环境变量的说法会指错路。
           （旧版只读项也跳「查看」，落到一个改不了的灰行，等于给了一条死路） -->
    <template v-if="!keyword.trim() && !loading && checks.length">
      <div class="ready-bar" :class="{ ok: readyCount === checks.length }">
        <span class="ready-bar-title">上线就绪检查</span>
        <span class="ready-bar-count">{{ readyCount }} / {{ checks.length }} 项就绪</span>
        <span v-if="readyCount < checks.length" class="ready-bar-pending">
          还有 {{ checks.length - readyCount }} 项待处理<template v-if="uiPending">：本页可配 {{ uiPending }} 项</template><template v-if="envPending">、环境变量 / 配置文件 {{ envPending }} 项</template>
        </span>
        <span v-else class="ready-bar-done">上线必配项已全部就绪</span>
        <el-button size="small" text type="primary" class="ready-bar-toggle" @click="toggleReady">
          {{ readyOpen ? '收起' : '查看详情' }}
        </el-button>
      </div>
      <el-card v-if="readyOpen" shadow="never" class="ready-card">

      <template v-if="uiChecks.length">
        <div class="ready-section-title">
          本页可配置
          <span class="ready-section-count">{{ uiReadyCount }} / {{ uiChecks.length }}</span>
        </div>
        <div v-for="c in uiChecks" :key="c.label" class="ready-row">
          <span class="ready-badge" :class="c.ok ? 'ok' : 'warn'">{{ c.ok ? '✓' : '!' }}</span>
          <div class="ready-main">
            <div class="ready-label">{{ c.label }}</div>
            <div class="ready-desc">{{ c.ok ? '已就绪' : c.hint || '待配置' }}</div>
          </div>
          <el-button v-if="!c.ok" size="small" text type="primary" @click="jumpTo(c.keys[0])">
            去配置
          </el-button>
        </div>
      </template>

      <template v-if="envChecks.length">
        <div class="ready-section-title">
          环境变量 / 配置文件
          <span class="ready-section-count">{{ envReadyCount }} / {{ envChecks.length }}</span>
        </div>
        <div v-for="c in envChecks" :key="c.label" class="ready-row">
          <span class="ready-badge" :class="c.ok ? 'ok' : 'warn'">{{ c.ok ? '✓' : '!' }}</span>
          <div class="ready-main">
            <div class="ready-label">{{ c.label }}</div>
            <div class="ready-desc">{{ c.ok ? '已就绪' : c.hint || '待配置' }}</div>
            <div v-if="!c.ok && c.envSample" class="ready-env">
              <code class="ready-env-code">{{ c.envSample }}</code>
              <el-button size="small" text type="primary" @click="copyEnv(c)">复制</el-button>
              <span class="ready-env-tip">设为环境变量，或写进配置文件 data/runtime.env，重启后生效</span>
            </div>
          </div>
        </div>
      </template>

      <!-- 底部入口：向导只覆盖「本页可配置」项，所以只在还有本页项时才给入口 -->
      <div class="ready-foot">
        <span v-if="readyCount < checks.length">
          还有 {{ checks.length - readyCount }} 项上线前要处理：<template v-if="uiPending">本页可配 {{ uiPending }} 项（点「去配置」直接改）</template><template
            v-if="uiPending && envPending"
          >，</template><template
            v-if="envPending"
          >环境变量 / 配置文件 {{ envPending }} 项（需在服务器 / 容器的环境变量或 data/runtime.env 里设置后重启）</template>。
        </span>
        <span v-else>上线必配项已全部就绪。</span>
        <el-button v-if="uiPending > 0" size="small" type="primary" text @click="openWizard">
          开始配置向导
        </el-button>
      </div>
      </el-card>
    </template>

    <div v-if="loading" class="loading">加载中…</div>

    <template v-else>
      <!-- 搜索过滤：35 个配置项平铺时「找一个项」本身就是成本。
           中文名 / key / 环境变量都能搜；分组整体命中（如搜「存储」出整组），
           未命中分组整组隐藏。草稿按 key 持有，过滤掉的行不会丢改动。 -->
      <div v-if="keyword.trim() && matchCount === 0" class="filter-empty">
        没有匹配「{{ keyword.trim() }}」的配置项——换个关键词，或清空搜索查看全部。
      </div>

      <el-card v-for="g in displayGroups" :key="g.key" shadow="never" class="group-card" :id="'grp-' + g.key">
        <template #header>
          <div class="group-header">
            <div class="group-title-wrap">
              <span class="group-accent" />
              <span class="group-title">{{ g.label }}</span>
            </div>
            <span class="group-count">{{ g.items.length }} 项</span>
          </div>
          <!-- 分组导语：一句话说清「这组管什么、什么时候该动」，
               商业产品的配置页不能只甩一堆输入框给运营 -->
          <div v-if="g.blurb" class="group-blurb">{{ g.blurb }}</div>
        </template>

        <!-- 单行渲染抽成 SettingRow 组件：主列表与配置向导共用同一形态，
             draft 仍由本页持有（:model-value + @change），避免两处各存一份值。
             组内分区与配置向导 steps 的 groups 同结构：项多的组按主题分堆，
             小标题 + 尾部细线；无分区的组平铺 -->
        <template v-for="sec in sectionsOf(g.items)" :key="sec.label">
          <div class="group-sub">{{ sec.label }}</div>
          <SettingRow
            v-for="item in sec.items"
            :key="item.key"
            :id="'set-' + item.key"
            :item="item"
            :model-value="draft[item.key]"
            :focus="depState(item.key).focus"
            :dim="depState(item.key).dim"
            @change="setVal"
            @clear="clearSecret"
          />
        </template>
        <SettingRow
          v-for="item in ungroupedOf(g.items)"
          :key="item.key"
          :id="'set-' + item.key"
          :item="item"
          :model-value="draft[item.key]"
          :focus="depState(item.key).focus"
          :dim="depState(item.key).dim"
          @change="setVal"
          @clear="clearSecret"
        />
      </el-card>
    </template>

    <!-- 配置向导：按依赖顺序分步，每步只显示当前相关的项（复用 depState 过滤），
         默认落在「第一个有未配置项的步骤」；草稿与主列表共享，「稍后再说」可原样恢复 -->
    <el-dialog
      v-model="wizardOpen"
      title="配置向导"
      width="720px"
      class="wizard-dialog"
      :close-on-click-modal="false"
    >
      <!-- 步骤条：自绘 chips 而非 el-steps——要的是「可点击回跳 + 每步待配置数」，
           el-steps 既不 emit 点击、标题也带不上计数。草稿全程共享、步骤间无校验闸，
           所以允许自由跳转（含未完成步），与「安全确认步的去配置按钮」行为一致。 -->
      <div class="wizard-stepbar">
        <button
          v-for="(s, i) in WIZARD_STEPS"
          :key="s.key"
          type="button"
          class="wizard-stepchip"
          :class="{ cur: i === wizardStep, done: stepStatus(i) === 'success' }"
          :aria-current="i === wizardStep ? 'step' : undefined"
          @click="wizardStep = i"
        >
          <span class="wizard-stepchip-no">{{ stepStatus(i) === 'success' ? '✓' : i + 1 }}</span>
          <span class="wizard-stepchip-title">{{ s.short || s.title }}</span>
          <span v-if="stepPending(i) > 0" class="wizard-stepchip-count">{{ stepPending(i) }}</span>
        </button>
      </div>
      <!-- 步骤名在标题里也重复一遍：步骤条为了等宽会把长标题折成两行，
           这里是「当前到底在第几步、这步叫什么」的唯一不折行出处 -->
      <div class="wizard-progress">
        第 {{ wizardStep + 1 }} 步 / 共 {{ WIZARD_STEPS.length }} 步 ·
        {{ currentStep.title }}<span
          v-if="stepPending(wizardStep) > 0"
          class="wizard-progress-pending"
          >本步 {{ stepPending(wizardStep) }} 项待配置</span
        >
      </div>
      <div class="wizard-blurb">{{ currentStep.blurb }}</div>
      <!-- 快速开始预设：只出现在第 1 步。「这是什么部署」是开始配之前要先回答的
           问题——选好模式，存储与上传限制落到推荐值，后面每一步仍可改。
           放在第 1 步而不是存储步：先定调子再看明细，符合「从粗到细」的认知顺序。 -->
      <div v-if="currentStep.key === 'brand'" class="wizard-preset">
        <span class="wizard-preset-label">快速开始：</span>
        <el-button
          size="small"
          :type="activePreset === 'demo' ? 'primary' : 'default'"
          @click="applyPreset('demo')"
        >
          演示模式
        </el-button>
        <el-button
          size="small"
          :type="activePreset === 'prod' ? 'primary' : 'default'"
          @click="applyPreset('prod')"
        >
          生产模式
        </el-button>
        <el-button v-if="presetNote" size="small" text type="primary" @click="wizardJumpToKey('storage_driver')">
          去「存储与上传」步
        </el-button>
        <span v-if="presetNote" class="wizard-preset-note">{{ presetNote }}</span>
      </div>
      <div class="wizard-body">
        <template v-for="g in currentGroups" :key="g.label">
          <div class="wizard-group-title">{{ g.label }}</div>
          <SettingRow
            v-for="item in g.items"
            :key="item.key"
            :item="item"
            :model-value="draft[item.key]"
            :focus="depState(item.key).focus"
            :dim="depState(item.key).dim"
            :error="currentStepErrors[item.key]"
            @change="setVal"
            @clear="clearSecret"
          />
        </template>
        <SettingRow
          v-for="item in currentUngrouped"
          :key="item.key"
          :item="item"
          :model-value="draft[item.key]"
          :focus="depState(item.key).focus"
          :dim="depState(item.key).dim"
          :error="currentStepErrors[item.key]"
          @change="setVal"
          @clear="clearSecret"
        />
      </div>
      <!-- 安全确认步：复用主页那套就绪清单（同一份 checks 数据），而不是几行文字。
           「本页可配」未就绪项给「去配置」= 跳回拥有该配置项的步骤（向导内闭环）；
           只读项给可复制的环境变量写法 + 说明，避免「看到问题却找不到解法」。
           只读项本来就不进向导步骤，放这里是让最后一步成为完整的收尾核对。 -->
      <div v-if="currentStep.key === 'final'" class="wizard-final">
        <div class="wizard-final-head">
          <span class="wizard-final-title">上线就绪</span>
          <span class="wizard-final-count" :class="{ ok: readyCount === checks.length }">
            {{ readyCount }} / {{ checks.length }}
          </span>
        </div>
        <template v-if="uiChecks.length">
          <div class="wizard-final-sec">本页可配置</div>
          <div v-for="c in uiChecks" :key="c.label" class="wizard-final-row">
            <span class="ready-badge" :class="c.ok ? 'ok' : 'warn'">{{ c.ok ? '✓' : '!' }}</span>
            <span class="wizard-final-label">{{ c.label }}</span>
            <span class="wizard-final-desc">{{ c.ok ? '已就绪' : c.hint || '待配置' }}</span>
            <el-button v-if="!c.ok" size="small" text type="primary" @click="wizardJumpToKey(c.keys[0])">
              去配置
            </el-button>
          </div>
        </template>
        <template v-if="envChecks.length">
          <div class="wizard-final-sec">只读项 · 到部署环境改（界面不开放）</div>
          <div v-for="c in envChecks" :key="c.label" class="wizard-final-row">
            <span class="ready-badge" :class="c.ok ? 'ok' : 'warn'">{{ c.ok ? '✓' : '!' }}</span>
            <span class="wizard-final-label">{{ c.label }}</span>
            <span class="wizard-final-desc">{{ c.ok ? '已就绪' : c.hint || '待配置' }}</span>
            <span v-if="!c.ok && c.envSample" class="wizard-final-env-wrap">
              <code class="wizard-final-env">{{ c.envSample }}</code>
              <el-button size="small" text type="primary" @click="copyEnv(c)">复制</el-button>
            </span>
          </div>
        </template>
        <p v-if="wizardRestartKeys.length" class="wizard-final-line">
          本次改动中 {{ wizardRestartKeys.length }} 项需重启服务后生效：{{ wizardRestartKeys.join('、') }}
        </p>
        <p class="wizard-final-line">保存后请按部署方式重启进程 / 容器；重启前新存储配置不生效，属预期行为。</p>
        <!-- 向导只走「首配最小路径」：有缺省值或可选增强的项（登录限流 / 令牌有效期 /
             CDN 刷新 / 功能开关等）不占步骤，但要说清它们去哪了——否则用户会以为
             系统里只有这几项，该调的限流永远找不到 -->
        <p class="wizard-final-line">
          未进向导的项（登录限流 / 令牌有效期 / CDN 刷新 / 功能开关等）均有可用缺省值，可在本页对应分组按需调整。
        </p>
      </div>
      <template #footer>
        <div class="wizard-foot">
          <el-button text @click="wizardCancel">稍后再说</el-button>
          <div class="wizard-foot-right">
            <el-button v-if="wizardStep > 0" @click="prevStep">上一步</el-button>
            <el-button v-if="!isLastStep" type="primary" @click="nextStep">下一步</el-button>
            <el-button v-else type="primary" :loading="saving" @click="wizardFinish">
              {{ dirty ? '保存并完成' : '完成' }}
            </el-button>
          </div>
        </div>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage, ElMessageBox } from 'element-plus'
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { getSettings, updateSettings, type SettingItem } from '@/api/settings'
import { useSettingsStore } from '@/store/settings'
import SettingRow from '@/components/SettingRow.vue'
import { clearWizardDraft, loadWizardDraft, saveWizardDraft } from '@/utils/wizardDraft'
import { CDN_DEFAULT_PROVIDER, CDN_RESOURCE_PROVIDERS } from '@/utils/cdn'

// 分组展示顺序与中文名；与后端 setting.registry 的 group 字段对应。
// 未登记的分组兜底显示在最后，避免后端新增分组后前端丢项。
//
// 排列逻辑（依赖链，从「产品是什么」到「怎么运营」）：
//   通用 → 文件存储 → 上传 → 小程序 → 微信 → 后台上传小程序代码 → 登录与令牌 → 安全与基础设施 → 功能开关
// mpupload 紧挨 wechat：同一条小程序业务线（后台直接传代码也要用小程序凭据 + 服务器环境），
// 与微信组相邻配置不用跳组。
// 存储必须排在上传**之前**：上传限的是「文件多大」，可文件存哪还没定就是倒挂；
// 小程序与微信相邻：同一条小程序业务线（基址 + 凭据 + 小程序码缓存），
// 旧顺序里被拆到第 2 和第 4 位，配一次要在两个组之间跳。
const GROUP_LABELS: Record<string, string> = {
  general: '通用',
  miniprogram: '小程序',
  upload: '上传',
  wechat: '微信',
  mpupload: '后台上传小程序代码',
  auth: '登录与令牌',
  storage: '文件存储与 CDN',
  security: '安全与基础设施（只读）',
  feature: '功能开关',
}
const GROUP_ORDER = ['general', 'storage', 'upload', 'miniprogram', 'wechat', 'mpupload', 'auth', 'security', 'feature']

// 分组导语：每组一句话「管什么、什么时候该动」，降低首配心智负担。
const GROUP_BLURBS: Record<string, string> = {
  general: '产品的基础展示信息，随时可改、即时生效。',
  storage:
    '图片 / 视频 / 小程序码存哪、怎么访问，是上传功能的前提。⚠️ 本组任一项改动都需重启服务；更换存储方式前请确认历史文件仍可访问。切换「存储方式」后只显示当前方式相关的项，另一种方式已填的值不会丢、切回来还在；想查看全部项可用顶部搜索。',
  upload:
    '文件存哪定了，再限制单文件大小、类型与临时链接时效。默认值已可用：「上传链接有效期」管直传预签名（对象存储下公私桶都会走），「私有桶下载链接有效期」只在公开访问关掉时才涉及。',
  miniprogram: '小程序端如何找到后端、是否锁定为单一企业版。首次部署必配「小程序接口基址」。',
  wechat:
    '微信凭据（小程序码与微信登录用）、小程序码的缓存时长，以及消息推送（图片内容安全靠它把结论推回来，不配则图片不送检）。',
  mpupload: '后台直接把小程序代码传到微信时需要的服务器环境：Node 可执行文件、miniprogram-ci 安装目录、小程序源码目录、上传超时。纯 CI 上传（代码由 CI 传、后台只登记版本号）用不到，可以不管；要走「后台上传代码」这条路才需要配。各项留空都会回落环境变量或默认值。',
  auth: '后台登录凭证能用多久、防爆破的尝试次数。',
  security: '数据库连接、签名密钥等关键项，界面不能改，只展示可辨认的预览（隐去口令与密钥中段），必须到部署环境改环境变量或配置文件（默认 data/runtime.env）后重启——改错会把自己锁在系统外。',
  feature: '运营期功能开关，按需开启。',
}

// 组内顺序：呈现顺序由前端显式掌控，不随后端 registry 的声明顺序漂移
// （registry 按「新增时间」堆叠，前端按「配置时的逻辑顺序」排）。
// 未登记的组保持后端返回顺序——那些组（通用 / 小程序 / 令牌 / 只读 / 开关）
// 的声明顺序本就合理，逐个写死只会多一处要同步的地方。
const GROUP_ITEM_ORDER: Record<string, string[]> = {
  storage: [
    'storage_driver',
    // local 分支
    'upload_dir',
    // 云存储分支（minio / oss / s3）
    'storage_endpoint',
    'storage_bucket',
    'storage_access_key',
    'storage_secret_key',
    'storage_use_ssl',
    // S3 兼容存储专属：部分云（COS / OBS 等）需显式区域，留空自动探测
    'storage_region',
    // 访问方式：公私总闸 + 对外基址（两种方式都生效）
    'storage_public',
    'upload_base_url',
    // CDN 刷新：可选增强，不开则整区用不上；厂商决定下面哪些凭据要填
    'cdn_enabled',
    'cdn_provider',
    'cdn_access_key_id',
    'cdn_access_key_secret',
    // 资源标识：只有 Cloudflare（Zone ID）与 CloudFront（Distribution ID）需要
    'cdn_resource_id',
    'cdn_endpoint',
  ],
  upload: ['upload_max_mb', 'upload_video_max_mb', 'upload_allow_ext', 'presign_put_ttl_minutes', 'sign_get_ttl_minutes'],
  wechat: [
    'wx_appid',
    'wx_appsecret',
    'wxacode_ttl_hours',
    'wxacode_sign_ttl_minutes',
    'wxacode_rate_limit_per_min',
    // 消息推送：图片内容安全的结论靠它推回来（不配则图片不送检）
    'wx_msg_token',
    'wx_msg_aeskey',
  ],
  mpupload: ['mp_node_bin', 'mp_ci_module_dir', 'mp_source_dir', 'mp_upload_timeout_sec'],
}

// 组内分区：项多的组按「在配什么」再分堆，小标题让人不用逐行读描述。
// 与配置向导 steps 的 groups 元数据是同一套结构——主列表与向导结构一致，
// 用户在一边看懂的分区，另一边直接认出来。
const GROUP_SECTIONS: Record<string, { label: string; keys: string[] }[]> = {
  storage: [
    // local 与云存储两套互斥配置在这一区里按 depState 二选一出现
    { label: '存储位置', keys: ['storage_driver', 'upload_dir', 'storage_endpoint', 'storage_bucket', 'storage_access_key', 'storage_secret_key', 'storage_use_ssl', 'storage_region'] },
    { label: '访问方式', keys: ['storage_public', 'upload_base_url'] },
    { label: 'CDN 刷新（可选）', keys: ['cdn_enabled', 'cdn_provider', 'cdn_access_key_id', 'cdn_access_key_secret', 'cdn_resource_id', 'cdn_endpoint'] },
  ],
  upload: [
    { label: '大小与类型', keys: ['upload_max_mb', 'upload_video_max_mb', 'upload_allow_ext'] },
    { label: '临时链接有效期', keys: ['presign_put_ttl_minutes', 'sign_get_ttl_minutes'] },
  ],
  wechat: [
    { label: '小程序凭据', keys: ['wx_appid', 'wx_appsecret'] },
    { label: '小程序码', keys: ['wxacode_ttl_hours', 'wxacode_sign_ttl_minutes', 'wxacode_rate_limit_per_min'] },
    // 消息推送只服务图片内容安全：图片检测是异步的，结论靠推送回来。
    // 单独一区，避免和「小程序码」混在一起被当成可选项跳过。
    { label: '消息推送（图片内容安全）', keys: ['wx_msg_token', 'wx_msg_aeskey'] },
  ],
  mpupload: [
    { label: '上传环境（仅后台直接传代码时需要）', keys: ['mp_node_bin', 'mp_ci_module_dir', 'mp_source_dir', 'mp_upload_timeout_sec'] },
  ],
}

const settings = useSettingsStore()

const items = ref<SettingItem[]>([])
const original = reactive<Record<string, string>>({})
const draft = reactive<Record<string, string>>({})
// 敏感项点「清除」= 显式提交空串（回退环境变量）。它与「没动过」不是一回事，
// 因为两者在输入框里都是空值，必须单独记一笔才能正确生成提交载荷。
const cleared = reactive<Record<string, boolean>>({})
// 草稿改动即暂存到 localStorage：刷新 / 误关标签页后回来接着改，不用从头填。
// 主列表与配置向导共用 draft，两边都受益；保存成功（save）与登出（auth store）会清掉。
// 只暂存「未保存的中间态」——保存成功 / 重置后草稿回到干净态，此时必须清而不是
// 再写一遍（否则下次进页面会恢复出一份「已保存过」的旧改动，白让人以为有未存修改）。
watch(
  [draft, cleared],
  () => {
    if (changedKeys.value.length === 0) {
      clearWizardDraft()
      return
    }
    saveWizardDraft(draft, cleared)
  },
  { deep: true },
)
const loading = ref(true)
const saving = ref(false)
// 保存后「需重启才生效」的项（中文名）：常驻提示到用户手动关掉。
// toast 几秒就消失，错过就会变成「改了为什么不生效」的排查。
const restartPending = ref<string[]>([])
const restartDismissed = ref(false)

// 按分组聚合（已按后端返回顺序 stable，这里再套一层分组顺序 + 组内逻辑顺序）
const groups = computed(() => {
  const map = new Map<string, SettingItem[]>()
  for (const it of items.value) {
    if (!map.has(it.group)) map.set(it.group, [])
    map.get(it.group)!.push(it)
  }
  const keys = [...map.keys()].sort((a, b) => {
    const ia = GROUP_ORDER.indexOf(a)
    const ib = GROUP_ORDER.indexOf(b)
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
  })
  return keys.map((k) => {
    const list = map.get(k)!
    const order = GROUP_ITEM_ORDER[k]
    // 未登记的 key 排到最后：后端新增项不该被隐藏到已登记项前面，
    // 也不能让它打乱已排好的逻辑顺序
    const rank = (key: string) => {
      const i = order.indexOf(key)
      return i < 0 ? order.length : i
    }
    return {
      key: k,
      label: GROUP_LABELS[k] || k,
      blurb: GROUP_BLURBS[k] || '',
      items: order ? [...list].sort((a, b) => rank(a.key) - rank(b.key)) : list,
    }
  })
})

// 组内分区（输入 items 可以是过滤前 / 后的，分区按实际传入的项计算）
function sectionsOf(items: SettingItem[]): { label: string; items: SettingItem[] }[] {
  const secs = GROUP_SECTIONS[items[0]?.group]
  if (!secs) return []
  return secs
    .map((s) => ({
      label: s.label,
      items: s.keys
        .map((k) => items.find((i) => i.key === k))
        .filter((it): it is SettingItem => !!it),
    }))
    .filter((s) => s.items.length > 0)
}
// 未归入分区的项（无分区元数据的组 / 元数据没覆盖的 key）：平铺在分区之后，不丢项
function ungroupedOf(items: SettingItem[]): SettingItem[] {
  const secs = GROUP_SECTIONS[items[0]?.group]
  if (!secs) return items
  const covered = new Set(secs.flatMap((s) => s.keys))
  return items.filter((it) => !covered.has(it.key))
}

// 非搜索态：按依赖联动过滤——当前存储方式 / 未填 AppID 下用不上的项不占版面
// （与配置向导 stepVisible 同一套 depState 规则，两边行为一致：向导里切 minio
// 后 upload_dir 会消失，主列表以前只是置灰，现在一致地「只显示相关的」）。
// 注意只过滤「呈现」：草稿按 key 全量持有，被过滤掉的项保存时照样进载荷，
// 切回另一种方式时值还在——过滤是视图，不是删除。
const visibleGroups = computed(() =>
  groups.value.map((g) => ({ ...g, items: g.items.filter((it) => !depState(it.key).dim) })),
)

// ---- 上线就绪检查 ----
// 把「上线前必须配好的项」从全量里挑出来集中呈现（规则与 scripts/preflight.sh 对齐）。
// 每项默认以「相关 key 均已 configured」为就绪标准；两处例外按「后端实际读取路径」
// 驱动感知（2026-10-22 全量核查：本地驱动下要求云存储专属项 = 永远就绪不了的假警报）：
//   - cookie_secure：只读开关，就绪标准是值为 true/1（生产 HTTPS）；
//   - 文件访问基址：仅对象存储参与 URL 生成（minio/oss/s3 用 BaseURL 拼绝对地址），
//     local 返回相对地址、端上自行拼基址——本地部署根本不看这项，不进清单；
//   - 存储方式与凭据：云驱动时端点 / 桶 / 凭据缺一项上传就全坏（minio 空端点甚至
//     构造客户端不出错，拖到运行时才炸），就绪判定要把它们一并纳入。
interface ReadyCheck {
  label: string
  keys: string[]
  hint?: string
  ok: boolean
  readOnly: boolean
  /** 只读项对应的环境变量名（派生自后端 registry，前端不写死） */
  envVar?: string
  /** 只读项的环境变量示例：界面改不了，必须把这条交给部署的人去设 */
  envSample?: string
}
// 云驱动才检查的存储凭据（minio / oss / s3 共用同一组界面字段）
const CLOUD_CREDENTIALS = ['storage_endpoint', 'storage_bucket', 'storage_access_key', 'storage_secret_key']
const checks = computed<ReadyCheck[]>(() => {
  const find = (k: string) => items.value.find((i) => i.key === k)
  const cloud = driverVal.value !== 'local'
  const defs: Array<Omit<ReadyCheck, 'ok' | 'readOnly' | 'envVar'>> = [
    {
      label: '登录签名密钥',
      keys: ['jwt_key'],
      hint: '未配置 = 仍用内置缺省值，上线前必须改',
      envSample: 'JWT_KEY=<至少 32 位随机字符串>',
    },
    {
      label: '数据库连接串',
      keys: ['db_dsn'],
      hint: '在部署环境配置后重启',
      envSample: 'DB_DSN=用户名:密码@tcp(主机:3306)/库名?parseTime=true',
    },
    {
      label: '登录 Cookie 强制 HTTPS',
      keys: ['cookie_secure'],
      hint: '生产 HTTPS 环境应为「开」',
      envSample: 'COOKIE_SECURE=true',
    },
    {
      label: '存储方式与凭据',
      keys: ['storage_driver'],
      hint: cloud
        ? '生产建议 minio / oss / s3（S3 兼容云），并填好端点 / 存储桶 / AccessKey / SecretKey'
        : '演示可用 local；生产建议 minio / oss / s3',
    },
    // local 驱动不经过这项（见上方注释），进清单就是永远划不掉的假警报
    ...(cloud
      ? [
          {
            label: '文件访问基址',
            keys: ['upload_base_url'],
            hint: '图片 / 视频的公开访问地址，须为 http:// 或 https:// 完整地址',
          },
        ]
      : []),
    { label: '微信 AppID / AppSecret', keys: ['wx_appid', 'wx_appsecret'], hint: '小程序码与微信登录依赖，未配则相关功能不可用' },
    { label: '小程序接口基址', keys: ['miniprogram_base_url'], hint: '小程序端冷启动引导地址，须为 https 完整地址' },
  ]
  return defs.map((c) => {
    const first = find(c.keys[0])
    let ok: boolean
    if (c.keys[0] === 'cookie_secure') {
      ok = ['true', '1'].includes((first?.value || '').trim())
    } else if (c.keys[0] === 'storage_driver') {
      ok = !!first?.configured && (cloud ? CLOUD_CREDENTIALS.every((k) => !!find(k)?.configured) : true)
    } else {
      ok = c.keys.every((k) => !!find(k)?.configured)
    }
    return { ...c, ok, readOnly: !!first?.readOnly, envVar: first?.envVar }
  })
})
const readyCount = computed(() => checks.value.filter((c) => c.ok).length)
// 分两类呈现：只读项顶着「待配置」却在这一页改不了，会让人以为能点开解决
// （旧版「查看」跳到一个改不了的灰行，等于死路）。拆开后「本页可配置」可点，
// 「环境变量 / 配置文件」明确要在部署环境改这两处之一并重启（多数部署是安装向导
// 装的，实际落在 data/runtime.env，只提环境变量会让人找不到地方改）。
const uiChecks = computed(() => checks.value.filter((c) => !c.readOnly))
const envChecks = computed(() => checks.value.filter((c) => c.readOnly))
const uiReadyCount = computed(() => uiChecks.value.filter((c) => c.ok).length)
const envReadyCount = computed(() => envChecks.value.filter((c) => c.ok).length)
const uiPending = computed(() => uiChecks.value.length - uiReadyCount.value)
const envPending = computed(() => envChecks.value.length - envReadyCount.value)

// 只读项在界面里改不了，就把「到底要设什么」直接给出来（复制走即可）
function copyEnv(c: ReadyCheck) {
  const text = c.envSample || ''
  if (!text) return
  const ok = () => ElMessage.success('已复制：' + text)
  try {
    // 非 HTTPS / 无剪贴板权限时 navigator.clipboard 不存在：把内容显示出来供手动复制
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(ok).catch(() => ElMessage.info(text))
    } else {
      ElMessage.info(text)
    }
  } catch {
    ElMessage.info(text)
  }
}

// ---- 上线就绪清单的展开状态 ----
// 默认收起（清单是「体检报告」，不是每天要看的东西），但两种情况下默认展开：
// ① 一项都没配（readyCount=0）——这时清单就是首配的导航图；
// ② 用户手动展开过——记住选择，之后保持展开，不用每次点。
const READY_OPEN_KEY = 'settings_ready_open'
const readyOpen = ref(false)
// 只按「初始状态」决定一次：之后用户手动开合过就不再覆盖（否则每次刷新都被重置）
let readyOpenDecided = false
function decideReadyOpen() {
  if (readyOpenDecided || checks.value.length === 0) return
  readyOpenDecided = true
  const fallback = readyCount.value === 0
  try {
    const s = localStorage.getItem(READY_OPEN_KEY)
    // 没存过（含隐私模式写不进）→ 按「全新部署才展开」给默认值
    readyOpen.value = s === null ? fallback : s === '1'
  } catch {
    readyOpen.value = fallback
  }
}
function toggleReady() {
  readyOpen.value = !readyOpen.value
  try {
    localStorage.setItem(READY_OPEN_KEY, readyOpen.value ? '1' : '0')
  } catch {
    // 写不进只影响「记住选择」，本次开合已生效，静默跳过
  }
}

// 定位到对应配置行（行上锚点 id="set-<key>"）。
// 防御 scrollIntoView 缺失：jsdom 等环境没有该方法（真实浏览器均有），缺省时静默跳过。
function jumpTo(key: string) {
  const el = document.getElementById('set-' + key)
  if (el && typeof el.scrollIntoView === 'function') {
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }
}

// ---- 模块导航 + 滚动联动（让长列表滚动时随时知道「我在哪个模块、一键跳过去」）----
// 顶部工具条吸顶（搜索 + 模块锚点），分组标题也吸顶，二者用 --toolbar-h 衔接。
// 滚动时根据各分组顶部位置算出「当前模块」并高亮对应锚点（scroll-spy）。
const toolbarRef = ref<HTMLElement | null>(null)
// 工具条实时高度：搜索行 + 模块锚点行，窄屏锚点换行会变高，故用 ResizeObserver 动态量
const toolbarH = ref(112)
const activeKey = ref('')
// 是否已吸顶（用于吸顶时加强阴影，让工具条「浮起来」；未吸顶时保持扁平）
const pinned = ref(false)
let scrollEl: HTMLElement | null = null
let ro: ResizeObserver | null = null
let scrollHandler: (() => void) | null = null

function measureToolbar() {
  if (toolbarRef.value) toolbarH.value = Math.ceil(toolbarRef.value.getBoundingClientRect().height)
}

// 工具条挂载后才能量高 + 监听尺寸变化（加载时 v-else 未渲染，onMounted 时 ref 还是空）
function attachToolbarObserver() {
  if (typeof ResizeObserver === 'undefined' || !toolbarRef.value || ro) return
  ro = new ResizeObserver(() => {
    measureToolbar()
    computeActive()
  })
  ro.observe(toolbarRef.value)
}

// 当前可视模块 = 顶部越过「工具条底边 + 余量」的最后一个分组
function computeActive() {
  if (!scrollEl) return
  const base = scrollEl.getBoundingClientRect().top
  // 吸顶判定：工具条贴到滚动口顶部（等于容器 rect.top）即视为吸住。
  // 不做「滚过多少像素」的硬编码阈值——窄屏工具条换行时自然位置会变，比阈值可靠。
  if (toolbarRef.value) {
    pinned.value = toolbarRef.value.getBoundingClientRect().top <= base + 1
  }
  const line = base + toolbarH.value + 6
  let cur = displayGroups.value[0]?.key || ''
  for (const g of displayGroups.value) {
    const el = document.getElementById('grp-' + g.key)
    if (el && el.getBoundingClientRect().top <= line) cur = g.key
  }
  activeKey.value = cur
}

// 点击模块锚点平滑滚动到对应分组；scroll-margin-top 已为吸顶工具条留出空间
function jumpToGroup(key: string) {
  const el = document.getElementById('grp-' + key)
  if (el && typeof el.scrollIntoView === 'function') {
    el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    activeKey.value = key
  }
}

onMounted(() => {
  measureToolbar()
  attachToolbarObserver()
  // 真正的滚动容器是 .admin-main；测试里没有它，缺省跳过滚动监听
  scrollEl = document.querySelector('.admin-main') as HTMLElement | null
  if (scrollEl) {
    scrollHandler = () => computeActive()
    scrollEl.addEventListener('scroll', scrollHandler, { passive: true })
  }
  nextTick(computeActive)
})

onUnmounted(() => {
  ro?.disconnect()
  if (scrollEl && scrollHandler) scrollEl.removeEventListener('scroll', scrollHandler)
})

// 数据加载完、工具条真正挂载后再补一次量高 + 监听（初始 loading=true 时 ref 为空）
watch(loading, (v) => {
  if (!v) nextTick(() => {
    measureToolbar()
    attachToolbarObserver()
    computeActive()
  })
})

const changedKeys = computed(() =>
  items.value
    .filter((it) => !it.readOnly)
    .filter((it) => cleared[it.key] || draft[it.key] !== original[it.key])
    .map((it) => it.key),
)
const dirty = computed(() => changedKeys.value.length > 0)
const changedCount = computed(() => changedKeys.value.length)

// ---- 搜索过滤 ----
// 命中范围：中文名 / key / 描述 / 环境变量名 / 分组中文名。
// 分组中文名参与匹配是特性不是漏洞：搜「存储」的人往往就是想找那一组。
const keyword = ref('')
const filteredGroups = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return groups.value
  const hit = (it: SettingItem) =>
    [it.name, it.key, it.description, it.envVar, GROUP_LABELS[it.group] || it.group]
      .filter(Boolean)
      .some((s) => s!.toLowerCase().includes(kw))
  return groups.value
    .map((g) => ({ ...g, items: g.items.filter(hit) }))
    .filter((g) => g.items.length > 0)
})
const matchCount = computed(() => filteredGroups.value.reduce((n, g) => n + g.items.length, 0))

// 实际渲染用哪份分组：搜索态走全量（被联动过滤掉的项也要能搜到——
// local 时搜 minio_endpoint 必须命中，否则用户会以为系统里没这项，
// 那时行内的「当前选项下不需要」置灰标记就是正确答案）；
// 非搜索态走 visibleGroups（只显示当前选项下相关的项）。
const displayGroups = computed(() => (keyword.value.trim() ? filteredGroups.value : visibleGroups.value))

// 搜索过滤后可见分组变化，重新判定当前模块（默认落到第一个）
watch(displayGroups, () => nextTick(computeActive))

// ---- 破坏性更改二次确认 ----
// 这些改动会让「已经存下来的文件」失联、或把「已配好的凭据」清掉回退到
// 可能并不存在的环境变量上。分组导语里的警告只配看，这里的确认逼一次清醒决策：
// 误确认只是一次多余点击，漏确认可能就是一批文件打不开 + 工单。
const DESTRUCTIVE: Record<string, string> = {
  storage_driver: '存储方式变更后，按原方式上传的历史文件可能无法访问（请先确认历史文件仍可访问）',
  upload_dir: '本地上传目录变更后，旧目录中的已有文件将不再可见',
  storage_public: '存储空间公开访问变更会影响所有已生成文件链接的可访问范围',
}
function destructiveWarnings(): string[] {
  const warns: string[] = []
  changedKeys.value.forEach((k) => {
    const it = items.value.find((i) => i.key === k)
    if (!it) return
    const label = it.name || k
    if (it.secret && cleared[k]) {
      warns.push(`「${label}」将被清空并回退环境变量；若部署环境没有对应变量，相关功能会直接失效`)
    } else if (DESTRUCTIVE[k]) {
      warns.push(`「${label}」：${DESTRUCTIVE[k]}`)
    }
  })
  return warns
}

// ---- 依赖联动 ----
// 以下拉 / 开关的当前草稿值为准：用户刚切换就能看到哪些项「转亮 / 置灰」，不必先保存再刷新。
const driverVal = computed(() => (draft.storage_driver || '').trim() || 'local')
const cdnOn = computed(() => (draft.cdn_enabled || '') === 'true')
const wxAppidFilled = computed(
  () => !!(draft.wx_appid || '').trim() || !!items.value.find((i) => i.key === 'wx_appid')?.configured,
)

// local 只关心「文件落盘目录」；minio / oss / s3 只关心端点 / 桶 / 凭据 / 访问基址。
// 两类项互斥出现，避免对着一堆当前选项下用不上的输入框。
// 「专属」边界按后端实际读取路径划（2026-10-22 全量核查核实过源码，2026-10-23 加 s3 时重核）：
//   - storage_use_ssl / storage_region 是 S3 兼容驱动（minio / s3）专属：
//     cmd/server 的 NewOssUploader 不收 SSL 参数（阿里云 OSS 客户端固定 https），
//     也没有区域参数；而 minio 与 s3 在 newUploader 里是同一个 case，
//     共用 MinioUploader 的 useSSL / region 入参——两项对两者都真正生效；
//   - upload_base_url 是云存储专属：LocalUploader.Save 返回相对地址 /uploads/...，
//     小程序端用 miniprogram_base_url 拼绝对地址、浏览器按页面源解析，都不经过它。
const S3_COMPAT_ONLY = ['storage_use_ssl', 'storage_region']
const CLOUD_ONLY = [
  'storage_endpoint',
  'storage_bucket',
  'storage_access_key',
  'storage_secret_key',
  'upload_base_url',
]
const CDN_ONLY = ['cdn_provider', 'cdn_access_key_id', 'cdn_access_key_secret', 'cdn_resource_id', 'cdn_endpoint']
// 五家的凭据形态不同（与后端 storage.CDNProviderNeeds* 同源，改一边要改另一边）：
//   - 资源标识只有 Cloudflare（Zone ID）与 CloudFront（Distribution ID）需要，
//     AK/SK 三家按提交 URL 的域名自己定位加速域名；
//   - Cloudflare 只用一个 API Token（填在 Secret 位），用不到 AccessKeyId。
// 厂商清单与「谁需要什么」在 utils/cdn（与后端 storage.CDNProviderNeeds* 同源）。
// 未启用 CDN 时整区不占版面；启用后按厂商再收窄一次，避免对着用不上的输入框。
const cdnProvider = computed(() => (draft.cdn_provider || '').trim() || CDN_DEFAULT_PROVIDER)
const cdnNeedsResource = computed(() => CDN_RESOURCE_PROVIDERS.includes(cdnProvider.value))

interface DepState {
  focus: boolean
  dim: boolean
}
function depState(key: string): DepState {
  if (key === 'upload_dir') {
    return { focus: driverVal.value === 'local', dim: driverVal.value !== 'local' }
  }
  if (S3_COMPAT_ONLY.includes(key)) {
    return { focus: driverVal.value === 'minio' || driverVal.value === 's3', dim: driverVal.value !== 'minio' && driverVal.value !== 's3' }
  }
  if (CLOUD_ONLY.includes(key)) {
    return { focus: driverVal.value !== 'local', dim: driverVal.value === 'local' }
  }
  if (CDN_ONLY.includes(key)) {
    if (!cdnOn.value) return { focus: false, dim: true }
    // Cloudflare 只用 API Token：AccessKeyId 用不到，别让人对着它发愣
    if (key === 'cdn_access_key_id') {
      return { focus: cdnProvider.value !== 'cloudflare', dim: cdnProvider.value === 'cloudflare' }
    }
    if (key === 'cdn_resource_id') {
      return { focus: cdnNeedsResource.value, dim: !cdnNeedsResource.value }
    }
    return { focus: true, dim: false }
  }
  if (key === 'wx_appsecret') {
    return { focus: wxAppidFilled.value, dim: !wxAppidFilled.value }
  }
  return { focus: false, dim: false }
}

// ---- 首次配置向导 ----
// 35 个输入框平铺给第一次部署的人，最大的问题不是「找不到」，而是「不知道从哪开始、按什么顺序」。
// 向导按依赖顺序分 6 步，每步只显示当前相关的项（复用 depState 过滤），
// 默认落在「第一个有未配置项的步骤」——已配好的步骤直接跳过，不让人重复确认。
interface WizardStep {
  key: string
  title: string
  /** 步骤条专用短名（≤6 字）。步骤条是等宽网格，格子宽度固定，
      长标题会被迫折行、甚至一字一行；全名放不下时用短名，全名仍在
      进度行与步骤正文里出现，不会丢信息 */
  short?: string
  blurb: string
  /** 候选 key；条件显隐的按 depState 过滤，后端缺省的（旧版本）自动跳过 */
  keys: string[]
  /** 步内小组：项多的步骤再按「在配什么」分堆，小标题让人不用逐行读描述。
      小组 key 应是 keys 的子集；未归组的 key 平铺在小组之后。
      整组被 depState 置灰时连标题一起隐藏（见 currentGroups） */
  groups?: { label: string; keys: string[] }[]
}
const WIZARD_STEPS: WizardStep[] = [
  {
    key: 'brand',
    title: '品牌与身份',
    blurb: '先定品牌名：后台侧边栏、登录页与小程序都会展示它。随时可改、即时生效。',
    keys: ['brand_name'],
  },
  {
    key: 'storage',
    title: '存储与上传',
    blurb: '图片 / 视频 / 小程序码存哪、怎么访问。演示选 local 即可；生产建议 minio / oss / s3（腾讯云 COS、华为云 OBS 等 S3 兼容云），并配上公开访问基址。切换存储方式后只显示当前方式相关的项，另一种方式已填的值不会丢。本组改动需重启服务。',
    keys: [
      'storage_driver',
      'upload_dir',
      'storage_endpoint',
      'storage_bucket',
      'storage_access_key',
      'storage_secret_key',
      'storage_use_ssl',
      // S3 兼容云（s3）常需显式区域；minio 同样生效（同一 S3 客户端实现）
      'storage_region',
      // 公私总闸：两种存储方式都生效（本地私有文件也要鉴权后读取），必须进首配路径
      'storage_public',
      'upload_base_url',
    ],
    // 与主列表 storage 组的分区一致：本地 / 云存储两套互斥配置按 depState 二选一出现
    groups: [
      {
        label: '存储位置',
        keys: [
          'storage_driver',
          'upload_dir',
          'storage_endpoint',
          'storage_bucket',
          'storage_access_key',
          'storage_secret_key',
          'storage_use_ssl',
          'storage_region',
        ],
      },
      { label: '访问方式', keys: ['storage_public', 'upload_base_url'] },
    ],
  },
  {
    key: 'limits',
    title: '上传限制',
    blurb: '限制单文件大小与允许的类型，防止撑爆存储。默认值已可用，按需调整。',
    keys: [
      'upload_max_mb',
      'upload_allow_ext',
      'upload_video_max_mb',
      // 直传预签名链接（对象存储下公私桶都会走：UploadField 总是先试预签名）
      // 与私有桶下载签名链接。两者都有默认值，不专门开一步，放在本步尾部
      'presign_put_ttl_minutes',
      'sign_get_ttl_minutes',
    ],
    groups: [
      { label: '大小与类型', keys: ['upload_max_mb', 'upload_video_max_mb', 'upload_allow_ext'] },
      { label: '临时链接有效期', keys: ['presign_put_ttl_minutes', 'sign_get_ttl_minutes'] },
    ],
  },
  {
    key: 'wechat',
    title: '微信与小程序码',
    short: '微信配置',
    blurb: '小程序码与微信登录依赖微信 AppID / AppSecret。没有微信业务可先跳过，相关功能不可用而已。',
    keys: [
      'wx_appid',
      'wx_appsecret',
      'wxacode_ttl_hours',
      'wxacode_sign_ttl_minutes',
      'wxacode_rate_limit_per_min',
      'wx_msg_token',
      'wx_msg_aeskey',
    ],
    groups: [
      { label: '小程序凭据', keys: ['wx_appid', 'wx_appsecret'] },
      { label: '小程序码', keys: ['wxacode_ttl_hours', 'wxacode_sign_ttl_minutes', 'wxacode_rate_limit_per_min'] },
      { label: '消息推送（图片内容安全）', keys: ['wx_msg_token', 'wx_msg_aeskey'] },
    ],
  },
  {
    key: 'miniprogram',
    title: '小程序接入',
    blurb: '小程序端冷启动第一个请求打到哪。必须是小程序能访问到的公网 https 地址，填错小程序直接连不上。',
    keys: ['miniprogram_base_url', 'pinned_company_id'],
		groups: [
			{ label: '接入地址', keys: ['miniprogram_base_url'] },
			{ label: '独立版锁定（可选）', keys: ['pinned_company_id'] },
		],
	},
	{
		key: 'mpupload',
		title: '后台上传小程序代码',
		short: '代码上传',
		blurb: '若要从后台直接把代码传到微信（不走 CI、也不用微信开发者工具），需要在这里告诉后台：服务器上 Node 在哪、miniprogram-ci 装在哪个目录、小程序源码放哪个目录，以及一次上传最长等多久。这些项都有环境变量兜底，纯 CI 上传可以跳过本步。',
		keys: ['mp_node_bin', 'mp_ci_module_dir', 'mp_source_dir', 'mp_upload_timeout_sec'],
		groups: [
			{ label: '上传环境（仅后台直接传代码时需要）', keys: ['mp_node_bin', 'mp_ci_module_dir', 'mp_source_dir', 'mp_upload_timeout_sec'] },
		],
	},
	{
		key: 'final',
    title: '安全确认',
    blurb: '最后核对一遍：哪些改了要重启、哪些只能改环境变量或配置文件。确认无误再保存。',
    keys: [],
  },
]

// 只读项不进向导：它们本来就只能改环境变量，放进来只会给人「这里能配」的错觉
function stepVisible(s: WizardStep): SettingItem[] {
  return s.keys
    .map((k) => items.value.find((i) => i.key === k))
    .filter((it): it is SettingItem => !!it && !it.readOnly && !depState(it.key).dim)
}
function stepDone(s: WizardStep): boolean {
  return s.key !== 'final' && stepVisible(s).every((it) => it.configured)
}
const firstUnfinishedStep = computed(() => {
  const idx = WIZARD_STEPS.findIndex((s) => !stepDone(s))
  return idx < 0 ? WIZARD_STEPS.length - 1 : idx
})

const WIZARD_SEEN_KEY = 'settings_wizard_seen'
const wizardOpen = ref(false)
const wizardStep = ref(0)
// 打开向导时快照草稿：「稍后再说」要能原样恢复，
// 不能把用户手滑改的半截状态留在主列表里（那会变成一次意外的未保存改动）
let wizardSnapshot: Record<string, string> = {}
let wizardClearedSnapshot: Record<string, boolean> = {}

const currentStep = computed(() => WIZARD_STEPS[wizardStep.value])
const currentVisible = computed(() => stepVisible(currentStep.value))
// 步内小组：groups 元数据 + depState 过滤（整组被置灰则连标题一起收起）。
// 与 stepVisible 用同一套过滤条件，保证「标题下出现的行为」与 chips 计数一致。
const currentGroups = computed(() => {
  const groups = currentStep.value.groups
  if (!groups) return []
  return groups
    .map((g) => ({
      label: g.label,
      items: g.keys
        .map((k) => items.value.find((i) => i.key === k))
        .filter((it): it is SettingItem => !!it && !it.readOnly && !depState(it.key).dim),
    }))
    .filter((g) => g.items.length > 0)
})
// 未归组的项（无 groups 的步骤 / 元数据没覆盖的 key）：平铺在小组之后，不丢项
const currentUngrouped = computed(() => {
  const s = currentStep.value
  if (!s.groups) return currentVisible.value
  const grouped = new Set(s.groups.flatMap((g) => g.keys))
  return currentVisible.value.filter((it) => !grouped.has(it.key))
})
const isLastStep = computed(() => wizardStep.value === WIZARD_STEPS.length - 1)
const wizardRestartKeys = computed(() =>
  changedKeys.value
    .filter((k) => items.value.find((i) => i.key === k)?.restart)
    // 展示中文名而不是 key：面向运营的界面不裸露内部标识符（key 在行内灰字仍可对照）
    .map((k) => items.value.find((i) => i.key === k)?.name || k),
)
function stepStatus(i: number): 'success' | 'process' | 'wait' {
  if (i === wizardStep.value) return 'process'
  return stepDone(WIZARD_STEPS[i]) ? 'success' : 'wait'
}
// 每步「还要填几项」：可见项里未配置的数量（final 步无数可计，恒 0）。
// 步骤 chips 与进度行共用——用户一眼知道每步的工作量，不用点进去才发现还有 5 项。
function stepPending(i: number): number {
  const s = WIZARD_STEPS[i]
  if (s.key === 'final') return 0
  return stepVisible(s).filter((it) => !it.configured).length
}
// 安全确认步的「去配置」：跳回拥有该配置项的步骤（向导内闭环）。
// 与主页 jumpTo 不同——那里滚主列表，这里在对话框内切步骤。
function wizardJumpToKey(key: string) {
  const idx = WIZARD_STEPS.findIndex((s) => s.keys.includes(key))
  if (idx >= 0) wizardStep.value = idx
}

// ---- 向导内联校验 ----
// 地址类项填错的表现最远（图片全打不开 / 小程序连不上），且后台保存时的校验
// 只告诉你「哪一项不合法」，不告诉你「在向导哪一步」。所以在「下一步 / 完成」
// 之前就按后端 validate.go 的同一条规则先拦一道：当场指出错误、就地改。
// miniprogram_base_url 比后端更严一档（要求 https）：微信小程序官方只收 https，
// http:// 能过后端校验但连不上，提示语里说清原因。
const URL_RULES: Record<string, { httpsOnly?: boolean; example: string }> = {
  upload_base_url: { example: 'https://cdn.example.com' },
  miniprogram_base_url: { httpsOnly: true, example: 'https://mp.example.com' },
}
function validateUrlValue(key: string, value: string): string {
  const rule = URL_RULES[key]
  if (!rule) return ''
  const s = (value ?? '').trim()
  // 留空 = 回退环境变量 / 内置缺省，合法（与后端 httpURLOrEmpty 一致）
  if (!s) return ''
  let u: URL
  try {
    u = new URL(s)
  } catch {
    return `不是完整地址，应类似 ${rule.example}`
  }
  if (!u.hostname) return `缺少域名或 IP，应类似 ${rule.example}`
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    return `只支持 http:// 或 https:// 开头的完整地址（例 ${rule.example}）`
  }
  if (rule.httpsOnly && u.protocol !== 'https:') {
    return `微信小程序只支持 https 地址，http:// 会导致小程序连不上（例 ${rule.example}）`
  }
  return ''
}
// 某一步可见项里的地址错误（含中文名，提示语直接说「哪一项」）
function stepErrors(i: number): { key: string; name: string; msg: string }[] {
  return stepVisible(WIZARD_STEPS[i])
    .map((it) => ({
      key: it.key,
      name: it.name || it.key,
      msg: validateUrlValue(it.key, draft[it.key] ?? ''),
    }))
    .filter((x) => !!x.msg)
}
// 当前步的行内错误：随草稿实时重算——用户一改成合法值，错误立即消失
const currentStepErrors = computed<Record<string, string>>(() => {
  const errs: Record<string, string> = {}
  currentVisible.value.forEach((it) => {
    const msg = validateUrlValue(it.key, draft[it.key] ?? '')
    if (msg) errs[it.key] = msg
  })
  return errs
})
function wizardSeen(): boolean {
  try {
    return !!localStorage.getItem(WIZARD_SEEN_KEY)
  } catch {
    // 隐私模式等读不了的场景：不当「没看过」，避免每次刷新都自动弹
    return true
  }
}
function markWizardSeen() {
  try {
    localStorage.setItem(WIZARD_SEEN_KEY, '1')
  } catch {
    // 写不进只影响「不再自动弹」，向导仍可手动打开，静默跳过即可
  }
}
// ---- 快速开始预设 ----
// 第一次部署的人面对 8 个存储项无从下手：演示要能尽快跑起来，生产要安全。
// 一键填入推荐组合，之后每一步里仍可改——预设只是起点，不是锁定。
// 上传限制的「默认值」与后端 registry 缺省一致（前端没有缺省值接口，属镜像值，
// 后端改缺省时这里要同步）；且只在「没设过」时填：用户显式改过的值不被预设覆盖。
const PRESET_LIMIT_DEFAULTS: Record<string, string> = {
  upload_max_mb: '10',
  upload_video_max_mb: '100',
  upload_allow_ext: 'png,jpg,jpeg,gif,webp,mp4',
}
const presetNote = ref('')
const activePreset = computed(() => {
  if (draft.storage_driver === 'minio') return 'prod'
  if (draft.storage_driver === 'local' && draft.upload_dir === './data/uploads') return 'demo'
  return ''
})
function applyPreset(mode: 'demo' | 'prod') {
  Object.entries(PRESET_LIMIT_DEFAULTS).forEach(([k, v]) => {
    // 只填空项：后端还没有这一项（旧版本）或用户没设过时才填推荐值
    if (items.value.some((x) => x.key === k) && !draft[k]) setVal(k, v)
  })
  if (mode === 'demo') {
    setVal('storage_driver', 'local')
    setVal('upload_dir', './data/uploads')
    // local 走同源 /uploads 访问，外部基址留空即可；显式清掉旧值，
    // 避免上一套 CDN 域名残留，把图片继续指到已经不存在的地方
    setVal('upload_base_url', '')
    presetNote.value =
      '已按演示模式填入：存储=local、目录=./data/uploads、上传限制=默认值（你改过的保持原样）。local 下文件经同源 /uploads 访问，不需要外部基址。'
  } else {
    setVal('storage_driver', 'minio')
    setVal('storage_use_ssl', 'true')
    presetNote.value =
      '已按生产模式填入：存储=minio、连接走 HTTPS。存储端点、桶名、AccessKey / SecretKey、文件访问基址需按你的对象存储手动填写——到下一步逐项确认。'
  }
}

function openWizard() {
  wizardSnapshot = { ...draft }
  wizardClearedSnapshot = { ...cleared }
  wizardStep.value = firstUnfinishedStep.value
  wizardOpen.value = true
}
function prevStep() {
  if (wizardStep.value > 0) wizardStep.value--
}
function nextStep() {
  // 地址类填错当场拦住：不让错误值被带到后面步骤（保存时才发现就晚了）
  const errs = stepErrors(wizardStep.value)
  if (errs.length > 0) {
    ElMessage.warning(`「${errs[0].name}」${errs[0].msg}，请修正后再进入下一步`)
    return
  }
  if (wizardStep.value < WIZARD_STEPS.length - 1) wizardStep.value++
}
function wizardCancel() {
  Object.assign(draft, wizardSnapshot)
  Object.assign(cleared, wizardClearedSnapshot)
  markWizardSeen()
  wizardOpen.value = false
}
async function wizardFinish() {
  markWizardSeen()
  // 全步骤兜底：步骤条允许自由跳转，用户可能没逐步走过，
  // 前面步骤里填错的地址项在这里统一拦下——直接保存会被后端拒绝，
  // 那时错误信息指不回向导的某一步，不如现在跳回去就地改
  for (let i = 0; i < WIZARD_STEPS.length - 1; i++) {
    const errs = stepErrors(i)
    if (errs.length > 0) {
      wizardStep.value = i
      ElMessage.warning(`「${errs[0].name}」${errs[0].msg}，请修正后再完成`)
      return
    }
  }
  // 一步没改就直接关：不让「完成」按钮制造一次空保存的等待
  if (!dirty.value) {
    wizardOpen.value = false
    return
  }
  if (await save()) wizardOpen.value = false
}

function setVal(k: string, v: string) {
  draft[k] = v
  cleared[k] = false
}
// 清除敏感值：置空并标记，提交时会带上空串让后端回退环境变量
function clearSecret(k: string) {
  draft[k] = ''
  cleared[k] = true
}

function reset() {
  items.value.forEach((it) => {
    draft[it.key] = original[it.key]
    cleared[it.key] = false
  })
}

async function load() {
  loading.value = true
  try {
    const r = await getSettings()
    if (r.code === 0 && r.data) {
      items.value = r.data
      r.data.forEach((it) => {
        // 敏感项后端只给掩码，界面输入框一律留空：留空 = 不修改，避免把掩码回写成真实值。
        // 只读的敏感项例外：它本来就不能提交，直接把掩码显示出来，管理员才看得到「当前已配置」。
        original[it.key] = it.secret && !it.readOnly ? '' : it.value
        draft[it.key] = original[it.key]
        cleared[it.key] = false
      })
      // 恢复上次未保存的草稿（刷新 / 误关页面不丢）。只认当前后端还认的 key——
      // 旧版本删掉的配置项不复活；值与原值相同的项不计入提示，避免「什么都没改却弹通知」。
      const stored = loadWizardDraft()
      if (stored) {
        let restored = 0
        items.value.forEach((it) => {
          if (!(it.key in stored.draft)) return
          draft[it.key] = stored.draft[it.key]
          if (it.key in stored.cleared) cleared[it.key] = !!stored.cleared[it.key]
          if (draft[it.key] !== original[it.key] || cleared[it.key]) restored++
        })
        if (restored > 0) {
          ElMessage.info(`已恢复上次未保存的 ${restored} 项改动，可继续编辑；不需要可点「重置」还原`)
        }
      }
      // 全新部署（一项都没就绪、也没主动关过向导）自动拉起向导：
      // 否则用户第一次打开设置页，看到的只是一片空输入框，不知道从哪开始。
      if (readyCount.value === 0 && !wizardSeen()) {
        openWizard()
      }
      // 数据到位后才能判断「是否全新部署」→ 就绪清单的初始展开态在这里定
      decideReadyOpen()
    }
  } catch {
    // 拦截器已提示（如非平台超管越权）
  } finally {
    loading.value = false
  }
}

// 返回是否保存成功：向导的「保存并完成」据此决定是否关闭对话框
async function save(): Promise<boolean> {
  if (!dirty.value) return true
  // 破坏性更改先过确认闸门：取消则整个保存作废（草稿保留，可改回去或再次确认）
  const warns = destructiveWarnings()
  if (warns.length > 0) {
    try {
      await ElMessageBox.confirm(
        `以下更改可能影响已有数据：${warns.join('；')}。确认保存吗？`,
        '破坏性更改确认',
        { type: 'warning', confirmButtonText: '确认保存', cancelButtonText: '取消' },
      )
    } catch {
      return false
    }
  }
  saving.value = true
  const payload: Record<string, string> = {}
  changedKeys.value.forEach((k) => {
    payload[k] = cleared[k] ? '' : draft[k]
  })
  // 先记下本次保存了几项：下面的乐观推进会把草稿变成「无变更」，
  // 事后再读 changedCount 只会得到 0（提示语会说「已保存 0 项」）。
  const savedCount = Object.keys(payload).length
  try {
    // 后端逐条校验类型，任一非法即返回 code:1（拦截器已弹错误提示并 reject）
    const r = await updateSettings(payload)
    // 成功后把「原值」推进到新值，按钮回到禁用态
    Object.keys(payload).forEach((k) => {
      const wasCleared = cleared[k]
      cleared[k] = false
      const it = items.value.find((x) => x.key === k)
      // 乐观推进 configured：保存成功即反映到「待配置」标记与就绪统计上，
      // 不用整页刷新（刷新会丢掉其它未保存改动）
      if (it) it.configured = !wasCleared
      // 敏感项保存后输入框回到空（下发的仍是掩码），原值也保持空，避免显示成「已修改」
      original[k] = it?.secret ? '' : payload[k]
      draft[k] = original[k]
    })
    // 品牌名变了要立刻同步侧边栏展示，避免用户以为没生效
    if (payload.brand_name !== undefined) {
      settings.setBrandName(payload.brand_name || '')
    }
    // 保存成功 = 暂存的草稿已落地，再留着下次会恢复出一份「已保存过」的旧改动
    clearWizardDraft()
    const restart = r?.data?.restartKeys || []
    if (restart.length > 0) {
      // 详情交给常驻提示条（含重启命令），toast 只负责让人立刻注意到
      restartPending.value = restart.map((k: string) => items.value.find((i) => i.key === k)?.name || k)
      restartDismissed.value = false
      ElMessage.warning(`已保存 ${savedCount} 项；其中 ${restart.length} 项需重启服务后才生效`)
      // 提示条在页面顶部，而保存按钮在吸顶头——用户很可能是滚到很下面的分组
      // 才点的保存，不滚过去等于这条提示根本没人看到（实测提示条在视口外 2000px+）
      nextTick(() => {
        const el = document.querySelector('.restart-alert')
        if (el && typeof el.scrollIntoView === 'function') {
          el.scrollIntoView({ behavior: 'smooth', block: 'center' })
        }
      })
    } else {
      ElMessage.success(`已保存 ${savedCount} 项设置`)
    }
    return true
  } catch {
    // 失败保留草稿，方便用户修正后重试
    return false
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.loading {
  padding: 24px 0;
  text-align: center;
  color: #909399;
}
.group-card {
  margin-bottom: 14px;
  border-radius: 8px;
}
/* 外壳与分组卡片放开 overflow：默认 el-card / el-card__body 的 overflow 会让 sticky
   被卡片自身「夹住」而失效，必须让吸顶相对真正滚动的 .admin-main 生效 */
.settings-shell {
  overflow: visible;
}
.settings-shell :deep(.el-card__body) {
  overflow: visible;
}
.group-card {
  overflow: visible;
  /* 模块锚点跳转时为吸顶工具条留出空间：落点 = 滚动口顶 + 工具条可见高 - 1px，
     让分组标题正好贴住工具条下缘（同 header 吸顶位的 -1px 压边逻辑），不留缝 */
  scroll-margin-top: calc(var(--toolbar-h, 112px) - 1px);
}
/* 分组标题吸顶：滚到本组内容时标题停在工具条下方，始终知道当前在哪个模块。
   sticky 参考系同样被 .admin-main 的 16px padding 内缩，且要压在工具条下 1px
   （工具条 z-index 更高盖住接缝），故为 toolbar-h - 16 - 1 = -17px；
   用 +Npx 留白会在两截吸顶条之间漏出 1-2px 的内容缝（实测踩过） */
.group-card :deep(.el-card__header) {
  position: sticky;
  top: calc(var(--toolbar-h, 112px) - 17px);
  z-index: 4;
  /* 与工具条同款毛玻璃，两截吸顶条质感一致 */
  background: rgba(255, 255, 255, 0.82);
  backdrop-filter: blur(12px) saturate(1.5);
  -webkit-backdrop-filter: blur(12px) saturate(1.5);
  border-bottom: 1px solid #ebeef5;
}
.group-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.group-title-wrap {
  display: flex;
  align-items: center;
  gap: 10px;
}
.group-accent {
  flex: none;
  width: 4px;
  height: 16px;
  border-radius: 2px;
  background: #409eff;
}
.group-title {
  font-size: 16px;
  font-weight: 700;
  color: #303133;
}
.group-count {
  flex: none;
  font-size: 12px;
  color: #409eff;
  background: #ecf5ff;
  border: 1px solid #d9ecff;
  border-radius: 999px;
  padding: 2px 10px;
}
.group-blurb {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
/* 组内分区小标题：与配置向导的 .wizard-group-title 同一形态
   （12px 500 字重 + 尾部细线），两处结构一致、看起来也像一家产品 */
.group-sub {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  font-weight: 500;
  color: #606266;
  margin: 14px 0 2px;
}
.group-card .group-sub:first-child {
  margin-top: 0;
}
.group-sub::after {
  content: '';
  flex: 1;
  height: 1px;
  background: #ebeef5;
}
/* 吸顶头：标题 + 搜索 + 操作按钮 + 模块锚点。整块常驻，滚不滚动、搜不搜索
   看到的结构都一样（旧版标题随内容滚走、搜索后才露出，两态不一致） */
.sticky-head {
  position: sticky;
  /* sticky 的参考系会被滚动容器的 padding 内缩：.admin-main 有 16px 顶部 padding，
     top:0 会钉在 padding 下缘、上方留一条 16px 透明带，滚动内容从带里漏出来。
     top:-16px 恰好抵消内缩，让吸顶头贴住滚动口顶部（60px 处）盖住这条带。
     ⚠️ 数值必须与 AdminLayout 里 .admin-main 的 padding-top 保持一致。 */
  top: -16px;
  z-index: 6;
  /* 毛玻璃：半透明白 + 背景模糊，吸顶时滚动内容从条后透过呈磨砂感。
     未吸顶时身后是白卡片，视觉上与不透明无异 */
  background: rgba(255, 255, 255, 0.82);
  backdrop-filter: blur(12px) saturate(1.5);
  -webkit-backdrop-filter: blur(12px) saturate(1.5);
  border-bottom: 1px solid #ebeef5;
  /* 未吸顶时保持扁平（与卡片同一平面），吸住后才浮起——避免页面顶部凭空多一道阴影 */
  box-shadow: 0 0 0 rgba(0, 0, 0, 0);
  transition: box-shadow 0.18s ease, border-color 0.18s ease;
  padding: 12px 0 4px;
  margin-bottom: 14px;
}
/* 吸顶态：更强的投影 + 更实的分隔线，明确「这一层浮在内容之上」 */
.sticky-head.pinned {
  border-bottom-color: #d4d7de;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.12);
}
.page-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  /* 标题行在吸顶头内部，间距交给 .module-nav 的 margin-top，这里不再留白 */
  margin-bottom: 0;
  flex-wrap: wrap;
}
.page-bar h3 {
  margin: 0;
  flex: none;
}
.bar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
/* 搜索框在「配置向导」左侧：常规后台布局（标题 | 搜索 | 操作按钮），
   且与操作同处一行，滚动 / 搜索两态都不会移位 */
.head-search {
  flex: none;
  width: 300px;
  max-width: 46vw;
}
/* 模块锚点导航：横向排开所有模块，超出可横滑；当前模块高亮 */
.module-nav {
  display: flex;
  gap: 8px;
  margin-top: 12px;
  overflow-x: auto;
  padding-bottom: 6px;
  scrollbar-width: thin;
}
.module-nav::-webkit-scrollbar {
  height: 6px;
}
.module-nav::-webkit-scrollbar-thumb {
  background: #dcdfe6;
  border-radius: 3px;
}
.module-pill {
  flex: none;
  border: 1px solid #dcdfe6;
  background: #fff;
  color: #606266;
  font-size: 13px;
  line-height: 1;
  padding: 7px 12px;
  border-radius: 999px;
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.15s;
}
.module-pill:hover {
  color: #409eff;
  border-color: #c6e2ff;
  background: #ecf5ff;
}
.module-pill.active {
  color: #fff;
  background: #409eff;
  border-color: #409eff;
}
/* 待重启提示条：常驻至用户关闭，命令可直接选中复制 */
.restart-alert {
  margin-bottom: 14px;
}
.restart-body {
  font-size: 13px;
  line-height: 1.7;
  color: #606266;
}
.restart-keys {
  font-weight: 600;
  color: #303133;
}
.restart-cmd {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}
.restart-cmd code {
  font-size: 12px;
  color: #303133;
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 2px 8px;
}
/* 上线就绪：常驻摘要条（默认） + 点「查看详情」展开的清单卡片 */
.ready-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 10px;
  padding: 8px 12px;
  border: 1px solid #f0d8b8;
  background: #fdf6ec;
  border-radius: 8px;
  font-size: 13px;
}
/* 全部就绪：换成绿色，不再用橙色「警告」语气打扰 */
.ready-bar.ok {
  border-color: #d9eccb;
  background: #f0f9eb;
}
.ready-bar-title {
  font-weight: 600;
  color: #303133;
}
.ready-bar-count {
  color: #409eff;
  background: #ecf5ff;
  border: 1px solid #d9ecff;
  border-radius: 999px;
  padding: 1px 10px;
  font-size: 12px;
  white-space: nowrap;
}
.ready-bar-pending {
  color: #e6a23c;
}
.ready-bar-done {
  color: #67c23a;
}
/* 展开开关推到最右，与摘要文字分开：摘要是状态，按钮是动作 */
.ready-bar-toggle {
  margin-left: auto;
}
/* 展开后的清单卡片紧贴摘要条（摘要条已给 10px 下间距） */
.ready-card {
  margin-bottom: 14px;
  border-radius: 8px;
}
.ready-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
  border-bottom: 1px solid #f2f6fc;
}
.ready-row:last-child {
  border-bottom: none;
}
.ready-badge {
  flex: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 700;
  color: #fff;
}
.ready-badge.ok {
  background: #67c23a;
}
.ready-badge.warn {
  background: #e6a23c;
}
.ready-main {
  flex: 1;
  min-width: 0;
}
.ready-label {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
}
/* 就绪检查按「本页可配置 / 部署环境变量」分节 */
.ready-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 10px 0 2px;
  font-size: 12px;
  font-weight: 600;
  color: #606266;
}
.ready-section-count {
  font-weight: 400;
  color: #909399;
}
/* 只读项：给出要设的写法（可复制，环境变量与配置文件同格式），不再跳到改不了的灰行 */
.ready-env {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  flex-wrap: wrap;
}
.ready-env-code {
  font-size: 12px;
  color: #303133;
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 3px 8px;
  word-break: break-all;
}
.ready-env-tip {
  font-size: 12px;
  color: #909399;
}
.ready-desc {
  margin-top: 2px;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}
.ready-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed #ebeef5;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
/* 窄屏：标题与操作按钮各占一行，搜索框铺满，否则 300px 固定宽会把按钮挤到折行错位 */
@media (max-width: 900px) {
  .page-bar {
    align-items: stretch;
  }
  .bar-actions {
    width: 100%;
  }
  .head-search {
    width: 100%;
    max-width: none;
    order: -1;
  }
}
.filter-count {
  font-size: 12px;
  color: #909399;
  white-space: nowrap;
}
.filter-empty {
  padding: 24px 0;
  text-align: center;
  font-size: 13px;
  color: #909399;
}
/* 配置向导 */
/* 步骤条用 max-width 而不是改 width prop：el-dialog 的 width 是内联样式，
   样式表覆盖不了内联值，max-width 则能在窄屏直接生效（无需 !important） */
.wizard-dialog {
  max-width: 92vw;
}
/* 步骤条：等宽网格，不是 flex: 1 1 88px。
   7 步硬塞一行时每格只剩 ~90px，而最长的一步叫「后台上传小程序代码」（9 字），
   中文可在任意字间断行 → 标题被挤成一字一行的竖排，格子高低还各不相同（2026-10-25 截图）。
   换成 auto-fit 网格：宽屏 4 列、窄屏自动降列，每格等宽、标题最多折两行。 */
.wizard-stepbar {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 6px;
  margin-bottom: 8px;
}
.wizard-stepchip {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 6px;
  min-width: 0;
  padding: 7px 9px;
  border: 0.5px solid #ebeef5;
  border-radius: 6px;
  background: #fff;
  color: #909399;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}
/* 标题允许折行（窄屏时），但最多两行并省略，绝不出现一字一行 */
.wizard-stepchip-title {
  min-width: 0;
  line-height: 1.3;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.wizard-stepchip:hover {
  border-color: #c0c4cc;
  color: #606266;
}
.wizard-stepchip.done {
  background: #f0f9eb;
  border-color: #e1f3d8;
  color: #67c23a;
}
.wizard-stepchip.cur {
  background: #ecf5ff;
  border-color: #409eff;
  color: #409eff;
  font-weight: 500;
}
.wizard-stepchip-no {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  min-width: 15px;
  height: 15px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.08);
  font-size: 10px;
}
.wizard-stepchip.cur .wizard-stepchip-no {
  background: #409eff;
  color: #fff;
}
.wizard-stepchip.done .wizard-stepchip-no {
  background: #67c23a;
  color: #fff;
}
/* 每步待配置数：琥珀色小徽标，步骤没填完就一直挂着 */
.wizard-stepchip-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  min-width: 15px;
  height: 15px;
  padding: 0 4px;
  border-radius: 999px;
  background: #fdf6ec;
  color: #e6a23c;
  font-size: 10px;
}
.wizard-progress {
  text-align: center;
  font-size: 12px;
  color: #909399;
  margin: 8px 0 4px;
}
.wizard-progress-pending {
  margin-left: 8px;
  padding: 1px 8px;
  border-radius: 999px;
  background: #fdf6ec;
  color: #e6a23c;
}
.wizard-blurb {
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  margin-bottom: 8px;
}
/* 快速开始预设条：浅灰底把自己和步骤内容分开，按钮 + 说明同一行不抢焦点 */
.wizard-preset {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 10px 12px;
  margin-bottom: 10px;
  background: #f5f7fa;
  border-radius: 6px;
}
.wizard-preset-label {
  font-size: 12px;
  color: #606266;
}
.wizard-preset-note {
  flex-basis: 100%;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
.wizard-body {
  max-height: 46vh;
  overflow-y: auto;
}
/* 步内小组标题：一步里项多时按「在配什么」再分堆。小标题 + 尾部细线，
   分组一眼可辨又不加大视觉重量（与步骤 chips 的轻量风格一致） */
.wizard-group-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  font-weight: 500;
  color: #606266;
  margin: 14px 0 2px;
}
.wizard-body > .wizard-group-title:first-child {
  margin-top: 0;
}
.wizard-group-title::after {
  content: '';
  flex: 1;
  height: 1px;
  background: #ebeef5;
}
.wizard-final {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed #ebeef5;
}
.wizard-final-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.wizard-final-title {
  font-size: 13px;
  font-weight: 500;
  color: #303133;
}
.wizard-final-count {
  font-size: 12px;
  padding: 1px 8px;
  border-radius: 999px;
  background: #fdf6ec;
  color: #e6a23c;
}
.wizard-final-count.ok {
  background: #f0f9eb;
  color: #67c23a;
}
.wizard-final-sec {
  font-size: 12px;
  font-weight: 500;
  color: #606266;
  margin: 10px 0 2px;
}
.wizard-final-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #303133;
  padding: 4px 0;
}
.wizard-final-label {
  flex: none;
}
.wizard-final-desc {
  color: #909399;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.wizard-final-row .el-button {
  margin-left: auto;
  flex: none;
}
.wizard-final-env-wrap {
  margin-left: auto;
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
}
.wizard-final-env {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  background: #f5f7fa;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 2px 6px;
  color: #606266;
}
.wizard-final-line {
  margin: 8px 0 0;
  font-size: 12px;
  color: #606266;
  line-height: 1.7;
}
.wizard-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.wizard-foot-right {
  display: flex;
  gap: 8px;
}
</style>
