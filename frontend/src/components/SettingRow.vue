<template>
  <!-- 单行配置项：主列表与「配置向导」共用同一行渲染，保证两处形态/语义一致。
       draft 值由父级持有（v-model 拆成 :model-value + change 事件），
       避免行组件内部再复制一份状态导致两处不同步。 -->
  <div class="row" :id="id" :class="{ 'row-dim': dim }">
    <div class="row-main">
      <div class="row-label">
        <span class="label-text">{{ item.name || item.key }}</span>
        <!-- 格式最易错的项：填错后果写进悬浮徽标（默认不占版面）。
             upload_base_url 填错=图片全打不开、miniprogram_base_url 填错=小程序连不上，
             这两类「后果严重且现场才发现」的项必须给最强引导。 -->
        <el-tooltip v-if="fieldHint" placement="top" popper-class="si-hint-popper" :content="fieldHint">
          <span class="row-hint" tabindex="0" aria-label="填写说明">?</span>
        </el-tooltip>
        <!-- key 降级为次要灰字：界面面向运营用中文名，排查日志/环境变量时仍可对照 -->
        <span v-if="item.name" class="label-key">{{ item.key }}</span>
        <el-tag size="small" effect="plain">{{ typeLabel(item.type) }}</el-tag>
        <!-- 依赖联动：当前选项下「应该配」且还没配 → 待配置；「暂时不用管」 → 置灰说明 -->
        <el-tag v-if="focus && !item.configured" size="small" type="warning" effect="plain">待配置</el-tag>
        <el-tag v-if="dim" size="small" type="info" effect="plain">当前选项下不需要</el-tag>
        <el-tag v-if="item.restart" size="small" type="warning" effect="plain">需重启</el-tag>
        <el-tag v-if="item.readOnly" size="small" type="info" effect="plain">只读</el-tag>
        <!-- 只读项也要说清「现在到底配了没」：只写「只读」会让运营以为功能没生效。
             未配置 = 进程在用内置缺省值（如 JWT_KEY 仍是 change-me），属上线前要修的项。 -->
        <el-tag
          v-if="item.readOnly"
          size="small"
          effect="plain"
          :type="item.configured ? 'success' : 'warning'"
        >
          {{ item.configured ? '已配置' : '未配置·用缺省值' }}
        </el-tag>
        <el-tag v-if="item.secret" size="small" type="danger" effect="plain">敏感</el-tag>
      </div>
      <div class="row-desc">
        {{ item.description }}
        <span v-if="item.envVar" class="env-hint">
          部署环境配置名：{{ item.envVar }}
          <template v-if="item.readOnly">
            ——本页不可改，请到部署环境改环境变量或配置文件（默认 data/runtime.env）后重启服务
          </template>
        </span>
      </div>
      <!-- 行内校验错误：配置向导在「下一步 / 完成」前校验地址类项，当场指出错误。
           主列表不传（保存时由后端逐条校验并弹提示），故这里默认不占版面 -->
      <div v-if="error" class="row-error">{{ error }}</div>
    </div>

    <div class="row-control">
      <!-- 开关用三态：未设置（跟随环境变量）/ 开 / 关。
           后端以「空串 = 未在界面设置」表达回退环境变量，两态开关表达不了。
           placeholder 必须绑成未设置文案：Element Plus 2.14 的 hasModelValue 对
           空串为 false，不传 placeholder 会回落通用「请选择」，看着像必选项，
           与「留空=回退环境变量/缺省值」的语义相反（2026-10-22 全量核查发现）。
           fallback-placements 砍掉 left/right：选择框贴内容区右缘，面板一旦比
           右侧剩余空间宽，popper.js 会翻到选择框左侧盖住标签列（2026-10-23 实测），
           只留下/上两个方向——最坏情况是被 preventOverflow 平移到贴住视口右缘，
           仍然是「选择框正下方展开」的正常形态。 -->
      <el-select
        v-if="item.type === 'bool'"
        :model-value="modelValue"
        :placeholder="unsetLabel"
        :disabled="item.readOnly"
        :fallback-placements="['bottom-start', 'top-start']"
        @change="(v: any) => emit('change', item.key, String(v ?? ''))"
      >
        <!-- 「未设置」的含义按项而异：登记了环境变量的项才是回退到部署环境，
             没有的（如 independent_lock）就是回退内置缺省值——统一写「跟随环境变量」
             会让没有环境变量的项指错路 -->
        <el-option :label="unsetLabel" value="" />
        <el-option label="开" value="true" />
        <el-option label="关" value="false" />
      </el-select>
      <!-- 枚举项（后端 registry 登记了 Options，与 oneOf 校验同源）：同样三态下拉。
           渲染成文本框是缺陷——候选值就摆在后端却让人手打，填错只能等保存时报错；
           且「存储方式」这类项手打 local/minio/oss 毫无意义（2026-10-22 用户实测反馈）。
           placeholder 同 bool：空值态要显示「未设置（…）」而不是「请选择」。
           fallback-placements 同 bool：见上方注释（2026-10-23 下拉翻到左侧的修复）。 -->
      <el-select
        v-else-if="item.options && item.options.length"
        :model-value="modelValue"
        :placeholder="unsetLabel"
        :disabled="item.readOnly"
        :fallback-placements="['bottom-start', 'top-start']"
        @change="(v: any) => emit('change', item.key, String(v ?? ''))"
      >
        <el-option :label="unsetLabel" value="" />
        <el-option v-for="opt in item.options" :key="opt" :label="optionLabel(opt)" :value="opt" />
      </el-select>
      <!-- 整数：上传上限 / 缓存小时 / 锁定租户 -->
      <el-input-number
        v-else-if="item.type === 'int'"
        :model-value="numVal"
        :min="minOf(item)"
        :disabled="item.readOnly"
        controls-position="right"
        @change="(v: any) => emit('change', item.key, String(v ?? 0))"
      />
      <!-- 敏感文本：可写项用密码框（只显示掩码，留空不提交，可点「清除」）。
           只读项例外：后端给的是「可辨认预览」（保留结构与首尾、遮住中段），
           用密码框会把预览变成圆点、反而什么都看不清，故用普通只读框原样显示。
           注意 v-else-if：这里与上面 bool/int 同属一条分支链，
           写成 v-if 会让开关/整数行再多渲染一个文本框（历史回归）。 -->
      <div v-else-if="item.secret && !item.readOnly" class="secret-control">
        <el-input
          :model-value="modelValue"
          type="password"
          show-password
          :disabled="item.readOnly"
          :placeholder="secretPlaceholder(item)"
          class="text-input"
          @update:model-value="(v: any) => emit('change', item.key, String(v ?? ''))"
        />
        <el-button
          v-if="!item.readOnly && item.configured"
          size="small"
          text
          type="danger"
          @click="emit('clear', item.key)"
        >
          清除
        </el-button>
      </div>
      <!-- 其余（string / string_slice）按文本编辑；只读敏感项也走这里，
           原样显示后端给的「可辨认预览」。
           路径类配置（pathKind 非空）额外给一个「浏览服务器目录」按钮：这些路径指向
           后端要 exec 的**服务器**文件系统，浏览器原生文件框选的是客户端机器、没意义，
           所以走服务端目录浏览（见 PathPickerDialog + 后端 pathpick 包）。 -->
      <div v-else class="text-input path-control">
        <el-input
          :model-value="modelValue"
          :disabled="item.readOnly"
          :placeholder="placeholderOf(item)"
          class="path-input"
          @update:model-value="(v: any) => emit('change', item.key, String(v ?? ''))"
        />
        <el-button
          v-if="item.pathKind && !item.readOnly"
          size="small"
          class="path-browse"
          @click="openPicker"
        >
          浏览…
        </el-button>
        <PathPickerDialog
          v-if="item.pathKind && pickerReady"
          v-model:visible="pickerVisible"
          :kind="item.pathKind"
          :model-value="modelValue"
          @pick="(p: string) => emit('change', item.key, p)"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SettingItem } from '@/api/settings'
