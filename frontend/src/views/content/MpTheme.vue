<template>
  <el-card>
    <p class="desc">
      小程序的外观分两步选：第 1 步「<strong>整体风格</strong>」决定首页各模块的布局
      （分类列数、精选大图排法、服务卡密度、封面高度与圆角），并自带一套默认色；
      第 2 步「<strong>品牌配色</strong>」只在所选风格之上替换主色，版式不变，用于对齐企业自有 VI 主色。
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
      高级款暂不可新选，访客端会临时回落到经典蓝；已经选中的高级款会为您保留，续费后自动恢复。
      已选中的款式仍可继续使用或切回经典蓝。如需开通请联系服务商升级授权。
    </el-alert>

    <div class="theme-workbench">
      <!-- ===== 固定预览区：整页只有这一个，随 (styleId, brandColorId) 组合切换 =====
           放在 sticky 栏里，右侧选项再多、页面再长，预览始终停在同一位置，
           客户不用把十几张卡片的缩略图横向比来比去。 -->
      <aside class="preview-pane">
        <div class="pane-head">
          <span class="pane-title">首页预览</span>
          <span class="pane-combo">{{ currentName }}</span>
        </div>
        <!-- 真实预览图由 scripts/gen_theme_previews.py 从 app.wxss 的 token + 风格布局
             规则生成，和访客端共用同一套配色 / 圆角 / 模块排布，不是手画的第二套样式。
             底层 CSS 示意常驻：图片加载中、404、或新增组合忘了补图时都不会留空白；
             右下角「示意图」角标让降级可见，而不是悄悄退回旧样式。
             .layout-<id> 只给「整体风格」款画版式差异；品牌配色款版式沿用经典蓝，
             没有专属规则，示意自然落在基线样式上。 -->
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
               窗口比例由 .theme-preview 控制，与长图自身比例无关。 -->
          <div class="preview-scroll">
            <img
              v-if="!failedPreviews[currentId]"
              class="preview-shot"
              :src="previewUrl(currentId)"
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
              <p>在所选风格之上只替换主色，版式沿用该风格；用于对齐企业自有 VI 主色。</p>
            </div>
          </div>
          <!-- 树形归属：P1 起 8 款 brand-* 全挂在 default 风格下，其它风格没有可叠加的色系。
               不解释就置灰，客户会以为页面坏了；切风格时也必须自动清空，否则保存时后端
               BelongsToStyle 会直接 code=1，客户只看到「保存失败」却不知道为什么。 -->
          <div v-if="styleId !== DEFAULT_STYLE" class="step-disabled">
            「{{ styleName }}」使用自己的默认色，暂无可用品牌配色；切回「经典蓝」即可叠加色系。
          </div>
          <div v-else-if="!brandTemplates.length" class="step-disabled">
            当前服务端版本未提供品牌配色清单，请升级服务端后再使用。
          </div>
          <div class="color-grid">
            <div
              class="color-option"
              :class="{ selected: brandColorId === null, disabled: styleId !== DEFAULT_STYLE }"
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
                disabled: styleId !== DEFAULT_STYLE,
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
          <img class="zoom-shot" :src="previewUrl(zoomId)" :alt="zoomName + ' 小程序首页预览大图'" />
        </div>
      </div>
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, computed, onMounted } from 'vue'
import { getHomeLayout, saveHomeLayout, type HomeLayoutData, type ThemeTemplate } from '@/api/content'

// embedded=true：被「小程序设置」页签内嵌时不再重复自己的标题（当前仅用于约定，
// 标题行与操作按钮已上移到父组件「小程序设置」的页签切换栏，本组件不再自行渲染）。
defineProps<{ embedded?: boolean }>()

// 两轴真值与后端 internal/mptemplate 一致：brand-* 全归 default 风格。
// 旧后端只下发兼容派生列 templateId 时，按前缀反推，升级零突变。
const DEFAULT_STYLE = 'default'
const BRAND_PREFIX = 'brand-'
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
const defaultTemplate = computed(() => templates.value.find((t) => t.id === DEFAULT_STYLE))
const defaultPrimary = computed(() => defaultTemplate.value?.previewPrimary || FALLBACK_PRIMARY)

