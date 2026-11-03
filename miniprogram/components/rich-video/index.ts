// 富文本视频块：按块的 type 属性选源渲染（与 company-video 的多档思路一致，
// 但这里档位写在内容里——每个块自带 type，不由后台配置统一决定）。
//
// 三种 type 的渲染分支：
//   channels（缺省，存量内容无 type）  → channel-video，feed-id > feed-token > channel-id
//   tencent                           → txv-video 插件（vid 取片，原位播放）
//   mp4                               → 标准 video（src 直链）
//
// 边界（与 company-video 同源，此处不重复兜底）：
//   - tencent 档依赖 app.json 声明插件 + MP 后台授权；未声明的部署上 txv-video
//     是未注册组件、渲染不出来——编辑器侧应提示客户「部署未开启腾讯视频档时勿用」；
//   - mp4 档的 src 域名在正式版需加入小程序「业务域名」白名单；
//   - channel-video 官方只开放 binderror，无播放事件——视频号块的播放统计缺失
//     是官方组件的边界（company-video 文件头有同款说明）。
//
// 播放埋点（video_play）覆盖 mp4 与 tencent 两档（video/txv-video 都有 bindplay）：
// video_play 在统计口径里属于「内容浏览」组，富文本块挂在案例/服务详情页，
// 归属 staffId 由页面透传。组件实例级去重：暂停恢复再播不重复计。
import { track } from '../../utils/track'

Component({
  options: { addGlobalClass: true },
  properties: {
    // 块类型：'channels'（缺省，存量内容无 type）| 'tencent' | 'mp4'
    videoType: String,
    // ---- channels 三件套（优先级 feedId > feedToken > channelId）----
    // 同主体：视频号视频 feedId（优先，可自动播放）
    feedId: String,
    // 跨主体：feed-token（feedId 为空时启用，强制不能自动播放）
    feedToken: String,
    // 视频号 ID（sph 开头）：前两者都缺失时的兼容回退，只展示视频号主页
    channelId: String,
    // ---- tencent：腾讯视频 vid（插件取片唯一凭证）----
    vid: String,
    // ---- mp4：直链地址 ----
    src: String,
    // 埋点归属：video_play 计到哪位员工的名片账上（详情页透传）
    staffId: Number,
  },
  data: {
    // 播放埋点去重标记：同一组件实例只报一次（bindplay 在「暂停后继续播放」时会再触发）
    playTracked: false,
  },
  observers: {
    // 换块（详情页复用组件实例切内容）时重置去重：换了视频应允许再计一次
    'videoType, vid, src, feedId, feedToken, channelId': function () {
      this.setData({ playTracked: false })
    },
  },
  methods: {
    // video_play 埋点（mp4 / tencent 档；channels 档无播放事件，见文件头）
    onVideoPlay() {
      if (this.data.playTracked) return
      this.setData({ playTracked: true })
      track('video_play', this.data.staffId || 0)
    },
    // txv-video / video 的报错打出来：体验版开调试即可见，别让「块不显示」无从排查
    onVideoError(e?: { detail?: Record<string, unknown> }) {
      console.warn('[rich-video] 视频块报错', (e && e.detail) || e || '')
    },
  },
})