import { CDN_PROVIDERS, CDN_PROVIDER_LABELS } from '@/utils/cdn'
import PathPickerDialog from '@/components/PathPickerDialog.vue'

const props = defineProps<{
  item: SettingItem
  /** 当前草稿值（父级 draft[item.key]） */
  modelValue: string
  /** 行锚点 id（主列表用，供就绪面板 jumpTo 定位；向导内可不传） */
  id?: string
  /** 依赖联动：当前选项下应当配置 */
  focus?: boolean
  /** 依赖联动：当前选项下暂不需要（置灰） */
  dim?: boolean
  /** 行内校验错误文案（配置向导用；主列表不传） */
  error?: string
}>()

const emit = defineEmits<{
  (e: 'change', key: string, value: string): void
  (e: 'clear', key: string): void
}>()

function typeLabel(t: string) {
  return { int: '整数', bool: '开关', string: '文本', string_slice: '列表' }[t] || t
}

// 有安全下限的整数项：界面先拦一道（后端也会拒绝，但能在选数时就提示更直观）。
// 例如上传上限填 0 会让所有上传失败，报错还指向文件而不是配置。
// 与后端 intRange 的下限对齐：图片 1~1024、视频 1~2048（视频漏了 min 会让
// 界面允许 0，保存才被后端拒——2026-10-22 全量核查时发现）。
const INT_MIN: Record<string, number> = { upload_max_mb: 1, upload_video_max_mb: 1 }
function minOf(item: SettingItem) {
  return INT_MIN[item.key] ?? 0
}