// 12 张 PNG 仍按 12 个合法组合命名：4 款风格各用自己 id，8 款品牌色各用自己 id。
// brandColorId 非空时 styleId 必为 default（pickStyle 自动清空 + load 反推都保证），
// 所以这里直接取色系 id，与后端 DeriveTemplateID 的口径一致。
const currentId = computed(() => brandColorId.value || styleId.value)
const currentTemplate = computed(() => templates.value.find((t) => t.id === currentId.value))
const currentName = computed(() => currentTemplate.value?.name || currentId.value)
const currentPreviewPrimary = computed(() => currentTemplate.value?.previewPrimary || defaultPrimary.value)
const currentPreviewSoft = computed(() => currentTemplate.value?.previewSoft || FALLBACK_SOFT)
const styleName = computed(
  () => styleTemplates.value.find((t) => t.id === styleId.value)?.name || styleId.value
)
const brandName = computed(() => {
  if (!brandColorId.value) return '默认色'
  return brandTemplates.value.find((t) => t.id === brandColorId.value)?.name || brandColorId.value
})
const isCurrentInUse = computed(
  () => styleId.value === savedStyleId.value && brandColorId.value === savedBrandColorId.value
)

// 预览图是随前端版本发布的静态资源：加载失败时移除该张 <img>，露出底层示意。
// 用「id -> 失败」而不是一个全局开关，某一张图坏了不能把其余组合一起降级。
const failedPreviews = ref<Record<string, boolean>>({})

// public/ 下的文件由 Vite 原样拷到产物根目录；带 BASE_URL 前缀，部署到子路径时
// 不会误打到站点根下的 /theme-previews。encodeURIComponent 防 id 拼出非预期路径，
// 真出异常也就是这张图 404 → 走兜底，不会静默显示另一款模板的图。
function previewUrl(id: string): string {
  return `${import.meta.env.BASE_URL}theme-previews/${encodeURIComponent(id)}.png`
}

function onPreviewError(id: string) {
  failedPreviews.value = { ...failedPreviews.value, [id]: true }
  // 大图用的是同一个 URL：这张图坏了，别让浮层停在一片空白上
  if (zoomId.value === id) zoomId.value = ''
}

// 查看大图只改一个 id，不复制图片数据；关闭时一并清掉，避免下次打开闪出上一款。
const zoomId = ref('')
const zoomName = computed(() => templates.value.find((t) => t.id === zoomId.value)?.name || '')
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
  // 树形归属：非 default 风格下没有可叠加的品牌色系。切风格必须清空，否则
  // 保存时后端 BelongsToStyle 会以 code=1 拒绝，而客户只看到一句「保存失败」。
  if (t.id !== DEFAULT_STYLE) brandColorId.value = null
  syncZoom()
}

function pickBrand(t: ThemeTemplate | null) {
  if (styleId.value !== DEFAULT_STYLE) return
  if (t && isBrandLocked(t)) return
  brandColorId.value = t ? t.id : null
  syncZoom()
}

// 大图浮层也是「预览显示区」的一部分：开着浮层时切风格 / 色系，图必须跟着换，
// 否则客户以为自己放大看的还是刚选中的那一款，是没人报错的静默错位。
function syncZoom() {
  if (zoomId.value) zoomId.value = currentId.value
}

// 旧后端（P1 之前）只下发 templateId 兼容派生列，按 (style, brand) 拆回两轴。
// 两轴齐备时以两轴为准；缺 styleId 才看 templateId，避免旧值把新值盖掉。
function splitThemeId(d?: HomeLayoutData | null): { styleId: string; brandColorId: string | null } {
  if (d?.styleId) {
    return { styleId: d.styleId, brandColorId: d.brandColorId || null }
  }
  const id = d?.templateId || DEFAULT_STYLE
  return id.startsWith(BRAND_PREFIX)
    ? { styleId: DEFAULT_STYLE, brandColorId: id }
    : { styleId: id, brandColorId: null }
}

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
    const sel = splitThemeId(d)
    styleId.value = sel.styleId
    brandColorId.value = sel.brandColorId
    savedStyleId.value = sel.styleId
    savedBrandColorId.value = sel.brandColorId
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
   沉稳墨 = 近直角 + 细描边代替投影；暖金棕 = 大圆角 + 胶囊按钮；
   松石青 = 中圆角 + 极轻投影。真实预览图已按同一风格出图，这里只服务降级兜底。
   加新模板必须补一条 .layout-<id>，theme.spec.ts 会逐个对账。 */
.layout-ink .preview-nav,
.layout-ink .preview-line,
.layout-ink .preview-btn { border-radius: 2px; }
.layout-ink .preview-card {
  border-radius: 2px;
  box-shadow: 0 0 0 1px rgba(61, 75, 92, 0.22);
}
.layout-warm .preview-nav,
.layout-warm .preview-line { border-radius: 8px; }
.layout-warm .preview-card {
  border-radius: 16px;
  box-shadow: 0 6px 14px rgba(143, 99, 32, 0.16);
}
.layout-warm .preview-btn { border-radius: 999px; }
.layout-teal .preview-nav,
.layout-teal .preview-line { border-radius: 5px; }
.layout-teal .preview-card {
  border-radius: 6px;
  box-shadow: 0 1px 5px rgba(15, 118, 110, 0.14);
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
