<template>
    <el-card>
    <!-- 内嵌在「小程序设置」页签里时，重新加载由父组件切换栏右侧的按钮统一提供，
         本页不再重复渲染自己的页签栏。独立访问时才需要它。 -->
    <div v-if="!props.embedded" class="page-bar">
      <h3>小程序页面样式</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <p class="desc">设置小程序首页背景、视频下方公司名称与简介的大小/显示、名片右上角 LOGO 的大小与位置、首页「经典业绩」与「业务服务」的先后顺序、业绩分类与精选案例的展示数量与效果、「封面图名称」（封面图下是否再显示名称），以及两个详情页是否显示「更新于」。</p>

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
        <div class="bg-hint">
          数值就是黑色遮罩的不透明度：0% 为原图、25% 为叠一层 25% 不透明的黑、100% 全黑。
          背景图较花时调高可压暗，保证白色卡片与文字清晰可读。它不改变图片本身，只改变上面这层黑。
        </div>
      </el-form-item>

      <!-- 顶部主色浓度滑杆已移到「风格模板」页签（与品牌配色同页，调色即看即时预览），
           保存也归「保存风格」。后端 PUT 是部分更新，本页不提交该字段就不会冲掉它。 -->

      <el-form-item v-if="bgImage" label="效果预览">
        <!-- 按真机首页版式复刻的示意：背景铺满含顶部，黑遮罩夹在背景与卡片之间。
             只放中性占位内容，不含任何真实企业资料；不是真机截图，最终以真机为准。
             点击整块可放大看原图（遮罩会压暗细节）。 -->
        <div class="bg-preview clickable" @click="bgViewer = true">
          <img class="bg-preview-img" :src="bgImage" alt="背景预览" />
          <div class="bg-preview-mask" :style="{ opacity: bgMask / 100 }" />
          <div class="bp-ui">
            <div class="bp-nav">
              <span class="bp-me"><i class="bp-me-icon" />我的</span>
              <span class="bp-name">企业简称</span>
            </div>
            <div class="bp-card bp-person">
              <i class="bp-avatar" />
              <span class="bp-person-lines"><i /><i /><i /></span>
            </div>
            <div class="bp-video"><i class="bp-play" /></div>
            <div class="bp-card bp-block">
              <span class="bp-sec-title">{{ firstBlock === 'service' ? '业务服务' : '经典业绩' }}</span>
              <span class="bp-grid"><i v-for="n in 4" :key="n" /></span>
            </div>
            <div class="bp-card bp-block">
              <span class="bp-sec-title">{{ firstBlock === 'service' ? '经典业绩' : '业务服务' }}</span>
              <span class="bp-grid"><i v-for="n in 4" :key="n" /></span>
            </div>
            <div class="bp-actions"><span>一键拨号</span><span>存入通讯录</span></div>
          </div>
        </div>
        <div class="bg-hint">按真机首页版式绘制的示意图（非真机截图）；背景含顶部整幅铺满，卡片浮在遮罩之上。拖动遮罩浓度可即时看深浅变化。</div>
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

      <el-divider content-position="left">名片 LOGO</el-divider>

      <p class="desc desc-tight">
        名片卡右上角的企业 LOGO 原来是固定尺寸、固定位置：一张<strong>正方形或竖版</strong>的图会被缩到框内居中，
        左右各留一大块空白，看起来像「浮在偏左的位置」，小尺寸图尤其明显。这两组档位就是用来调它的。
      </p>

      <el-form-item label="LOGO 大小">
        <el-radio-group v-model="logoSize">
          <el-radio value="small">小</el-radio>
          <el-radio value="medium">中（默认）</el-radio>
          <el-radio value="large">大</el-radio>
        </el-radio-group>
        <div class="bg-hint">
          「中」就是加这个设置前的样子（高 44rpx、区块宽 150rpx），升级后没有任何变化。
        </div>
      </el-form-item>

      <el-form-item label="LOGO 位置">
        <el-radio-group v-model="logoAlign">
          <el-radio value="left">靠左</el-radio>
          <el-radio value="center">居中（默认）</el-radio>
          <el-radio value="right">靠右</el-radio>
        </el-radio-group>
        <div class="bg-hint">
          只影响<strong>方块 / 竖版</strong> LOGO；<strong>横版长条</strong> LOGO 本来就会撑满整块区域，三档看不出差别。
          想在右上角再往右挪一点，选「靠右」。
        </div>
      </el-form-item>

      <el-form-item label="效果预览">
        <div>
          <!-- 预览必须按「当前风格」摆姿态。LOGO 区块在卡片里的落点由风格决定：
               标准/浅墨贴着卡片右边缘、暖色换行到姓名下方居中、青色因头像在右而落在中部。
               只画一种姿态，暖色 / 青色的客户就是对着一个不存在的落点调档位。 -->
          <div class="logo-preview">
            <!-- 用方块占位模拟「正方形 LOGO」——正是会被压缩、最能看出位置差别的那种。
                 区块与方块的尺寸按真实比例（1rpx = 0.5px @375）换算，别为了好看放大，
                 否则客户按预览调完到真机上会不一致；卡宽与头像只是示意。 -->
            <div class="lp-card" :class="`lp-layout-${logoLayout}`">
              <span class="lp-avatar" />
              <span class="lp-info">
                <span class="lp-line1">
                  <span class="lp-name-group">
                    <span class="lp-name">姓名</span>
                    <span class="lp-title">职位</span>
                  </span>
                  <span class="lp-slot" :class="[`lp-slot-${logoSize}`, `lp-align-${logoAlign}`]">
                    <span class="lp-img" :class="`lp-img-${logoSize}`" />
                  </span>
                </span>
              </span>
            </div>
          </div>
          <div class="bg-hint">
            当前风格：<strong>{{ logoStyleLabel }}</strong>。灰字「职位」与姓名同一行，彩色块代表方块 LOGO，虚线框是 LOGO 区块本身。
            <br />
            区块在卡片里的落点由「风格模板」决定（标准/浅墨贴右边缘、暖色换到姓名下方居中、青色落在头像左侧），
            本页只调区块<strong>内部</strong>的左 / 中 / 右。
          </div>
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

      <el-form-item label="精选案例裁切比例">
        <el-radio-group v-model="featuredRatio">
          <el-radio value="none">原图比例</el-radio>
          <el-radio value="16:9">16:9</el-radio>
          <el-radio value="4:3">4:3</el-radio>
          <el-radio value="2:1">2:1</el-radio>
          <el-radio value="1:1">1:1</el-radio>
        </el-radio-group>
        <div class="bg-hint">
          默认「原图比例」不裁切。选比例后封面图按该比例居中裁切显示：以图片短边贴合容器，
          长边两端超出部分均匀裁掉——适合把不同尺寸的案例图统一成整齐的版式。
        </div>
      </el-form-item>

      <el-divider content-position="left">封面图名称</el-divider>

      <p class="desc desc-tight">
        当前的封面图（业绩分类方图、精选案例图、业务服务竖版卡）<strong>已经把名称画在图片里</strong>，所以默认不在图下再写一遍。
        如果你换成了不带文字的封面图，打开对应开关，名称就会显示在图片下方。
      </p>

      <!-- 两个开关分开：分类图与服务卡常常不是同一批素材风格，
           典型情况是「分类换了素图、服务卡仍是带字卡」，共用一个开关会逼客户二选一。
           精选案例是第三块独立素材（0065），同样各自一个开关。 -->
      <el-form-item label="业绩分类名称">
        <el-switch v-model="showCategoryName" active-text="显示" inactive-text="不显示" />
        <div class="bg-hint">打开后，每个业绩分类的封面图下方显示该分类名称；没有封面图时始终显示名称。</div>
      </el-form-item>

      <el-form-item label="精选案例名称">
        <el-switch v-model="showFeaturedName" active-text="显示" inactive-text="不显示" />
        <div class="bg-hint">打开后，每张精选案例的封面图下方显示案例名称；没有封面图时始终显示名称。</div>
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
import {
  getHomeLayout,
  saveHomeLayout,
  type DetailTimeMode,
  type FeaturedEffect,
  type FeaturedRatio,
  type LogoAlign,
  type LogoSize,
} from '@/api/content'
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
// 精选案例封面裁切比例：缺省 none = 按原图比例（加配置前行为），四档比例走固定容器 + 居中裁切
const featuredRatio = ref<FeaturedRatio>('none')
// 封面图下方是否再显示名称（0050）。缺省 false = 名称已在图内，与升级前完全一致。
// 两个开关分开：分类图与服务卡常常不是同一批素材风格。
const showCategoryName = ref(false)
const showFeaturedName = ref(false)
const showServiceName = ref(false)
// 详情页「更新于」档位：案例与服务各一个，互不影响
const projectTimeMode = ref<DetailTimeMode>('date')
const serviceTimeMode = ref<DetailTimeMode>('date')
// 首页视频下方两行文字的展示档位（大/中/小/关闭）。缺省 large 保持存量零视觉突变；
// 历史行该列可能为空，load 时用 || 'large' 兜底，避免 radio 落到未选中态。
const companyNameScale = ref<string>('large')
const introFontScale = ref<string>('large')
// 名片卡右上角 LOGO 的尺寸 / 位置档位（0053）。缺省 medium / center =
// 加配置前写死的 150×44 + aspectFit 居中；历史行该列可能为空，load 时兜底。
const logoSize = ref<LogoSize>('medium')
const logoAlign = ref<LogoAlign>('center')
// 当前风格（本页不提交，只用来给 LOGO 预览摆对姿态）。风格在「风格模板」页签改，
// 保存成功后由 MpSettings 调 refreshStyle() 只重读这一个值——直接调 load() 会把
// 客户在本页还没保存的编辑冲掉。
const LOGO_STYLE_LABELS: Record<string, string> = {
  default: '标准均衡',
  ink: '浅墨',
  warm: '暖色',
  teal: '青色',
}
// 与 app.wxss 的 .wc-style-* 逐条对应，别凭印象写：
//   default / ink → ec-head 是 row，ec-line1 用 space-between，区块贴卡片右边缘；
//   warm          → ec-head 列向居中、ec-info 宽 100%，.ec-logo-slot 换成 margin-top: 12rpx；
//   teal          → ec-head 是 row-reverse、ec-info 让出 margin-right: 24rpx，区块落在头像左侧。
const LOGO_STYLE_LAYOUTS: Record<string, string> = {
  default: 'row',
  ink: 'row',
  warm: 'stack',
  teal: 'reverse',
}
const styleId = ref('default')
const logoStyleLabel = computed(() => LOGO_STYLE_LABELS[styleId.value] || LOGO_STYLE_LABELS.default)
const logoLayout = computed(() => LOGO_STYLE_LAYOUTS[styleId.value] || 'row')

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
    featuredRatio.value = d?.featuredRatio || 'none'
    // 用 === true 而不是 ||：后端可能下发 false，|| 会把它当假值再兜一遍虽然结果相同，
    // 但显式比较能挡住字符串 "false" 被当真值的情况。
    showCategoryName.value = d?.showCategoryName === true
    showFeaturedName.value = d?.showFeaturedName === true
    showServiceName.value = d?.showServiceName === true
    projectTimeMode.value = d?.projectTimeMode || 'date'
    serviceTimeMode.value = d?.serviceTimeMode || 'date'
    companyNameScale.value = d?.companyNameScale || 'large'
    introFontScale.value = d?.introFontScale || 'large'
    // 历史行这两列可能为空（0053 之前建的），兜底成缺省档，否则 radio 会一个都不选中。
    logoSize.value = (d?.logoSize || 'medium') as LogoSize
    logoAlign.value = (d?.logoAlign || 'center') as LogoAlign
    styleId.value = d?.styleId || 'default'
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
      featuredRatio: featuredRatio.value,
      showCategoryName: showCategoryName.value,
      showFeaturedName: showFeaturedName.value,
      showServiceName: showServiceName.value,
      projectTimeMode: projectTimeMode.value,
      serviceTimeMode: serviceTimeMode.value,
      companyNameScale: companyNameScale.value,
      introFontScale: introFontScale.value,
      logoSize: logoSize.value,
      logoAlign: logoAlign.value,
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

