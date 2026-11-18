<template>
    <el-card>
    <!-- 内嵌在「小程序设置」页签里时，重新加载由父组件切换栏右侧的按钮统一提供，
         本页不再重复渲染自己的页签栏。独立访问时才需要它。 -->
    <div v-if="!props.embedded" class="page-bar">
      <h3>小程序页面样式</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">设置小程序首页背景、视频下方公司名称与简介的大小/显示、首页「经典业绩」与「业务服务」的先后顺序、业绩分类与精选案例的展示数量与效果、「封面图名称」（封面图下是否再显示名称），以及两个详情页是否显示「更新于」。</p>

    <!-- 表单宽度与「访客先看到谁」对齐（720）：560 时「从素材库选择」按钮的文字会被
         挤成两行，撑满卡片后这个窄表单也显得孤零零 -->
    <el-form label-width="120px" style="max-width: 720px">
      <!-- 模块顺序是产品约定：首页背景 → 板块顺序 → 详情页时间显示。
           三块都带标题，调整顺序时别只挪表单项、把标题留在原地。
           HomeLayout.spec.ts 有源码级守门钉死这个顺序。 -->
      <el-divider content-position="left">首页背景</el-divider>

      <el-form-item label="背景图">
        <UploadField v-model="bgImage" library-type="background" :multiple="false" tip="建议竖幅 750×1334 以上，JPG/PNG，≤10MB" />
        <div class="bg-hint">
          从素材库「背景」分类可直接选用内置高端商务背景；留空则保持小程序默认灰底。
        </div>
      </el-form-item>

      <el-form-item label="遮罩浓度">
        <div class="mask-row">
          <el-slider v-model="bgMask" :min="0" :max="100" :step="5" :disabled="!bgImage" style="flex: 1" />
          <span class="mask-val">{{ bgMask }}%</span>
        </div>
        <div class="bg-hint">背景图较花时调高遮罩压暗，保证白色卡片与文字清晰可读。</div>
      </el-form-item>

      <!-- 顶部主色浓度滑杆已移到「风格模板」页签（与品牌配色同页，调色即看即时预览），
           保存也归「保存风格」。后端 PUT 是部分更新，本页不提交该字段就不会冲掉它。 -->

      <el-form-item v-if="bgImage" label="效果预览">
        <!-- 点击整块预览可放大查看原图（遮罩/卡片只是叠加效果层，原图可能较花看不清细节） -->
        <div class="bg-preview clickable" @click="bgViewer = true">
          <img class="bg-preview-img" :src="bgImage" alt="背景预览" />
          <div class="bg-preview-mask" :style="{ opacity: bgMask / 100 }" />
          <div class="bg-preview-card">白色内容卡片</div>
        </div>
      </el-form-item>

      <!-- 首页视频下方「公司名称 / 简介文字」的展示档位：四档 = 大 / 中 / 小 / 关闭。
           控件从「公司信息」「公司简介」页搬来，是因为客户按呈现效果反复调整这两处，
           归到「页面样式」页签和背景/顺序/时间档位放一起，改版式时一处就能配齐。
           物理列仍在 company_infos / company_profiles，本页经 home-layout 端点窄更新，
           不提交时保持原值（指针语义），不会冲掉别处字段。 -->
      <el-divider content-position="left">首页文字展示</el-divider>

      <p class="desc desc-tight">
        控制小程序首页视频下方两行文字的<strong>大小与是否显示</strong>。「关闭」时小程序端会移除该行节点、
        不再占用屏幕空间，方便随时调整首页留白。默认「大」与升级前一致。
      </p>

      <el-form-item label="公司名称">
        <el-radio-group v-model="companyNameScale">
          <el-radio value="large">大</el-radio>
          <el-radio value="medium">中</el-radio>
          <el-radio value="small">小</el-radio>
          <el-radio value="hidden">关闭（不显示公司名）</el-radio>
        </el-radio-group>
        <div class="bg-hint">
          只控制首页视频下方的<b>公司名一行</b>（及其分隔线），不影响顶栏简称与后台标识。
        </div>
      </el-form-item>

      <el-form-item label="简介文字">
        <el-radio-group v-model="introFontScale">
          <el-radio value="large">大</el-radio>
          <el-radio value="medium">中</el-radio>
          <el-radio value="small">小</el-radio>
          <el-radio value="hidden">关闭（不显示简介）</el-radio>
        </el-radio-group>
        <div class="bg-hint">
          只影响首页的<b>简介文字一段</b>，不改公司名与其它文字。关闭后首页不再渲染简介，
          腾出的空间给下面的板块。
        </div>
      </el-form-item>

      <el-divider content-position="left">板块顺序</el-divider>

      <el-form-item label="谁在前">
        <el-radio-group v-model="firstBlock">
          <el-radio value="achievement">业绩在前，服务在后</el-radio>
          <el-radio value="service">服务在前，业绩在后</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="当前顺序">
        <el-tag v-for="b in order" :key="b" class="order-tag">{{ blockLabel(b) }}</el-tag>
      </el-form-item>

      <!-- 业绩分类数量 / 精选案例数量与循环效果：都是首页「经典业绩」板块的呈现偏好，
           紧挨板块顺序放，管理员一次就能配齐「谁在前、各显多少、怎么动」。 -->
      <el-divider content-position="left">业绩与案例展示</el-divider>

      <p class="desc desc-tight">
        控制首页「经典业绩」板块的分类图标墙长度、精选案例每页铺几张（均分整行），以及精选案例的循环呈现方式。
        实际精选案例少于设置值时按实际数量显示，不会补空位。
      </p>

      <el-form-item label="业绩分类数量">
        <el-input-number v-model="categoryLimit" :min="0" :max="20" :step="1" controls-position="right" />
        <div class="bg-hint">0 = 首页不显示业绩分类图标墙（精选案例与「更多案例」入口照旧）；最多 20 个。</div>
      </el-form-item>

      <el-form-item label="精选案例数量">
        <el-input-number v-model="featuredCount" :min="0" :max="3" :step="1" controls-position="right" />
        <div class="bg-hint">0 = 不显示精选；1/2/3 = 每页并排 1/2/3 张铺满整行（一张铺满、两张各半、三张各三分之一）。精选多于每页张数时，选轮播效果才能翻看后面的页。</div>
      </el-form-item>

      <el-form-item label="循环呈现效果">
        <el-radio-group v-model="featuredEffect">
          <el-radio value="static">静态平铺</el-radio>
          <el-radio value="fade">淡入淡出</el-radio>
          <el-radio value="scroll">横向滚动</el-radio>
          <el-radio value="flip">立体翻转</el-radio>
        </el-radio-group>
        <div class="bg-hint">只有一张精选案例时始终静态显示；轮播自动循环，点图仍进案例详情。</div>
      </el-form-item>

      <el-divider content-position="left">封面图名称</el-divider>

      <p class="desc desc-tight">
        当前的封面图（业绩分类方图、业务服务竖版卡）<strong>已经把名称画在图片里</strong>，所以默认不在图下再写一遍。
        如果你换成了不带文字的封面图，打开对应开关，名称就会显示在图片下方。
      </p>

      <!-- 两个开关分开：分类图与服务卡常常不是同一批素材风格，
           典型情况是「分类换了素图、服务卡仍是带字卡」，共用一个开关会逼客户二选一。 -->
      <el-form-item label="业绩分类名称">
        <el-switch v-model="showCategoryName" active-text="显示" inactive-text="不显示" />
        <div class="bg-hint">打开后，每个业绩分类的封面图下方显示该分类名称；没有封面图时始终显示名称。</div>
      </el-form-item>

      <el-form-item label="业务服务名称">
        <el-switch v-model="showServiceName" active-text="显示" inactive-text="不显示" />
        <div class="bg-hint">打开后，每张业务服务卡片下方显示该服务标题；没有封面图时始终显示标题。</div>
      </el-form-item>

      <el-divider content-position="left">详情页时间显示</el-divider>

      <p class="desc desc-tight">
        详情页原本固定显示「更新于 + 最后一次保存时间」。这个时间是运营保存内容的时刻，
        不是案例交付时间——批量整理内容时全站案例会显示同一天，精确到秒对访客也偏噪音。
        可以调成只到日，或直接关掉。
      </p>

      <!-- 案例与服务分开控制：案例带「交付年份」语义、服务更像常青内容，
           两者该不该显示时间往往不是一个答案，共用一个开关会逼客户二选一。 -->
      <el-form-item label="项目案例详情">
        <el-radio-group v-model="projectTimeMode">
          <el-radio value="hidden">不显示</el-radio>
          <el-radio value="date">只显示日期</el-radio>
          <el-radio value="minute">显示到分</el-radio>
        </el-radio-group>
        <div class="bg-hint">小程序效果：{{ modePreview(projectTimeMode) }}</div>
      </el-form-item>

      <el-form-item label="业务服务详情">
        <el-radio-group v-model="serviceTimeMode">
          <el-radio value="hidden">不显示</el-radio>
          <el-radio value="date">只显示日期</el-radio>
          <el-radio value="minute">显示到分</el-radio>
        </el-radio-group>
        <div class="bg-hint">小程序效果：{{ modePreview(serviceTimeMode) }}</div>
      </el-form-item>

      <el-form-item>
        <!-- 内嵌时保存由父组件切换栏右侧按钮统一提供；独立访问才在本页渲染。
             「清除背景」是跟背景字段绑定的便捷操作，留在表单内不动。 -->
        <el-button v-if="!props.embedded" type="primary" :loading="saving" @click="save">保存</el-button>
        <el-button v-if="bgImage" link @click="clearBg">清除背景</el-button>
      </el-form-item>
    </el-form>
    <ImageViewer v-if="bgViewer && bgImage" :url-list="[bgImage]" @close="bgViewer = false" />
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { ref, computed, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getHomeLayout, saveHomeLayout, type DetailTimeMode, type FeaturedEffect } from '@/api/content'
import UploadField from '@/components/UploadField.vue'
import ImageViewer from '@/components/ImageViewer.vue'

