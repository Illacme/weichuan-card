<template>
  <el-card>
    <p class="desc">
      小程序的外观分两步选：第 1 步「<strong>整体风格</strong>」按布局特色区分，决定首页各模块的布局
      （分类列数、精选大图排法、服务卡密度、封面高度与圆角），并自带一套默认色；
      第 2 步「<strong>品牌配色</strong>」只在所选风格之上替换主色，版式不变，用于对齐企业自有 VI 主色。
      <strong>4 款风格 × 9 档颜色 = 36 种组合</strong>：每款风格都能叠加全部品牌配色，色板在四款风格上完全一致。
      风格与色系都随小程序版本内置，后台只记录「选了哪一款」，保存后访客端即时生效，
      不需要重新上传代码或提交微信审核。
      左侧预览区<strong>固定在一个位置</strong>，随上面两步的选择实时切换，不必在十几张卡片间来回找图。
      下方是<strong>按真机首页复刻版式的完整首页风格示意，不是真机截图</strong>：
      区块与按钮都和小程序一致；<strong>本产品是通用平台，示意图全部使用中性占位信息，
      不含任何真实企业客户资料</strong>（企业名、LOGO、头像、联系方式与简介均为占位）。
      布局取自小程序 <code>app.wxss</code> 的同一套风格规则，色系只换主色、照片与插画跟真机上一样保持原样。
      它不体现真机字体、系统状态栏、安全区与首页背景图；
      预览内可上下滚动查看完整首页，悬停后点右上角「查看大图」可放大查看，最终观感请以真机验收为准。
    </p>

    <!-- 授权到期 / 未开通时客户必须知道：不是保存失败，是这一档没买；
         且访客端会临时回落经典蓝，避免他看着后台的「使用中」以为已经生效。 -->
    <el-alert
      v-if="!templateEntitled"
      type="warning"
      :closable="false"
      show-icon
      title="当前授权未开通高级风格模板"
      class="entitle-alert"
    >
      高级款暂不可新选，访客端会临时回落到标准均衡默认款；已经选中的高级款会为您保留，续费后自动恢复。
      已选中的款式仍可继续使用或切回标准均衡。如需开通请联系服务商升级授权。
    </el-alert>

    <div class="theme-workbench">
      <!-- ===== 固定预览区：整页只有这一个，随 (styleId, brandColorId) 组合切换 =====
           放在 sticky 栏里，右侧选项再多、页面再长，预览始终停在同一位置，
           客户不用把十几张卡片的缩略图横向比来比去。 -->
      <aside class="preview-pane">
        <div class="pane-head">
          <span class="pane-title">首页预览</span>
          <span class="pane-combo">{{ styleName }}</span>
        </div>
        <!-- 真实预览图由 scripts/gen_theme_previews.py 从 app.wxss 的 token + 风格布局
             规则生成，和访客端共用同一套配色 / 圆角 / 模块排布，不是手画的第二套样式。
             底层 CSS 示意常驻：图片加载中、404、或新增组合忘了补图时都不会留空白；
             右下角「示意图」角标让降级可见，而不是悄悄退回旧样式。
             .layout-<id> 只给「整体风格」款画版式差异；品牌配色不另给版式规则，
             示意自然跟着当前风格走，不会出现「换色顺手换掉圆角」。 -->
        <div class="theme-preview" :class="'layout-' + styleId">
          <div class="preview-fallback" :style="{ background: currentPreviewSoft }">
            <div class="preview-nav" :style="{ background: currentPreviewPrimary }" />
            <div class="preview-card">
              <div class="preview-line" :style="{ background: currentPreviewPrimary }" />
              <div class="preview-line short" />
              <div class="preview-btn" :style="{ background: currentPreviewPrimary }" />
            </div>
            <span class="fallback-tag">示意图</span>
          </div>
          <!-- 长图放在滚动窗口里：默认露出手机窗口高度，管理员滚动看完整首页；
               窗口比例由 .theme-preview 控制，与长图自身比例无关。
               这里取缩略图（460px 宽 / 100~210KB），原图只在「查看大图」时才拉。 -->
          <div class="preview-scroll">
            <img
              v-if="!failedPreviews[currentId]"
              class="preview-shot"
              :src="thumbUrl(currentId)"
              :alt="currentName + ' 小程序首页预览'"
              decoding="async"
              @error="onPreviewError(currentId)"
            />
          </div>
          <!-- 悬停预览图时才显示的标注，默认不遮挡示意图观感：客户最容易把「按 token 复刻的示意图」
               直接当成真机页面效果去验收，这个误解不会报错，只能靠标注说清；
               常驻反而干扰选色对比，故改为悬停浮现（降级提示 fallback-tag 仍常显）。 -->
          <span class="preview-badge">风格示意 · 非真机截图</span>
          <!-- 缩略卡只有 230px 宽，完整首页在其中看不清细节；默认隐藏、悬停预览图才浮现，
               避免常驻遮挡选色对比。图降级时不给这个入口，否则点开还是空白；
               .stop 是必须的，不然「查看大图」会顺手把这款选中。 -->
          <button
            v-if="!failedPreviews[currentId]"
            type="button"
            class="preview-zoom-btn"
            @click.stop="openZoom(currentId)"
          >查看大图</button>
        </div>
        <div class="pane-foot">
          <div class="pane-combo-line">
            当前组合：<strong>{{ styleName }}</strong> ＋ <strong>{{ brandName }}</strong>
            <span v-if="isCurrentInUse" class="current">使用中</span>
          </div>
          <div class="pane-hint">切换右侧选项即可换图；点页签右上角的「保存风格」才会生效。</div>
        </div>
      </aside>

      <!-- ===== 两步选择：先定布局，再叠颜色 ===== -->
      <section class="picker-pane">
        <div class="picker-step">
          <div class="step-head">
            <span class="step-no">1</span>
            <div>
              <h4>整体风格</h4>
              <p>决定首页四个模块怎么排：分类列数、精选大图、服务卡密度、封面高度与圆角。</p>
            </div>
          </div>
          <div class="option-grid">
            <div
              v-for="t in styleTemplates"
              :key="t.id"
              class="option-card"
              :class="{ selected: styleId === t.id, locked: isStyleLocked(t) }"
              @click="pickStyle(t)"
            >
              <span class="option-swatch" :style="{ background: t.previewPrimary }" />
              <div class="option-body">
                <div class="option-name">
                  {{ t.name }}
                  <el-tag v-if="t.advanced" size="small" type="warning" class="adv-tag">高级</el-tag>
                </div>
                <div class="option-desc">{{ t.description }}</div>
                <div v-if="isStyleLocked(t)" class="theme-lock">未开通 · 暂不可选</div>
              </div>
            </div>
          </div>
        </div>

        <div class="picker-step">
          <div class="step-head">
            <span class="step-no">2</span>
            <div>
              <h4>品牌配色</h4>
              <p>在所选风格之上只替换主色，版式沿用该风格；9 档颜色在任意风格下都能选，用于对齐企业自有 VI 主色。</p>
            </div>
          </div>
          <!-- P5：树形归属已废止。4 款风格都能叠加全部 8 款色系，色板在四款上完全一致；
               这里只保留「清单没下发」的升级提示，不再按风格禁用，也不在切风格时
               清空色系——那会让客户刚对齐好的 VI 主色无声消失。 -->
          <div v-if="!brandTemplates.length" class="step-disabled">
            当前服务端版本未提供品牌配色清单，请升级服务端后再使用。
          </div>
          <div class="color-grid">
            <div
              class="color-option"
              :class="{ selected: brandColorId === null }"
              @click="pickBrand(null)"
            >
              <span class="color-swatch" :style="{ background: defaultPrimary }" />
              <span>默认色</span>
            </div>
            <div
              v-for="t in brandTemplates"
              :key="t.id"
              class="color-option"
              :class="{
                selected: brandColorId === t.id,
                locked: isBrandLocked(t),
              }"
              @click="pickBrand(t)"
            >
              <span class="color-swatch" :style="{ background: t.previewPrimary }" />
              <span>{{ t.name }}</span>
              <el-tag v-if="t.advanced" size="small" type="warning" class="adv-tag">高级</el-tag>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 查看大图：用组件内 fixed 浮层而不是 Teleport，单测可直接断言，
         position:fixed 也不受 .theme-card 的 overflow:hidden 裁剪。
         点遮罩空白处或「关闭」都能收起，图片本身用 contain 不裁切。 -->
    <div v-if="zoomId" class="preview-zoom" @click.self="closeZoom">
      <div class="zoom-panel">
        <div class="zoom-head">
          <span class="zoom-title">{{ zoomName }} · 首页风格示意</span>
          <span class="zoom-note">风格示意 · 非真机截图</span>
          <el-button size="small" @click="closeZoom">关闭</el-button>
        </div>
        <div class="zoom-scroll">
          <img class="zoom-shot" :src="fullUrl(zoomId)" :alt="zoomName + ' 小程序首页预览大图'" />
        </div>
      </div>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, computed, onMounted } from 'vue'