// 只重读「当前风格」这一个值，供 MpSettings 在「风格模板」保存成功后调用：
// LOGO 预览的姿态跟着风格走，客户刚换完风格回来看到的必须是新姿态。
// 刻意不调 load()——那会把本页还没保存的编辑（背景图、板块顺序、档位）一起冲掉。
async function refreshStyle() {
  try {
    const d = await getHomeLayout()
    styleId.value = d?.styleId || 'default'
  } catch {
    /* 拦截器已提示；预览退回上一次的风格，不影响本页保存 */
  }
}

onMounted(load)

// 内嵌在「小程序设置」时，父组件用 ref 调这里的 load/save 来驱动切换栏右侧按钮的
// loading 态；首页样式页无「未填完不可保存」的限制，故不暴露 disabled 判定。
defineExpose({ load, save, loading, saving, refreshStyle })
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
/* 名片 LOGO 效果预览：按 1rpx = 0.5px（375 视口）换算真实比例，
   放大只会让客户按预览调完、到真机上对不上。
   虚线框是「LOGO 区块」本身——横版长条 LOGO 会撑满它，三档才看不出差别；
   彩色方块代表方块 LOGO。
   卡宽与头像是示意（真机卡片内容宽 ≈ 700rpx，按 1:2 会撑到 350px），
   但区块与方块的像素必须与真机严格 1:2，由 home_logo_style.spec 守着。 */