// embedded=true：被「小程序设置」内嵌在「名片页面样式」页签里时，不再重复自己的标题。
const props = defineProps<{ embedded?: boolean }>()

const BLOCKS = ['achievement', 'service']
const loading = ref(false)
const saving = ref(false)
const firstBlock = ref<string>('achievement')
// 首页背景（可选增强）：空串=不设背景；遮罩 0~100
const bgImage = ref('')
// 背景原图全屏查看器（点击效果预览块打开）
const bgViewer = ref(false)
const bgMask = ref(0)
// 顶部品牌主色浓度已移到「风格模板」页签管理（与品牌配色同页，调色即看即时预览）。
// 本页不读写、不提交该字段；后端 PUT 是部分更新，故保存本页不会冲掉它。
// 首页业绩分类 / 精选案例的展示配置（0049）。缺省取上限与 static，与升级前一致。
const categoryLimit = ref(20)
const featuredCount = ref(3)
const featuredEffect = ref<FeaturedEffect>('static')
// 封面图下方是否再显示名称（0050）。缺省 false = 名称已在图内，与升级前完全一致。
// 两个开关分开：分类图与服务卡常常不是同一批素材风格。
const showCategoryName = ref(false)
const showServiceName = ref(false)
// 详情页「更新于」档位：案例与服务各一个，互不影响
const projectTimeMode = ref<DetailTimeMode>('date')
const serviceTimeMode = ref<DetailTimeMode>('date')
// 首页视频下方两行文字的展示档位（大/中/小/关闭）。缺省 large 保持存量零视觉突变；
// 历史行该列可能为空，load 时用 || 'large' 兜底，避免 radio 落到未选中态。
const companyNameScale = ref<string>('large')
const introFontScale = ref<string>('large')