import { getHomeLayout, saveHomeLayout, type ThemeTemplate } from '@/api/content'

// embedded=true：被「小程序设置」页签内嵌时不再重复自己的标题（当前仅用于约定，
// 标题行与操作按钮已上移到父组件「小程序设置」的页签切换栏，本组件不再自行渲染）。
defineProps<{ embedded?: boolean }>()

// P5 两轴真值与后端 internal/mptemplate 一致：任意风格 × 任意已知色系都合法，
// 色系不再隶属于某个风格。后端直接下发 styleId / brandColorId 两轴，存储真值唯一
// （0043 已删除 template_id 兼容派生列，端上旧缓存经小程序 splitTheme 零迁移读取）。
const DEFAULT_STYLE = 'default'
const FALLBACK_PRIMARY = '#1a8cff'
const FALLBACK_SOFT = '#e8f1fb'

const loading = ref(false)
const saving = ref(false)
const styleId = ref(DEFAULT_STYLE)
const brandColorId = ref<string | null>(null)
// 「使用中」是后端已保存的组合，不是界面上刚点过的组合：未保存时不能显示使用中，
// 否则客户会以为已经生效。load 时记一次，save 成功后同步。
const savedStyleId = ref(DEFAULT_STYLE)
const savedBrandColorId = ref<string | null>(null)
const templates = ref<ThemeTemplate[]>([])
const templateEntitled = ref(true)

