<template>
  <el-card>
    <p class="desc">
      为小程序选择一套内置风格：「整体风格」把主色、卡片圆角、按钮形态与投影质感一并切换；
      「品牌配色」按预设色板只换主色，版式沿用经典蓝，用于对齐企业自有 VI 主色。
      两个系列都随小程序版本内置，后台只记录「选了哪一款」，
      保存后访客端即时生效，不需要重新上传代码或提交微信审核。
      下方是<strong>按真机首页复刻版式的完整首页风格示意，不是真机截图</strong>：
      版式、区块与按钮都和小程序一致；<strong>本产品是通用平台，示意图全部使用中性占位信息，
      不含任何真实企业客户资料</strong>（企业名、LOGO、头像、联系方式与简介均为占位）。
      图形只取通用行业图标与照片，换模板时只有主色、页面底色、圆角、按钮与投影会变，
      照片与插画跟真机上一样保持原样。
      它不体现真机字体、系统状态栏、安全区与首页背景图；
      卡片内可上下滚动查看完整首页，悬停卡片后点右上角「查看大图」可放大查看，最终观感请以真机验收为准。
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

    <!-- 两个系列的分组由服务端下发的 series 决定，前端不猜 id 前缀：
         加一款品牌配色时后端清单会自动带 series，这里不需要跟着改。
         旧后端不下发 series 时全部归入「整体风格」，不会凭空少一组卡片。 -->
    <div v-for="group in groups" :key="group.key" class="theme-group">
      <div class="group-head">
        <h4>{{ group.title }}</h4>
        <span class="group-hint">{{ group.hint }}</span>
      </div>
      <div class="theme-grid">
        <div
          v-for="t in group.items"
          :key="t.id"
          class="theme-card"
          :class="{ selected: selected === t.id, locked: isLocked(t) }"
          @click="pick(t)"
        >
          <!-- 真实预览图由 scripts/gen_theme_previews.py 从 miniprogram/app.wxss 的
               token 生成，和访客端共用同一套配色 / 圆角 / 投影，不是手画的第二套样式。
               底层 CSS 示意常驻：图片加载中、404、或新增模板忘了补图时都不会留空白；
               右下角「示意图」角标让降级可见，而不是悄悄退回旧样式。
               .layout-<id> 只给「整体风格」款画版式差异；品牌配色款版式沿用经典蓝，
               没有专属规则，示意自然落在基线样式上。 -->
          <div class="theme-preview" :class="'layout-' + t.id">
            <div class="preview-fallback" :style="{ background: t.previewSoft }">
              <div class="preview-nav" :style="{ background: t.previewPrimary }" />
              <div class="preview-card">
                <div class="preview-line" :style="{ background: t.previewPrimary }" />
                <div class="preview-line short" />
                <div class="preview-btn" :style="{ background: t.previewPrimary }" />
              </div>
              <span class="fallback-tag">示意图</span>
            </div>
            <!-- 长图放在滚动窗口里：默认露出手机窗口高度，管理员滚动看完整首页；
                 窗口比例由 .theme-preview 控制，与长图自身比例（375x1210）无关。 -->
            <div class="preview-scroll">
              <img
                v-if="!failedPreviews[t.id]"
                class="preview-shot"
                :src="previewUrl(t.id)"
                :alt="t.name + ' 小程序首页预览'"
                decoding="async"
                @error="onPreviewError(t.id)"
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
              v-if="!failedPreviews[t.id]"
              type="button"
              class="preview-zoom-btn"
              @click.stop="openZoom(t.id)"
            >查看大图</button>
          </div>
          <div class="theme-meta">
            <div class="theme-name">
              {{ t.name }}
              <el-tag v-if="t.advanced" size="small" type="warning" class="adv-tag">高级</el-tag>
              <span v-if="selected === t.id" class="current">使用中</span>
            </div>
            <div class="theme-desc">{{ t.description }}</div>
            <div v-if="isLocked(t)" class="theme-lock">未开通 · 暂不可选</div>
          </div>
        </div>
      </div>
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
import { getHomeLayout, saveHomeLayout, type ThemeTemplate } from '@/api/content'

// embedded=true：被「小程序设置」页签内嵌时不再重复自己的标题（当前仅用于约定，
// 标题行与操作按钮已上移到父组件「小程序设置」的页签切换栏，本组件不再自行渲染）。
defineProps<{ embedded?: boolean }>()

const loading = ref(false)
const saving = ref(false)
const selected = ref('default')
const templates = ref<ThemeTemplate[]>([])
const templateEntitled = ref(true)

