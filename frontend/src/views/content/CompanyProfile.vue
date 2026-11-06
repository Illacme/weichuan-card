<template>
  <el-card>
    <div class="page-bar">
      <h3>公司简介</h3>
      <el-button :icon="Refresh" :loading="loading" @click="load">重新加载</el-button>
    </div>

    <el-form :model="form" label-width="110px" style="max-width: 680px">
      <el-form-item label="简介文字">
        <el-input v-model="form.introText" type="textarea" :rows="5" placeholder="首页简介区文字" />
      </el-form-item>

      <el-form-item label="视频来源">
        <el-select v-model="sourceUi" style="width: 280px">
          <el-option label="不展示视频（显示封面）" value="none" />
          <el-option label="微信视频号（推荐）" value="channels" />
          <el-option label="自托管 MP4（上传或直链）" value="mp4" />
          <el-option label="腾讯视频（插件）" value="tencent" :disabled="!vcap.tencentVideo" />
        </el-select>
        <div v-if="form.videoSource === 'channels'" class="hint hint-good">
          视频存微信服务器、播放走微信 CDN，<b>不产生存储与流量费用</b>，且官方声明该组件无视听资质要求。
          <span class="hint-warn">
            形态说明（微信组件固定行为，均无开关可改）：封面自带<b>视频号名称与推荐角标</b>，无法隐藏；
            跨主体（feed-token）点击播放会<b>拉起视频号页面</b>播放，只有同主体（feedId）才能在原位自动播放。
            要求「原位播放、画面干净」请用<b>自托管 MP4</b>（上传文件，或直接填任意 https mp4 地址）；
            其中「上传到云存储」仅当存储配置为对象存储时可用，本地磁盘存储下只能填直链。
          </span>
        </div>
        <div v-else-if="form.videoSource === 'tencent'" class="hint hint-good">
          视频由腾讯视频托管并走其 CDN，<b>不占您的存储与流量</b>；在页面内<b>原位播放</b>，
          封面<b>不带视频号名称与推荐角标</b>，且能记上播放统计（视频号档官方没有播放事件，记不了）。
          <span class="hint-warn">
            前置条件（缺一不可）：① 在「系统设置 → 功能开关」开启<b>腾讯视频档</b>；
            ② 小程序后台「设置 → 第三方服务 → 插件管理」已添加并授权腾讯视频插件；
            ③ 小程序 app.json 已声明该插件；少任何一项，这里要么选不了、要么保存被拒。
            ④ vid 填<b>腾讯视频公开可播的视频 ID</b>即可（v.qq.com 播放页 `page/` 与 `.html` 之间那段）——<b>您自己创作号、或别人公开的非会员视频都行</b>；插件只认腾讯视频 `vid`，<b>不认任意 mp4/http URL</b>。
            创作号上传分类别选「文化 / 历史」可能放不全片（有用户反馈），建议选「企业 / 商业」类。
          </span>
        </div>
        <div v-else-if="isMp4" class="hint">
          视频由您自己托管，小程序里<b>原位播放、画面干净</b>，也不占微信与腾讯视频的资源。
          <span class="hint-warn">
            存储与流量费用记在您自己的云账号上（视频是名片页里最费流量的资源），视听类目资质需自行具备。
          </span>
        </div>
      </el-form-item>

      <!-- 视频号档：channelId 仅作留档，真正决定播哪条视频的是 feedId -->
      <template v-if="form.videoSource === 'channels'">
        <el-form-item label="视频号 ID">
          <el-input v-model="form.videoChannelId" placeholder="sph 开头，视频号助手首页可查看" />
        </el-form-item>
        <el-form-item label="视频 feedId">
          <el-input v-model="form.videoFeedId" placeholder="视频号助手 → 动态管理 → 复制 feedId" />
          <div class="hint">
            <b>同主体用这个</b>（视频号与小程序同一主体）：可自动播放，从视频号助手复制即长期稳定。
          </div>
        </el-form-item>
        <el-form-item label="feed-token">
          <el-input v-model="form.videoFeedToken" placeholder="跨主体才填：MP 平台开权限后移动端复制" />
          <div class="hint">
            <b>跨主体才需要</b>：官方明确 feed-id「仅同主体时生效」，视频号与我们小程序不同主体时必须改用它。
            代价是<b>强制不能自动播放</b>（用户需点一下）。获取入口有 24 小时窗口且只对操作者本人可见，需由我们协助提取。
            <span class="hint-warn">
              两个硬条件：小程序须为非个人主体；用户微信基础库 ≥ 2.31.1（低版本不认 feed-token，组件报错后会回落成封面图）。
              保存后若小程序只显示封面，多半是 token 不完整或基础库过低。
              组件报错 <code>channel-video:fail 1416106</code> 即「feed-token 跨主体播放失败」：
              ① token 必须是在移动端「在小程序中引用该视频」时<b>选中本小程序</b>生成的（该 token 仅对生成时所指定的小程序生效）；
              ② 存的 token 要完整，正常形如 <code>token/</code> 开头一长串，短于十位多半被截断。
            </span>
            <span v-if="form.videoFeedId.trim()" class="hint-warn">当前已填 feedId，feed-token 不会被使用。</span>
          </div>
        </el-form-item>
      </template>

      <!-- 腾讯视频档：vid 是唯一取片凭证（腾讯视频播放页 URL 末段），插件自带 bindplay
           所以 video_play 埋点也能记上——这是它相对视频号档的第二个好处 -->
      <template v-else-if="form.videoSource === 'tencent'">
        <el-alert v-if="!vcap.tencentVideo" type="warning" :closable="false" show-icon class="cap-alert">
          <template #title>{{ vcap.tencentReason || '当前部署未启用腾讯视频插件' }}</template>
        </el-alert>
        <el-form-item v-if="vcap.tencentVideo" label="视频 vid">
          <el-input v-model="form.videoVid" placeholder="如 n0041aa087e（腾讯视频播放页地址末段）" />
          <div class="hint">
            先把视频上传到<b>腾讯视频</b>并通过审核，取播放页地址里的 vid；未过审的视频播不出来。
            <span class="hint-warn">
              免费版插件片头会插广告（插件形态自带，界面无法关闭），介意的话回到「外部 MP4 直链」档。
            </span>
          </div>
        </el-form-item>
        <!-- 自动播放：腾讯视频插件的 autoplay **默认是 true**（与微信原生 video 相反），
             不显式关掉的话，访客一进名片页就带声音自己播起来。
             默认关（数据库默认 0）：名片页是被动打开的落地页，突然出声是负面体验。 -->
        <el-form-item v-if="vcap.tencentVideo" label="自动播放">
          <el-switch v-model="form.videoAutoplay" />
          <div class="hint">
            打开后访客一进名片页就自动开始播放（<b>带声音</b>）。默认关闭，需要用户点一下播放按钮。
            <span class="hint-warn">
              这一档的自动播放是腾讯视频插件的默认行为（原生 video 默认不自动播），
              所以我们这里必须显式关掉——改完保存，小程序重新进入即可生效。
            </span>
          </div>
        </el-form-item>
      </template>

      <!-- 自托管 MP4 档：「上传到云存储」与「填写直链」在**小程序端完全等价**——
           company-video 里两者是同一个分支，都渲染原生 <video src>，访客看不出区别。
           差别只在「文件怎么放上去」，所以合成一档、内部二选一，
           不再让客户对着两个效果一样的选项猜。存库时仍分别写 upload / url。 -->
      <template v-else-if="isMp4">
        <!-- 历史配置落在不可用环境（以前配了 upload，现在存储退回本地磁盘）：
            不能静默改档，否则一保存就把客户的配置换掉了，只提示并让他自己选。 -->
        <el-alert v-if="mp4Mode === 'upload' && !cap.videoUpload" type="warning" :closable="false" show-icon class="cap-alert">
          <template #title>{{ cap.reason || '当前存储配置不支持视频上传' }}</template>
        </el-alert>

        <el-form-item label="交付方式">
          <el-radio-group v-model="mp4Mode">
            <el-radio value="upload" :disabled="!cap.videoUpload">上传到云存储</el-radio>
            <el-radio value="url">填写 MP4 地址</el-radio>
          </el-radio-group>
          <div v-if="!cap.videoUpload" class="hint hint-warn">
            {{ cap.reason || '当前存储配置不支持视频上传' }}
          </div>
        </el-form-item>

        <!-- 复用通用上传组件（内部优先走直传预签名，失败回退普通上传） -->
        <el-form-item v-if="mp4Mode === 'upload' && cap.videoUpload" label="视频文件">
          <UploadField
            v-model="uploadUrl"
            :multiple="false"
            accept="video/mp4"
            list-type="text"
            hide-library
            :max-m-b="cap.videoMaxMb"
            :tip="`仅支持 mp4，单文件 ≤${cap.videoMaxMb}MB`"
          />
          <div class="hint">
            直传到已配置的对象存储（OSS / COS / MinIO），<b>不经应用服务器</b>，费用记在您自己的云账号上。
            <span class="hint-warn">
              直传由浏览器直接 PUT 到对象存储，属于跨域请求：若对象存储没配跨域规则，会被浏览器拦下并
              <b>回退成经应用服务器中转</b>（大视频会占用服务器带宽）。配置方法见部署文档。
            </span>
          </div>
        </el-form-item>

        <el-form-item v-if="mp4Mode === 'url'" label="MP4 地址">
          <el-input v-model="externalUrl" placeholder="https:// 开头的 mp4 地址" />
          <div class="hint">我们不保存文件。流量费由您的存储服务商收取，视听类目资质需自行具备。</div>
        </el-form-item>

        <!-- 把最终要写进库的地址摆出来，并允许点开验证。
             小程序播不出来时，九成是这个地址本身在浏览器里就打不开
             （CDN 证书失效 / 对象不存在 / 相对路径没拼绝对 / 私有桶没签名）。
             不给这一行，客户只能对着小程序的黑屏 00:00 猜。 -->
        <el-form-item v-if="activeVideoUrl" label="最终播放地址">
          <div class="video-url-check">
            <a v-if="canOpenVideoUrl" :href="activeVideoUrl" target="_blank" rel="noopener noreferrer">{{ activeVideoUrl }}</a>
            <span v-else>{{ activeVideoUrl }}</span>
            <div class="hint">
              点开应当能直接播放。这里打不开，小程序里也一定播不了（先在浏览器排除证书与地址问题）。
              相对路径会在小程序端自动拼上服务域名，请确认该域名下确实有这个文件。
              如果对象存储控制台里文件名带目录前缀（如 aitel/xxx.mp4），「文件访问基址」也必须带上同样的目录前缀，
              否则这里拼出的地址会少一层目录、直接 404；改完配置只影响新上传，已保存的地址要重新上传才会更新。
            </div>
          </div>
        </el-form-item>
      </template>

      <!-- 兜底：四种视频档都可能播不出来，配置形态保持一致。
           但两条地址各占一个槽：channels / tencent 用 form.videoUrl（该档下它没有别的用途），
           upload / url 用 form.videoBackupUrl —— videoUrl 在主源保存时会被 videoUrlToSave()
           覆盖，复用就会把用户填的备用地址悄悄丢掉。 -->
      <el-form-item v-if="needFallback" label="兜底 MP4">
        <el-checkbox v-model="form.videoFallbackUrl">
          {{ isMp4 ? '主 MP4 加载失败时切换到备用地址' : '主源播放失败时改用 MP4' }}
        </el-checkbox>
        <el-input
          v-if="form.videoFallbackUrl && isMp4"
          v-model="form.videoBackupUrl"
          class="fallback-input"
          placeholder="备用 mp4 地址（建议与主源用不同域名 / CDN）"
        />
        <el-input
          v-else-if="form.videoFallbackUrl"
          v-model="form.videoUrl"
          class="fallback-input"
          placeholder="兜底用的 mp4 地址（云存储或任意 CDN 均可）"
        />
        <div v-if="form.videoFallbackUrl" class="hint">
          <template v-if="isMp4">
            主视频地址打不开时自动切换，只切一次；备用地址也失败才会显示「视频加载失败」。
            <span class="hint-warn">
              备用地址<b>不能与主视频地址相同</b>：填成同一个地址时切换不会触发，保存会被拦下。
              建议用<b>不同域名或不同 CDN</b>，同一域名挂了两边都挂，等于没兜底。
              填完请在浏览器里打开一次确认能播放。
            </span>
          </template>
          <template v-else>
            仅在视频号 / 腾讯视频主源报错时切换，正常情况下不会用到。留空等于没开兜底。
          </template>
        </div>
        <!-- 这里刻意不把备用地址再回显一遍（2026-11-06 用户反馈「为什么还显示个备用地址」）。
             上面的「最终播放地址」必须单列，因为它是 videoUrlToSave() 按档位算出来的，
             输入框里看到的未必是写库值；而 form.videoBackupUrl 原样写库、没有任何计算，
             再摆一个「备用地址：」只是把输入框里的内容重复一遍，反而像存在第二份配置。 -->
      </el-form-item>

      <el-form-item label="封面 / 占位图">
        <UploadField v-model="form.cover" :multiple="false" accept="image/*" library-type="cover" />
        <div class="hint">视频加载前与「不展示视频」时显示这张图。</div>
      </el-form-item>

      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { reactive, ref, computed, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import {
  getCompanyProfile,
  saveCompanyProfile,
  getUploadCapability,
  getVideoCapability,
  type UploadCapability,
  type VideoCapability,
} from '@/api/content'
import UploadField from '@/components/UploadField.vue'

const loading = ref(false)
const saving = ref(false)
const form = reactive({
  id: 1,
  introText: '',
  videoSource: 'none',
  videoChannelId: '',
  videoFeedId: '',
  videoFeedToken: '',
  videoVid: '',
  videoUrl: '',
  videoFallbackUrl: false,
  // 自托管 MP4 档的备用地址。与主源、与「视频号/腾讯视频的兜底地址」都分开存：
  // 一个 videoUrl 装不下三条语义，共用就会在保存时互相覆盖。
  videoBackupUrl: '',
  videoAutoplay: false,
  cover: '',
})

// 「自托管 MP4」两种交付方式各自的值，**不能共用一个字段**。
//
// 踩过的坑：upload 与 url 曾经共用 form.videoUrl，于是从「填写 MP4 地址」
// 切到「上传到云存储」时，上传组件把那个外部链接当成「已选中的视频文件」
// 显示出来（`video-langhui....mp4` + 一个绿色对勾）——看起来像已经上传好了，
// 实际那个文件根本不在云存储里。反过来也成立：上传得到的地址会跑进直链输入框。
//
// 四个来源各占一个槽，切换交付方式时谁都不动，只按当前档位取用：
//   uploadUrl          云存储上传得到的地址
//   externalUrl        手填的外部直链
//   form.videoUrl      视频号 / 腾讯视频档的「兜底 MP4」（后端只有这一个字段装它）
//   form.videoBackupUrl 自托管 MP4 档的「备用 MP4」（主源失败时切换）
// 后两个不能合并：自托管档保存时 videoUrl 会被主源覆盖。
const uploadUrl = ref('')
const externalUrl = ref('')

// 存储能力：决定「云存储上传」档可选与否。探测失败按不可用处理（界面禁用并给出原因）。
const cap = ref<UploadCapability>({
  driver: 'unknown',
  presign: false,
  videoUpload: false,
  videoMaxMb: 100,
  reason: '',
})

// 视频来源能力：决定「腾讯视频」档可选与否（部署级开关，探测失败按不可用处理）。
const vcap = ref<VideoCapability>({ tencentVideo: false, tencentReason: '' })

// 四种视频档都可能播不出来：视频号（组件报错 / feed-token 失效）、
// 腾讯视频（插件未授权 / vid 未过审）、自托管 MP4（CDN 证书失效、对象被删、域名欠费）。
// 所以四档都给同样的兜底配置，只有 none（本来就不展示视频）不需要。
//
// 但「兜底地址」不是同一个槽：
//   channels / tencent → form.videoUrl（这两档下它没有别的用途）
//   upload / url       → form.videoBackupUrl（videoUrl 在主源保存时会被 videoUrlToSave()
//                        覆盖，复用就等于把用户填的备用地址悄悄丢掉）
const needFallback = computed(() => form.videoSource !== 'none')

// ---- 自托管 MP4：把 upload 与 url 合成一档 ----
//
// 为什么合：小程序端 company-video 里两者是**同一个分支**
// （`if ((source === 'upload' || source === 'url') && url) return 'mp4'`），
// 都渲染原生 <video src>，访客看到的一模一样。差别只在「文件怎么放上去」：
//   upload 我们帮客户传到已配置的对象存储（客户不必自己有存储）
//   url    客户填一个现成的 https mp4
// 并列成两个平级选项，客户只能对着两个效果相同的选项猜「到底选哪个」。
//
// 存库时仍分别写 upload / url：后端取值、历史数据、开放接口、小程序组件全都不动，
// 合并只发生在后台这一层。所以「videoSource === 'upload' || 'url'」这个判断是
// 存量的兼容点——历史配置回显也靠它。
const isMp4 = computed(() => form.videoSource === 'upload' || form.videoSource === 'url')

// 下拉的界面值：mp4 ←→ upload/url 的映射。写回时只改 videoSource，不新增字段。
const sourceUi = computed({
  get: () => (isMp4.value ? 'mp4' : form.videoSource),
  set: (v: string) => {
    if (v !== 'mp4') {
      form.videoSource = v
      return
    }
    // 从别的档切进来：能上传就默认上传（客户最省事），
    // 不能上传就直接落到直链——否则一进来就看到一个灰掉的单选项。
    form.videoSource = cap.value.videoUpload ? 'upload' : 'url'
  },
})

// 子选择。它就是 videoSource 本身，不另存一份状态：
// 两份状态一旦不同步，界面显示「上传」而库里存的是 url，保存后视频就没了。
const mp4Mode = computed({
  get: () => (form.videoSource === 'url' ? 'url' : 'upload'),
  set: (v: string) => {
    form.videoSource = v === 'url' ? 'url' : 'upload'
  },
})

// 当前会写进库、也最终交给小程序 <video src> 的那个地址。
// 保存与「最终播放地址」展示必须取同一个值，所以抽成一份：
// 两处各写一遍三元表达式，改一处漏一处就是「显示的和存的不是一回事」。
function videoUrlToSave(): string {
  return form.videoSource === 'upload'
    ? uploadUrl.value
    : form.videoSource === 'url'
      ? externalUrl.value
      : form.videoUrl
}

// 只在「自托管 MP4」档展示：其余档位下这一列装的是兜底地址，
// 它已经有自己的输入框，再摆一遍只会让人分不清哪个才是主视频源。
const activeVideoUrl = computed(() => (isMp4.value ? videoUrlToSave() : ''))
// 只有 http(s) 绝对地址与站内相对路径才允许变成可点链接。
// videoUrl 是后台自由文本，直接绑 href 的话 `javascript:` 之类会变成可点链接；
// 非预期格式只作纯文本展示（仍能看出填了什么，便于排查）。
const canOpenVideoUrl = computed(() => /^(https?:\/\/|\/)/i.test(activeVideoUrl.value))

// 兜底地址：四档共用 videoFallbackUrl 这一个开关，但地址槽按档位分开。
// 校验「开了兜底却没填地址」也必须按槽取值，否则 MP4 档会拿主源地址冒充备用地址，
// 拦不住真正没填的情况（主源永远非空）。
const fallbackUrlValue = computed(() => (isMp4.value ? form.videoBackupUrl : form.videoUrl))

async function load() {
  loading.value = true
  try {
    const d = await getCompanyProfile()
    Object.assign(
      form,
      {
        videoSource: 'none',
        videoChannelId: '',
        videoFeedId: '',
        videoFeedToken: '',
        videoVid: '',
        videoUrl: '',
        videoFallbackUrl: false,
        // 后加的列：历史行没有这一项，必须给默认值，否则切档时残留上一次的值
        videoBackupUrl: '',
        videoAutoplay: false,
      },
      d
    )
    // 该列是后加的，历史行可能为空串
    form.videoSource = d.videoSource || 'none'
    // 后端只有一个 videoUrl，分不清它当初是「上传得到的」还是「手填的」——
    // 按 videoSource 归位即可：upload / url 是主视频源，其余档位下它是兜底地址
    // （兜底输入框直接绑 form.videoUrl，见模板）。
    const vurl = d.videoUrl || ''
    uploadUrl.value = form.videoSource === 'upload' ? vurl : ''
    externalUrl.value = form.videoSource === 'url' ? vurl : ''
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

async function loadCapability() {
  try {
    cap.value = await getUploadCapability()
  } catch {
    // 探测失败按「不可用」处理。这里必须自己兜住：onMounted 里是裸调用，
    // 让 rejection 冒出去会变成浏览器的 Uncaught (in promise)，在前端监控里
    // 就是一条看不出所以然的错误；同时给出可读原因，别只留默认文案。
    cap.value = { ...cap.value, videoUpload: false, reason: '存储能力探测失败，暂时无法上传视频' }
  }
}

async function loadVideoCapability() {
  try {
    vcap.value = await getVideoCapability()
  } catch {
    // 同上：探测失败按「不可用」处理，并给出可读原因
    vcap.value = { tencentVideo: false, tencentReason: '视频来源能力探测失败，暂时无法选择腾讯视频' }
  }
}

async function save() {
  // 开了兜底却不填地址，等于没兜底——直接拦住，避免存一个永远不生效的开关。
  // 两档各查各的槽：查 form.videoUrl 在自托管 MP4 档会误判（主源永远非空，
  // 拦不住「备用地址没填」），查 videoBackupUrl 在视频号档又永远为空。
  if (needFallback.value && form.videoFallbackUrl && !fallbackUrlValue.value.trim()) {
    ElMessage.warning('已开启兜底，请填写兜底 MP4 地址')
    return
  }
  // 备用地址与主源填成同一个 URL，同样是「存了也永远不生效」的配置：
  // 小程序端切换条件是 `backup !== currentSrc`，地址相同就不切，主源一失败
  // 直接显示「视频加载失败」，兜底开关形同虚设（2026-11-06 截图反馈：两个输入框
  // 都是 http://qiniu.ziyuanxingqiu.cn/video-langhui.mp4）。
  // 只对自托管 MP4 档比较：视频号 / 腾讯视频档的主源是 feedId / vid，不是 URL，
  // 且其兜底地址在 form.videoUrl，拿它跟 videoBackupUrl 比没有意义。
  // 主源取 videoUrlToSave() 而不是某个输入框，保证与真正写库的值同源。
  if (
    needFallback.value &&
    form.videoFallbackUrl &&
    isMp4.value &&
    form.videoBackupUrl.trim() &&
    form.videoBackupUrl.trim() === videoUrlToSave().trim()
  ) {
    ElMessage.warning('备用地址不能与主视频地址相同，请换用另一个域名或 CDN 的地址')
    return
  }
  // feedId（同主体）与 feed-token（跨主体）二选一，feedId 优先；两个都空则播不了任何东西
  if (form.videoSource === 'channels' && !form.videoFeedId.trim() && !form.videoFeedToken.trim()) {
    ElMessage.warning('视频号档需要填写 feedId（同主体）或 feed-token（跨主体）')
    return
  }
  // 「自托管 MP4」两个子模式各自的空值拦截：选了档却没有一个能播的地址，
  // 存下去就是一张永远播不出视频的名片，而界面上看不出任何异常。
  // 注意各查各的槽：查 form.videoUrl 会误判——它是「兜底地址」，这两个档位下根本不参与。
  if (form.videoSource === 'upload' && !uploadUrl.value.trim()) {
    ElMessage.warning('请先上传视频文件，或改用「填写 MP4 地址」')
    return
  }
  if (form.videoSource === 'url' && !externalUrl.value.trim()) {
    ElMessage.warning('请填写 MP4 地址')
    return
  }
  // 腾讯视频档只有 vid 一个凭证，空着就是配了个永远播不出来的档
  if (form.videoSource === 'tencent' && !form.videoVid.trim()) {
    ElMessage.warning('腾讯视频档需要填写 vid')
    return
  }
  saving.value = true
  try {
    await saveCompanyProfile({
      introText: form.introText,
      videoSource: form.videoSource,
      videoChannelId: form.videoChannelId,
      videoFeedId: form.videoFeedId,
      videoFeedToken: form.videoFeedToken,
      videoVid: form.videoVid,
      // 取值规则见 videoUrlToSave()（与「最终播放地址」展示同源）
      videoUrl: videoUrlToSave(),
      videoFallbackUrl: form.videoFallbackUrl,
      // 自托管 MP4 档的备用地址。与 videoUrl 独立一列，服务端各存各的；
      // 始终随表单提交（和 videoVid / videoChannelId 一样，不按档位裁剪），
      // 切档再切回来时用户填过的值不会丢。
      videoBackupUrl: form.videoBackupUrl,
      videoAutoplay: form.videoAutoplay,
      cover: form.cover,
    })
    ElMessage.success('已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  load()
  loadCapability()
  loadVideoCapability()
})
</script>

<style scoped>
.video-url-check {
  max-width: 620px;
  line-height: 1.6;
  word-break: break-all;
}
.video-url-check a {
  color: #409eff;
  text-decoration: none;
}
.page-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.page-bar h3 {
  margin: 0;
}
.hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: #909399;
}
.hint-good {
  color: #67c23a;
}
.hint-warn {
  color: #e6a23c;
}
.cap-alert {
  margin-bottom: 12px;
}
.fallback-input {
  margin-top: 8px;
}
</style>