// 分组真值来自服务端下发的 series，不靠 id 前缀猜。旧后端（升级前的二进制）
// 不下发 series 时一律归入「整体风格」组，色系区给出升级提示，卡片不会凭空消失。
const styleTemplates = computed(() => templates.value.filter((t) => t.series !== 'brand'))
const brandTemplates = computed(() => templates.value.filter((t) => t.series === 'brand'))
const activeStyle = computed(() => styleTemplates.value.find((t) => t.id === styleId.value))
const activeBrand = computed(() => brandTemplates.value.find((t) => t.id === brandColorId.value))
// 「默认色」色卡要显示**当前风格**的默认主色，不是全局 default 的蓝色：
// 紧凑信息 + 默认色是深灰，画成蓝色会让客户以为自己点错了。
const defaultPrimary = computed(() => activeStyle.value?.previewPrimary || FALLBACK_PRIMARY)

// P5：36 个组合各有一张 PNG，文件名 <styleId>--<brandColorId 或 default>.png。
// 旧 12 款平铺模板 id 装不下 36 个组合，预览图必须按两轴组合命名，绝不能回退到旧 id。
function comboId(s: string, b: string | null): string {
  return `${s}--${b || 'default'}`
}
const currentId = computed(() => comboId(styleId.value, brandColorId.value))
const styleName = computed(() => activeStyle.value?.name || styleId.value)
const brandName = computed(() => {
  if (!brandColorId.value) return '默认色'
  return activeBrand.value?.name || brandColorId.value
})
// 组合名按 id 反解，不用「当前选中值」：大图浮层打开后切组合，标题必须跟着换。
function comboName(id: string): string {
  const [s, b] = id.split('--')
  const sn = styleTemplates.value.find((t) => t.id === s)?.name || s
  const bn = b === 'default'
    ? '默认色'
    : brandTemplates.value.find((t) => t.id === b)?.name || b
  return `${sn} ＋ ${bn}`
}
const currentName = computed(() => comboName(currentId.value))
const currentPreviewPrimary = computed(
  () => activeBrand.value?.previewPrimary || activeStyle.value?.previewPrimary || FALLBACK_PRIMARY
)
const currentPreviewSoft = computed(
  () => activeBrand.value?.previewSoft || activeStyle.value?.previewSoft || FALLBACK_SOFT
)
const isCurrentInUse = computed(
  () => styleId.value === savedStyleId.value && brandColorId.value === savedBrandColorId.value
)

