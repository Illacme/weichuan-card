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
        <el-select v-model="form.videoSource" style="width: 280px">
          <el-option label="不展示视频（显示封面）" value="none" />
          <el-option label="微信视频号（推荐）" value="channels" />
          <el-option label="云存储上传" value="upload" :disabled="!cap.videoUpload" />
          <el-option label="外部 MP4 直链" value="url" />
          <el-option label="腾讯视频（插件）" value="tencent" :disabled="!vcap.tencentVideo" />
        </el-select>
        <div v-if="form.videoSource === 'channels'" class="hint hint-good">
          视频存微信服务器、播放走微信 CDN，<b>不产生存储与流量费用</b>，且官方声明该组件无视听资质要求。
          <span class="hint-warn">
            形态说明（微信组件固定行为，均无开关可改）：封面自带<b>视频号名称与推荐角标</b>，无法隐藏；
            跨主体（feed-token）点击播放会<b>拉起视频号页面</b>播放，只有同主体（feedId）才能在原位自动播放。
            要求「原位播放、画面干净」请用<b>外部 MP4 直链</b>（任意 https mp4 地址，与存储驱动无关）；
            云存储上传档仅当存储配置为对象存储时可用，本地磁盘存储下会被禁用。
          </span>
        </div>
        <div v-else-if="form.videoSource === 'tencent'" class="hint hint-good">
          视频由腾讯视频托管并走其 CDN，<b>不占您的存储与流量</b>；在页面内<b>原位播放</b>，
          封面<b>不带视频号名称与推荐角标</b>，且能记上播放统计（视频号档官方没有播放事件，记不了）。
          <span class="hint-warn">
            前置条件（缺一不可）：① 在「系统设置 → 功能开关」开启<b>腾讯视频档</b>；
            ② 小程序后台「设置 → 第三方服务 → 插件管理」已添加并授权腾讯视频插件；
            ③ 小程序 app.json 已声明该插件；少任何一项，这里要么选不了、要么保存被拒。
            ④ vid 必须是<b>您自己腾讯视频创作号</b>上传的视频（插件不支持播他人视频 / 任意 URL）；
            创作号分类别选「文化 / 历史」可能无法播放全片，建议选「企业 / 商业」类。
          </span>
        </div>
        <div v-else-if="form.videoSource === 'upload'" class="hint">
          文件上传到已配置的对象存储（OSS / COS / MinIO），<b>直传不经应用服务器</b>，费用记在您自己的云账号上。
        </div>
        <div v-else-if="form.videoSource === 'url'" class="hint">
          填写现成的 MP4 地址即可，我们不保存文件。<b>流量费由您的存储服务商收取，视听类目资质需自行具备。</b>
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
      </template>

      <!-- 云存储上传档：复用通用上传组件（内部优先走直传预签名，失败回退普通上传） -->
      <template v-else-if="form.videoSource === 'upload'">
        <el-alert v-if="!cap.videoUpload" type="warning" :closable="false" show-icon class="cap-alert">
          <template #title>{{ cap.reason || '当前存储配置不支持视频上传' }}</template>
        </el-alert>
        <el-form-item v-if="cap.videoUpload" label="视频文件">
          <UploadField
            v-model="form.videoUrl"
            :multiple="false"
            accept="video/mp4"
            list-type="text"
            hide-library
            :max-m-b="cap.videoMaxMb"
            :tip="`仅支持 mp4，单文件 ≤${cap.videoMaxMb}MB`"
          />
        </el-form-item>
      </template>

      <!-- 外部直链档 -->
      <template v-else-if="form.videoSource === 'url'">
        <el-form-item label="MP4 地址">
          <el-input v-model="form.videoUrl" placeholder="https:// 开头的 mp4 地址" />
        </el-form-item>
      </template>

      <!-- 兜底：只有「主源可能播不出来」的档位才有意义（视频号 / 腾讯视频） -->
      <el-form-item v-if="needFallback" label="兜底 MP4">
        <el-checkbox v-model="form.videoFallbackUrl">主源播放失败时改用 MP4</el-checkbox>
        <el-input
          v-if="form.videoFallbackUrl"
          v-model="form.videoUrl"
          class="fallback-input"
          placeholder="兜底用的 mp4 地址（云存储或任意 CDN 均可）"
        />
        <div v-if="form.videoFallbackUrl" class="hint">
          仅在视频号组件报错时切换，正常情况下不会用到。留空等于没开兜底。
        </div>
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
  cover: '',
})

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

// 兜底只对「主源可能失败」的档位开放：视频号（组件报错）、腾讯视频（插件未授权）。
// upload / url 两档本身就是 MP4，兜底没有意义。
const needFallback = computed(
  () => form.videoSource === 'channels' || form.videoSource === 'tencent'
)

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
      },
      d
    )
    // 该列是后加的，历史行可能为空串
    form.videoSource = d.videoSource || 'none'
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
  // 开了兜底却不填地址，等于没兜底——直接拦住，避免存一个永远不生效的开关
  if (needFallback.value && form.videoFallbackUrl && !form.videoUrl.trim()) {
    ElMessage.warning('已开启兜底，请填写兜底 MP4 地址')
    return
  }
  // feedId（同主体）与 feed-token（跨主体）二选一，feedId 优先；两个都空则播不了任何东西
  if (form.videoSource === 'channels' && !form.videoFeedId.trim() && !form.videoFeedToken.trim()) {
    ElMessage.warning('视频号档需要填写 feedId（同主体）或 feed-token（跨主体）')
    return
  }
  if (form.videoSource === 'url' && !form.videoUrl.trim()) {
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
      videoUrl: form.videoUrl,
      videoFallbackUrl: form.videoFallbackUrl,
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