// 枚举候选的展示文案：值本身（local / minio / oss）对运营不够直白，补一括号说明；
// 未登记的组合原样显示值（后端新增候选时界面不留空白）。
// **长度是硬约束**：设置页每行的选择框贴在内容区右缘，它左边到视口右边只有约
// 320px，而下拉面板按最宽选项撑开——s3 标签曾写成「s3（S3 兼容云存储：腾讯云 COS
// / 华为云 OBS / 七牛云 / AWS S3 / R2 等）」，面板宽到 528px > 318px，popper.js
// 沿 fallback 链翻到 left，下拉整个跑到选择框左侧盖住标签列（2026-10-23 实测）。
// 所以候选文案保持短：完整说明交给行内的 description，那儿不比下拉里挤着看更清楚。
const OPTION_LABELS: Record<string, string> = {
  'storage_driver|local': 'local（服务器本地磁盘）',
  'storage_driver|minio': 'minio（MinIO / 自建 S3 兼容服务）',
  'storage_driver|oss': 'oss（阿里云 OSS）',
  'storage_driver|s3': 's3（S3 兼容云存储）',
  // CDN 厂商：候选值本身就是品牌名，只补中文名，不加括号说明（同上长度约束）。
  // 厂商清单来自 utils/cdn（与后端同源），新增厂商时界面自动跟上，不用在这儿再补一行
  ...Object.fromEntries(
    CDN_PROVIDERS.map((p) => [`cdn_provider|${p}`, `${p}（${CDN_PROVIDER_LABELS[p]}）`]),
  ),
}
function optionLabel(opt: string): string {
  return OPTION_LABELS[`${props.item.key}|${opt}`] || opt
}

// 格式易错项的悬浮说明：只放「填错的后果当场发现不了」的项（地址类）。
// 默认不占版面，指针停留/聚焦时弹出；与安装向导的 JWT/Cookie 徽标同一模式。
const FIELD_HINTS: Record<string, string> = {
  // 云存储专属（local 用相对地址、不经过它）——这一项只在 minio / oss / s3 下可见，
  // 提示里点明归属，被搜索拽出来的置灰行也能自解释
  upload_base_url:
    '图片 / 视频 / 小程序码的公开访问地址，http:// 或 https:// 完整地址（例 https://cdn.example.com）；配了 CDN 就填加速域名。仅 minio / oss / s3 需要——本地存储用相对地址，不经过这项。填错的结果是图片全部打不开。',
  miniprogram_base_url:
    '小程序端冷启动第一个请求打到这，必须是小程序能访问到的公网 https 地址（例 https://mp.example.com）。填错小程序直接连不上。',
  storage_endpoint: '对象存储的服务地址，如 minio.example.com:9000；存储方式选 local 时不需要。',
}
const fieldHint = computed(() => FIELD_HINTS[props.item.key] || '')