// 预览图是随前端版本发布的静态资源：加载失败时移除该张 <img>，露出底层示意。
// 用「id -> 失败」而不是一个全局开关，某一张图坏了不能把其余组合一起降级。
const failedPreviews = ref<Record<string, boolean>>({})

// public/ 下的文件由 Vite 原样拷到产物根目录；带 BASE_URL 前缀，部署到子路径时
// 不会误打到站点根下的 /theme-previews。encodeURIComponent 防 id 拼出非预期路径，
// 真出异常也就是这张图 404 → 走兜底，不会静默显示另一款模板的图。
//
// **一图两档**（scripts/gen_theme_previews.py 同时产出）：
//   · `<id>.thumb.png` 460px 宽、100~210KB —— 只给下面 230px 宽的卡片窗口用；
//   · `<id>.png`       750px 宽、0.8~1.8MB —— 只在「查看大图」浮层里才请求。
// 别把卡片也指到大图：切一次风格就要回源拉 1~2MB（本目录是 no-cache，每次真下），
// 而且 750×5144 的位图解码约 15MB 内存，老机器切风格会明显卡。这种退化不会报错，
// 只会「感觉有点慢」，所以两档必须分开，并由测试钉住谁用哪一档。
//
// **必须带 `?v=<构建戳>`**：这 36 张图的文件名是「风格--色系」组合键，不含内容 hash，
// 重出图后 URL 一个字都不变。不带版本号时，浏览器与 CDN 会按各自的默认静态缓存
// （线上实测 Cloudflare 给的是 `max-age=14400`）继续喂旧图——管理员在后台切风格 / 色系，
// 看到的却是上一版的版式，而 Network 里全是 200、控制台一个错都没有（典型静默失败，
// 2026-11-12 线上真实反馈「切换风格预览图看不到变化，是不是发布文件不全」即此因，
// 排查结论是文件齐全、字节数与本地一致，纯粹是被缓存住了）。
function previewAssetUrl(id: string, thumb: boolean): string {
  const suffix = thumb ? '.thumb' : ''
  return `${import.meta.env.BASE_URL}theme-previews/${encodeURIComponent(id)}${suffix}.png?v=${__APP_BUILD__}`
}
const thumbUrl = (id: string) => previewAssetUrl(id, true)
const fullUrl = (id: string) => previewAssetUrl(id, false)

function onPreviewError(id: string) {
  failedPreviews.value = { ...failedPreviews.value, [id]: true }
  // 缩略图都取不到，说明这一款根本没出图，别让浮层停在一片空白上。
  // （大图走的是另一个 URL，但缺的是同一款组合，一起收掉才对。）
  if (zoomId.value === id) zoomId.value = ''
}

// 查看大图只改一个 id，不复制图片数据；关闭时一并清掉，避免下次打开闪出上一款。
const zoomId = ref('')
const zoomName = computed(() => (zoomId.value ? comboName(zoomId.value) : ''))
function openZoom(id: string) {
  zoomId.value = id
}
function closeZoom() {
  zoomId.value = ''
}

// 未开通授权时禁止「新选」高级款；但**当前已在用**的那一款不能锁——
// 授权到期后客户仍要能带着它保存别处设置，或主动切回经典蓝（后端同值放行）。
function isStyleLocked(t: ThemeTemplate): boolean {
  return t.advanced && !templateEntitled.value && t.id !== styleId.value
}
function isBrandLocked(t: ThemeTemplate): boolean {
  return t.advanced && !templateEntitled.value && t.id !== brandColorId.value
}

function pickStyle(t: ThemeTemplate) {
  if (isStyleLocked(t)) return
  styleId.value = t.id
  // P5：切风格**保留**已选色系。风格只换布局、统一色板在四款上取值一致，
  // 没有理由把客户刚对齐好的 VI 主色清掉；后端也早已放开跨风格组合。
  syncZoom()
}

function pickBrand(t: ThemeTemplate | null) {
  if (t && isBrandLocked(t)) return
  brandColorId.value = t ? t.id : null
  syncZoom()
}