// 分组真值来自服务端下发的 series，不靠 id 前缀猜。旧后端（升级前的二进制）
// 不下发 series 时一律归入「整体风格」组：宁可都挤在一组，也不能让卡片凭空消失。
const styleTemplates = computed(() => templates.value.filter((t) => t.series !== 'brand'))
const brandTemplates = computed(() => templates.value.filter((t) => t.series === 'brand'))

interface ThemeGroup {
  key: string
  title: string
  hint: string
  items: ThemeTemplate[]
}

const groups = computed<ThemeGroup[]>(() => {
  const out: ThemeGroup[] = []
  if (styleTemplates.value.length) {
    out.push({
      key: 'style',
      title: '整体风格',
      hint: '主色、卡片圆角、按钮形态与投影质感一并切换',
      items: styleTemplates.value,
    })
  }
  if (brandTemplates.value.length) {
    out.push({
      key: 'brand',
      title: '品牌配色',
      hint: '按预设色板只换主色，版式沿用经典蓝；用于对齐企业自有 VI 主色',
      items: brandTemplates.value,
    })
  }
  return out
})

// 预览图是随前端版本发布的静态资源：加载失败时移除该张 <img>，露出底层示意。
// 用「id -> 失败」而不是一个全局开关，某一张图坏了不能把其余三张一起降级。
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
function isLocked(t: ThemeTemplate): boolean {
  return t.advanced && !templateEntitled.value && t.id !== selected.value
}

function pick(t: ThemeTemplate) {
  if (isLocked(t)) return
  selected.value = t.id
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
    // 后端已归一过未知 id；这里再兜一次空值，连老后端也不会出现「一个都没选中」
    selected.value = d?.templateId || 'default'
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 只提交 templateId：三个页签各存各的，后端 PUT 对未提供的字段保持原值。
    // 整份表单回传会把别的页签刚改过的值一起写回去，属于典型的跨页签覆盖。
    await saveHomeLayout({ templateId: selected.value })
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
.entitle-alert {
  margin-bottom: 16px;
}
.theme-group + .theme-group {
  margin-top: 40px;
}
.group-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid #ebeef5;
}
.group-head h4 {
  margin: 0;
  flex: none;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.group-hint {
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}
.theme-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 28px;
  margin-top: 8px;
}
.theme-card {
  width: 230px;
  border: 2px solid #e4e7ed;
  border-radius: 12px;
  overflow: hidden;
  cursor: pointer;
  background: #fff;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.theme-card.selected {
  border-color: #1a8cff;
  box-shadow: 0 0 0 2px rgba(26, 140, 255, 0.14);
}
/* 未开通的款式降透明度，但仍保留文字说明——置灰不解释会让客户以为页面坏了 */
.theme-card.locked {
  cursor: not-allowed;
  opacity: 0.62;
}
/* 预览容器是**手机窗口**而非整张长图：窄于 1:3.2 的长图，默认只露出约两屏高度，
   完整首页靠内部滚动查看——比把整张长图压扁在卡片里更贴近真机预览。
   比例必须落在手机竖屏范围（约 1:1.6 ~ 1:2.3），超出会被 theme.spec.ts 判红；
   退回 375/1210 长条也会变红。border-bottom 仍是「窗口到此结束」的硬边界：
   窗口底部（图内底栏白底）下面的名称区也是白底，没有这条线客户会以为
   按钮和模板说明是同一块内容。长图本身的画布比例由 gen_theme_previews.py 的
   375x1210 决定，与窗口比例无关。 */
.theme-preview {
  position: relative;
  aspect-ratio: 375 / 750;
  overflow: hidden;
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
/* 查看大图浮层：position:fixed 不受 .theme-card 的 overflow:hidden 影响。
   预览图是 750x2420 的长图，不能再用 max-height 把它压成一条细线——
   固定面板宽度、内部纵向滚动，客户才能按真实比例看清首页每一屏。 */
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
   松石青 = 中圆角 + 极轻投影。
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
/* 名称 / 说明区用浅灰底与预览图分开：长图底部的白色操作栏紧贴卡片下缘，
   若这里也是纯白，图内「一键拨号 / 存入通讯录」会和模板名称、说明视觉混在一起。 */
.theme-meta {
  padding: 12px 14px 14px;
  background: #f8fafc;
}
.theme-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}
.adv-tag {
  transform: scale(0.9);
}
.current {
  font-size: 12px;
  font-weight: 400;
  color: #1a8cff;
}
.theme-desc {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  min-height: 38px;
}
.theme-lock {
  margin-top: 4px;
  font-size: 12px;
  color: #e6a23c;
}
</style>