.logo-preview {
  width: 240px;
  padding: 10px 12px;
  background: #fff;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.06);
}
.lp-card {
  display: flex;
  align-items: center;
}
.lp-avatar {
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border-radius: 50%;
  background: #e8f0fe;
}
.lp-info {
  flex: 1;
  min-width: 0;
  padding-left: 6px;
}
.lp-line1 {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.lp-name-group {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}
.lp-title {
  font-size: 12px;
  color: #6b7a89;
  white-space: nowrap;
}
.lp-name {
  font-size: 17px; /* 34rpx */
  font-weight: 700;
  color: #222;
}
/* 三种落点姿态，逐条对齐 app.wxss 的 .wc-style-*：
   row     = 标准 / 浅墨：ec-head 是 row，区块贴卡片右边缘
   stack   = 暖色：ec-head 列向居中，.ec-logo-slot 换成 margin-top: 12rpx（=6px）
   reverse = 青色：ec-head 是 row-reverse，ec-info 让出 margin-right: 24rpx（=12px），区块落在头像左侧 */
.lp-layout-stack {
  flex-direction: column;
  align-items: center;
}
.lp-layout-stack .lp-info {
  width: 100%;
  padding-left: 0;
}
.lp-layout-stack .lp-line1 {
  flex-direction: column;
  align-items: center; /* 与真机 .wc-style-warm .ec-line1 同值：整组居中，组内仍横排 */
  justify-content: center;
}
.lp-layout-stack .lp-slot {
  margin: 6px 0 0;
}
.lp-layout-reverse {
  flex-direction: row-reverse;
}
.lp-layout-reverse .lp-info {
  padding-left: 0;
  padding-right: 12px;
}
.lp-slot {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  margin-left: 8px; /* 16rpx */
  border: 1px dashed #dcdfe6;
  box-sizing: border-box;
}
/* 区块宽度：110 / 150 / 190 rpx */
.lp-slot-small {
  width: 55px;
  height: 18px;
}
.lp-slot-medium {
  width: 75px;
  height: 22px;
}
.lp-slot-large {
  width: 95px;
  height: 26px;
}
.lp-align-left {
  justify-content: flex-start;
}
.lp-align-center {
  justify-content: center;
}
.lp-align-right {
  justify-content: flex-end;
}
/* 方块 LOGO：高 = 18 / 22 / 26 rpx×0.5，正方形，所以宽高相等 */
.lp-img {
  display: block;
  background: linear-gradient(135deg, #409eff, #79bbff);
  border-radius: 2px;
}
.lp-img-small {
  width: 18px;
  height: 18px;
}
.lp-img-medium {
  width: 22px;
  height: 22px;
}
.lp-img-large {
  width: 26px;
  height: 26px;
}
/* 效果预览：按真机首页版式复刻的中性占位示意图——
   背景整幅铺底（含顶部）+ 黑遮罩 + 半透明白卡片 + 悬浮操作条。
   以前只有一张图 + 一个白方块，客户看不出深浅是否合适（2026-11-25 用户反馈）。 */
.bg-preview {
  position: relative;
  width: 180px;
  height: 320px;
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid #ebeef5;
  background: #f2f4f7;
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
.bp-ui {
  position: absolute;
  inset: 0;
}
.bp-nav {
  position: relative;
  height: 34px;
  padding: 8px 8px 0;
}
.bp-me {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 6px;
  font-size: 9px;
  color: #333;
  background: rgba(255, 255, 255, 0.82);
  border-radius: 10px;
}
.bp-me-icon {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #409eff;
}
.bp-name {
  position: absolute;
  left: 0;
  right: 0;
  top: 9px;
  text-align: center;
  font-size: 10px;
  font-weight: 500;
  color: #fff;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.65);
}
.bp-card {
  margin: 5px 6px 0;
  padding: 6px 7px;
  background: rgba(255, 255, 255, 0.95);
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.12);
}
.bp-person {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 46px;
  box-sizing: border-box;
}
.bp-avatar {
  flex: none;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: #d9e4f2;
}
.bp-person-lines {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.bp-person-lines i {
  height: 4px;
  border-radius: 2px;
  background: #d7dce5;
}
.bp-person-lines i:nth-child(1) { width: 72%; }
.bp-person-lines i:nth-child(2) { width: 46%; }
.bp-person-lines i:nth-child(3) { width: 86%; }
.bp-video {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 74px;
  margin: 5px 6px 0;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.12);
}
.bp-play {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #409eff;
  position: relative;
}
.bp-play::after {
  content: "";
  position: absolute;
  left: 9px;
  top: 6px;
  border-left: 7px solid #fff;
  border-top: 5px solid transparent;
  border-bottom: 5px solid transparent;
}
.bp-sec-title {
  display: block;
  width: 44px;
  height: 4px;
  margin-bottom: 5px;
  border-radius: 2px;
  background: #9fb4cc;
}
.bp-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 5px;
}
.bp-grid i {
  height: 20px;
  border-radius: 4px;
  background: #e6eef8;
  border: 1px solid #dce7f4;
}
.bp-actions {
  position: absolute;
  left: 6px;
  right: 6px;
  bottom: 7px;
  display: flex;
  gap: 6px;
}
.bp-actions span {
  flex: 1;
  padding: 5px 0;
  text-align: center;
  font-size: 9px;
  border-radius: 12px;
  background: #fff;
  color: #606266;
  border: 1px solid #dcdfe6;
}
.bp-actions span:first-child {
  background: #409eff;
  color: #fff;
  border-color: #409eff;
}
</style>