// 大图浮层也是「预览显示区」的一部分：开着浮层时切风格 / 色系，图必须跟着换，
// 否则客户以为自己放大看的还是刚选中的那一款，是没人报错的静默错位。
function syncZoom() {
  if (zoomId.value) zoomId.value = currentId.value
}

// 后端直接下发 styleId / brandColorId 两轴（0043 已删除 template_id 兼容派生列），
// load 只需原样读两轴，不再需要单列 id 反推。
async function load() {
  loading.value = true
  // 「重新加载」也该给预览图一次重试机会：例如刚补上漏生成的 PNG，
  // 或上一轮只是临时网络抖动，不能让客户一直停在示意图上。
  failedPreviews.value = {}
  zoomId.value = ''
  try {
    const d = await getHomeLayout()
    templates.value = Array.isArray(d?.templates) ? d.templates : []
    templateEntitled.value = d?.templateEntitled !== false
    // 后端直接下发两轴：styleId 缺省回 default，brandColorId 缺失回 null（用风格默认色）。
    styleId.value = d?.styleId || DEFAULT_STYLE
    brandColorId.value = d?.brandColorId || null
    savedStyleId.value = styleId.value
    savedBrandColorId.value = brandColorId.value
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 只提交两轴：三个页签各存各的，后端 PUT 对未提供的字段保持原值。
    // 整份表单回传会把别的页签刚改过的值一起写回去，属于典型的跨页签覆盖。
    // brandColorId 为 null = 用该风格默认色，后端会写 NULL 而不是留下旧色系。
    await saveHomeLayout({ styleId: styleId.value, brandColorId: brandColorId.value })
    savedStyleId.value = styleId.value
    savedBrandColorId.value = brandColorId.value
    ElMessage.success('已保存小程序风格模板')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

// 操作按钮（重新加载 / 保存风格）已上移到父组件「小程序设置」的页签切换栏，
// 通过 ref 调用本组件暴露的 load / save；loading / saving / templates 一并暴露，
// 供父组件驱动按钮的 loading 与 disabled 态。
defineExpose({ load, save, loading, saving, templates })

onMounted(load)
</script>

<style scoped>
.desc {
  color: #606266;
  font-size: 13px;
  line-height: 1.7;
}
.desc code {
  font-size: 12px;
  background: #f4f6f9;
  border-radius: 3px;
  padding: 0 4px;
}
.entitle-alert {
  margin-bottom: 16px;
}
/* 左预览 / 右两步：预览区 sticky 固定在一个位置，右侧选项再长也不用来回滚回去找图 */
.theme-workbench {
  display: flex;
  align-items: flex-start;
  gap: 24px;
}
.preview-pane {
  flex: none;
  width: 280px;
  position: sticky;
  top: 16px;
}
.picker-pane {
  flex: 1;
  min-width: 0;
}
.pane-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
.pane-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.pane-combo {
  font-size: 12px;
  color: #1a8cff;
}
/* 长图底部的白色操作栏紧贴预览窗口下缘：说明区若也是纯白，图内按钮会和
   「当前组合」文字连成一片，客户分不清哪部分属于示意图。浅灰底 + 边框给出硬分隔。 */
.pane-foot {
  margin-top: 10px;
  padding: 8px 10px;
  font-size: 12px;
  color: #606266;
  line-height: 1.6;
  background: #f8fafc;
  border: 1px solid #ebeef5;
  border-radius: 8px;
}
.pane-combo-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}
.pane-hint {
  margin-top: 4px;
  font-size: 11px;
  color: #909399;
}
.current {
  font-size: 12px;
  color: #1a8cff;
}

/* 两步选择 */
.picker-step + .picker-step {
  margin-top: 28px;
}
.step-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid #ebeef5;
}
.step-no {
  flex: none;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #1a8cff;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  text-align: center;
  line-height: 22px;
}
.step-head h4 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.step-head p {
  margin: 2px 0 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}