// 把档位翻译成「小程序上会看到什么」，比让客户理解 hidden/date/minute 直观。
// 时间取当下（不是真实内容时间，只演示格式），够用且不会误导成「这是某条内容的时间」。
function modePreview(mode: string): string {
  if (mode === 'hidden') return '不显示时间'
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  if (mode === 'minute') return `更新于 ${date} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `更新于 ${date}`
}

const order = computed(() => {
  const rest = BLOCKS.filter((b) => b !== firstBlock.value)
  return [firstBlock.value, ...rest]
})

function blockLabel(b: string) {
  return b === 'achievement' ? '经典业绩' : '业务服务'
}

async function load() {
  loading.value = true
  try {
    const d = await getHomeLayout()
    if (d && d.order && d.order.length) firstBlock.value = d.order[0]
    bgImage.value = d?.bgImage || ''
    bgMask.value = d?.bgMask || 0
    // ?? 而非 ||：0（隐藏分类网格 / 隐藏精选案例）都是合法值
    categoryLimit.value = d?.categoryLimit ?? 20
    featuredCount.value = d?.featuredCount ?? 3
    featuredEffect.value = d?.featuredEffect || 'static'
    // 用 === true 而不是 ||：后端可能下发 false，|| 会把它当假值再兜一遍虽然结果相同，
    // 但显式比较能挡住字符串 "false" 被当真值的情况。
    showCategoryName.value = d?.showCategoryName === true
    showServiceName.value = d?.showServiceName === true
    projectTimeMode.value = d?.projectTimeMode || 'date'
    serviceTimeMode.value = d?.serviceTimeMode || 'date'
    companyNameScale.value = d?.companyNameScale || 'large'
    introFontScale.value = d?.introFontScale || 'large'
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    // 背景与板块顺序一起提交：bgImage 传空串即清空（后端区分「未提供」与「空串」）。
    // 顶部浓度归「风格模板」页签管理，本页不提交（后端 PUT 部分更新，不会冲掉它）；
    // 两个时间档位全量带上（不传后端会保持原值，但这里本就都渲染出来了，
    // 全量提交更符合「所见即所存」）。
    await saveHomeLayout({
      order: order.value,
      bgImage: bgImage.value,
      bgMask: bgMask.value,
      categoryLimit: categoryLimit.value,
      featuredCount: featuredCount.value,
      featuredEffect: featuredEffect.value,
      showCategoryName: showCategoryName.value,
      showServiceName: showServiceName.value,
      projectTimeMode: projectTimeMode.value,
      serviceTimeMode: serviceTimeMode.value,
      companyNameScale: companyNameScale.value,
      introFontScale: introFontScale.value,
    })
    ElMessage.success('已保存小程序展示设置')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

function clearBg() {
  bgImage.value = ''
  bgMask.value = 0
}

onMounted(load)

// 内嵌在「小程序设置」时，父组件用 ref 调这里的 load/save 来驱动切换栏右侧按钮的
// loading 态；首页样式页无「未填完不可保存」的限制，故不暴露 disabled 判定。
defineExpose({ load, save, loading, saving })
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
.desc {
  color: #606266;
  font-size: 13px;
}
/* 夹在两组表单之间的说明段：上下留白交给 el-form-item，自身别再撑开 */
.desc-tight {
  margin: 0 0 8px;
  line-height: 1.7;
}
/* 内嵌时只剩「重新加载」按钮，别让它贴左 */
.page-bar.only-action {
  justify-content: flex-end;
}
.order-tag {
  margin-right: 8px;
}
.bg-hint {
  margin-top: 6px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}
.mask-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
.mask-val {
  width: 44px;
  flex-shrink: 0;
  color: #606266;
  font-size: 13px;
  text-align: right;
}
/* 效果预览：模拟小程序首页——背景铺底 + 遮罩 + 白色卡片浮起 */
.bg-preview {
  position: relative;
  width: 160px;
  height: 284px;
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid #ebeef5;
}
.bg-preview.clickable {
  cursor: zoom-in;
}
.bg-preview-img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.bg-preview-mask {
  position: absolute;
  inset: 0;
  background: #000;
}
.bg-preview-card {
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: 14px;
  padding: 10px 0;
  text-align: center;
  font-size: 12px;
  color: #333;
  background: rgba(255, 255, 255, 0.95);
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}
</style>