// 示例占位：比笼统的「未设置」更能告诉用户该填成什么样。
// 与 item.description（讲「是什么、改了会怎样」）互补：示例讲「填什么格式」。
const PLACEHOLDER_EXAMPLES: Record<string, string> = {
  upload_dir: '如 ./data/uploads',
  storage_endpoint: '如 minio.example.com:9000',
  storage_bucket: '如 weichuan',
  storage_region: '如 ap-guangzhou（大多数服务留空自动探测）',
  upload_base_url: '如 https://cdn.example.com',
  miniprogram_base_url: '如 https://mp.example.com',
  cdn_endpoint: '如 cdn.example.com（留空按厂商取默认）',
  // 资源标识只有 Cloudflare / CloudFront 需要，示例里点名两家各自填什么
  cdn_resource_id: 'Cloudflare 填 Zone ID，CloudFront 填 Distribution ID',
}
function placeholderOf(item: SettingItem) {
  if (item.readOnly) return '只读'
  if (item.type === 'string_slice') return '逗号分隔，如 png,jpg,webp'
  return PLACEHOLDER_EXAMPLES[item.key] || '未设置'
}
// 三态下拉第一档的文案：登记了环境变量名的项留空才回退到部署环境，
// 没登记的（如 independent_lock）留空就是回退内置缺省值，不能混为一谈
const unsetLabel = computed(() =>
  props.item.envVar ? '未设置（用部署环境的值）' : '未设置（用缺省值）',
)
function secretPlaceholder(item: SettingItem) {
  // 只读敏感项不走密码框（走普通只读框显示后端预览），这里只服务于可写项
  if (item.readOnly) return '只读'
  return item.configured ? '已配置，留空则不修改' : '未设置，留空则用部署环境的值'
}

// 整型控件的值：草稿是字符串，转不成数字时给 0（与旧实现一致）
const numVal = computed(() => {
  const n = Number(props.modelValue)
  return Number.isFinite(n) ? n : 0
})

// 服务端路径选择器弹窗：仅 pathKind 项才有「浏览…」按钮；打开后由子组件自行拉取目录。
// pickerReady 让弹窗**首次点开时才挂载**：设置页主列表 + 配置向导共有 6 个 pathKind 项，
// 若全都常驻，页面一进来就在 body 里塞 6 套 el-dialog（各自带遮罩层），既浪费又给
// 层叠 / 锁滚动留隐患。挂载后不再销毁，关掉时保留状态，二次打开是瞬时的。
const pickerVisible = ref(false)
const pickerReady = ref(false)
function openPicker() {
  pickerReady.value = true
  pickerVisible.value = true
}
</script>

<style scoped>
.row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding: 12px 0;
  border-bottom: 1px solid #f2f6fc;
}
.row:last-child {
  border-bottom: none;
}
.row-main {
  min-width: 0;
  flex: 1;
}
.row-label {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.label-text {
  font-weight: 600;
  color: #303133;
  font-size: 14px;
}
.label-key {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  color: #c0c4cc;
}
/* 悬浮说明徽标：常驻不占版面，指针停留/聚焦时由 el-tooltip 弹出 */
.row-hint {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #c0c4cc;
  color: #fff;
  font-size: 10px;
  line-height: 1;
  cursor: help;
  user-select: none;
  transition: background 0.15s ease;
}
.row-hint:hover,
.row-hint:focus {
  background: #409eff;
  outline: none;
}
.row-desc {
  margin-top: 4px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}
.env-hint {
  margin-left: 6px;
  color: #c0c4cc;
}
/* 行内校验错误：紧贴说明下方，红色小字。不放大字号、不加图标——
   向导的目标是「改完就能过」，错误提示只需要被看见，不需要被强调 */
.row-error {
  margin-top: 4px;
  color: #f56c6c;
  font-size: 12px;
  line-height: 1.6;
}
.row-control {
  flex: none;
  width: 260px;
  display: flex;
  justify-content: flex-end;
}
.text-input {
  width: 100%;
}
/* 路径类配置：文本框 + 「浏览…」按钮并排，按钮不再独占一行挤压标签 */
.path-control {
  display: flex;
  align-items: center;
  gap: 6px;
}
.path-input {
  flex: 1;
}
.path-browse {
  flex: none;
}
.secret-control {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}
/* 依赖联动：当前选项下暂不需要的项整行置灰（只读项另由控件 disabled 表达） */
.row-dim {
  opacity: 0.55;
}
/* 手机端：控制区改为独占一行，避免标签被挤压 */
@media (max-width: 767px) {
  .row {
    flex-direction: column;
    gap: 10px;
  }
  .row-control {
    width: 100%;
    justify-content: flex-start;
  }
}
</style>

<!-- 说明弹层样式：el-tooltip 的 popper 挂在 body 下，scoped 样式够不到，必须用非 scoped 块。
     限宽让长说明折行成短段，而不是横穿全屏的一长行（与 SetupInstaller 同一模式）。 -->
<style>
.si-hint-popper {
  max-width: 300px;
  text-align: left;
  line-height: 1.7;
}
</style>