.option-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 12px;
  margin-top: 12px;
}
.option-card {
  display: flex;
  gap: 10px;
  padding: 12px;
  border: 2px solid #e4e7ed;
  border-radius: 10px;
  background: #fff;
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.option-card.selected {
  border-color: #1a8cff;
  box-shadow: 0 0 0 2px rgba(26, 140, 255, 0.14);
}
/* 未开通的款式降透明度，但仍保留文字说明——置灰不解释会让客户以为页面坏了 */
.option-card.locked {
  cursor: not-allowed;
  opacity: 0.62;
}
.option-swatch {
  flex: none;
  width: 26px;
  height: 26px;
  border-radius: 6px;
  border: 1px solid rgba(0, 0, 0, 0.06);
}
.option-body {
  min-width: 0;
}
.option-name {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.adv-tag {
  transform: scale(0.9);
}
.option-desc {
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
.theme-lock {
  margin-top: 4px;
  font-size: 12px;
  color: #e6a23c;
}
.step-disabled {
  margin-top: 12px;
  padding: 10px 12px;
  font-size: 12px;
  line-height: 1.6;
  color: #909399;
  background: #f8fafc;
  border: 1px solid #ebeef5;
  border-radius: 8px;
}
.color-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 12px;
}
.color-option {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  font-size: 13px;
  color: #303133;
  background: #fff;
  border: 2px solid #e4e7ed;
  border-radius: 8px;
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.color-option.selected {
  border-color: #1a8cff;
  box-shadow: 0 0 0 2px rgba(26, 140, 255, 0.14);
}
.color-option.locked,
.color-option.disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.color-swatch {
  flex: none;
  width: 16px;
  height: 16px;
  border-radius: 4px;
  border: 1px solid rgba(0, 0, 0, 0.06);
}

/* 预览容器是**手机窗口**而非整张长图：窄于 1:3.2 的长图，默认只露出约两屏高度，
   完整首页靠内部滚动查看——比把整张长图压扁在卡片里更贴近真机预览。
   比例必须落在手机竖屏范围（约 1:1.6 ~ 1:2.3），超出会被 theme.spec.ts 判红；
   退回 375/1210 长条也会变红。border-bottom 仍是「窗口到此结束」的硬边界。
   长图本身的画布高度由 gen_theme_previews.py 按风格实测收口，与窗口比例无关。 */
.theme-preview {
  position: relative;
  aspect-ratio: 375 / 750;
  overflow: hidden;
  border: 1px solid #dcdfe6;
  border-radius: 12px;
  border-bottom: 1px solid #dcdfe6;
}
/* 底层示意常驻：img 还在下载 / 404 / 新模板漏补图时都有内容；
   「示意图」角标让降级是可见的，不会没人报错就没人发现。 */
.preview-fallback {
  position: absolute;
  inset: 0;
  padding: 12px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}
.fallback-tag {
  position: absolute;
  right: 8px;
  bottom: 8px;
  font-size: 11px;
  color: #606266;
  background: rgba(255, 255, 255, 0.92);
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 1px 6px;
}
/* 长图滚动窗口：填满手机窗口，超出部分纵向滚动；overscroll-behavior 防止滚动
   穿透到页面。图按原始比例显示（height: auto），不会裁掉下半部，也不会压扁。 */
.preview-scroll {
  position: absolute;
  inset: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
}
.preview-shot {
  display: block;
  width: 100%;
  height: auto;
}
/* 常显标注盖在真实图之上（DOM 顺序在 img 后面 + z-index）：降级时 .fallback-tag
   说「示意图」，正常时这枚说「非真机截图」，两个语义不能合并成一个角标。 */
.preview-badge {
  position: absolute;
  left: 8px;
  top: 8px;
  z-index: 2;
  font-size: 10px;
  line-height: 1.4;
  color: #606266;
  background: rgba(255, 255, 255, 0.92);
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 2px 6px;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.12s ease;
}
.preview-zoom-btn {
  position: absolute;
  right: 8px;
  top: 8px;
  z-index: 3;
  font-size: 10px;
  line-height: 1.4;
  color: #409eff;
  background: rgba(255, 255, 255, 0.94);
  border: 1px solid #d9ecff;
  border-radius: 4px;
  padding: 2px 6px;
  cursor: pointer;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.12s ease;
}
.preview-zoom-btn:hover {
  background: #ecf5ff;
}
/* 角标与查看大图默认隐藏，仅指针悬停预览图（或键盘聚焦）时浮现，
   不再常驻遮挡示意图与选色对比；降级提示 fallback-tag 仍常显，二者语义不同。
   生成脚本的悬停约定由 theme.spec.ts 与本块对账，改回常驻会变红。 */
.theme-preview:hover .preview-badge,
.theme-preview:focus-within .preview-badge,
.theme-preview:hover .preview-zoom-btn,
.theme-preview:focus-within .preview-zoom-btn {
  opacity: 1;
  pointer-events: auto;
}
/* 查看大图浮层：position:fixed 不受 .theme-preview 的 overflow:hidden 影响。
   预览长图不能再用 max-height 把它压成一条细线——固定面板宽度、内部纵向滚动，
   客户才能按真实比例看清首页每一屏。 */
.preview-zoom {
  position: fixed;
  inset: 0;
  z-index: 2000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  box-sizing: border-box;
  background: rgba(0, 0, 0, 0.55);
}
.zoom-panel {
  display: flex;
  flex-direction: column;
  width: min(420px, 92vw);
  max-height: calc(100vh - 48px);
  padding: 12px;
  background: #fff;
  border-radius: 10px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.25);
}
.zoom-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
  margin-bottom: 10px;
}
.zoom-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.zoom-note {
  font-size: 12px;
  color: #909399;
}
.zoom-head .el-button {
  margin-left: auto;
}
.zoom-scroll {
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  border-radius: 6px;
}
.zoom-shot {
  display: block;
  width: 100%;
  height: auto;
}
.preview-nav {
  height: 14px;
  border-radius: 4px;
  opacity: 0.9;
  flex: none;
}
.preview-card {
  margin-top: 12px;
  padding: 12px;
  flex: 1;
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}
.preview-line {
  height: 8px;
  width: 78%;
  border-radius: 4px;
  opacity: 0.85;
}
.preview-line.short {
  margin-top: 8px;
  width: 52%;
  background: #dcdfe6;
  opacity: 1;
}
.preview-btn {
  margin-top: 14px;
  height: 12px;
  width: 44%;
  border-radius: 6px;
}
/* 版式示意（与端上 --wc-r-* token 同向，px 只是后台缩略图尺度）：
   P6 起不只改圆角，还要示意「版式族」——端上名片区已经是
   ink 单列四行 / warm 竖版居中 / teal 头像在右 + 左色块，降级示意必须同向，
   否则真图挂了以后管理员看到的是一张说不上区别的灰卡。
   真实预览图已按同一风格出图，这里只服务降级兜底。
   风格名按布局特色命名、不含颜色词；加新模板必须补一条 .layout-<id>，
   theme.spec.ts 会逐个对账。 */
.layout-ink .preview-nav,
.layout-ink .preview-line,
.layout-ink .preview-btn { border-radius: 2px; }
.layout-ink .preview-card {
  border-radius: 2px;
  box-shadow: 0 0 0 1px rgba(61, 75, 92, 0.22);
  /* 工牌单栏款：三段信息拉通单列，行与行之间加 1px 细线（对应 .ec-line 的 border-bottom） */
  display: flex;
  flex-direction: column;
  align-items: stretch;
}
.layout-ink .preview-line,
.layout-ink .preview-btn { width: 100%; }
.layout-ink .preview-line + .preview-line {
  margin-top: 0;
  padding-top: 8px;
  border-top: 1px solid rgba(61, 75, 92, 0.18);
}
.layout-warm .preview-nav,
.layout-warm .preview-line { border-radius: 8px; }
.layout-warm .preview-card {
  border-radius: 16px;
  box-shadow: 0 6px 14px rgba(143, 99, 32, 0.16);
  /* 竖版居中款：对应端上 .ec-head { flex-direction: column; align-items: center } */
  display: flex;
  flex-direction: column;
  align-items: center;
}
.layout-warm .preview-btn { border-radius: 999px; }
.layout-teal .preview-nav,
.layout-teal .preview-line { border-radius: 5px; }
.layout-teal .preview-card {
  border-radius: 6px;
  box-shadow: 0 1px 5px rgba(15, 118, 110, 0.14);
  /* 左色块侧栏款：对应端上 .emp-card 的左缘通高色块 + 头像移到右侧（内容整体右倾） */
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  border-left: 4px solid rgba(15, 118, 110, 0.75);
}
.layout-teal .preview-btn { border-radius: 10px; }

@media (max-width: 1100px) {
  .theme-workbench {
    flex-direction: column;
  }
  .preview-pane {
    position: static;
    width: 100%;
    max-width: 320px;
  }
}
</style>
